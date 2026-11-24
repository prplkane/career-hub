import assert from "node:assert/strict";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { before, after, test } from "node:test";
import { chromium } from "playwright";
import { buildFixture, withWorkflow } from "./fixture.mjs";

const ALERTS = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../fixtures/email_alerts");

let build, browser;
before(async () => {
  build = await buildFixture();
  browser = await chromium.launch({ headless: true });
}, { timeout: 130_000 });
after(async () => { await browser?.close(); });

test("Applications: unmatched email updates can be dismissed one by one or cleared", { timeout: 120_000 }, (t) =>
  withWorkflow(t, build, browser, async ({ page, step, origin }) => {
    const dismissals = [];
    page.on("request", (request) => {
      if (request.url().endsWith("/api/application-email-updates/dismiss")) dismissals.push(JSON.parse(request.postData() || "{}"));
    });
    const section = page.locator("#application-email-updates");
    const rows = page.locator("#application-email-updates-list .application-email-update-row");
    const message = (text) => page.locator("#application-ledger-message.success").filter({ hasText: text });

    await step("import a LinkedIn rejection and a Handshake confirmation that match no application", async () => {
      await page.setViewportSize({ width: 1280, height: 900 });
      await page.goto(origin + "/#job-feed", { waitUntil: "domcontentloaded" });
      await page.locator('[data-discovery-mode="email"]').click();
      await page.locator(".email-import-fallback > summary").click();
      await page.locator("#email-import-files").setInputFiles([
        path.join(ALERTS, "linkedin_rejection_with_suggestions.eml"),
        path.join(ALERTS, "handshake_application_sent_with_suggestions.eml"),
      ]);
      const [response] = await Promise.all([
        page.waitForResponse((r) => r.url() === origin + "/api/discoveries/email-imports"),
        page.locator("#email-import-button").click(),
      ]);
      assert.equal(response.status(), 201);
    });

    await step("Applications lists both as unmatched, each with Dismiss", async () => {
      await page.goto(origin + "/#applications", { waitUntil: "domcontentloaded" });
      await section.waitFor({ state: "attached" });
      await page.waitForFunction(() => !document.querySelector("#application-email-updates")?.classList.contains("hidden"));
      await section.locator("summary").first().click();
      assert.equal(await rows.count(), 2);
      assert.equal(await rows.locator("[data-email-update-dismiss]").count(), 2);
      assert.equal((await page.locator("#application-email-updates-clear").textContent()).trim(), "Clear unmatched (2)");
    });

    await step("Dismiss deletes one update at its revision", async () => {
      await rows.first().locator("[data-email-update-dismiss]").click();
      await message("Email update dismissed.").waitFor();
      assert.equal(dismissals.length, 1);
      assert.match(dismissals[0].outcome_id, /^email_outcome_[0-9a-f]{64}$/);
      assert.equal(dismissals[0].expected_revision >= 1, true);
      assert.equal(await rows.count(), 1);
    });

    await step("Clear unmatched asks in-app; Escape keeps it, confirming clears it", async () => {
      await page.locator("#application-email-updates-clear").click();
      const dialog = page.getByRole("alertdialog");
      await dialog.waitFor();
      await page.keyboard.press("Escape");
      await dialog.waitFor({ state: "hidden" });
      assert.equal(dismissals.length, 1);
      await page.locator("#application-email-updates-clear").click();
      await page.getByRole("alertdialog").getByRole("button", { name: "Clear unmatched" }).click();
      await message("Cleared 1 unmatched email update.").waitFor();
      assert.deepEqual(dismissals[1], { contract_version: 1, all_unmatched: true });
      await page.waitForFunction(() => document.querySelector("#application-email-updates")?.classList.contains("hidden"));
    });
  }));
