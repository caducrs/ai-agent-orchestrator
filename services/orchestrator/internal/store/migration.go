package store

const schemaSQL = `
CREATE TABLE IF NOT EXISTS tasks (
    id text PRIMARY KEY,
    owner_subject text NOT NULL,
    description text NOT NULL,
    status text NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    next_event_id bigint NOT NULL DEFAULT 0,
    deadline_at timestamptz NOT NULL,
    final_result jsonb,
    error_code text NOT NULL DEFAULT '',
    cancel_requested_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS tasks_status_created_idx ON tasks(status, created_at);

CREATE TABLE IF NOT EXISTS idempotency_records (
    principal text NOT NULL,
    key_hash text NOT NULL,
    request_fingerprint text NOT NULL,
    task_id text NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (principal, key_hash)
);
CREATE INDEX IF NOT EXISTS idempotency_expiry_idx ON idempotency_records(expires_at);

CREATE TABLE IF NOT EXISTS execution_plans (
    task_id text PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    schema_version integer NOT NULL,
    plan jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS subtasks (
    id text PRIMARY KEY,
    task_id text NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    description text NOT NULL,
    capability text NOT NULL,
    status text NOT NULL,
    depends_on jsonb NOT NULL DEFAULT '[]',
    attempt integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 3,
    timeout_seconds integer NOT NULL DEFAULT 120,
    current_attempt_id text,
    result jsonb,
    error_code text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS subtasks_task_idx ON subtasks(task_id, status);

CREATE TABLE IF NOT EXISTS subtask_dependencies (
    task_id text NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    predecessor_id text NOT NULL REFERENCES subtasks(id) ON DELETE CASCADE,
    successor_id text NOT NULL REFERENCES subtasks(id) ON DELETE CASCADE,
    PRIMARY KEY (predecessor_id, successor_id),
    CHECK (predecessor_id <> successor_id)
);

CREATE TABLE IF NOT EXISTS attempts (
    id text PRIMARY KEY,
    subtask_id text NOT NULL REFERENCES subtasks(id) ON DELETE CASCADE,
    number integer NOT NULL,
    status text NOT NULL,
    deadline_at timestamptz NOT NULL,
    error_code text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    UNIQUE (subtask_id, number)
);

CREATE TABLE IF NOT EXISTS agent_assignments (
    id text PRIMARY KEY,
    attempt_id text NOT NULL UNIQUE REFERENCES attempts(id) ON DELETE CASCADE,
    agent_type text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS normalized_results (
    attempt_id text PRIMARY KEY REFERENCES attempts(id) ON DELETE CASCADE,
    result jsonb NOT NULL,
    result_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS final_results (
    task_id text PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    result jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS task_events (
    task_id text NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    event_id bigint NOT NULL,
    type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}',
    occurred_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, event_id)
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
CREATE INDEX IF NOT EXISTS outbox_pending_idx ON outbox_messages(available_at, created_at) WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS inbox_messages (
    consumer text NOT NULL,
    message_id text NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    PRIMARY KEY (consumer, message_id)
);

CREATE TABLE IF NOT EXISTS agent_registry (
    id text PRIMARY KEY,
    agent_type text NOT NULL UNIQUE,
    version text NOT NULL,
    capabilities jsonb NOT NULL,
    status text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO agent_registry(id, agent_type, version, capabilities, status) VALUES
 ('code-agent', 'code', '1.0.0', '["code"]', 'HEALTHY'),
 ('log-agent', 'logs', '1.0.0', '["logs"]', 'HEALTHY'),
 ('database-agent', 'database', '1.0.0', '["database"]', 'HEALTHY'),
 ('infrastructure-agent', 'infrastructure', '1.0.0', '["infrastructure"]', 'HEALTHY')
ON CONFLICT (id) DO UPDATE SET version = EXCLUDED.version, capabilities = EXCLUDED.capabilities, updated_at = now();
`
