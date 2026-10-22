const OFFLINE_MESSAGE = "Offline — local features remain available. Internet actions will wait for a connection.";
const CONNECTION_PROBLEM_MESSAGE = "Internet action could not connect — local features remain available. Check the connection and retry.";
const CONNECTION_AVAILABLE_MESSAGE = "Connection available — retry internet actions when ready.";
const OFFLINE_ACTION_MESSAGE = "Unavailable while offline. Local features remain available.";

const NETWORK_FAILURE_CODES = new Set([
  "EMAIL_SYNC_NETWORK_FAILED",
  "PUBLIC_JOB_RESOLUTION_CONNECT_FAILED",
  "PUBLIC_JOB_RESOLUTION_TIMEOUT",
]);

let browserOffline = globalThis.navigator?.onLine === false;
let reportedNetworkFailure = false;
let initialized = false;
let banner = null;
let bannerMessage = null;
let recoveryTimer = null;
let actionObserver = null;
const listeners = new Set();

export function classifyInternetFailureCode(errorCode) {
  return typeof errorCode === "string" && NETWORK_FAILURE_CODES.has(errorCode);
}

export function connectivityMessage(state) {
  return {
    available: CONNECTION_AVAILABLE_MESSAGE,
    offline: OFFLINE_MESSAGE,
    unreachable: CONNECTION_PROBLEM_MESSAGE,
  }[state] || "";
}

export function connectivitySnapshot() {
  const state = browserOffline
    ? "offline"
    : reportedNetworkFailure
      ? "unreachable"
      : "available";
  return Object.freeze({
    internetActionsAvailable: !browserOffline,
    message: connectivityMessage(state),
    state,
  });
}

export function isOffline() {
  return browserOffline;
}

function notify() {
  const snapshot = connectivitySnapshot();
  listeners.forEach((listener) => listener(snapshot));
}

export function onConnectivityChange(listener) {
  if (typeof listener !== "function") {
    throw new TypeError("Connectivity listener must be a function");
  }
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function setActionOfflineState(action) {
  if (!(action instanceof Element) || !action.matches("[data-network-action]")) return;
  if (browserOffline) {
    action.setAttribute("aria-disabled", "true");
    if (!Object.hasOwn(action.dataset, "connectivityTooltip")) {
      action.dataset.connectivityTooltip = action.dataset.tooltip || "";
      action.dataset.connectivityTitle = action.title || "";
    }
    action.dataset.tooltip = OFFLINE_ACTION_MESSAGE;
    action.title = OFFLINE_ACTION_MESSAGE;
    return;
  }
  action.removeAttribute("aria-disabled");
  if (Object.hasOwn(action.dataset, "connectivityTooltip")) {
    const tooltip = action.dataset.connectivityTooltip;
    const title = action.dataset.connectivityTitle;
    if (tooltip) action.dataset.tooltip = tooltip;
    else delete action.dataset.tooltip;
    if (title) action.title = title;
    else action.removeAttribute("title");
    delete action.dataset.connectivityTooltip;
    delete action.dataset.connectivityTitle;
  }
}

function syncNetworkActions(root = document) {
  if (root instanceof Element) setActionOfflineState(root);
  root.querySelectorAll?.("[data-network-action]").forEach(setActionOfflineState);
}

function clearRecoveryTimer() {
  if (recoveryTimer !== null) {
    window.clearTimeout(recoveryTimer);
    recoveryTimer = null;
  }
}

function showPersistentState() {
  if (!banner || !bannerMessage) return;
  clearRecoveryTimer();
  const snapshot = connectivitySnapshot();
  banner.dataset.state = snapshot.state;
  bannerMessage.textContent = snapshot.message;
  banner.hidden = snapshot.state === "available";
}

function showConnectionAvailable() {
  if (!banner || !bannerMessage) return;
  clearRecoveryTimer();
  banner.dataset.state = "available";
  bannerMessage.textContent = CONNECTION_AVAILABLE_MESSAGE;
  banner.hidden = false;
  recoveryTimer = window.setTimeout(() => {
    recoveryTimer = null;
    if (connectivitySnapshot().state === "available") banner.hidden = true;
  }, 5_000);
}

function handleOffline() {
  browserOffline = true;
  showPersistentState();
  syncNetworkActions();
  notify();
}

function handleOnline() {
  const recovered = browserOffline || reportedNetworkFailure;
  browserOffline = false;
  reportedNetworkFailure = false;
  syncNetworkActions();
  if (recovered) showConnectionAvailable();
  else showPersistentState();
  notify();
}

function blockOfflineNetworkAction(event) {
  if (!browserOffline) return;
  const action = event.target.closest?.("[data-network-action]");
  if (!action) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  showPersistentState();
}

export function reportInternetActionFailure(error) {
  const errorCode = error?.payload?.error_code || error?.code || "";
  if (!classifyInternetFailureCode(errorCode)) return false;
  if (!browserOffline) reportedNetworkFailure = true;
  showPersistentState();
  notify();
  return true;
}

export function reportInternetActionSuccess() {
  if (browserOffline || !reportedNetworkFailure) return;
  reportedNetworkFailure = false;
  showConnectionAvailable();
  notify();
}

export function initializeConnectivity() {
  if (initialized) return connectivitySnapshot();
  initialized = true;
  browserOffline = navigator.onLine === false;
  banner = document.querySelector("#connectivity-banner");
  bannerMessage = document.querySelector("#connectivity-message");
  if (!banner || !bannerMessage) {
    throw new Error("Connectivity status surface is missing");
  }

  window.addEventListener("offline", handleOffline);
  window.addEventListener("online", handleOnline);
  document.addEventListener("click", blockOfflineNetworkAction, true);
  document.addEventListener("auxclick", blockOfflineNetworkAction, true);
  actionObserver = new MutationObserver((mutations) => {
    mutations.forEach((mutation) => {
      mutation.addedNodes.forEach((node) => syncNetworkActions(node));
    });
  });
  actionObserver.observe(document.body, { childList: true, subtree: true });
  syncNetworkActions();
  showPersistentState();
  return connectivitySnapshot();
}

export const connectivityCopy = Object.freeze({
  available: CONNECTION_AVAILABLE_MESSAGE,
  offline: OFFLINE_MESSAGE,
  offlineAction: OFFLINE_ACTION_MESSAGE,
  unreachable: CONNECTION_PROBLEM_MESSAGE,
});
