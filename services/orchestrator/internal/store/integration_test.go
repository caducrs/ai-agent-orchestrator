package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	asyncv1 "github.com/caduc/ai-agent-orchestrator/contracts/async/v1"
	"github.com/caduc/ai-agent-orchestrator/services/orchestrator/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var integration struct {
	once      sync.Once
	container *postgres.PostgresContainer
	adminURL  string
	err       error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if integration.container != nil {
		_ = integration.container.Terminate(context.Background())
	}
	os.Exit(code)
}

// newIntegrationStore returns a migrated Store backed by a fresh database in a
// PostgreSQL container shared by the package, so tests never see each other's
// queued Tasks.
func newIntegrationStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	if os.Getenv("RUN_INTEGRATION_TESTS") != "1" {
		t.Skip("integration tests are disabled")
	}
	integration.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		integration.container, integration.err = postgres.Run(ctx, "postgres:17.4-alpine",
			postgres.WithDatabase("orchestrator_test"),
			postgres.WithUsername("orchestrator"),
			postgres.WithPassword("orchestrator"),
			testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second)),
		)
		if integration.err == nil {
			integration.adminURL, integration.err = integration.container.ConnectionString(ctx, "sslmode=disable")
		}
	})
	if integration.err != nil {
		t.Fatalf("start PostgreSQL container: %v", integration.err)
	}
	ctx := context.Background()
	database := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	admin, err := pgx.Connect(ctx, integration.adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+database); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, strings.Replace(integration.adminURL, "/orchestrator_test?", "/"+database+"?", 1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	storage := New(pool, 30*time.Second)
	if err := storage.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return storage, pool
}

// startTask creates a Task, claims it for owner and persists plan.
func startTask(t *testing.T, storage *Store, owner string, deadline time.Duration, plan []domain.PlannedSubtask) domain.Task {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	task := domain.Task{ID: uuid.NewString(), OwnerSubject: "integration-user", Description: "Investigate HTTP 500", Status: domain.TaskQueued, CreatedAt: now, UpdatedAt: now, DeadlineAt: now.Add(deadline)}
	if _, _, err := storage.CreateTask(ctx, task, "", ""); err != nil {
		t.Fatal(err)
	}
	claimed, err := storage.ClaimQueuedTask(ctx, owner, time.Minute)
	if err != nil || claimed == nil || claimed.ID != task.ID {
		t.Fatalf("claim task: task=%#v error=%v", claimed, err)
	}
	if err := storage.SavePlan(ctx, task.ID, owner, plan); err != nil {
		t.Fatal(err)
	}
	return task
}

func subtaskByID(t *testing.T, storage *Store, taskID, subtaskID string) domain.Subtask {
	t.Helper()
	task, err := storage.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, subtask := range task.Subtasks {
		if subtask.ID == subtaskID {
			return subtask
		}
	}
	t.Fatalf("subtask %s not found in %#v", subtaskID, task.Subtasks)
	return domain.Subtask{}
}

func applyResult(t *testing.T, storage *Store, taskID string, subtask domain.Subtask, success bool, code string) bool {
	t.Helper()
	result := asyncv1.AgentResult{SchemaVersion: asyncv1.SchemaVersion, MessageID: uuid.NewString(), TaskID: taskID, SubtaskID: subtask.ID, AttemptID: subtask.CurrentAttemptID, AgentType: subtask.Capability, Success: success, Summary: "result", ErrorCode: code, CompletedAt: time.Now().UTC()}
	if success {
		result.Evidence = []asyncv1.Evidence{{Source: "code", Reference: "main.go:1", Content: "panic"}}
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := storage.ApplyAgentResult(context.Background(), result.MessageID, payload, result)
	if err != nil {
		t.Fatal(err)
	}
	return aggregate
}

func eventTypes(t *testing.T, storage *Store, taskID string) []string {
	t.Helper()
	events, err := storage.EventsAfter(context.Background(), taskID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	types := make([]string, 0, len(events))
	for index, event := range events {
		if event.EventID != int64(index+1) {
			t.Fatalf("event ids are not monotonic: %#v", events)
		}
		types = append(types, event.Type)
	}
	return types
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestCreateTaskIdempotencyIntegration(t *testing.T) {
	storage, _ := newIntegrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	firstTask := domain.Task{ID: "01999999-0000-7000-8000-000000000001", OwnerSubject: "integration-user", Description: "Investigate HTTP 500", Status: domain.TaskQueued, CreatedAt: now, UpdatedAt: now, DeadlineAt: now.Add(time.Minute)}
	created, replayed, err := storage.CreateTask(ctx, firstTask, "same-key", "same-fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	if replayed || created.ID != firstTask.ID {
		t.Fatalf("unexpected first creation: %#v replayed=%v", created, replayed)
	}
	secondTask := firstTask
	secondTask.ID = "01999999-0000-7000-8000-000000000002"
	replayedTask, replayed, err := storage.CreateTask(ctx, secondTask, "same-key", "same-fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	if !replayed || replayedTask.ID != firstTask.ID {
		t.Fatalf("idempotency replay failed: %#v replayed=%v", replayedTask, replayed)
	}
	events, err := storage.EventsAfter(ctx, firstTask.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventID != 1 || events[0].Type != "task.queued" {
		t.Fatalf("unexpected events: %#v", events)
	}
	claimed, err := storage.ClaimQueuedTask(ctx, "planner-a", time.Minute)
	if err != nil || claimed == nil || claimed.ID != firstTask.ID {
		t.Fatalf("claim queued task failed: task=%#v error=%v", claimed, err)
	}
	plan := []domain.PlannedSubtask{{ID: "01999999-0000-7000-8000-000000000010", Description: "inspect source", Capability: "code", TimeoutSeconds: 30, MaxAttempts: 3}}
	if err := storage.SavePlan(ctx, firstTask.ID, "planner-a", plan); err != nil {
		t.Fatal(err)
	}
	running := subtaskByID(t, storage, firstTask.ID, plan[0].ID)
	firstAttemptID := running.CurrentAttemptID
	if aggregate := applyResult(t, storage, firstTask.ID, running, false, "SOURCE_UNAVAILABLE"); aggregate {
		t.Fatal("transient failure must not close the DAG")
	}
	retried := subtaskByID(t, storage, firstTask.ID, plan[0].ID)
	if retried.Attempt != 2 || retried.Status != domain.SubtaskRunning || retried.CurrentAttemptID == firstAttemptID {
		t.Fatalf("retry was not scheduled: %#v", retried)
	}
	if !contains(eventTypes(t, storage, firstTask.ID), "subtask.retry_scheduled") {
		t.Fatal("retry event missing")
	}
}

func TestMigrateIsVersionedAndConcurrentSafeIntegration(t *testing.T) {
	storage, _ := newIntegrationStore(t)
	ctx := context.Background()
	var group sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		group.Add(1)
		go func() {
			defer group.Done()
			errs <- storage.Migrate(ctx)
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migration failed: %v", err)
		}
	}
	applied, err := storage.AppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]int, 0, len(migrations))
	for _, item := range migrations {
		want = append(want, item.version)
	}
	if !reflect.DeepEqual(applied, want) {
		t.Fatalf("applied migrations = %v, want %v", applied, want)
	}
}

func TestOverdueAttemptRetriesThenFailsIntegration(t *testing.T) {
	storage, pool := newIntegrationStore(t)
	ctx := context.Background()
	plan := []domain.PlannedSubtask{{ID: uuid.NewString(), Description: "inspect source", Capability: "code", TimeoutSeconds: 30, MaxAttempts: 2}}
	task := startTask(t, storage, "planner-a", time.Hour, plan)
	expire := func() (bool, string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE attempts SET deadline_at=now()-interval '1 minute' WHERE status='DISPATCHED'`); err != nil {
			t.Fatal(err)
		}
		found, aggregateTaskID, err := storage.ExpireOverdueAttempt(ctx, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		return found, aggregateTaskID
	}

	firstAttempt := subtaskByID(t, storage, task.ID, plan[0].ID)
	if found, aggregateTaskID := expire(); !found || aggregateTaskID != "" {
		t.Fatalf("first timeout: found=%v aggregate=%q", found, aggregateTaskID)
	}
	retried := subtaskByID(t, storage, task.ID, plan[0].ID)
	if retried.Status != domain.SubtaskRunning || retried.Attempt != 2 || retried.CurrentAttemptID == firstAttempt.CurrentAttemptID {
		t.Fatalf("timeout did not schedule a retry: %#v", retried)
	}
	if aggregate := applyResult(t, storage, task.ID, firstAttempt, true, ""); aggregate {
		t.Fatal("a Late_Result of a superseded Attempt must not close the DAG")
	}

	if found, aggregateTaskID := expire(); !found || aggregateTaskID != task.ID {
		t.Fatalf("second timeout: found=%v aggregate=%q", found, aggregateTaskID)
	}
	failed := subtaskByID(t, storage, task.ID, plan[0].ID)
	if failed.Status != domain.SubtaskFailed || failed.ErrorCode != "TIMEOUT" {
		t.Fatalf("exhausted subtask = %#v", failed)
	}
	if found, _ := expire(); found {
		t.Fatal("no attempt should remain overdue")
	}
	types := eventTypes(t, storage, task.ID)
	for _, wanted := range []string{"attempt.timed_out", "subtask.retry_scheduled", "subtask.completed", "task.aggregating"} {
		if !contains(types, wanted) {
			t.Fatalf("event %s missing from %v", wanted, types)
		}
	}
	if err := storage.CompleteTask(ctx, task.ID, domain.DeterministicFallback(task.Description, nil)); err != nil {
		t.Fatal(err)
	}
	completed, err := storage.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != domain.TaskFailed || completed.ErrorCode != "NO_USEFUL_RESULT" {
		t.Fatalf("task without useful result = %s/%s", completed.Status, completed.ErrorCode)
	}
}

func TestTaskDeadlineClosesDAGWithPartialResultIntegration(t *testing.T) {
	storage, pool := newIntegrationStore(t)
	ctx := context.Background()
	first, second, third := uuid.NewString(), uuid.NewString(), uuid.NewString()
	plan := []domain.PlannedSubtask{
		{ID: first, Description: "inspect logs", Capability: "logs", TimeoutSeconds: 30, MaxAttempts: 3},
		{ID: second, Description: "inspect source", Capability: "code", DependsOn: []string{first}, TimeoutSeconds: 30, MaxAttempts: 3},
		{ID: third, Description: "inspect database", Capability: "database", DependsOn: []string{second}, TimeoutSeconds: 30, MaxAttempts: 3},
	}
	task := startTask(t, storage, "planner-a", time.Hour, plan)
	if aggregate := applyResult(t, storage, task.ID, subtaskByID(t, storage, task.ID, first), true, ""); aggregate {
		t.Fatal("DAG must stay open while successors run")
	}
	if found, _, err := storage.ExpireTaskDeadline(ctx); err != nil || found {
		t.Fatalf("task within deadline was expired: found=%v error=%v", found, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tasks SET deadline_at=now()-interval '1 second' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	found, aggregateTaskID, err := storage.ExpireTaskDeadline(ctx)
	if err != nil || !found || aggregateTaskID != task.ID {
		t.Fatalf("expire deadline: found=%v aggregate=%q error=%v", found, aggregateTaskID, err)
	}
	if got := subtaskByID(t, storage, task.ID, second); got.Status != domain.SubtaskFailed || got.ErrorCode != "TIMEOUT" {
		t.Fatalf("running subtask after deadline = %#v", got)
	}
	if got := subtaskByID(t, storage, task.ID, third); got.Status != domain.SubtaskSkipped || got.ErrorCode != "TASK_DEADLINE_EXCEEDED" {
		t.Fatalf("blocked subtask after deadline = %#v", got)
	}
	if !contains(eventTypes(t, storage, task.ID), "task.deadline_exceeded") {
		t.Fatal("deadline event missing")
	}
	if err := storage.CompleteTask(ctx, task.ID, domain.FinalResult{Summary: "partial"}); err != nil {
		t.Fatal(err)
	}
	completed, err := storage.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != domain.TaskPartiallyCompleted {
		t.Fatalf("task with useful result after deadline = %s", completed.Status)
	}
}

func TestPlanningLeaseRecoveryIntegration(t *testing.T) {
	storage, pool := newIntegrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	task := domain.Task{ID: uuid.NewString(), OwnerSubject: "integration-user", Description: "Investigate HTTP 500", Status: domain.TaskQueued, CreatedAt: now, UpdatedAt: now, DeadlineAt: now.Add(time.Hour)}
	if _, _, err := storage.CreateTask(ctx, task, "", ""); err != nil {
		t.Fatal(err)
	}
	if claimed, err := storage.ClaimQueuedTask(ctx, "planner-a", time.Minute); err != nil || claimed == nil {
		t.Fatalf("first claim: %#v %v", claimed, err)
	}
	if claimed, err := storage.ClaimQueuedTask(ctx, "planner-b", time.Minute); err != nil || claimed != nil {
		t.Fatalf("a live planning lease was stolen: %#v %v", claimed, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tasks SET lease_until=now()-interval '1 second' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := storage.ClaimQueuedTask(ctx, "planner-b", time.Minute)
	if err != nil || reclaimed == nil || reclaimed.ID != task.ID {
		t.Fatalf("expired planning lease was not reclaimed: %#v %v", reclaimed, err)
	}
	plan := []domain.PlannedSubtask{{ID: uuid.NewString(), Description: "inspect source", Capability: "code", TimeoutSeconds: 30, MaxAttempts: 3}}
	if err := storage.SavePlan(ctx, task.ID, "planner-a", plan); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale planner saved a plan: %v", err)
	}
	if err := storage.FailPlanning(ctx, task.ID, "planner-a", "LLM_UNAVAILABLE"); err != nil {
		t.Fatal(err)
	}
	if err := storage.SavePlan(ctx, task.ID, "planner-b", plan); err != nil {
		t.Fatalf("lease owner could not save plan: %v", err)
	}
	running, err := storage.GetTask(ctx, task.ID)
	if err != nil || running.Status != domain.TaskRunning {
		t.Fatalf("task after recovery = %#v %v", running, err)
	}
	types := eventTypes(t, storage, task.ID)
	if !contains(types, "task.planning_resumed") || contains(types, "task.failed") {
		t.Fatalf("unexpected planning events %v", types)
	}
}

func TestStaleAggregationIsReclaimedIntegration(t *testing.T) {
	storage, pool := newIntegrationStore(t)
	ctx := context.Background()
	plan := []domain.PlannedSubtask{{ID: uuid.NewString(), Description: "inspect source", Capability: "code", TimeoutSeconds: 30, MaxAttempts: 3}}
	task := startTask(t, storage, "planner-a", time.Hour, plan)
	if aggregate := applyResult(t, storage, task.ID, subtaskByID(t, storage, task.ID, plan[0].ID), true, ""); !aggregate {
		t.Fatal("single successful subtask must close the DAG")
	}
	if taskID, err := storage.ClaimStaleAggregation(ctx, "aggregator-b", time.Minute); err != nil || taskID != "" {
		t.Fatalf("fresh aggregation was considered stale: %q %v", taskID, err)
	}
	if claimed, err := storage.ClaimAggregation(ctx, task.ID, "aggregator-a", time.Minute); err != nil || !claimed {
		t.Fatalf("claim aggregation: %v %v", claimed, err)
	}
	if claimed, err := storage.ClaimAggregation(ctx, task.ID, "aggregator-b", time.Minute); err != nil || claimed {
		t.Fatalf("a live aggregation lease was stolen: %v %v", claimed, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tasks SET lease_until=now()-interval '1 second' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	taskID, err := storage.ClaimStaleAggregation(ctx, "aggregator-b", time.Minute)
	if err != nil || taskID != task.ID {
		t.Fatalf("stale aggregation was not reclaimed: %q %v", taskID, err)
	}
	if err := storage.CompleteTask(ctx, task.ID, domain.FinalResult{Summary: "done"}); err != nil {
		t.Fatal(err)
	}
	if err := storage.CompleteTask(ctx, task.ID, domain.FinalResult{Summary: "duplicate"}); err != nil {
		t.Fatalf("repeated completion must be idempotent: %v", err)
	}
	completed, err := storage.GetTask(ctx, task.ID)
	if err != nil || completed.Status != domain.TaskCompleted || completed.FinalResult.Summary != "done" {
		t.Fatalf("completed task = %#v %v", completed, err)
	}
	terminalEvents := 0
	types := eventTypes(t, storage, task.ID)
	for _, eventType := range types {
		if eventType == "task.completed" {
			terminalEvents++
		}
	}
	if terminalEvents != 1 {
		t.Fatalf("terminal event count = %d in %v", terminalEvents, types)
	}
}
