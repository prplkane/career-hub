"use strict";
const assert = require("node:assert/strict");
const test = require("node:test");

function element() {
  return { hidden: true, disabled: true, value: "", open: false, listeners: {}, attributes: {},
    addEventListener(name, fn) { this.listeners[name] = fn; },
    setAttribute(name, value) { this.attributes[name] = value; },
    matches() { return this.open; }, focus() {}, select() { this.selected = true; } };
}
const tick = () => new Promise(resolve => setImmediate(resolve));
const connectionFile = String.raw`C:\Users\Example\AppData\Local\CareerHub\dev\session\agent-connection.json`;
const connection = (port = 12345) => ({ available: true, server: `http://127.0.0.1:${port}`, connection: connectionFile,
  executablePath: "C:\\Program Files\\Career Hub\\career-hub-agent.exe", contractVersion: 4, commands: ["context"] });

async function harness(t, getConnection, clipboard = async () => {}) {
  const root = element(), panel = element(), details = element(), output = element(), status = element(), copy = element();
  root.querySelector = selector => ({ details, textarea: output, '[role="status"]': status, button: copy })[selector];
  const previous = [global.window, global.document];
  global.window = { careerHubDesktop: { agent: { getConnection, async copyConnection() {
    const state = await getConnection();
    if (!state.available) return state;
    const { available, ...connection } = state;
    try { await clipboard(JSON.stringify(connection)); return { ...state, copied: true }; }
    catch (_error) { return { ...state, copied: false }; }
  } } } };
  global.document = { querySelector: selector => selector === "#desktop-agent-settings" ? root : panel };
  t.after(() => {
    global.window = previous[0]; global.document = previous[1];
  });
  const { initDesktopAgent } = await import("../../src/intake_ui_static/features/desktop_agent.js");
  initDesktopAgent();
  return { root, panel, details, output, status, copy, async open() {
    panel.open = true; details.open = true; details.listeners.toggle(); await tick();
  } };
}

test("connection stays collapsed, refreshes for copy, and discards details when Settings closes", async t => {
  let reads = 0, copied;
  const h = await harness(t, async () => connection(12345 + reads++), async value => { copied = value; });
  assert.equal(reads, 0);
  assert.equal(h.root.hidden, false);
  await h.open();
  assert.equal(JSON.parse(h.output.value).server, "http://127.0.0.1:12345");
  assert.equal(JSON.parse(h.output.value).connection, connectionFile);
  assert.equal(Object.hasOwn(JSON.parse(h.output.value), "token"), false);
  await h.copy.listeners.click();
  assert.equal(JSON.parse(copied).server, "http://127.0.0.1:12346");
  assert.equal(Object.hasOwn(JSON.parse(copied), "token"), false);
  h.panel.open = false; h.panel.listeners.toggle();
  assert.equal(h.output.value, "");
  assert.equal(h.copy.disabled, true);
  h.panel.open = true; h.panel.listeners.toggle(); await tick();
  assert.equal(JSON.parse(h.output.value).server, "http://127.0.0.1:12347");
});

test("late connection does not repopulate closed Settings; failure clears stale details", async t => {
  let resolve, fail = false;
  const h = await harness(t, () => fail ? Promise.reject(new Error("private error")) : new Promise(r => { resolve = r; }));
  await h.open();
  h.panel.open = false; h.panel.listeners.toggle();
  resolve(connection()); await tick();
  assert.equal(h.output.value, "");
  assert.equal(h.copy.disabled, true);
  fail = true; h.panel.open = true; h.panel.listeners.toggle(); await tick();
  assert.match(h.status.textContent, /Could not read/);
  assert.doesNotMatch(h.status.textContent, /private error/);
  assert.equal(h.root.attributes["aria-busy"], "false");
});

test("clipboard refusal offers manual selection; unavailable hosts never expose details", async t => {
  let state = connection();
  const h = await harness(t, async () => state, async () => { throw new Error("denied"); });
  await h.open(); await h.copy.listeners.click();
  assert.equal(h.output.selected, true);
  assert.match(h.status.textContent, /manually/);
  for (const reason of ["service_unavailable", "package_changed", "not_supported"]) {
    state = { available: false, reason };
    h.panel.listeners.toggle(); await tick();
    assert.equal(h.output.value, "");
    assert.equal(h.copy.disabled, true);
  }
  assert.equal(h.root.hidden, true);
});

test("a hung discovery releases its loading state at the renderer deadline", async t => {
  let expire;
  const oldTimer = global.setTimeout, oldClear = global.clearTimeout;
  global.setTimeout = (callback, milliseconds) => { assert.equal(milliseconds, 5000); expire = callback; return 1; };
  global.clearTimeout = () => {};
  t.after(() => { global.setTimeout = oldTimer; global.clearTimeout = oldClear; });
  const h = await harness(t, () => new Promise(() => {}));
  await h.open();
  assert.equal(h.root.attributes["aria-busy"], "true");
  expire(); await tick();
  assert.equal(h.root.attributes["aria-busy"], "false");
  assert.equal(h.copy.disabled, true);
  assert.match(h.status.textContent, /Close and reopen/);
});
