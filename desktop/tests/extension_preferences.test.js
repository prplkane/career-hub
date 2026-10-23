"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const {
  ExtensionPreferencesError,
  PREFERENCES_FILE_NAME,
  createExtensionPreferenceStore,
  effectiveExtensionOrigins,
} = require("../src/extension_preferences");
const { BUNDLED_EXTENSION_ORIGIN } = require("../src/launch_configuration");

const FIREFOX = "moz-extension://4be18b7a-7660-4ec3-8d76-2b11a4af40d1";

function temporaryDirectory(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-extension-prefs-"));
  t.after(() => fs.rmSync(root, { force: true, recursive: true }));
  return root;
}

test("the extension is off by default and the owner's choice persists", (t) => {
  const root = temporaryDirectory(t);
  const store = createExtensionPreferenceStore(root);
  assert.deepEqual(store.read(), { enabled: false, origins: [] });

  assert.deepEqual(store.write({ enabled: true, origins: [`${FIREFOX}/`] }), {
    enabled: true, origins: [FIREFOX],
  });
  assert.deepEqual(store.read(), { enabled: true, origins: [FIREFOX] });
  assert.deepEqual(
    JSON.parse(fs.readFileSync(path.join(root, PREFERENCES_FILE_NAME), "utf8")),
    { enabled: true, origins: [FIREFOX], schema_version: 1 },
  );
  // The bundled origin is implied by "enabled" and never stored twice.
  assert.deepEqual(store.write({ enabled: true, origins: [BUNDLED_EXTENSION_ORIGIN] }), {
    enabled: true, origins: [],
  });
});

test("only validated extension origins are accepted from the window", (t) => {
  const root = temporaryDirectory(t);
  const store = createExtensionPreferenceStore(root);
  for (const request of [
    null, [], { enabled: "yes", origins: [] }, { enabled: true },
    { enabled: true, origins: "moz-extension://a" }, { enabled: true, origins: [], extra: 1 },
    { enabled: true, origins: ["https://example.test"] },
    { enabled: true, origins: ["chrome-extension://a/path"] },
    { enabled: true, origins: ["chrome-extension://a?query"] },
    { enabled: true, origins: [`chrome-extension://${"a".repeat(256)}`] },
    { enabled: true, origins: Array.from({ length: 16 }, (_, index) => `moz-extension://origin-${index}`) },
  ]) {
    assert.throws(() => store.write(request), (error) =>
      error instanceof ExtensionPreferencesError && error.code === "DESKTOP_EXTENSION_PREFERENCES_INVALID");
  }
  assert.deepEqual(store.read(), { enabled: false, origins: [] }, "a rejected write changes nothing");
});

test("linked or malformed preference files fail closed", (t) => {
  const root = temporaryDirectory(t);
  const preferences = path.join(root, PREFERENCES_FILE_NAME);
  fs.writeFileSync(preferences, '{"enabled":true,"origins":["https://example.test"],"schema_version":1}\n');
  assert.throws(() => createExtensionPreferenceStore(root).read(), (error) =>
    error instanceof ExtensionPreferencesError && error.code === "DESKTOP_EXTENSION_PREFERENCES_INVALID");
  fs.writeFileSync(preferences, '{"enabled":true,"origins":[],"schema_version":1}');
  assert.throws(() => createExtensionPreferenceStore(root).read(), { code: "DESKTOP_EXTENSION_PREFERENCES_INVALID" });

  const external = path.join(root, "external.json");
  fs.writeFileSync(external, '{"enabled":true,"origins":[],"schema_version":1}\n');
  fs.rmSync(preferences);
  try {
    fs.symlinkSync(external, preferences, "file");
  } catch (error) {
    if (["EPERM", "EACCES", "ENOSYS"].includes(error?.code)) {
      t.skip("The current platform cannot create a file link fixture");
      return;
    }
    throw error;
  }
  assert.throws(() => createExtensionPreferenceStore(root).read(), { code: "DESKTOP_EXTENSION_PREFERENCES_INVALID" });
});

test("effective origins merge launch arguments with the saved preference", () => {
  assert.deepEqual(effectiveExtensionOrigins(), []);
  assert.deepEqual(effectiveExtensionOrigins({ preferences: { enabled: false, origins: [FIREFOX] } }), []);
  assert.deepEqual(effectiveExtensionOrigins({ preferences: { enabled: true, origins: [] } }), [BUNDLED_EXTENSION_ORIGIN]);
  assert.deepEqual(
    effectiveExtensionOrigins({ preferences: { enabled: true, origins: [FIREFOX] } }),
    [BUNDLED_EXTENSION_ORIGIN, FIREFOX],
  );
  assert.deepEqual(
    effectiveExtensionOrigins({ launchOrigins: [FIREFOX], preferences: { enabled: true, origins: [FIREFOX] } }),
    [FIREFOX, BUNDLED_EXTENSION_ORIGIN],
    "duplicates collapse",
  );
  assert.deepEqual(
    effectiveExtensionOrigins({ launchOrigins: [BUNDLED_EXTENSION_ORIGIN], preferences: { enabled: false, origins: [] } }),
    [BUNDLED_EXTENSION_ORIGIN],
    "the development launch flag still works on its own",
  );
  assert.throws(() => effectiveExtensionOrigins({ launchOrigins: ["https://example.test"] }),
    { code: "DESKTOP_LAUNCH_CONFIGURATION_INVALID" });
});
