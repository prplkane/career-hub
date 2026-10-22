"use strict";

const crypto = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { isDeepStrictEqual } = require("node:util");

const MANIFEST_NAME = "sidecar-manifest.json";
const MANIFEST_CHECKSUM_NAME = "sidecar-manifest.sha256";
const GO_MANIFEST_SCHEMA_VERSION = 3;
const GO_AGENT_CLIENT = Object.freeze({
  executable: "career-hub-agent.exe",
  contract_version: 4,
  commands: Object.freeze(["analyze", "prepare", "context", "material-context", "save-material", "render-material", "save-draft", "registry-add-exact", "record-submission"]),
});
const GO_LAYOUT = "go-onedir";
const GO_VERSION = "go1.27.3";
const GO_RUNTIME_CAPABILITIES = Object.freeze(["backup_create_v1", "backup_restore_v1", "data_home_recovery_v1", "email_credential_recovery_v1"]);
const FORBIDDEN_ARTIFACT_ROOTS = new Set([
  ".git",
  ".venv",
  "applications",
  "backups",
  "career_vault",
  "documents",
  "jobs",
  "logs",
  "node_modules",
  "outputs",
  "private_documents",
  "resume_templates",
  "resume_versions",
  "secrets",
  "security-reports",
  "templates",
  "tests",
]);
const PROJECT_RESOURCE_PATHS = [
  "VERSION",
  "src/data/job_technologies.yaml",
  "src/data/requirement_concepts.yaml",
  "src/data/skill_groups.yaml",
];

class PackageManifestError extends Error {
  constructor(code) {
    super(code);
    this.name = "PackageManifestError";
    this.code = code;
  }
}

function compareLogicalPaths(left, right) {
  return left < right ? -1 : left > right ? 1 : 0;
}

function posixRelative(root, candidate) {
  return path.relative(root, candidate).split(path.sep).join("/");
}

function validateLogicalPath(value) {
  return (
    typeof value === "string" &&
    value.length > 0 &&
    !value.includes("\\") &&
    !value.includes("\0") &&
    !value.startsWith("/") &&
    value.split("/").every((component) => component && component !== "." && component !== "..")
  );
}

function sha256File(filePath) {
  const descriptor = fs.openSync(filePath, "r");
  const digest = crypto.createHash("sha256");
  const buffer = Buffer.allocUnsafe(1024 * 1024);
  try {
    for (;;) {
      const length = fs.readSync(descriptor, buffer, 0, buffer.length, null);
      if (length === 0) break;
      digest.update(buffer.subarray(0, length));
    }
  } finally {
    fs.closeSync(descriptor);
  }
  return digest.digest("hex");
}

function containedRealPath(rootRealPath, candidate) {
  const candidateRealPath = fs.realpathSync(candidate);
  const relative = path.relative(rootRealPath, candidateRealPath);
  return relative === "" || (!relative.startsWith(`..${path.sep}`) && relative !== "..");
}

function collectArtifactEntries(artifactRoot) {
  const rootMetadata = fs.lstatSync(artifactRoot, { throwIfNoEntry: false });
  if (!rootMetadata?.isDirectory() || rootMetadata.isSymbolicLink()) {
    throw new PackageManifestError("SIDECAR_ARTIFACT_ROOT_INVALID");
  }
  const rootRealPath = fs.realpathSync(artifactRoot);
  const entries = [];

  function visit(directory) {
    const children = fs
      .readdirSync(directory, { withFileTypes: true })
      .sort((left, right) => compareLogicalPaths(left.name, right.name));
    for (const child of children) {
      const candidate = path.join(directory, child.name);
      const relative = posixRelative(artifactRoot, candidate);
      if (relative === MANIFEST_NAME || relative === MANIFEST_CHECKSUM_NAME) continue;
      if (FORBIDDEN_ARTIFACT_ROOTS.has(relative.split("/", 1)[0])) {
        throw new PackageManifestError("SIDECAR_PRIVATE_ARTIFACT_PATH_FORBIDDEN");
      }
      const metadata = fs.lstatSync(candidate);
      if (metadata.isSymbolicLink()) {
        const target = fs.readlinkSync(candidate);
        if (path.isAbsolute(target) || !containedRealPath(rootRealPath, candidate)) {
          throw new PackageManifestError("SIDECAR_ARTIFACT_SYMLINK_INVALID");
        }
        entries.push({ path: relative, target, type: "symlink" });
      } else if (metadata.isDirectory()) {
        visit(candidate);
      } else if (metadata.isFile()) {
        entries.push({
          path: relative,
          sha256: sha256File(candidate),
          size: metadata.size,
          type: "file",
        });
      } else {
        throw new PackageManifestError("SIDECAR_ARTIFACT_ENTRY_UNSUPPORTED");
      }
    }
  }

  visit(artifactRoot);
  entries.sort((left, right) => compareLogicalPaths(left.path, right.path));
  if (entries.length === 0) {
    throw new PackageManifestError("SIDECAR_ARTIFACT_EMPTY");
  }
  return entries;
}

function collectProjectResourceEntries(projectRoot) {
  const logicalPaths = [...PROJECT_RESOURCE_PATHS];
  const staticRoot = path.join(projectRoot, "src", "intake_ui_static");

  function visit(directory) {
    const children = fs
      .readdirSync(directory, { withFileTypes: true })
      .sort((left, right) => compareLogicalPaths(left.name, right.name));
    for (const child of children) {
      const candidate = path.join(directory, child.name);
      const metadata = fs.lstatSync(candidate);
      if (metadata.isSymbolicLink()) {
        throw new PackageManifestError("SIDECAR_PROJECT_RESOURCE_LINKED");
      }
      if (metadata.isDirectory()) {
        visit(candidate);
      } else if (metadata.isFile()) {
        logicalPaths.push(posixRelative(projectRoot, candidate));
      } else {
        throw new PackageManifestError("SIDECAR_PROJECT_RESOURCE_INVALID");
      }
    }
  }

  visit(staticRoot);
  return logicalPaths
    .sort(compareLogicalPaths)
    .map((logicalPath) => {
      const candidate = path.join(projectRoot, ...logicalPath.split("/"));
      const metadata = fs.lstatSync(candidate, { throwIfNoEntry: false });
      if (!metadata?.isFile() || metadata.isSymbolicLink()) {
        throw new PackageManifestError("SIDECAR_PROJECT_RESOURCE_INVALID");
      }
      return {
        path: logicalPath,
        sha256: sha256File(candidate),
        size: metadata.size,
      };
    });
}

function collectGoProjectResourceEntries(projectRoot) {
  return collectProjectResourceEntries(projectRoot);
}

function exactKeys(record, expectedKeys) {
  return (
    record &&
    typeof record === "object" &&
    !Array.isArray(record) &&
    isDeepStrictEqual(Object.keys(record).sort(), [...expectedKeys].sort())
  );
}

function validateFileRecords(files) {
  if (!Array.isArray(files) || files.length === 0) return false;
  const seen = new Set();
  for (const entry of files) {
    if (!validateLogicalPath(entry?.path) || seen.has(entry.path)) return false;
    seen.add(entry.path);
    if (entry.type === "file") {
      if (
        !exactKeys(entry, ["path", "sha256", "size", "type"]) ||
        !/^[0-9a-f]{64}$/.test(entry.sha256) ||
        !Number.isSafeInteger(entry.size) ||
        entry.size < 0
      ) {
        return false;
      }
    } else if (
      entry.type !== "symlink" ||
      !exactKeys(entry, ["path", "target", "type"]) ||
      typeof entry.target !== "string" ||
      entry.target.length === 0 ||
      entry.target.includes("\0") ||
      path.isAbsolute(entry.target)
    ) {
      return false;
    }
  }
  return true;
}

function validateResourceRecords(resources) {
  if (!Array.isArray(resources) || resources.length === 0) return false;
  const seen = new Set();
  for (const entry of resources) {
    if (
      !exactKeys(entry, ["path", "sha256", "size"]) ||
      !validateLogicalPath(entry.path) ||
      seen.has(entry.path) ||
      !/^[0-9a-f]{64}$/.test(entry.sha256) ||
      !Number.isSafeInteger(entry.size) ||
      entry.size < 0
    ) {
      return false;
    }
    seen.add(entry.path);
  }
  return true;
}

function validateGoManifestShape(
  manifest,
  { appVersion, expectedSourceCommit, platform },
) {
  const keys = [
    "agent_client",
    "app_version",
    "architecture",
    "files",
    "go_mod_sha256",
    "go_sum_sha256",
    "go_version",
    "layout",
    "platform",
    "resources",
    "runtime",
    "runtime_capabilities",
    "schema_version",
    "source_commit",
    "source_tree_dirty",
  ];
  const executableRecords = Array.isArray(manifest?.files)
    ? manifest.files.filter(
        (entry) =>
          entry?.path === "career-hub-sidecar.exe" && entry.type === "file",
      )
    : [];
  const agentRecords = Array.isArray(manifest?.files)
    ? manifest.files.filter((entry) => entry?.path === GO_AGENT_CLIENT.executable && entry.type === "file")
    : [];
  if (
    !exactKeys(manifest, keys) ||
    !isDeepStrictEqual(manifest.agent_client, GO_AGENT_CLIENT) ||
    manifest.app_version !== appVersion ||
    manifest.source_commit !== expectedSourceCommit ||
    manifest.layout !== GO_LAYOUT ||
    manifest.platform !== platform ||
    manifest.platform !== "win32" ||
    manifest.architecture !== "x64" ||
    manifest.schema_version !== GO_MANIFEST_SCHEMA_VERSION ||
    manifest.runtime !== "go" ||
    manifest.go_version !== GO_VERSION ||
    !/^[0-9a-f]{64}$/.test(manifest.go_mod_sha256) ||
    !/^[0-9a-f]{64}$/.test(manifest.go_sum_sha256) ||
    !isDeepStrictEqual(
      manifest.runtime_capabilities,
      GO_RUNTIME_CAPABILITIES,
    ) ||
    typeof manifest.source_commit !== "string" ||
    !/^[0-9a-f]+$/.test(manifest.source_commit) ||
    typeof manifest.source_tree_dirty !== "boolean" ||
    !validateFileRecords(manifest.files) ||
    !validateResourceRecords(manifest.resources) ||
    executableRecords.length !== 1 || agentRecords.length !== 1
  ) {
    throw new PackageManifestError("GO_SIDECAR_MANIFEST_INVALID");
  }
}

function verifyGoResourceInventory(manifest, projectRoot) {
  const expected = collectProjectResourceEntries(projectRoot);
  if (!isDeepStrictEqual(manifest.resources, expected)) {
    throw new PackageManifestError("GO_SIDECAR_PROJECT_RESOURCE_MISMATCH");
  }
  const fileByPath = new Map(manifest.files.map((entry) => [entry.path, entry]));
  for (const resource of expected) {
    const bundled = fileByPath.get(resource.path);
    if (
      !bundled ||
      bundled.type !== "file" ||
      bundled.sha256 !== resource.sha256 ||
      bundled.size !== resource.size
    ) {
      throw new PackageManifestError("GO_SIDECAR_BUNDLED_RESOURCE_MISMATCH");
    }
  }
}

function readVerifiedManifest(artifactRoot) {
  const manifestPath = path.join(artifactRoot, MANIFEST_NAME);
  const checksumPath = path.join(artifactRoot, MANIFEST_CHECKSUM_NAME);
  for (const candidate of [manifestPath, checksumPath]) {
    const metadata = fs.lstatSync(candidate, { throwIfNoEntry: false });
    if (!metadata?.isFile() || metadata.isSymbolicLink()) {
      throw new PackageManifestError("SIDECAR_MANIFEST_FILE_INVALID");
    }
  }

  let manifestBytes;
  let checksumRecord;
  let manifest;
  try {
    manifestBytes = fs.readFileSync(manifestPath);
    checksumRecord = fs.readFileSync(checksumPath, "ascii");
    manifest = JSON.parse(manifestBytes.toString("utf8"));
  } catch (_error) {
    throw new PackageManifestError("SIDECAR_MANIFEST_INVALID");
  }
  const manifestHash = crypto.createHash("sha256").update(manifestBytes).digest("hex");
  if (checksumRecord !== `${manifestHash}  ${MANIFEST_NAME}\n`) {
    throw new PackageManifestError("SIDECAR_MANIFEST_CHECKSUM_MISMATCH");
  }
  return Object.freeze({ manifest, manifestBytes });
}

function canonicalManifestBytes(payload) {
  return Buffer.from(`${JSON.stringify(payload, null, 2)}\n`, "utf8");
}

function containsDeveloperPath(manifestBytes, projectRoot) {
  const text = manifestBytes.toString("utf8");
  for (const privatePath of [path.resolve(projectRoot), os.homedir()]) {
    if (!privatePath) continue;
    const variants = new Set([
      privatePath,
      privatePath.replaceAll("\\", "\\\\"),
      privatePath.split(path.sep).join("/"),
    ]);
    if ([...variants].some((candidate) => candidate && text.includes(candidate))) {
      return true;
    }
  }
  return false;
}

function writeGoSidecarManifest({
  artifactRoot,
  projectRoot,
  appVersion,
  sourceCommit,
  sourceTreeDirty,
  goVersion,
}) {
  if (goVersion !== GO_VERSION) {
    throw new PackageManifestError("GO_SIDECAR_TOOLCHAIN_INVALID");
  }
  const rootMetadata = fs.lstatSync(artifactRoot, { throwIfNoEntry: false });
  if (!rootMetadata?.isDirectory() || rootMetadata.isSymbolicLink()) {
    throw new PackageManifestError("SIDECAR_ARTIFACT_ROOT_INVALID");
  }
  const manifestPath = path.join(artifactRoot, MANIFEST_NAME);
  const checksumPath = path.join(artifactRoot, MANIFEST_CHECKSUM_NAME);
  if (
    fs.lstatSync(manifestPath, { throwIfNoEntry: false }) ||
    fs.lstatSync(checksumPath, { throwIfNoEntry: false })
  ) {
    throw new PackageManifestError("GO_SIDECAR_MANIFEST_ALREADY_EXISTS");
  }
  const payload = {
    agent_client: GO_AGENT_CLIENT,
    app_version: appVersion,
    architecture: "x64",
    files: collectArtifactEntries(artifactRoot),
    go_mod_sha256: sha256File(path.join(projectRoot, "backend", "go.mod")),
    go_sum_sha256: sha256File(path.join(projectRoot, "backend", "go.sum")),
    go_version: goVersion,
    layout: GO_LAYOUT,
    platform: "win32",
    resources: collectProjectResourceEntries(projectRoot),
    runtime: "go",
    runtime_capabilities: [...GO_RUNTIME_CAPABILITIES],
    schema_version: GO_MANIFEST_SCHEMA_VERSION,
    source_commit: sourceCommit,
    source_tree_dirty: sourceTreeDirty,
  };
  validateGoManifestShape(payload, {
    appVersion,
    expectedSourceCommit: sourceCommit,
    platform: "win32",
  });
  verifyGoResourceInventory(payload, projectRoot);
  const encoded = canonicalManifestBytes(payload);
  if (containsDeveloperPath(encoded, projectRoot)) {
    throw new PackageManifestError("GO_SIDECAR_DEVELOPER_PATH_FORBIDDEN");
  }
  fs.writeFileSync(manifestPath, encoded, { flag: "wx" });
  const checksum = crypto.createHash("sha256").update(encoded).digest("hex");
  fs.writeFileSync(
    checksumPath,
    `${checksum}  ${MANIFEST_NAME}\n`,
    { encoding: "ascii", flag: "wx" },
  );
  return payload;
}

function verifyGoSidecarPackageInput({
  artifactRoot,
  projectRoot,
  appVersion,
  expectedSourceCommit,
  platform,
  requireClean = false,
}) {
  const { manifest, manifestBytes } = readVerifiedManifest(artifactRoot);
  validateGoManifestShape(manifest, {
    appVersion,
    expectedSourceCommit,
    platform,
  });
  if (requireClean && manifest.source_tree_dirty) {
    throw new PackageManifestError("GO_SIDECAR_DIRTY_ARTIFACT_FORBIDDEN");
  }
  const actualFiles = collectArtifactEntries(artifactRoot);
  if (!isDeepStrictEqual(manifest.files, actualFiles)) {
    throw new PackageManifestError("GO_SIDECAR_ARTIFACT_CONTENT_MISMATCH");
  }
  verifyGoResourceInventory(manifest, projectRoot);
  if (
    manifest.go_mod_sha256 !==
      sha256File(path.join(projectRoot, "backend", "go.mod")) ||
    manifest.go_sum_sha256 !==
      sha256File(path.join(projectRoot, "backend", "go.sum"))
  ) {
    throw new PackageManifestError("GO_SIDECAR_MODULE_INPUT_MISMATCH");
  }
  if (containsDeveloperPath(manifestBytes, projectRoot)) {
    throw new PackageManifestError("GO_SIDECAR_DEVELOPER_PATH_FORBIDDEN");
  }
  return manifest;
}

module.exports = {
  GO_AGENT_CLIENT,
  GO_MANIFEST_SCHEMA_VERSION,
  GO_RUNTIME_CAPABILITIES,
  PackageManifestError,
  collectArtifactEntries,
  collectGoProjectResourceEntries,
  verifyGoSidecarPackageInput,
  writeGoSidecarManifest,
};
