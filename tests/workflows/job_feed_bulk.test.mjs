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

async function listSettled(page) {
  await page.waitForFunction(() => document.querySelector("#discovery-list")?.getAttribute("aria-busy") === "false");
}

test("Job Feed: folded detail sections, bulk dismiss and delete, single delete", { timeout: 120_000 }, (t) =>
  withWorkflow(t, build, browser, async ({ page, step, origin }) => {
    // Item writes only: PUT changes state, DELETE removes one opportunity.
    const writes = [];
    page.on("request", (request) => {
      const { pathname } = new URL(request.url());
      if (/^\/api\/discoveries\/[^/]+$/.test(pathname) && ["PUT", "DELETE"].includes(request.method())) {
        writes.push(request.method());
      }
    });
    const rows = page.locator("#discovery-list [data-discovery-id]");
    const rowFor = (title) => page.locator("#discovery-list .job-feed-row-item").filter({ hasText: title });
    const message = (text) => page.locator("#discovery-message.success").filter({ hasText: text });

    await step("import three synthetic job alerts", async () => {
      await page.setViewportSize({ width: 1280, height: 900 });
      await page.goto(origin + "/#job-feed", { waitUntil: "domcontentloaded" });
      await page.locator('[data-discovery-mode="email"]').click();
      await page.locator(".email-import-fallback > summary").click();
      await page.locator("#email-import-files").setInputFiles([
        path.join(ALERTS, "multi_job_digest_html.eml"),
        path.join(ALERTS, "capture_quality_drift.eml"),
      ]);
      const [response] = await Promise.all([
        page.waitForResponse((r) => r.url() === origin + "/api/discoveries/email-imports"),
        page.locator("#email-import-button").click(),
      ]);
      assert.equal(response.status(), 201);
      await page.locator('[data-discovery-mode="feed"]').click();
      await listSettled(page);
      assert.equal(await rows.count(), 3);
    });

    await step("detail sections start folded and keep the choice across a re-render", async () => {
      await rowFor("Platform Engineer").locator("[data-discovery-id]").click();
      await page.locator("#discovery-detail h2").filter({ hasText: "Platform Engineer" }).waitFor();
      const fold = () => page.locator('#discovery-detail [data-discovery-fold="assessment"]');
      assert.equal(await fold().evaluate((node) => node.open), false);
      await fold().locator("summary").click();
      assert.equal(await fold().evaluate((node) => node.open), true);
      // Shortlisting rebuilds the whole panel; the open fold must survive it.
      await Promise.all([
        page.waitForResponse((r) => r.request().method() === "PUT" && r.status() === 200),
        page.locator('#discovery-detail [data-discovery-state="shortlisted"]').click(),
      ]);
      await page.locator('#discovery-detail [data-discovery-state="inbox"]').waitFor();
      assert.equal(await fold().evaluate((node) => node.open), true);
      assert.equal(await page.locator("#discovery-detail").evaluate((node) => getComputedStyle(node).position), "sticky");
    });

    await step("ticking rows selects them without opening them", async () => {
      await rowFor("Backend Engineer").locator("[data-discovery-select]").check();
      await rowFor("Product Analyst").locator("[data-discovery-select]").check();
      await page.locator("#discovery-bulk-bar").waitFor({ state: "visible" });
      assert.equal((await page.locator("#discovery-bulk-count").textContent()).trim(), "2 selected");
      assert.match(await page.locator("#discovery-detail h2").textContent(), /Platform Engineer/);
      assert.equal(await page.locator("#discovery-select-page").evaluate((node) => node.indeterminate), true);
    });

    await step("bulk Dismiss moves both to the dismissed view", async () => {
      const before = writes.length;
      await page.locator("#discovery-bulk-state").click();
      await message("Dismissed 2 opportunities.").waitFor();
      assert.deepEqual(writes.slice(before), ["PUT", "PUT"]);
      await listSettled(page);
      assert.equal(await rows.count(), 1);
      assert.equal(await page.locator("#discovery-bulk-bar").isHidden(), true);
      assert.equal((await page.locator("#discovery-archived-count").textContent()).trim(), "2");
    });

    await step("bulk Delete asks in-app; Escape keeps both, confirming deletes both", async () => {
      await page.locator("#discovery-archived-filter").click();
      await listSettled(page);
      await page.locator("#discovery-select-page").check();
      assert.equal((await page.locator("#discovery-bulk-count").textContent()).trim(), "2 selected");
      assert.equal((await page.locator("#discovery-bulk-state").textContent()).trim(), "Restore");
      const before = writes.length;
      await page.locator("#discovery-bulk-delete").click();
      const dialog = page.getByRole("alertdialog");
      await dialog.waitFor();
      assert.match(await dialog.textContent(), /Delete 2 opportunities\?/);
      await page.keyboard.press("Escape");
      await dialog.waitFor({ state: "hidden" });
      assert.equal(writes.length, before);
      await page.locator("#discovery-bulk-delete").click();
      await page.getByRole("alertdialog").getByRole("button", { name: "Delete permanently" }).click();
      await message("Deleted 2 opportunities.").waitFor();
      assert.deepEqual(writes.slice(before), ["DELETE", "DELETE"]);
      await listSettled(page);
      assert.equal(await rows.count(), 0);
    });

    await step("the detail panel deletes one opportunity", async () => {
      await page.locator("#discovery-archived-filter").click();
      await listSettled(page);
      await rowFor("Platform Engineer").locator("[data-discovery-id]").click();
      await page.locator("#discovery-detail h2").filter({ hasText: "Platform Engineer" }).waitFor();
      const before = writes.length;
      await page.locator("#discovery-detail [data-discovery-delete]").click();
      await page.getByRole("alertdialog").getByRole("button", { name: "Delete permanently" }).click();
      await message("Opportunity deleted.").waitFor();
      assert.deepEqual(writes.slice(before), ["DELETE"]);
      await rowFor("Platform Engineer").waitFor({ state: "detached" });
    });
  }));
