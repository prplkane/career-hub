import assert from "node:assert/strict";
import test from "node:test";
import {readFile} from "node:fs/promises";
import vm from "node:vm";

const source = await readFile(new URL("../src/intake_ui_static/features/discoveries.js", import.meta.url), "utf8");
const helper = source.slice(source.indexOf("function renderPaginationControls("), source.indexOf("function renderRows("));
function setup() {
  const state = {currentPage: 2, loading: true};
  const el = {pagination: {setAttribute(key, value) { this[key] = value; }}, pageNumbers: {innerHTML: ""}};
  for (const name of ["firstPage", "prevPage", "nextPage", "lastPage"]) el[name] = {disabled: false};
  return {state, el, render: vm.runInNewContext(helper + "\nrenderPaginationControls", {state, el})};
}

test("every feed pagination control is disabled while a page is loading", () => {
  const {el, render} = setup();
  render(4);
  for (const name of ["firstPage", "prevPage", "nextPage", "lastPage"]) assert.equal(el[name].disabled, true, name);
  const buttons = el.pageNumbers.innerHTML.match(/<button[^>]*>/g);
  assert.equal(buttons.length, 4);
  for (const button of buttons) assert.match(button, / disabled>/);
  assert.equal(el.pagination["aria-busy"], "true");
});

test("pagination recovers after a load and retains first/last page boundaries", () => {
  const {state, el, render} = setup();
  render(4);
  state.loading = false;
  render(4);
  for (const name of ["firstPage", "prevPage", "nextPage", "lastPage"]) assert.equal(el[name].disabled, false, name);
  assert.doesNotMatch(el.pageNumbers.innerHTML, / disabled/);
  assert.equal(el.pagination["aria-busy"], "false");
  assert.match(el.pageNumbers.innerHTML, /data-page="2"[^>]*aria-current="page"/);
  state.currentPage = 1;
  render(4);
  assert.equal(el.firstPage.disabled, true);
  assert.equal(el.prevPage.disabled, true);
  assert.equal(el.nextPage.disabled, false);
  state.currentPage = 4;
  render(4);
  assert.equal(el.prevPage.disabled, false);
  assert.equal(el.nextPage.disabled, true);
  assert.equal(el.lastPage.disabled, true);
});

test("dismiss refreshes the current view without reopening a selected deep link", async () => {
  const state = {detail: {discovery_id: "fictional", revision: 1}, routeTargetId: "fictional", routeIntent: "prepare", visibility: "active"};
  const el = {message: {}};
  let complete, calls = 0, reloads = 0;
  const code = source.slice(source.indexOf("async function changeState("), source.indexOf("async function deleteSelectedDiscovery("));
  const change = vm.runInNewContext(code + "\nchangeState", {
    state, el, renderDetail() {}, formatRequestError: String,
    api: {async setDiscoveryState() { calls++; return new Promise(resolve => { complete = resolve; }); }},
    async loadDiscoveries() { reloads++; assert.equal(state.routeTargetId, null); assert.equal(state.routeIntent, null); assert.equal(state.visibility, "active"); },
  });
  const pending = change("dismissed");
  await change("dismissed");
  assert.equal(calls, 1, "a duplicate click must not submit another mutation");
  complete({discovery_id: "fictional", revision: 2, state: "dismissed"});
  await pending;
  assert.equal(reloads, 1);
  assert.equal(state.changingId, null);
});

test("failed lifecycle save releases its controls for a deliberate retry", async () => {
  const state = {detail: {discovery_id: "fictional", revision: 1}};
  const el = {message: {}};
  const code = source.slice(source.indexOf("async function changeState("), source.indexOf("async function deleteSelectedDiscovery("));
  const change = vm.runInNewContext(code + "\nchangeState", {
    state, el, renderDetail() {}, formatRequestError: String,
    api: {async setDiscoveryState() { throw Error("synthetic failure"); }},
    async loadDiscoveries() { assert.fail("must not reload after a refused write"); },
  });
  await change("dismissed");
  assert.equal(state.changingId, null);
  assert.match(el.message.textContent, /synthetic failure/);
});
