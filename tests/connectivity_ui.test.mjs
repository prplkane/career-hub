import assert from "node:assert/strict";
import test from "node:test";

import {
  classifyInternetFailureCode,
  connectivityCopy,
  connectivityMessage,
} from "../src/intake_ui_static/ui/connectivity.js";

test("connectivity copy distinguishes offline, unverified recovery, and provider reachability", () => {
  assert.equal(connectivityMessage("offline"), connectivityCopy.offline);
  assert.equal(connectivityMessage("available"), connectivityCopy.available);
  assert.equal(connectivityMessage("unreachable"), connectivityCopy.unreachable);
});

test("only explicit external transport failures affect connectivity feedback", () => {
  for (const code of [
    "EMAIL_SYNC_NETWORK_FAILED",
    "PUBLIC_JOB_RESOLUTION_CONNECT_FAILED",
    "PUBLIC_JOB_RESOLUTION_TIMEOUT",
  ]) {
    assert.equal(classifyInternetFailureCode(code), true, code);
  }
  for (const code of [
    "EMAIL_SYNC_AUTH_FAILED",
    "PUBLIC_JOB_RESOLUTION_TLS_FAILED",
    "LOCAL_API_ACCESS_DENIED",
    "DESKTOP_SIDECAR_EXITED",
    "",
  ]) {
    assert.equal(classifyInternetFailureCode(code), false, code);
  }
});
