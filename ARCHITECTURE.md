# Architecture

## Boundaries

The platform uses seven business microservices, a static operational dashboard, and a reproducible demo target. Every business service has an independent executable, deployment lifecycle and Go `internal/` boundary. The dashboard is served by an unprivileged Nginx container and communicates only through the API Gateway. The Orchestrator owns the execution aggregate; the API Gateway is stateless; the LLM Gateway owns provider budgets; each Agent Service owns tool execution records.

## Data ownership

- Orchestrator DB: Tasks, plans, Subtasks, Attempts, assignments, accepted results, final results, events and transport inbox/outbox.
- LLM DB: Task budgets and LLM call audit.
- Agent DBs: command inbox, execution lease, tool audit, local result and result outbox.
- Redis: reconstructible rate-limit state only.
- JetStream: durable transport, not business truth.

The Compose profile uses one PostgreSQL server with independent databases and roles. No application role can read another service database.

## Communication

- REST/JSON: public commands and queries.
- SSE: public resumable progress stream.
- gRPC: API Gateway to Orchestrator and Orchestrator to LLM Gateway.
- NATS JetStream: durable agent commands and results.

Synchronous calls have deadlines. Work that must survive caller failure uses JetStream.

## Consistency

Every state change that requires a message writes an outbox row in the same PostgreSQL transaction. Publishers use the outbox ID as `Nats-Msg-Id`. Consumers insert an inbox row and claim a lease before execution. Results are ACKed only after commit. Duplicate delivery can occur, but unique constraints and current-Attempt checks prevent duplicate logical effects.

## Concurrency

The Orchestrator creates independent DAG nodes in one transaction and JetStream makes them concurrently available. Agent replicas share durable consumers. Each process uses a bounded jobs channel and fixed workers. Context cancellation and the command deadline bound every tool execution.

## Security model

The local profile uses a development token. The production design replaces it with OIDC and mTLS. Agent credentials are capability-specific. The Code Agent sees only a read-only volume, the Database Agent has a read-only PostgreSQL role, the Log Agent has only a Loki endpoint, and the Infrastructure Agent has configured health/Prometheus targets.

All external content is untrusted. LLM responses are proposals; deterministic validation and local tool policy remain authoritative.

## Failure model

- PostgreSQL commit succeeds and publish fails: outbox retries.
- Publish succeeds and publisher crashes: NATS deduplication plus consumer inbox.
- Agent crashes after claim: lease expires and JetStream redelivers.
- Result is delivered twice: Orchestrator inbox and Attempt identity absorb it.
- Agent capability fails: independent branches continue and Task can be partially completed.
- LLM aggregation fails: deterministic fallback preserves evidence.
- Cancellation races with result: terminal state and current Attempt reject the late result.

## Scaling

API Gateway and LLM Gateway are stateless above their databases. Orchestrator replicas coordinate through row claims. Agent replicas coordinate through JetStream durable consumers and shared service databases.

```powershell
docker compose up -d --scale code-agent=3 --scale log-agent=3
```

The detailed decisions, risk analysis and requirement traceability are maintained in `.kiro/specs/ai-agent-orchestrator/design.md`.
