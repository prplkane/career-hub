// Application materials editor: resume plan, resume material and cover letter drafts for the selected job.
import {
  elements,
  requestJson,
  state,
  toast,
} from "../app_context.js";
import { api } from "../ui/api.js";
import { formatRequestError } from "../ui/primitives.js";
import { canManageApplicationMaterials } from "./applications.js";
import { MaterialReview } from "./material_review.js";
import {
  coverLetterIssueSummary,
  materialIssueSummary,
  renderCoverLetterEvidenceCatalog,
  renderCoverLetterEvidenceItems,
  renderMaterialIssues,
  renderResumeBlocks,
  renderResumeEducation,
  renderResumeEvidenceRefs,
  renderResumeSkills,
} from "./materials.js";
import {
  firstResumePlanApprovalIssue,
  renderResumePlanEducation,
  renderResumePlanExperiences,
  renderResumePlanIssues,
  renderResumePlanProjects,
  renderResumePlanSections,
  renderResumePlanSkills,
} from "./resumes.js";

// Bootstrap hooks supplied by app.js through initApplicationMaterials().
let closeApplicationDrawer = null;
let confirmReviewNavigation = null;
let coverLetterSummary = null;
let selectJob = null;
let switchView = null;

const materialReviews = {
  resume: new MaterialReview({ root: document.querySelector("#resume-material-fact-review"), api, kind: "resume", onChange: updateMaterialReviewControls }),
  cover_letter: new MaterialReview({ root: document.querySelector("#cover-letter-fact-review"), api, kind: "cover_letter", onChange: updateMaterialReviewControls }),
};

function syncMaterialReviews() {
  const jobId = state.activeView === "application-materials" ? state.materialEditorJobId : null;
  materialReviews.resume.setContext({ jobId, editor: state.resumeMaterialEditor, dirty: state.resumeMaterialDirty, busy: state.resumeMaterialLoading });
  materialReviews.cover_letter.setContext({ jobId, editor: state.materialEditor, dirty: state.materialEditorDirty, busy: state.materialEditorLoading });
}

function updateMaterialReviewControls() {
  for (const kind of ["resume", "cover_letter"]) {
    const resume = kind === "resume";
    const editor = resume ? state.resumeMaterialEditor : state.materialEditor;
    const dirty = resume ? state.resumeMaterialDirty : state.materialEditorDirty;
    const loading = resume ? state.resumeMaterialLoading : state.materialEditorLoading;
    const review = materialReviews[kind];
    const buttons = resume ? elements.resumeMaterialExportButtons : elements.coverLetterExportButtons;
    const draftBlocked = !editor || loading || (resume && (!editor.persisted || dirty || !editor.validation?.ready_to_export));
    buttons.forEach((button) => { button.disabled = draftBlocked || review.busy || (editor?.persisted && !dirty && review.blocksExport); });
    const agentButton = resume ? elements.copyResumeAiCommand : elements.copyCoverLetterAiCommand;
    agentButton.disabled = !editor || loading || review.busy || review.supported || Boolean(review.error);
    agentButton.dataset.tooltip = review.supported
      ? "The agent adapter is not yet available in this Go migration preview. Edit this draft here."
      : "Copy the bounded Career Hub agent command. No external AI call is made by the browser.";
  }
}

function markResumePlanDirty() {
  state.resumePlanDirty = true;
  elements.resumePlanStatus.textContent = "Unsaved changes";
  elements.resumePlanIssueCount.textContent = "Save to validate";
  elements.resumePlanMessage.className = "form-message";
  elements.resumePlanMessage.textContent = "Selections changed locally.";
}

async function loadResumePlan({ force = false } = {}) {
  const jobId = state.resumePlanJobId;
  if (!jobId || state.resumePlanLoading) return;
  if (!force && state.resumePlanEditor?.plan?.job_id === jobId) return;
  state.resumePlanLoading = true;
  elements.resumePlanMessage.className = "form-message";
  elements.resumePlanMessage.textContent = "Loading Career Vault selections...";
  renderResumePlan();
  try {
    const editor = await requestJson(`/api/jobs/${encodeURIComponent(jobId)}/resume-plan`);
    if (state.resumePlanJobId !== jobId) return;
    state.resumePlanEditor = editor;
    state.resumePlanDirty = false;
    elements.resumePlanWorkspace.open = editor.plan?.status !== "approved";
    elements.resumePlanMessage.textContent = editor.persisted
      ? "Saved content selection loaded."
      : "Choose only the evidence that belongs in this resume.";
  } catch (error) {
    if (state.resumePlanJobId !== jobId) return;
    state.resumePlanEditor = null;
    elements.resumePlanMessage.className = "form-message error";
    elements.resumePlanMessage.textContent = formatRequestError(error);
  } finally {
    if (state.resumePlanJobId === jobId) {
      state.resumePlanLoading = false;
      renderResumePlan();
    }
  }
}

function renderResumePlan() {
  const editor = state.resumePlanEditor;
  const job = state.jobs.find((item) => item.id === state.resumePlanJobId);
  setResumeMaterialStep(state.resumeMaterialStep);
  elements.resumePlanTitle.textContent = "Choose what belongs in this resume";
  elements.resumePlanMeta.textContent = job
    ? [job.company, job.title].filter(Boolean).join(" / ") || "Career Vault evidence"
    : "Career Vault remains unchanged.";
  const disabled = state.resumePlanLoading || !editor;
  elements.resumePlanPages.disabled = disabled;
  elements.saveResumePlanButton.disabled = disabled || !state.resumePlanDirty;
  elements.approveResumePlanButton.disabled = disabled;
  if (!editor) {
    elements.resumePlanStatus.textContent = state.resumePlanLoading ? "Loading" : "Unavailable";
    elements.resumePlanRevision.textContent = "Not loaded";
    elements.resumePlanIssueCount.textContent = state.resumePlanLoading ? "Loading" : "Unavailable";
    elements.resumePlanIssues.innerHTML = "";
    elements.resumePlanSectionList.innerHTML = "";
    elements.resumePlanExperienceList.innerHTML = "";
    elements.resumePlanProjectSection.hidden = true;
    elements.resumePlanProjectList.innerHTML = "";
    elements.resumePlanSkillList.innerHTML = "";
    elements.resumePlanEducationList.innerHTML = "";
    return;
  }

  const plan = editor.plan;
  const catalog = editor.catalog;
  elements.resumePlanPages.value = String(plan.target_pages);
  elements.resumePlanStatus.textContent = state.resumePlanDirty
    ? "Editing"
    : plan.status === "approved" ? "Selected" : "In progress";
  elements.resumePlanRevision.textContent = editor.persisted
    ? `Revision ${editor.revision}`
    : "Not saved";
  const issues = editor.validation.issues || [];
  elements.resumePlanIssueCount.textContent = state.resumePlanDirty
    ? "Selection changed"
    : editor.validation.ready_for_approval ? "Ready to use" : `${issues.length} to resolve`;
  elements.resumePlanValidation.classList.toggle(
    "is-ready",
    editor.validation.ready_for_approval && !state.resumePlanDirty
  );
  elements.resumePlanIssues.innerHTML = renderResumePlanIssues(issues);

  elements.resumePlanSectionList.innerHTML = renderResumePlanSections(plan.sections);

  const experienceView = renderResumePlanExperiences({
    catalog,
    plan,
    attentionBlockId: state.resumePlanAttentionBlockId,
  });
  elements.resumePlanExperienceCount.textContent = experienceView.count;
  elements.resumePlanExperienceList.innerHTML = experienceView.html;

  const projectsEnabled = plan.sections.some((section) => (
    section.key === "projects" && section.included
  ));
  elements.resumePlanProjectSection.hidden = !projectsEnabled;
  const projectView = renderResumePlanProjects({
    catalog,
    plan,
    enabled: projectsEnabled,
  });
  elements.resumePlanProjectCount.textContent = projectView.count;
  elements.resumePlanProjectList.innerHTML = projectView.html;

  const skillView = renderResumePlanSkills({ catalog, plan });
  elements.resumePlanSkillCount.textContent = skillView.count;
  elements.resumePlanSkillList.innerHTML = skillView.html;

  const educationView = renderResumePlanEducation({ catalog, plan });
  elements.resumePlanEducationCount.textContent = educationView.count;
  elements.resumePlanEducationList.innerHTML = educationView.html;

}

function findExperiencePlan(blockId) {
  return state.resumePlanEditor?.plan.experiences.find((item) => item.block_id === blockId);
}

function updateStringSelection(items, value, selected) {
  const values = new Set(items);
  if (selected) values.add(value);
  else values.delete(value);
  return [...values];
}

function setResumePlanExperienceDecision(button) {
  const item = findExperiencePlan(button.dataset.planExperienceDecision);
  if (!item) return;
  const decision = button.dataset.planDecisionValue === "include" ? "include" : "exclude";
  item.decision = decision;
  if (decision === "include") {
    item.target_bullets = item.target_bullets || 2;
    item.presentation = item.target_bullets === 2 ? "compact" : "full";
    item.reason = "";
  } else {
    item.presentation = null;
    item.target_bullets = null;
    item.proof_ids = [];
  }
  markResumePlanDirty();
  renderResumePlan();
}

function handleResumePlanChange(event) {
  const editor = state.resumePlanEditor;
  if (!editor) return;
  const target = event.target;
  let rerender = false;
  if (target === elements.resumePlanPages) {
    editor.plan.target_pages = Number(target.value);
  } else if (target.dataset.planSection) {
    const section = editor.plan.sections.find((item) => item.key === target.dataset.planSection);
    if (section) {
      section.included = target.checked;
      if (section.key === "projects" && !section.included) {
        editor.plan.projects = [];
      }
    }
    rerender = true;
  } else if (target.dataset.planExperienceTarget) {
    const item = findExperiencePlan(target.dataset.planExperienceTarget);
    if (item) {
      item.target_bullets = Number(target.value);
      item.presentation = item.target_bullets === 2 ? "compact" : "full";
    }
    rerender = true;
  } else if (target.dataset.planExperienceReason) {
    const item = findExperiencePlan(target.dataset.planExperienceReason);
    if (item) item.reason = target.value;
  } else if (target.dataset.planExperienceProof) {
    const item = findExperiencePlan(target.dataset.planExperienceProof);
    if (item) item.proof_ids = updateStringSelection(item.proof_ids, target.value, target.checked);
    rerender = true;
  } else if (target.dataset.planProject) {
    const blockId = target.dataset.planProject;
    const projectsEnabled = editor.plan.sections.some((section) => (
      section.key === "projects" && section.included
    ));
    if (!projectsEnabled) return;
    if (target.checked) editor.plan.projects.push({ block_id: blockId, proof_ids: [], reason: "" });
    else editor.plan.projects = editor.plan.projects.filter((item) => item.block_id !== blockId);
    rerender = true;
  } else if (target.dataset.planProjectProof) {
    const item = editor.plan.projects.find((project) => project.block_id === target.dataset.planProjectProof);
    if (item) item.proof_ids = updateStringSelection(item.proof_ids, target.value, target.checked);
  } else if (target.dataset.planSkill) {
    const blockId = target.dataset.planSkill;
    if (target.checked) editor.plan.skills.push({ block_id: blockId, keywords: [] });
    else editor.plan.skills = editor.plan.skills.filter((item) => item.block_id !== blockId);
    rerender = true;
  } else if (target.dataset.planSkillKeyword) {
    const item = editor.plan.skills.find((skill) => skill.block_id === target.dataset.planSkillKeyword);
    if (item) item.keywords = updateStringSelection(item.keywords, target.value, target.checked);
  } else if (target.dataset.planEducation) {
    const blockId = target.dataset.planEducation;
    const recordType = target.dataset.planEducationType;
    if (target.checked) editor.plan.education.push({ block_id: blockId, record_type: recordType });
    else editor.plan.education = editor.plan.education.filter((item) => !(item.block_id === blockId && item.record_type === recordType));
    rerender = true;
  } else {
    return;
  }
  markResumePlanDirty();
  if (rerender) renderResumePlan();
}

function moveResumePlanSection(button) {
  const sections = state.resumePlanEditor?.plan.sections;
  if (!sections) return;
  const index = sections.findIndex((item) => item.key === button.dataset.planSectionKey);
  const destination = button.dataset.planSectionMove === "up" ? index - 1 : index + 1;
  if (index < 0 || destination < 0 || destination >= sections.length) return;
  [sections[index], sections[destination]] = [sections[destination], sections[index]];
  markResumePlanDirty();
  renderResumePlan();
}

function focusResumePlanIssue(issue) {
  if (!issue?.blockId) return false;
  state.resumePlanAttentionBlockId = issue.blockId;
  elements.resumePlanMessage.className = "form-message error";
  elements.resumePlanMessage.textContent = issue.message;
  renderResumePlan();
  window.requestAnimationFrame(() => {
    const card = [...elements.resumePlanExperienceList.querySelectorAll("[data-plan-experience-card]")]
      .find((candidate) => candidate.dataset.planExperienceCard === issue.blockId);
    if (!card) return;
    card.scrollIntoView({ behavior: "smooth", block: "center" });
    const target = card.querySelector(`input[data-plan-experience-proof]:not(:checked)`)
      || card.querySelector(`select[data-plan-experience-target]`)
      || card;
    target.focus({ preventScroll: true });
  });
  return true;
}

function focusResumePlanIssueFromError(error) {
  const message = error?.payload?.technical_message || error?.message || "";
  const source = state.resumePlanEditor?.catalog?.experiences?.find((item) => (
    message.includes(`${item.company} / ${item.role}`)
  ));
  return source ? focusResumePlanIssue({ blockId: source.id, message }) : false;
}

async function saveCurrentResumePlan(approve) {
  const editor = state.resumePlanEditor;
  const jobId = state.resumePlanJobId;
  if (!editor || !jobId || state.resumePlanLoading) return;
  if (approve) {
    const localIssue = firstResumePlanApprovalIssue(editor);
    if (localIssue) {
      focusResumePlanIssue(localIssue);
      return;
    }
  }
  state.resumePlanAttentionBlockId = null;
  state.resumePlanLoading = true;
  elements.resumePlanMessage.className = "form-message";
  elements.resumePlanMessage.textContent = approve
    ? "Checking the selected content..."
    : "Saving selection progress...";
  editor.plan.vault_revision = editor.catalog.vault_revision;
  editor.plan.job_fingerprint = editor.current_job_fingerprint;
  renderResumePlan();
  try {
    const saved = await requestJson(`/api/jobs/${encodeURIComponent(jobId)}/resume-plan`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        action: approve ? "approve" : "save_draft",
        expected_revision: editor.revision,
        plan: editor.plan,
      }),
    });
    state.resumePlanEditor = saved;
    state.resumePlanDirty = false;
    const job = state.jobs.find((item) => item.id === jobId);
    if (job?.application) {
      job.application.resume_plan = {
        status: saved.plan.status,
        revision: saved.revision,
        updated_at: new Date().toISOString(),
      };
    }
    elements.resumePlanMessage.textContent = approve
      ? "Content selected. The tailored wording is ready below."
      : "Selection progress saved.";
    toast.show({ message: approve ? "Resume content selected." : "Selection progress saved." });
  } catch (error) {
    if (!focusResumePlanIssueFromError(error)) {
      elements.resumePlanMessage.className = "form-message error";
      elements.resumePlanMessage.textContent = formatRequestError(error);
    }
  } finally {
    state.resumePlanLoading = false;
    renderResumePlan();
  }
  if (approve && state.resumePlanEditor?.plan?.status === "approved") {
    elements.resumePlanWorkspace.open = false;
    state.resumeMaterialEditor = null;
    await loadResumeMaterial({ force: true });
    setResumeMaterialStep("edit");
  }
}

function materialJob() {
  return state.jobs.find((item) => item.id === state.materialEditorJobId);
}

function setMaterialTab(tab) {
  state.materialEditorTab = tab === "resume" ? "resume" : "cover-letter";
  elements.materialTabButtons.forEach((button) => {
    const active = button.dataset.materialTab === state.materialEditorTab;
    button.setAttribute("aria-selected", active ? "true" : "false");
    button.tabIndex = active ? 0 : -1;
    button.classList.toggle("is-active", active);
  });
  elements.materialResumePanel.hidden = state.materialEditorTab !== "resume";
  elements.materialCoverLetterPanel.hidden = state.materialEditorTab !== "cover-letter";
  if (state.materialEditorTab === "resume") setResumeMaterialStep(state.resumeMaterialStep);
}

function setResumeMaterialStep(step, { focus = false } = {}) {
  const steps = ["select", "edit", "review"];
  const planApproved = state.resumePlanEditor?.plan?.status === "approved";
  const busy = state.resumePlanLoading || state.resumeMaterialLoading;
  const requestedStep = steps.includes(step) ? step : "select";
  const nextStep = requestedStep !== "select" && !planApproved ? "select" : requestedStep;
  state.resumeMaterialStep = nextStep;
  elements.resumeMaterialStepButtons.forEach((button) => {
    const active = button.dataset.resumeMaterialStep === nextStep;
    const unavailable = button.dataset.resumeMaterialStep !== "select" && !planApproved;
    if (active) button.setAttribute("aria-current", "step");
    else button.removeAttribute("aria-current");
    button.disabled = unavailable || busy;
    button.setAttribute("aria-disabled", String(unavailable || busy));
    button.classList.toggle("is-active", active);
    if (active && focus) button.focus();
  });
  const sections = {
    select: [elements.resumePlanWorkspace, elements.materialResumeContext],
    edit: [
      elements.materialResumeContext,
      elements.resumeMaterialToolbar,
      elements.resumeMaterialValidation,
      elements.resumeMaterialForm,
    ],
    review: [
      elements.resumeMaterialValidation,
      elements.resumeMaterialPreview,
      elements.resumeMaterialExportBar,
      elements.resumeMaterialScopeNote,
    ],
  };
  const managed = new Set(Object.values(sections).flat().filter(Boolean));
  managed.forEach((node) => {
    node.classList.toggle("resume-material-step-hidden", !sections[nextStep].includes(node));
  });
  elements.resumeMaterialStepBack.disabled = busy || nextStep === "select";
  elements.resumeMaterialStepNext.disabled = busy
    || (nextStep === "select" ? !state.resumePlanEditor : !state.resumeMaterialEditor);
  elements.resumeMaterialStepNext.textContent = nextStep === "select"
    ? (state.resumePlanEditor?.plan?.status === "approved" ? "Continue" : "Use selection")
    : nextStep === "edit" ? "Review" : "Done";
  if (nextStep === "select") elements.resumePlanWorkspace.open = true;
  if (nextStep === "review") elements.resumeMaterialPreview.open = true;
}

async function advanceResumeMaterialStep() {
  if (state.resumeMaterialStep === "select") {
    if (state.resumePlanEditor?.plan?.status !== "approved") {
      await saveCurrentResumePlan(true);
      if (state.resumePlanEditor?.plan?.status !== "approved") return;
    }
    setResumeMaterialStep("edit", { focus: true });
    return;
  }
  if (state.resumeMaterialStep === "edit") {
    if (state.resumeMaterialDirty) {
      await saveResumeMaterial(false);
      if (state.resumeMaterialDirty) return;
    }
    setResumeMaterialStep("review", { focus: true });
    return;
  }
  returnFromMaterialEditor();
}

async function retreatResumeMaterialStep() {
  const prior = state.resumeMaterialStep === "review" ? "edit" : "select";
  await activateResumeMaterialStep(prior, { focus: true });
}

async function activateResumeMaterialStep(step, { focus = false } = {}) {
  if (step !== state.resumeMaterialStep && state.resumeMaterialStep === "edit" && state.resumeMaterialDirty) {
    await saveResumeMaterial(false);
    if (state.resumeMaterialDirty) return;
  }
  setResumeMaterialStep(step, { focus });
}

async function activateMaterialTab(tab) {
  setMaterialTab(tab);
  renderApplicationMaterials();
  if (state.materialEditorTab === "resume") {
    state.resumePlanJobId = state.materialEditorJobId;
    await Promise.all([loadResumePlan(), loadResumeMaterial()]);
  } else {
    await loadCoverLetterMaterial();
  }
}

async function openApplicationMaterials(tab = "cover-letter") {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job?.valid || !canManageApplicationMaterials(job.application)) return;
  state.materialEditorReturnView = state.activeView === "applications"
    ? "applications"
    : "job-library";
  state.materialEditorJobId = job.id;
  state.materialEditor = null;
  state.materialEditorSaving = false;
  state.materialEditorDirty = false;
  state.resumeMaterialEditor = null;
  state.resumeMaterialDirty = false;
  state.resumePlanJobId = job.id;
  state.resumePlanEditor = null;
  state.resumePlanDirty = false;
  state.resumeMaterialStep = "select";
  state.materialEvidenceSearch = "";
  setMaterialTab(tab);
  if (state.applicationDrawerOpen) closeApplicationDrawer();
  switchView("application-materials");
  if (state.materialEditorTab === "resume") {
    await Promise.all([loadResumePlan(), loadResumeMaterial()]);
  } else {
    await loadCoverLetterMaterial();
  }
}

export async function loadCoverLetterMaterial({ force = false } = {}) {
  const jobId = state.materialEditorJobId;
  if (!jobId || state.materialEditorLoading) return;
  if (!force && state.materialEditor?.draft?.job_id === jobId) return;
  state.materialEditorLoading = true;
  state.materialEditorDirty = false;
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = "Loading application material...";
  renderApplicationMaterials();
  try {
    const editor = await api.getCoverLetter(jobId);
    if (state.materialEditorJobId !== jobId) return;
    state.materialEditor = editor;
    state.coverLetterSummaries[jobId] = coverLetterSummary(editor);
    elements.materialEditorMessage.textContent = editor.persisted
      ? "Saved draft loaded."
      : "New cover letter started from the standard template and current source facts.";
  } catch (error) {
    if (state.materialEditorJobId !== jobId) return;
    state.materialEditor = null;
    elements.materialEditorMessage.className = "form-message error";
    elements.materialEditorMessage.textContent = formatRequestError(error);
  } finally {
    if (state.materialEditorJobId === jobId) {
      state.materialEditorLoading = false;
      renderApplicationMaterials();
    }
  }
}

export async function loadResumeMaterial({ force = false } = {}) {
  const jobId = state.materialEditorJobId;
  if (!jobId || state.resumeMaterialLoading) return;
  if (!force && state.resumeMaterialEditor?.draft?.job_id === jobId) return;
  state.resumeMaterialLoading = true;
  state.resumeMaterialDirty = false;
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = "Loading application resume...";
  renderApplicationMaterials();
  try {
    const editor = await api.getResumeMaterial(jobId);
    if (state.materialEditorJobId !== jobId) return;
    state.resumeMaterialEditor = editor;
    elements.materialEditorMessage.textContent = editor.persisted
      ? "Saved application resume loaded."
      : "New application resume built from the current content selection.";
  } catch (error) {
    if (state.materialEditorJobId !== jobId) return;
    state.resumeMaterialEditor = null;
    elements.materialEditorMessage.className = "form-message error";
    elements.materialEditorMessage.textContent = formatRequestError(error);
  } finally {
    if (state.materialEditorJobId === jobId) {
      state.resumeMaterialLoading = false;
      renderApplicationMaterials();
    }
  }
}

function selectedCoverLetterEvidenceRefs() {
  return (state.materialEditor?.draft?.content?.evidence_items || [])
    .flatMap((item) => item.evidence_refs || []);
}

function renderCoverLetterCatalog() {
  const editor = state.materialEditor;
  const expandedSourceKeys = [
    ...(elements.coverLetterEvidenceCatalog?.querySelectorAll(
      ".material-evidence-source-group[open]",
    ) || []),
  ].map((group) => group.dataset.evidenceSourceKey).filter(Boolean);
  elements.coverLetterEvidenceCatalog.innerHTML = renderCoverLetterEvidenceCatalog(
    editor?.catalog?.evidence || [],
    state.materialEvidenceSearch,
    selectedCoverLetterEvidenceRefs(),
    expandedSourceKeys,
  );
}

function writeCoverLetterForm(draft) {
  elements.coverLetterName.value = draft.candidate.name || "";
  elements.coverLetterEmail.value = draft.candidate.email || "";
  elements.coverLetterLocation.value = draft.candidate.location_line || "";
  elements.coverLetterPhone.value = draft.candidate.phone || "";
  elements.coverLetterLinkedin.value = draft.candidate.linkedin || "";
  elements.coverLetterGithub.value = draft.candidate.github || "";
  elements.coverLetterWebsite.value = draft.candidate.website || "";
  elements.coverLetterDate.value = draft.content.letter_date || "";
  elements.coverLetterCompany.value = draft.company || "";
  elements.coverLetterRole.value = draft.role_title || "";
  elements.coverLetterSalutation.value = draft.content.salutation || "";
  elements.coverLetterOpening.value = draft.content.opening || "";
  elements.coverLetterLeadIn.value = draft.content.evidence_lead_in || "";
  elements.coverLetterClosing.value = draft.content.closing || "";
  elements.coverLetterSignOff.value = draft.content.sign_off || "";
  elements.coverLetterSignature.value = draft.content.signature_name || "";
  elements.coverLetterCredential.value = draft.content.credential_line || "";
}

function coverLetterHasStaleSources(editor) {
  return (editor?.validation?.issues || []).some((issue) => (
    ["MATERIAL_JOB_STALE", "MATERIAL_PROFILE_STALE", "MATERIAL_EVIDENCE_STALE"].includes(issue.code)
  ));
}

function writeResumeMaterialForm(draft) {
  elements.resumeMaterialName.value = draft.candidate.name || "";
  elements.resumeMaterialEmail.value = draft.candidate.email || "";
  elements.resumeMaterialLocation.value = draft.candidate.location_line || "";
  elements.resumeMaterialPhone.value = draft.candidate.phone || "";
  elements.resumeMaterialLinkedin.value = draft.candidate.linkedin || "";
  elements.resumeMaterialGithub.value = draft.candidate.github || "";
  elements.resumeMaterialWebsite.value = draft.candidate.website || "";
  elements.resumeMaterialSummary.value = draft.summary || "";
}

function resumeMaterialHasStalePlan(editor) {
  return (editor?.validation?.issues || []).some((issue) => [
    "RESUME_JOB_STALE",
    "RESUME_PROFILE_STALE",
    "RESUME_EVIDENCE_STALE",
    "RESUME_PLAN_STALE",
    "RESUME_PLAN_CONTENT_STALE",
  ].includes(issue.code));
}

function renderResumeMaterialPanel() {
  renderResumePlan();
  const editor = state.resumeMaterialEditor;
  const disabled = state.resumeMaterialLoading || !editor;
  elements.resumeMaterialForm.querySelectorAll("input, textarea").forEach((input) => {
    input.disabled = disabled;
  });
  elements.saveResumeMaterialButton.disabled = disabled || !state.resumeMaterialDirty;
  elements.approveResumeMaterialButton.disabled = disabled
    || state.resumeMaterialDirty
    || !editor?.validation?.ready_to_export;
  elements.copyResumeAiCommand.disabled = disabled;
  if (!editor) {
    elements.resumeMaterialIssueCount.textContent = state.resumeMaterialLoading
      ? "Loading"
      : "Unavailable";
    elements.resumeMaterialIssues.innerHTML = "";
    elements.resumeMaterialExperiences.innerHTML = "";
    elements.resumeMaterialProjects.innerHTML = "";
    elements.resumeMaterialSkills.innerHTML = "";
    elements.resumeMaterialEducation.innerHTML = "";
    elements.resumeMaterialSummaryEvidence.innerHTML = "";
    elements.resumeMaterialPreviewText.textContent = "";
    elements.rebuildResumeMaterialButton.classList.add("hidden");
    elements.resumeMaterialExportButtons.forEach((button) => { button.disabled = true; });
    if (state.materialEditorTab === "resume") {
      elements.materialEditorStatus.textContent = state.resumeMaterialLoading ? "Loading" : "Unavailable";
      elements.materialEditorRevision.textContent = "Not loaded";
    }
    return;
  }

  const draft = editor.draft;
  elements.resumeMaterialIssueCount.textContent = materialIssueSummary(
    editor.validation,
    state.resumeMaterialDirty,
  );
  elements.resumeMaterialValidation.classList.toggle(
    "is-ready",
    editor.validation.ready_to_export && !state.resumeMaterialDirty,
  );
  elements.resumeMaterialIssues.innerHTML = renderMaterialIssues(editor.validation, {
    dirty: state.resumeMaterialDirty,
  });
  elements.rebuildResumeMaterialButton.classList.toggle(
    "hidden",
    !resumeMaterialHasStalePlan(editor),
  );
  elements.rebuildResumeMaterialButton.disabled = state.resumeMaterialLoading;
  writeResumeMaterialForm(draft);
  const rendered = renderResumeBlocks(draft, editor.catalog?.evidence || []);
  elements.resumeMaterialExperiences.innerHTML = rendered.experience;
  elements.resumeMaterialProjects.innerHTML = rendered.projects;
  elements.resumeMaterialSkills.innerHTML = renderResumeSkills(draft.skills);
  elements.resumeMaterialEducation.innerHTML = renderResumeEducation(draft.education);
  elements.resumeMaterialForm.querySelectorAll("input, textarea").forEach((input) => {
    input.disabled = disabled;
  });
  const evidenceById = new Map(
    (editor.catalog?.evidence || []).map((item) => [item.id, item])
  );
  elements.resumeMaterialSummaryEvidence.innerHTML = renderResumeEvidenceRefs(
    draft.summary_evidence_refs,
    evidenceById,
  ) || '<span class="empty-state">No evidence attached.</span>';
  elements.resumeMaterialSummarySection.hidden = !draft.sections.includes("professional_summary");
  elements.resumeMaterialProjectsSection.hidden = !draft.sections.includes("projects");
  elements.resumeMaterialPreviewText.textContent = editor.preview?.plain_text || "";
  const exportReady = Boolean(
    editor.persisted
    && editor.validation.ready_to_export
    && !state.resumeMaterialDirty
    && !state.resumeMaterialLoading
  );
  elements.resumeMaterialExportNote.textContent = exportReady
    ? `${editor.preview.word_count} words / ATS structure and evidence checks passed.`
    : "Use a valid content selection and save the application-only wording before exporting.";
  elements.resumeMaterialExportButtons.forEach((button) => {
    button.disabled = !exportReady;
  });
  if (state.materialEditorTab === "resume") {
    elements.materialEditorStatus.textContent = state.resumeMaterialDirty
      ? "Unsaved changes"
      : draft.status;
    elements.materialEditorRevision.textContent = editor.persisted
      ? `Revision ${editor.revision}`
      : "Not saved";
  }
}

function renderApplicationMaterials() {
  syncMaterialReviews();
  const job = materialJob();
  const editor = state.materialEditor;
  elements.materialEditorTitle.textContent = job
    ? [job.company, job.title].filter(Boolean).join(" / ") || "Application materials"
    : "Application materials";
  elements.materialEditorMeta.textContent = job
    ? [job.location, job.remote_policy].filter(Boolean).join(" / ") || "Location not stated"
    : "Select a prepared application.";
  elements.materialEditorBackButton.textContent = state.materialEditorReturnView === "applications"
    ? "← Applications"
    : "← Job Workspace";
  setMaterialTab(state.materialEditorTab);

  const selectedResume = job?.application?.resume_selection?.resume;
  elements.materialSelectedResumeName.textContent = selectedResume?.display_name || "No resume selected";
  elements.materialSelectedResumeMeta.textContent = selectedResume
    ? "This immutable Resume Version is selected for tailoring."
    : "Select or upload an immutable Resume Version in Job Workspace.";

  renderResumeMaterialPanel();

  const disabled = state.materialEditorLoading || !editor;
  elements.coverLetterForm.querySelectorAll("input, textarea").forEach((input) => {
    input.disabled = disabled;
  });
  elements.copyCoverLetterAiCommand.disabled = disabled;
  elements.addCoverLetterEvidenceButton.disabled = disabled
    || (editor?.draft?.content?.evidence_items || []).length >= 4;
  if (!editor) {
    if (state.materialEditorTab === "cover-letter") {
      elements.materialEditorStatus.textContent = state.materialEditorLoading ? "Loading" : "Unavailable";
      elements.materialEditorRevision.textContent = "Not loaded";
    }
    elements.coverLetterIssueCount.textContent = state.materialEditorLoading ? "Loading" : "Unavailable";
    elements.coverLetterIssues.innerHTML = "";
    elements.coverLetterEvidenceCatalog.innerHTML = "";
    elements.coverLetterEvidenceItems.innerHTML = "";
    elements.coverLetterPreviewText.textContent = "";
    elements.refreshCoverLetterSources.classList.add("hidden");
    elements.coverLetterSaveState.textContent = state.materialEditorLoading
      ? "Loading..."
      : "Unavailable";
    elements.coverLetterExportButtons.forEach((button) => { button.disabled = true; });
    updateMaterialReviewControls();
    return;
  }

  const draft = editor.draft;
  if (state.materialEditorTab === "cover-letter") {
    elements.materialEditorStatus.textContent = state.materialEditorDirty
      ? "Editing"
      : editor.validation.ready_to_export ? "Ready" : "Needs review";
    elements.materialEditorRevision.textContent = editor.persisted
      ? `Saved revision ${editor.revision}`
      : "Not saved";
  }
  elements.coverLetterSaveState.textContent = state.materialEditorDirty
    ? "Current changes will be saved when you export."
    : editor.persisted
      ? `Saved locally / revision ${editor.revision}`
      : "Export saves and validates automatically.";
  elements.coverLetterIssueCount.textContent = coverLetterIssueSummary(
    editor.validation,
    state.materialEditorDirty,
  );
  elements.coverLetterValidation.classList.toggle(
    "is-ready",
    editor.validation.ready_to_export && !state.materialEditorDirty,
  );
  elements.coverLetterIssues.innerHTML = renderMaterialIssues(editor.validation, {
    dirty: state.materialEditorDirty,
  });
  elements.refreshCoverLetterSources.classList.toggle(
    "hidden",
    !coverLetterHasStaleSources(editor),
  );
  elements.refreshCoverLetterSources.disabled = state.materialEditorLoading;
  writeCoverLetterForm(draft);
  elements.coverLetterEvidenceItems.innerHTML = renderCoverLetterEvidenceItems(
    draft.content.evidence_items,
    editor.catalog.evidence,
  );
  renderCoverLetterCatalog();
  elements.coverLetterPreviewText.textContent = editor.preview?.plain_text || "";

  const exportReady = Boolean(editor.validation.ready_to_export && !state.materialEditorDirty);
  elements.coverLetterExportNote.textContent = exportReady
    ? `${editor.preview.word_count} words / structure and source references passed.`
    : state.materialEditorDirty || !editor.persisted
      ? "Export saves the current letter, reruns checks, and then creates the file."
      : "Resolve the readiness items above; Export will recheck automatically.";
  elements.coverLetterExportButtons.forEach((button) => {
    button.disabled = disabled;
  });
  updateMaterialReviewControls();
}

function syncCoverLetterSimpleField(target) {
  const draft = state.materialEditor?.draft;
  if (!draft) return false;
  const bindings = new Map([
    [elements.coverLetterName, [draft.candidate, "name"]],
    [elements.coverLetterEmail, [draft.candidate, "email"]],
    [elements.coverLetterLocation, [draft.candidate, "location_line"]],
    [elements.coverLetterPhone, [draft.candidate, "phone"]],
    [elements.coverLetterLinkedin, [draft.candidate, "linkedin"]],
    [elements.coverLetterGithub, [draft.candidate, "github"]],
    [elements.coverLetterWebsite, [draft.candidate, "website"]],
    [elements.coverLetterDate, [draft.content, "letter_date"]],
    [elements.coverLetterCompany, [draft, "company"]],
    [elements.coverLetterRole, [draft, "role_title"]],
    [elements.coverLetterSalutation, [draft.content, "salutation"]],
    [elements.coverLetterOpening, [draft.content, "opening"]],
    [elements.coverLetterLeadIn, [draft.content, "evidence_lead_in"]],
    [elements.coverLetterClosing, [draft.content, "closing"]],
    [elements.coverLetterSignOff, [draft.content, "sign_off"]],
    [elements.coverLetterSignature, [draft.content, "signature_name"]],
    [elements.coverLetterCredential, [draft.content, "credential_line"]],
  ]);
  const binding = bindings.get(target);
  if (!binding) return false;
  binding[0][binding[1]] = target.value;
  return true;
}

function coverLetterLines(value) {
  return [...new Set(String(value || "").split(/\r?\n/).map((item) => item.trim()).filter(Boolean))];
}

function handleCoverLetterInput(event) {
  const editor = state.materialEditor;
  if (!editor || state.materialEditorLoading) return;
  const target = event.target;
  let changed = syncCoverLetterSimpleField(target);
  const itemField = target.dataset.coverLetterItemField;
  if (itemField) {
    const card = target.closest("[data-cover-letter-item-index]");
    const item = editor.draft.content.evidence_items[Number(card?.dataset.coverLetterItemIndex)];
    if (item) {
      item[itemField] = itemField === "manual_checks"
        ? coverLetterLines(target.value)
        : target.value;
      changed = true;
    }
  }
  if (!changed) return;
  state.materialEditorDirty = true;
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = "Editing locally. Export will save and validate the current letter.";
  syncMaterialReviews();
  updateMaterialReviewControls();
  elements.materialEditorStatus.textContent = "Editing";
  elements.coverLetterIssueCount.textContent = "Changes not checked";
  elements.coverLetterSaveState.textContent = "Current changes will be saved when you export.";
  elements.coverLetterExportNote.textContent = "Export saves the current letter, reruns checks, and then creates the file.";
}

function newCoverLetterItem({ evidence = null } = {}) {
  return {
    id: `point_${crypto.randomUUID().replaceAll("-", "").slice(0, 12)}`,
    label: evidence?.source_type === "project" ? "Applied project work" : "Relevant experience",
    claim: evidence?.claim || "",
    evidence_refs: evidence ? [evidence.id] : [],
    manual_checks: evidence ? [] : ["Verify this claim before export."],
    origin: "user",
  };
}

function addCoverLetterEvidence(reference = null) {
  const editor = state.materialEditor;
  const items = editor?.draft?.content?.evidence_items;
  if (!items || items.length >= 4) return;
  const evidence = reference
    ? editor.catalog.evidence.find((item) => item.id === reference)
    : null;
  if (reference && !evidence) return;
  items.push(newCoverLetterItem({ evidence }));
  state.materialEditorDirty = true;
  renderApplicationMaterials();
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = evidence
    ? "Evidence added. Tailor the label and claim without overstating the source."
    : "Manual point added. Resolve its verification check before export.";
}

function removeCoverLetterEvidence(index) {
  const items = state.materialEditor?.draft?.content?.evidence_items;
  if (!items?.[index]) return;
  items.splice(index, 1);
  state.materialEditorDirty = true;
  renderApplicationMaterials();
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = "Evidence point removed locally.";
}

function refreshCoverLetterSources() {
  const editor = state.materialEditor;
  if (!editor) return;
  const defaults = editor.source_defaults;
  const current = editor.current_sources;
  editor.draft.job_fingerprint = current.job_fingerprint;
  editor.draft.profile_revision = current.profile_revision;
  editor.draft.evidence_revision = current.evidence_revision;
  editor.draft.company = defaults.company;
  editor.draft.role_title = defaults.role_title;
  editor.draft.candidate = structuredClone(defaults.candidate);
  editor.draft.content.salutation = defaults.content.salutation;
  editor.draft.content.signature_name = defaults.content.signature_name;
  editor.draft.content.credential_line = defaults.content.credential_line;
  state.materialEditorDirty = true;
  renderApplicationMaterials();
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = "Current job/profile facts refreshed. Review the prose, then export.";
}

async function persistCoverLetterForExport() {
  const editor = state.materialEditor;
  const jobId = state.materialEditorJobId;
  if (!editor || !jobId || state.materialEditorLoading || state.materialEditorSaving) return false;
  if (!elements.coverLetterForm.reportValidity()) return false;
  if (editor.persisted && !state.materialEditorDirty) {
    const ready = Boolean(editor.validation?.ready_to_export);
    if (!ready) {
      elements.materialEditorMessage.className = "form-message error";
      elements.materialEditorMessage.textContent = "Resolve the readiness items above before export.";
    }
    return ready;
  }
  state.materialEditorSaving = true;
  state.materialEditorLoading = true;
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = "Saving and checking the current cover letter...";
  renderApplicationMaterials();
  try {
    const saved = await api.saveCoverLetter(jobId, {
      action: "save_draft",
      expectedRevision: editor.revision,
      draft: editor.draft,
    });
    state.materialEditor = saved;
    state.materialEditorDirty = false;
    state.coverLetterSummaries[jobId] = coverLetterSummary(saved);
    if (!saved.validation?.ready_to_export) {
      elements.materialEditorMessage.className = "form-message error";
      elements.materialEditorMessage.textContent = "Saved locally. Resolve the readiness items before export.";
      return false;
    }
    elements.materialEditorMessage.textContent = "Draft saved. Checking export readiness...";
    return true;
  } catch (error) {
    elements.materialEditorMessage.className = "form-message error";
    elements.materialEditorMessage.textContent = formatRequestError(error);
    return false;
  } finally {
    state.materialEditorSaving = false;
    state.materialEditorLoading = false;
    renderApplicationMaterials();
  }
}

async function downloadCoverLetter(format) {
  const jobId = state.materialEditorJobId;
  if (!jobId || state.materialEditorLoading) return;
  const ready = await persistCoverLetterForExport();
  if (!ready) {
    renderApplicationMaterials();
    return;
  }
  if (!await materialReviews.cover_letter.ensureReady() || state.materialEditorJobId !== jobId || state.materialEditorDirty) return;
  state.materialEditorLoading = true;
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = `Rendering and validating ${format.toUpperCase()}...`;
  renderApplicationMaterials();
  try {
    const { blob, filename } = await api.downloadCoverLetter(jobId, format);
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = filename;
    document.body.append(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(url);
    elements.materialEditorMessage.textContent = `${filename} passed structural checks and was downloaded.`;
  } catch (error) {
    elements.materialEditorMessage.className = "form-message error";
    elements.materialEditorMessage.textContent = formatRequestError(error);
  } finally {
    state.materialEditorLoading = false;
    renderApplicationMaterials();
  }
}

// Build-with-AI commands target the verified career-hub-agent client. In the
// desktop app the command is complete (verified executable and current server);
// in plain browser mode it uses the $agent/$connection placeholders from the skills.
async function buildAgentMaterialCommand(jobId, kind) {
  const args = `material-context --job-id ${jobId} --kind ${kind} --pretty`;
  const bridge = window.careerHubDesktop?.agent;
  if (bridge?.getConnection) {
    try {
      const connection = await bridge.getConnection();
      if (connection?.available && connection.executablePath && connection.connection) {
        // The connection file path, never a token (ADR SEC-250).
        return { command: `& "${connection.executablePath}" ${args.replace("material-context", `material-context --connection "${connection.connection}"`)}`, complete: true };
      }
    } catch (_error) {
      // Fall back to the placeholder form below.
    }
  }
  return { command: `& $agent ${args.replace("material-context", "material-context --connection $connection")}`, complete: false };
}

async function copyAgentMaterialCommand(kind, copiedMessage, toastMessage) {
  const jobId = state.materialEditorJobId;
  const button = kind === "resume" ? elements.copyResumeAiCommand : elements.copyCoverLetterAiCommand;
  if (!jobId || button.disabled || api.capabilities.material_integrity === 1) return;
  const { command, complete } = await buildAgentMaterialCommand(jobId, kind);
  const hint = complete
    ? copiedMessage
    : `${copiedMessage} Set $agent and $connection from Settings > Local agent connection in the desktop app.`;
  try {
    await navigator.clipboard.writeText(command);
    elements.materialEditorMessage.className = "form-message";
    elements.materialEditorMessage.textContent = hint;
    toast.show({ message: toastMessage });
  } catch {
    elements.materialEditorMessage.className = "form-message error";
    elements.materialEditorMessage.textContent = command;
  }
}

function copyCoverLetterAiCommand() {
  return copyAgentMaterialCommand(
    "cover_letter",
    "Bounded agent command copied. It returns the current draft and evidence catalog without broad Vault rereads.",
    "Build-with-AI command copied.",
  );
}

function syncResumeMaterialSimpleField(target) {
  const draft = state.resumeMaterialEditor?.draft;
  if (!draft) return false;
  const bindings = new Map([
    [elements.resumeMaterialName, [draft.candidate, "name"]],
    [elements.resumeMaterialEmail, [draft.candidate, "email"]],
    [elements.resumeMaterialLocation, [draft.candidate, "location_line"]],
    [elements.resumeMaterialPhone, [draft.candidate, "phone"]],
    [elements.resumeMaterialLinkedin, [draft.candidate, "linkedin"]],
    [elements.resumeMaterialGithub, [draft.candidate, "github"]],
    [elements.resumeMaterialWebsite, [draft.candidate, "website"]],
    [elements.resumeMaterialSummary, [draft, "summary"]],
  ]);
  const binding = bindings.get(target);
  if (!binding) return false;
  binding[0][binding[1]] = target.value;
  return true;
}

function handleResumeMaterialInput(event) {
  const editor = state.resumeMaterialEditor;
  if (!editor || state.resumeMaterialLoading) return;
  const target = event.target;
  let changed = syncResumeMaterialSimpleField(target);
  const collection = target.dataset.resumeBulletCollection;
  if (collection) {
    const record = editor.draft[collection]?.[Number(target.dataset.resumeRecordIndex)];
    const bullet = record?.bullets?.[Number(target.dataset.resumeBulletIndex)];
    if (bullet) {
      bullet.text = target.value;
      bullet.origin = "user";
      changed = true;
    }
  }
  if (!changed) return;
  state.resumeMaterialDirty = true;
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = "Resume changes are application-only until you save.";
  syncMaterialReviews();
  elements.materialEditorStatus.textContent = "Unsaved changes";
  elements.resumeMaterialIssueCount.textContent = "Save to validate";
  elements.saveResumeMaterialButton.disabled = false;
  elements.approveResumeMaterialButton.disabled = true;
  elements.resumeMaterialExportButtons.forEach((button) => { button.disabled = true; });
  elements.resumeMaterialExportNote.textContent = "Save a valid draft before exporting.";
}

async function saveResumeMaterial(approve) {
  const editor = state.resumeMaterialEditor;
  const jobId = state.materialEditorJobId;
  if (!editor || !jobId || state.resumeMaterialLoading) return;
  if (!elements.resumeMaterialForm.reportValidity()) return;
  state.resumeMaterialLoading = true;
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = approve
    ? "Checking and approving application resume..."
    : "Validating and saving application resume...";
  renderApplicationMaterials();
  try {
    const saved = await api.saveResumeMaterial(jobId, {
      action: approve ? "approve" : "save_draft",
      expectedRevision: editor.revision,
      draft: editor.draft,
    });
    state.resumeMaterialEditor = saved;
    state.resumeMaterialDirty = false;
    elements.materialEditorMessage.textContent = approve
      ? "Application resume approved. Final checks run before export."
      : "Application resume draft saved and validated.";
    toast.show({ message: approve ? "Application resume approved." : "Resume draft saved." });
  } catch (error) {
    elements.materialEditorMessage.className = "form-message error";
    elements.materialEditorMessage.textContent = formatRequestError(error);
  } finally {
    state.resumeMaterialLoading = false;
    renderApplicationMaterials();
  }
}

function rebuildResumeMaterialFromPlan() {
  const editor = state.resumeMaterialEditor;
  if (!editor?.source_defaults) return;
  if (!window.confirm("Update this resume from the current content selection? Unsaved custom wording will be replaced.")) return;
  editor.draft = structuredClone(editor.source_defaults);
  state.resumeMaterialDirty = true;
  renderApplicationMaterials();
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = "Rebuilt from current sources. Review the summary and bullets, then save.";
}

async function downloadResumeMaterial(format) {
  const jobId = state.materialEditorJobId;
  if (!jobId || state.resumeMaterialLoading) return;
  if (!await materialReviews.resume.ensureReady() || state.materialEditorJobId !== jobId || state.resumeMaterialDirty) return;
  state.resumeMaterialLoading = true;
  elements.materialEditorMessage.className = "form-message";
  elements.materialEditorMessage.textContent = `Rendering and validating resume ${format.toUpperCase()}...`;
  renderApplicationMaterials();
  try {
    const { blob, filename } = await api.downloadResumeMaterial(jobId, format);
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = filename;
    document.body.append(anchor);
    anchor.click();
    anchor.remove();
    URL.revokeObjectURL(url);
    elements.materialEditorMessage.textContent = `${filename} passed structural checks and was downloaded.`;
  } catch (error) {
    elements.materialEditorMessage.className = "form-message error";
    elements.materialEditorMessage.textContent = formatRequestError(error);
  } finally {
    state.resumeMaterialLoading = false;
    renderApplicationMaterials();
  }
}

function copyResumeAiCommand() {
  return copyAgentMaterialCommand(
    "resume",
    "Bounded resume command copied. It exposes only selected Career Vault evidence and editable prose.",
    "Resume Build-with-AI command copied.",
  );
}

async function returnFromMaterialEditor({ selectResume = false } = {}) {
  if (!(await confirmReviewNavigation(state.materialEditorReturnView))) return;
  const jobId = state.materialEditorJobId;
  switchView(state.materialEditorReturnView);
  if (state.materialEditorReturnView === "job-library" && jobId) await selectJob(jobId);
  if (selectResume) elements.applicationResumeCombobox?.scrollIntoView({ behavior: "smooth", block: "center" });
}

export function initApplicationMaterials(deps = {}) {
  ({ closeApplicationDrawer, confirmReviewNavigation, coverLetterSummary, selectJob, switchView } = deps);
  elements.openResumeMaterialButton.addEventListener("click", () => openApplicationMaterials("resume"));
  elements.openCoverLetterButton.addEventListener("click", () => openApplicationMaterials("cover-letter"));
  elements.saveResumePlanButton.addEventListener("click", () => saveCurrentResumePlan(false));
  elements.approveResumePlanButton.addEventListener("click", () => saveCurrentResumePlan(true));
  elements.resumePlanWorkspace.addEventListener("change", handleResumePlanChange);
  elements.resumePlanWorkspace.addEventListener("click", (event) => {
    const decisionButton = event.target.closest("[data-plan-experience-decision]");
    if (decisionButton) {
      setResumePlanExperienceDecision(decisionButton);
      return;
    }
    const moveButton = event.target.closest("[data-plan-section-move]");
    if (moveButton) moveResumePlanSection(moveButton);
  });
  elements.materialEditorBackButton.addEventListener("click", () => returnFromMaterialEditor());
  elements.materialTabButtons.forEach((button) => {
    button.addEventListener("click", () => { void activateMaterialTab(button.dataset.materialTab); });
    button.addEventListener("keydown", (event) => {
      if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
      event.preventDefault();
      const currentIndex = elements.materialTabButtons.indexOf(button);
      const offset = event.key === "ArrowRight" ? 1 : -1;
      const nextIndex = event.key === "Home"
        ? 0
        : event.key === "End"
          ? elements.materialTabButtons.length - 1
          : (currentIndex + offset + elements.materialTabButtons.length)
            % elements.materialTabButtons.length;
      const next = elements.materialTabButtons[nextIndex];
      void activateMaterialTab(next.dataset.materialTab).then(() => next.focus());
    });
  });
  elements.resumeMaterialStepButtons.forEach((button, index) => {
    button.addEventListener("click", () => {
      void activateResumeMaterialStep(button.dataset.resumeMaterialStep);
    });
    button.addEventListener("keydown", (event) => {
      if (!["ArrowLeft", "ArrowRight"].includes(event.key)) return;
      event.preventDefault();
      const offset = event.key === "ArrowRight" ? 1 : -1;
      const next = elements.resumeMaterialStepButtons[
        (index + offset + elements.resumeMaterialStepButtons.length)
        % elements.resumeMaterialStepButtons.length
      ];
      void activateResumeMaterialStep(next.dataset.resumeMaterialStep, { focus: true });
    });
  });
  elements.resumeMaterialStepBack.addEventListener("click", () => { void retreatResumeMaterialStep(); });
  elements.resumeMaterialStepNext.addEventListener("click", () => { void advanceResumeMaterialStep(); });
  elements.materialReturnToResumeSelection.addEventListener("click", () => (
    returnFromMaterialEditor({ selectResume: true })
  ));
  elements.copyResumeAiCommand.addEventListener("click", copyResumeAiCommand);
  elements.saveResumeMaterialButton.addEventListener("click", () => saveResumeMaterial(false));
  elements.approveResumeMaterialButton.addEventListener("click", () => saveResumeMaterial(true));
  elements.rebuildResumeMaterialButton.addEventListener("click", rebuildResumeMaterialFromPlan);
  elements.resumeMaterialForm.addEventListener("input", handleResumeMaterialInput);
  elements.resumeMaterialExportButtons.forEach((button) => {
    button.addEventListener("click", () => downloadResumeMaterial(button.dataset.resumeMaterialExport));
  });
  elements.copyCoverLetterAiCommand.addEventListener("click", copyCoverLetterAiCommand);
  elements.refreshCoverLetterSources.addEventListener("click", refreshCoverLetterSources);
  elements.addCoverLetterEvidenceButton.addEventListener("click", () => addCoverLetterEvidence());
  elements.coverLetterForm.addEventListener("input", handleCoverLetterInput);
  elements.coverLetterEvidenceSearch.addEventListener("input", (event) => {
    state.materialEvidenceSearch = event.target.value;
    renderCoverLetterCatalog();
  });
  elements.coverLetterEvidenceCatalog.addEventListener("click", (event) => {
    const button = event.target.closest("[data-add-cover-letter-evidence]");
    if (button) addCoverLetterEvidence(button.dataset.addCoverLetterEvidence);
  });
  elements.coverLetterEvidenceItems.addEventListener("click", (event) => {
    const button = event.target.closest("[data-remove-cover-letter-evidence]");
    if (button) removeCoverLetterEvidence(Number(button.dataset.removeCoverLetterEvidence));
  });
  elements.coverLetterExportButtons.forEach((button) => {
    button.addEventListener("click", () => downloadCoverLetter(button.dataset.coverLetterExport));
  });
}
