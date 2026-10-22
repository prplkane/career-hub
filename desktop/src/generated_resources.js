"use strict";

const fs = require("node:fs");
const path = require("node:path");

const {
  validateBuildMetadata,
  validateLegacyBuildMetadata,
} = require("./build_metadata");

const METADATA_NAME = "build-metadata.json";

class GeneratedResourceError extends Error {
  constructor(code) {
    super(code);
    this.name = "GeneratedResourceError";
    this.code = code;
  }
}

function invalidResources() {
  return new GeneratedResourceError("DESKTOP_GENERATED_RESOURCES_INVALID");
}

function requireUnlinkedDirectory(directoryPath) {
  let metadata;
  try {
    metadata = fs.lstatSync(directoryPath);
  } catch (_error) {
    throw invalidResources();
  }
  if (!metadata.isDirectory() || metadata.isSymbolicLink()) {
    throw invalidResources();
  }
}

function requireUnlinkedDirectoryChain(trustedRoot, directoryPath) {
  if (!path.isAbsolute(trustedRoot) || !path.isAbsolute(directoryPath)) {
    throw invalidResources();
  }
  const relative = path.relative(trustedRoot, directoryPath);
  if (relative === "" || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative)) {
    if (relative === "") {
      requireUnlinkedDirectory(trustedRoot);
      return;
    }
    throw invalidResources();
  }
  let current = trustedRoot;
  requireUnlinkedDirectory(current);
  for (const component of relative.split(path.sep)) {
    current = path.join(current, component);
    requireUnlinkedDirectory(current);
  }
}

function assertGeneratedResourceInventory(
  generatedRoot,
  { trustedRoot = generatedRoot } = {},
) {
  try {
    requireUnlinkedDirectoryChain(trustedRoot, generatedRoot);
    const entries = fs.readdirSync(generatedRoot);
    if (entries.length !== 1 || entries[0] !== METADATA_NAME) {
      throw invalidResources();
    }
    const metadataPath = path.join(generatedRoot, METADATA_NAME);
    const metadata = fs.lstatSync(metadataPath);
    if (!metadata.isFile() || metadata.isSymbolicLink() || metadata.size > 4_096) {
      throw invalidResources();
    }
    const encoded = fs.readFileSync(metadataPath, "utf8");
    if (!encoded.endsWith("\n") || encoded.slice(0, -1).includes("\n\n")) {
      throw invalidResources();
    }
    validateBuildMetadata(JSON.parse(encoded));
    return generatedRoot;
  } catch (error) {
    if (error instanceof GeneratedResourceError) throw error;
    throw invalidResources();
  }
}

function assertReplaceableStage(generatedRoot) {
  const metadata = fs.lstatSync(generatedRoot, { throwIfNoEntry: false });
  if (!metadata) return;
  if (!metadata.isDirectory() || metadata.isSymbolicLink()) {
    throw invalidResources();
  }
  const entries = fs.readdirSync(generatedRoot);
  if (entries.length === 0) return;
  try {
    assertGeneratedResourceInventory(generatedRoot);
    return;
  } catch (_currentSchemaError) {
    try {
      if (entries.length !== 1 || entries[0] !== METADATA_NAME) {
        throw invalidResources();
      }
      const metadataPath = path.join(generatedRoot, METADATA_NAME);
      const fileMetadata = fs.lstatSync(metadataPath);
      if (
        !fileMetadata.isFile() ||
        fileMetadata.isSymbolicLink() ||
        fileMetadata.size > 4_096
      ) {
        throw invalidResources();
      }
      const encoded = fs.readFileSync(metadataPath, "utf8");
      if (!encoded.endsWith("\n") || encoded.slice(0, -1).includes("\n\n")) {
        throw invalidResources();
      }
      validateLegacyBuildMetadata(JSON.parse(encoded));
    } catch (_legacySchemaError) {
      throw invalidResources();
    }
  }
}

function prepareGeneratedResources({ desktopRoot, payload }) {
  if (typeof desktopRoot !== "string" || !path.isAbsolute(desktopRoot)) {
    throw invalidResources();
  }
  validateBuildMetadata(payload);
  const generatedParent = path.join(desktopRoot, ".generated");
  try {
    fs.mkdirSync(generatedParent, { mode: 0o700 });
  } catch (error) {
    if (error?.code !== "EEXIST") throw invalidResources();
  }
  requireUnlinkedDirectory(generatedParent);

  const generatedRoot = path.join(generatedParent, "career-hub");
  assertReplaceableStage(generatedRoot);
  const stagingRoot = fs.mkdtempSync(
    path.join(generatedParent, ".career-hub-stage-"),
  );
  try {
    fs.writeFileSync(
      path.join(stagingRoot, METADATA_NAME),
      `${JSON.stringify(payload, null, 2)}\n`,
      { encoding: "utf8", flag: "wx", mode: 0o600 },
    );
    assertGeneratedResourceInventory(stagingRoot, { trustedRoot: generatedParent });
    if (fs.existsSync(generatedRoot)) {
      fs.rmSync(generatedRoot, { recursive: true });
    }
    fs.renameSync(stagingRoot, generatedRoot);
  } catch (error) {
    fs.rmSync(stagingRoot, { force: true, recursive: true });
    if (error instanceof GeneratedResourceError) throw error;
    throw invalidResources();
  }
  return assertGeneratedResourceInventory(generatedRoot, { trustedRoot: generatedParent });
}

function packagedGeneratedResourceRoot(outputPath, platform) {
  return platform === "darwin"
    ? path.join(
        outputPath,
        "career-hub.app",
        "Contents",
        "Resources",
        "career-hub",
      )
    : path.join(outputPath, "resources", "career-hub");
}

module.exports = {
  GeneratedResourceError,
  assertGeneratedResourceInventory,
  packagedGeneratedResourceRoot,
  prepareGeneratedResources,
};
