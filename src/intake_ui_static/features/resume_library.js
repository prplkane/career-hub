// Resume intake sources, imports in progress, exact re-import checks and the resume library (application copies).
import {
  elements,
  requestJson,
  state,
  toast,
} from "../app_context.js";
import { api } from "../ui/api.js";
import {
  escapeHtml,
  formatBytes,
  formatRequestError,
  formatTime,
} from "../ui/primitives.js";
import { updateArchiveScopeToggle } from "./vault_shared.js";

// Bootstrap hooks supplied by app.js through initResumeLibrary().
let loadResumeCollections = null;
let openApplicationResumeIntake = null;
let openResumeReview = null;
let prepareReviewForSource = null;
let renderJobApplication = null;
let renderJobWorkspaceMaterials = null;
let uploadDocument = null;

let resumeLibraryManagementLoaded = false;

export function renderSources() {
  const previousSource = elements.sourceSelect.value;
  elements.sourceSelect.innerHTML = state.sources.length
    ? state.sources.map((source) => (
      `<option value="${escapeHtml(source.name)}">${escapeHtml(source.name)} (${formatBytes(source.size_bytes)})</option>`
    )).join("")
    : '<option value="">No TXT or DOCX sources found</option>';
  if (state.sources.some((source) => source.name === previousSource)) {
    elements.sourceSelect.value = previousSource;
  }
  elements.runButton.disabled = state.sources.length === 0;
  elements.loadMoreSourcesButton.classList.toggle("hidden", !state.sourcesNextCursor);
  elements.loadMoreSourcesButton.textContent = `Load more sources (${state.sources.length}/${state.sourcesTotal})`;
  renderResumeImportsInProgress();
}

const MAX_RESUME_IMPORTS_IN_PROGRESS = 5;

function unfinishedResumeImports() {
  return state.extractions.filter((item) => (
    item.valid && item.workflow_status !== "saved_to_vault"
  ));
}

function renderResumeImportsInProgress() {
  const imports = unfinishedResumeImports().slice(0, MAX_RESUME_IMPORTS_IN_PROGRESS);
  elements.resumeImportsInProgress.hidden = imports.length === 0;
  if (!imports.length) return;
  const remaining = Math.max(0, unfinishedResumeImports().length - imports.length);
  elements.resumeImportsInProgressCount.textContent = remaining
    ? `${imports.length} shown / ${remaining} more`
    : `${imports.length} import${imports.length === 1 ? "" : "s"}`;
  elements.resumeImportsInProgressList.innerHTML = imports.map((item) => {
    const workflow = RESUME_WORKFLOW_STATUS[item.workflow_status]
      || RESUME_WORKFLOW_STATUS.extracted;
    const sourceName = item.source_name || item.id;
    return `<article class="resume-import-row">
      <div>
        <strong>${escapeHtml(sourceName)}</strong>
        <span>${escapeHtml(workflow.label)}: ${escapeHtml(resumeReviewSummary(item))}</span>
      </div>
      <button type="button" class="secondary-button compact-button" data-resume-review-id="${escapeHtml(item.id)}">Continue review</button>
    </article>`;
  }).join("");
}

function ensureExactReimportPanel() {
  let panel = document.querySelector("#exact-reimport-panel");
  if (panel) return panel;
  panel = document.createElement("section");
  panel.id = "exact-reimport-panel";
  panel.className = "completion-gate";
  panel.setAttribute("aria-live", "polite");
  panel.hidden = true;
  panel.innerHTML = `
    <div class="completion-heading">
      <div>
        <span class="completion-label">Existing import</span>
        <strong id="exact-reimport-title"></strong>
      </div>
      <div class="form-row">
        <button type="button" class="primary-button compact-button" id="open-previous-import-button">Open previous import</button>
        <button type="button" class="secondary-button compact-button" id="process-again-button">Process again</button>
      </div>
    </div>
    <p class="form-message" id="exact-reimport-message"></p>
    <ul class="plain-list" id="exact-reimport-list"></ul>
  `;
  elements.uploadMessage.insertAdjacentElement("afterend", panel);
  panel.querySelector("#open-previous-import-button").addEventListener(
    "click", openPreviousExactImport,
  );
  panel.querySelector("#process-again-button").addEventListener(
    "click", processExactReimport,
  );
  return panel;
}

function formatPriorImportTimestamp(value) {
  const timestamp = new Date(value);
  return Number.isNaN(timestamp.valueOf())
    ? "Earlier local import"
    : timestamp.toLocaleString();
}

export function renderExactReimportPanel() {
  const panel = ensureExactReimportPanel();
  const check = state.sourceUploadCheck;
  const file = elements.sourceUpload.files[0];
  const currentCheck = check?.file === file ? check : null;
  const priorImports = currentCheck?.prior_imports || [];
  const sameNameConflict = currentCheck?.same_name_conflict === true;
  const title = panel.querySelector("#exact-reimport-title");
  const message = panel.querySelector("#exact-reimport-message");
  const list = panel.querySelector("#exact-reimport-list");
  const openButton = panel.querySelector("#open-previous-import-button");
  const processButton = panel.querySelector("#process-again-button");
  list.replaceChildren();
  openButton.hidden = true;
  processButton.hidden = true;
  if (!file || state.sourceUploadCheckLoading) {
    panel.hidden = true;
    elements.uploadButton.disabled = Boolean(file && state.sourceUploadCheckLoading);
    return;
  }
  if (currentCheck?.error) {
    panel.hidden = false;
    title.textContent = "Could not check this file";
    message.textContent = "Choose the file again before adding it; no local file was created.";
    elements.uploadButton.disabled = true;
    return;
  }
  if (priorImports.length) {
    panel.hidden = false;
    title.textContent = "This exact file was processed before";
    message.textContent = "Open the existing review, or explicitly create a separate new review.";
    priorImports.forEach((item, index) => {
      const status = RESUME_WORKFLOW_STATUS[item.workflow_status]
        || RESUME_WORKFLOW_STATUS.extracted;
      const row = document.createElement("li");
      row.textContent = `${index === 0 ? "Most recent" : "Earlier"} import: ${status.label} — ${formatPriorImportTimestamp(item.processed_at)}`;
      list.append(row);
    });
    openButton.hidden = false;
    processButton.hidden = false;
    elements.uploadButton.disabled = true;
    return;
  }
  if (sameNameConflict) {
    panel.hidden = false;
    title.textContent = "A different local file already uses this name";
    message.textContent = "It will not be replaced. Rename this file, choose it again, then add it as a new import.";
    elements.uploadButton.disabled = true;
    return;
  }
  panel.hidden = true;
  elements.uploadButton.disabled = false;
}

export function clearSourceUploadCheck() {
  state.sourceUploadCheck = null;
  state.sourceUploadCheckLoading = false;
  renderExactReimportPanel();
}

export async function inspectUploadedDocument() {
  const file = elements.sourceUpload.files[0];
  state.sourceUploadCheck = null;
  if (!file) {
    renderExactReimportPanel();
    return;
  }
  state.sourceUploadCheckLoading = true;
  renderExactReimportPanel();
  elements.uploadMessage.className = "form-message upload-message";
  elements.uploadMessage.textContent = `Checking ${file.name} against prior imports...`;
  try {
    const result = await requestJson("/api/upload/check", {
      method: "POST",
      headers: {
        "Content-Type": "application/octet-stream",
        "X-Source-Name": encodeURIComponent(file.name),
      },
      body: file,
    });
    if (elements.sourceUpload.files[0] !== file) return;
    state.sourceUploadCheck = { file, ...result };
    elements.uploadMessage.textContent = result.prior_imports?.length
      ? "An exact prior import was found. Choose how to continue."
      : "Ready to add locally.";
  } catch (error) {
    if (elements.sourceUpload.files[0] !== file) return;
    state.sourceUploadCheck = { file, error: formatRequestError(error) };
    elements.uploadMessage.className = "form-message upload-message error";
    elements.uploadMessage.textContent = formatRequestError(error);
  } finally {
    if (elements.sourceUpload.files[0] === file) {
      state.sourceUploadCheckLoading = false;
      renderExactReimportPanel();
    }
  }
}

async function openPreviousExactImport() {
  const file = elements.sourceUpload.files[0];
  const priorImport = state.sourceUploadCheck?.file === file
    ? state.sourceUploadCheck.prior_imports?.[0]
    : null;
  if (!priorImport?.source_id) return;
  elements.sourceUpload.value = "";
  clearSourceUploadCheck();
  await openResumeReview(priorImport.source_id);
}

async function processExactReimport() {
  const file = elements.sourceUpload.files[0];
  const priorImports = state.sourceUploadCheck?.file === file
    ? state.sourceUploadCheck.prior_imports
    : [];
  if (!priorImports?.length) return;
  await uploadDocument({ processAgain: true });
}

const RESUME_WORKFLOW_STATUS = {
  extracted: { label: "Needs review", className: "status-warning" },
  in_review: { label: "In review", className: "status-warning" },
  vault_decision: { label: "Vault decision", className: "status-warning" },
  ready_to_save: { label: "Ready to save", className: "status-success" },
  saved_to_vault: { label: "Saved to Vault", className: "status-success" },
  review_error: { label: "Review error", className: "status-error" },
};

function resumeReviewSummary(item) {
  if (item.workflow_status === "saved_to_vault") {
    return `${item.review_entity_count || 0} saved decisions`;
  }
  if (item.workflow_status === "extracted") return "Review not started";
  if (item.workflow_status === "review_error") return "Needs inspection";
  const cards = `${item.review_entity_count || 0} card${item.review_entity_count === 1 ? "" : "s"}`;
  const unresolved = Number(item.unresolved_block_count || 0);
  if (unresolved) return `${cards} / ${unresolved} unresolved`;
  const decisions = Number(item.approval_issue_count || 0);
  return decisions ? `${cards} / ${decisions} decisions` : cards;
}

export function renderExtractions() {
  renderResumeImportsInProgress();
}

function resumeLibraryRecordsForVisibility() {
  const archived = state.resumeLibraryVisibility === "archived";
  return state.resumeLibraryManagement.filter((resume) => (resume.archived === true) === archived);
}

function resumeUsageLabel(usage = {}) {
  const references = [
    Number(usage.current_application_selections || 0),
    Number(usage.applied_resume_snapshots || 0),
    Number(usage.submitted_resume_events || 0),
  ].reduce((total, value) => total + value, 0);
  if (!references) return "Not used in an application yet";
  return `${references} application reference${references === 1 ? "" : "s"}`;
}

function filteredResumeLibraryRecords() {
  const query = elements.resumeLibrarySearch.value.trim().toLocaleLowerCase();
  const digestFilter = state.resumeLibraryDigestFilter;
  const records = resumeLibraryRecordsForVisibility().filter((resume) => (
    (!query || String(resume.display_name || "").toLocaleLowerCase().includes(query))
    && (digestFilter === "all" || resume.digest_status === digestFilter)
  ));
  return records.sort((left, right) => {
    if (state.resumeLibrarySort === "oldest") return String(left.added_at).localeCompare(String(right.added_at));
    if (state.resumeLibrarySort === "name_asc") return left.display_name.localeCompare(right.display_name);
    if (state.resumeLibrarySort === "name_desc") return right.display_name.localeCompare(left.display_name);
    if (state.resumeLibrarySort === "digest") return String(left.digest_status).localeCompare(String(right.digest_status)) || left.display_name.localeCompare(right.display_name);
    return String(right.added_at).localeCompare(String(left.added_at));
  });
}

const RESUME_DIGEST_STATES = {
  not_digested: { label: "Not reviewed", className: "is-neutral" },
  in_progress: { label: "Review in progress", className: "is-progress" },
  digested: { label: "Reviewed", className: "is-complete" },
  outdated: { label: "Update available", className: "is-outdated" },
  error: { label: "Needs attention", className: "is-error" },
};

function resumeLibraryEmptyState(recordsInVisibility) {
  const query = elements.resumeLibrarySearch.value.trim();
  if (query) {
    return `<div class="empty-state">No ${state.resumeLibraryVisibility} Resume Versions match "${escapeHtml(query)}".</div>`;
  }
  if (state.resumeLibraryVisibility === "archived") {
    return '<div class="empty-state">No archived Resume Versions. Archived materials remain available in application history.</div>';
  }
  if (recordsInVisibility) {
    return '<div class="empty-state">No active Resume Versions match this view.</div>';
  }
  return `
    <div class="empty-state resume-library-empty-state">
      <strong>No active Resume Versions yet.</strong>
      <p>Add a resume, then choose <strong>Save for applications</strong> to keep it ready for your next application.</p>
      <button type="button" class="primary-button" data-add-application-resume>Add resume</button>
    </div>`;
}

function renderResumeLibrary() {
  const unavailable = state.resumeLibraryManagementError;
  const recordsInVisibility = resumeLibraryRecordsForVisibility();
  const records = filteredResumeLibraryRecords();
  const activeCount = state.resumeLibraryManagement.filter((resume) => resume.archived !== true).length;
  const archivedCount = state.resumeLibraryManagement.length - activeCount;

  elements.resumeLibraryPanel.classList.toggle(
    "is-empty",
    !unavailable && state.resumeLibraryManagement.length === 0,
  );

  elements.resumeLibraryCount.textContent = unavailable
    ? "Resume list unavailable"
    : `${activeCount} active version${activeCount === 1 ? "" : "s"}`;
  elements.resumeLibraryArchivedCount.textContent = unavailable ? "–" : archivedCount;
  elements.resumeLibraryArchivedFilter.disabled = unavailable;
  updateArchiveScopeToggle(
    elements.resumeLibraryArchivedFilter,
    state.resumeLibraryVisibility === "archived",
    "Resume Versions",
  );

  if (unavailable) {
    elements.resumeLibraryRecordList.innerHTML = "";
    return;
  }

  elements.resumeLibraryRecordList.innerHTML = records.length
    ? records.map((resume) => {
      const archived = resume.archived === true;
      const fileType = String(resume.file_type || "File").toUpperCase();
      const availability = archived
        ? "Archived - retained for existing history"
        : "Available for new applications";
      const mutating = state.resumeLibraryMutationId === resume.id;
      const mutationAction = mutating ? state.resumeLibraryMutationAction : null;
      const action = archived ? "restore" : "archive";
      const actionLabel = mutating && mutationAction === action
        ? (archived ? "Restoring..." : "Archiving...")
        : (archived ? "Restore" : "Archive");
      const actionTooltip = archived
        ? "Restore this Resume Version so it appears in new application pickers again."
        : "Hide this Resume Version from new application pickers. Existing application history remains intact.";
      const digestState = RESUME_DIGEST_STATES[resume.digest_status] || RESUME_DIGEST_STATES.not_digested;
      const digestRouteAvailable = api.capabilities.resume_digest !== false;
      const canDigest = digestRouteAvailable && !archived && resume.digest_supported === true;
      const continueReview = canDigest && resume.digest_status === "in_progress" && resume.digest_source_id;
      const digestAction = continueReview ? "continue-digest" : "digest";
      const digestLabel = !digestRouteAvailable
        ? "Review unavailable"
        : continueReview
        ? "Continue review"
        : resume.digest_status === "not_digested" ? "Review" : "Review again";
      const digestTooltip = !digestRouteAvailable
        ? "Resume review is not yet connected in this preview. The saved version remains available for applications."
        : !resume.digest_supported
        ? "Local review currently supports TXT and DOCX resume files."
        : archived
          ? "Restore this resume before reviewing it."
          : continueReview
            ? "Continue the existing local review without analyzing the file again."
            : "Create a new local deterministic review without changing prior reviews or Career Vault.";
      const deleteButton = resume.deletable === true
        ? `<button type="button" class="secondary-button compact-button"
            data-resume-library-action="delete"
            data-resume-library-id="${escapeHtml(resume.id)}"
            data-tooltip="Permanently delete this unused Resume Version and its stored file. This cannot be undone."
            ${mutating ? "disabled" : ""}>${mutationAction === "delete" ? "Deleting..." : "Delete"}</button>`
        : "";
      const downloadLabel = mutating && mutationAction === "download"
        ? "Downloading..."
        : "Download";
      return `
        <article class="resume-library-record${state.resumeLibraryFocusId === resume.id ? " is-route-target" : ""}"
          data-resume-library-record-id="${escapeHtml(resume.id)}" tabindex="-1">
          <div class="resume-library-record-heading">
            <strong translate="no">${escapeHtml(resume.display_name)}</strong>
            <span class="resume-library-state ${archived ? "is-archived" : "is-active"}">${archived ? "Archived" : "Active"}</span>
          </div>
          <div class="resume-library-record-meta">
            <span>Added ${escapeHtml(formatTime(resume.added_at))}</span>
            <span>${escapeHtml(fileType)} / ${escapeHtml(formatBytes(Number(resume.size_bytes)))}</span>
            <span class="resume-digest-state ${digestState.className}">${escapeHtml(digestState.label)}</span>
          </div>
          <div class="resume-library-record-footer">
            <div class="resume-library-record-summary">
              <span>${escapeHtml(resumeUsageLabel(resume.usage))}</span>
              <span>${availability}</span>
            </div>
            <div class="resume-library-record-actions">
              <button type="button" class="secondary-button compact-button"
                data-resume-library-action="download"
                data-resume-library-id="${escapeHtml(resume.id)}"
                data-tooltip="Download this exact immutable Resume Version for the current application."
                ${mutating ? "disabled" : ""}>${downloadLabel}</button>
              <button type="button" class="secondary-button compact-button"
                data-resume-library-action="${digestAction}"
                data-resume-library-id="${escapeHtml(resume.id)}"
                data-resume-digest-source-id="${escapeHtml(resume.digest_source_id || "")}"
                data-tooltip="${digestTooltip}"
                ${!canDigest || mutating ? "disabled" : ""}>${mutationAction === "digest" ? "Reviewing..." : digestLabel}</button>
              <button type="button" class="secondary-button compact-button"
                data-resume-library-action="${action}"
                data-resume-library-id="${escapeHtml(resume.id)}"
                data-tooltip="${actionTooltip}"
                ${mutating ? "disabled" : ""}>${actionLabel}</button>
              ${deleteButton}
            </div>
          </div>
        </article>`;
    }).join("")
    : resumeLibraryEmptyState(recordsInVisibility.length);
  focusResumeLibraryRouteTarget();
}

function focusResumeLibraryRouteTarget() {
  const resumeId = state.resumeLibraryFocusId;
  if (!resumeId || state.activeView !== "resume-library") return;
  const target = Array.from(
    elements.resumeLibraryRecordList.querySelectorAll("[data-resume-library-record-id]")
  ).find((record) => record.dataset.resumeLibraryRecordId === resumeId);
  if (!target) {
    if (resumeLibraryManagementLoaded) {
      state.resumeLibraryFocusId = null;
      elements.resumeLibraryMessage.className = "form-message error";
      elements.resumeLibraryMessage.textContent = "That Resume Version is no longer available in the library.";
    }
    return;
  }
  state.resumeLibraryFocusId = null;
  window.requestAnimationFrame(() => {
    target.scrollIntoView({ behavior: "smooth", block: "center" });
    target.focus({ preventScroll: true });
  });
}

function setResumeLibraryVisibility(visibility) {
  state.resumeLibraryVisibility = visibility;
  renderResumeLibrary();
}

async function digestResumeLibraryVersion(resumeId) {
  const resume = state.resumeLibraryManagement.find((item) => item.id === resumeId);
  if (!resume || resume.archived || !resume.digest_supported
    || api.capabilities.resume_digest === false || state.resumeLibraryMutationId) return;
  if (resume.digest_status !== "not_digested" && !window.confirm(
    `Digest "${resume.display_name}" again? A new review will be created. Existing reviews and Career Vault records will not be overwritten.`
  )) return;
  state.resumeLibraryMutationId = resumeId;
  state.resumeLibraryMutationAction = "digest";
  elements.resumeLibraryMessage.className = "form-message";
  elements.resumeLibraryMessage.textContent = `Digesting ${resume.display_name} locally...`;
  renderResumeLibrary();
  try {
    const result = await requestJson(`/api/resumes/${encodeURIComponent(resumeId)}/digest`, {
      method: "POST",
    });
    await prepareReviewForSource(result.source_id);
    resumeLibraryManagementLoaded = false;
    await loadResumeCollections({ force: true });
    await openResumeReview(result.source_id);
  } catch (error) {
    elements.resumeLibraryMessage.className = "form-message error";
    elements.resumeLibraryMessage.textContent = formatRequestError(error);
  } finally {
    state.resumeLibraryMutationId = null;
    state.resumeLibraryMutationAction = null;
    renderResumeLibrary();
  }
}

async function downloadResumeLibraryVersion(resumeId) {
  const resume = state.resumeLibraryManagement.find((item) => item.id === resumeId);
  if (!resume || state.resumeLibraryMutationId) return;
  state.resumeLibraryMutationId = resumeId;
  state.resumeLibraryMutationAction = "download";
  elements.resumeLibraryMessage.className = "form-message";
  elements.resumeLibraryMessage.textContent = `Preparing ${resume.display_name}...`;
  renderResumeLibrary();
  try {
    const { blob, filename } = await api.downloadResumeVersion(resumeId);
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = filename;
    document.body.append(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(url);
    elements.resumeLibraryMessage.textContent = `${filename} downloaded.`;
  } catch (error) {
    elements.resumeLibraryMessage.className = "form-message error";
    elements.resumeLibraryMessage.textContent = formatRequestError(error);
  } finally {
    state.resumeLibraryMutationId = null;
    state.resumeLibraryMutationAction = null;
    renderResumeLibrary();
  }
}

export async function loadResumeLibrary() {
  let loaded = false;
  try {
    const result = await api.listResumeVersions();
    state.resumeLibrary = Array.isArray(result.resumes) ? result.resumes : [];
    loaded = true;
  } catch (error) {
    state.resumeLibrary = [];
    state.applicationResumeMessageJobId = state.selectedJobId;
    elements.applicationResumeMessage.className = "form-message application-resume-message error";
    elements.applicationResumeMessage.textContent = formatRequestError(error);
  } finally {
    renderJobWorkspaceMaterials();
    renderJobApplication();
  }
  return loaded;
}

export function invalidateResumeLibraryManagement() {
  resumeLibraryManagementLoaded = false;
}

export async function loadResumeLibraryManagement({ force = false } = {}) {
  let loaded = true;
  if (!resumeLibraryManagementLoaded || force) {
    state.resumeLibraryManagementError = false;
    elements.resumeLibraryMessage.className = "form-message";
    elements.resumeLibraryMessage.textContent = "";
    try {
      const result = await api.listResumeVersions({ includeArchived: true });
      state.resumeLibraryManagement = Array.isArray(result.resumes) ? result.resumes : [];
      resumeLibraryManagementLoaded = true;
    } catch (error) {
      loaded = false;
      resumeLibraryManagementLoaded = false;
      state.resumeLibraryManagementError = true;
      state.resumeLibraryManagement = [];
      elements.resumeLibraryMessage.className = "form-message error";
      elements.resumeLibraryMessage.textContent = formatRequestError(error);
    }
  }
  renderResumeLibrary();
  return loaded;
}

async function setResumeLibraryArchiveState(resumeId, archived) {
  const resume = state.resumeLibraryManagement.find((item) => item.id === resumeId);
  if (!resume || state.resumeLibraryMutationId === resumeId) return;
  const action = archived ? "archive" : "restore";
  if (archived) {
    const confirmed = window.confirm(
      `Archive ${resume.display_name}? It will be hidden from new application pickers. Existing application history will remain available.`
    );
    if (!confirmed) return;
  }

  state.resumeLibraryMutationId = resumeId;
  state.resumeLibraryMutationAction = action;
  elements.resumeLibraryMessage.className = "form-message";
  elements.resumeLibraryMessage.textContent = archived
    ? `Archiving ${resume.display_name}...`
    : `Restoring ${resume.display_name}...`;
  renderResumeLibrary();
  try {
    const result = await requestJson(
      `/api/resumes/${encodeURIComponent(resumeId)}/${action}`,
      { method: "POST" }
    );
    if (result.archived !== archived) {
      throw new Error(`Resume archive state did not update to ${archived}.`);
    }
    const [pickerLoaded, managementLoaded] = await Promise.all([
      loadResumeLibrary(),
      loadResumeLibraryManagement({ force: true }),
    ]);
    if (!pickerLoaded || !managementLoaded) {
      throw new Error(
        "Archive state changed, but the Resume Library could not be refreshed. Reload before continuing."
      );
    }
    elements.resumeLibraryMessage.className = "form-message success";
    elements.resumeLibraryMessage.textContent = archived
      ? `${resume.display_name} archived. It is hidden from new application pickers and remains in existing application history.`
      : `${resume.display_name} restored. It is available in the active library and new application pickers.`;
    toast.show({
      message: archived
        ? `Resume archived: ${resume.display_name}.`
        : `Resume restored: ${resume.display_name}.`,
    });
  } catch (error) {
    elements.resumeLibraryMessage.className = "form-message error";
    elements.resumeLibraryMessage.textContent = formatRequestError(error);
  } finally {
    state.resumeLibraryMutationId = null;
    state.resumeLibraryMutationAction = null;
    renderResumeLibrary();
  }
}

async function deleteResumeLibraryVersion(resumeId) {
  const resume = state.resumeLibraryManagement.find((item) => item.id === resumeId);
  if (
    !resume
    || resume.deletable !== true
    || state.resumeLibraryMutationId === resumeId
  ) return;
  const confirmed = window.confirm(
    `Permanently delete "${resume.display_name}"? This permanently removes the stored Resume Version file. This action cannot be undone. Career Vault data is not affected.`
  );
  if (!confirmed) return;

  state.resumeLibraryMutationId = resumeId;
  state.resumeLibraryMutationAction = "delete";
  elements.resumeLibraryMessage.className = "form-message";
  elements.resumeLibraryMessage.textContent = `Deleting ${resume.display_name}...`;
  renderResumeLibrary();
  try {
    const result = await requestJson(
      `/api/resumes/${encodeURIComponent(resumeId)}`,
      { method: "DELETE" }
    );
    if (result.deleted !== true || result.id !== resumeId) {
      throw new Error("Resume Version deletion was not confirmed by the server.");
    }
    const [pickerLoaded, managementLoaded] = await Promise.all([
      loadResumeLibrary(),
      loadResumeLibraryManagement({ force: true }),
    ]);
    if (!pickerLoaded || !managementLoaded) {
      throw new Error(
        "Resume Version was deleted, but the Resume Library could not be refreshed. Reload before continuing."
      );
    }
    elements.resumeLibraryMessage.className = "form-message success";
    elements.resumeLibraryMessage.textContent = `${resume.display_name} was permanently deleted. Career Vault data was not changed.`;
    toast.show({ message: `Resume deleted: ${resume.display_name}.` });
  } catch (error) {
    elements.resumeLibraryMessage.className = "form-message error";
    elements.resumeLibraryMessage.textContent = formatRequestError(error);
  } finally {
    state.resumeLibraryMutationId = null;
    state.resumeLibraryMutationAction = null;
    renderResumeLibrary();
  }
}

export function initResumeLibrary(deps = {}) {
  ({ loadResumeCollections, openApplicationResumeIntake, openResumeReview, prepareReviewForSource, renderJobApplication, renderJobWorkspaceMaterials, uploadDocument } = deps);
  elements.resumeLibrarySearch.addEventListener("input", renderResumeLibrary);
  elements.resumeLibraryDigestFilter.addEventListener("change", () => {
    state.resumeLibraryDigestFilter = elements.resumeLibraryDigestFilter.value;
    renderResumeLibrary();
  });
  elements.resumeLibrarySort.addEventListener("change", () => {
    state.resumeLibrarySort = elements.resumeLibrarySort.value;
    renderResumeLibrary();
  });
  elements.resumeLibraryArchivedFilter.addEventListener("click", () => setResumeLibraryVisibility(
    state.resumeLibraryVisibility === "archived" ? "active" : "archived"
  ));
  elements.resumeLibraryAddResumeButton.addEventListener("click", openApplicationResumeIntake);
  elements.resumeLibraryRecordList.addEventListener("click", async (event) => {
    const actionButton = event.target.closest("[data-resume-library-action]");
    if (actionButton) {
      const action = actionButton.dataset.resumeLibraryAction;
      if (action === "download") {
        await downloadResumeLibraryVersion(actionButton.dataset.resumeLibraryId);
      } else if (action === "delete") {
        await deleteResumeLibraryVersion(actionButton.dataset.resumeLibraryId);
      } else if (action === "digest") {
        await digestResumeLibraryVersion(actionButton.dataset.resumeLibraryId);
      } else if (action === "continue-digest") {
        await openResumeReview(actionButton.dataset.resumeDigestSourceId);
      } else {
        await setResumeLibraryArchiveState(
          actionButton.dataset.resumeLibraryId,
          action === "archive"
        );
      }
      return;
    }
    if (event.target.closest("[data-add-application-resume]")) openApplicationResumeIntake();
  });
}
