"use strict";

const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const { GO_AGENT_CLIENT, GO_MANIFEST_SCHEMA_VERSION } = require("../src/package_manifest");

const {
  AppPathError,
  canonicalGoSidecarPaths,
  ensurePrivateDirectory,
  resolveProductPaths,
  resolveSidecarPaths,
  SIDECAR_CAPABILITIES,
  validateSidecarDeployment,
} = require("../src/app_paths");

function temporaryDirectory(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-paths-"));
  t.after(() => fs.rmSync(root, { force: true, recursive: true }));
  return root;
}

test("Windows and macOS data homes follow the accepted channel contract", () => {
  assert.deepEqual(
    resolveProductPaths({
      channel: "stable",
      platform: "win32",
      electronPaths: { appData: "C:\\Users\\Test\\AppData\\Roaming" },
      environment: { LOCALAPPDATA: "C:\\Users\\Test\\AppData\\Local" },
    }),
    {
      channel: "stable",
      dataRoot: "C:\\Users\\Test\\AppData\\Local\\CareerHub\\stable",
      electronSessionRoot:
        "C:\\Users\\Test\\AppData\\Local\\CareerHub\\.electron\\stable\\session",
      electronUserDataRoot:
        "C:\\Users\\Test\\AppData\\Local\\CareerHub\\.electron\\stable\\user-data",
      productRoot: "C:\\Users\\Test\\AppData\\Local\\CareerHub",
    },
  );
  assert.equal(
    resolveProductPaths({
      channel: "dev",
      platform: "darwin",
      electronPaths: { appData: "/Users/Test/Library/Application Support" },
      environment: {},
    }).dataRoot,
    "/Users/Test/Library/Application Support/CareerHub/dev",
  );
});

test("unsupported installed platforms and invalid channels fail closed", () => {
  assert.throws(
    () =>
      resolveProductPaths({
        channel: "stable",
        platform: "linux",
        electronPaths: { appData: "/tmp/test" },
      }),
    (error) =>
      error instanceof AppPathError && error.code === "DESKTOP_PLATFORM_UNSUPPORTED",
  );
  assert.throws(
    () =>
      resolveProductPaths({
        channel: "nightly",
        platform: "darwin",
        electronPaths: { appData: "/tmp/test" },
      }),
    (error) =>
      error instanceof AppPathError && error.code === "DESKTOP_CHANNEL_INVALID",
  );
});

test("Go preview isolates data, Electron storage and artifact without changing defaults", () => {
  const options = { channel: "dev", platform: "win32", electronPaths: {},
    environment: { LOCALAPPDATA: "C:\\Users\\Test\\AppData\\Local" } };
  const regular = resolveProductPaths(options);
  const preview = resolveProductPaths({ ...options, goPreview: true });
  for (const key of ["dataRoot", "electronSessionRoot", "electronUserDataRoot"]) {
    assert.notEqual(preview[key], regular[key]);
    assert.match(preview[key], /dev-go-preview/);
  }
  for (const channel of ["beta", "stable"]) {
    assert.throws(() => resolveProductPaths({ ...options, channel, goPreview: true }), AppPathError);
  }
  const runtime = { isPackaged: false, platform: "win32", projectRoot: "/repo" };
  assert.equal(resolveSidecarPaths(runtime).deploymentRoot, path.join("/repo", "packaging/dist/go-onedir/career-hub-sidecar"));
  assert.equal(resolveSidecarPaths({ ...runtime, goPreview: true }).deploymentRoot,
    path.join("/repo", "packaging/dist/go-onedir/career-hub-sidecar"));
  assert.equal(resolveSidecarPaths({ ...runtime, isPackaged: true, processResourcesPath: "/installed/resources", goPreview: true }).deploymentRoot,
    path.join("/installed/resources", "career-hub-sidecar"));
  assert.throws(() => resolveSidecarPaths({ ...runtime, platform: "darwin", isPackaged: true, goPreview: true }), AppPathError);
});

test("private directory validation rejects a symlink component", (t) => {
  const root = temporaryDirectory(t);
  const target = path.join(root, "target");
  const linked = path.join(root, "linked");
  fs.mkdirSync(target);
  fs.symlinkSync(target, linked, process.platform === "win32" ? "junction" : "dir");
  assert.throws(
    () => ensurePrivateDirectory(path.join(linked, "data")),
    (error) =>
      error instanceof AppPathError && error.code === "DESKTOP_DATA_ROOT_LINKED",
  );
});

test("Go deployment binds a virtualized installation to its native directory identity", (t) => {
  const root = temporaryDirectory(t);
  const paths = resolveSidecarPaths({ isPackaged: true, platform: "win32",
    goPreview: true, processResourcesPath: path.join(root, "requested", "resources") });
  const physical = path.join(root, "LocalCache", "resources", "career-hub-sidecar");
  fs.mkdirSync(paths.deploymentRoot, { recursive: true });
  fs.mkdirSync(physical, { recursive: true });
  const fileSystem = { ...fs,
    lstatSync: (candidate, options) => fs.lstatSync(candidate === paths.deploymentRoot ? physical : candidate, options),
    realpathSync: Object.assign(() => { throw new Error("non-native resolution used"); }, {
      native: () => physical,
    }),
  };
  const actual = canonicalGoSidecarPaths(paths, fileSystem);
  assert.equal(actual.deploymentRoot, physical);
  for (const key of ["executablePath", "manifestPath", "manifestSha256Path"]) {
    assert.equal(actual[key], path.join(physical, path.basename(paths[key])));
  }
  assert.equal(paths.deploymentRoot.includes("requested"), true);
  assert.deepEqual(fs.readdirSync(physical), []); // Resolution must not write probes.

  const rejects = (adapter) => assert.throws(() => canonicalGoSidecarPaths(paths, adapter),
    (error) => error.code === "DESKTOP_SIDECAR_RESOURCE_INVALID");
  rejects({ ...fileSystem, lstatSync: fs.lstatSync }); // Same contents/name do not prove identity.
  let observations = 0;
  rejects({ ...fileSystem, lstatSync(candidate, options) {
    if (candidate === paths.deploymentRoot && ++observations === 2) return fs.lstatSync(candidate, options);
    return fileSystem.lstatSync(candidate, options);
  } });
  rejects({ ...fileSystem, realpathSync: { native: () => { throw new Error("unavailable"); } } });
});

test("Go deployment canonicalization preserves ordinary paths and rejects linked ancestors", (t) => {
  const root = temporaryDirectory(t);
  const paths = resolveSidecarPaths({ isPackaged: true, platform: "win32",
    goPreview: true, processResourcesPath: path.join(root, "resources") });
  fs.mkdirSync(paths.deploymentRoot, { recursive: true });
  assert.deepEqual(canonicalGoSidecarPaths(paths), paths);
  assert.throws(() => canonicalGoSidecarPaths({ ...paths, executablePath: path.join(root, "other.exe") }), AppPathError);
  const linked = path.join(root, "linked");
  fs.symlinkSync(path.join(root, "resources"), linked, process.platform === "win32" ? "junction" : "dir");
  const linkedPaths = resolveSidecarPaths({ isPackaged: true, platform: "win32",
    goPreview: true, processResourcesPath: linked });
  assert.throws(() => canonicalGoSidecarPaths(linkedPaths),
    (error) => error.code === "DESKTOP_SIDECAR_RESOURCE_INVALID");
});

test("private directory follows the exact probe when Windows virtualizes writes", (t) => {
  const root = temporaryDirectory(t);
  const requested = path.join(root, "requested", "CareerHub", "dev");
  const redirected = path.join(root, "LocalCache", "CareerHub", "dev");
  fs.mkdirSync(requested, { recursive: true });
  fs.mkdirSync(redirected, { recursive: true });

  const redirect = (candidate) => {
    const relative = path.relative(requested, candidate);
    if (relative && !relative.startsWith("..") && !path.isAbsolute(relative)) {
      return path.join(redirected, relative);
    }
    return candidate;
  };
  const fileSystem = {
    ...fs,
    openSync: (candidate, ...args) => fs.openSync(redirect(candidate), ...args),
    realpathSync: (candidate, ...args) =>
      fs.realpathSync(redirect(candidate), ...args),
    unlinkSync: (candidate) => fs.unlinkSync(redirect(candidate)),
  };

  assert.equal(
    ensurePrivateDirectory(requested, {
      fileSystem,
      randomUUID: () => "redirected-probe",
    }),
    fs.realpathSync(redirected),
  );
  assert.equal(
    fs.existsSync(path.join(redirected, ".career-hub-write-redirected-probe")),
    false,
  );
});

test("sidecar deployment validates manifest and executable checksums", (t) => {
  const root = temporaryDirectory(t);
  const projectRoot = path.join(root, "project");
  const platform = process.platform;
  const paths = resolveSidecarPaths({
    isPackaged: false,
    processResourcesPath: "unused",
    projectRoot,
    platform,
  });
  fs.mkdirSync(paths.deploymentRoot, { recursive: true });
  fs.writeFileSync(paths.executablePath, "sidecar");
  fs.chmodSync(paths.executablePath, 0o700);
  const executableHash = crypto
    .createHash("sha256")
    .update(fs.readFileSync(paths.executablePath))
    .digest("hex");
  const manifestRecord = {
    app_version: "0.1.0",
    files: [
      {
        path: path.basename(paths.executablePath),
        sha256: executableHash,
        size: 7,
        type: "file",
      },
    ],
  };
  function writeManifest() {
    const manifest = `${JSON.stringify(manifestRecord)}\n`;
    fs.writeFileSync(paths.manifestPath, manifest);
    const manifestHash = crypto.createHash("sha256").update(manifest).digest("hex");
    fs.writeFileSync(
      paths.manifestSha256Path,
      `${manifestHash}  sidecar-manifest.json\n`,
    );
  }
  writeManifest();

  // Standard launch must reject the retired Python manifest as well as a
  // native manifest that predates the required bundled client.
  assert.throws(() => validateSidecarDeployment(paths, { appVersion: "0.1.0", platform }), AppPathError);
  manifestRecord.runtime_capabilities = [SIDECAR_CAPABILITIES.DATA_HOME_RECOVERY_V1];
  manifestRecord.layout = "go-onedir";
  manifestRecord.schema_version = 2;
  writeManifest();
  assert.throws(() => validateSidecarDeployment(paths, { appVersion: "0.1.0", platform }), AppPathError);
  manifestRecord.schema_version = GO_MANIFEST_SCHEMA_VERSION;
  manifestRecord.agent_client = GO_AGENT_CLIENT;
  const agentBytes = "fictional client";
  fs.writeFileSync(path.join(paths.deploymentRoot, GO_AGENT_CLIENT.executable), agentBytes);
  manifestRecord.files.push({ path: GO_AGENT_CLIENT.executable, type: "file", size: Buffer.byteLength(agentBytes), sha256: crypto.createHash("sha256").update(agentBytes).digest("hex") });
  writeManifest();
  assert.deepEqual(validateSidecarDeployment(paths, {
    appVersion: "0.1.0", platform }).runtimeCapabilities,
  [SIDECAR_CAPABILITIES.DATA_HOME_RECOVERY_V1]);
  // The build commit is read from the verified manifest; malformed is absent, not a failure.
  assert.equal(validateSidecarDeployment(paths, { appVersion: "0.1.0", platform }).sourceCommit, null);
  manifestRecord.source_commit = "0123456789abcdef0123456789abcdef01234567";
  writeManifest();
  assert.equal(validateSidecarDeployment(paths, { appVersion: "0.1.0", platform }).sourceCommit,
    manifestRecord.source_commit);
  manifestRecord.source_commit = "not-a-commit";
  writeManifest();
  assert.equal(validateSidecarDeployment(paths, { appVersion: "0.1.0", platform }).sourceCommit, null);

  manifestRecord.runtime_capabilities.unshift(SIDECAR_CAPABILITIES.BACKUP_CREATE_V1);
  writeManifest();
  assert.equal(validateSidecarDeployment(paths, {
    appVersion: "0.1.0", platform }).runtimeCapabilities.includes(
      SIDECAR_CAPABILITIES.BACKUP_CREATE_V1), true);

  manifestRecord.runtime_capabilities = ["unknown_capability"];
  writeManifest();
  assert.throws(
    () => validateSidecarDeployment(paths, { appVersion: "0.1.0", platform }),
    (error) =>
      error instanceof AppPathError &&
      error.code === "DESKTOP_SIDECAR_MANIFEST_INVALID",
  );

  manifestRecord.runtime_capabilities = [
    SIDECAR_CAPABILITIES.DATA_HOME_RECOVERY_V1,
  ];
  writeManifest();
  fs.appendFileSync(paths.executablePath, "tampered");
  assert.throws(
    () =>
      validateSidecarDeployment(paths, {
        appVersion: "0.1.0",
        platform,
      }),
    (error) =>
      error instanceof AppPathError &&
      error.code === "DESKTOP_SIDECAR_MANIFEST_INVALID",
  );
});
