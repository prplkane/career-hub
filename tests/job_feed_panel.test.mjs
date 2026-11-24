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

// The fold helper and the checklist renderer, run against a stub state.
function loadRenderers(detailFolds) {
  const context = vm.createContext({
    state: { detailFolds },
    escapeHtml: (value) => String(value ?? "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]),
  });
  vm.runInContext(`${slice("function detailFold(", "function renderDiscoveryAssessment(")}
    ${slice("const APPLICATION_INSTRUCTION_CATEGORY_LABELS", "const CONTACT_CONTEXT_LABELS")}
    this.renderDiscoveryInstructions = renderDiscoveryInstructions;`, context);
  return context;
}

const checklist = {
  review_assessment: {
    application_instructions: {
      eligible: true,
      items: [{ category: "deadline", confidence: "explicit", source_text: "Apply by Friday", line_start: 4, line_end: 4, extractor_version: 1 }],
    },
  },
};

test("a detail section is a fold whose summary keeps the heading and count", () => {
  const { renderDiscoveryInstructions } = loadRenderers({ assessment: false, checklist: false, contacts: false });
  const markup = renderDiscoveryInstructions(checklist);
  assert.match(markup, /^<details class="discovery-instruction-panel discovery-fold" data-discovery-fold="checklist">/);
  const summary = markup.slice(markup.indexOf("<summary"), markup.indexOf("</summary>"));
  assert.match(summary, /Application checklist/);
  assert.match(summary, /id="discovery-instruction-heading">Extra steps</);
  assert.match(summary, /1 flagged/);
  assert.match(markup, /Apply by Friday/, "the folded body is still rendered");
});

test("a fold opens from state, so a re-render keeps the user's choice", () => {
  const { renderDiscoveryInstructions } = loadRenderers({ assessment: false, checklist: true, contacts: false });
  assert.match(renderDiscoveryInstructions(checklist), /data-discovery-fold="checklist" open>/);
});

test("an empty checklist still says so in its folded summary", () => {
  const { renderDiscoveryInstructions } = loadRenderers({ assessment: false, checklist: false, contacts: false });
  const empty = { review_assessment: { application_instructions: { eligible: true, items: [] } } };
  assert.match(renderDiscoveryInstructions(empty), /<span>None found<\/span>/);
});

test("folds start closed and the bulk bar starts hidden", () => {
  assert.match(source, /detailFolds: \{ assessment: false, checklist: false, contacts: false \}/);
  assert.match(html, /<div class="job-feed-bulk-bar" id="discovery-bulk-bar"[^>]* hidden>/);
  for (const id of ["discovery-bulk-count", "discovery-bulk-state", "discovery-bulk-delete", "discovery-bulk-clear", "discovery-select-page"]) {
    assert.match(html, new RegExp(`id="${id}"`), id);
  }
});

test("the retained job text renders above the verification panel that points to it", () => {
  const template = slice("el.detail.innerHTML = `<button", "function useUpdatedDiscovery(");
  assert.ok(template.indexOf("${sourceDetails}") < template.indexOf("${verificationPanel}"));
  assert.match(source, /Review the retained text above\./);
});
