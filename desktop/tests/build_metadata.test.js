"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const {
  BuildMetadataError,
  loadBuildMetadata,
  validateBuildMetadata,
} = require("../src/build_metadata");

function temporaryDirectory(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-metadata-"));
  t.after(() => fs.rmSync(root, { force: true, recursive: true }));
  return root;
}

test("Go preview metadata is dev-only, explicit and has no update feed", (t) => {
  const payload = { app_version: "1.2.3", channel: "dev", schema_version: 3,
    update_feed_url: null, sidecar_runtime: "go-preview" };
  const root = temporaryDirectory(t);
  fs.mkdirSync(path.join(root, "career-hub"));
  fs.writeFileSync(path.join(root, "career-hub/build-metadata.json"), JSON.stringify(payload));
  assert.deepEqual(loadBuildMetadata({ isPackaged: true, processResourcesPath: root, projectRoot: "unused" }), {
    appVersion: "1.2.3", channel: "dev", schemaVersion: 3, updateFeedUrl: null, sidecarRuntime: "go-preview" });
  for (const override of [{ channel: "stable" }, { channel: "beta" }, { sidecar_runtime: "python" },
    { sidecar_runtime: "go" }, { schema_version: 2 }, { update_feed_url: "https://updates.example.test/dev" },
    { unexpected: true }, { sidecar_runtime: undefined }]) {
    assert.throws(() => validateBuildMetadata({ ...payload, ...override }), BuildMetadataError);
  }
});

test("development metadata consumes the repository VERSION and fixed dev channel", (t) => {
  const root = temporaryDirectory(t);
  fs.writeFileSync(path.join(root, "VERSION"), "1.2.3\n");
  assert.deepEqual(
    loadBuildMetadata({
      isPackaged: false,
      processResourcesPath: "unused",
      projectRoot: root,
    }),
    {
      appVersion: "1.2.3",
      channel: "dev",
      schemaVersion: 4,
      sidecarRuntime: "go",
      updateFeedUrl: null,
    },
  );
});

test("standard Go metadata refuses retired or implicit runtime selection", () => {
  const payload = { app_version: "1.2.3", channel: "stable", schema_version: 4,
    update_feed_url: null, sidecar_runtime: "go" };
  for (const override of [{ schema_version: 2 }, { sidecar_runtime: undefined },
    { sidecar_runtime: "python" }, { sidecar_runtime: "go-preview" }]) {
    assert.throws(() => validateBuildMetadata({ ...payload, ...override }), BuildMetadataError);
  }
});

test("packaged metadata is exact and rejects extra fields", (t) => {
  const root = temporaryDirectory(t);
  const metadataRoot = path.join(root, "career-hub");
  fs.mkdirSync(metadataRoot);
  fs.writeFileSync(
    path.join(metadataRoot, "build-metadata.json"),
    '{"app_version":"2.0.0","channel":"stable","schema_version":4,"sidecar_runtime":"go","update_feed_url":"https://updates.example.com/stable/win32/x64"}\n',
  );
  assert.deepEqual(
    loadBuildMetadata({
      isPackaged: true,
      processResourcesPath: root,
      projectRoot: "unused",
    }),
    {
      appVersion: "2.0.0",
      channel: "stable",
      schemaVersion: 4,
      sidecarRuntime: "go",
      updateFeedUrl: "https://updates.example.com/stable/win32/x64",
    },
  );
  assert.throws(
    () =>
      validateBuildMetadata({
        app_version: "2.0.0",
        channel: "stable",
        schema_version: 4,
        sidecar_runtime: "go",
        update_feed_url: null,
        unexpected: true,
      }),
    (error) =>
      error instanceof BuildMetadataError &&
      error.code === "DESKTOP_BUILD_METADATA_INVALID",
  );
});

test("update feeds must be public HTTPS paths without embedded credentials", () => {
  for (const updateFeedUrl of [
    "http://updates.example.com/stable/win32/x64",
    "https://user:secret@updates.example.com/stable/win32/x64",
    "https://updates.example.com/stable/win32/x64?token=secret",
    "https://updates.example.com/",
  ]) {
    assert.throws(
      () =>
        validateBuildMetadata({
          app_version: "2.0.0",
          channel: "stable",
          schema_version: 4,
        sidecar_runtime: "go",
          update_feed_url: updateFeedUrl,
        }),
      (error) =>
        error instanceof BuildMetadataError &&
        error.code === "DESKTOP_BUILD_METADATA_INVALID",
    );
  }
});

test("non-canonical versions fail closed", (t) => {
  const root = temporaryDirectory(t);
  fs.writeFileSync(path.join(root, "VERSION"), "v1.2.3\n");
  assert.throws(
    () =>
      loadBuildMetadata({
        isPackaged: false,
        processResourcesPath: "unused",
        projectRoot: root,
      }),
    (error) =>
      error instanceof BuildMetadataError &&
      error.code === "DESKTOP_VERSION_INVALID",
  );
});
