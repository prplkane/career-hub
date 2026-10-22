import { api } from "../ui/api.js";
import {
  connectivityCopy,
  isOffline,
  onConnectivityChange,
  reportInternetActionFailure,
  reportInternetActionSuccess,
} from "../ui/connectivity.js";
import {
  escapeHtml,
  formatRequestError,
  renderLoadingState,
} from "../ui/primitives.js";
import { reportEmailConnectionClientFailure } from "../ui/client_diagnostics.js";
import { openContactCandidateConfirmation } from "./contacts.js";

const MODE_KEY = "careerHub.jobFeedMode";
const MODES = new Set(["feed", "email", "watchers"]);
const MAX_FILES = 10;
const MAX_FILE_BYTES = 10 * 1024 * 1024;
const MAX_TOTAL_BYTES = 25 * 1024 * 1024;
const EMAIL_CONNECTION_SAVE_TIMEOUT_MS = 15_000;
const EMAIL_DIAGNOSTIC_RUN_HEADER = "X-Career-Hub-Diagnostic-Run";

const PAGE_SIZE_KEY = "careerHub.jobFeedPageSize";
const DEFAULT_PAGE_SIZE = 20;

function readStoredPageSize() {
  try {
    const val = Number(window.localStorage.getItem(PAGE_SIZE_KEY));
    return [10, 20, 50].includes(val) ? val : DEFAULT_PAGE_SIZE;
  } catch {
    return DEFAULT_PAGE_SIZE;
  }
}

function createEmailDiagnosticRunId() {
  const timestamp = new Date().toISOString()
    .replace(/[-:]/g, "")
    .replace(/\.(\d{3})Z$/, ".$1000Z");
  const entropy = new Uint8Array(4);
  window.crypto.getRandomValues(entropy);
  const suffix = Array.from(entropy, (value) => value.toString(16).padStart(2, "0")).join("");
  return `${timestamp}_${suffix}`;
}

const state = {
  items: [],
  total: 0,
  currentPage: 1,
  pageSize: readStoredPageSize(),
  nextCursor: null,
  mobileDetailOpen: false,
  searchTimer: null,
  archivedTotal: 0,
  selectedId: null,
  detail: null,
  visibility: "active",
  mode: "feed",
  pendingFiles: [],
  loading: false,
  importing: false,
  promotingId: null,
  verifyingId: null,
  changingId: null,
  routeTargetId: null,
  routeIntent: null,
  fieldEditing: false,
  fieldSaving: false,
  fieldDraft: null,
  fieldReset: new Set(),
  fieldNotice: "",
  fieldNoticeTone: "",
  loadEpoch: 0,
  emailConnection: null,
  emailLoading: false,
  emailSyncing: false,
  emailSyncAbortController: null,
  emailSyncStopRequested: false,
  emailSaving: false,
  emailFormDirty: false,
  senderDraft: { job_alerts: [], application_tracking: [] },
  emailSenderOperation: null,
  emailSourceFocus: "",
  emailSyncTimer: null,
  emailSchedulerKey: "",
  emailSchedulerRelease: null,
};
const el = {};

function formatDate(value) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return new Intl.DateTimeFormat(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  }).format(date);
}

function formatDateTime(value) {
  if (!value) return "Never";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "Never";
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  }).format(date);
}

function lifecycleLabel(value) {
  return {
    inbox: "Inbox",
    shortlisted: "Shortlisted",
    dismissed: "Dismissed",
    promoted: "Saved job",
  }[value] || value || "Unknown";
}

function originLabels(item) {
  const sources = new Set(item?.source_kinds || []);
  const labels = [];
  if (sources.has("email_file")) labels.push("Email");
  if (sources.has("watcher")) labels.push("Watcher");
  if (sources.has("manual") || sources.has("extension")) labels.push("Captured");
  if (sources.has("public_resolver")) labels.push("Resolved link");
  return labels.length ? labels : ["Unknown"];
}

function originLabel(item) {
  return originLabels(item)[0];
}

function providerLabels(item) {
  const providers = Array.isArray(item?.source_providers) ? item.source_providers : [];
  const labels = {
    linkedin: "LinkedIn",
    indeed: "Indeed",
    handshake: "Handshake",
    meeboss: "MeeBoss",
    "12twenty": "12twenty",
  };
  return providers.map((provider) => labels[provider] || provider).filter(Boolean);
}

function providerLabel(item) {
  return providerLabels(item)[0] || "";
}

function localizedUiText(value) {
  return window.CareerHubLocale?.translate?.(value) || value;
}

function sourceBadgeHtml(item) {
  const labels = [...new Set([...providerLabels(item), ...originLabels(item)])];
  return `<span class="discovery-source-stack">${labels.map((label) => {
    const token = label.toLocaleLowerCase().replaceAll(" ", "-");
    return `<span class="discovery-source-badge discovery-source-${escapeHtml(token)}">${escapeHtml(label)}</span>`;
  }).join("")}</span>`;
}

function displayTitle(item) {
  return item?.title || "Untitled opportunity";
}

function displayCompany(item) {
  return item?.company || "Company not stated";
}

function captureScopeLabel(detail) {
  const originalScope = detail?.content_verification?.from_scope;
  if (detail?.content_scope === "confirmed_full") {
    if (originalScope === "page_visible") return "Verified visible page";
    if (originalScope === "selection") return "Verified selected text";
    return "Confirmed complete JD";
  }
  return {
    link_only: "Link only",
    selection: "Selected text · unverified",
    page_visible: "Visible page · unverified",
  }[detail?.content_scope] || "Unknown";
}

function renderDiscoveryAssessment(detail) {
  const context = detail?.review_assessment;
  const receipt = context?.receipt;
  if (!receipt) return "";
  const findings = [];
  for (const blocker of receipt.blockers || []) {
    findings.push(`<li class="discovery-assessment-finding">
      <strong>${escapeHtml(blocker.message)}</strong>
      <span>JD line ${escapeHtml(blocker.source_evidence?.line)}: “${escapeHtml(blocker.source_evidence?.text || "")}"</span>
      <span>Career Profile: ${escapeHtml(blocker.candidate_fact || "")}</span>
    </li>`);
  }
  for (const item of receipt.review_items || []) {
    const reviewLabel = item.kind === "unknown_vocabulary"
      ? "Skill term to review"
      : item.kind === "policy_review"
        ? "Policy review"
        : item.kind === "evidence_not_found"
          ? "Required evidence gap"
          : item.kind === "preferred_evidence_not_found"
            ? "Preferred evidence gap"
            : "Review item";
    findings.push(`<li class="discovery-assessment-finding">
      <strong>${escapeHtml(reviewLabel)}</strong>
      <span>${item.line ? `JD line ${escapeHtml(item.line)}: ` : ""}${escapeHtml(item.text || "")}</span>
      ${item.candidate_fact ? `<span>Career Profile: ${escapeHtml(item.candidate_fact)}</span>` : ""}
    </li>`);
  }
  const count = receipt.status === "clear_conflict"
    ? `${receipt.blocker_count} clear ${receipt.blocker_count === 1 ? "conflict" : "conflicts"}`
    : receipt.review_count
      ? `${receipt.review_count} ${receipt.review_count === 1 ? "item" : "items"}`
      : "";
  const note = receipt.status === "unavailable"
    ? "This capture is partial or unverified. No missing requirements were inferred."
    : receipt.unknown_vocabulary_count
      ? `${receipt.unknown_vocabulary_count} source ${receipt.unknown_vocabulary_count === 1 ? "phrase needs" : "phrases need"} review. No vocabulary was added.`
      : "Advisory only. This review never blocks saving or records an application.";
  const contextLine = context.computed_at
    ? `Current Career Profile and Skills Registry · ${formatDateTime(context.computed_at)}`
    : "Current Career Profile context is unavailable for this capture.";
  return `<section class="discovery-assessment-panel" data-assessment-status="${escapeHtml(receipt.status)}" aria-labelledby="discovery-assessment-heading">
    <div class="discovery-assessment-heading">
      <div><span class="section-kicker">Assessment context</span><h3 id="discovery-assessment-heading">${escapeHtml(receipt.label)}</h3></div>
      ${count ? `<span>${escapeHtml(count)}</span>` : ""}
    </div>
    <p class="discovery-assessment-context">${escapeHtml(contextLine)}</p>
    ${findings.length ? `<ul class="discovery-assessment-findings">${findings.join("")}</ul>` : ""}
    <p class="discovery-assessment-note">${escapeHtml(note)}</p>
  </section>`;
}

const APPLICATION_INSTRUCTION_CATEGORY_LABELS = {
  email_destination: "Email destination",
  exact_phrase: "Exact phrase or subject",
  required_artifact: "Required artifact",
  cover_letter_format: "Cover-letter or question format",
  external_step: "External step",
  deadline: "Deadline",
};

function renderDiscoveryInstructions(detail) {
  const instructions = detail?.review_assessment?.application_instructions;
  if (!instructions) return "";
  if (instructions.eligible !== true) {
    return `<section class="discovery-instruction-panel is-unavailable" aria-labelledby="discovery-instruction-heading">
      <div class="discovery-instruction-heading">
        <div><span class="section-kicker">Application checklist</span><h3 id="discovery-instruction-heading">Full JD required</h3></div>
      </div>
      <p>A partial or link-only capture cannot prove that extra application steps are absent.</p>
    </section>`;
  }
  const items = Array.isArray(instructions.items) ? instructions.items : [];
  const rows = items.map((item) => {
    const lineLabel = item.line_start === item.line_end
      ? `Line ${item.line_start}`
      : `Lines ${item.line_start}–${item.line_end}`;
    return `<li class="discovery-instruction-item">
      <div><strong>${escapeHtml(APPLICATION_INSTRUCTION_CATEGORY_LABELS[item.category] || item.category)}</strong><span>${escapeHtml(item.confidence === "explicit" ? "Explicit" : "Review")}</span></div>
      <blockquote translate="no">${escapeHtml(item.source_text || "")}</blockquote>
      <small>${escapeHtml(lineLabel)} · extractor v${escapeHtml(item.extractor_version)}</small>
    </li>`;
  });
  return `<section class="discovery-instruction-panel" aria-labelledby="discovery-instruction-heading">
    <div class="discovery-instruction-heading">
      <div><span class="section-kicker">Application checklist</span><h3 id="discovery-instruction-heading">Extra steps</h3></div>
      ${items.length ? `<span>${items.length} flagged</span>` : ""}
    </div>
    ${rows.length
      ? `<ul class="discovery-instruction-list">${rows.join("")}</ul>`
      : '<p>No explicit extra step detected. This is not a guarantee.</p>'}
    <p class="discovery-instruction-note">Exact captured wording only. This checklist never blocks saving or continuing to the posting.</p>
  </section>`;
}

const CONTACT_CONTEXT_LABELS = {
  application_channel: "Application channel",
  application_recipient: "Application recipient",
  recruiter: "Recruiter",
  hiring_manager: "Hiring manager",
  possible_team_contact: "Possible team contact",
};

const CONTACT_CHANNEL_LABELS = {
  email: "Email",
  phone: "Phone",
  professional_profile: "Professional profile",
};

function renderContactCandidateRows(candidates, detail) {
  const canSaveContact = (
    detail?.content_scope === "confirmed_full"
    && detail?.content_verification?.verified === true
  );
  return candidates.map((candidate) => {
    const lineLabel = candidate.line_start === candidate.line_end
      ? `Line ${candidate.line_start}`
      : `Lines ${candidate.line_start}–${candidate.line_end}`;
    const channels = (Array.isArray(candidate.channels) ? candidate.channels : [])
      .map((channel) => `<li><span>${escapeHtml(CONTACT_CHANNEL_LABELS[channel.kind] || channel.kind)}</span><strong translate="no">${escapeHtml(channel.value || "")}</strong></li>`)
      .join("");
    const facts = [candidate.role, candidate.company].filter(Boolean);
    const title = candidate.name || candidate.channels?.[0]?.value || "Application channel";
    return `<article class="contact-candidate-card">
      <header>
        <div><strong>${escapeHtml(title)}</strong>${facts.length ? `<span>${escapeHtml(facts.join(" · "))}</span>` : ""}</div>
        <span>${escapeHtml(candidate.confidence === "explicit" ? "Explicit" : "Review")}</span>
      </header>
      <p class="contact-candidate-context">${escapeHtml(CONTACT_CONTEXT_LABELS[candidate.context_kind] || candidate.context_kind)}</p>
      ${channels ? `<ul class="contact-candidate-channels">${channels}</ul>` : '<p class="contact-candidate-no-channel">No direct channel stated</p>'}
      <blockquote translate="no">${escapeHtml(candidate.source_text || "")}</blockquote>
      <footer><small>${escapeHtml(lineLabel)} · extractor v${escapeHtml(candidate.extractor_version)}</small><button class="secondary-button compact-button" type="button" data-save-contact-candidate data-contact-source-kind="discovery" data-contact-source-id="${escapeHtml(detail.discovery_id)}" data-contact-candidate-id="${escapeHtml(candidate.candidate_id)}" data-contact-content-sha256="${escapeHtml(candidate.content_sha256)}"${canSaveContact ? "" : ' disabled title="Confirm the complete JD before saving a contact"'}>Save contact</button></footer>
    </article>`;
  }).join("");
}

function renderDiscoveryContactCandidates(detail) {
  const analysis = detail?.review_assessment?.contact_candidates;
  if (!analysis) return "";
  if (analysis.eligible !== true) {
    return `<section class="contact-candidate-panel is-unavailable" aria-labelledby="discovery-contact-candidates-heading">
      <div class="contact-candidate-heading"><div><span class="section-kicker">People & channels</span><h3 id="discovery-contact-candidates-heading">Full JD required</h3></div></div>
      <p>A partial or link-only capture cannot support source-backed contact review.</p>
    </section>`;
  }
  const candidates = Array.isArray(analysis.candidates) ? analysis.candidates : [];
  return `<section class="contact-candidate-panel" aria-labelledby="discovery-contact-candidates-heading">
    <div class="contact-candidate-heading">
      <div><span class="section-kicker">People & channels</span><h3 id="discovery-contact-candidates-heading">Contact candidates</h3></div>
      ${candidates.length ? `<span>${candidates.length} flagged</span>` : ""}
    </div>
    ${candidates.length
      ? `<div class="contact-candidate-list">${renderContactCandidateRows(candidates, detail)}</div>`
      : '<p>No explicit recruiting contact or application channel detected.</p>'}
    <p class="contact-candidate-note">Exact captured wording only. Reviewing creates nothing; Save contact always asks for confirmation.</p>
  </section>`;
}

const REVIEWABLE_DISCOVERY_FIELDS = [
  { name: "title", label: "Role / title" },
  { name: "company", label: "Company" },
  { name: "location", label: "Location" },
];

function normalizeFieldValue(value) {
  return String(value || "").trim().replace(/\s+/g, " ");
}

function resetFieldEditor({ keepNotice = false } = {}) {
  state.fieldEditing = false;
  state.fieldSaving = false;
  state.fieldDraft = null;
  state.fieldReset = new Set();
  if (!keepNotice) {
    state.fieldNotice = "";
    state.fieldNoticeTone = "";
  }
}

function extractedValue(detail, field) {
  return String(detail?.extracted_fields?.[field] || "");
}

function overrideValue(detail, field) {
  const value = detail?.field_overrides?.[field];
  return value === null || value === undefined ? null : String(value);
}

function fieldIsEdited(detail, field) {
  return overrideValue(detail, field) !== null;
}

function renderDiscoveryFields(detail) {
  const canEdit = (
    detail.state !== "promoted"
    && state.promotingId !== detail.discovery_id
    && state.verifyingId !== detail.discovery_id
  );
  const notice = state.fieldNotice
    ? `<p class="form-message discovery-field-notice ${escapeHtml(state.fieldNoticeTone)}" aria-live="polite">${escapeHtml(state.fieldNotice)}</p>`
    : '<p class="form-message discovery-field-notice" aria-live="polite"></p>';
  if (!state.fieldEditing) {
    const rows = REVIEWABLE_DISCOVERY_FIELDS.map(({ name, label }) => {
      const edited = fieldIsEdited(detail, name);
      const effective = String(detail[name] || "");
      const extracted = extractedValue(detail, name);
      return `<div class="discovery-field-row">
        <div class="discovery-field-label"><span>${escapeHtml(label)}</span><span class="discovery-field-origin ${edited ? "is-edited" : "is-extracted"}">${edited ? "Edited by you" : "Extracted"}</span></div>
        <strong>${escapeHtml(effective || "Not provided")}</strong>
        ${edited ? `<small><span>Extracted value</span>: ${escapeHtml(extracted || "Not provided")}</small>` : ""}
      </div>`;
    }).join("");
    return `<section class="discovery-field-review" aria-labelledby="discovery-field-heading">
      <div class="discovery-field-heading">
        <div><span class="section-kicker">Review</span><h3 id="discovery-field-heading">Job details</h3></div>
        ${canEdit ? '<button type="button" class="text-button" data-discovery-fields-edit>Edit details</button>' : ""}
      </div>
      <p class="discovery-field-help">These effective values will be used when you save the job. Captured evidence stays unchanged.</p>
      <div class="discovery-field-rows">${rows}</div>
      ${notice}
    </section>`;
  }

  const draft = state.fieldDraft || Object.fromEntries(
    REVIEWABLE_DISCOVERY_FIELDS.map(({ name }) => [name, String(detail[name] || "")]),
  );
  const fields = REVIEWABLE_DISCOVERY_FIELDS.map(({ name, label }) => {
    const extracted = extractedValue(detail, name);
    const reset = state.fieldReset.has(name);
    const edited = !reset && normalizeFieldValue(draft[name]) !== normalizeFieldValue(extracted);
    return `<label class="discovery-field-editor-row">
      <span class="discovery-field-label"><span>${escapeHtml(label)}</span><span class="discovery-field-origin ${edited ? "is-edited" : "is-extracted"}">${edited ? "Edited by you" : "Extracted"}</span></span>
      <span class="discovery-field-input-row">
        <input type="text" maxlength="500" autocomplete="off" data-discovery-field-input="${escapeHtml(name)}" value="${escapeHtml(String(draft[name] || ""))}"${state.fieldSaving ? " disabled" : ""}>
        <button type="button" class="text-button" data-discovery-field-reset="${escapeHtml(name)}"${state.fieldSaving ? " disabled" : ""}>Use extracted</button>
      </span>
      <small><span>Captured value</span>: ${escapeHtml(extracted || "Not provided")}</small>
    </label>`;
  }).join("");
  return `<form class="discovery-field-review is-editing" data-discovery-fields-form>
    <div class="discovery-field-heading">
      <div><span class="section-kicker">Review</span><h3>Correct job details</h3></div>
    </div>
    <p class="discovery-field-help">Corrections change the effective job details, not the captured source.</p>
    <div class="discovery-field-editor">${fields}</div>
    ${notice}
    <div class="discovery-field-actions">
      <button type="button" class="text-button" data-discovery-fields-cancel${state.fieldSaving ? " disabled" : ""}>Cancel</button>
      <button type="submit" class="secondary-button"${state.fieldSaving ? " disabled" : ""}>${state.fieldSaving ? "Saving…" : "Save changes"}</button>
    </div>
  </form>`;
}

function readStoredMode() {
  try {
    const value = window.localStorage.getItem(MODE_KEY);
    return MODES.has(value) ? value : "feed";
  } catch {
    return "feed";
  }
}

function setMode(mode, { persist = true } = {}) {
  state.mode = MODES.has(mode) ? mode : "feed";
  el.modeButtons.forEach((button) => {
    const active = button.dataset.discoveryMode === state.mode;
    button.setAttribute("aria-pressed", String(active));
    button.classList.toggle("active", active);
  });
  el.surfaces.forEach((surface) => {
    surface.hidden = surface.dataset.discoverySurface !== state.mode;
  });
  if (persist) {
    try { window.localStorage.setItem(MODE_KEY, state.mode); } catch { /* browser preference is optional */ }
  }
}

export function setDiscoveryRouteTarget(discoveryId, intent = null) {
  const normalized = typeof discoveryId === "string" ? discoveryId.trim() : "";
  state.routeTargetId = normalized && !/[\\/]/.test(normalized) ? normalized : null;
  state.routeIntent = state.routeTargetId && intent === "prepare" ? "prepare" : null;
  if (state.routeTargetId) {
    state.mode = "feed";
    state.currentPage = 1;
    state.selectedId = state.routeTargetId;
    state.detail = null;
    state.mobileDetailOpen = true;
    resetFieldEditor();
    if (el.modeButtons) setMode("feed", { persist: false });
  }
}

function publishSelectedDiscovery(discoveryId) {
  window.dispatchEvent(new CustomEvent("careerhub:discovery-selected", {
    detail: { discoveryId, intent: state.routeIntent },
  }));
}

function visibleItems() {
  return state.items;
}

function renderPaginationControls(totalPages) {
  if (!el.pagination) return;
  const isFirst = state.currentPage <= 1;
  const isLast = state.currentPage >= totalPages;

  // goToDiscoveryPage refuses overlapping loads; reflect that in every control
  // so an apparently available page change cannot silently discard a click.
  el.pagination.setAttribute("aria-busy", String(state.loading));
  if (el.firstPage) el.firstPage.disabled = state.loading || isFirst;
  if (el.prevPage) el.prevPage.disabled = state.loading || isFirst;
  if (el.nextPage) el.nextPage.disabled = state.loading || isLast;
  if (el.lastPage) el.lastPage.disabled = state.loading || isLast;

  if (!el.pageNumbers) return;

  const pages = [];
  const maxButtons = 5;
  let startPage = Math.max(1, state.currentPage - 2);
  let endPage = Math.min(totalPages, startPage + maxButtons - 1);

  if (endPage - startPage + 1 < maxButtons) {
    startPage = Math.max(1, endPage - maxButtons + 1);
  }

  if (startPage > 1) {
    pages.push({ type: "page", number: 1 });
    if (startPage > 2) pages.push({ type: "ellipsis" });
  }

  for (let i = startPage; i <= endPage; i++) {
    if (!pages.some((p) => p.type === "page" && p.number === i)) {
      pages.push({ type: "page", number: i });
    }
  }

  if (endPage < totalPages) {
    if (endPage < totalPages - 1) pages.push({ type: "ellipsis" });
    pages.push({ type: "page", number: totalPages });
  }

  el.pageNumbers.innerHTML = pages.map((p) => {
    if (p.type === "ellipsis") {
      return '<span class="job-feed-page-ellipsis" aria-hidden="true">…</span>';
    }
    const active = p.number === state.currentPage;
    return `<button type="button" class="job-feed-page-number ${active ? "is-active" : ""}" data-page="${p.number}" data-tooltip="Go to page ${p.number}" ${active ? 'aria-current="page"' : ""} aria-label="Page ${p.number}"${state.loading ? " disabled" : ""}>${p.number}</button>`;
  }).join("");
}

function renderRows() {
  const rows = visibleItems();
  const totalCount = state.total;
  const pageSize = Number(state.pageSize) || DEFAULT_PAGE_SIZE;
  const totalPages = Math.max(1, Math.ceil(totalCount / pageSize));

  if (state.currentPage > totalPages) {
    state.currentPage = totalPages;
  }
  if (state.currentPage < 1) {
    state.currentPage = 1;
  }

  const startIndex = (state.currentPage - 1) * pageSize;
  const endIndex = Math.min(startIndex + rows.length, totalCount);
  const pagedRows = rows;

  const postingLinkLabel = escapeHtml(localizedUiText("Posting link available"));

  if (el.resultCount) {
    el.resultCount.textContent = `${totalCount.toLocaleString()} result${totalCount === 1 ? "" : "s"}`;
  }
  if (el.pageInfo) {
    el.pageInfo.textContent = totalCount === 0
      ? "Showing 0 of 0"
      : `Showing ${(startIndex + 1).toLocaleString()}–${endIndex.toLocaleString()} of ${totalCount.toLocaleString()}`;
  }

  if (!pagedRows.some((item) => item.discovery_id === state.selectedId)) {
    state.selectedId = pagedRows[0]?.discovery_id || null;
    state.detail = null;
    resetFieldEditor();
  }

  el.workspace?.classList.toggle("has-mobile-detail", state.mobileDetailOpen);
  el.list.innerHTML = pagedRows.length
    ? pagedRows.map((item) => {
      return `<div role="listitem" class="job-feed-row-item"><button type="button" class="job-feed-row ${item.discovery_id === state.selectedId ? "is-selected" : ""}" data-discovery-id="${escapeHtml(item.discovery_id)}" aria-pressed="${item.discovery_id === state.selectedId ? "true" : "false"}" aria-label="${escapeHtml(`${displayTitle(item)}, ${displayCompany(item)}`)}">
        <span class="job-feed-opportunity"><strong><span class="job-feed-title-text">${escapeHtml(displayTitle(item))}</span>${item.observed_url ? `<span class="job-feed-link-indicator" aria-label="${postingLinkLabel}" data-tooltip="${postingLinkLabel}">↗</span>` : ""}</strong><small>${escapeHtml(displayCompany(item))}${item.location ? ` · ${escapeHtml(item.location)}` : ""}</small></span>
        <span>${sourceBadgeHtml(item)}</span>
        <span class="job-feed-date">${escapeHtml(formatDate(item.first_seen_at))}</span>
        <span class="job-feed-date">${escapeHtml(formatDate(item.last_seen_at))}</span>
      </button></div>`;
    }).join("")
    : `<div class="empty-state">No ${state.visibility === "archived" ? "dismissed" : "active"} opportunities match these filters.</div>`;

  renderPaginationControls(totalPages);
  if (el.flushArchived) {
    el.flushArchived.hidden = state.visibility !== "archived" || totalCount === 0;
  }
  renderDetail();
}

function recommendationFrom(detail) {
  const observations = Array.isArray(detail?.observations) ? [...detail.observations].reverse() : [];
  for (const observation of observations) {
    const recommendation = observation?.metadata?.source_recommendation;
    if (recommendation?.kind && recommendation.kind !== "none") return recommendation;
  }
  return null;
}

function renderDetail() {
  const item = state.items.find((candidate) => candidate.discovery_id === state.selectedId);
  if (!item) {
    el.detail.innerHTML = '<div class="empty-state">Select an opportunity to inspect it.</div>';
    return;
  }
  if (!state.detail || state.detail.discovery_id !== item.discovery_id) {
    el.detail.innerHTML = '<div class="job-feed-detail-loading">Loading opportunity…</div>';
    return;
  }
  const detail = state.detail;
  const preparingApplication = state.routeIntent === "prepare";
  const promotionBusy = state.promotingId === detail.discovery_id;
  const verificationBusy = state.verifyingId === detail.discovery_id;
  const actionBusy = state.loading || Boolean(state.changingId) || promotionBusy || verificationBusy || state.fieldSaving;
  const recommendation = recommendationFrom(detail);
  const sourceKinds = (detail.source_kinds || []).map(origin => ({
    email_file: "Email file",
    watcher: "Watcher",
    manual: "Manual capture",
    extension: "Browser capture",
    public_resolver: "Public resolver",
  }[origin] || origin));
  const primaryAction = detail.state === "inbox"
    ? `<button type="button" class="secondary-button" data-discovery-state="shortlisted"${actionBusy ? " disabled" : ""}>Shortlist</button>`
    : detail.state === "shortlisted"
      ? `<button type="button" class="secondary-button" data-discovery-state="inbox"${actionBusy ? " disabled" : ""}>Move to inbox</button>`
      : detail.state === "dismissed"
        ? `<button type="button" class="secondary-button" data-discovery-state="inbox"${actionBusy ? " disabled" : ""}>Restore</button>`
        : "";
  const dismissAction = ["inbox", "shortlisted"].includes(detail.state)
    ? `<button type="button" class="text-button discovery-dismiss-action" data-discovery-state="dismissed"${actionBusy ? " disabled" : ""}>Dismiss</button>`
    : "";
  const openUrl = detail.resolved_url || detail.observed_url;
  let openHost = "";
  try { openHost = new URL(openUrl).hostname.replace(/^www\./, ""); } catch { /* Server URLs are normally validated. */ }
  const openAction = openUrl
    ? `<a class="job-feed-posting-link" href="${escapeHtml(openUrl)}" target="_blank" rel="noopener noreferrer" data-network-action="external-link" data-tooltip="Open the source posting in a new tab. Opening it does not save a job or record an application."><span><strong>Open posting</strong>${openHost ? `<small>${escapeHtml(openHost)}</small>` : ""}</span><span aria-hidden="true">↗</span><span class="visually-hidden"> (opens in new tab)</span></a>`
    : '<span class="job-feed-no-link">Posting link unavailable</span>';
  const canPromote = (
    detail.content_scope === "confirmed_full"
    && detail.content_verification?.verified === true
    && ["inbox", "shortlisted"].includes(detail.state)
  );
  const canVerify = (
    ["page_visible", "selection"].includes(detail.content_scope)
    && Boolean(detail.source_text)
    && ["inbox", "shortlisted"].includes(detail.state)
  );
  const verificationPanel = canVerify
    ? `<section class="discovery-verification-panel" aria-label="Verify captured job description">
        <div>
          <span class="section-kicker">Verification required</span>
          <strong>Is this one complete job description?</strong>
          <small>Review the retained text above. Confirmation ${preparingApplication ? "unlocks the application workspace" : "enables intake controls"} but does not create a job or application.</small>
        </div>
        <div class="discovery-promotion-actions">
          <button type="button" class="secondary-button" data-discovery-review-later${actionBusy ? " disabled" : ""}>Review later</button>
          <button type="button" class="primary-button" data-discovery-confirm-content${actionBusy ? " disabled" : ""}>${verificationBusy ? "Confirming…" : "Confirm complete JD"}</button>
        </div>
      </section>`
    : "";
  const promotionPanel = canPromote
    ? preparingApplication
      ? `<section class="discovery-promotion-panel" aria-label="Prepare application from complete job description">
          <div>
            <span class="section-kicker">Ready to prepare</span>
            <strong>Open the application workspace</strong>
            <small>Career Hub will save this JD, run its deterministic assessment, move it to Preparing, and show the resume and cover-letter choices.</small>
          </div>
          <div class="discovery-promotion-actions">
            <button type="button" class="text-button" data-discovery-promote="save_job"${promotionBusy ? " disabled" : ""}>Save for later</button>
            <button type="button" class="primary-button" data-discovery-promote="prepare_application"${promotionBusy ? " disabled" : ""}>${promotionBusy ? "Preparing…" : "Prepare application"}</button>
          </div>
        </section>`
      : `<section class="discovery-promotion-panel" aria-label="Save complete job description">
          <div>
            <span class="section-kicker">Ready for intake</span>
            <strong>Save this complete JD</strong>
            <small>Career Hub will create the saved job, run its deterministic assessment, and keep this discovery as provenance.</small>
          </div>
          <div class="discovery-promotion-actions">
            <button type="button" class="text-button" data-discovery-review-later${actionBusy ? " disabled" : ""}>Review later</button>
            <button type="button" class="primary-button" data-discovery-promote="save_job"${promotionBusy ? " disabled" : ""}>${promotionBusy ? "Saving…" : "Save job"}</button>
            <button type="button" class="secondary-button" data-discovery-promote="record_as_applied"${promotionBusy ? " disabled" : ""}>Record as applied</button>
          </div>
        </section>`
    : detail.state === "promoted" && detail.promoted_job_id
      ? `<section class="discovery-promotion-panel is-complete" aria-label="Saved job">
          <div><span class="section-kicker">Saved job</span><strong>This discovery is now in Job Workspace.</strong></div>
          <button type="button" class="secondary-button" data-open-promoted-job="${escapeHtml(detail.promoted_job_id)}">${preparingApplication ? "Open application workspace" : "Open job workspace"}</button>
        </section>`
      : "";
  const contentState = detail.content_scope === "confirmed_full"
    ? {
      title: "Complete JD verified",
      text: "The retained source is eligible for explicit Save job or Record as applied actions.",
      tone: "is-ready",
    }
    : detail.content_scope === "link_only"
      ? {
        title: "Full JD still needed",
        text: "Open the posting and use attended capture before verification.",
        tone: "needs-jd",
      }
      : {
        title: "Captured text needs verification",
        text: "Review the exact retained text before confirming that it contains one complete JD.",
        tone: "needs-review",
      };
  const verifiedAt = detail.content_verification?.verified_at;
  const sourceDetails = detail.source_text
    ? `<details class="discovery-snippet"${detail.content_scope === "confirmed_full" ? "" : " open"}>
        <summary>Exact retained ${detail.content_scope === "selection" ? "selection" : "job text"}</summary>
        <pre>${escapeHtml(detail.source_text)}</pre>
      </details>`
    : '<div class="discovery-source-empty">No job-description text is retained for this link-only item.</div>';
  el.detail.innerHTML = `<button type="button" class="job-feed-detail-back text-button" data-discovery-back>← All opportunities</button>
    <div class="job-feed-detail-header">
      <div class="job-feed-detail-badges">${sourceBadgeHtml(detail)}<span class="discovery-lifecycle">${escapeHtml(lifecycleLabel(detail.state))}</span></div>
      <h2>${escapeHtml(displayTitle(detail))}</h2>
      <p>${escapeHtml(displayCompany(detail))}${detail.location ? ` · ${escapeHtml(detail.location)}` : ""}</p>
    </div>
    ${recommendation ? `<div class="source-recommendation"><strong>Recommended by source</strong><span>${escapeHtml(recommendation.evidence_text || recommendation.kind.replaceAll("_", " "))}</span><small>This is email wording, not a Career Hub fit assessment.</small></div>` : ""}
    <div class="discovery-content-state ${contentState.tone}">
      <strong>${escapeHtml(contentState.title)}</strong>
      <span>${escapeHtml(contentState.text)}</span>
    </div>
    <div class="job-feed-posting-action">${openAction}</div>
    ${verificationPanel}
    ${promotionPanel}
    <div class="job-feed-detail-actions"><span class="job-feed-detail-state-actions">${primaryAction}${dismissAction}</span></div>
    ${renderDiscoveryFields(detail)}
    <dl class="job-feed-metadata">
      <div><dt>Capture</dt><dd>${escapeHtml(captureScopeLabel(detail))}</dd></div>
      <div><dt>First seen</dt><dd>${escapeHtml(formatDate(detail.first_seen_at))}</dd></div>
      <div><dt>Last seen</dt><dd>${escapeHtml(formatDate(detail.last_seen_at))}</dd></div>
      <div><dt>Sources</dt><dd>${escapeHtml(sourceKinds.join(", ") || "Unknown")}</dd></div>
      <div><dt>Posting ID</dt><dd>${escapeHtml(detail.posting_identity || "Not provided")}</dd></div>
      ${verifiedAt ? `<div><dt>Verified</dt><dd>${escapeHtml(formatDateTime(verifiedAt))}</dd></div>` : ""}
    </dl>
    ${sourceDetails}
    ${renderDiscoveryAssessment(detail)}
    ${renderDiscoveryInstructions(detail)}
    ${renderDiscoveryContactCandidates(detail)}`;
}

function useUpdatedDiscovery(updated) {
  state.detail = state.detail?.discovery_id === updated.discovery_id
    ? { ...state.detail, ...updated }
    : updated;
  const index = state.items.findIndex((item) => item.discovery_id === updated.discovery_id);
  if (index >= 0) state.items[index] = { ...state.items[index], ...updated };
}

function beginFieldEdit() {
  const detail = state.detail;
  if (
    !detail
    || detail.state === "promoted"
    || state.fieldSaving
    || state.promotingId
    || state.verifyingId
  ) return;
  state.fieldEditing = true;
  state.fieldDraft = Object.fromEntries(
    REVIEWABLE_DISCOVERY_FIELDS.map(({ name }) => [name, String(detail[name] || "")]),
  );
  state.fieldReset = new Set();
  state.fieldNotice = "";
  state.fieldNoticeTone = "";
  renderDetail();
  el.detail.querySelector("[data-discovery-field-input]")?.focus();
}

function cancelFieldEdit() {
  if (state.fieldSaving) return;
  resetFieldEditor();
  renderDetail();
}

function resetCorrectedField(field) {
  const detail = state.detail;
  if (
    !detail
    || state.fieldSaving
    || !REVIEWABLE_DISCOVERY_FIELDS.some((item) => item.name === field)
  ) return;
  state.fieldDraft = {
    ...(state.fieldDraft || {}),
    [field]: extractedValue(detail, field),
  };
  state.fieldReset.add(field);
  state.fieldNotice = "";
  state.fieldNoticeTone = "";
  renderDetail();
  el.detail.querySelector(`[data-discovery-field-input="${field}"]`)?.focus();
}

async function saveFieldCorrections(event) {
  event.preventDefault();
  const detail = state.detail;
  if (!detail || state.fieldSaving || detail.state === "promoted") return;

  const overrides = {};
  const draft = Object.fromEntries(
    REVIEWABLE_DISCOVERY_FIELDS.map(({ name }) => [
      name,
      el.detail.querySelector(`[data-discovery-field-input="${name}"]`)?.value || "",
    ]),
  );
  for (const { name, label } of REVIEWABLE_DISCOVERY_FIELDS) {
    const value = normalizeFieldValue(draft[name]);
    if (state.fieldReset.has(name) || value === normalizeFieldValue(extractedValue(detail, name))) {
      overrides[name] = null;
    } else if (!value) {
      state.fieldDraft = draft;
      state.fieldNotice = `${label} cannot be empty. Use “Use extracted” to remove a correction.`;
      state.fieldNoticeTone = "error";
      renderDetail();
      el.detail.querySelector(`[data-discovery-field-input="${name}"]`)?.focus();
      return;
    } else {
      overrides[name] = value;
    }
  }

  state.fieldDraft = draft;
  state.fieldSaving = true;
  state.fieldNotice = "Saving corrections…";
  state.fieldNoticeTone = "";
  renderDetail();
  try {
    const updated = await api.setDiscoveryFieldOverrides(
      detail.discovery_id,
      overrides,
      Number(detail.field_overrides?.revision || 0),
    );
    useUpdatedDiscovery(updated);
    resetFieldEditor();
    state.fieldNotice = "Job details updated. Captured evidence was not changed.";
    state.fieldNoticeTone = "success";
    renderRows();
    if (state.emailConnection && !state.emailFormDirty) {
      await loadEmailConnection();
    }
  } catch (error) {
    if (error?.payload?.error_code === "DISCOVERY_FIELD_OVERRIDE_REVISION_CONFLICT") {
      try {
        const latest = await api.getDiscovery(detail.discovery_id);
        useUpdatedDiscovery(latest);
      } catch {
        // The original conflict remains the useful user-facing error.
      }
      resetFieldEditor();
      state.fieldNotice = "Job details changed in another tab. Review the latest values before editing again.";
      state.fieldNoticeTone = "error";
    } else {
      state.fieldSaving = false;
      state.fieldNotice = formatRequestError(error);
      state.fieldNoticeTone = "error";
    }
    renderRows();
  }
}

async function loadDetail(discoveryId) {
  if (!discoveryId) return;
  if (state.detail?.discovery_id !== discoveryId) resetFieldEditor();
  state.detail = null;
  renderDetail();
  try {
    const detail = await api.getDiscovery(discoveryId);
    if (state.selectedId !== discoveryId) return;
    state.detail = detail;
    renderDetail();
  } catch (error) {
    if (state.selectedId !== discoveryId) return;
    el.detail.innerHTML = `<div class="empty-state error-state">${escapeHtml(formatRequestError(error))}</div>`;
  }
}

function selectedSourceKinds() {
  return el.sourceFilter.value ? el.sourceFilter.value.split(",") : [];
}

function selectedQueues() {
  return el.queueFilter.value ? [el.queueFilter.value] : [];
}

function selectedObservedAfter() {
  const days = Number(el.observedFilter.value || 0);
  if (!days) return "";
  return new Date(Date.now() - days * 24 * 60 * 60 * 1000).toISOString();
}

function discoveryPageCursor() {
  const offset = (state.currentPage - 1) * state.pageSize;
  return offset > 0 ? `offset:${offset}` : null;
}

function resetDiscoverySelectionForQuery() {
  state.routeTargetId = null;
  state.routeIntent = null;
  state.selectedId = null;
  state.detail = null;
  state.mobileDetailOpen = false;
  resetFieldEditor();
}

function setDiscoveryVisibility(visibility) {
  state.visibility = visibility === "archived" ? "archived" : "active";
  const archived = state.visibility === "archived";
  if (el.flushArchived) {
    el.flushArchived.hidden = !archived || state.items.length === 0;
  }
  if (!el.archivedFilter) return;
  el.archivedFilter.setAttribute("aria-pressed", String(archived));
  el.archivedFilter.setAttribute(
    "aria-label",
    archived ? "Show active opportunities" : "Show dismissed opportunities",
  );
  el.archivedFilter.dataset.tooltip = archived
    ? "Show active opportunities"
    : "Show dismissed opportunities";
}

export async function loadDiscoveries() {
  const epoch = ++state.loadEpoch;
  state.loading = true;
  el.message.className = "form-message job-feed-message";
  el.message.textContent = "Loading opportunities…";
  el.list.setAttribute("aria-busy", "true");
  renderRows();
  const sourceKinds = selectedSourceKinds();
  try {
    let targetDetail = null;
    if (state.routeTargetId) {
      targetDetail = await api.getDiscovery(state.routeTargetId);
      if (epoch !== state.loadEpoch) return;
      if (targetDetail.state === "dismissed") setDiscoveryVisibility("archived");
    }
    const states = state.visibility === "archived"
      ? ["dismissed"]
      : ["inbox", "shortlisted"];
    const request = {
      states,
      sourceKinds,
      queues: selectedQueues(),
      search: el.search.value.trim(),
      observedAfter: selectedObservedAfter(),
      sort: el.sort.value,
      cursor: discoveryPageCursor(),
      limit: state.pageSize,
    };
    const [firstPage, archivedPage] = await Promise.all([
      api.getDiscoveries(request),
      api.getDiscoveries({ states: ["dismissed"], sourceKinds, limit: 1 }),
    ]);
    if (epoch !== state.loadEpoch) return;

    if (
      targetDetail
      && !(firstPage.items || []).some(
        (item) => item.discovery_id === targetDetail.discovery_id,
      )
    ) {
      firstPage.items = [targetDetail, ...(firstPage.items || [])].slice(
        0,
        state.pageSize,
      );
    }
    state.items = firstPage.items || [];
    state.total = Math.max(Number(firstPage.total || 0), state.items.length);
    state.nextCursor = firstPage.next_cursor || null;
    state.archivedTotal = state.visibility === "archived"
      ? state.total
      : Number(archivedPage.total || 0);

    const totalPages = Math.max(1, Math.ceil(state.total / state.pageSize));
    if (state.currentPage > totalPages) {
      state.currentPage = totalPages;
      await loadDiscoveries();
      return;
    }

    if (targetDetail) {
      state.selectedId = targetDetail.discovery_id;
      state.detail = targetDetail;
      state.mobileDetailOpen = true;
    }
    const visible = visibleItems();
    if (!visible.some((item) => item.discovery_id === state.selectedId)) {
      state.selectedId = visible[0]?.discovery_id || null;
      state.detail = null;
      resetFieldEditor();
    }
    el.message.textContent = "";
    renderRows();
    if (el.archivedCount) el.archivedCount.textContent = state.archivedTotal.toLocaleString();
    if (state.selectedId && state.detail?.discovery_id !== state.selectedId) {
      await loadDetail(state.selectedId);
    }
  } catch (error) {
    if (epoch !== state.loadEpoch) return;
    el.message.className = "form-message job-feed-message error";
    el.message.textContent = formatRequestError(error);
    state.items = [];
    state.total = 0;
    state.selectedId = null;
    state.detail = null;
    resetFieldEditor();
    renderRows();
  } finally {
    if (epoch === state.loadEpoch) {
      state.loading = false;
      el.list.setAttribute("aria-busy", "false");
      renderRows();
    }
  }
}

async function goToDiscoveryPage(page) {
  const totalPages = Math.max(1, Math.ceil(state.total / state.pageSize));
  const target = Math.max(1, Math.min(totalPages, Number(page) || 1));
  if (target === state.currentPage || state.loading) return;
  state.currentPage = target;
  resetDiscoverySelectionForQuery();
  await loadDiscoveries();
  el.list.scrollTop = 0;
  el.list.querySelector("[data-discovery-id]")?.focus();
}

async function changeState(nextState) {
  const detail = state.detail;
  if (
    !detail
    || state.loading
    || state.promotingId
    || state.verifyingId
    || state.changingId
  ) return;
  state.changingId = detail.discovery_id;
  renderDetail();
  el.message.textContent = "Saving…";
  try {
    const updated = await api.setDiscoveryState(
      detail.discovery_id,
      nextState,
      detail.revision,
    );
    state.detail = updated;
    state.selectedId = updated.discovery_id;
    // A selected row is not a new deep-link navigation. Otherwise dismissing
    // it reopens the archive on reload and flips the user's current filter.
    state.routeTargetId = null;
    state.routeIntent = null;
    await loadDiscoveries();
    el.message.className = "form-message job-feed-message success";
    el.message.textContent = nextState === "dismissed"
      ? "Opportunity dismissed."
      : nextState === "shortlisted"
        ? "Opportunity shortlisted."
        : "Opportunity returned to the inbox.";
  } catch (error) {
    el.message.className = "form-message job-feed-message error";
    el.message.textContent = formatRequestError(error);
  } finally {
    state.changingId = null;
    renderDetail();
  }
}

async function deleteSelectedDiscovery() {
  const detail = state.detail;
  if (!detail || detail.state === "promoted" || state.loading) return;
  const title = displayTitle(detail) || "this opportunity";
  const confirmed = window.confirm(
    `Permanently delete "${title}"? This removes the opportunity and its stored capture from the system. This cannot be undone.`
  );
  if (!confirmed) return;

  state.loading = true;
  el.message.className = "form-message job-feed-message";
  el.message.textContent = "Deleting opportunity…";
  try {
    await api.deleteDiscovery(detail.discovery_id, detail.revision);
    el.message.className = "form-message job-feed-message success";
    el.message.textContent = "Opportunity deleted.";
    const deletedId = detail.discovery_id;
    state.items = state.items.filter((item) => item.discovery_id !== deletedId);
    state.total = Math.max(0, state.total - 1);
    if (state.visibility === "archived") {
      state.archivedTotal = Math.max(0, state.archivedTotal - 1);
    }
    if (el.archivedCount) el.archivedCount.textContent = state.archivedTotal.toLocaleString();
    const visible = visibleItems();
    state.selectedId = visible[0]?.discovery_id || null;
    state.detail = null;
    state.routeTargetId = state.selectedId;
    resetFieldEditor();
    renderRows();
    if (state.selectedId) {
      await loadDetail(state.selectedId);
    } else {
      renderDetail();
    }
  } catch (error) {
    el.message.className = "form-message job-feed-message error";
    el.message.textContent = formatRequestError(error);
  } finally {
    state.loading = false;
    renderRows();
  }
}

async function flushArchived() {
  if (state.archivedTotal <= 0 && state.items.length <= 0) return;
  const count = state.archivedTotal || state.items.length;
  const confirmed = window.confirm(
    `Permanently delete all ${count} dismissed opportunities? This removes all dismissed captures from the system. This cannot be undone.`
  );
  if (!confirmed) return;

  state.loading = true;
  el.message.className = "form-message job-feed-message";
  el.message.textContent = "Deleting all dismissed opportunities…";
  try {
    const result = await api.flushArchivedDiscoveries();
    const deletedCount = Number(result?.count || 0);
    el.message.className = "form-message job-feed-message success";
    el.message.textContent = `Deleted ${deletedCount} dismissed ${deletedCount === 1 ? "opportunity" : "opportunities"}.`;
    state.selectedId = null;
    state.detail = null;
    state.routeTargetId = null;
    resetFieldEditor();
    await loadDiscoveries();
  } catch (error) {
    el.message.className = "form-message job-feed-message error";
    el.message.textContent = formatRequestError(error);
  } finally {
    state.loading = false;
    renderRows();
  }
}

function openPromotedJob(jobId, { focusMaterials = false } = {}) {
  if (!jobId || /[\\/]/.test(jobId)) return;
  window.dispatchEvent(new CustomEvent("careerhub:open-saved-job", {
    detail: { jobId, focusMaterials },
  }));
}

function reviewSelectedDiscoveryLater() {
  const detail = state.detail;
  if (!detail || state.promotingId || state.verifyingId) return;
  el.message.className = "form-message job-feed-message success";
  el.message.textContent = "Kept in Job Feed for later review. No job or application was created.";
  el.list.querySelector(`[data-discovery-id="${detail.discovery_id}"]`)?.focus();
}

async function confirmSelectedDiscoveryContent() {
  const detail = state.detail;
  if (
    !detail
    || state.verifyingId
    || state.promotingId
    || !["page_visible", "selection"].includes(detail.content_scope)
    || !["inbox", "shortlisted"].includes(detail.state)
  ) return;
  const confirmed = window.confirm(
    "Confirm this is one complete job description? Review the exact retained text first. This enables intake actions but does not create a job or application.",
  );
  if (!confirmed) return;

  state.verifyingId = detail.discovery_id;
  el.message.className = "form-message job-feed-message";
  el.message.textContent = "Confirming retained JD…";
  renderDetail();
  try {
    const updated = await api.confirmDiscoveryContent(
      detail.discovery_id,
      detail.revision,
    );
    useUpdatedDiscovery(updated);
    state.verifyingId = null;
    el.message.className = "form-message job-feed-message success";
    el.message.textContent = "Complete JD confirmed. No job or application was created.";
    renderRows();
  } catch (error) {
    state.verifyingId = null;
    if (error?.payload?.error_code === "DISCOVERY_REVISION_CONFLICT") {
      await loadDiscoveries();
    }
    el.message.className = "form-message job-feed-message error";
    el.message.textContent = formatRequestError(error);
    renderDetail();
  }
}

async function promoteSelectedDiscovery(action) {
  const detail = state.detail;
  if (
    !detail
    || state.promotingId
    || state.verifyingId
    || detail.content_scope !== "confirmed_full"
    || detail.content_verification?.verified !== true
    || !["inbox", "shortlisted"].includes(detail.state)
  ) return;

  const title = displayTitle(detail);
  const confirmed = action === "record_as_applied"
    ? window.confirm(
      `Record ${title} as applied? Confirm only if the application was already submitted. Career Hub will save this verified JD and set its application status to Applied.`,
    )
    : action === "prepare_application"
      ? window.confirm(
        `Prepare an application for ${title}? Career Hub will save this verified JD, set it to Preparing, and open its material choices.`,
      )
      : window.confirm(
        `Save ${title} as a job? Career Hub will create one saved JD and assessment. No application status will be changed.`,
      );
  if (!confirmed) return;

  state.promotingId = detail.discovery_id;
  el.message.className = "form-message job-feed-message";
  el.message.textContent = action === "record_as_applied"
    ? "Saving JD and recording the application…"
    : action === "prepare_application"
      ? "Saving JD and preparing the application workspace…"
      : "Saving JD and running its assessment…";
  renderDetail();
  try {
    const result = await api.promoteDiscovery(
      detail.discovery_id,
      action,
      detail.revision,
    );
    const jobId = result?.job?.id || result?.discovery?.promoted_job_id;
    if (!jobId) throw new Error("Promotion completed without a saved job reference.");
    state.detail = { ...detail, ...(result.discovery || {}) };
    el.message.className = "form-message job-feed-message success";
    el.message.textContent = action === "record_as_applied"
      ? "Job saved and application recorded. Opening Job Workspace…"
      : action === "prepare_application"
        ? "Application workspace is ready. Opening material choices…"
        : "Job saved and assessed. Opening Job Workspace…";
    state.promotingId = null;
    renderDetail();
    openPromotedJob(jobId, { focusMaterials: action === "prepare_application" });
  } catch (error) {
    state.promotingId = null;
    if (error?.payload?.error_code === "DISCOVERY_REVISION_CONFLICT") {
      await loadDiscoveries();
    }
    el.message.className = "form-message job-feed-message error";
    el.message.textContent = formatRequestError(error);
    renderDetail();
  }
}

function setEmailMessage(text = "", tone = "", {
  detail = "",
  errorCode = "",
  runId = "",
  diagnostics = false,
} = {}) {
  el.emailSyncMessage.className = `form-message email-sync-message${tone ? ` ${tone}` : ""}`;
  el.emailSyncMessage.replaceChildren();
  if (!text) return;
  const summary = document.createElement("span");
  summary.className = "email-sync-message__summary";
  summary.textContent = text;
  el.emailSyncMessage.append(summary);
  if (detail) {
    const explanation = document.createElement("span");
    explanation.className = "email-sync-message__detail";
    explanation.textContent = detail;
    el.emailSyncMessage.append(explanation);
  }
  if (errorCode || runId || diagnostics) {
    const metadata = document.createElement("span");
    metadata.className = "email-sync-message__metadata";
    if (errorCode) {
      const code = document.createElement("code");
      code.textContent = errorCode;
      metadata.append(code);
    }
    if (runId) {
      const runLabel = document.createElement("span");
      runLabel.textContent = "Diagnostic run";
      const run = document.createElement("code");
      run.textContent = runId;
      metadata.append(runLabel, run);
    }
    if (diagnostics) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "text-button email-sync-message__action";
      button.textContent = "Open Diagnostics";
      button.addEventListener("click", () => {
        window.dispatchEvent(new CustomEvent("careerhub:opendiagnostics", {
          detail: { runId },
        }));
      });
      metadata.append(button);
    }
    el.emailSyncMessage.append(metadata);
  }
}

function setEmailRequestError(error, fallbackRunId = "") {
  const payload = error?.payload || {};
  setEmailMessage(
    payload.summary || payload.error || error?.message || "The email operation failed.",
    "error",
    {
      detail: payload.suggestion || "Open Diagnostics and retry after resolving the reported stage.",
      errorCode: payload.error_code || "EMAIL_REQUEST_FAILED",
      runId: payload.run_id || fallbackRunId,
      diagnostics: true,
    },
  );
}

function senderRuleHasPurpose(rule, purpose) {
  return rule?.purpose === purpose || rule?.purpose === "both";
}

function allowedConnectionSenders(connection = state.emailConnection, purpose = null) {
  return (connection?.sender_rules || [])
    .filter((rule) => (
      rule.disposition === "allowed"
      && (!purpose || senderRuleHasPurpose(rule, purpose))
    ))
    .map((rule) => String(rule.sender_address || ""))
    .filter(Boolean);
}

function senderDraftFromConnection(connection) {
  return {
    job_alerts: allowedConnectionSenders(connection, "job_alerts"),
    application_tracking: allowedConnectionSenders(
      connection,
      "application_tracking",
    ),
  };
}

function automaticEmailSyncEnabled(connection = state.emailConnection) {
  return Boolean(
    api.capabilities.email_connection !== false
    && !emailReconnectRequired(connection)
    && connection?.configured
    && connection.enabled
    && connection.credential_present
    && connection.schedule_enabled,
  );
}

function stopEmailScheduler() {
  if (state.emailSyncTimer !== null) {
    window.clearInterval(state.emailSyncTimer);
    state.emailSyncTimer = null;
  }
  if (state.emailSchedulerRelease) {
    state.emailSchedulerRelease();
    state.emailSchedulerRelease = null;
  }
}

function startOwnedEmailSchedule(scheduleKey) {
  if (state.emailSchedulerKey !== scheduleKey || !automaticEmailSyncEnabled()) return null;
  void syncEmailNow({ automatic: true });
  const intervalMinutes = Math.max(
    15,
    Math.min(1440, Number(state.emailConnection.interval_minutes || 30)),
  );
  state.emailSyncTimer = window.setInterval(() => {
    void syncEmailNow({ automatic: true });
  }, intervalMinutes * 60 * 1000);
  return state.emailSyncTimer;
}

async function claimEmailScheduler(scheduleKey) {
  if (!navigator.locks?.request) {
    startOwnedEmailSchedule(scheduleKey);
    return;
  }
  try {
    await navigator.locks.request("career-hub-email-sync-scheduler", async () => {
      if (state.emailSchedulerKey !== scheduleKey || !automaticEmailSyncEnabled()) return;
      const ownedTimer = startOwnedEmailSchedule(scheduleKey);
      let releaseOwnership = null;
      await new Promise((resolve) => {
        releaseOwnership = resolve;
        state.emailSchedulerRelease = resolve;
      });
      if (state.emailSchedulerKey === scheduleKey) state.emailSchedulerKey = "";
      if (state.emailSyncTimer === ownedTimer && ownedTimer !== null) {
        window.clearInterval(ownedTimer);
        state.emailSyncTimer = null;
      }
      if (state.emailSchedulerRelease === releaseOwnership) {
        state.emailSchedulerRelease = null;
      }
    });
  } catch {
    if (state.emailSchedulerKey === scheduleKey) startOwnedEmailSchedule(scheduleKey);
  }
}

function refreshEmailScheduler() {
  const nextKey = automaticEmailSyncEnabled()
    ? `${state.emailConnection.account_address}:${Number(state.emailConnection.interval_minutes || 30)}`
    : "";
  if (nextKey === state.emailSchedulerKey) return;
  stopEmailScheduler();
  state.emailSchedulerKey = nextKey;
  if (nextKey) void claimEmailScheduler(nextKey);
}

function useEmailConnection(
  connection,
  { preserveDraft = state.emailFormDirty } = {},
) {
  state.emailConnection = connection;
  if (!preserveDraft) {
    state.emailFormDirty = false;
    state.senderDraft = senderDraftFromConnection(connection);
    el.emailAccount.value = connection?.account_address || "";
    el.emailPassword.value = "";
    el.emailScheduleEnabled.checked = Boolean(connection?.schedule_enabled);
    el.emailSyncInterval.value = String(connection?.interval_minutes || 30);
    el.emailConnectionSettings.open = !connection?.configured
      || !connection?.enabled
      || !connection?.credential_present;
  }
  renderEmailConnection();
  refreshEmailScheduler();
}

function emailReconnectRequired(connection = state.emailConnection) {
  return Boolean(connection?.configured
    && connection.last_error_code === "EMAIL_CREDENTIAL_RECONNECT_REQUIRED");
}

function emailConnectionStatus(connection) {
  if (api.capabilities.email_connection === false) return { label: "Unavailable in Go preview", tone: "is-warning" };
  if (state.emailLoading) return { label: "Loading", tone: "is-loading" };
  if (!connection?.configured) return { label: "Not configured", tone: "is-offline" };
  if (emailReconnectRequired(connection)) return { label: "Reconnect required", tone: "is-warning" };
  if (!connection.credential_present) return { label: "Password required", tone: "is-warning" };
  if (!connection.enabled) return { label: "Disconnected", tone: "is-offline" };
  if (connection.last_test_status === "failed" || connection.sync?.last_result === "failed") {
    return { label: "Needs attention", tone: "is-warning" };
  }
  if (state.emailSyncing) return { label: "Syncing", tone: "is-loading" };
  if (connection.sync?.last_result === "partial") {
    return { label: "Connected · import issues", tone: "is-warning" };
  }
  if (connection.last_test_status === "succeeded") return { label: "Connected", tone: "is-ready" };
  return { label: "Ready to sync", tone: "is-ready" };
}

function draftSenderAddresses() {
  return [...new Set([
    ...(state.senderDraft.job_alerts || []),
    ...(state.senderDraft.application_tracking || []),
  ])].sort();
}

function senderRouteEnabled(address, route) {
  return (state.senderDraft[route] || []).includes(address);
}

function sourceRouteSummary(address) {
  const jobs = senderRouteEnabled(address, "job_alerts");
  const applications = senderRouteEnabled(address, "application_tracking");
  if (jobs && applications) return "Jobs + applications";
  return jobs ? "Jobs" : "Applications";
}

function captureQualityAlerts(connection = state.emailConnection) {
  return Array.isArray(connection?.capture_quality?.alerts)
    ? connection.capture_quality.alerts
    : [];
}

function captureQualityIssueCount(alert) {
  return Object.values(alert?.status_counts || {}).reduce(
    (total, count) => total + Number(count || 0),
    0,
  );
}

function captureQualityCountLabel(alert) {
  const counts = alert?.status_counts || {};
  return [
    Number(counts.suspected_noise || 0) ? `${Number(counts.suspected_noise)} likely non-job` : "",
    Number(counts.needs_review || 0) ? `${Number(counts.needs_review)} field check` : "",
    Number(counts.limited || 0) ? `${Number(counts.limited)} limited details` : "",
  ].filter(Boolean).join(" · ");
}

function renderSourceRoute(address, route, title, description) {
  const enabled = senderRouteEnabled(address, route);
  return `<label class="email-source-route${enabled ? " is-enabled" : ""}"><input type="checkbox" data-email-source-route-toggle data-email-source-address="${escapeHtml(address)}" data-email-source-route="${escapeHtml(route)}"${enabled ? " checked" : ""}><span><strong>${title}</strong><small>${description}</small></span></label>`;
}

function renderSenderDrafts() {
  const addresses = draftSenderAddresses();
  const qualityAlerts = captureQualityAlerts();
  const qualityBySender = new Map(qualityAlerts.map((alert) => [
    String(alert.sender_address || "").toLocaleLowerCase(),
    alert,
  ]));
  const totalQualityIssues = Number(state.emailConnection?.capture_quality?.active_issue_count || 0);
  const selectedAddress = addresses.includes(state.emailSourceFocus)
    ? state.emailSourceFocus
    : addresses[0] || "";
  state.emailSourceFocus = selectedAddress;
  el.emailSourceCount.textContent = `${addresses.length} source${addresses.length === 1 ? "" : "s"}`;
  el.emailSourceQualityIndicator.classList.toggle("hidden", !totalQualityIssues);
  el.emailSourceQualityCount.textContent = totalQualityIssues
    ? `${totalQualityIssues} card${totalQualityIssues === 1 ? " needs" : "s need"} review`
    : "";
  el.emailSourcePolicyList.innerHTML = addresses.length
    ? addresses.map((address) => {
      const qualityCount = captureQualityIssueCount(qualityBySender.get(address));
      const warning = qualityCount
        ? `<span class="email-source-directory-warning"><span class="email-source-warning-mark" aria-hidden="true">!</span>${qualityCount}</span>`
        : "";
      return `<button type="button" class="email-source-directory-item${selectedAddress === address ? " is-selected" : ""}" data-email-source-select="${escapeHtml(address)}"${selectedAddress === address ? ' aria-current="true"' : ""}><strong>${escapeHtml(address)}</strong><span>${escapeHtml(sourceRouteSummary(address))}${warning}</span></button>`;
    }).join("")
    : '<div class="email-source-directory-empty">No sources configured</div>';
  if (!selectedAddress) {
    el.emailSourcePolicyEditor.innerHTML = '<div class="email-source-policy-empty"><strong>No sources configured</strong><span>Add one exact sender and choose at least one route.</span></div>';
    return;
  }
  const alert = qualityBySender.get(selectedAddress);
  const warning = alert
    ? `<aside class="email-source-quality-notice"><span class="email-source-warning-mark" aria-hidden="true">!</span><span><strong>${captureQualityIssueCount(alert)} captured card${captureQualityIssueCount(alert) === 1 ? " needs" : "s need"} review</strong><small>${escapeHtml(captureQualityCountLabel(alert))}</small></span><button type="button" class="text-button" data-email-quality-sample="${escapeHtml(alert.sample_discovery_id)}">Inspect card</button></aside>`
    : "";
  el.emailSourcePolicyEditor.innerHTML = `<article class="email-source-policy-card is-focused" tabindex="-1"><header><div><strong>${escapeHtml(selectedAddress)}</strong><span>${escapeHtml(sourceRouteSummary(selectedAddress))}</span></div><button type="button" class="text-button danger-text" data-email-source-remove="${escapeHtml(selectedAddress)}">Remove source</button></header>${warning}<div class="email-source-route-grid">${renderSourceRoute(selectedAddress, "job_alerts", "Jobs", "Create Job Feed opportunities")}${renderSourceRoute(selectedAddress, "application_tracking", "Applications", "Update the application ledger")}</div></article>`;
}

function pendingSenderSuggestion(sender) {
  const recommendation = String(sender?.recommendation || "none");
  const reasons = Array.isArray(sender?.suggestion_reasons)
    ? sender.suggestion_reasons
    : [];
  if (!["likely_application_tracking", "likely_job_alert", "recurring_review"].includes(recommendation)) {
    return { badge: "", tone: "", details: [] };
  }

  const details = [];
  if (reasons.includes("known_tracking_sender")) details.push("known application-notification sender");
  if (reasons.includes("subject_application_keyword")) details.push("application wording in message header");
  if (reasons.includes("sender_keyword")) details.push("job-alert wording in sender");
  if (reasons.includes("subject_keyword")) details.push("job-alert wording in message header");
  if (reasons.includes("recurring_pattern")) {
    const count = Number(sender.message_count || 0);
    const span = Number(sender.observed_span_days || 0);
    details.push(`${count} messages across ${span} days`);
  }
  if (recommendation === "likely_application_tracking") {
    return { badge: "Suggested application tracking", tone: "is-tracking", details };
  }
  return recommendation === "likely_job_alert"
    ? { badge: "Suggested job alert", tone: "is-likely", details }
    : { badge: "Recurring — review", tone: "is-recurring", details };
}

function pendingSenderPurpose(sender) {
  if (sender?.recommendation === "likely_application_tracking") {
    return "application_tracking";
  }
  return "job_alerts";
}

function renderPendingSenders(connection) {
  const pending = connection?.pending_senders || [];
  el.emailSenderReview.classList.toggle("hidden", !pending.length);
  el.emailSenderReviewCount.textContent = pending.length
    ? `${pending.length} sender${pending.length === 1 ? "" : "s"}`
    : "";
  el.emailSenderReviewList.innerHTML = pending.map((sender) => {
    const suggestion = pendingSenderSuggestion(sender);
    const purpose = pendingSenderPurpose(sender);
    const operation = state.emailSenderOperation?.address === sender.sender_address
      ? state.emailSenderOperation
      : null;
    const badge = suggestion.badge
      ? `<span class="email-sender-suggestion-badge ${suggestion.tone}">${escapeHtml(suggestion.badge)}</span>`
      : "";
    const detail = suggestion.details.length
      ? `<span class="email-sender-suggestion-detail">${suggestion.details.map((item) => `<span>${escapeHtml(item)}</span>`).join('<i aria-hidden="true">·</i>')}</span>`
      : "";
    const actions = operation
      ? renderLoadingState({ label: operation.label, compact: true })
      : `<label class="email-sender-route-select"><span>Capture from this sender</span><select data-email-sender-purpose-select aria-label="Route for ${escapeHtml(sender.sender_address)}"><option value="job_alerts"${purpose === "job_alerts" ? " selected" : ""}>Job opportunities only</option><option value="application_tracking"${purpose === "application_tracking" ? " selected" : ""}>Application updates only</option><option value="both"${purpose === "both" ? " selected" : ""}>Updates + included job suggestions</option></select></label><button type="button" class="secondary-button compact-button" data-email-sender-action="allowed" data-email-sender-address="${escapeHtml(sender.sender_address)}">Allow &amp; sync</button><button type="button" class="text-button" data-email-sender-action="disabled" data-email-sender-address="${escapeHtml(sender.sender_address)}">Ignore</button>`;
    return `<article class="email-sender-review-row" data-email-sender-row="${escapeHtml(sender.sender_address)}">
      <div><div class="email-sender-review-title"><strong>${escapeHtml(sender.sender_address)}</strong>${badge}</div><span>${Number(sender.message_count || 0)} message${Number(sender.message_count || 0) === 1 ? "" : "s"} waiting · first seen ${escapeHtml(formatDate(sender.first_seen_at))}</span>${detail}</div>
      <div>${actions}</div>
    </article>`;
  }).join("");
}

function pendingSenderMessageTotal(connection) {
  return (connection?.pending_senders || []).reduce(
    (total, sender) => total + Number(sender.message_count || 0),
    0,
  );
}

function pendingSenderSyncSummary(newCount, connection) {
  const total = pendingSenderMessageTotal(connection);
  if (!total) {
    return newCount
      ? `${newCount} excluded by source policy`
      : "0 awaiting sender approval";
  }
  if (!newCount) return `${total} still awaiting sender approval`;
  if (newCount === total) return `${total} awaiting sender approval`;
  return `${newCount} new · ${total} total awaiting sender approval`;
}

function applicationOutcomeResolution(item) {
  return {
    application_updated: { label: "Saved application updated", tone: "is-linked" },
    application_already_current: { label: "Already recorded", tone: "is-linked" },
    discovery_dismissed: { label: "Removed from active Feed · not linked", tone: "is-unlinked" },
    unmatched: { label: "Not linked to a saved application", tone: "is-unlinked" },
    status_conflict: { label: "Needs status review", tone: "is-warning" },
    ambiguous: { label: "Needs identity review", tone: "is-warning" },
  }[item?.resolution] || { label: "Recorded", tone: "is-unlinked" };
}

function applicationOutcomeStatusLabel(value) {
  return { applied: "Applied", rejected: "Rejected" }[value] || value || "Updated";
}

function renderEmailConnection() {
  const connection = state.emailConnection || {};
  const unavailable = api.capabilities.email_connection === false;
  const reconnect = emailReconnectRequired(connection);
  const status = emailConnectionStatus(connection);
  el.emailStatus.className = `email-sync-status ${status.tone}`;
  el.emailStatus.querySelector("span").textContent = status.label;
  const receipts = connection.receipts || {};
  const sync = connection.sync || {};
  const jobSenders = allowedConnectionSenders(connection, "job_alerts");
  const applicationSenders = allowedConnectionSenders(
    connection,
    "application_tracking",
  );
  el.emailSyncFacts.innerHTML = `<div><span>Last sync</span><strong>${escapeHtml(formatDateTime(sync.last_completed_at))}</strong></div>
    <div><span>Imported emails</span><strong>${Number(receipts.imported || 0).toLocaleString()}</strong></div>
    <div><span>Job senders</span><strong>${jobSenders.length.toLocaleString()}</strong></div>
    <div><span>Tracking senders</span><strong>${applicationSenders.length.toLocaleString()}</strong></div>
    <div><span>Awaiting approval</span><strong>${pendingSenderMessageTotal(connection).toLocaleString()}</strong></div>`;
  el.emailConnectionSummary.textContent = connection.configured
    ? `${connection.account_address} · ${connection.folder} · ${jobSenders.length} job · ${applicationSenders.length} tracking · ${connection.schedule_enabled ? `auto every ${Number(connection.interval_minutes || 30)} min` : "manual only"}`
    : "Add a dedicated Gmail account and exact sender addresses.";
  if (reconnect && !unavailable) {
    el.emailConnectionSummary.textContent = `${connection.account_address} · Re-enter an App Password to bind this workspace. The previous credential will not be read or removed.`;
  }
  const loadingLabel = state.emailLoading
    ? "Loading mailbox configuration…"
    : state.emailSyncing
      ? "Checking approved messages…"
      : state.emailSaving && !state.emailSenderOperation
        ? reconnect ? "Reconnecting this workspace…" : "Saving connection settings…"
        : "";
  el.emailSyncProgress.classList.toggle("hidden", !loadingLabel);
  el.emailSyncProgress.innerHTML = loadingLabel
    ? renderLoadingState({ label: loadingLabel })
    : "";
  el.emailAccount.disabled = unavailable || Boolean(connection.configured);
  el.emailPassword.disabled = unavailable || state.emailSaving || state.emailSyncing;
  el.emailPassword.required = reconnect || !connection.credential_present;
  el.emailPassword.placeholder = connection.credential_present && !reconnect
    ? "Leave blank to keep the current App Password"
    : "16-character App Password";
  const emailSyncOffline = isOffline();
  const emailSyncReady = connection.configured
    && !unavailable && !reconnect
    && connection.enabled
    && connection.credential_present
    && !state.emailSaving;
  el.emailSyncNow.disabled = emailSyncOffline
    || state.emailSyncStopRequested
    || (!state.emailSyncing && !emailSyncReady);
  el.emailSyncNow.dataset.tooltip = emailSyncOffline
    ? connectivityCopy.offlineAction
    : state.emailSyncing
      ? "Stop waiting in this window. The local mailbox check may still finish."
      : "Read unseen approved alerts without changing read state or moving messages.";
  el.emailSyncNow.textContent = state.emailSyncStopRequested
    ? "Stopping…"
    : state.emailSyncing ? "Stop waiting" : "Sync now";
  el.emailConnectionSave.disabled = unavailable || state.emailSaving || state.emailSyncing;
  el.emailConnectionSave.textContent = state.emailSaving && !state.emailSenderOperation
    ? reconnect ? "Reconnecting…" : "Saving…"
    : reconnect ? "Reconnect" : connection.configured ? "Save settings" : "Save connection";
  el.emailConnectionSave.dataset.tooltip = reconnect
    ? "Confirm a new credential for this workspace; the previous credential is left untouched."
    : "Save mailbox, sender, and scheduling settings on this device.";
  if (reconnect) el.emailScheduleEnabled.checked = false;
  el.emailScheduleEnabled.disabled = unavailable || reconnect || state.emailSaving || state.emailSyncing;
  el.emailSyncInterval.disabled = unavailable || reconnect || !el.emailScheduleEnabled.checked || state.emailSaving || state.emailSyncing;
  el.emailDisconnect.classList.toggle("hidden", !connection.configured);
  el.emailDisconnect.disabled = unavailable || reconnect || state.emailSaving || state.emailSyncing;
  el.emailRescanRecent.classList.toggle(
    "hidden",
    !connection.configured || (!sync.uidvalidity_known && !Number(sync.last_uid || 0)),
  );
  el.emailForget.classList.toggle("hidden", !connection.configured);
  el.emailForget.disabled = unavailable || reconnect || state.emailSaving || state.emailSyncing;
  el.emailRescanRecent.disabled = unavailable || reconnect || state.emailSaving || state.emailSyncing;
  renderSenderDrafts();
  renderPendingSenders(connection);
}

export function stopWaitingForEmailSync() {
  const controller = state.emailSyncAbortController;
  if (!controller || controller.signal.aborted) return;
  state.emailSyncStopRequested = true;
  controller.abort();
  setEmailMessage("Stopping the wait…", "warning");
  renderEmailConnection();
}

export async function loadEmailConnection({ preserveDraft = null } = {}) {
  if (state.emailLoading) return;
  state.emailLoading = true;
  renderEmailConnection();
  try {
    if (!await api.hasEmailConnection()) {
      setEmailMessage("Email sync is not available in this Go migration preview. Your existing mailbox settings have not been changed.", "warning");
      return;
    }
    useEmailConnection(await api.getEmailConnection(), {
      preserveDraft: preserveDraft ?? state.emailFormDirty,
    });
    setEmailMessage();
  } catch (error) {
    setEmailMessage(formatRequestError(error), "error");
  } finally {
    state.emailLoading = false;
    renderEmailConnection();
  }
}

function addSourceValue(rawValue, purpose) {
  const address = String(rawValue || "").trim().toLocaleLowerCase();
  if (!address) return true;
  if (!/^[^@\s<>]+@[^@\s<>]+$/.test(address)) {
    setEmailMessage(`Enter one exact sender address, not “${address}”.`, "error");
    return false;
  }
  const routes = purpose === "both"
    ? ["job_alerts", "application_tracking"]
    : [purpose];
  state.senderDraft = {
    ...state.senderDraft,
    ...Object.fromEntries(routes.map((route) => [
      route,
      [...new Set([...(state.senderDraft[route] || []), address])].sort(),
    ])),
  };
  state.emailSourceFocus = address;
  state.emailFormDirty = true;
  el.emailSourceAddress.value = "";
  setEmailMessage();
  renderSenderDrafts();
  return true;
}

async function inspectEmailQualitySample(discoveryId) {
  const normalized = String(discoveryId || "").trim();
  if (!normalized || /[\\/]/.test(normalized)) return;
  setMode("feed");
  setDiscoveryVisibility("active");
  state.routeTargetId = normalized;
  state.routeIntent = null;
  state.selectedId = normalized;
  state.detail = null;
  resetFieldEditor();
  publishSelectedDiscovery(normalized);
  await loadDiscoveries();
  el.detail?.scrollIntoView?.({ block: "start", behavior: "smooth" });
}

async function saveEmailConnection(event) {
  event.preventDefault();
  if (api.capabilities.email_connection === false || state.emailSaving || state.emailSyncing) return;
  if (!state.senderDraft.job_alerts.length && !state.senderDraft.application_tracking.length) {
    setEmailMessage("Add at least one exact sender before saving.", "error");
    el.emailSourceAddress.focus();
    return;
  }
  const reconnect = emailReconnectRequired();
  if (reconnect && !el.emailPassword.value.trim()) {
    setEmailMessage("Enter an App Password to reconnect this workspace.", "error");
    el.emailPassword.focus();
    return;
  }
  if (reconnect && !window.confirm(
    `Reconnect ${state.emailConnection.account_address} in this workspace? A new local credential will be stored. The previous credential will not be read or removed. Automatic sync will stay off; use Sync now to test the connection.`,
  )) return;
  // Rendering disables the password input while this exact request is pending.
  const appPassword = el.emailPassword.value || null;
  state.emailSaving = true;
  renderEmailConnection();
  setEmailMessage(reconnect ? "Reconnecting this workspace…" : "Saving the local connection…");
  const diagnosticStartedAt = Date.now();
  const diagnosticRunId = createEmailDiagnosticRunId();
  const requestController = new AbortController();
  const requestTimeout = window.setTimeout(
    () => requestController.abort(),
    EMAIL_CONNECTION_SAVE_TIMEOUT_MS,
  );
  try {
    const connection = await api.configureEmailConnection({
      accountAddress: el.emailAccount.value,
      appPassword,
      reconnect,
      senderAddresses: state.senderDraft.job_alerts,
      applicationSenderAddresses: state.senderDraft.application_tracking,
      scheduleEnabled: reconnect ? false : el.emailScheduleEnabled.checked,
      intervalMinutes: Number(el.emailSyncInterval.value),
      expectedRevision: Number(state.emailConnection?.revision || 0),
    }, {
      signal: requestController.signal,
      headers: { [EMAIL_DIAGNOSTIC_RUN_HEADER]: diagnosticRunId },
    });
    useEmailConnection(connection, { preserveDraft: false });
    setEmailMessage("Connection saved locally. Use Sync now to test the App Password and import alerts.", "success");
  } catch (error) {
    if (["EMAIL_CONNECTION_REVISION_CONFLICT", "EMAIL_CREDENTIAL_RECONNECT_NOT_REQUIRED"].includes(error?.payload?.error_code)) {
      await loadEmailConnection();
    }
    if (
      error?.name === "AbortError"
      || error?.name === "TimeoutError"
      || error?.message?.includes("timed out")
    ) {
      const summary = "Career Hub did not answer within 15 seconds. The save was not confirmed.";
      const suggestion = "Restart Career Hub before retrying. After restart, first check whether the connection was saved.";
      reportEmailConnectionClientFailure({
        runId: diagnosticRunId,
        startedAt: diagnosticStartedAt,
        errorCode: "EMAIL_CONNECTION_SAVE_RESPONSE_TIMEOUT",
        summary,
        suggestion,
      });
      setEmailMessage(
        summary,
        "error",
        {
          detail: suggestion,
          errorCode: "EMAIL_CONNECTION_SAVE_RESPONSE_TIMEOUT",
          runId: diagnosticRunId,
          diagnostics: true,
        },
      );
    } else if (!error?.payload?.error_code && error instanceof TypeError) {
      const summary = "Career Hub could not reach its local service. The save was not confirmed.";
      const suggestion = "Restart Career Hub before retrying. After restart, first check whether the connection was saved.";
      reportEmailConnectionClientFailure({
        runId: diagnosticRunId,
        startedAt: diagnosticStartedAt,
        errorCode: "EMAIL_CONNECTION_SAVE_TRANSPORT_FAILED",
        summary,
        suggestion,
      });
      setEmailMessage(
        summary,
        "error",
        {
          detail: suggestion,
          errorCode: "EMAIL_CONNECTION_SAVE_TRANSPORT_FAILED",
          runId: diagnosticRunId,
          diagnostics: true,
        },
      );
    } else {
      setEmailRequestError(error, diagnosticRunId);
    }
  } finally {
    window.clearTimeout(requestTimeout);
    if (reconnect) el.emailPassword.value = "";
    state.emailSaving = false;
    renderEmailConnection();
  }
}

function countLabel(count, singular, plural = `${singular}s`) {
  return `${count.toLocaleString()} ${count === 1 ? singular : plural}`;
}

function emailSyncResultSummary(counts, connection, hasMore) {
  const imported = Number(counts.messages_imported || 0);
  const jobs = Number(counts.discoveries_written || 0);
  const duplicates = Number(counts.messages_duplicate || 0);
  const withoutCandidates = Number(counts.messages_without_candidates || 0);
  const filtered = Number(counts.messages_filtered_subject || 0);
  const applicationUpdates = Number(counts.application_outcomes_found || 0);
  const unknownSenders = Number(counts.messages_unknown_sender || 0);
  const parts = [
    countLabel(imported, "email imported", "emails imported"),
    countLabel(jobs, "job card added", "job cards added"),
  ];
  if (duplicates) parts.push(`${duplicates.toLocaleString()} already known`);
  if (applicationUpdates) {
    parts.push(countLabel(applicationUpdates, "application update"));
  }
  if (withoutCandidates) {
    parts.push(`${withoutCandidates.toLocaleString()} without a job or application update`);
  }
  if (filtered) parts.push(`${filtered.toLocaleString()} filtered by subject rules`);
  if (unknownSenders || pendingSenderMessageTotal(connection)) {
    parts.push(pendingSenderSyncSummary(unknownSenders, connection));
  }
  if (hasMore) parts.push("more messages remain");
  return parts.join(" · ");
}

function emailSyncIssueDetail(counts) {
  const failed = Number(counts.messages_failed || 0);
  const retryPending = Number(counts.messages_retry_pending || 0);
  const failedFinal = Number(counts.messages_failed_final || 0);
  const tooLarge = Number(counts.messages_too_large || 0);
  const unclassified = Math.max(0, failed - retryPending - failedFinal);
  const parts = [];
  if (retryPending) {
    parts.push(countLabel(retryPending, "message will retry", "messages will retry"));
  }
  if (failedFinal) {
    parts.push(countLabel(failedFinal, "message was skipped after repeated failures", "messages were skipped after repeated failures"));
  }
  if (tooLarge) parts.push(countLabel(tooLarge, "oversized message was skipped", "oversized messages were skipped"));
  if (unclassified) parts.push(countLabel(unclassified, "message needs import review", "messages need import review"));
  return `Gmail connected and newer messages were still checked. ${parts.join("; ")}.`;
}

async function syncEmailNow({ automatic = false } = {}) {
  if (state.emailSyncing || state.emailSaving) return;
  if (isOffline()) {
    if (!automatic) setEmailMessage(connectivityCopy.offline, "warning");
    return;
  }
  state.emailSyncing = true;
  state.emailSyncAbortController = new AbortController();
  state.emailSyncStopRequested = false;
  const diagnosticRunId = createEmailDiagnosticRunId();
  renderEmailConnection();
  if (!automatic) setEmailMessage("Checking unseen messages from approved senders…");
  try {
    const result = await api.syncEmailConnection(
      automatic ? "scheduled" : "manual",
      {
        signal: state.emailSyncAbortController.signal,
        headers: { [EMAIL_DIAGNOSTIC_RUN_HEADER]: diagnosticRunId },
      },
    );
    reportInternetActionSuccess();
    useEmailConnection(result.connection, { preserveDraft: state.emailFormDirty });
    if (result.status === "disabled" || result.status === "deferred") {
      if (!automatic) {
        setEmailMessage(
          result.status === "deferred"
            ? `Automatic retry is paused until ${formatDateTime(result.deferred_until)}. Manual Sync now remains available.`
            : "Automatic sync is disabled. Manual Sync now remains available.",
          "warning",
        );
      }
      return;
    }
    const counts = result.counts || {};
    const summary = emailSyncResultSummary(counts, result.connection, result.has_more);
    if (result.status === "partial") {
      setEmailMessage(
        `${automatic ? "Automatic sync" : "Sync"} incomplete: ${summary}.`,
        "warning",
        {
          detail: emailSyncIssueDetail(counts),
          errorCode: result.error_code || "EMAIL_SYNC_MESSAGE_RETRY",
          runId: result.run_id || diagnosticRunId,
          diagnostics: true,
        },
      );
    } else {
      setEmailMessage(
        automatic ? `Automatic sync complete: ${summary}.` : `Sync complete: ${summary}.`,
        "success",
      );
    }
    const applicationUpdates = Number(counts.application_outcomes_found || 0);
    if (Number(counts.discoveries_written || 0) || Number(counts.discoveries_dismissed || 0)) {
      await loadDiscoveries();
    }
    if (applicationUpdates) {
      window.dispatchEvent(new CustomEvent("careerhub:applicationoutcomeschanged"));
    }
  } catch (error) {
    if (error?.name === "AbortError" || error?.message?.includes("aborted")) {
      setEmailMessage(
        "Stopped waiting. The local mailbox check may still finish; refresh before starting another sync.",
        "warning",
        { runId: diagnosticRunId, diagnostics: true },
      );
      return;
    }
    reportInternetActionFailure(error);
    if (automatic && ["EMAIL_SYNC_ALREADY_RUNNING", "EMAIL_SYNC_BACKOFF_ACTIVE"].includes(error?.payload?.error_code)) return;
    await loadEmailConnection();
    setEmailRequestError(error, diagnosticRunId);
  } finally {
    state.emailSyncAbortController = null;
    state.emailSyncStopRequested = false;
    state.emailSyncing = false;
    renderEmailConnection();
  }
}

async function initializeEmailConnector() {
  await loadEmailConnection();
}

async function disconnectEmail() {
  const connection = state.emailConnection;
  if (!connection?.configured || state.emailSaving || state.emailSyncing) return;
  if (!window.confirm("Disconnect this mailbox? Imported opportunities and dedup receipts will stay on this device.")) return;
  state.emailSaving = true;
  renderEmailConnection();
  try {
    useEmailConnection(
      await api.disconnectEmailConnection(connection.revision),
      { preserveDraft: false },
    );
    setEmailMessage("Mailbox disconnected. Existing Job Feed items were kept.", "success");
  } catch (error) {
    if (error?.payload?.error_code === "EMAIL_SYNC_ALREADY_RUNNING") {
      setEmailMessage("A mailbox sync is still running. Wait for it to finish, then disconnect.", "warning");
    } else {
      setEmailMessage(formatRequestError(error), "error");
    }
  } finally {
    state.emailSaving = false;
    renderEmailConnection();
  }
}

async function rescanRecentEmail() {
  const connection = state.emailConnection;
  if (!connection?.configured || state.emailSaving || state.emailSyncing) return;
  if (!window.confirm("Rescan recent email? Career Hub will recheck the last 14 days of approved senders. Existing Job Feed cards will not duplicate.")) return;
  state.emailSaving = true;
  renderEmailConnection();
  try {
    useEmailConnection(await api.resetEmailCursor(connection.revision));
    setEmailMessage("Recent email will be rechecked on the next sync. Existing cards will not duplicate.", "success");
  } catch (error) {
    if (error?.payload?.error_code === "EMAIL_SYNC_ALREADY_RUNNING") {
      setEmailMessage("A mailbox sync is still running. Wait for it to finish, then rescan.", "warning");
    } else {
      setEmailMessage(formatRequestError(error), "error");
    }
  } finally {
    state.emailSaving = false;
    renderEmailConnection();
  }
}

async function forgetEmailConnection() {
  const connection = state.emailConnection;
  if (!connection?.configured || state.emailSaving || state.emailSyncing) return;
  if (!window.confirm("Forget this email setup? This permanently removes the mailbox address, App Password, approved senders, sync position, and local email receipts from Career Hub. Existing Job Feed cards, saved jobs, and applications stay. To use email sync again, you must complete the full mailbox setup again.")) return;
  state.emailSaving = true;
  renderEmailConnection();
  try {
    useEmailConnection(
      await api.forgetEmailConnection(connection.revision),
      { preserveDraft: false },
    );
    setEmailMessage("Email setup forgotten. Existing Job Feed items were kept.", "success");
  } catch (error) {
    if (error?.payload?.error_code === "EMAIL_SYNC_ALREADY_RUNNING") {
      setEmailMessage("A mailbox sync is still running. Wait for it to finish, then forget this setup.", "warning");
    } else {
      setEmailMessage(formatRequestError(error), "error");
    }
  } finally {
    state.emailSaving = false;
    renderEmailConnection();
  }
}

async function updatePendingSender(address, disposition, purpose) {
  const connection = state.emailConnection;
  if (!connection?.configured || state.emailSaving || state.emailSyncing) return;
  state.emailSaving = true;
  state.emailSenderOperation = {
    address,
    label: disposition === "allowed" ? "Allowing sender…" : "Ignoring sender…",
  };
  renderEmailConnection();
  setEmailMessage(disposition === "allowed" ? "Allowing sender…" : "Ignoring sender…");
  try {
    useEmailConnection(await api.setEmailSender(
      address,
      disposition,
      purpose,
      connection.revision,
    ));
    if (disposition === "allowed") {
      setEmailMessage("Sender allowed. Importing its retained messages…");
      state.emailSaving = false;
      await syncEmailNow();
      return;
    }
    setEmailMessage("Sender ignored. Its message bodies were not fetched.", "success");
  } catch (error) {
    if (error?.payload?.error_code === "EMAIL_CONNECTION_REVISION_CONFLICT") {
      await loadEmailConnection();
    }
    setEmailMessage(formatRequestError(error), "error");
  } finally {
    state.emailSaving = false;
    state.emailSenderOperation = null;
    renderEmailConnection();
  }
}

export function validateEmailFiles(files) {
  const selected = Array.from(files || []);
  if (!selected.length) return { files: [], error: "Choose at least one .eml file." };
  if (selected.length > MAX_FILES) return { files: [], error: "Choose no more than 10 .eml files at once." };
  const invalid = selected.find((file) => !String(file.name || "").toLocaleLowerCase().endsWith(".eml"));
  if (invalid) return { files: [], error: `${invalid.name || "One file"} is not an .eml file.` };
  const oversized = selected.find((file) => Number(file.size || 0) > MAX_FILE_BYTES);
  if (oversized) return { files: [], error: `${oversized.name} exceeds the 10 MB per-file limit.` };
  const total = selected.reduce((sum, file) => sum + Number(file.size || 0), 0);
  if (total > MAX_TOTAL_BYTES) return { files: [], error: "Selected files exceed the 25 MB total import limit." };
  return { files: selected, error: "" };
}

function selectEmailFiles(files) {
  const validation = validateEmailFiles(files);
  state.pendingFiles = validation.files;
  el.importMessage.className = validation.error ? "form-message error" : "form-message";
  el.importMessage.textContent = validation.error;
  el.importSelection.textContent = state.pendingFiles.length
    ? `${state.pendingFiles.length} file${state.pendingFiles.length === 1 ? "" : "s"} selected`
    : "No files selected";
  el.importButton.disabled = !state.pendingFiles.length || state.importing;
}

function fileAsBase64(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.addEventListener("load", () => {
      const value = String(reader.result || "");
      const separator = value.indexOf(",");
      if (separator < 0) reject(new Error("The selected email file could not be encoded."));
      else resolve(value.slice(separator + 1));
    });
    reader.addEventListener("error", () => reject(new Error("The selected email file could not be read.")));
    reader.readAsDataURL(file);
  });
}

function renderImportResult(result) {
  const summary = result.summary || {};
  el.importResults.classList.remove("hidden");
  const outcomeCount = Number(summary.application_outcomes_found || 0);
  el.importSummary.textContent = `${Number(summary.candidates_found || 0)} candidate${Number(summary.candidates_found || 0) === 1 ? "" : "s"} · ${Number(summary.discoveries_created || 0)} new · ${Number(summary.discoveries_duplicate || 0)} already known${outcomeCount ? ` · ${outcomeCount} application update${outcomeCount === 1 ? "" : "s"}` : ""}`;
  el.importResultList.innerHTML = (result.files || []).map((file) => {
    const labels = {
      imported: "Imported",
      partial: "Partially imported",
      failed: "Failed",
      no_candidates: "No candidates",
    };
    const candidates = (file.discoveries || []).map((candidate) => candidate.outcome === "failed"
      ? `<li class="email-import-candidate is-error">Candidate ${escapeHtml(candidate.ordinal)} could not be stored.</li>`
      : `<li class="email-import-candidate"><strong>${escapeHtml(candidate.title || "Untitled opportunity")}</strong><span>${escapeHtml(candidate.company || "Company not stated")} · ${escapeHtml(candidate.outcome)}</span></li>`).join("");
    const outcomes = (file.application_outcomes || []).map((outcome) => outcome.outcome === "failed"
      ? '<li class="email-import-candidate is-error">Application update could not be stored.</li>'
      : `<li class="email-import-candidate"><strong>${escapeHtml(outcome.title || "Untitled application")}</strong><span>${escapeHtml(outcome.company || "Company not stated")} · ${escapeHtml(localizedUiText(applicationOutcomeStatusLabel(outcome.application_status)))} · ${escapeHtml(applicationOutcomeResolution(outcome).label)}</span></li>`).join("");
    return `<article class="email-import-result is-${escapeHtml(file.status)}">
      <div><strong>${escapeHtml(file.file_name)}</strong><span>${escapeHtml(labels[file.status] || file.status)} · ${Number(file.candidate_count || 0)} candidate${Number(file.candidate_count || 0) === 1 ? "" : "s"}</span></div>
      ${file.error ? `<p>${escapeHtml(file.error.message || "Import failed.")}</p>` : ""}
      ${candidates || outcomes ? `<ul>${candidates}${outcomes}</ul>` : ""}
    </article>`;
  }).join("");
}

async function importFiles(event) {
  event.preventDefault();
  if (!state.pendingFiles.length || state.importing) return;
  state.importing = true;
  el.importButton.disabled = true;
  el.importMessage.className = "form-message";
  el.importMessage.textContent = "Reading and parsing locally…";
  try {
    const files = await Promise.all(state.pendingFiles.map(async (file) => ({
      file_name: file.name,
      content_base64: await fileAsBase64(file),
    })));
    const result = await api.importEmailFiles(files);
    renderImportResult(result);
    const created = Number(result.summary?.discoveries_created || 0);
    const updated = Number(result.summary?.discoveries_updated || 0);
    el.importMessage.className = "form-message success";
    el.importMessage.textContent = created + updated === 1
      ? "1 opportunity added or refreshed in the Job Feed."
      : `${created + updated} opportunities added or refreshed in the Job Feed.`;
    state.pendingFiles = [];
    el.importFiles.value = "";
    el.importSelection.textContent = "No files selected";
    await loadDiscoveries();
  } catch (error) {
    el.importMessage.className = "form-message error";
    el.importMessage.textContent = formatRequestError(error);
  } finally {
    state.importing = false;
    el.importButton.disabled = !state.pendingFiles.length;
  }
}

export function initDiscoveries() {
  Object.assign(el, {
    modeButtons: [...document.querySelectorAll("[data-discovery-mode]")],
    surfaces: [...document.querySelectorAll("[data-discovery-surface]")],
    search: document.querySelector("#discovery-search"),
    sourceFilter: document.querySelector("#discovery-source-filter"),
    queueFilter: document.querySelector("#discovery-queue-filter"),
    observedFilter: document.querySelector("#discovery-observed-filter"),
    sort: document.querySelector("#discovery-sort"),
    archivedFilter: document.querySelector("#discovery-archived-filter"),
    archivedCount: document.querySelector("#discovery-archived-count"),
    flushArchived: document.querySelector("#discovery-flush-archived"),
    resultCount: document.querySelector("#discovery-result-count"),
    message: document.querySelector("#discovery-message"),
    list: document.querySelector("#discovery-list"),
    detail: document.querySelector("#discovery-detail"),
    workspace: document.querySelector(".job-feed-workspace"),
    pagination: document.querySelector("#discovery-pagination"),
    pageSize: document.querySelector("#discovery-page-size"),
    pageInfo: document.querySelector("#discovery-page-info"),
    pageNav: document.querySelector("#discovery-page-nav"),
    firstPage: document.querySelector("#discovery-first-page"),
    prevPage: document.querySelector("#discovery-prev-page"),
    pageNumbers: document.querySelector("#discovery-page-numbers"),
    nextPage: document.querySelector("#discovery-next-page"),
    lastPage: document.querySelector("#discovery-last-page"),
    importForm: document.querySelector("#email-import-form"),
    importDropzone: document.querySelector("#email-import-dropzone"),
    importFiles: document.querySelector("#email-import-files"),
    importButton: document.querySelector("#email-import-button"),
    importSelection: document.querySelector("#email-import-selection"),
    importMessage: document.querySelector("#email-import-message"),
    importResults: document.querySelector("#email-import-results"),
    importSummary: document.querySelector("#email-import-summary"),
    importResultList: document.querySelector("#email-import-result-list"),
    emailSyncCard: document.querySelector("#email-sync-card"),
    emailStatus: document.querySelector("#email-sync-status"),
    emailSyncNow: document.querySelector("#email-sync-now"),
    emailSyncFacts: document.querySelector("#email-sync-facts"),
    emailSyncMessage: document.querySelector("#email-sync-message"),
    emailSyncProgress: document.querySelector("#email-sync-progress"),
    emailSenderReview: document.querySelector("#email-sender-review"),
    emailSenderReviewCount: document.querySelector("#email-sender-review-count"),
    emailSenderReviewList: document.querySelector("#email-sender-review-list"),
    emailConnectionSettings: document.querySelector("#email-connection-settings"),
    emailConnectionSummary: document.querySelector("#email-connection-summary"),
    emailConnectionForm: document.querySelector("#email-connection-form"),
    emailAccount: document.querySelector("#email-account-address"),
    emailPassword: document.querySelector("#email-app-password"),
    emailSourceAddress: document.querySelector("#email-source-address"),
    emailSourceRoute: document.querySelector("#email-source-route"),
    emailSourceAdd: document.querySelector("#email-source-add"),
    emailSourceCount: document.querySelector("#email-source-count"),
    emailSourceQualityIndicator: document.querySelector("#email-source-quality-indicator"),
    emailSourceQualityCount: document.querySelector("#email-source-quality-count"),
    emailSourceWorkspace: document.querySelector("#email-source-policy-workspace"),
    emailSourcePolicyList: document.querySelector("#email-source-policy-list"),
    emailSourcePolicyEditor: document.querySelector("#email-source-policy-editor"),
    emailScheduleEnabled: document.querySelector("#email-schedule-enabled"),
    emailSyncInterval: document.querySelector("#email-sync-interval"),
    emailConnectionSave: document.querySelector("#email-connection-save"),
    emailDisconnect: document.querySelector("#email-connection-disconnect"),
    emailRescanRecent: document.querySelector("#email-rescan-recent"),
    emailForget: document.querySelector("#email-connection-forget"),
  });
  state.mode = readStoredMode();
  onConnectivityChange(() => {
    renderEmailConnection();
    renderDetail();
  });
  setMode(state.mode, { persist: false });
  el.modeButtons.forEach((button) => button.addEventListener("click", () => {
    setMode(button.dataset.discoveryMode);
    if (state.mode === "email" && !state.emailConnection) void loadEmailConnection();
  }));
  window.addEventListener("careerhub:localechange", renderRows);
  if (el.pageSize) {
    el.pageSize.value = String(state.pageSize);
    el.pageSize.addEventListener("change", () => {
      state.pageSize = Number(el.pageSize.value) || DEFAULT_PAGE_SIZE;
      try { window.localStorage.setItem(PAGE_SIZE_KEY, String(state.pageSize)); } catch { /* ignore */ }
      state.currentPage = 1;
      resetDiscoverySelectionForQuery();
      void loadDiscoveries();
    });
  }
  if (el.firstPage) {
    el.firstPage.addEventListener("click", () => {
      void goToDiscoveryPage(1);
    });
  }
  if (el.prevPage) {
    el.prevPage.addEventListener("click", () => {
      void goToDiscoveryPage(state.currentPage - 1);
    });
  }
  if (el.nextPage) {
    el.nextPage.addEventListener("click", () => {
      void goToDiscoveryPage(state.currentPage + 1);
    });
  }
  if (el.lastPage) {
    el.lastPage.addEventListener("click", () => {
      void goToDiscoveryPage(Math.ceil(state.total / state.pageSize));
    });
  }
  if (el.pageNumbers) {
    el.pageNumbers.addEventListener("click", (event) => {
      const btn = event.target.closest("[data-page]");
      if (!btn) return;
      const pageNum = Number(btn.dataset.page);
      if (pageNum && pageNum !== state.currentPage) {
        void goToDiscoveryPage(pageNum);
      }
    });
  }
  el.search.addEventListener("input", () => {
    state.currentPage = 1;
    resetDiscoverySelectionForQuery();
    if (state.searchTimer !== null) window.clearTimeout(state.searchTimer);
    state.searchTimer = window.setTimeout(() => {
      state.searchTimer = null;
      void loadDiscoveries();
    }, 250);
  });
  [
    el.sourceFilter,
    el.queueFilter,
    el.observedFilter,
    el.sort,
  ].forEach((node) => node.addEventListener("change", () => {
    state.currentPage = 1;
    resetDiscoverySelectionForQuery();
    void loadDiscoveries();
  }));
  el.archivedFilter.addEventListener("click", () => {
    setDiscoveryVisibility(
      state.visibility === "archived" ? "active" : "archived",
    );
    state.routeTargetId = null;
    state.routeIntent = null;
    state.selectedId = null;
    state.detail = null;
    state.mobileDetailOpen = false;
    resetFieldEditor();
    state.currentPage = 1;
    void loadDiscoveries();
  });
  if (el.flushArchived) {
    el.flushArchived.addEventListener("click", () => {
      void flushArchived();
    });
  }
  el.list.addEventListener("click", (event) => {
    const row = event.target.closest("[data-discovery-id]");
    if (!row) return;
    state.selectedId = row.dataset.discoveryId;
    state.routeTargetId = state.selectedId;
    state.routeIntent = null;
    state.detail = null;
    state.mobileDetailOpen = true;
    resetFieldEditor();
    publishSelectedDiscovery(state.selectedId);
    renderRows();
    void loadDetail(state.selectedId).then(() => {
      if (window.matchMedia("(max-width: 1050px)").matches) {
        el.detail?.scrollIntoView?.({ block: "start", behavior: "smooth" });
      }
    });
  });
  el.detail.addEventListener("click", (event) => {
    if (event.target.closest("[data-discovery-back]")) {
      state.mobileDetailOpen = false;
      renderRows();
      el.list.querySelector(`[data-discovery-id="${CSS.escape(state.selectedId || "")}"]`)?.focus();
      return;
    }
    const contactCandidate = event.target.closest("[data-save-contact-candidate]");
    if (contactCandidate) {
      void openContactCandidateConfirmation({
        sourceKind: contactCandidate.dataset.contactSourceKind,
        sourceId: contactCandidate.dataset.contactSourceId,
        candidateId: contactCandidate.dataset.contactCandidateId,
        contentSha256: contactCandidate.dataset.contactContentSha256,
      }, { trigger: contactCandidate });
      return;
    }
    if (event.target.closest("[data-discovery-fields-edit]")) {
      beginFieldEdit();
      return;
    }
    if (event.target.closest("[data-discovery-fields-cancel]")) {
      cancelFieldEdit();
      return;
    }
    const reset = event.target.closest("[data-discovery-field-reset]");
    if (reset) {
      resetCorrectedField(reset.dataset.discoveryFieldReset);
      return;
    }
    const promotion = event.target.closest("[data-discovery-promote]");
    if (promotion) {
      void promoteSelectedDiscovery(promotion.dataset.discoveryPromote);
      return;
    }
    if (event.target.closest("[data-discovery-confirm-content]")) {
      void confirmSelectedDiscoveryContent();
      return;
    }
    if (event.target.closest("[data-discovery-review-later]")) {
      reviewSelectedDiscoveryLater();
      return;
    }
    if (event.target.closest("[data-discovery-delete]")) {
      void deleteSelectedDiscovery();
      return;
    }
    const savedJob = event.target.closest("[data-open-promoted-job]");
    if (savedJob) {
      openPromotedJob(savedJob.dataset.openPromotedJob, {
        focusMaterials: state.routeIntent === "prepare",
      });
      return;
    }
    const button = event.target.closest("[data-discovery-state]");
    if (button) void changeState(button.dataset.discoveryState);
  });
  el.detail.addEventListener("input", (event) => {
    const input = event.target.closest("[data-discovery-field-input]");
    if (!input || !state.fieldEditing || state.fieldSaving) return;
    const field = input.dataset.discoveryFieldInput;
    state.fieldDraft = { ...(state.fieldDraft || {}), [field]: input.value };
    state.fieldReset.delete(field);
    const origin = input.closest(".discovery-field-editor-row")?.querySelector(
      ".discovery-field-origin",
    );
    if (origin) {
      const edited = normalizeFieldValue(input.value) !== normalizeFieldValue(
        extractedValue(state.detail, field),
      );
      origin.textContent = edited ? "Edited by you" : "Extracted";
      origin.classList.toggle("is-edited", edited);
      origin.classList.toggle("is-extracted", !edited);
    }
  });
  el.detail.addEventListener("submit", (event) => {
    if (event.target.matches("[data-discovery-fields-form]")) {
      void saveFieldCorrections(event);
    }
  });
  el.importFiles.addEventListener("change", () => selectEmailFiles(el.importFiles.files));
  el.importForm.addEventListener("submit", importFiles);
  ["dragenter", "dragover"].forEach((name) => el.importDropzone.addEventListener(name, (event) => {
    event.preventDefault();
    el.importDropzone.classList.add("is-dragging");
  }));
  ["dragleave", "drop"].forEach((name) => el.importDropzone.addEventListener(name, (event) => {
    event.preventDefault();
    el.importDropzone.classList.remove("is-dragging");
  }));
  el.importDropzone.addEventListener("drop", (event) => selectEmailFiles(event.dataTransfer?.files));
  el.emailConnectionForm.addEventListener("submit", saveEmailConnection);
  el.emailSourceAdd.addEventListener("click", () => addSourceValue(
    el.emailSourceAddress.value,
    el.emailSourceRoute.value,
  ));
  el.emailSourceAddress.addEventListener("keydown", (event) => {
    if (event.key !== "Enter") return;
    event.preventDefault();
    addSourceValue(el.emailSourceAddress.value, el.emailSourceRoute.value);
  });
  el.emailSourceQualityIndicator.addEventListener("click", () => {
    const warned = new Set(captureQualityAlerts().map(
      (alert) => String(alert.sender_address || "").toLocaleLowerCase(),
    ));
    const first = draftSenderAddresses().find((address) => warned.has(address));
    if (!first) return;
    state.emailSourceFocus = first;
    renderSenderDrafts();
    el.emailSourcePolicyEditor.querySelector(".email-source-policy-card")?.focus();
  });
  el.emailSourceWorkspace.addEventListener("change", (event) => {
    const toggle = event.target.closest("[data-email-source-route-toggle]");
    if (!toggle) return;
    const address = toggle.dataset.emailSourceAddress;
    const route = toggle.dataset.emailSourceRoute;
    const next = toggle.checked
      ? [...new Set([...(state.senderDraft[route] || []), address])].sort()
      : (state.senderDraft[route] || []).filter((item) => item !== address);
    if (!next.length && !senderRouteEnabled(address, route === "job_alerts" ? "application_tracking" : "job_alerts")) {
      toggle.checked = true;
      setEmailMessage("Keep at least one route enabled, or remove the source.", "warning");
      return;
    }
    state.senderDraft = { ...state.senderDraft, [route]: next };
    state.emailFormDirty = true;
    setEmailMessage();
    renderSenderDrafts();
  });
  el.emailSourceWorkspace.addEventListener("click", (event) => {
    const qualitySample = event.target.closest("[data-email-quality-sample]");
    if (qualitySample) {
      void inspectEmailQualitySample(qualitySample.dataset.emailQualitySample);
      return;
    }
    const select = event.target.closest("[data-email-source-select]");
    if (select) {
      state.emailSourceFocus = select.dataset.emailSourceSelect;
      renderSenderDrafts();
      return;
    }
    const remove = event.target.closest("[data-email-source-remove]");
    if (!remove) return;
    const address = remove.dataset.emailSourceRemove;
    state.senderDraft = Object.fromEntries(Object.entries(state.senderDraft).map(
      ([route, values]) => [route, values.filter((item) => item !== address)],
    ));
    state.emailSourceFocus = "";
    state.emailFormDirty = true;
    renderSenderDrafts();
  });
  [el.emailAccount, el.emailPassword].forEach((node) => node.addEventListener("input", () => {
    state.emailFormDirty = true;
  }));
  el.emailScheduleEnabled.addEventListener("change", () => {
    state.emailFormDirty = true;
    renderEmailConnection();
  });
  el.emailSyncInterval.addEventListener("change", () => {
    state.emailFormDirty = true;
  });
  el.emailSyncNow.addEventListener("click", () => {
    if (state.emailSyncing) {
      stopWaitingForEmailSync();
    } else {
      void syncEmailNow();
    }
  });
  el.emailDisconnect.addEventListener("click", () => { void disconnectEmail(); });
  el.emailRescanRecent.addEventListener("click", () => { void rescanRecentEmail(); });
  el.emailForget.addEventListener("click", () => { void forgetEmailConnection(); });
  el.emailSenderReviewList.addEventListener("click", (event) => {
    const button = event.target.closest("[data-email-sender-action]");
    if (!button) return;
    const row = button.closest("[data-email-sender-row]");
    const purpose = row?.querySelector("[data-email-sender-purpose-select]")?.value
      || "job_alerts";
    void updatePendingSender(
      button.dataset.emailSenderAddress,
      button.dataset.emailSenderAction,
      purpose,
    );
  });
  renderEmailConnection();
  void initializeEmailConnector();
  renderRows();
}
