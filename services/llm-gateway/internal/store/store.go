package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrBudgetExhausted = errors.New("LLM budget exhausted")

const schema = `
CREATE TABLE IF NOT EXISTS task_budgets (
    task_id text PRIMARY KEY,
    max_tokens bigint NOT NULL CHECK(max_tokens > 0),
    reserved_tokens bigint NOT NULL DEFAULT 0 CHECK(reserved_tokens >= 0),
    used_tokens bigint NOT NULL DEFAULT 0 CHECK(used_tokens >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS llm_calls (
    id text PRIMARY KEY,
    task_id text NOT NULL REFERENCES task_budgets(task_id) ON DELETE CASCADE,
    purpose text NOT NULL,
    provider text NOT NULL,
    model text NOT NULL,
    reserved_tokens bigint NOT NULL,
    input_tokens bigint NOT NULL DEFAULT 0,
    output_tokens bigint NOT NULL DEFAULT 0,
    status text NOT NULL,
    error_code text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE INDEX IF NOT EXISTS llm_calls_task_idx ON llm_calls(task_id, started_at);
`

type Store struct{ pool *pgxpool.Pool }

type Budget struct {
	TaskID         string
	MaxTokens      int64
	ReservedTokens int64
	UsedTokens     int64
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, schema)
	if err != nil {
		return fmt.Errorf("migrate LLM database: %w", err)
	}
	return nil
}

func (s *Store) OpenBudget(ctx context.Context, taskID string, maxTokens int64) (Budget, error) {
	if maxTokens <= 0 {
		return Budget{}, fmt.Errorf("max tokens must be positive")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO task_budgets(task_id,max_tokens) VALUES($1,$2) ON CONFLICT(task_id) DO NOTHING`, taskID, maxTokens)
	if err != nil {
		return Budget{}, fmt.Errorf("open task budget: %w", err)
	}
	return s.GetBudget(ctx, taskID)
}

func (s *Store) GetBudget(ctx context.Context, taskID string) (Budget, error) {
	var budget Budget
	err := s.pool.QueryRow(ctx, `SELECT task_id,max_tokens,reserved_tokens,used_tokens FROM task_budgets WHERE task_id=$1`, taskID).Scan(&budget.TaskID, &budget.MaxTokens, &budget.ReservedTokens, &budget.UsedTokens)
	if err != nil {
		return Budget{}, fmt.Errorf("get task budget: %w", err)
	}
	return budget, nil
}

func (s *Store) Reserve(ctx context.Context, taskID, purpose, provider, model string, tokens int64) (string, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var maxTokens, reserved, used int64
	if err := tx.QueryRow(ctx, `SELECT max_tokens,reserved_tokens,used_tokens FROM task_budgets WHERE task_id=$1 FOR UPDATE`, taskID).Scan(&maxTokens, &reserved, &used); err != nil {
		return "", err
	}
	if used+reserved+tokens > maxTokens {
		return "", ErrBudgetExhausted
	}
	callID := uuid.NewString()
	if _, err := tx.Exec(ctx, `UPDATE task_budgets SET reserved_tokens=reserved_tokens+$2,updated_at=now() WHERE task_id=$1`, taskID, tokens); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO llm_calls(id,task_id,purpose,provider,model,reserved_tokens,status) VALUES($1,$2,$3,$4,$5,$6,'RUNNING')`, callID, taskID, purpose, provider, model, tokens); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return callID, nil
}

func (s *Store) Finish(ctx context.Context, callID string, inputTokens, outputTokens int64, status, errorCode string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var taskID string
	var reserved int64
	err = tx.QueryRow(ctx, `SELECT task_id,reserved_tokens FROM llm_calls WHERE id=$1 AND status='RUNNING' FOR UPDATE`, callID).Scan(&taskID, &reserved)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	used := inputTokens + outputTokens
	if used < 0 {
		used = 0
	}
	if used > reserved {
		used = reserved
	}
	if _, err := tx.Exec(ctx, `UPDATE llm_calls SET input_tokens=$2,output_tokens=$3,status=$4,error_code=$5,finished_at=now() WHERE id=$1`, callID, inputTokens, outputTokens, status, errorCode); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE task_budgets SET reserved_tokens=greatest(0,reserved_tokens-$2),used_tokens=used_tokens+$3,updated_at=now() WHERE task_id=$1`, taskID, reserved, used); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
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
			return nil, fmt.Errorf("connect LLM database: %w", ctx.Err())
		case <-time.After(backoff):
			if backoff < 2*time.Second {
				backoff *= 2
			}
		}
	}
}
