// Diagnostics view: run selector, event log rendering and the log polling loop.
import { elements, requestJson, state } from "../app_context.js";
import { CLIENT_DIAGNOSTIC_EVENT } from "../ui/client_diagnostics.js";
import { DIAGNOSTIC_PAGES_PER_POLL, classifyDiagnosticReadFailure, parseDiagnosticPage } from "../ui/diagnostics_paging.js";
import {
  escapeHtml,
  formatRequestError,
  formatTime,
  statusClass,
} from "../ui/primitives.js";

// Bootstrap hooks supplied by app.js through initDiagnostics().
let navigateToView = null;

const MAX_LOG_EVENTS = 1_000;
let diagnosticsPollTimer = null;
let diagnosticsPollInFlight = false;
let diagnosticsPollPaused = false;
let diagnosticsRetryAfter = 0;

function groupedRuns() {
  const runs = new Map();
  for (const event of state.logEvents) {
    if (!runs.has(event.run_id)) runs.set(event.run_id, []);
    runs.get(event.run_id).push(event);
  }
  return [...runs.entries()].reverse();
}

function mergeDiagnosticEvents(events) {
  if (!Array.isArray(events) || !events.length) return;
  const eventKeys = new Set(state.logEvents.map((event) => (
    `${event.run_id}:${event.source}:${event.stage}:${event.sequence}`
  )));
  const additions = events.filter((event) => {
    if (!event?.run_id || !event?.source || !event?.stage) return false;
    const key = `${event.run_id}:${event.source}:${event.stage}:${event.sequence}`;
    if (eventKeys.has(key)) return false;
    eventKeys.add(key);
    return true;
  });
  if (!additions.length) return;
  state.logEvents = [...state.logEvents, ...additions]
    .sort((left, right) => String(left.timestamp).localeCompare(String(right.timestamp)))
    .slice(-MAX_LOG_EVENTS);
}

function renderRunSelector() {
  const runs = groupedRuns();
  if (!state.selectedRunId && runs.length) state.selectedRunId = runs[0][0];
  elements.runSelect.innerHTML = runs.length
    ? runs.map(([runId, events]) => {
      const first = events[0];
      return `<option value="${escapeHtml(runId)}">${escapeHtml(first.source)} - ${formatTime(first.timestamp)}</option>`;
    }).join("")
    : '<option value="">No diagnostic runs</option>';
  if (state.selectedRunId) elements.runSelect.value = state.selectedRunId;
}

export function renderDiagnostics() {
  renderRunSelector();
  const events = state.logEvents.filter((event) => event.run_id === state.selectedRunId);
  if (!events.length) {
    elements.runSummary.textContent = "No diagnostic run selected.";
    elements.diagnosticEvents.innerHTML = '<tr><td colspan="5" class="empty-state">No events.</td></tr>';
    return;
  }
  const finalEvent = [...events].reverse().find((event) => (
    ["completed", "failed"].includes(event.event)
      && ["run", "review_run", "card_suggestion_run", "review_ui_save", "approval_apply_run", "source_upload", "job_intake_run", "vault_editor_run", "email_connection_run", "email_connection_client", "email_sync_run"].includes(event.stage)
  ));
  const attentionEvent = [...events].reverse().find((event) => (
    ["error", "warning", "success_with_warnings"].includes(event.status)
      && event.details?.error_code
  ));
  const total = finalEvent?.details?.total_duration_ms;
  const durationLabel = Number.isFinite(total)
    ? `${total.toFixed(1)} ms`
    : (finalEvent ? "duration n/a" : "in progress");
  const attention = attentionEvent ? `
    <div class="diagnostic-failure${attentionEvent.status === "error" ? "" : " is-warning"}">
      <strong>${escapeHtml(attentionEvent.details?.error_code || "UNEXPECTED_ERROR")}</strong>
      <span>${escapeHtml(attentionEvent.details?.summary || "The operation needs attention.")}</span>
      ${attentionEvent.details?.error_message ? `<span>Details: ${escapeHtml(attentionEvent.details.error_message)}</span>` : ""}
      ${attentionEvent.details?.suggestion ? `<span>Next: ${escapeHtml(attentionEvent.details.suggestion)}</span>` : ""}
      <span>Run: ${escapeHtml(attentionEvent.run_id)}</span>
    </div>` : "";
  elements.runSummary.innerHTML = `
    <div><strong>${escapeHtml(events[0].source)}</strong>
    <span> / ${events.length} events / ${durationLabel}</span>
    ${finalEvent ? ` / <span class="${statusClass(finalEvent.status)}">${escapeHtml(finalEvent.status)}</span>` : ""}</div>
    ${attention}`;
  elements.diagnosticEvents.innerHTML = events.map((event) => {
    const duration = event.details?.duration_ms;
    const details = { ...event.details };
    delete details.duration_ms;
    return `
      <tr>
        <td>${escapeHtml(event.stage)}</td>
        <td>${escapeHtml(event.event)}</td>
        <td class="status-text ${statusClass(event.status)}">${escapeHtml(event.status)}</td>
        <td>${Number.isFinite(duration) ? `${duration.toFixed(1)} ms` : "-"}</td>
        <td><pre class="details-code">${escapeHtml(JSON.stringify(details, null, 2))}</pre></td>
      </tr>`;
  }).join("");
}

function updateLogBar(event) {
  if (!event) return;
  const duration = event.details?.duration_ms;
  const visualStatus = event.status === "success_with_warnings" ? "warning" : event.status;
  elements.statusDot.className = `status-dot ${visualStatus}`;
  elements.logStage.textContent = `${event.stage} / ${event.event}`;
  elements.logStatus.textContent = event.status;
  elements.logDuration.textContent = Number.isFinite(duration) ? `${duration.toFixed(1)} ms` : "";
  elements.logRun.textContent = event.run_id;
}

let diagnosticsResetOnRetry = false;

function showDiagnosticsHistory(message = "", retryLabel = "") {
  elements.diagnosticsHistory.hidden = !message;
  elements.diagnosticsHistoryStatus.textContent = message;
  elements.diagnosticsHistoryRetry.hidden = !retryLabel;
  elements.diagnosticsHistoryRetry.textContent = retryLabel;
}

export async function pollLogs() {
  if (state.activeView !== "diagnostics" || diagnosticsPollInFlight || diagnosticsPollPaused
    || Date.now() < diagnosticsRetryAfter) return;
  diagnosticsPollInFlight = true;
  let changed = false;
  try {
    let hasMore = false;
    for (let pageNumber = 0; pageNumber < DIAGNOSTIC_PAGES_PER_POLL; pageNumber += 1) {
      const payload = await requestJson(`/api/logs?after=${encodeURIComponent(state.logCursor)}`);
      if (state.activeView !== "diagnostics") return;
      const page = parseDiagnosticPage(payload, state.logCursor);
      state.logCursor = page.cursor;
      hasMore = page.hasMore;
      if (page.events.length) {
        mergeDiagnosticEvents(page.events);
        if (!state.logSelectionPinned) {
          state.selectedRunId = state.logEvents.at(-1)?.run_id || null;
        }
        updateLogBar(page.events.at(-1));
        changed = true;
      }
      if (!hasMore) break;
    }
    if (!hasMore && state.logSelectionPinned
      && !state.logEvents.some((event) => event.run_id === state.selectedRunId)) {
      state.logSelectionPinned = false;
      state.selectedRunId = state.logEvents.at(-1)?.run_id || null;
      changed = true;
    }
    diagnosticsRetryAfter = 0;
    diagnosticsResetOnRetry = false;
    showDiagnosticsHistory(hasMore ? "Reading saved diagnostic history…" : "");
  } catch (error) {
    if (state.activeView !== "diagnostics") return;
    const failure = classifyDiagnosticReadFailure(error);
    diagnosticsPollPaused = failure.paused;
    diagnosticsResetOnRetry = failure.resetCursor;
    diagnosticsRetryAfter = failure.paused ? 0 : Date.now() + 3000;
    elements.statusDot.className = "status-dot error";
    elements.logStage.textContent = "diagnostics read failed";
    elements.logStatus.textContent = failure.code || "read failed";
    showDiagnosticsHistory(
      formatRequestError(error),
      failure.paused ? (failure.resetCursor ? "Read from beginning" : "Retry reading") : "",
    );
  } finally {
    if (changed && state.activeView === "diagnostics") renderDiagnostics();
    diagnosticsPollInFlight = false;
  }
}

export function startDiagnosticsPolling() {
  if (diagnosticsPollTimer !== null) return;
  if (!diagnosticsPollPaused && state.logCursor === 0 && !state.logEvents.length) {
    showDiagnosticsHistory("Reading saved diagnostic history…");
  }
  void pollLogs();
  diagnosticsPollTimer = window.setInterval(() => {
    void pollLogs();
  }, 750);
}

export function stopDiagnosticsPolling() {
  if (diagnosticsPollTimer === null) return;
  window.clearInterval(diagnosticsPollTimer);
  diagnosticsPollTimer = null;
}

export function initDiagnostics(deps = {}) {
  ({ navigateToView } = deps);
  elements.diagnosticsHistoryRetry.addEventListener("click", () => {
    if (diagnosticsResetOnRetry) {
      state.logCursor = 0;
      state.logEvents = [];
      state.selectedRunId = null;
      state.logSelectionPinned = false;
      renderDiagnostics();
    }
    diagnosticsPollPaused = false;
    diagnosticsRetryAfter = 0;
    diagnosticsResetOnRetry = false;
    showDiagnosticsHistory("Reading saved diagnostic history…");
    void pollLogs();
  });
  window.addEventListener("careerhub:opendiagnostics", (event) => {
    const runId = String(event.detail?.runId || "").trim();
    if (runId) {
      state.selectedRunId = runId;
      state.logSelectionPinned = true;
    }
    void navigateToView("diagnostics");
  });
  window.addEventListener(CLIENT_DIAGNOSTIC_EVENT, (event) => {
    const events = event.detail?.events;
    mergeDiagnosticEvents(events);
    const runId = String(events?.[0]?.run_id || "").trim();
    if (runId) {
      state.selectedRunId = runId;
      state.logSelectionPinned = true;
    }
    if (state.activeView === "diagnostics") renderDiagnostics();
  });
  elements.runSelect.addEventListener("change", (event) => {
    state.selectedRunId = event.target.value;
    state.logSelectionPinned = true;
    renderDiagnostics();
  });
}
