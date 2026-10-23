// Career Vault education list and editor (schools and certifications).
import { elements, requestJson, state } from "../app_context.js";
import { createMonthPicker, createMonthRangePicker } from "../ui/dates.js";
import { escapeHtml, formatRequestError } from "../ui/primitives.js";
import { pollLogs } from "./diagnostics.js";
import {
  closeCareerVaultCardDetail,
  getVaultSnapshot,
  openCareerVaultCardDetail,
  renderVaultCardOpenLabel,
  renderVaultCardPreview,
  updateArchiveScopeToggle,
  vaultLines,
  withVaultLoading,
} from "./vault_shared.js";

const educationDateRange = createMonthRangePicker(elements.educationDateRange, { label: "Attendance dates" });
const graduationMonth = createMonthPicker(elements.educationGraduationMonth, { label: "Graduation month" });
const certificationCompletedMonth = createMonthPicker(elements.certificationCompletedMonth, { label: "Completion month", required: true });

function educationRecords() {
  return state.educationType === "school" ? state.vaultSchools : state.vaultCertifications;
}

function selectedEducationRecord() {
  return educationRecords().find((record) => record.id === state.selectedEducationId) || null;
}

function statusEducationRecords() {
  const archived = state.educationVisibility === "archived";
  return educationRecords().filter((record) => (record.archived === true) === archived);
}

function filteredEducationRecords() {
  const query = elements.educationSearch.value.trim().toLocaleLowerCase();
  return statusEducationRecords().filter((record) => {
    if (!query) return true;
    const values = state.educationType === "school"
      ? [record.institution, record.degree, record.major, record.location, ...(record.honors || [])]
      : [record.name, record.credential, record.completed, ...(record.proof_points || [])];
    return values.join(" ").toLocaleLowerCase().includes(query);
  });
}

function educationTypeLabel(plural = false) {
  if (state.educationType === "school") return plural ? "Schools" : "school";
  return plural ? "Certifications" : "certification";
}

function renderEducationList() {
  const records = filteredEducationRecords();
  const statusRecords = statusEducationRecords();
  const current = educationRecords();
  const activeCount = current.filter((record) => record.archived !== true).length;
  const archivedCount = current.length - activeCount;
  elements.educationSchoolCount.textContent = state.vaultSchools.length;
  elements.educationCertificationCount.textContent = state.vaultCertifications.length;
  elements.educationArchivedCount.textContent = archivedCount;
  elements.educationSchoolFilter.classList.toggle("active", state.educationType === "school");
  elements.educationCertificationFilter.classList.toggle("active", state.educationType === "certification");
  elements.educationSchoolFilter.setAttribute("aria-pressed", state.educationType === "school");
  elements.educationCertificationFilter.setAttribute("aria-pressed", state.educationType === "certification");
  updateArchiveScopeToggle(
    elements.educationArchivedFilter,
    state.educationVisibility === "archived",
    "education records",
  );
  elements.educationListTitle.textContent = educationTypeLabel(true);
  elements.newEducationButton.textContent = `New ${educationTypeLabel()}`;
  elements.educationRecordCount.textContent = `${records.length} of ${statusRecords.length}`;
  elements.educationRecordList.innerHTML = records.length
    ? records.map((record) => {
      const selected = record.id === state.selectedEducationId;
      const primary = state.educationType === "school" ? record.institution : record.name;
      const secondary = state.educationType === "school"
        ? [record.degree, record.major].filter(Boolean).join(" / ")
        : record.credential;
      const timing = state.educationType === "school"
        ? (record.dates || record.graduation_date || "Dates not set")
        : (record.completed || "Completion not set");
      const itemCount = state.educationType === "school"
        ? (record.honors || []).length
        : (record.proof_points || []).length;
      return `<button type="button" class="vault-record ${selected ? "is-selected" : ""}"
          data-education-id="${escapeHtml(record.id)}" aria-pressed="${selected}"
          data-tooltip="Open this canonical ${educationTypeLabel()} in the editor.">
          <span class="vault-card-kind vault-card-only">${escapeHtml(educationTypeLabel())}</span>
          <span class="vault-record-main"><strong>${escapeHtml(primary || record.id)}</strong></span>
          <span class="vault-keyword-preview">${escapeHtml(secondary || "No additional detail")}</span>
          <span class="vault-record-meta"><span>${escapeHtml(timing)}</span><span>${itemCount} items</span></span>
          ${renderVaultCardPreview(
            state.educationType === "school" ? record.honors : record.proof_points,
            state.educationType === "school" ? "No honors listed" : "No evidence items listed",
          )}
          <span class="vault-record-counts vault-education-record-id">${escapeHtml(record.id)}${record.archived === true ? " / archived" : ""}</span>
          ${renderVaultCardOpenLabel()}
        </button>`;
    }).join("")
    : `<div class="empty-state">No ${state.educationVisibility} ${educationTypeLabel(true).toLocaleLowerCase()} match this search.</div>`;
}

function setEducationFieldAvailability() {
  const school = state.educationType === "school";
  elements.schoolFields.hidden = !school;
  elements.certificationFields.hidden = school;
  elements.schoolFields.querySelectorAll("input, textarea").forEach((field) => { field.disabled = !school; });
  elements.certificationFields.querySelectorAll("input, textarea").forEach((field) => { field.disabled = school; });
}

function populateEducationForm(record) {
  const isNew = state.educationEditorMode === "new";
  setEducationFieldAvailability();
  elements.educationEditorSource.textContent = isNew
    ? "New manual record"
    : (record?.source === "manual" ? "Manual record" : "Canonical record");
  const primary = state.educationType === "school" ? record?.institution : record?.name;
  elements.educationEditorTitle.textContent = isNew
    ? `New ${educationTypeLabel()}`
    : (primary || "Education details");
  elements.educationEditorId.textContent = isNew ? "ID created on save" : (record?.id || "");
  elements.educationInstitution.value = record?.institution || "";
  elements.educationDegree.value = record?.degree || "";
  elements.educationMajor.value = record?.major || "";
  elements.educationLocation.value = record?.location || "";
  educationDateRange.setValue(record || {});
  graduationMonth.setValue(record?.graduation_date || "", record?.graduation_month || "");
  elements.educationGpa.value = record?.gpa || "";
  elements.educationStatus.value = record?.status || "";
  elements.educationHonors.value = (record?.honors || []).join("\n");
  elements.certificationName.value = record?.name || "";
  elements.certificationCredential.value = record?.credential || "";
  certificationCompletedMonth.setValue(record?.completed || "", record?.completed_month || "");
  elements.certificationProofPoints.value = (record?.proof_points || []).join("\n");
  elements.archiveEducationButton.hidden = isNew || !record;
  if (!isNew && record) {
    const restoring = record.archived === true;
    elements.archiveEducationButton.textContent = restoring ? "Restore" : "Archive";
    elements.archiveEducationButton.dataset.tooltip = restoring
      ? `Return this ${educationTypeLabel()} to active workflows.`
      : `Move this ${educationTypeLabel()} out of active workflows while preserving it.`;
  }
  elements.saveEducationButton.textContent = `Save ${educationTypeLabel()}`;
  state.educationFormDirty = false;
}

function renderEducation() {
  renderEducationList();
  if (state.educationEditorMode === "new") return;
  let record = selectedEducationRecord();
  const visibleRecords = statusEducationRecords();
  if (!record || (record.archived === true) !== (state.educationVisibility === "archived")) {
    state.selectedEducationId = visibleRecords[0]?.id || null;
    record = selectedEducationRecord();
    renderEducationList();
  }
  populateEducationForm(record);
}

export async function loadVaultEducation({ keepMessage = false, refresh = false } = {}) {
  return withVaultLoading("education", async () => {
    const payload = (await getVaultSnapshot({ refresh })).education;
    state.vaultSchools = payload.schools || [];
    state.vaultCertifications = payload.certifications || [];
    state.educationRevision = payload.revision;
    const visibleRecords = statusEducationRecords();
    if (!visibleRecords.some((record) => record.id === state.selectedEducationId)) {
      state.selectedEducationId = visibleRecords[0]?.id || null;
    }
    state.educationEditorMode = state.selectedEducationId ? "edit" : "new";
    renderEducation();
    if (!keepMessage) elements.educationMessage.textContent = "";
  });
}

function setEducationType(recordType) {
  if (recordType === state.educationType) return;
  if (state.educationFormDirty && !window.confirm("Discard unsaved education changes?")) return;
  state.educationType = recordType;
  state.educationEditorMode = "edit";
  state.selectedEducationId = statusEducationRecords()[0]?.id || null;
  closeCareerVaultCardDetail("education");
  renderEducation();
}

function beginNewEducation() {
  if (state.educationFormDirty && !window.confirm("Discard unsaved education changes?")) return;
  state.educationVisibility = "active";
  state.educationEditorMode = "new";
  state.selectedEducationId = null;
  populateEducationForm(null);
  renderEducationList();
  openCareerVaultCardDetail("education");
  const focusTarget = state.educationType === "school"
    ? elements.educationInstitution
    : elements.certificationName;
  focusTarget.focus();
}

export function selectEducationRecord(recordId) {
  if (recordId === state.selectedEducationId && state.educationEditorMode === "edit") {
    openCareerVaultCardDetail("education");
    return;
  }
  if (state.educationFormDirty && !window.confirm("Discard unsaved education changes?")) return;
  state.selectedEducationId = recordId;
  state.educationEditorMode = "edit";
  populateEducationForm(selectedEducationRecord());
  renderEducationList();
  openCareerVaultCardDetail("education");
}

function cancelEducationEdit() {
  if (state.educationEditorMode === "new") {
    state.selectedEducationId = statusEducationRecords()[0]?.id || null;
    state.educationEditorMode = state.selectedEducationId ? "edit" : "new";
  }
  populateEducationForm(selectedEducationRecord());
  renderEducationList();
  elements.educationMessage.textContent = "Unsaved changes discarded.";
}

function setEducationVisibility(visibility) {
  if (visibility === state.educationVisibility) return;
  if (state.educationFormDirty && !window.confirm("Discard unsaved education changes?")) return;
  state.educationVisibility = visibility;
  state.educationEditorMode = "edit";
  state.selectedEducationId = statusEducationRecords()[0]?.id || null;
  closeCareerVaultCardDetail("education");
  renderEducation();
}

function educationApiCollection() {
  return state.educationType === "school" ? "schools" : "certifications";
}

async function toggleEducationArchive() {
  const record = selectedEducationRecord();
  if (!record || state.educationEditorMode === "new") return;
  const restoring = record.archived === true;
  if (!restoring && !window.confirm(`Archive ${record.institution || record.name}? It will be excluded from new active workflows.`)) return;
  elements.archiveEducationButton.disabled = true;
  elements.saveEducationButton.disabled = true;
  elements.educationMessage.className = "form-message";
  elements.educationMessage.textContent = restoring ? "Restoring record..." : "Archiving record...";
  try {
    const result = await requestJson(
      `/api/vault/education/${educationApiCollection()}/${encodeURIComponent(record.id)}/${restoring ? "restore" : "archive"}`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ expected_revision: state.educationRevision }),
      },
    );
    state.selectedEducationId = null;
    state.educationFormDirty = false;
    await loadVaultEducation({ keepMessage: true, refresh: true });
    elements.educationMessage.textContent = restoring
      ? `Restored ${result.record.id}.`
      : `Archived ${result.record.id}.`;
  } catch (error) {
    elements.educationMessage.className = "form-message error";
    elements.educationMessage.textContent = formatRequestError(error);
  } finally {
    elements.archiveEducationButton.disabled = false;
    elements.saveEducationButton.disabled = false;
    await pollLogs();
  }
}

async function saveVaultEducation(event) {
  event.preventDefault();
  const creating = state.educationEditorMode === "new";
  const recordId = state.selectedEducationId;
  const school = state.educationType === "school";
  const dates = educationDateRange.value();
  const graduation = graduationMonth.value();
  const completed = certificationCompletedMonth.value();
  const record = school
    ? {
      institution: elements.educationInstitution.value,
      degree: elements.educationDegree.value,
      major: elements.educationMajor.value,
      location: elements.educationLocation.value,
      ...dates,
      graduation_date: graduation.display,
      graduation_month: graduation.month,
      gpa: elements.educationGpa.value,
      status: elements.educationStatus.value,
      honors: vaultLines(elements.educationHonors.value),
    }
    : {
      name: elements.certificationName.value,
      credential: elements.certificationCredential.value,
      completed: completed.display,
      completed_month: completed.month,
      proof_points: vaultLines(elements.certificationProofPoints.value),
    };
  elements.saveEducationButton.disabled = true;
  elements.saveEducationButton.textContent = "Saving...";
  elements.educationMessage.className = "form-message";
  elements.educationMessage.textContent = "Validating canonical Career Vault...";
  try {
    const collection = educationApiCollection();
    const result = await requestJson(
      creating
        ? `/api/vault/education/${collection}`
        : `/api/vault/education/${collection}/${encodeURIComponent(recordId)}`,
      {
        method: creating ? "POST" : "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ expected_revision: state.educationRevision, record }),
      },
    );
    state.selectedEducationId = result.record.id;
    state.educationFormDirty = false;
    await loadVaultEducation({ keepMessage: true, refresh: true });
    elements.educationMessage.textContent = creating
      ? `Created ${result.record.id}.`
      : `Saved ${result.record.id}.`;
  } catch (error) {
    elements.educationMessage.className = "form-message error";
    elements.educationMessage.textContent = formatRequestError(error);
  } finally {
    elements.saveEducationButton.disabled = false;
    elements.saveEducationButton.textContent = `Save ${educationTypeLabel()}`;
    await pollLogs();
  }
}

export function initEducation() {
  elements.newEducationButton.addEventListener("click", beginNewEducation);
  elements.educationSchoolFilter.addEventListener("click", () => setEducationType("school"));
  elements.educationCertificationFilter.addEventListener("click", () => setEducationType("certification"));
  elements.educationArchivedFilter.addEventListener("click", () => setEducationVisibility(
    state.educationVisibility === "archived" ? "active" : "archived"
  ));
  elements.archiveEducationButton.addEventListener("click", toggleEducationArchive);
  elements.cancelEducationButton.addEventListener("click", cancelEducationEdit);
  elements.educationForm.addEventListener("submit", saveVaultEducation);
  elements.educationForm.addEventListener("input", () => { state.educationFormDirty = true; });
  elements.educationSearch.addEventListener("input", () => {
    closeCareerVaultCardDetail("education");
    renderEducationList();
  });
  elements.educationRecordList.addEventListener("click", (event) => {
    const record = event.target.closest("[data-education-id]");
    if (record) selectEducationRecord(record.dataset.educationId);
  });
}
