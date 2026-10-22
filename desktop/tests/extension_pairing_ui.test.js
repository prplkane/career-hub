"use strict";
const assert = require("node:assert/strict");
const test = require("node:test");

function element() {
  return { hidden: true, disabled: false, open: false, textContent: "", listeners: {},
    addEventListener(name, fn) { this.listeners[name] = fn; }, matches() { return this.open; } };
}
const tick = () => new Promise(resolve => setImmediate(resolve));

async function harness(t, api) {
  const root = element(), panel = element(), details = element(), code = element(), status = element();
  const create = element(), revoke = element();
  root.querySelector = selector => ({ details, code, '[role="status"]': status,
    '[data-action="create"]': create, '[data-action="revoke"]': revoke })[selector];
  const previous = global.document;
  global.document = { querySelector: selector => selector === "#extension-pairing-settings" ? root : panel };
  t.after(() => { global.document = previous; });
  const { initExtensionPairing } = await import("../../src/intake_ui_static/features/extension_pairing.js");
  let probes = 0;
  initExtensionPairing({ ...api, getExtensionPairing() { probes++; return api.getExtensionPairing(); } });
  await tick();
  assert.equal(probes, 0, "no request before Settings opens");
  panel.open = true; panel.listeners.toggle(); await tick();
  return { root, panel, details, code, status, create, revoke, async open() {
    panel.open = true; details.open = true; details.listeners.toggle(); await tick();
  } };
}

test("pairing code is shown once on request and discarded when Settings closes", async t => {
  let state = { enabled: true, paired: false };
  const h = await harness(t, {
    async getExtensionPairing() { return state; },
    async createExtensionPairingCode() { return { code: "ABCD-EFGH", expires_at: "2026-10-14T12:05:00Z" }; },
    async revokeExtensionPairing() { state = { enabled: true, paired: false }; return state; },
  });
  assert.equal(h.root.hidden, false);
  await h.open();
  assert.equal(h.code.hidden, true, "no code until the owner asks for one");
  assert.equal(h.revoke.hidden, true);
  assert.match(h.status.textContent, /Not paired/);
  await h.create.listeners.click();
  assert.equal(h.code.textContent, "ABCD-EFGH");
  assert.equal(h.code.hidden, false);
  assert.match(h.status.textContent, /works once/);
  h.panel.open = false; h.panel.listeners.toggle();
  assert.equal(h.code.textContent, "");
  assert.equal(h.code.hidden, true);

  state = { enabled: true, paired: true, paired_at: "2026-10-14T12:01:00Z" };
  await h.open();
  assert.equal(h.revoke.hidden, false);
  assert.match(h.status.textContent, /Paired/);
  await h.revoke.listeners.click();
  assert.equal(h.revoke.hidden, true);
  assert.match(h.status.textContent, /Unpaired/);
});

test("section stays hidden when the extension is not enabled and failures reveal nothing", async t => {
  const h = await harness(t, {
    async getExtensionPairing() { return { enabled: false, paired: false }; },
    async createExtensionPairingCode() { throw new Error("private detail"); },
    async revokeExtensionPairing() { throw new Error("private detail"); },
  });
  assert.equal(h.root.hidden, true);
  await h.create.listeners.click();
  assert.equal(h.code.hidden, true);
  assert.doesNotMatch(h.status.textContent, /private detail/);
});

test("a late code does not appear after Settings closed", async t => {
  let resolve;
  const h = await harness(t, {
    async getExtensionPairing() { return { enabled: true, paired: false }; },
    createExtensionPairingCode: () => new Promise(r => { resolve = r; }),
    async revokeExtensionPairing() { return { enabled: true, paired: false }; },
  });
  await h.open();
  const pending = h.create.listeners.click();
  h.panel.open = false; h.panel.listeners.toggle();
  resolve({ code: "ABCD-EFGH", expires_at: "2026-10-14T12:05:00Z" });
  await pending;
  assert.equal(h.code.textContent, "");
  assert.equal(h.code.hidden, true);
});
