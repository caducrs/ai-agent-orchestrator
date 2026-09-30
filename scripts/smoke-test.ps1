param(
    [string]$BaseUrl = "http://localhost:8080",
    [string]$Token = "dev-token",
    [int]$TimeoutSeconds = 180
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
$headers = @{ Authorization = "Bearer $Token" }
$health = Invoke-RestMethod -Uri "$BaseUrl/api/v1/health" -TimeoutSec 10
if (-not $health.ready) { throw "API is not ready" }

$createHeaders = @{
    Authorization = "Bearer $Token"
    "Content-Type" = "application/json"
    "Idempotency-Key" = "smoke-$([DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds())"
}
$body = @{ description = "Analise por que minha aplicação está retornando HTTP 500." } | ConvertTo-Json
$created = Invoke-RestMethod -Method Post -Uri "$BaseUrl/api/v1/tasks" -Headers $createHeaders -Body $body -TimeoutSec 20
$taskId = $created.task.id
if (-not $taskId) { throw "Task ID was not returned" }

$terminal = @("TASK_STATUS_COMPLETED", "TASK_STATUS_PARTIALLY_COMPLETED", "TASK_STATUS_FAILED", "TASK_STATUS_CANCELLED")
$deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
do {
    Start-Sleep -Seconds 1
    $taskResponse = Invoke-RestMethod -Uri "$BaseUrl/api/v1/tasks/$taskId" -Headers $headers -TimeoutSec 10
    $status = $taskResponse.task.status
    Write-Host "task=$taskId status=$status"
    if ($terminal -contains $status) { break }
} while ([DateTime]::UtcNow -lt $deadline)

if (-not ($terminal -contains $status)) { throw "Task did not reach a terminal state" }
if ($status -ne "TASK_STATUS_COMPLETED" -and $status -ne "TASK_STATUS_PARTIALLY_COMPLETED") { throw "Task failed with status $status" }
if ($taskResponse.task.subtasks.Count -ne 4) { throw "Expected four subtasks" }
$successful = @($taskResponse.task.subtasks | Where-Object { $_.status -eq "SUBTASK_STATUS_SUCCEEDED" })
if ($successful.Count -ne 4) { throw "Expected all four agents to succeed" }

$events = Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/api/v1/tasks/$taskId/events" -Headers $headers -TimeoutSec 30
if ($events.Content -notmatch "task.completed") { throw "Terminal SSE event was not replayed" }

$result = [ordered]@{
    task_id = $taskId
    status = $status
    subtasks = $taskResponse.task.subtasks.Count
    evidence = $taskResponse.task.final_result.evidence.Count
    sse_terminal = $true
}
$result | ConvertTo-Json
