"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const {
  PREFERENCES_FILE_NAME,
  UpdatePreferencesError,
  createUpdatePreferenceStore,
} = require("../src/update_preferences");

function temporaryDirectory(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-update-prefs-"));
  t.after(() => fs.rmSync(root, { force: true, recursive: true }));
  return root;
}

test("automatic updates default on and persist per desktop channel", (t) => {
  const root = temporaryDirectory(t);
  const store = createUpdatePreferenceStore(root);
  assert.deepEqual(store.read(), { automaticUpdates: true });

  assert.deepEqual(store.write(false), { automaticUpdates: false });
  assert.deepEqual(store.read(), { automaticUpdates: false });
  assert.deepEqual(
    JSON.parse(fs.readFileSync(path.join(root, PREFERENCES_FILE_NAME), "utf8")),
    { automatic_updates: false, schema_version: 1 },
  );
});

test("linked or malformed preference files fail closed", (t) => {
  const root = temporaryDirectory(t);
  const external = path.join(root, "external.json");
  const preferences = path.join(root, PREFERENCES_FILE_NAME);
  fs.writeFileSync(external, '{"automatic_updates":false,"schema_version":1}\n');
  try {
    fs.symlinkSync(external, preferences, "file");
  } catch (error) {
    if (["EPERM", "EACCES", "ENOSYS"].includes(error?.code)) {
      t.skip("The current platform cannot create a file link fixture");
      return;
    }
    throw error;
  }
  const store = createUpdatePreferenceStore(root);
  assert.throws(
    () => store.read(),
    (error) =>
      error instanceof UpdatePreferencesError &&
      error.code === "DESKTOP_UPDATE_PREFERENCES_INVALID",
  );
  assert.equal(fs.readFileSync(external, "utf8").includes("false"), true);
});
