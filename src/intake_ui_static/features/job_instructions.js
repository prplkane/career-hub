// Job workspace: application instructions and contact candidates extracted from the posting.
import { elements, state } from "../app_context.js";
import { api } from "../ui/api.js";
import { escapeHtml, formatRequestError, renderLoadingState } from "../ui/primitives.js";
import { openContactCandidateConfirmation } from "./contacts.js";

// Bootstrap hooks supplied by app.js through initJobInstructions().
let loadSelectedJobSource = null;

const JOB_INSTRUCTION_CATEGORY_LABELS = {
  email_destination: "Email destination",
  exact_phrase: "Exact phrase or subject",
  required_artifact: "Required artifact",
  cover_letter_format: "Cover-letter or question format",
  external_step: "External step",
  deadline: "Deadline",
};

export function renderJobInstructions() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const available = Boolean(job?.valid);
  elements.jobInstructionChecklist.classList.toggle("hidden", !available);
  if (!available) return;
  elements.addSelectedJobInstruction.disabled = state.jobInstructionSaving;
  elements.jobInstructionCategory.disabled = state.jobInstructionSaving;
  if (state.jobInstructionsLoading) {
    elements.jobInstructionCount.textContent = "Loading";
    elements.jobInstructionList.innerHTML = renderLoadingState({
      label: "Checking exact posting instructions…",
      compact: true,
    });
    elements.jobInstructionStale.innerHTML = "";
    return;
  }
  if (state.jobInstructionsError) {
    elements.jobInstructionCount.textContent = "Unavailable";
    elements.jobInstructionList.innerHTML = `<div class="empty-state error">${escapeHtml(state.jobInstructionsError)}</div>`;
    elements.jobInstructionStale.innerHTML = "";
    return;
  }
  const checklist = state.jobInstructions;
  if (!checklist || checklist.job_id !== job.id) {
    elements.jobInstructionCount.textContent = "Not loaded";
    elements.jobInstructionList.innerHTML = '<div class="empty-state">Checklist is not loaded.</div>';
    elements.jobInstructionStale.innerHTML = "";
    return;
  }
  const items = Array.isArray(checklist.items) ? checklist.items : [];
  const counts = checklist.counts || {};
  elements.jobInstructionCount.textContent = items.length
    ? `${Number(counts.done || 0)} done · ${items.length} total`
    : "0 flagged";
  elements.jobInstructionList.innerHTML = items.length
    ? items.map((item) => {
      const lineLabel = item.line_start === item.line_end
        ? `Line ${item.line_start}`
        : `Lines ${item.line_start}–${item.line_end}`;
      const staleCopy = item.stale
        ? `<p class="job-instruction-stale-note">The posting changed since this was marked ${escapeHtml((item.previous_state || "pending").replaceAll("_", " "))}. Confirm it again against the current source.</p>`
        : "";
      return `<article class="job-instruction-item${item.stale ? " is-stale" : ""}" data-instruction-state="${escapeHtml(item.state)}">
        <header>
          <div><strong>${escapeHtml(JOB_INSTRUCTION_CATEGORY_LABELS[item.category] || item.category)}</strong><span>${escapeHtml(item.origin === "manual" ? "Added by you" : item.confidence === "explicit" ? "Explicit" : "Review")}</span></div>
          <small>${escapeHtml(lineLabel)} · v${escapeHtml(item.extractor_version)}</small>
        </header>
        <blockquote translate="no">${escapeHtml(item.source_text)}</blockquote>
        ${staleCopy}
        <div class="job-instruction-actions" role="group" aria-label="Checklist state">
          ${[
            ["pending", "Pending"],
            ["done", "Done"],
            ["not_applicable", "Not applicable"],
          ].map(([value, label]) => `<button type="button" class="text-button${item.state === value ? " is-active" : ""}" data-job-instruction-action="${value}" data-job-instruction-fingerprint="${escapeHtml(item.fingerprint)}" aria-pressed="${item.state === value}"${state.jobInstructionSaving ? " disabled" : ""}>${label}</button>`).join("")}
        </div>
      </article>`;
    }).join("")
    : `<div class="job-instruction-empty"><strong>No explicit extra step detected.</strong><span>This is not a guarantee. You can add exact selected text from the posting below.</span></div>`;
  const staleItems = Array.isArray(checklist.stale_items) ? checklist.stale_items : [];
  elements.jobInstructionStale.innerHTML = staleItems.length
    ? `<details><summary>${staleItems.length} stale ${staleItems.length === 1 ? "instruction" : "instructions"} from an earlier posting version</summary><div>${staleItems.map((item) => `<article><strong>${escapeHtml(JOB_INSTRUCTION_CATEGORY_LABELS[item.category] || item.category)}</strong><blockquote translate="no">${escapeHtml(item.source_text)}</blockquote><small>${escapeHtml((item.state || "pending").replaceAll("_", " "))}</small></article>`).join("")}</div></details>`
    : "";
}

const JOB_CONTACT_CONTEXT_LABELS = {
  application_channel: "Application channel",
  application_recipient: "Application recipient",
  recruiter: "Recruiter",
  hiring_manager: "Hiring manager",
  possible_team_contact: "Possible team contact",
};

const JOB_CONTACT_CHANNEL_LABELS = {
  email: "Email",
  phone: "Phone",
  professional_profile: "Professional profile",
};

export function renderJobContactCandidates() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const available = Boolean(job?.valid);
  elements.jobContactCandidates.classList.toggle("hidden", !available);
  if (!available) return;
  const analysis = job?.digest?.contact_candidates;
  const candidates = Array.isArray(analysis?.candidates) ? analysis.candidates : [];
  if (job.analysis_stale || !analysis) {
    elements.jobContactCandidateCount.textContent = "Analysis update available";
    elements.jobContactCandidateList.innerHTML = '<div class="job-instruction-empty"><strong>Refresh the analysis to check the current posting.</strong><span>No contact candidate is inferred from stale or missing analysis.</span></div>';
    return;
  }
  elements.jobContactCandidateCount.textContent = candidates.length
    ? `${candidates.length} flagged`
    : "0 flagged";
  elements.jobContactCandidateList.innerHTML = candidates.length
    ? candidates.map((candidate) => {
      const lineLabel = candidate.line_start === candidate.line_end
        ? `Line ${candidate.line_start}`
        : `Lines ${candidate.line_start}–${candidate.line_end}`;
      const channels = (Array.isArray(candidate.channels) ? candidate.channels : [])
        .map((channel) => `<li><span>${escapeHtml(JOB_CONTACT_CHANNEL_LABELS[channel.kind] || channel.kind)}</span><strong translate="no">${escapeHtml(channel.value || "")}</strong></li>`)
        .join("");
      const facts = [candidate.role, candidate.company].filter(Boolean);
      const title = candidate.name || candidate.channels?.[0]?.value || "Application channel";
      return `<article class="contact-candidate-card">
        <header><div><strong>${escapeHtml(title)}</strong>${facts.length ? `<span>${escapeHtml(facts.join(" · "))}</span>` : ""}</div><span>${escapeHtml(candidate.confidence === "explicit" ? "Explicit" : "Review")}</span></header>
        <p class="contact-candidate-context">${escapeHtml(JOB_CONTACT_CONTEXT_LABELS[candidate.context_kind] || candidate.context_kind)}</p>
        ${channels ? `<ul class="contact-candidate-channels">${channels}</ul>` : '<p class="contact-candidate-no-channel">No direct channel stated</p>'}
        <blockquote translate="no">${escapeHtml(candidate.source_text || "")}</blockquote>
        <footer><small>${escapeHtml(lineLabel)} · extractor v${escapeHtml(candidate.extractor_version)}</small><button class="secondary-button compact-button" type="button" data-save-contact-candidate data-contact-source-kind="job" data-contact-source-id="${escapeHtml(job.id)}" data-contact-candidate-id="${escapeHtml(candidate.candidate_id)}" data-contact-content-sha256="${escapeHtml(candidate.content_sha256)}">Save contact</button></footer>
      </article>`;
    }).join("")
    : '<div class="job-instruction-empty"><strong>No explicit contact candidate detected.</strong><span>This narrow deterministic check does not prove that the posting contains no useful person or channel.</span></div>';
}

export async function loadSelectedJobInstructions() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid) {
    state.jobInstructions = null;
    state.jobInstructionsLoading = false;
    state.jobInstructionsError = "";
    renderJobInstructions();
    return;
  }
  const requestedJobId = job.id;
  state.jobInstructions = null;
  state.jobInstructionsLoading = true;
  state.jobInstructionsError = "";
  renderJobInstructions();
  try {
    const checklist = await api.getApplicationInstructions(requestedJobId);
    if (state.selectedJobId !== requestedJobId) return;
    state.jobInstructions = checklist;
  } catch (error) {
    if (state.selectedJobId !== requestedJobId) return;
    state.jobInstructionsError = formatRequestError(error);
  } finally {
    if (state.selectedJobId === requestedJobId) {
      state.jobInstructionsLoading = false;
      renderJobInstructions();
    }
  }
}

async function saveJobInstructionState(fingerprint, nextState) {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const checklist = state.jobInstructions;
  if (!job?.valid || !checklist || state.jobInstructionSaving) return;
  state.jobInstructionSaving = true;
  elements.jobInstructionMessage.className = "form-message";
  elements.jobInstructionMessage.textContent = "Saving checklist state…";
  renderJobInstructions();
  try {
    state.jobInstructions = await api.setApplicationInstructionState(
      job.id,
      checklist.source_fingerprint,
      fingerprint,
      nextState,
    );
    elements.jobInstructionMessage.className = "form-message success";
    elements.jobInstructionMessage.textContent = "Checklist updated.";
  } catch (error) {
    if (error?.payload?.error_code === "APPLICATION_INSTRUCTION_REVISION_CONFLICT") {
      await loadSelectedJobInstructions();
    }
    elements.jobInstructionMessage.className = "form-message error";
    elements.jobInstructionMessage.textContent = formatRequestError(error);
  } finally {
    state.jobInstructionSaving = false;
    renderJobInstructions();
  }
}

async function addSelectedJobInstruction() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const checklist = state.jobInstructions;
  if (!job?.valid || !checklist || state.jobInstructionSaving) return;
  const sourcePreview = state.jobSourcePreview?.jobId === job.id
    ? state.jobSourcePreview
    : null;
  if (!sourcePreview) {
    elements.jobInstructionMessage.className = "form-message";
    elements.jobInstructionMessage.textContent = "Loading the original posting. Select exact text there, then click Add selected text again.";
    await loadSelectedJobSource();
    return;
  }
  const selection = window.getSelection();
  const range = selection?.rangeCount ? selection.getRangeAt(0) : null;
  if (!range || !elements.jobSourcePreviewText.contains(range.commonAncestorContainer)) {
    elements.jobInstructionMessage.className = "form-message error";
    elements.jobInstructionMessage.textContent = "Select exact text inside Original job description first.";
    return;
  }
  const selectedText = selection.toString().trim();
  if (!selectedText) {
    elements.jobInstructionMessage.className = "form-message error";
    elements.jobInstructionMessage.textContent = "Select exact text from the posting first.";
    return;
  }
  state.jobInstructionSaving = true;
  elements.jobInstructionMessage.className = "form-message";
  elements.jobInstructionMessage.textContent = "Adding exact source text…";
  renderJobInstructions();
  try {
    state.jobInstructions = await api.addManualApplicationInstruction(
      job.id,
      checklist.source_fingerprint,
      elements.jobInstructionCategory.value,
      selectedText,
    );
    selection.removeAllRanges();
    elements.jobInstructionMessage.className = "form-message success";
    elements.jobInstructionMessage.textContent = "Exact posting instruction added.";
  } catch (error) {
    if (error?.payload?.error_code === "APPLICATION_INSTRUCTION_REVISION_CONFLICT") {
      await loadSelectedJobInstructions();
    }
    elements.jobInstructionMessage.className = "form-message error";
    elements.jobInstructionMessage.textContent = formatRequestError(error);
  } finally {
    state.jobInstructionSaving = false;
    renderJobInstructions();
  }
}

export function initJobInstructions(deps = {}) {
  ({ loadSelectedJobSource } = deps);
  elements.jobInstructionList.addEventListener("click", (event) => {
    const button = event.target.closest("[data-job-instruction-action]");
    if (!button) return;
    void saveJobInstructionState(
      button.dataset.jobInstructionFingerprint,
      button.dataset.jobInstructionAction,
    );
  });
  elements.jobContactCandidateList.addEventListener("click", (event) => {
    const button = event.target.closest("[data-save-contact-candidate]");
    if (!button) return;
    void openContactCandidateConfirmation({
      sourceKind: button.dataset.contactSourceKind,
      sourceId: button.dataset.contactSourceId,
      candidateId: button.dataset.contactCandidateId,
      contentSha256: button.dataset.contactContentSha256,
    }, { trigger: button });
  });
  elements.addSelectedJobInstruction.addEventListener("click", () => {
    void addSelectedJobInstruction();
  });
}
