"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const PROJECT_ROOT = path.resolve(__dirname, "..");

async function main() {
  const { chromium } = require("playwright");
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ viewport: { width: 390, height: 844 } });
    await context.addInitScript(() => {
      let advanceCalls = 0;
      let availabilityCalls = 0;
      const safePlan = (nextStep) => Object.freeze({
        canAdvance: true,
        disposition: "advance",
        fileState: "complete",
        initialStatus: "applied",
        ledgerState: "assessed",
        nextStep,
        reasonCode: "JOB_INTAKE_RECOVERY_STEP_AVAILABLE",
        receiptState: "missing",
        recoveryKind: "new_job",
      });
      Object.defineProperty(window, "careerHubDesktop", {
        configurable: false,
        value: Object.freeze({
          advanceDataHomeRecovery: async () => {
            advanceCalls += 1;
            return advanceCalls === 1
              ? Object.freeze({ plan: safePlan("terminal"), settled: false, step: "assessment" })
              : Object.freeze({ settled: true, step: "terminal" });
          },
          getDataHomeRecoveryAvailability: async () => {
            availabilityCalls += 1;
            return Object.freeze({
              available:
                new URL(window.location.href).searchParams.get("native_recovery") ===
                "1",
            });
          },
          planDataHomeRecovery: async () => safePlan("assessment"),
          retryStartup: async () => Object.freeze({ accepted: true }),
        }),
      });
      window.__recoverySmoke = Object.freeze({
        advanceCalls: () => advanceCalls,
        availabilityCalls: () => availabilityCalls,
      });
    });
    const page = await context.newPage();
    const errors = [];
    page.on("console", (message) => {
      if (message.type() === "error") errors.push(message.text());
    });
    page.on("pageerror", (error) => errors.push(error.message));
    const target = pathToFileURL(path.join(PROJECT_ROOT, "desktop", "ui", "loading.html"));
    target.searchParams.set("state", "error");
    target.searchParams.set("error_code", "DATA_HOME_RECOVERY_REQUIRED");
    target.searchParams.set("native_recovery", "1");
    await page.goto(target.toString(), { waitUntil: "load" });

    await page.locator("#recovery-panel").waitFor({ state: "visible" });
    assert.equal(await page.locator("#migration-panel").isVisible(), false);
    assert.equal(await page.locator("#retry-startup").isVisible(), false);
    await page.locator("#review-recovery").click();
    await page.getByText("Next verified step: restore the saved assessment.", { exact: true }).waitFor();
    await page.locator("#advance-recovery").click();
    await page.getByText("Next verified step: finish and verify recovery.", { exact: true }).waitFor();
    assert.equal(await page.evaluate(() => window.__recoverySmoke.advanceCalls()), 1);
    await page.locator("#advance-recovery").click();
    await page.getByText("Recovery complete. Opening Career Hub…", { exact: true }).waitFor();
    assert.equal(await page.evaluate(() => window.__recoverySmoke.advanceCalls()), 2);
    assert.equal(await page.evaluate(() => window.__recoverySmoke.availabilityCalls()), 1);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true);
    assert.deepEqual(errors, []);

    const legacyPage = await context.newPage();
    const legacyTarget = pathToFileURL(
      path.join(PROJECT_ROOT, "desktop", "ui", "loading.html"),
    );
    legacyTarget.searchParams.set("state", "error");
    legacyTarget.searchParams.set("error_code", "DATA_HOME_RECOVERY_REQUIRED");
    await legacyPage.goto(legacyTarget.toString(), { waitUntil: "load" });
    await legacyPage.waitForFunction(
      () => window.__recoverySmoke.availabilityCalls() === 1,
    );
    assert.equal(await legacyPage.locator("#recovery-panel").isVisible(), false);
    assert.equal(await legacyPage.locator("#retry-startup").isVisible(), true);
    assert.equal(
      await legacyPage.locator("#startup-message").textContent(),
      "Career Hub’s local service did not start. Your data was not removed or reset.",
    );
    await context.close();
  } finally {
    await browser.close();
  }
  process.stdout.write("desktop recovery loading smoke passed\n");
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
