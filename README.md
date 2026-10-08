# AI Agent Orchestrator

A production-oriented portfolio project in Go that decomposes a natural-language investigation into a DAG, dispatches independent subtasks to scalable specialist microservices, executes real read-only tools, and returns a grounded consolidated result in real time.

## What it demonstrates

- Go microservices with one module per service and compiler-enforced `internal/` boundaries
- goroutines, bounded channels, worker pools, `context.Context`, timeouts and graceful shutdown
- REST, gRPC, SSE and versioned asynchronous contracts
- NATS JetStream at-least-once delivery with transactional outbox/inbox
- PostgreSQL database-per-service and Redis distributed rate limiting
- local and OpenAI-compatible LLM providers
- real tools for code, logs, PostgreSQL and infrastructure metrics
- structured logs and an observability stack with OpenTelemetry, Prometheus, Tempo, Loki and Grafana
- Docker Compose horizontal scaling

## Architecture

```text
Client
  |
  | HTML
  v
Web Dashboard
  |
  | REST / SSE
  v
API Gateway ---- Redis
  |
  | gRPC
  v
Orchestrator ---- Orchestrator PostgreSQL
  |       |
  |       +---- gRPC ---- LLM Gateway ---- LLM PostgreSQL / Provider
  |
  +---- NATS JetStream ---- Code Agent -------- read-only repository
                         |-- Log Agent --------- Loki
                         |-- Database Agent ---- read-only PostgreSQL target
                         +-- Infrastructure ---- health endpoints / Prometheus
```

The Orchestrator exclusively owns Task, plan, Subtask, Attempt, event and final-result state. Agents never update the Orchestrator database and never communicate directly with one another. Each Agent Service owns its inbox, leases, tool audit and outbox.

See [ARCHITECTURE.md](ARCHITECTURE.md) and the approved [technical design](.kiro/specs/ai-agent-orchestrator/design.md).

## Services

| Service | Public responsibility | Internal boundary |
|---|---|---|
| Web Dashboard | Task workspace, authenticated SSE and result exploration | `web/` |
| API Gateway | REST, SSE, local auth and Redis GCRA | `services/api-gateway/internal` |
| Orchestrator | Task lifecycle, DAG, scheduling and aggregation | `services/orchestrator/internal` |
| LLM Gateway | provider isolation and token budgets | `services/llm-gateway/internal` |
| Code Agent | safe source search | `services/agents/code-agent/internal` |
| Log Agent | Loki investigation | `services/agents/log-agent/internal` |
| Database Agent | read-only PostgreSQL diagnostics | `services/agents/database-agent/internal` |
| Infrastructure Agent | health and Prometheus inspection | `services/agents/infrastructure-agent/internal` |

Agent transport mechanics are shared only inside `services/agents/internal/runtime`, which cannot be imported outside the agents boundary.

## Quick start

Requirements: Docker Desktop with Linux containers and Docker Compose.

```powershell
docker compose up --build -d
docker compose ps
Invoke-RestMethod http://localhost:8080/api/v1/health
```

Open the operational dashboard at **http://localhost:3001**. It creates Tasks, follows authenticated SSE, displays the four agents, and renders evidence and the final result.

Create a task:

```powershell
$headers = @{
  Authorization = "Bearer dev-token"
  "Content-Type" = "application/json"
  "Idempotency-Key" = "demo-500-1"
}
$created = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/tasks -Headers $headers -Body '{"description":"Analise por que minha aplicação está retornando HTTP 500."}'
$taskId = $created.task.id
$taskId
```

Read progress:

```powershell
curl.exe -N -H "Authorization: Bearer dev-token" "http://localhost:8080/api/v1/tasks/$taskId/events"
```

Read the persisted result:

```powershell
Invoke-RestMethod -Headers @{Authorization="Bearer dev-token"} "http://localhost:8080/api/v1/tasks/$taskId" | ConvertTo-Json -Depth 20
```

Run the automated smoke test:

```powershell
powershell -ExecutionPolicy Bypass -File scripts/smoke-test.ps1
```

## Horizontal scaling

Agent replicas share durable pull consumers. The inbox and lease tables prevent duplicate logical effects during JetStream redelivery.

```powershell
docker compose up -d --scale code-agent=3 --scale log-agent=2 --scale database-agent=2 --scale infrastructure-agent=2
docker compose ps
```

No service uses `container_name`, so Compose can create replicas. Worker concurrency is independently controlled with `WORKER_CONCURRENCY`.

## Task flow

1. API Gateway validates authentication, payload, rate limit and idempotency metadata.
2. Orchestrator commits the Task and monotonic event before returning HTTP 202.
3. Planner asks the LLM Gateway for a structured DAG and validates IDs, capabilities and acyclicity.
4. Scheduler atomically persists Attempt, assignment and outbox command.
5. JetStream distributes independent commands concurrently by capability.
6. Agent workers claim an inbox lease, execute a real read-only tool and commit result plus result outbox.
7. Orchestrator accepts each result idempotently, releases dependencies and preserves partial successes.
8. Aggregator requests a grounded synthesis or uses deterministic fallback.
9. SSE replays persisted events and emits the terminal result.

## Concurrency and failure handling

- Pull consumers and bounded worker channels apply backpressure.
- Global, per-Task and per-agent capacity are explicit.
- Delivery retry and business retry are separate.
- ACK occurs only after local database commit.
- Duplicate commands/results are absorbed by inbox and unique constraints.
- Tool and task deadlines derive from cancellation-aware contexts.
- Panics are contained per unit of work, so planning, reconciliation and result workers keep running.
- Terminal Task state rejects late results.
- A reconciler (`RECONCILE_INTERVAL`, default `5s`) times out Attempts without results, closes Tasks past their total deadline and resumes planning (`PLANNING_LEASE`) or aggregation (`AGGREGATION_LEASE`) left behind by a crashed replica.
- Schema changes are versioned migrations recorded in `schema_migrations` and serialized by an advisory lock.
- Shutdown stops admission, drains work, persists recoverable state and closes producers before consumers.

## Tools and protections

| Agent | Real source | Main controls |
|---|---|---|
| Code | repository mounted read-only | canonical roots, symlink containment, file/byte limits, no execution |
| Logs | Loki query API | configured target, fixed query shape, time/result/byte limits |
| Database | PostgreSQL target | dedicated read-only role, read-only transaction, fixed diagnostics, statement timeout |
| Infrastructure | health and Prometheus HTTP APIs | configured targets, GET only, target allowlist and response limits |

LLM output never authorizes a tool. Tools execute behind deterministic local policy and their output remains untrusted data.

## LLM providers

The default `local` provider is deterministic and requires no API key. It creates four independent diagnostic subtasks so concurrency and the full tool chain are observable.

To use an OpenAI-compatible endpoint:

```powershell
$env:LLM_PROVIDER = "openai"
$env:OPENAI_API_KEY = "replace-me"
$env:OPENAI_BASE_URL = "https://api.openai.com/v1"
$env:OPENAI_MODEL = "gpt-4.1-mini"
docker compose up --build -d
```

Keys are passed at runtime and are not stored in source control.

## API

| Method | Path | Scope |
|---|---|---|
| POST | `/api/v1/tasks` | `tasks:create` |
| GET | `/api/v1/tasks/{id}` | `tasks:read` |
| POST | `/api/v1/tasks/{id}/cancel` | `tasks:cancel` |
| GET | `/api/v1/tasks/{id}/events` | `tasks:read` |
| GET | `/api/v1/agents` | `agents:read` |
| GET | `/api/v1/health` | public operational check |

See [infra/docs/API.md](infra/docs/API.md), [OpenAPI](contracts/openapi/v1/orchestrator.yaml) and [AsyncAPI](contracts/asyncapi/orchestrator.yaml).

## Observability

| Component | URL |
|---|---|
| Dashboard | http://localhost:3001 |
| API | http://localhost:8080 |
| Grafana | http://localhost:3000 (`admin` / `admin`) |
| Prometheus | http://localhost:9095 |
| Loki | http://localhost:3100 |
| Tempo | http://localhost:3200 |
| NATS monitoring | http://localhost:8222 |

Logs carry service, request, Task, Subtask, Attempt and message identifiers where available. High-cardinality IDs are excluded from metric labels.

## Development and tests

```powershell
go work sync
go test ./contracts/...
go test ./services/orchestrator/...
go test ./services/api-gateway/...
go test ./services/llm-gateway/...
go test ./services/agents/...
go test ./services/agents/code-agent/...
go test ./services/agents/log-agent/...
go test ./services/agents/database-agent/...
go test ./services/agents/infrastructure-agent/...
go test ./services/demo-app/...
```

Race detector:

```powershell
go test -race ./services/orchestrator/...
go test -race ./services/api-gateway/...
go test -race ./services/llm-gateway/...
go test -race ./services/agents/...
```

Regenerate contracts:

```powershell
$gobin = Join-Path (go env GOPATH) "bin"
& "$gobin\buf.exe" lint
& "$gobin\buf.exe" generate
```

## Project layout

```text
contracts/                 Protobuf, generated Go, AsyncAPI and OpenAPI
services/api-gateway/      public HTTP boundary
services/orchestrator/     execution ownership
services/llm-gateway/      LLM provider and budgets
services/agents/           agent services and restricted shared runtime
services/demo-app/         reproducible failing target
infra/                     PostgreSQL seed and observability configuration
scripts/                   smoke test
docs/                      API and architecture documentation
```

## Current limitations

- The runnable local profile uses an explicit development bearer token. A production OIDC adapter and service mTLS are documented design targets but are not enabled in the local Compose profile.
- Tool policies are intentionally narrow and read-only.
- The OpenAI-compatible adapter uses JSON mode; provider-specific structured-output differences may require an adapter extension.
- PostgreSQL databases share one local container, while credentials and databases remain isolated.
- The demo targets local fixtures rather than external production systems.
- Exactly-once delivery is not claimed; effects are idempotent under at-least-once delivery.

## Roadmap

- OIDC/JWKS and mTLS production profile
- signed execution grants and NATS account isolation
- Testcontainers integration and automated crash/redelivery scenarios
- paginated task history
- human approval for higher-risk tools
- Kubernetes deployment and autoscaling from consumer lag
- multi-tenant policy and data isolation

## License

MIT
