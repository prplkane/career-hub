"use strict";

const fs = require("node:fs");
const path = require("node:path");

const PREFERENCES_FILE_NAME = "update-preferences.json";
const PREFERENCES_SCHEMA_VERSION = 1;

class UpdatePreferencesError extends Error {
  constructor(code) {
    super(code);
    this.name = "UpdatePreferencesError";
    this.code = code;
  }
}

function defaultPreferences() {
  return Object.freeze({ automaticUpdates: true });
}

function validatePayload(payload) {
  if (
    payload === null ||
    typeof payload !== "object" ||
    Array.isArray(payload) ||
    Object.keys(payload).sort().join(",") !== "automatic_updates,schema_version" ||
    payload.schema_version !== PREFERENCES_SCHEMA_VERSION ||
    typeof payload.automatic_updates !== "boolean"
  ) {
    throw new UpdatePreferencesError("DESKTOP_UPDATE_PREFERENCES_INVALID");
  }
  return Object.freeze({ automaticUpdates: payload.automatic_updates });
}

function createUpdatePreferenceStore(directoryPath, { fileSystem = fs } = {}) {
  if (
    typeof directoryPath !== "string" ||
    !path.isAbsolute(directoryPath) ||
    path.parse(directoryPath).root === directoryPath
  ) {
    throw new UpdatePreferencesError("DESKTOP_UPDATE_PREFERENCES_PATH_INVALID");
  }
  const preferencesPath = path.join(directoryPath, PREFERENCES_FILE_NAME);

  function read() {
    const metadata = fileSystem.lstatSync(preferencesPath, { throwIfNoEntry: false });
    if (!metadata) return defaultPreferences();
    if (!metadata.isFile() || metadata.isSymbolicLink() || metadata.size > 4_096) {
      throw new UpdatePreferencesError("DESKTOP_UPDATE_PREFERENCES_INVALID");
    }
    try {
      const encoded = fileSystem.readFileSync(preferencesPath, "utf8");
      if (!encoded.endsWith("\n")) {
        throw new UpdatePreferencesError("DESKTOP_UPDATE_PREFERENCES_INVALID");
      }
      return validatePayload(JSON.parse(encoded));
    } catch (error) {
      if (error instanceof UpdatePreferencesError) throw error;
      throw new UpdatePreferencesError("DESKTOP_UPDATE_PREFERENCES_INVALID");
    }
  }

  function write(automaticUpdates) {
    if (typeof automaticUpdates !== "boolean") {
      throw new UpdatePreferencesError("DESKTOP_UPDATE_PREFERENCES_INVALID");
    }
    const existing = fileSystem.lstatSync(preferencesPath, { throwIfNoEntry: false });
    if (existing && (!existing.isFile() || existing.isSymbolicLink())) {
      throw new UpdatePreferencesError("DESKTOP_UPDATE_PREFERENCES_INVALID");
    }
    try {
      fileSystem.writeFileSync(
        preferencesPath,
        `${JSON.stringify({
          automatic_updates: automaticUpdates,
          schema_version: PREFERENCES_SCHEMA_VERSION,
        })}\n`,
        { encoding: "utf8", mode: 0o600 },
      );
      return Object.freeze({ automaticUpdates });
    } catch (_error) {
      throw new UpdatePreferencesError("DESKTOP_UPDATE_PREFERENCES_WRITE_FAILED");
    }
  }

  return Object.freeze({ read, write });
}

module.exports = {
  PREFERENCES_FILE_NAME,
  UpdatePreferencesError,
  createUpdatePreferenceStore,
  defaultPreferences,
  validatePayload,
};
