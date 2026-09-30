package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	asyncv1 "github.com/caduc/ai-agent-orchestrator/contracts/async/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
)

type ExecuteFunc func(context.Context, string) (string, []asyncv1.Evidence, []string, error)

type runtime struct {
	typeName string
	instance string
	pool     *pgxpool.Pool
	js       nats.JetStreamContext
	nats     *nats.Conn
	execute  ExecuteFunc
	logger   *slog.Logger
	workers  int
	wg       sync.WaitGroup
}

const schema = `
CREATE TABLE IF NOT EXISTS inbox_messages (
    message_id text PRIMARY KEY,
    status text NOT NULL,
    lease_owner text,
    lease_until timestamptz,
    result jsonb,
    received_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);
CREATE TABLE IF NOT EXISTS tool_calls (
    id text PRIMARY KEY,
    message_id text NOT NULL REFERENCES inbox_messages(message_id) ON DELETE CASCADE,
    tool_name text NOT NULL,
    status text NOT NULL,
    duration_ms bigint NOT NULL,
    error_code text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS outbox_messages (
    id text PRIMARY KEY,
    subject text NOT NULL,
    payload jsonb NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS agent_outbox_pending_idx ON outbox_messages(available_at,created_at) WHERE published_at IS NULL;
`

func Run(ctx context.Context, execute ExecuteFunc) error {
	typeName := os.Getenv("AGENT_TYPE")
	if asyncv1.ExecuteSubject(typeName) == "" {
		return fmt.Errorf("AGENT_TYPE is invalid")
	}
	workers := envInt("WORKER_CONCURRENCY", 8)
	if workers < 1 || workers > 256 {
		return fmt.Errorf("WORKER_CONCURRENCY must be between 1 and 256")
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", typeName+"-agent", "instance_id", hostname()+"-"+uuid.NewString())
	startupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := connectPostgres(startupCtx, env("DATABASE_URL", "postgres://agent:agent@localhost:5432/agent?sslmode=disable"))
	if err != nil {
		return err
	}
	defer pool.Close()
	if _, err := pool.Exec(startupCtx, schema); err != nil {
		return fmt.Errorf("migrate agent database: %w", err)
	}
	natsConnection, err := nats.Connect(env("NATS_URL", "nats://localhost:4222"), nats.Name(typeName+"-agent"), nats.Timeout(5*time.Second), nats.MaxReconnects(-1))
	if err != nil {
		return fmt.Errorf("connect NATS: %w", err)
	}
	defer natsConnection.Close()
	js, err := natsConnection.JetStream()
	if err != nil {
		return err
	}
	if err := ensureStreams(js); err != nil {
		return err
	}
	r := &runtime{typeName: typeName, instance: hostname() + "-" + uuid.NewString(), pool: pool, js: js, nats: natsConnection, execute: execute, logger: logger, workers: workers}
	if err := r.start(ctx); err != nil {
		return err
	}
	health := &http.Server{Addr: env("HEALTH_ADDR", ":8081"), Handler: r.healthHandler()}
	healthErrors := make(chan error, 1)
	go func() { healthErrors <- health.ListenAndServe() }()
	select {
	case <-ctx.Done():
	case err := <-healthErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	_ = health.Shutdown(shutdownCtx)
	r.wg.Wait()
	return natsConnection.Drain()
}

func (r *runtime) start(ctx context.Context) error {
	subscription, err := r.js.PullSubscribe(asyncv1.ExecuteSubject(r.typeName), "agent-"+r.typeName, nats.BindStream("ORCH_COMMANDS"), nats.ManualAck(), nats.AckExplicit())
	if err != nil {
		return fmt.Errorf("subscribe commands: %w", err)
	}
	jobs := make(chan *nats.Msg, r.workers*2)
	r.run(ctx, func(ctx context.Context) {
		defer close(jobs)
		for {
			messages, err := subscription.Fetch(1, nats.MaxWait(time.Second))
			if err != nil {
				if errors.Is(err, nats.ErrTimeout) && ctx.Err() == nil {
					continue
				}
				if ctx.Err() != nil {
					return
				}
				r.logger.ErrorContext(ctx, "fetch command failed", "error", err)
				continue
			}
			select {
			case jobs <- messages[0]:
			case <-ctx.Done():
				return
			}
		}
	})
	for index := 0; index < r.workers; index++ {
		r.run(ctx, func(ctx context.Context) {
			for {
				select {
				case <-ctx.Done():
					return
				case message, open := <-jobs:
					if !open {
						return
					}
					r.process(ctx, message)
				}
			}
		})
	}
	r.run(ctx, r.publishOutbox)
	return nil
}

func (r *runtime) run(ctx context.Context, function func(context.Context)) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() {
			if recovered := recover(); recovered != nil {
				r.logger.Error("worker panic", "panic", recovered)
			}
		}()
		function(ctx)
	}()
}

func (r *runtime) process(ctx context.Context, message *nats.Msg) {
	var command asyncv1.AgentCommand
	if err := json.Unmarshal(message.Data, &command); err != nil || command.SchemaVersion != asyncv1.SchemaVersion || command.Capability != r.typeName {
		_ = message.Term()
		return
	}
	claimed, completed, err := r.claim(ctx, command.MessageID)
	if err != nil {
		_ = message.NakWithDelay(time.Second)
		return
	}
	if completed {
		_ = message.Ack()
		return
	}
	if !claimed {
		_ = message.NakWithDelay(time.Second)
		return
	}
	deadline := command.Deadline
	if deadline.IsZero() || deadline.After(time.Now().Add(2*time.Minute)) {
		deadline = time.Now().Add(2 * time.Minute)
	}
	toolCtx, cancel := context.WithDeadline(ctx, deadline)
	started := time.Now()
	summary, evidence, warnings, toolErr := r.execute(toolCtx, command.Objective)
	cancel()
	result := asyncv1.AgentResult{SchemaVersion: asyncv1.SchemaVersion, MessageID: uuid.NewString(), TaskID: command.TaskID, SubtaskID: command.SubtaskID, AttemptID: command.AttemptID, AgentType: r.typeName, Success: toolErr == nil, Summary: summary, Evidence: evidence, Warnings: warnings, CompletedAt: time.Now().UTC()}
	if toolErr != nil {
		result.ErrorCode = "TOOL_EXECUTION_FAILED"
		if errors.Is(toolErr, context.DeadlineExceeded) || errors.Is(toolCtx.Err(), context.DeadlineExceeded) {
			result.ErrorCode = "TIMEOUT"
		}
		if result.Summary == "" {
			result.Summary = "Agent tool failed"
		}
	}
	if err := r.complete(ctx, command.MessageID, started, result); err != nil {
		r.logger.ErrorContext(ctx, "persist agent result failed", "task_id", command.TaskID, "subtask_id", command.SubtaskID, "error", err)
		_ = message.NakWithDelay(time.Second)
		return
	}
	_ = message.Ack()
	r.logger.InfoContext(ctx, "agent command completed", "task_id", command.TaskID, "subtask_id", command.SubtaskID, "success", result.Success)
}

func (r *runtime) claim(ctx context.Context, messageID string) (bool, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO inbox_messages(message_id,status) VALUES($1,'RECEIVED') ON CONFLICT DO NOTHING`, messageID)
	if err != nil {
		return false, false, err
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM inbox_messages WHERE message_id=$1 FOR UPDATE`, messageID).Scan(&status); err != nil {
		return false, false, err
	}
	if status == "COMPLETED" {
		return false, true, tx.Commit(ctx)
	}
	tag, err := tx.Exec(ctx, `UPDATE inbox_messages SET status='PROCESSING',lease_owner=$2,lease_until=now()+interval '3 minutes' WHERE message_id=$1 AND (status='RECEIVED' OR lease_until < now() OR lease_owner=$2)`, messageID, r.instance)
	if err != nil {
		return false, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, false, err
	}
	return tag.RowsAffected() == 1, false, nil
}

func (r *runtime) complete(ctx context.Context, commandMessageID string, started time.Time, result asyncv1.AgentResult) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE inbox_messages SET status='COMPLETED',result=$3,completed_at=now(),lease_until=NULL WHERE message_id=$1 AND lease_owner=$2 AND status='PROCESSING'`, commandMessageID, r.instance, payload)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("execution lease lost")
	}
	duration := time.Since(started).Milliseconds()
	toolState := "SUCCEEDED"
	if !result.Success {
		toolState = "FAILED"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tool_calls(id,message_id,tool_name,status,duration_ms,error_code) VALUES($1,$2,$3,$4,$5,$6)`, uuid.NewString(), commandMessageID, r.typeName+".inspect", toolState, duration, result.ErrorCode); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_messages(id,subject,payload) VALUES($1,$2,$3)`, result.MessageID, asyncv1.ResultSubject(r.typeName), payload); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *runtime) publishOutbox(ctx context.Context) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		rows, err := r.pool.Query(ctx, `SELECT id,subject,payload::text FROM outbox_messages WHERE published_at IS NULL AND available_at<=now() ORDER BY created_at LIMIT 50`)
		if err == nil {
			type pending struct {
				id, subject string
				payload     []byte
			}
			var messages []pending
			for rows.Next() {
				var item pending
				if scanErr := rows.Scan(&item.id, &item.subject, &item.payload); scanErr == nil {
					messages = append(messages, item)
				}
			}
			rows.Close()
			for _, item := range messages {
				message := nats.NewMsg(item.subject)
				message.Data = item.payload
				message.Header.Set("Nats-Msg-Id", item.id)
				if _, publishErr := r.js.PublishMsg(message, nats.Context(ctx)); publishErr != nil {
					_, _ = r.pool.Exec(ctx, `UPDATE outbox_messages SET attempts=attempts+1,last_error=$2,available_at=now()+interval '1 second' WHERE id=$1`, item.id, publishErr.Error())
					continue
				}
				_, _ = r.pool.Exec(ctx, `UPDATE outbox_messages SET published_at=now(),attempts=attempts+1,last_error='' WHERE id=$1`, item.id)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *runtime) healthHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/health" {
			http.NotFound(writer, request)
			return
		}
		ready := r.pool.Ping(request.Context()) == nil && r.nats.IsConnected()
		statusCode := http.StatusOK
		if !ready {
			statusCode = http.StatusServiceUnavailable
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(statusCode)
		_ = json.NewEncoder(writer).Encode(map[string]any{"live": true, "ready": ready, "agent": r.typeName})
	})
}

func ensureStreams(js nats.JetStreamContext) error {
	configs := []*nats.StreamConfig{
		{Name: "ORCH_COMMANDS", Subjects: []string{"commands.agent.*.execute.v1"}, Retention: nats.WorkQueuePolicy, Storage: nats.FileStorage, MaxAge: 24 * time.Hour},
		{Name: "ORCH_RESULTS", Subjects: []string{"results.agent.*.completed.v1"}, Retention: nats.WorkQueuePolicy, Storage: nats.FileStorage, MaxAge: 24 * time.Hour},
	}
	for _, config := range configs {
		if _, err := js.StreamInfo(config.Name); err == nil {
			continue
		}
		if _, err := js.AddStream(config); err != nil && !stringsContain(err.Error(), "stream name already in use") {
			return err
		}
	}
	return nil
}

func connectPostgres(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	backoff := 250 * time.Millisecond
	for {
		pool, err := pgxpool.New(ctx, databaseURL)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				return pool, nil
			}
			pool.Close()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
			if backoff < 2*time.Second {
				backoff *= 2
			}
		}
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return -1
	}
	return parsed
}

func hostname() string {
	value, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return value
}

func stringsContain(value, fragment string) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if value[index:index+len(fragment)] == fragment {
			return true
		}
	}
	return false
}

var _ = pgx.ErrNoRows
