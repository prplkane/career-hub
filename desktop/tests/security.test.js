"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const test = require("node:test");

const {
  LOCKED_WEB_PREFERENCES,
  denySessionPermissions,
  secureBrowserWindowOptions,
} = require("../src/security");

test("BrowserWindow preferences explicitly keep renderer privileges disabled", () => {
  const preloadPath = path.resolve("desktop", "src", "preload.js");
  const options = secureBrowserWindowOptions({ preloadPath });
  assert.deepEqual(options.webPreferences, {
    ...LOCKED_WEB_PREFERENCES,
    preload: preloadPath,
  });
  assert.equal(options.webPreferences.nodeIntegration, false);
  assert.equal(options.webPreferences.contextIsolation, true);
  assert.equal(options.webPreferences.sandbox, true);
  assert.equal(options.webPreferences.webviewTag, false);
  assert.equal(options.webPreferences.webSecurity, true);
  assert.equal(options.webPreferences.devTools, false);
});

test("BrowserWindow rejects a relative preload", () => {
  assert.throws(
    () => secureBrowserWindowOptions({ preloadPath: "preload.js" }),
    /Preload path must be absolute/,
  );
});

test("session permission handlers default every request to denied", () => {
  let checkHandler;
  let requestHandler;
  let displayHandler;
  denySessionPermissions({
    setDisplayMediaRequestHandler(handler) {
      displayHandler = handler;
    },
    setPermissionCheckHandler(handler) {
      checkHandler = handler;
    },
    setPermissionRequestHandler(handler) {
      requestHandler = handler;
    },
  });

  assert.equal(checkHandler({}, "notifications", "http://127.0.0.1"), false);
  let permissionResult = true;
  requestHandler({}, "geolocation", (value) => {
    permissionResult = value;
  });
  assert.equal(permissionResult, false);
  let displayResult = null;
  displayHandler({}, (value) => {
    displayResult = value;
  });
  assert.deepEqual(displayResult, {});
});
