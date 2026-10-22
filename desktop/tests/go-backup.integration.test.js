"use strict";

const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawn } = require("node:child_process");
const test = require("node:test");
const { SidecarSupervisor } = require("../src/sidecar");

const executablePath = process.env.CAREERHUB_GO_SIDECAR || "";
const expectedDigest = process.env.CAREERHUB_GO_SIDECAR_SHA256 || "";
const digest = (file) => crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex");

test("real Go backup and restore lifecycle preserve and resume the same synthetic Home",
  { skip: !executablePath || !expectedDigest, timeout: 60_000 }, async () => {
    assert.equal(path.isAbsolute(executablePath), true);
    assert.equal(digest(executablePath), expectedDigest);
    const temporary = fs.mkdtempSync(path.join(os.tmpdir(), "careerhub-backup-supervisor-"));
    const resources = path.join(temporary, "resources"), home = path.join(temporary, "home");
    const files = {
      "VERSION": "1.2.3\n", "src/intake_ui_static/index.html": "<!doctype html><title>Synthetic UI</title>",
      "src/data/skill_groups.yaml": "[]\n", "src/data/job_technologies.yaml": "[]\n",
    };
    for (const [name, value] of Object.entries(files)) {
      const target = path.join(resources, name);
      fs.mkdirSync(path.dirname(target), { recursive: true });
      fs.writeFileSync(target, value, { flag: "wx" });
    }
    const live = new Set(), launches = [];
    const supervisor = new SidecarSupervisor({
      spawn(executable, args, options) {
        assert.equal(live.size, 0, "overlapping writer/maintenance process");
        assert.equal(executable, executablePath);
        assert.equal(options.cwd, resources);
        const child = spawn(executable, args, options);
        live.add(child);
        child.once("exit", () => live.delete(child));
        launches.push(args[0]);
        return child;
      },
      startupTimeoutMs: 15_000, backupTimeoutMs: 30_000, restoreTimeoutMs: 30_000,
    });
    try {
      await supervisor.start({ executablePath, deploymentRoot: resources, dataRoot: home,
        channel: "dev", appVersion: "1.2.3", mode: "go-bootstrap" });
      const markerPath = path.join(home, ".career_hub_data_home.json");
      const markerDigest = digest(markerPath);
      fs.writeFileSync(path.join(home, "jobs", "synthetic.txt"), "synthetic retained posting", { flag: "wx" });
      const result = await supervisor.createBackup({ confirmWritersStopped: true, archiveRelativePath: "backups/native.zip" });
      assert.equal(result.backendRestarted, true);
      assert.equal(result.backup.archivePublication, "published");
      assert.equal(supervisor.state, "ready");
      assert.equal(supervisor.backupInProgress, false);
      const archive = path.join(home, "backups", "native.zip"), archiveDigest = digest(archive);
      // Archive structure and checksums are verified by the Go homebackup tests.
      const health = await fetch(`${result.origin}/api/health`, { signal: AbortSignal.timeout(3000) });
      assert.equal(result.backup.snapshotCleanup.status, "complete");
      assert.equal(result.backup.snapshotCleanup.removedFiles, result.backup.stagedFiles);
      assert.equal(fs.existsSync(path.join(home, result.backup.snapshotRelativePath)), false);
      assert.equal(Object.isFrozen(result.backup.snapshotCleanup), true);
      assert.equal(health.status, 200);
      await assert.rejects(supervisor.createBackup({ confirmWritersStopped: true, archiveRelativePath: "backups/native.zip" }), (error) => {
        assert.equal(error.details.reportedCode, "BACKUP_DESTINATION_EXISTS");
        assert.equal(error.details.backup.archivePublication, "not_published");
        assert.equal(error.details.backendRestarted, true);
        return true;
      });
      assert.deepEqual(launches, ["--sidecar-bootstrap", "--desktop-maintenance", "--sidecar-bootstrap", "--desktop-maintenance", "--sidecar-bootstrap"]);
      await assert.rejects(supervisor.createBackup({ confirmWritersStopped: false }), { code: "DESKTOP_BACKUP_REQUEST_INVALID" });
      assert.equal(launches.length, 5);
      assert.equal(digest(archive), archiveDigest);
      assert.equal(digest(markerPath), markerDigest);
      assert.equal(fs.readFileSync(path.join(home, "jobs", "synthetic.txt"), "utf8"), "synthetic retained posting");
      assert.equal(fs.existsSync(path.join(home, "documents", "key_state.json")), false);

      // Change only this disposable fixture while its writer is stopped, then
      // exercise the supervisor's complete stop/restore/restart lifecycle.
      await supervisor.stop();
      assert.equal(live.size, 0);
      const posting = path.join(home, "jobs", "synthetic.txt");
      fs.writeFileSync(posting, "newer synthetic posting");
      await supervisor.start({ executablePath, deploymentRoot: resources, dataRoot: home,
        channel: "dev", appVersion: "1.2.3", mode: "go-bootstrap" });
      const resultRestore = await supervisor.restoreBackup({ confirmWritersStopped: true, archiveRelativePath: "backups/native.zip" });
      const restored = resultRestore.restore;
      assert.equal(restored.success, true);
      assert.equal(restored.complete, true);
      for (const phase of [restored.initialValidation, restored.archiveValidation, restored.finalValidation]) {
        assert.equal(phase.cleanup.status, "complete");
        assert.equal(phase.cleanup.removedFiles, phase.cleanup.files);
        assert.equal(phase.cleanup.snapshotRemoval, "removed");
        assert.equal(Object.isFrozen(phase.cleanup), true);
        assert.equal(fs.existsSync(path.join(home, phase.snapshotRelativePath)), false);
      }
      assert.equal(restored.journalRemoval, "removed");
      assert.equal(restored.finalValidation.validated, true);
      assert.equal(restored.archiveValidation.validated, true);
      assert.equal(restored.archiveValidation.namespace, "created");
      assert.match(restored.archiveValidation.snapshotRelativePath, /^\.career_hub_staging\/backups\/[a-f0-9]{32}$/);
      assert.equal(Object.isFrozen(restored.archiveValidation), true);
      assert.equal(Object.isFrozen(restored.roots), true);
      assert.equal(live.size, 1);
      assert.equal(supervisor.state, "ready");
      assert.equal(resultRestore.backendRestarted, true);
      assert.equal((await fetch(`${resultRestore.origin}/api/health`, { signal: AbortSignal.timeout(3000) })).status, 200);
      assert.equal(fs.readFileSync(posting, "utf8"), "synthetic retained posting");
      assert.equal(fs.readFileSync(path.join(home, restored.recoveryRelativePath, "jobs", "synthetic.txt"), "utf8"), "newer synthetic posting");
      assert.equal(digest(archive), archiveDigest);
      assert.equal(digest(markerPath), markerDigest);
      await assert.rejects(supervisor.restoreBackup({ confirmWritersStopped: true, archiveRelativePath: "backups/missing.zip" }), (error) => {
        assert.equal(error.details.restore.complete, false);
        assert.equal(error.details.restore.journalRemoval, "not_removed");
        assert.equal(error.details.backendRestarted, false);
        return true;
      });
      assert.equal(live.size, 0);
      assert.equal(supervisor.state, "idle", "failed restore must not restart implicitly");
      assert.equal(supervisor.canAttemptRestoreRecovery, true);
      await assert.rejects(supervisor.recoverRestore({ confirmWritersStopped: true }), (error) => {
        assert.equal(error.details.reportedCode, "DATA_HOME_RECOVERY_NOT_REQUIRED");
        assert.equal(error.details.restore.recovery, true);
        assert.equal(error.details.restore.journalRemoval, "not_removed");
        assert.equal(error.details.restore.roots.completedMoves, 0);
        return true;
      });
      const restarted = await supervisor.start({ executablePath, deploymentRoot: resources, dataRoot: home,
        channel: "dev", appVersion: "1.2.3", mode: "go-bootstrap" });
      assert.equal((await fetch(`${restarted.origin}/api/health`, { signal: AbortSignal.timeout(3000) })).status, 200);
      assert.deepEqual(launches.slice(5), ["--sidecar-bootstrap", "--desktop-maintenance", "--sidecar-bootstrap",
        "--desktop-maintenance", "--desktop-maintenance", "--sidecar-bootstrap"]);
      assert.equal(supervisor.canAttemptRestoreRecovery, false);
      assert.equal(fs.existsSync(path.join(home, "documents", "key_state.json")), false);
    } finally {
      await supervisor.stop();
      assert.equal(live.size, 0);
      assert.equal(path.dirname(temporary), path.resolve(os.tmpdir()));
      assert.ok(path.basename(temporary).startsWith("careerhub-backup-supervisor-"));
      // Only this call's mkdtemp fixture, after every owned process has exited.
      fs.rmSync(temporary, { recursive: true, force: true });
    }
    assert.equal(digest(executablePath), expectedDigest);
  });
