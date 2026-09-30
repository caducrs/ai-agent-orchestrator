package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	asyncv1 "github.com/caduc/ai-agent-orchestrator/contracts/async/v1"
	llmv1 "github.com/caduc/ai-agent-orchestrator/contracts/gen/go/llm/v1"
	"github.com/caduc/ai-agent-orchestrator/services/orchestrator/internal/config"
	"github.com/caduc/ai-agent-orchestrator/services/orchestrator/internal/domain"
	"github.com/caduc/ai-agent-orchestrator/services/orchestrator/internal/store"
	"github.com/nats-io/nats.go"
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
				r.logger.Error("runtime panic recovered", "component", name, "panic", recovered)
			}
		}()
		function(ctx)
	}()
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
		r.processOnePlanningTask(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Runtime) processOnePlanningTask(ctx context.Context) {
	task, err := r.store.ClaimQueuedTask(ctx)
	if err != nil {
		if ctx.Err() == nil {
			r.logger.ErrorContext(ctx, "claim queued task failed", "error", err)
		}
		return
	}
	if task == nil {
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
		_ = r.store.FailPlanning(ctx, task.ID, "LLM_UNAVAILABLE")
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
		_ = r.store.FailPlanning(ctx, task.ID, "PLAN_INVALID")
		return
	}
	if err := r.store.SavePlan(ctx, task.ID, plan); err != nil {
		r.logger.ErrorContext(ctx, "persist execution plan failed", "task_id", task.ID, "error", err)
		_ = r.store.FailPlanning(ctx, task.ID, "PLAN_PERSISTENCE_FAILED")
		return
	}
	r.logger.InfoContext(ctx, "task plan dispatched", "task_id", task.ID, "subtasks", len(plan))
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
					r.handleResult(ctx, message)
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
	messageID := result.MessageID
	if messageID == "" {
		messageID = message.Header.Get("Nats-Msg-Id")
	}
	aggregate, err := r.store.ApplyAgentResult(ctx, messageID, message.Data, result)
	if err != nil {
		r.logger.ErrorContext(ctx, "apply agent result failed", "task_id", result.TaskID, "subtask_id", result.SubtaskID, "error", err)
		_ = message.NakWithDelay(time.Second)
		return
	}
	if err := message.Ack(); err != nil {
		r.logger.WarnContext(ctx, "ack agent result failed", "message_id", messageID, "error", err)
	}
	if aggregate {
		r.aggregateTask(ctx, result.TaskID)
	}
}

func (r *Runtime) aggregateTask(ctx context.Context, taskID string) {
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
	r.logger.InfoContext(ctx, "task completed", "task_id", taskID, "llm_fallback", callErr != nil)
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}
