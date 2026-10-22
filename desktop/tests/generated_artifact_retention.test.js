"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const {
  GeneratedArtifactRetentionError,
  pruneVersionedDesktopArchives,
} = require("../src/generated_artifact_retention");

test("desktop archive retention removes only superseded SemVer directories", (t) => {
  const projectRoot = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-retention-"));
  t.after(() => fs.rmSync(projectRoot, { force: true, recursive: true }));
  const root = path.join(projectRoot, "outputs", "desktop", "windows");
  for (const name of ["0.1.0", "0.1.3", "notes", "current"]) {
    fs.mkdirSync(path.join(root, name), { recursive: true });
    fs.writeFileSync(path.join(root, name, "marker.txt"), name);
  }
  fs.writeFileSync(path.join(root, "0.1.2.txt"), "not a version directory");

  assert.deepEqual(
    pruneVersionedDesktopArchives({ currentVersion: "0.1.3", projectRoot }),
    ["0.1.0"],
  );
  assert.equal(fs.existsSync(path.join(root, "0.1.0")), false);
  assert.equal(fs.existsSync(path.join(root, "0.1.3", "marker.txt")), true);
  assert.equal(fs.existsSync(path.join(root, "notes", "marker.txt")), true);
  assert.equal(fs.existsSync(path.join(root, "current", "marker.txt")), true);
  assert.equal(fs.existsSync(path.join(root, "0.1.2.txt")), true);
});

test("desktop archive retention rejects invalid versions and roots", (t) => {
  const projectRoot = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-retention-"));
  t.after(() => fs.rmSync(projectRoot, { force: true, recursive: true }));
  const root = path.join(projectRoot, "outputs", "desktop", "windows");
  fs.mkdirSync(path.dirname(root), { recursive: true });
  fs.writeFileSync(root, "not a directory");

  assert.throws(
    () => pruneVersionedDesktopArchives({ currentVersion: "0.1.3", projectRoot }),
    (error) =>
      error instanceof GeneratedArtifactRetentionError &&
      error.code === "DESKTOP_GENERATED_ARTIFACT_ROOT_INVALID",
  );
  assert.throws(
    () => pruneVersionedDesktopArchives({ currentVersion: "v0.1.3", projectRoot }),
    (error) =>
      error instanceof GeneratedArtifactRetentionError &&
      error.code === "DESKTOP_GENERATED_ARTIFACT_INPUT_INVALID",
  );
});
