"use strict";

const BUNDLED_EXTENSION_ORIGIN = "chrome-extension://jhgaecfochimmafoopcpodncoeobecfg";
const EXTENSION_PORT = 8766;

class LaunchConfigurationError extends Error {
  constructor() {
    super("DESKTOP_LAUNCH_CONFIGURATION_INVALID");
    this.code = "DESKTOP_LAUNCH_CONFIGURATION_INVALID";
  }
}

function normalizedExtensionOrigins(origins) {
  if (!Array.isArray(origins) || origins.length > 16) throw new LaunchConfigurationError();
  const result = [];
  for (const origin of origins) {
    if (typeof origin !== "string") throw new LaunchConfigurationError();
    const match = /^(chrome-extension|moz-extension|safari-web-extension):\/\/([A-Za-z0-9._-]{1,255})\/?$/.exec(origin);
    if (!match) throw new LaunchConfigurationError();
    result.push(`${match[1]}://${match[2]}`);
  }
  return Object.freeze([...new Set(result)]);
}

// Host arguments only. No webpage, renderer IPC, environment or directory scan
// can select a runtime, enable an extension or change the data channel.
function readLaunchConfiguration({ argv, isPackaged, channel, platform = process.platform, sidecarRuntime = "go" }) {
  if (!Array.isArray(argv)) throw new LaunchConfigurationError();
  if (!["go", "go-preview"].includes(sidecarRuntime) || platform !== "win32" ||
      !["dev", "stable"].includes(channel)) throw new LaunchConfigurationError();
  const packagedGo = sidecarRuntime === "go-preview";
  if (packagedGo && (!isPackaged || channel !== "dev" || platform !== "win32")) throw new LaunchConfigurationError();
  let goPreview = packagedGo;
  let bundled = false;
  const origins = [];
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === "--go-preview") {
      if (goPreview) throw new LaunchConfigurationError();
      goPreview = true;
    } else if (arg === "--enable-extension") {
      if (bundled) throw new LaunchConfigurationError();
      bundled = true;
    } else if (arg === "--extension-origin") {
      origins.push(argv[++i]);
    } else if (typeof arg === "string" && /^(--go-preview|--enable-extension|--extension-origin)=/.test(arg)) {
      throw new LaunchConfigurationError();
    }
  }
  if ((bundled && origins.length)
    || (goPreview && ((isPackaged && !packagedGo) || channel !== "dev" || platform !== "win32"))) {
    throw new LaunchConfigurationError();
  }
  return Object.freeze({ goPreview,
    extensionOrigins: normalizedExtensionOrigins(bundled ? [BUNDLED_EXTENSION_ORIGIN] : origins) });
}

function sidecarLaunchOptions({ mode = "readonly", extensionOrigins = [] } = {}) {
  if (!["readonly", "go-bootstrap"].includes(mode)) throw new LaunchConfigurationError();
  const origins = normalizedExtensionOrigins(extensionOrigins);
  if (mode !== "go-bootstrap" && origins.length) throw new LaunchConfigurationError();
  return Object.freeze({
    args: Object.freeze([mode === "go-bootstrap" ? "--sidecar-bootstrap" : "--sidecar",
      ...origins.flatMap((origin) => ["--extension-origin", origin]),
      ...(origins.length ? ["--extension-port", String(EXTENSION_PORT)] : [])]),
    port: origins.length ? EXTENSION_PORT : 0,
  });
}

module.exports = { BUNDLED_EXTENSION_ORIGIN, EXTENSION_PORT, LaunchConfigurationError,
  readLaunchConfiguration, sidecarLaunchOptions };
