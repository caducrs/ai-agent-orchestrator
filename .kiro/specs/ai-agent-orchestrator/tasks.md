# Implementation Tasks

- [x] 1. Establish contracts and Go workspace
  - Create `go.work` and isolated Go modules.
  - Define Protobuf, AsyncAPI and asynchronous DTO contracts.
  - Generate and validate gRPC code with pinned Buf plugins.
  - _Requirements: 1, 15, 21, 22, 28_

- [x] 2. Implement the Orchestrator Service
  - Implement Task/Subtask state machines, DAG validation and deterministic fallback.
  - Implement PostgreSQL persistence, monotonic events, idempotency, attempts and assignments.
  - Implement planning, scheduling, bounded result workers and aggregation.
  - Implement transactional outbox/inbox with NATS JetStream.
  - Expose gRPC create, get, cancel, list agents, event stream and health APIs.
  - _Requirements: 1-4, 10-16, 22-25, 28_

- [x] 3. Implement the LLM Gateway
  - Implement task budgets and call audit in PostgreSQL.
  - Implement deterministic local provider and OpenAI-compatible provider.
  - Expose Plan, AgentStep, Aggregate and Health over gRPC.
  - Add structured output parsing, deadlines and deterministic local aggregation.
  - _Requirements: 3, 14, 18-21, 23_

- [x] 4. Implement the API Gateway
  - Implement REST v1 routes and gRPC translation.
  - Implement explicit local authentication, scopes and ownership propagation.
  - Implement Redis GCRA rate limiting, body limits and safe error envelopes.
  - Implement resumable SSE with heartbeat and bounded buffering.
  - _Requirements: 1-2, 12, 15, 17-18, 24-25_

- [x] 5. Implement specialized Agent Services
  - Implement scalable JetStream consumers, bounded workers, leases, inbox and outbox.
  - Implement Code Agent with canonical read-only filesystem access.
  - Implement Log Agent with bounded Loki queries.
  - Implement Database Agent with fixed read-only diagnostics and read-only transaction.
  - Implement Infrastructure Agent with configured health and Prometheus targets.
  - _Requirements: 4-13, 20, 22-25, 28_

- [x] 6. Containerize the platform
  - Add non-root multi-stage Dockerfiles.
  - Add scalable Docker Compose services without fixed container names.
  - Add PostgreSQL databases and isolated roles, Redis and NATS JetStream.
  - Add Loki, Prometheus, Tempo, OpenTelemetry Collector and Grafana.
  - Add a real demo service that emits errors, logs and metrics.
  - _Requirements: 19, 23-26_

- [x] 7. Add automated tests
  - Cover state transitions, DAG validation, deterministic aggregation and HTTP/auth boundaries.
  - Cover each real tool adapter without replacing its validation rules.
  - _Requirements: 5-10, 13-14, 17, 20, 27-28_

- [x] 8. Add project and API documentation
  - Document architecture, data ownership, concurrency, tools, security and operations.
  - Add OpenAPI, AsyncAPI, run commands, examples, limitations and roadmap.
  - _Requirements: 1, 15, 17, 22-28_

- [x] 9. Implement the operational web dashboard
  - Add a responsive static application served by Nginx.
  - Add Task creation, authenticated SSE, event timeline and cancellation.
  - Add agent cards, dependencies, evidence and Final Result views.
  - Add session-scoped credentials and safe text-only rendering of untrusted content.
  - Integrate the frontend with Docker Compose at port 3001.
  - _Requirements: 1-2, 4-8, 12, 14-15, 20, 23-24, 29_

- [x] 10. Complete final validation
  - Run formatting, vet, unit tests and race detector for every module.
  - Build every Docker image and validate Compose configuration.
  - _Requirements: 27-28_

- [x] 11. Execute the end-to-end demonstration
  - Start the complete Docker stack.
  - Submit an HTTP 500 investigation and observe terminal SSE.
  - Verify results from all four agent capabilities.
  - Scale Agent Services and verify healthy replicas and continued processing.
  - _Requirements: 5-16, 22-27_
