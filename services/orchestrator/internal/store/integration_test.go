package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	asyncv1 "github.com/caduc/ai-agent-orchestrator/contracts/async/v1"
	"github.com/caduc/ai-agent-orchestrator/services/orchestrator/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestCreateTaskIdempotencyIntegration(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION_TESTS") != "1" {
		t.Skip("integration tests are disabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := postgres.Run(ctx, "postgres:17.4-alpine",
		postgres.WithDatabase("orchestrator_test"),
		postgres.WithUsername("orchestrator"),
		postgres.WithPassword("orchestrator"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Errorf("terminate PostgreSQL container: %v", err)
		}
	}()
	connectionString, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, connectionString)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	storage := New(pool, 30*time.Second)
	if err := storage.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
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
	claimed, err := storage.ClaimQueuedTask(ctx)
	if err != nil || claimed == nil || claimed.ID != firstTask.ID {
		t.Fatalf("claim queued task failed: task=%#v error=%v", claimed, err)
	}
	plan := []domain.PlannedSubtask{{ID: "01999999-0000-7000-8000-000000000010", Description: "inspect source", Capability: "code", TimeoutSeconds: 30, MaxAttempts: 3}}
	if err := storage.SavePlan(ctx, firstTask.ID, plan); err != nil {
		t.Fatal(err)
	}
	running, err := storage.GetTask(ctx, firstTask.ID)
	if err != nil || len(running.Subtasks) != 1 {
		t.Fatalf("load running task failed: %#v error=%v", running, err)
	}
	firstAttemptID := running.Subtasks[0].CurrentAttemptID
	failedResult := asyncv1.AgentResult{SchemaVersion: asyncv1.SchemaVersion, MessageID: "result-message-1", TaskID: firstTask.ID, SubtaskID: plan[0].ID, AttemptID: firstAttemptID, AgentType: "code", Success: false, Summary: "source unavailable", ErrorCode: "SOURCE_UNAVAILABLE", CompletedAt: time.Now().UTC()}
	payload, err := json.Marshal(failedResult)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := storage.ApplyAgentResult(ctx, failedResult.MessageID, payload, failedResult)
	if err != nil || aggregate {
		t.Fatalf("apply transient result failed: aggregate=%v error=%v", aggregate, err)
	}
	retried, err := storage.GetTask(ctx, firstTask.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Subtasks[0].Attempt != 2 || retried.Subtasks[0].Status != domain.SubtaskRunning || retried.Subtasks[0].CurrentAttemptID == firstAttemptID {
		t.Fatalf("retry was not scheduled: %#v", retried.Subtasks[0])
	}
	events, err = storage.EventsAfter(ctx, firstTask.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	foundRetry := false
	for _, event := range events {
		if event.Type == "subtask.retry_scheduled" {
			foundRetry = true
		}
	}
	if !foundRetry {
		t.Fatalf("retry event missing: %#v", events)
	}
}
