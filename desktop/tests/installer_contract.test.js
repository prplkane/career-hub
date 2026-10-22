"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const forgeConfig = require("../forge.config");
const {
  InstallerContractError,
  assertWindowsInstallerDataBoundary,
  installerIdentityForChannel,
  isSquirrelStartupEvent,
  mutatePackageForInstaller,
  windowsInstallerConfig,
} = require("../src/installer_contract");

test("one Windows Squirrel maker remains separate from release publication", () => {
  assert.equal(forgeConfig.makers.length, 1);
  assert.equal(forgeConfig.makers[0].name, "@electron-forge/maker-squirrel");
  assert.deepEqual(forgeConfig.makers[0].platforms, ["win32"]);
  assert.equal(typeof forgeConfig.makers[0].config, "function");
  assert.equal(forgeConfig.publishers, undefined);
  const goPreview = forgeConfig.packagerConfig.name === "career-hub-go-preview";
  assert.equal(forgeConfig.outDir, goPreview ? "out/go-preview" : "out");
  assert.ok(forgeConfig.packagerConfig.extraResource.some((entry) => entry.endsWith(path.join("go-onedir", "career-hub-sidecar"))));
});

test("all generated output is excluded even when preview output is nested", () => {
  const ignored = (entry) => forgeConfig.packagerConfig.ignore.some((rule) => rule.test(entry));
  for (const entry of ["/out", "/out/career-hub-win32-x64/resources/app.asar",
    "/out/make/squirrel.windows/x64/old.nupkg", "/out/go-preview/make/RELEASES",
    "/.generated/career-hub/build-metadata.json", "/scripts/build-go-sidecar.cjs", "/tests/example.test.js"]) {
    assert.equal(ignored(entry), true, entry);
  }
  for (const entry of ["/src/main.js", "/ui/startup.html", "/node_modules/electron-squirrel-startup/index.js"]) {
    assert.equal(ignored(entry), false, entry);
  }
});

test("channel identities remain separate from the private data directory", () => {
  const packageNames = new Set();
  for (const channel of ["dev", "stable"]) {
    const identity = installerIdentityForChannel(channel);
    packageNames.add(identity.packageName);
    const roots = assertWindowsInstallerDataBoundary({
      installerDirectoryName: identity.packageName,
      localAppData: "C:\\SyntheticLocalAppData",
    });
    assert.notEqual(roots.installRoot.toLowerCase(), roots.dataRoot.toLowerCase());
    assert.match(identity.appUserModelId, /^com\.squirrel\./);
  }
  assert.equal(packageNames.size, 2);
  // The beta channel was retired: only dev and stable have installer identities.
  assert.throws(() => installerIdentityForChannel("beta"), InstallerContractError);
});

test("Go preview has a distinct installer identity without changing release channels", () => {
  const identity = installerIdentityForChannel("dev", { goPreview: true });
  assert.equal(identity.packageName, "CHDesktopGoPreview");
  for (const channel of ["dev", "stable"]) {
    assert.notEqual(identity.packageName, installerIdentityForChannel(channel).packageName);
    assert.notEqual(identity.appUserModelId, installerIdentityForChannel(channel).appUserModelId);
  }
  assertWindowsInstallerDataBoundary({ installerDirectoryName: identity.packageName });
  const metadata = { appVersion: "1.2.3", channel: "dev", sidecarRuntime: "go-preview" };
  assert.equal(windowsInstallerConfig(metadata).name, identity.packageName);
  assert.equal(windowsInstallerConfig(metadata).setupExe, "Career Hub Go Preview-1.2.3 Setup.exe");
  assert.equal(mutatePackageForInstaller({ name: "tooling", version: "0.0.0" }, metadata).productName, identity.productName);
  for (const channel of ["beta", "stable"]) {
    assert.throws(() => windowsInstallerConfig({ ...metadata, channel }), InstallerContractError);
  }
  assert.throws(() => windowsInstallerConfig({ ...metadata, sidecarRuntime: "auto" }), InstallerContractError);
});

test("data-root collisions and sibling-prefix identities fail closed", () => {
  for (const installerDirectoryName of [
    "CareerHub",
    "careerhub",
    "CareerHubDesktop",
  ]) {
    assert.throws(
      () => assertWindowsInstallerDataBoundary({ installerDirectoryName }),
      (error) =>
        error instanceof InstallerContractError &&
        error.code === "DESKTOP_INSTALLER_DATA_ROOT_COLLISION",
    );
  }
});

test("installer config uses one full Squirrel package and canonical version", () => {
  const config = windowsInstallerConfig({ appVersion: "0.1.0", channel: "dev" });
  assert.deepEqual(config, {
    authors: "Career Hub Contributors",
    description: "Local-first career application workspace.",
    name: "CHDesktopDev",
    noDelta: true,
    noMsi: true,
    setupExe: "Career Hub Dev-0.1.0 Setup.exe",
    setupIcon: path.resolve(__dirname, "..", "assets", "career-hub.ico"),
    title: "Career Hub Dev",
  });
  assert.ok(fs.statSync(config.setupIcon).isFile());
  assert.equal(forgeConfig.packagerConfig.icon + ".ico", config.setupIcon);
  assert.equal("remoteReleases" in config, false);
  assert.equal("certificateFile" in config, false);
});

test("Forge package metadata is mutated in memory without changing the source", () => {
  const source = Object.freeze({
    name: "career-hub-desktop-host",
    productName: "Career Hub Dev",
    version: "0.0.0",
  });
  const mutated = mutatePackageForInstaller(source, {
    appVersion: "2.3.4",
    channel: "stable",
  });
  assert.equal(mutated.version, "2.3.4");
  assert.equal(mutated.productName, "Career Hub");
  assert.equal(source.version, "0.0.0");
});

test("known Squirrel events are handled before normal desktop startup", () => {
  for (const event of [
    "--squirrel-install",
    "--squirrel-updated",
    "--squirrel-uninstall",
    "--squirrel-obsolete",
  ]) {
    assert.equal(isSquirrelStartupEvent(["career-hub.exe", event], "win32"), true);
  }
  assert.equal(isSquirrelStartupEvent(["career-hub.exe", "--other"], "win32"), false);
  assert.equal(
    isSquirrelStartupEvent(["career-hub.exe", "--squirrel-install"], "linux"),
    false,
  );

  const mainSource = fs.readFileSync(
    path.resolve(__dirname, "..", "src", "main.js"),
    "utf8",
  );
  const eventGuard = mainSource.indexOf("if (isSquirrelStartupEvent");
  const eventHandler = mainSource.indexOf('require("electron-squirrel-startup")');
  const normalElectronLoad = mainSource.indexOf('require("electron")');
  assert.ok(eventGuard >= 0);
  assert.ok(eventHandler > eventGuard);
  assert.ok(normalElectronLoad > eventHandler);
  assert.match(mainSource.slice(eventGuard, normalElectronLoad), /return;/);
});
