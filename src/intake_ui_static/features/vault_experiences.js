// Career Vault experience list and editor.
import { confirmAction, elements, requestJson, state } from "../app_context.js";
import { createMonthRangePicker } from "../ui/dates.js";
import { escapeHtml, formatRequestError } from "../ui/primitives.js";
import { pollLogs } from "./diagnostics.js";
import {
  evidenceItemsFromInput,
  moveEvidenceItem,
  normalizedEvidenceItems,
  removeEvidenceItem,
} from "./evidence_items.js";
import { loadSkillsRegistry, registrySkillIndex } from "./skills_hub.js";
import {
  manualSkillRecordFields,
  onManualSkillsChange,
  setManualSkillsFromRecord,
} from "./vault_manual_skills.js";
import {
  bindCategorySuggestion,
  createSkillHighlighter,
  getVaultSnapshot,
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

// Compact rows: company and dates on one line, role and category on the next.
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
          <span class="vault-record-main">
            <strong>${escapeHtml(record.company)}</strong>
            <span class="vault-record-dates">${escapeHtml(record.dates)}</span>
          </span>
          <span class="vault-record-meta">
            <span>${escapeHtml(record.role)}</span>
            <span>${escapeHtml(record.category)}</span>
          </span>
        </button>`;
    }).join("")
    : '<div class="empty-state">No experience records match these filters.</div>';
}

function currentVaultProofPoints() {
  return normalizedEvidenceItems(state.vaultProofPoints);
}

// The skills this record links, including custom ones proposed but not saved.
function linkedExperienceSkills() {
  const registry = registrySkillIndex();
  const selection = state.manualSkills.experience;
  return [
    ...selection.ids.map((id) => registry.get(id)).filter(Boolean),
    ...selection.pending.map((item) => ({ name: item.name, aliases: [], origin: "custom" })),
  ];
}

function evidenceActionButtons(index, total) {
  const label = `evidence item ${index + 1}`;
  return `
    <span class="vault-proof-point-actions">
      <button type="button" class="icon-action-button" data-vault-proof-move="up" data-vault-proof-index="${index}" aria-label="Move ${label} up" ${index === 0 ? "disabled" : ""} title="Move up">
        <svg aria-hidden="true" viewBox="0 0 24 24"><path d="m6 14 6-6 6 6"></path></svg>
      </button>
      <button type="button" class="icon-action-button" data-vault-proof-move="down" data-vault-proof-index="${index}" aria-label="Move ${label} down" ${index === total - 1 ? "disabled" : ""} title="Move down">
        <svg aria-hidden="true" viewBox="0 0 24 24"><path d="m6 10 6 6 6-6"></path></svg>
      </button>
      <button type="button" class="icon-action-button danger-action" data-vault-proof-remove="${index}" aria-label="Remove ${label}" title="Remove item">
        <svg aria-hidden="true" viewBox="0 0 24 24"><path d="M5 7h14M9 7V4h6v3m-7 4v7m4-7v7m4-7v7M7 7l1 14h8l1-14"></path></svg>
      </button>
    </span>`;
}

// Evidence rows: number, text clamped to two lines with the linked skills
// marked, and the controls. The row being edited shows a textarea instead.
function renderVaultProofPointEditor() {
  const points = state.vaultProofPoints;
  const count = currentVaultProofPoints().length;
  elements.vaultProofPointCount.textContent = `${count} item${count === 1 ? "" : "s"}`;
  const highlight = createSkillHighlighter(linkedExperienceSkills());
  elements.vaultProofPointList.innerHTML = points.length
    ? points.map((point, index) => {
      const editing = index === state.vaultProofEditingIndex;
      const rows = Math.min(8, Math.max(3, Math.ceil(String(point).length / 90)));
      const body = editing
        ? `<textarea id="vault-proof-point-${index}" data-vault-proof-value="${index}" rows="${rows}" maxlength="5000" aria-label="Evidence item ${index + 1}">${escapeHtml(point)}</textarea>`
        : `<button type="button" class="vault-evidence-text" data-vault-proof-open="${index}" aria-label="Edit evidence item ${index + 1}" title="Click to edit">${String(point).trim() ? highlight(point) : '<span class="is-empty">Empty item</span>'}</button>`;
      return `
      <article class="vault-evidence-row${editing ? " is-editing" : ""}" data-vault-proof-point-card="${index}">
        <span class="vault-proof-point-number" aria-hidden="true">${index + 1}</span>
        ${body}
        ${evidenceActionButtons(index, points.length)}
      </article>`;
    }).join("")
    : '<div class="empty-state compact">No evidence items yet. Add a source-backed responsibility or achievement.</div>';
}

function focusVaultProofPoint(index) {
  const field = elements.vaultProofPointList.querySelector(`[data-vault-proof-value="${index}"]`);
  if (!field) return false;
  field.focus();
  field.setSelectionRange(field.value.length, field.value.length);
  return true;
}

function openVaultProofPoint(index) {
  if (!Number.isInteger(index) || index < 0 || index >= state.vaultProofPoints.length) return;
  state.vaultProofEditingIndex = index;
  renderVaultProofPointEditor();
  focusVaultProofPoint(index);
}

function closeVaultProofPoint() {
  if (state.vaultProofEditingIndex === null) return;
  const index = state.vaultProofEditingIndex;
  state.vaultProofEditingIndex = null;
  renderVaultProofPointEditor();
  elements.vaultProofPointList.querySelector(`[data-vault-proof-open="${index}"]`)?.focus();
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
  state.vaultProofEditingIndex = null;
  renderVaultProofPointEditor();
  elements.vaultProofPointInput.focus();
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
  const editing = state.vaultProofEditingIndex === index;
  if (editing) state.vaultProofEditingIndex = targetIndex;
  renderVaultProofPointEditor();
  if (editing && focusVaultProofPoint(targetIndex)) return;
  // The same-direction button is disabled at the top or bottom; the row's text
  // button keeps focus inside the list then.
  const nextButton = elements.vaultProofPointList.querySelector(`[data-vault-proof-move="${direction}"][data-vault-proof-index="${targetIndex}"]:not(:disabled)`);
  (nextButton || elements.vaultProofPointList.querySelector(`[data-vault-proof-open="${targetIndex}"]`))?.focus();
}

function removeVaultProofPoint(index) {
  if (!Number.isInteger(index) || index < 0 || index >= state.vaultProofPoints.length) return;
  state.vaultProofPoints = removeEvidenceItem(state.vaultProofPoints, index);
  state.vaultFormDirty = true;
  state.vaultProofEditingIndex = null;
  renderVaultProofPointEditor();
  elements.vaultProofPointList.querySelector(`[data-vault-proof-open="${Math.max(0, index - 1)}"]`)?.focus()
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
  elements.vaultCategoryHint.hidden = true;
  setManualSkillsFromRecord("experience", record);
  state.vaultProofPoints = [...(record?.proof_points || [])];
  state.vaultProofEditingIndex = null;
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

// Unlike window.confirm, the dialog does not pause background loads, so each
// caller re-checks its own state after awaiting it.
function confirmDiscardExperienceChanges() {
  if (!state.vaultFormDirty) return Promise.resolve(true);
  return confirmAction({
    title: "Discard unsaved experience changes?",
    message: "Edits to this record have not been saved.",
    confirmLabel: "Discard changes",
    cancelLabel: "Keep editing",
    tone: "danger",
  });
}

async function beginNewExperience() {
  if (!(await confirmDiscardExperienceChanges())) return;
  state.vaultVisibility = "active";
  state.vaultEditorMode = "new";
  state.selectedExperienceId = null;
  populateVaultExperienceForm(null);
  renderVaultExperienceList();
  elements.vaultCompany.focus();
}

export async function selectVaultExperience(recordId) {
  if (recordId === state.selectedExperienceId && state.vaultEditorMode === "edit") return;
  if (!(await confirmDiscardExperienceChanges())) return;
  state.selectedExperienceId = recordId;
  state.vaultEditorMode = "edit";
  populateVaultExperienceForm(selectedVaultExperience());
  renderVaultExperienceList();
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

async function setVaultVisibility(visibility) {
  if (visibility === state.vaultVisibility) return;
  if (!(await confirmDiscardExperienceChanges()) || visibility === state.vaultVisibility) return;
  state.vaultVisibility = visibility;
  state.vaultEditorMode = "edit";
  state.selectedExperienceId = statusVaultExperiences()[0]?.id || null;
  renderVault();
}

async function toggleVaultExperienceArchive() {
  const record = selectedVaultExperience();
  if (!record || state.vaultEditorMode === "new") return;
  const restoring = record.archived === true;
  if (!restoring) {
    const confirmed = await confirmAction({
      title: `Archive ${record.company} / ${record.role}?`,
      message: "It will be excluded from active matching. The complete record is kept and can be restored.",
      confirmLabel: "Archive",
    });
    if (!confirmed || selectedVaultExperience()?.id !== record.id) return;
  }
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
  bindCategorySuggestion(
    elements.vaultCategory,
    elements.vaultCategoryHint,
    () => state.vaultCategories,
    () => { state.vaultFormDirty = true; },
  );
  onManualSkillsChange("experience", renderVaultProofPointEditor);
  elements.addVaultProofPoint.addEventListener("click", addVaultProofPoint);
  elements.vaultProofPointList.addEventListener("input", (event) => {
    if (!event.target.matches("[data-vault-proof-value]")) return;
    updateVaultProofPoint(Number(event.target.dataset.vaultProofValue), event.target.value);
  });
  elements.vaultProofPointList.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && event.target.matches("[data-vault-proof-value]")) {
      event.preventDefault();
      closeVaultProofPoint();
    }
  });
  elements.vaultProofPointList.addEventListener("click", (event) => {
    const open = event.target.closest("[data-vault-proof-open]");
    if (open) {
      openVaultProofPoint(Number(open.dataset.vaultProofOpen));
      return;
    }
    const remove = event.target.closest("[data-vault-proof-remove]");
    if (remove) {
      removeVaultProofPoint(Number(remove.dataset.vaultProofRemove));
      return;
    }
    const move = event.target.closest("[data-vault-proof-move]");
    if (move) moveVaultProofPoint(Number(move.dataset.vaultProofIndex), move.dataset.vaultProofMove);
  });
  elements.vaultSearch.addEventListener("input", renderVaultExperienceList);
  elements.vaultCategoryFilter.addEventListener("change", renderVaultExperienceList);
  elements.vaultExperienceList.addEventListener("click", (event) => {
    const record = event.target.closest("[data-vault-experience-id]");
    if (record) selectVaultExperience(record.dataset.vaultExperienceId);
  });
}
