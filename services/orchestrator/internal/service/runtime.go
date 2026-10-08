package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	asyncv1 "github.com/caduc/ai-agent-orchestrator/contracts/async/v1"
	llmv1 "github.com/caduc/ai-agent-orchestrator/contracts/gen/go/llm/v1"
	"github.com/caduc/ai-agent-orchestrator/services/orchestrator/internal/config"
	"github.com/caduc/ai-agent-orchestrator/services/orchestrator/internal/domain"
	"github.com/caduc/ai-agent-orchestrator/services/orchestrator/internal/store"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type Runtime struct {
	store  *store.Store
	llm    llmv1.LLMGatewayServiceClient
	js     nats.JetStreamContext
	logger *slog.Logger
	cfg    config.Config
	wg     sync.WaitGroup
}

func NewRuntime(storage *store.Store, llm llmv1.LLMGatewayServiceClient, js nats.JetStreamContext, logger *slog.Logger, cfg config.Config) *Runtime {
	return &Runtime{store: storage, llm: llm, js: js, logger: logger, cfg: cfg}
}

func (r *Runtime) Start(ctx context.Context) error {
	if err := r.ensureStreams(); err != nil {
		return fmt.Errorf("ensure JetStream resources: %w", err)
	}
	resultSubscription, err := r.js.PullSubscribe(
		asyncv1.SubjectAgentResults,
		"orchestrator-results",
		nats.BindStream("ORCH_RESULTS"),
		nats.ManualAck(),
	)
	if err != nil {
		return fmt.Errorf("create result consumer: %w", err)
	}

	r.goSafe(ctx, "planning-loop", r.planningLoop)
	r.goSafe(ctx, "outbox-loop", r.outboxLoop)
	r.goSafe(ctx, "reconcile-loop", r.reconcileLoop)
	r.startResultWorkers(ctx, resultSubscription)
	return nil
}

func (r *Runtime) Wait() { r.wg.Wait() }

func (r *Runtime) goSafe(ctx context.Context, name string, function func(context.Context)) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() {
			if recovered := recover(); recovered != nil {
				r.logger.Error("runtime panic recovered", "component", name, "panic", recovered, "stack", string(debug.Stack()))
			}
		}()
		function(ctx)
	}()
}

// safely runs one unit of work and converts a panic into an error, so a
// failing unit cannot terminate the long-lived loop or worker that runs it.
func (r *Runtime) safely(ctx context.Context, unit string, function func(context.Context)) (failed bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			failed = true
			r.logger.ErrorContext(ctx, "unit of work panicked", "unit", unit, "panic", fmt.Sprint(recovered), "stack", string(debug.Stack()))
		}
	}()
	function(ctx)
	return false
}

func (r *Runtime) ensureStreams() error {
	streams := []*nats.StreamConfig{
		{Name: "ORCH_COMMANDS", Subjects: []string{"commands.agent.*.execute.v1"}, Retention: nats.WorkQueuePolicy, Storage: nats.FileStorage, MaxAge: 24 * time.Hour},
		{Name: "ORCH_RESULTS", Subjects: []string{"results.agent.*.completed.v1"}, Retention: nats.WorkQueuePolicy, Storage: nats.FileStorage, MaxAge: 24 * time.Hour},
	}
	for _, stream := range streams {
		if _, err := r.js.StreamInfo(stream.Name); err == nil {
			continue
		} else if !errors.Is(err, nats.ErrStreamNotFound) {
			return err
		}
		if _, err := r.js.AddStream(stream); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) planningLoop(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.SchedulerInterval)
	defer ticker.Stop()
	for {
		r.safely(ctx, "plan-task", r.processOnePlanningTask)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Runtime) processOnePlanningTask(ctx context.Context) {
	owner := r.cfg.InstanceID
	task, err := r.store.ClaimQueuedTask(ctx, owner, r.cfg.PlanningLease)
	if err != nil {
		if ctx.Err() == nil {
			r.logger.ErrorContext(ctx, "claim queued task failed", "error", err)
		}
		return
	}
	if task == nil {
		return
	}
	ctx, span := otel.Tracer("orchestrator.runtime").Start(ctx, "task.plan", trace.WithAttributes(attribute.String("task.id", task.ID)))
	defer span.End()
	if !time.Now().Before(task.DeadlineAt) {
		r.failPlanning(ctx, task.ID, "TASK_DEADLINE_EXCEEDED")
		return
	}
	planningCtx, cancel := context.WithDeadline(ctx, minTime(task.DeadlineAt, time.Now().Add(45*time.Second)))
	defer cancel()
	_, _ = r.llm.OpenTaskBudget(planningCtx, &llmv1.OpenTaskBudgetRequest{TaskId: task.ID, MaxTokens: 100_000})
	response, err := r.llm.Plan(planningCtx, &llmv1.PlanRequest{
		TaskId: task.ID, Description: task.Description,
		AvailableCapabilities: []string{"code", "logs", "database", "infrastructure"},
		MaxSubtasks:           int32(r.cfg.MaxSubtasks),
	})
	if err != nil {
		r.logger.ErrorContext(ctx, "task planning failed", "task_id", task.ID, "error", err)
		r.failPlanning(ctx, task.ID, "LLM_UNAVAILABLE")
		return
	}
	plan := make([]domain.PlannedSubtask, 0, len(response.GetSubtasks()))
	for _, item := range response.GetSubtasks() {
		id := item.GetId()
		if id == "" {
			id = newID()
		}
		timeout := item.GetTimeoutSeconds()
		if timeout <= 0 {
			timeout = int32(r.cfg.SubtaskTimeout / time.Second)
		}
		attempts := item.GetMaxAttempts()
		if attempts <= 0 {
			attempts = 3
		}
		plan = append(plan, domain.PlannedSubtask{
			ID: id, Description: item.GetDescription(), Capability: item.GetCapability(),
			DependsOn: item.GetDependsOn(), TimeoutSeconds: timeout, MaxAttempts: attempts,
		})
	}
	capabilities := map[string]bool{"code": true, "logs": true, "database": true, "infrastructure": true}
	if err := domain.ValidatePlan(plan, r.cfg.MaxSubtasks, capabilities); err != nil {
		r.logger.ErrorContext(ctx, "LLM returned invalid plan", "task_id", task.ID, "error", err)
		r.failPlanning(ctx, task.ID, "PLAN_INVALID")
		return
	}
	if err := r.store.SavePlan(ctx, task.ID, owner, plan); err != nil {
		if errors.Is(err, store.ErrConflict) {
			// Cancellation, deadline expiry or a planner that reclaimed an
			// expired lease already decided this Task; this plan is stale.
			r.logger.WarnContext(ctx, "discarding stale execution plan", "task_id", task.ID)
			return
		}
		r.logger.ErrorContext(ctx, "persist execution plan failed", "task_id", task.ID, "error", err)
		r.failPlanning(ctx, task.ID, "PLAN_PERSISTENCE_FAILED")
		return
	}
	span.SetStatus(codes.Ok, "plan persisted")
	r.logger.InfoContext(ctx, "task plan dispatched", "task_id", task.ID, "subtasks", len(plan))
}

func (r *Runtime) failPlanning(ctx context.Context, taskID, code string) {
	if err := r.store.FailPlanning(ctx, taskID, r.cfg.InstanceID, code); err != nil {
		r.logger.ErrorContext(ctx, "fail planning task failed", "task_id", taskID, "error_code", code, "error", err)
	}
}

// reconcileLoop recovers work that no event will advance: Tasks past their
// total deadline, Attempts whose result never arrived and aggregations whose
// owner stopped. Every step is idempotent and safe across replicas.
func (r *Runtime) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.ReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		r.safely(ctx, "reconcile", r.reconcileOnce)
	}
}

// reconcileBatch bounds the work of one reconciliation tick.
const reconcileBatch = 50

func (r *Runtime) reconcileOnce(ctx context.Context) {
	r.drain(ctx, "expire task deadline", func(ctx context.Context) (bool, string, error) {
		return r.store.ExpireTaskDeadline(ctx)
	})
	r.drain(ctx, "expire overdue attempt", func(ctx context.Context) (bool, string, error) {
		return r.store.ExpireOverdueAttempt(ctx, r.cfg.AttemptResultGrace)
	})
	for range reconcileBatch {
		taskID, err := r.store.ClaimStaleAggregation(ctx, r.cfg.InstanceID, r.cfg.AggregationLease)
		if err != nil {
			if ctx.Err() == nil {
				r.logger.ErrorContext(ctx, "claim stale aggregation failed", "error", err)
			}
			return
		}
		if taskID == "" {
			return
		}
		r.logger.WarnContext(ctx, "resuming interrupted aggregation", "task_id", taskID)
		r.runAggregation(ctx, taskID)
	}
}

// drain repeats one recovery step until it finds nothing or the batch ends,
// aggregating every Task whose DAG the step closed.
func (r *Runtime) drain(ctx context.Context, name string, step func(context.Context) (bool, string, error)) {
	for range reconcileBatch {
		found, aggregateTaskID, err := step(ctx)
		if err != nil {
			if ctx.Err() == nil {
				r.logger.ErrorContext(ctx, name+" failed", "error", err)
			}
			return
		}
		if !found {
			return
		}
		if aggregateTaskID != "" {
			r.aggregateTask(ctx, aggregateTaskID)
		}
	}
}

func (r *Runtime) outboxLoop(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.OutboxInterval)
	defer ticker.Stop()
	for {
		messages, err := r.store.PendingOutbox(ctx, 50)
		if err != nil {
			if ctx.Err() == nil {
				r.logger.ErrorContext(ctx, "load outbox failed", "error", err)
			}
		} else {
			for _, pending := range messages {
				message := nats.NewMsg(pending.Subject)
				message.Data = pending.Payload
				message.Header.Set("Nats-Msg-Id", pending.ID)
				if _, err := r.js.PublishMsg(message, nats.Context(ctx)); err != nil {
					_ = r.store.MarkOutboxFailed(ctx, pending.ID, err)
					continue
				}
				if err := r.store.MarkOutboxPublished(ctx, pending.ID); err != nil {
					r.logger.ErrorContext(ctx, "mark outbox published failed", "message_id", pending.ID, "error", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Runtime) startResultWorkers(ctx context.Context, subscription *nats.Subscription) {
	jobs := make(chan *nats.Msg, r.cfg.ResultConcurrency*2)
	r.goSafe(ctx, "result-fetcher", func(ctx context.Context) {
		defer close(jobs)
		for {
			messages, err := subscription.Fetch(1, nats.MaxWait(time.Second))
			if err != nil {
				if errors.Is(err, nats.ErrTimeout) {
					if ctx.Err() != nil {
						return
					}
					continue
				}
				if ctx.Err() == nil {
					r.logger.ErrorContext(ctx, "fetch agent result failed", "error", err)
				}
				continue
			}
			for _, message := range messages {
				select {
				case jobs <- message:
				case <-ctx.Done():
					return
				}
			}
		}
	})
	for worker := 0; worker < r.cfg.ResultConcurrency; worker++ {
		workerID := worker
		r.goSafe(ctx, fmt.Sprintf("result-worker-%d", workerID), func(ctx context.Context) {
			for {
				select {
				case <-ctx.Done():
					return
				case message, ok := <-jobs:
					if !ok {
						return
					}
					if r.safely(ctx, "apply-result", func(ctx context.Context) { r.handleResult(ctx, message) }) {
						_ = message.NakWithDelay(time.Second)
					}
				}
			}
		})
	}
}

func (r *Runtime) handleResult(ctx context.Context, message *nats.Msg) {
	var result asyncv1.AgentResult
	if err := json.Unmarshal(message.Data, &result); err != nil || result.SchemaVersion != asyncv1.SchemaVersion {
		r.logger.WarnContext(ctx, "invalid agent result", "subject", message.Subject, "error", err)
		_ = message.Term()
		return
	}
	carrier := propagation.MapCarrier{}
	if result.Traceparent != "" {
		carrier.Set("traceparent", result.Traceparent)
	}
	parentCtx := otel.GetTextMapPropagator().Extract(ctx, carrier)
	ctx, span := otel.Tracer("orchestrator.runtime").Start(parentCtx, "agent.result.consume", trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(
		attribute.String("task.id", result.TaskID),
		attribute.String("subtask.id", result.SubtaskID),
		attribute.String("attempt.id", result.AttemptID),
		attribute.String("agent.type", result.AgentType),
	))
	defer span.End()
	messageID := result.MessageID
	if messageID == "" {
		messageID = message.Header.Get("Nats-Msg-Id")
	}
	aggregate, err := r.store.ApplyAgentResult(ctx, messageID, message.Data, result)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		r.logger.ErrorContext(ctx, "apply agent result failed", "task_id", result.TaskID, "subtask_id", result.SubtaskID, "error", err)
		_ = message.NakWithDelay(time.Second)
		return
	}
	if err := message.Ack(); err != nil {
		r.logger.WarnContext(ctx, "ack agent result failed", "message_id", messageID, "error", err)
	}
	span.SetStatus(codes.Ok, "result applied")
	if aggregate {
		r.aggregateTask(ctx, result.TaskID)
	}
}

// aggregateTask aggregates a Task whose DAG closed, unless another replica
// already holds its aggregation lease.
func (r *Runtime) aggregateTask(ctx context.Context, taskID string) {
	claimed, err := r.store.ClaimAggregation(ctx, taskID, r.cfg.InstanceID, r.cfg.AggregationLease)
	if err != nil {
		r.logger.ErrorContext(ctx, "claim aggregation failed", "task_id", taskID, "error", err)
		return
	}
	if !claimed {
		return
	}
	r.runAggregation(ctx, taskID)
}

// runAggregation produces and persists the Final_Result of a leased Task.
func (r *Runtime) runAggregation(ctx context.Context, taskID string) {
	ctx, span := otel.Tracer("orchestrator.runtime").Start(ctx, "task.aggregate", trace.WithAttributes(attribute.String("task.id", taskID)))
	defer span.End()
	task, err := r.store.GetTask(ctx, taskID)
	if err != nil {
		r.logger.ErrorContext(ctx, "load task for aggregation failed", "task_id", taskID, "error", err)
		return
	}
	request := &llmv1.AggregateRequest{TaskId: task.ID, TaskDescription: task.Description}
	for _, subtask := range task.Subtasks {
		item := &llmv1.NormalizedResult{SubtaskId: subtask.ID, Capability: subtask.Capability, Success: subtask.Status == domain.SubtaskSucceeded, ErrorCode: subtask.ErrorCode}
		if subtask.Result != nil {
			item.Summary = subtask.Result.Summary
			item.Warnings = subtask.Result.Warnings
			for _, evidence := range subtask.Result.Evidence {
				item.Evidence = append(item.Evidence, &llmv1.Evidence{Source: evidence.Source, Reference: evidence.Reference, Content: evidence.Content})
			}
		}
		request.Results = append(request.Results, item)
	}
	aggregationCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	response, callErr := r.llm.Aggregate(aggregationCtx, request)
	cancel()
	final := domain.DeterministicFallback(task.Description, task.Subtasks)
	if callErr == nil {
		final = domain.FinalResult{Summary: response.GetSummary(), Conclusions: response.GetConclusions(), Failures: response.GetFailures(), Limitations: response.GetLimitations()}
		for _, evidence := range response.GetEvidence() {
			final.Evidence = append(final.Evidence, domain.Evidence{Source: evidence.GetSource(), Reference: evidence.GetReference(), Content: evidence.GetContent()})
		}
	} else {
		final.Limitations = append(final.Limitations, "LLM aggregation unavailable; deterministic fallback used")
	}
	if err := r.store.CompleteTask(ctx, taskID, final); err != nil {
		r.logger.ErrorContext(ctx, "complete task failed", "task_id", taskID, "error", err)
		return
	}
	span.SetStatus(codes.Ok, "task completed")
	r.logger.InfoContext(ctx, "task completed", "task_id", taskID, "llm_fallback", callErr != nil)
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}
