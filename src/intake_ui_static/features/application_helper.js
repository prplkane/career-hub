import { api } from "../ui/api.js";
import {
  escapeHtml,
  formatRequestError,
  renderLoadingState,
} from "../ui/primitives.js";

const state = {
  initialized: false,
  configuration: null,
  loading: false,
  saving: false,
  dirty: false,
  draftEnabledFields: null,
  demographics: null,
  demographicsLoading: false,
  demographicsSaving: false,
  demographicsDirty: false,
};

let elements = null;

function helperElements() {
  return {
    dialog: document.querySelector("#application-helper-dialog"),
    form: document.querySelector("#application-helper-form"),
    openButton: document.querySelector("#configure-application-helper"),
    closeButton: document.querySelector("#close-application-helper-dialog"),
    cancelButton: document.querySelector("#cancel-application-helper"),
    saveButton: document.querySelector("#save-application-helper"),
    enabledCount: document.querySelector("#application-helper-enabled-count"),
    boundaryTitle: document.querySelector("#application-helper-boundary-title"),
    summaryMessage: document.querySelector("#application-helper-summary-message"),
    fieldList: document.querySelector("#application-helper-field-list"),
    message: document.querySelector("#application-helper-message"),
    demographicsCount: document.querySelector("#application-demographics-us-count"),
    demographicsFields: document.querySelector("#application-demographics-us-fields"),
    demographicsMessage: document.querySelector("#application-demographics-message"),
    demographicsSaveButton: document.querySelector("#save-application-demographics"),
  };
}

function pluralFields(count) {
  return `${count} field${count === 1 ? "" : "s"} enabled`;
}

function setMessage(target, text = "", kind = "") {
  target.className = `form-message${kind ? ` ${kind}` : ""}`;
  target.textContent = text;
}

function renderSummary() {
  if (!elements) return;
  const count = Number(state.configuration?.enabled_count || 0);
  elements.enabledCount.textContent = state.loading ? "Loading" : pluralFields(count);
  elements.boundaryTitle.textContent = count
    ? "Canonical fields enabled for the assistant"
    : "No canonical fields enabled";
  elements.openButton.disabled = state.loading || state.saving;
}

function fieldOption(field, draftEnabled) {
  const copyOnly = field.source.startsWith("US ·");
  return `
    <label class="application-helper-field-option">
      <input type="checkbox" data-application-helper-field="${escapeHtml(field.id)}" ${(draftEnabled ? draftEnabled.has(field.id) : field.enabled) ? "checked" : ""} ${state.saving ? "disabled" : ""}>
      <span><strong>${escapeHtml(field.label)}</strong><small>${escapeHtml(field.source)}</small></span>
      <em>${copyOnly ? "Copy only" : field.value_kind === "boolean" ? "Yes / No" : "Text"}</em>
    </label>
  `;
}

function fieldGroup(label, fields, draftEnabled) {
  if (!fields.length) return "";
  return `
    <section class="application-helper-field-group" aria-label="${escapeHtml(label)} assistant fields">
      <h4>${escapeHtml(label)}</h4>
      <div>${fields.map((field) => fieldOption(field, draftEnabled)).join("")}</div>
    </section>
  `;
}

function renderFields() {
  if (!elements) return;
  if (state.loading) {
    elements.fieldList.innerHTML = renderLoadingState({
      label: "Loading private field controls…",
      compact: true,
    });
    elements.saveButton.disabled = true;
    return;
  }
  const fields = Array.isArray(state.configuration?.fields)
    ? state.configuration.fields
    : [];
  if (!fields.length) {
    elements.fieldList.innerHTML = '<p class="empty-state compact">No canonical helper fields are available.</p>';
    elements.saveButton.disabled = true;
    return;
  }
  const draftEnabled = state.draftEnabledFields
    ? new Set(state.draftEnabledFields)
    : null;
  const usFields = fields.filter((field) => field.source.startsWith("US ·"));
  const standardFields = fields.filter((field) => !field.source.startsWith("US ·"));
  elements.fieldList.innerHTML = [
    fieldGroup("Standard", standardFields, draftEnabled),
    fieldGroup("US", usFields, draftEnabled),
  ].join("");
  elements.saveButton.disabled = state.saving || !state.dirty;
  elements.saveButton.textContent = state.saving ? "Saving…" : "Save field access";
}

function usDemographicSection() {
  return Array.isArray(state.demographics?.sections)
    ? state.demographics.sections.find((section) => section.id === "US")
    : null;
}

function renderDemographics() {
  if (!elements?.demographicsFields) return;
  if (state.demographicsLoading) {
    elements.demographicsCount.textContent = "Loading";
    elements.demographicsFields.innerHTML = renderLoadingState({
      label: "Loading US application answers…",
      compact: true,
    });
    elements.demographicsSaveButton.disabled = true;
    return;
  }
  const section = usDemographicSection();
  const fields = Array.isArray(section?.fields) ? section.fields : [];
  const savedCount = fields.filter((field) => field.value).length;
  elements.demographicsCount.textContent = `${savedCount} of ${fields.length} saved`;
  if (!fields.length) {
    elements.demographicsFields.innerHTML = '<p class="empty-state compact">US application answers are unavailable.</p>';
    elements.demographicsSaveButton.disabled = true;
    return;
  }
  elements.demographicsFields.innerHTML = fields.map((field) => `
    <label class="application-demographic-field">
      <span>${escapeHtml(field.label)}</span>
      <select data-application-demographic-field="${escapeHtml(field.id)}" ${state.demographicsSaving ? "disabled" : ""}>
        <option value="">Not set</option>
        ${field.options.map((option) => `<option value="${escapeHtml(option)}" ${option === field.value ? "selected" : ""}>${escapeHtml(option)}</option>`).join("")}
      </select>
    </label>
  `).join("");
  elements.demographicsSaveButton.disabled = state.demographicsSaving
    || !state.demographicsDirty;
  elements.demographicsSaveButton.textContent = state.demographicsSaving
    ? "Saving…"
    : "Save US answers";
}

function selectedDemographicAnswers() {
  return Object.fromEntries(
    [...elements.demographicsFields.querySelectorAll("[data-application-demographic-field]")]
      .map((select) => [select.dataset.applicationDemographicField, select.value]),
  );
}

function selectedFieldIds() {
  return [...elements.fieldList.querySelectorAll("[data-application-helper-field]:checked")]
    .map((input) => input.dataset.applicationHelperField);
}

export async function loadApplicationHelperConfiguration({ keepMessage = false } = {}) {
  if (!elements) elements = helperElements();
  state.loading = true;
  state.demographicsLoading = true;
  renderSummary();
  if (elements.dialog.open) {
    renderFields();
    renderDemographics();
  }
  try {
    [state.configuration, state.demographics] = await Promise.all([
      api.getApplicationHelperConfiguration(),
      api.getApplicationDemographics(),
    ]);
    state.dirty = false;
    state.draftEnabledFields = null;
    state.demographicsDirty = false;
    if (!keepMessage) {
      setMessage(elements.summaryMessage);
      setMessage(elements.message);
    }
  } catch (error) {
    const message = formatRequestError(error);
    setMessage(elements.summaryMessage, message, "error");
    if (elements.dialog.open) setMessage(elements.message, message, "error");
  } finally {
    state.loading = false;
    state.demographicsLoading = false;
    renderSummary();
    if (elements.dialog.open) {
      renderFields();
      renderDemographics();
    }
  }
}

export async function openApplicationHelperConfiguration() {
  if (!elements) elements = helperElements();
  if (!elements.dialog) return;
  if (!elements.dialog.open) elements.dialog.showModal();
  setMessage(elements.message);
  await loadApplicationHelperConfiguration();
}

function closeConfiguration() {
  if (elements.dialog.open) elements.dialog.close();
  state.dirty = false;
  state.draftEnabledFields = null;
  state.demographicsDirty = false;
  setMessage(elements.message);
  setMessage(elements.demographicsMessage);
}

async function saveConfiguration() {
  if (!state.configuration || !state.dirty || state.saving) return;
  const enabledFields = state.draftEnabledFields || selectedFieldIds();
  state.saving = true;
  setMessage(elements.message, "Saving the explicit helper allowlist…");
  renderSummary();
  renderFields();
  try {
    state.configuration = await api.updateApplicationHelperConfiguration(
      enabledFields,
      state.configuration.revision,
    );
    state.dirty = false;
    state.draftEnabledFields = null;
    setMessage(
      elements.message,
      "Field access saved. The extension receives values only when the helper requests them.",
      "success",
    );
    setMessage(elements.summaryMessage);
  } catch (error) {
    setMessage(elements.message, formatRequestError(error), "error");
  } finally {
    state.saving = false;
    renderSummary();
    renderFields();
  }
}

async function saveDemographics() {
  if (!state.demographics || !state.demographicsDirty || state.demographicsSaving) return;
  const answers = selectedDemographicAnswers();
  state.demographicsSaving = true;
  setMessage(elements.demographicsMessage, "Saving US application answers…");
  renderDemographics();
  try {
    state.demographics = await api.updateApplicationDemographics(
      answers,
      state.demographics.revision,
    );
    state.demographicsDirty = false;
    setMessage(
      elements.demographicsMessage,
      "US answers saved locally. Enable a field below to make its one-field Copy action available.",
      "success",
    );
  } catch (error) {
    setMessage(elements.demographicsMessage, formatRequestError(error), "error");
  } finally {
    state.demographicsSaving = false;
    renderDemographics();
  }
}

export function initApplicationHelperConfiguration() {
  if (state.initialized) return;
  elements = helperElements();
  if (Object.values(elements).some((element) => !element)) return;
  state.initialized = true;
  elements.openButton.addEventListener("click", openApplicationHelperConfiguration);
  elements.closeButton.addEventListener("click", closeConfiguration);
  elements.cancelButton.addEventListener("click", closeConfiguration);
  elements.fieldList.addEventListener("input", (event) => {
    if (!event.target.closest("[data-application-helper-field]")) return;
    state.dirty = true;
    state.draftEnabledFields = selectedFieldIds();
    setMessage(elements.message, "Unsaved helper-field change.");
    elements.saveButton.disabled = false;
  });
  elements.demographicsFields.addEventListener("input", (event) => {
    if (!event.target.closest("[data-application-demographic-field]")) return;
    state.demographicsDirty = true;
    setMessage(elements.demographicsMessage, "Unsaved US answer change.");
    elements.demographicsSaveButton.disabled = false;
  });
  elements.demographicsSaveButton.addEventListener("click", saveDemographics);
  elements.form.addEventListener("submit", (event) => {
    event.preventDefault();
    saveConfiguration();
  });
  elements.dialog.addEventListener("cancel", (event) => {
    event.preventDefault();
    closeConfiguration();
  });
  renderSummary();
}
