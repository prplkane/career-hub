// Connection discovery only. Commands still require an explicit user/agent action.
export function initDesktopAgent() {
  const bridge = window.careerHubDesktop?.agent;
  const root = document.querySelector("#desktop-agent-settings");
  const panel = document.querySelector("#interface-settings-panel");
  if (!bridge || !root || !panel) return;
  const details = root.querySelector("details");
  const output = root.querySelector("textarea");
  const status = root.querySelector('[role="status"]');
  const copy = root.querySelector("button");
  let revision = 0;
  root.hidden = false;

  function clear() {
    revision++;
    output.value = "";
    output.hidden = true;
    copy.disabled = true;
    root.setAttribute("aria-busy", "false");
  }

  async function load(copyRequested = false) {
    clear();
    const requested = revision;
    status.textContent = "Checking the current local connection…";
    root.setAttribute("aria-busy", "true");
    let timer;
    try {
      const state = await Promise.race([
        copyRequested ? bridge.copyConnection() : bridge.getConnection(),
        new Promise((_, reject) => { timer = setTimeout(() => reject(new Error("timeout")), 5000); }),
      ]);
      if (requested !== revision) return null;
      if (!state?.available) {
        if (state?.reason === "not_supported") root.hidden = true;
        status.textContent = state?.reason === "package_changed"
          ? "The installed client changed or could not be verified. Restart Career Hub before connecting."
          : "The local service is unavailable. Reopen Settings after it restarts.";
        return null;
      }
      const connection = { executablePath: state.executablePath, server: state.server,
        connection: state.connection, contractVersion: state.contractVersion, commands: state.commands };
      output.value = JSON.stringify(connection, null, 2);
      output.hidden = false;
      copy.disabled = false;
      status.textContent = "Keep this app open. Re-copy after a restart; the server address can change.";
      return { text: output.value, copied: state.copied === true };
    } catch (_error) {
      if (requested === revision) status.textContent = "Could not read the connection. Close and reopen this section to retry.";
      return null;
    } finally {
      clearTimeout(timer);
      if (requested === revision) root.setAttribute("aria-busy", "false");
    }
  }

  details.addEventListener("toggle", () => { if (details.open) void load(); else clear(); });
  panel.addEventListener("toggle", () => {
    if (!panel.matches(":popover-open")) clear();
    else if (details.open) void load();
  });
  copy.addEventListener("click", async () => {
    if (copy.disabled) return;
    const result = await load(true);
    if (!result) return;
    if (result.copied) {
      status.textContent = "Connection copied. No access token or profile data is included.";
    } else {
      output.focus(); output.select();
      status.textContent = "Automatic copy is unavailable. Copy the selected connection details manually.";
    }
  });
}
