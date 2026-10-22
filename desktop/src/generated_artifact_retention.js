"use strict";

const fs = require("node:fs");
const path = require("node:path");

const APP_VERSION = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;

class GeneratedArtifactRetentionError extends Error {
  constructor(code) {
    super(code);
    this.name = "GeneratedArtifactRetentionError";
    this.code = code;
  }
}

function assertDirectChild(parent, candidate) {
  const resolvedParent = path.resolve(parent);
  const resolvedCandidate = path.resolve(candidate);
  if (path.dirname(resolvedCandidate) !== resolvedParent) {
    throw new GeneratedArtifactRetentionError(
      "DESKTOP_GENERATED_ARTIFACT_PATH_INVALID",
    );
  }
  return resolvedCandidate;
}

function pruneVersionedDesktopArchives({
  currentVersion,
  projectRoot,
  fileSystem = fs,
}) {
  if (
    typeof currentVersion !== "string" ||
    !APP_VERSION.test(currentVersion) ||
    typeof projectRoot !== "string" ||
    !path.isAbsolute(projectRoot)
  ) {
    throw new GeneratedArtifactRetentionError(
      "DESKTOP_GENERATED_ARTIFACT_INPUT_INVALID",
    );
  }

  const archiveRoot = path.resolve(
    projectRoot,
    "outputs",
    "desktop",
    "windows",
  );
  const rootRecord = fileSystem.lstatSync(archiveRoot, { throwIfNoEntry: false });
  if (rootRecord === undefined) return Object.freeze([]);
  if (!rootRecord.isDirectory() || rootRecord.isSymbolicLink()) {
    throw new GeneratedArtifactRetentionError(
      "DESKTOP_GENERATED_ARTIFACT_ROOT_INVALID",
    );
  }

  const removed = [];
  for (const entry of fileSystem.readdirSync(archiveRoot, {
    withFileTypes: true,
  })) {
    if (
      !entry.isDirectory() ||
      entry.isSymbolicLink() ||
      !APP_VERSION.test(entry.name) ||
      entry.name === currentVersion
    ) {
      continue;
    }
    const target = assertDirectChild(archiveRoot, path.join(archiveRoot, entry.name));
    fileSystem.rmSync(target, { force: false, recursive: true });
    removed.push(entry.name);
  }
  return Object.freeze(removed.sort());
}

module.exports = {
  GeneratedArtifactRetentionError,
  pruneVersionedDesktopArchives,
};
