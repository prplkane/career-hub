import { escapeHtml, renderLoadingState } from "../ui/primitives.js";

const PAGE_SIZE = 6;
const TARGET_ID = /^fact_[a-f0-9]{64}$/;

// Backend spans count Unicode scalars, not JavaScript UTF-16 code units.
export function highlightedReviewText(text, spans) {
  const chars = Array.from(String(text));
  let end = 0;
  let html = "";
  for (const span of [...spans].sort((a, b) => a.start - b.start)) {
    if (!Number.isInteger(span.start) || !Number.isInteger(span.end) || span.start < end || span.end <= span.start || span.end > chars.length) throw new Error("Invalid review span");
    html += escapeHtml(chars.slice(end, span.start).join(""));
    html += `<mark>${escapeHtml(chars.slice(span.start, span.end).join(""))}</mark>`;
    end = span.end;
  }
  return html + escapeHtml(chars.slice(end).join(""));
}

export function validateReviewView(view) {
  if (!view || !Number.isSafeInteger(view.revision) || view.revision < 0 || !Number.isSafeInteger(view.stale_checks) || view.stale_checks < 0 || !/^[a-f0-9]{64}$/.test(view.review_sha256 || "") || !Array.isArray(view.targets) || view.targets.length > 1000 || !Array.isArray(view.fields) || view.fields.length > 1000 || !["pass", "warning", "block"].includes(view.assessment?.facts?.validation?.status)) throw new Error("Invalid review response");
  const fields = new Map();
  const findings = view.assessment.facts.validation.findings;
  if (!Array.isArray(findings) || findings.length > 1000 || findings.some((f) => !["pass", "warning", "block"].includes(f?.severity) || typeof f.code !== "string" || typeof f.draft_path !== "string")) throw new Error("Invalid review findings");
  for (const field of view.fields) {
    if (typeof field?.path !== "string" || !field.path || fields.has(field.path) || typeof field.text !== "string") throw new Error("Invalid review field");
    fields.set(field.path, field.text);
  }
  const seen = new Set();
  for (const target of view.targets) {
    if (!TARGET_ID.test(target.id) || seen.has(target.id) || !["unreviewed", "confirmed", "revoked"].includes(target.state) || typeof fields.get(target.draft_path) !== "string" || !Array.isArray(target.spans) || !target.spans.length) throw new Error("Invalid review target");
    highlightedReviewText(fields.get(target.draft_path), target.spans);
    seen.add(target.id);
  }
  return view;
}

function fieldLabel(path) {
  return path.replaceAll("_", " ").replace(/\[(\d+)\]/g, (_, n) => ` ${Number(n) + 1}`).replaceAll(".", " / ");
}

export function reviewCorrections(view) {
  return [...new Set((view?.assessment.facts.validation.findings || [])
    .filter((finding) => finding.severity === "block" && finding.code !== "UNSUPPORTED_FACT_TOKEN")
    .map((finding) => finding.draft_path))];
}

export class MaterialReview {
  constructor({ root, api, kind, onChange = () => {} }) {
    this.root = root;
    this.api = api;
    this.kind = kind;
    this.onChange = onChange;
    this.generation = 0;
    this.supported = false;
    this.selected = new Set();
    this.page = 0;
    this.root.addEventListener("change", (event) => {
      const id = event.target.dataset.reviewTarget;
      if (!id || !this.view?.targets.some((t) => t.id === id) || this.busy) return;
      if (event.target.checked) this.selected.add(id); else this.selected.delete(id);
      this.updateActions();
    });
    this.root.addEventListener("click", (event) => {
      const action = event.target.closest("[data-review-action]")?.dataset.reviewAction;
      if (this.busy || this.externalBusy || !action) return;
      if (action === "reload") this.pending = this.load();
      else if (action === "confirm" || action === "revoke") void this.act(action);
      else if (action === "next" || action === "previous") {
        this.page += action === "next" ? 1 : -1;
        this.render();
      }
    });
  }

  setContext({ jobId, editor, dirty = false, busy = false }) {
    const context = { jobId, revision: editor?.revision, persisted: Boolean(editor?.persisted), dirty, ready: Boolean(editor?.validation?.ready_to_export) };
    this.externalBusy = busy;
    const key = JSON.stringify(context);
    if (key === this.key) { this.updateActions(); return; }
    this.key = key;
    this.context = context;
    this.generation++;
    this.view = null;
    this.error = "";
    this.selected.clear();
    this.page = 0;
    this.busy = false;
    this.render();
    this.pending = this.load();
  }

  async load() {
    const generation = ++this.generation;
    const current = this.context;
    if (!current?.jobId) { this.render(); return; }
    this.busy = true;
    this.error = "";
    this.selected.clear();
    this.render();
    try {
      const supported = await this.api.hasMaterialIntegrity();
      if (generation !== this.generation) return;
      this.supported = supported;
      if (!supported || current.dirty || !current.persisted || !current.ready) { this.view = null; return; }
      const view = await this.api.getMaterialReview(current.jobId, this.kind, { signal: AbortSignal.timeout(30_000) });
      if (generation !== this.generation) return;
      this.view = validateReviewView(view);
    } catch {
      if (generation !== this.generation) return;
      this.error = "Review could not be loaded. Reload the review before exporting or confirming facts.";
      this.view = null;
    } finally {
      if (generation === this.generation) { this.busy = false; this.render(); this.onChange(); }
    }
  }

  async act(operation) {
    if (this.busy || this.externalBusy || !this.view || this.context?.dirty) return;
    const ids = this.view.targets.filter((t) => this.selected.has(t.id) && (operation === "confirm" ? t.state !== "confirmed" : t.state === "confirmed")).map((t) => t.id);
    if (!ids.length) return;
    const generation = ++this.generation;
    this.busy = true;
    this.error = "";
    this.render();
    this.onChange();
    try {
      const result = await this.api.updateMaterialReview(this.context.jobId, this.kind, { operation, expectedRevision: this.view.revision, reviewSha256: this.view.review_sha256, targetIds: ids }, { signal: AbortSignal.timeout(30_000) });
      if (generation !== this.generation) return;
      this.view = validateReviewView(result);
      this.selected.clear();
    } catch {
      if (generation !== this.generation) return;
      this.error = "The review change was not confirmed. It may have been saved. Reload the review before another action.";
      this.view = null;
      this.selected.clear();
    } finally {
      if (generation === this.generation) { this.busy = false; this.render(); this.onChange(); }
    }
  }

  async ensureReady() {
    await this.pending;
    if (!this.supported && !this.error) return true; // Existing Python workflow.
    if (!this.busy && this.view && !this.error && this.view.assessment.facts.validation.status !== "block" && !this.context?.dirty) return true;
    const details = this.root.querySelector("details");
    if (details) details.open = true;
    this.root.scrollIntoView({ block: "nearest" });
    this.root.querySelector("summary")?.focus();
    return false;
  }

  get blocksExport() {
    return this.supported && (this.busy || !this.view || Boolean(this.error) || this.view.assessment.facts.validation.status === "block");
  }

  updateActions() {
    const disabled = this.busy || this.externalBusy || !this.view || this.context?.dirty;
    for (const op of ["confirm", "revoke"]) {
      const button = this.root.querySelector(`[data-review-action="${op}"]`);
      if (!button) continue;
      const count = this.view?.targets.filter((t) => this.selected.has(t.id) && (op === "confirm" ? t.state !== "confirmed" : t.state === "confirmed")).length || 0;
      button.disabled = disabled || count === 0;
      button.textContent = `${op === "confirm" ? "Confirm selected facts" : "Revoke selected checks"} (${count})`;
    }
    this.root.querySelectorAll("[data-review-target]").forEach((input) => { input.disabled = disabled; });
  }

  render() {
    this.root.hidden = !this.context?.jobId || !this.supported && !this.error;
    if (this.root.hidden) { this.root.replaceChildren(); return; }
    const open = this.root.querySelector("details")?.open || Boolean(this.error);
    const targets = this.view?.targets || [];
    const corrections = reviewCorrections(this.view);
    const count = targets.filter((t) => t.state !== "confirmed").length;
    const status = this.context.dirty || !this.context.persisted ? "Save to check" : this.busy ? "Checking…" : this.error ? "Reload needed" : !this.context.ready || corrections.length ? "Draft needs changes" : !this.view ? "Not checked" : this.view.assessment.facts.validation.status === "block" ? `${count || "Some"} to review` : "Configured checks passed";
    const fields = new Map((this.view?.fields || []).map((f) => [f.path, f.text]));
    this.page = Math.max(0, Math.min(this.page, Math.ceil(targets.length / PAGE_SIZE) - 1));
    const start = this.page * PAGE_SIZE;
    const rows = targets.slice(start, start + PAGE_SIZE).map((t) => `<li>
      <label><input type="checkbox" data-review-target="${t.id}" ${this.selected.has(t.id) ? "checked" : ""}>
      <span><small>${escapeHtml(fieldLabel(t.draft_path))} · ${t.state === "confirmed" ? "Confirmed by you" : "Needs your review"}</small>
      <span class="material-fact-claim">${highlightedReviewText(fields.get(t.draft_path), t.spans)}</span></span></label>
    </li>`).join("");
    const note = this.context.dirty || !this.context.persisted ? "Save the current draft before checking exact facts. For a cover letter, Export saves it first." : !this.context.ready ? "Resolve the draft readiness items above, then save before factual review." : this.view?.assessment.facts.validation.status === "block" && !targets.length ? "These findings cannot be manually confirmed. Correct the draft and save it again." : "Only configured terms are checked, not every fact. Verify each selected claim against your own records. A confirmation is not new Career Vault evidence.";
    this.root.innerHTML = `<details class="material-fact-review" ${open ? "open" : ""}>
      <summary><strong>Factual review</strong><span aria-live="polite">${escapeHtml(status)}</span></summary>
      <div class="material-fact-body" aria-busy="${this.busy}">
        <p>${escapeHtml(note)}</p>
        ${corrections.length ? `<p>Correct unresolved placeholders or non-confirmable findings in ${escapeHtml(corrections.slice(0, PAGE_SIZE).map(fieldLabel).join(", "))}${corrections.length > PAGE_SIZE ? ` and ${corrections.length - PAGE_SIZE} more fields` : ""}. Edit and save the draft; confirmations cannot waive these checks.</p>` : ""}
        ${this.busy ? renderLoadingState({ label: "Checking the saved draft…", compact: true }) : ""}
        ${this.error ? `<p role="alert">${escapeHtml(this.error)}</p>` : ""}
        ${this.view?.stale_checks ? `<p>${this.view.stale_checks} earlier checks no longer apply to this source revision.</p>` : ""}
        ${rows ? `<ol start="${start + 1}">${rows}</ol>` : ""}
        ${targets.length > PAGE_SIZE ? `<nav aria-label="Review findings pages"><button type="button" data-review-action="previous" ${this.page === 0 || this.busy ? "disabled" : ""}>Previous</button><span>${start + 1}–${Math.min(start + PAGE_SIZE, targets.length)} of ${targets.length}</span><button type="button" data-review-action="next" ${start + PAGE_SIZE >= targets.length || this.busy ? "disabled" : ""}>Next</button></nav>` : ""}
        <div class="material-fact-actions">${rows ? '<button type="button" class="primary-button compact-button" data-review-action="confirm" data-tooltip="Record your confirmation of only the selected exact claims for this saved draft."></button><button type="button" class="secondary-button compact-button" data-review-action="revoke" data-tooltip="Withdraw only the selected confirmations. This does not edit the draft or Career Vault."></button>' : ""}
        <button type="button" class="text-button" data-review-action="reload" data-tooltip="Read the latest saved review. Does not confirm facts or save local edits." ${this.busy || this.externalBusy ? "disabled" : ""}>Reload review</button></div>
      </div></details>`;
    this.updateActions();
  }
}
