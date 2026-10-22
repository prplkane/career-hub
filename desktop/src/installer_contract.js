"use strict";

const path = require("node:path");

const APP_ICON = path.resolve(__dirname, "..", "assets", "career-hub.ico");
const APP_VERSION = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;
const DATA_DIRECTORY_NAME = "CareerHub";
const SQUIRREL_EVENTS = Object.freeze(
  new Set([
    "--squirrel-install",
    "--squirrel-obsolete",
    "--squirrel-uninstall",
    "--squirrel-updated",
  ]),
);
const WINDOWS_IDENTITIES = Object.freeze({
  dev: Object.freeze({
    appUserModelId: "com.squirrel.CHDesktopDev.CHDesktopDev",
    packageName: "CHDesktopDev",
    productName: "Career Hub Dev",
  }),
  stable: Object.freeze({
    appUserModelId: "com.squirrel.CHDesktop.CHDesktop",
    packageName: "CHDesktop",
    productName: "Career Hub",
  }),
});
const GO_PREVIEW_IDENTITY = Object.freeze({
  appUserModelId: "com.squirrel.CHDesktopGoPreview.CHDesktopGoPreview",
  packageName: "CHDesktopGoPreview",
  productName: "Career Hub Go Preview",
});

class InstallerContractError extends Error {
  constructor(code) {
    super(code);
    this.name = "InstallerContractError";
    this.code = code;
  }
}

function installerIdentityForChannel(channel, { goPreview = false } = {}) {
  if (typeof goPreview !== "boolean" || (goPreview && channel !== "dev")) {
    throw new InstallerContractError("DESKTOP_INSTALLER_CHANNEL_INVALID");
  }
  if (goPreview) return GO_PREVIEW_IDENTITY;
  const identity = WINDOWS_IDENTITIES[channel];
  if (!identity) throw new InstallerContractError("DESKTOP_INSTALLER_CHANNEL_INVALID");
  return identity;
}

function isContainedBy(parent, candidate) {
  const relative = path.win32.relative(parent, candidate);
  return (
    relative === "" ||
    (!relative.startsWith(`..${path.win32.sep}`) &&
      relative !== ".." &&
      !path.win32.isAbsolute(relative))
  );
}

function assertWindowsInstallerDataBoundary({
  installerDirectoryName,
  dataDirectoryName = DATA_DIRECTORY_NAME,
  localAppData = "C:\\CareerHubBoundaryFixture",
}) {
  if (
    !path.win32.isAbsolute(localAppData) ||
    path.win32.parse(localAppData).root === path.win32.normalize(localAppData) ||
    !/^[A-Za-z][A-Za-z0-9]{2,63}$/.test(installerDirectoryName) ||
    !/^[A-Za-z][A-Za-z0-9]{2,63}$/.test(dataDirectoryName)
  ) {
    throw new InstallerContractError("DESKTOP_INSTALLER_IDENTITY_INVALID");
  }

  const installRoot = path.win32.resolve(localAppData, installerDirectoryName);
  const dataRoot = path.win32.resolve(localAppData, dataDirectoryName);
  const normalizedInstaller = installerDirectoryName.toLowerCase();
  const normalizedData = dataDirectoryName.toLowerCase();
  if (
    isContainedBy(installRoot, dataRoot) ||
    isContainedBy(dataRoot, installRoot) ||
    normalizedInstaller.startsWith(normalizedData) ||
    normalizedData.startsWith(normalizedInstaller)
  ) {
    throw new InstallerContractError("DESKTOP_INSTALLER_DATA_ROOT_COLLISION");
  }
  return Object.freeze({ dataRoot, installRoot });
}

function windowsInstallerConfig({ appVersion, channel, sidecarRuntime = "go" }) {
  if (typeof appVersion !== "string" || !APP_VERSION.test(appVersion)) {
    throw new InstallerContractError("DESKTOP_INSTALLER_VERSION_INVALID");
  }
  if (!["go", "go-preview"].includes(sidecarRuntime)) throw new InstallerContractError("DESKTOP_INSTALLER_CHANNEL_INVALID");
  const identity = installerIdentityForChannel(channel, { goPreview: sidecarRuntime === "go-preview" });
  assertWindowsInstallerDataBoundary({
    installerDirectoryName: identity.packageName,
  });
  return Object.freeze({
    authors: "Career Hub Contributors",
    description: "Local-first career application workspace.",
    name: identity.packageName,
    noDelta: true,
    noMsi: true,
    setupExe: `${identity.productName}-${appVersion} Setup.exe`,
    setupIcon: APP_ICON,
    title: identity.productName,
  });
}

function mutatePackageForInstaller(packageData, { appVersion, channel, sidecarRuntime = "go" }) {
  if (packageData === null || typeof packageData !== "object" || Array.isArray(packageData)) {
    throw new InstallerContractError("DESKTOP_INSTALLER_PACKAGE_INVALID");
  }
  const identity = installerIdentityForChannel(channel, { goPreview: sidecarRuntime === "go-preview" });
  windowsInstallerConfig({ appVersion, channel, sidecarRuntime });
  return {
    ...packageData,
    productName: identity.productName,
    version: appVersion,
  };
}

function isSquirrelStartupEvent(argv = process.argv, platform = process.platform) {
  return (
    platform === "win32" &&
    Array.isArray(argv) &&
    typeof argv[1] === "string" &&
    SQUIRREL_EVENTS.has(argv[1])
  );
}

module.exports = {
  DATA_DIRECTORY_NAME,
  InstallerContractError,
  assertWindowsInstallerDataBoundary,
  installerIdentityForChannel,
  isSquirrelStartupEvent,
  mutatePackageForInstaller,
  windowsInstallerConfig,
};
