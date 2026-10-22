"use strict";

const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const { execFileSync } = require("node:child_process");

const { SidecarSupervisor } = require("../src/sidecar");

function sha256(file) {
  return crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex");
}

const executablePath = process.env.CAREERHUB_GO_SIDECAR || "";
const expectedDigest = process.env.CAREERHUB_GO_SIDECAR_SHA256 || "";
const fixtureExecutable = process.env.CAREERHUB_GO_STARTUP_FIXTURE || "";

test("real Go child inspects and settles an absent synthetic credential without creating one",
  { skip: !executablePath || !expectedDigest || !fixtureExecutable }, async (t) => {
    assert.equal(sha256(executablePath), expectedDigest);
    assert.equal(path.isAbsolute(fixtureExecutable), true);
    const root = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-email-maintenance-"));
    t.after(() => fs.rmSync(root, { force: true, recursive: true }));
    const runtime = path.join(root, "runtime");
    fs.mkdirSync(path.join(runtime, "src", "data"), { recursive: true });
    fs.writeFileSync(path.join(runtime, "VERSION"), "1.2.3\n");
    fs.writeFileSync(path.join(runtime, "src", "intake_ui_static"), "synthetic presence sentinel");
    fs.writeFileSync(path.join(runtime, "src", "data", "skill_groups.yaml"), "[]\n");
    fs.writeFileSync(path.join(runtime, "src", "data", "job_technologies.yaml"), "- {id: core, name: Core, category: tool}\n");
    const supervisor = new SidecarSupervisor({ maintenanceTimeoutMs: 30_000 });
    t.after(() => supervisor.stop().catch(() => {}));
    const run = (home, command, expectedEvent, confirm) => supervisor.runMaintenance({ executablePath, deploymentRoot: runtime,
      expectedEvent, record: { command, protocol_version: 1, data_root: home, resource_root: runtime, channel: "dev",
        ...(confirm === undefined ? {} : { confirm }) } });
    for (const mode of ["before", "copied", "legacy", "mixed"]) {
      let home = path.join(root, mode);
      execFileSync(fixtureExecutable, ["-test.run=^TestEmailMaintenanceNativeHelper$"], { timeout: 30_000, windowsHide: true,
        env: { ...process.env, CH_EMAIL_MAINT_STAGE: "prepare", CH_EMAIL_MAINT_MODE: mode,
          CH_EMAIL_MAINT_HOME: home, CH_EMAIL_MAINT_RES: runtime } });
      if (mode === "copied") {
        const copy = `${home}-copy`;
        fs.cpSync(home, copy, { recursive: true });
        home = copy;
      }
      const db = path.join(home, "applications", "career_hub.sqlite3");
      const before = sha256(db);
      if (mode === "mixed") {
        await assert.rejects(run(home, "data_home_recovery_inspect", "data_home_recovery_inspected"),
          (error) => error.details.reportedCode === "DATA_HOME_RECOVERY_AMBIGUOUS");
      } else {
        assert.deepEqual(await run(home, "data_home_recovery_inspect", "data_home_recovery_inspected"), { recoveryKind: "email_credential" });
        if (mode === "before") {
          // The target is freshly randomized by the Go fixture and was never
          // written to the OS store. Recovery may observe absence, never create it.
          const result = await run(home, "email_credential_recover", "email_credential_recovered", true);
          assert.equal(result.success, true);
          assert.equal(result.cleanup, "none");
          assert.equal(result.cleanupAttempted, false);
          assert.equal(result.intentRemoved, true);
          await assert.rejects(run(home, "data_home_recovery_inspect", "data_home_recovery_inspected"),
            (error) => error.details.reportedCode === "DATA_HOME_RECOVERY_NOT_REQUIRED");
          execFileSync(fixtureExecutable, ["-test.run=^TestEmailMaintenanceNativeHelper$"], { timeout: 30_000, windowsHide: true,
            env: { ...process.env, CH_EMAIL_MAINT_STAGE: "restart", CH_EMAIL_MAINT_MODE: mode,
              CH_EMAIL_MAINT_HOME: home, CH_EMAIL_MAINT_RES: runtime } });
        } else {
          await assert.rejects(run(home, "email_credential_recover", "email_credential_recovered", true), (error) => {
            assert.equal(error.details.recovery.success, false);
            assert.equal(error.details.recovery.cleanupAttempted, false);
            assert.equal(error.details.recovery.intentRemoved, false);
            return true;
          });
        }
      }
      assert.equal(supervisor.ownsChild, false);
      assert.equal(sha256(db), before);
    }
  });

test(
  "Electron supervisor runs the reviewed Go maintenance child on fictional data",
  { skip: !executablePath || !expectedDigest },
  async (t) => {
    assert.equal(path.isAbsolute(executablePath), true);
    assert.match(expectedDigest, /^[0-9a-f]{64}$/);
    assert.equal(sha256(executablePath), expectedDigest);

    const fixtureRoot = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-go-maintenance-"));
    const dataRoot = path.join(fixtureRoot, "data");
    const deploymentRoot = path.join(fixtureRoot, "runtime");
    assert.equal(path.dirname(path.resolve(fixtureRoot)), path.resolve(os.tmpdir()));
    assert.match(path.basename(fixtureRoot), /^career-hub-go-maintenance-/);
    fs.mkdirSync(dataRoot);
    fs.mkdirSync(deploymentRoot);
    fs.writeFileSync(
      path.join(dataRoot, ".career_hub_data_home.json"),
      `${JSON.stringify({ application: "CareerHub", channel: "dev", format_version: 1 })}\n`,
      { mode: 0o600 },
    );
    t.after(() => fs.rmSync(fixtureRoot, { force: true, recursive: true }));

    const supervisor = new SidecarSupervisor({ maintenanceTimeoutMs: 30_000 });
    t.after(async () => supervisor.stop().catch(() => {}));
    const result = await supervisor.runMaintenance({
      deploymentRoot,
      executablePath,
      expectedEvent: "document_migration_plan",
      record: {
        command: "document_migration_plan",
        data_root: dataRoot,
        protocol_version: 1,
      },
    });
    assert.deepEqual(result, {
      ciphertextFileCount: 0,
      documentCount: 0,
      planId: result.planId,
      sourceFormatVersion: 1,
      targetFormatVersion: 2,
      totalCiphertextBytes: 0,
    });
    assert.match(result.planId, /^sha256:[0-9a-f]{64}$/);
    assert.equal(supervisor.ownsChild, false);
  },
);
