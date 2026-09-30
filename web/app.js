const $ = (id) => document.getElementById(id)

const agentCatalog = {
  code: { name: "Code Agent", short: "</>", subtitle: "Source & dependencies" },
  logs: { name: "Log Agent", short: "LOG", subtitle: "Errors & patterns" },
  database: { name: "Database Agent", short: "DB", subtitle: "Schema & connectivity" },
  infrastructure: { name: "Infrastructure Agent", short: "INF", subtitle: "Health & metrics" }
}

const terminalStatuses = new Set([
  "TASK_STATUS_COMPLETED",
  "TASK_STATUS_PARTIALLY_COMPLETED",
  "TASK_STATUS_FAILED",
  "TASK_STATUS_CANCELLED"
])

const state = {
  token: sessionStorage.getItem("agentops.token") || "dev-token",
  task: null,
  agents: [],
  events: [],
  eventIds: new Set(),
  recent: readRecent(),
  streamController: null,
  streamGeneration: 0,
  refreshTimer: null,
  health: null
}

function element(tag, className, text) {
  const node = document.createElement(tag)
  if (className) node.className = className
  if (text !== undefined) node.textContent = text
  return node
}

function readRecent() {
  try {
    const value = JSON.parse(localStorage.getItem("agentops.recent") || "[]")
    return Array.isArray(value) ? value.slice(0, 8) : []
  } catch {
    return []
  }
}

function saveRecent() {
  localStorage.setItem("agentops.recent", JSON.stringify(state.recent.slice(0, 8)))
}

function statusName(value) {
  return String(value || "IDLE")
    .replace("TASK_STATUS_", "")
    .replace("SUBTASK_STATUS_", "")
    .replaceAll("_", " ")
}

function statusClass(value) {
  return statusName(value).toLowerCase().replaceAll(" ", "-")
}

function isTerminal(value) {
  return terminalStatuses.has(value)
}

function authHeaders(extra = {}) {
  return { Authorization: `Bearer ${state.token}`, ...extra }
}

async function api(path, options = {}) {
  const response = await fetch(path, { ...options, headers: authHeaders(options.headers || {}) })
  const text = await response.text()
  let body = null
  if (text) {
    try {
      body = JSON.parse(text)
    } catch {
      body = { message: text }
    }
  }
  if (!response.ok) {
    const error = new Error(body?.message || `Request failed with HTTP ${response.status}`)
    error.status = response.status
    error.code = body?.code || "REQUEST_FAILED"
    throw error
  }
  return body
}

async function loadHealth() {
  try {
    const health = await api("/api/v1/health")
    state.health = health
    const ready = Boolean(health.ready)
    $("health-badge").className = `health-badge ${ready ? "ready" : "error"}`
    $("health-badge").querySelector("b").textContent = ready ? "Platform ready" : "Platform degraded"
    $("metric-platform").textContent = ready ? "HEALTHY" : "DEGRADED"
    $("metric-platform-trend").textContent = health.version || "unknown"
    $("metric-platform-trend").className = `metric-trend ${ready ? "positive" : "negative"}`
    $("connection-dot").className = `pulse-dot ${ready ? "online" : "offline"}`
    $("connection-label").textContent = ready ? "Platform online" : "Platform degraded"
    $("topology-api").className = ready ? "online" : ""
    $("topology-core").className = ready ? "online" : ""
  } catch (error) {
    state.health = null
    $("health-badge").className = "health-badge error"
    $("health-badge").querySelector("b").textContent = "Platform offline"
    $("metric-platform").textContent = "OFFLINE"
    $("metric-platform-trend").textContent = error.code || "unreachable"
    $("metric-platform-trend").className = "metric-trend negative"
    $("connection-dot").className = "pulse-dot offline"
    $("connection-label").textContent = "Platform offline"
    $("topology-api").className = ""
    $("topology-core").className = ""
  }
}

async function loadAgents() {
  try {
    const response = await api("/api/v1/agents")
    state.agents = response?.agents || []
  } catch {
    state.agents = []
  }
  renderAgents()
  const online = state.agents.filter((agent) => agent.status === "HEALTHY").length
  $("metric-agents").textContent = `${online} / 4`
}

function renderRecent() {
  const container = $("recent-tasks")
  container.replaceChildren()
  if (state.recent.length === 0) {
    container.append(element("div", "empty-compact", "Nenhuma Task recente"))
    return
  }
  for (const item of state.recent) {
    const button = element("button", `recent-task ${state.task?.id === item.id ? "active" : ""}`)
    button.type = "button"
    button.addEventListener("click", () => selectTask(item.id))
    const dot = element("i", recentStatusClass(item.status))
    const copy = element("span")
    copy.append(element("span", "", item.description || "Investigation"))
    copy.append(element("small", "", compactID(item.id)))
    button.append(dot, copy)
    container.append(button)
  }
}

function recentStatusClass(status) {
  if (status === "TASK_STATUS_COMPLETED" || status === "TASK_STATUS_PARTIALLY_COMPLETED") return "complete"
  if (status && status !== "TASK_STATUS_QUEUED") return "running"
  return ""
}

function upsertRecent(task) {
  if (!task?.id) return
  state.recent = state.recent.filter((item) => item.id !== task.id)
  state.recent.unshift({ id: task.id, description: task.description, status: task.status, createdAt: task.created_at })
  state.recent = state.recent.slice(0, 8)
  saveRecent()
  renderRecent()
}

async function createTask(event) {
  event.preventDefault()
  const description = $("task-description").value.trim()
  if (!description) {
    toast("Describe the investigation before launching it.", "error")
    return
  }
  const button = $("submit-task")
  button.disabled = true
  button.querySelector("span").textContent = "Launching..."
  try {
    const response = await api("/api/v1/tasks", {
      method: "POST",
      headers: { "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() },
      body: JSON.stringify({ description })
    })
    if (!response?.task?.id) throw new Error("API did not return a Task ID")
    toast("Investigation accepted by the Orchestrator.", "success")
    await selectTask(response.task.id, response.task)
  } catch (error) {
    toast(`${error.code || "CREATE_FAILED"}: ${error.message}`, "error")
  } finally {
    button.disabled = false
    button.querySelector("span").textContent = "Launch investigation"
  }
}

async function selectTask(taskId, initialTask = null) {
  if (!taskId) return
  disconnectStream()
  state.events = []
  state.eventIds.clear()
  state.task = initialTask
  location.hash = `task=${encodeURIComponent(taskId)}`
  renderTask()
  renderTimeline()
  try {
    await refreshTask(taskId)
    connectEventStream(taskId)
  } catch (error) {
    toast(`${error.code || "LOAD_FAILED"}: ${error.message}`, "error")
  }
}

async function refreshTask(taskId = state.task?.id) {
  if (!taskId) return
  const response = await api(`/api/v1/tasks/${encodeURIComponent(taskId)}`)
  if (!response?.task) throw new Error("Task payload is missing")
  state.task = response.task
  upsertRecent(state.task)
  renderTask()
}

function scheduleRefresh() {
  clearTimeout(state.refreshTimer)
  state.refreshTimer = setTimeout(() => refreshTask().catch((error) => toast(error.message, "error")), 100)
}

function disconnectStream() {
  state.streamGeneration += 1
  if (state.streamController) state.streamController.abort()
  state.streamController = null
  setStreamState("offline")
}

async function connectEventStream(taskId) {
  const generation = ++state.streamGeneration
  let retry = 500
  while (generation === state.streamGeneration && state.task?.id === taskId) {
    const controller = new AbortController()
    state.streamController = controller
    try {
      setStreamState("live")
      const lastEvent = state.events.at(-1)?.id || 0
      const headers = authHeaders({ Accept: "text/event-stream" })
      if (lastEvent) headers["Last-Event-ID"] = String(lastEvent)
      const response = await fetch(`/api/v1/tasks/${encodeURIComponent(taskId)}/events`, { headers, signal: controller.signal })
      if (!response.ok || !response.body) {
        const text = await response.text()
        throw new Error(text || `Event stream returned HTTP ${response.status}`)
      }
      await consumeSSE(response.body, generation)
      if (state.task && isTerminal(state.task.status)) {
        setStreamState("complete")
        return
      }
    } catch (error) {
      if (controller.signal.aborted || generation !== state.streamGeneration) return
      setStreamState("reconnecting")
      await delay(retry)
      retry = Math.min(retry * 2, 5000)
      continue
    }
    await delay(250)
  }
}

async function consumeSSE(body, generation) {
  const reader = body.getReader()
  const decoder = new TextDecoder()
  let buffer = ""
  while (generation === state.streamGeneration) {
    const { value, done } = await reader.read()
    buffer += decoder.decode(value || new Uint8Array(), { stream: !done }).replaceAll("\r\n", "\n")
    let boundary = buffer.indexOf("\n\n")
    while (boundary >= 0) {
      const block = buffer.slice(0, boundary)
      buffer = buffer.slice(boundary + 2)
      processSSEBlock(block)
      boundary = buffer.indexOf("\n\n")
    }
    if (done) return
  }
}

function processSSEBlock(block) {
  if (!block || block.startsWith(":")) return
  let id = 0
  let type = "message"
  const data = []
  for (const line of block.split("\n")) {
    if (line.startsWith("id:")) id = Number(line.slice(3).trim())
    if (line.startsWith("event:")) type = line.slice(6).trim()
    if (line.startsWith("data:")) data.push(line.slice(5).trimStart())
  }
  if (!id || state.eventIds.has(id)) return
  let payload = {}
  try {
    payload = JSON.parse(data.join("\n") || "{}")
  } catch {
    payload = { raw: data.join("\n") }
  }
  state.eventIds.add(id)
  state.events.push({ id, type, payload, receivedAt: new Date() })
  state.events.sort((left, right) => left.id - right.id)
  renderTimeline()
  renderMetrics()
  scheduleRefresh()
}

function renderTask() {
  const task = state.task
  if (!task) {
    $("task-title").textContent = "No task selected"
    $("task-description-display").textContent = "Launch an investigation to see the orchestration flow."
    $("task-status-badge").textContent = "IDLE"
    $("task-status-badge").className = "status-badge idle"
    $("task-progress").style.width = "0%"
    $("copy-task-id").textContent = "—"
    $("copy-task-id").disabled = true
    $("refresh-task").disabled = true
    $("cancel-task").disabled = true
    $("result").classList.add("hidden")
    renderAgents()
    renderMetrics()
    return
  }
  const displayStatus = statusName(task.status)
  $("task-title").textContent = `${displayStatus.toLowerCase().replace(/\b\w/g, (value) => value.toUpperCase())} investigation`
  $("task-description-display").textContent = task.description || "—"
  $("task-status-badge").textContent = displayStatus
  $("task-status-badge").className = `status-badge ${statusClass(task.status)}`
  $("task-progress").style.width = `${progressFor(task)}%`
  $("copy-task-id").textContent = task.id
  $("copy-task-id").disabled = false
  $("task-started").textContent = formatDate(task.created_at)
  $("task-deadline").textContent = formatDate(task.deadline_at)
  $("refresh-task").disabled = false
  $("cancel-task").disabled = isTerminal(task.status)
  renderAgents()
  renderResult()
  renderMetrics()
}

function progressFor(task) {
  if (!task) return 0
  if (isTerminal(task.status)) return 100
  const base = {
    TASK_STATUS_QUEUED: 6,
    TASK_STATUS_PLANNING: 18,
    TASK_STATUS_RUNNING: 35,
    TASK_STATUS_AGGREGATING: 90
  }[task.status] || 0
  const subtasks = task.subtasks || []
  if (task.status !== "TASK_STATUS_RUNNING" || subtasks.length === 0) return base
  const terminal = subtasks.filter((item) => ["SUBTASK_STATUS_SUCCEEDED", "SUBTASK_STATUS_FAILED", "SUBTASK_STATUS_SKIPPED", "SUBTASK_STATUS_CANCELLED"].includes(item.status)).length
  return Math.min(86, 35 + Math.round((terminal / subtasks.length) * 50))
}

function renderMetrics() {
  const task = state.task
  $("metric-status").textContent = statusName(task?.status)
  const evidence = (task?.subtasks || []).reduce((count, subtask) => count + (subtask.result?.evidence?.length || 0), 0)
  $("metric-evidence").textContent = String(evidence)
  $("metric-events").textContent = `${state.events.length} events`
  if (!task?.created_at) {
    $("metric-elapsed").textContent = "00:00"
    return
  }
  const end = isTerminal(task.status) && task.updated_at ? new Date(task.updated_at) : new Date()
  const seconds = Math.max(0, Math.floor((end - new Date(task.created_at)) / 1000))
  $("metric-elapsed").textContent = `${String(Math.floor(seconds / 60)).padStart(2, "0")}:${String(seconds % 60).padStart(2, "0")}`
}

function renderAgents() {
  const grid = $("agent-grid")
  grid.replaceChildren()
  for (const capability of Object.keys(agentCatalog)) {
    const descriptor = state.agents.find((agent) => agent.type === capability)
    const subtask = state.task?.subtasks?.find((item) => item.capability === capability)
    grid.append(agentCard(capability, descriptor, subtask))
  }
}

function agentCard(capability, descriptor, subtask) {
  const metadata = agentCatalog[capability]
  const status = subtask?.status || "SUBTASK_STATUS_PENDING"
  const cssStatus = statusClass(status)
  const card = element("article", `agent-card ${cssStatus}`)
  const header = element("header", "agent-header")
  const identity = element("div", "agent-identity")
  const avatar = element("span", "agent-avatar", metadata.short)
  const names = element("div")
  names.append(element("strong", "", metadata.name), element("small", "", descriptor ? `${metadata.subtitle} · v${descriptor.version}` : metadata.subtitle))
  identity.append(avatar, names)
  header.append(identity, element("span", "agent-status", statusName(status)))

  const objective = element("p", "agent-objective", subtask?.description || "Waiting for the Orchestrator to assign work.")
  const result = element("div", "agent-result")
  result.append(element("p", "", subtask?.result?.summary || agentPlaceholder(status)))
  const meta = element("div", "agent-meta")
  const dependencies = subtask?.depends_on?.length || 0
  const evidence = subtask?.result?.evidence?.length || 0
  meta.append(element("span", "", `attempt ${subtask?.attempt || 0}`), element("span", "", `${evidence} evidence`), element("span", "", `${dependencies} deps`))
  result.append(meta)
  card.append(header, objective, result)
  card.title = subtask?.error_code || subtask?.result?.warnings?.join(" · ") || metadata.name
  return card
}

function agentPlaceholder(status) {
  if (status === "SUBTASK_STATUS_RUNNING") return "Tool execution is in progress..."
  if (status === "SUBTASK_STATUS_BLOCKED") return "Waiting for required dependencies."
  if (status === "SUBTASK_STATUS_FAILED") return "The agent failed to produce a result."
  if (status === "SUBTASK_STATUS_CANCELLED") return "Execution was cancelled."
  return "No result available yet."
}

function renderTimeline() {
  const container = $("timeline")
  container.replaceChildren()
  if (state.events.length === 0) {
    const empty = element("div", "empty-state")
    empty.append(element("span", "", "◎"), element("strong", "", "Waiting for an execution"), element("small", "", "Events will appear here in real time."))
    container.append(empty)
    return
  }
  for (const event of state.events) {
    const typeClass = event.type.includes("completed") ? "complete" : event.type.includes("running") || event.type.includes("dispatched") || event.type.includes("planning") ? "running" : ""
    const item = element("div", `timeline-item ${typeClass}`)
    item.append(element("span", "timeline-index", String(event.id).padStart(2, "0")))
    const copy = element("div", "timeline-copy")
    copy.append(element("strong", "", humanEvent(event.type)), element("small", "", eventDetails(event)))
    item.append(copy, element("time", "timeline-time", formatTime(event.receivedAt)))
    container.append(item)
  }
  container.scrollTop = container.scrollHeight
}

function humanEvent(type) {
  const names = {
    "task.queued": "Task accepted",
    "task.planning": "Planner decomposing investigation",
    "task.running": "Parallel execution started",
    "subtask.dispatched": "Subtask dispatched",
    "subtask.completed": "Agent completed",
    "subtask.skipped": "Subtask skipped",
    "task.aggregating": "Aggregating grounded results",
    "task.completed": "Investigation completed",
    "task.failed": "Investigation failed",
    "task.cancelled": "Investigation cancelled"
  }
  return names[type] || type
}

function eventDetails(event) {
  const payload = event.payload || {}
  const parts = []
  if (payload.capability) parts.push(agentCatalog[payload.capability]?.name || payload.capability)
  if (payload.agent_type) parts.push(agentCatalog[payload.agent_type]?.name || payload.agent_type)
  if (payload.status) parts.push(statusName(payload.status))
  if (payload.subtask_id) parts.push(compactID(payload.subtask_id))
  if (payload.subtasks) parts.push(`${payload.subtasks} subtasks`)
  return parts.join(" · ") || "Orchestrator state transition"
}

function renderResult() {
  const task = state.task
  const result = task?.final_result
  if (!result || !isTerminal(task.status)) {
    $("result").classList.add("hidden")
    return
  }
  $("result").classList.remove("hidden")
  $("result-summary").textContent = result.summary || "No summary was generated."
  renderResultList($("result-conclusions"), result.conclusions || [], false, "No conclusions available")
  const limitations = [...(result.failures || []), ...(result.limitations || [])]
  renderResultList($("result-limitations"), limitations, true, "No failures or limitations")
  const evidenceContainer = $("result-evidence")
  evidenceContainer.replaceChildren()
  const evidence = result.evidence || []
  if (evidence.length === 0) {
    evidenceContainer.append(element("div", "empty-compact", "No evidence references available."))
  } else {
    for (const item of evidence) {
      const card = element("article", "evidence-card")
      const header = element("header")
      header.append(element("strong", "", item.source || "source"), element("code", "", item.reference || "reference unavailable"))
      card.append(header, element("p", "", item.content || "No content"))
      evidenceContainer.append(card)
    }
  }
}

function renderResultList(container, values, warning, emptyText) {
  container.replaceChildren()
  if (values.length === 0) {
    container.append(element("div", "empty-compact", emptyText))
    return
  }
  for (const value of values) {
    const row = element("div", `result-list-item ${warning ? "warning" : ""}`)
    row.append(element("i", "", warning ? "!" : "✓"), element("span", "", value))
    container.append(row)
  }
}

async function cancelTask() {
  if (!state.task || isTerminal(state.task.status)) return
  $("cancel-task").disabled = true
  try {
    const response = await api(`/api/v1/tasks/${encodeURIComponent(state.task.id)}/cancel`, { method: "POST" })
    state.task = response.task
    upsertRecent(state.task)
    renderTask()
    toast("Cancellation confirmed by the Orchestrator.", "success")
  } catch (error) {
    toast(`${error.code || "CANCEL_FAILED"}: ${error.message}`, "error")
  }
}

function setStreamState(value) {
  const node = $("stream-state")
  node.className = `stream-state ${value === "complete" ? "live" : value}`
  node.lastChild.textContent = value
}

function toast(message, type = "") {
  const node = element("div", `toast ${type}`, message)
  $("toast-region").append(node)
  setTimeout(() => node.remove(), 4500)
}

function compactID(value) {
  if (!value) return "—"
  return value.length > 13 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value
}

function formatDate(value) {
  if (!value) return "—"
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? "—" : date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })
}

function formatTime(value) {
  return value instanceof Date ? value.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" }) : "—"
}

function delay(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds))
}

function bindUI() {
  $("task-form").addEventListener("submit", createTask)
  $("task-description").addEventListener("keydown", (event) => {
    if ((event.ctrlKey || event.metaKey) && event.key === "Enter") {
      event.preventDefault()
      $("task-form").requestSubmit()
    }
  })
  document.querySelectorAll(".suggestion").forEach((button) => button.addEventListener("click", () => {
    $("task-description").value = button.dataset.prompt || ""
    $("task-description").focus()
  }))
  document.querySelectorAll(".nav-item").forEach((button) => button.addEventListener("click", () => {
    document.querySelectorAll(".nav-item").forEach((item) => item.classList.remove("active"))
    button.classList.add("active")
    $(button.dataset.scroll)?.scrollIntoView({ behavior: "smooth", block: "start" })
  }))
  $("new-task-button").addEventListener("click", () => {
    $("task-description").focus()
    $("overview").scrollIntoView({ behavior: "smooth" })
  })
  $("refresh-task").addEventListener("click", () => refreshTask().catch((error) => toast(error.message, "error")))
  $("cancel-task").addEventListener("click", cancelTask)
  $("copy-task-id").addEventListener("click", async () => {
    if (!state.task?.id) return
    await navigator.clipboard.writeText(state.task.id)
    toast("Task ID copied.", "success")
  })
  $("copy-result").addEventListener("click", async () => {
    const summary = state.task?.final_result?.summary
    if (!summary) return
    await navigator.clipboard.writeText(summary)
    toast("Result summary copied.", "success")
  })
  $("open-settings").addEventListener("click", () => {
    $("token-input").value = state.token
    $("settings-dialog").showModal()
  })
  $("save-token").addEventListener("click", () => {
    const token = $("token-input").value.trim()
    if (!token) return
    state.token = token
    sessionStorage.setItem("agentops.token", token)
    disconnectStream()
    Promise.all([loadHealth(), loadAgents()]).then(() => {
      if (state.task?.id) connectEventStream(state.task.id)
    })
    toast("Session token updated.", "success")
  })
}

async function initialize() {
  bindUI()
  renderRecent()
  renderTask()
  await Promise.all([loadHealth(), loadAgents()])
  const match = location.hash.match(/^#task=(.+)$/)
  if (match) selectTask(decodeURIComponent(match[1]))
  setInterval(loadHealth, 10000)
  setInterval(loadAgents, 30000)
  setInterval(renderMetrics, 1000)
}

initialize()
