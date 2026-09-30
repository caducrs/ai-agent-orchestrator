# API v1

Base URL: `http://localhost:8080/api/v1`

Local authorization:

```text
Authorization: Bearer dev-token
```

## Create Task

`POST /tasks`

Headers:

```text
Content-Type: application/json
Idempotency-Key: client-generated-key
```

Body:

```json
{"description":"Analise por que minha aplicação está retornando HTTP 500."}
```

Returns `202 Accepted`. Reusing the same key and payload returns the original Task. Reusing the key with another payload returns `409`.

## Get Task

`GET /tasks/{task_id}`

Returns Task status, Subtasks, Attempts represented by each Subtask, agent results and final result when available.

## Cancel Task

`POST /tasks/{task_id}/cancel`

Cancellation is idempotent for a cancelled Task. A different terminal state returns `409`.

## Watch Events

`GET /tasks/{task_id}/events`

Response type: `text/event-stream`.

Resume with:

```text
Last-Event-ID: 12
```

Example event:

```text
id: 13
event: subtask.completed
data: {"subtask_id":"...","status":"SUCCEEDED"}
```

The stream sends heartbeats and closes after the terminal event.

## List Agents

`GET /agents`

Returns only public identifiers, type, version, capabilities and status.

## Health

`GET /health`

Returns `200` when the Orchestrator database is ready and `503` otherwise.

## Errors

```json
{
  "code": "INVALID_REQUEST",
  "message": "invalid JSON request",
  "request_id": "..."
}
```

Common statuses: `400`, `401`, `403`, `404`, `409`, `413`, `415`, `429`, `503`, `504`.
