"use strict";

const fs = require("node:fs");
const path = require("node:path");

const CHANNELS = Object.freeze(new Set(["dev", "stable"]));
const METADATA_FIELDS = Object.freeze([
  "app_version",
  "channel",
  "schema_version",
  "update_feed_url",
]);
const LEGACY_METADATA_FIELDS = Object.freeze([
  "app_version",
  "channel",
  "schema_version",
]);
const METADATA_SCHEMA_VERSION = 4;
const GO_PREVIEW_METADATA_SCHEMA_VERSION = 3;
const SEMVER = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;

class BuildMetadataError extends Error {
  constructor(code) {
    super(code);
    this.name = "BuildMetadataError";
    this.code = code;
  }
}

function readApplicationVersion(projectRoot) {
  let value;
  try {
    value = fs.readFileSync(path.join(projectRoot, "VERSION"), "utf8");
  } catch (_error) {
    throw new BuildMetadataError("DESKTOP_VERSION_UNAVAILABLE");
  }
  if (!value.endsWith("\n") || value.slice(0, -1).includes("\n")) {
    throw new BuildMetadataError("DESKTOP_VERSION_INVALID");
  }
  const version = value.slice(0, -1);
  if (!SEMVER.test(version)) {
    throw new BuildMetadataError("DESKTOP_VERSION_INVALID");
  }
  return version;
}

function validatedUpdateFeedUrl(value) {
  if (value === null) return null;
  if (typeof value !== "string" || value.length > 2_048) {
    throw new BuildMetadataError("DESKTOP_BUILD_METADATA_INVALID");
  }
  let parsed;
  try {
    parsed = new URL(value);
  } catch (_error) {
    throw new BuildMetadataError("DESKTOP_BUILD_METADATA_INVALID");
  }
  if (
    parsed.protocol !== "https:" ||
    parsed.username !== "" ||
    parsed.password !== "" ||
    parsed.search !== "" ||
    parsed.hash !== "" ||
    parsed.pathname === "/"
  ) {
    throw new BuildMetadataError("DESKTOP_BUILD_METADATA_INVALID");
  }
  return parsed.toString().replace(/\/$/, "");
}

function validateBuildMetadata(payload) {
  if (payload === null || typeof payload !== "object" || Array.isArray(payload)) {
    throw new BuildMetadataError("DESKTOP_BUILD_METADATA_INVALID");
  }
  const fields = Object.keys(payload).sort();
  const goPreview = payload.schema_version === GO_PREVIEW_METADATA_SCHEMA_VERSION;
  const expectedFields = [...METADATA_FIELDS, "sidecar_runtime"].sort();
  if (
    fields.length !== expectedFields.length ||
    fields.some((field, index) => field !== expectedFields[index])
  ) {
    throw new BuildMetadataError("DESKTOP_BUILD_METADATA_INVALID");
  }
  if (typeof payload.app_version !== "string" || !SEMVER.test(payload.app_version)) {
    throw new BuildMetadataError("DESKTOP_BUILD_METADATA_INVALID");
  }
  if (
    typeof payload.channel !== "string" ||
    !CHANNELS.has(payload.channel) ||
    (!goPreview && payload.schema_version !== METADATA_SCHEMA_VERSION)
  ) {
    throw new BuildMetadataError("DESKTOP_BUILD_METADATA_INVALID");
  }
  const updateFeedUrl = validatedUpdateFeedUrl(payload.update_feed_url);
  if (goPreview && (payload.sidecar_runtime !== "go-preview" || payload.channel !== "dev" || updateFeedUrl !== null)) {
    throw new BuildMetadataError("DESKTOP_BUILD_METADATA_INVALID");
  }
  if (!goPreview && payload.sidecar_runtime !== "go") {
    throw new BuildMetadataError("DESKTOP_BUILD_METADATA_INVALID");
  }
  return Object.freeze({
    appVersion: payload.app_version,
    channel: payload.channel,
    schemaVersion: payload.schema_version,
    updateFeedUrl,
    sidecarRuntime: payload.sidecar_runtime,
  });
}

function validateLegacyBuildMetadata(payload) {
  if (payload === null || typeof payload !== "object" || Array.isArray(payload)) {
    throw new BuildMetadataError("DESKTOP_BUILD_METADATA_INVALID");
  }
  const fields = Object.keys(payload).sort();
  // Replacement admission only: old generated metadata may be overwritten by
  // preparation, but loadBuildMetadata never admits it for a runtime launch.
  const expectedFields = payload.schema_version === 2 ? METADATA_FIELDS : LEGACY_METADATA_FIELDS;
  if (
    fields.length !== expectedFields.length ||
    fields.some(
      (field, index) => field !== [...expectedFields].sort()[index],
    ) ||
    typeof payload.app_version !== "string" ||
    !SEMVER.test(payload.app_version) ||
    typeof payload.channel !== "string" ||
    !CHANNELS.has(payload.channel) ||
    ![1, 2].includes(payload.schema_version)
  ) {
    throw new BuildMetadataError("DESKTOP_BUILD_METADATA_INVALID");
  }
  return Object.freeze({
    appVersion: payload.app_version,
    channel: payload.channel,
    schemaVersion: payload.schema_version,
    ...(payload.schema_version === 2 ? { updateFeedUrl: validatedUpdateFeedUrl(payload.update_feed_url) } : {}),
  });
}

function loadBuildMetadata({ isPackaged, processResourcesPath, projectRoot }) {
  if (!isPackaged) {
    return validateBuildMetadata({
      app_version: readApplicationVersion(projectRoot),
      channel: "dev",
      schema_version: METADATA_SCHEMA_VERSION,
      sidecar_runtime: "go",
      update_feed_url: null,
    });
  }

  let payload;
  try {
    const metadataPath = path.join(
      processResourcesPath,
      "career-hub",
      "build-metadata.json",
    );
    payload = JSON.parse(fs.readFileSync(metadataPath, "utf8"));
  } catch (_error) {
    throw new BuildMetadataError("DESKTOP_BUILD_METADATA_UNAVAILABLE");
  }
  return validateBuildMetadata(payload);
}

module.exports = {
  BuildMetadataError,
  CHANNELS,
  METADATA_SCHEMA_VERSION,
  GO_PREVIEW_METADATA_SCHEMA_VERSION,
  loadBuildMetadata,
  readApplicationVersion,
  validateBuildMetadata,
  validateLegacyBuildMetadata,
};
