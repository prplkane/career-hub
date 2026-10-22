import assert from "node:assert/strict";
import test from "node:test";

import { CareerHubApiClient } from "../src/intake_ui_static/ui/api.js";
import {
  CLIENT_DIAGNOSTIC_EVENT,
  reportEmailConnectionClientFailure,
} from "../src/intake_ui_static/ui/client_diagnostics.js";
import { installDesktopSession } from "./helpers/desktop_session.mjs";

installDesktopSession("synthetic-session");

for (const reconnect of [false, true]) {
test(`a ${reconnect ? "reconnect" : "configure"} abort also bounds initial session acquisition`, async (t) => {
  const originalFetch = globalThis.fetch;
  t.after(() => { globalThis.fetch = originalFetch; });
  let sessionSignal;
  globalThis.fetch = (_url, options = {}) => {
    sessionSignal = options.signal;
    return new Promise((_resolve, reject) => {
      const abort = () => {
        const error = new Error("request aborted");
        error.name = "AbortError";
        reject(error);
      };
      // Like real fetch: an already-aborted signal rejects at once. The token
      // now arrives from the desktop host first, so fetch can start after abort.
      if (options.signal?.aborted) return abort();
      options.signal?.addEventListener("abort", abort, { once: true });
    });
  };
  const client = new CareerHubApiClient();
  const controller = new AbortController();
  const request = client.configureEmailConnection({
    accountAddress: "private@example.test",
    appPassword: "never-log-this",
    senderAddresses: ["jobs@example.test"],
    applicationSenderAddresses: [],
    scheduleEnabled: false,
    intervalMinutes: 30,
    expectedRevision: 0,
    reconnect,
  }, { signal: controller.signal });

  controller.abort();
  await assert.rejects(request, { name: "AbortError" });
  assert.equal(sessionSignal, controller.signal);
  assert.equal(client.sessionPromise, null);
});
}

test("reconnect uses only the explicit POST route and preserves the v2 save envelope", async (t) => {
  const originalFetch = globalThis.fetch;
  t.after(() => { globalThis.fetch = originalFetch; });
  const requests = [];
  globalThis.fetch = async (url, options) => {
    if (url === "/api/session") return Response.json({ token: "synthetic-session" });
    requests.push({ url, options });
    return Response.json({ revision: 8 });
  };
  const client = new CareerHubApiClient();
  const values = {
    accountAddress: "fixture@example.test", appPassword: "synthetic-password",
    senderAddresses: ["jobs@example.test"], applicationSenderAddresses: [],
    scheduleEnabled: false, intervalMinutes: 30, expectedRevision: 7,
  };
  const controller = new AbortController();
  const options = { signal: controller.signal, headers: { "X-Career-Hub-Diagnostic-Run": "synthetic-run" } };
  await client.configureEmailConnection(values, options);
  await client.configureEmailConnection({ ...values, reconnect: true }, options);
  assert.deepEqual(requests.map(({ url, options: request }) => [url, request.method]), [
    ["/api/email-connection", "PUT"], ["/api/email-connection/reconnect", "POST"],
  ]);
  const expected = {
    contract_version: 2, account_address: values.accountAddress, app_password: values.appPassword,
    sender_addresses: values.senderAddresses, application_sender_addresses: [],
    schedule_enabled: false, interval_minutes: 30, expected_revision: 7,
  };
  for (const { options: request } of requests) {
    assert.deepEqual(JSON.parse(request.body), expected);
    assert.equal(request.signal, controller.signal);
    assert.equal(request.headers.get("X-Career-Hub-Token"), "synthetic-session");
    assert.equal(request.headers.get("X-Career-Hub-Diagnostic-Run"), "synthetic-run");
  }
});

test("client fallback diagnostic is bounded and content-minimized", (t) => {
  const originalWindow = globalThis.window;
  const originalCustomEvent = globalThis.CustomEvent;
  t.after(() => {
    globalThis.window = originalWindow;
    globalThis.CustomEvent = originalCustomEvent;
  });
  let emitted;
  globalThis.CustomEvent = class {
    constructor(type, init) {
      this.type = type;
      this.detail = init.detail;
    }
  };
  globalThis.window = {
    dispatchEvent(event) { emitted = event; },
  };
  const runId = "20260903T163409.450000Z_05eb3959";

  reportEmailConnectionClientFailure({
    runId,
    startedAt: Date.now() - 15_000,
    errorCode: "EMAIL_CONNECTION_SAVE_RESPONSE_TIMEOUT",
    summary: "The local response was not confirmed.",
    suggestion: "Restart and inspect connection state.",
  });

  assert.equal(emitted.type, CLIENT_DIAGNOSTIC_EVENT);
  assert.equal(emitted.detail.events.length, 2);
  assert.equal(emitted.detail.events[1].run_id, runId);
  assert.equal(emitted.detail.events[1].stage, "email_connection_client");
  assert.equal(emitted.detail.events[1].details.outcome, "unconfirmed");
  const serialized = JSON.stringify(emitted.detail);
  assert.doesNotMatch(serialized, /private@example\.test|never-log-this|jobs@example\.test/);
});
