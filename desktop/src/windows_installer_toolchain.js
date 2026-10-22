"use strict";

const crypto = require("node:crypto");
const fs = require("node:fs");
const path = require("node:path");

const ELECTRON_WINSTALLER_VERSION = "5.4.4";

class WindowsInstallerToolchainError extends Error {
  constructor(code) {
    super(code);
    this.name = "WindowsInstallerToolchainError";
    this.code = code;
  }
}

function fileHash(fileSystem, filePath) {
  return crypto.createHash("sha256").update(fileSystem.readFileSync(filePath)).digest("hex");
}

function assertRegularFile(fileSystem, filePath) {
  const record = fileSystem.lstatSync(filePath, { throwIfNoEntry: false });
  if (record === undefined || !record.isFile() || record.isSymbolicLink()) {
    throw new WindowsInstallerToolchainError(
      "DESKTOP_WINDOWS_INSTALLER_TOOL_MISSING",
    );
  }
}

function assertReplaceableFile(fileSystem, filePath) {
  const record = fileSystem.lstatSync(filePath, { throwIfNoEntry: false });
  if (
    record !== undefined &&
    (!record.isFile() || record.isSymbolicLink())
  ) {
    throw new WindowsInstallerToolchainError(
      "DESKTOP_WINDOWS_INSTALLER_TOOL_INVALID",
    );
  }
}

function prepareWindowsInstallerToolchain({
  desktopRoot,
  platform = process.platform,
  architecture = process.arch,
  fileSystem = fs,
}) {
  if (platform !== "win32") return Object.freeze({ status: "not_applicable" });
  if (
    architecture !== "x64" ||
    typeof desktopRoot !== "string" ||
    !path.isAbsolute(desktopRoot)
  ) {
    throw new WindowsInstallerToolchainError(
      "DESKTOP_WINDOWS_INSTALLER_HOST_UNSUPPORTED",
    );
  }

  const dependencyRoot = path.join(
    desktopRoot,
    "node_modules",
    "electron-winstaller",
  );
  const packagePath = path.join(dependencyRoot, "package.json");
  assertRegularFile(fileSystem, packagePath);
  let packageData;
  try {
    packageData = JSON.parse(fileSystem.readFileSync(packagePath, "utf8"));
  } catch (_error) {
    throw new WindowsInstallerToolchainError(
      "DESKTOP_WINDOWS_INSTALLER_TOOL_INVALID",
    );
  }
  if (packageData?.version !== ELECTRON_WINSTALLER_VERSION) {
    throw new WindowsInstallerToolchainError(
      "DESKTOP_WINDOWS_INSTALLER_TOOL_VERSION_INVALID",
    );
  }

  const vendorRoot = path.join(dependencyRoot, "vendor");
  const prepared = [];
  for (const extension of ["exe", "dll"]) {
    const source = path.join(vendorRoot, `7z-${architecture}.${extension}`);
    const destination = path.join(vendorRoot, `7z.${extension}`);
    assertRegularFile(fileSystem, source);
    assertReplaceableFile(fileSystem, destination);
    const sourceHash = fileHash(fileSystem, source);
    fileSystem.copyFileSync(source, destination);
    assertRegularFile(fileSystem, destination);
    if (fileHash(fileSystem, destination) !== sourceHash) {
      throw new WindowsInstallerToolchainError(
        "DESKTOP_WINDOWS_INSTALLER_TOOL_COPY_FAILED",
      );
    }
    prepared.push(path.basename(destination));
  }
  return Object.freeze({
    architecture,
    files: Object.freeze(prepared),
    status: "prepared",
    version: ELECTRON_WINSTALLER_VERSION,
  });
}

module.exports = {
  ELECTRON_WINSTALLER_VERSION,
  WindowsInstallerToolchainError,
  prepareWindowsInstallerToolchain,
};
