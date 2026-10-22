const assert = require("node:assert/strict");

async function main() {
  const {
    DIAGNOSTIC_PAGES_PER_POLL,
    classifyDiagnosticReadFailure,
    parseDiagnosticPage,
  } = await import("../src/intake_ui_static/ui/diagnostics_paging.js");

  assert.equal(DIAGNOSTIC_PAGES_PER_POLL, 4);
  assert.deepEqual(
    parseDiagnosticPage({ cursor: "dl1:50:ne:000001:0", events: [{ run_id: "r1" }], has_more: true }, 0),
    { cursor: "dl1:50:ne:000001:0", events: [{ run_id: "r1" }], hasMore: true },
  );
  assert.deepEqual(
    parseDiagnosticPage({ cursor: "byte:50", events: [] }, "byte:0"),
    { cursor: "byte:50", events: [], hasMore: false },
  );
  for (const payload of [null, {}, { cursor: "x", events: null }, { cursor: "x", events: [], has_more: "yes" }]) {
    assert.throws(() => parseDiagnosticPage(payload, 0), { code: "DIAGNOSTIC_PAGE_INVALID" });
  }
  assert.throws(
    () => parseDiagnosticPage({ cursor: "dl1:50:ne:000001:0", events: [], has_more: true }, "dl1:50:ne:000001:0"),
    { code: "DIAGNOSTIC_CURSOR_STALLED" },
  );
  assert.throws(
    () => parseDiagnosticPage({ cursor: "byte:50", events: [{ run_id: "r1" }] }, "byte:50"),
    { code: "DIAGNOSTIC_CURSOR_STALLED" },
  );
  assert.deepEqual(
    classifyDiagnosticReadFailure({ payload: { error_code: "DIAGNOSTIC_CURSOR_EXPIRED" } }),
    { code: "DIAGNOSTIC_CURSOR_EXPIRED", paused: true, resetCursor: true },
  );
  assert.deepEqual(
    classifyDiagnosticReadFailure({ payload: { error_code: "DIAGNOSTIC_LEGACY_CORRUPT" } }),
    { code: "DIAGNOSTIC_LEGACY_CORRUPT", paused: true, resetCursor: false },
  );
  assert.deepEqual(
    classifyDiagnosticReadFailure({ payload: { error_code: "DIAGNOSTIC_LEGACY_CHANGED" } }),
    { code: "DIAGNOSTIC_LEGACY_CHANGED", paused: false, resetCursor: false },
  );
  assert.deepEqual(
    classifyDiagnosticReadFailure({ payload: { error_code: "INTAKE_DIAGNOSTIC_CURSOR_EXPIRED" } }),
    { code: "INTAKE_DIAGNOSTIC_CURSOR_EXPIRED", paused: true, resetCursor: true },
  );
  console.log("diagnostics paging contract passed");
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
