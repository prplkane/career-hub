"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const test = require("node:test");

const {
  LOADING_SCHEME,
  LOADING_CSP,
  createLoadingProtocolHandler,
  isTrustedLoadingUrl,
  loadingUrl,
  registerLoadingProtocol,
} = require("../src/loading_protocol");

test("the loading scheme receives only standard and secure privileges", () => {
  assert.deepEqual(LOADING_SCHEME, {
    privileges: { secure: true, standard: true },
    scheme: "career-hub",
  });
});

test("loading URLs have strict state and sanitized error-code contracts", () => {
  assert.equal(loadingUrl("restore"), "career-hub://app/loading.html?state=restore");
  assert.equal(isTrustedLoadingUrl(loadingUrl("restore")), true);
  assert.equal(
    loadingUrl("starting"),
    "career-hub://app/loading.html?state=starting",
  );
  assert.equal(
    loadingUrl("error", "DESKTOP_SIDECAR_EXITED"),
    "career-hub://app/loading.html?state=error&error_code=DESKTOP_SIDECAR_EXITED",
  );
  assert.throws(() => loadingUrl("unknown"), /state is invalid/);
  assert.throws(() => loadingUrl("error", "<script>"), /code is invalid/);
  assert.throws(() => loadingUrl("ready", "ERROR"), /only valid/);
});

test("trusted loading URL validation rejects origin and query confusion", () => {
  assert.equal(isTrustedLoadingUrl(loadingUrl("backup")), true);
  assert.throws(() => loadingUrl("backup", "ERROR"), /only valid/);
  assert.equal(isTrustedLoadingUrl(loadingUrl("ready")), true);
  for (const candidate of [
    "career-hub://evil/loading.html?state=ready",
    "career-hub://app/../secret?state=ready",
    "career-hub://app/loading.html?state=ready&next=https://evil.invalid",
    "career-hub://app/loading.html?state=ready&state=error",
    "career-hub://app/loading.html?state=error",
    "career-hub://app/loading.css?state=ready",
    "file:///loading.html?state=ready",
  ]) {
    assert.equal(isTrustedLoadingUrl(candidate), false, candidate);
  }
});

test("protocol handler serves only the three packaged assets", async () => {
  const reads = [];
  const handler = createLoadingProtocolHandler(
    path.resolve("desktop", "ui"),
    async (candidate) => {
      reads.push(candidate);
      return Buffer.from("asset");
    },
  );

  const documentResponse = await handler({
    method: "GET",
    url: loadingUrl("starting"),
  });
  assert.equal(documentResponse.status, 200);
  assert.equal(documentResponse.headers.get("content-type"), "text/html; charset=utf-8");
  assert.equal(
    documentResponse.headers.get("content-security-policy"),
    LOADING_CSP,
  );
  assert.equal(await documentResponse.text(), "asset");

  const cssResponse = await handler({
    method: "HEAD",
    url: "career-hub://app/loading.css",
  });
  assert.equal(cssResponse.status, 200);
  assert.equal(cssResponse.headers.get("content-length"), "5");
  assert.equal(await cssResponse.text(), "");

  for (const request of [
    { method: "POST", url: loadingUrl("starting") },
    { method: "GET", url: "career-hub://app/secrets.txt" },
    { method: "GET", url: "career-hub://app/%2e%2e/src/main.js" },
    { method: "GET", url: "career-hub://other/loading.css" },
    { method: "GET", url: "career-hub://app/loading.js?cache=1" },
  ]) {
    const result = await handler(request);
    assert.notEqual(result.status, 200, JSON.stringify(request));
  }
  assert.equal(reads.length, 2);
  assert.match(reads[0], /loading\.html$/);
  assert.match(reads[1], /loading\.css$/);
});

test("protocol registration binds one handler to the loading scheme", () => {
  const calls = [];
  registerLoadingProtocol(
    {
      handle(scheme, handler) {
        calls.push({ handler, scheme });
      },
    },
    path.resolve("desktop", "ui"),
  );
  assert.equal(calls.length, 1);
  assert.equal(calls[0].scheme, "career-hub");
  assert.equal(typeof calls[0].handler, "function");
});
