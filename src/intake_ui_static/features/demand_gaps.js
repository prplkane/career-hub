// Demand gaps view: period preference, coverage summary, insight rows and the skill drawer.
import { elements, state } from "../app_context.js";
import { api } from "../ui/api.js";
import { escapeHtml, formatRequestError } from "../ui/primitives.js";
import { competencyLabel, loadSkillsHub, openSkillEvidence } from "./skills_hub.js";
import { registryChevronIcon } from "./vault_shared.js";

// Bootstrap hooks supplied by app.js through initDemandGaps().
let navigateToView = null;
let openSavedJobWorkspace = null;

const DEMAND_GAPS_PERIOD_KEY = "careerHub.demandGapsPeriod";
let demandGapsLoaded = false;
let demandGapsLoadEpoch = 0;
let demandGapDrawerReturnFocus = null;

function demandGapStatusMeta(status) {
  return {
    supported: { label: "Supported", tone: "success" },
    thin_evidence: { label: "Thin evidence", tone: "warning" },
    evidence_not_found: { label: "Evidence not found", tone: "danger" },
  }[status] || { label: competencyLabel(status), tone: "info" };
}

function demandGapDateValue(dateValue) {
  const year = dateValue.getFullYear();
  const month = String(dateValue.getMonth() + 1).padStart(2, "0");
  const day = String(dateValue.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

function demandGapPresetDates(preset) {
  if (!/^(30|90|180)$/.test(preset)) return { startDate: "", endDate: "" };
  const endDate = new Date();
  const startDate = new Date(endDate);
  startDate.setDate(startDate.getDate() - (Number(preset) - 1));
  return {
    startDate: demandGapDateValue(startDate),
    endDate: demandGapDateValue(endDate),
  };
}

function persistDemandGapsPeriodPreference() {
  try {
    window.localStorage.setItem(DEMAND_GAPS_PERIOD_KEY, JSON.stringify({
      preset: elements.demandGapsPeriodPreset.value,
      startDate: elements.demandGapsFrom.value,
      endDate: elements.demandGapsTo.value,
    }));
  } catch (_error) {
    // The period remains usable when browser storage is unavailable.
  }
}

function syncDemandGapsPeriodControls() {
  elements.demandGapsPeriodForm.hidden = elements.demandGapsPeriodPreset.value !== "custom";
}

export function loadDemandGapsPeriodPreference() {
  let stored = {};
  try {
    stored = JSON.parse(window.localStorage.getItem(DEMAND_GAPS_PERIOD_KEY) || "{}");
  } catch (_error) {
    stored = {};
  }
  const allowed = new Set(["all", "30", "90", "180", "custom"]);
  const preset = allowed.has(stored.preset) ? stored.preset : "all";
  elements.demandGapsPeriodPreset.value = preset;
  if (preset === "custom") {
    elements.demandGapsFrom.value = /^\d{4}-\d{2}-\d{2}$/.test(stored.startDate || "") ? stored.startDate : "";
    elements.demandGapsTo.value = /^\d{4}-\d{2}-\d{2}$/.test(stored.endDate || "") ? stored.endDate : "";
  } else {
    const range = demandGapPresetDates(preset);
    elements.demandGapsFrom.value = range.startDate;
    elements.demandGapsTo.value = range.endDate;
  }
  syncDemandGapsPeriodControls();
}

function demandGapsPeriodText() {
  const preset = elements.demandGapsPeriodPreset.value;
  if (preset === "all") return "All saved jobs";
  if (preset === "30") return "Last 30 days";
  if (preset === "90") return "Last 90 days";
  if (preset === "180") return "Last 6 months";
  const startDate = elements.demandGapsFrom.value;
  const endDate = elements.demandGapsTo.value;
  if (startDate && endDate) return `${startDate} to ${endDate}`;
  if (startDate) return `From ${startDate}`;
  if (endDate) return `Through ${endDate}`;
  return "Custom period";
}

async function setDemandGapsPeriodPreset(preset, { reload = true } = {}) {
  if (!["all", "30", "90", "180", "custom"].includes(preset)) return;
  elements.demandGapsPeriodPreset.value = preset;
  if (preset === "custom") {
    syncDemandGapsPeriodControls();
    elements.demandGapsFilterMenu.open = true;
    elements.demandGapsFrom.focus();
    return;
  }
  const range = demandGapPresetDates(preset);
  elements.demandGapsFrom.value = range.startDate;
  elements.demandGapsTo.value = range.endDate;
  syncDemandGapsPeriodControls();
  persistDemandGapsPeriodPreference();
  if (reload) await loadDemandGaps({ force: true });
}

function requestedDemandGapRows() {
  return (state.demandGaps?.rows || []).filter(
    (item) => Number(item.distinct_job_count || 0) > 0
  );
}

function demandGapInsightRows() {
  const domain = elements.demandGapsDomain.value;
  return requestedDemandGapRows().filter(
    (item) => !domain || item.primary_domain === domain
  );
}

function sortDemandGapRows(rows) {
  const sort = elements.demandGapsSort.value;
  const number = (item, key) => Number(item[key] || 0);
  const name = (item) => String(item.competency_name || "");
  return [...rows].sort((left, right) => {
    if (sort === "name_asc") return name(left).localeCompare(name(right));
    if (sort === "required_desc") {
      return number(right, "required_job_count") - number(left, "required_job_count")
        || number(right, "preferred_job_count") - number(left, "preferred_job_count")
        || name(left).localeCompare(name(right));
    }
    if (sort === "preferred_desc") {
      return number(right, "preferred_job_count") - number(left, "preferred_job_count")
        || number(right, "required_job_count") - number(left, "required_job_count")
        || name(left).localeCompare(name(right));
    }
    const leftDemand = number(left, "required_job_count") + number(left, "preferred_job_count");
    const rightDemand = number(right, "required_job_count") + number(right, "preferred_job_count");
    return rightDemand - leftDemand
      || number(right, "required_job_count") - number(left, "required_job_count")
      || name(left).localeCompare(name(right));
  });
}

function filteredDemandGapRows() {
  const requirement = elements.demandGapsRequirement.value;
  const coverage = state.demandGapsCoverageFilter;
  const query = elements.demandGapsSearch.value.trim().toLocaleLowerCase();
  const rows = demandGapInsightRows().filter((item) => {
    if (coverage && item.status !== coverage) return false;
    if (requirement === "required" && !Number(item.required_job_count || 0)) return false;
    if (requirement === "preferred" && !Number(item.preferred_job_count || 0)) return false;
    if (query) {
      const haystack = [item.competency_name, item.primary_domain, item.kind]
        .map((value) => String(value || "").toLocaleLowerCase())
        .join(" ");
      if (!haystack.includes(query)) return false;
    }
    return true;
  });
  return sortDemandGapRows(rows);
}

function populateDemandGapFilters() {
  const currentDomain = elements.demandGapsDomain.value;
  const domains = state.demandGaps?.filters?.domains || [];
  elements.demandGapsDomain.innerHTML = '<option value="">All domains</option>'
    + domains.map(
      (value) => `<option value="${escapeHtml(value)}" translate="no">${escapeHtml(competencyLabel(value))}</option>`
    ).join("");
  if (domains.includes(currentDomain)) elements.demandGapsDomain.value = currentDomain;
}

function demandGapCounts(rows) {
  return {
    supported: rows.filter((item) => item.status === "supported").length,
    thin_evidence: rows.filter((item) => item.status === "thin_evidence").length,
    evidence_not_found: rows.filter((item) => item.status === "evidence_not_found").length,
  };
}

function demandGapDistinctJobCount(rows) {
  const jobIds = new Set();
  rows.forEach((item) => {
    (item.required_job_ids || []).forEach((jobId) => jobIds.add(jobId));
    (item.preferred_job_ids || []).forEach((jobId) => jobIds.add(jobId));
  });
  return jobIds.size;
}

function renderDemandGapCoverage(rows, counts) {
  const total = rows.length;
  const segments = [
    { status: "supported", label: "Supported", count: counts.supported },
    { status: "thin_evidence", label: "Thin evidence", count: counts.thin_evidence },
    { status: "evidence_not_found", label: "Evidence not found", count: counts.evidence_not_found },
  ];
  let offset = 0;
  const coverageRects = segments.filter((item) => item.count > 0).map((item) => {
    const rect = `<rect class="demand-coverage-rect demand-coverage-rect--${item.status}" x="${offset}" y="0" width="${item.count}" height="28"><title>${escapeHtml(item.label)}: ${item.count.toLocaleString()} of ${total.toLocaleString()}</title></rect>`;
    offset += item.count;
    return rect;
  }).join("");
  elements.demandGapsCoverageBar.innerHTML = total
    ? `<svg class="demand-coverage-chart" viewBox="0 0 ${total} 28" preserveAspectRatio="none" role="img" aria-label="Evidence coverage: ${counts.supported.toLocaleString()} supported, ${counts.thin_evidence.toLocaleString()} thin evidence, ${counts.evidence_not_found.toLocaleString()} evidence not found">${coverageRects}</svg>`
    : '<span class="demand-coverage-empty">No requested skills in this period</span>';
  elements.demandGapsCoverageLegend.innerHTML = segments.map((item) => `
    <button type="button" data-demand-gap-coverage="${item.status}" aria-pressed="${state.demandGapsCoverageFilter === item.status}"${item.count ? "" : " disabled"}>
      <span class="demand-coverage-key demand-coverage-key--${item.status}" aria-hidden="true"></span>
      <span>${escapeHtml(item.label)}</span>
      <strong>${item.count.toLocaleString()}</strong>
    </button>`).join("");
}

function renderDemandGapSummary() {
  const rows = demandGapInsightRows();
  const counts = demandGapCounts(rows);
  const source = state.demandGaps?.source || {};
  const jobCount = elements.demandGapsDomain.value
    ? demandGapDistinctJobCount(rows)
    : Number(source.distinct_job_count || 0);
  elements.demandGapsJobCount.textContent = jobCount.toLocaleString();
  elements.demandGapsSkillCount.textContent = rows.length.toLocaleString();
  elements.demandGapsAllCount.textContent = rows.length.toLocaleString();
  elements.demandGapsSupportedCount.textContent = counts.supported.toLocaleString();
  elements.demandGapsThinCount.textContent = counts.thin_evidence.toLocaleString();
  elements.demandGapsMissingCount.textContent = counts.evidence_not_found.toLocaleString();
  elements.demandGapsPeriodLabel.textContent = demandGapsPeriodText();
  elements.demandGapsFilterSummaryLabel.textContent = elements.demandGapsDomain.value ? "Filters (1)" : "Filters";
  renderDemandGapCoverage(rows, counts);
  elements.demandGapsWorkflow.querySelectorAll("[data-demand-gap-coverage]").forEach((button) => {
    const selected = button.dataset.demandGapCoverage === state.demandGapsCoverageFilter;
    button.setAttribute("aria-pressed", String(selected));
    button.classList.toggle("is-active", selected);
  });
}

function demandGapEvidenceCell(item) {
  const evidenceCount = Number(item.evidence_count || 0);
  const contextCount = Number(item.professional_context_count || 0);
  if (!evidenceCount) {
    return '<strong class="is-empty">Not found</strong><span>in Career Vault</span>';
  }
  const evidenceLabel = evidenceCount === 1 ? "record" : "records";
  const contextLabel = contextCount === 1 ? "professional context" : "professional contexts";
  return `<strong>${evidenceCount.toLocaleString()} ${evidenceLabel}</strong><span>${contextCount.toLocaleString()} ${contextLabel}</span>`;
}

function renderDemandGapInsights() {
  const rows = demandGapInsightRows();
  const ranked = [...rows].sort((left, right) => {
    const leftDemand = Number(left.required_job_count || 0) + Number(left.preferred_job_count || 0);
    const rightDemand = Number(right.required_job_count || 0) + Number(right.preferred_job_count || 0);
    return rightDemand - leftDemand
      || Number(right.required_job_count || 0) - Number(left.required_job_count || 0)
      || String(left.competency_name).localeCompare(String(right.competency_name));
  }).slice(0, 5);
  const maxDemand = Math.max(1, ...ranked.map(
    (item) => Number(item.required_job_count || 0) + Number(item.preferred_job_count || 0)
  ));
  elements.demandGapsTopDemand.innerHTML = ranked.length
    ? ranked.map((item) => {
      const required = Number(item.required_job_count || 0);
      const preferred = Number(item.preferred_job_count || 0);
      return `<button type="button" class="demand-ranking-item" data-demand-gap-id="${escapeHtml(item.competency_id)}">
        <span class="demand-ranking-copy"><strong>${escapeHtml(item.competency_name)}</strong><span>${required.toLocaleString()} required &middot; ${preferred.toLocaleString()} preferred</span></span>
        <progress class="demand-ranking-bar" max="${maxDemand}" value="${required + preferred}" aria-label="${required + preferred} demand occurrences"></progress>
      </button>`;
    }).join("")
    : '<p class="demand-insight-empty">No requested skills in this period.</p>';

  const priorities = [...rows].filter((item) => item.status === "evidence_not_found")
    .sort((left, right) => (
      Number(right.required_job_count || 0) - Number(left.required_job_count || 0)
      || Number(right.preferred_job_count || 0) - Number(left.preferred_job_count || 0)
      || String(left.competency_name).localeCompare(String(right.competency_name))
    )).slice(0, 5);
  elements.demandGapsPriority.innerHTML = priorities.length
    ? priorities.map((item, index) => `<button type="button" class="demand-priority-item" data-demand-gap-id="${escapeHtml(item.competency_id)}">
        <span class="demand-priority-rank">${index + 1}</span>
        <strong>${escapeHtml(item.competency_name)}</strong>
        <span>${Number(item.required_job_count || 0).toLocaleString()} required &middot; ${Number(item.preferred_job_count || 0).toLocaleString()} preferred</span>
      </button>`).join("")
    : '<p class="demand-insight-empty">No evidence gaps in this scope.</p>';
}

function demandGapSkillMeta(item) {
  const values = [];
  if (item.primary_domain && item.primary_domain !== "unclassified") {
    values.push(competencyLabel(item.primary_domain));
  }
  if (item.kind && item.kind !== "technology") values.push(competencyLabel(item.kind));
  return values;
}

export function renderDemandGaps() {
  renderDemandGapSummary();
  renderDemandGapInsights();
  const allRequested = requestedDemandGapRows();
  const scopedRows = demandGapInsightRows();
  const rows = filteredDemandGapRows();
  if (!rows.length) {
    const periodApplied = Boolean(elements.demandGapsFrom.value || elements.demandGapsTo.value);
    const hasDemand = scopedRows.length > 0;
    const noPeriodDemand = !allRequested.length && periodApplied;
    const emptyTitle = noPeriodDemand
      ? "No saved-job demand in this period"
      : hasDemand
        ? "No rows match these filters"
        : elements.demandGapsDomain.value
          ? "No requested skills in this domain"
          : "No requested skills yet";
    const emptyDetail = noPeriodDemand
      ? "Choose All time to return to every saved job in the local ledger."
      : hasDemand
        ? "Try another search, demand level, or evidence status."
        : elements.demandGapsDomain.value
          ? "Choose another domain or clear the filter."
          : "Saved jobs with recognized required or preferred skills will appear here.";
    elements.demandGapsRows.innerHTML = `<div class="demand-gaps-empty">
      <strong>${emptyTitle}</strong>
      <span>${emptyDetail}</span>
      ${noPeriodDemand ? '<button type="button" class="secondary-button compact-button" data-demand-gaps-show-all>View all saved jobs</button>' : ""}
    </div>`;
  } else {
    elements.demandGapsRows.innerHTML = rows.map((item) => {
      const status = demandGapStatusMeta(item.status);
      const meta = demandGapSkillMeta(item);
      const selected = state.demandGapsDrawerOpen && state.selectedDemandGapId === item.competency_id;
      return `<div class="demand-gap-row-shell" role="listitem"><button type="button" class="demand-gap-row${selected ? " is-selected" : ""}" data-demand-gap-id="${escapeHtml(item.competency_id)}" aria-label="Open demand detail for ${escapeHtml(item.competency_name)}" aria-pressed="${selected}">
        <span class="demand-gap-skill" data-label="Skill"><strong>${escapeHtml(item.competency_name)}</strong>${meta.length ? `<span>${meta.map(escapeHtml).join(" &middot; ")}</span>` : ""}</span>
        <span class="demand-gap-demand" data-label="Demand"><strong>${Number(item.required_job_count || 0).toLocaleString()} required</strong><span>${Number(item.preferred_job_count || 0).toLocaleString()} preferred</span></span>
        <span class="demand-gap-evidence" data-label="Evidence">${demandGapEvidenceCell(item)}</span>
        <span class="demand-gap-coverage" data-label="Coverage"><span class="status-badge status-badge--${status.tone}" title="${escapeHtml(item.explanation)}">${escapeHtml(status.label)}</span></span>
        <span class="demand-gap-open" aria-hidden="true">${registryChevronIcon()}</span>
      </button></div>`;
    }).join("");
  }
  elements.demandGapsResultCount.textContent = rows.length === scopedRows.length
    ? `${scopedRows.length.toLocaleString()} requested skill${scopedRows.length === 1 ? "" : "s"}`
    : `${rows.length.toLocaleString()} of ${scopedRows.length.toLocaleString()} requested skills`;
  const source = state.demandGaps?.source || {};
  const duplicateCount = Number(source.duplicate_job_count || 0);
  const unregisteredCount = Number(source.unregistered_job_count || 0);
  const notes = [
    `${Number(source.distinct_job_count || 0).toLocaleString()} distinct of ${Number(source.saved_job_count || 0).toLocaleString()} saved jobs counted`,
  ];
  if (duplicateCount) notes.push(`${duplicateCount.toLocaleString()} exact duplicate${duplicateCount === 1 ? "" : "s"} excluded`);
  if (unregisteredCount) notes.push(`${unregisteredCount.toLocaleString()} normalized card${unregisteredCount === 1 ? "" : "s"} outside the ledger excluded`);
  elements.demandGapsSourceNote.textContent = `${notes.join(" / ")}.`;
  renderDemandGapDrawer();
}

export async function loadDemandGaps({ force = false } = {}) {
  if (demandGapsLoaded && !force) {
    renderDemandGaps();
    return state.demandGaps;
  }
  const epoch = ++demandGapsLoadEpoch;
  elements.demandGapsMessage.className = "form-message";
  elements.demandGapsMessage.textContent = "Building a read-only comparison from saved jobs and Career Vault evidence...";
  elements.demandGapsRows.innerHTML = '<div class="demand-gaps-empty"><strong>Loading demand</strong><span>Counting distinct saved jobs and matching their registered skills.</span></div>';
  try {
    const payload = await api.getDemandGaps({
      startDate: elements.demandGapsFrom.value,
      endDate: elements.demandGapsTo.value,
    });
    if (epoch !== demandGapsLoadEpoch) return state.demandGaps;
    state.demandGaps = payload;
    demandGapsLoaded = true;
    populateDemandGapFilters();
    renderDemandGaps();
    elements.demandGapsMessage.textContent = `Read-only comparison as of ${payload.as_of_month}. Evidence not found is not a claim about candidate knowledge.`;
    return payload;
  } catch (error) {
    if (epoch !== demandGapsLoadEpoch) return state.demandGaps;
    demandGapsLoaded = false;
    elements.demandGapsMessage.className = "form-message error";
    elements.demandGapsMessage.textContent = formatRequestError(error);
    elements.demandGapsRows.innerHTML = '<div class="demand-gaps-empty"><strong>Demand could not be loaded</strong><span>Review the error below, then use the global refresh action to retry.</span></div>';
    throw error;
  }
}

async function applyDemandGapsPeriod() {
  const startDate = elements.demandGapsFrom.value;
  const endDate = elements.demandGapsTo.value;
  if (startDate && endDate && startDate > endDate) {
    elements.demandGapsMessage.className = "form-message error";
    elements.demandGapsMessage.textContent = "From date must be on or before To date.";
    elements.demandGapsFrom.focus();
    return;
  }
  elements.demandGapsPeriodPreset.value = "custom";
  syncDemandGapsPeriodControls();
  persistDemandGapsPeriodPreference();
  await loadDemandGaps({ force: true });
}

function setDemandGapCoverageFilter(value, { scroll = false } = {}) {
  if (!["", "supported", "thin_evidence", "evidence_not_found"].includes(value)) return;
  state.demandGapsCoverageFilter = value;
  renderDemandGaps();
  if (scroll) {
    document.querySelector(".demand-gaps-list-section")?.scrollIntoView({ behavior: "smooth", block: "start" });
  }
}

function relatedDemandGapJobs(item) {
  const demandByJob = new Map();
  const demandJobById = new Map(
    (state.demandGaps?.jobs || []).map((job) => [job.job_id, job])
  );
  (item?.required_job_ids || []).forEach((jobId) => demandByJob.set(jobId, new Set(["Required"])));
  (item?.preferred_job_ids || []).forEach((jobId) => {
    if (!demandByJob.has(jobId)) demandByJob.set(jobId, new Set());
    demandByJob.get(jobId).add("Preferred");
  });
  return [...demandByJob.entries()].map(([jobId, levels]) => {
    const demandJob = demandJobById.get(jobId);
    const loadedJob = state.jobs.find((candidate) => candidate.id === jobId);
    return {
      id: jobId,
      title: demandJob?.title || loadedJob?.title || jobId,
      company: demandJob?.company || loadedJob?.company || "Saved job",
      demand: [...levels].join(" + "),
    };
  });
}

function renderDemandGapDrawer() {
  if (!state.demandGapsDrawerOpen) return;
  const item = requestedDemandGapRows().find(
    (candidate) => candidate.competency_id === state.selectedDemandGapId
  );
  if (!item) {
    closeDemandGapDrawer({ restoreFocus: false });
    return;
  }
  const status = demandGapStatusMeta(item.status);
  const jobs = relatedDemandGapJobs(item);
  const visibleJobs = state.demandGapsRelatedJobsExpanded ? jobs : jobs.slice(0, 3);
  elements.demandGapDrawerTitle.textContent = item.competency_name;
  elements.demandGapDrawerStatus.innerHTML = `<span class="status-badge status-badge--${status.tone}">${escapeHtml(status.label)}</span><p>${escapeHtml(item.explanation)}</p>`;
  elements.demandGapDrawerRequired.textContent = Number(item.required_job_count || 0).toLocaleString();
  elements.demandGapDrawerPreferred.textContent = Number(item.preferred_job_count || 0).toLocaleString();
  elements.demandGapDrawerJobCount.textContent = `${jobs.length.toLocaleString()} job${jobs.length === 1 ? "" : "s"}`;
  elements.demandGapRelatedJobs.innerHTML = visibleJobs.length
    ? visibleJobs.map((job) => `<button type="button" class="demand-gap-related-job" data-demand-gap-job-id="${escapeHtml(job.id)}">
        <span><strong>${escapeHtml(job.title)}</strong><small>${escapeHtml(job.company)}</small></span>
        <em>${escapeHtml(job.demand)}</em>
      </button>`).join("")
    : '<p class="demand-insight-empty">No related saved jobs are available.</p>';
  elements.toggleDemandGapJobs.hidden = jobs.length <= 3;
  elements.toggleDemandGapJobs.textContent = state.demandGapsRelatedJobsExpanded ? "Show fewer jobs" : `View all ${jobs.length} related jobs`;
}

function openDemandGapDrawer(skillId) {
  const item = requestedDemandGapRows().find((candidate) => candidate.competency_id === skillId);
  if (!item) return;
  const activeElement = document.activeElement;
  demandGapDrawerReturnFocus = {
    element: activeElement,
    skillId,
    surface: ["demand-gap-row", "demand-ranking-item", "demand-priority-item"]
      .find((className) => activeElement?.classList?.contains(className)) || "",
  };
  state.selectedDemandGapId = skillId;
  state.demandGapsDrawerOpen = true;
  state.demandGapsRelatedJobsExpanded = false;
  elements.demandGapDrawer.classList.remove("hidden");
  elements.demandGapDrawerBackdrop.classList.remove("hidden");
  document.body.classList.add("demand-gap-drawer-open");
  renderDemandGaps();
  window.requestAnimationFrame(() => elements.closeDemandGapDrawer.focus());
}

export function closeDemandGapDrawer({ restoreFocus = true } = {}) {
  const returnFocus = demandGapDrawerReturnFocus;
  state.demandGapsDrawerOpen = false;
  state.demandGapsRelatedJobsExpanded = false;
  state.selectedDemandGapId = null;
  elements.demandGapDrawer.classList.add("hidden");
  elements.demandGapDrawerBackdrop.classList.add("hidden");
  document.body.classList.remove("demand-gap-drawer-open");
  elements.demandGapsWorkflow.querySelectorAll(".is-selected").forEach((element) => {
    element.classList.remove("is-selected");
    element.setAttribute("aria-pressed", "false");
  });
  demandGapDrawerReturnFocus = null;
  if (!restoreFocus) return;
  let focusTarget = returnFocus?.element;
  if (!focusTarget?.isConnected && returnFocus?.skillId) {
    focusTarget = [...elements.demandGapsWorkflow.querySelectorAll("[data-demand-gap-id]")]
      .find((candidate) => (
        candidate.dataset.demandGapId === returnFocus.skillId
        && (!returnFocus.surface || candidate.classList.contains(returnFocus.surface))
      ));
  }
  if (focusTarget?.isConnected) focusTarget.focus();
}

async function openDemandGapSkill(skillId) {
  closeDemandGapDrawer({ restoreFocus: false });
  if (!(await navigateToView("skills"))) return;
  try {
    await loadSkillsHub();
    openSkillEvidence(skillId);
  } catch (error) {
    elements.runMessage.className = "form-message error";
    elements.runMessage.textContent = formatRequestError(error);
  }
}

async function openDemandGapJob(jobId) {
  closeDemandGapDrawer({ restoreFocus: false });
  await openSavedJobWorkspace(jobId);
}

export function initDemandGaps(deps = {}) {
  ({ navigateToView, openSavedJobWorkspace } = deps);
  elements.demandGapsPeriodForm.addEventListener("submit", (event) => {
    event.preventDefault();
    void applyDemandGapsPeriod();
  });
  elements.demandGapsPeriodPreset.addEventListener("change", () => {
    void setDemandGapsPeriodPreset(elements.demandGapsPeriodPreset.value);
  });
  elements.demandGapsDomain.addEventListener("change", renderDemandGaps);
  elements.demandGapsRequirement.addEventListener("change", renderDemandGaps);
  elements.demandGapsSort.addEventListener("change", renderDemandGaps);
  elements.demandGapsSearch.addEventListener("input", renderDemandGaps);
  elements.demandGapsWorkflow.addEventListener("click", (event) => {
    const showAll = event.target.closest("[data-demand-gaps-show-all]");
    if (showAll) {
      void setDemandGapsPeriodPreset("all");
      return;
    }
    const coverageButton = event.target.closest("[data-demand-gap-coverage]");
    if (coverageButton) {
      setDemandGapCoverageFilter(coverageButton.dataset.demandGapCoverage);
      return;
    }
    const listAction = event.target.closest("[data-demand-gaps-list-action]");
    if (listAction) {
      const value = listAction.dataset.demandGapsListAction === "all"
        ? ""
        : listAction.dataset.demandGapsListAction;
      setDemandGapCoverageFilter(value, { scroll: true });
      return;
    }
    const skill = event.target.closest("[data-demand-gap-id]");
    if (skill) openDemandGapDrawer(skill.dataset.demandGapId);
  });
  elements.demandGapDrawerBackdrop.addEventListener("click", closeDemandGapDrawer);
  elements.closeDemandGapDrawer.addEventListener("click", closeDemandGapDrawer);
  elements.openDemandGapSkill.addEventListener("click", () => {
    if (state.selectedDemandGapId) void openDemandGapSkill(state.selectedDemandGapId);
  });
  elements.toggleDemandGapJobs.addEventListener("click", () => {
    state.demandGapsRelatedJobsExpanded = !state.demandGapsRelatedJobsExpanded;
    renderDemandGapDrawer();
  });
  elements.demandGapRelatedJobs.addEventListener("click", (event) => {
    const job = event.target.closest("[data-demand-gap-job-id]");
    if (job) void openDemandGapJob(job.dataset.demandGapJobId);
  });
}
