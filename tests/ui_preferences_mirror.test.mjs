import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import vm from "node:vm";

const read = (path) => readFile(new URL(`../${path}`, import.meta.url), "utf8");
const themeSource = await read("src/intake_ui_static/theme.js");

function storage(initial = {}) {
  const values = new Map(Object.entries(initial));
  return {
    values,
    getItem: (key) => (values.has(key) ? values.get(key) : null),
    setItem: (key, value) => { values.set(key, String(value)); },
  };
}

// Runs theme.js the way the page does: first, before anything paints.
function runTheme({ snapshot, stored = {} }) {
  const localStorage = storage(stored);
  const saved = [];
  const root = { dataset: {}, style: {} };
  const window = {
    localStorage,
    matchMedia: () => ({ matches: false, addEventListener() {}, addListener() {} }),
    careerHubDesktop: snapshot === undefined ? undefined : {
      preferences: { snapshot, set: (key, value) => { saved.push([key, value]); return Promise.resolve(true); } },
    },
    addEventListener() {},
  };
  const document = { documentElement: root, addEventListener() {}, querySelectorAll: () => [] };
  vm.runInNewContext(themeSource, { window, document, console });
  return { localStorage, root, saved };
}

test("saved desktop choices reach storage before the theme is applied", () => {
  const { localStorage, root } = runTheme({
    snapshot: { "careerHub.theme": "dark", "careerHub.locale": "en", "careerHub.lastView": "job-feed" },
    stored: { "careerHub.theme": "light" },
  });
  assert.equal(root.dataset.theme, "dark", "the saved theme wins over the empty new origin");
  assert.equal(localStorage.getItem("careerHub.locale"), "en", "locale.js will find the saved language");
  assert.equal(localStorage.getItem("careerHub.lastView"), "job-feed");
});

test("a plain browser without the desktop bridge keeps its own storage", () => {
  const { root } = runTheme({ snapshot: undefined, stored: { "careerHub.theme": "dark" } });
  assert.equal(root.dataset.theme, "dark");
});

test("every place that saves a UI choice also saves it to the desktop file", async () => {
  const sites = [
    ["src/intake_ui_static/app.js", "saveDesktopPreference(LAST_VIEW_KEY, viewName);"],
    ["src/intake_ui_static/features/discoveries.js", "saveDesktopPreference(MODE_KEY, state.mode);"],
    ["src/intake_ui_static/features/discoveries.js", "saveDesktopPreference(PAGE_SIZE_KEY, String(state.pageSize));"],
    ["src/intake_ui_static/features/skills_hub.js", "saveDesktopPreference(SKILLS_HUB_MODE_KEY, mode);"],
    ["src/intake_ui_static/features/skills_hub.js", "saveDesktopPreference(SKILLS_REGISTRY_LAYOUT_KEY, layout);"],
    ["src/intake_ui_static/features/demand_gaps.js", "saveDesktopPreference(DEMAND_GAPS_PERIOD_KEY, period);"],
    ["src/intake_ui_static/theme.js", "desktopPreferences?.set?.(storageKey, preference)"],
    ["src/intake_ui_static/locale.js", "window.careerHubDesktop?.preferences?.set?.(storageKey, preference)"],
  ];
  for (const [path, call] of sites) {
    assert.ok((await read(path)).includes(call), `${path} does not mirror: ${call}`);
  }
  // Every localStorage write of a careerHub.* key must sit in one of those files.
  const writers = (await Promise.all([
    "src/intake_ui_static/app.js", "src/intake_ui_static/features/discoveries.js",
    "src/intake_ui_static/features/skills_hub.js", "src/intake_ui_static/features/demand_gaps.js",
  ].map(read))).join("\n");
  assert.equal((writers.match(/localStorage\.setItem\(/g) || []).length, (writers.match(/saveDesktopPreference\(/g) || []).length,
    "each module setItem has a matching saveDesktopPreference call");
});

test("the sidecar serves the new module, or every page import would fail", async () => {
  assert.match(await read("backend/internal/sidecar/httpapi/static.go"), /"ui\/desktop_preferences\.js"/);
});
