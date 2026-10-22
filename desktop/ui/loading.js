"use strict";

const params = new URLSearchParams(window.location.search);
const state = params.get("state") || "starting";
const errorCode = params.get("error_code") || "";
const message = document.querySelector("#startup-message");
const code = document.querySelector("#startup-code");
const retry = document.querySelector("#retry-startup");
const migrationPanel = document.querySelector("#migration-panel");
const reviewMigration = document.querySelector("#review-migration");
const migrationConfirmation = document.querySelector("#migration-confirmation");
const migrationSummary = document.querySelector("#migration-summary");
const applyMigration = document.querySelector("#apply-migration");
const recoveryPanel = document.querySelector("#recovery-panel");
const reviewRecovery = document.querySelector("#review-recovery");
const recoveryConfirmation = document.querySelector("#recovery-confirmation");
const recoverySummary = document.querySelector("#recovery-summary");
const recoveryNote = document.querySelector("#recovery-note");
const advanceRecovery = document.querySelector("#advance-recovery");
const offlineIndicator = document.querySelector("#offline-indicator");

if (state === "ready") {
  message.textContent = "The local service is ready. Opening Career Hub…";
} else if (state === "error") {
  const migrationRequired = errorCode === "DATA_HOME_MIGRATION_REQUIRED";
  const recoveryRequired = errorCode === "DATA_HOME_RECOVERY_REQUIRED";
  const permissionsUnsafe = errorCode === "DATA_HOME_PERMISSIONS_UNSAFE";
  message.textContent = migrationRequired
    ? "This workspace was created by an earlier Career Hub build and needs a reviewed local update before it can open."
    : permissionsUnsafe
      ? "Career Hub stopped because the permissions or owner of your data folder changed, so other accounts on this PC might be able to read it. Nothing was changed or deleted. See “Repairing Data Home permissions” in the private data guide."
      : "Career Hub’s local service did not start. Your data was not removed or reset.";
  code.textContent = errorCode;
  code.hidden = errorCode.length === 0;
  migrationPanel.hidden = !migrationRequired || !window.careerHubDesktop;
  recoveryPanel.hidden = true;
  retry.hidden = migrationRequired || !window.careerHubDesktop;

  if (
    recoveryRequired &&
    typeof window.careerHubDesktop?.getDataHomeRecoveryAvailability === "function"
  ) {
    void window.careerHubDesktop
      .getDataHomeRecoveryAvailability()
      .then((result) => {
        if (result?.available !== true) return;
        message.textContent =
          "Career Hub stopped before opening because a local save was interrupted.";
        recoveryPanel.hidden = false;
        retry.hidden = true;
      })
      .catch(() => {});
  }
}

retry.addEventListener("click", async () => {
  retry.disabled = true;
  message.textContent = "Retrying the private local service…";
  code.hidden = true;
  try {
    await window.careerHubDesktop.retryStartup();
  } catch (_error) {
    message.textContent = "Startup retry failed. You can retry again or close the app.";
    retry.disabled = false;
  }
});

function formatBytes(bytes) {
  if (!Number.isSafeInteger(bytes) || bytes <= 0) return "0 bytes";
  const units = ["bytes", "KB", "MB", "GB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value >= 10 || unit === 0 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`;
}

reviewMigration.addEventListener("click", async () => {
  reviewMigration.disabled = true;
  message.textContent = "Checking the existing local document store…";
  code.hidden = true;
  try {
    const plan = await window.careerHubDesktop.planDataHomeMigration();
    const documentText =
      plan.documentCount === 0
        ? "No saved document files need conversion."
        : `${plan.documentCount} saved document${plan.documentCount === 1 ? "" : "s"} (${formatBytes(plan.totalCiphertextBytes)}) will be re-encrypted locally.`;
    migrationSummary.textContent = `Format ${plan.sourceFormatVersion} → ${plan.targetFormatVersion}. ${documentText}`;
    migrationConfirmation.hidden = false;
    reviewMigration.hidden = true;
    message.textContent = "The update plan is ready for your confirmation.";
  } catch (_error) {
    if (document.body.contains(reviewMigration)) {
      message.textContent =
        "Career Hub could not verify the update plan. No migration was applied.";
      reviewMigration.disabled = false;
      code.hidden = false;
    }
  }
});

applyMigration.addEventListener("click", async () => {
  applyMigration.disabled = true;
  message.textContent =
    "Creating a verified recovery copy and updating the local workspace…";
  try {
    await window.careerHubDesktop.applyDataHomeMigration();
    message.textContent = "Update complete. Opening Career Hub…";
  } catch (_error) {
    if (document.body.contains(applyMigration)) {
      message.textContent =
        "The workspace update did not complete. Career Hub retained recovery data and stopped safely.";
      applyMigration.disabled = false;
    }
  }
});

const recoveryStepLabels = Object.freeze({
  applied: "restore the applied status",
  assessment: "restore the saved assessment",
  ledger: "restore the application record",
  terminal: "finish and verify recovery",
});

let recoveryKind = null;
const restoreRecoveryConfirmation = document.querySelector("#restore-recovery-confirmation");
const restoreRecoveryConfirm = document.querySelector("#restore-recovery-confirm");
restoreRecoveryConfirm.addEventListener("change", () => {
  if (recoveryKind === "restore") advanceRecovery.disabled = !restoreRecoveryConfirm.checked;
});
function renderRecoveryPlan(plan) {
  recoveryKind = plan.recoveryKind;
  restoreRecoveryConfirm.checked = false;
  restoreRecoveryConfirmation.hidden = plan.recoveryKind !== "restore";
  recoveryConfirmation.hidden = false;
  if (plan.recoveryKind === "restore") {
    document.querySelector("#startup-title").textContent = "Recover your workspace";
    document.querySelector("#recovery-title").textContent = "Interrupted backup restore";
    recoverySummary.textContent = "A backup restore was interrupted.";
    recoveryNote.textContent = "Roll back to the data held before that restore. Career Hub first validates the recovery record and retained files; it will not continue importing the archive. Conflicting or older recovery records require manual review. Keep the app open until the result is shown.";
    advanceRecovery.textContent = "Verify and roll back restore";
    advanceRecovery.hidden = false;
    advanceRecovery.disabled = true;
    return;
  }
  if (plan.recoveryKind === "email_credential") {
    recoverySummary.textContent = "An email connection change was interrupted.";
    recoveryNote.textContent = "Career Hub will check the saved connection and remove only its unused credential if needed. It will not read mail or create a new password. Copies or conflicting records require manual review.";
    advanceRecovery.textContent = "Recover email connection";
    advanceRecovery.hidden = false;
    advanceRecovery.disabled = false;
    return;
  }
  if (plan.disposition === "advance") {
    const label = recoveryStepLabels[plan.nextStep] || "continue recovery";
    recoverySummary.textContent = `Next verified step: ${label}.`;
    recoveryNote.textContent =
      "Only this one step will run. Career Hub will verify the result before offering another step.";
    advanceRecovery.textContent =
      plan.nextStep === "terminal" ? "Finish recovery" : "Continue one step";
    advanceRecovery.hidden = false;
    advanceRecovery.disabled = false;
    return;
  }
  advanceRecovery.hidden = true;
  if (plan.disposition === "schema_required") {
    recoverySummary.textContent = "A local data update is required first.";
    recoveryNote.textContent =
      "No recovery step was run. Update support must be completed before this application can be restored.";
    return;
  }
  recoverySummary.textContent = "Manual review is required.";
  recoveryNote.textContent =
    "The saved files and application record do not agree. Career Hub will not guess, delete, or overwrite them.";
}

reviewRecovery.addEventListener("click", async () => {
  reviewRecovery.disabled = true;
  message.textContent = "Checking the interrupted local save…";
  code.hidden = true;
  try {
    const plan = await window.careerHubDesktop.planDataHomeRecovery();
    renderRecoveryPlan(plan);
    reviewRecovery.hidden = true;
    message.textContent = "Recovery status is ready for review.";
  } catch (_error) {
    if (document.body.contains(reviewRecovery)) {
      message.textContent =
        "Career Hub could not verify the recovery state. No recovery step was run.";
      reviewRecovery.disabled = false;
      code.hidden = false;
    }
  }
});

advanceRecovery.addEventListener("click", async () => {
  if (recoveryKind === "restore" && !restoreRecoveryConfirm.checked) return;
  advanceRecovery.disabled = true;
  restoreRecoveryConfirm.disabled = true;
  message.textContent = recoveryKind === "restore" ? "Validating and rolling back the interrupted restore…" : "Completing one verified recovery step…";
  try {
    const result = await window.careerHubDesktop.advanceDataHomeRecovery(recoveryKind === "restore" ? { confirmWritersStopped: true } : undefined);
    if (result.settled) {
      message.textContent = "Recovery complete. Opening Career Hub…";
      advanceRecovery.hidden = true;
      return;
    }
    if (result.recoveryKind === "restore") {
      message.textContent = "Restore recovery did not finish cleanly.";
      recoverySummary.textContent = result.facts?.complete && result.facts?.journalRemoval === "removed"
        ? "Rollback was committed, but clean completion was not confirmed."
        : "Rollback is not confirmed. Some files may already have moved.";
      recoveryNote.textContent = (result.facts?.complete
        ? "Do not repeat rollback to clean temporary files. Some validation copies may remain. " : "Keep all retained files. ") +
        "No automatic retry or restart was made. Retry startup to inspect the current state; do not repeat the previous confirmation.";
      if (result.errorCode === "BACKUP_RESTORE_LEGACY_RECOVERY_REQUIRED") {
        message.textContent = "An older restore needs manual recovery.";
        recoverySummary.textContent = "This attempt did not move data or remove the recovery record.";
        recoveryNote.textContent = "This record uses the older Python format and cannot be adopted by the new runtime. Close Career Hub and keep the original workspace and all recovery files. Use the legacy recovery instructions before upgrading; retrying here cannot repair this record.";
      }
      code.textContent = result.errorCode;
      code.hidden = false;
      restoreRecoveryConfirmation.hidden = true;
      advanceRecovery.hidden = true;
      retry.hidden = false;
      retry.disabled = false;
      return;
    }
    if (result.recoveryKind === "email_credential") {
      message.textContent = "Email recovery could not be confirmed.";
      recoverySummary.textContent = result.facts?.cleanupConfirmed
        ? "Credential cleanup was verified. Workspace recovery is not confirmed."
        : "Credential cleanup is not confirmed. Some changes may have completed.";
      recoveryNote.textContent = result.facts?.intentRemoved
        ? "The recovery record was removed, but the operation did not finish cleanly. Retry startup to inspect the current state; do not repeat the previous action."
        : "No automatic retry was made. Retry startup to inspect the current state. If it remains unavailable, close and reopen Career Hub.";
      if (["EMAIL_CREDENTIAL_INTENT_INVALID", "EMAIL_CREDENTIAL_INTENT_SCOPE_CHANGED",
        "EMAIL_CREDENTIAL_INTENT_HOME_CHANGED", "EMAIL_CREDENTIAL_HOME_CHANGED"].includes(result.errorCode)) {
        recoveryNote.textContent = "This recovery record could not be verified for the current workspace. Keep the workspace for manual recovery; repeating this action cannot repair the record.";
      }
      code.textContent = result.errorCode;
      code.hidden = false;
      advanceRecovery.hidden = true;
      retry.hidden = false;
      retry.disabled = false;
      return;
    }
    renderRecoveryPlan(result.plan);
    message.textContent = "Step complete. Review the next step before continuing.";
  } catch (_error) {
    if (document.body.contains(advanceRecovery)) {
      message.textContent =
        "Recovery completion could not be confirmed. Do not repeat the previous action; retry startup to inspect the current state.";
      advanceRecovery.hidden = true;
      restoreRecoveryConfirmation.hidden = true;
      retry.hidden = false;
      retry.disabled = false;
    }
  }
});

function updateConnectivity() {
  offlineIndicator.hidden = navigator.onLine;
}

window.addEventListener("offline", updateConnectivity);
window.addEventListener("online", updateConnectivity);
updateConnectivity();

if (state === "backup") {
  document.querySelector("#startup-title").textContent = "Local backup";
  const panel = document.querySelector("#backup-panel");
  const detail = document.querySelector("#backup-detail");
  const archivePath = document.querySelector("#backup-path");
  const progress = document.querySelector("#backup-progress");
  const returnButton = document.querySelector("#backup-return");
  const bridge = window.careerHubDesktop?.backups;
  panel.hidden = false;
  message.textContent = "Reading backup status…";
  let current = null;
  function renderBackup(next) {
    if (!next || !["idle", "running", "complete", "error"].includes(next.phase) ||
        !Number.isSafeInteger(next.revision) || (current && next.revision < current.revision) ||
        (current && next.revision === current.revision && current.phase !== "running" && next.phase === "running")) return;
    current = next;
    const running = next.phase === "running";
    const published = next.backup?.archivePublication === "published";
    panel.setAttribute("aria-busy", String(running));
    progress.hidden = !running;
    message.textContent = running ? "Stopping the local service, copying and verifying your data, then restarting…"
      : published ? next.phase === "complete" ? "Verified backup saved." : "Backup was published; check the result."
      : next.phase === "idle" ? "No backup has been started in this app session."
      : next.backup?.archivePublication === "unknown" ? "Backup outcome is not confirmed."
      : "Backup was not published.";
    detail.textContent = running ? "Keep Career Hub open. This can take several minutes; no percentage estimate is available."
      : next.phase === "complete" ? "The temporary working copy was removed. The local service is ready. You can return to your workspace."
      : next.phase === "idle" ? "Open Settings in your workspace to create a backup."
      : (next.backup?.snapshotCleanup?.status === "incomplete"
        ? "Temporary-copy cleanup did not finish cleanly. Keep the saved ZIP; do not repeat the backup to retry cleanup. "
        : "No automatic retry was made. Some files may have been retained. ") +
        (next.backendRestarted ? "The local service restarted." : "Return to inspect startup before trying another backup.");
    archivePath.textContent = published ? next.backup.archiveRelativePath : "";
    archivePath.hidden = !published;
    code.textContent = [...new Set([next.backup?.errorCode, next.errorCode, next.restartErrorCode].filter(Boolean))].join(" · ");
    code.hidden = !code.textContent;
    returnButton.hidden = next.canReturn !== true;
    returnButton.disabled = false;
  }
  function unavailable() {
    message.textContent = "Backup status could not be read. Do not start another backup.";
    detail.textContent = "The operation may still be running. Wait for a status update. If it remains unavailable, close and reopen Career Hub and inspect retained files before repeating the backup.";
    progress.hidden = true;
  }
  if (bridge) {
    const unsubscribe = bridge.onState(renderBackup);
    if (typeof unsubscribe === "function") window.addEventListener("beforeunload", unsubscribe, { once: true });
    void bridge.getState().then(renderBackup).catch(unavailable);
    returnButton.addEventListener("click", async () => {
      returnButton.disabled = true;
      try { await bridge.returnToApp(); } catch (_error) {
        detail.textContent = "Career Hub could not reopen the workspace. Close and reopen the app to inspect startup; do not repeat the backup.";
        returnButton.disabled = false;
      }
    });
  } else unavailable();
}

if (state === "restore") {
  document.querySelector("#startup-title").textContent = "Restore local backup";
  const panel = document.querySelector("#restore-panel"), detail = document.querySelector("#restore-detail");
  const archive = document.querySelector("#restore-path"), progress = document.querySelector("#restore-progress");
  const returnButton = document.querySelector("#restore-return"), bridge = window.careerHubDesktop?.restores;
  panel.hidden = false;
  message.textContent = "Reading restore status…";
  let current = null;
  function renderRestore(next) {
    if (!next || !["idle", "selecting", "ready", "running", "complete", "error"].includes(next.phase) ||
        !Number.isSafeInteger(next.revision) || current && (next.revision < current.revision ||
        next.revision === current.revision && ["complete", "error"].includes(current.phase) && next.phase === "running")) return;
    current = next;
    const running = next.phase === "running";
    const committed = next.restore?.complete === true && next.restore?.journalRemoval === "removed";
    panel.setAttribute("aria-busy", String(running));
    progress.hidden = !running;
    message.textContent = running ? "Stopping the local service, verifying the archive and restoring its data…"
      : next.phase === "complete" ? "Restore verified."
      : next.phase === "error" ? committed ? "Restore committed, but the operation needs attention." : "Restore completion is not confirmed."
      : "No restore is running in this app session.";
    detail.textContent = running ? "Keep Career Hub open. Validation can take several minutes; no percentage estimate is available."
      : next.phase === "complete" ? "The local service restarted. Temporary validation copies were removed; previous data remains in recovery history."
      : next.phase === "error" ? (committed ? "Do not repeat restore to clean temporary files. Some validation copies may remain. " : "") +
        "No automatic retry was made. Keep all recovery files; inspect startup before taking another action."
      : "Choose a local archive from Settings before confirming restoration.";
    archive.textContent = next.restore?.recoveryRelativePath || next.archiveRelativePath || "";
    archive.hidden = !archive.textContent;
    code.textContent = [...new Set([next.restore?.errorCode, next.errorCode, next.restartErrorCode].filter(Boolean))].join(" · ");
    code.hidden = !code.textContent;
    returnButton.textContent = next.backendRestarted ? "Return to Career Hub" : "Inspect startup";
    returnButton.hidden = next.canReturn !== true;
    returnButton.disabled = false;
  }
  function unavailable() {
    message.textContent = "Restore status could not be read. Do not repeat the request.";
    detail.textContent = "The operation may still be running. Keep retained files and wait for a status update; if none arrives, close and reopen Career Hub to inspect recovery.";
    progress.hidden = true;
  }
  if (bridge) {
    const unsubscribe = bridge.onState(renderRestore);
    if (typeof unsubscribe === "function") window.addEventListener("beforeunload", unsubscribe, { once: true });
    void bridge.getState().then(renderRestore).catch(unavailable);
    returnButton.addEventListener("click", async () => {
      returnButton.disabled = true;
      try { await bridge.returnToApp(); } catch (_error) {
        detail.textContent = "Startup inspection could not be opened. Close and reopen the app; do not repeat the restore request.";
        returnButton.disabled = false;
      }
    });
  } else unavailable();
}
