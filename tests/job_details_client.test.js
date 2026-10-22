import assert from "node:assert/strict";
import test from "node:test";
import { CareerHubApiClient } from "../src/intake_ui_static/ui/api.js";
import { installDesktopSession } from "./helpers/desktop_session.mjs";

installDesktopSession("fictional-token");

const revision = `sha256:${"a".repeat(64)}`;

async function withSession(capabilities, callback) {
  const original = globalThis.fetch;
  const calls = [];
  globalThis.fetch = async (path, options) => {
    if (path === "/api/session") return Response.json({ token: "fictional-token", capabilities });
    calls.push({ path, options });
    return Response.json({ job: { id: "synthetic", card_revision: revision } });
  };
  try { await callback(new CareerHubApiClient(), calls); } finally { globalThis.fetch = original; }
}

test("Python sessions keep the existing correction body", async () => {
  await withSession({}, async (api, calls) => {
    await api.saveJobDetails("synthetic", { title: "Engineer" }, revision);
    assert.equal(calls.length, 1);
    assert.deepEqual(JSON.parse(calls[0].options.body), { fields: { title: "Engineer" } });
  });
});

test("Go preview echoes the displayed revision and refuses a missing one", async () => {
  await withSession({ job_details_revision: true }, async (api, calls) => {
    await assert.rejects(api.saveJobDetails("synthetic", { title: "Engineer" }, ""), /revision unavailable/i);
    assert.equal(calls.length, 0, "missing revision must not reach the writer");
    await api.saveJobDetails("synthetic", { title: "Engineer" }, revision);
    assert.deepEqual(JSON.parse(calls[0].options.body), { fields: { title: "Engineer" }, expected_card_revision: revision });
    assert.equal((await api.getJob("synthetic")).job.card_revision, revision);
    assert.equal(calls[1].path, "/api/jobs/synthetic");
  });
});

test("saved-JD rescan preserves Python request and binds Go preview to displayed revision", async () => {
  await withSession({}, async (api, calls) => {
    await api.reanalyzeJob("synthetic", revision);
    assert.equal(calls[0].path, "/api/jobs/synthetic/reanalyze");
    assert.equal(calls[0].options.method, "POST");
    assert.equal(calls[0].options.body, undefined);
  });
  await withSession({ job_reanalysis_revision: true }, async (api, calls) => {
    await assert.rejects(api.reanalyzeJob("synthetic", ""), /revision unavailable/i);
    assert.equal(calls.length, 0);
    await api.reanalyzeJob("synthetic", revision);
    assert.deepEqual(JSON.parse(calls[0].options.body), { expected_card_revision: revision });
  });
});
