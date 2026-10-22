import assert from "node:assert/strict";
import test from "node:test";

// ADR SEC-250: inside the desktop app the window token comes from the Electron
// host through the preload, and /api/session supplies capabilities only.
test("desktop session uses the preload token and reads only capabilities over HTTP", async (t) => {
  const hostToken = "A".repeat(43);
  const calls = [];
  const previous = [globalThis.window, globalThis.fetch];
  globalThis.window = { careerHubDesktop: { session: { token: async () => hostToken } } };
  globalThis.fetch = async (url, options = {}) => {
    calls.push({ url, header: new Headers(options.headers || {}).get("X-Career-Hub-Token") });
    // A response that also contains some other token must not replace the host's.
    return new Response(JSON.stringify({ token: "B".repeat(43), client: "browser", capabilities: { material_integrity: 1 } }), {
      status: 200, headers: { "Content-Type": "application/json" },
    });
  };
  t.after(() => { [globalThis.window, globalThis.fetch] = previous; });

  const { CareerHubApiClient } = await import("../src/intake_ui_static/ui/api.js");
  const client = new CareerHubApiClient();
  assert.equal(await client.sessionToken(), hostToken);
  assert.deepEqual(client.capabilities, { material_integrity: 1 });
  assert.deepEqual(calls, [{ url: "/api/session", header: hostToken }]);
});

test("a desktop host without a ready token fails closed instead of asking HTTP", async (t) => {
  const calls = [];
  const previous = [globalThis.window, globalThis.fetch];
  globalThis.window = { careerHubDesktop: { session: { token: async () => null } } };
  globalThis.fetch = async (url) => { calls.push(url); throw new Error("must not be called"); };
  t.after(() => { [globalThis.window, globalThis.fetch] = previous; });

  const { CareerHubApiClient } = await import("../src/intake_ui_static/ui/api.js");
  const client = new CareerHubApiClient();
  await assert.rejects(client.sessionToken());
  assert.deepEqual(calls, []);
  assert.deepEqual(client.capabilities, {});
});
