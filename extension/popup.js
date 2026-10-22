"use strict";

const extensionApi = CareerHubBrowser.api;
const INSPECT_COMMAND = "career-hub.inspect-active-tab.v1";
const CAPTURE_COMMAND = "career-hub.capture-active-tab.v2";
const OPEN_REVIEW_COMMAND = "career-hub.open-discovery-review.v1";
const PREPARE_APPLICATION_COMMAND = "career-hub.prepare-application.v1";
const ARM_APPLICATION_COMMAND = "career-hub.arm-application-session.v1";
const OPEN_APPLICATIONS_COMMAND = "career-hub.open-applications.v1";
const BUNDLED_EXTENSION_ORIGIN = "chrome-extension://jhgaecfochimmafoopcpodncoeobecfg";

const captureView = document.querySelector("#capture-view");
const resultView = document.querySelector("#result-view");
const prepareApplicationButton = document.querySelector("#prepare-application-button");
const pageButton = document.querySelector("#page-button");
const selectionButton = document.querySelector("#selection-button");
const linkButton = document.querySelector("#link-button");
const retryButton = document.querySelector("#retry-button");
const reviewButton = document.querySelector("#review-button");
const resultApplicationsButton = document.querySelector("#result-applications-button");
const armApplicationButton = document.querySelector("#arm-application-button");
const openApplicationsButton = document.querySelector("#open-applications-button");
const helperButton = document.querySelector("#helper-button");
const applicationSessionStatus = document.querySelector("#application-session-status");
const extensionOriginPanel = document.querySelector("#extension-origin-panel");
const extensionOriginValue = document.querySelector("#extension-origin-value");
const extensionOriginCopy = document.querySelector("#extension-origin-copy");
const extensionOriginStatus = document.querySelector("#extension-origin-status");
const tabPreview = document.querySelector("#tab-preview");
const tabTitle = document.querySelector("#tab-title");
const tabHost = document.querySelector("#tab-host");
const selectionStatus = document.querySelector("#selection-status");
const resultTitle = document.querySelector("#result-title");
const resultMessage = document.querySelector("#result-message");
const assessmentReceipt = document.querySelector("#assessment-receipt");
const receiptStatus = document.querySelector("#receipt-status");
const receiptCount = document.querySelector("#receipt-count");
const receiptExtraStepCount = document.querySelector("#receipt-extra-step-count");
const receiptContactCandidateCount = document.querySelector("#receipt-contact-candidate-count");
const receiptFindings = document.querySelector("#receipt-findings");
const receiptNote = document.querySelector("#receipt-note");
const successIcon = document.querySelector("#success-icon");
const errorIcon = document.querySelector("#error-icon");
const actionButtons = [prepareApplicationButton, pageButton, selectionButton, linkButton];
let retryMode = "page";
let activeWindowId = null;
let activeTabId = null;

function activeHttpPage(tab) {
  if (!tab || typeof tab.url !== "string") {
    return null;
  }
  try {
    const url = new URL(tab.url);
    if (url.protocol !== "http:" && url.protocol !== "https:") {
      return null;
    }
    return {
      host: url.hostname,
      title: typeof tab.title === "string" && tab.title.trim()
        ? tab.title.replace(/\s+/g, " ").trim()
        : "Untitled page",
    };
  } catch {
    return null;
  }
}

function initializeExtensionOriginPanel() {
  const origin = CareerHubBrowser.extensionOrigin();
  const requiresExplicitPairing = origin !== BUNDLED_EXTENSION_ORIGIN;
  extensionOriginPanel.hidden = !requiresExplicitPairing;
  extensionOriginValue.textContent = requiresExplicitPairing ? origin : "";
}

async function copyExtensionOrigin() {
  extensionOriginCopy.disabled = true;
  extensionOriginStatus.textContent = "";
  try {
    await navigator.clipboard.writeText(extensionOriginValue.textContent);
    extensionOriginStatus.textContent = "Copied. Pass it to -ExtensionOrigin when starting Career Hub.";
  } catch {
    extensionOriginStatus.textContent = "Copy failed. Select the origin above and copy it manually.";
  } finally {
    extensionOriginCopy.disabled = false;
  }
}

function renderReceipt(receipt) {
  assessmentReceipt.hidden = false;
  assessmentReceipt.dataset.status = receipt.status;
  receiptStatus.textContent = receipt.label;
  receiptFindings.replaceChildren();
  receiptExtraStepCount.hidden = receipt.extra_step_count === 0;
  receiptExtraStepCount.textContent = receipt.extra_step_count
    ? `${receipt.extra_step_count} extra application ${receipt.extra_step_count === 1 ? "step" : "steps"} flagged`
    : "";
  receiptContactCandidateCount.hidden = receipt.contact_candidate_count === 0;
  receiptContactCandidateCount.textContent = receipt.contact_candidate_count
    ? `${receipt.contact_candidate_count} contact ${receipt.contact_candidate_count === 1 ? "candidate" : "candidates"} flagged`
    : "";
  if (receipt.status === "clear_conflict") {
    receiptCount.textContent = `${receipt.blocker_count} clear ${receipt.blocker_count === 1 ? "conflict" : "conflicts"}`;
    for (const blocker of receipt.blockers) {
      const item = document.createElement("li");
      const message = document.createElement("strong");
      message.textContent = blocker.message;
      const source = document.createElement("span");
      source.textContent = `JD line ${blocker.source_evidence.line}: “${blocker.source_evidence.text}”`;
      const fact = document.createElement("span");
      fact.textContent = `Profile: ${blocker.candidate_fact}`;
      item.append(message, source, fact);
      receiptFindings.append(item);
    }
    receiptNote.textContent = receipt.unknown_vocabulary_count
      ? `${receipt.unknown_vocabulary_count} unrecognized source ${receipt.unknown_vocabulary_count === 1 ? "phrase also needs" : "phrases also need"} review. No vocabulary was added and no application status was changed.`
      : "Advisory only. No application status was changed.";
    return;
  }
  if (receipt.status === "review") {
    receiptCount.textContent = receipt.review_count
      ? `${receipt.review_count} ${receipt.review_count === 1 ? "item" : "items"}`
      : "Preview unavailable";
    for (const review of receipt.review_items) {
      const item = document.createElement("li");
      const label = review.kind === "evidence_not_found"
        ? "Required evidence gap"
        : review.kind === "preferred_evidence_not_found"
          ? "Preferred evidence gap"
          : review.kind === "unknown_vocabulary"
            ? "Skill term to review"
            : "Review";
      const heading = document.createElement("strong");
      heading.textContent = label;
      const copy = document.createElement("span");
      copy.textContent = review.line
        ? `Line ${review.line}: ${review.text}`
        : review.text;
      item.append(heading, copy);
      receiptFindings.append(item);
    }
    receiptNote.textContent = receipt.unknown_vocabulary_count
      ? `${receipt.unknown_vocabulary_count} unrecognized source ${receipt.unknown_vocabulary_count === 1 ? "phrase needs" : "phrases need"} review. No vocabulary was added.`
      : "Review the retained source in Career Hub. No application status was changed.";
    return;
  }
  receiptCount.textContent = "";
  receiptNote.textContent = receipt.status === "unavailable"
    ? "A complete visible job description is required. No gaps were inferred from this partial capture."
    : "Based on the captured visible text. No application status was changed.";
}

function showResult(
  kind,
  title,
  message,
  retryable = false,
  receipt = null,
  reviewAvailable = false,
) {
  captureView.hidden = true;
  resultView.hidden = false;
  successIcon.toggleAttribute("hidden", kind !== "success");
  errorIcon.toggleAttribute("hidden", kind !== "error");
  resultView.dataset.kind = kind;
  resultTitle.textContent = title;
  resultMessage.textContent = message;
  assessmentReceipt.hidden = true;
  assessmentReceipt.removeAttribute("data-status");
  receiptFindings.replaceChildren();
  receiptExtraStepCount.hidden = true;
  receiptExtraStepCount.textContent = "";
  if (receipt) {
    renderReceipt(receipt);
  }
  retryButton.hidden = !retryable;
  reviewButton.hidden = kind !== "success" || !reviewAvailable;
  resultApplicationsButton.hidden = true;
  reviewButton.disabled = false;
  reviewButton.textContent = "Review in Career Hub";
  if (retryable) {
    retryButton.focus({ preventScroll: true });
  }
}

function renderApplicationSession(session) {
  armApplicationButton.hidden = false;
  openApplicationsButton.hidden = true;
  applicationSessionStatus.hidden = true;
  if (session?.status === "armed") {
    applicationSessionStatus.hidden = false;
    applicationSessionStatus.textContent = "Submission tracking is on for this tab. Career Hub will ask before changing status.";
    armApplicationButton.textContent = "Tracking on";
    armApplicationButton.disabled = true;
    return;
  }
  if (session?.status === "submission_detected") {
    applicationSessionStatus.hidden = false;
    applicationSessionStatus.textContent = "A likely successful submission is waiting for your confirmation. No status changed automatically.";
    armApplicationButton.hidden = true;
    openApplicationsButton.hidden = false;
    return;
  }
  if (session?.status === "interrupted") {
    applicationSessionStatus.hidden = false;
    applicationSessionStatus.textContent = "Tracking ended without a confirmed submission. You can start it again from this posting.";
  }
  armApplicationButton.textContent = "Track submission";
  armApplicationButton.disabled = false;
}

function successCopy(result) {
  if (result.capture_scope === "page_visible") {
    return [
      result.outcome === "created" ? "Page saved for review" : "Page capture updated",
      `${result.content_chars.toLocaleString()} visible characters captured. Review the source and job details in Career Hub.`,
    ];
  }
  if (result.capture_scope === "selection") {
    return [
      result.outcome === "created" ? "Selection saved" : "Selection updated",
      `${result.content_chars.toLocaleString()} characters captured and marked as partial.`,
    ];
  }
  if (result.outcome === "duplicate") {
    return ["Already in Job Feed", "This link is already saved as Needs JD."];
  }
  if (result.outcome === "updated") {
    return ["Updated in Job Feed", "Added this source to an existing Needs JD item."];
  }
  return ["Added to Job Feed", "Saved as Needs JD."];
}

function errorCopy(code) {
  if (code === "ACTIVE_TAB_UNAVAILABLE" || code === "ACTIVE_TAB_UNSUPPORTED") {
    return [
      "This page can’t be captured",
      "Open a normal HTTP or HTTPS job page, then try again.",
      false,
    ];
  }
  if (code === "PAGE_ACCESS_UNAVAILABLE") {
    return [
      "Page text isn’t available",
      "Save the link only, or try again on the visible job page.",
      false,
    ];
  }
  if (code === "CAPTURE_TOO_LARGE") {
    return [
      "Page is too large",
      "Select the exact job description text, then use Save selection.",
      false,
    ];
  }
  if (code === "CAPTURE_AMBIGUOUS") {
    return [
      "One job description wasn’t clear",
      "Select the exact job description text, then use Save selection.",
      false,
    ];
  }
  if (code === "CAPTURE_EMPTY") {
    return [
      "No job text found",
      "Open the job details or select the description, then try again.",
      false,
    ];
  }
  if (code === "EXTENSION_PAIRING_REQUIRED") {
    return [
      "Pair this extension first",
      "Open the Career Hub side panel and type the code from Career Hub Settings.",
      false,
    ];
  }
  if (code === "LOCAL_API_ACCESS_DENIED") {
    return [
      "Extension isn’t allowed",
      "Reload the unpacked extension, confirm its ID, and start Career Hub with -EnableExtension.",
      true,
    ];
  }
  if (code === "APPLICATION_SESSION_NOT_PENDING") {
    return [
      "Prepare this job first",
      "No single saved application matches this exact posting URL. Use Prepare application, then retry from the posting.",
      false,
    ];
  }
  if (code === "APPLICATION_SESSION_CLAIM_AMBIGUOUS") {
    return [
      "More than one session matches",
      "Return to Applications, cancel the extra session, and start again from the exact job.",
      false,
    ];
  }
  if (code === "APPLICATION_SESSION_INSTRUMENTATION_FAILED") {
    return [
      "This tab couldn’t be attended",
      "The application remained unchanged. Reload the job page and start a fresh session from Career Hub.",
      false,
    ];
  }
  if (code === "EXTENSION_CAPTURE_CONTRACT_UNSUPPORTED") {
    return [
      "Career Hub needs an update",
      "Update Career Hub or reload the latest extension files, then retry.",
      true,
    ];
  }
  if (code === "RESOURCE_NOT_FOUND") {
    return [
      "Career Hub needs a restart",
      "Restart the local server with -EnableExtension, then retry this capture.",
      true,
    ];
  }
  if (code === "APP_TIMEOUT") {
    return [
      "Career Hub took too long",
      "Confirm the local app is running, then retry.",
      true,
    ];
  }
  if (code === "APP_UNAVAILABLE") {
    return [
      "Career Hub isn’t reachable",
      "Start Career Hub with this extension origin, then retry.",
      true,
    ];
  }
  return [
    "Couldn’t save this page",
    "Keep Career Hub open and retry from this job page.",
    true,
  ];
}

function setActionsDisabled(disabled) {
  for (const button of actionButtons) {
    button.disabled = disabled || button.dataset.available === "false";
  }
}

async function loadActiveTabPreview() {
  const [tab] = await extensionApi.tabs.query({ active: true, currentWindow: true });
  activeWindowId = Number.isInteger(tab?.windowId) ? tab.windowId : null;
  activeTabId = Number.isSafeInteger(tab?.id) && tab.id >= 0 ? tab.id : null;
  const page = activeHttpPage(tab);
  if (!page) {
    activeTabId = null;
    tabPreview.dataset.invalid = "true";
    tabTitle.textContent = "Unsupported browser page";
    tabHost.textContent = "Open an HTTP or HTTPS job page";
    selectionStatus.textContent = "Page capture unavailable";
    for (const button of actionButtons) {
      button.dataset.available = "false";
      button.disabled = true;
    }
    armApplicationButton.disabled = true;
    helperButton.disabled = true;
    return;
  }
  helperButton.disabled = false;
  tabTitle.textContent = page.title;
  tabHost.textContent = page.host;
  linkButton.dataset.available = "true";
  linkButton.disabled = false;
  armApplicationButton.disabled = false;

  const inspection = await extensionApi.runtime.sendMessage({ command: INSPECT_COMMAND });
  if (!inspection || inspection.ok !== true) {
    pageButton.dataset.available = "false";
    prepareApplicationButton.dataset.available = "false";
    selectionButton.dataset.available = "false";
    selectionStatus.textContent = "Visible text unavailable · link capture still works";
    linkButton.disabled = false;
    linkButton.focus();
    return;
  }
  renderApplicationSession(inspection.application_session);
  pageButton.dataset.available = inspection.page_capture_available ? "true" : "false";
  pageButton.disabled = !inspection.page_capture_available;
  prepareApplicationButton.dataset.available = inspection.page_capture_available ? "true" : "false";
  prepareApplicationButton.disabled = !inspection.page_capture_available;
  const selectionChars = Number(inspection.selection_chars) || 0;
  const selectionAvailable = selectionChars > 0 && inspection.selection_too_large !== true;
  selectionButton.dataset.available = selectionAvailable ? "true" : "false";
  selectionButton.disabled = !selectionAvailable;
  if (inspection.selection_too_large === true) {
    selectionStatus.textContent = "Selection is too large · narrow it before saving";
  } else if (selectionAvailable) {
    selectionStatus.textContent = `${selectionChars.toLocaleString()} selected characters ready`;
  } else {
    selectionStatus.textContent = "Select job text to enable Save selection";
  }
  if (inspection.page_capture_available) {
    pageButton.focus();
  } else {
    linkButton.focus();
  }
}

async function armApplicationSession() {
  armApplicationButton.disabled = true;
  const originalText = armApplicationButton.textContent;
  armApplicationButton.textContent = "Starting…";
  try {
    const result = await extensionApi.runtime.sendMessage({ command: ARM_APPLICATION_COMMAND });
    if (!result || result.ok !== true) {
      const [title, message] = errorCopy(result?.error_code);
      showResult("error", title, message, false);
      if (["APPLICATION_SESSION_NOT_PENDING", "APPLICATION_SESSION_CLAIM_AMBIGUOUS"].includes(result?.error_code)) {
        resultApplicationsButton.hidden = false;
      }
      return;
    }
    showResult(
      "success",
      "Submission tracking is on",
      "Apply normally in this tab. Career Hub records no form values and will ask before marking the application Applied.",
      false,
    );
  } catch {
    const [title, message] = errorCopy("APP_UNAVAILABLE");
    showResult("error", title, message, false);
  } finally {
    armApplicationButton.textContent = originalText;
  }
}

async function prepareApplication() {
  setActionsDisabled(true);
  const originalText = prepareApplicationButton.textContent;
  prepareApplicationButton.dataset.loading = "true";
  prepareApplicationButton.textContent = "Opening workspace…";
  try {
    const result = await extensionApi.runtime.sendMessage({
      command: PREPARE_APPLICATION_COMMAND,
    });
    if (!result || result.ok !== true) {
      const [title, message, retryable] = errorCopy(result?.error_code);
      showResult("error", title, message, retryable);
      return;
    }
    window.close();
  } catch {
    const [title, message, retryable] = errorCopy("APP_UNAVAILABLE");
    showResult("error", title, message, retryable);
  } finally {
    prepareApplicationButton.dataset.loading = "false";
    prepareApplicationButton.textContent = originalText;
    setActionsDisabled(false);
  }
}

async function openApplications() {
  const buttons = [openApplicationsButton, resultApplicationsButton];
  buttons.forEach((button) => { button.disabled = true; });
  try {
    const result = await extensionApi.runtime.sendMessage({ command: OPEN_APPLICATIONS_COMMAND });
    if (!result || result.ok !== true) throw new Error("open failed");
    window.close();
  } catch {
    applicationSessionStatus.textContent = "Career Hub could not open Applications. Keep the local app running and try again.";
    buttons.forEach((button) => { button.disabled = false; });
  }
}

function openApplicationHelper() {
  helperButton.disabled = true;
  CareerHubBrowser.openApplicationHelperSurface(activeWindowId, activeTabId)
    .then(() => window.close())
    .catch(() => {
      showResult(
        "error",
        "Application helper couldn’t open",
        "Keep this browser window open and try again.",
        false,
      );
      helperButton.disabled = false;
    });
}

async function captureCurrentTab(mode) {
  retryMode = mode;
  setActionsDisabled(true);
  const activeButton = {
    link: linkButton,
    page: pageButton,
    selection: selectionButton,
  }[mode];
  const originalText = activeButton.textContent;
  activeButton.dataset.loading = "true";
  activeButton.textContent = "Saving…";
  try {
    const result = await extensionApi.runtime.sendMessage({
      command: CAPTURE_COMMAND,
      mode,
    });
    if (!result || result.ok !== true) {
      const [title, message, retryable] = errorCopy(result?.error_code);
      showResult("error", title, message, retryable);
      return;
    }
    const [title, message] = successCopy(result);
    showResult(
      "success",
      title,
      message,
      false,
      result.receipt,
      result.review_available === true,
    );
  } catch {
    const [title, message, retryable] = errorCopy("APP_UNAVAILABLE");
    showResult("error", title, message, retryable);
  } finally {
    activeButton.dataset.loading = "false";
    activeButton.textContent = originalText;
    setActionsDisabled(false);
  }
}

async function openReview() {
  reviewButton.disabled = true;
  reviewButton.textContent = "Opening…";
  try {
    const result = await extensionApi.runtime.sendMessage({ command: OPEN_REVIEW_COMMAND });
    if (!result || result.ok !== true) {
      resultMessage.textContent = result?.error_code === "REVIEW_TARGET_UNAVAILABLE"
        ? "Capture this page again to restore its exact local review link."
        : "Career Hub could not open this saved capture. Keep the local app running and try again.";
      reviewButton.disabled = false;
      reviewButton.textContent = "Try opening again";
      return;
    }
    window.close();
  } catch {
    resultMessage.textContent = "Career Hub could not open this saved capture. Keep the local app running and try again.";
    reviewButton.disabled = false;
    reviewButton.textContent = "Try opening again";
  }
}

pageButton.addEventListener("click", () => captureCurrentTab("page"));
selectionButton.addEventListener("click", () => captureCurrentTab("selection"));
linkButton.addEventListener("click", () => captureCurrentTab("link"));
prepareApplicationButton.addEventListener("click", prepareApplication);
retryButton.addEventListener("click", () => captureCurrentTab(retryMode));
reviewButton.addEventListener("click", openReview);
armApplicationButton.addEventListener("click", armApplicationSession);
openApplicationsButton.addEventListener("click", openApplications);
resultApplicationsButton.addEventListener("click", openApplications);
helperButton.addEventListener("click", openApplicationHelper);
extensionOriginCopy.addEventListener("click", copyExtensionOrigin);
initializeExtensionOriginPanel();
loadActiveTabPreview().catch(() => {
  showResult(
    "error",
    "Current tab isn’t available",
    "Close this popup, open a normal job page, and try again.",
  );
});
