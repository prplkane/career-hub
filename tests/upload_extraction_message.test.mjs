import assert from "node:assert/strict";
import test from "node:test";
import {readFile} from "node:fs/promises";
import vm from "node:vm";

const source = await readFile(new URL("../src/intake_ui_static/app.js", import.meta.url), "utf8");
const runner = source.slice(
  source.indexOf("async function runExtractionForSource("),
  source.indexOf("async function continueResumeReview("),
);

function message() {
  return {className: "", textContent: ""};
}

function setup(extract) {
  const elements = {
    runButton: {disabled: false, textContent: ""},
    runMessage: message(),
    uploadMessage: message(),
    overwriteInput: {checked: true},
  };
  const run = vm.runInNewContext(`${runner}\nrunExtractionForSource`, {
    elements,
    requestJson: extract,
    prepareReviewForSource: async () => {},
    loadResumeCollections: async () => {},
    openResumeReview: async () => {},
    formatRequestError: (error) => error.message,
    pollLogs: async () => {},
  });
  return {elements, run};
}

test("Upload extraction failures appear in the visible upload message", async () => {
  const {elements, run} = setup(async () => { throw new Error("Extraction failed: unreadable DOCX"); });
  await run("resume.docx", {message: elements.uploadMessage, messageClass: "form-message upload-message"});
  assert.equal(elements.uploadMessage.textContent, "Extraction failed: unreadable DOCX");
  assert.equal(elements.uploadMessage.className, "form-message upload-message error");
  assert.equal(elements.runMessage.textContent, "");
  assert.equal(elements.runButton.disabled, false);
});

test("Upload extraction progress appears in the visible upload message", async () => {
  const {elements, run} = setup(async () => ({source_id: "src_resume_0123456789", technology_count: 3, bullet_count: 5}));
  await run("resume.docx", {message: elements.uploadMessage, messageClass: "form-message upload-message"});
  assert.equal(elements.uploadMessage.textContent, "Ready to review: 3 technologies, 5 bullets.");
  assert.equal(elements.uploadMessage.className, "form-message upload-message");
});

test("The legacy run control keeps its own message by default", async () => {
  const {elements, run} = setup(async () => { throw new Error("synthetic"); });
  await run("resume.docx");
  assert.equal(elements.runMessage.textContent, "synthetic");
  assert.equal(elements.runMessage.className, "form-message error");
  assert.equal(elements.uploadMessage.textContent, "");
});

test("The upload path passes its visible message to extraction", () => {
  const start = source.indexOf("async function uploadDocument(");
  const body = source.slice(start, source.indexOf("\n}\n", start));
  assert.match(body, /runExtractionForSource\(result\.name, \{\s*message: elements\.uploadMessage,/);
});
