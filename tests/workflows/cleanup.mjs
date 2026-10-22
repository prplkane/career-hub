import assert from "node:assert/strict";

// Bound both graceful shutdown and the wait after terminating an owned process.
export async function stopOwnedService(child, exited, { graceMs = 10_000, killMs = 2000 } = {}) {
  let gracefulTimer, killTimer, forced = false;
  const deadline = new Promise((_, reject) => {
    gracefulTimer = setTimeout(() => {
      forced = true;
      try { child.kill(); } catch (cause) {
        reject(new Error("fixture cleanup: native termination failed", { cause }));
        return;
      }
      killTimer = setTimeout(() => reject(new Error(
        "fixture cleanup: native service did not exit after forced termination",
      )), killMs);
    }, graceMs);
  });
  try {
    child.stdin.end();
    const code = await Promise.race([exited, deadline]);
    assert.equal(forced, false, "fixture cleanup: native service required forced termination");
    assert.equal(code, 0, `fixture cleanup: native service exit ${code}`);
  } finally {
    clearTimeout(gracefulTimer);
    clearTimeout(killTimer);
  }
}
