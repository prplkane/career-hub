import assert from "node:assert/strict";
import test from "node:test";
import {readFile} from "node:fs/promises";
import vm from "node:vm";

const source = await readFile(new URL("../src/intake_ui_static/app.js",import.meta.url),"utf8");
const helper = source.slice(source.indexOf("const vaultPendingLoads ="),source.indexOf("async function loadVaultExperiences("));
function setup() {
  const views = new Map();
  const document = {getElementById(id) {
    if (!views.has(id)) views.set(id,{inert:false,attributes:{},setAttribute(key,value){this.attributes[key]=value;},removeAttribute(key){delete this.attributes[key];}});
    return views.get(id);
  }};
  return {views,load:vm.runInNewContext(helper+"\nwithVaultLoading",{document})};
}
test("Vault stays inert through overlapping reads, without freezing other views",async()=>{
  const {views,load}=setup();let finishFirst,finishSecond;
  const first=load("vault",()=>new Promise(r=>{finishFirst=r;}));
  const second=load("vault",()=>new Promise(r=>{finishSecond=r;}));
  const view=views.get("view-vault");assert.equal(view.inert,true);assert.equal(view.attributes["aria-busy"],"true");
  await load("projects",async()=>{});assert.equal(views.get("view-projects").inert,false);assert.equal(view.inert,true);
  finishFirst();await first;assert.equal(view.inert,true);
  finishSecond();await second;assert.equal(view.inert,false);assert.equal(view.attributes["aria-busy"],undefined);
});
test("Failed loads always release their editor",async()=>{
  const {views,load}=setup();await assert.rejects(load("education",async()=>{throw Error("synthetic");}));
  assert.equal(views.get("view-education").inert,false);
});
test("All three mutable Vault editors acquire the loading boundary before awaiting data",()=>{
  for(const [name,view] of [["Experiences","vault"],["Projects","projects"],["Education","education"]]) {
    const start=source.indexOf(`async function loadVault${name}(`);
    assert.match(source.slice(start,start+200),new RegExp(`return withVaultLoading\\("${view}"`));
  }
});

const viewLoader = source.slice(source.indexOf("async function ensureViewData("), source.indexOf("async function refreshActiveView("));
function setupViewLoader(view, check, read = async () => {}) {
  const calls = [];
  const state = {activeView: view};
  const views = new Map();
  const document = {getElementById(id) {
    if (!views.has(id)) views.set(id, {inert: false, attributes: {}, setAttribute(key, value) {this.attributes[key] = value;}, removeAttribute(key) {delete this.attributes[key];}});
    return views.get(id);
  }};
  const recordRead = (view, options) => {calls.push([view, options.refresh]); return read();};
  const load = vm.runInNewContext(helper + viewLoader + "\nensureViewData", {
    document,
    state,
    ensureVaultSetup: check,
    switchView: next => calls.push(["navigate", next]),
    loadVaultExperiences: options => recordRead("vault", options),
    loadVaultProjects: options => recordRead("projects", options),
    loadVaultEducation: options => recordRead("education", options),
  });
  return {load, state, calls, views};
}

for (const view of ["vault", "projects", "education"]) {
  test(`Empty ${view} opens setup without reading candidate data`, async () => {
    const {load, calls} = setupViewLoader(view, async () => false);
    await load(view);
    assert.deepEqual(calls, [["navigate", "profile"]]);
  });
  test(`Configured ${view} retains its requested refresh`, async () => {
    const {load, calls} = setupViewLoader(view, async () => true);
    await load(view, {force: true});
    assert.deepEqual(calls, [[view, true]]);
  });
  test(`${view} stays inert through delayed setup and candidate reads`, async () => {
    let finishSetup, finishRead, readStarted;
    const started = new Promise(resolve => {readStarted = resolve;});
    const {load, views} = setupViewLoader(view,
      () => new Promise(resolve => {finishSetup = resolve;}),
      () => {readStarted(); return new Promise(resolve => {finishRead = resolve;});});
    const pending = load(view);
    const editor = views.get(`view-${view}`);
    assert.equal(editor.inert, true);
    assert.equal(editor.attributes["aria-busy"], "true");
    finishSetup(true);
    await started;
    assert.equal(editor.inert, true);
    finishRead();
    await pending;
    assert.equal(editor.inert, false);
    assert.equal(editor.attributes["aria-busy"], undefined);
  });
}

test("An obsolete empty-Vault check does not redirect a newer view", async () => {
  let finish;
  const {load, state, calls} = setupViewLoader("vault", () => new Promise(resolve => {finish = resolve;}));
  const pending = load("vault");
  state.activeView = "job-feed";
  finish(false);
  await pending;
  assert.deepEqual(calls, []);
});

test("Setup failures do not become successful empty candidate reads", async () => {
  const {load, calls} = setupViewLoader("vault", async () => {throw Error("synthetic setup failure");});
  await assert.rejects(load("vault"), /synthetic setup failure/);
  assert.deepEqual(calls, []);
});
