import assert from "node:assert/strict";
import test from "node:test";
import {readFile} from "node:fs/promises";
import vm from "node:vm";

import {
  applicationStatusLabel,
  inlineStageKeyIntent,
} from "../src/intake_ui_static/features/applications.js";
import { createConfirmDialog } from "../src/intake_ui_static/ui/primitives.js";

const appSource = await readFile(new URL("../src/intake_ui_static/app.js", import.meta.url), "utf8");
const librarySource = await readFile(new URL("../src/intake_ui_static/features/resume_library.js", import.meta.url), "utf8");

function slice(source, start, end) {
  const from = source.indexOf(start);
  const to = source.indexOf(end, from);
  assert.ok(from >= 0 && to > from, `missing ${start}`);
  return source.slice(from, to).replace(/^export /gm, "");
}

test("keyboard steps on a stage list defer; Enter commits and Escape cancels", () => {
  for (const key of ["ArrowDown", "ArrowUp", "Home", "End", "PageDown", "a", "R"]) {
    assert.equal(inlineStageKeyIntent({key}), "defer", key);
  }
  assert.equal(inlineStageKeyIntent({key: "Enter"}), "commit");
  assert.equal(inlineStageKeyIntent({key: "Escape"}), "cancel");
  // Popup openers and shortcuts keep their native behavior.
  for (const event of [{key: " "}, {key: "F4"}, {key: "ArrowDown", altKey: true}, {key: "c", ctrlKey: true}, {key: "Tab"}]) {
    assert.equal(inlineStageKeyIntent(event), null, JSON.stringify(event));
  }
});

function stageHarness() {
  const saved = [];
  const elements = {applicationLedgerMessage: {className: "", textContent: ""}};
  const handlers = vm.runInNewContext(
    slice(appSource, "function restoreInlineApplicationStatus(", "async function saveApplicationStatus(")
      + "\n({restoreInlineApplicationStatus, handleInlineApplicationStatusKey, showInlineApplicationStatusHint})",
    {
      elements,
      inlineStageKeyIntent,
      applicationStatusLabel,
      saveInlineApplicationStatus: (select) => saved.push(select.value),
    },
  );
  const select = {value: "applied", dataset: {previousStatus: "applied"}};
  const key = (name) => {
    const event = {key: name, prevented: false, preventDefault() { this.prevented = true; }};
    handlers.handleInlineApplicationStatusKey(select, event);
    return event;
  };
  return {saved, elements, handlers, select, key};
}

test("an arrow-key stage change waits for Enter before saving", () => {
  const {saved, elements, handlers, select, key} = stageHarness();
  key("ArrowDown");
  select.value = "interview";
  handlers.showInlineApplicationStatusHint(select);
  assert.deepEqual(saved, []);
  assert.equal(elements.applicationLedgerMessage.textContent, "Press Enter to save Interview, or Escape to keep Applied.");
  const enter = key("Enter");
  assert.equal(enter.prevented, true);
  assert.deepEqual(saved, ["interview"]);
  assert.equal(select.dataset.keyboardPending, undefined);
});

test("Escape and leaving the list restore the saved stage without writing", () => {
  const {saved, elements, handlers, select, key} = stageHarness();
  key("ArrowDown");
  select.value = "rejected";
  key("Escape");
  assert.equal(select.value, "applied");
  assert.equal(elements.applicationLedgerMessage.textContent, "");
  key("End");
  select.value = "closed";
  handlers.restoreInlineApplicationStatus(select);
  assert.equal(select.value, "applied");
  assert.deepEqual(saved, []);
});

test("Enter without a pending keyboard change is left to the browser", () => {
  const {saved, key} = stageHarness();
  assert.equal(key("Enter").prevented, false);
  assert.deepEqual(saved, []);
});

test("inline stage messages use display labels, not status codes", () => {
  const body = slice(appSource, "async function saveInlineApplicationStatus(", "function restoreInlineApplicationStatus(");
  assert.doesNotMatch(body, /: \$\{status\}/);
  assert.match(body, /applicationStatusLabel\(status\)/);
});

const library = vm.runInNewContext(
  slice(librarySource, "const RESUME_DIGEST_URGENCY", "function filteredResumeLibraryRecords(")
    + "\n({compareResumeDigestUrgency, resumeReviewFormatNotice})",
);

test("review-status sort puts the most actionable resumes first", () => {
  const records = ["digested", "not_digested", "error", "in_progress", "outdated", "unknown_future_state"]
    .map((digest_status, index) => ({digest_status, display_name: `Resume ${index}`}));
  assert.deepEqual(
    records.sort(library.compareResumeDigestUrgency).map((record) => record.digest_status),
    ["error", "outdated", "in_progress", "not_digested", "unknown_future_state", "digested"],
  );
  const tie = [{digest_status: "error", display_name: "Beta"}, {digest_status: "error", display_name: "Alpha"}];
  assert.deepEqual(tie.sort(library.compareResumeDigestUrgency).map((record) => record.display_name), ["Alpha", "Beta"]);
});

test("choosing a format Review cannot read explains it before saving", () => {
  assert.equal(library.resumeReviewFormatNotice("Kane_SOC.DOCX"), "");
  assert.equal(library.resumeReviewFormatNotice("notes.txt"), "");
  assert.equal(library.resumeReviewFormatNotice(""), "");
  assert.equal(
    library.resumeReviewFormatNotice("Kane_SOC_2026.pdf"),
    "PDF files are saved for applications, but Review into Career Vault reads only DOCX and TXT.",
  );
  assert.match(library.resumeReviewFormatNotice("old.rtf"), /^RTF files/);
  assert.match(library.resumeReviewFormatNotice("legacy.doc"), /^DOC files/);
});

function fakeDialog() {
  const part = () => {
    const classes = new Set();
    return {
      textContent: "",
      focused: false,
      focus() { this.focused = true; },
      classList: {toggle: (name, on) => (on ? classes.add(name) : classes.delete(name)), has: (name) => classes.has(name)},
    };
  };
  const parts = {
    "[data-confirm-title]": part(),
    "[data-confirm-message]": part(),
    "[data-confirm-accept]": part(),
    "[data-confirm-cancel]": part(),
  };
  const dialog = new EventTarget();
  Object.assign(dialog, {
    open: false,
    returnValue: "",
    parts,
    querySelector: (selector) => parts[selector],
    showModal() { this.open = true; },
    // Matches HTMLDialogElement: close(value) sets returnValue; Escape closes without one.
    close(value) {
      if (value !== undefined) this.returnValue = value;
      this.open = false;
      this.dispatchEvent(new Event("close"));
    },
  });
  return dialog;
}

test("the confirm dialog resolves true only for its confirm button", async () => {
  const dialog = fakeDialog();
  const confirmAction = createConfirmDialog(dialog);
  const accepted = confirmAction({title: "Delete resume?", confirmLabel: "Delete permanently", tone: "danger"});
  assert.equal(dialog.open, true);
  assert.equal(dialog.parts["[data-confirm-cancel]"].focused, true, "focus starts on Cancel");
  assert.equal(dialog.parts["[data-confirm-accept]"].textContent, "Delete permanently");
  assert.equal(dialog.parts["[data-confirm-accept]"].classList.has("danger-button"), true);
  dialog.close("confirm");
  assert.equal(await accepted, true);

  const cancelled = confirmAction({title: "Archive?"});
  assert.equal(dialog.parts["[data-confirm-accept]"].classList.has("danger-button"), false);
  assert.equal(dialog.parts["[data-confirm-accept]"].classList.has("primary-button"), true);
  dialog.close("cancel");
  assert.equal(await cancelled, false);

  const escaped = confirmAction({title: "Archive?"});
  dialog.close();
  assert.equal(await escaped, false, "Escape leaves the reset returnValue and declines");
});

test("a second confirm while one is open is declined without disturbing the first", async () => {
  const dialog = fakeDialog();
  const confirmAction = createConfirmDialog(dialog);
  const first = confirmAction({title: "First"});
  assert.equal(await confirmAction({title: "Second"}), false);
  assert.equal(dialog.parts["[data-confirm-title]"].textContent, "First");
  dialog.close("confirm");
  assert.equal(await first, true);
});

test("confirm text is set as text, never parsed as markup", async () => {
  const dialog = fakeDialog();
  const confirmAction = createConfirmDialog(dialog);
  const pending = confirmAction({title: 'Delete "<img src=x onerror=alert(1)>"?', message: "<b>bold</b>"});
  assert.equal(dialog.parts["[data-confirm-title]"].textContent, 'Delete "<img src=x onerror=alert(1)>"?');
  assert.equal(dialog.parts["[data-confirm-message]"].textContent, "<b>bold</b>");
  dialog.close("cancel");
  await pending;
  assert.equal(await createConfirmDialog(null)({title: "No dialog"}), false);
});

test("tour pages no longer use the native confirm for the reviewed actions", async () => {
  const files = {
    "features/discoveries.js": ["deleteSelectedDiscovery", "flushArchived", "confirmSelectedDiscoveryContent", "promoteSelectedDiscovery"],
    "features/resume_library.js": ["digestResumeLibraryVersion", "setResumeLibraryArchiveState", "deleteResumeLibraryVersion"],
    "features/vault_experiences.js": ["beginNewExperience", "selectVaultExperience", "setVaultVisibility", "toggleVaultExperienceArchive"],
  };
  for (const [file, functions] of Object.entries(files)) {
    const source = await readFile(new URL(`../src/intake_ui_static/${file}`, import.meta.url), "utf8");
    for (const name of functions) {
      const start = source.indexOf(`async function ${name}(`);
      assert.ok(start >= 0, `${file}: ${name} is async`);
      // Top-level functions end at the first closing brace in column 0.
      const body = source.slice(start, source.indexOf("\n}\n", start));
      assert.doesNotMatch(body, /window\.confirm\(/, `${file}: ${name}`);
      assert.match(body, /await confirm(Action|DiscardExperienceChanges)\(/, `${file}: ${name}`);
    }
  }
});

test("the window title is the product name in every locale", async () => {
  const html = await readFile(new URL("../src/intake_ui_static/index.html", import.meta.url), "utf8");
  const locale = await readFile(new URL("../src/intake_ui_static/locale.js", import.meta.url), "utf8");
  assert.match(html, /<title>Career Hub<\/title>/);
  assert.doesNotMatch(locale, /"Career Hub Intake"/);
  // An exact-text "Career Hub" entry would also rewrite the sidebar brand.
  assert.doesNotMatch(locale, /^\s*"Career Hub":/m);
});

test("a confirmed Job Feed delete is never dropped because a list load is in flight", async () => {
  const source = await readFile(new URL("../src/intake_ui_static/features/discoveries.js", import.meta.url), "utf8");
  for (const name of ["flushArchived", "deleteSelectedDiscovery"]) {
    const start = source.indexOf(`async function ${name}(`);
    const body = source.slice(start, source.indexOf("\n}\n", start));
    const afterDialog = body.slice(body.indexOf("await confirmAction("));
    const guard = afterDialog.slice(afterDialog.indexOf("if (!confirmed"), afterDialog.indexOf(") return;") + 1);
    assert.doesNotMatch(guard, /state\.loading/, name);
  }
});

test("an inline stage blocked by loading restores the saved stage and says why", () => {
  const body = slice(appSource, "async function saveInlineApplicationStatus(", "function restoreInlineApplicationStatus(");
  const blocked = body.slice(body.indexOf("if (state.jobApplicationLoading)"), body.indexOf("const previousStatus"));
  assert.match(blocked, /select\.value = select\.dataset\.previousStatus/);
  assert.match(blocked, /still loading/);
});
