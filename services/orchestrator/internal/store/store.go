package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	asyncv1 "github.com/caduc/ai-agent-orchestrator/contracts/async/v1"
	"github.com/caduc/ai-agent-orchestrator/services/orchestrator/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("resource not found")
	ErrConflict = errors.New("resource conflict")
)

type Store struct {
	pool           *pgxpool.Pool
	subtaskTimeout time.Duration
}

func New(pool *pgxpool.Pool, subtaskTimeout time.Duration) *Store {
	return &Store{pool: pool, subtaskTimeout: subtaskTimeout}
}

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("migrate orchestrator database: %w", err)
	}
	return nil
}

func (s *Store) CreateTask(ctx context.Context, task domain.Task, keyHash, fingerprint string) (domain.Task, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return domain.Task{}, false, fmt.Errorf("begin create task: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if keyHash != "" {
		var existingID, existingFingerprint string
		err = tx.QueryRow(ctx, `
			SELECT task_id, request_fingerprint
			FROM idempotency_records
			WHERE principal=$1 AND key_hash=$2 AND expires_at > now()
			FOR UPDATE`, task.OwnerSubject, keyHash).Scan(&existingID, &existingFingerprint)
		if err == nil {
			if existingFingerprint != fingerprint {
				return domain.Task{}, false, ErrConflict
			}
			if err := tx.Commit(ctx); err != nil {
				return domain.Task{}, false, fmt.Errorf("commit idempotency replay: %w", err)
			}
			existing, err := s.GetTask(ctx, existingID)
			return existing, true, err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.Task{}, false, fmt.Errorf("lookup idempotency record: %w", err)
		}
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO tasks(id, owner_subject, description, status, deadline_at, created_at, updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$6)`, task.ID, task.OwnerSubject, task.Description, task.Status, task.DeadlineAt, task.CreatedAt)
	if err != nil {
		return domain.Task{}, false, fmt.Errorf("insert task: %w", err)
	}
	if keyHash != "" {
		_, err = tx.Exec(ctx, `
			INSERT INTO idempotency_records(principal,key_hash,request_fingerprint,task_id,expires_at)
			VALUES($1,$2,$3,$4,now()+interval '24 hours')`, task.OwnerSubject, keyHash, fingerprint, task.ID)
		if err != nil {
			return domain.Task{}, false, fmt.Errorf("insert idempotency record: %w", err)
		}
	}
	if _, err = s.appendEventTx(ctx, tx, task.ID, "task.queued", map[string]any{"status": task.Status}); err != nil {
		return domain.Task{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Task{}, false, fmt.Errorf("commit create task: %w", err)
	}
	return task, false, nil
}

func (s *Store) GetTask(ctx context.Context, taskID string) (domain.Task, error) {
	var task domain.Task
	var status string
	var finalRaw []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, owner_subject, description, status, created_at, updated_at, deadline_at,
		       COALESCE(final_result::text, 'null'), error_code
		FROM tasks WHERE id=$1`, taskID).Scan(
		&task.ID, &task.OwnerSubject, &task.Description, &status, &task.CreatedAt, &task.UpdatedAt,
		&task.DeadlineAt, &finalRaw, &task.ErrorCode,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Task{}, ErrNotFound
	}
	if err != nil {
		return domain.Task{}, fmt.Errorf("get task: %w", err)
	}
	task.Status = domain.TaskStatus(status)
	task.FinalResult, err = unmarshalFinal(finalRaw)
	if err != nil {
		return domain.Task{}, fmt.Errorf("decode final result: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, task_id, description, capability, status, depends_on::text, attempt,
		       max_attempts, timeout_seconds, COALESCE(current_attempt_id,''), COALESCE(result::text,'null'), error_code
		FROM subtasks WHERE task_id=$1 ORDER BY created_at,id`, taskID)
	if err != nil {
		return domain.Task{}, fmt.Errorf("list subtasks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item domain.Subtask
		var subStatus string
		var dependenciesRaw, resultRaw []byte
		if err := rows.Scan(&item.ID, &item.TaskID, &item.Description, &item.Capability, &subStatus,
			&dependenciesRaw, &item.Attempt, &item.MaxAttempts, &item.TimeoutSeconds,
			&item.CurrentAttemptID, &resultRaw, &item.ErrorCode); err != nil {
			return domain.Task{}, fmt.Errorf("scan subtask: %w", err)
		}
		item.Status = domain.SubtaskStatus(subStatus)
		if err := json.Unmarshal(dependenciesRaw, &item.DependsOn); err != nil {
			return domain.Task{}, fmt.Errorf("decode dependencies: %w", err)
		}
		item.Result, err = unmarshalResult(resultRaw)
		if err != nil {
			return domain.Task{}, fmt.Errorf("decode agent result: %w", err)
		}
		task.Subtasks = append(task.Subtasks, item)
	}
	if err := rows.Err(); err != nil {
		return domain.Task{}, fmt.Errorf("iterate subtasks: %w", err)
	}
	return task, nil
}

func (s *Store) CancelTask(ctx context.Context, taskID string) (domain.Task, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Task{}, fmt.Errorf("begin cancel task: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current string
	if err := tx.QueryRow(ctx, `SELECT status FROM tasks WHERE id=$1 FOR UPDATE`, taskID).Scan(&current); errors.Is(err, pgx.ErrNoRows) {
		return domain.Task{}, ErrNotFound
	} else if err != nil {
		return domain.Task{}, fmt.Errorf("lock task for cancel: %w", err)
	}
	status := domain.TaskStatus(current)
	if domain.IsTaskTerminal(status) && status != domain.TaskCancelled {
		return domain.Task{}, ErrConflict
	}
	if status != domain.TaskCancelled {
		_, err = tx.Exec(ctx, `UPDATE tasks SET status='CANCELLED', cancel_requested_at=now(), updated_at=now(), version=version+1 WHERE id=$1`, taskID)
		if err != nil {
			return domain.Task{}, fmt.Errorf("cancel task: %w", err)
		}
		_, _ = tx.Exec(ctx, `UPDATE subtasks SET status='CANCELLED', updated_at=now() WHERE task_id=$1 AND status NOT IN ('SUCCEEDED','FAILED','SKIPPED','CANCELLED')`, taskID)
		_, _ = tx.Exec(ctx, `UPDATE attempts SET status='CANCELLED', finished_at=now() WHERE subtask_id IN (SELECT id FROM subtasks WHERE task_id=$1) AND status IN ('DISPATCHED','RUNNING')`, taskID)
		if _, err = s.appendEventTx(ctx, tx, taskID, "task.cancelled", map[string]any{"status": domain.TaskCancelled}); err != nil {
			return domain.Task{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Task{}, fmt.Errorf("commit cancel task: %w", err)
	}
	return s.GetTask(ctx, taskID)
}

func (s *Store) ClaimQueuedTask(ctx context.Context) (*domain.Task, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin claim queued task: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var task domain.Task
	err = tx.QueryRow(ctx, `
		SELECT id, owner_subject, description, created_at, updated_at, deadline_at
		FROM tasks
		WHERE status='QUEUED' AND cancel_requested_at IS NULL
		ORDER BY created_at
		FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(
		&task.ID, &task.OwnerSubject, &task.Description, &task.CreatedAt, &task.UpdatedAt, &task.DeadlineAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select queued task: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE tasks SET status='PLANNING', updated_at=now(), version=version+1 WHERE id=$1`, task.ID); err != nil {
		return nil, fmt.Errorf("mark task planning: %w", err)
	}
	if _, err = s.appendEventTx(ctx, tx, task.ID, "task.planning", map[string]any{"status": domain.TaskPlanning}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit queued task claim: %w", err)
	}
	task.Status = domain.TaskPlanning
	return &task, nil
}

func (s *Store) SavePlan(ctx context.Context, taskID string, plan []domain.PlannedSubtask) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin save plan: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM tasks WHERE id=$1 FOR UPDATE`, taskID).Scan(&status); err != nil {
		return fmt.Errorf("lock task for plan: %w", err)
	}
	if status != string(domain.TaskPlanning) {
		return ErrConflict
	}
	planRaw, err := marshal(plan)
	if err != nil {
		return fmt.Errorf("encode plan: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO execution_plans(task_id,schema_version,plan) VALUES($1,1,$2)`, taskID, planRaw); err != nil {
		return fmt.Errorf("insert execution plan: %w", err)
	}
	for _, item := range plan {
		dependencies, err := marshal(item.DependsOn)
		if err != nil {
			return fmt.Errorf("encode dependencies: %w", err)
		}
		state := domain.SubtaskReady
		if len(item.DependsOn) > 0 {
			state = domain.SubtaskBlocked
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO subtasks(id,task_id,description,capability,status,depends_on,max_attempts,timeout_seconds)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, item.ID, taskID, item.Description, item.Capability,
			state, dependencies, item.MaxAttempts, item.TimeoutSeconds); err != nil {
			return fmt.Errorf("insert subtask %s: %w", item.ID, err)
		}
	}
	for _, item := range plan {
		for _, dependency := range item.DependsOn {
			if _, err = tx.Exec(ctx, `INSERT INTO subtask_dependencies(task_id,predecessor_id,successor_id) VALUES($1,$2,$3)`, taskID, dependency, item.ID); err != nil {
				return fmt.Errorf("insert dependency: %w", err)
			}
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE tasks SET status='RUNNING', updated_at=now(), version=version+1 WHERE id=$1`, taskID); err != nil {
		return fmt.Errorf("mark task running: %w", err)
	}
	if _, err = s.appendEventTx(ctx, tx, taskID, "task.running", map[string]any{"status": domain.TaskRunning, "subtasks": len(plan)}); err != nil {
		return err
	}
	for _, item := range plan {
		if len(item.DependsOn) == 0 {
			if err := s.dispatchTx(ctx, tx, taskID, item.ID, item.Capability, item.Description, item.TimeoutSeconds); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit execution plan: %w", err)
	}
	return nil
}

func (s *Store) FailPlanning(ctx context.Context, taskID, code string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `UPDATE tasks SET status='FAILED',error_code=$2,updated_at=now(),version=version+1 WHERE id=$1 AND status='PLANNING'`, taskID, code); err != nil {
		return fmt.Errorf("fail planning: %w", err)
	}
	if _, err = s.appendEventTx(ctx, tx, taskID, "task.failed", map[string]any{"status": domain.TaskFailed, "error_code": code}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) PendingOutbox(ctx context.Context, limit int) ([]OutboxMessage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id,subject,payload::text FROM outbox_messages
		WHERE published_at IS NULL AND available_at <= now()
		ORDER BY created_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("query outbox: %w", err)
	}
	defer rows.Close()
	var messages []OutboxMessage
	for rows.Next() {
		var message OutboxMessage
		if err := rows.Scan(&message.ID, &message.Subject, &message.Payload); err != nil {
			return nil, fmt.Errorf("scan outbox: %w", err)
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *Store) MarkOutboxPublished(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox_messages SET published_at=now(),attempts=attempts+1,last_error='' WHERE id=$1`, id)
	return err
}

func (s *Store) MarkOutboxFailed(ctx context.Context, id string, cause error) error {
	_, err := s.pool.Exec(ctx, `UPDATE outbox_messages SET attempts=attempts+1,last_error=$2,available_at=now()+least(attempts+1,30)*interval '1 second' WHERE id=$1`, id, cause.Error())
	return err
}

func (s *Store) ApplyAgentResult(ctx context.Context, messageID string, payload []byte, incoming asyncv1.AgentResult) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin apply agent result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `INSERT INTO inbox_messages(consumer,message_id) VALUES('orchestrator-results',$1) ON CONFLICT DO NOTHING`, messageID)
	if err != nil {
		return false, fmt.Errorf("insert inbox message: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}

	var taskStatus, subtaskStatus, currentAttempt string
	err = tx.QueryRow(ctx, `
		SELECT t.status,s.status,COALESCE(s.current_attempt_id,'')
		FROM subtasks s JOIN tasks t ON t.id=s.task_id
		WHERE s.id=$1 AND s.task_id=$2 FOR UPDATE OF s,t`, incoming.SubtaskID, incoming.TaskID).Scan(&taskStatus, &subtaskStatus, &currentAttempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("lock result target: %w", err)
	}
	if domain.IsTaskTerminal(domain.TaskStatus(taskStatus)) || currentAttempt != incoming.AttemptID || domain.IsSubtaskTerminal(domain.SubtaskStatus(subtaskStatus)) {
		_, _ = tx.Exec(ctx, `UPDATE inbox_messages SET completed_at=now() WHERE consumer='orchestrator-results' AND message_id=$1`, messageID)
		return false, tx.Commit(ctx)
	}

	result := domain.AgentResult{Summary: incoming.Summary, Warnings: incoming.Warnings}
	for _, evidence := range incoming.Evidence {
		result.Evidence = append(result.Evidence, domain.Evidence{Source: evidence.Source, Reference: evidence.Reference, Content: evidence.Content})
	}
	resultRaw, err := marshal(result)
	if err != nil {
		return false, fmt.Errorf("encode result: %w", err)
	}
	hash := sha256.Sum256(payload)
	if _, err = tx.Exec(ctx, `INSERT INTO normalized_results(attempt_id,result,result_hash) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, incoming.AttemptID, resultRaw, hex.EncodeToString(hash[:])); err != nil {
		return false, fmt.Errorf("insert normalized result: %w", err)
	}
	newStatus := domain.SubtaskSucceeded
	attemptStatus := "SUCCEEDED"
	if !incoming.Success {
		newStatus = domain.SubtaskFailed
		attemptStatus = "FAILED"
	}
	if _, err = tx.Exec(ctx, `UPDATE attempts SET status=$2,error_code=$3,finished_at=now() WHERE id=$1`, incoming.AttemptID, attemptStatus, incoming.ErrorCode); err != nil {
		return false, fmt.Errorf("finish attempt: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE subtasks SET status=$2,result=$3,error_code=$4,updated_at=now() WHERE id=$1`, incoming.SubtaskID, newStatus, resultRaw, incoming.ErrorCode); err != nil {
		return false, fmt.Errorf("finish subtask: %w", err)
	}
	if _, err = s.appendEventTx(ctx, tx, incoming.TaskID, "subtask.completed", map[string]any{"subtask_id": incoming.SubtaskID, "status": newStatus, "agent_type": incoming.AgentType}); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE inbox_messages SET completed_at=now() WHERE consumer='orchestrator-results' AND message_id=$1`, messageID); err != nil {
		return false, err
	}

	aggregate, err := s.scheduleAndCloseTx(ctx, tx, incoming.TaskID)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit agent result: %w", err)
	}
	return aggregate, nil
}

func (s *Store) CompleteTask(ctx context.Context, taskID string, result domain.FinalResult) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM tasks WHERE id=$1 FOR UPDATE`, taskID).Scan(&status); err != nil {
		return err
	}
	if domain.IsTaskTerminal(domain.TaskStatus(status)) {
		return tx.Commit(ctx)
	}
	if status != string(domain.TaskAggregating) {
		return ErrConflict
	}
	var successes, failures int
	if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='SUCCEEDED'), count(*) FILTER (WHERE status IN ('FAILED','SKIPPED','CANCELLED')) FROM subtasks WHERE task_id=$1`, taskID).Scan(&successes, &failures); err != nil {
		return err
	}
	finalStatus := domain.TaskCompleted
	errorCode := ""
	if successes == 0 {
		finalStatus = domain.TaskFailed
		errorCode = "NO_USEFUL_RESULT"
	} else if failures > 0 {
		finalStatus = domain.TaskPartiallyCompleted
	}
	raw, err := marshal(result)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO final_results(task_id,result) VALUES($1,$2) ON CONFLICT(task_id) DO UPDATE SET result=EXCLUDED.result`, taskID, raw); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE tasks SET status=$2,final_result=$3,error_code=$4,updated_at=now(),version=version+1 WHERE id=$1`, taskID, finalStatus, raw, errorCode); err != nil {
		return err
	}
	if _, err = s.appendEventTx(ctx, tx, taskID, "task.completed", map[string]any{"status": finalStatus}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) EventsAfter(ctx context.Context, taskID string, after int64, limit int) ([]domain.Event, error) {
	rows, err := s.pool.Query(ctx, `SELECT task_id,event_id,type,occurred_at,payload::text FROM task_events WHERE task_id=$1 AND event_id>$2 ORDER BY event_id LIMIT $3`, taskID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []domain.Event
	for rows.Next() {
		var event domain.Event
		if err := rows.Scan(&event.TaskID, &event.EventID, &event.Type, &event.OccurredAt, &event.PayloadJSON); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) ListAgents(ctx context.Context) ([]AgentDescriptor, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,agent_type,version,capabilities::text,status FROM agent_registry ORDER BY agent_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var agents []AgentDescriptor
	for rows.Next() {
		var agent AgentDescriptor
		var capabilitiesRaw []byte
		if err := rows.Scan(&agent.ID, &agent.Type, &agent.Version, &capabilitiesRaw, &agent.Status); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(capabilitiesRaw, &agent.Capabilities); err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

func (s *Store) appendEventTx(ctx context.Context, tx pgx.Tx, taskID, eventType string, payload any) (int64, error) {
	raw, err := marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("encode event payload: %w", err)
	}
	var eventID int64
	if err := tx.QueryRow(ctx, `UPDATE tasks SET next_event_id=next_event_id+1 WHERE id=$1 RETURNING next_event_id`, taskID).Scan(&eventID); err != nil {
		return 0, fmt.Errorf("allocate event id: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO task_events(task_id,event_id,type,payload) VALUES($1,$2,$3,$4)`, taskID, eventID, eventType, raw); err != nil {
		return 0, fmt.Errorf("insert task event: %w", err)
	}
	return eventID, nil
}

func (s *Store) dispatchTx(ctx context.Context, tx pgx.Tx, taskID, subtaskID, capability, objective string, timeoutSeconds int32) error {
	attemptID := uuid.New().String()
	assignmentID := uuid.New().String()
	outboxID := uuid.New().String()
	var attemptNumber int32
	if err := tx.QueryRow(ctx, `UPDATE subtasks SET status='RUNNING',attempt=attempt+1,current_attempt_id=$2,updated_at=now() WHERE id=$1 AND status IN ('READY','BLOCKED') RETURNING attempt`, subtaskID, attemptID).Scan(&attemptNumber); err != nil {
		return fmt.Errorf("dispatch subtask %s: %w", subtaskID, err)
	}
	attemptDeadline := deadline(time.Now().UTC(), timeoutSeconds, s.subtaskTimeout)
	if _, err := tx.Exec(ctx, `INSERT INTO attempts(id,subtask_id,number,status,deadline_at) VALUES($1,$2,$3,'DISPATCHED',$4)`, attemptID, subtaskID, attemptNumber, attemptDeadline); err != nil {
		return fmt.Errorf("insert attempt: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO agent_assignments(id,attempt_id,agent_type) VALUES($1,$2,$3)`, assignmentID, attemptID, capability); err != nil {
		return fmt.Errorf("insert assignment: %w", err)
	}
	command := asyncv1.AgentCommand{SchemaVersion: asyncv1.SchemaVersion, MessageID: outboxID, TaskID: taskID, SubtaskID: subtaskID, AttemptID: attemptID, Capability: capability, Objective: objective, Deadline: attemptDeadline}
	payload, err := marshal(command)
	if err != nil {
		return fmt.Errorf("encode agent command: %w", err)
	}
	subject := asyncv1.ExecuteSubject(capability)
	if subject == "" {
		return fmt.Errorf("unknown capability %q", capability)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_messages(id,subject,payload) VALUES($1,$2,$3)`, outboxID, subject, payload); err != nil {
		return fmt.Errorf("insert outbox command: %w", err)
	}
	_, err = s.appendEventTx(ctx, tx, taskID, "subtask.dispatched", map[string]any{"subtask_id": subtaskID, "attempt_id": attemptID, "capability": capability})
	return err
}

func (s *Store) scheduleAndCloseTx(ctx context.Context, tx pgx.Tx, taskID string) (bool, error) {
	rows, err := tx.Query(ctx, `SELECT id,description,capability,status,depends_on::text,timeout_seconds FROM subtasks WHERE task_id=$1 ORDER BY created_at,id`, taskID)
	if err != nil {
		return false, err
	}
	type state struct {
		id, description, capability, status string
		dependencies                        []string
		timeout                             int32
	}
	var items []state
	statusByID := map[string]string{}
	for rows.Next() {
		var item state
		var raw []byte
		if err := rows.Scan(&item.id, &item.description, &item.capability, &item.status, &raw, &item.timeout); err != nil {
			rows.Close()
			return false, err
		}
		if err := json.Unmarshal(raw, &item.dependencies); err != nil {
			rows.Close()
			return false, err
		}
		items = append(items, item)
		statusByID[item.id] = item.status
	}
	rows.Close()

	for _, item := range items {
		if item.status != string(domain.SubtaskBlocked) {
			continue
		}
		allSucceeded := true
		failedDependency := ""
		for _, dependency := range item.dependencies {
			status := domain.SubtaskStatus(statusByID[dependency])
			if status == domain.SubtaskFailed || status == domain.SubtaskSkipped || status == domain.SubtaskCancelled {
				failedDependency = dependency
				break
			}
			if status != domain.SubtaskSucceeded {
				allSucceeded = false
			}
		}
		if failedDependency != "" {
			if _, err := tx.Exec(ctx, `UPDATE subtasks SET status='SKIPPED',error_code='DEPENDENCY_FAILED',updated_at=now() WHERE id=$1 AND status='BLOCKED'`, item.id); err != nil {
				return false, err
			}
			statusByID[item.id] = string(domain.SubtaskSkipped)
			if _, err := s.appendEventTx(ctx, tx, taskID, "subtask.skipped", map[string]any{"subtask_id": item.id, "dependency_id": failedDependency}); err != nil {
				return false, err
			}
		} else if allSucceeded {
			if _, err := tx.Exec(ctx, `UPDATE subtasks SET status='READY',updated_at=now() WHERE id=$1 AND status='BLOCKED'`, item.id); err != nil {
				return false, err
			}
			if err := s.dispatchTx(ctx, tx, taskID, item.id, item.capability, item.description, item.timeout); err != nil {
				return false, err
			}
			statusByID[item.id] = string(domain.SubtaskRunning)
		}
	}

	var total, terminal int
	if err := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE status IN ('SUCCEEDED','FAILED','SKIPPED','CANCELLED')) FROM subtasks WHERE task_id=$1`, taskID).Scan(&total, &terminal); err != nil {
		return false, err
	}
	if total > 0 && total == terminal {
		tag, err := tx.Exec(ctx, `UPDATE tasks SET status='AGGREGATING',updated_at=now(),version=version+1 WHERE id=$1 AND status='RUNNING'`, taskID)
		if err != nil {
			return false, err
		}
		if tag.RowsAffected() == 1 {
			if _, err := s.appendEventTx(ctx, tx, taskID, "task.aggregating", map[string]any{"status": domain.TaskAggregating}); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}
