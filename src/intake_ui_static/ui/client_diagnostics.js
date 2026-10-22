export const CLIENT_DIAGNOSTIC_EVENT = "careerhub:clientdiagnostic";

function diagnosticEvent({
  runId,
  sequence,
  timestamp,
  event,
  status,
  details,
}) {
  return {
    schema_version: 1,
    timestamp,
    run_id: runId,
    sequence,
    component: "career_hub_ui",
    source: "gmail_connection_ui",
    stage: "email_connection_client",
    event,
    status,
    details,
  };
}

export function reportEmailConnectionClientFailure({
  runId,
  startedAt,
  errorCode,
  summary,
  suggestion,
}) {
  if (!runId || !errorCode) return;
  const finishedAt = Date.now();
  const operation = "save";
  const boundary = "renderer";
  const events = [
    diagnosticEvent({
      runId,
      sequence: 1,
      timestamp: new Date(startedAt).toISOString(),
      event: "started",
      status: "running",
      details: { operation, boundary },
    }),
    diagnosticEvent({
      runId,
      sequence: 2,
      timestamp: new Date(finishedAt).toISOString(),
      event: "failed",
      status: "error",
      details: {
        operation,
        boundary,
        outcome: "unconfirmed",
        error_code: errorCode,
        summary,
        suggestion,
        total_duration_ms: Math.max(0, finishedAt - startedAt),
      },
    }),
  ];
  window.dispatchEvent(new CustomEvent(CLIENT_DIAGNOSTIC_EVENT, {
    detail: { events },
  }));
}
