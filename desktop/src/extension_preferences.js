"use strict";

const fs = require("node:fs");
const path = require("node:path");

const {
  BUNDLED_EXTENSION_ORIGIN,
  LaunchConfigurationError,
  normalizedExtensionOrigins,
} = require("./launch_configuration");

// ADR SEC-250 item 4, amended: the owner turns the extension endpoint on in
// Settings and the choice persists. The preference is a boolean plus any
// non-bundled origins (Firefox, Safari, another Chromium ID); it holds no
// secret. Pairing still gates every request, so this file grants nothing on its
// own. Host launch arguments keep working and are merged at sidecar start.
const PREFERENCES_FILE_NAME = "extension-preferences.json";
const PREFERENCES_SCHEMA_VERSION = 1;
const MAX_ORIGINS = 15; // plus the bundled origin stays within the host limit

class ExtensionPreferencesError extends Error {
  constructor(code) {
    super(code);
    this.name = "ExtensionPreferencesError";
    this.code = code;
  }
}

function defaultPreferences() {
  return Object.freeze({ enabled: false, origins: Object.freeze([]) });
}

function normalizedPreferences(value) {
  if (
    value === null ||
    typeof value !== "object" ||
    Array.isArray(value) ||
    Object.keys(value).sort().join(",") !== "enabled,origins" ||
    typeof value.enabled !== "boolean" ||
    !Array.isArray(value.origins) ||
    value.origins.length > MAX_ORIGINS
  ) {
    throw new ExtensionPreferencesError("DESKTOP_EXTENSION_PREFERENCES_INVALID");
  }
  let origins;
  try {
    origins = normalizedExtensionOrigins(value.origins);
  } catch (error) {
    if (error instanceof LaunchConfigurationError) {
      throw new ExtensionPreferencesError("DESKTOP_EXTENSION_PREFERENCES_INVALID");
    }
    throw error;
  }
  return Object.freeze({
    enabled: value.enabled,
    origins: Object.freeze(origins.filter((origin) => origin !== BUNDLED_EXTENSION_ORIGIN)),
  });
}

function validatePayload(payload) {
  if (
    payload === null ||
    typeof payload !== "object" ||
    Array.isArray(payload) ||
    Object.keys(payload).sort().join(",") !== "enabled,origins,schema_version" ||
    payload.schema_version !== PREFERENCES_SCHEMA_VERSION
  ) {
    throw new ExtensionPreferencesError("DESKTOP_EXTENSION_PREFERENCES_INVALID");
  }
  return normalizedPreferences({ enabled: payload.enabled, origins: payload.origins });
}

function createExtensionPreferenceStore(directoryPath, { fileSystem = fs } = {}) {
  if (
    typeof directoryPath !== "string" ||
    !path.isAbsolute(directoryPath) ||
    path.parse(directoryPath).root === directoryPath
  ) {
    throw new ExtensionPreferencesError("DESKTOP_EXTENSION_PREFERENCES_PATH_INVALID");
  }
  const preferencesPath = path.join(directoryPath, PREFERENCES_FILE_NAME);

  function read() {
    const metadata = fileSystem.lstatSync(preferencesPath, { throwIfNoEntry: false });
    if (!metadata) return defaultPreferences();
    if (!metadata.isFile() || metadata.isSymbolicLink() || metadata.size > 8_192) {
      throw new ExtensionPreferencesError("DESKTOP_EXTENSION_PREFERENCES_INVALID");
    }
    try {
      const encoded = fileSystem.readFileSync(preferencesPath, "utf8");
      if (!encoded.endsWith("\n")) {
        throw new ExtensionPreferencesError("DESKTOP_EXTENSION_PREFERENCES_INVALID");
      }
      return validatePayload(JSON.parse(encoded));
    } catch (error) {
      if (error instanceof ExtensionPreferencesError) throw error;
      throw new ExtensionPreferencesError("DESKTOP_EXTENSION_PREFERENCES_INVALID");
    }
  }

  function write(preferences) {
    const normalized = normalizedPreferences(preferences);
    const existing = fileSystem.lstatSync(preferencesPath, { throwIfNoEntry: false });
    if (existing && (!existing.isFile() || existing.isSymbolicLink())) {
      throw new ExtensionPreferencesError("DESKTOP_EXTENSION_PREFERENCES_INVALID");
    }
    try {
      fileSystem.writeFileSync(
        preferencesPath,
        `${JSON.stringify({
          enabled: normalized.enabled,
          origins: [...normalized.origins],
          schema_version: PREFERENCES_SCHEMA_VERSION,
        })}\n`,
        { encoding: "utf8", mode: 0o600 },
      );
      return normalized;
    } catch (_error) {
      throw new ExtensionPreferencesError("DESKTOP_EXTENSION_PREFERENCES_WRITE_FAILED");
    }
  }

  return Object.freeze({ read, write });
}

// The origins the sidecar is started with: the host launch arguments plus, when
// the owner turned the extension on, the bundled origin and the saved extras.
function effectiveExtensionOrigins({ launchOrigins = [], preferences = defaultPreferences() } = {}) {
  const combined = [...normalizedExtensionOrigins(launchOrigins)];
  if (preferences.enabled) {
    combined.push(BUNDLED_EXTENSION_ORIGIN, ...preferences.origins);
  }
  return normalizedExtensionOrigins(combined);
}

module.exports = {
  BUNDLED_EXTENSION_ORIGIN,
  ExtensionPreferencesError,
  MAX_ORIGINS,
  PREFERENCES_FILE_NAME,
  createExtensionPreferenceStore,
  defaultPreferences,
  effectiveExtensionOrigins,
  normalizedPreferences,
  validatePayload,
};
