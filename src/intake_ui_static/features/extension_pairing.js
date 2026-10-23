import { initExtensionSettings } from "./extension_settings.js";

// Extension pairing (ADR SEC-250 item 4). The code is shown only while this
// section is open and is discarded when Settings closes; it works once.
// Enablement (the amended item 4) is the sibling section above it and is
// initialised here so app.js keeps one entry point for the extension settings.
export function initExtensionPairing(api) {
  initExtensionSettings();
  const root = document.querySelector("#extension-pairing-settings");
  const panel = document.querySelector("#interface-settings-panel");
  if (!root || !panel) return;
  const details = root.querySelector("details");
  const code = root.querySelector("code");
  const status = root.querySelector('[role="status"]');
  const create = root.querySelector('[data-action="create"]');
  const revoke = root.querySelector('[data-action="revoke"]');
  let revision = 0;

  function clearCode() {
    revision++;
    code.textContent = "";
    code.hidden = true;
  }

  function render(state) {
    root.hidden = state.enabled !== true;
    revoke.hidden = state.paired !== true;
    create.disabled = false;
    status.textContent = state.paired
      ? `Paired ${new Date(state.paired_at).toLocaleString()}. Creating a new code replaces this pairing.`
      : "Not paired. Create a code, then type it in the extension side panel.";
  }

  async function load() {
    clearCode();
    const requested = revision;
    try {
      const state = await api.getExtensionPairing();
      if (requested === revision) render(state);
    } catch (_error) {
      if (requested === revision) status.textContent = "Pairing status is unavailable. Reopen Settings to retry.";
    }
  }

  create.addEventListener("click", async () => {
    clearCode();
    const requested = revision;
    create.disabled = true;
    try {
      const issued = await api.createExtensionPairingCode();
      if (requested !== revision) return;
      code.textContent = issued.code;
      code.hidden = false;
      const expires = new Date(issued.expires_at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
      status.textContent = `Type this code in the extension side panel before ${expires}. It works once.`;
    } catch (_error) {
      if (requested === revision) status.textContent = "A pairing code could not be created. Try again.";
    } finally {
      create.disabled = false;
    }
  });
  revoke.addEventListener("click", async () => {
    clearCode();
    revoke.disabled = true;
    try {
      render(await api.revokeExtensionPairing());
      status.textContent = "Unpaired. The extension must be paired again before it can reach Career Hub.";
    } catch (_error) {
      status.textContent = "The pairing could not be removed. Try again.";
    } finally {
      revoke.disabled = false;
    }
  });
  details.addEventListener("toggle", () => { if (details.open) void load(); else clearCode(); });
  // No network at app start: whether pairing applies is checked only when
  // Settings opens, because the extension endpoint is a launch option.
  panel.addEventListener("toggle", () => {
    if (!panel.matches(":popover-open")) clearCode();
    else if (details.open) void load();
    else void api.getExtensionPairing().then((state) => { root.hidden = state.enabled !== true; }, () => {});
  });
}
