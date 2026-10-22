"use strict";

const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const {
  GO_RUNTIME_CAPABILITIES,
  GO_AGENT_CLIENT,
  GO_MANIFEST_SCHEMA_VERSION,
  PackageManifestError,
  collectGoProjectResourceEntries,
  verifyGoSidecarPackageInput,
  writeGoSidecarManifest,
} = require("../src/package_manifest");
const { validateSidecarDeployment, AppPathError } = require("../src/app_paths");

function temporaryDirectory(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-go-package-"));
  t.after(() => fs.rmSync(root, { force: true, recursive: true }));
  return root;
}

function write(root, logicalPath, content) {
  const candidate = path.join(root, ...logicalPath.split("/"));
  fs.mkdirSync(path.dirname(candidate), { recursive: true });
  fs.writeFileSync(candidate, content);
  return candidate;
}

function rewriteManifest(artifactRoot, mutate) {
  const manifestPath = path.join(artifactRoot, "sidecar-manifest.json");
  const checksumPath = path.join(artifactRoot, "sidecar-manifest.sha256");
  const payload = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
  mutate(payload);
  const encoded = `${JSON.stringify(payload, null, 2)}\n`;
  fs.writeFileSync(manifestPath, encoded);
  const checksum = crypto.createHash("sha256").update(encoded).digest("hex");
  fs.writeFileSync(checksumPath, `${checksum}  sidecar-manifest.json\n`, "ascii");
}

function fixture(t) {
  const root = temporaryDirectory(t);
  const projectRoot = path.join(root, "project");
  const artifactRoot = path.join(root, "artifact");
  fs.mkdirSync(projectRoot);
  fs.mkdirSync(artifactRoot);
  write(projectRoot, "VERSION", "1.2.3\n");
  write(projectRoot, "backend/go.mod", "module example.invalid/test\n\ngo 1.27.2\n");
  write(projectRoot, "backend/go.sum", "example.invalid/module v1.0.0 h1:test\n");
  write(projectRoot, "src/data/job_technologies.yaml", "[]\n");
  write(projectRoot, "src/data/requirement_concepts.yaml", "[]\n");
  write(projectRoot, "src/data/skill_groups.yaml", "[]\n");
  write(projectRoot, "src/intake_ui_static/app.js", "console.log('fixture');\n");
  for (const resource of collectGoProjectResourceEntries(projectRoot)) {
    const source = path.join(projectRoot, ...resource.path.split("/"));
    const destination = path.join(artifactRoot, ...resource.path.split("/"));
    fs.mkdirSync(path.dirname(destination), { recursive: true });
    fs.copyFileSync(source, destination);
  }
  write(artifactRoot, "career-hub-sidecar.exe", "fictional executable\n");
  write(artifactRoot, GO_AGENT_CLIENT.executable, "fictional agent executable\n");
  writeGoSidecarManifest({
    appVersion: "1.2.3",
    artifactRoot,
    goVersion: "go1.27.3",
    projectRoot,
    sourceCommit: "abc123",
    sourceTreeDirty: false,
  });
  return { artifactRoot, projectRoot };
}

function verify(value, overrides = {}) {
  return verifyGoSidecarPackageInput({
    ...value,
    appVersion: "1.2.3",
    expectedSourceCommit: "abc123",
    platform: "win32",
    requireClean: true,
    ...overrides,
  });
}

test("Go package manifest binds the exact executable, resources, and capability", (t) => {
  const value = fixture(t);
  const manifest = verify(value);
  assert.equal(manifest.runtime, "go");
  assert.equal(manifest.layout, "go-onedir");
  assert.equal(manifest.schema_version, GO_MANIFEST_SCHEMA_VERSION);
  assert.deepEqual(manifest.agent_client, GO_AGENT_CLIENT);
  assert.deepEqual(manifest.runtime_capabilities, GO_RUNTIME_CAPABILITIES);
  assert.equal(
    manifest.files.some((entry) => entry.path.startsWith("_internal/")),
    false,
  );
});

function runtime(value) {
  return validateSidecarDeployment({
    deploymentRoot: value.artifactRoot,
    executablePath: path.join(value.artifactRoot, "career-hub-sidecar.exe"),
    manifestPath: path.join(value.artifactRoot, "sidecar-manifest.json"),
    manifestSha256Path: path.join(value.artifactRoot, "sidecar-manifest.sha256"),
  }, { appVersion: "1.2.3", platform: "win32", goPreview: true });
}

test("Go runtime verifies the agent before exposing its packaged path", (t) => {
  const value = fixture(t);
  assert.deepEqual(runtime(value).agentClient, {
    executablePath: path.join(value.artifactRoot, GO_AGENT_CLIENT.executable),
    contractVersion: 4,
    commands: GO_AGENT_CLIENT.commands,
  });
  fs.appendFileSync(path.join(value.artifactRoot, GO_AGENT_CLIENT.executable), "tampered");
  assert.throws(() => runtime(value), (e) => e instanceof AppPathError && e.code === "DESKTOP_AGENT_MANIFEST_INVALID");
  assert.throws(() => verify(value), (e) => e.code === "GO_SIDECAR_ARTIFACT_CONTENT_MISMATCH");
  fs.unlinkSync(path.join(value.artifactRoot, GO_AGENT_CLIENT.executable));
  assert.throws(() => runtime(value), (e) => e.code === "DESKTOP_AGENT_RESOURCE_INVALID");
});

test("Go package refuses missing or incompatible client contracts even with a valid checksum", (t) => {
  for (const mode of ["old-schema", "missing-contract", "wrong-path", "wrong-version", "missing-command", "missing-binary", "duplicate-binary"]) {
    const value = fixture(t);
    rewriteManifest(value.artifactRoot, (manifest) => {
      if (mode === "old-schema") manifest.schema_version = 2;
      if (mode === "missing-contract") delete manifest.agent_client;
      if (mode === "wrong-path") manifest.agent_client.executable = "../outside.exe";
      if (mode === "wrong-version") manifest.agent_client.contract_version = 5;
      if (mode === "missing-command") manifest.agent_client.commands.pop();
      if (mode === "missing-binary") manifest.files = manifest.files.filter((r) => r.path !== GO_AGENT_CLIENT.executable);
      if (mode === "duplicate-binary") manifest.files.push(manifest.files.find((r) => r.path === GO_AGENT_CLIENT.executable));
    });
    assert.throws(() => verify(value), (e) => e.code === "GO_SIDECAR_MANIFEST_INVALID", mode);
    assert.throws(() => runtime(value), AppPathError, mode);
  }
});

test("Go package verification rejects artifact and source-resource drift", (t) => {
  const artifactDrift = fixture(t);
  fs.appendFileSync(
    path.join(artifactDrift.artifactRoot, "career-hub-sidecar.exe"),
    "changed",
  );
  assert.throws(
    () => verify(artifactDrift),
    (error) =>
      error instanceof PackageManifestError &&
      error.code === "GO_SIDECAR_ARTIFACT_CONTENT_MISMATCH",
  );

  const sourceDrift = fixture(t);
  fs.appendFileSync(
    path.join(sourceDrift.projectRoot, "src", "intake_ui_static", "app.js"),
    "changed",
  );
  assert.throws(
    () => verify(sourceDrift),
    (error) =>
      error instanceof PackageManifestError &&
      error.code === "GO_SIDECAR_PROJECT_RESOURCE_MISMATCH",
  );

  const moduleDrift = fixture(t);
  fs.appendFileSync(
    path.join(moduleDrift.projectRoot, "backend", "go.mod"),
    "// changed\n",
  );
  assert.throws(
    () => verify(moduleDrift),
    (error) =>
      error instanceof PackageManifestError &&
      error.code === "GO_SIDECAR_MODULE_INPUT_MISMATCH",
  );
});

test("Go package verification rejects unreviewed capabilities and target metadata", (t) => {
  const capability = fixture(t);
  rewriteManifest(capability.artifactRoot, (manifest) => {
    manifest.runtime_capabilities = ["unreviewed_capability"];
  });
  assert.throws(
    () => verify(capability),
    (error) =>
      error instanceof PackageManifestError &&
      error.code === "GO_SIDECAR_MANIFEST_INVALID",
  );

  const platform = fixture(t);
  assert.throws(
    () => verify(platform, { platform: "darwin" }),
    (error) =>
      error instanceof PackageManifestError &&
      error.code === "GO_SIDECAR_MANIFEST_INVALID",
  );

  const privatePath = fixture(t);
  rewriteManifest(privatePath.artifactRoot, (manifest) => {
    manifest.go_version = privatePath.projectRoot;
  });
  assert.throws(
    () => verify(privatePath),
    (error) =>
      error instanceof PackageManifestError &&
      error.code === "GO_SIDECAR_MANIFEST_INVALID",
  );
});

test("Go package writer refuses an unpinned toolchain", (t) => {
  const value = fixture(t);
  fs.rmSync(path.join(value.artifactRoot, "sidecar-manifest.json"));
  fs.rmSync(path.join(value.artifactRoot, "sidecar-manifest.sha256"));
  assert.throws(
    () =>
      writeGoSidecarManifest({
        appVersion: "1.2.3",
        artifactRoot: value.artifactRoot,
        goVersion: "go1.28.0",
        projectRoot: value.projectRoot,
        sourceCommit: "abc123",
        sourceTreeDirty: false,
      }),
    (error) =>
      error instanceof PackageManifestError &&
      error.code === "GO_SIDECAR_TOOLCHAIN_INVALID",
  );
});

test("Go package verification rejects a checksum-valid malformed manifest", (t) => {
  const value = fixture(t);
  const malformed = "{not-json\n";
  fs.writeFileSync(
    path.join(value.artifactRoot, "sidecar-manifest.json"),
    malformed,
  );
  fs.writeFileSync(
    path.join(value.artifactRoot, "sidecar-manifest.sha256"),
    `${crypto.createHash("sha256").update(malformed).digest("hex")}  sidecar-manifest.json\n`,
    "ascii",
  );
  assert.throws(
    () => verify(value),
    (error) =>
      error instanceof PackageManifestError &&
      error.code === "SIDECAR_MANIFEST_INVALID",
  );
});
