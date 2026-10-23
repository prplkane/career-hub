"use strict";

const extensionApi = CareerHubBrowser.api;
const LOAD_HELPER_FIELDS_COMMAND = "career-hub.application-helper.load.v1";
const OPEN_HELPER_SETTINGS_COMMAND = "career-hub.application-helper.open-settings.v1";
const REVIEW_HELPER_FORM_COMMAND = "career-hub.application-helper.review-form.v1";
const FILL_HELPER_FIELDS_COMMAND = "career-hub.application-helper.fill-reviewed.v1";
const OPEN_HELPER_RESUME_COMMAND = "career-hub.application-helper.open-resume.v1";
const REVIEW_SAVED_ANSWERS_COMMAND = "career-hub.application-helper.review-answers.v1";
const FILL_SAVED_ANSWERS_COMMAND = "career-hub.application-helper.fill-reviewed-answers.v1";
const FIND_REUSABLE_ANSWERS_COMMAND = "career-hub.application-helper.find-reusable-answers.v1";
const SAVE_REUSABLE_ANSWER_COMMAND = "career-hub.application-helper.save-reviewed-answer.v1";
const DELETE_REUSABLE_ANSWER_COMMAND = "career-hub.application-helper.delete-answer.v1";
const PAIR_EXTENSION_COMMAND = "career-hub.extension.pair.v1";
const PAIRING_CODE_PATTERN = /^[A-Za-z0-9]{4}-?[A-Za-z0-9]{4}$/;
const PREFILL_FIELD_IDS = CareerHubHelperCatalog.prefillFieldIds;

const refreshButton = document.querySelector("#refresh-button");
const settingsButton = document.querySelector("#settings-button");
const statusPanel = document.querySelector("#status-panel");
const statusTitle = document.querySelector("#status-title");
const statusMessage = document.querySelector("#status-message");
const loadingIcon = document.querySelector("#loading-icon");
const emptyState = document.querySelector("#empty-state");
const fieldList = document.querySelector("#field-list");
const profileFieldCount = document.querySelector("#profile-field-count");
const profileCopyCount = document.querySelector("#profile-copy-count");
const profileValueShelf = document.querySelector("#profile-value-shelf");
const copyAnnouncement = document.querySelector("#copy-announcement");
const reviewFieldsButton = document.querySelector("#review-fields-button");
const prefillStatus = document.querySelector("#prefill-status");
const prefillStatusTitle = document.querySelector("#prefill-status-title");
const prefillStatusMessage = document.querySelector("#prefill-status-message");
const mappingList = document.querySelector("#mapping-list");
const fillSelectedButton = document.querySelector("#fill-selected-button");
const resumeCount = document.querySelector("#resume-count");
const resumeEmptyState = document.querySelector("#resume-empty-state");
const resumeList = document.querySelector("#resume-list");
const answerCount = document.querySelector("#answer-count");
const answerEmptyState = document.querySelector("#answer-empty-state");
const answerList = document.querySelector("#answer-list");
const reviewAnswersButton = document.querySelector("#review-answers-button");
const findAnswersButton = document.querySelector("#find-answers-button");
const answerStatus = document.querySelector("#answer-status");
const answerStatusTitle = document.querySelector("#answer-status-title");
const answerStatusMessage = document.querySelector("#answer-status-message");
const answerMappingList = document.querySelector("#answer-mapping-list");
const fillAnswersButton = document.querySelector("#fill-answers-button");
const answerCandidateList = document.querySelector("#answer-candidate-list");
const pairingSection = document.querySelector("#pairing-section");
const pairingForm = document.querySelector("#pairing-form");
const pairingCodeInput = document.querySelector("#pairing-code-input");
const pairingSubmitButton = document.querySelector("#pairing-submit-button");
const pairingStatus = document.querySelector("#pairing-status");
const pairingStatusTitle = document.querySelector("#pairing-status-title");
const pairingStatusMessage = document.querySelector("#pairing-status-message");

let currentProjection = null;
let currentProfileReview = null;
let currentAnswerReview = null;
let currentCandidateReview = null;
let requestInProgress = false;
let profileActionInProgress = false;
let answerActionInProgress = false;
let copiedResetTimer = null;

function setStatus(title, message, tone = "neutral", loading = false) {
  statusPanel.dataset.tone = tone;
  statusTitle.textContent = title;
  statusMessage.textContent = message;
  loadingIcon.hidden = !loading;
}

function setPrefillStatus(title, message, tone = "neutral") {
  prefillStatus.dataset.tone = tone;
  prefillStatusTitle.textContent = title;
  prefillStatusMessage.textContent = message;
}

function setPairingStatus(title, message, tone = "neutral") {
  pairingStatus.dataset.tone = tone;
  pairingStatusTitle.textContent = title;
  pairingStatusMessage.textContent = message;
}

function setAnswerStatus(title, message, tone = "neutral") {
  answerStatus.dataset.tone = tone;
  answerStatusTitle.textContent = title;
  answerStatusMessage.textContent = message;
}

function safeText(value, maximum = 84) {
  const normalized = String(value).replace(/\s+/g, " ").trim();
  return normalized.length <= maximum
    ? normalized
    : `${normalized.slice(0, maximum - 1)}…`;
}

function errorCopy(code) {
  // Pairing copy never reveals how many guesses remain (ADR SEC-250 item 4).
  if (code === "EXTENSION_PAIRING_REQUIRED") {
    return [
      "Pair with Career Hub",
      "One-time setup: create a code in Career Hub Settings and type it below.",
    ];
  }
  if (code === "EXTENSION_PAIRING_CODE_INVALID") {
    return [
      "That code didn’t match",
      "Check the code shown in Career Hub Settings and type it again.",
    ];
  }
  if (code === "EXTENSION_PAIRING_CODE_EXPIRED") {
    return [
      "Code no longer valid",
      "Create a new code in Career Hub Settings. Codes expire after five minutes or too many attempts.",
    ];
  }
  if (code === "EXTENSION_PAIRING_UNAVAILABLE") {
    return [
      "Pairing couldn’t be saved",
      "Restart Career Hub, then create a new code and try again.",
    ];
  }
  if (code === "LOCAL_API_ACCESS_DENIED") {
    return [
      "Extension isn’t allowed",
      "In Career Hub, open Settings → Browser extension, turn it on, add this browser’s origin if needed, then restart Career Hub.",
    ];
  }
  if (code === "APPLICATION_HELPER_CONTRACT_UNSUPPORTED") {
    return [
      "Career Hub and extension differ",
      "Reload the latest extension files and restart Career Hub, then refresh.",
    ];
  }
  if (code === "APPLICATION_HELPER_SOURCE_UNAVAILABLE") {
    return [
      "A local source needs attention",
      "Open Career Hub and review the enabled Profile fields and Resume Library.",
    ];
  }
  if (code === "APPLICATION_HELPER_CONFIGURATION_INVALID") {
    return [
      "Helper settings need attention",
      "Open Career Hub Profile and save the allowed-field configuration again.",
    ];
  }
  if (code === "APP_TIMEOUT") {
    return [
      "Career Hub took too long",
      "Confirm the local app is running, then refresh this panel.",
    ];
  }
  if (code === "APP_RESPONSE_INVALID") {
    return [
      "Assistant response wasn’t compatible",
      "Reload the latest extension files and restart Career Hub before retrying.",
    ];
  }
  return [
    "Career Hub isn’t reachable",
    "Start Career Hub with the browser extension turned on in its Settings, then refresh this panel.",
  ];
}

function prefillErrorCopy(code) {
  if (["ACTIVE_TAB_UNAVAILABLE", "ACTIVE_TAB_UNSUPPORTED"].includes(code)) {
    return [
      "Open an application page",
      "Review fields while a normal HTTP or HTTPS application form is active.",
    ];
  }
  if (code === "PAGE_ACCESS_UNAVAILABLE") {
    return [
      "This page cannot be inspected",
      "Use the one-field copy fallback or open the application in a normal browser tab.",
    ];
  }
  if (code === "APPLICATION_HELPER_NO_PREFILL_FIELDS") {
    return [
      "No profile fields ready",
      "Enable a supported Profile or eligibility field, then refresh.",
    ];
  }
  if (code === "APPLICATION_HELPER_REVIEW_EXPIRED") {
    return ["Review expired", "Review the current page again before filling anything."];
  }
  if (code === "APPLICATION_HELPER_PAGE_CHANGED") {
    return ["Application page changed", "Return to the intended form and review its fields again."];
  }
  if (code === "APPLICATION_HELPER_DATA_CHANGED") {
    return ["Career Hub data changed", "Refresh, then review the page again."];
  }
  if (code === "APPLICATION_HELPER_FORM_CHANGED") {
    return ["Form changed after review", "Nothing was filled. Review the current fields again."];
  }
  if (code === "APPLICATION_HELPER_SELECTION_INVALID") {
    return ["Selection needs review", "Nothing was filled. Review supported empty fields again."];
  }
  if (["APPLICATION_HELPER_FORM_RESPONSE_INVALID", "APPLICATION_HELPER_CONTRACT_UNSUPPORTED"].includes(code)) {
    return [
      "Assistant versions differ",
      "Reload the latest extension files and restart Career Hub before retrying.",
    ];
  }
  const [, message] = errorCopy(code);
  return ["Page review unavailable", message];
}

function answerErrorCopy(code) {
  if (["ACTIVE_TAB_UNAVAILABLE", "ACTIVE_TAB_UNSUPPORTED"].includes(code)) {
    return ["Open an application page", "Use this action while the intended HTTP or HTTPS form is active."];
  }
  if (code === "PAGE_ACCESS_UNAVAILABLE") {
    return ["This page cannot be inspected", "Open the application form in a normal browser tab and try again."];
  }
  if (code === "APPLICATION_HELPER_NO_SAVED_ANSWERS") {
    return ["No saved answers", "Find a completed safe answer first, then explicitly save it."];
  }
  if (code === "APPLICATION_ANSWER_PAGE_TOO_COMPLEX") {
    return ["Page is too complex", "Use manual entry on this page; no answer was read or filled."];
  }
  if (code === "APPLICATION_ANSWER_FORM_AMBIGUOUS") {
    return ["Multiple forms look equally likely", "Open the intended application step by itself and review again."];
  }
  if (code === "APPLICATION_ANSWER_REVIEW_EXPIRED") {
    return ["Answer review expired", "Review the current page again before filling or saving."];
  }
  if (code === "APPLICATION_ANSWER_PAGE_CHANGED") {
    return ["Application page changed", "Return to the reviewed form and start a new answer review."];
  }
  if (["APPLICATION_ANSWER_DATA_CHANGED", "APPLICATION_ANSWER_REVISION_CONFLICT"].includes(code)) {
    return ["Saved answers changed", "Refresh the assistant, then start a new answer review."];
  }
  if (code === "APPLICATION_ANSWER_FORM_CHANGED") {
    return ["Answer changed after review", "Nothing was saved or filled. Review the page again."];
  }
  if (code === "APPLICATION_ANSWER_SELECTION_INVALID") {
    return ["Answer selection is no longer valid", "Nothing was changed. Review the page again."];
  }
  if (code === "APPLICATION_ANSWER_INVALID") {
    return ["This answer cannot be reused", "Sensitive, job-specific, eligibility, or open-ended answers remain manual."];
  }
  if (["APPLICATION_ANSWER_FORM_RESPONSE_INVALID", "APPLICATION_ANSWER_REQUEST_INVALID"].includes(code)) {
    return ["Assistant versions differ", "Reload the extension and restart Career Hub before retrying."];
  }
  const [, message] = errorCopy(code);
  return ["Reusable-answer action unavailable", message];
}

function safeUrlPreview(value) {
  try {
    const parsed = new URL(value);
    const path = parsed.pathname === "/" ? "" : parsed.pathname.replace(/\/$/, "");
    return safeText(`${parsed.hostname}${path}`, 68);
  } catch {
    return "Saved link";
  }
}

function previewProfileValue(field) {
  if (field.value_kind === "boolean") return field.value ? "Yes" : "No";
  if (field.id === "email") {
    const at = field.value.lastIndexOf("@");
    return at > 0 && at < field.value.length - 1
      ? `${field.value.slice(0, 1)}•••${field.value.slice(at)}`
      : "Saved email";
  }
  if (field.id === "phone") {
    const digits = field.value.replace(/\D/g, "");
    return digits.length >= 4 ? `••• ••• ${digits.slice(-4)}` : "Saved phone";
  }
  if (
    field.id === "address"
    || ["address_line1", "address_line2", "city", "region", "postal_code", "country"].includes(field.id)
  ) return "Saved address detail";
  if (["linkedin", "github", "website"].includes(field.id)) return safeUrlPreview(field.value);
  return safeText(field.value);
}

function answerValueText(answer) {
  return answer.value_kind === "boolean"
    ? (answer.value ? "Yes" : "No")
    : safeText(answer.value, 120);
}

function clipboardValue(record) {
  return record.value_kind === "boolean"
    ? (record.value ? "Yes" : "No")
    : record.value;
}

async function requestExtension(message) {
  let result;
  try {
    result = await extensionApi.runtime.sendMessage(message);
  } catch {
    throw new Error("APP_UNAVAILABLE");
  }
  if (!result || result.ok !== true) {
    throw new Error(result?.error_code || "APP_UNAVAILABLE");
  }
  return result;
}

function requestProjection() {
  return requestExtension({ command: LOAD_HELPER_FIELDS_COMMAND });
}

function clearProfileReviewState(resetStatus = true) {
  currentProfileReview = null;
  mappingList.replaceChildren();
  mappingList.hidden = true;
  fillSelectedButton.hidden = true;
  fillSelectedButton.disabled = true;
  fillSelectedButton.textContent = "Fill selected fields";
  if (resetStatus) {
    setPrefillStatus("Page not inspected", "No application-page profile field has been read.");
  }
}

function clearAnswerReviewState(resetStatus = true) {
  currentAnswerReview = null;
  currentCandidateReview = null;
  answerMappingList.replaceChildren();
  answerCandidateList.replaceChildren();
  answerMappingList.hidden = true;
  answerCandidateList.hidden = true;
  fillAnswersButton.hidden = true;
  fillAnswersButton.disabled = true;
  fillAnswersButton.textContent = "Fill selected answers";
  if (resetStatus) {
    setAnswerStatus("Page not inspected", "No application-page answer has been read.");
  }
}

function clearSensitiveState() {
  currentProjection = null;
  fieldList.replaceChildren();
  resumeList.replaceChildren();
  answerList.replaceChildren();
  fieldList.hidden = true;
  resumeList.hidden = true;
  answerList.hidden = true;
  emptyState.hidden = true;
  resumeEmptyState.hidden = true;
  answerEmptyState.hidden = true;
  profileFieldCount.textContent = "0 ready";
  profileCopyCount.textContent = "0";
  resumeCount.textContent = "0 ready";
  answerCount.textContent = "0 saved";
  copyAnnouncement.textContent = "";
  clearProfileReviewState();
  clearAnswerReviewState();
  if (copiedResetTimer !== null) {
    clearTimeout(copiedResetTimer);
    copiedResetTimer = null;
  }
}

function updateActionAvailability() {
  const hasProjection = Boolean(currentProjection);
  const profileReady = hasProjection && currentProjection.fields.some((field) => (
    PREFILL_FIELD_IDS.has(field.id)
  ));
  reviewFieldsButton.disabled = requestInProgress || profileActionInProgress || !profileReady;
  reviewAnswersButton.disabled = requestInProgress
    || answerActionInProgress
    || !hasProjection
    || currentProjection.answers.length === 0;
  findAnswersButton.disabled = requestInProgress || answerActionInProgress || !hasProjection;
  refreshButton.disabled = requestInProgress || profileActionInProgress || answerActionInProgress;
}

function makeFieldRow(field) {
  const item = document.createElement("li");
  item.className = "field-row";
  const copy = document.createElement("div");
  copy.className = "field-copy";
  const label = document.createElement("span");
  label.className = "field-label";
  label.textContent = field.label;
  const preview = document.createElement("span");
  preview.className = "field-preview";
  preview.textContent = previewProfileValue(field);
  const source = document.createElement("span");
  source.className = "field-source";
  source.textContent = field.source;
  copy.append(label, preview, source);
  const button = document.createElement("button");
  button.className = "copy-button";
  button.type = "button";
  button.textContent = "Copy";
  button.setAttribute("aria-label", `Copy ${field.label}`);
  button.title = `Copy ${field.label}`;
  button.addEventListener("click", () => copyOneProfileField(field.id, button));
  item.append(copy, button);
  return item;
}

function makeResumeRow(resume) {
  const item = document.createElement("li");
  item.className = "shelf-row";
  const copy = document.createElement("div");
  copy.className = "shelf-copy";
  const title = document.createElement("span");
  title.className = "shelf-title";
  title.textContent = resume.display_name;
  const meta = document.createElement("span");
  meta.className = "shelf-meta";
  meta.textContent = "Active local Resume Version";
  copy.append(title, meta);
  const button = document.createElement("button");
  button.className = "compact-action-button";
  button.type = "button";
  button.textContent = "Open";
  button.setAttribute("aria-label", `Open ${resume.display_name} in Resume Library`);
  button.addEventListener("click", () => openResumeVersion(resume.id, button));
  item.append(copy, button);
  return item;
}

function makeAnswerRow(answer) {
  const item = document.createElement("li");
  item.className = "shelf-row";
  const copy = document.createElement("div");
  copy.className = "shelf-copy";
  const title = document.createElement("span");
  title.className = "shelf-title";
  title.textContent = answer.question;
  const preview = document.createElement("span");
  preview.className = "shelf-preview";
  preview.textContent = answerValueText(answer);
  const meta = document.createElement("span");
  meta.className = "shelf-meta";
  meta.textContent = "Exact question · saved locally";
  copy.append(title, preview, meta);
  const actions = document.createElement("div");
  actions.className = "shelf-actions";
  const copyButton = document.createElement("button");
  copyButton.className = "compact-action-button";
  copyButton.type = "button";
  copyButton.textContent = "Copy";
  copyButton.setAttribute("aria-label", `Copy saved answer for ${answer.question}`);
  copyButton.addEventListener("click", () => copyOneAnswer(answer.id, copyButton));
  const deleteButton = document.createElement("button");
  deleteButton.className = "compact-action-button danger-action-button";
  deleteButton.type = "button";
  deleteButton.textContent = "Delete";
  deleteButton.setAttribute("aria-label", `Delete saved answer for ${answer.question}`);
  deleteButton.addEventListener("click", () => deleteAnswer(answer.id, deleteButton));
  actions.append(copyButton, deleteButton);
  item.append(copy, actions);
  return item;
}

function renderProjection(projection, message = "Review the current application page when ready.") {
  currentProjection = projection;
  clearProfileReviewState();
  clearAnswerReviewState();

  fieldList.replaceChildren();
  for (const field of projection.fields) fieldList.append(makeFieldRow(field));
  fieldList.hidden = projection.fields.length === 0;
  emptyState.hidden = projection.fields.length !== 0;
  profileFieldCount.textContent = `${projection.fields.length} ready`;
  profileCopyCount.textContent = String(projection.fields.length);
  if (projection.fields.length === 0) profileValueShelf.open = true;

  resumeList.replaceChildren();
  for (const resume of projection.resumes) resumeList.append(makeResumeRow(resume));
  resumeList.hidden = projection.resumes.length === 0;
  resumeEmptyState.hidden = projection.resumes.length !== 0;
  resumeCount.textContent = `${projection.resumes.length} ready`;

  answerList.replaceChildren();
  for (const answer of projection.answers) answerList.append(makeAnswerRow(answer));
  answerList.hidden = projection.answers.length === 0;
  answerEmptyState.hidden = projection.answers.length !== 0;
  answerCount.textContent = `${projection.answers.length} saved`;

  const readyCount = projection.fields.length + projection.resumes.length + projection.answers.length;
  setStatus(
    readyCount ? "Application assistant ready" : "No saved application data yet",
    message,
    readyCount ? "success" : "warning",
  );
  updateActionAvailability();
}

function renderError(code) {
  clearSensitiveState();
  const [title, message] = errorCopy(code);
  setStatus(title, message, "error");
  pairingSection.hidden = code !== "EXTENSION_PAIRING_REQUIRED";
  updateActionAvailability();
}

// The code goes only to the worker; the worker alone talks to Career Hub and
// keeps the resulting credential. Nothing here stores or sees the credential.
async function pairExtension(event) {
  event.preventDefault();
  const code = pairingCodeInput.value.trim();
  if (!PAIRING_CODE_PATTERN.test(code)) {
    setPairingStatus("Check the code", "Type the eight characters shown in Career Hub Settings.", "warning");
    return;
  }
  pairingSubmitButton.disabled = true;
  setPairingStatus("Pairing…", "Checking the code with Career Hub.");
  try {
    await requestExtension({ command: PAIR_EXTENSION_COMMAND, code });
    pairingCodeInput.value = "";
    pairingSection.hidden = true;
    await loadAssistant();
  } catch (error) {
    const [title, message] = errorCopy(error instanceof Error ? error.message : "APP_UNAVAILABLE");
    setPairingStatus(title, message, "error");
  } finally {
    pairingSubmitButton.disabled = false;
  }
}

function profileMappingState(mapping) {
  if (mapping.selectable) return "Empty · selected";
  if (mapping.skip_reason === "non_empty") return "Skipped · already contains a value";
  return "Skipped · multiple possible targets";
}

function answerMappingState(mapping) {
  if (mapping.selectable) return "Empty · selected";
  if (mapping.skip_reason === "non_empty") return "Skipped · already contains a value";
  if (mapping.skip_reason === "incompatible") return "Skipped · answer type does not fit";
  return "Skipped · multiple possible targets";
}

function makeMappingRow(mapping, kind) {
  const answerMode = kind === "answer";
  const stateCopy = answerMode ? answerMappingState(mapping) : profileMappingState(mapping);
  const displayName = answerMode ? mapping.question : mapping.field_label;
  const item = document.createElement("li");
  item.className = "mapping-row";
  item.dataset.selectable = String(mapping.selectable);
  const checkbox = document.createElement("input");
  checkbox.className = answerMode ? "answer-mapping-checkbox" : "mapping-checkbox";
  checkbox.type = "checkbox";
  checkbox.checked = mapping.selectable;
  checkbox.disabled = !mapping.selectable;
  checkbox.value = mapping.mapping_id;
  checkbox.setAttribute(
    "aria-label",
    mapping.selectable
      ? `Fill ${displayName} into ${mapping.target_label}`
      : `${displayName} cannot be filled: ${stateCopy}`,
  );
  checkbox.addEventListener(
    "change",
    answerMode ? updateAnswerFillSelection : updateProfileFillSelection,
  );
  const copy = document.createElement("div");
  copy.className = "mapping-copy";
  const title = document.createElement("span");
  title.className = "mapping-title";
  title.textContent = displayName;
  const target = document.createElement("span");
  target.className = "mapping-target";
  target.textContent = `Page field · ${mapping.target_label}`;
  const state = document.createElement("span");
  state.className = "mapping-state";
  state.textContent = stateCopy;
  copy.append(title, target, state);
  item.append(checkbox, copy);
  return item;
}

function updateProfileFillSelection() {
  if (!currentProfileReview || profileActionInProgress) {
    fillSelectedButton.disabled = true;
    return;
  }
  const count = mappingList.querySelectorAll(".mapping-checkbox:checked:not(:disabled)").length;
  fillSelectedButton.disabled = count === 0;
  fillSelectedButton.textContent = count === 1
    ? "Fill 1 selected field"
    : `Fill ${count} selected fields`;
}

function updateAnswerFillSelection() {
  if (!currentAnswerReview || answerActionInProgress) {
    fillAnswersButton.disabled = true;
    return;
  }
  const count = answerMappingList.querySelectorAll(
    ".answer-mapping-checkbox:checked:not(:disabled)",
  ).length;
  fillAnswersButton.disabled = count === 0;
  fillAnswersButton.textContent = count === 1
    ? "Fill 1 selected answer"
    : `Fill ${count} selected answers`;
}

function renderProfileReview(review) {
  currentProfileReview = review;
  mappingList.replaceChildren();
  for (const mapping of review.mappings) mappingList.append(makeMappingRow(mapping, "profile"));
  mappingList.hidden = review.mappings.length === 0;
  const selectableCount = review.mappings.filter((mapping) => mapping.selectable).length;
  fillSelectedButton.hidden = selectableCount === 0;
  if (selectableCount) {
    setPrefillStatus(
      `${selectableCount} ${selectableCount === 1 ? "field" : "fields"} ready for review`,
      `Supported controls on ${review.page_host} were inspected. Nothing has been filled.`,
      "success",
    );
  } else if (review.mappings.length) {
    setPrefillStatus(
      "Nothing safe to fill",
      "Matched fields were non-empty or ambiguous. One-field copy remains available.",
      "warning",
    );
  } else {
    setPrefillStatus(
      "No supported profile fields found",
      `No enabled value matched a visible supported empty control on ${review.page_host}.`,
      "warning",
    );
  }
  updateProfileFillSelection();
}

function renderAnswerReview(review) {
  currentAnswerReview = review;
  currentCandidateReview = null;
  answerCandidateList.replaceChildren();
  answerCandidateList.hidden = true;
  answerMappingList.replaceChildren();
  for (const mapping of review.mappings) answerMappingList.append(makeMappingRow(mapping, "answer"));
  answerMappingList.hidden = review.mappings.length === 0;
  const selectableCount = review.mappings.filter((mapping) => mapping.selectable).length;
  fillAnswersButton.hidden = selectableCount === 0;
  if (selectableCount) {
    setAnswerStatus(
      `${selectableCount} saved ${selectableCount === 1 ? "answer" : "answers"} ready`,
      `Exact questions on ${review.page_host} matched supported empty controls. Nothing has been filled.`,
      "success",
    );
  } else if (review.mappings.length) {
    setAnswerStatus(
      "No saved answer is fillable",
      "Matches were non-empty, ambiguous, or incompatible. Nothing was changed.",
      "warning",
    );
  } else {
    setAnswerStatus(
      "No exact saved questions found",
      `No saved question matched a supported control on ${review.page_host}.`,
      "warning",
    );
  }
  updateAnswerFillSelection();
}

function makeCandidateRow(candidate) {
  const item = document.createElement("li");
  item.className = "candidate-row";
  const copy = document.createElement("div");
  copy.className = "candidate-copy";
  const question = document.createElement("span");
  question.className = "candidate-question";
  question.textContent = candidate.question;
  const value = document.createElement("span");
  value.className = "candidate-value";
  value.textContent = answerValueText(candidate);
  copy.append(question, value);
  const button = document.createElement("button");
  button.className = "compact-action-button";
  button.type = "button";
  button.textContent = "Save";
  button.setAttribute("aria-label", `Save reusable answer for ${candidate.question}`);
  button.addEventListener("click", () => saveCandidateAnswer(candidate.candidate_id, button));
  item.append(copy, button);
  return item;
}

function renderCandidates(review) {
  currentCandidateReview = review;
  currentAnswerReview = null;
  answerMappingList.replaceChildren();
  answerMappingList.hidden = true;
  fillAnswersButton.hidden = true;
  answerCandidateList.replaceChildren();
  for (const candidate of review.candidates) answerCandidateList.append(makeCandidateRow(candidate));
  answerCandidateList.hidden = review.candidates.length === 0;
  if (review.candidates.length) {
    setAnswerStatus(
      `${review.candidates.length} completed ${review.candidates.length === 1 ? "answer" : "answers"} found`,
      `Review each value from ${review.page_host}. Saving one creates or updates only that exact question.`,
      "success",
    );
  } else {
    setAnswerStatus(
      "No new reusable answers found",
      "Only supported completed short questions were checked. Existing, sensitive, job-specific, and open-ended answers were excluded.",
      "warning",
    );
  }
}

async function loadAssistant() {
  if (requestInProgress) return;
  requestInProgress = true;
  clearSensitiveState();
  setStatus("Loading application assistant", "Checking the local Career Hub.", "neutral", true);
  updateActionAvailability();
  try {
    renderProjection(await requestProjection());
    pairingSection.hidden = true;
  } catch (error) {
    renderError(error instanceof Error ? error.message : "APP_UNAVAILABLE");
  } finally {
    requestInProgress = false;
    updateActionAvailability();
  }
}

async function copyOneProfileField(fieldId, button) {
  if (requestInProgress || !currentProjection) return;
  const displayed = currentProjection.fields.find((field) => field.id === fieldId);
  if (!displayed) return;
  requestInProgress = true;
  button.disabled = true;
  copyAnnouncement.textContent = "Checking for updates…";
  updateActionAvailability();
  try {
    const fresh = await requestProjection();
    const field = fresh.fields.find((item) => item.id === fieldId);
    if (!field || field.value_kind !== displayed.value_kind || field.value !== displayed.value) {
      renderProjection(fresh, "Career Hub data changed. Review the refreshed values before copying.");
      statusPanel.dataset.tone = "warning";
      copyAnnouncement.textContent = "Nothing was copied because the displayed value was stale.";
      return;
    }
    if (!navigator.clipboard || typeof navigator.clipboard.writeText !== "function") {
      throw new Error("CLIPBOARD_UNAVAILABLE");
    }
    await navigator.clipboard.writeText(clipboardValue(field));
    copyAnnouncement.textContent = `${field.label} copied.`;
    showCopiedButton(button);
  } catch (error) {
    if (error instanceof Error && error.message === "CLIPBOARD_UNAVAILABLE") {
      copyAnnouncement.textContent = "Clipboard access failed. Keep the panel open and try again.";
    } else {
      renderError(error instanceof Error ? error.message : "APP_UNAVAILABLE");
    }
  } finally {
    requestInProgress = false;
    if (button.isConnected) button.disabled = false;
    updateActionAvailability();
  }
}

function showCopiedButton(button) {
  if (copiedResetTimer !== null) clearTimeout(copiedResetTimer);
  button.textContent = "Copied";
  button.dataset.copied = "true";
  copiedResetTimer = setTimeout(() => {
    if (button.isConnected) {
      button.textContent = "Copy";
      button.removeAttribute("data-copied");
    }
    copiedResetTimer = null;
  }, 1600);
}

async function reviewProfileFields() {
  if (profileActionInProgress || requestInProgress || reviewFieldsButton.disabled) return;
  profileActionInProgress = true;
  clearProfileReviewState(false);
  setPrefillStatus("Inspecting visible fields", "Reading supported labels and empty state in the active main frame.");
  updateActionAvailability();
  try {
    renderProfileReview(await requestExtension({ command: REVIEW_HELPER_FORM_COMMAND }));
  } catch (error) {
    const [title, message] = prefillErrorCopy(error instanceof Error ? error.message : "APP_UNAVAILABLE");
    clearProfileReviewState(false);
    setPrefillStatus(title, message, "error");
  } finally {
    profileActionInProgress = false;
    updateProfileFillSelection();
    updateActionAvailability();
  }
}

function selectedProfileMappingIds() {
  return Array.from(mappingList.querySelectorAll(".mapping-checkbox:checked:not(:disabled)"))
    .map((checkbox) => checkbox.value);
}

async function fillProfileFields() {
  if (profileActionInProgress || requestInProgress || !currentProfileReview) return;
  const mappingIds = selectedProfileMappingIds();
  if (!mappingIds.length) return;
  const reviewToken = currentProfileReview.review_token;
  profileActionInProgress = true;
  fillSelectedButton.disabled = true;
  setPrefillStatus("Checking reviewed fields", "Nothing is written until the review is revalidated.");
  updateActionAvailability();
  try {
    const result = await requestExtension({
      command: FILL_HELPER_FIELDS_COMMAND,
      review_token: reviewToken,
      mapping_ids: mappingIds,
    });
    clearProfileReviewState(false);
    setPrefillStatus(
      `${result.filled_count} ${result.filled_count === 1 ? "field" : "fields"} filled`,
      "Values were inserted into the reviewed empty controls. No page action was clicked.",
      "success",
    );
  } catch (error) {
    const code = error instanceof Error ? error.message : "APP_UNAVAILABLE";
    if (code === "APPLICATION_HELPER_DATA_CHANGED") await loadAssistant();
    const [title, message] = prefillErrorCopy(code);
    clearProfileReviewState(false);
    setPrefillStatus(title, message, "error");
  } finally {
    profileActionInProgress = false;
    updateProfileFillSelection();
    updateActionAvailability();
  }
}

async function openResumeVersion(resumeId, button) {
  if (requestInProgress || answerActionInProgress || profileActionInProgress) return;
  button.disabled = true;
  try {
    await requestExtension({ command: OPEN_HELPER_RESUME_COMMAND, resume_id: resumeId });
    setStatus("Resume Library opened", "The exact Resume Version is focused there for download.", "success");
  } catch (error) {
    const code = error instanceof Error ? error.message : "APP_UNAVAILABLE";
    if (code === "APPLICATION_HELPER_RESUME_UNAVAILABLE") {
      setStatus("Resume is no longer active", "Refresh the assistant and choose an available Resume Version.", "warning");
    } else {
      const [title, message] = errorCopy(code);
      setStatus(title, message, "error");
    }
  } finally {
    if (button.isConnected) button.disabled = false;
  }
}

async function copyOneAnswer(answerId, button) {
  if (requestInProgress || answerActionInProgress || !currentProjection) return;
  const displayedProjection = currentProjection;
  const displayed = displayedProjection.answers.find((answer) => answer.id === answerId);
  if (!displayed) return;
  answerActionInProgress = true;
  button.disabled = true;
  setAnswerStatus("Checking saved answer", "Nothing is copied until the current local value is revalidated.");
  updateActionAvailability();
  try {
    const fresh = await requestProjection();
    const answer = fresh.answers.find((item) => item.id === answerId);
    if (
      fresh.answer_revision !== displayedProjection.answer_revision
      || !answer
      || answer.value_kind !== displayed.value_kind
      || answer.value !== displayed.value
    ) {
      renderProjection(fresh, "Saved answers changed. Review the refreshed values.");
      setAnswerStatus("Saved answer changed", "Nothing was copied because the displayed answer was stale.", "warning");
      return;
    }
    if (!navigator.clipboard || typeof navigator.clipboard.writeText !== "function") {
      throw new Error("CLIPBOARD_UNAVAILABLE");
    }
    await navigator.clipboard.writeText(clipboardValue(answer));
    setAnswerStatus("Answer copied", `Copied the saved answer for “${safeText(answer.question, 70)}”.`, "success");
    showCopiedButton(button);
  } catch (error) {
    if (error instanceof Error && error.message === "CLIPBOARD_UNAVAILABLE") {
      setAnswerStatus("Clipboard unavailable", "Keep the panel open and try the Copy action again.", "error");
    } else {
      const [title, message] = answerErrorCopy(error instanceof Error ? error.message : "APP_UNAVAILABLE");
      setAnswerStatus(title, message, "error");
    }
  } finally {
    answerActionInProgress = false;
    if (button.isConnected) button.disabled = false;
    updateActionAvailability();
  }
}

async function deleteAnswer(answerId, button) {
  if (requestInProgress || answerActionInProgress || !currentProjection) return;
  const answer = currentProjection.answers.find((item) => item.id === answerId);
  if (!answer) return;
  if (!window.confirm(`Delete this reusable answer?\n\n${safeText(answer.question, 140)}`)) return;
  const expectedRevision = currentProjection.answer_revision;
  answerActionInProgress = true;
  button.disabled = true;
  setAnswerStatus("Deleting saved answer", "Checking the current local revision first.");
  updateActionAvailability();
  try {
    const projection = await requestExtension({
      command: DELETE_REUSABLE_ANSWER_COMMAND,
      answer_id: answerId,
      answer_revision: expectedRevision,
    });
    renderProjection(projection, "Reusable answer deleted.");
    setAnswerStatus("Reusable answer deleted", "The exact saved question and value were removed locally.", "success");
  } catch (error) {
    const code = error instanceof Error ? error.message : "APP_UNAVAILABLE";
    if (code === "APPLICATION_ANSWER_DATA_CHANGED") await loadAssistant();
    const [title, message] = answerErrorCopy(code);
    setAnswerStatus(title, message, "error");
  } finally {
    answerActionInProgress = false;
    if (button.isConnected) button.disabled = false;
    updateActionAvailability();
  }
}

async function reviewSavedAnswers() {
  if (answerActionInProgress || requestInProgress || reviewAnswersButton.disabled) return;
  answerActionInProgress = true;
  clearAnswerReviewState(false);
  setAnswerStatus("Inspecting exact saved questions", "Reading supported labels and empty state in the active main frame.");
  updateActionAvailability();
  try {
    renderAnswerReview(await requestExtension({ command: REVIEW_SAVED_ANSWERS_COMMAND }));
  } catch (error) {
    const [title, message] = answerErrorCopy(error instanceof Error ? error.message : "APP_UNAVAILABLE");
    clearAnswerReviewState(false);
    setAnswerStatus(title, message, "error");
  } finally {
    answerActionInProgress = false;
    updateAnswerFillSelection();
    updateActionAvailability();
  }
}

function selectedAnswerMappingIds() {
  return Array.from(answerMappingList.querySelectorAll(
    ".answer-mapping-checkbox:checked:not(:disabled)",
  )).map((checkbox) => checkbox.value);
}

async function fillSavedAnswers() {
  if (answerActionInProgress || requestInProgress || !currentAnswerReview) return;
  const mappingIds = selectedAnswerMappingIds();
  if (!mappingIds.length) return;
  const reviewToken = currentAnswerReview.review_token;
  answerActionInProgress = true;
  fillAnswersButton.disabled = true;
  setAnswerStatus("Checking reviewed answers", "Nothing is written until every selected target is revalidated.");
  updateActionAvailability();
  try {
    const result = await requestExtension({
      command: FILL_SAVED_ANSWERS_COMMAND,
      review_token: reviewToken,
      mapping_ids: mappingIds,
    });
    clearAnswerReviewState(false);
    setAnswerStatus(
      `${result.filled_count} saved ${result.filled_count === 1 ? "answer" : "answers"} filled`,
      "Exact saved values were inserted into reviewed empty controls. No page action was clicked.",
      "success",
    );
  } catch (error) {
    const code = error instanceof Error ? error.message : "APP_UNAVAILABLE";
    if (code === "APPLICATION_ANSWER_DATA_CHANGED") await loadAssistant();
    const [title, message] = answerErrorCopy(code);
    clearAnswerReviewState(false);
    setAnswerStatus(title, message, "error");
  } finally {
    answerActionInProgress = false;
    updateAnswerFillSelection();
    updateActionAvailability();
  }
}

async function findCompletedAnswers() {
  if (answerActionInProgress || requestInProgress || findAnswersButton.disabled) return;
  answerActionInProgress = true;
  clearAnswerReviewState(false);
  setAnswerStatus(
    "Finding completed safe answers",
    "Reading only supported short text, select, and radio answers in the active main frame.",
  );
  updateActionAvailability();
  try {
    renderCandidates(await requestExtension({ command: FIND_REUSABLE_ANSWERS_COMMAND }));
  } catch (error) {
    const [title, message] = answerErrorCopy(error instanceof Error ? error.message : "APP_UNAVAILABLE");
    clearAnswerReviewState(false);
    setAnswerStatus(title, message, "error");
  } finally {
    answerActionInProgress = false;
    updateActionAvailability();
  }
}

async function saveCandidateAnswer(candidateId, button) {
  if (answerActionInProgress || requestInProgress || !currentCandidateReview) return;
  if (!currentCandidateReview.candidates.some((candidate) => candidate.candidate_id === candidateId)) return;
  const reviewToken = currentCandidateReview.review_token;
  answerActionInProgress = true;
  button.disabled = true;
  setAnswerStatus("Rechecking completed answer", "The current page value and local answer revision must still match.");
  updateActionAvailability();
  try {
    const projection = await requestExtension({
      command: SAVE_REUSABLE_ANSWER_COMMAND,
      review_token: reviewToken,
      candidate_id: candidateId,
    });
    renderProjection(projection, "Reusable answer saved locally.");
    setAnswerStatus(
      "Reusable answer saved",
      "Find completed answers again to review another value from this page.",
      "success",
    );
  } catch (error) {
    const code = error instanceof Error ? error.message : "APP_UNAVAILABLE";
    clearAnswerReviewState(false);
    if (code === "APPLICATION_ANSWER_DATA_CHANGED") await loadAssistant();
    const [title, message] = answerErrorCopy(code);
    setAnswerStatus(title, message, "error");
  } finally {
    answerActionInProgress = false;
    if (button.isConnected) button.disabled = false;
    updateActionAvailability();
  }
}

async function openSettings() {
  settingsButton.disabled = true;
  try {
    const result = await extensionApi.runtime.sendMessage({ command: OPEN_HELPER_SETTINGS_COMMAND });
    if (!result || result.ok !== true) {
      setStatus("Career Hub Profile could not be opened", "Keep the local app running and try again.", "error");
    }
  } catch {
    setStatus("Career Hub Profile could not be opened", "Keep the local app running and try again.", "error");
  } finally {
    settingsButton.disabled = false;
  }
}

refreshButton.addEventListener("click", loadAssistant);
settingsButton.addEventListener("click", openSettings);
reviewFieldsButton.addEventListener("click", reviewProfileFields);
fillSelectedButton.addEventListener("click", fillProfileFields);
reviewAnswersButton.addEventListener("click", reviewSavedAnswers);
fillAnswersButton.addEventListener("click", fillSavedAnswers);
findAnswersButton.addEventListener("click", findCompletedAnswers);
pairingForm.addEventListener("submit", pairExtension);
window.addEventListener("pagehide", clearSensitiveState);
loadAssistant();
