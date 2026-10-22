#!/usr/bin/env node
"use strict";

const childProcess = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");

const {
  collectGoProjectResourceEntries,
  GO_AGENT_CLIENT,
  verifyGoSidecarPackageInput,
  writeGoSidecarManifest,
} = require("../src/package_manifest");

const desktopRoot = path.resolve(__dirname, "..");
const projectRoot = path.resolve(desktopRoot, "..");
const backendRoot = path.join(projectRoot, "backend");
const buildRoot = path.join(projectRoot, "packaging", "build", "go-sidecar");
const distRoot = path.join(projectRoot, "packaging", "dist", "go-onedir");
const outputRoot = path.join(distRoot, "career-hub-sidecar");
const allowDirty = process.argv.length === 3 && process.argv[2] === "--allow-dirty";

if (process.argv.length !== (allowDirty ? 3 : 2)) {
  throw new Error("Usage: build-go-sidecar.cjs [--allow-dirty]");
}
if (process.platform !== "win32" || process.arch !== "x64") {
  throw new Error("GO_SIDECAR_WINDOWS_X64_REQUIRED");
}

function containedGeneratedPath(candidate, parent) {
  const relative = path.relative(parent, candidate);
  return (
    relative.length > 0 &&
    relative !== ".." &&
    !relative.startsWith(`..${path.sep}`) &&
    !path.isAbsolute(relative)
  );
}

function ensureGeneratedDirectory(directory) {
  const relative = path.relative(projectRoot, directory);
  if (
    relative.length === 0 ||
    relative === ".." ||
    relative.startsWith(`..${path.sep}`) ||
    path.isAbsolute(relative)
  ) {
    throw new Error("GO_SIDECAR_GENERATED_PATH_INVALID");
  }
  let current = projectRoot;
  for (const component of relative.split(path.sep)) {
    current = path.join(current, component);
    const metadata = fs.lstatSync(current, { throwIfNoEntry: false });
    if (metadata?.isSymbolicLink()) {
      throw new Error("GO_SIDECAR_GENERATED_PATH_LINKED");
    }
    if (metadata && !metadata.isDirectory()) {
      throw new Error("GO_SIDECAR_GENERATED_PATH_INVALID");
    }
    if (!metadata) fs.mkdirSync(current);
  }
  if (path.relative(fs.realpathSync(directory), path.resolve(directory)) !== "") {
    throw new Error("GO_SIDECAR_GENERATED_PATH_LINKED");
  }
}

function removeGeneratedDirectory(candidate, parent) {
  if (!containedGeneratedPath(candidate, parent)) {
    throw new Error("GO_SIDECAR_GENERATED_PATH_INVALID");
  }
  const metadata = fs.lstatSync(candidate, { throwIfNoEntry: false });
  if (metadata?.isSymbolicLink()) {
    throw new Error("GO_SIDECAR_GENERATED_PATH_LINKED");
  }
  if (metadata) fs.rmSync(candidate, { force: true, recursive: true });
}

function gitOutput(args) {
  return childProcess.execFileSync("git", args, {
    cwd: projectRoot,
    encoding: "utf8",
    maxBuffer: 1024 * 1024,
    stdio: ["ignore", "pipe", "ignore"],
  });
}

function executable() {
  const configured = process.env.CAREER_HUB_GO_EXECUTABLE?.trim();
  if (!configured) return "go";
  if (!path.isAbsolute(configured)) {
    throw new Error("GO_SIDECAR_TOOLCHAIN_PATH_INVALID");
  }
  const metadata = fs.lstatSync(configured, { throwIfNoEntry: false });
  if (!metadata?.isFile() || metadata.isSymbolicLink()) {
    throw new Error("GO_SIDECAR_TOOLCHAIN_PATH_INVALID");
  }
  return configured;
}

const buildEnvironment = {
  ...process.env,
  CGO_ENABLED: "0",
  GOARCH: "amd64",
  GOFLAGS: "",
  GOOS: "windows",
  GOAMD64: "v1",
  GOTOOLCHAIN: "local",
  GOWORK: "off",
};
const goExecutable = executable();
const goVersion = childProcess
  .execFileSync(goExecutable, ["env", "GOVERSION"], {
    cwd: backendRoot,
    encoding: "utf8",
    env: buildEnvironment,
    maxBuffer: 1024 * 1024,
    stdio: ["ignore", "pipe", "inherit"],
  })
  .trim();
if (goVersion !== "go1.27.3") {
  throw new Error("GO_SIDECAR_TOOLCHAIN_INVALID");
}

const appVersion = fs.readFileSync(path.join(projectRoot, "VERSION"), "utf8");
if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)\n$/.test(appVersion)) {
  throw new Error("GO_SIDECAR_VERSION_INVALID");
}
const sourceCommit = gitOutput(["rev-parse", "HEAD"]).trim();
const sourceState = gitOutput(["status", "--porcelain", "--untracked-files=normal"]);
const sourceTreeDirty = sourceState.length > 0;
if (sourceTreeDirty && !allowDirty) {
  throw new Error("GO_SIDECAR_CLEAN_SOURCE_REQUIRED");
}

ensureGeneratedDirectory(buildRoot);
const stagingRoot = fs.mkdtempSync(path.join(buildRoot, "candidate-"));
const artifactRoot = path.join(stagingRoot, "career-hub-sidecar");
fs.mkdirSync(artifactRoot);
let movedToOutput = false;
let outputVerified = false;

try {
  for (const [name, target] of [
    ["career-hub-sidecar.exe", "./cmd/career-hub-sidecar"],
    [GO_AGENT_CLIENT.executable, "./cmd/career-hub-agent"],
  ]) {
    childProcess.execFileSync(
      goExecutable,
      [
        "build",
        "-buildvcs=false",
        "-trimpath",
        "-mod=readonly",
        "-ldflags=-buildid=",
        "-o",
        path.join(artifactRoot, name),
        target,
      ],
      {
        cwd: backendRoot,
        env: buildEnvironment,
        maxBuffer: 4 * 1024 * 1024,
        stdio: ["ignore", "inherit", "inherit"],
      },
    );
  }

  for (const resource of collectGoProjectResourceEntries(projectRoot)) {
    const source = path.join(projectRoot, ...resource.path.split("/"));
    const destination = path.join(artifactRoot, ...resource.path.split("/"));
    fs.mkdirSync(path.dirname(destination), { recursive: true });
    fs.copyFileSync(source, destination, fs.constants.COPYFILE_EXCL);
  }

  writeGoSidecarManifest({
    appVersion: appVersion.trimEnd(),
    artifactRoot,
    goVersion,
    projectRoot,
    sourceCommit,
    sourceTreeDirty,
  });
  verifyGoSidecarPackageInput({
    appVersion: appVersion.trimEnd(),
    artifactRoot,
    expectedSourceCommit: sourceCommit,
    platform: "win32",
    projectRoot,
    requireClean: !allowDirty,
  });

  if (
    gitOutput(["rev-parse", "HEAD"]).trim() !== sourceCommit ||
    gitOutput(["status", "--porcelain", "--untracked-files=normal"]) !==
      sourceState
  ) {
    throw new Error("GO_SIDECAR_SOURCE_CHANGED_DURING_BUILD");
  }

  ensureGeneratedDirectory(distRoot);
  removeGeneratedDirectory(outputRoot, distRoot);
  fs.renameSync(artifactRoot, outputRoot);
  movedToOutput = true;
  verifyGoSidecarPackageInput({
    appVersion: appVersion.trimEnd(),
    artifactRoot: outputRoot,
    expectedSourceCommit: sourceCommit,
    platform: "win32",
    projectRoot,
    requireClean: !allowDirty,
  });
  outputVerified = true;
} finally {
  if (movedToOutput && !outputVerified) {
    removeGeneratedDirectory(outputRoot, distRoot);
  }
  removeGeneratedDirectory(stagingRoot, buildRoot);
}

process.stdout.write(`${path.relative(projectRoot, outputRoot).split(path.sep).join("/")}\n`);
