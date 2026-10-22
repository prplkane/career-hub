import assert from "node:assert/strict";
import { CareerHubApiClient } from "../src/intake_ui_static/ui/api.js";
import { installDesktopSession } from "./helpers/desktop_session.mjs";

installDesktopSession("fictional-local-capability");

const originalFetch = globalThis.fetch;
const calls = [];
globalThis.fetch = async (path, options = {}) => {
  calls.push({ path, options });
  if (path === "/api/session") {
    return new Response(JSON.stringify({ token: "fictional-local-capability" }), {
      headers: { "Content-Type": "application/json" },
    });
  }
  assert.equal(path, "/api/jobs/job-one/application/submitted-resume/file");
  assert.equal(options.headers.get("X-Career-Hub-Token"), "fictional-local-capability");
  assert.equal(options.cache, "no-store");
  return new Response("fictional submitted bytes", {
    headers: { "Content-Type": "text/plain" },
  });
};

try {
  const api = new CareerHubApiClient();
  const response = await api.submittedResumeFile("job-one");
  assert.equal(await response.text(), "fictional submitted bytes");
  assert.equal(calls.length, 2);
  assert.equal(calls[0].path, "/api/session");
} finally {
  globalThis.fetch = originalFetch;
}
