import assert from "node:assert/strict";
import test from "node:test";

import { initDesktopUpdates, versionLabel } from "../src/intake_ui_static/features/desktop_updates.js";

test("Settings shows the version with the short build commit when it is known", () => {
  assert.equal(versionLabel({ currentVersion: "0.3.0", currentCommit: "abc1234" }), "v0.3.0 (abc1234)");
});

test("a missing or malformed commit shows the version alone", () => {
  for (const currentCommit of [null, undefined, "", "ABC1234", "abc123", "abc1234<b>", 42]) {
    assert.equal(versionLabel({ currentVersion: "0.3.0", currentCommit }), "v0.3.0", String(currentCommit));
  }
});

test("changing the automatic update checkbox sends the selected value while controls are busy", async (t) => {
  const elements = new Map();
  const root = { querySelector(id) {
    if (!elements.has(id)) elements.set(id, Object.assign(new EventTarget(), { removeAttribute() {} }));
    return elements.get(id);
  } };
  const state = { phase: "idle", currentVersion: "0.3.4", automaticUpdates: true,
    configured: true, supported: true, canCheck: true, canInstall: false };
  const requests = [];
  const original = [globalThis.window, globalThis.document];
  t.after(() => { [globalThis.window, globalThis.document] = original; });
  globalThis.document = { querySelector: () => root };
  globalThis.window = { careerHubDesktop: { updates: {
    getState: async () => state, onState() {},
    async setAutomatic(value) { requests.push(value); return { ...state, automaticUpdates: value }; },
  } } };
  initDesktopUpdates();
  await Promise.resolve();
  const checkbox = root.querySelector("#desktop-update-automatic");
  for (const value of [false, true]) {
    checkbox.checked = value;
    checkbox.dispatchEvent(new Event("change"));
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(checkbox.checked, value);
  }
  assert.deepEqual(requests, [false, true]);
});
