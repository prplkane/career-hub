import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";

const library = await readFile(new URL("../src/intake_ui_static/features/resume_library.js", import.meta.url), "utf8");
const app = await readFile(new URL("../src/intake_ui_static/app.js", import.meta.url), "utf8");
const css = await readFile(new URL("../src/intake_ui_static/styles/components.css", import.meta.url), "utf8");

function between(text, start, end) {
  const from = text.indexOf(start);
  const to = text.indexOf(end, from);
  assert.ok(from >= 0 && to > from, `missing ${start}`);
  return text.slice(from, to);
}

test("row actions render in fixed slot order: Review, Download, Archive or Restore, Delete", () => {
  const actions = between(library, '<div class="resume-library-record-actions">', "</div>");
  const order = [
    'data-resume-library-action="${digestAction}"',
    'data-resume-library-action="download"',
    'data-resume-library-action="${action}"',
    "${deleteButton}",
  ].map((marker) => actions.indexOf(marker));
  assert.ok(order.every((at) => at >= 0), "every slot is rendered");
  assert.deepEqual([...order].sort((a, b) => a - b), order);
});

test("a resume that cannot be deleted keeps an empty Delete slot", () => {
  const deleteButton = between(library, "const deleteButton = ", ";\n");
  assert.match(deleteButton, /class="text-button danger-text compact-button"/);
  assert.match(deleteButton, /: '<span class="resume-library-action-slot" aria-hidden="true"><\/span>'/);
});

test("the action block is a four-slot grid", () => {
  const rule = between(css, ".resume-library-record-actions {", "}");
  assert.match(rule, /display: grid;/);
  assert.equal(rule.match(/grid-template-columns: ([^;]+);/)[1].trim().split(/\s+/).length, 4);
});

test("a save clears the form only while the saved file is still chosen", () => {
  const save = between(app, "async function saveLibraryResume()", "\n}\n");
  const guard = save.indexOf("if (elements.libraryResumeFile.files[0] === file) {");
  const clear = save.indexOf('elements.libraryResumeDisplayName.value = "";');
  assert.ok(guard >= 0 && clear > guard, "the clear sits inside the guard");
  assert.ok(save.indexOf("await loadResumeLibrary();") < guard, "the guard runs after the reload that opens the race");
});
