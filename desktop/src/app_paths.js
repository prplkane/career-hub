"use strict";

const crypto = require("node:crypto");
const fs = require("node:fs");
const path = require("node:path");
const { isDeepStrictEqual } = require("node:util");

const { CHANNELS } = require("./build_metadata");
const { GO_AGENT_CLIENT, GO_MANIFEST_SCHEMA_VERSION } = require("./package_manifest");

const SIDECAR_CAPABILITIES = Object.freeze({
  BACKUP_CREATE_V1: "backup_create_v1",
  BACKUP_RESTORE_V1: "backup_restore_v1",
  DATA_HOME_RECOVERY_V1: "data_home_recovery_v1",
  EMAIL_CREDENTIAL_RECOVERY_V1: "email_credential_recovery_v1",
});
const KNOWN_SIDECAR_CAPABILITIES = new Set(
  Object.values(SIDECAR_CAPABILITIES),
);

class AppPathError extends Error {
  constructor(code) {
    super(code);
    this.name = "AppPathError";
    this.code = code;
  }
}

function platformPath(platform) {
  return platform === "win32" ? path.win32 : path.posix;
}

function requireAbsolute(candidate, pathApi) {
  if (
    typeof candidate !== "string" ||
    candidate.length === 0 ||
    candidate.includes("\0") ||
    !pathApi.isAbsolute(candidate) ||
    pathApi.parse(candidate).root === candidate
  ) {
    throw new AppPathError("DESKTOP_DATA_ROOT_INVALID");
  }
  return pathApi.normalize(candidate);
}

function resolveProductPaths({
  channel,
  platform = process.platform,
  electronPaths,
  environment = process.env,
  goPreview = false,
}) {
  if (!CHANNELS.has(channel)) {
    throw new AppPathError("DESKTOP_CHANNEL_INVALID");
  }
  if (typeof goPreview !== "boolean" || (goPreview && (channel !== "dev" || platform !== "win32"))) {
    throw new AppPathError("DESKTOP_CHANNEL_INVALID");
  }
  if (!electronPaths || typeof electronPaths !== "object") {
    throw new AppPathError("DESKTOP_PLATFORM_PATH_UNAVAILABLE");
  }

  const pathApi = platformPath(platform);
  let applicationDataBase;
  if (platform === "win32") {
    applicationDataBase = requireAbsolute(environment.LOCALAPPDATA, pathApi);
  } else if (platform === "darwin") {
    applicationDataBase = requireAbsolute(electronPaths.appData, pathApi);
  } else if (platform === "linux" && channel === "dev") {
    applicationDataBase = requireAbsolute(electronPaths.appData, pathApi);
  } else {
    throw new AppPathError("DESKTOP_PLATFORM_UNSUPPORTED");
  }

  const productRoot = pathApi.join(applicationDataBase, "CareerHub");
  const dataChannel = goPreview ? "dev-go-preview" : channel;
  const electronRoot = pathApi.join(productRoot, ".electron", dataChannel);
  return Object.freeze({
    channel,
    dataRoot: pathApi.join(productRoot, dataChannel),
    electronSessionRoot: pathApi.join(electronRoot, "session"),
    electronUserDataRoot: pathApi.join(electronRoot, "user-data"),
    productRoot,
  });
}

function ensureDirectoryComponents(directoryPath, fileSystem = fs) {
  const parsed = path.parse(directoryPath);
  let current = parsed.root;
  for (const component of directoryPath.slice(parsed.root.length).split(path.sep)) {
    if (!component) continue;
    current = path.join(current, component);
    const metadata = fileSystem.lstatSync(current, { throwIfNoEntry: false });
    if (metadata?.isSymbolicLink()) {
      throw new AppPathError("DESKTOP_DATA_ROOT_LINKED");
    }
    if (metadata && !metadata.isDirectory()) {
      throw new AppPathError("DESKTOP_DATA_ROOT_INVALID");
    }
    if (!metadata) {
      fileSystem.mkdirSync(current, { mode: 0o700 });
      const created = fileSystem.lstatSync(current);
      if (created.isSymbolicLink() || !created.isDirectory()) {
        throw new AppPathError("DESKTOP_DATA_ROOT_LINKED");
      }
    }
  }
}

function ensurePrivateDirectory(
  directoryPath,
  { fileSystem = fs, randomUUID = crypto.randomUUID } = {},
) {
  if (!path.isAbsolute(directoryPath) || path.parse(directoryPath).root === directoryPath) {
    throw new AppPathError("DESKTOP_DATA_ROOT_INVALID");
  }
  try {
    ensureDirectoryComponents(directoryPath, fileSystem);
    fileSystem.accessSync(
      directoryPath,
      fileSystem.constants.R_OK | fileSystem.constants.W_OK,
    );
  } catch (error) {
    if (error instanceof AppPathError) throw error;
    throw new AppPathError("DESKTOP_DATA_ROOT_UNWRITABLE");
  }

  const probe = path.join(directoryPath, `.career-hub-write-${randomUUID()}`);
  try {
    const descriptor = fileSystem.openSync(
      probe,
      fileSystem.constants.O_CREAT |
        fileSystem.constants.O_EXCL |
        fileSystem.constants.O_WRONLY,
      0o600,
    );
    fileSystem.closeSync(descriptor);

    // Windows package virtualization can keep the requested directory path
    // unchanged while redirecting created files into the package LocalCache.
    // The exact O_EXCL probe is the authoritative writable location.
    const canonicalProbe = fileSystem.realpathSync(probe);
    if (path.basename(canonicalProbe) !== path.basename(probe)) {
      throw new AppPathError("DESKTOP_DATA_ROOT_INVALID");
    }
    const canonicalDirectory = path.dirname(canonicalProbe);
    if (
      !path.isAbsolute(canonicalDirectory) ||
      path.parse(canonicalDirectory).root === canonicalDirectory
    ) {
      throw new AppPathError("DESKTOP_DATA_ROOT_INVALID");
    }
    ensureDirectoryComponents(canonicalDirectory, fileSystem);
    fileSystem.accessSync(
      canonicalDirectory,
      fileSystem.constants.R_OK | fileSystem.constants.W_OK,
    );
    const result = fileSystem.realpathSync(canonicalDirectory);
    fileSystem.unlinkSync(probe);
    return result;
  } catch (error) {
    try {
      fileSystem.unlinkSync(probe);
    } catch (_cleanupError) {
      // A failed best-effort cleanup does not change the sanitized error.
    }
    if (error instanceof AppPathError) throw error;
    throw new AppPathError("DESKTOP_DATA_ROOT_UNWRITABLE");
  }
}

function resolveSidecarPaths({ isPackaged, processResourcesPath, projectRoot, platform }) {
  if (platform !== "win32") {
    throw new AppPathError("DESKTOP_PLATFORM_UNSUPPORTED");
  }
  const deploymentRoot = isPackaged
    ? path.join(processResourcesPath, "career-hub-sidecar")
    : path.join(
        projectRoot,
        "packaging",
        "dist",
        "go-onedir",
        "career-hub-sidecar",
      );
  const executableName =
    platform === "win32" ? "career-hub-sidecar.exe" : "career-hub-sidecar";
  return Object.freeze({
    deploymentRoot,
    executablePath: path.join(deploymentRoot, executableName),
    manifestPath: path.join(deploymentRoot, "sidecar-manifest.json"),
    manifestSha256Path: path.join(deploymentRoot, "sidecar-manifest.sha256"),
  });
}

function regularUnlinkedFile(filePath) {
  const metadata = fs.lstatSync(filePath, { throwIfNoEntry: false });
  return Boolean(metadata?.isFile() && !metadata.isSymbolicLink());
}

function sha256File(filePath) {
  return crypto.createHash("sha256").update(fs.readFileSync(filePath)).digest("hex");
}

function canonicalGoSidecarPaths(sidecarPaths, fileSystem = fs) {
  // Native Go rooted opens do not follow Windows package virtualization. Bind
  // the installed resource directory to its physical name before spawning Go.
  // Check both paths without creating anything or admitting linked ancestors.
  const directoryIdentity = (directory) => {
    if (!path.isAbsolute(directory) || path.parse(directory).root === directory) {
      throw new AppPathError("DESKTOP_SIDECAR_RESOURCE_INVALID");
    }
    const anchor = path.parse(directory).root;
    let current = anchor, identity;
    for (const component of path.relative(anchor, directory).split(path.sep)) {
      current = path.join(current, component);
      identity = fileSystem.lstatSync(current, { bigint: true });
      if (!identity.isDirectory() || identity.isSymbolicLink()) {
        throw new AppPathError("DESKTOP_SIDECAR_RESOURCE_INVALID");
      }
    }
    return identity;
  };
  try {
    const before = directoryIdentity(sidecarPaths.deploymentRoot);
    const deploymentRoot = fileSystem.realpathSync.native(sidecarPaths.deploymentRoot);
    const canonical = directoryIdentity(deploymentRoot);
    const after = directoryIdentity(sidecarPaths.deploymentRoot);
    if (before.ino === 0n || before.ino !== canonical.ino || before.dev !== canonical.dev ||
        before.ino !== after.ino || before.dev !== after.dev) {
      throw new AppPathError("DESKTOP_SIDECAR_RESOURCE_INVALID");
    }
    const result = { ...sidecarPaths, deploymentRoot };
    for (const [key, name] of Object.entries({ executablePath: "career-hub-sidecar.exe",
      manifestPath: "sidecar-manifest.json", manifestSha256Path: "sidecar-manifest.sha256" })) {
      if (sidecarPaths[key] !== path.join(sidecarPaths.deploymentRoot, name)) {
        throw new AppPathError("DESKTOP_SIDECAR_RESOURCE_INVALID");
      }
      result[key] = path.join(deploymentRoot, name);
    }
    return Object.freeze(result);
  } catch (error) {
    if (error instanceof AppPathError) throw error;
    throw new AppPathError("DESKTOP_SIDECAR_RESOURCE_INVALID");
  }
}

function validatedRuntimeCapabilities(manifest) {
  if (manifest.runtime_capabilities === undefined) {
    return Object.freeze([]);
  }
  if (!Array.isArray(manifest.runtime_capabilities)) {
    throw new AppPathError("DESKTOP_SIDECAR_MANIFEST_INVALID");
  }
  let previous = null;
  for (const capability of manifest.runtime_capabilities) {
    if (
      typeof capability !== "string" ||
      !KNOWN_SIDECAR_CAPABILITIES.has(capability) ||
      (previous !== null && previous >= capability)
    ) {
      throw new AppPathError("DESKTOP_SIDECAR_MANIFEST_INVALID");
    }
    previous = capability;
  }
  return Object.freeze([...manifest.runtime_capabilities]);
}

function validateSidecarDeployment(sidecarPaths, { appVersion, platform }) {
  try {
    if (platform !== "win32") throw new AppPathError("DESKTOP_PLATFORM_UNSUPPORTED");
    sidecarPaths = canonicalGoSidecarPaths(sidecarPaths);
    const rootMetadata = fs.lstatSync(sidecarPaths.deploymentRoot);
    if (!rootMetadata.isDirectory() || rootMetadata.isSymbolicLink()) {
      throw new AppPathError("DESKTOP_SIDECAR_RESOURCE_INVALID");
    }
    for (const filePath of [
      sidecarPaths.executablePath,
      sidecarPaths.manifestPath,
      sidecarPaths.manifestSha256Path,
    ]) {
      if (!regularUnlinkedFile(filePath)) {
        throw new AppPathError("DESKTOP_SIDECAR_RESOURCE_INVALID");
      }
    }

    const checksumRecord = fs.readFileSync(sidecarPaths.manifestSha256Path, "utf8");
    const checksumMatch = /^([0-9a-f]{64})  sidecar-manifest\.json\n$/.exec(checksumRecord);
    if (!checksumMatch || sha256File(sidecarPaths.manifestPath) !== checksumMatch[1]) {
      throw new AppPathError("DESKTOP_SIDECAR_MANIFEST_INVALID");
    }

    const manifest = JSON.parse(fs.readFileSync(sidecarPaths.manifestPath, "utf8"));
    if (manifest.layout !== "go-onedir" || manifest.schema_version !== GO_MANIFEST_SCHEMA_VERSION || !isDeepStrictEqual(manifest.agent_client, GO_AGENT_CLIENT)) {
      throw new AppPathError("DESKTOP_SIDECAR_MANIFEST_INVALID");
    }
    if (manifest.app_version !== appVersion || !Array.isArray(manifest.files)) {
      throw new AppPathError("DESKTOP_SIDECAR_MANIFEST_INVALID");
    }
    const runtimeCapabilities = validatedRuntimeCapabilities(manifest);
    const executableName = path.basename(sidecarPaths.executablePath);
    const executableRecords = manifest.files.filter(
      (entry) => entry?.path === executableName && entry.type === "file",
    );
    const executableRecord = executableRecords[0];
    if (
      executableRecords.length !== 1 ||
      !/^[0-9a-f]{64}$/.test(executableRecord.sha256) ||
      executableRecord.size !== fs.statSync(sidecarPaths.executablePath).size ||
      sha256File(sidecarPaths.executablePath) !== executableRecord.sha256
    ) {
      throw new AppPathError("DESKTOP_SIDECAR_MANIFEST_INVALID");
    }
    if (platform !== "win32" && !(fs.statSync(sidecarPaths.executablePath).mode & 0o100)) {
      throw new AppPathError("DESKTOP_SIDECAR_RESOURCE_INVALID");
    }
    let agentClient = null;
    {
      const executablePath = path.join(sidecarPaths.deploymentRoot, GO_AGENT_CLIENT.executable);
      if (!regularUnlinkedFile(executablePath)) {
        throw new AppPathError("DESKTOP_AGENT_RESOURCE_INVALID");
      }
      const records = manifest.files.filter((entry) => entry?.path === GO_AGENT_CLIENT.executable && entry.type === "file");
      if (records.length !== 1 || !/^[0-9a-f]{64}$/.test(records[0].sha256) || records[0].size !== fs.statSync(executablePath).size || records[0].sha256 !== sha256File(executablePath)) {
        throw new AppPathError("DESKTOP_AGENT_MANIFEST_INVALID");
      }
      agentClient = Object.freeze({ executablePath, contractVersion: GO_AGENT_CLIENT.contract_version, commands: GO_AGENT_CLIENT.commands });
    }
    // The manifest hash was verified above; a malformed commit is shown as absent.
    const sourceCommit = typeof manifest.source_commit === "string" &&
      /^[0-9a-f]{40}$/.test(manifest.source_commit) ? manifest.source_commit : null;
    return Object.freeze({ ...sidecarPaths, runtimeCapabilities, sourceCommit,
      ...(agentClient ? { agentClient, manifestSha256: checksumMatch[1] } : {}) });
  } catch (error) {
    if (error instanceof AppPathError) throw error;
    throw new AppPathError("DESKTOP_SIDECAR_RESOURCE_INVALID");
  }
}

module.exports = {
  AppPathError,
  canonicalGoSidecarPaths,
  SIDECAR_CAPABILITIES,
  ensurePrivateDirectory,
  resolveProductPaths,
  resolveSidecarPaths,
  validateSidecarDeployment,
};
