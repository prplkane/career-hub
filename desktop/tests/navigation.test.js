"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const {
  isAllowedPreviewNavigation,
  isAllowedPreviewWindowRequest,
  isOwnedAppUrl,
  isSafeExternalUrl,
  normalizeOwnedOrigin,
} = require("../src/navigation");

const ORIGIN = "http://127.0.0.1:41234";

test("owned origin is one exact ephemeral IPv4 loopback authority", () => {
  assert.equal(normalizeOwnedOrigin(ORIGIN), ORIGIN);
  for (const candidate of [
    "https://127.0.0.1:41234",
    "http://localhost:41234",
    "http://127.0.0.1",
    "http://127.0.0.1:41234/path",
    "http://user@127.0.0.1:41234",
  ]) {
    assert.equal(normalizeOwnedOrigin(candidate), null, candidate);
  }
  assert.equal(isOwnedAppUrl(`${ORIGIN}/#jobs`, ORIGIN), true);
  assert.equal(isOwnedAppUrl("http://127.0.0.1:41235/", ORIGIN), false);
});

test("only an owned page may request an empty sandboxed preview", () => {
  assert.equal(
    isAllowedPreviewWindowRequest("about:blank", `${ORIGIN}/#documents`, ORIGIN),
    true,
  );
  assert.equal(
    isAllowedPreviewWindowRequest("https://example.com", `${ORIGIN}/`, ORIGIN),
    false,
  );
  assert.equal(
    isAllowedPreviewWindowRequest("about:blank", "https://example.com", ORIGIN),
    false,
  );
  assert.equal(
    isAllowedPreviewNavigation(`blob:${ORIGIN}/document-id`, ORIGIN),
    true,
  );
  assert.equal(
    isAllowedPreviewNavigation("blob:https://example.com/document-id", ORIGIN),
    false,
  );
});

test("external open allowlist rejects local, credentialed, and executable URLs", () => {
  for (const candidate of [
    "https://jobs.example.com/role?id=1",
    "http://jobs.example.com/role",
    "mailto:person%40example.com",
    "tel:%2B1%20212%20555%201212",
  ]) {
    assert.equal(isSafeExternalUrl(candidate), true, candidate);
  }
  for (const candidate of [
    "javascript:alert(1)",
    "file:///etc/passwd",
    "data:text/html,unsafe",
    "https://user:secret@example.com/",
    "http://127.0.0.1:8766/api/session",
    "http://2130706433/",
    "http://localhost/admin",
    "http://[::]/",
    "http://[::1]/",
    "http://[::ffff:127.0.0.1]/",
    "http://[::ffff:7f00:1]/",
    "mailto:person@example.com?body=payload",
    "tel:*123%23",
  ]) {
    assert.equal(isSafeExternalUrl(candidate), false, candidate);
  }
});
