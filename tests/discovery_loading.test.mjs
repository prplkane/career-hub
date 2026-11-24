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

function loadHarness({items = [], dismissedTotal = 0, fail = false, detail = null, selectedId = null} = {}) {
  const code = source.slice(source.indexOf("export async function loadDiscoveries("), source.indexOf("async function goToDiscoveryPage("))
    .replace("export async function", "async function");
  const state = {loadEpoch: 0, visibility: "active", pageSize: 20, currentPage: 1, selectedId, detail, items: []};
  const el = {
    message: {}, list: {setAttribute() {}}, archivedCount: {}, search: {value: ""}, sort: {value: "last_desc"},
  };
  const calls = {requests: [], renders: 0, details: []};
  const load = vm.runInNewContext(code + "\nloadDiscoveries", {
    state, el, Math, Number,
    api: {async getDiscoveries(request) {
      calls.requests.push(request);
      if (fail) throw Error("synthetic failure");
      return {items, total: items.length, dismissed_total: dismissedTotal, next_cursor: null};
    }},
    renderRows() { calls.renders++; },
    selectedSourceKinds: () => ["email_file"], selectedQueues: () => [], selectedObservedAfter: () => "",
    discoveryPageCursor: () => null, setDiscoveryVisibility() {}, resetFieldEditor() {},
    visibleItems: () => state.items, formatRequestError: String,
    async loadDetail(id) { calls.details.push(id); },
  });
  return {state, el, calls, load};
}

test("one feed request carries the dismissed count for the archive badge", async () => {
  const item = {discovery_id: "fictional-a"};
  const {state, el, calls, load} = loadHarness({items: [item], dismissedTotal: 7, selectedId: "fictional-a", detail: item});
  await load();
  assert.equal(calls.requests.length, 1, "the dismissed count must not need a second request");
  assert.equal(state.archivedTotal, 7);
  assert.equal(el.archivedCount.textContent, "7");
});

test("a settled load renders once after the loading state when no detail is fetched", async () => {
  const item = {discovery_id: "fictional-a"};
  const {calls, load} = loadHarness({items: [item], selectedId: "fictional-a", detail: item});
  await load();
  assert.equal(calls.renders, 2, "loading state, then the single settled render");
  assert.deepEqual(calls.details, []);
});

test("rows are drawn before a pending detail fetch, then settle once", async () => {
  const {calls, load} = loadHarness({items: [{discovery_id: "fictional-a"}]});
  await load();
  assert.deepEqual(calls.details, ["fictional-a"]);
  assert.equal(calls.renders, 3);
});

test("a failed load renders its error state once", async () => {
  const {state, el, calls, load} = loadHarness({fail: true});
  await load();
  assert.equal(calls.renders, 2, "loading state, then the finally render only");
  assert.match(el.message.textContent, /synthetic failure/);
  assert.equal(state.loading, false);
});
