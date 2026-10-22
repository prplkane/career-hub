export const DIAGNOSTIC_PAGES_PER_POLL = 4;

const CURSOR_RESET_CODES = new Set([
  "DIAGNOSTIC_CURSOR_EXPIRED",
  "DIAGNOSTIC_CURSOR_INVALID",
  "EMAIL_DIAGNOSTIC_CURSOR_EXPIRED",
  "EMAIL_DIAGNOSTIC_CURSOR_INVALID",
  "INTAKE_DIAGNOSTIC_CURSOR_EXPIRED",
  "INTAKE_DIAGNOSTIC_CURSOR_INVALID",
]);

const PAUSE_CODES = new Set([
  ...CURSOR_RESET_CODES,
  "DIAGNOSTIC_LEGACY_CORRUPT",
  "DIAGNOSTIC_LEGACY_LINE_TOO_LARGE",
  "DIAGNOSTIC_LEGACY_LIMIT",
  "DIAGNOSTIC_HOME_CHANGED",
  "DIAGNOSTIC_OWNERSHIP_REQUIRED",
  "EMAIL_DIAGNOSTIC_HOME_CHANGED",
  "EMAIL_DIAGNOSTIC_OWNERSHIP_REQUIRED",
  "EMAIL_DIAGNOSTIC_RECOVERY_REQUIRED",
  "EMAIL_DIAGNOSTIC_LIMIT",
  "INTAKE_DIAGNOSTIC_HOME_CHANGED",
  "INTAKE_DIAGNOSTIC_OWNERSHIP_REQUIRED",
  "INTAKE_DIAGNOSTIC_DIRECTORY_INVALID",
  "INTAKE_DIAGNOSTIC_RECOVERY_REQUIRED",
  "DIAGNOSTIC_PAGE_INVALID",
  "DIAGNOSTIC_CURSOR_STALLED",
]);

function pageError(code) {
  const error = new Error(code);
  error.code = code;
  return error;
}

// Python returns one unpaged response without has_more; Go returns bounded
// pages. Do not advance the cursor on a malformed or non-progressing page.
export function parseDiagnosticPage(payload, previousCursor) {
  const cursor = payload?.cursor;
  const validCursor = typeof cursor === "string"
    ? cursor.length > 0 && cursor.length <= 128
    : Number.isSafeInteger(cursor) && cursor >= 0;
  if (!validCursor || !Array.isArray(payload?.events)
    || (payload.has_more !== undefined && typeof payload.has_more !== "boolean")) {
    throw pageError("DIAGNOSTIC_PAGE_INVALID");
  }
  const hasMore = payload.has_more === true;
  if ((hasMore || payload.events.length > 0) && String(cursor) === String(previousCursor)) {
    throw pageError("DIAGNOSTIC_CURSOR_STALLED");
  }
  return { cursor, events: payload.events, hasMore };
}

export function classifyDiagnosticReadFailure(error) {
  const code = String(error?.payload?.error_code || error?.code || "");
  return {
    code,
    paused: PAUSE_CODES.has(code),
    resetCursor: CURSOR_RESET_CODES.has(code),
  };
}
