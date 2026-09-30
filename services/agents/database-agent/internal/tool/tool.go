package tool

import (
	"context"
	"fmt"
	"os"
	"strings"

	asyncv1 "github.com/caduc/ai-agent-orchestrator/contracts/async/v1"
	"github.com/jackc/pgx/v5"
)

func Execute(ctx context.Context, objective string) (string, []asyncv1.Evidence, []string, error) {
	databaseURL := os.Getenv("TARGET_DATABASE_URL")
	if databaseURL == "" {
		return "", nil, nil, fmt.Errorf("TARGET_DATABASE_URL is required")
	}
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return "", nil, nil, fmt.Errorf("connect target database: %w", err)
	}
	defer connection.Close(ctx)
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return "", nil, nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout='20s'`); err != nil {
		return "", nil, nil, err
	}
	var database, user, version string
	if err := tx.QueryRow(ctx, `SELECT current_database(),current_user,version()`).Scan(&database, &user, &version); err != nil {
		return "", nil, nil, err
	}
	rows, err := tx.Query(ctx, `SELECT table_schema||'.'||table_name FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema') ORDER BY 1 LIMIT 20`)
	if err != nil {
		return "", nil, nil, err
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			return "", nil, nil, err
		}
		tables = append(tables, table)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return "", nil, nil, err
	}
	evidence := []asyncv1.Evidence{
		{Source: "postgresql", Reference: "connection", Content: fmt.Sprintf("database=%s user=%s version=%s", database, user, version)},
		{Source: "postgresql", Reference: "information_schema.tables", Content: strings.Join(tables, ", ")},
	}
	summary := fmt.Sprintf("Database analysis for %q verified read-only connectivity and inspected %d application tables", objective, len(tables))
	return summary, evidence, nil, nil
}
