// Career Vault experience list and editor.
import { elements, requestJson, state } from "../app_context.js";
import { createMonthRangePicker } from "../ui/dates.js";
import { escapeHtml, formatRequestError } from "../ui/primitives.js";
import { pollLogs } from "./diagnostics.js";
import {
  evidenceItemsFromInput,
  moveEvidenceItem,
  normalizedEvidenceItems,
  removeEvidenceItem,
} from "./evidence_items.js";
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
  withVaultLoading,
} from "./vault_shared.js";

const experienceDateRange = createMonthRangePicker(elements.vaultDateRange, { label: "Employment dates", required: true });

function selectedVaultExperience() {
  return state.vaultExperiences.find(
    (record) => record.id === state.selectedExperienceId
  ) || null;
}

function renderVaultCategoryOptions() {
  const selectedFilter = elements.vaultCategoryFilter.value;
  elements.vaultCategoryFilter.innerHTML = '<option value="">All categories</option>'
    + state.vaultCategories.map(
      (category) => `<option value="${escapeHtml(category)}" translate="no">${escapeHtml(category)}</option>`
    ).join("");
  if (state.vaultCategories.includes(selectedFilter)) {
    elements.vaultCategoryFilter.value = selectedFilter;
  }
  elements.vaultCategoryOptions.innerHTML = state.vaultCategories.map(
    (category) => `<option value="${escapeHtml(category)}"></option>`
  ).join("");
}

function statusVaultExperiences() {
  const archived = state.vaultVisibility === "archived";
  return state.vaultExperiences.filter((record) => (record.archived === true) === archived);
}

function filteredVaultExperiences() {
  const query = elements.vaultSearch.value.trim().toLocaleLowerCase();
  const category = elements.vaultCategoryFilter.value;
  return statusVaultExperiences().filter((record) => {
    if (category && record.category !== category) return false;
    if (!query) return true;
    const searchText = [
      record.company,
      record.role,
      record.category,
      ...(record.technologies || []),
      ...(record.proof_points || []),
    ].join(" ").toLocaleLowerCase();
    return searchText.includes(query);
  });
}

function renderVaultExperienceList() {
  const records = filteredVaultExperiences();
  const statusRecords = statusVaultExperiences();
  const activeCount = state.vaultExperiences.filter((record) => record.archived !== true).length;
  const archivedCount = state.vaultExperiences.length - activeCount;
  elements.vaultArchivedCount.textContent = archivedCount;
  updateArchiveScopeToggle(
    elements.vaultArchivedFilter,
    state.vaultVisibility === "archived",
    "experience",
  );
  elements.vaultExperienceCount.textContent = `${records.length} of ${statusRecords.length}`;
  elements.vaultExperienceList.innerHTML = records.length
    ? records.map((record) => {
      const selected = record.id === state.selectedExperienceId;
      return `
        <button type="button" class="vault-record ${selected ? "is-selected" : ""}"
          data-vault-experience-id="${escapeHtml(record.id)}" aria-pressed="${selected}"
          data-tooltip="Open ${escapeHtml(record.company)} experience for editing.">
          <span class="vault-card-kind vault-card-only">Experience</span>
          <span class="vault-record-main">
            <strong>${escapeHtml(record.company)}</strong>
            <span>${escapeHtml(record.role)}</span>
          </span>
          <span class="vault-record-meta">
            <span>${escapeHtml(record.dates)}</span>
            <span>${escapeHtml(record.category)}</span>
          </span>
          ${renderVaultCardPreview(record.technologies, "No technologies listed")}
          <span class="vault-record-counts">${(record.technologies || []).length} technologies / ${(record.proof_points || []).length} items${record.archived === true ? " / archived" : ""}</span>
          ${renderVaultCardOpenLabel()}
        </button>`;
    }).join("")
    : '<div class="empty-state">No experience records match these filters.</div>';
}

function currentVaultProofPoints() {
  return normalizedEvidenceItems(state.vaultProofPoints);
}

function renderVaultProofPointEditor() {
  const points = state.vaultProofPoints;
  const count = currentVaultProofPoints().length;
  elements.vaultProofPointCount.textContent = `${count} item${count === 1 ? "" : "s"}`;
  elements.vaultProofPointList.innerHTML = points.length
    ? points.map((point, index) => `
      <article class="vault-proof-point-card" data-vault-proof-point-card="${index}">
        <div class="vault-proof-point-card-heading">
          <span class="vault-proof-point-number" aria-hidden="true">${index + 1}</span>
          <label for="vault-proof-point-${index}">Evidence item ${index + 1}</label>
          <div class="vault-proof-point-actions" aria-label="Evidence item ${index + 1} actions">
            <button type="button" class="icon-action-button" data-vault-proof-move="up" data-vault-proof-index="${index}" aria-label="Move evidence item ${index + 1} up" ${index === 0 ? "disabled" : ""} title="Move up">
              <svg aria-hidden="true" viewBox="0 0 24 24"><path d="m6 14 6-6 6 6"></path></svg>
            </button>
            <button type="button" class="icon-action-button" data-vault-proof-move="down" data-vault-proof-index="${index}" aria-label="Move evidence item ${index + 1} down" ${index === points.length - 1 ? "disabled" : ""} title="Move down">
              <svg aria-hidden="true" viewBox="0 0 24 24"><path d="m6 10 6 6 6-6"></path></svg>
            </button>
            <button type="button" class="icon-action-button danger-action" data-vault-proof-remove="${index}" aria-label="Remove evidence item ${index + 1}" title="Remove item">
              <svg aria-hidden="true" viewBox="0 0 24 24"><path d="M5 7h14M9 7V4h6v3m-7 4v7m4-7v7m4-7v7M7 7l1 14h8l1-14"></path></svg>
            </button>
          </div>
        </div>
        <textarea id="vault-proof-point-${index}" data-vault-proof-value="${index}" rows="3" maxlength="5000" aria-label="Evidence item ${index + 1}">${escapeHtml(point)}</textarea>
      </article>
    `).join("")
    : '<div class="empty-state compact">No evidence items yet. Add a source-backed responsibility or achievement.</div>';
}

function addVaultProofPoint() {
  const additions = evidenceItemsFromInput(elements.vaultProofPointInput.value);
  if (!additions.length) {
    elements.vaultProofPointInput.focus();
    return;
  }
  state.vaultProofPoints.push(...additions);
  elements.vaultProofPointInput.value = "";
  state.vaultFormDirty = true;
  renderVaultProofPointEditor();
  elements.vaultProofPointList.querySelector(
    `[data-vault-proof-value="${state.vaultProofPoints.length - 1}"]`,
  )?.focus();
}

function updateVaultProofPoint(index, value) {
  if (!Number.isInteger(index) || index < 0 || index >= state.vaultProofPoints.length) return;
  state.vaultProofPoints[index] = value;
  state.vaultFormDirty = true;
  const count = currentVaultProofPoints().length;
  elements.vaultProofPointCount.textContent = `${count} item${count === 1 ? "" : "s"}`;
}

function moveVaultProofPoint(index, direction) {
  const targetIndex = direction === "up" ? index - 1 : index + 1;
  if (index < 0 || targetIndex < 0 || targetIndex >= state.vaultProofPoints.length) return;
  state.vaultProofPoints = moveEvidenceItem(state.vaultProofPoints, index, direction);
  state.vaultFormDirty = true;
  renderVaultProofPointEditor();
  elements.vaultProofPointList.querySelector(`[data-vault-proof-value="${targetIndex}"]`)?.focus();
}

function removeVaultProofPoint(index) {
  if (!Number.isInteger(index) || index < 0 || index >= state.vaultProofPoints.length) return;
  state.vaultProofPoints = removeEvidenceItem(state.vaultProofPoints, index);
  state.vaultFormDirty = true;
  renderVaultProofPointEditor();
  elements.vaultProofPointList.querySelector(`[data-vault-proof-value="${Math.max(0, index - 1)}"]`)?.focus()
    || elements.vaultProofPointInput.focus();
}

function populateVaultExperienceForm(record) {
  const isNew = state.vaultEditorMode === "new";
  elements.vaultEditorSource.textContent = isNew
    ? "Manual entry"
    : (record?.source === "manual" ? "Manual record" : "Canonical imported record");
  elements.vaultEditorTitle.textContent = isNew
    ? "New experience"
    : ([record?.company, record?.role].filter(Boolean).join(" / ") || "Experience details");
  elements.vaultEditorId.textContent = isNew ? "ID created on save" : (record?.id || "");
  elements.vaultCompany.value = record?.company || "";
  elements.vaultRole.value = record?.role || "";
  experienceDateRange.setValue(record || {});
  elements.vaultCategory.value = record?.category || "";
  setManualSkillsFromRecord("experience", record);
  state.vaultProofPoints = [...(record?.proof_points || [])];
  elements.vaultProofPointInput.value = "";
  renderVaultProofPointEditor();
  const archived = record?.archived === true;
  elements.archiveVaultButton.hidden = isNew || !record;
  setArchiveActionButton(elements.archiveVaultButton, archived, "experience");
  elements.archiveVaultButton.dataset.tooltip = archived
    ? "Return this experience to active matching and resume workflows."
    : "Move this experience out of active matching while preserving the complete record.";
  state.vaultFormDirty = false;
}

function renderVault() {
  renderVaultCategoryOptions();
  renderVaultExperienceList();
  if (state.vaultEditorMode === "new") return;
  let record = selectedVaultExperience();
  const statusRecords = statusVaultExperiences();
  if (!record || (record.archived === true) !== (state.vaultVisibility === "archived")) {
    state.selectedExperienceId = statusRecords[0]?.id || null;
    record = statusRecords[0] || null;
    renderVaultExperienceList();
  }
  populateVaultExperienceForm(record);
}

export async function loadVaultExperiences({ keepMessage = false, refresh = false } = {}) {
  return withVaultLoading("vault", async () => {
    if (!state.skillsRegistry || refresh) await loadSkillsRegistry();
    const payload = (await getVaultSnapshot({ refresh })).experiences;
    state.vaultExperiences = payload.records || [];
    state.vaultCategories = payload.categories || [];
    state.vaultRevision = payload.revision;
    const visibleRecords = statusVaultExperiences();
    if (!visibleRecords.some((record) => record.id === state.selectedExperienceId)) {
      state.selectedExperienceId = visibleRecords[0]?.id || null;
    }
    state.vaultEditorMode = state.selectedExperienceId ? "edit" : "new";
    renderVault();
    if (!keepMessage) elements.vaultMessage.textContent = "";
  });
}

function beginNewExperience() {
  if (state.vaultFormDirty && !window.confirm("Discard unsaved experience changes?")) return;
  state.vaultVisibility = "active";
  state.vaultEditorMode = "new";
  state.selectedExperienceId = null;
  populateVaultExperienceForm(null);
  renderVaultExperienceList();
  openCareerVaultCardDetail("experience");
  elements.vaultCompany.focus();
}

export function selectVaultExperience(recordId) {
  if (recordId === state.selectedExperienceId && state.vaultEditorMode === "edit") {
    openCareerVaultCardDetail("experience");
    return;
  }
  if (state.vaultFormDirty && !window.confirm("Discard unsaved experience changes?")) return;
  state.selectedExperienceId = recordId;
  state.vaultEditorMode = "edit";
  populateVaultExperienceForm(selectedVaultExperience());
  renderVaultExperienceList();
  openCareerVaultCardDetail("experience");
}

function cancelVaultEdit() {
  if (state.vaultEditorMode === "new") {
    state.selectedExperienceId = state.vaultExperiences[0]?.id || null;
    state.vaultEditorMode = state.selectedExperienceId ? "edit" : "new";
  }
  populateVaultExperienceForm(selectedVaultExperience());
  renderVaultExperienceList();
  elements.vaultMessage.textContent = "Unsaved changes discarded.";
}

function setVaultVisibility(visibility) {
  if (visibility === state.vaultVisibility) return;
  if (state.vaultFormDirty && !window.confirm("Discard unsaved experience changes?")) return;
  state.vaultVisibility = visibility;
  state.vaultEditorMode = "edit";
  state.selectedExperienceId = statusVaultExperiences()[0]?.id || null;
  closeCareerVaultCardDetail("experience");
  renderVault();
}

async function toggleVaultExperienceArchive() {
  const record = selectedVaultExperience();
  if (!record || state.vaultEditorMode === "new") return;
  const restoring = record.archived === true;
  if (!restoring && !window.confirm(
    `Archive ${record.company} / ${record.role}? It will be excluded from active matching.`
  )) return;
  elements.archiveVaultButton.disabled = true;
  elements.vaultMessage.className = "form-message";
  elements.vaultMessage.textContent = restoring ? "Restoring experience..." : "Archiving experience...";
  try {
    const result = await requestJson(
      `/api/vault/experiences/${encodeURIComponent(record.id)}/${restoring ? "restore" : "archive"}`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ expected_revision: state.vaultRevision }),
      }
    );
    state.vaultRevision = result.revision;
    state.selectedExperienceId = null;
    state.vaultFormDirty = false;
    await loadVaultExperiences({ keepMessage: true, refresh: true });
    elements.vaultMessage.textContent = restoring
      ? `Restored ${result.record.id}.`
      : `Archived ${result.record.id}.`;
  } catch (error) {
    elements.vaultMessage.className = "form-message error";
    elements.vaultMessage.textContent = formatRequestError(error);
  } finally {
    elements.archiveVaultButton.disabled = false;
    await pollLogs();
  }
}

async function saveVaultExperience(event) {
  event.preventDefault();
  const creating = state.vaultEditorMode === "new";
  const recordId = state.selectedExperienceId;
  elements.saveVaultButton.disabled = true;
  elements.saveVaultButton.textContent = "Saving...";
  elements.vaultMessage.className = "form-message";
  elements.vaultMessage.textContent = "Validating canonical Career Vault...";
  const dates = experienceDateRange.value();
  const record = {
    company: elements.vaultCompany.value,
    role: elements.vaultRole.value,
    ...dates,
    category: elements.vaultCategory.value,
    ...manualSkillRecordFields("experience"),
    proof_points: currentVaultProofPoints(),
  };
  try {
    const result = await requestJson(
      creating
        ? "/api/vault/experiences"
        : `/api/vault/experiences/${encodeURIComponent(recordId)}`,
      {
        method: creating ? "POST" : "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          expected_revision: state.vaultRevision,
          record,
        }),
      }
    );
    state.selectedExperienceId = result.record.id;
    if (result.registry_revision) state.skillsRegistry.revision = result.registry_revision;
    state.vaultFormDirty = false;
    await loadVaultExperiences({ keepMessage: true, refresh: true });
    elements.vaultMessage.textContent = creating
      ? `Created ${result.record.id}.`
      : `Saved ${result.record.id}.`;
  } catch (error) {
    elements.vaultMessage.className = "form-message error";
    elements.vaultMessage.textContent = formatRequestError(error);
  } finally {
    elements.saveVaultButton.disabled = false;
    elements.saveVaultButton.textContent = "Save experience";
    await pollLogs();
  }
}

export function initVaultExperiences() {
  elements.newExperienceButton.addEventListener("click", beginNewExperience);
  elements.vaultArchivedFilter.addEventListener("click", () => setVaultVisibility(
    state.vaultVisibility === "archived" ? "active" : "archived"
  ));
  elements.archiveVaultButton.addEventListener("click", toggleVaultExperienceArchive);
  elements.cancelVaultButton.addEventListener("click", cancelVaultEdit);
  elements.vaultExperienceForm.addEventListener("submit", saveVaultExperience);
  elements.vaultExperienceForm.addEventListener("input", () => { state.vaultFormDirty = true; });
  elements.addVaultProofPoint.addEventListener("click", addVaultProofPoint);
  elements.vaultProofPointList.addEventListener("input", (event) => {
    if (!event.target.matches("[data-vault-proof-value]")) return;
    updateVaultProofPoint(Number(event.target.dataset.vaultProofValue), event.target.value);
  });
  elements.vaultProofPointList.addEventListener("click", (event) => {
    const remove = event.target.closest("[data-vault-proof-remove]");
    if (remove) {
      removeVaultProofPoint(Number(remove.dataset.vaultProofRemove));
      return;
    }
    const move = event.target.closest("[data-vault-proof-move]");
    if (move) moveVaultProofPoint(Number(move.dataset.vaultProofIndex), move.dataset.vaultProofMove);
  });
  elements.vaultSearch.addEventListener("input", () => {
    closeCareerVaultCardDetail("experience");
    renderVaultExperienceList();
  });
  elements.vaultCategoryFilter.addEventListener("change", () => {
    closeCareerVaultCardDetail("experience");
    renderVaultExperienceList();
  });
  elements.vaultExperienceList.addEventListener("click", (event) => {
    const record = event.target.closest("[data-vault-experience-id]");
    if (record) selectVaultExperience(record.dataset.vaultExperienceId);
  });
}
