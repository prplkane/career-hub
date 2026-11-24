import assert from "node:assert/strict";
import { before, after, test } from "node:test";
import { chromium } from "playwright";
import { buildFixture, withWorkflow } from "./fixture.mjs";

let build, browser;
before(async () => {
  build = await buildFixture();
  browser = await chromium.launch({ headless: true });
}, { timeout: 130_000 });
after(async () => { await browser?.close(); });

test("Email tab: folds, and source policies edited as one table", { timeout: 90_000 }, (t) =>
  withWorkflow(t, build, browser, async ({ page, step, origin }) => {
    const table = page.locator("#email-source-policy-list");
    const row = table.locator('[data-email-source-row="alerts@example.test"]');
    const route = (name) => row.locator(`[data-email-source-route="${name}"]`);

    await step("open the email tab on an unconfigured workspace", async () => {
      await page.setViewportSize({ width: 1280, height: 900 });
      await page.goto(origin + "/#job-feed", { waitUntil: "domcontentloaded" });
      await page.locator('[data-discovery-mode="email"]').click();
      await page.locator("#email-sync-status").filter({ hasText: "Not configured" }).waitFor();
      assert.equal(await page.locator("#email-sender-review").evaluate((node) => node.classList.contains("hidden")), true, "no senders wait");
      assert.equal(await page.locator("#email-connection-settings").evaluate((node) => node.open), true, "settings open until connected");
      assert.equal(await page.locator("#email-source-policies").evaluate((node) => node.open), true);
      await table.locator(".email-source-empty").waitFor();
    });

    await step("adding a sender makes one table row with named switches", async () => {
      await page.locator("#email-source-address").fill("Alerts@Example.test");
      await page.locator("#email-source-route").selectOption("job_alerts");
      await page.locator("#email-source-add").click();
      await row.waitFor();
      assert.equal(await route("job_alerts").isChecked(), true);
      assert.equal(await route("application_tracking").isChecked(), false);
      assert.equal((await page.locator("#email-source-count").textContent()).trim(), "1 source");
      assert.equal((await page.locator("#email-source-dirty").textContent()).trim(), "Unsaved changes");
    });

    await step("switches turn routes on, but never leave a sender with none", async () => {
      await route("application_tracking").check();
      assert.equal(await route("application_tracking").isChecked(), true);
      await route("job_alerts").uncheck();
      assert.equal(await route("job_alerts").isChecked(), false);
      await route("application_tracking").click();
      assert.equal(await route("application_tracking").isChecked(), true, "the last route stays on");
      await page.locator("#email-sync-message").filter({ hasText: "Keep at least one route enabled" }).waitFor();
    });

    await step("Remove empties the table again", async () => {
      await row.getByRole("button", { name: "Remove alerts@example.test" }).click();
      await row.waitFor({ state: "detached" });
      await table.locator(".email-source-empty").waitFor();
      assert.equal((await page.locator("#email-source-count").textContent()).trim(), "0 sources");
    });

    await step("both save buttons submit the same form", async () => {
      assert.equal(await page.locator("#email-source-save").evaluate((node) => node.form?.id), "email-connection-form");
      assert.equal(await page.locator("#email-connection-save").evaluate((node) => node.form?.id), "email-connection-form");
    });
  }));
