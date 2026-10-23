"use strict";

if (typeof CareerHubBrowser === "undefined" && typeof importScripts === "function") {
  importScripts(
    "browser-api.js",
    "helper-catalog.js",
    "page-extractor.js",
    "application-session-observer.js",
    "application-form-helper.js",
    "application-answer-helper.js",
  );
}

const extensionApi = CareerHubBrowser.api;

const APP_ORIGIN = "http://127.0.0.1:8766";
const SESSION_PATH = "/api/session";
const CAPTURE_PATH = "/api/discoveries/captures";
const APPLICATION_CLAIM_PATH = "/api/application-sessions/claim";
const APPLICATION_SIGNAL_PATH = "/api/application-sessions/submission-signal";
const APPLICATION_INTERRUPT_PATH = "/api/application-sessions/interrupt";
const APPLICATION_HELPER_SESSION_PATH = "/api/application-helper/session";
const APPLICATION_HELPER_FIELDS_PATH = "/api/application-helper/fields";
const APPLICATION_HELPER_ANSWER_SAVE_PATH = "/api/application-helper/answers/save";
const APPLICATION_HELPER_ANSWER_DELETE_PATH = "/api/application-helper/answers/delete";
const INSPECT_COMMAND = "career-hub.inspect-active-tab.v1";
const CAPTURE_COMMAND = "career-hub.capture-active-tab.v2";
const OPEN_REVIEW_COMMAND = "career-hub.open-discovery-review.v1";
const PREPARE_APPLICATION_COMMAND = "career-hub.prepare-application.v1";
const ARM_APPLICATION_COMMAND = "career-hub.arm-application-session.v1";
const OPEN_APPLICATIONS_COMMAND = "career-hub.open-applications.v1";
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
const APPLICATION_CONTENT_COMMAND = "career-hub.application-session.content-signal.v1";
const PAIR_EXTENSION_COMMAND = "career-hub.extension.pair.v1";
const PAIRING_STATUS_COMMAND = "career-hub.extension.pairing-status.v1";
const CONNECTION_STATUS_COMMAND = "career-hub.extension.connection-status.v1";
const OPEN_APP_COMMAND = "career-hub.open-app.v1";
const EXTENSION_PAIR_PATH = "/api/extension/pair";
const EXTENSION_CREDENTIAL_HEADER = "X-Career-Hub-Extension-Credential";
const CREDENTIAL_STORAGE_KEY = "careerHubExtensionCredential.v1";
const CREDENTIAL_PATTERN = /^[A-Za-z0-9_-]{43}$/;
const PAIRING_CODE_PATTERN = /^[A-Za-z0-9]{4}-?[A-Za-z0-9]{4}$/;
const CONTRACT_VERSION = 1;
const HELPER_CONTRACT_VERSION = 2;
const FETCH_TIMEOUT_MS = 6000;
const MAX_URL_CHARS = 4096;
const MAX_TITLE_CHARS = 500;
const MAX_SOURCE_CHARS = 500000;
const MIN_PAGE_CHARS = 160;
const MAX_DOM_NODES = 50000;
const MAX_JSONLD_CHARS = 1000000;
const CAPTURE_MODES = new Set(["link", "selection", "page"]);
const RECEIPT_STATUSES = new Set([
  "unavailable",
  "no_clear_conflict",
  "review",
  "clear_conflict",
]);
const RECEIPT_AVAILABILITY = new Set([
  "available",
  "partial_capture",
  "candidate_context_unavailable",
]);
const MAX_RECEIPT_FINDINGS = 3;
const MAX_RECEIPT_TEXT_CHARS = 500;
const DISCOVERY_ID_PATTERN = /^discovery_[a-f0-9]{32}$/;
const APPLICATION_SESSION_ID_PATTERN = /^application_session_[a-f0-9]{32}$/;
const APPLICATION_SESSION_STORAGE_KEY_PREFIX = "application_session_by_tab_v2:";
const APPLICATION_SUCCESS_RULES = new Set([
  "application_submitted",
  "application_received",
  "successfully_applied",
  "thank_you_for_applying",
]);
const APPLICATION_SIGNAL_WINDOW_MS = 15 * 60 * 1000;
const HELPER_REVISION_PATTERN = /^sha256:[a-f0-9]{64}$/;
const MAX_HELPER_TEXT_CHARS = 2048;
const MAX_HELPER_FORM_CONTROLS = 240;
const MAX_HELPER_PREFILL_FIELDS = 28;
const HELPER_PREFILL_REVIEW_MS = 10 * 60 * 1000;
const MAX_HELPER_PREFILL_REVIEWS = 8;
const MAX_HELPER_ANSWERS = 200;
const MAX_HELPER_ANSWER_CHARS = 500;
const MAX_HELPER_QUESTION_CHARS = 300;
const MAX_HELPER_RESUMES = 50;
const MAX_HELPER_RESUME_NAME_CHARS = 240;
const MAX_HELPER_ANSWER_MAPPINGS = 30;
const HELPER_REVIEW_TOKEN_PATTERN = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/i;
const HELPER_MAPPING_ID_PATTERN = /^mapping_(?:[1-9]|1\d|2[0-3])$/;
const HELPER_ANSWER_MAPPING_ID_PATTERN = /^answer_mapping_(?:[1-9]|[12]\d|30)$/;
const HELPER_ANSWER_CANDIDATE_ID_PATTERN = /^candidate_(?:[1-9]|[12]\d|30)$/;
const HELPER_ANSWER_ID_PATTERN = /^answer_[a-f0-9]{32}$/;
const HELPER_RESUME_ID_PATTERN = /^resume_[a-f0-9]{32}$/;
const HELPER_FIELD_CATALOG = CareerHubHelperCatalog.catalog;
const HELPER_PREFILL_FIELD_IDS = CareerHubHelperCatalog.prefillFieldIds;
// One-shot review records and the per-tab review target live in WebExtensions
// storage.session, which is memory-backed and never written to disk, so they
// survive the browser's idle termination of this worker. No profile value or
// saved answer value is stored there; candidate values are kept as a digest.
const HELPER_REVIEW_STORAGE_PREFIX = "application_helper_review_v1:";
const HELPER_REVIEW_KINDS = new Set(["prefill", "answers", "candidates"]);
const DISCOVERY_REVIEW_STORAGE_PREFIX = "discovery_review_by_tab_v1:";
const submissionSignalByTab = new Map();

class CaptureClientError extends Error {
  constructor(code) {
    super(code);
    this.name = "CaptureClientError";
    this.code = code;
  }
}

function isCaptureCommand(message) {
  return Boolean(
    message
    && typeof message === "object"
    && !Array.isArray(message)
    && Object.keys(message).length === 2
    && message.command === CAPTURE_COMMAND
    && typeof message.mode === "string"
    && CAPTURE_MODES.has(message.mode)
  );
}

function isInspectCommand(message) {
  return Boolean(
    message
    && typeof message === "object"
    && !Array.isArray(message)
    && Object.keys(message).length === 1
    && message.command === INSPECT_COMMAND
  );
}

function isOpenReviewCommand(message) {
  return Boolean(
    message
    && typeof message === "object"
    && !Array.isArray(message)
    && Object.keys(message).length === 1
    && message.command === OPEN_REVIEW_COMMAND
  );
}

function isPrepareApplicationCommand(message) {
  return Boolean(
    message
    && typeof message === "object"
    && !Array.isArray(message)
    && Object.keys(message).length === 1
    && message.command === PREPARE_APPLICATION_COMMAND
  );
}

function isArmApplicationCommand(message) {
  return Boolean(
    message
    && typeof message === "object"
    && !Array.isArray(message)
    && Object.keys(message).length === 1
    && message.command === ARM_APPLICATION_COMMAND
  );
}

function isOpenApplicationsCommand(message) {
  return Boolean(
    message
    && typeof message === "object"
    && !Array.isArray(message)
    && Object.keys(message).length === 1
    && message.command === OPEN_APPLICATIONS_COMMAND
  );
}

function isLoadHelperFieldsCommand(message) {
  return Boolean(
    message
    && typeof message === "object"
    && !Array.isArray(message)
    && Object.keys(message).length === 1
    && message.command === LOAD_HELPER_FIELDS_COMMAND
  );
}

function isOpenHelperSettingsCommand(message) {
  return Boolean(
    message
    && typeof message === "object"
    && !Array.isArray(message)
    && Object.keys(message).length === 1
    && message.command === OPEN_HELPER_SETTINGS_COMMAND
  );
}

function isReviewHelperFormCommand(message) {
  return Boolean(
    message
    && typeof message === "object"
    && !Array.isArray(message)
    && Object.keys(message).length === 1
    && message.command === REVIEW_HELPER_FORM_COMMAND
  );
}

function isFillHelperFieldsCommand(message) {
  if (
    !message
    || typeof message !== "object"
    || Array.isArray(message)
    || !exactKeys(message, ["command", "review_token", "mapping_ids"])
    || message.command !== FILL_HELPER_FIELDS_COMMAND
    || typeof message.review_token !== "string"
    || !HELPER_REVIEW_TOKEN_PATTERN.test(message.review_token)
    || !Array.isArray(message.mapping_ids)
    || message.mapping_ids.length === 0
    || message.mapping_ids.length > MAX_HELPER_PREFILL_FIELDS
  ) {
    return false;
  }
  const unique = new Set(message.mapping_ids);
  return unique.size === message.mapping_ids.length
    && message.mapping_ids.every((value) => (
      typeof value === "string" && HELPER_MAPPING_ID_PATTERN.test(value)
    ));
}

function isOpenHelperResumeCommand(message) {
  return Boolean(
    message
    && typeof message === "object"
    && !Array.isArray(message)
    && exactKeys(message, ["command", "resume_id"])
    && message.command === OPEN_HELPER_RESUME_COMMAND
    && typeof message.resume_id === "string"
    && HELPER_RESUME_ID_PATTERN.test(message.resume_id)
  );
}

function isReviewSavedAnswersCommand(message) {
  return Boolean(
    message
    && typeof message === "object"
    && !Array.isArray(message)
    && exactKeys(message, ["command"])
    && message.command === REVIEW_SAVED_ANSWERS_COMMAND
  );
}

function isFillSavedAnswersCommand(message) {
  if (
    !message
    || typeof message !== "object"
    || Array.isArray(message)
    || !exactKeys(message, ["command", "review_token", "mapping_ids"])
    || message.command !== FILL_SAVED_ANSWERS_COMMAND
    || typeof message.review_token !== "string"
    || !HELPER_REVIEW_TOKEN_PATTERN.test(message.review_token)
    || !Array.isArray(message.mapping_ids)
    || message.mapping_ids.length === 0
    || message.mapping_ids.length > MAX_HELPER_ANSWER_MAPPINGS
  ) return false;
  const unique = new Set(message.mapping_ids);
  return unique.size === message.mapping_ids.length
    && message.mapping_ids.every((mappingId) => (
      typeof mappingId === "string"
      && HELPER_ANSWER_MAPPING_ID_PATTERN.test(mappingId)
    ));
}

function isFindReusableAnswersCommand(message) {
  return Boolean(
    message
    && typeof message === "object"
    && !Array.isArray(message)
    && exactKeys(message, ["command"])
    && message.command === FIND_REUSABLE_ANSWERS_COMMAND
  );
}

function isSaveReusableAnswerCommand(message) {
  return Boolean(
    message
    && typeof message === "object"
    && !Array.isArray(message)
    && exactKeys(message, ["command", "review_token", "candidate_id"])
    && message.command === SAVE_REUSABLE_ANSWER_COMMAND
    && typeof message.review_token === "string"
    && HELPER_REVIEW_TOKEN_PATTERN.test(message.review_token)
    && typeof message.candidate_id === "string"
    && HELPER_ANSWER_CANDIDATE_ID_PATTERN.test(message.candidate_id)
  );
}

function isDeleteReusableAnswerCommand(message) {
  return Boolean(
    message
    && typeof message === "object"
    && !Array.isArray(message)
    && exactKeys(message, ["command", "answer_id", "answer_revision"])
    && message.command === DELETE_REUSABLE_ANSWER_COMMAND
    && typeof message.answer_id === "string"
    && HELPER_ANSWER_ID_PATTERN.test(message.answer_id)
    && typeof message.answer_revision === "string"
    && HELPER_REVISION_PATTERN.test(message.answer_revision)
  );
}

function isApplicationContentSignal(message, sender) {
  if (
    !message
    || typeof message !== "object"
    || Array.isArray(message)
    || message.command !== APPLICATION_CONTENT_COMMAND
    || sender.id !== extensionApi.runtime.id
    || typeof sender.tab?.id !== "number"
  ) {
    return false;
  }
  const expected = ["native_submit", "final_action_click"].includes(message.signal)
    ? ["command", "session_id", "page_origin", "signal", "event_key", "observed_at"]
    : message.signal === "success_state"
      ? ["command", "session_id", "page_origin", "signal", "success_rule", "observed_at"]
      : [];
  return expected.length > 0
    && Object.keys(message).length === expected.length
    && expected.every((key) => Object.hasOwn(message, key));
}

async function fetchWithTimeout(path, options = {}) {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), FETCH_TIMEOUT_MS);
  try {
    return await fetch(`${APP_ORIGIN}${path}`, {
      ...options,
      cache: "no-store",
      credentials: "omit",
      redirect: "error",
      signal: controller.signal,
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") {
      throw new CaptureClientError("APP_TIMEOUT");
    }
    throw new CaptureClientError("APP_UNAVAILABLE");
  } finally {
    clearTimeout(timeout);
  }
}

async function readJson(response) {
  let payload;
  try {
    payload = await response.json();
  } catch {
    throw new CaptureClientError("APP_RESPONSE_INVALID");
  }
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
    throw new CaptureClientError("APP_RESPONSE_INVALID");
  }
  if (!response.ok) {
    const code = typeof payload.error_code === "string"
      ? payload.error_code
      : "APP_RESPONSE_INVALID";
    throw new CaptureClientError(code);
  }
  return payload;
}

function helperTargetTabId(sender) {
  if (typeof sender?.url !== "string") return null;
  let senderUrl;
  let helperUrl;
  try {
    senderUrl = new URL(sender.url);
    helperUrl = new URL(extensionApi.runtime.getURL("sidepanel.html"));
  } catch {
    throw new CaptureClientError("ACTIVE_TAB_UNSUPPORTED");
  }
  if (
    senderUrl.protocol !== helperUrl.protocol
    || senderUrl.host !== helperUrl.host
    || senderUrl.pathname !== helperUrl.pathname
  ) return null;
  if (!senderUrl.search && !senderUrl.hash) return null;
  const keys = Array.from(senderUrl.searchParams.keys());
  const rawTarget = senderUrl.searchParams.get("target_tab_id");
  if (
    senderUrl.hash
    || keys.length !== 1
    || keys[0] !== "target_tab_id"
    || typeof rawTarget !== "string"
    || !/^(?:0|[1-9]\d*)$/.test(rawTarget)
  ) throw new CaptureClientError("ACTIVE_TAB_UNSUPPORTED");
  const targetTabId = Number(rawTarget);
  if (!Number.isSafeInteger(targetTabId) || targetTabId < 0) {
    throw new CaptureClientError("ACTIVE_TAB_UNSUPPORTED");
  }
  return targetTabId;
}

async function getActiveHttpTab(sender = null) {
  const targetTabId = helperTargetTabId(sender);
  let tab;
  if (targetTabId === null) {
    [tab] = await extensionApi.tabs.query({ active: true, currentWindow: true });
  } else {
    try {
      tab = await extensionApi.tabs.get(targetTabId);
    } catch {
      throw new CaptureClientError("ACTIVE_TAB_UNAVAILABLE");
    }
    if (
      typeof sender?.tab?.id !== "number"
      || sender.tab.id === targetTabId
      || typeof sender.tab.windowId !== "number"
      || tab?.windowId !== sender.tab.windowId
    ) throw new CaptureClientError("ACTIVE_TAB_UNAVAILABLE");
  }
  if (!tab || typeof tab.url !== "string") {
    throw new CaptureClientError("ACTIVE_TAB_UNAVAILABLE");
  }
  if (tab.url.length > MAX_URL_CHARS) {
    throw new CaptureClientError("ACTIVE_TAB_UNSUPPORTED");
  }

  let parsed;
  try {
    parsed = new URL(tab.url);
  } catch {
    throw new CaptureClientError("ACTIVE_TAB_UNSUPPORTED");
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw new CaptureClientError("ACTIVE_TAB_UNSUPPORTED");
  }

  const title = typeof tab.title === "string"
    ? tab.title.replace(/\s+/g, " ").trim().slice(0, MAX_TITLE_CHARS)
    : "";
  if (typeof tab.id !== "number") {
    throw new CaptureClientError("ACTIVE_TAB_UNAVAILABLE");
  }
  return { id: tab.id, title, url: tab.url };
}

async function executeMainFrame(tabId, func, args) {
  let results;
  try {
    results = await extensionApi.scripting.executeScript({
      target: { tabId, allFrames: false },
      func,
      args,
    });
  } catch {
    throw new CaptureClientError("PAGE_ACCESS_UNAVAILABLE");
  }
  if (
    !Array.isArray(results)
    || results.length !== 1
    || results[0].frameId !== 0
    || !results[0].result
    || typeof results[0].result !== "object"
  ) {
    throw new CaptureClientError("EXTRACTION_FAILED");
  }
  return results[0].result;
}

function pageOrigin(url) {
  try {
    const parsed = new URL(url);
    return ["http:", "https:"].includes(parsed.protocol) ? parsed.origin : "";
  } catch {
    return "";
  }
}

function validStoredApplicationSession(value, tabId) {
  return Boolean(
    value
    && typeof value === "object"
    && !Array.isArray(value)
    && APPLICATION_SESSION_ID_PATTERN.test(value.session_id)
    && value.tab_id === tabId
    && typeof value.origin === "string"
    && /^https?:\/\//.test(value.origin)
    && typeof value.expires_at === "string"
    && Number.isFinite(Date.parse(value.expires_at))
    && ["armed", "submission_detected"].includes(value.status)
    && typeof value.session_token === "string"
    && (value.status !== "armed" || value.session_token.length >= 32)
    && (value.pending_submit === null || (
      value.pending_submit
      && typeof value.pending_submit === "object"
      && typeof value.pending_submit.event_key === "string"
      && typeof value.pending_submit.observed_at === "string"
    ))
  );
}

function applicationSessionStorageKey(tabId) {
  return `${APPLICATION_SESSION_STORAGE_KEY_PREFIX}${tabId}`;
}

async function storedApplicationSession(tabId) {
  const key = applicationSessionStorageKey(tabId);
  const stored = await extensionApi.storage.session.get(key);
  const value = stored?.[key];
  if (value === undefined) return null;
  if (!validStoredApplicationSession(value, tabId)) {
    await extensionApi.storage.session.remove(key);
    return null;
  }
  return { ...value };
}

async function saveApplicationSession(value) {
  if (!validStoredApplicationSession(value, value?.tab_id)) {
    throw new CaptureClientError("APPLICATION_SESSION_STATE_INVALID");
  }
  await extensionApi.storage.session.set({
    [applicationSessionStorageKey(value.tab_id)]: { ...value },
  });
}

async function removeApplicationSession(tabId) {
  await extensionApi.storage.session.remove(applicationSessionStorageKey(tabId));
}

function discoveryReviewStorageKey(tabId) {
  return `${DISCOVERY_REVIEW_STORAGE_PREFIX}${tabId}`;
}

async function saveDiscoveryReviewTarget(tabId, discoveryId) {
  await extensionApi.storage.session.set({
    [discoveryReviewStorageKey(tabId)]: discoveryId,
  });
}

async function storedDiscoveryReviewTarget(tabId) {
  const key = discoveryReviewStorageKey(tabId);
  const stored = await extensionApi.storage.session.get(key);
  const value = stored?.[key];
  return typeof value === "string" && DISCOVERY_ID_PATTERN.test(value) ? value : null;
}

async function removeDiscoveryReviewTarget(tabId) {
  await extensionApi.storage.session.remove(discoveryReviewStorageKey(tabId));
}

function publicApplicationSession(value) {
  if (!value) return null;
  return {
    status: value.status,
    expires_at: value.expires_at,
    review_required: value.status === "submission_detected",
  };
}

async function extensionPost(path, body) {
  const token = await extensionSessionToken();
  const response = await fetchWithTimeout(path, {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
      "X-Career-Hub-Token": token,
    },
    body: JSON.stringify(body),
  });
  return readJson(response);
}

async function interruptStoredApplicationSession(value, reason) {
  if (!value || value.status !== "armed" || !value.session_token) return;
  try {
    await extensionPost(APPLICATION_INTERRUPT_PATH, {
      contract_version: CONTRACT_VERSION,
      session_id: value.session_id,
      session_token: value.session_token,
      tab_id: value.tab_id,
      page_origin: value.origin,
      reason,
    });
  } catch {
    // The server retains an expiring, reviewable record even if this best-effort
    // interruption cannot be delivered while Career Hub is offline.
  }
}

async function restoreApplicationObserver(tab) {
  if (!tab || typeof tab.id !== "number") return null;
  const stored = await storedApplicationSession(tab.id);
  if (!stored || stored.status !== "armed") return stored;
  if (Date.parse(stored.expires_at) <= Date.now()) {
    await interruptStoredApplicationSession(stored, "timeout");
    await removeApplicationSession(tab.id);
    return null;
  }
  const currentOrigin = pageOrigin(tab.url || "");
  if (!currentOrigin || currentOrigin !== stored.origin) {
    await interruptStoredApplicationSession(stored, "origin_changed");
    await removeApplicationSession(tab.id);
    return null;
  }
  try {
    const result = await executeMainFrame(
      tab.id,
      installCareerHubApplicationSessionObserver,
      [stored.session_id],
    );
    if (result.ok !== true || result.origin !== stored.origin) {
      throw new CaptureClientError("APPLICATION_SESSION_INSTRUMENTATION_FAILED");
    }
  } catch {
    await interruptStoredApplicationSession(stored, "instrumentation_lost");
    await removeApplicationSession(tab.id);
    return null;
  }
  return stored;
}

async function inspectActiveTab() {
  const tab = await getActiveHttpTab();
  const applicationSession = await restoreApplicationObserver(tab);
  try {
    const selection = await executeMainFrame(
      tab.id,
      inspectCareerHubSelection,
      [MAX_SOURCE_CHARS],
    );
    return {
      ok: true,
      page_capture_available: true,
      selection_chars: Number(selection.selection_chars) || 0,
      selection_too_large: selection.selection_too_large === true,
      application_session: publicApplicationSession(applicationSession),
    };
  } catch (error) {
    if (
      error instanceof CaptureClientError
      && ["PAGE_ACCESS_UNAVAILABLE", "EXTRACTION_FAILED"].includes(error.code)
    ) {
      return {
        ok: true,
        page_capture_available: false,
        selection_chars: 0,
        selection_too_large: false,
        application_session: publicApplicationSession(applicationSession),
      };
    }
    throw error;
  }
}

// ADR SEC-250 item 4: the owner pairs this extension once with a code shown in
// Career Hub Settings. The credential lives only in this worker's local
// extension storage and is sent only to the fixed loopback app.
async function storedCredential() {
  const stored = await extensionApi.storage.local.get(CREDENTIAL_STORAGE_KEY);
  const value = stored?.[CREDENTIAL_STORAGE_KEY];
  return typeof value === "string" && CREDENTIAL_PATTERN.test(value) ? value : null;
}

async function pairedBootstrap(path) {
  const credential = await storedCredential();
  if (!credential) throw new CaptureClientError("EXTENSION_PAIRING_REQUIRED");
  // POST keeps the request attributable to the configured extension Origin
  // (Chrome may omit Origin on a host-permitted GET); the credential proves it.
  const response = await fetchWithTimeout(path, {
    method: "POST",
    headers: { Accept: "application/json", [EXTENSION_CREDENTIAL_HEADER]: credential },
  });
  try {
    return await readJson(response);
  } catch (error) {
    if (error instanceof CaptureClientError && error.code === "EXTENSION_PAIRING_REQUIRED") {
      await extensionApi.storage.local.remove(CREDENTIAL_STORAGE_KEY); // revoked in Career Hub
    }
    throw error;
  }
}

async function pairExtension(code) {
  const response = await fetchWithTimeout(EXTENSION_PAIR_PATH, {
    method: "POST",
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    body: JSON.stringify({ code: code.trim() }),
  });
  const paired = await readJson(response);
  if (
    !exactKeys(paired, ["contract_version", "client", "credential"])
    || paired.contract_version !== 1
    || paired.client !== "extension"
    || typeof paired.credential !== "string"
    || !CREDENTIAL_PATTERN.test(paired.credential)
  ) {
    throw new CaptureClientError("APP_RESPONSE_INVALID");
  }
  await extensionApi.storage.local.set({ [CREDENTIAL_STORAGE_KEY]: paired.credential });
  return { ok: true, paired: true };
}

async function pairingStatus() {
  return { ok: true, paired: Boolean(await storedCredential()) };
}

// One probe tells both surfaces which of four states the connection is in:
// "offline" (nothing answers on the fixed port), "not_enabled" (Career Hub
// answers but this origin is not admitted), "unpaired" (admitted, no valid
// credential) or "ready". It sends only the bootstrap the worker sends anyway
// and discards the token it returns.
async function connectionStatus() {
  const credential = await storedCredential();
  const headers = { Accept: "application/json" };
  if (credential) headers[EXTENSION_CREDENTIAL_HEADER] = credential;
  let response;
  try {
    response = await fetchWithTimeout(SESSION_PATH, { method: "POST", headers });
  } catch (error) {
    if (error instanceof CaptureClientError && ["APP_UNAVAILABLE", "APP_TIMEOUT"].includes(error.code)) {
      return { ok: true, status: "offline", paired: Boolean(credential) };
    }
    throw error;
  }
  try {
    const session = await readJson(response);
    const ready = session.client === "extension" && typeof session.token === "string" && session.token;
    return { ok: true, status: ready ? "ready" : "not_enabled", paired: Boolean(credential) };
  } catch (error) {
    if (error instanceof CaptureClientError) {
      if (error.code === "EXTENSION_PAIRING_REQUIRED") {
        if (credential) await extensionApi.storage.local.remove(CREDENTIAL_STORAGE_KEY); // revoked in Career Hub
        return { ok: true, status: "unpaired", paired: false };
      }
      if (["LOCAL_API_ACCESS_DENIED", "RESOURCE_NOT_FOUND", "APP_RESPONSE_INVALID"].includes(error.code)) {
        return { ok: true, status: "not_enabled", paired: Boolean(credential) };
      }
    }
    throw error;
  }
}

function isConnectionStatusCommand(message) {
  return Boolean(
    message
    && typeof message === "object"
    && !Array.isArray(message)
    && Object.keys(message).length === 1
    && message.command === CONNECTION_STATUS_COMMAND
  );
}

function isOpenAppCommand(message) {
  return Boolean(
    message
    && typeof message === "object"
    && !Array.isArray(message)
    && Object.keys(message).length === 1
    && message.command === OPEN_APP_COMMAND
  );
}

async function openCareerHubApp() {
  try {
    await extensionApi.tabs.create({ active: true, url: `${APP_ORIGIN}/` });
  } catch {
    throw new CaptureClientError("REVIEW_OPEN_FAILED");
  }
  return { ok: true };
}

function isPairExtensionCommand(message) {
  return Boolean(
    message
    && typeof message === "object"
    && !Array.isArray(message)
    && Object.keys(message).length === 2
    && message.command === PAIR_EXTENSION_COMMAND
    && typeof message.code === "string"
    && PAIRING_CODE_PATTERN.test(message.code.trim())
  );
}

// Pairing is an owner action in an extension page, never a content script.
// A content script's sender.url is the web page; the side panel's is ours,
// whether it is shown as a panel or opened in a tab.
function fromExtensionPage(sender) {
  return typeof sender.url === "string"
    && sender.url.startsWith(extensionApi.runtime.getURL(""));
}

function isPairingStatusCommand(message) {
  return Boolean(
    message
    && typeof message === "object"
    && !Array.isArray(message)
    && Object.keys(message).length === 1
    && message.command === PAIRING_STATUS_COMMAND
  );
}

async function extensionSessionToken() {
  const session = await pairedBootstrap(SESSION_PATH);
  if (session.client !== "extension" || typeof session.token !== "string" || !session.token) {
    throw new CaptureClientError("LOCAL_API_ACCESS_DENIED");
  }
  return session.token;
}

function exactKeys(value, expected) {
  return Boolean(
    value
    && typeof value === "object"
    && !Array.isArray(value)
    && Object.keys(value).length === expected.length
    && expected.every((key) => Object.hasOwn(value, key))
  );
}

async function helperSessionToken() {
  const session = await pairedBootstrap(APPLICATION_HELPER_SESSION_PATH);
  if (
    !exactKeys(session, ["contract_version", "token", "client"])
    || session.contract_version !== HELPER_CONTRACT_VERSION
    || session.client !== "application_helper"
    || typeof session.token !== "string"
    || session.token.length < 32
  ) {
    throw new CaptureClientError("APPLICATION_HELPER_CONTRACT_UNSUPPORTED");
  }
  return session.token;
}

function helperQuestion(value) {
  if (typeof value !== "string" || value.includes("\0")) return null;
  const display = value.normalize("NFKC").replace(/\s+/g, " ").trim();
  if (display.length < 4 || display.length > MAX_HELPER_QUESTION_CHARS) return null;
  return {
    display,
    canonical: display.toLocaleLowerCase("en-US").replace(/[ ?!:.]+$/g, ""),
  };
}

function validatedHelperAnswers(value) {
  if (!Array.isArray(value) || value.length > MAX_HELPER_ANSWERS) {
    throw new CaptureClientError("APP_RESPONSE_INVALID");
  }
  const ids = new Set();
  const questions = new Set();
  return value.map((answer) => {
    if (!exactKeys(answer, ["id", "question", "value_kind", "value"])) {
      throw new CaptureClientError("APP_RESPONSE_INVALID");
    }
    const question = helperQuestion(answer.question);
    const textValueValid = ["text", "choice"].includes(answer.value_kind)
      && typeof answer.value === "string"
      && !answer.value.includes("\0")
      && Boolean(answer.value.trim())
      && answer.value.length <= MAX_HELPER_ANSWER_CHARS;
    const booleanValueValid = answer.value_kind === "boolean"
      && typeof answer.value === "boolean";
    if (
      typeof answer.id !== "string"
      || !HELPER_ANSWER_ID_PATTERN.test(answer.id)
      || !question
      || ids.has(answer.id)
      || questions.has(question.canonical)
      || (!textValueValid && !booleanValueValid)
    ) {
      throw new CaptureClientError("APP_RESPONSE_INVALID");
    }
    ids.add(answer.id);
    questions.add(question.canonical);
    return {
      id: answer.id,
      question: question.display,
      value_kind: answer.value_kind,
      value: textValueValid ? answer.value.trim() : answer.value,
    };
  });
}

function validatedHelperResumes(value) {
  if (!Array.isArray(value) || value.length > MAX_HELPER_RESUMES) {
    throw new CaptureClientError("APP_RESPONSE_INVALID");
  }
  const ids = new Set();
  return value.map((resume) => {
    if (
      !exactKeys(resume, ["id", "display_name"])
      || typeof resume.id !== "string"
      || !HELPER_RESUME_ID_PATTERN.test(resume.id)
      || ids.has(resume.id)
      || typeof resume.display_name !== "string"
      || resume.display_name.includes("\0")
      || !resume.display_name.trim()
      || resume.display_name.length > MAX_HELPER_RESUME_NAME_CHARS
    ) throw new CaptureClientError("APP_RESPONSE_INVALID");
    ids.add(resume.id);
    return { id: resume.id, display_name: resume.display_name.trim() };
  });
}

function validatedApplicationAnswerBank(value) {
  if (
    !exactKeys(value, ["contract_version", "revision", "answers"])
    || value.contract_version !== HELPER_CONTRACT_VERSION
    || typeof value.revision !== "string"
    || !HELPER_REVISION_PATTERN.test(value.revision)
  ) throw new CaptureClientError("APP_RESPONSE_INVALID");
  return {
    contract_version: HELPER_CONTRACT_VERSION,
    revision: value.revision,
    answers: validatedHelperAnswers(value.answers),
  };
}

function validatedHelperProjection(value) {
  if (
    !exactKeys(value, [
      "contract_version",
      "revision",
      "answer_revision",
      "fields",
      "resumes",
      "answers",
    ])
    || value.contract_version !== HELPER_CONTRACT_VERSION
  ) {
    throw new CaptureClientError("APPLICATION_HELPER_CONTRACT_UNSUPPORTED");
  }
  if (
    typeof value.revision !== "string"
    || !HELPER_REVISION_PATTERN.test(value.revision)
    || typeof value.answer_revision !== "string"
    || !HELPER_REVISION_PATTERN.test(value.answer_revision)
    || !Array.isArray(value.fields)
    || value.fields.length > HELPER_FIELD_CATALOG.size
  ) {
    throw new CaptureClientError("APP_RESPONSE_INVALID");
  }
  const seen = new Set();
  const fields = [];
  for (const field of value.fields) {
    if (!exactKeys(field, ["id", "label", "source", "value_kind", "value"])) {
      throw new CaptureClientError("APP_RESPONSE_INVALID");
    }
    const expected = HELPER_FIELD_CATALOG.get(field.id);
    if (
      !expected
      || seen.has(field.id)
      || field.label !== expected[0]
      || field.source !== expected[1]
      || field.value_kind !== expected[2]
      || (
        field.value_kind === "text"
        && (
          typeof field.value !== "string"
          || !field.value.trim()
          || field.value.length > MAX_HELPER_TEXT_CHARS
          || field.value.includes("\0")
        )
      )
      || (field.value_kind === "boolean" && typeof field.value !== "boolean")
    ) {
      throw new CaptureClientError("APP_RESPONSE_INVALID");
    }
    seen.add(field.id);
    fields.push({
      id: field.id,
      label: field.label,
      source: field.source,
      value_kind: field.value_kind,
      value: field.value_kind === "text" ? field.value.trim() : field.value,
    });
  }
  const answers = validatedHelperAnswers(value.answers);
  const resumes = validatedHelperResumes(value.resumes);
  return {
    ok: true,
    contract_version: HELPER_CONTRACT_VERSION,
    revision: value.revision,
    answer_revision: value.answer_revision,
    fields,
    resumes,
    answers,
  };
}

async function loadApplicationHelperFields() {
  const token = await helperSessionToken();
  const response = await fetchWithTimeout(APPLICATION_HELPER_FIELDS_PATH, {
    method: "POST",
    headers: {
      Accept: "application/json",
      "X-Career-Hub-Token": token,
    },
  });
  return validatedHelperProjection(await readJson(response));
}

function validatedHelperFormInspection(value, enabledFieldIds) {
  if (
    !exactKeys(value, [
      "ok",
      "contract_version",
      "controls_examined",
      "mappings",
      "skipped",
    ])
    || value.ok !== true
    || value.contract_version !== HELPER_CONTRACT_VERSION
    || !Number.isInteger(value.controls_examined)
    || value.controls_examined < 0
    || value.controls_examined > MAX_HELPER_FORM_CONTROLS
    || !Array.isArray(value.mappings)
    || value.mappings.length > MAX_HELPER_PREFILL_FIELDS
    || !exactKeys(value.skipped, ["ambiguous", "excluded", "non_empty"])
    || ![value.skipped.ambiguous, value.skipped.excluded, value.skipped.non_empty]
      .every((count) => Number.isInteger(count) && count >= 0 && count <= MAX_HELPER_FORM_CONTROLS)
  ) {
    throw new CaptureClientError("APPLICATION_HELPER_FORM_RESPONSE_INVALID");
  }
  const enabled = new Set(enabledFieldIds);
  const seenFields = new Set();
  const mappings = [];
  for (const mapping of value.mappings) {
    if (
      !exactKeys(mapping, [
        "field_id",
        "target_label",
        "target_kind",
        "empty",
        "selectable",
        "skip_reason",
        "target_key",
      ])
      || typeof mapping.field_id !== "string"
      || !enabled.has(mapping.field_id)
      || !HELPER_PREFILL_FIELD_IDS.has(mapping.field_id)
      || seenFields.has(mapping.field_id)
      || typeof mapping.target_label !== "string"
      || !mapping.target_label.trim()
      || mapping.target_label.length > 180
      || mapping.target_label.includes("\0")
      || !["text", "email", "tel", "url", "textarea", "select", "radio", "multiple"].includes(mapping.target_kind)
      || typeof mapping.empty !== "boolean"
      || typeof mapping.selectable !== "boolean"
      || !["", "ambiguous", "non_empty"].includes(mapping.skip_reason)
      || typeof mapping.target_key !== "string"
    ) {
      throw new CaptureClientError("APPLICATION_HELPER_FORM_RESPONSE_INVALID");
    }
    const selectableShape = mapping.selectable
      && mapping.empty
      && mapping.skip_reason === ""
      && mapping.target_kind !== "multiple"
      && /^[a-f0-9]{16}$/.test(mapping.target_key);
    const nonEmptyShape = !mapping.selectable
      && !mapping.empty
      && mapping.skip_reason === "non_empty"
      && mapping.target_kind !== "multiple"
      && /^[a-f0-9]{16}$/.test(mapping.target_key);
    const ambiguousShape = !mapping.selectable
      && !mapping.empty
      && mapping.skip_reason === "ambiguous"
      && mapping.target_kind === "multiple"
      && mapping.target_key === "";
    if (!selectableShape && !nonEmptyShape && !ambiguousShape) {
      throw new CaptureClientError("APPLICATION_HELPER_FORM_RESPONSE_INVALID");
    }
    seenFields.add(mapping.field_id);
    mappings.push({
      field_id: mapping.field_id,
      target_label: mapping.target_label.trim(),
      target_kind: mapping.target_kind,
      empty: mapping.empty,
      selectable: mapping.selectable,
      skip_reason: mapping.skip_reason,
      target_key: mapping.target_key,
    });
  }
  return {
    controls_examined: value.controls_examined,
    mappings,
    skipped: {
      ambiguous: value.skipped.ambiguous,
      excluded: value.skipped.excluded,
      non_empty: value.skipped.non_empty,
    },
  };
}

function helperReviewStorageKey(token) {
  return `${HELPER_REVIEW_STORAGE_PREFIX}${token}`;
}

function validStoredHelperReview(value) {
  return Boolean(
    value
    && typeof value === "object"
    && !Array.isArray(value)
    && HELPER_REVIEW_KINDS.has(value.kind)
    && Number.isSafeInteger(value.expires_at_ms)
    && Number.isSafeInteger(value.tab_id)
    && value.tab_id >= 0
    && typeof value.url === "string"
  );
}

async function storedHelperReviews() {
  const stored = await extensionApi.storage.session.get(null);
  const reviews = [];
  for (const [key, value] of Object.entries(stored || {})) {
    if (!key.startsWith(HELPER_REVIEW_STORAGE_PREFIX)) continue;
    const token = key.slice(HELPER_REVIEW_STORAGE_PREFIX.length);
    const valid = HELPER_REVIEW_TOKEN_PATTERN.test(token) && validStoredHelperReview(value);
    reviews.push({ key, token, value: valid ? value : null });
  }
  return reviews;
}

// Expired or malformed records go first; then the oldest of this kind make
// room so at most MAX_HELPER_PREFILL_REVIEWS - 1 remain before a new save.
async function pruneHelperReviews(kind) {
  const now = Date.now();
  const reviews = await storedHelperReviews();
  const remove = reviews
    .filter((review) => !review.value || review.value.expires_at_ms <= now)
    .map((review) => review.key);
  const live = reviews
    .filter((review) => review.value && review.value.expires_at_ms > now && review.value.kind === kind)
    .sort((left, right) => left.value.expires_at_ms - right.value.expires_at_ms);
  while (live.length >= MAX_HELPER_PREFILL_REVIEWS) remove.push(live.shift().key);
  if (remove.length) await extensionApi.storage.session.remove(remove);
}

async function saveHelperReview(kind, record) {
  await pruneHelperReviews(kind);
  const existing = new Set((await storedHelperReviews()).map((review) => review.token));
  for (let attempt = 0; attempt < 4; attempt += 1) {
    const token = crypto.randomUUID();
    if (!HELPER_REVIEW_TOKEN_PATTERN.test(token) || existing.has(token)) continue;
    const expiresAtMs = Date.now() + HELPER_PREFILL_REVIEW_MS;
    await extensionApi.storage.session.set({
      [helperReviewStorageKey(token)]: { ...record, kind, expires_at_ms: expiresAtMs },
    });
    return { token, expiresAtMs };
  }
  throw new CaptureClientError("APPLICATION_HELPER_REVIEW_UNAVAILABLE");
}

// Every review is consumed by its first use, whatever the outcome.
async function takeHelperReview(kind, token) {
  const key = helperReviewStorageKey(token);
  const stored = await extensionApi.storage.session.get(key);
  const value = stored?.[key];
  if (value !== undefined) await extensionApi.storage.session.remove(key);
  if (
    !validStoredHelperReview(value)
    || value.kind !== kind
    || value.expires_at_ms <= Date.now()
  ) return null;
  return value;
}

async function invalidateHelperReviewsForTab(tabId) {
  const keys = (await storedHelperReviews())
    .filter((review) => !review.value || review.value.tab_id === tabId)
    .map((review) => review.key);
  if (keys.length) await extensionApi.storage.session.remove(keys);
}

// Candidate values read from a page are never stored; the digest lets Save
// confirm the page still shows the value the user reviewed.
async function reusableValueDigest(valueKind, value) {
  const bytes = new TextEncoder().encode(`${valueKind}\n${String(value)}`);
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

async function reviewApplicationHelperForm(sender) {
  const tab = await getActiveHttpTab(sender);
  const projection = await loadApplicationHelperFields();
  const enabledFieldIds = projection.fields
    .map((field) => field.id)
    .filter((fieldId) => HELPER_PREFILL_FIELD_IDS.has(fieldId));
  if (enabledFieldIds.length === 0) {
    throw new CaptureClientError("APPLICATION_HELPER_NO_PREFILL_FIELDS");
  }
  const inspection = validatedHelperFormInspection(
    await executeMainFrame(
      tab.id,
      runCareerHubApplicationForm,
      ["inspect", enabledFieldIds, MAX_HELPER_FORM_CONTROLS],
    ),
    enabledFieldIds,
  );
  const mappings = inspection.mappings.map((mapping, index) => ({
    ...mapping,
    mapping_id: `mapping_${index + 1}`,
  }));
  const pageHost = helperPageHost(tab.url);
  const { token: reviewToken, expiresAtMs } = await saveHelperReview("prefill", {
    mappings,
    projection_revision: projection.revision,
    tab_id: tab.id,
    url: tab.url,
  });
  return {
    ok: true,
    contract_version: HELPER_CONTRACT_VERSION,
    review_token: reviewToken,
    expires_at: new Date(expiresAtMs).toISOString(),
    page_host: pageHost,
    controls_examined: inspection.controls_examined,
    mappings: mappings.map((mapping) => ({
      mapping_id: mapping.mapping_id,
      field_id: mapping.field_id,
      field_label: HELPER_FIELD_CATALOG.get(mapping.field_id)[0],
      target_label: mapping.target_label,
      target_kind: mapping.target_kind,
      empty: mapping.empty,
      selectable: mapping.selectable,
      skip_reason: mapping.skip_reason,
    })),
    skipped: inspection.skipped,
  };
}

function validatedHelperFillResult(value, expectedMappingIds) {
  if (
    exactKeys(value, ["ok", "error_code"])
    && value.ok === false
    && ["APPLICATION_HELPER_FORM_CHANGED", "APPLICATION_HELPER_SELECTION_INVALID"]
      .includes(value.error_code)
  ) {
    throw new CaptureClientError(value.error_code);
  }
  if (
    !exactKeys(value, ["ok", "filled_mapping_ids"])
    || value.ok !== true
    || !Array.isArray(value.filled_mapping_ids)
    || value.filled_mapping_ids.length !== expectedMappingIds.length
    || value.filled_mapping_ids.some((mappingId, index) => mappingId !== expectedMappingIds[index])
  ) {
    throw new CaptureClientError("APPLICATION_HELPER_FORM_RESPONSE_INVALID");
  }
}

async function fillReviewedApplicationHelperFields(message, sender) {
  const review = await takeHelperReview("prefill", message.review_token);
  if (!review || !Array.isArray(review.mappings)) {
    throw new CaptureClientError("APPLICATION_HELPER_REVIEW_EXPIRED");
  }
  const tab = await getActiveHttpTab(sender);
  if (tab.id !== review.tab_id || tab.url !== review.url) {
    throw new CaptureClientError("APPLICATION_HELPER_PAGE_CHANGED");
  }
  const selectedMappings = message.mapping_ids.map((mappingId) => (
    review.mappings.find((mapping) => mapping.mapping_id === mappingId)
  ));
  if (
    selectedMappings.some((mapping) => !mapping || !mapping.selectable)
    || new Set(selectedMappings.map((mapping) => mapping.target_key)).size
      !== selectedMappings.length
  ) {
    throw new CaptureClientError("APPLICATION_HELPER_SELECTION_INVALID");
  }
  const projection = await loadApplicationHelperFields();
  if (projection.revision !== review.projection_revision) {
    throw new CaptureClientError("APPLICATION_HELPER_DATA_CHANGED");
  }
  const projectionById = new Map(projection.fields.map((field) => [field.id, field]));
  const instructions = selectedMappings.map((mapping) => {
    const field = projectionById.get(mapping.field_id);
    if (
      !field
      || !["text", "boolean"].includes(field.value_kind)
      || !HELPER_PREFILL_FIELD_IDS.has(field.id)
    ) {
      throw new CaptureClientError("APPLICATION_HELPER_DATA_CHANGED");
    }
    return {
      mapping_id: mapping.mapping_id,
      field_id: mapping.field_id,
      target_key: mapping.target_key,
      value_kind: field.value_kind,
      value: field.value,
    };
  });
  const result = await executeMainFrame(
    tab.id,
    runCareerHubApplicationForm,
    ["fill", instructions, MAX_HELPER_FORM_CONTROLS],
  );
  validatedHelperFillResult(result, message.mapping_ids);
  return {
    ok: true,
    contract_version: HELPER_CONTRACT_VERSION,
    filled_count: selectedMappings.length,
    filled: selectedMappings.map((mapping) => ({
      mapping_id: mapping.mapping_id,
      field_id: mapping.field_id,
      field_label: HELPER_FIELD_CATALOG.get(mapping.field_id)[0],
      target_label: mapping.target_label,
    })),
  };
}

function helperPageHost(url) {
  try {
    return new URL(url).hostname.slice(0, 253);
  } catch {
    throw new CaptureClientError("ACTIVE_TAB_UNSUPPORTED");
  }
}

function reusableAnswerScannerFailure(value) {
  if (
    exactKeys(value, ["ok", "error_code"])
    && value.ok === false
    && [
      "APPLICATION_ANSWER_PAGE_TOO_COMPLEX",
      "APPLICATION_ANSWER_FORM_AMBIGUOUS",
      "APPLICATION_ANSWER_FORM_CHANGED",
      "APPLICATION_ANSWER_SELECTION_INVALID",
      "APPLICATION_ANSWER_REQUEST_INVALID",
    ].includes(value.error_code)
  ) {
    throw new CaptureClientError(value.error_code);
  }
}

function validatedReusableAnswerInspection(value, projectionAnswers) {
  reusableAnswerScannerFailure(value);
  if (
    !exactKeys(value, [
      "ok",
      "contract_version",
      "controls_examined",
      "mappings",
      "skipped",
    ])
    || value.ok !== true
    || value.contract_version !== HELPER_CONTRACT_VERSION
    || !Number.isInteger(value.controls_examined)
    || value.controls_examined < 0
    || value.controls_examined > MAX_HELPER_FORM_CONTROLS
    || !Array.isArray(value.mappings)
    || value.mappings.length > MAX_HELPER_ANSWER_MAPPINGS
    || !exactKeys(value.skipped, ["ambiguous", "non_empty"])
    || ![value.skipped.ambiguous, value.skipped.non_empty].every((count) => (
      Number.isInteger(count) && count >= 0 && count <= MAX_HELPER_FORM_CONTROLS
    ))
  ) throw new CaptureClientError("APPLICATION_ANSWER_FORM_RESPONSE_INVALID");

  const answerById = new Map(projectionAnswers.map((answer) => [answer.id, answer]));
  const seenAnswers = new Set();
  const mappings = value.mappings.map((mapping) => {
    if (
      !exactKeys(mapping, [
        "answer_id",
        "question",
        "target_label",
        "target_kind",
        "empty",
        "selectable",
        "skip_reason",
        "target_key",
      ])
      || typeof mapping.answer_id !== "string"
      || seenAnswers.has(mapping.answer_id)
      || typeof mapping.question !== "string"
      || typeof mapping.target_label !== "string"
      || !mapping.target_label.trim()
      || mapping.target_label.length > MAX_HELPER_QUESTION_CHARS
      || mapping.target_label.includes("\0")
      || !["text", "select", "radio", "multiple"].includes(mapping.target_kind)
      || typeof mapping.empty !== "boolean"
      || typeof mapping.selectable !== "boolean"
      || !["", "ambiguous", "non_empty", "incompatible"].includes(mapping.skip_reason)
      || typeof mapping.target_key !== "string"
    ) throw new CaptureClientError("APPLICATION_ANSWER_FORM_RESPONSE_INVALID");
    const answer = answerById.get(mapping.answer_id);
    if (!answer || mapping.question !== answer.question) {
      throw new CaptureClientError("APPLICATION_ANSWER_FORM_RESPONSE_INVALID");
    }
    const selectable = mapping.selectable
      && mapping.empty
      && mapping.skip_reason === ""
      && mapping.target_kind !== "multiple"
      && /^[a-f0-9]{16}$/.test(mapping.target_key);
    const nonEmpty = !mapping.selectable
      && !mapping.empty
      && mapping.skip_reason === "non_empty"
      && mapping.target_kind !== "multiple"
      && /^[a-f0-9]{16}$/.test(mapping.target_key);
    const incompatible = !mapping.selectable
      && mapping.empty
      && mapping.skip_reason === "incompatible"
      && mapping.target_kind !== "multiple"
      && /^[a-f0-9]{16}$/.test(mapping.target_key);
    const ambiguous = !mapping.selectable
      && !mapping.empty
      && mapping.skip_reason === "ambiguous"
      && mapping.target_kind === "multiple"
      && mapping.target_key === "";
    if (!selectable && !nonEmpty && !incompatible && !ambiguous) {
      throw new CaptureClientError("APPLICATION_ANSWER_FORM_RESPONSE_INVALID");
    }
    seenAnswers.add(mapping.answer_id);
    return {
      answer_id: mapping.answer_id,
      question: answer.question,
      target_label: mapping.target_label.trim(),
      target_kind: mapping.target_kind,
      empty: mapping.empty,
      selectable: mapping.selectable,
      skip_reason: mapping.skip_reason,
      target_key: mapping.target_key,
    };
  });
  return {
    controls_examined: value.controls_examined,
    mappings,
    skipped: {
      ambiguous: value.skipped.ambiguous,
      non_empty: value.skipped.non_empty,
    },
  };
}

function validReusableCandidateValue(valueKind, value) {
  if (valueKind === "boolean") return typeof value === "boolean";
  return ["text", "choice"].includes(valueKind)
    && typeof value === "string"
    && !value.includes("\0")
    && Boolean(value.trim())
    && value.length <= MAX_HELPER_ANSWER_CHARS;
}

function normalizedReusableCandidate(candidate, keys) {
  if (!exactKeys(candidate, keys)) {
    throw new CaptureClientError("APPLICATION_ANSWER_FORM_RESPONSE_INVALID");
  }
  const question = helperQuestion(candidate.question);
  if (
    !question
    || typeof candidate.target_key !== "string"
    || !/^[a-f0-9]{16}$/.test(candidate.target_key)
    || !validReusableCandidateValue(candidate.value_kind, candidate.value)
  ) throw new CaptureClientError("APPLICATION_ANSWER_FORM_RESPONSE_INVALID");
  return {
    question: question.display,
    target_key: candidate.target_key,
    value_kind: candidate.value_kind,
    value: typeof candidate.value === "string" ? candidate.value.trim() : candidate.value,
  };
}

function validatedReusableAnswerCandidates(value) {
  reusableAnswerScannerFailure(value);
  if (
    !exactKeys(value, [
      "ok",
      "contract_version",
      "controls_examined",
      "candidates",
    ])
    || value.ok !== true
    || value.contract_version !== HELPER_CONTRACT_VERSION
    || !Number.isInteger(value.controls_examined)
    || value.controls_examined < 0
    || value.controls_examined > MAX_HELPER_FORM_CONTROLS
    || !Array.isArray(value.candidates)
    || value.candidates.length > MAX_HELPER_ANSWER_MAPPINGS
  ) throw new CaptureClientError("APPLICATION_ANSWER_FORM_RESPONSE_INVALID");
  const ids = new Set();
  const questions = new Set();
  const targetKeys = new Set();
  const candidates = value.candidates.map((candidate) => {
    const normalized = normalizedReusableCandidate(candidate, [
      "candidate_id",
      "question",
      "target_kind",
      "target_key",
      "value_kind",
      "value",
    ]);
    const question = helperQuestion(normalized.question);
    const compatible = (
      (candidate.target_kind === "text" && normalized.value_kind === "text")
      || (candidate.target_kind === "select" && normalized.value_kind === "choice")
      || (
        candidate.target_kind === "radio"
        && ["choice", "boolean"].includes(normalized.value_kind)
      )
    );
    if (
      typeof candidate.candidate_id !== "string"
      || !HELPER_ANSWER_CANDIDATE_ID_PATTERN.test(candidate.candidate_id)
      || ids.has(candidate.candidate_id)
      || questions.has(question.canonical)
      || targetKeys.has(normalized.target_key)
      || !compatible
    ) throw new CaptureClientError("APPLICATION_ANSWER_FORM_RESPONSE_INVALID");
    ids.add(candidate.candidate_id);
    questions.add(question.canonical);
    targetKeys.add(normalized.target_key);
    return {
      candidate_id: candidate.candidate_id,
      target_kind: candidate.target_kind,
      ...normalized,
    };
  });
  return { controls_examined: value.controls_examined, candidates };
}

function validatedReusableAnswerRead(value) {
  reusableAnswerScannerFailure(value);
  if (
    !exactKeys(value, ["ok", "contract_version", "candidate"])
    || value.ok !== true
    || value.contract_version !== HELPER_CONTRACT_VERSION
  ) throw new CaptureClientError("APPLICATION_ANSWER_FORM_RESPONSE_INVALID");
  return normalizedReusableCandidate(value.candidate, [
    "question",
    "target_key",
    "value_kind",
    "value",
  ]);
}

function validatedReusableAnswerFill(value, expectedMappingIds) {
  reusableAnswerScannerFailure(value);
  if (
    !exactKeys(value, [
      "ok",
      "contract_version",
      "filled_mapping_ids",
    ])
    || value.ok !== true
    || value.contract_version !== HELPER_CONTRACT_VERSION
    || !Array.isArray(value.filled_mapping_ids)
    || value.filled_mapping_ids.length !== expectedMappingIds.length
    || value.filled_mapping_ids.some((mappingId, index) => (
      mappingId !== expectedMappingIds[index]
    ))
  ) throw new CaptureClientError("APPLICATION_ANSWER_FORM_RESPONSE_INVALID");
}

async function mutateApplicationAnswer(path, body) {
  const token = await helperSessionToken();
  const response = await fetchWithTimeout(path, {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
      "X-Career-Hub-Token": token,
    },
    body: JSON.stringify(body),
  });
  return validatedApplicationAnswerBank(await readJson(response));
}

async function reviewSavedApplicationAnswers(sender) {
  const tab = await getActiveHttpTab(sender);
  const projection = await loadApplicationHelperFields();
  if (projection.answers.length === 0) {
    throw new CaptureClientError("APPLICATION_HELPER_NO_SAVED_ANSWERS");
  }
  const inspection = validatedReusableAnswerInspection(
    await executeMainFrame(
      tab.id,
      runCareerHubReusableAnswers,
      [
        "inspect_saved",
        projection.answers.map((answer) => ({
          id: answer.id,
          question: answer.question,
          value_kind: answer.value_kind,
        })),
        MAX_HELPER_FORM_CONTROLS,
      ],
    ),
    projection.answers,
  );
  const mappings = inspection.mappings.map((mapping, index) => ({
    ...mapping,
    mapping_id: `answer_mapping_${index + 1}`,
  }));
  const { token: reviewToken, expiresAtMs } = await saveHelperReview("answers", {
    answer_revision: projection.answer_revision,
    mappings,
    tab_id: tab.id,
    url: tab.url,
  });
  return {
    ok: true,
    contract_version: HELPER_CONTRACT_VERSION,
    review_token: reviewToken,
    expires_at: new Date(expiresAtMs).toISOString(),
    page_host: helperPageHost(tab.url),
    controls_examined: inspection.controls_examined,
    mappings: mappings.map((mapping) => ({
      mapping_id: mapping.mapping_id,
      answer_id: mapping.answer_id,
      question: mapping.question,
      target_label: mapping.target_label,
      target_kind: mapping.target_kind,
      empty: mapping.empty,
      selectable: mapping.selectable,
      skip_reason: mapping.skip_reason,
    })),
    skipped: inspection.skipped,
  };
}

async function fillReviewedApplicationAnswers(message, sender) {
  const review = await takeHelperReview("answers", message.review_token);
  if (!review || !Array.isArray(review.mappings)) {
    throw new CaptureClientError("APPLICATION_ANSWER_REVIEW_EXPIRED");
  }
  const tab = await getActiveHttpTab(sender);
  if (tab.id !== review.tab_id || tab.url !== review.url) {
    throw new CaptureClientError("APPLICATION_ANSWER_PAGE_CHANGED");
  }
  const selected = message.mapping_ids.map((mappingId) => (
    review.mappings.find((mapping) => mapping.mapping_id === mappingId)
  ));
  if (
    selected.some((mapping) => !mapping || !mapping.selectable)
    || new Set(selected.map((mapping) => mapping.target_key)).size !== selected.length
  ) throw new CaptureClientError("APPLICATION_ANSWER_SELECTION_INVALID");
  const projection = await loadApplicationHelperFields();
  if (projection.answer_revision !== review.answer_revision) {
    throw new CaptureClientError("APPLICATION_ANSWER_DATA_CHANGED");
  }
  const answerById = new Map(projection.answers.map((answer) => [answer.id, answer]));
  const instructions = selected.map((mapping) => {
    const answer = answerById.get(mapping.answer_id);
    if (
      !answer
      || answer.question !== mapping.question
      || !["text", "choice", "boolean"].includes(answer.value_kind)
    ) throw new CaptureClientError("APPLICATION_ANSWER_DATA_CHANGED");
    return {
      mapping_id: mapping.mapping_id,
      answer_id: answer.id,
      question: answer.question,
      target_key: mapping.target_key,
      value_kind: answer.value_kind,
      value: answer.value,
    };
  });
  validatedReusableAnswerFill(
    await executeMainFrame(
      tab.id,
      runCareerHubReusableAnswers,
      ["fill_saved", instructions, MAX_HELPER_FORM_CONTROLS],
    ),
    message.mapping_ids,
  );
  return {
    ok: true,
    contract_version: HELPER_CONTRACT_VERSION,
    filled_count: selected.length,
    filled: selected.map((mapping) => ({
      mapping_id: mapping.mapping_id,
      answer_id: mapping.answer_id,
      question: mapping.question,
      target_label: mapping.target_label,
    })),
  };
}

async function findReusableApplicationAnswers(sender) {
  const tab = await getActiveHttpTab(sender);
  const projection = await loadApplicationHelperFields();
  const inspection = validatedReusableAnswerCandidates(
    await executeMainFrame(
      tab.id,
      runCareerHubReusableAnswers,
      [
        "find_completed",
        projection.answers.map((answer) => answer.question),
        MAX_HELPER_FORM_CONTROLS,
      ],
    ),
  );
  const candidates = [];
  for (const candidate of inspection.candidates) {
    candidates.push({
      candidate_id: candidate.candidate_id,
      question: candidate.question,
      target_kind: candidate.target_kind,
      target_key: candidate.target_key,
      value_kind: candidate.value_kind,
      value_digest: await reusableValueDigest(candidate.value_kind, candidate.value),
    });
  }
  const { token: reviewToken, expiresAtMs } = await saveHelperReview("candidates", {
    answer_revision: projection.answer_revision,
    candidates,
    tab_id: tab.id,
    url: tab.url,
  });
  return {
    ok: true,
    contract_version: HELPER_CONTRACT_VERSION,
    review_token: reviewToken,
    expires_at: new Date(expiresAtMs).toISOString(),
    page_host: helperPageHost(tab.url),
    controls_examined: inspection.controls_examined,
    candidates: inspection.candidates.map((candidate) => ({
      candidate_id: candidate.candidate_id,
      question: candidate.question,
      target_kind: candidate.target_kind,
      value_kind: candidate.value_kind,
      value: candidate.value,
    })),
  };
}

async function saveReviewedApplicationAnswer(message, sender) {
  const review = await takeHelperReview("candidates", message.review_token);
  if (!review || !Array.isArray(review.candidates)) {
    throw new CaptureClientError("APPLICATION_ANSWER_REVIEW_EXPIRED");
  }
  const candidate = review.candidates.find((item) => (
    item.candidate_id === message.candidate_id
  ));
  if (!candidate) throw new CaptureClientError("APPLICATION_ANSWER_SELECTION_INVALID");
  const tab = await getActiveHttpTab(sender);
  if (tab.id !== review.tab_id || tab.url !== review.url) {
    throw new CaptureClientError("APPLICATION_ANSWER_PAGE_CHANGED");
  }
  const current = validatedReusableAnswerRead(
    await executeMainFrame(
      tab.id,
      runCareerHubReusableAnswers,
      [
        "read_completed",
        {
          question: candidate.question,
          target_key: candidate.target_key,
          value_kind: candidate.value_kind,
        },
        MAX_HELPER_FORM_CONTROLS,
      ],
    ),
  );
  if (
    current.question !== candidate.question
    || current.target_key !== candidate.target_key
    || current.value_kind !== candidate.value_kind
    || await reusableValueDigest(current.value_kind, current.value) !== candidate.value_digest
  ) throw new CaptureClientError("APPLICATION_ANSWER_FORM_CHANGED");
  const projection = await loadApplicationHelperFields();
  if (projection.answer_revision !== review.answer_revision) {
    throw new CaptureClientError("APPLICATION_ANSWER_DATA_CHANGED");
  }
  const saved = await mutateApplicationAnswer(APPLICATION_HELPER_ANSWER_SAVE_PATH, {
    contract_version: HELPER_CONTRACT_VERSION,
    expected_revision: projection.answer_revision,
    question: current.question,
    value_kind: current.value_kind,
    value: current.value,
  });
  const refreshed = await loadApplicationHelperFields();
  if (refreshed.answer_revision !== saved.revision) {
    throw new CaptureClientError("APPLICATION_ANSWER_DATA_CHANGED");
  }
  return refreshed;
}

async function deleteReusableApplicationAnswer(message) {
  const projection = await loadApplicationHelperFields();
  if (projection.answer_revision !== message.answer_revision) {
    throw new CaptureClientError("APPLICATION_ANSWER_DATA_CHANGED");
  }
  if (!projection.answers.some((answer) => answer.id === message.answer_id)) {
    throw new CaptureClientError("APPLICATION_ANSWER_DATA_CHANGED");
  }
  const deleted = await mutateApplicationAnswer(APPLICATION_HELPER_ANSWER_DELETE_PATH, {
    contract_version: HELPER_CONTRACT_VERSION,
    expected_revision: projection.answer_revision,
    answer_id: message.answer_id,
  });
  const refreshed = await loadApplicationHelperFields();
  if (refreshed.answer_revision !== deleted.revision) {
    throw new CaptureClientError("APPLICATION_ANSWER_DATA_CHANGED");
  }
  return refreshed;
}

async function openHelperResume(resumeId) {
  const projection = await loadApplicationHelperFields();
  if (!projection.resumes.some((resume) => resume.id === resumeId)) {
    throw new CaptureClientError("APPLICATION_HELPER_RESUME_UNAVAILABLE");
  }
  try {
    await extensionApi.tabs.create({
      active: true,
      url: `${APP_ORIGIN}/#resume-library/${encodeURIComponent(resumeId)}`,
    });
  } catch {
    throw new CaptureClientError("REVIEW_OPEN_FAILED");
  }
  return { ok: true };
}

function boundedReceiptText(value) {
  return typeof value === "string"
    && value.length > 0
    && Array.from(value).length <= MAX_RECEIPT_TEXT_CHARS;
}

function validReceipt(receipt, expectedScope) {
  const keys = [
    "schema_version",
    "status",
    "label",
    "availability",
    "scope",
    "blocker_count",
    "blockers",
    "review_count",
    "review_items",
    "unknown_vocabulary_count",
    "extra_step_count",
    "contact_candidate_count",
    "truncated",
  ];
  if (
    !exactKeys(receipt, keys)
    // Native v4 retains v3 fields and bounds the whole experience message.
    || ![3, 4].includes(receipt.schema_version)
    || !RECEIPT_STATUSES.has(receipt.status)
    || !boundedReceiptText(receipt.label)
    || !RECEIPT_AVAILABILITY.has(receipt.availability)
    || receipt.scope !== expectedScope
    || !Number.isInteger(receipt.blocker_count)
    || receipt.blocker_count < 0
    || !Number.isInteger(receipt.review_count)
    || receipt.review_count < 0
    || !Number.isInteger(receipt.unknown_vocabulary_count)
    || receipt.unknown_vocabulary_count < 0
    || !Number.isInteger(receipt.extra_step_count)
    || receipt.extra_step_count < 0
    || !Number.isInteger(receipt.contact_candidate_count)
    || receipt.contact_candidate_count < 0
    || typeof receipt.truncated !== "boolean"
    || !Array.isArray(receipt.blockers)
    || receipt.blockers.length > MAX_RECEIPT_FINDINGS
    || !Array.isArray(receipt.review_items)
    || receipt.review_items.length > MAX_RECEIPT_FINDINGS
  ) {
    return false;
  }
  for (const blocker of receipt.blockers) {
    if (
      !exactKeys(blocker, ["code", "message", "source_evidence", "candidate_fact"])
      || !boundedReceiptText(blocker.code)
      || !boundedReceiptText(blocker.message)
      || !boundedReceiptText(blocker.candidate_fact)
      || !exactKeys(blocker.source_evidence, ["line", "text"])
      || !Number.isInteger(blocker.source_evidence.line)
      || blocker.source_evidence.line < 1
      || !boundedReceiptText(blocker.source_evidence.text)
    ) {
      return false;
    }
  }
  for (const item of receipt.review_items) {
    const hasLine = Object.hasOwn(item || {}, "line");
    const hasSourceEvidence = Object.hasOwn(item || {}, "source_evidence");
    const hasCandidateFact = Object.hasOwn(item || {}, "candidate_fact");
    const itemKeys = [
      "kind",
      ...(hasLine ? ["line"] : []),
      "text",
      ...(hasSourceEvidence ? ["source_evidence"] : []),
      ...(hasCandidateFact ? ["candidate_fact"] : []),
    ];
    if (
      !exactKeys(item, itemKeys)
      || !boundedReceiptText(item.kind)
      || !boundedReceiptText(item.text)
      || (hasLine && (!Number.isInteger(item.line) || item.line < 1))
      || ((hasSourceEvidence || hasCandidateFact) && item.kind !== "policy_review")
      || (hasSourceEvidence && (
        !hasLine
        || !exactKeys(item.source_evidence, ["line", "text"])
        || item.source_evidence.line !== item.line
        || !boundedReceiptText(item.source_evidence.text)
      ))
      || (hasCandidateFact && !boundedReceiptText(item.candidate_fact))
    ) {
      return false;
    }
  }
  return true;
}

function validReviewTarget(review) {
  return Boolean(
    exactKeys(review, ["discovery_id"])
    && typeof review.discovery_id === "string"
    && DISCOVERY_ID_PATTERN.test(review.discovery_id)
  );
}

async function captureActiveTab(mode) {
  const tab = await getActiveHttpTab();
  const requestId = crypto.randomUUID();
  const token = await extensionSessionToken();
  let capture = {
    source_text: "",
    structured: {
      title: "",
      company: "",
      location: "",
      posting_id: "",
    },
    extractor: { version: 1, strategy: "link" },
  };
  if (mode === "selection") {
    capture = await executeMainFrame(
      tab.id,
      extractCareerHubSelection,
      [MAX_SOURCE_CHARS],
    );
  } else if (mode === "page") {
    capture = await executeMainFrame(
      tab.id,
      extractCareerHubVisiblePage,
      [MAX_SOURCE_CHARS, MIN_PAGE_CHARS, MAX_DOM_NODES, MAX_JSONLD_CHARS],
    );
  }
  if (capture.ok === false) {
    throw new CaptureClientError(
      typeof capture.error_code === "string"
        ? capture.error_code
        : "EXTRACTION_FAILED",
    );
  }
  const response = await fetchWithTimeout(CAPTURE_PATH, {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
      "X-Career-Hub-Token": token,
    },
    body: JSON.stringify({
      contract_version: CONTRACT_VERSION,
      request_id: requestId,
      capture_kind: mode,
      destination: "feed",
      observed_url: tab.url,
      page_title: tab.title,
      captured_at_client: new Date().toISOString(),
      source_text: capture.source_text,
      structured: capture.structured,
      extractor: capture.extractor,
    }),
  });
  const result = await readJson(response);
  if (result.contract_version !== CONTRACT_VERSION) {
    throw new CaptureClientError("EXTENSION_CAPTURE_CONTRACT_UNSUPPORTED");
  }
  const expectedResult = {
    link: { capture_scope: "link_only", readiness: "needs_content" },
    page: { capture_scope: "page_visible", readiness: "needs_review" },
    selection: { capture_scope: "selection", readiness: "needs_review" },
  }[mode];
  if (
    !exactKeys(result, [
      "contract_version",
      "outcome",
      "readiness",
      "capture_scope",
      "content_chars",
      "receipt",
      "review",
    ])
    || !["created", "updated", "duplicate"].includes(result.outcome)
    || result.readiness !== expectedResult.readiness
    || result.capture_scope !== expectedResult.capture_scope
    || !Number.isInteger(result.content_chars)
    // Both backends count Unicode codepoints, not UTF-16 code units.
    || result.content_chars !== Array.from(capture.source_text).length
    || !validReceipt(result.receipt, expectedResult.capture_scope)
    || !validReviewTarget(result.review)
  ) {
    throw new CaptureClientError("APP_RESPONSE_INVALID");
  }
  await saveDiscoveryReviewTarget(tab.id, result.review.discovery_id);
  return {
    ok: true,
    outcome: result.outcome,
    readiness: result.readiness,
    capture_scope: result.capture_scope,
    content_chars: result.content_chars,
    receipt: result.receipt,
    review_available: true,
  };
}

async function armApplicationSession() {
  const tab = await getActiveHttpTab();
  const result = await extensionPost(APPLICATION_CLAIM_PATH, {
    contract_version: CONTRACT_VERSION,
    request_id: crypto.randomUUID(),
    current_url: tab.url,
    tab_id: tab.id,
  });
  if (
    !exactKeys(result, [
      "contract_version",
      "outcome",
      "session_id",
      "session_token",
      "bound_origin",
      "expires_at",
    ])
    || result.contract_version !== CONTRACT_VERSION
    || result.outcome !== "armed"
    || !APPLICATION_SESSION_ID_PATTERN.test(result.session_id)
    || typeof result.session_token !== "string"
    || result.session_token.length < 32
    || result.bound_origin !== pageOrigin(tab.url)
    || !Number.isFinite(Date.parse(result.expires_at))
  ) {
    throw new CaptureClientError("APP_RESPONSE_INVALID");
  }
  const stored = {
    session_id: result.session_id,
    session_token: result.session_token,
    tab_id: tab.id,
    origin: result.bound_origin,
    expires_at: result.expires_at,
    status: "armed",
    pending_submit: null,
  };
  await saveApplicationSession(stored);
  try {
    const observer = await executeMainFrame(
      tab.id,
      installCareerHubApplicationSessionObserver,
      [stored.session_id],
    );
    if (observer.ok !== true || observer.origin !== stored.origin) {
      throw new CaptureClientError("APPLICATION_SESSION_INSTRUMENTATION_FAILED");
    }
  } catch {
    await interruptStoredApplicationSession(stored, "instrumentation_lost");
    await removeApplicationSession(tab.id);
    throw new CaptureClientError("APPLICATION_SESSION_INSTRUMENTATION_FAILED");
  }
  return {
    ok: true,
    status: "armed",
    expires_at: stored.expires_at,
  };
}

function validIsoTimestamp(value) {
  return typeof value === "string" && Number.isFinite(Date.parse(value));
}

async function handleApplicationContentSignal(message, sender) {
  const tabId = sender.tab.id;
  const stored = await storedApplicationSession(tabId);
  if (
    !stored
    || stored.status !== "armed"
    || stored.session_id !== message.session_id
    || stored.origin !== message.page_origin
  ) {
    return { ok: true, outcome: "ignored_unbound" };
  }
  if (["native_submit", "final_action_click"].includes(message.signal)) {
    if (
      !/^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(message.event_key)
      || !validIsoTimestamp(message.observed_at)
    ) {
      return { ok: false, error_code: "APPLICATION_SESSION_SIGNAL_INVALID" };
    }
    stored.pending_submit = {
      event_key: message.event_key.toLowerCase(),
      observed_at: message.observed_at,
    };
    await saveApplicationSession(stored);
    return { ok: true, outcome: "submit_candidate_recorded" };
  }

  if (
    !APPLICATION_SUCCESS_RULES.has(message.success_rule)
    || !validIsoTimestamp(message.observed_at)
    || !stored.pending_submit
  ) {
    return { ok: true, outcome: "ignored_without_submit" };
  }
  const submitTime = Date.parse(stored.pending_submit.observed_at);
  const successTime = Date.parse(message.observed_at);
  if (
    !Number.isFinite(submitTime)
    || successTime < submitTime
    || successTime - submitTime > APPLICATION_SIGNAL_WINDOW_MS
  ) {
    stored.pending_submit = null;
    await saveApplicationSession(stored);
    return { ok: true, outcome: "ignored_outside_window" };
  }

  if (submissionSignalByTab.has(tabId)) {
    return submissionSignalByTab.get(tabId);
  }
  const signal = (async () => {
    const response = await extensionPost(APPLICATION_SIGNAL_PATH, {
      contract_version: CONTRACT_VERSION,
      session_id: stored.session_id,
      session_token: stored.session_token,
      tab_id: tabId,
      page_origin: stored.origin,
      event_key: stored.pending_submit.event_key,
      success_rule: message.success_rule,
      submit_observed_at: stored.pending_submit.observed_at,
      success_observed_at: message.observed_at,
    });
    if (
      !exactKeys(response, ["contract_version", "outcome", "review_required"])
      || response.contract_version !== CONTRACT_VERSION
      || !["submission_detected", "duplicate", "already_detected"].includes(response.outcome)
      || response.review_required !== true
    ) {
      throw new CaptureClientError("APP_RESPONSE_INVALID");
    }
    stored.status = "submission_detected";
    stored.session_token = "";
    stored.pending_submit = null;
    await saveApplicationSession(stored);
    return { ok: true, outcome: response.outcome, review_required: true };
  })();
  submissionSignalByTab.set(tabId, signal);
  try {
    return await signal;
  } finally {
    submissionSignalByTab.delete(tabId);
  }
}

async function openCapturedReview(intent = null) {
  const tab = await getActiveHttpTab();
  const discoveryId = await storedDiscoveryReviewTarget(tab.id);
  if (!discoveryId) {
    throw new CaptureClientError("REVIEW_TARGET_UNAVAILABLE");
  }
  try {
    const intentPath = intent === "prepare" ? "/prepare" : "";
    await extensionApi.tabs.create({
      active: true,
      url: `${APP_ORIGIN}/#job-feed/${encodeURIComponent(discoveryId)}${intentPath}`,
    });
  } catch {
    throw new CaptureClientError("REVIEW_OPEN_FAILED");
  }
  return { ok: true };
}

async function prepareCurrentApplication() {
  const capture = await captureActiveTab("page");
  await openCapturedReview("prepare");
  return {
    ok: true,
    outcome: capture.outcome,
    readiness: capture.readiness,
  };
}

async function openApplications() {
  try {
    await extensionApi.tabs.create({
      active: true,
      url: `${APP_ORIGIN}/#applications`,
    });
  } catch {
    throw new CaptureClientError("REVIEW_OPEN_FAILED");
  }
  return { ok: true };
}

async function openApplicationHelperSettings() {
  try {
    await extensionApi.tabs.create({
      active: true,
      url: `${APP_ORIGIN}/#profile/application-helper`,
    });
  } catch {
    throw new CaptureClientError("REVIEW_OPEN_FAILED");
  }
  return { ok: true };
}

function publicFailure(error) {
  return {
    ok: false,
    error_code: error instanceof CaptureClientError
      ? error.code
      : "APP_UNAVAILABLE",
  };
}

extensionApi.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (sender.id !== extensionApi.runtime.id) {
    sendResponse({ ok: false, error_code: "UNSUPPORTED_COMMAND" });
    return false;
  }
  if (isApplicationContentSignal(message, sender)) {
    handleApplicationContentSignal(message, sender)
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isInspectCommand(message)) {
    inspectActiveTab()
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isArmApplicationCommand(message)) {
    armApplicationSession()
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isOpenApplicationsCommand(message)) {
    openApplications()
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isLoadHelperFieldsCommand(message)) {
    loadApplicationHelperFields()
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isPairExtensionCommand(message) && fromExtensionPage(sender)) {
    pairExtension(message.code)
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isPairingStatusCommand(message) && fromExtensionPage(sender)) {
    pairingStatus()
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isConnectionStatusCommand(message) && fromExtensionPage(sender)) {
    connectionStatus()
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isOpenAppCommand(message) && fromExtensionPage(sender)) {
    openCareerHubApp()
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isOpenHelperSettingsCommand(message)) {
    openApplicationHelperSettings()
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isOpenHelperResumeCommand(message)) {
    openHelperResume(message.resume_id)
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isReviewHelperFormCommand(message)) {
    reviewApplicationHelperForm(sender)
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isReviewSavedAnswersCommand(message)) {
    reviewSavedApplicationAnswers(sender)
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isFillSavedAnswersCommand(message)) {
    fillReviewedApplicationAnswers(message, sender)
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isFindReusableAnswersCommand(message)) {
    findReusableApplicationAnswers(sender)
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isSaveReusableAnswerCommand(message)) {
    saveReviewedApplicationAnswer(message, sender)
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isDeleteReusableAnswerCommand(message)) {
    deleteReusableApplicationAnswer(message)
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isFillHelperFieldsCommand(message)) {
    fillReviewedApplicationHelperFields(message, sender)
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isOpenReviewCommand(message)) {
    openCapturedReview()
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (isPrepareApplicationCommand(message)) {
    prepareCurrentApplication()
      .then(sendResponse)
      .catch((error) => sendResponse(publicFailure(error)));
    return true;
  }
  if (!isCaptureCommand(message)) {
    sendResponse({ ok: false, error_code: "UNSUPPORTED_COMMAND" });
    return false;
  }
  captureActiveTab(message.mode)
    .then(sendResponse)
    .catch((error) => sendResponse(publicFailure(error)));
  return true;
});

extensionApi.tabs.onUpdated.addListener((tabId, changeInfo, tab) => {
  if (typeof changeInfo.url === "string") {
    void invalidateHelperReviewsForTab(tabId);
    void removeDiscoveryReviewTarget(tabId);
  }
  if (changeInfo.status !== "complete" || typeof tab?.url !== "string") return;
  void restoreApplicationObserver({ ...tab, id: tabId });
});

extensionApi.tabs.onRemoved.addListener((tabId) => {
  void (async () => {
    await invalidateHelperReviewsForTab(tabId);
    await removeDiscoveryReviewTarget(tabId);
    const stored = await storedApplicationSession(tabId);
    if (!stored) return;
    await interruptStoredApplicationSession(stored, "tab_closed");
    await removeApplicationSession(tabId);
  })();
});
