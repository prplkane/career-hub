import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { before, after, test } from "node:test";
import { chromium } from "playwright";
import { buildFixture, withWorkflow } from "./fixture.mjs";

let build, browser;
before(async () => {
  build = await buildFixture();
  browser = await chromium.launch({ headless: true });
}, { timeout: 130_000 });
after(async () => { await browser?.close(); });

async function storedResumeFiles(home) {
  const found = [];
  async function walk(directory) {
    for (const entry of await fs.readdir(directory, { withFileTypes: true }).catch(() => [])) {
      const full = path.join(directory, entry.name);
      if (entry.isDirectory()) await walk(full);
      else if (full.replaceAll("\\", "/").includes("applications/resumes/library/")) found.push(full);
    }
  }
  await walk(home);
  return found;
}

async function chooseApplicationResume(page, name, content) {
  await page.locator("#library-resume-file").setInputFiles({ name, mimeType: "application/octet-stream", buffer: Buffer.from(content) });
}

test("Resumes: mismatched content is refused, unreviewable formats are explained, delete asks in-app", { timeout: 90_000 }, (t) =>
  withWorkflow(t, build, browser, async ({ page, step, origin, home }) => {
    await step("open Save for applications", async () => {
      await page.goto(origin + "/#intake", { waitUntil: "domcontentloaded" });
      await page.locator("#resume-intent-applications").check();
      await page.locator("#library-resume-file").waitFor({ state: "visible" });
    });
    await step("a text file renamed to .pdf is explained, then refused without storing", async () => {
      await chooseApplicationResume(page, "Synthetic_Renamed.pdf", "plain synthetic text, not a PDF");
      await page.locator("#library-resume-message").filter({ hasText: "PDF files are saved for applications" }).waitFor();
      const [response] = await Promise.all([
        page.waitForResponse((r) => r.url().endsWith("/api/resumes") && r.request().method() === "POST"),
        page.locator("#save-library-resume-button").click(),
      ]);
      assert.equal(response.status(), 400);
      assert.equal((await response.json()).error_code, "RESUME_LIBRARY_UPLOAD_INVALID");
      await page.locator("#library-resume-message.error").waitFor();
      assert.deepEqual(await storedResumeFiles(home), []);
    });
    await step("a real PDF signature is stored", async () => {
      await chooseApplicationResume(page, "Synthetic_SOC.pdf", "%PDF-1.7\n% synthetic test resume\n");
      const [response] = await Promise.all([
        page.waitForResponse((r) => r.url().endsWith("/api/resumes") && r.request().method() === "POST"),
        page.locator("#save-library-resume-button").click(),
      ]);
      assert.equal(response.status(), 201);
      assert.equal((await storedResumeFiles(home)).length, 1);
    });
    await step("a DOCX choice shows no review notice", async () => {
      await chooseApplicationResume(page, "Synthetic.docx", "PK\x03\x04synthetic");
      assert.equal((await page.locator("#library-resume-message").textContent()).trim(), "");
    });
    await step("an auto-filled name follows the file; a typed name is kept", async () => {
      const name = page.locator("#library-resume-display-name");
      assert.equal(await name.inputValue(), "Synthetic");
      await chooseApplicationResume(page, "Other_Synthetic.docx", "PK\x03\x04synthetic");
      assert.equal(await name.inputValue(), "Other_Synthetic");
      await name.fill("Typed synthetic name");
      await chooseApplicationResume(page, "Third.docx", "PK\x03\x04synthetic");
      assert.equal(await name.inputValue(), "Typed synthetic name");
    });
    await step("Escape in the delete dialog keeps the resume", async () => {
      await page.goto(origin + "/#resume-library", { waitUntil: "domcontentloaded" });
      const remove = page.locator('[data-resume-library-action="delete"]');
      await remove.click();
      const dialog = page.getByRole("alertdialog");
      await dialog.waitFor();
      assert.match(await dialog.textContent(), /Delete "Synthetic_SOC"\?/);
      assert.equal(await page.evaluate(() => document.activeElement?.dataset.confirmCancel !== undefined), true, "Cancel has focus");
      await page.keyboard.press("Escape");
      await dialog.waitFor({ state: "hidden" });
      assert.equal((await storedResumeFiles(home)).length, 1);
      await remove.waitFor();
    });
    await step("confirming the danger button deletes the stored file", async () => {
      await page.locator('[data-resume-library-action="delete"]').click();
      const [response] = await Promise.all([
        page.waitForResponse((r) => r.url().includes("/api/resumes/") && r.request().method() === "DELETE"),
        page.getByRole("alertdialog").getByRole("button", { name: "Delete permanently" }).click(),
      ]);
      assert.equal(response.status(), 200);
      assert.deepEqual(await storedResumeFiles(home), []);
    });
  }, { expectedHTTPFailures: [{ method: "POST", path: "/api/resumes", status: 400 }] }));

const SYNTHETIC_JD = [
  "Synthetic Security Analyst",
  "Fictional Labs is hiring a Security Analyst to monitor alerts, triage incidents and tune detections.",
  "Requirements: 2+ years in a security operations center, SIEM experience, scripting in Python or PowerShell.",
  "Nice to have: cloud security fundamentals and incident response documentation.",
].join("\n\n");

async function createAppliedJob(page) {
  return page.evaluate(async (sourceText) => {
    const { api } = await import("/ui/api.js");
    const result = await api.createJob({
      source_text: sourceText, source_name: "synthetic_security_analyst.txt",
      company: "Fictional Labs", title: "Security Analyst", source_url: "https://example.test/jobs/synthetic-analyst",
      posting_id: "", idempotency_key: crypto.randomUUID(), initial_status: "applied",
    });
    return result.job.id;
  }, SYNTHETIC_JD);
}

test("Applications: keyboard stage steps wait for Enter, Escape restores", { timeout: 90_000 }, (t) =>
  withWorkflow(t, build, browser, async ({ page, step, origin }) => {
    const statusPosts = [];
    page.on("request", (request) => {
      if (request.method() === "POST" && request.url().endsWith("/application/status")) statusPosts.push(request.postDataJSON());
    });
    let jobId;
    await step("set up a synthetic Career Vault", async () => {
      await page.goto(origin + "/#profile", { waitUntil: "domcontentloaded" });
      await page.locator("#vault-setup-name").fill("Synthetic Tour Candidate");
      await page.locator("#vault-setup-create").click();
      await page.waitForFunction(() => !document.querySelector("#vault-setup-apply").disabled);
      await page.locator("#vault-setup-apply").click();
      await page.waitForFunction(() => document.querySelector("#profile-name")?.value === "Synthetic Tour Candidate");
      // Material drafts on the Applications drawer need candidate contact fields.
      await page.locator("#edit-profile-button").click();
      await page.locator("#profile-email").fill("synthetic@example.invalid");
      await page.locator("#profile-current-location").fill("Synthetic City");
      await Promise.all([
        page.waitForResponse((r) => r.url().endsWith("/api/vault/profile") && r.request().method() === "PUT" && r.status() === 200),
        page.locator("#save-profile-button").click(),
      ]);
    });
    await step("save a synthetic applied job", async () => {
      await page.goto(origin + "/#applications", { waitUntil: "domcontentloaded" });
      jobId = await createAppliedJob(page);
      await page.reload({ waitUntil: "networkidle" });
    });
    const select = () => page.locator(`.application-inline-status[data-application-job-id="${jobId}"]`);
    const message = page.locator("#application-ledger-message");
    await step("an arrow key changes the list without saving", async () => {
      await select().waitFor();
      assert.equal(await select().inputValue(), "applied");
      await select().focus();
      await page.keyboard.press("ArrowDown");
      await page.waitForTimeout(400);
      assert.notEqual(await select().inputValue(), "applied", "closed select stepped to the next stage");
      assert.deepEqual(statusPosts, []);
      assert.match(await message.textContent(), /Press Enter to save Interview, or Escape to keep Applied\./);
    });
    await step("Escape restores the saved stage without a write", async () => {
      await page.keyboard.press("Escape");
      assert.equal(await select().inputValue(), "applied");
      assert.deepEqual(statusPosts, []);
    });
    await step("Enter saves exactly one stage event with its label", async () => {
      await page.keyboard.press("ArrowDown");
      await Promise.all([
        page.waitForResponse((r) => r.url().endsWith("/application/status") && r.status() === 200),
        page.keyboard.press("Enter"),
      ]);
      assert.deepEqual(statusPosts.map((body) => body.status), ["interview"]);
      await message.filter({ hasText: "Saved Fictional Labs: Interview." }).waitFor();
      assert.equal(await select().inputValue(), "interview");
    });
  }));

const ALERT_FIXTURE = path.resolve(path.dirname(new URL(import.meta.url).pathname.replace(/^\/(\w:)/, "$1")), "../fixtures/email_alerts/capture_quality_drift.eml");

test("Job Feed: emptying dismissed opportunities asks in-app; Escape keeps them", { timeout: 90_000 }, (t) =>
  withWorkflow(t, build, browser, async ({ page, step, origin }) => {
    const deletes = [];
    page.on("request", (request) => { if (request.url().includes("/api/discoveries/flush-archived")) deletes.push(request.method()); });
    await step("import a synthetic job-alert email", async () => {
      await page.goto(origin + "/#job-feed", { waitUntil: "domcontentloaded" });
      await page.locator('[data-discovery-mode="email"]').click();
      await page.locator(".email-import-fallback > summary").click();
      await page.locator("#email-import-files").setInputFiles(ALERT_FIXTURE);
      const [response] = await Promise.all([
        page.waitForResponse((r) => r.url() === origin + "/api/discoveries/email-imports"),
        page.locator("#email-import-button").click(),
      ]);
      assert.equal(response.status(), 201);
      await page.locator('[data-discovery-mode="feed"]').click();
    });
    const row = page.locator("#discovery-list [data-discovery-id]").filter({ hasText: "Platform Engineer" }).first();
    await step("dismiss it and open the dismissed view", async () => {
      await row.click();
      await page.locator("#discovery-detail h2").filter({ hasText: "Platform Engineer" }).waitFor();
      await Promise.all([
        page.waitForResponse((r) => r.url().includes("/api/discoveries/") && r.request().method() === "PUT" && r.status() === 200),
        page.locator(".discovery-dismiss-action").click(),
      ]);
      // A fresh load of the active feed takes the badge from the page's
      // dismissed_total, not from the local count kept after the dismissal.
      // The plain route matters: a route naming the dismissed item would open
      // the dismissed view, whose badge is that view's own total.
      await page.evaluate(() => { window.location.hash = "#job-feed"; });
      await page.reload();
      await page.waitForFunction(() => document.querySelector("#discovery-list")?.getAttribute("aria-busy") === "false");
      assert.equal(await page.locator("#discovery-archived-filter").getAttribute("aria-pressed"), "false");
      assert.equal((await page.locator("#discovery-archived-count").textContent()).trim(), "1");
      await page.locator("#discovery-archived-filter").click();
      await row.waitFor();
      await page.locator("#discovery-flush-archived").waitFor({ state: "visible" });
    });
    await step("the dismissed view finishes loading", async () => {
      await page.waitForFunction(() => document.querySelector("#discovery-list").getAttribute("aria-busy") === "false");
    });
    await step("Escape keeps the dismissed opportunity", async () => {
      await page.locator("#discovery-flush-archived").click();
      const dialog = page.getByRole("alertdialog");
      await dialog.waitFor();
      assert.match(await dialog.textContent(), /Delete all 1 dismissed opportunities\?/);
      assert.equal(await dialog.getByRole("button", { name: "Delete all dismissed" }).getAttribute("class"), "danger-button");
      await page.keyboard.press("Escape");
      await dialog.waitFor({ state: "hidden" });
      assert.deepEqual(deletes, []);
      await row.waitFor();
    });
    await step("confirming deletes every dismissed opportunity", async () => {
      await page.locator("#discovery-flush-archived").click();
      await Promise.all([
        page.waitForResponse((r) => r.url().includes("/api/discoveries/flush-archived") && r.status() === 200),
        page.getByRole("alertdialog").getByRole("button", { name: "Delete all dismissed" }).click(),
      ]);
      assert.deepEqual(deletes, ["POST"]);
      await row.waitFor({ state: "detached" });
    });
  }));

test("Experience: unsaved edits survive Keep editing and clear on Discard changes", { timeout: 90_000 }, (t) =>
  withWorkflow(t, build, browser, async ({ page, step, origin }) => {
    await step("set up a synthetic Career Vault", async () => {
      await page.goto(origin + "/#profile", { waitUntil: "domcontentloaded" });
      await page.locator("#vault-setup-name").fill("Synthetic Tour Candidate");
      await page.locator("#vault-setup-create").click();
      await page.waitForFunction(() => !document.querySelector("#vault-setup-apply").disabled);
      await page.locator("#vault-setup-apply").click();
      await page.waitForFunction(() => document.querySelector("#profile-name")?.value === "Synthetic Tour Candidate");
    });
    const company = page.locator("#vault-company");
    await step("start a new experience with an unsaved company", async () => {
      await page.goto(origin + "/#vault", { waitUntil: "domcontentloaded" });
      await page.locator("#new-experience-button").click();
      await company.fill("Unsaved Synthetic Company");
    });
    await step("Keep editing preserves the draft", async () => {
      await page.locator("#new-experience-button").click();
      const dialog = page.getByRole("alertdialog");
      await dialog.getByText("Discard unsaved experience changes?").waitFor();
      await dialog.getByRole("button", { name: "Keep editing" }).click();
      await dialog.waitFor({ state: "hidden" });
      assert.equal(await company.inputValue(), "Unsaved Synthetic Company");
    });
    await step("Discard changes clears the draft", async () => {
      await page.locator("#new-experience-button").click();
      await page.getByRole("alertdialog").getByRole("button", { name: "Discard changes" }).click();
      await page.waitForFunction(() => document.querySelector("#vault-company").value === "");
    });
  }));
