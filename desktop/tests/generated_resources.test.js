"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const {
  GeneratedResourceError,
  assertGeneratedResourceInventory,
  packagedGeneratedResourceRoot,
  prepareGeneratedResources,
} = require("../src/generated_resources");

function temporaryDirectory(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-generated-"));
  t.after(() => fs.rmSync(root, { force: true, recursive: true }));
  return root;
}

function payload(channel = "dev") {
  return {
    app_version: "1.2.3",
    channel,
    schema_version: 4,
    sidecar_runtime: "go",
    update_feed_url: null,
  };
}

test("preparation creates and replaces only the exact metadata inventory", (t) => {
  const desktopRoot = temporaryDirectory(t);
  const generatedRoot = prepareGeneratedResources({ desktopRoot, payload: payload() });
  assert.deepEqual(fs.readdirSync(generatedRoot), ["build-metadata.json"]);

  prepareGeneratedResources({ desktopRoot, payload: payload("stable") });
  assertGeneratedResourceInventory(generatedRoot);
  assert.deepEqual(
    JSON.parse(fs.readFileSync(path.join(generatedRoot, "build-metadata.json"))),
    payload("stable"),
  );
});

test("preparation replaces only the exact legacy metadata inventory", (t) => {
  const desktopRoot = temporaryDirectory(t);
  const generatedRoot = path.join(desktopRoot, ".generated", "career-hub");
  fs.mkdirSync(generatedRoot, { recursive: true });
  fs.writeFileSync(
    path.join(generatedRoot, "build-metadata.json"),
    '{"app_version":"1.2.2","channel":"dev","schema_version":1}\n',
  );

  prepareGeneratedResources({ desktopRoot, payload: payload() });
  assert.deepEqual(
    JSON.parse(fs.readFileSync(path.join(generatedRoot, "build-metadata.json"))),
    payload(),
  );
});

test("schema 2 is replaceable but never admitted as a packaged runtime", (t) => {
  const desktopRoot = temporaryDirectory(t);
  const generatedRoot = path.join(desktopRoot, ".generated", "career-hub");
  fs.mkdirSync(generatedRoot, { recursive: true });
  const metadataPath = path.join(generatedRoot, "build-metadata.json");
  const legacy = { app_version: "1.2.2", channel: "dev", schema_version: 2, update_feed_url: null };
  for (const override of [{ unexpected: true }, { sidecar_runtime: "python" },
    { update_feed_url: "http://unsafe.example.test/feed" }]) {
    const encoded = JSON.stringify({ ...legacy, ...override }) + "\n";
    fs.writeFileSync(metadataPath, encoded);
    assert.throws(() => prepareGeneratedResources({ desktopRoot, payload: payload() }), GeneratedResourceError);
    assert.equal(fs.readFileSync(metadataPath, "utf8"), encoded);
  }
  fs.writeFileSync(metadataPath, JSON.stringify(legacy) + "\n");
  assert.throws(() => assertGeneratedResourceInventory(generatedRoot), GeneratedResourceError);
  prepareGeneratedResources({ desktopRoot, payload: payload() });
  assertGeneratedResourceInventory(generatedRoot);
  assert.deepEqual(JSON.parse(fs.readFileSync(metadataPath)), payload());
});

test("unexpected ignored siblings fail closed and remain untouched", (t) => {
  const desktopRoot = temporaryDirectory(t);
  const generatedRoot = path.join(desktopRoot, ".generated", "career-hub");
  fs.mkdirSync(generatedRoot, { recursive: true });
  const sentinel = path.join(generatedRoot, "stale-private-sentinel.txt");
  fs.writeFileSync(sentinel, "private\n");

  assert.throws(
    () => prepareGeneratedResources({ desktopRoot, payload: payload() }),
    (error) =>
      error instanceof GeneratedResourceError &&
      error.code === "DESKTOP_GENERATED_RESOURCES_INVALID",
  );
  assert.equal(fs.readFileSync(sentinel, "utf8"), "private\n");
});

test("linked stage roots fail without touching their targets", (t) => {
  const desktopRoot = temporaryDirectory(t);
  const external = temporaryDirectory(t);
  const sentinel = path.join(external, "sentinel.txt");
  fs.writeFileSync(sentinel, "outside\n");
  fs.mkdirSync(path.join(desktopRoot, ".generated"));
  try {
    fs.symlinkSync(
      external,
      path.join(desktopRoot, ".generated", "career-hub"),
      process.platform === "win32" ? "junction" : "dir",
    );
  } catch (error) {
    if (["EPERM", "EACCES", "ENOSYS"].includes(error?.code)) {
      t.skip("The current platform cannot create a linked directory fixture");
      return;
    }
    throw error;
  }

  assert.throws(
    () => prepareGeneratedResources({ desktopRoot, payload: payload() }),
    (error) => error instanceof GeneratedResourceError,
  );
  assert.equal(fs.readFileSync(sentinel, "utf8"), "outside\n");
});

test("a linked generated parent cannot bypass direct package preflight", (t) => {
  const desktopRoot = temporaryDirectory(t);
  const external = temporaryDirectory(t);
  const generatedRoot = path.join(external, "career-hub");
  fs.mkdirSync(generatedRoot);
  fs.writeFileSync(
    path.join(generatedRoot, "build-metadata.json"),
    `${JSON.stringify(payload())}\n`,
  );
  try {
    fs.symlinkSync(
      external,
      path.join(desktopRoot, ".generated"),
      process.platform === "win32" ? "junction" : "dir",
    );
  } catch (error) {
    if (["EPERM", "EACCES", "ENOSYS"].includes(error?.code)) {
      t.skip("The current platform cannot create a linked directory fixture");
      return;
    }
    throw error;
  }

  assert.throws(
    () =>
      assertGeneratedResourceInventory(
        path.join(desktopRoot, ".generated", "career-hub"),
        { trustedRoot: desktopRoot },
      ),
    (error) => error instanceof GeneratedResourceError,
  );
});

test("packaged inventory check rejects a sibling injected after preparation", (t) => {
  const packageRoot = temporaryDirectory(t);
  const generatedRoot = packagedGeneratedResourceRoot(packageRoot, "win32");
  fs.mkdirSync(generatedRoot, { recursive: true });
  fs.writeFileSync(
    path.join(generatedRoot, "build-metadata.json"),
    `${JSON.stringify(payload())}\n`,
  );
  assertGeneratedResourceInventory(generatedRoot);

  fs.writeFileSync(path.join(generatedRoot, "injected.txt"), "private\n");
  assert.throws(
    () => assertGeneratedResourceInventory(generatedRoot),
    (error) => error instanceof GeneratedResourceError,
  );
});

test("packaged resource paths preserve the accepted platform layout", () => {
  assert.equal(
    packagedGeneratedResourceRoot("C:\\package", "win32"),
    path.join("C:\\package", "resources", "career-hub"),
  );
  assert.equal(
    packagedGeneratedResourceRoot("/package", "darwin"),
    path.join(
      "/package",
      "career-hub.app",
      "Contents",
      "Resources",
      "career-hub",
    ),
  );
});
