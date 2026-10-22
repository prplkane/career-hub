import {
  escapeHtml,
  formatTime,
  renderEmptyState,
  renderStatusBadge,
} from "../ui/primitives.js";

export const CLOSED_APPLICATION_STATUSES = new Set(["rejected", "withdrawn", "closed"]);
export const SAVED_APPLICATION_STATUSES = new Set(["captured"]);
export const LEGACY_APPLICATION_STATUSES = new Set(["reviewed"]);
export const CURRENT_APPLICATION_STATUSES = new Set([
  "captured",
  "preparing",
  "applied",
  "interview",
  "offer",
]);
export const APPLICATION_STATUS_ORDER = [
  "captured",
  "reviewed",
  "preparing",
  "applied",
  "interview",
  "offer",
  "rejected",
  "withdrawn",
  "closed",
];

export function hasApplicationRecord(application) {
  return Boolean(application && (
    CURRENT_APPLICATION_STATUSES.has(application.current_status)
    || CLOSED_APPLICATION_STATUSES.has(application.current_status)
  ));
}

// Compatibility name for job-library helpers that only need to know whether
// a persisted application record exists.
export const hasStartedApplication = hasApplicationRecord;

export function canManageApplicationMaterials(application) {
  return Boolean(application && (
    CURRENT_APPLICATION_STATUSES.has(application.current_status)
    || CLOSED_APPLICATION_STATUSES.has(application.current_status)
    || LEGACY_APPLICATION_STATUSES.has(application.current_status)
  ));
}

const APPLICATION_STATUS_LABELS = {
  captured: "Saved",
  reviewed: "Reviewed (legacy)",
  preparing: "Preparing",
  applied: "Applied",
  interview: "Interview",
  offer: "Offer",
  rejected: "Rejected",
  withdrawn: "Withdrawn",
  closed: "Closed",
};

export function applicationStatusLabel(status) {
  return APPLICATION_STATUS_LABELS[status] || "Saved";
}

export function applicationStatusOptions(selectedStatus) {
  const legacyOption = LEGACY_APPLICATION_STATUSES.has(selectedStatus)
    ? `<option value="${escapeHtml(selectedStatus)}" selected disabled>${escapeHtml(applicationStatusLabel(selectedStatus))}</option>`
    : "";
  return legacyOption + APPLICATION_STATUS_ORDER.map((status) => (
    `<option value="${status}"${status === selectedStatus ? " selected" : ""}>${escapeHtml(applicationStatusLabel(status))}</option>`
  )).join("");
}

function scopeMatches(application, scope) {
  if (!hasApplicationRecord(application)) return false;
  if (scope === "all") return true;
  if (scope === "closed") return CLOSED_APPLICATION_STATUSES.has(application.current_status);
  return CURRENT_APPLICATION_STATUSES.has(application.current_status);
}

export function filterApplicationJobs(jobs, { query = "", status = "", scope = "current" } = {}) {
  const normalizedQuery = query.trim().toLocaleLowerCase();
  return (jobs || []).filter((job) => {
    const application = job.application;
    if (!job.valid || !scopeMatches(application, scope)) return false;
    if (status && application.current_status !== status) return false;
    if (!normalizedQuery) return true;
    return [job.company, job.title, job.location, job.remote_policy]
      .filter(Boolean)
      .join(" ")
      .toLocaleLowerCase()
      .includes(normalizedQuery);
  }).sort((left, right) => {
    const leftTime = Date.parse(left.application?.updated_at || left.modified_at || 0);
    const rightTime = Date.parse(right.application?.updated_at || right.modified_at || 0);
    return rightTime - leftTime;
  });
}

/** Pure application-ledger row rendering. */
export function renderApplicationLedgerRows(jobs, { selectedJobId, drawerOpen } = {}) {
  if (!jobs.length) {
    return renderEmptyState({
      title: "No saved jobs match this view.",
      message: "Capture or review a job to see it here.",
      compact: true,
    });
  }
  return jobs.map((job) => {
    const application = job.application;
    const status = application.current_status;
    const hasLocation = Boolean(job.location);
    const location = job.location || "Location not stated";
    const workplace = job.remote_policy
      ? `<small class="application-workplace-badge">${escapeHtml(job.remote_policy)}</small>`
      : "";
    const updatedAt = application.updated_at || job.modified_at;
    const selected = drawerOpen && job.id === selectedJobId;
    const source = job.source_url
      ? `<a class="table-link" href="${escapeHtml(job.source_url)}" target="_blank" rel="noreferrer" data-network-action="external-link">Open posting</a>`
      : '<span class="muted-text application-source-missing">No posting link</span>';
    const role = [job.company, job.title].filter(Boolean).join(" ") || job.id;
    const submittedResume = application.applied_resume?.recorded
      ? application.applied_resume.resume
      : null;
    const submittedResumeSummary = submittedResume
      ? `<small class="application-row-resume" title="${escapeHtml(submittedResume.display_name)}">Submitted resume: ${escapeHtml(submittedResume.display_name)}</small>`
      : !["captured", "preparing"].includes(status)
        ? '<small class="application-row-resume is-unknown">Submitted resume: Not recorded</small>'
        : "";
    return `<div class="application-row${selected ? " is-selected" : ""}" role="listitem" data-application-job-id="${escapeHtml(job.id)}" tabindex="0">
      <div class="application-row-main"><strong${job.company ? ' translate="no"' : ""}>${escapeHtml(job.company || "Company not identified")}</strong><span${job.title ? ' translate="no"' : ""}>${escapeHtml(job.title || "Role not identified")}</span>${submittedResumeSummary}</div>
      <div class="application-row-stage"><select class="application-inline-status" data-application-job-id="${escapeHtml(job.id)}" data-previous-status="${escapeHtml(status)}" aria-label="Stage for ${escapeHtml(role)}">${applicationStatusOptions(status)}</select></div>
      <span class="application-row-location" title="${escapeHtml(location)}"${hasLocation ? ' translate="no"' : ""}>${escapeHtml(location)}${workplace}</span>
      <span class="application-row-activity">${escapeHtml(formatTime(updatedAt))}</span>
      <div class="application-row-actions">${source}<button class="secondary-button compact-button application-open-button" data-application-job-id="${escapeHtml(job.id)}" data-tooltip="Open application history and detailed status controls.">Details</button></div>
    </div>`;
  }).join("");
}

export function renderApplicationStatusBadge(status) {
  return renderStatusBadge({ label: applicationStatusLabel(status) });
}

export function applicationEmailUpdateResolution(item) {
  return {
    application_updated: { label: "Saved application updated", tone: "is-linked" },
    application_already_current: { label: "Already recorded", tone: "is-linked" },
    discovery_dismissed: { label: "Removed from active Feed · not linked", tone: "is-unlinked" },
    unmatched: { label: "Not linked to a saved application", tone: "is-unlinked" },
    status_conflict: { label: "Needs status review", tone: "is-warning" },
    ambiguous: { label: "Needs identity review", tone: "is-warning" },
  }[item?.resolution] || { label: "Recorded", tone: "is-unlinked" };
}

function emailUpdateApplicationOptions(jobs) {
  return (jobs || [])
    .filter((job) => job.valid && hasApplicationRecord(job.application))
    .map((job) => {
      const label = [
        job.company || "Company not identified",
        job.title || "Role not identified",
        applicationStatusLabel(job.application.current_status),
      ].join(" · ");
      return `<option value="${escapeHtml(job.id)}">${escapeHtml(label)}</option>`;
    })
    .join("");
}

export function renderApplicationEmailUpdateRows(items, jobs = []) {
  const applicationOptions = emailUpdateApplicationOptions(jobs);
  return (items || []).map((item) => {
    const resolution = applicationEmailUpdateResolution(item);
    const provider = {
      linkedin: "LinkedIn",
      handshake: "Handshake",
    }[item.source_provider] || item.source_provider || "Email";
    const linkable = ["unmatched", "discovery_dismissed", "ambiguous", "status_conflict"]
      .includes(item.resolution) && !item.matched_job_id && applicationOptions;
    const linker = linkable
      ? `<details class="application-email-update-linker">
          <summary>Link to an existing application</summary>
          <div>
            <select data-email-update-job aria-label="Application for ${escapeHtml(item.company || item.title || "email update")}">
              <option value="">Choose application…</option>${applicationOptions}
            </select>
            <button type="button" class="secondary-button compact-button" data-email-update-link data-outcome-id="${escapeHtml(item.outcome_id)}" data-outcome-revision="${Number(item.revision)}" disabled>Link update</button>
          </div>
          <small>Nothing is created automatically. Linking uses the status confirmed by this email.</small>
        </details>`
      : "";
    const title = item.title
      || (item.application_status === "applied" ? "Application submission" : "Untitled application");
    return `<article class="application-email-update-row">
      <div><strong>${escapeHtml(title)}</strong><span>${escapeHtml(item.company || "Company not stated")}${item.location ? ` · ${escapeHtml(item.location)}` : ""}</span>${linker}</div>
      <div class="application-email-update-state"><span class="application-email-outcome-badge${item.application_status === "applied" ? " is-applied" : ""}">${escapeHtml(applicationStatusLabel(item.application_status))}</span><small class="${escapeHtml(resolution.tone)}">${escapeHtml(resolution.label)}</small></div>
      <div class="application-email-update-source"><strong>${escapeHtml(provider)}</strong><span>${escapeHtml(formatApplicationDate(item.observed_at))}</span></div>
    </article>`;
  }).join("");
}

export function renderApplicationSessionReviewRows(items) {
  return (items || []).map((item) => `<article class="application-session-review-row" data-application-session-id="${escapeHtml(item.session_id)}">
    <div class="application-session-review-copy">
      <span class="application-session-review-signal">Submission detected</span>
      <strong>${escapeHtml(item.company || "Company not identified")} · ${escapeHtml(item.title || "Role not identified")}</strong>
      <p>Mark this application as applied?</p>
      <small>Observed ${escapeHtml(formatApplicationDate(item.detected_at))}. The application is still ${escapeHtml(applicationStatusLabel(item.application_status))}.</small>
    </div>
    <div class="application-session-review-actions">
      <button class="primary-button compact-button" type="button" data-application-session-action="confirm" data-session-revision="${Number(item.revision)}">Mark Applied</button>
      <button class="secondary-button compact-button" type="button" data-application-session-action="dismiss" data-session-revision="${Number(item.revision)}">Not submitted</button>
      <button class="text-button compact-button" type="button" data-application-session-job-id="${escapeHtml(item.job_id)}">Open details</button>
    </div>
  </article>`).join("");
}

export function formatApplicationDate(value) {
  if (!value) return "Date not recorded";
  return new Intl.DateTimeFormat(undefined, {
    year: "numeric",
    month: "short",
    day: "2-digit",
    timeZone: "UTC",
  }).format(new Date(value));
}

export function renderApplicationHistory(history) {
  if (!history.length) {
    return renderEmptyState({
      title: "No application status history loaded.",
      compact: true,
    });
  }
  return [...history]
    .sort((left, right) => Number(right.event_id) - Number(left.event_id))
    .map((event) => `<div class="application-history-row">
      <span class="application-history-marker" aria-hidden="true"></span>
      <div>
        <strong>${escapeHtml(applicationStatusLabel(event.status))}</strong>
        <span>${escapeHtml(formatApplicationDate(event.occurred_at))}</span>
        ${event.note ? `<p>${escapeHtml(event.note)}</p>` : ""}
      </div>
    </div>`)
    .join("");
}
