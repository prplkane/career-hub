"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const {
  WindowsInstallerToolchainError,
  prepareWindowsInstallerToolchain,
} = require("../src/windows_installer_toolchain");

function fixture(t, version = "5.4.4") {
  const desktopRoot = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-winstaller-"));
  t.after(() => fs.rmSync(desktopRoot, { force: true, recursive: true }));
  const dependencyRoot = path.join(
    desktopRoot,
    "node_modules",
    "electron-winstaller",
  );
  const vendorRoot = path.join(dependencyRoot, "vendor");
  fs.mkdirSync(vendorRoot, { recursive: true });
  fs.writeFileSync(path.join(dependencyRoot, "package.json"), JSON.stringify({ version }));
  fs.writeFileSync(path.join(vendorRoot, "7z-x64.exe"), "reviewed executable fixture");
  fs.writeFileSync(path.join(vendorRoot, "7z-x64.dll"), "reviewed library fixture");
  return { desktopRoot, vendorRoot };
}

test("Windows installer preflight hydrates exact host-architecture 7-Zip tools", (t) => {
  const { desktopRoot, vendorRoot } = fixture(t);
  const result = prepareWindowsInstallerToolchain({
    architecture: "x64",
    desktopRoot,
    platform: "win32",
  });

  assert.deepEqual(result, {
    architecture: "x64",
    files: ["7z.exe", "7z.dll"],
    status: "prepared",
    version: "5.4.4",
  });
  assert.equal(
    fs.readFileSync(path.join(vendorRoot, "7z.exe"), "utf8"),
    "reviewed executable fixture",
  );
  assert.equal(
    fs.readFileSync(path.join(vendorRoot, "7z.dll"), "utf8"),
    "reviewed library fixture",
  );
});

test("Windows installer preflight rejects an unreviewed dependency or host", (t) => {
  const { desktopRoot } = fixture(t, "5.4.5");
  assert.throws(
    () => prepareWindowsInstallerToolchain({
      architecture: "x64",
      desktopRoot,
      platform: "win32",
    }),
    (error) =>
      error instanceof WindowsInstallerToolchainError &&
      error.code === "DESKTOP_WINDOWS_INSTALLER_TOOL_VERSION_INVALID",
  );
  assert.throws(
    () => prepareWindowsInstallerToolchain({
      architecture: "arm64",
      desktopRoot,
      platform: "win32",
    }),
    (error) =>
      error instanceof WindowsInstallerToolchainError &&
      error.code === "DESKTOP_WINDOWS_INSTALLER_HOST_UNSUPPORTED",
  );
  assert.deepEqual(
    prepareWindowsInstallerToolchain({ desktopRoot, platform: "darwin" }),
    { status: "not_applicable" },
  );
});

test("Windows installer preflight does not replace a non-file destination", (t) => {
  const { desktopRoot, vendorRoot } = fixture(t);
  fs.mkdirSync(path.join(vendorRoot, "7z.exe"));

  assert.throws(
    () => prepareWindowsInstallerToolchain({
      architecture: "x64",
      desktopRoot,
      platform: "win32",
    }),
    (error) =>
      error instanceof WindowsInstallerToolchainError &&
      error.code === "DESKTOP_WINDOWS_INSTALLER_TOOL_INVALID",
  );
});
