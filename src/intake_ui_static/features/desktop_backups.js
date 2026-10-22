export function initDesktopBackups() {
  initDesktopRestore();
  const bridge = window.careerHubDesktop?.backups;
  const root = document.querySelector("#desktop-backup-settings");
  if (!bridge || !root) return;
  const confirm = root.querySelector("#desktop-backup-confirm");
  const start = root.querySelector("#desktop-backup-start");
  const status = root.querySelector("#desktop-backup-status");
  const path = root.querySelector("#desktop-backup-path");
  const errorCode = root.querySelector("#desktop-backup-error");
  let current = null;
  let pending = false;

  function render(state) {
    if (!state || !["idle", "running", "complete", "error"].includes(state.phase) ||
        typeof state.available !== "boolean" || typeof state.canStart !== "boolean" ||
        !Number.isSafeInteger(state.revision) || (current && state.revision < current.revision) ||
        (current && state.revision === current.revision && current.phase !== "running" && state.phase === "running")) return;
    current = state;
    root.hidden = !state.available;
    start.disabled = pending || !state.canStart || !confirm.checked;
    confirm.disabled = pending || state.phase === "running";
    root.setAttribute("aria-busy", String(pending || state.phase === "running"));
    const published = state.backup?.archivePublication === "published";
    status.textContent = state.phase === "idle" ? "Save your work before starting. The local service will briefly restart."
      : state.phase === "running" ? "Preparing and verifying a local backup…"
      : state.phase === "complete" ? "Verified backup saved; its temporary copy was removed. The local service restarted."
      : published ? state.backup?.snapshotCleanup?.status === "incomplete"
        ? "Backup saved, but temporary-copy cleanup did not finish cleanly. Keep the ZIP; do not repeat the backup to retry cleanup."
        : "Backup saved, but the operation needs attention. See the error code below before trying again."
      : "Backup did not finish cleanly. It may have left files; no automatic retry was made.";
    path.textContent = published ? state.backup.archiveRelativePath : "";
    path.hidden = !published;
    errorCode.textContent = [...new Set([state.backup?.errorCode, state.errorCode, state.restartErrorCode].filter(Boolean))].join(" · ");
    errorCode.hidden = !errorCode.textContent;
  }
  confirm.addEventListener("change", () => { if (current) render(current); });
  start.addEventListener("click", async () => {
    if (pending || !current?.canStart || !confirm.checked) return;
    pending = true;
    render(current);
    status.textContent = "Opening the backup progress screen…";
    try {
      await bridge.start({ confirmWritersStopped: true });
    } catch (_error) {
      // The main process may already own an operation when navigation destroys
      // this invocation. Never treat transport failure as proof of no writes.
      status.textContent = "Backup request could not be confirmed. Check the desktop progress screen; do not repeat the request.";
    } finally {
      pending = false;
      confirm.checked = false;
      start.disabled = true;
      confirm.disabled = false;
      root.setAttribute("aria-busy", "false");
    }
  });
  const unsubscribe = bridge.onState(render);
  if (typeof unsubscribe === "function") window.addEventListener("beforeunload", unsubscribe, { once: true });
  void bridge.getState().then(render).catch(() => {
    // An absent/incompatible desktop host must not expose an unusable control.
    root.hidden = true;
  });
}

function initDesktopRestore() {
  const bridge = window.careerHubDesktop?.restores;
  const root = document.querySelector("#desktop-restore-settings");
  if (!bridge || !root) return;
  const choose = root.querySelector("#desktop-restore-choose"), start = root.querySelector("#desktop-restore-start");
  const confirm = root.querySelector("#desktop-restore-confirm"), status = root.querySelector("#desktop-restore-status");
  const archive = root.querySelector("#desktop-restore-path"), errorCode = root.querySelector("#desktop-restore-error");
  const rank = { selecting: 1, idle: 2, ready: 2, running: 3, complete: 4, error: 4 };
  let current = null, pending = false;
  function render(state) {
    if (!state || !Object.hasOwn(rank, state.phase) || !Number.isSafeInteger(state.revision) ||
        typeof state.available !== "boolean" || typeof state.canChoose !== "boolean" || typeof state.canStart !== "boolean" ||
        current && (state.revision < current.revision || state.revision === current.revision && rank[state.phase] < rank[current.phase])) return;
    if (!current || current.revision !== state.revision || state.phase !== "ready") confirm.checked = false;
    current = state;
    root.hidden = !state.available;
    choose.disabled = pending || !state.canChoose;
    confirm.disabled = pending || !state.canStart;
    start.disabled = pending || !state.canStart || !confirm.checked;
    root.setAttribute("aria-busy", String(pending || ["selecting", "running"].includes(state.phase)));
    archive.textContent = state.archiveRelativePath || "";
    archive.hidden = !archive.textContent;
    errorCode.textContent = [...new Set([state.restore?.errorCode, state.errorCode, state.restartErrorCode].filter(Boolean))].join(" · ");
    errorCode.hidden = !errorCode.textContent;
    status.textContent = state.phase === "ready" ? "Review the selected archive and confirm before restoring."
      : state.phase === "selecting" ? "Choose a ZIP inside this workspace’s backups folder."
      : state.phase === "running" ? "Restoring and verifying the selected archive…"
      : state.phase === "complete" ? "Restore verified; temporary validation copies removed. The local service restarted."
      : state.phase === "error" ? state.errorCode === "DESKTOP_RESTORE_ARCHIVE_OUTSIDE_HOME"
        ? "Choose a ZIP inside this workspace’s backups folder. External archive import is not available."
        : state.restore?.complete ? "Restore committed, but the operation needs attention. Do not repeat restore to clean temporary files; inspect the result."
        : "Restoration did not finish cleanly. Inspect the result before selecting another archive."
      : "Choose an archive before confirming restoration.";
  }
  confirm.addEventListener("change", () => { if (current) render(current); });
  choose.addEventListener("click", async () => {
    if (pending || !current?.canChoose) return;
    pending = true; confirm.checked = false; render(current);
    try { await bridge.choose(); } catch (_error) { status.textContent = "Archive selection could not be confirmed. No restore was requested."; }
    finally { pending = false; try { render(await bridge.getState()); } catch (_error) { choose.disabled = false; start.disabled = true; } }
  });
  start.addEventListener("click", async () => {
    if (pending || !current?.canStart || !confirm.checked) return;
    const selectionRevision = current.revision;
    pending = true; render(current);
    try { await bridge.start({ confirmWritersStopped: true, selectionRevision }); }
    catch (_error) { status.textContent = "Restore request could not be confirmed. Check the desktop progress screen; do not repeat the request."; }
    finally {
      pending = false; confirm.checked = false; start.disabled = true;
      try { render(await bridge.getState()); } catch (_error) { confirm.disabled = true; choose.disabled = true; }
    }
  });
  const unsubscribe = bridge.onState(render);
  if (typeof unsubscribe === "function") window.addEventListener("beforeunload", unsubscribe, { once: true });
  void bridge.getState().then(render).catch(() => { root.hidden = true; });
}
