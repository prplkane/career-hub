#!/usr/bin/env node

const childProcess = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");

const { verifyGoSidecarPackageInput } = require("../src/package_manifest");
const { METADATA_SCHEMA_VERSION, GO_PREVIEW_METADATA_SCHEMA_VERSION } = require("../src/build_metadata");
const { prepareGeneratedResources } = require("../src/generated_resources");
const {
  pruneVersionedDesktopArchives,
} = require("../src/generated_artifact_retention");
const {
  prepareWindowsInstallerToolchain,
} = require("../src/windows_installer_toolchain");

const CHANNELS = new Set(["dev", "stable"]);
const desktopRoot = path.resolve(__dirname, "..");
const projectRoot = path.resolve(desktopRoot, "..");
const channel = process.argv[2];
const goPreview = process.argv[3] === "--go-preview";

if (!CHANNELS.has(channel) || process.argv.length !== (goPreview ? 4 : 3)) {
  throw new Error("Package channel must be dev or stable");
}
if (goPreview && (channel !== "dev" || process.platform !== "win32" || process.arch !== "x64")) {
  throw new Error("GO_PREVIEW_WINDOWS_DEV_REQUIRED");
}
if (process.platform !== "win32" || process.arch !== "x64") throw new Error("GO_WINDOWS_X64_REQUIRED");

const updateFeedUrl = process.env.CAREER_HUB_UPDATE_FEED_URL?.trim() || null;
if (goPreview && updateFeedUrl !== null) throw new Error("GO_PREVIEW_UPDATE_FEED_FORBIDDEN");

const version = fs.readFileSync(path.join(projectRoot, "VERSION"), "utf8");
if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)\n$/.test(version)) {
  throw new Error("VERSION must contain one canonical release SemVer");
}

function gitOutput(args) {
  return childProcess.execFileSync("git", args, {
    cwd: projectRoot,
    encoding: "utf8",
    maxBuffer: 1024 * 1024,
    stdio: ["ignore", "pipe", "ignore"],
  });
}

const sourceCommit = gitOutput(["rev-parse", "HEAD"]).trim();
const sourceTreeDirty = gitOutput(["status", "--porcelain"]).length > 0;
if (sourceTreeDirty) {
  throw new Error("Go packages require a clean source checkout");
}

const sidecarRoot = path.join(
  projectRoot,
  "packaging",
  "dist",
  "go-onedir",
  "career-hub-sidecar",
);
verifyGoSidecarPackageInput({
  appVersion: version.trimEnd(),
  artifactRoot: sidecarRoot,
  expectedSourceCommit: sourceCommit,
  platform: process.platform,
  projectRoot,
  requireClean: true,
});

// A migration preview must not prune artifacts of the existing Python product.
if (!goPreview) {
  pruneVersionedDesktopArchives({ currentVersion: version.trimEnd(), projectRoot });
}
prepareWindowsInstallerToolchain({ desktopRoot });

prepareGeneratedResources({
  desktopRoot,
  payload: {
    app_version: version.trimEnd(),
    channel,
    schema_version: goPreview ? GO_PREVIEW_METADATA_SCHEMA_VERSION : METADATA_SCHEMA_VERSION,
    update_feed_url: updateFeedUrl,
    sidecar_runtime: goPreview ? "go-preview" : "go",
  },
});
