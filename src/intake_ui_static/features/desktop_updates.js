const UPDATE_PHASES = new Set([
  "checking",
  "downloading",
  "error",
  "idle",
  "installing",
  "ready",
  "unavailable",
  "up_to_date",
]);

export function versionLabel(state) {
  const commit = typeof state.currentCommit === "string" && /^[0-9a-f]{7}$/.test(state.currentCommit)
    ? ` (${state.currentCommit})` : "";
  return `v${state.currentVersion}${commit}`;
}

function updateCopy(state) {
  if (!state.supported) {
    return {
      title: "Updates are unavailable on this build.",
      detail: "Use the supported installer or package manager for this platform.",
    };
  }
  if (!state.configured) {
    return {
      title: "Update feed is not configured.",
      detail: "This development build must be updated with a newer installer.",
    };
  }
  switch (state.phase) {
    case "checking":
      return { title: "Checking for updates…", detail: "Your current version stays available while Career Hub checks." };
    case "downloading":
      return { title: "Downloading update…", detail: "You can keep working. Career Hub will ask before restarting." };
    case "up_to_date":
      return { title: "Career Hub is up to date.", detail: `Version ${state.currentVersion}` };
    case "ready":
      return {
        title: state.releaseName ? `Update ${state.releaseName} is ready.` : "An update is ready.",
        detail: "Restart to install it. Your Data Home stays in place.",
      };
    case "installing":
      return { title: "Preparing to restart…", detail: "Career Hub is closing its local service before installing." };
    case "error":
      return { title: "Update check failed.", detail: "Try again later. Your current version is unchanged." };
    default:
      return {
        title: state.automaticUpdates ? "Automatic updates are on." : "Automatic updates are off.",
        detail: state.automaticUpdates
          ? "Career Hub checks quietly in the background."
          : "Use Check now whenever you want to look for a newer version.",
      };
  }
}

function validState(payload) {
  return Boolean(
    payload &&
    typeof payload === "object" &&
    typeof payload.automaticUpdates === "boolean" &&
    typeof payload.canCheck === "boolean" &&
    typeof payload.canInstall === "boolean" &&
    typeof payload.configured === "boolean" &&
    typeof payload.currentVersion === "string" &&
    typeof payload.supported === "boolean" &&
    UPDATE_PHASES.has(payload.phase),
  );
}

export function initDesktopUpdates() {
  const updates = window.careerHubDesktop?.updates;
  const root = document.querySelector("#desktop-update-settings");
  if (!updates || !root) return;

  const automatic = root.querySelector("#desktop-update-automatic");
  const check = root.querySelector("#desktop-update-check");
  const detail = root.querySelector("#desktop-update-status-detail");
  const install = root.querySelector("#desktop-update-install");
  const progress = root.querySelector("#desktop-update-progress");
  const statusTitle = root.querySelector("#desktop-update-status-title");
  const version = root.querySelector("#desktop-update-version");
  let requestPending = false;
  let currentState = null;

  function render(state) {
    if (!validState(state)) return;
    currentState = state;
    root.hidden = false;
    automatic.checked = state.automaticUpdates;
    automatic.disabled = requestPending || !state.supported;
    check.disabled = requestPending || !state.canCheck;
    install.hidden = !state.canInstall;
    install.disabled = requestPending || !state.canInstall;
    version.textContent = versionLabel(state);
    const copy = updateCopy(state);
    statusTitle.textContent = copy.title;
    detail.textContent = copy.detail;

    const activeProgress = ["checking", "downloading", "installing", "ready"].includes(state.phase);
    progress.hidden = !activeProgress;
    if (state.phase === "ready") {
      progress.value = 100;
    } else {
      progress.removeAttribute("value");
    }
  }

  function renderRequestFailure() {
    root.hidden = false;
    statusTitle.textContent = "Update controls could not respond.";
    detail.textContent = "Restart Career Hub, then try once more. Your current version is unchanged.";
    progress.hidden = true;
  }

  async function perform(action) {
    if (requestPending) return;
    requestPending = true;
    let failed = false;
    if (currentState) render(currentState);
    try {
      const nextState = await action();
      if (validState(nextState)) render(nextState);
    } catch (_error) {
      failed = true;
      renderRequestFailure();
    } finally {
      requestPending = false;
      if (!failed && currentState) render(currentState);
    }
  }

  automatic.addEventListener("change", () => {
    void perform(() => updates.setAutomatic(automatic.checked));
  });
  check.addEventListener("click", () => {
    void perform(() => updates.check());
  });
  install.addEventListener("click", () => {
    void perform(() => updates.install());
  });

  const unsubscribe = updates.onState(render);
  if (typeof unsubscribe === "function") {
    window.addEventListener("beforeunload", unsubscribe, { once: true });
  }
  void updates.getState().then(render).catch(renderRequestFailure);
}
