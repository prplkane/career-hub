"use strict";

const path = require("node:path");

const LOCKED_WEB_PREFERENCES = Object.freeze({
  allowRunningInsecureContent: false,
  contextIsolation: true,
  devTools: false,
  experimentalFeatures: false,
  javascript: true,
  nodeIntegration: false,
  nodeIntegrationInSubFrames: false,
  nodeIntegrationInWorker: false,
  sandbox: true,
  spellcheck: true,
  webSecurity: true,
  webviewTag: false,
});

function secureBrowserWindowOptions({ preloadPath } = {}) {
  const webPreferences = { ...LOCKED_WEB_PREFERENCES };
  if (preloadPath !== undefined) {
    if (typeof preloadPath !== "string" || !path.isAbsolute(preloadPath)) {
      throw new TypeError("Preload path must be absolute");
    }
    webPreferences.preload = preloadPath;
  }
  return {
    backgroundColor: "#f4f1e8",
    height: 860,
    minHeight: 640,
    minWidth: 900,
    show: false,
    title: "Career Hub",
    useContentSize: true,
    width: 1320,
    webPreferences,
  };
}

function denySessionPermissions(electronSession) {
  if (
    !electronSession ||
    typeof electronSession.setPermissionCheckHandler !== "function" ||
    typeof electronSession.setPermissionRequestHandler !== "function"
  ) {
    throw new TypeError("Electron session permission APIs are required");
  }
  electronSession.setPermissionCheckHandler(() => false);
  electronSession.setPermissionRequestHandler((_webContents, _permission, callback) => {
    callback(false);
  });
  if (typeof electronSession.setDisplayMediaRequestHandler === "function") {
    electronSession.setDisplayMediaRequestHandler((_request, callback) => {
      callback({});
    });
  }
}

module.exports = {
  LOCKED_WEB_PREFERENCES,
  denySessionPermissions,
  secureBrowserWindowOptions,
};
