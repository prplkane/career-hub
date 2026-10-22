import assert from "node:assert/strict";
import test from "node:test";
import { createHash } from "node:crypto";
import { MaterialReview, highlightedReviewText, validateReviewView, reviewCorrections } from "../src/intake_ui_static/features/material_review.js";
import { CareerHubApiClient } from "../src/intake_ui_static/ui/api.js";
import { installDesktopSession } from "./helpers/desktop_session.mjs";

installDesktopSession("test");

const id = `fact_${"a".repeat(64)}`;
function view() {
  return { revision: 0, stale_checks: 0, review_sha256: "b".repeat(64), assessment: { facts: { validation: { status: "block", findings: [] } } }, fields: [{ path: "summary", text: "😀 <script> 20%" }], targets: [{ id, state: "unreviewed", draft_path: "summary", spans: [{ start: 11, end: 14 }] }] };
}

test("review spans use Unicode scalars and escape every source fragment", () => {
  assert.equal(highlightedReviewText("😀 <b> 20%", [{ start: 6, end: 9 }]), "😀 &lt;b&gt; <mark>20%</mark>");
  for (const spans of [[{ start: -1, end: 1 }], [{ start: 1, end: 500 }], [{ start: 2, end: 1 }], [{ start: 0, end: 2 }, { start: 1, end: 3 }]]) assert.throws(() => highlightedReviewText("test", spans));
});

test("malformed or ambiguous review authority is not selectable", () => {
  assert.equal(validateReviewView(view()).targets[0].id, id);
  const revoked = view(); revoked.targets[0].state = "revoked";
  assert.equal(validateReviewView(revoked).targets[0].state, "revoked");
  for (const mutate of [
    (v) => { v.revision = 0.5; }, (v) => { v.stale_checks = "<script>"; },
    (v) => { v.targets.push(v.targets[0]); }, (v) => { v.targets[0].state = "approved"; },
    (v) => { v.targets[0].draft_path = "unknown"; }, (v) => { v.targets[0].spans = []; },
    (v) => { v.targets[0].id = '" onclick="'; }, (v) => { v.review_sha256 = "missing"; },
    (v) => { v.fields.push(v.fields[0]); }, (v) => { v.fields[0].path = 12; },
    (v) => { v.assessment.facts.validation.findings = null; },
  ]) { const v = view(); mutate(v); assert.throws(() => validateReviewView(v)); }
});

test("non-confirmable findings still request draft correction alongside selected facts", () => {
  const v = view();
  v.assessment.facts.validation.findings = [
    { severity: "block", code: "UNRESOLVED_PLACEHOLDER", draft_path: "summary" },
    { severity: "block", code: "UNRESOLVED_PLACEHOLDER", draft_path: "summary" },
    { severity: "block", code: "UNSUPPORTED_FACT_TOKEN", draft_path: "other" },
  ];
  assert.deepEqual(reviewCorrections(validateReviewView(v)), ["summary"]);
  assert.equal(v.targets.length, 1, "Placeholder findings do not acquire confirmation authority");
});

function controller(api) {
  return Object.assign(Object.create(MaterialReview.prototype), { api, kind: "resume", generation: 0, context: { jobId: "job", persisted: true, ready: true, dirty: false }, view: view(), selected: new Set([id]), busy: false, externalBusy: false, render() {}, onChange() {} });
}

test("late review response cannot overwrite an edited or different context", async () => {
  let resolveRead;
  const c = controller({ hasMaterialIntegrity: async () => true, getMaterialReview: () => new Promise((resolve) => { resolveRead = resolve; }) });
  const pending = c.load();
  await Promise.resolve();
  c.generation++;
  c.context = { jobId: "other", dirty: true };
  c.view = null;
  resolveRead(view());
  await pending;
  assert.equal(c.view, null);
  assert.equal(c.context.jobId, "other");
});

test("unconfirmed mutation requires reload without retry or invented rollback", async () => {
  let calls = 0;
  const c = controller({ updateMaterialReview: async () => { calls++; throw new Error("private native failure"); } });
  await c.act("confirm");
  assert.equal(calls, 1);
  assert.equal(c.view, null);
  assert.equal(c.selected.size, 0);
  assert.match(c.error, /may have been saved/);
  assert.doesNotMatch(c.error, /private native/);
  await c.act("confirm");
  assert.equal(calls, 1);
});

test("explicit reconfirmation supports revoked checks without selecting other targets", async () => {
  let request;
  const c = controller({ updateMaterialReview: async (_job, _kind, r) => { request = r; return view(); } });
  c.view.targets[0].state = "revoked";
  await c.act("confirm");
  assert.deepEqual(request.targetIds, [id]);
  assert.equal(request.operation, "confirm");
});

test("old sessions leave review unsupported; refreshed sessions replace capabilities", async () => {
  const original = globalThis.fetch;
  const api = new CareerHubApiClient();
  try {
    globalThis.fetch = async (_path, options) => {
      assert.ok(options.signal instanceof AbortSignal, "Session discovery is bounded");
      return Response.json({ token: "test" });
    };
    assert.equal(await api.hasMaterialIntegrity(), false);
    api.sessionPromise = null;
    globalThis.fetch = async () => Response.json({ token: "new", capabilities: { material_integrity: 1 } });
    assert.equal(await api.hasMaterialIntegrity(), true);
    api.sessionPromise = null;
    globalThis.fetch = async () => Response.json({ token: "old" });
    assert.equal(await api.hasMaterialIntegrity(), false);
  } finally { globalThis.fetch = original; }
});

test("a failed session does not inherit capabilities or retry a mutation", async () => {
  const original = globalThis.fetch;
  const api = new CareerHubApiClient();
  api.capabilities = { material_integrity: 1 };
  try {
    globalThis.fetch = async () => { throw new DOMException("Timed out", "TimeoutError"); };
    await assert.rejects(api.hasMaterialIntegrity());
    assert.deepEqual(api.capabilities, {});
    assert.equal(api.sessionPromise, null);
  } finally { globalThis.fetch = original; }
});

test("an older Python session uses its original download route", async () => {
  const api = new CareerHubApiClient();
  api.hasMaterialIntegrity = async () => false;
  const calls = [];
  api.raw = async (p) => { calls.push(p); return new Response("old", { headers: { "Content-Disposition": 'attachment; filename="old.txt"' } }); };
  api.json = async () => { throw new Error("No new export route on Python"); };
  await api.downloadResumeMaterial("job", "txt");
  await api.downloadCoverLetter("job", "txt");
  assert.deepEqual(calls, ["/api/jobs/job/materials/resume/file?format=txt", "/api/jobs/job/materials/cover-letter/file?format=txt"]);
});

test("review sends only explicitly selected optimistic IDs", async () => {
  const api = new CareerHubApiClient();
  let request;
  api.json = (...args) => { request = args; return {}; };
  await api.updateMaterialReview("exact job", "cover_letter", { operation: "confirm", expectedRevision: 2, reviewSha256: "b".repeat(64), targetIds: [id] });
  assert.equal(request[0], "/api/jobs/exact%20job/materials/cover-letter/reviews");
  assert.deepEqual(request[2], { operation: "confirm", expected_revision: 2, review_sha256: "b".repeat(64), target_ids: [id] });
  assert.throws(() => api.updateMaterialReview("job", "resume", { operation: "approve-all" }));
  assert.throws(() => api.materialPath("job", "../data"));
});

test("verified export downloads only the returned exact identity without legacy fallback", async () => {
  const api = new CareerHubApiClient();
  const artifact = `material_${"a".repeat(64)}`;
  let path;
  const metadata = { artifact_id: artifact, provenance: "versioned", filename: "Synthetic_Resume_Test.txt", job_id: "job", kind: "resume", format: "txt", size_bytes: 6, sha256: createHash("sha256").update("sample").digest("hex") };
  api.json = async () => metadata;
  api.raw = async (p) => { path = p; return new Response("sample", { headers: { "X-Career-Hub-Artifact-ID": artifact, "X-Career-Hub-Provenance": "versioned" } }); };
  assert.equal((await api.downloadVerifiedMaterial("job", "resume", "txt")).filename, "Synthetic_Resume_Test.txt");
  assert.equal(path, `/api/jobs/job/materials/resume/file?artifact_id=${artifact}`);
  api.raw = async () => new Response("wrong", { headers: { "X-Career-Hub-Artifact-ID": `material_${"b".repeat(64)}`, "X-Career-Hub-Provenance": "versioned" } });
  await assert.rejects(api.downloadVerifiedMaterial("job", "resume", "txt"));
  api.raw = async () => new Response("sample", { headers: { "X-Career-Hub-Artifact-ID": artifact, "X-Career-Hub-Provenance": "versioned" } });
  for (const change of [{ job_id: "other" }, { kind: "cover_letter" }, { format: "pdf" }, { filename: "../file.txt" }, { size_bytes: 7 }, { sha256: "0".repeat(64) }]) {
    api.json = async () => ({ ...metadata, ...change });
    await assert.rejects(api.downloadVerifiedMaterial("job", "resume", "txt"));
  }
});
