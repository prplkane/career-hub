"use strict";

const fs = require("node:fs");
const path = require("node:path");

// The sidecar binds a new port at every launch, so the page's origin, and the
// browser storage that belongs to it, starts empty each time: theme and
// language had to be chosen again on every start. These few view choices live
// in a desktop-owned file instead. They are plain UI settings, never content,
// paths or secrets, and each value must be one the UI itself accepts.
const PREFERENCES_FILE_NAME = "ui-preferences.json";
const PREFERENCES_SCHEMA_VERSION = 1;
const MAX_FILE_BYTES = 4_096;
const ISO_DATE = /^\d{4}-\d{2}-\d{2}$/;

class UiPreferencesError extends Error {
  constructor(code) {
    super(code);
    this.name = "UiPreferencesError";
    this.code = code;
  }
}

function oneOf(...allowed) {
  return (value) => allowed.includes(value);
}

// Insights stores its period as JSON: {preset, startDate?, endDate?}.
function demandGapsPeriod(value) {
  if (value.length > 200) return false;
  let parsed;
  try {
    parsed = JSON.parse(value);
  } catch {
    return false;
  }
  if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) return false;
  if (!Object.keys(parsed).every((key) => ["preset", "startDate", "endDate"].includes(key))) return false;
  if (!["all", "30", "90", "180", "custom"].includes(parsed.preset)) return false;
  return ["startDate", "endDate"].every((key) => (
    parsed[key] === undefined || parsed[key] === "" || (typeof parsed[key] === "string" && ISO_DATE.test(parsed[key]))
  ));
}

const PREFERENCE_RULES = Object.freeze({
  "careerHub.theme": oneOf("system", "light", "dark"),
  "careerHub.locale": oneOf("system", "en", "ru"),
  "careerHub.lastView": (value) => /^[a-z][a-z0-9-]{0,39}$/.test(value),
  "careerHub.jobFeedPageSize": oneOf("10", "20", "50"),
  "careerHub.jobFeedMode": oneOf("feed", "email", "watchers"),
  "careerHub.skillsHubMode": oneOf("groups", "all"),
  "careerHub.skillsRegistryLayout": oneOf("list", "cards"),
  "careerHub.demandGapsPeriod": demandGapsPeriod,
});

function validEntry(key, value) {
  return Object.hasOwn(PREFERENCE_RULES, key) && typeof value === "string" && PREFERENCE_RULES[key](value);
}

function createUiPreferenceStore(directoryPath, { fileSystem = fs } = {}) {
  if (
    typeof directoryPath !== "string" ||
    !path.isAbsolute(directoryPath) ||
    path.parse(directoryPath).root === directoryPath
  ) {
    throw new UiPreferencesError("DESKTOP_UI_PREFERENCES_PATH_INVALID");
  }
  const preferencesPath = path.join(directoryPath, PREFERENCES_FILE_NAME);

  // These are conveniences: a missing, damaged or foreign file means "nothing
  // saved", never a startup failure. Unknown keys and bad values are dropped.
  function read() {
    try {
      const metadata = fileSystem.lstatSync(preferencesPath, { throwIfNoEntry: false });
      if (!metadata || !metadata.isFile() || metadata.isSymbolicLink() || metadata.size > MAX_FILE_BYTES) {
        return Object.freeze({});
      }
      const payload = JSON.parse(fileSystem.readFileSync(preferencesPath, "utf8"));
      if (
        payload === null || typeof payload !== "object" || Array.isArray(payload) ||
        payload.schema_version !== PREFERENCES_SCHEMA_VERSION ||
        payload.values === null || typeof payload.values !== "object" || Array.isArray(payload.values)
      ) {
        return Object.freeze({});
      }
      const values = {};
      for (const [key, value] of Object.entries(payload.values)) {
        if (validEntry(key, value)) values[key] = value;
      }
      return Object.freeze(values);
    } catch {
      return Object.freeze({});
    }
  }

  // value null forgets the choice. Anything outside the allow-list is refused.
  function set(key, value) {
    if (!Object.hasOwn(PREFERENCE_RULES, key) || (value !== null && !validEntry(key, value))) {
      throw new UiPreferencesError("DESKTOP_UI_PREFERENCES_INVALID");
    }
    const values = { ...read() };
    if (value === null) delete values[key];
    else values[key] = value;
    const existing = fileSystem.lstatSync(preferencesPath, { throwIfNoEntry: false });
    if (existing && (!existing.isFile() || existing.isSymbolicLink())) {
      throw new UiPreferencesError("DESKTOP_UI_PREFERENCES_INVALID");
    }
    try {
      fileSystem.writeFileSync(
        preferencesPath,
        `${JSON.stringify({ schema_version: PREFERENCES_SCHEMA_VERSION, values })}\n`,
        { encoding: "utf8", mode: 0o600 },
      );
    } catch {
      throw new UiPreferencesError("DESKTOP_UI_PREFERENCES_WRITE_FAILED");
    }
    return Object.freeze(values);
  }

  return Object.freeze({ read, set });
}

module.exports = {
  PREFERENCES_FILE_NAME,
  PREFERENCE_KEYS: Object.freeze(Object.keys(PREFERENCE_RULES)),
  UiPreferencesError,
  createUiPreferenceStore,
};
