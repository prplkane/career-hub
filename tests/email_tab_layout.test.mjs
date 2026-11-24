import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import vm from "node:vm";

const source = await readFile(new URL("../src/intake_ui_static/features/discoveries.js", import.meta.url), "utf8");
const html = await readFile(new URL("../src/intake_ui_static/index.html", import.meta.url), "utf8");

function slice(start, end) {
  const from = source.indexOf(start);
  const to = source.indexOf(end, from);
  assert.ok(from >= 0 && to > from, `missing ${start}`);
  return source.slice(from, to);
}

function between(text, start, end) {
  const from = text.indexOf(start);
  const to = text.indexOf(end, from);
  assert.ok(from >= 0 && to > from, `missing ${start}`);
  return text.slice(from, to);
}

test("new senders and source policies are folds; senders start closed", () => {
  assert.match(html, /<details class="email-sender-review email-fold hidden" id="email-sender-review">/);
  assert.match(html, /<details class="email-source-policies email-fold" id="email-source-policies" open>/);
});

test("one form wraps both folds, so Save sources and Save settings send the same connection", () => {
  const form = between(html, '<form class="email-settings-form" id="email-connection-form">', "</form>");
  const settingsAt = form.indexOf('id="email-connection-settings"');
  const sourcesAt = form.indexOf('id="email-source-policies"');
  assert.ok(sourcesAt >= 0 && settingsAt > sourcesAt, "source policies sit before, not inside, connection settings");
  assert.match(form, /<button type="submit" class="secondary-button" id="email-source-save">Save sources<\/button>/);
  assert.match(form, /<button type="submit" class="primary-button" id="email-connection-save"/);
  assert.match(form, /<tbody id="email-source-policy-list"><\/tbody>/);
});

function loadRoutes(senderDraft) {
  const context = vm.createContext({
    state: { senderDraft },
    escapeHtml: (value) => String(value ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]),
  });
  vm.runInContext(`${slice("function senderRouteEnabled(", "function captureQualityAlerts(")}
    ${slice("function renderSourceRoute(", "function renderSourceQuality(")}
    this.renderSourceRoute = renderSourceRoute;`, context);
  return context.renderSourceRoute;
}

test("a route renders as a named on/off switch over a real checkbox", () => {
  const renderSourceRoute = loadRoutes({ job_alerts: ["alerts@example.test"], application_tracking: [] });
  const on = renderSourceRoute("alerts@example.test", "job_alerts", "Jobs");
  assert.match(on, /class="email-route-toggle is-on"/);
  assert.match(on, /type="checkbox" data-email-source-route-toggle/);
  assert.match(on, /aria-label="Jobs from alerts@example.test" checked>/);
  assert.match(on, /<span>On<\/span>/);
  const off = renderSourceRoute("alerts@example.test", "application_tracking", "Applications");
  assert.doesNotMatch(off, / checked>/);
  assert.match(off, /<span>Off<\/span>/);
});

test("connection settings move only when readiness changes, never over the user's choice", () => {
  const settings = { open: true };
  const context = vm.createContext({
    state: { emailFormDirty: false, emailSettingsReady: null },
    el: {
      emailConnectionSettings: settings,
      emailAccount: {}, emailPassword: {}, emailScheduleEnabled: {}, emailSyncInterval: {},
    },
    senderDraftFromConnection: () => ({ job_alerts: [], application_tracking: [] }),
    renderEmailConnection: () => {},
    refreshEmailScheduler: () => {},
  });
  vm.runInContext(`${slice("function useEmailConnection(", "function emailReconnectRequired(")}
    this.useEmailConnection = useEmailConnection;`, context);
  const ready = { configured: true, enabled: true, credential_present: true };

  context.useEmailConnection(ready);
  assert.equal(settings.open, false, "a ready connection folds its settings");
  settings.open = true; // the user opens them again
  context.useEmailConnection(ready); // a later sync reloads the same, still-ready connection
  assert.equal(settings.open, true, "a reload leaves the user's choice alone");
  context.useEmailConnection({ ...ready, credential_present: false });
  assert.equal(settings.open, true, "losing readiness opens the settings");
  settings.open = false;
  context.useEmailConnection({ ...ready, credential_present: false });
  assert.equal(settings.open, false);
});
