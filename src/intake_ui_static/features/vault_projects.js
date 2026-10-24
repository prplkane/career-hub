// Career Vault project list and editor.
import { elements, requestJson, state } from "../app_context.js";
import { createMonthRangePicker } from "../ui/dates.js";
import { escapeHtml, formatRequestError } from "../ui/primitives.js";
import { pollLogs } from "./diagnostics.js";
import { loadSkillsRegistry } from "./skills_hub.js";
import { manualSkillRecordFields, setManualSkillsFromRecord } from "./vault_manual_skills.js";
import {
  closeCareerVaultCardDetail,
  getVaultSnapshot,
  openCareerVaultCardDetail,
  renderVaultCardOpenLabel,
  renderVaultCardPreview,
  setArchiveActionButton,
  updateArchiveScopeToggle,
  vaultLines,
  withVaultLoading,
} from "./vault_shared.js";

const projectDateRange = createMonthRangePicker(elements.projectDateRange, { label: "Project dates" });

function selectedVaultProject() {
  return state.vaultProjects.find((record) => record.id === state.selectedProjectId) || null;
}

function renderProjectCategoryOptions() {
  const selectedFilter = elements.projectCategoryFilter.value;
  elements.projectCategoryFilter.innerHTML = '<option value="">All categories</option>'
    + state.projectCategories.map(
      (category) => `<option value="${escapeHtml(category)}" translate="no">${escapeHtml(category)}</option>`,
    ).join("");
  if (state.projectCategories.includes(selectedFilter)) {
    elements.projectCategoryFilter.value = selectedFilter;
  }
  elements.projectCategoryOptions.innerHTML = state.projectCategories.map(
    (category) => `<option value="${escapeHtml(category)}"></option>`,
  ).join("");
}

function statusVaultProjects() {
  const archived = state.projectVisibility === "archived";
  return state.vaultProjects.filter((record) => (record.archived === true) === archived);
}

function filteredVaultProjects() {
  const query = elements.projectSearch.value.trim().toLocaleLowerCase();
  const category = elements.projectCategoryFilter.value;
  return statusVaultProjects().filter((record) => {
    if (category && record.category !== category) return false;
    if (!query) return true;
    return [record.name, record.category, record.summary, ...(record.technologies || [])]
      .join(" ")
      .toLocaleLowerCase()
      .includes(query);
  });
}

function renderProjectList() {
  const records = filteredVaultProjects();
  const statusRecords = statusVaultProjects();
  const activeCount = state.vaultProjects.filter((record) => record.archived !== true).length;
  const archivedCount = state.vaultProjects.length - activeCount;
  elements.projectArchivedCount.textContent = archivedCount;
  updateArchiveScopeToggle(
    elements.projectArchivedFilter,
    state.projectVisibility === "archived",
    "projects",
  );
  elements.projectRecordCount.textContent = `${records.length} of ${statusRecords.length}`;
  elements.projectRecordList.innerHTML = records.length
    ? records.map((record) => {
      const selected = record.id === state.selectedProjectId;
      return `<button type="button" class="vault-record ${selected ? "is-selected" : ""}"
          data-project-id="${escapeHtml(record.id)}" aria-pressed="${selected}"
          data-tooltip="Open this canonical project in the editor.">
          <span class="vault-card-kind vault-card-only">Project</span>
          <span class="vault-record-main">
            <strong>${escapeHtml(record.name)}</strong>
            <span>${escapeHtml(record.summary || "No summary")}</span>
          </span>
          <span class="vault-record-meta"><span>${escapeHtml(record.category)}</span><span>${escapeHtml(record.dates || "Dates not set")}</span></span>
          ${renderVaultCardPreview(record.technologies, "No technologies listed")}
          <span class="vault-record-counts">${(record.technologies || []).length} technologies / ${(record.proof_points || []).length} evidence items / ${(record.related_experience || []).length} experience links${record.archived === true ? " / archived" : ""}</span>
          ${renderVaultCardOpenLabel()}
        </button>`;
    }).join("")
    : `<div class="empty-state">No ${state.projectVisibility} projects match these filters.</div>`;
}

function renderProjectExperienceOptions(selectedIds = []) {
  const selected = new Set(selectedIds);
  elements.projectRelatedExperience.innerHTML = state.projectExperienceOptions.length
    ? state.projectExperienceOptions.map((option) => {
      const label = [option.company, option.role].filter(Boolean).join(" / ") || option.id;
      return `<label class="vault-relation-option">
        <input type="checkbox" value="${escapeHtml(option.id)}" ${selected.has(option.id) ? "checked" : ""}>
        <span><strong>${escapeHtml(label)}</strong><small>${escapeHtml(option.id)}${option.archived ? " / archived" : ""}</small></span>
      </label>`;
    }).join("")
    : '<div class="empty-state compact">No Experience records are available.</div>';
}

function populateProjectForm(record) {
  const isNew = state.projectEditorMode === "new";
  elements.projectEditorSource.textContent = isNew
    ? "New manual record"
    : (record?.source === "manual" ? "Manual record" : "Canonical record");
  elements.projectEditorTitle.textContent = isNew ? "New project" : (record?.name || "Project details");
  elements.projectEditorId.textContent = isNew ? "ID created on save" : (record?.id || "");
  elements.projectName.value = record?.name || "";
  elements.projectCategory.value = record?.category || "";
  elements.projectSummary.value = record?.summary || "";
  projectDateRange.setValue(record || {});
  setManualSkillsFromRecord("project", record);
  renderProjectExperienceOptions(record?.related_experience || []);
  elements.projectProofPoints.value = (record?.proof_points || []).join("\n");
  elements.archiveProjectButton.hidden = isNew || !record;
  if (!isNew && record) {
    const restoring = record.archived === true;
    setArchiveActionButton(elements.archiveProjectButton, restoring, "project");
    elements.archiveProjectButton.dataset.tooltip = restoring
      ? "Return this project to active matching and resume workflows."
      : "Move this project out of active workflows while preserving the complete record.";
  }
  state.projectFormDirty = false;
}

function renderProjects() {
  renderProjectCategoryOptions();
  renderProjectList();
  if (state.projectEditorMode === "new") return;
  let record = selectedVaultProject();
  const statusRecords = statusVaultProjects();
  if (!record || (record.archived === true) !== (state.projectVisibility === "archived")) {
    state.selectedProjectId = statusRecords[0]?.id || null;
    record = selectedVaultProject();
    renderProjectList();
  }
  populateProjectForm(record);
}

export async function loadVaultProjects({ keepMessage = false, refresh = false } = {}) {
  return withVaultLoading("projects", async () => {
    if (!state.skillsRegistry || refresh) await loadSkillsRegistry();
    const payload = (await getVaultSnapshot({ refresh })).projects;
    state.vaultProjects = payload.records || [];
    state.projectCategories = payload.categories || [];
    state.projectExperienceOptions = payload.experience_options || [];
    state.projectRevision = payload.revision;
    const visibleRecords = statusVaultProjects();
    if (!visibleRecords.some((record) => record.id === state.selectedProjectId)) {
      state.selectedProjectId = visibleRecords[0]?.id || null;
    }
    state.projectEditorMode = state.selectedProjectId ? "edit" : "new";
    renderProjects();
    if (!keepMessage) elements.projectMessage.textContent = "";
  });
}

function beginNewProject() {
  if (state.projectFormDirty && !window.confirm("Discard unsaved project changes?")) return;
  state.projectVisibility = "active";
  state.projectEditorMode = "new";
  state.selectedProjectId = null;
  populateProjectForm(null);
  renderProjectList();
  openCareerVaultCardDetail("projects");
  elements.projectName.focus();
}

export function selectVaultProject(recordId) {
  if (recordId === state.selectedProjectId && state.projectEditorMode === "edit") {
    openCareerVaultCardDetail("projects");
    return;
  }
  if (state.projectFormDirty && !window.confirm("Discard unsaved project changes?")) return;
  state.selectedProjectId = recordId;
  state.projectEditorMode = "edit";
  populateProjectForm(selectedVaultProject());
  renderProjectList();
  openCareerVaultCardDetail("projects");
}

function cancelProjectEdit() {
  if (state.projectEditorMode === "new") {
    state.selectedProjectId = statusVaultProjects()[0]?.id || null;
    state.projectEditorMode = state.selectedProjectId ? "edit" : "new";
  }
  populateProjectForm(selectedVaultProject());
  renderProjectList();
  elements.projectMessage.textContent = "Unsaved changes discarded.";
}

function setProjectVisibility(visibility) {
  if (visibility === state.projectVisibility) return;
  if (state.projectFormDirty && !window.confirm("Discard unsaved project changes?")) return;
  state.projectVisibility = visibility;
  state.projectEditorMode = "edit";
  state.selectedProjectId = statusVaultProjects()[0]?.id || null;
  closeCareerVaultCardDetail("projects");
  renderProjects();
}

async function toggleVaultProjectArchive() {
  const record = selectedVaultProject();
  if (!record || state.projectEditorMode === "new") return;
  const restoring = record.archived === true;
  if (!restoring && !window.confirm(`Archive ${record.name}? The record will be excluded from new matching.`)) return;
  elements.archiveProjectButton.disabled = true;
  elements.saveProjectButton.disabled = true;
  elements.projectMessage.className = "form-message";
  elements.projectMessage.textContent = restoring ? "Restoring project..." : "Archiving project...";
  try {
    const result = await requestJson(
      `/api/vault/projects/${encodeURIComponent(record.id)}/${restoring ? "restore" : "archive"}`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ expected_revision: state.projectRevision }),
      },
    );
    state.projectRevision = result.revision;
    state.selectedProjectId = null;
    state.projectFormDirty = false;
    await loadVaultProjects({ keepMessage: true, refresh: true });
    elements.projectMessage.textContent = restoring
      ? `Restored ${result.record.id}.`
      : `Archived ${result.record.id}.`;
  } catch (error) {
    elements.projectMessage.className = "form-message error";
    elements.projectMessage.textContent = formatRequestError(error);
  } finally {
    elements.archiveProjectButton.disabled = false;
    elements.saveProjectButton.disabled = false;
    await pollLogs();
  }
}

async function saveVaultProject(event) {
  event.preventDefault();
  const creating = state.projectEditorMode === "new";
  const recordId = state.selectedProjectId;
  elements.saveProjectButton.disabled = true;
  elements.saveProjectButton.textContent = "Saving...";
  elements.projectMessage.className = "form-message";
  elements.projectMessage.textContent = "Validating canonical Career Vault...";
  const dates = projectDateRange.value();
  const record = {
    name: elements.projectName.value,
    category: elements.projectCategory.value,
    summary: elements.projectSummary.value,
    ...dates,
    ...manualSkillRecordFields("project"),
    related_experience: [...elements.projectRelatedExperience.querySelectorAll('input[type="checkbox"]:checked')]
      .map((input) => input.value),
    proof_points: vaultLines(elements.projectProofPoints.value),
  };
  try {
    const result = await requestJson(
      creating ? "/api/vault/projects" : `/api/vault/projects/${encodeURIComponent(recordId)}`,
      {
        method: creating ? "POST" : "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ expected_revision: state.projectRevision, record }),
      },
    );
    state.selectedProjectId = result.record.id;
    if (result.registry_revision) state.skillsRegistry.revision = result.registry_revision;
    state.projectFormDirty = false;
    await loadVaultProjects({ keepMessage: true, refresh: true });
    elements.projectMessage.textContent = creating
      ? `Created ${result.record.id}.`
      : `Saved ${result.record.id}.`;
  } catch (error) {
    elements.projectMessage.className = "form-message error";
    elements.projectMessage.textContent = formatRequestError(error);
  } finally {
    elements.saveProjectButton.disabled = false;
    elements.saveProjectButton.textContent = "Save project";
    await pollLogs();
  }
}

export function initVaultProjects() {
  elements.newProjectButton.addEventListener("click", beginNewProject);
  elements.projectArchivedFilter.addEventListener("click", () => setProjectVisibility(
    state.projectVisibility === "archived" ? "active" : "archived"
  ));
  elements.archiveProjectButton.addEventListener("click", toggleVaultProjectArchive);
  elements.cancelProjectButton.addEventListener("click", cancelProjectEdit);
  elements.projectForm.addEventListener("submit", saveVaultProject);
  elements.projectForm.addEventListener("input", () => { state.projectFormDirty = true; });
  elements.projectForm.addEventListener("change", () => { state.projectFormDirty = true; });
  elements.projectSearch.addEventListener("input", () => {
    closeCareerVaultCardDetail("projects");
    renderProjectList();
  });
  elements.projectCategoryFilter.addEventListener("change", () => {
    closeCareerVaultCardDetail("projects");
    renderProjectList();
  });
  elements.projectRecordList.addEventListener("click", (event) => {
    const record = event.target.closest("[data-project-id]");
    if (record) selectVaultProject(record.dataset.projectId);
  });
}
