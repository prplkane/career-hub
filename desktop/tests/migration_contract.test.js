"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

function source(relativePath) {
  return fs.readFileSync(path.resolve(__dirname, "..", relativePath), "utf8");
}

test("desktop migration remains an explicit two-step loading-page action", () => {
  const main = source("src/main.js");
  const preload = source("src/preload.js");
  const loading = source("ui/loading.js");
  const html = source("ui/loading.html");

  assert.match(main, /career-hub:plan-data-home-migration/);
  assert.match(main, /career-hub:apply-data-home-migration/);
  assert.match(main, /lastStartupError !== "DATA_HOME_MIGRATION_REQUIRED"/);
  assert.match(main, /pendingMigrationPlan = plan/);
  assert.match(main, /confirm_plan: reviewedPlan\.planId/);
  assert.match(preload, /planDataHomeMigration/);
  assert.match(preload, /applyDataHomeMigration/);
  assert.match(html, /Review update/);
  assert.match(loading, /Creating a verified recovery copy/);
  assert.match(html, /id="review-migration"/);
  assert.match(html, /id="apply-migration"/);
  assert.ok(
    loading.indexOf("planDataHomeMigration") <
      loading.indexOf("applyDataHomeMigration"),
  );
});

test("desktop loading UI never accepts a migration plan id from the renderer", () => {
  const preload = source("src/preload.js");
  const main = source("src/main.js");

  assert.match(
    preload,
    /applyDataHomeMigration: \(\) =>\s*ipcRenderer\.invoke\("career-hub:apply-data-home-migration"\)/,
  );
  assert.doesNotMatch(preload, /applyDataHomeMigration: \([^)]*[a-zA-Z]/);
  assert.match(main, /const reviewedPlan = pendingMigrationPlan/);
});

test("new-job recovery keeps host authority and advances one reviewed step", () => {
  const main = source("src/main.js");
  const preload = source("src/preload.js");
  const loading = source("ui/loading.js");
  const html = source("ui/loading.html");
  const flow = source("src/recovery_flow.js");

  assert.match(main, /career-hub:plan-data-home-recovery/);
  assert.match(main, /career-hub:advance-data-home-recovery/);
  assert.match(main, /career-hub:get-data-home-recovery-availability/);
  assert.match(main, /SIDECAR_CAPABILITIES\.DATA_HOME_RECOVERY_V1/);
  assert.match(main, /lastStartupError !== "DATA_HOME_RECOVERY_REQUIRED"/);
  assert.match(
    preload,
    /getDataHomeRecoveryAvailability: \(\) =>\s*ipcRenderer\.invoke\("career-hub:get-data-home-recovery-availability"\)/,
  );
  assert.match(
    preload,
    /planDataHomeRecovery: \(\) =>\s*ipcRenderer\.invoke\("career-hub:plan-data-home-recovery"\)/,
  );
  assert.match(
    preload,
    /advanceDataHomeRecovery: \(request\) =>\s*ipcRenderer\.invoke\("career-hub:advance-data-home-recovery", request\)/,
  );
  assert.match(loading, /advanceDataHomeRecovery\(recoveryKind === "restore" \? \{ confirmWritersStopped: true \} : undefined\)/);
  assert.match(flow, /Object\.keys\(request\)\.length !== 1 \|\| request\.confirmWritersStopped !== true/);
  assert.match(flow, /confirm_plan: reviewed\.planId/);
  assert.match(flow, /const next = await this\._requestPlan\(\)/);
  assert.doesNotMatch(loading, /planId|confirm_plan|jobId|data_root/);
  assert.match(html, /id="review-recovery"/);
  assert.match(html, /id="advance-recovery"/);
  assert.match(loading, /Only this one step will run/);
});

test("email recovery uses the same trusted loading-page guards and no renderer authority", () => {
  const main = source("src/main.js"), preload = source("src/preload.js");
  const flow = source("src/recovery_flow.js"), loading = source("ui/loading.js");
  assert.match(main, /!isLoadingPageSender\(event\)/);
  assert.match(main, /startupPromise !== null/);
  assert.match(main, /supervisor\.ownsChild/);
  assert.match(preload, /advanceDataHomeRecovery: \(request\) =>/);
  assert.match(loading, /advanceDataHomeRecovery\(recoveryKind === "restore" \? \{ confirmWritersStopped: true \} : undefined\)/);
  assert.match(flow, /SIDECAR_CAPABILITIES\.EMAIL_CREDENTIAL_RECOVERY_V1/);
  assert.match(flow, /command: "data_home_recovery_inspect"/);
  assert.match(flow, /command: "email_credential_recover"/);
  assert.match(flow, /confirm: true/);
  assert.match(loading, /No automatic retry was made/);
  assert.match(loading, /repeating this action cannot repair the record/);
  assert.doesNotMatch(loading, /secret_name|data_root|resource_root|confirm_plan/);
});
