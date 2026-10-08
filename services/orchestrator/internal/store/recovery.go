package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/caduc/ai-agent-orchestrator/services/orchestrator/internal/domain"
	"github.com/jackc/pgx/v5"
)

// ClaimAggregation grants owner the aggregation lease of an AGGREGATING Task.
// It returns false when another live owner already holds the lease.
func (s *Store) ClaimAggregation(ctx context.Context, taskID, owner string, lease time.Duration) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE tasks SET lease_owner=$2, lease_until=now()+$3*interval '1 millisecond'
		WHERE id=$1 AND status='AGGREGATING' AND (lease_until IS NULL OR lease_until < now() OR lease_owner=$2)`,
		taskID, owner, lease.Milliseconds())
	if err != nil {
		return false, fmt.Errorf("claim aggregation of task %s: %w", taskID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// ClaimStaleAggregation leases one AGGREGATING Task whose aggregator stopped
// before persisting a Final_Result. A Task without lease is only considered
// stale after a full lease period, leaving time for the worker that closed
// its DAG to claim it first. It returns an empty ID when nothing is stale.
func (s *Store) ClaimStaleAggregation(ctx context.Context, owner string, lease time.Duration) (string, error) {
	var taskID string
	err := s.pool.QueryRow(ctx, `
		UPDATE tasks SET lease_owner=$1, lease_until=now()+$2*interval '1 millisecond'
		WHERE id = (
			SELECT id FROM tasks
			WHERE status='AGGREGATING'
			  AND (lease_until < now() OR (lease_until IS NULL AND updated_at < now()-$2*interval '1 millisecond'))
			ORDER BY updated_at
			FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING id`, owner, lease.Milliseconds()).Scan(&taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("claim stale aggregation: %w", err)
	}
	return taskID, nil
}

// ExpireOverdueAttempt classifies one dispatched Attempt whose deadline plus
// grace elapsed without a result as TIMEOUT, then retries or fails its Subtask
// through the same path as an agent-reported failure. It returns found=false
// when no Attempt is overdue and aggregateTaskID when the DAG closed.
func (s *Store) ExpireOverdueAttempt(ctx context.Context, grace time.Duration) (found bool, aggregateTaskID string, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, "", fmt.Errorf("begin expire attempt: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var attemptID, subtaskID, taskID string
	err = tx.QueryRow(ctx, `
		SELECT a.id, a.subtask_id, s.task_id
		FROM attempts a JOIN subtasks s ON s.id=a.subtask_id
		WHERE a.status='DISPATCHED' AND a.deadline_at < now()-$1*interval '1 millisecond'
		ORDER BY a.deadline_at LIMIT 1`, grace.Milliseconds()).Scan(&attemptID, &subtaskID, &taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("select overdue attempt: %w", err)
	}
	// Lock Subtask and Task before touching the Attempt, in the same order as
	// result consumption, then re-check that no concurrent writer closed it.
	target, err := s.lockAttemptTargetTx(ctx, tx, taskID, subtaskID)
	if err != nil {
		return false, "", err
	}
	var attemptStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM attempts WHERE id=$1`, attemptID).Scan(&attemptStatus); err != nil {
		return false, "", fmt.Errorf("recheck overdue attempt: %w", err)
	}
	if attemptStatus != "DISPATCHED" {
		return true, "", tx.Commit(ctx)
	}
	if !target.accepts(attemptID) {
		// The Attempt was superseded or its Task closed; only close the record.
		if _, err := tx.Exec(ctx, `UPDATE attempts SET status='TIMED_OUT',error_code='TIMEOUT',finished_at=now() WHERE id=$1`, attemptID); err != nil {
			return false, "", fmt.Errorf("close superseded attempt: %w", err)
		}
		return true, "", tx.Commit(ctx)
	}
	if _, err := s.appendEventTx(ctx, tx, taskID, "attempt.timed_out", map[string]any{"subtask_id": subtaskID, "attempt_id": attemptID, "error_code": "TIMEOUT"}); err != nil {
		return false, "", err
	}
	outcome := attemptOutcome{attemptID: attemptID, agentType: target.capability, attemptStatus: "TIMED_OUT", errorCode: "TIMEOUT"}
	aggregate, err := s.finishAttemptTx(ctx, tx, target, outcome)
	if err != nil {
		return false, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, "", fmt.Errorf("commit expire attempt: %w", err)
	}
	if aggregate {
		return true, taskID, nil
	}
	return true, "", nil
}

// ExpireTaskDeadline closes one PLANNING or RUNNING Task whose total deadline
// elapsed. PLANNING fails with TASK_DEADLINE_EXCEEDED; RUNNING stops new work,
// times out in-flight Attempts, skips unstarted Subtasks and closes the DAG so
// confirmed results can still be aggregated. It returns found=false when no
// Task is overdue and aggregateTaskID when the Task moved to AGGREGATING.
func (s *Store) ExpireTaskDeadline(ctx context.Context) (found bool, aggregateTaskID string, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, "", fmt.Errorf("begin expire task deadline: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var taskID, status string
	err = tx.QueryRow(ctx, `
		SELECT id, status FROM tasks
		WHERE status IN ('PLANNING','RUNNING') AND deadline_at < now()
		ORDER BY deadline_at
		FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&taskID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("select overdue task: %w", err)
	}
	const code = "TASK_DEADLINE_EXCEEDED"
	if domain.TaskStatus(status) == domain.TaskPlanning {
		if _, err := tx.Exec(ctx, `UPDATE tasks SET status='FAILED',error_code=$2,lease_owner=NULL,lease_until=NULL,updated_at=now(),version=version+1 WHERE id=$1`, taskID, code); err != nil {
			return false, "", fmt.Errorf("fail overdue planning task: %w", err)
		}
		if _, err := s.appendEventTx(ctx, tx, taskID, "task.failed", map[string]any{"status": domain.TaskFailed, "error_code": code}); err != nil {
			return false, "", err
		}
		return true, "", tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE attempts SET status='TIMED_OUT',error_code='TIMEOUT',finished_at=now()
		WHERE status='DISPATCHED' AND subtask_id IN (SELECT id FROM subtasks WHERE task_id=$1)`, taskID); err != nil {
		return false, "", fmt.Errorf("time out attempts of overdue task: %w", err)
	}
	timedOut, err := tx.Exec(ctx, `UPDATE subtasks SET status='FAILED',error_code='TIMEOUT',updated_at=now() WHERE task_id=$1 AND status='RUNNING'`, taskID)
	if err != nil {
		return false, "", fmt.Errorf("fail running subtasks of overdue task: %w", err)
	}
	skipped, err := tx.Exec(ctx, `UPDATE subtasks SET status='SKIPPED',error_code=$2,updated_at=now() WHERE task_id=$1 AND status IN ('PENDING','BLOCKED','READY')`, taskID, code)
	if err != nil {
		return false, "", fmt.Errorf("skip unstarted subtasks of overdue task: %w", err)
	}
	if _, err := s.appendEventTx(ctx, tx, taskID, "task.deadline_exceeded", map[string]any{
		"error_code": code, "timed_out_subtasks": timedOut.RowsAffected(), "skipped_subtasks": skipped.RowsAffected(),
	}); err != nil {
		return false, "", err
	}
	aggregate, err := s.scheduleAndCloseTx(ctx, tx, taskID)
	if err != nil {
		return false, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, "", fmt.Errorf("commit expire task deadline: %w", err)
	}
	if aggregate {
		return true, taskID, nil
	}
	return true, "", nil
}
