"use strict";
const assert = require("node:assert/strict");
const test = require("node:test");
const { BUNDLED_EXTENSION_ORIGIN, readLaunchConfiguration, sidecarLaunchOptions } = require("../src/launch_configuration");
const read = (argv, extra = {}) => readLaunchConfiguration({ argv: ["electron", "/app", ...argv],
  isPackaged: false, channel: "dev", platform: "win32", ...extra });

test("default Go remains unpaired; standard and preview accept explicit origins", () => {
  assert.deepEqual(read([]), { goPreview: false, extensionOrigins: [] });
  for (const channel of ["dev", "stable"]) {
    assert.deepEqual(read(["--enable-extension"], { isPackaged: true, channel, sidecarRuntime: "go" }), {
      goPreview: false, extensionOrigins: [BUNDLED_EXTENSION_ORIGIN] });
  }
  for (const extra of [{ sidecarRuntime: "python" }, { platform: "darwin" }, { platform: "linux" }, { channel: "unknown" }, { channel: "beta" }]) {
    assert.throws(() => read([], extra), { code: "DESKTOP_LAUNCH_CONFIGURATION_INVALID" });
  }
  assert.deepEqual(read(["--go-preview"]), { goPreview: true, extensionOrigins: [] });
  assert.deepEqual(read(["--go-preview", "--enable-extension"]), {
    goPreview: true, extensionOrigins: [BUNDLED_EXTENSION_ORIGIN] });
  assert.deepEqual(read(["--go-preview", "--extension-origin", "moz-extension://one/",
    "--extension-origin", "moz-extension://one"]), { goPreview: true, extensionOrigins: ["moz-extension://one"] });
});
test("preview cannot cross release-channel, installed or platform gates", () => {
  for (const extra of [{ isPackaged: true }, { channel: "stable" }, { channel: "beta" },
    { platform: "darwin" }, { platform: "linux" }]) {
    assert.throws(() => read(["--go-preview"], extra), { code: "DESKTOP_LAUNCH_CONFIGURATION_INVALID" });
  }
});

test("installed Go preview is selected only by validated package metadata", () => {
  const extra = { isPackaged: true, sidecarRuntime: "go-preview" };
  assert.deepEqual(read([], extra), { goPreview: true, extensionOrigins: [] });
  assert.deepEqual(read(["--enable-extension"], extra), { goPreview: true, extensionOrigins: [BUNDLED_EXTENSION_ORIGIN] });
  assert.throws(() => read(["--go-preview"], extra), { code: "DESKTOP_LAUNCH_CONFIGURATION_INVALID" });
  for (const override of [{ channel: "stable" }, { channel: "beta" }, { platform: "darwin" },
    { platform: "linux" }, { isPackaged: false }, { sidecarRuntime: "unrecognized" }]) {
    assert.throws(() => read([], { ...extra, ...override }), { code: "DESKTOP_LAUNCH_CONFIGURATION_INVALID" });
  }
  assert.deepEqual(read([], { isPackaged: true }), { goPreview: false, extensionOrigins: [] });
});
test("malformed, conflicting and implicit origin authorization fails closed", () => {
  for (const args of [
    ["--go-preview", "--go-preview"], ["--go-preview=false"],
    ["--go-preview", "--enable-extension", "--enable-extension"],
    ["--go-preview", "--enable-extension", "--extension-origin", BUNDLED_EXTENSION_ORIGIN],
    ["--go-preview", "--extension-origin"],
    ...["https://example.test", "chrome-extension://", "chrome-extension://a:99", "chrome-extension://a/path",
      "chrome-extension://a?query", "chrome-extension://a#fragment", "chrome-extension://a\nb", "chrome-extension://a\0",
      "chrome-extension://" + "a".repeat(256)].map((value) => ["--go-preview", "--extension-origin", value]),
    ["--go-preview", ...Array.from({ length: 17 }, () => ["--extension-origin", BUNDLED_EXTENSION_ORIGIN]).flat()]]) {
    assert.throws(() => read(args), { code: "DESKTOP_LAUNCH_CONFIGURATION_INVALID" });
  }
});
test("native pairing selects fixed 8766; default and unpaired keep ephemeral port", () => {
  assert.deepEqual(sidecarLaunchOptions(), { args: ["--sidecar"], port: 0 });
  assert.deepEqual(sidecarLaunchOptions({ mode: "go-bootstrap" }), { args: ["--sidecar-bootstrap"], port: 0 });
  assert.deepEqual(sidecarLaunchOptions({ mode: "go-bootstrap", extensionOrigins: [BUNDLED_EXTENSION_ORIGIN] }), {
    args: ["--sidecar-bootstrap", "--extension-origin", BUNDLED_EXTENSION_ORIGIN, "--extension-port", "8766"], port: 8766 });
  for (const options of [{ mode: "python-fallback" }, { extensionOrigins: [BUNDLED_EXTENSION_ORIGIN] },
    { mode: "go-bootstrap", extensionOrigins: "chrome-extension://test" }]) {
    assert.throws(() => sidecarLaunchOptions(options), { code: "DESKTOP_LAUNCH_CONFIGURATION_INVALID" });
  }
});
