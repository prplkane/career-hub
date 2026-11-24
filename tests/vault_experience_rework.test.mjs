import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import vm from "node:vm";

import {
  createSkillHighlighter,
  matchingCategory,
  normalizeCategoryKey,
} from "../src/intake_ui_static/features/vault_shared.js";

const appSource = await readFile(new URL("../src/intake_ui_static/app.js", import.meta.url), "utf8");
const indexHtml = await readFile(new URL("../src/intake_ui_static/index.html", import.meta.url), "utf8");
const vaultCss = await readFile(new URL("../src/intake_ui_static/styles/career-vault.css", import.meta.url), "utf8");

function slice(source, start, end) {
  const from = source.indexOf(start);
  const to = source.indexOf(end, from);
  assert.ok(from >= 0 && to > from, `missing ${start}`);
  return source.slice(from, to).replace(/^export /gm, "");
}

test("category keys ignore case, spaces and punctuation", () => {
  assert.equal(normalizeCategoryKey(" Security-Engineering "), "securityengineering");
  assert.equal(normalizeCategoryKey("Data & Analytics"), "dataanalytics");
  assert.equal(normalizeCategoryKey(""), "");
});

test("an existing category is suggested only when the spelling differs", () => {
  const categories = ["Security Engineering", "Data"];
  assert.equal(matchingCategory("security engineering", categories), "Security Engineering");
  assert.equal(matchingCategory("Security_Engineering ", categories), "Security Engineering");
  assert.equal(matchingCategory("Security Engineering", categories), null);
  assert.equal(matchingCategory("Platform", categories), null);
  assert.equal(matchingCategory("", categories), null);
  assert.equal(matchingCategory("data", undefined), null);
});

test("linked skills are marked inline, longest name first, with the text escaped", () => {
  const highlight = createSkillHighlighter([
    { name: "Go", aliases: ["Golang"], origin: "core" },
    { name: "C++", aliases: [], origin: "custom" },
    { name: "Python", aliases: ["py"], origin: "core" },
  ]);
  assert.equal(
    highlight("Wrote Golang and C++ tools in python <fast>"),
    'Wrote <mark class="skill-highlight registry-skill-core">Golang</mark> and '
    + '<mark class="skill-highlight registry-skill-custom">C++</mark> tools in '
    + '<mark class="skill-highlight registry-skill-core">python</mark> &lt;fast&gt;',
  );
  // Whole words only: "Pythonic" and "cargo" do not contain a linked skill.
  assert.equal(highlight("Pythonic cargo"), "Pythonic cargo");
  assert.equal(createSkillHighlighter([])("a < b"), "a &lt; b");
  assert.equal(createSkillHighlighter([{ name: "", aliases: [] }])("plain"), "plain");
});

function routeHarness({ hash, stored }) {
  const storage = new Map(Object.entries(stored || {}));
  const window = {
    location: { hash },
    localStorage: {
      getItem: (key) => (storage.has(key) ? storage.get(key) : null),
      setItem: (key, value) => storage.set(key, String(value)),
    },
  };
  const code = slice(appSource, "const SUPPORTED_VIEWS = new Set([", "function switchView(")
    + "\n({ viewFromLocalRoute, rememberLastView })";
  const desktop = [];
  const saveDesktopPreference = (key, value) => desktop.push([key, value]);
  return { ...vm.runInNewContext(code, { window, saveDesktopPreference }), storage, desktop };
}

test("a configured workspace defaults to Jobs, restores the last view, and honors explicit routes", () => {
  assert.equal(routeHarness({ hash: "" }).viewFromLocalRoute().viewName, "job-feed");
  assert.equal(
    routeHarness({ hash: "", stored: { "careerHub.lastView": "projects" } }).viewFromLocalRoute().viewName,
    "projects",
  );
  // A stored value that is not a view name cannot be restored.
  assert.equal(
    routeHarness({ hash: "", stored: { "careerHub.lastView": "review" } }).viewFromLocalRoute().viewName,
    "job-feed",
  );
  // An explicit route still wins.
  assert.equal(
    routeHarness({ hash: "#vault", stored: { "careerHub.lastView": "projects" } }).viewFromLocalRoute().viewName,
    "vault",
  );
});

test("switching views remembers only real views", () => {
  const harness = routeHarness({ hash: "" });
  harness.rememberLastView("education");
  assert.equal(harness.storage.get("careerHub.lastView"), "education");
  harness.rememberLastView("review");
  assert.equal(harness.storage.get("careerHub.lastView"), "education");
  // The desktop file gets the same real view, so it survives the next launch.
  assert.deepEqual(harness.desktop, [["careerHub.lastView", "education"]]);
});

test("the vault pages have one navigation model: no layout toggle or card mode", () => {
  assert.doesNotMatch(indexHtml, /vault-layout-switch|data-vault-layout|vault-card-back/);
  assert.doesNotMatch(vaultCss, /data-vault-layout|vault-card-/);
  assert.doesNotMatch(appSource, /careerVaultLayout/);
});

test("the Experience editor lists evidence before skills and pins the list and Save bar", () => {
  const form = slice(indexHtml, '<form id="vault-experience-form">', "</form>");
  assert.ok(form.indexOf("vault-proof-point-list") < form.indexOf("vault-skill-pills"));
  assert.match(form, /id="vault-skill-toggle"/);
  assert.match(form, /id="vault-category-hint"/);
  const panel = slice(vaultCss, ".vault-record-panel {", "}");
  assert.match(panel, /position: sticky/);
  assert.doesNotMatch(slice(vaultCss, ".vault-record-list {", "}"), /max-height/);
  assert.match(slice(vaultCss, ".vault-workspace .vault-form-actions {", "}"), /position: sticky;[\s\S]*bottom: 46px/);
});
