"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const PROJECT_ROOT = path.resolve(__dirname, "..");
const source = fs.readFileSync(
  path.join(PROJECT_ROOT, "extension", "browser-api.js"),
  "utf8",
);

function loadAdapter(namespace, api) {
  const sandbox = { URL, [namespace]: api };
  sandbox.globalThis = sandbox;
  vm.runInContext(source, vm.createContext(sandbox), { filename: "browser-api.js" });
  return sandbox.CareerHubBrowser;
}

async function main() {
  const chromeCalls = [];
  const chromeAdapter = loadAdapter("chrome", {
    runtime: {
      getURL: () => "chrome-extension://fixed-extension-id/",
    },
    sidePanel: {
      async open(options) {
        chromeCalls.push(["sidePanel", { ...options }]);
      },
    },
    tabs: {
      async create(options) {
        chromeCalls.push(["tab", { ...options }]);
      },
    },
  });
  assert.equal(chromeAdapter.extensionOrigin(), "chrome-extension://fixed-extension-id");
  assert.equal(
    (await chromeAdapter.openApplicationHelperSurface(7, 41)).surface,
    "side_panel",
  );
  assert.deepEqual(chromeCalls, [["sidePanel", { windowId: 7 }]]);

  const firefoxCalls = [];
  const firefoxAdapter = loadAdapter("browser", {
    runtime: {
      getURL: () => "moz-extension://4be18b7a-7660-4ec3-8d76-2b11a4af40d1/",
    },
    sidebarAction: {
      async open() {
        firefoxCalls.push(["sidebar"]);
      },
    },
    tabs: {
      async create(options) {
        firefoxCalls.push(["tab", { ...options }]);
      },
    },
  });
  assert.equal(
    firefoxAdapter.extensionOrigin(),
    "moz-extension://4be18b7a-7660-4ec3-8d76-2b11a4af40d1",
  );
  assert.equal(
    (await firefoxAdapter.openApplicationHelperSurface(3, 41)).surface,
    "sidebar",
  );
  assert.deepEqual(firefoxCalls, [["sidebar"]]);

  const safariCalls = [];
  const safariAdapter = loadAdapter("browser", {
    runtime: {
      getURL: (resource) => (
        `safari-web-extension://42A4188E-6082-4D79-A729-9C0A12D4A719/${resource}`
      ),
    },
    tabs: {
      async create(options) {
        safariCalls.push({ ...options });
      },
    },
  });
  assert.equal(
    safariAdapter.extensionOrigin(),
    "safari-web-extension://42A4188E-6082-4D79-A729-9C0A12D4A719",
  );
  assert.equal(
    (await safariAdapter.openApplicationHelperSurface(null, 41)).surface,
    "tab",
  );
  assert.deepEqual(safariCalls, [{
    active: true,
    url: "safari-web-extension://42A4188E-6082-4D79-A729-9C0A12D4A719/sidepanel.html?target_tab_id=41",
  }]);

  const fallbackCalls = [];
  const fallbackAdapter = loadAdapter("chrome", {
    runtime: {
      getURL: (resource) => `chrome-extension://fallback-id/${resource}`,
    },
    sidePanel: {
      async open() {
        throw new Error("side panel unavailable");
      },
    },
    tabs: {
      async create(options) {
        fallbackCalls.push({ ...options });
      },
    },
  });
  assert.equal(
    (await fallbackAdapter.openApplicationHelperSurface(9, 41)).surface,
    "tab",
  );
  assert.equal(fallbackCalls.length, 1);
  assert.equal(
    fallbackCalls[0].url,
    "chrome-extension://fallback-id/sidepanel.html?target_tab_id=41",
  );

  await assert.rejects(
    safariAdapter.openApplicationHelperSurface(null, null),
    /requires an application tab target/,
  );

  console.log("browser extension compatibility harness: OK");
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
