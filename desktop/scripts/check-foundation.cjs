#!/usr/bin/env node

const fs = require("node:fs");
const path = require("node:path");

const desktopRoot = path.resolve(__dirname, "..");
const projectRoot = path.resolve(desktopRoot, "..");
const packageData = JSON.parse(
  fs.readFileSync(path.join(desktopRoot, "package.json"), "utf8"),
);
const version = fs.readFileSync(path.join(projectRoot, "VERSION"), "utf8");

if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)\n$/.test(version)) {
  throw new Error("VERSION must contain one canonical release SemVer");
}
if (packageData.version !== "0.0.0") {
  throw new Error("Desktop package.json version is tooling-only and must remain 0.0.0");
}
if (packageData.author !== "Career Hub Contributors") {
  throw new Error("Desktop package author metadata must remain explicit");
}
if (packageData.devDependencies.electron !== "44.7.0") {
  throw new Error("Electron must remain exactly pinned for this support matrix");
}
if (
  packageData.devDependencies["@electron-forge/maker-squirrel"] !== "7.11.2" ||
  packageData.dependencies["electron-squirrel-startup"] !== "1.0.1"
) {
  throw new Error("The reviewed Windows installer dependencies must remain pinned");
}
if (packageData.engines.node !== "24.19.0") {
  throw new Error("Desktop build Node must remain exactly pinned");
}
if (
  packageData.overrides["extract-zip"] !== "$extract-zip" ||
  packageData.overrides["@electron/rebuild"] !== "4.2.0" ||
  packageData.overrides.tmp !== "0.2.7" ||
  packageData.devDependencies["extract-zip"] !==
    "npm:@electron-internal/extract-zip@1.0.1"
) {
  throw new Error("Reviewed Forge security overrides must remain exactly pinned");
}

for (const relativePath of [
  "forge.config.js",
  "scripts/build-go-sidecar.cjs",
  "scripts/qualify-go-sidecar.cjs",
  "src/main.js",
  "src/app_paths.js",
  "src/extension_preferences.js",
  "src/generated_resources.js",
  "src/generated_artifact_retention.js",
  "src/installer_contract.js",
  "src/lifecycle.js",
  "src/loading_protocol.js",
  "src/navigation.js",
  "src/package_manifest.js",
  "src/recovery_flow.js",
  "src/security.js",
  "src/sidecar.js",
  "src/update_ipc.js",
  "src/update_preferences.js",
  "src/updater.js",
  "src/windows_installer_toolchain.js",
  "ui/loading.html",
]) {
  if (!fs.statSync(path.join(desktopRoot, relativePath), { throwIfNoEntry: false })?.isFile()) {
    throw new Error(`Desktop foundation file is missing: ${relativePath}`);
  }
}

const forgeConfig = require(path.join(desktopRoot, "forge.config.js"));
if (
  forgeConfig.makers.length !== 1 ||
  forgeConfig.makers[0].name !== "@electron-forge/maker-squirrel" ||
  forgeConfig.makers[0].platforms?.length !== 1 ||
  forgeConfig.makers[0].platforms[0] !== "win32" ||
  forgeConfig.publishers !== undefined ||
  packageData.scripts["make:windows:dev"] !==
    "node scripts/prepare-package.cjs dev && electron-forge make --platform=win32 --arch=x64"
) {
  throw new Error("Exactly one reviewed Windows installer target must be configured");
}

process.stdout.write("desktop foundation checks passed\n");
