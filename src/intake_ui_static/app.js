import { api } from "./ui/api.js";
import { initializeConnectivity } from "./ui/connectivity.js";
import {
  createSearchableCombobox,
  escapeHtml,
  formatRequestError,
  formatTime,
  renderActionBar,
  renderStatusBadge,
} from "./ui/primitives.js";
import {
  isJobWorkflowView,
  jobWorkflowCopy,
  renderNavigation,
} from "./navigation.js";
import { assessmentRecommendation, renderAssessmentSummary } from "./features/jobs.js";
import {
  APPLICATION_STATUS_ORDER,
  applicationStatusLabel,
  formatApplicationDate,
  canManageApplicationMaterials,
  filterApplicationJobs,
  hasApplicationRecord,
  renderApplicationHistory,
  renderApplicationEmailUpdateRows,
  renderApplicationSessionReviewRows,
  renderApplicationLedgerRows,
} from "./features/applications.js";
import { initDocuments, loadDocuments } from "./features/documents.js";
import {
  initDiscoveries,
  loadDiscoveries,
  setDiscoveryRouteTarget,
} from "./features/discoveries.js";
import { initContacts, loadContacts, selectContact } from "./features/contacts.js";
import {
  initApplicationHelperConfiguration,
  loadApplicationHelperConfiguration,
  openApplicationHelperConfiguration,
} from "./features/application_helper.js";
import { initHelpCenter } from "./features/help.js";
import { initDesktopUpdates } from "./features/desktop_updates.js";
import { initDesktopBackups } from "./features/desktop_backups.js";
import { initDesktopAgent } from "./features/desktop_agent.js";
import { initExtensionPairing } from "./features/extension_pairing.js";
import {
  elements,
  requestJson,
  state,
  toast,
} from "./app_context.js";
import { initApplicationMaterials, loadCoverLetterMaterial, loadResumeMaterial } from "./features/application_materials.js";
import {
  closeDemandGapDrawer,
  initDemandGaps,
  loadDemandGaps,
  loadDemandGapsPeriodPreference,
  renderDemandGaps,
} from "./features/demand_gaps.js";
import {
  initDiagnostics,
  pollLogs,
  renderDiagnostics,
  startDiagnosticsPolling,
  stopDiagnosticsPolling,
} from "./features/diagnostics.js";
import { initEducation, loadVaultEducation, selectEducationRecord } from "./features/education.js";
import {
  beginReviewLoad,
  initIntakeReview,
  loadReview,
  renderReview,
  syncReviewInspector,
} from "./features/intake_review.js";
import { initJobDigest, renderJobDigest } from "./features/job_digest.js";
import {
  initJobInstructions,
  loadSelectedJobInstructions,
  renderJobContactCandidates,
  renderJobInstructions,
} from "./features/job_instructions.js";
import {
  ensureVaultSetup,
  initProfile,
  loadEligibilityProfile,
  loadHardBlockerPolicy,
  loadVaultProfile,
} from "./features/profile.js";
import {
  clearSourceUploadCheck,
  initResumeLibrary,
  inspectUploadedDocument,
  invalidateResumeLibraryManagement,
  loadResumeLibrary,
  loadResumeLibraryManagement,
  renderExactReimportPanel,
  renderExtractions,
  renderSources,
} from "./features/resume_library.js";
import { initSkillQuickAction, openSkillQuickAction } from "./features/skill_quick_action.js";
import {
  initSkillsHub,
  loadSkillsHub,
  loadSkillsHubModePreference,
  loadSkillsRegistryLayoutPreference,
  setSkillsHubMode,
} from "./features/skills_hub.js";
import { initVaultExperiences, loadVaultExperiences, selectVaultExperience } from "./features/vault_experiences.js";
import { initVaultManualSkills } from "./features/vault_manual_skills.js";
import { initVaultProjects, loadVaultProjects, selectVaultProject } from "./features/vault_projects.js";
import {
  initVaultShared,
  loadCareerVaultLayoutPreference,
  vaultLines,
  withVaultLoading,
} from "./features/vault_shared.js";

const viewScrollPositions = new Map();
const mobileNavigationMedia = window.matchMedia("(max-width: 820px)");
let jobsLoaded = false;
let resumeCollectionsLoaded = false;

const jobDraftCombobox = createSearchableCombobox(elements.jobDraftCombobox, {
  inputId: "job-draft-select-input",
  labelledBy: "job-draft-select-label",
  placeholder: "Search saved jobs",
  onChange: (jobId) => {
    void selectJob(jobId);
  },
});
const applicationResumeCombobox = createSearchableCombobox(elements.applicationResumeCombobox, {
  inputId: "application-resume-select-input",
  labelledBy: "application-resume-select-label",
  placeholder: "Search by resume name",
  emptyMessage: "No saved resumes match.",
  onChange: (resumeId) => {
    void selectApplicationResume(resumeId);
  },
});
renderNavigation(elements.primaryNavigation, state.activeView);

let activeTooltipTarget = null;
let tooltipTimer = null;

function cancelScheduledTooltip() {
  if (tooltipTimer !== null) window.clearTimeout(tooltipTimer);
  tooltipTimer = null;
}

function hideButtonTooltip() {
  cancelScheduledTooltip();
  if (!activeTooltipTarget) return;
  const describedBy = (activeTooltipTarget.getAttribute("aria-describedby") || "")
    .split(/\s+/)
    .filter((id) => id && id !== "button-tooltip");
  if (describedBy.length) {
    activeTooltipTarget.setAttribute("aria-describedby", describedBy.join(" "));
  } else {
    activeTooltipTarget.removeAttribute("aria-describedby");
  }
  activeTooltipTarget = null;
  elements.buttonTooltip.hidden = true;
}

function positionButtonTooltip(button) {
  const buttonRect = button.getBoundingClientRect();
  const tooltipRect = elements.buttonTooltip.getBoundingClientRect();
  const margin = 8;
  const left = Math.min(
    window.innerWidth - tooltipRect.width - margin,
    Math.max(margin, buttonRect.left + (buttonRect.width - tooltipRect.width) / 2)
  );
  let top = buttonRect.top - tooltipRect.height - margin;
  if (top < margin) top = buttonRect.bottom + margin;
  elements.buttonTooltip.style.left = `${Math.round(left)}px`;
  elements.buttonTooltip.style.top = `${Math.round(top)}px`;
}

function showButtonTooltip(button) {
  const message = button?.dataset.tooltip?.trim();
  if (!message) return;
  hideButtonTooltip();
  activeTooltipTarget = button;
  elements.buttonTooltip.textContent = message;
  elements.buttonTooltip.hidden = false;
  const describedBy = new Set(
    (button.getAttribute("aria-describedby") || "").split(/\s+/).filter(Boolean)
  );
  describedBy.add("button-tooltip");
  button.setAttribute("aria-describedby", [...describedBy].join(" "));
  positionButtonTooltip(button);
}

function initializeButtonTooltips() {
  document.addEventListener("pointerover", (event) => {
    const target = event.target.closest("[data-tooltip]");
    if (!target) return;
    cancelScheduledTooltip();
    tooltipTimer = window.setTimeout(() => showButtonTooltip(target), 350);
  });
  document.addEventListener("pointerout", (event) => {
    const target = event.target.closest("[data-tooltip]");
    if (target && !target.contains(event.relatedTarget)) hideButtonTooltip();
  });
  document.addEventListener("focusin", (event) => {
    const target = event.target.closest("[data-tooltip]");
    if (target && target.dataset.tooltipFocus !== "false") showButtonTooltip(target);
  });
  document.addEventListener("focusout", (event) => {
    if (event.target === activeTooltipTarget) hideButtonTooltip();
  });
  document.addEventListener("click", hideButtonTooltip);
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      hideButtonTooltip();
      if (state.applicationDrawerOpen) closeApplicationDrawer();
      if (state.demandGapsDrawerOpen) closeDemandGapDrawer();
    }
  });
  const syncFocusedTooltip = () => {
    if (activeTooltipTarget && document.activeElement === activeTooltipTarget) {
      positionButtonTooltip(activeTooltipTarget);
    } else {
      hideButtonTooltip();
    }
  };
  window.addEventListener("resize", syncFocusedTooltip);
  window.addEventListener("scroll", syncFocusedTooltip, true);
}

function syncJobWorkflowView() {
  const workflow = isJobWorkflowView(state.activeView) ? state.activeView : "job-intake";
  const copy = jobWorkflowCopy(workflow);
  elements.jobWorkflowTitle.textContent = copy.title;
  elements.jobWorkflowDescription.textContent = copy.description;
  elements.jobWorkflowState.textContent = copy.state;
  elements.jobWorkflowState.classList.toggle("hidden", workflow === "demand-gaps");
  elements.demandGapsHeaderControls.classList.toggle("workflow-hidden", workflow !== "demand-gaps");
  elements.jobFeedWorkflow.classList.toggle("workflow-hidden", workflow !== "job-feed");
  elements.jobIntakeWorkflow.classList.toggle("workflow-hidden", workflow !== "job-intake");
  elements.jobLibraryWorkflow.classList.toggle("workflow-hidden", workflow !== "job-library");
  elements.jobDigestSection.classList.toggle("workflow-hidden", workflow !== "job-library");
  elements.applicationLedgerWorkflow.classList.toggle("workflow-hidden", workflow !== "applications");
  elements.demandGapsWorkflow.classList.toggle("workflow-hidden", workflow !== "demand-gaps");
  elements.demandGapDrawer.classList.toggle("workflow-hidden", workflow !== "demand-gaps");
  elements.demandGapDrawerBackdrop.classList.toggle("workflow-hidden", workflow !== "demand-gaps");
  elements.jobApplicationSection.classList.toggle("workflow-hidden", workflow !== "applications");
  elements.applicationDrawerBackdrop.classList.toggle("workflow-hidden", workflow !== "applications");
  if (workflow !== "job-library") hideJobEditForm();
  if (workflow !== "applications") state.applicationDrawerOpen = false;
  if (workflow !== "demand-gaps") closeDemandGapDrawer({ restoreFocus: false });
  if (workflow === "applications") {
    renderApplicationLedger();
    renderApplicationEmailUpdates();
  }
  if (workflow === "job-library") setJobWorkspaceTab(state.jobWorkspaceTab);
  renderJobApplication();
}

function setJobWorkspaceTab(tab, { focus = false } = {}) {
  const nextTab = ["prepare", "assessment", "details"].includes(tab) ? tab : "prepare";
  state.jobWorkspaceTab = nextTab;
  elements.jobWorkspaceTabButtons.forEach((button) => {
    const active = button.dataset.jobWorkspaceTab === nextTab;
    button.setAttribute("aria-selected", String(active));
    button.tabIndex = active ? 0 : -1;
    button.classList.toggle("is-active", active);
    if (active && focus) button.focus();
  });
  const groups = {
    prepare: [elements.jobWorkspaceMaterials, elements.jobInstructionChecklist, elements.jobContactCandidates],
    assessment: [elements.jobAssessment, elements.jobDigestColumns],
    details: [
      elements.jobOverviewLayout,
      elements.jobEditMessage,
      elements.jobDetailGrid,
      elements.jobSourcePreview,
      elements.jobCoverageSection,
    ],
  };
  Object.entries(groups).forEach(([group, nodes]) => {
    nodes.filter(Boolean).forEach((node) => {
      const hidden = group !== nextTab;
      node.hidden = hidden;
      node.classList.toggle("job-workspace-panel-hidden", hidden);
    });
  });
}

function setResumeIntent(intent) {
  const showVaultIntake = intent === "vault";
  elements.resumeVaultIntake.hidden = !showVaultIntake;
  elements.resumeApplicationIntent.hidden = showVaultIntake;
  if (showVaultIntake && state.activeView === "intake") {
    void loadResumeCollections().catch((error) => {
      elements.runMessage.className = "form-message error";
      elements.runMessage.textContent = formatRequestError(error);
    });
  }
}

function selectResumeIntent(input) {
  input.checked = true;
  setResumeIntent(input.value);
}

function openApplicationResumeIntake() {
  const applicationIntent = elements.resumeIntentInputs.find(
    (input) => input.value === "applications"
  );
  if (!applicationIntent) return;
  selectResumeIntent(applicationIntent);
  switchView("intake");
  applicationIntent.focus();
}

function localRouteForView(viewName) {
  if (viewName === "review" && state.reviewSourceId) {
    return `#review/${encodeURIComponent(state.reviewSourceId)}`;
  }
  if (viewName === "application-materials" && state.materialEditorJobId) {
    return `#application-materials/${encodeURIComponent(state.materialEditorJobId)}`;
  }
  if (viewName === "job-feed" && state.jobFeedDiscoveryId) {
    const intent = state.jobFeedIntent === "prepare" ? "/prepare" : "";
    return `#job-feed/${encodeURIComponent(state.jobFeedDiscoveryId)}${intent}`;
  }
  if (viewName === "skills" && state.skillsHubMode === "all") return "#skills/all";
  return `#${viewName}`;
}

function replaceLocalRouteForCurrentView() {
  const route = localRouteForView(state.activeView);
  if (window.location.hash !== route) {
    window.history.replaceState({ careerHubView: state.activeView }, "", route);
  }
}

function discardLocalReviewChanges() {
  state.reviewDirty = false;
  state.approvalPreview = null;
}

async function confirmReviewNavigation(viewName, sourceId = null) {
  const remainsOnMaterialEditor = (
    state.activeView === "application-materials"
    && viewName === "application-materials"
  );
  if ((state.materialEditorDirty || state.resumeMaterialDirty || state.resumePlanDirty)
      && !remainsOnMaterialEditor) {
    if (!window.confirm("Discard unsaved application-material changes?")) return false;
    state.materialEditorDirty = false;
    state.resumeMaterialDirty = false;
    state.resumePlanDirty = false;
  }
  const remainsOnCurrentReview = (
    state.activeView === "review"
    && viewName === "review"
    && sourceId === state.reviewSourceId
  );
  if (!state.reviewDirty || remainsOnCurrentReview) return true;
  if (!window.confirm("Discard unsaved review changes? Save the draft first if you want to keep these decisions.")) {
    return false;
  }
  discardLocalReviewChanges();
  return true;
}

async function navigateToView(viewName) {
  if (!(await confirmReviewNavigation(viewName))) return false;
  switchView(viewName);
  return true;
}

async function navigateFromHelp(destination) {
  if (destination === "job-feed-email") {
    if (!(await navigateToView("job-feed"))) return;
    document.querySelector('[data-discovery-mode="email"]')?.click();
    return;
  }
  if (["diagnostics", "profile"].includes(destination)) {
    await navigateToView(destination);
  }
}

function setMobileNavigation(open) {
  const nextOpen = mobileNavigationMedia.matches && Boolean(open);
  document.body.classList.toggle("navigation-open", nextOpen);
  elements.mobileNavigationToggle.setAttribute("aria-expanded", String(nextOpen));
  elements.sidebarBackdrop.hidden = !nextOpen;
  elements.sidebar.inert = mobileNavigationMedia.matches && !nextOpen;
  if (mobileNavigationMedia.matches) {
    elements.sidebar.setAttribute("aria-hidden", String(!nextOpen));
  } else {
    elements.sidebar.removeAttribute("aria-hidden");
  }
  if (nextOpen) window.requestAnimationFrame(() => elements.mobileNavigationClose.focus());
}

function openMobileQuickActions() {
  const addMenu = elements.primaryNavigation.querySelector(".nav-add-menu");
  if (!addMenu) return;
  addMenu.open = true;
  setMobileNavigation(true);
  window.requestAnimationFrame(() => addMenu.querySelector("summary")?.focus());
}

function scheduleViewScroll(route, restoreScroll) {
  window.requestAnimationFrame(() => {
    const top = restoreScroll ? Number(viewScrollPositions.get(route) || 0) : 0;
    window.scrollTo({ top, left: 0, behavior: "auto" });
  });
}

function viewFromLocalRoute() {
  const [viewName, encodedSourceId, encodedIntent] = window.location.hash.slice(1).split("/");
  let sourceId = null;
  try {
    sourceId = encodedSourceId ? decodeURIComponent(encodedSourceId) : null;
  } catch {
    sourceId = null;
  }
  if (viewName === "review" && sourceId?.startsWith("src_") && !/[\\/]/.test(sourceId)) {
    return { viewName, sourceId };
  }
  if (viewName === "application-materials" && sourceId && !/[\\/]/.test(sourceId)) {
    return { viewName, sourceId: null, materialJobId: sourceId };
  }
  if (
    viewName === "job-feed"
    && sourceId?.startsWith("discovery_")
    && !/[\\/]/.test(sourceId)
  ) {
    return {
      viewName,
      sourceId: null,
      discoveryId: sourceId,
      discoveryIntent: encodedIntent === "prepare" ? "prepare" : null,
    };
  }
  if (viewName === "competencies") {
    return { viewName: "skills", sourceId: null, skillsMode: "all", legacyRoute: true };
  }
  if (viewName === "skills") {
    return { viewName, sourceId: null, skillsMode: encodedSourceId === "all" ? "all" : "groups" };
  }
  if (viewName === "profile" && encodedSourceId === "application-helper") {
    return { viewName, sourceId: null, applicationHelperIntent: "configure" };
  }
  if (
    viewName === "resume-library"
    && /^resume_[a-f0-9]{32}$/.test(sourceId || "")
  ) {
    return { viewName, sourceId: null, resumeLibraryFocusId: sourceId };
  }
  const supportedViews = new Set([
    "vault", "profile", "projects", "skills", "education", "job-intake", "job-feed",
    "job-library", "applications", "application-materials", "contacts", "demand-gaps", "resume-library", "intake", "documents", "diagnostics",
  ]);
  return { viewName: supportedViews.has(viewName) ? viewName : "vault", sourceId: null };
}

function switchView(viewName, { writeHistory = true, restoreScroll = false } = {}) {
  const priorRoute = localRouteForView(state.activeView);
  if (state.activeView !== viewName) viewScrollPositions.set(priorRoute, window.scrollY);
  if (viewName === "competencies") {
    viewName = "skills";
    setSkillsHubMode("all", { writeHistory: false });
  }
  if (viewName !== "review") state.reviewInspectorOpen = false;
  state.activeView = viewName;
  if (writeHistory) {
    const route = localRouteForView(viewName);
    if (window.location.hash !== route) {
      window.history.pushState({ careerHubView: viewName }, "", route);
    }
  }
  const route = localRouteForView(viewName);
  renderNavigation(elements.primaryNavigation, viewName);
  setMobileNavigation(false);
  const physicalView = isJobWorkflowView(viewName) ? "jobs" : viewName;
  document.querySelectorAll(".view").forEach((view) => {
    view.classList.toggle("active", view.id === `view-${physicalView}`);
  });
  syncReviewInspector();
  if (physicalView === "jobs") syncJobWorkflowView();
  if (viewName === "diagnostics") {
    renderDiagnostics();
    startDiagnosticsPolling();
  } else {
    stopDiagnosticsPolling();
  }
  void ensureViewData(viewName).catch((error) => {
    elements.runMessage.className = "form-message error";
    elements.runMessage.textContent = formatRequestError(error);
  });
  scheduleViewScroll(route, restoreScroll);
}

async function restoreLocalRoute() {
  const {
    viewName,
    sourceId,
    skillsMode,
    materialJobId,
    discoveryId,
    discoveryIntent,
    applicationHelperIntent,
    resumeLibraryFocusId,
    legacyRoute = false,
  } = viewFromLocalRoute();
  if (!(await confirmReviewNavigation(viewName, sourceId))) {
    replaceLocalRouteForCurrentView();
    return;
  }
  if (viewName === "review" && sourceId === state.reviewSourceId && state.reviewDirty) {
    switchView(viewName, { writeHistory: false, restoreScroll: true });
    return;
  }
  if (viewName === "skills" && skillsMode) {
    setSkillsHubMode(skillsMode, { persist: true, writeHistory: false });
  }
  if (viewName === "application-materials" && materialJobId) {
    state.materialEditorJobId = materialJobId;
    state.selectedJobId = materialJobId;
  }
  if (viewName === "job-feed") {
    state.jobFeedDiscoveryId = discoveryId || null;
    state.jobFeedIntent = discoveryIntent || null;
    setDiscoveryRouteTarget(discoveryId || null, discoveryIntent || null);
  }
  if (viewName === "resume-library") {
    state.resumeLibraryFocusId = resumeLibraryFocusId || null;
  }
  const reviewEpoch = viewName === "review" && sourceId ? beginReviewLoad(sourceId) : null;
  switchView(viewName, { writeHistory: false, restoreScroll: true });
  if (legacyRoute) replaceLocalRouteForCurrentView();
  if (resumeLibraryFocusId) replaceLocalRouteForCurrentView();
  if (applicationHelperIntent === "configure") {
    replaceLocalRouteForCurrentView();
    await openApplicationHelperConfiguration();
  }
  if (viewName === "review" && sourceId) await loadReview(sourceId, { epoch: reviewEpoch });
  if (viewName === "application-materials" && materialJobId) {
    await loadCoverLetterMaterial();
  }
}

async function openSavedJobWorkspace(jobId, { focusMaterials = false } = {}) {
  if (!(await confirmReviewNavigation("job-library"))) return;
  state.selectedJobId = null;
  switchView("job-library");
  try {
    await loadJobs({ force: true, autoReanalyze: false });
    let pageCount = 0;
    while (!state.jobs.some((job) => job.id === jobId) && state.jobsNextCursor && pageCount < 20) {
      const previousCursor = state.jobsNextCursor;
      await loadMoreJobs();
      pageCount += 1;
      if (state.jobsNextCursor === previousCursor) break;
    }
    if (!state.jobs.some((job) => job.id === jobId)) {
      elements.runMessage.className = "form-message error";
      elements.runMessage.textContent = "That saved job is not available in the loaded ledger page.";
      return;
    }
    await selectJob(jobId, { scroll: true, autoReanalyze: false });
    if (
      focusMaterials
      && !elements.jobWorkspaceMaterials.classList.contains("hidden")
      && typeof elements.jobWorkspaceMaterials.scrollIntoView === "function"
    ) {
      elements.jobWorkspaceMaterials.scrollIntoView({ block: "start" });
    }
  } catch (error) {
    elements.runMessage.className = "form-message error";
    elements.runMessage.textContent = formatRequestError(error);
  }
}

window.addEventListener("careerhub:open-saved-job", (event) => {
  const jobId = event.detail?.jobId;
  if (!jobId || /[\\/]/.test(jobId)) return;
  void openSavedJobWorkspace(jobId, {
    focusMaterials: event.detail?.focusMaterials === true,
  });
});

window.addEventListener("careerhub:discovery-selected", (event) => {
  const discoveryId = event.detail?.discoveryId;
  if (!discoveryId || /[\\/]/.test(discoveryId)) return;
  state.jobFeedDiscoveryId = discoveryId;
  state.jobFeedIntent = event.detail?.intent === "prepare" ? "prepare" : null;
  if (state.activeView === "job-feed") replaceLocalRouteForCurrentView();
});

function todayInputValue() {
  const now = new Date();
  const local = new Date(now.getTime() - now.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 10);
}

function filteredApplicationJobs() {
  return filterApplicationJobs(state.jobs, {
    query: elements.applicationLedgerSearch.value,
    status: elements.applicationLedgerStatusFilter.value,
    scope: state.applicationLedgerScope,
  });
}

function renderApplicationLedger() {
  const jobs = filteredApplicationJobs();
  const applicationCount = state.jobs.filter((job) => (
    job.valid && hasApplicationRecord(job.application)
  )).length;
  elements.applicationLedgerCount.textContent = jobs.length.toLocaleString()
    + " shown / " + applicationCount.toLocaleString() + " saved jobs";
  document.querySelectorAll("[data-application-scope]").forEach((button) => {
    button.setAttribute("aria-pressed", String(button.dataset.applicationScope === state.applicationLedgerScope));
  });
  elements.applicationLedgerList.innerHTML = renderApplicationLedgerRows(jobs, {
    selectedJobId: state.selectedJobId,
    drawerOpen: state.applicationDrawerOpen,
  });
}

function renderApplicationEmailUpdates() {
  const snapshot = state.applicationEmailUpdates || {};
  const items = Array.isArray(snapshot.items) ? snapshot.items : [];
  elements.applicationEmailUpdates.classList.toggle("hidden", !items.length);
  const needsReview = Number(snapshot.needs_review || 0);
  elements.applicationEmailUpdatesCount.textContent = items.length
    ? `${Number(snapshot.total || items.length).toLocaleString()} total${needsReview ? ` · ${needsReview.toLocaleString()} need review` : ""}`
    : "";
  elements.applicationEmailUpdatesList.innerHTML = renderApplicationEmailUpdateRows(
    items,
    state.jobs,
  );
  elements.applicationEmailUpdatesMore.classList.toggle(
    "hidden",
    !state.applicationEmailUpdatesNextCursor,
  );
  elements.applicationEmailUpdatesMore.disabled = state.applicationEmailUpdatesLoading;
  elements.applicationEmailUpdatesMore.textContent = state.applicationEmailUpdatesLoading
    ? "Loading…"
    : "Load more email updates";
  if (state.applicationEmailUpdateMutationId) {
    elements.applicationEmailUpdatesList
      .querySelectorAll(`[data-outcome-id="${CSS.escape(state.applicationEmailUpdateMutationId)}"]`)
      .forEach((button) => {
        button.disabled = true;
        const select = button.closest(".application-email-update-linker")?.querySelector("select");
        if (select) select.disabled = true;
      });
  }
}

function renderApplicationSessionReviews() {
  const snapshot = state.applicationSessionReviews || {};
  const items = Array.isArray(snapshot.items) ? snapshot.items : [];
  elements.applicationSessionReviews.classList.toggle("hidden", !items.length);
  elements.applicationSessionReviewsCount.textContent = items.length
    ? `${items.length.toLocaleString()} awaiting confirmation`
    : "";
  elements.applicationSessionReviewList.innerHTML = renderApplicationSessionReviewRows(items);
  if (state.applicationSessionMutationId) {
    elements.applicationSessionReviewList
      .querySelectorAll(`[data-application-session-id="${CSS.escape(state.applicationSessionMutationId)}"] button`)
      .forEach((button) => { button.disabled = true; });
  }
}

async function loadApplicationSessionReviews() {
  if (state.applicationSessionReviewsLoading) return;
  state.applicationSessionReviewsLoading = true;
  try {
    state.applicationSessionReviews = await api.getApplicationSessionReviews();
  } catch (error) {
    elements.applicationLedgerMessage.className = "form-message application-ledger-message error";
    elements.applicationLedgerMessage.textContent = formatRequestError(error);
  } finally {
    state.applicationSessionReviewsLoading = false;
    renderApplicationSessionReviews();
  }
}

async function resolveApplicationSessionReview(sessionId, action, expectedRevision) {
  if (state.applicationSessionMutationId) return;
  state.applicationSessionMutationId = sessionId;
  elements.applicationLedgerMessage.className = "form-message application-ledger-message";
  elements.applicationLedgerMessage.textContent = action === "confirm"
    ? "Recording the confirmed submission..."
    : "Leaving the application stage unchanged...";
  renderApplicationSessionReviews();
  renderJobApplication();
  try {
    const result = await api.resolveApplicationSession(
      sessionId,
      action,
      Number(expectedRevision),
    );
    result.application.application_session = result.session;
    storeJobApplication(result.application);
    elements.applicationLedgerMessage.className = "form-message application-ledger-message success";
    elements.applicationLedgerMessage.textContent = action === "confirm"
      ? `Recorded ${result.session.company || result.session.title} as Applied.`
      : "Submission signal dismissed. The application stage was not changed.";
    toast.show({
      message: action === "confirm"
        ? "Application marked Applied after confirmation."
        : "Submission signal dismissed.",
    });
    state.applicationSessionReviews = null;
    await loadApplicationSessionReviews();
  } catch (error) {
    elements.applicationLedgerMessage.className = "form-message application-ledger-message error";
    elements.applicationLedgerMessage.textContent = formatRequestError(error);
  } finally {
    state.applicationSessionMutationId = null;
    renderJobs();
    renderApplicationSessionReviews();
  }
}

function clearApplicationEmailUpdates() {
  state.applicationEmailUpdates = null;
  state.applicationEmailUpdatesNextCursor = null;
}

async function loadApplicationEmailUpdates({ append = false } = {}) {
  if (state.applicationEmailUpdatesLoading) return;
  const cursor = append ? state.applicationEmailUpdatesNextCursor : null;
  if (append && !cursor) return;
  state.applicationEmailUpdatesLoading = true;
  renderApplicationEmailUpdates();
  try {
    const snapshot = await api.getApplicationEmailUpdatesPage({ cursor });
    const priorItems = append && Array.isArray(state.applicationEmailUpdates?.items)
      ? state.applicationEmailUpdates.items
      : [];
    state.applicationEmailUpdates = {
      ...snapshot,
      items: [...priorItems, ...(snapshot.items || [])],
    };
    state.applicationEmailUpdatesNextCursor = snapshot.next_cursor || null;
  } catch (error) {
    elements.applicationLedgerMessage.className = "form-message application-ledger-message error";
    elements.applicationLedgerMessage.textContent = formatRequestError(error);
  } finally {
    state.applicationEmailUpdatesLoading = false;
    renderApplicationEmailUpdates();
  }
}

async function linkApplicationEmailUpdate(button) {
  if (state.applicationEmailUpdateMutationId) return;
  const linker = button.closest(".application-email-update-linker");
  const select = linker?.querySelector("[data-email-update-job]");
  const jobId = select?.value || "";
  if (!jobId) return;
  const outcomeId = button.dataset.outcomeId;
  state.applicationEmailUpdateMutationId = outcomeId;
  elements.applicationLedgerMessage.className = "form-message application-ledger-message";
  elements.applicationLedgerMessage.textContent = "Linking the email update to the selected application…";
  renderApplicationEmailUpdates();
  try {
    const result = await api.linkApplicationEmailUpdate(
      outcomeId,
      jobId,
      Number(button.dataset.outcomeRevision),
    );
    storeJobApplication(result.application);
    clearApplicationEmailUpdates();
    await loadApplicationEmailUpdates();
    elements.applicationLedgerMessage.className = "form-message application-ledger-message success";
    elements.applicationLedgerMessage.textContent = result.update.application_changed
      ? `Email confirmation linked and application moved to ${applicationStatusLabel(result.application.current_status)}.`
      : "Email confirmation linked. The application already had this or a later stage.";
    toast.show({ message: "Email update linked to the application." });
  } catch (error) {
    elements.applicationLedgerMessage.className = "form-message application-ledger-message error";
    elements.applicationLedgerMessage.textContent = formatRequestError(error);
    if (error?.payload?.error_code === "EMAIL_APPLICATION_UPDATE_REVISION_CONFLICT") {
      clearApplicationEmailUpdates();
      await loadApplicationEmailUpdates();
    }
  } finally {
    state.applicationEmailUpdateMutationId = null;
    renderJobs();
    renderApplicationLedger();
    renderApplicationEmailUpdates();
  }
}

function renderJobLibraryApplication() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const application = job?.application;
  const status = hasApplicationRecord(application)
    ? applicationStatusLabel(application.current_status)
    : "Not tracked";
  elements.jobLibraryApplicationBar.innerHTML = renderActionBar({
    label: "Application status",
    className: "job-library-action-bar",
    summary: `<div class="job-library-application-summary"><span>Application status</span>${renderStatusBadge({ label: status })}</div>`,
    actions: [
      {
        label: "Open in Applications",
        variant: "primary",
        attributes: {
          "data-open-selected-application": "true",
          "data-tooltip": "Open this saved job in the application ledger and update its status when needed.",
        },
        disabled: !job?.valid || !hasApplicationRecord(application) || state.jobApplicationLoading,
      },
    ],
  });
}

function storeJobApplication(application) {
  const job = state.jobs.find((item) => item.id === application?.job_id);
  if (job) job.application = application;
  if (application?.assessment && application.job_id === state.selectedJobId) {
    state.jobAssessment = application.assessment;
    state.jobAssessmentLoading = false;
    state.jobAssessmentError = "";
  }
  if (application?.job_id === state.selectedJobId) state.jobApplication = application;
}

async function selectApplicationResume(resumeId) {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid || state.applicationResumeLoading) return;
  state.applicationResumeLoading = true;
  state.applicationResumeMessageJobId = job.id;
  elements.applicationResumeMessage.className = "form-message application-resume-message";
  elements.applicationResumeMessage.textContent = resumeId
    ? "Selecting resume..."
    : "Recording no resume...";
  renderJobWorkspaceMaterials();
  renderJobApplication();
  try {
    const application = await requestJson(
      `/api/jobs/${encodeURIComponent(job.id)}/application/resume-selection`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ resume_id: resumeId || null }),
      }
    );
    storeJobApplication(application);
    const selected = application.resume_selection?.resume;
    elements.applicationResumeMessage.textContent = selected
      ? `Selected ${selected.display_name}.`
      : "Resume marked as not recorded.";
    toast.show({
      message: selected
        ? `Resume selected: ${selected.display_name}.`
        : "Resume marked as not recorded.",
    });
  } catch (error) {
    elements.applicationResumeMessage.className = "form-message application-resume-message error";
    elements.applicationResumeMessage.textContent = formatRequestError(error);
  } finally {
    state.applicationResumeLoading = false;
    renderJobs();
  }
}

async function openSelectedApplication() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid || !hasApplicationRecord(job.application) || state.jobApplicationLoading) return;
  elements.jobLibraryMessage.className = "form-message job-library-application-message";
  elements.jobLibraryMessage.textContent = "";
  switchView("applications");
  await selectApplicationJob(job.id);
}

async function selectApplicationJob(jobId) {
  if (!state.jobs.some((job) => (
    job.valid && job.id === jobId && hasApplicationRecord(job.application)
  ))) return;
  state.selectedJobId = jobId;
  state.applicationDrawerOpen = true;
  state.jobApplication = null;
  state.jobApplicationError = "";
  state.applicationResumeLoading = false;
  elements.applicationResumeFile.value = "";
  elements.applicationResumeDisplayName.value = "";
  elements.applicationResumeMessage.className = "form-message application-resume-message";
  elements.applicationResumeMessage.textContent = "";
  renderJobs();
  await Promise.all([loadResumeLibrary(), loadSelectedJobApplication()]);
}
function closeApplicationDrawer() {
  state.applicationDrawerOpen = false;
  document.body.classList.remove("application-drawer-open");
  renderApplicationLedger();
  renderJobApplication();
}

async function openSelectedJobInLibrary() {
  const jobId = state.selectedJobId;
  closeApplicationDrawer();
  switchView("job-library");
  if (jobId) await selectJob(jobId);
}

function coverLetterSummary(editor) {
  return {
    persisted: Boolean(editor?.persisted),
    revision: Number(editor?.revision || 0),
    status: editor?.draft?.status || "draft",
    ready: Boolean(editor?.validation?.ready_to_export),
  };
}

async function ensureCoverLetterSummary(jobId) {
  if (!jobId || state.coverLetterSummaries[jobId]) return;
  state.coverLetterSummaries[jobId] = { loading: true };
  renderJobWorkspaceMaterials();
  try {
    const editor = await api.getCoverLetter(jobId);
    state.coverLetterSummaries[jobId] = coverLetterSummary(editor);
  } catch (error) {
    state.coverLetterSummaries[jobId] = {
      error: formatRequestError(error),
    };
  }
  if (state.selectedJobId === jobId) renderJobWorkspaceMaterials();
}

function renderJobWorkspaceMaterials() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const application = state.jobApplication?.job_id === job?.id
    ? state.jobApplication
    : job?.application;
  const available = Boolean(job?.valid && hasApplicationRecord(application));
  elements.jobWorkspaceMaterials.classList.toggle("hidden", !available);
  if (!available) return;
  if (state.applicationResumeMessageJobId !== job.id) {
    state.applicationResumeMessageJobId = job.id;
    elements.applicationResumeMessage.className = "form-message application-resume-message";
    elements.applicationResumeMessage.textContent = "";
  }

  const resumePlan = application.resume_plan;
  const canManageMaterials = canManageApplicationMaterials(application);
  elements.jobWorkspaceMaterialsStatus.textContent = "Ready";

  const resumePlanApproved = resumePlan?.status === "approved";
  elements.applicationResumeMaterialState.textContent = resumePlanApproved
    ? `Content selected / revision ${resumePlan.revision}`
    : resumePlan
      ? "Tailoring in progress"
      : "Not tailored";
  elements.applicationResumeMaterialGuidance.textContent = resumePlanApproved
    ? "Review application-only wording and export DOCX, PDF, or TXT from the same workspace."
    : "Select truthful Career Vault evidence and edit only where the job benefits.";
  elements.openResumeMaterialButton.textContent = resumePlanApproved
    ? "Continue tailoring"
    : "Tailor resume";
  elements.openResumeMaterialButton.disabled = state.jobApplicationLoading || !canManageMaterials;

  const coverLetter = state.coverLetterSummaries[job.id];
  if (!coverLetter) void ensureCoverLetterSummary(job.id);
  elements.applicationCoverLetterState.textContent = coverLetter?.loading
    ? "Loading"
    : coverLetter?.error
      ? "Unavailable"
      : coverLetter?.persisted
        ? `${coverLetter.status} / revision ${coverLetter.revision}`
        : "Not started";
  elements.applicationCoverLetterGuidance.textContent = coverLetter?.ready
    ? "Saved and validated. Open it to preview or export DOCX, PDF, or TXT."
    : coverLetter?.persisted
      ? "A draft exists. Resolve readiness checks before export."
      : "Start from the standard structure, then tailor only evidence and job-specific prose.";
  elements.openCoverLetterButton.textContent = coverLetter?.persisted
    ? "Edit cover letter"
    : "Create cover letter";
  elements.openCoverLetterButton.disabled = (
    state.jobApplicationLoading || !canManageMaterials || coverLetter?.loading
  );

  const selectedResume = application.resume_selection?.resume;
  applicationResumeCombobox.setOptions(state.resumeLibrary.map((resume) => ({
    value: resume.id,
    label: resume.display_name,
  })));
  applicationResumeCombobox.setValue(selectedResume?.id || "");
  const resumeControlsDisabled = state.applicationResumeLoading || state.jobApplicationLoading;
  applicationResumeCombobox.setDisabled(resumeControlsDisabled);
  elements.clearApplicationResumeSelectionButton.disabled = resumeControlsDisabled;
  elements.clearApplicationResumeSelectionButton.classList.toggle("hidden", !selectedResume);
  const appliedResume = application.applied_resume;
  const appliedResumeHistory = appliedResume?.history || [];
  const appliedResumeCorrected = appliedResumeHistory.some((event) => event.action === "corrected");
  const appliedResumeVersion = appliedResume?.recorded ? appliedResume.resume : null;
  const latestSubmittedEvent = appliedResumeHistory.at(-1);
  const canOpenSubmittedFile = Boolean(
    appliedResumeVersion?.id && latestSubmittedEvent?.resume?.id === appliedResumeVersion.id
  );
  const selectedMatchesSubmitted = Boolean(selectedResume?.id && selectedResume.id === appliedResumeVersion?.id);
  elements.applicationResumeState.textContent = selectedResume ? "Selected for tailoring" : "";
  elements.applicationResumeState.classList.toggle("hidden", !selectedResume);
  elements.applicationResumeLabel.textContent = appliedResumeVersion?.display_name || "Not recorded";
  elements.applicationResumeMeta.textContent = appliedResumeVersion
    ? (appliedResumeCorrected
      ? "Recorded for this application. Earlier corrections remain in local history."
      : "Recorded as the resume used for this application.")
    : appliedResume?.recorded
      ? "No resume was recorded when this application was marked Applied."
      : "Record the version you actually sent after the application reaches Applied.";
  elements.openApplicationResume.classList.toggle("hidden", !canOpenSubmittedFile);
  const canRecordSubmittedResume = Boolean(appliedResume?.recorded && selectedResume && !selectedMatchesSubmitted);
  elements.recordSubmittedResumeButton.classList.toggle("hidden", !canRecordSubmittedResume);
  elements.recordSubmittedResumeButton.disabled = resumeControlsDisabled || !selectedResume;
  elements.recordSubmittedResumeButton.textContent = appliedResumeVersion
    ? "Use selected as submitted"
    : "Record selected as submitted";
  elements.applicationResumeFile.disabled = resumeControlsDisabled;
  elements.applicationResumeDisplayName.disabled = resumeControlsDisabled;
  elements.attachApplicationResumeButton.disabled = resumeControlsDisabled;
  elements.attachApplicationResumeButton.textContent = state.applicationResumeLoading
    ? "Uploading..."
    : "Upload and select";
}

function renderApplicationSessionPanel(job, application) {
  const session = application?.application_session || null;
  const stateLabel = {
    pending_claim: "Waiting for posting",
    armed: "Tracking tab",
    submission_detected: "Confirmation required",
    confirmed_applied: "Applied confirmed",
    dismissed: "Signal dismissed",
    interrupted: "Tracking interrupted",
    expired: "Expired",
    cancelled: "Cancelled",
  }[session?.state] || "Not started";
  elements.applicationSessionState.textContent = stateLabel;
  elements.applicationSessionState.dataset.state = session?.state || "idle";

  const reasonCopy = {
    origin_changed: "The tab left the site used when it was armed.",
    tab_closed: "The attended tab was closed before a confirmed submission.",
    tab_reused: "That browser tab was armed for another application.",
    instrumentation_lost: "The extension could no longer observe the attended tab.",
    timeout: "The four-hour session expired without a confirmed submission.",
    replaced: "A newer session replaced this one.",
    manual_stop: "You ended this session.",
  };
  if (session?.state === "pending_claim") {
    elements.applicationSessionGuidance.textContent = "Open the exact posting, then choose Track submission in the Career Hub extension. The tracking link expires after four hours.";
  } else if (session?.state === "armed") {
    elements.applicationSessionGuidance.textContent = "Apply normally in the armed tab. Only a native submit followed by a recognized success state can create a confirmation request; no form values are recorded.";
  } else if (session?.state === "submission_detected") {
    elements.applicationSessionGuidance.textContent = "A likely successful submission is waiting at the top of Applications. The stage is unchanged until you confirm it.";
  } else if (["interrupted", "expired", "cancelled"].includes(session?.state)) {
    elements.applicationSessionGuidance.textContent = reasonCopy[session.interruption_reason]
      || "This session ended without changing the application stage. Start again if you are still applying.";
  } else if (session?.state === "confirmed_applied") {
    elements.applicationSessionGuidance.textContent = "You confirmed the detected submission. The application history keeps the exact Applied event.";
  } else if (session?.state === "dismissed") {
    elements.applicationSessionGuidance.textContent = "You dismissed the detected signal and the application stage was left unchanged.";
  } else {
    elements.applicationSessionGuidance.textContent = "From the posting, choose Track submission in the extension. It starts only when one saved job matches the exact URL; no form values are recorded. Create a tracking link here only as a fallback.";
  }

  const canStart = Boolean(
    job?.source_url
    && ["captured", "reviewed", "preparing"].includes(application?.current_status)
    && session?.state !== "submission_detected"
    && !state.applicationSessionMutationId
  );
  const reviewMode = session?.state === "submission_detected";
  elements.startApplicationSessionButton.dataset.mode = reviewMode ? "review" : "start";
  elements.startApplicationSessionButton.textContent = reviewMode
    ? "Review confirmation"
    : session
      ? "Create fresh link"
      : "Create tracking link";
  elements.startApplicationSessionButton.disabled = reviewMode
    ? false
    : !canStart;

  const canOpen = Boolean(job?.source_url && ["pending_claim", "armed"].includes(session?.state));
  elements.openApplicationSessionPosting.href = canOpen ? job.source_url : "#";
  elements.openApplicationSessionPosting.classList.toggle("hidden", !canOpen);
  const canCancel = ["pending_claim", "armed", "interrupted"].includes(session?.state);
  elements.cancelApplicationSessionButton.classList.toggle("hidden", !canCancel);
  elements.cancelApplicationSessionButton.disabled = Boolean(state.applicationSessionMutationId);
}

function renderJobApplication() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const visible = state.activeView === "applications"
    && state.applicationDrawerOpen
    && job?.valid
    && hasApplicationRecord(job.application);
  elements.jobApplicationSection.classList.toggle("hidden", !visible);
  elements.applicationDrawerBackdrop.classList.toggle("hidden", !visible);
  document.body.classList.toggle("application-drawer-open", Boolean(visible));
  if (!visible) return;

  const role = [job.company, job.title].filter(Boolean).join(" / ") || job.id;
  elements.applicationDetailTitle.textContent = role;
  elements.applicationDetailMeta.textContent = [job.location, job.remote_policy]
    .filter(Boolean)
    .join(" / ") || "Location not stated";
  if (job.source_url) {
    elements.applicationSourceLink.href = job.source_url;
    elements.applicationSourceLink.classList.remove("hidden");
  } else {
    elements.applicationSourceLink.href = "#";
    elements.applicationSourceLink.classList.add("hidden");
  }
  const application = state.jobApplication || job.application;

  const currentStatus = application.current_status;
  elements.applicationCurrentStatus.textContent = applicationStatusLabel(currentStatus);
  elements.applicationCurrentStatus.dataset.status = currentStatus;
  elements.applicationSummaryStatus.textContent = applicationStatusLabel(currentStatus);
  elements.applicationStatusSelect.value = currentStatus;
  if (!elements.applicationStatusDate.value) {
    elements.applicationStatusDate.value = todayInputValue();
  }

  elements.applicationStatusSelect.disabled = state.jobApplicationLoading;
  elements.applicationStatusDate.disabled = state.jobApplicationLoading;
  elements.applicationStatusNote.disabled = state.jobApplicationLoading;
  elements.saveApplicationStatusButton.disabled = state.jobApplicationLoading;

  const statusHistory = (application.status_history || []).filter((event) => (
    APPLICATION_STATUS_ORDER.includes(event.status)
  ));
  const appliedEvent = statusHistory.find((event) => event.status === "applied");
  elements.applicationAppliedDate.textContent = appliedEvent
    ? formatApplicationDate(appliedEvent.occurred_at)
    : "Not recorded";

  const assessment = application.assessment
    || (state.selectedJobId === job.id ? state.jobAssessment : null);
  elements.applicationAssessmentSummary.textContent = assessment
    ? assessmentRecommendation(assessment)
    : "Unavailable";
  elements.applicationAssessmentDetail.innerHTML = assessment
    ? '<p class="application-assessment-link-copy">Full findings, evidence, and user resolutions live in Job Workspace.</p>'
    : '<p class="application-assessment-link-copy">Assessment is unavailable. Open Job Workspace to refresh it.</p>';
  renderApplicationSessionPanel(job, application);

  const appliedResumeVersion = application.applied_resume?.recorded
    ? application.applied_resume.resume
    : null;
  const latestSubmittedEvent = application.applied_resume?.history?.at(-1);
  const canOpenSubmittedFile = Boolean(
    appliedResumeVersion?.id && latestSubmittedEvent?.resume?.id === appliedResumeVersion.id
  );
  elements.applicationSubmittedResumeSummary.textContent = appliedResumeVersion?.display_name || "Not recorded";
  elements.applicationSubmittedResumeSummary.title = appliedResumeVersion
    ? "The resume recorded as submitted for this application."
    : ["captured", "reviewed", "preparing"].includes(currentStatus)
      ? "No submitted resume is expected until the application is sent."
      : "The application is recorded, but the submitted resume is still unknown.";
  elements.openSubmittedResumeSummary.classList.toggle("hidden", !canOpenSubmittedFile);

  if (state.jobApplicationLoading && !state.jobApplication) {
    elements.applicationHistoryCount.textContent = "Loading";
    elements.applicationHistoryList.innerHTML = '<p class="empty-state">Loading local status history...</p>';
    return;
  }
  if (state.jobApplicationError) {
    elements.applicationMessage.className = "form-message error";
    elements.applicationMessage.textContent = state.jobApplicationError;
  }

  const history = (state.jobApplication?.status_history || []).filter((event) => (
    APPLICATION_STATUS_ORDER.includes(event.status)
  ));
  elements.applicationHistoryCount.textContent = `${history.length} event${history.length === 1 ? "" : "s"}`;
  elements.applicationHistoryList.innerHTML = renderApplicationHistory(history);
}

async function openSubmittedResumeFile(event, messageNode) {
  event.preventDefault();
  if (!state.selectedJobId) return;
  // Reserve the preview window during the user gesture, before the authorized fetch.
  const preview = window.open("", "_blank");
  if (!preview) {
    messageNode.classList.add("error");
    messageNode.textContent = "Allow local pop-ups to open the submitted resume.";
    return;
  }
  preview.opener = null;
  try {
    const response = await api.submittedResumeFile(state.selectedJobId);
    const url = URL.createObjectURL(await response.blob());
    preview.location.href = url;
    setTimeout(() => URL.revokeObjectURL(url), 60_000);
  } catch (error) {
    preview.close();
    messageNode.classList.add("error");
    messageNode.textContent = formatRequestError(error);
  }
}

async function recordSubmittedResume() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const application = state.jobApplication || job?.application;
  const resumeId = application?.resume_selection?.resume?.id;
  if (!job?.valid || !resumeId || state.applicationResumeLoading) return;
  state.applicationResumeLoading = true;
  elements.applicationResumeMessage.className = "form-message application-resume-message";
  elements.applicationResumeMessage.textContent = "Recording submitted resume...";
  renderJobApplication();
  try {
    const application = await requestJson(
      `/api/jobs/${encodeURIComponent(job.id)}/application/submitted-resume`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ resume_id: resumeId }),
      }
    );
    storeJobApplication(application);
    const submitted = application.applied_resume?.resume;
    elements.applicationResumeMessage.textContent = submitted
      ? `Submitted resume recorded: ${submitted.display_name}.`
      : "Submitted resume record updated.";
    toast.show({ message: "Submitted resume recorded in application history." });
  } catch (error) {
    elements.applicationResumeMessage.className = "form-message application-resume-message error";
    elements.applicationResumeMessage.textContent = formatRequestError(error);
  } finally {
    state.applicationResumeLoading = false;
    renderJobs();
  }
}

async function saveLibraryResume() {
  const file = elements.libraryResumeFile.files[0];
  if (state.libraryResumeUploadLoading) return;
  if (!file) {
    elements.libraryResumeMessage.className = "form-message error";
    elements.libraryResumeMessage.textContent = "Choose a resume file first.";
    return;
  }
  state.libraryResumeUploadLoading = true;
  elements.saveLibraryResumeButton.disabled = true;
  elements.saveLibraryResumeButton.textContent = "Saving...";
  elements.libraryResumeMessage.className = "form-message";
  elements.libraryResumeMessage.textContent = `Saving ${file.name} for applications...`;
  try {
    const resume = await api.uploadResumeVersion(file, elements.libraryResumeDisplayName.value.trim());
    invalidateResumeLibraryManagement();
    await loadResumeLibrary();
    elements.libraryResumeFile.value = "";
    elements.libraryResumeDisplayName.value = "";
    elements.libraryResumeMessage.textContent = `Saved ${resume.display_name}. It is now available in Applications.`;
    toast.show({ message: `Resume saved: ${resume.display_name}.` });
  } catch (error) {
    elements.libraryResumeMessage.className = "form-message error";
    elements.libraryResumeMessage.textContent = formatRequestError(error);
  } finally {
    state.libraryResumeUploadLoading = false;
    elements.saveLibraryResumeButton.disabled = false;
    elements.saveLibraryResumeButton.textContent = "Save resume";
  }
}

async function attachApplicationResume() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const file = elements.applicationResumeFile.files[0];
  if (!job?.valid || state.applicationResumeLoading) return;
  if (!file) {
    elements.applicationResumeMessage.className = "form-message application-resume-message error";
    elements.applicationResumeMessage.textContent = "Choose a resume file first.";
    return;
  }

  state.applicationResumeLoading = true;
  elements.applicationResumeMessage.className = "form-message application-resume-message";
  elements.applicationResumeMessage.textContent = `Adding ${file.name} to your resume library...`;
  renderJobApplication();
  try {
    const application = await requestJson(
      `/api/jobs/${encodeURIComponent(job.id)}/application/resume`,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/octet-stream",
          "X-Resume-Name": encodeURIComponent(file.name),
          "X-Resume-Label": encodeURIComponent(
            elements.applicationResumeDisplayName.value.trim()
          ),
        },
        body: file,
      }
    );
    storeJobApplication(application);
    await loadResumeLibrary();
    elements.applicationResumeFile.value = "";
    elements.applicationResumeDisplayName.value = "";
    const selected = application.resume_selection?.resume;
    elements.applicationResumeMessage.textContent = selected
      ? `Added and selected ${selected.display_name}.`
      : "Resume uploaded.";
    toast.show({
      message: selected
        ? `Resume added: ${selected.display_name}.`
        : "Resume uploaded.",
    });
  } catch (error) {
    elements.applicationResumeMessage.className = "form-message application-resume-message error";
    elements.applicationResumeMessage.textContent = formatRequestError(error);
  } finally {
    state.applicationResumeLoading = false;
    renderJobs();
  }
}

async function loadSelectedJobApplication({ includeSession = true } = {}) {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid || !hasApplicationRecord(job.application)) {
    state.jobApplication = null;
    state.jobApplicationLoading = false;
    state.jobApplicationError = "";
    renderJobApplication();
    renderJobWorkspaceMaterials();
    return;
  }
  const requestedJobId = job.id;
  state.jobApplication = null;
  state.jobApplicationLoading = true;
  state.jobApplicationError = "";
  elements.applicationMessage.className = "form-message";
  elements.applicationMessage.textContent = "";
  renderJobApplication();
  try {
    const [application, sessionPayload] = await Promise.all([
      api.getApplication(requestedJobId),
      includeSession ? api.getApplicationSession(requestedJobId) : Promise.resolve(null),
    ]);
    if (state.selectedJobId !== requestedJobId) return;
    if (sessionPayload) application.application_session = sessionPayload.session || null;
    storeJobApplication(application);
  } catch (error) {
    if (state.selectedJobId !== requestedJobId) return;
    state.jobApplicationError = formatRequestError(error);
  } finally {
    if (state.selectedJobId === requestedJobId) {
      state.jobApplicationLoading = false;
      renderJobApplication();
      renderJobWorkspaceMaterials();
    }
  }
}

async function startAttendedApplicationSession() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const application = state.jobApplication || job?.application;
  if (!job?.valid || !application || state.applicationSessionMutationId) return;
  if (elements.startApplicationSessionButton.dataset.mode === "review") {
    closeApplicationDrawer();
    elements.applicationSessionReviews.scrollIntoView({ behavior: "smooth", block: "start" });
    return;
  }
  if (application.application_session?.state === "armed" && !window.confirm(
    "End the currently armed session and start a fresh one for this same application?"
  )) return;

  state.applicationSessionMutationId = "creating";
  elements.applicationMessage.className = "form-message";
  elements.applicationMessage.textContent = "Creating an expiring session for this exact application...";
  renderJobApplication();
  try {
    const result = await api.startApplicationSession(job.id);
    result.application.application_session = result.session;
    storeJobApplication(result.application);
    elements.applicationMessage.className = "form-message success";
    elements.applicationMessage.textContent = "Tracking link ready. Open the posting and choose Track submission in the extension.";
    toast.show({ message: "Application session ready to arm." });
  } catch (error) {
    elements.applicationMessage.className = "form-message error";
    elements.applicationMessage.textContent = formatRequestError(error);
  } finally {
    state.applicationSessionMutationId = null;
    renderJobs();
  }
}

async function cancelAttendedApplicationSession() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const application = state.jobApplication || job?.application;
  const session = application?.application_session;
  if (!session || state.applicationSessionMutationId) return;
  if (!window.confirm(
    "Cancel this attended session? The application stage will remain unchanged."
  )) return;
  state.applicationSessionMutationId = session.session_id;
  elements.applicationMessage.className = "form-message";
  elements.applicationMessage.textContent = "Cancelling attended session...";
  renderJobApplication();
  try {
    const result = await api.resolveApplicationSession(
      session.session_id,
      "cancel",
      session.revision,
    );
    result.application.application_session = result.session;
    storeJobApplication(result.application);
    elements.applicationMessage.className = "form-message success";
    elements.applicationMessage.textContent = "Session cancelled. Application status was not changed.";
  } catch (error) {
    elements.applicationMessage.className = "form-message error";
    elements.applicationMessage.textContent = formatRequestError(error);
  } finally {
    state.applicationSessionMutationId = null;
    renderJobs();
  }
}

async function saveInlineApplicationStatus(select) {
  const job = state.jobs.find((item) => item.id === select.dataset.applicationJobId);
  if (!job?.valid || state.jobApplicationLoading) return;
  const previousStatus = select.dataset.previousStatus;
  const status = select.value;
  if (status === previousStatus) return;
  select.disabled = true;
  elements.applicationLedgerMessage.className = "form-message application-ledger-message";
  elements.applicationLedgerMessage.textContent = `Saving ${job.company || "application"}: ${status}...`;
  try {
    const application = await requestJson(
      `/api/jobs/${encodeURIComponent(job.id)}/application/status`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status, occurred_at: todayInputValue(), note: "" }),
      }
    );
    storeJobApplication(application);
    elements.applicationLedgerMessage.textContent = `Saved ${job.company || job.title}: ${status}.`;
  } catch (error) {
    select.value = previousStatus;
    elements.applicationLedgerMessage.className = "form-message application-ledger-message error";
    elements.applicationLedgerMessage.textContent = formatRequestError(error);
  } finally {
    renderJobs();
  }
}

async function saveApplicationStatus(event) {
  event.preventDefault();
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid || state.jobApplicationLoading) return;
  const status = elements.applicationStatusSelect.value;
  const note = elements.applicationStatusNote.value.trim();
  if (status === state.jobApplication?.current_status && !note) {
    elements.applicationMessage.className = "form-message error";
    elements.applicationMessage.textContent = "Choose a different status or add a note.";
    return;
  }
  state.jobApplicationLoading = true;
  elements.applicationMessage.className = "form-message";
  elements.applicationMessage.textContent = "Adding status update...";
  renderJobApplication();
  try {
    const application = await requestJson(
      `/api/jobs/${encodeURIComponent(job.id)}/application/status`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          status,
          occurred_at: elements.applicationStatusDate.value || null,
          note,
        }),
      }
    );
    storeJobApplication(application);
    elements.applicationStatusNote.value = "";
    elements.applicationMessage.textContent = `Status added: ${status}.`;
    toast.show({ message: `Application status updated to ${applicationStatusLabel(status)}.` });
  } catch (error) {
    elements.applicationMessage.className = "form-message error";
    elements.applicationMessage.textContent = formatRequestError(error);
  } finally {
    state.jobApplicationLoading = false;
    renderJobs();
  }
}
function renderJobAssessment() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid || state.jobAssessmentLoading) {
    elements.jobAssessment.classList.add("hidden");
    return;
  }
  if (!state.jobAssessment && !state.jobAssessmentError) {
    elements.jobAssessment.classList.add("hidden");
    return;
  }
  elements.jobAssessment.classList.remove("hidden");
  elements.jobAssessment.innerHTML = renderAssessmentSummary(
    state.jobAssessmentError ? null : state.jobAssessment,
    { job, profile: state.profileRecord, title: "Application assessment" },
  );
}

function openAssessmentResolution(button) {
  const findingKey = button?.dataset.assessmentFindingKey || "";
  if (!findingKey || !state.jobAssessment) return;
  const existing = (state.jobAssessment.user_resolutions || []).find(
    (item) => item.finding_key === findingKey,
  );
  state.assessmentResolutionFindingKey = findingKey;
  state.assessmentResolutionEditTarget = button.dataset.assessmentEditTarget || "";
  elements.assessmentResolutionFinding.textContent = button.dataset.assessmentFindingLabel
    || findingKey;
  const normalizedExisting = ["accepted_risk"].includes(existing?.resolution)
    ? "accepted_risk"
    : existing
      ? "resolved"
      : ["warning", "danger"].includes(button.dataset.assessmentFindingTone)
        ? "accepted_risk"
        : "resolved";
  elements.assessmentResolutionValues.forEach((input) => {
    input.checked = input.value === normalizedExisting;
  });
  elements.assessmentResolutionNote.value = existing?.note || "";
  elements.assessmentResolutionMessage.className = "form-message";
  elements.assessmentResolutionMessage.textContent = existing?.stale
    ? "The assessment changed after this decision. Record a new decision to confirm it again."
    : "";
  elements.assessmentResolutionDialog.showModal();
  elements.assessmentResolutionValues.find((input) => input.checked)?.focus();
}

function closeAssessmentResolution() {
  state.assessmentResolutionFindingKey = "";
  state.assessmentResolutionEditTarget = "";
  if (elements.assessmentResolutionDialog.open) {
    elements.assessmentResolutionDialog.close();
  }
}

async function saveAssessmentResolution(event) {
  event.preventDefault();
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  const findingKey = state.assessmentResolutionFindingKey;
  if (!job?.valid || !findingKey) return;
  const revisioned = api.capabilities.assessment_resolution_revision === true;
  const assessmentRevision = state.jobAssessment?.assessment_revision || "";
  if (revisioned && !/^sha256:[0-9a-f]{64}$/.test(assessmentRevision)) {
    elements.assessmentResolutionMessage.className = "form-message error";
    elements.assessmentResolutionMessage.textContent = "Assessment revision unavailable. Close this review and reload the assessment.";
    return;
  }
  elements.saveAssessmentResolutionButton.disabled = true;
  elements.assessmentResolutionMessage.className = "form-message";
  elements.assessmentResolutionMessage.textContent = "Recording decision...";
  try {
    const payload = {
      finding_key: findingKey,
      resolution: elements.assessmentResolutionValues.find((input) => input.checked)?.value || "accepted_risk",
      note: elements.assessmentResolutionNote.value,
    };
    if (revisioned) payload.expected_assessment_revision = assessmentRevision;
    const assessment = await requestJson(
      `/api/jobs/${encodeURIComponent(job.id)}/assessment/resolution`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      },
    );
    state.jobAssessment = assessment;
    if (job.application) job.application.assessment = assessment;
    closeAssessmentResolution();
    renderJobAssessment();
    setJobWorkspaceTab(state.jobWorkspaceTab);
    renderJobApplication();
    toast.show({ message: "Assessment decision recorded." });
  } catch (error) {
    if (revisioned && error?.payload?.error_code === "JOB_REVISION_CONFLICT") {
      closeAssessmentResolution();
      await loadSelectedJobAssessment();
      toast.show({ message: "Assessment changed. Review the latest findings before recording a decision." });
      return;
    }
    elements.assessmentResolutionMessage.className = "form-message error";
    elements.assessmentResolutionMessage.textContent = formatRequestError(error);
  } finally {
    elements.saveAssessmentResolutionButton.disabled = false;
  }
}

function correctAssessmentFacts() {
  const target = state.assessmentResolutionEditTarget;
  closeAssessmentResolution();
  if (target === "eligibility") {
    setJobWorkspaceTab("assessment");
    openJobGatesEditor();
    elements.jobEligibilitySection.scrollIntoView({ behavior: "smooth", block: "center" });
    return;
  }
  if (target === "requirements") {
    setJobWorkspaceTab("assessment");
    openJobRequirementsEditor();
    elements.jobRequirementsSection.scrollIntoView({ behavior: "smooth", block: "center" });
    return;
  }
  setJobWorkspaceTab("details");
  openJobEditForm();
  elements.jobFactGrid.scrollIntoView({ behavior: "smooth", block: "center" });
}

async function loadSelectedJobAssessment() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid) {
    state.jobAssessment = null;
    state.jobAssessmentLoading = false;
    state.jobAssessmentError = "";
    renderJobAssessment();
    return;
  }
  const requestedJobId = job.id;
  state.jobAssessment = null;
  state.jobAssessmentLoading = true;
  state.jobAssessmentError = "";
  renderJobAssessment();
  try {
    const assessment = await requestJson(`/api/jobs/${encodeURIComponent(requestedJobId)}/assessment`);
    if (state.selectedJobId !== requestedJobId) return;
    state.jobAssessment = assessment;
  } catch (error) {
    if (state.selectedJobId !== requestedJobId) return;
    state.jobAssessmentError = formatRequestError(error);
  } finally {
    if (state.selectedJobId === requestedJobId) {
      state.jobAssessmentLoading = false;
      renderJobAssessment();
    }
  }
}

function hideJobEditForm({ render = true } = {}) {
  state.jobIdentityEditing = false;
  elements.editJobDetailsButton.setAttribute("aria-expanded", "false");
  elements.jobEditMessage.className = "form-message";
  elements.jobEditMessage.textContent = "";
  if (render) renderJobDigest();
}

function openJobEditForm() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid || state.jobEditSaving) return;
  state.jobIdentityEditing = true;
  setJobWorkspaceTab("details");
  hideJobEditMessage();
  elements.editJobDetailsButton.setAttribute("aria-expanded", "true");
  renderJobDigest();
  document.querySelector("#job-edit-company")?.focus();
}

function hideJobEditMessage() {
  elements.jobEditMessage.className = "form-message";
  elements.jobEditMessage.textContent = "";
}

async function persistJobFacts(job, fields) {
  let result;
  try {
    result = await api.saveJobDetails(job.id, fields, job.card_revision);
  } catch (error) {
    const operation = error?.payload?.operation_state;
    if (api.capabilities.job_details_revision === true && (
      error?.payload?.error_code === "JOB_DETAILS_REVISION_CONFLICT" ||
      operation?.committed === true || operation?.known === false
    )) {
      // Read the exact job, including when it was selected from a later page.
      // Never re-submit an edit based on a card the user has not reviewed.
      try {
        const latest = await api.getJob(job.id);
        const index = state.jobs.findIndex((item) => item.id === job.id);
        if (index >= 0 && latest?.job?.valid) state.jobs[index] = { ...state.jobs[index], ...latest.job };
        renderJobs();
        if (state.selectedJobId === job.id) await loadSelectedJobAssessment();
      } catch { /* Original conflict is still the actionable error. */ }
    }
    throw error;
  }
  const index = state.jobs.findIndex((item) => item.id === result.job.id);
  if (index >= 0) state.jobs[index] = { ...state.jobs[index], ...result.job };
  renderJobs();
  if (state.selectedJobId === job.id) await loadSelectedJobAssessment();
  return result.job;
}

async function updateSelectedJobFacts(fields) {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid) throw new Error("The selected job is no longer available.");
  return persistJobFacts(job, fields);
}

async function saveJobDetails() {
  if (state.jobEditSaving) return;
  state.jobEditSaving = true;
  elements.saveJobEditButton.disabled = true;
  elements.saveJobEditButton.textContent = "Saving...";
  hideJobEditMessage();
  try {
    await updateSelectedJobFacts({
      company: document.querySelector("#job-edit-company")?.value || "",
      title: document.querySelector("#job-edit-title")?.value || "",
      seniority: document.querySelector("#job-edit-seniority")?.value || "",
      location: document.querySelector("#job-edit-location")?.value || "",
      remote_policy: document.querySelector("#job-edit-remote-policy")?.value || "",
      source_url: document.querySelector("#job-edit-source-url")?.value || "",
    });
    state.jobIdentityEditing = false;
    renderJobDigest();
    toast.show({ message: "Job facts updated." });
  } catch (error) {
    elements.jobEditMessage.className = "form-message error";
    elements.jobEditMessage.textContent = formatRequestError(error);
  } finally {
    state.jobEditSaving = false;
    elements.saveJobEditButton.disabled = false;
    elements.saveJobEditButton.textContent = "Save";
  }
}

function setJobGateEditing(editing) {
  state.jobGatesEditing = editing;
  elements.jobGatesMessage.className = "form-message";
  elements.jobGatesMessage.textContent = "";
  renderJobDigest();
}

function openJobGatesEditor() {
  setJobWorkspaceTab("assessment");
  setJobGateEditing(true);
  document.querySelector("[data-job-gate-name]")?.focus();
}

async function saveJobGates() {
  if (state.jobEditSaving) return;
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid) return;
  const currentGates = job.digest?.eligibility_gates || {};
  const eligibilityGates = {};
  document.querySelectorAll("[data-job-gate-name]").forEach((select) => {
    const name = select.dataset.jobGateName;
    const original = currentGates[name] || {};
    const changed = select.value !== original.status;
    eligibilityGates[name] = {
      status: select.value,
      reasons: changed ? ["Manually reviewed for this saved job."] : (original.reasons || []),
      evidence: changed ? [] : (original.evidence || []),
    };
  });
  const byName = Object.fromEntries(
    [...document.querySelectorAll("[data-job-gate-name]")].map((select) => [select.dataset.jobGateName, select.value]),
  );
  const visaSignals = { ...job.visa_signals };
  visaSignals.citizenship_required = byName.citizenship === "not_stated" ? null : true;
  visaSignals.clearance_required = byName.clearance === "not_stated" ? null : true;
  if (byName.sponsorship === "incompatible") {
    visaSignals.no_visa_or_opt = true;
    visaSignals.sponsorship_mentioned = true;
  } else if (byName.sponsorship === "not_stated") {
    visaSignals.no_visa_or_opt = null;
    visaSignals.sponsorship_mentioned = null;
  } else {
    visaSignals.no_visa_or_opt = null;
    visaSignals.sponsorship_mentioned = true;
  }
  state.jobEditSaving = true;
  elements.saveJobGatesButton.disabled = true;
  try {
    await updateSelectedJobFacts({ eligibility_gates: eligibilityGates, visa_signals: visaSignals });
    state.jobGatesEditing = false;
    renderJobDigest();
    toast.show({ message: "Eligibility facts updated." });
  } catch (error) {
    elements.jobGatesMessage.className = "form-message error";
    elements.jobGatesMessage.textContent = formatRequestError(error);
  } finally {
    state.jobEditSaving = false;
    elements.saveJobGatesButton.disabled = false;
  }
}

function setJobRequirementsEditing(editing) {
  state.jobRequirementsEditing = editing;
  elements.jobRequirementsMessage.className = "form-message";
  elements.jobRequirementsMessage.textContent = "";
  renderJobDigest();
}

function openJobRequirementsEditor() {
  setJobWorkspaceTab("details");
  setJobRequirementsEditing(true);
  document.querySelector("#job-edit-required-skills")?.focus();
}

function linesFromJobEditor(id) {
  return vaultLines(document.querySelector(id)?.value || "", true);
}

async function saveJobRequirements() {
  if (state.jobEditSaving) return;
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid) return;
  const experienceRequirements = [...document.querySelectorAll("[data-job-experience-row]")]
    .map((row) => {
      const minimum = Number(row.querySelector("[data-experience-min]")?.value);
      const maximumValue = row.querySelector("[data-experience-max]")?.value;
      const area = row.querySelector("[data-experience-area]")?.value.trim() || "";
      if (!area || !Number.isFinite(minimum) || minimum < 0) return null;
      const item = { minimum_years: minimum, area };
      if (maximumValue !== "") item.maximum_years = Number(maximumValue);
      return item;
    })
    .filter(Boolean);
  const classified = (job.digest?.classified_requirements || []).filter((_, index) => (
    document.querySelector(`[data-classified-requirement-index="${index}"]`)?.checked
  ));
  state.jobEditSaving = true;
  elements.saveJobRequirementsButton.disabled = true;
  try {
    await updateSelectedJobFacts({
      required_skills: linesFromJobEditor("#job-edit-required-skills"),
      required_platforms: linesFromJobEditor("#job-edit-required-platforms"),
      required_capabilities: linesFromJobEditor("#job-edit-required-capabilities"),
      preferred_skills: linesFromJobEditor("#job-edit-preferred-skills"),
      education_requirements: linesFromJobEditor("#job-edit-education-requirements"),
      experience_requirements: experienceRequirements,
      classified_requirements: classified,
    });
    state.jobRequirementsEditing = false;
    renderJobDigest();
    toast.show({ message: "Requirement facts updated." });
  } catch (error) {
    elements.jobRequirementsMessage.className = "form-message error";
    elements.jobRequirementsMessage.textContent = formatRequestError(error);
  } finally {
    state.jobEditSaving = false;
    elements.saveJobRequirementsButton.disabled = false;
  }
}

function renderJobs() {
  if (!state.jobs.length) {
    elements.jobDraftSelect.innerHTML = '<option value="">No saved job descriptions</option>';
    elements.jobDraftSelect.disabled = true;
    jobDraftCombobox.setOptions([]);
    jobDraftCombobox.setDisabled(true);
    elements.loadJobSourceButton.disabled = true;
    elements.reanalyzeJobButton.disabled = true;
    elements.jobSavedCount.textContent = "0 saved";
    elements.loadMoreJobsButton.classList.add("hidden");
    elements.jobSelectionMeta.textContent = "Save a job description to create the first local record.";
    renderJobLibraryApplication();
    renderJobWorkspaceMaterials();
    renderApplicationLedger();
    elements.jobApplicationSection.classList.add("hidden");
    elements.jobDigestSection.classList.add("hidden");
    elements.jobAssessment.classList.add("hidden");
    elements.jobInstructionChecklist.classList.add("hidden");
    elements.jobContactCandidates.classList.add("hidden");
    return;
  }
  if (!state.jobs.some((job) => job.id === state.selectedJobId)) {
    state.selectedJobId = state.jobs[0].id;
  }

  elements.jobDraftSelect.disabled = false;
  jobDraftCombobox.setDisabled(false);
  elements.loadJobSourceButton.disabled = false;
  const loadedLabel = state.jobs.length < state.jobsTotal
    ? `${state.jobs.length.toLocaleString()} loaded / ${state.jobsTotal.toLocaleString()} saved`
    : `${state.jobsTotal.toLocaleString()} saved`;
  elements.jobSavedCount.textContent = loadedLabel;
  elements.loadMoreJobsButton.classList.toggle("hidden", !state.jobsNextCursor);
  elements.loadMoreJobsButton.textContent = `Load more (${state.jobs.length}/${state.jobsTotal})`;
  elements.jobDraftSelect.innerHTML = state.jobs.map((job) => {
    const role = [job.company, job.title].filter(Boolean).join(" / ") || job.id;
    const status = job.valid ? job.status : "invalid";
    return '<option value="' + escapeHtml(job.id) + '">' + escapeHtml(role + " - " + status) + "</option>";
  }).join("");
  elements.jobDraftSelect.value = state.selectedJobId;
  jobDraftCombobox.setOptions(state.jobs.map((job) => ({
    value: job.id,
    label: `${[job.company, job.title].filter(Boolean).join(" / ") || job.id} - ${job.valid ? job.status : "invalid"}`,
  })));
  jobDraftCombobox.setValue(state.selectedJobId);

  const selectedJob = state.jobs.find((job) => job.id === state.selectedJobId);
  const selectedStatus = selectedJob.valid ? selectedJob.status : "invalid";
  const analysisStatus = selectedJob.analysis_stale ? "analysis update available" : "analysis current";
  elements.reanalyzeJobButton.disabled = selectedStatus !== "draft";
  elements.reanalyzeJobButton.textContent = selectedStatus === "draft" ? "Refresh analysis" : "Analysis locked";
  elements.jobSelectionMeta.textContent = selectedStatus + " | " + analysisStatus + " | "
    + Number(selectedJob.source_chars || 0).toLocaleString()
    + " characters | updated " + formatTime(selectedJob.modified_at);
  renderJobLibraryApplication();
  renderJobWorkspaceMaterials();
  renderApplicationLedger();
  renderJobApplication();
  renderJobDigest();
  renderJobInstructions();
  renderJobContactCandidates();
  renderJobAssessment();
  setJobWorkspaceTab(state.jobWorkspaceTab);
}

async function selectJob(jobId, { scroll = false, autoReanalyze = true } = {}) {
  if (!state.jobs.some((job) => job.id === jobId)) return;
  state.selectedJobId = jobId;
  state.jobSourcePreview = null;
  state.jobAssessment = null;
  state.jobAssessmentError = "";
  state.jobInstructions = null;
  state.jobInstructionsError = "";
  elements.jobInstructionMessage.className = "form-message";
  elements.jobInstructionMessage.textContent = "";
  hideJobEditForm();
  renderJobs();
  const selectedJob = state.jobs.find((job) => job.id === jobId);
  if (autoReanalyze && api.capabilities.job_reanalysis_revision !== true && selectedJob?.status === "draft" && selectedJob.analysis_stale) {
    await reanalyzeSelectedJob({ automatic: true });
  } else {
    await Promise.all([
      loadSelectedJobAssessment(),
      loadSelectedJobInstructions(),
    ]);
  }
  if (state.activeView === "job-library") {
    await loadSelectedJobApplication({ includeSession: false });
  }
  if (scroll) {
    elements.jobDigestSection.scrollIntoView({ behavior: "smooth", block: "start" });
  }
}

function updateJobCharacterCount() {
  const count = elements.jobSourceText.value.length;
  elements.jobCharacterCount.textContent = `${count.toLocaleString()} characters`;
}

function mergeCollection(existing, incoming, key) {
  const merged = new Map(existing.map((item) => [item[key], item]));
  incoming.forEach((item) => merged.set(item[key], item));
  return [...merged.values()];
}

async function loadJobs({ force = false, autoReanalyze = true } = {}) {
  if (!jobsLoaded || force) {
    const jobsPage = await api.getJobs();
    state.jobs = jobsPage.items || [];
    state.jobsTotal = Number(jobsPage.total || 0);
    state.jobsNextCursor = jobsPage.next_cursor || null;
    jobsLoaded = true;
  }
  renderJobs();
  const selectedJob = state.jobs.find((job) => job.id === state.selectedJobId);
  if (state.activeView === "job-library" && autoReanalyze && api.capabilities.job_reanalysis_revision !== true && selectedJob?.status === "draft" && selectedJob.analysis_stale) {
    await reanalyzeSelectedJob({ automatic: true });
    if (state.jobApplication?.job_id !== state.selectedJobId) {
      await loadSelectedJobApplication({ includeSession: false });
    }
    return;
  }
  if (state.activeView === "job-library") {
    await Promise.all([
      loadSelectedJobAssessment(),
      loadSelectedJobInstructions(),
    ]);
    await loadSelectedJobApplication({ includeSession: false });
  }
  if (state.activeView === "applications") await loadSelectedJobApplication();
}

async function loadResumeCollections({ force = false } = {}) {
  if (!resumeCollectionsLoaded || force) {
    const [sourcesPage, extractionsPage] = await Promise.all([
      api.getSources({ limit: 50 }),
      api.getExtractions({ limit: 100 }),
    ]);
    state.sources = sourcesPage.items || [];
    state.sourcesTotal = Number(sourcesPage.total || 0);
    state.sourcesNextCursor = sourcesPage.next_cursor || null;
    state.extractions = extractionsPage.items || [];
    state.extractionsTotal = Number(extractionsPage.total || 0);
    state.extractionsNextCursor = extractionsPage.next_cursor || null;
    resumeCollectionsLoaded = true;
  }
  renderSources();
  renderExtractions();
  if (state.activeView === "review" && state.reviewDetail) renderReview();
}

async function loadState({ autoReanalyze = true } = {}) {
  await Promise.all([
    loadJobs({ force: true, autoReanalyze }),
    loadResumeCollections({ force: true }),
  ]);
}

async function ensureViewData(viewName, { force = false } = {}) {
  if (["vault", "projects", "education"].includes(viewName)) {
    return withVaultLoading(viewName, async () => {
      if (!await ensureVaultSetup()) {
        if (state.activeView === viewName) switchView("profile");
        return;
      }
      if (viewName === "vault") await loadVaultExperiences({ refresh: force });
      else if (viewName === "projects") await loadVaultProjects({ refresh: force });
      else await loadVaultEducation({ refresh: force });
    });
  }
  if (viewName === "profile") {
    if (!await ensureVaultSetup()) return;
    await Promise.all([
      loadVaultProfile({ refresh: force }),
      loadEligibilityProfile(),
      loadHardBlockerPolicy(),
      loadApplicationHelperConfiguration(),
      loadDocuments(),
    ]);
  } else if (viewName === "skills") {
    await loadSkillsHub({ force });
  } else if (viewName === "documents") {
    await loadDocuments();
  } else if (viewName === "contacts") {
    await loadContacts();
  } else if (viewName === "demand-gaps") {
    await Promise.all([
      loadDemandGaps({ force: true }),
      loadJobs({ force, autoReanalyze: false }),
    ]);
    renderDemandGaps();
  } else if (viewName === "job-feed") {
    await loadDiscoveries();
  } else if (["job-library", "applications"].includes(viewName)) {
    await loadJobs({ force, autoReanalyze: viewName === "job-library" });
    if (viewName === "job-library") await loadResumeLibrary();
    if (viewName === "applications") {
      await Promise.all([
        loadApplicationEmailUpdates(),
        loadApplicationSessionReviews(),
      ]);
    }
  } else if (viewName === "application-materials") {
    await Promise.all([
      loadJobs({ force, autoReanalyze: false }),
      loadResumeLibrary(),
    ]);
    if (force && state.materialEditorJobId) {
      if (state.materialEditorTab === "resume") {
        await loadResumeMaterial({ force: true });
      } else {
        await loadCoverLetterMaterial({ force: true });
      }
    }
  } else if (viewName === "resume-library") {
    await loadResumeLibraryManagement({ force });
  } else if (viewName === "review" || (viewName === "intake"
    && elements.resumeIntentInputs.find((input) => input.checked)?.value !== "applications")) {
    await loadResumeCollections({ force });
  }
}

async function refreshActiveView() {
  try {
    await ensureViewData(state.activeView, { force: true });
  } catch (error) {
    elements.runMessage.className = "form-message error";
    elements.runMessage.textContent = formatRequestError(error);
  }
}

async function loadMoreJobs() {
  if (!state.jobsNextCursor) return;
  elements.loadMoreJobsButton.disabled = true;
  try {
    const page = await api.getJobs({ cursor: state.jobsNextCursor });
    state.jobs = mergeCollection(state.jobs, page.items || [], "id");
    state.jobsTotal = Number(page.total || state.jobsTotal);
    state.jobsNextCursor = page.next_cursor || null;
    renderJobs();
  } catch (error) {
    elements.jobLibraryMessage.className = "form-message error";
    elements.jobLibraryMessage.textContent = formatRequestError(error);
  } finally {
    elements.loadMoreJobsButton.disabled = false;
  }
}

async function loadMoreSources() {
  if (!state.sourcesNextCursor) return;
  elements.loadMoreSourcesButton.disabled = true;
  try {
    const page = await api.getSources({ cursor: state.sourcesNextCursor, limit: 50 });
    state.sources = mergeCollection(state.sources, page.items || [], "name");
    state.sourcesTotal = Number(page.total || state.sourcesTotal);
    state.sourcesNextCursor = page.next_cursor || null;
    renderSources();
  } catch (error) {
    elements.runMessage.className = "form-message error";
    elements.runMessage.textContent = formatRequestError(error);
  } finally {
    elements.loadMoreSourcesButton.disabled = false;
  }
}

async function loadJobSourceFile() {
  const file = elements.jobSourceFile.files[0];
  if (!file) return;
  const suffix = file.name.toLowerCase().split(".").pop();
  if (!["txt", "md"].includes(suffix)) {
    elements.jobMessage.className = "form-message error";
    elements.jobMessage.textContent = "Choose a TXT or Markdown file.";
    elements.jobSourceFile.value = "";
    return;
  }
  if (file.size > 500_000) {
    elements.jobMessage.className = "form-message error";
    elements.jobMessage.textContent = "Job description must not exceed 500,000 bytes.";
    elements.jobSourceFile.value = "";
    return;
  }
  try {
    elements.jobSourceText.value = await file.text();
    elements.jobSourceName.value = file.name;
    resetJobIntakeIdempotency();
    elements.jobMessage.className = "form-message";
    elements.jobMessage.textContent = `Loaded ${file.name}`;
    updateJobCharacterCount();
  } catch (error) {
    elements.jobMessage.className = "form-message error";
    elements.jobMessage.textContent = `Could not read ${file.name}: ${error.message}`;
  }
}

async function reanalyzeSelectedJob({ automatic = false } = {}) {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job || job.status !== "draft") return;
  const requestedJobId = job.id;
  const message = elements.jobLibraryMessage;
  const originalLabel = elements.reanalyzeJobButton.textContent;
  elements.reanalyzeJobButton.disabled = true;
  elements.reanalyzeJobButton.textContent = "Refreshing...";
  if (automatic) {
    message.className = "form-message job-library-application-message";
    message.textContent = `Updating ${job.company || job.title || job.id} with the current analyzer...`;
  }
  try {
    const result = await api.reanalyzeJob(job.id, job.card_revision);
    if (state.selectedJobId !== requestedJobId) return;
    state.selectedJobId = result.job.id;
    await loadJobs({ force: true, autoReanalyze: false });
    message.className = "form-message job-library-application-message";
    message.textContent = "Analysis refreshed.";
  } catch (error) {
    const stateAfterFailure = error?.payload?.operation_state;
    if (api.capabilities.job_reanalysis_revision === true && (
      stateAfterFailure?.revision_refresh_required === true ||
      stateAfterFailure?.committed === true || stateAfterFailure?.known === false
    )) {
      try {
        const latest = (await api.getJob(requestedJobId))?.job;
        if (latest?.id === requestedJobId) {
          upsertJobInState(latest);
          renderJobs();
        }
      } catch { /* Preserve the original operation error; no blind retry. */ }
    }
    message.className = "form-message job-library-application-message error";
    message.textContent = formatRequestError(error);
  } finally {
    const refreshedJob = state.jobs.find((item) => item.id === state.selectedJobId);
    elements.reanalyzeJobButton.disabled = refreshedJob?.status !== "draft";
    elements.reanalyzeJobButton.textContent = refreshedJob?.status === "draft" ? originalLabel : "Analysis locked";
    await pollLogs();
  }
}

async function loadSelectedJobSource() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job) return;
  const originalLabel = elements.loadJobSourceButton.textContent;
  elements.loadJobSourceButton.disabled = true;
  elements.loadJobSourceButton.textContent = "Loading...";
  setJobWorkspaceTab("details");
  try {
    const source = await requestJson(`/api/jobs/${encodeURIComponent(job.id)}/source`);
    state.jobSourcePreview = {
      jobId: job.id,
      sourceName: source.source_name || job.raw_jd_path || "Saved source",
      sourceText: source.source_text || "",
    };
    renderJobDigest();
    elements.jobSourcePreview.open = true;
    elements.jobSourcePreview.scrollIntoView({ behavior: "smooth", block: "start" });
  } catch (error) {
    elements.jobSelectionMeta.textContent = formatRequestError(error);
  } finally {
    elements.loadJobSourceButton.disabled = false;
    elements.loadJobSourceButton.textContent = originalLabel;
  }
}
function upsertJobInState(job) {
  const existingIndex = state.jobs.findIndex((item) => item.id === job.id);
  if (existingIndex >= 0) {
    state.jobs.splice(existingIndex, 1, job);
  } else {
    state.jobs.unshift(job);
  }
}

function resetJobIntakeIdempotency() {
  state.jobIntakeIdempotencyKey = "";
}

function jobIntakeIdempotencyKey() {
  if (!state.jobIntakeIdempotencyKey) {
    state.jobIntakeIdempotencyKey = globalThis.crypto?.randomUUID
      ? globalThis.crypto.randomUUID()
      : `intake-${Date.now()}-${Math.random().toString(36).slice(2)}`;
  }
  return state.jobIntakeIdempotencyKey;
}

async function createJobDraft(initialStatus = "captured") {
  const sourceText = elements.jobSourceText.value;
  if (!sourceText.trim()) {
    elements.jobMessage.className = "form-message error";
    elements.jobMessage.textContent = "Paste or load a complete job description first.";
    return;
  }
  const isApplied = initialStatus === "applied";
  const idempotencyKey = jobIntakeIdempotencyKey();
  elements.createJobButton.disabled = true;
  elements.createAppliedJobButton.disabled = true;
  elements.createJobButton.textContent = isApplied ? "Saving..." : "Creating...";
  elements.createAppliedJobButton.textContent = isApplied ? "Saving..." : "Save as applied";
  elements.jobMessage.className = "form-message";
  elements.jobMessage.textContent = isApplied
    ? "Saving the posting, assessment, and Applied application..."
    : "Saving and analyzing the posting...";
  try {
    const result = await api.createJob({
      source_text: sourceText,
      source_name: elements.jobSourceName.value,
      company: elements.jobCompany.value,
      title: elements.jobTitle.value,
      source_url: elements.jobSourceUrl.value,
      posting_id: elements.jobPostingId.value,
      idempotency_key: idempotencyKey,
      initial_status: initialStatus,
    });
    state.selectedJobId = result.job.id;
    elements.jobMessage.textContent = result.duplicate
      ? isApplied
        ? "This posting was already saved. The existing application is now marked Applied."
        : "This posting was already saved. Opening the existing job."
      : isApplied
        ? "Applied application saved."
        : "Job saved and analyzed.";
    elements.jobSourceFile.value = "";
    elements.jobSourceText.value = "";
    elements.jobSourceName.value = "";
    elements.jobCompany.value = "";
    elements.jobTitle.value = "";
    elements.jobSourceUrl.value = "";
    elements.jobPostingId.value = "";
    resetJobIntakeIdempotency();
    updateJobCharacterCount();
    upsertJobInState(result.job);
    state.jobAssessment = result.job.assessment || null;
    state.jobAssessmentLoading = false;
    state.jobAssessmentError = "";
    switchView("job-library");
    renderJobs();
    if (!state.jobAssessment) await loadSelectedJobAssessment();
    if (result.duplicate) {
      elements.jobLibraryMessage.className = "form-message";
      elements.jobLibraryMessage.textContent = isApplied
        ? "This exact job description was already captured. The existing application is now marked Applied."
        : "This exact job description was already captured. The existing record is open.";
    }
  } catch (error) {
    elements.jobMessage.className = "form-message error";
    elements.jobMessage.textContent = formatRequestError(error);
  } finally {
    elements.createJobButton.disabled = false;
    elements.createJobButton.textContent = "Save and analyze";
    elements.createAppliedJobButton.disabled = false;
    elements.createAppliedJobButton.textContent = "Save as applied";
    await pollLogs();
  }
}

async function runExtraction() {
  await runExtractionForSource(elements.sourceSelect.value);
}

async function prepareReviewForSource(sourceId) {
  let existing = null;
  try {
    existing = await requestJson(`/api/reviews/${encodeURIComponent(sourceId)}`);
  } catch (error) {
    if (error.status !== 404) throw error;
  }
  const built = await requestJson("/api/reviews/build", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      source_id: sourceId,
      ...(existing?.review_revision
        ? { expected_review_revision: existing.review_revision }
        : {}),
    }),
  });
  if (built.review.entities.length) return built;
  return requestJson(`/api/reviews/${encodeURIComponent(sourceId)}/suggest`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ expected_review_revision: built.review_revision }),
  });
}

async function runExtractionForSource(sourceName) {
  const source = sourceName;
  if (!source) return;
  elements.runButton.disabled = true;
  elements.runButton.textContent = "Running...";
  elements.runMessage.className = "form-message";
  elements.runMessage.textContent = `Extracting ${source}`;
  try {
    const result = await requestJson("/api/extract", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ source, overwrite: elements.overwriteInput.checked }),
    });
    elements.runMessage.textContent = "Building the review and linking clear resume sections...";
    await prepareReviewForSource(result.source_id);
    elements.runMessage.textContent = `Ready to review: ${result.technology_count} technologies, ${result.bullet_count} bullets.`;
    await loadResumeCollections({ force: true });
    await openResumeReview(result.source_id);
  } catch (error) {
    elements.runMessage.className = "form-message error";
    elements.runMessage.textContent = formatRequestError(error);
  } finally {
    elements.runButton.disabled = false;
    elements.runButton.textContent = "Extract and review";
    await pollLogs();
  }
}

async function continueResumeReview(sourceId) {
  if (!sourceId) return;
  await openResumeReview(sourceId);
}

async function uploadDocument({ processAgain = false } = {}) {
  const file = elements.sourceUpload.files[0];
  if (!file) {
    elements.uploadMessage.className = "form-message upload-message error";
    elements.uploadMessage.textContent = "Choose a TXT or DOCX document first.";
    return;
  }
  const check = state.sourceUploadCheck?.file === file ? state.sourceUploadCheck : null;
  if (state.sourceUploadCheckLoading || (!processAgain && (check?.error || check?.prior_imports?.length || check?.same_name_conflict))) {
    renderExactReimportPanel();
    return;
  }
  elements.uploadButton.disabled = true;
  elements.uploadButton.textContent = "Extracting...";
  elements.uploadMessage.className = "form-message upload-message";
  elements.uploadMessage.textContent = processAgain
    ? `Creating a separate review for ${file.name}`
    : `Adding and analyzing ${file.name}`;
  try {
    const result = await requestJson("/api/upload", {
      method: "POST",
      headers: {
        "Content-Type": "application/octet-stream",
        "X-Source-Name": encodeURIComponent(file.name),
        ...(processAgain ? { "X-Source-Reimport": "process-again" } : {}),
      },
      body: file,
    });
    await loadResumeCollections({ force: true });
    elements.sourceSelect.value = result.name;
    elements.uploadMessage.textContent = result.reimported
      ? `Added locally as ${result.name}. Creating a new review...`
      : `Added locally: ${result.name}`;
    await runExtractionForSource(result.name);
  } catch (error) {
    elements.uploadMessage.className = "form-message upload-message error";
    elements.uploadMessage.textContent = formatRequestError(error);
  } finally {
    if (elements.sourceUpload.files[0] === file) {
      elements.sourceUpload.value = "";
    }
    clearSourceUploadCheck();
    elements.uploadButton.disabled = false;
    elements.uploadButton.textContent = "Extract and review";
    await pollLogs();
  }
}

elements.primaryNavigation.addEventListener("click", (event) => {
  const button = event.target.closest(".nav-item[data-view]");
  if (!button || !elements.primaryNavigation.contains(button)) return;
  button.closest("details")?.removeAttribute("open");
  setMobileNavigation(false);
  void navigateToView(button.dataset.view);
});
initHelpCenter({ onNavigate: navigateFromHelp });
elements.mobileNavigationToggle.addEventListener("click", () => {
  setMobileNavigation(!document.body.classList.contains("navigation-open"));
});
elements.mobileNavigationClose.addEventListener("click", () => {
  setMobileNavigation(false);
  elements.mobileNavigationToggle.focus();
});
elements.sidebarBackdrop.addEventListener("click", () => {
  setMobileNavigation(false);
  elements.mobileNavigationToggle.focus();
});
mobileNavigationMedia.addEventListener("change", () => setMobileNavigation(false));
elements.mobileAddButton.addEventListener("click", openMobileQuickActions);
document.querySelector("#open-diagnostics").addEventListener("click", () => { void navigateToView("diagnostics"); });
document.querySelector("#refresh-active-view").addEventListener("click", refreshActiveView);
const interfaceSettingsPanel = document.querySelector("#interface-settings-panel");
const interfaceSettingsTrigger = document.querySelector("#open-interface-settings");
interfaceSettingsPanel.addEventListener("toggle", () => {
  interfaceSettingsTrigger.setAttribute(
    "aria-expanded",
    String(interfaceSettingsPanel.matches(":popover-open")),
  );
});
document.addEventListener("keydown", (event) => {
  if (event.key !== "Escape") return;
  if (document.body.classList.contains("navigation-open")) {
    event.preventDefault();
    setMobileNavigation(false);
    elements.mobileNavigationToggle.focus();
    return;
  }
  if (interfaceSettingsPanel.matches(":popover-open")) {
    event.preventDefault();
    interfaceSettingsPanel.hidePopover();
    interfaceSettingsTrigger.focus();
  }
}, { capture: true });
document.querySelectorAll('[data-view="documents"]:not(.nav-item)').forEach((button) => button.addEventListener("click", () => { void navigateToView("documents"); }));

elements.runButton.addEventListener("click", runExtraction);
elements.uploadButton.addEventListener("click", uploadDocument);
elements.sourceUpload.addEventListener("change", inspectUploadedDocument);
elements.resumeImportsInProgressList.addEventListener("click", (event) => {
  const button = event.target.closest("[data-resume-review-id]");
  if (!button) return;
  void continueResumeReview(button.dataset.resumeReviewId);
});
elements.libraryResumeFile.addEventListener("change", () => {
  const file = elements.libraryResumeFile.files[0];
  if (file && !elements.libraryResumeDisplayName.value.trim()) {
    elements.libraryResumeDisplayName.value = file.name.replace(/\.[^.]+$/, "");
  }
});
elements.saveLibraryResumeButton.addEventListener("click", saveLibraryResume);
elements.resumeIntentInputs.forEach((input) => {
  input.addEventListener("change", () => {
    if (input.checked) setResumeIntent(input.value);
  });
  input.addEventListener("keydown", (event) => {
    const offset = {
      ArrowLeft: -1,
      ArrowUp: -1,
      ArrowRight: 1,
      ArrowDown: 1,
    }[event.key];
    if (offset === undefined) return;
    event.preventDefault();
    const currentIndex = elements.resumeIntentInputs.indexOf(input);
    const nextIndex = (currentIndex + offset + elements.resumeIntentInputs.length)
      % elements.resumeIntentInputs.length;
    const nextInput = elements.resumeIntentInputs[nextIndex];
    selectResumeIntent(nextInput);
    nextInput.focus();
  });
});
setResumeIntent(elements.resumeIntentInputs.find((input) => input.checked)?.value || "vault");
elements.loadMoreSourcesButton.addEventListener("click", loadMoreSources);
elements.loadMoreJobsButton.addEventListener("click", loadMoreJobs);
elements.jobSourceFile.addEventListener("change", loadJobSourceFile);
elements.jobSourceText.addEventListener("input", () => {
  updateJobCharacterCount();
  resetJobIntakeIdempotency();
});
[elements.jobSourceName, elements.jobCompany, elements.jobTitle, elements.jobSourceUrl, elements.jobPostingId]
  .forEach((input) => input.addEventListener("input", resetJobIntakeIdempotency));
elements.createJobButton.addEventListener("click", () => createJobDraft("captured"));
elements.createAppliedJobButton.addEventListener("click", () => createJobDraft("applied"));
elements.jobDraftSelect.addEventListener("change", (event) => selectJob(event.target.value));
elements.loadJobSourceButton.addEventListener("click", loadSelectedJobSource);
elements.reanalyzeJobButton.addEventListener("click", () => reanalyzeSelectedJob());
elements.jobWorkspaceTabButtons.forEach((button, index) => {
  button.addEventListener("click", () => setJobWorkspaceTab(button.dataset.jobWorkspaceTab));
  button.addEventListener("keydown", (event) => {
    if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const offset = event.key === "ArrowRight" ? 1 : -1;
    const nextIndex = event.key === "Home"
      ? 0
      : event.key === "End"
        ? elements.jobWorkspaceTabButtons.length - 1
        : (index + offset + elements.jobWorkspaceTabButtons.length)
          % elements.jobWorkspaceTabButtons.length;
    const next = elements.jobWorkspaceTabButtons[nextIndex];
    setJobWorkspaceTab(next.dataset.jobWorkspaceTab, { focus: true });
  });
});
elements.editJobDetailsButton.addEventListener("click", openJobEditForm);
elements.cancelJobEditButton.addEventListener("click", () => hideJobEditForm());
elements.saveJobEditButton.addEventListener("click", saveJobDetails);
elements.editJobGatesButton.addEventListener("click", openJobGatesEditor);
elements.cancelJobGatesButton.addEventListener("click", () => setJobGateEditing(false));
elements.saveJobGatesButton.addEventListener("click", saveJobGates);
elements.editJobRequirementsButton.addEventListener("click", openJobRequirementsEditor);
elements.cancelJobRequirementsButton.addEventListener("click", () => setJobRequirementsEditing(false));
elements.saveJobRequirementsButton.addEventListener("click", saveJobRequirements);
elements.addJobSkillButton.addEventListener("click", () => {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (job?.valid) void openSkillQuickAction("skill", { jobId: job.id });
});
document.addEventListener("keydown", (event) => {
  if (event.key !== "Escape") return;
  if (state.jobIdentityEditing) hideJobEditForm();
  if (state.jobGatesEditing) setJobGateEditing(false);
  if (state.jobRequirementsEditing) setJobRequirementsEditing(false);
});
document.querySelector("#view-jobs").addEventListener("click", (event) => {
  const button = event.target.closest("[data-assessment-finding-key]");
  if (button) openAssessmentResolution(button);
});
elements.assessmentResolutionForm.addEventListener("submit", saveAssessmentResolution);
elements.closeAssessmentResolutionDialog.addEventListener("click", closeAssessmentResolution);
elements.cancelAssessmentResolutionButton.addEventListener("click", closeAssessmentResolution);
elements.correctAssessmentFactsButton.addEventListener("click", correctAssessmentFacts);
const refreshApplicationLedgerView = () => {
  renderApplicationLedger();
  renderJobApplication();
};
window.addEventListener("careerhub:applicationoutcomeschanged", () => {
  clearApplicationEmailUpdates();
  if (state.activeView === "applications") void loadApplicationEmailUpdates();
});
window.addEventListener("careerhub:opencontacts", (event) => {
  selectContact(event.detail?.contactId || null);
  switchView("contacts");
});
window.addEventListener("careerhub:openjob", (event) => {
  const jobId = event.detail?.jobId;
  if (!jobId) return;
  state.selectedJobId = jobId;
  switchView("job-library");
  void (async () => {
    await loadJobs({ force: true, autoReanalyze: false });
    await selectJob(jobId, { autoReanalyze: false });
  })();
});
window.addEventListener("careerhub:opendiscovery", (event) => {
  const discoveryId = event.detail?.discoveryId || null;
  state.jobFeedDiscoveryId = discoveryId;
  state.jobFeedIntent = null;
  setDiscoveryRouteTarget(discoveryId, null);
  switchView("job-feed");
});

elements.applicationLedgerSearch.addEventListener("input", refreshApplicationLedgerView);
elements.applicationLedgerStatusFilter.addEventListener("change", refreshApplicationLedgerView);
document.querySelectorAll("[data-application-scope]").forEach((button) => {
  button.addEventListener("click", () => {
    state.applicationLedgerScope = button.dataset.applicationScope;
    refreshApplicationLedgerView();
  });
});
elements.applicationLedgerList.addEventListener("click", (event) => {
  const button = event.target.closest(".application-open-button");
  if (button) {
    selectApplicationJob(button.dataset.applicationJobId);
    return;
  }
  if (event.target.closest("a, select, button")) return;
  const row = event.target.closest("[data-application-job-id]");
  if (row) selectApplicationJob(row.dataset.applicationJobId);
});
elements.applicationLedgerList.addEventListener("keydown", (event) => {
  if (event.key !== "Enter" || event.target.matches("select, a, button")) return;
  const row = event.target.closest("[data-application-job-id]");
  if (row) selectApplicationJob(row.dataset.applicationJobId);
});
elements.applicationLedgerList.addEventListener("change", (event) => {
  const select = event.target.closest(".application-inline-status");
  if (select) saveInlineApplicationStatus(select);
});
elements.applicationEmailUpdatesList.addEventListener("change", (event) => {
  const select = event.target.closest("[data-email-update-job]");
  if (!select) return;
  const button = select
    .closest(".application-email-update-linker")
    ?.querySelector("[data-email-update-link]");
  if (button) button.disabled = !select.value;
});
elements.applicationEmailUpdatesList.addEventListener("click", (event) => {
  const button = event.target.closest("[data-email-update-link]");
  if (button) void linkApplicationEmailUpdate(button);
});
elements.applicationEmailUpdatesMore.addEventListener("click", () => {
  void loadApplicationEmailUpdates({ append: true });
});
elements.applicationSessionReviewList.addEventListener("click", (event) => {
  const details = event.target.closest("[data-application-session-job-id]");
  if (details) {
    void selectApplicationJob(details.dataset.applicationSessionJobId);
    return;
  }
  const actionButton = event.target.closest("[data-application-session-action]");
  if (!actionButton) return;
  const row = actionButton.closest("[data-application-session-id]");
  if (!row) return;
  void resolveApplicationSessionReview(
    row.dataset.applicationSessionId,
    actionButton.dataset.applicationSessionAction,
    actionButton.dataset.sessionRevision,
  );
});
elements.jobLibraryApplicationBar.addEventListener("click", (event) => {
  if (event.target.closest("[data-open-selected-application]")) {
    openSelectedApplication();
  }
});
elements.applicationDrawerBackdrop.addEventListener("click", closeApplicationDrawer);
elements.closeApplicationDrawer.addEventListener("click", closeApplicationDrawer);
elements.openApplicationJobButton.addEventListener("click", openSelectedJobInLibrary);
elements.startApplicationSessionButton.addEventListener("click", startAttendedApplicationSession);
elements.cancelApplicationSessionButton.addEventListener("click", cancelAttendedApplicationSession);
elements.applicationResumeFile.addEventListener("change", () => {
  const file = elements.applicationResumeFile.files[0];
  if (file && !elements.applicationResumeDisplayName.value.trim()) {
    elements.applicationResumeDisplayName.value = file.name.replace(/\.[^.]+$/, "");
  }
});
elements.clearApplicationResumeSelectionButton.addEventListener("click", () => {
  void selectApplicationResume(null);
});
elements.recordSubmittedResumeButton.addEventListener("click", recordSubmittedResume);
elements.openApplicationResume.addEventListener("click", (event) => {
  void openSubmittedResumeFile(event, elements.applicationResumeMessage);
});
elements.openSubmittedResumeSummary.addEventListener("click", (event) => {
  void openSubmittedResumeFile(event, elements.applicationMessage);
});
elements.attachApplicationResumeButton.addEventListener("click", attachApplicationResume);
elements.applicationStatusForm.addEventListener("submit", saveApplicationStatus);

async function openResumeReview(sourceId) {
  if (!sourceId) return;
  if (!(await confirmReviewNavigation("review", sourceId))) return;
  const reviewEpoch = beginReviewLoad(sourceId);
  switchView("review");
  await loadReview(sourceId, { epoch: reviewEpoch });
}

async function loadRuntimeInfo() {
  try {
    state.runtimeInfo = await api.getRuntimeInfo();
    const profile = state.runtimeInfo.data_profile || "external";
    const channel = state.runtimeInfo.channel || "unknown";
    const dataRoot = state.runtimeInfo.data_root || "Unavailable";
    const appVersion = state.runtimeInfo.app_version || "unknown";
    elements.runtimeDataProfile.textContent = `Data: ${profile} · ${channel}`;
    elements.runtimeDataRoot.textContent = dataRoot;
    elements.runtimeStorageNote.title = `Active Career Hub Data Home: ${dataRoot}`;
    elements.appVersion.textContent = appVersion === "unknown" ? "v—" : `v${appVersion}`;
    elements.appVersion.setAttribute(
      "aria-label",
      appVersion === "unknown" ? "Application version unavailable" : `Career Hub version ${appVersion}`,
    );
    const security = state.runtimeInfo.platform_security || {};
    const credentialStore = security.credential_store || {};
    const documentEncryption = security.document_encryption || {};
    elements.emailAppPasswordHelp.textContent = credentialStore.storage_copy
      || "Protected credential storage status is unavailable.";
    elements.emailSecurityHelp.textContent = credentialStore.help_copy
      || "Protected credential storage status is unavailable.";
    elements.documentSecurityCopy.textContent = documentEncryption.storage_copy
      || "Document encryption status is unavailable.";
  } catch (error) {
    elements.runtimeDataProfile.textContent = "Data Home unavailable";
    elements.runtimeDataRoot.textContent = formatRequestError(error);
    elements.appVersion.textContent = "v—";
    elements.appVersion.setAttribute("aria-label", "Application version unavailable");
  }
}

async function initialize() {
  try {
    initializeConnectivity();
    initDocuments();
    initDiscoveries();
    initContacts();
    initApplicationHelperConfiguration();
    initDesktopUpdates();
    initDesktopBackups();
    initDesktopAgent();
    initExtensionPairing(api);
    initDiagnostics({ navigateToView });
    initVaultShared();
    initVaultManualSkills();
    initVaultExperiences();
    initVaultProjects();
    initSkillsHub({ loadVaultEducation, loadVaultExperiences, loadVaultProjects, localRouteForView, navigateToView, openSkillQuickAction, selectEducationRecord, selectVaultExperience, selectVaultProject });
    initDemandGaps({ navigateToView, openSavedJobWorkspace });
    initSkillQuickAction({ persistJobFacts, reanalyzeSelectedJob });
    initProfile({ ensureViewData });
    initEducation();
    initIntakeReview({ loadResumeCollections, navigateToView, openSkillQuickAction });
    initResumeLibrary({ loadResumeCollections, openApplicationResumeIntake, openResumeReview, prepareReviewForSource, renderJobApplication, renderJobWorkspaceMaterials, uploadDocument });
    initApplicationMaterials({ closeApplicationDrawer, confirmReviewNavigation, coverLetterSummary, selectJob, switchView });
    initJobInstructions({ loadSelectedJobSource });
    initJobDigest({ hideJobEditForm });
    loadSkillsHubModePreference();
    loadSkillsRegistryLayoutPreference();
    loadCareerVaultLayoutPreference();
    loadDemandGapsPeriodPreference();
    await loadRuntimeInfo();
    await restoreLocalRoute();
  } catch (error) {
    elements.runMessage.className = "form-message error";
    elements.runMessage.textContent = formatRequestError(error);
  }
}

window.addEventListener("popstate", () => { void restoreLocalRoute(); });
if ("scrollRestoration" in window.history) window.history.scrollRestoration = "manual";
window.addEventListener("beforeunload", (event) => {
  if (!state.reviewDirty && !state.materialEditorDirty
      && !state.resumeMaterialDirty && !state.resumePlanDirty) return;
  event.preventDefault();
  event.returnValue = "";
});
initializeButtonTooltips();
initialize();
