// Browser extension enablement (ADR SEC-250 item 4, amended). The desktop host
// owns the setting; this window only reads it back and asks for changes. A
// change takes effect after Career Hub restarts, and pairing still gates every
// request from the extension.
const ORIGIN_PATTERN = /^(chrome-extension|moz-extension|safari-web-extension):\/\/[A-Za-z0-9._-]{1,255}\/?$/;

export function parseOriginList(value) {
  const origins = [];
  for (const raw of String(value || "").split(/[\s,]+/)) {
    const candidate = raw.trim();
    if (!candidate) continue;
    if (!ORIGIN_PATTERN.test(candidate)) return null;
    const normalized = candidate.replace(/\/$/, "");
    if (!origins.includes(normalized)) origins.push(normalized);
  }
  return origins;
}

export function extensionStatusCopy(state) {
  if (state.readable === false) {
    return "The saved extension setting could not be read, so the extension stays off. Save it again to repair it.";
  }
  if (state.restartRequired) {
    return "Restart Career Hub to apply this change.";
  }
  if (state.runningEnabled) {
    return "On. The extension can connect on port 8766 once it is paired below.";
  }
  if (state.launchOrigins.length) {
    return "On for this launch only, from the host's start arguments.";
  }
  return "Off. Ordinary startup admits no browser extension.";
}

export function initExtensionSettings() {
  const bridge = typeof window === "undefined" ? undefined : window.careerHubDesktop?.extension;
  const root = bridge ? document.querySelector("#extension-enable-settings") : null;
  if (!bridge || !root) return;
  const enabled = root.querySelector("#extension-enable-toggle");
  const origins = root.querySelector("#extension-extra-origins");
  const status = root.querySelector('[role="status"]');
  const restart = root.querySelector('[data-action="restart"]');
  const save = root.querySelector('[data-action="save-origins"]');
  let requestPending = false;
  let currentState = null;

  function render(state) {
    if (!state || typeof state !== "object") return;
    currentState = state;
    root.hidden = false;
    enabled.checked = state.enabled === true;
    enabled.disabled = requestPending;
    if (document.activeElement !== origins) {
      origins.value = Array.isArray(state.origins) ? state.origins.join("\n") : "";
    }
    origins.disabled = requestPending || state.enabled !== true;
    save.disabled = requestPending || state.enabled !== true;
    restart.hidden = state.restartRequired !== true;
    restart.disabled = requestPending;
    status.textContent = typeof state.error === "string"
      ? "That setting could not be saved. Check each origin and try again."
      : extensionStatusCopy(state);
  }

  function renderFailure() {
    root.hidden = false;
    status.textContent = "Extension settings could not respond. Restart Career Hub, then try again.";
  }

  async function perform(action) {
    if (requestPending) return;
    requestPending = true;
    if (currentState) render(currentState);
    try {
      render(await action());
    } catch (_error) {
      renderFailure();
    } finally {
      requestPending = false;
      if (currentState) render(currentState);
    }
  }

  function requestedOrigins() {
    const parsed = parseOriginList(origins.value);
    if (parsed === null) {
      status.textContent = "Each origin must look like chrome-extension://<id>, moz-extension://<uuid> or safari-web-extension://<id>.";
      return null;
    }
    return parsed;
  }

  enabled.addEventListener("change", () => {
    // Read the click before the pending re-render restores the saved state.
    const nextEnabled = enabled.checked;
    const parsed = requestedOrigins() ?? (currentState?.origins || []);
    void perform(() => bridge.set({ enabled: nextEnabled, origins: parsed }));
  });
  save.addEventListener("click", () => {
    const nextEnabled = enabled.checked;
    const parsed = requestedOrigins();
    if (parsed === null) return;
    void perform(() => bridge.set({ enabled: nextEnabled, origins: parsed }));
  });
  restart.addEventListener("click", () => {
    restart.disabled = true;
    status.textContent = "Restarting Career Hub…";
    void bridge.relaunch().catch(() => {
      restart.disabled = false;
      status.textContent = "Career Hub could not restart now. Finish the running backup or restore, then try again.";
    });
  });

  void bridge.getState().then(render).catch(renderFailure);
}
