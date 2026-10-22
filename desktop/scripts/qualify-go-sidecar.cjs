#!/usr/bin/env node
"use strict";

const assert = require("node:assert/strict");
const childProcess = require("node:child_process");
const crypto = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const {
  validateSidecarDeployment,
} = require("../src/app_paths");
const { GO_AGENT_CLIENT, GO_RUNTIME_CAPABILITIES, verifyGoSidecarPackageInput } = require("../src/package_manifest");
const { SidecarSupervisor } = require("../src/sidecar");

const desktopRoot = path.resolve(__dirname, "..");
const projectRoot = path.resolve(desktopRoot, "..");
const artifactRoot = path.join(
  projectRoot,
  "packaging",
  "dist",
  "go-onedir",
  "career-hub-sidecar",
);
const executablePath = path.join(artifactRoot, "career-hub-sidecar.exe");
const manifestPath = path.join(artifactRoot, "sidecar-manifest.json");
const manifestSha256Path = path.join(artifactRoot, "sidecar-manifest.sha256");

if (process.argv.length !== 2) {
  throw new Error("Usage: qualify-go-sidecar.cjs");
}
if (process.platform !== "win32" || process.arch !== "x64") {
  throw new Error("GO_SIDECAR_WINDOWS_X64_REQUIRED");
}

function gitOutput(args) {
  return childProcess.execFileSync("git", args, {
    cwd: projectRoot,
    encoding: "utf8",
    maxBuffer: 1024 * 1024,
    stdio: ["ignore", "pipe", "ignore"],
  });
}

function sha256File(filePath) {
  return crypto.createHash("sha256").update(fs.readFileSync(filePath)).digest("hex");
}

function write(root, logicalPath, content) {
  const candidate = path.join(root, ...logicalPath.split("/"));
  fs.mkdirSync(path.dirname(candidate), { recursive: true });
  fs.writeFileSync(candidate, content, { flag: "wx", mode: 0o600 });
}

function snapshot(root) {
  const result = [];
  function visit(candidate) {
    const metadata = fs.lstatSync(candidate);
    assert.equal(metadata.isSymbolicLink(), false);
    const relative = path.relative(root, candidate).split(path.sep).join("/");
    if (metadata.isDirectory()) {
      result.push({ path: relative, type: "directory" });
      for (const name of fs.readdirSync(candidate).sort()) {
        visit(path.join(candidate, name));
      }
      return;
    }
    assert.equal(metadata.isFile(), true);
    result.push({
      path: relative,
      sha256: sha256File(candidate),
      size: metadata.size,
      type: "file",
    });
  }
  visit(root);
  return result;
}

function directorySize(root) {
  return snapshot(root)
    .filter((entry) => entry.type === "file")
    .reduce((total, entry) => total + entry.size, 0);
}

async function health(origin) {
  const response = await fetch(`${origin}/api/health`, {
    redirect: "error",
    signal: AbortSignal.timeout(5_000),
  });
  assert.equal(response.status, 200);
  const payload = await response.json();
  assert.deepEqual(payload, {
    app_version: appVersion,
    protocol_version: 1,
    status: "ok",
  });
}

const appVersion = fs.readFileSync(path.join(projectRoot, "VERSION"), "utf8").trimEnd();
const sourceCommit = gitOutput(["rev-parse", "HEAD"]).trim();
if (gitOutput(["status", "--porcelain", "--untracked-files=normal"]).length > 0) {
  throw new Error("GO_SIDECAR_QUALIFICATION_REQUIRES_CLEAN_SOURCE");
}
const manifest = verifyGoSidecarPackageInput({
  appVersion,
  artifactRoot,
  expectedSourceCommit: sourceCommit,
  platform: "win32",
  projectRoot,
  requireClean: true,
});
const deployment = validateSidecarDeployment(
  {
    deploymentRoot: artifactRoot,
    executablePath,
    manifestPath,
    manifestSha256Path,
  },
  { appVersion, platform: "win32", goPreview: true },
);
assert.deepEqual(deployment.runtimeCapabilities, GO_RUNTIME_CAPABILITIES);
assert.equal(deployment.agentClient.executablePath, path.join(artifactRoot, GO_AGENT_CLIENT.executable));
const agentHelp = childProcess.execFileSync(deployment.agentClient.executablePath, ["--help"], {
  encoding: "utf8", timeout: 5000, maxBuffer: 8192, windowsHide: true,
});
assert.equal(agentHelp.match(/^Commands: (.+)$/m)?.[1], GO_AGENT_CLIENT.commands.join(", "));

const fixtureRoot = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-go-package-"));
const maintenanceHome = path.join(fixtureRoot, "maintenance-home");
const runtimeHome = path.join(fixtureRoot, "runtime-home");
fs.mkdirSync(maintenanceHome);
fs.mkdirSync(runtimeHome);
write(
  maintenanceHome,
  ".career_hub_data_home.json",
  `${JSON.stringify({ application: "CareerHub", channel: "dev", format_version: 1 })}\n`,
);
write(
  runtimeHome,
  ".career_hub_data_home.json",
  `${JSON.stringify({
    application: "CareerHub",
    channel: "dev",
    data_home_id: `dh_${"1".repeat(32)}`,
    format_version: 2,
  })}\n`,
);
write(
  runtimeHome,
  "career_vault/taxonomy/skill_groups.yaml",
  "- {id: user_automation, name: My automation}\n",
);
write(
  runtimeHome,
  "career_vault/taxonomy/technologies.yaml",
  "- {id: user_vba, name: VBA, category: tool, primary_group_id: user_automation, aliases: [Excel VBA]}\n",
);

const maintenanceBefore = snapshot(maintenanceHome);
const runtimeBefore = snapshot(runtimeHome);
const supervisor = new SidecarSupervisor();
let runtimeStarted = false;

async function main() {
  try {
    const plan = await supervisor.runMaintenance({
      deploymentRoot: artifactRoot,
      executablePath,
      expectedEvent: "document_migration_plan",
      record: {
        command: "document_migration_plan",
        data_root: maintenanceHome,
        protocol_version: 1,
      },
    });
    assert.deepEqual(plan, {
      ciphertextFileCount: 0,
      documentCount: 0,
      planId: plan.planId,
      sourceFormatVersion: 1,
      targetFormatVersion: 2,
      totalCiphertextBytes: 0,
    });
    assert.match(plan.planId, /^sha256:[0-9a-f]{64}$/);
    assert.deepEqual(snapshot(maintenanceHome), maintenanceBefore);

    const ready = await supervisor.start({
      appVersion,
      channel: "dev",
      dataRoot: runtimeHome,
      deploymentRoot: artifactRoot,
      executablePath,
    });
    runtimeStarted = true;
    await health(ready.origin);
    const refusedAgent = childProcess.spawnSync(deployment.agentClient.executablePath,
      ["prepare", "--server", ready.origin, "--job-id", "synthetic-missing"],
      { encoding: "utf8", timeout: 10000, maxBuffer: 8192, windowsHide: true });
    assert.equal(refusedAgent.status, 1);
    assert.equal(refusedAgent.stderr, "");
    assert.equal(JSON.parse(refusedAgent.stdout).error_code, "AGENT_MATERIAL_CAPABILITY_REQUIRED");
    await supervisor.stop();
    runtimeStarted = false;
    assert.equal(supervisor.ownsChild, false);
    assert.deepEqual(snapshot(runtimeHome), runtimeBefore);
    verifyGoSidecarPackageInput({
      appVersion,
      artifactRoot,
      expectedSourceCommit: sourceCommit,
      platform: "win32",
      projectRoot,
      requireClean: true,
    });

    process.stdout.write(
      `${JSON.stringify({
        architecture: manifest.architecture,
        artifact_size_bytes: directorySize(artifactRoot),
        event: "go_sidecar_package_qualified",
        executable_sha256: sha256File(executablePath),
        agent_sha256: sha256File(deployment.agentClient.executablePath),
        agent_contract_version: deployment.agentClient.contractVersion,
        file_count: manifest.files.length,
        go_version: manifest.go_version,
        resource_count: manifest.resources.length,
        runtime_capabilities: manifest.runtime_capabilities,
        source_commit: sourceCommit,
      })}\n`,
    );
  } finally {
    if (runtimeStarted || supervisor.ownsChild) {
      await supervisor.stop().catch(() => {});
    }
    assert.equal(path.dirname(fixtureRoot), path.resolve(os.tmpdir()));
    assert.match(path.basename(fixtureRoot), /^career-hub-go-package-/);
    fs.rmSync(fixtureRoot, { force: true, recursive: true });
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
