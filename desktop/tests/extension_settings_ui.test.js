"use strict";
const assert = require("node:assert/strict");
const test = require("node:test");

function element() {
  return { hidden: true, disabled: false, checked: false, value: "", textContent: "", listeners: {},
    addEventListener(name, fn) { this.listeners[name] = fn; } };
}
const tick = () => new Promise(resolve => setImmediate(resolve));
const FIREFOX = "moz-extension://4be18b7a-7660-4ec3-8d76-2b11a4af40d1";

async function harness(t, bridge) {
  const root = element(), toggle = element(), origins = element(), status = element(), restart = element(), save = element();
  root.querySelector = selector => ({ "#extension-enable-toggle": toggle, "#extension-extra-origins": origins,
    '[role="status"]': status, '[data-action="restart"]': restart, '[data-action="save-origins"]': save })[selector];
  const previous = [global.window, global.document];
  global.window = { careerHubDesktop: { extension: bridge } };
  global.document = { activeElement: null, querySelector: selector => selector === "#extension-enable-settings" ? root : null };
  t.after(() => { global.window = previous[0]; global.document = previous[1]; });
  const { initExtensionSettings } = await import("../../src/intake_ui_static/features/extension_settings.js");
  initExtensionSettings();
  await tick();
  return { root, toggle, origins, status, restart, save };
}

function hostState(overrides = {}) {
  return { bundledOrigin: "chrome-extension://jhgaecfochimmafoopcpodncoeobecfg", enabled: false, origins: [],
    launchOrigins: [], readable: true, restartRequired: false, runningEnabled: false, ...overrides };
}

test("the toggle saves through the host, shows the restart step and keeps pairing separate", async t => {
  let state = hostState();
  const requests = [];
  let relaunches = 0;
  const h = await harness(t, {
    async getState() { return state; },
    async set(request) {
      requests.push(request);
      state = hostState({ enabled: request.enabled, origins: request.origins, restartRequired: true });
      return state;
    },
    async relaunch() { relaunches++; },
  });
  assert.equal(h.root.hidden, false);
  assert.equal(h.toggle.checked, false);
  assert.equal(h.origins.disabled, true, "extra origins need the toggle on");
  assert.equal(h.restart.hidden, true);
  assert.match(h.status.textContent, /Off\./);

  h.toggle.checked = true;
  await h.toggle.listeners.change(); await tick();
  assert.deepEqual(requests, [{ enabled: true, origins: [] }]);
  assert.equal(h.restart.hidden, false);
  assert.match(h.status.textContent, /Restart Career Hub/);
  assert.equal(h.origins.disabled, false);

  h.origins.value = `${FIREFOX}/\n${FIREFOX}`;
  await h.save.listeners.click(); await tick();
  assert.deepEqual(requests.at(-1), { enabled: true, origins: [FIREFOX] }, "duplicates and trailing slashes collapse");
  assert.equal(h.origins.value, FIREFOX);

  h.origins.value = "https://evil.test";
  await h.save.listeners.click(); await tick();
  assert.equal(requests.length, 2, "an invalid origin never reaches the host");
  assert.match(h.status.textContent, /must look like/);

  await h.restart.listeners.click(); await tick();
  assert.equal(relaunches, 1);
  assert.equal(h.restart.disabled, true);
});

test("an unreadable or rejected setting stays off and says so; bridge failures reveal nothing", async t => {
  const h = await harness(t, {
    async getState() { return hostState({ readable: false }); },
    async set() { return hostState({ enabled: false, error: "DESKTOP_EXTENSION_PREFERENCES_INVALID" }); },
    async relaunch() { throw new Error("private detail"); },
  });
  assert.equal(h.toggle.checked, false);
  assert.match(h.status.textContent, /could not be read/);
  h.toggle.checked = true;
  await h.toggle.listeners.change(); await tick();
  assert.equal(h.toggle.checked, false, "the host's answer wins over the click");
  assert.match(h.status.textContent, /could not be saved/);
  await h.restart.listeners.click(); await tick();
  assert.doesNotMatch(h.status.textContent, /private detail/);
  assert.equal(h.restart.disabled, false);

  const failing = await harness(t, { async getState() { throw new Error("private detail"); }, async set() {}, async relaunch() {} });
  assert.equal(failing.root.hidden, false);
  assert.match(failing.status.textContent, /could not respond/);
  assert.doesNotMatch(failing.status.textContent, /private detail/);
});

test("a running sidecar with the extension on, or a launch flag, reads as on", async t => {
  const on = await harness(t, { async getState() { return hostState({ enabled: true, runningEnabled: true }); }, async set() {}, async relaunch() {} });
  assert.equal(on.toggle.checked, true);
  assert.match(on.status.textContent, /port 8766/);
  const flagged = await harness(t, { async getState() { return hostState({ launchOrigins: ["chrome-extension://dev"], runningEnabled: true }); }, async set() {}, async relaunch() {} });
  assert.equal(flagged.toggle.checked, false);
  assert.match(flagged.status.textContent, /port 8766/);
});
