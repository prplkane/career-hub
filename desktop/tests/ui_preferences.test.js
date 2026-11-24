"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const {
  PREFERENCES_FILE_NAME,
  PREFERENCE_KEYS,
  UiPreferencesError,
  createUiPreferenceStore,
} = require("../src/ui_preferences");

function temporaryDirectory(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-ui-prefs-"));
  t.after(() => fs.rmSync(root, { force: true, recursive: true }));
  return root;
}

test("nothing is saved at first; a choice survives a new store on the same folder", (t) => {
  const root = temporaryDirectory(t);
  assert.deepEqual(createUiPreferenceStore(root).read(), {});
  createUiPreferenceStore(root).set("careerHub.locale", "en");
  createUiPreferenceStore(root).set("careerHub.theme", "dark");
  // A fresh store stands in for the next launch.
  assert.deepEqual(createUiPreferenceStore(root).read(), { "careerHub.locale": "en", "careerHub.theme": "dark" });
});

test("only the eight UI choices are kept, each with a value the UI accepts", (t) => {
  const store = createUiPreferenceStore(temporaryDirectory(t));
  assert.deepEqual([...PREFERENCE_KEYS].sort(), [
    "careerHub.demandGapsPeriod", "careerHub.jobFeedMode", "careerHub.jobFeedPageSize", "careerHub.lastView",
    "careerHub.locale", "careerHub.skillsHubMode", "careerHub.skillsRegistryLayout", "careerHub.theme",
  ]);
  for (const [key, value] of [
    ["careerHub.theme", "light"], ["careerHub.lastView", "resume-library"], ["careerHub.jobFeedPageSize", "50"],
    ["careerHub.jobFeedMode", "email"], ["careerHub.skillsHubMode", "all"], ["careerHub.skillsRegistryLayout", "cards"],
    ["careerHub.demandGapsPeriod", JSON.stringify({ preset: "custom", startDate: "2026-10-01", endDate: "" })],
  ]) {
    store.set(key, value);
  }
  for (const [key, value] of [
    ["careerHub.theme", "neon"], ["careerHub.locale", "de"], ["careerHub.lastView", "../settings"],
    ["careerHub.jobFeedPageSize", "1000"], ["careerHub.demandGapsPeriod", "{\"preset\":\"all\",\"extra\":1}"],
    ["careerHub.demandGapsPeriod", "{\"preset\":\"custom\",\"startDate\":\"yesterday\"}"],
    ["careerHub.sessionToken", "secret"], ["careerHub.theme", 1],
  ]) {
    assert.throws(() => store.set(key, value), (error) => error instanceof UiPreferencesError && error.code === "DESKTOP_UI_PREFERENCES_INVALID", `${key}=${value}`);
  }
  assert.equal(store.read()["careerHub.theme"], "light");
});

test("null forgets a choice", (t) => {
  const store = createUiPreferenceStore(temporaryDirectory(t));
  store.set("careerHub.locale", "ru");
  assert.deepEqual(store.set("careerHub.locale", null), {});
  assert.deepEqual(store.read(), {});
});

test("a damaged or tampered file means nothing saved, never a failed start", (t) => {
  const root = temporaryDirectory(t);
  const file = path.join(root, PREFERENCES_FILE_NAME);
  const store = createUiPreferenceStore(root);
  for (const content of ["not json", "[]", "{\"schema_version\":2,\"values\":{}}", "{\"schema_version\":1,\"values\":[]}", "x".repeat(5000)]) {
    fs.writeFileSync(file, content);
    assert.deepEqual(store.read(), {}, content.slice(0, 30));
  }
  // Bad entries are dropped one by one; good ones survive.
  fs.writeFileSync(file, JSON.stringify({ schema_version: 1, values: { "careerHub.locale": "en", "careerHub.theme": "neon", "careerHub.token": "x" } }));
  assert.deepEqual(store.read(), { "careerHub.locale": "en" });
});

test("the store refuses a relative or root folder", () => {
  for (const folder of ["relative/dir", path.parse(process.cwd()).root, ""]) {
    assert.throws(() => createUiPreferenceStore(folder), (error) => error.code === "DESKTOP_UI_PREFERENCES_PATH_INVALID");
  }
});
