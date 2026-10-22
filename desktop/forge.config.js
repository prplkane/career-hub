const path = require("node:path");
const fs = require("node:fs");
const childProcess = require("node:child_process");
const { verifyGoSidecarPackageInput } = require("./src/package_manifest");

const {
  assertGeneratedResourceInventory,
  packagedGeneratedResourceRoot,
} = require("./src/generated_resources");
const {
  mutatePackageForInstaller,
  windowsInstallerConfig,
} = require("./src/installer_contract");
const {
  METADATA_SCHEMA_VERSION,
  readApplicationVersion: readCanonicalApplicationVersion,
  validateBuildMetadata,
} = require("./src/build_metadata");

const desktopRoot = __dirname;
const projectRoot = path.resolve(desktopRoot, "..");
const generatedResources = path.join(desktopRoot, ".generated", "career-hub");
const preparedMetadata = activeBuildMetadata();
const goPreview = preparedMetadata.sidecarRuntime === "go-preview";
const sidecarResources = path.join(
  projectRoot,
  "packaging",
  "dist",
  "go-onedir",
  "career-hub-sidecar",
);

function activeBuildMetadata({ requirePrepared = false } = {}) {
  const canonicalVersion = readCanonicalApplicationVersion(projectRoot);
  const metadataPath = path.join(generatedResources, "build-metadata.json");
  if (!fs.lstatSync(generatedResources, { throwIfNoEntry: false })) {
    if (requirePrepared) {
      throw new Error("Prepared installer metadata is unavailable");
    }
    return Object.freeze({
      appVersion: canonicalVersion,
      channel: "dev",
      schemaVersion: METADATA_SCHEMA_VERSION,
      sidecarRuntime: "go",
      updateFeedUrl: null,
    });
  }
  assertGeneratedResourceInventory(generatedResources, { trustedRoot: desktopRoot });
  const metadata = validateBuildMetadata(
    JSON.parse(fs.readFileSync(metadataPath, "utf8")),
  );
  if (metadata.appVersion !== canonicalVersion) {
    throw new Error("Prepared installer version does not match VERSION");
  }
  return metadata;
}

async function applyElectronFuses(_forgeConfig, packageResult) {
  const { flipFuses, FuseV1Options, FuseVersion } = await import("@electron/fuses");
  const executableParts =
    packageResult.platform === "darwin"
      ? ["career-hub.app", "Contents", "MacOS", "career-hub"]
      : [packageResult.platform === "win32" ? "career-hub.exe" : "career-hub"];

  for (const outputPath of packageResult.outputPaths) {
    assertGeneratedResourceInventory(
      packagedGeneratedResourceRoot(outputPath, packageResult.platform),
      { trustedRoot: outputPath },
    );
    await flipFuses(path.join(outputPath, ...executableParts), {
      version: FuseVersion.V1,
      resetAdHocDarwinSignature:
        packageResult.platform === "darwin" && packageResult.arch === "arm64",
      strictlyRequireAllFuses: true,
      [FuseV1Options.RunAsNode]: false,
      [FuseV1Options.EnableCookieEncryption]: true,
      [FuseV1Options.EnableNodeOptionsEnvironmentVariable]: false,
      [FuseV1Options.EnableNodeCliInspectArguments]: false,
      [FuseV1Options.EnableEmbeddedAsarIntegrityValidation]: true,
      [FuseV1Options.OnlyLoadAppFromAsar]: true,
      [FuseV1Options.LoadBrowserProcessSpecificV8Snapshot]: false,
      [FuseV1Options.GrantFileProtocolExtraPrivileges]: false,
      [FuseV1Options.WasmTrapHandlers]: true,
    });
  }
}

module.exports = {
  // Squirrel clears its make directory, including RELEASES. Keep preview
  // outputs below their own root so a build cannot replace Python artifacts.
  outDir: goPreview ? "out/go-preview" : "out",
  packagerConfig: {
    appVersion: readCanonicalApplicationVersion(projectRoot),
    asar: true,
    executableName: "career-hub",
    extraResource: [generatedResources, sidecarResources],
    // Packager adds the platform extension; scripts/build_app_icon.py makes the .ico.
    icon: path.join(desktopRoot, "assets", "career-hub"),
    ignore: [
      // With a nested preview outDir, Packager's automatic output exclusion
      // does not cover sibling/older builds. Never embed generated artifacts.
      /^\/out(?:\/|$)/,
      /^\/\.generated(?:\/|$)/,
      /^\/scripts(?:\/|$)/,
      /^\/tests(?:\/|$)/,
    ],
    name: goPreview ? "career-hub-go-preview" : "career-hub",
    overwrite: true,
  },
  makers: [
    {
      name: "@electron-forge/maker-squirrel",
      platforms: ["win32"],
      config: () => windowsInstallerConfig(activeBuildMetadata({ requirePrepared: true })),
    },
  ],
  hooks: {
    readPackageJson: async (_forgeConfig, packageData) =>
      mutatePackageForInstaller(packageData, activeBuildMetadata()),
    prePackage: async (_forgeConfig, platform, arch) => {
      assertGeneratedResourceInventory(generatedResources, {
        trustedRoot: desktopRoot,
      });
      const metadata = activeBuildMetadata({ requirePrepared: true });
      if (metadata.sidecarRuntime !== preparedMetadata.sidecarRuntime) {
        throw new Error("DESKTOP_PREPARED_RUNTIME_CHANGED");
      }
      {
        if (platform !== "win32" || arch !== "x64") throw new Error("GO_WINDOWS_X64_REQUIRED");
        const git = (args) => childProcess.execFileSync("git", args, { cwd: projectRoot, encoding: "utf8", maxBuffer: 1024 * 1024 });
        if (git(["status", "--porcelain"]).length) throw new Error("GO_CLEAN_SOURCE_REQUIRED");
        verifyGoSidecarPackageInput({ artifactRoot: sidecarResources, projectRoot,
          appVersion: metadata.appVersion, expectedSourceCommit: git(["rev-parse", "HEAD"]).trim(),
          platform, requireClean: true });
      }
    },
    postPackage: applyElectronFuses,
  },
};
