"use strict";

const {
  installerIdentityForChannel,
  isSquirrelStartupEvent,
} = require("./installer_contract");

if (isSquirrelStartupEvent(process.argv, process.platform)) {
  require("electron-squirrel-startup");
  return;
}

const path = require("node:path");

const {
  app,
  autoUpdater,
  BrowserWindow,
  clipboard,
  dialog,
  ipcMain,
  Menu,
  protocol,
  session,
  shell,
} = require("electron");

const {
  ensurePrivateDirectory,
  resolveProductPaths,
  resolveSidecarPaths,
  SIDECAR_CAPABILITIES,
  validateSidecarDeployment,
} = require("./app_paths");
const { loadBuildMetadata } = require("./build_metadata");
const { readLaunchConfiguration } = require("./launch_configuration");
const {
  isTrustedLoadingUrl,
  loadingUrl,
  LOADING_SCHEME,
  registerLoadingProtocol,
} = require("./loading_protocol");
const {
  isAllowedPreviewNavigation,
  isAllowedPreviewWindowRequest,
  isOwnedAppUrl,
  isSafeExternalUrl,
} = require("./navigation");
const { createLifecycleController } = require("./lifecycle");
const { DataHomeRecoveryFlow } = require("./recovery_flow");
const { DesktopBackupFlow } = require("./backup_flow");
const { DesktopRestoreFlow, managedArchiveSelection } = require("./restore_flow");
const {
  denySessionPermissions,
  secureBrowserWindowOptions,
} = require("./security");
const { SidecarError, SidecarSupervisor } = require("./sidecar");
const { createUpdatePreferenceStore } = require("./update_preferences");
const {
  BUNDLED_EXTENSION_ORIGIN,
  ExtensionPreferencesError,
  createExtensionPreferenceStore,
  defaultPreferences: defaultExtensionPreferences,
  effectiveExtensionOrigins,
} = require("./extension_preferences");
const { createDesktopUpdater } = require("./updater");
const { isTrustedUpdateSender } = require("./update_ipc");

app.enableSandbox();
protocol.registerSchemesAsPrivileged([LOADING_SCHEME]);

const projectRoot = path.resolve(__dirname, "..", "..");
const loadingRoot = path.resolve(__dirname, "..", "ui");
const preloadPath = path.resolve(__dirname, "preload.js");
let metadata;
let launchConfiguration;
let productPaths;
let startupConfigurationError = null;

try {
  metadata = loadBuildMetadata({
    isPackaged: app.isPackaged,
    processResourcesPath: process.resourcesPath,
    projectRoot,
  });
  launchConfiguration = readLaunchConfiguration({ argv: process.argv,
    isPackaged: app.isPackaged, channel: metadata.channel, sidecarRuntime: metadata.sidecarRuntime });
  const productName =
    launchConfiguration.goPreview
      ? "Career Hub Go Preview"
      : metadata.channel === "stable"
      ? "Career Hub"
      : "Career Hub Dev";
  app.setName(productName);
  if (process.platform === "win32") {
    app.setAppUserModelId(
      installerIdentityForChannel(metadata.channel, { goPreview: launchConfiguration.goPreview }).appUserModelId,
    );
  }
  productPaths = resolveProductPaths({
    channel: metadata.channel,
    goPreview: launchConfiguration.goPreview,
    electronPaths: {
      appData: app.getPath("appData"),
      userData: app.getPath("userData"),
    },
  });
  ensurePrivateDirectory(productPaths.electronUserDataRoot);
  ensurePrivateDirectory(productPaths.electronSessionRoot);
  app.setPath("userData", productPaths.electronUserDataRoot);
  app.setPath("sessionData", productPaths.electronSessionRoot);
} catch (error) {
  startupConfigurationError =
    typeof error?.code === "string"
      ? error.code
      : "DESKTOP_CONFIGURATION_INVALID";
}

if (startupConfigurationError !== null) {
  app.whenReady().then(() => {
    dialog.showErrorBox(
      "Career Hub could not start",
      `Startup configuration failed (${startupConfigurationError}).`,
    );
    app.quit();
  });
} else if (!app.requestSingleInstanceLock({ channel: metadata.channel })) {
  app.quit();
} else {
  runPrimaryInstance();
}

function runPrimaryInstance() {
  let mainWindow = null;
  let startupPromise = null;
  let lastStartupError = null;
  let pendingMigrationPlan = null;
  let ownedOrigin = null;
  let startedManifestSha256 = null;
  let hostInitializationPromise = null;
  let desktopUpdater = null;
  // ADR SEC-250 item 4, amended: the extension endpoint is turned on in
  // Settings and persists. An unreadable preference file fails closed.
  let extensionPreferenceStore = null;
  let extensionPreferences = defaultExtensionPreferences();
  let extensionPreferencesReadable = true;
  try {
    extensionPreferenceStore = createExtensionPreferenceStore(productPaths.electronUserDataRoot);
    extensionPreferences = extensionPreferenceStore.read();
  } catch (_error) {
    extensionPreferencesReadable = false;
  }
  let runningExtensionOrigins = null;
  let backupAvailable = false;
  let backupScreen = false;
  let restoreAvailable = false;
  let restoreScreen = false;
  let restoreRuntimeIdentity = null;
  let restoreDataRoot = null;
  const supervisor = new SidecarSupervisor();
  let lifecycle;

  function isCurrentWindow(window) {
    return (
      window !== null &&
      window === mainWindow &&
      typeof window.isDestroyed === "function" &&
      !window.isDestroyed()
    );
  }

  function focusMainWindow() {
    if (!isCurrentWindow(mainWindow)) return;
    if (mainWindow.isMinimized()) mainWindow.restore();
    mainWindow.show();
    mainWindow.focus();
  }

  function isLoadingPageSender(event) {
    if (
      !isCurrentWindow(mainWindow) ||
      event.sender !== mainWindow.webContents ||
      !event.senderFrame ||
      event.senderFrame !== event.sender.mainFrame
    ) {
      return false;
    }
    return isTrustedLoadingUrl(event.senderFrame.url);
  }

  function isOwnedAppPageSender(event) {
    return isTrustedUpdateSender({
      event,
      isOwnedAppUrl,
      mainWindow,
      ownedOrigin,
    });
  }

  function publishUpdateState(state) {
    if (
      !isCurrentWindow(mainWindow) ||
      !isOwnedAppUrl(mainWindow.webContents.getURL(), ownedOrigin)
    ) {
      return;
    }
    mainWindow.webContents.send("career-hub:updates:state", state);
  }

  function reportedErrorCode(error, fallback) {
    if (
      error instanceof SidecarError &&
      error.code === "DESKTOP_SIDECAR_REPORTED_ERROR" &&
      typeof error.details?.reportedCode === "string"
    ) {
      return error.details.reportedCode;
    }
    return typeof error?.code === "string" ? error.code : fallback;
  }

  function resolveLocalRuntime() {
    const dataRoot = ensurePrivateDirectory(productPaths.dataRoot);
    return Object.freeze({ dataRoot, sidecarPaths: resolveDeployment() });
  }

  function resolveDeployment() {
    return validateSidecarDeployment(
      resolveSidecarPaths({
        isPackaged: app.isPackaged,
        processResourcesPath: process.resourcesPath,
        projectRoot,
        platform: process.platform,
      }),
      { appVersion: metadata.appVersion, platform: process.platform },
    );
  }

  function currentAgentConnection(event, args) {
    if (!isOwnedAppPageSender(event) || args.length !== 0) {
      throw new Error("DESKTOP_AGENT_REQUEST_NOT_ALLOWED");
    }
    if (lifecycle.isQuitting || startupPromise !== null || recoveryFlow.busy ||
        backupFlow.busy || backupScreen || restoreFlow.busy || restoreScreen ||
        supervisor.state !== "ready" || !supervisor.ownsChild || supervisor.origin !== ownedOrigin) {
      return { available: false, reason: "service_unavailable" };
    }
    try {
      // Read only: revalidate the fixed installation, never resolve/create a Home
      // or accept a renderer-selected executable, server, or credential.
      const deployment = resolveDeployment();
      if (!startedManifestSha256 || deployment.manifestSha256 !== startedManifestSha256 || !deployment.agentClient) {
        return { available: false, reason: "package_changed" };
      }
      const { executablePath, contractVersion, commands } = deployment.agentClient;
      // The path, not the token: the sidecar writes the token into this
      // owner-only Data Home file (ADR SEC-250) and the agent reads it there.
      const connection = path.join(productPaths.dataRoot, "session", "agent-connection.json");
      return { available: true, executablePath, server: ownedOrigin, connection, contractVersion, commands };
    } catch (_error) {
      return { available: false, reason: "package_changed" };
    }
  }

  // The window token (ADR SEC-250) reaches only Career Hub's own page on the
  // owned origin, and only while this host owns a ready sidecar.
  ipcMain.handle("career-hub:session:token", (event, ...args) => {
    if (!isOwnedAppPageSender(event) || args.length !== 0) {
      throw new Error("DESKTOP_SESSION_REQUEST_NOT_ALLOWED");
    }
    if (supervisor.state !== "ready" || !supervisor.ownsChild || supervisor.origin !== ownedOrigin) {
      return null;
    }
    return supervisor.sessionToken;
  });
  ipcMain.handle("career-hub:agent:get-connection", (event, ...args) => currentAgentConnection(event, args));
  ipcMain.handle("career-hub:agent:copy-connection", (event, ...args) => {
    const state = currentAgentConnection(event, args);
    if (!state.available) return state;
    const { executablePath, server, connection, contractVersion, commands } = state;
    try {
      // No clipboard read or arbitrary renderer text. Copy only this fresh,
      // host-verified connection in response to the explicit Settings action.
      clipboard.writeText(JSON.stringify({ executablePath, server, connection, contractVersion, commands }, null, 2));
      return { ...state, copied: true };
    } catch (_error) {
      return { ...state, copied: false };
    }
  });

  function resolveRecoveryRuntime() {
    const runtime = resolveLocalRuntime();
    if (
      !runtime.sidecarPaths.runtimeCapabilities.includes(
        SIDECAR_CAPABILITIES.DATA_HOME_RECOVERY_V1,
      )
    ) {
      throw new Error("DESKTOP_RECOVERY_UNAVAILABLE");
    }
    return runtime;
  }

  function dataHomeRecoveryAvailable() {
    try {
      resolveRecoveryRuntime();
      return true;
    } catch (_error) {
      return false;
    }
  }

  const recoveryFlow = new DataHomeRecoveryFlow({
    channel: metadata.channel,
    resolveRuntime: resolveRecoveryRuntime,
    runMaintenance: (request) => supervisor.runMaintenance(request),
  });

  function backupState() {
    return Object.freeze({ ...backupFlow.getState(), available: backupAvailable,
      canStart: backupAvailable && !backupFlow.busy && !backupScreen &&
        !restoreFlow.busy && !restoreScreen &&
        !lifecycle.isQuitting && !recoveryFlow.busy && startupPromise === null &&
        supervisor.state === "ready" && supervisor.ownsChild,
      canReturn: backupScreen && !backupFlow.busy && !lifecycle.isQuitting });
  }

  const backupFlow = new DesktopBackupFlow({
    prepare: async () => {
      backupScreen = true;
      if (lifecycle.isQuitting || !await showLoadingState(mainWindow, "backup")) {
        throw new Error("DESKTOP_BACKUP_VIEW_UNAVAILABLE");
      }
      ownedOrigin = null;
    },
    createBackup: (request) => {
      if (lifecycle.isQuitting) throw new SidecarError("DESKTOP_BACKUP_CANCELLED");
      return supervisor.createBackup(request);
    },
    onState: (state) => {
      if (state.phase !== "running") {
        ownedOrigin = supervisor.state === "ready" ? supervisor.origin : null;
        lastStartupError = ownedOrigin ? null : state.restartErrorCode || "DESKTOP_BACKUP_RESTART_REQUIRED";
      }
      if (isCurrentWindow(mainWindow) &&
          (isTrustedLoadingUrl(mainWindow.webContents.getURL()) ||
           isOwnedAppUrl(mainWindow.webContents.getURL(), ownedOrigin))) {
        mainWindow.webContents.send("career-hub:backups:state", backupState());
      }
    },
  });

  ipcMain.handle("career-hub:backups:get-state", (event) => {
    if (!isOwnedAppPageSender(event) && !isLoadingPageSender(event)) {
      throw new Error("DESKTOP_BACKUP_REQUEST_NOT_ALLOWED");
    }
    return backupState();
  });

  ipcMain.handle("career-hub:backups:start", (event, request) => {
    if (!isOwnedAppPageSender(event) || !backupState().canStart) {
      throw new Error("DESKTOP_BACKUP_REQUEST_NOT_ALLOWED");
    }
    return backupFlow.start(request);
  });

  ipcMain.handle("career-hub:backups:return", (event) => {
    if (!isLoadingPageSender(event) || !backupState().canReturn) {
      throw new Error("DESKTOP_BACKUP_RETURN_NOT_ALLOWED");
    }
    backupScreen = false;
    void presentWindowSafely(mainWindow);
    return Object.freeze({ accepted: true });
  });

  function restoreState() {
    const usable = restoreAvailable && !lifecycle.isQuitting && !backupFlow.busy && !backupScreen &&
      !recoveryFlow.busy && startupPromise === null && supervisor.state === "ready" && supervisor.ownsChild;
    return Object.freeze({ ...restoreFlow.getState(), available: restoreAvailable,
      canChoose: usable && !restoreFlow.busy && !restoreScreen,
      canStart: usable && !restoreScreen && restoreFlow.getState().phase === "ready",
      canReturn: restoreScreen && !restoreFlow.busy && !lifecycle.isQuitting });
  }
  const restoreFlow = new DesktopRestoreFlow({
    runtimeIdentity: () => !lifecycle.isQuitting && restoreAvailable && supervisor.state === "ready" && supervisor.ownsChild
      ? restoreRuntimeIdentity : null,
    chooseArchive: async () => {
      const dataRoot = restoreDataRoot;
      const selected = await dialog.showOpenDialog(mainWindow, { title: "Choose a local Career Hub backup",
        defaultPath: path.join(dataRoot, "backups"), properties: ["openFile"],
        filters: [{ name: "Career Hub backup", extensions: ["zip"] }] });
      if (selected.canceled) return null;
      const archive = selected.filePaths?.length === 1 && managedArchiveSelection(dataRoot, selected.filePaths[0]);
      if (!archive) throw new SidecarError("DESKTOP_RESTORE_ARCHIVE_OUTSIDE_HOME");
      return archive;
    },
    prepare: async () => {
      restoreScreen = true;
      if (lifecycle.isQuitting || !await showLoadingState(mainWindow, "restore")) throw new Error("DESKTOP_RESTORE_VIEW_UNAVAILABLE");
      ownedOrigin = null;
    },
    restoreBackup: (request) => {
      if (lifecycle.isQuitting) throw new SidecarError("DESKTOP_RESTORE_CANCELLED");
      return supervisor.restoreBackup(request);
    },
    onState: (state) => {
      if (restoreScreen && !["selecting", "running"].includes(state.phase)) {
        ownedOrigin = supervisor.state === "ready" ? supervisor.origin : null;
        lastStartupError = ownedOrigin ? null : state.restartErrorCode || "DESKTOP_RESTORE_RESTART_REQUIRED";
      }
      if (isCurrentWindow(mainWindow) && (isTrustedLoadingUrl(mainWindow.webContents.getURL()) ||
          isOwnedAppUrl(mainWindow.webContents.getURL(), ownedOrigin))) {
        mainWindow.webContents.send("career-hub:restores:state", restoreState());
        mainWindow.webContents.send("career-hub:backups:state", backupState());
      }
    },
  });
  ipcMain.handle("career-hub:restores:get-state", (event) => {
    if (!isOwnedAppPageSender(event) && !isLoadingPageSender(event)) throw new Error("DESKTOP_RESTORE_REQUEST_NOT_ALLOWED");
    return restoreState();
  });
  ipcMain.handle("career-hub:restores:choose", (event) => {
    if (!isOwnedAppPageSender(event) || !restoreState().canChoose) throw new Error("DESKTOP_RESTORE_REQUEST_NOT_ALLOWED");
    return restoreFlow.choose();
  });
  ipcMain.handle("career-hub:restores:start", (event, request) => {
    if (!isOwnedAppPageSender(event) || !restoreState().canStart) throw new Error("DESKTOP_RESTORE_REQUEST_NOT_ALLOWED");
    return restoreFlow.start(request);
  });
  ipcMain.handle("career-hub:restores:return", (event) => {
    if (!isLoadingPageSender(event) || !restoreState().canReturn) throw new Error("DESKTOP_RESTORE_RETURN_NOT_ALLOWED");
    restoreScreen = false;
    // A failed restore never restarts automatically. This explicit action may
    // inspect ordinary startup, which still refuses a pending recovery journal.
    void presentWindowSafely(mainWindow, { retry: true });
    return Object.freeze({ accepted: true });
  });

  ipcMain.handle("career-hub:retry-startup", async (event) => {
    if (!isLoadingPageSender(event) || lastStartupError === null || backupScreen || backupFlow.busy || recoveryFlow.busy || restoreScreen || restoreFlow.busy) {
      throw new Error("DESKTOP_RETRY_NOT_ALLOWED");
    }
    void presentWindowSafely(mainWindow, { retry: true });
    return Object.freeze({ accepted: true });
  });

  ipcMain.handle(
    "career-hub:get-data-home-recovery-availability",
    async (event) => {
      if (
        !isLoadingPageSender(event) ||
        lastStartupError !== "DATA_HOME_RECOVERY_REQUIRED"
      ) {
        throw new Error("DESKTOP_RECOVERY_AVAILABILITY_NOT_ALLOWED");
      }
      return Object.freeze({ available: dataHomeRecoveryAvailable() });
    },
  );

  ipcMain.handle("career-hub:plan-data-home-migration", async (event) => {
    if (
      !isLoadingPageSender(event) ||
      lastStartupError !== "DATA_HOME_MIGRATION_REQUIRED" ||
      startupPromise !== null ||
      supervisor.ownsChild
    ) {
      throw new Error("DESKTOP_MIGRATION_PLAN_NOT_ALLOWED");
    }
    pendingMigrationPlan = null;
    try {
      const { dataRoot, sidecarPaths } = resolveLocalRuntime();
      const plan = await supervisor.runMaintenance({
        deploymentRoot: sidecarPaths.deploymentRoot,
        executablePath: sidecarPaths.executablePath,
        expectedEvent: "document_migration_plan",
        record: {
          command: "document_migration_plan",
          data_root: dataRoot,
          protocol_version: 1,
        },
      });
      pendingMigrationPlan = plan;
      return Object.freeze({
        ciphertextFileCount: plan.ciphertextFileCount,
        documentCount: plan.documentCount,
        sourceFormatVersion: plan.sourceFormatVersion,
        targetFormatVersion: plan.targetFormatVersion,
        totalCiphertextBytes: plan.totalCiphertextBytes,
      });
    } catch (error) {
      lastStartupError = reportedErrorCode(
        error,
        "DESKTOP_MIGRATION_PLAN_FAILED",
      );
      void showLoadingState(mainWindow, "error", lastStartupError).catch(() => {});
      throw new Error(lastStartupError);
    }
  });

  ipcMain.handle("career-hub:apply-data-home-migration", async (event) => {
    if (
      !isLoadingPageSender(event) ||
      lastStartupError !== "DATA_HOME_MIGRATION_REQUIRED" ||
      pendingMigrationPlan === null ||
      startupPromise !== null ||
      supervisor.ownsChild
    ) {
      throw new Error("DESKTOP_MIGRATION_APPLY_NOT_ALLOWED");
    }
    const reviewedPlan = pendingMigrationPlan;
    pendingMigrationPlan = null;
    try {
      const { dataRoot, sidecarPaths } = resolveLocalRuntime();
      const completed = await supervisor.runMaintenance({
        deploymentRoot: sidecarPaths.deploymentRoot,
        executablePath: sidecarPaths.executablePath,
        expectedEvent: "document_migration_complete",
        record: {
          command: "document_migration_apply",
          confirm_plan: reviewedPlan.planId,
          data_root: dataRoot,
          protocol_version: 1,
        },
      });
      if (completed.planId !== reviewedPlan.planId) {
        throw new SidecarError("DESKTOP_MIGRATION_RESULT_MISMATCH");
      }
      lastStartupError = null;
      void presentWindowSafely(mainWindow, { retry: true });
      return Object.freeze({ accepted: true });
    } catch (error) {
      lastStartupError = reportedErrorCode(
        error,
        "DESKTOP_MIGRATION_APPLY_FAILED",
      );
      void showLoadingState(mainWindow, "error", lastStartupError).catch(() => {});
      throw new Error(lastStartupError);
    }
  });

  ipcMain.handle("career-hub:plan-data-home-recovery", async (event) => {
    if (
      !isLoadingPageSender(event) ||
      lastStartupError !== "DATA_HOME_RECOVERY_REQUIRED" ||
      startupPromise !== null ||
      supervisor.ownsChild ||
      !dataHomeRecoveryAvailable()
    ) {
      throw new Error("DESKTOP_RECOVERY_PLAN_NOT_ALLOWED");
    }
    try {
      return await recoveryFlow.plan();
    } catch (error) {
      lastStartupError = reportedErrorCode(
        error,
        "DESKTOP_RECOVERY_PLAN_FAILED",
      );
      void showLoadingState(mainWindow, "error", lastStartupError).catch(() => {});
      throw new Error(lastStartupError);
    }
  });

  ipcMain.handle("career-hub:advance-data-home-recovery", async (event, request) => {
    if (
      !isLoadingPageSender(event) ||
      lastStartupError !== "DATA_HOME_RECOVERY_REQUIRED" ||
      startupPromise !== null ||
      supervisor.ownsChild ||
      !dataHomeRecoveryAvailable()
    ) {
      throw new Error("DESKTOP_RECOVERY_ADVANCE_NOT_ALLOWED");
    }
    try {
      const result = await recoveryFlow.advance(request);
      if (result.settled) {
        lastStartupError = null;
        void presentWindowSafely(mainWindow, { retry: true });
      }
      return result;
    } catch (error) {
      lastStartupError = reportedErrorCode(
        error,
        "DESKTOP_RECOVERY_ADVANCE_FAILED",
      );
      void showLoadingState(mainWindow, "error", lastStartupError).catch(() => {});
      throw new Error(lastStartupError);
    }
  });

  ipcMain.handle("career-hub:open-external", async (event, targetUrl) => {
    if (
      !event.senderFrame ||
      event.senderFrame !== event.sender.mainFrame ||
      !isCurrentWindow(mainWindow) ||
      event.sender !== mainWindow.webContents ||
      !isOwnedAppUrl(event.senderFrame.url, ownedOrigin) ||
      !isSafeExternalUrl(targetUrl)
    ) {
      throw new Error("DESKTOP_EXTERNAL_OPEN_NOT_ALLOWED");
    }
    await shell.openExternal(targetUrl);
    return Object.freeze({ accepted: true });
  });

  async function loadIfCurrent(window, targetUrl) {
    if (!isCurrentWindow(window)) return false;
    try {
      await window.loadURL(targetUrl);
    } catch (error) {
      if (!isCurrentWindow(window)) return false;
      throw error;
    }
    return isCurrentWindow(window);
  }

  function showLoadingState(window, state, errorCode = "") {
    return loadIfCurrent(window, loadingUrl(state, errorCode));
  }

  async function startSidecar() {
    if (restoreFlow.busy) throw new SidecarError("DESKTOP_RESTORE_BUSY");
    if (backupFlow.busy) throw new SidecarError("DESKTOP_BACKUP_BUSY");
    if (recoveryFlow.busy) throw new SidecarError("DESKTOP_RECOVERY_BUSY");
    if (startupPromise !== null) return startupPromise;
    if (supervisor.ownsChild) {
      if (supervisor.state === "ready" && supervisor.origin !== null) {
        ownedOrigin = supervisor.origin;
        lastStartupError = null;
        return Object.freeze({ origin: supervisor.origin });
      }
      throw new SidecarError("DESKTOP_SIDECAR_ALREADY_OWNED");
    }

    const operation = (async () => {
      lastStartupError = null;
      pendingMigrationPlan = null;
      recoveryFlow.clear();
      ownedOrigin = null;
      startedManifestSha256 = null;
      try {
        const { dataRoot, sidecarPaths } = resolveLocalRuntime();
        const extensionOrigins = effectiveExtensionOrigins({
          launchOrigins: launchConfiguration.extensionOrigins,
          preferences: extensionPreferences,
        });
        const ready = await supervisor.start({
          appVersion: metadata.appVersion,
          channel: metadata.channel,
          dataRoot,
          deploymentRoot: sidecarPaths.deploymentRoot,
          executablePath: sidecarPaths.executablePath,
          mode: "go-bootstrap",
          extensionOrigins,
        });
        runningExtensionOrigins = extensionOrigins;
        backupAvailable = process.platform === "win32" &&
          sidecarPaths.runtimeCapabilities.includes(SIDECAR_CAPABILITIES.BACKUP_CREATE_V1);
        restoreAvailable = process.platform === "win32" &&
          sidecarPaths.runtimeCapabilities.includes(SIDECAR_CAPABILITIES.BACKUP_RESTORE_V1);
        restoreRuntimeIdentity = JSON.stringify({ dataRoot, deploymentRoot: sidecarPaths.deploymentRoot,
          executablePath: sidecarPaths.executablePath, channel: metadata.channel, appVersion: metadata.appVersion });
        restoreDataRoot = dataRoot;
        startedManifestSha256 = sidecarPaths.manifestSha256 || null;
        desktopUpdater?.setSourceCommit(sidecarPaths.sourceCommit);
        ownedOrigin = ready.origin;
        return ready;
      } catch (error) {
        ownedOrigin = null;
        lastStartupError = reportedErrorCode(
          error,
          "DESKTOP_SIDECAR_START_FAILED",
        );
        throw error;
      }
    })();
    startupPromise = operation;
    try {
      return await operation;
    } finally {
      if (startupPromise === operation) startupPromise = null;
    }
  }

  async function presentWindow(window, { retry = false } = {}) {
    if (restoreScreen) { await showLoadingState(window, "restore"); return; }
    if (restoreFlow.busy) return;
    if (!isCurrentWindow(window) || lifecycle.isQuitting) return;
    if (backupScreen || backupFlow.busy) {
      await showLoadingState(window, "backup");
      return;
    }
    if (recoveryFlow.busy) return;

    if (
      supervisor.ownsChild &&
      supervisor.state === "ready" &&
      supervisor.origin !== null
    ) {
      ownedOrigin = supervisor.origin;
      lastStartupError = null;
      await loadIfCurrent(window, `${ownedOrigin}/`);
      return;
    }

    if (lastStartupError !== null && startupPromise === null && !retry) {
      await showLoadingState(window, "error", lastStartupError);
      return;
    }

    const loadingWasShown = await showLoadingState(window, "starting");
    if (!loadingWasShown && startupPromise === null) return;

    try {
      const ready = await startSidecar();
      if (!lifecycle.isQuitting) {
        await loadIfCurrent(window, `${ready.origin}/`);
      }
    } catch (_error) {
      if (!lifecycle.isQuitting && isCurrentWindow(window)) {
        await showLoadingState(window, "error", lastStartupError);
      }
    }
  }

  async function presentWindowSafely(window, options = {}) {
    try {
      await presentWindow(window, options);
    } catch (_error) {
      if (!lifecycle.isQuitting && isCurrentWindow(window)) {
        lastStartupError ??= "DESKTOP_WINDOW_LOAD_FAILED";
        try {
          await showLoadingState(window, "error", lastStartupError);
        } catch (_loadingError) {
          // The custom loading protocol is itself part of the packaged smoke gate.
        }
      }
    }
  }

  supervisor.on("exit", () => {
    const exitedAfterReadiness = ownedOrigin !== null;
    ownedOrigin = null;
    if (!exitedAfterReadiness || lifecycle.isQuitting || backupScreen || restoreScreen) return;
    lastStartupError = "DESKTOP_SIDECAR_EXITED";
    void showLoadingState(mainWindow, "error", lastStartupError).catch(() => {});
  });

  supervisor.on("protocol-error", (errorCode) => {
    if (lifecycle.isQuitting) return;
    void supervisor
      .stop()
      .catch(() => {})
      .finally(() => {
        if (lifecycle.isQuitting) return;
        ownedOrigin = null;
        lastStartupError =
          typeof errorCode === "string"
            ? errorCode
            : "DESKTOP_SIDECAR_RUNTIME_OUTPUT_INVALID";
        if (!backupScreen && !restoreScreen) void showLoadingState(mainWindow, "error", lastStartupError).catch(() => {});
      });
  });

  function configureWindow(window) {
    window.webContents.setWindowOpenHandler((details) => {
      if (
        isAllowedPreviewWindowRequest(
          details.url,
          window.webContents.getURL(),
          ownedOrigin,
        )
      ) {
        return {
          action: "allow",
          overrideBrowserWindowOptions: {
            ...secureBrowserWindowOptions(),
            height: 780,
            minHeight: 480,
            minWidth: 640,
            title: "Career Hub document preview",
            width: 1040,
          },
        };
      }
      return { action: "deny" };
    });
    window.webContents.on("did-create-window", (previewWindow) => {
      previewWindow.setMenu(null);
      previewWindow.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
      const guardPreviewNavigation = (event, targetUrl) => {
        if (!isAllowedPreviewNavigation(targetUrl, ownedOrigin)) {
          event.preventDefault();
        }
      };
      previewWindow.webContents.on("will-navigate", guardPreviewNavigation);
      previewWindow.webContents.on("will-redirect", guardPreviewNavigation);
      previewWindow.webContents.on("did-navigate", (_event, targetUrl) => {
        if (isAllowedPreviewNavigation(targetUrl, ownedOrigin) && targetUrl !== "about:blank") {
          previewWindow.show();
        }
      });
    });
    window.webContents.on("will-navigate", (event, targetUrl) => {
      if (isTrustedLoadingUrl(targetUrl) || isOwnedAppUrl(targetUrl, ownedOrigin)) {
        return;
      }
      event.preventDefault();
    });
    window.webContents.on("will-redirect", (event, targetUrl) => {
      if (isTrustedLoadingUrl(targetUrl) || isOwnedAppUrl(targetUrl, ownedOrigin)) {
        return;
      }
      event.preventDefault();
    });
    window.once("ready-to-show", () => {
      if (!window.isDestroyed()) window.show();
    });
    window.on("closed", () => {
      if (mainWindow === window) mainWindow = null;
    });
  }

  function initializeHost() {
    if (hostInitializationPromise === null) {
      hostInitializationPromise = Promise.resolve().then(() => {
        registerLoadingProtocol(protocol, loadingRoot);
        Menu.setApplicationMenu(null);
        denySessionPermissions(session.defaultSession);
      });
    }
    return hostInitializationPromise;
  }

  async function ensureMainWindow() {
    if (lifecycle.isQuitting) return null;
    if (isCurrentWindow(mainWindow)) {
      focusMainWindow();
      return mainWindow;
    }

    await app.whenReady();
    await initializeHost();
    if (lifecycle.isQuitting) return null;
    if (isCurrentWindow(mainWindow)) {
      focusMainWindow();
      return mainWindow;
    }

    const window = new BrowserWindow(
      secureBrowserWindowOptions({ preloadPath }),
    );
    mainWindow = window;
    configureWindow(window);
    await presentWindowSafely(window);
    if (isCurrentWindow(window)) focusMainWindow();
    return isCurrentWindow(window) ? window : null;
  }

  lifecycle = createLifecycleController({
    app,
    ensureWindow: ensureMainWindow,
    onShutdownError: (error) => {
      const errorCode =
        typeof error?.code === "string"
          ? error.code
          : "DESKTOP_SIDECAR_STOP_FAILED";
      dialog.showErrorBox(
        "Career Hub is still running",
        `The local data service did not stop (${errorCode}). Career Hub remains open to protect your data. Wait a moment and quit again.`,
      );
    },
    platform: process.platform,
    supervisor,
  });

  desktopUpdater = createDesktopUpdater({
    appVersion: metadata.appVersion,
    autoUpdater,
    isPackaged: app.isPackaged,
    onInstall: () => backupFlow.busy || backupScreen || recoveryFlow.busy || restoreFlow.busy || restoreScreen
      ? false : lifecycle.beginShutdown(() => autoUpdater.quitAndInstall()),
    platform: process.platform,
    preferenceStore: createUpdatePreferenceStore(
      productPaths.electronUserDataRoot,
    ),
    updateFeedUrl: metadata.updateFeedUrl,
  });
  desktopUpdater.on("state", publishUpdateState);

  ipcMain.handle("career-hub:updates:get-state", async (event) => {
    if (!isOwnedAppPageSender(event)) {
      throw new Error("DESKTOP_UPDATE_REQUEST_NOT_ALLOWED");
    }
    return desktopUpdater.getState();
  });

  ipcMain.handle("career-hub:updates:check", async (event) => {
    if (!isOwnedAppPageSender(event)) {
      throw new Error("DESKTOP_UPDATE_REQUEST_NOT_ALLOWED");
    }
    return desktopUpdater.checkForUpdates();
  });

  ipcMain.handle("career-hub:updates:set-automatic", async (event, enabled) => {
    if (!isOwnedAppPageSender(event)) {
      throw new Error("DESKTOP_UPDATE_REQUEST_NOT_ALLOWED");
    }
    return desktopUpdater.setAutomaticUpdates(enabled);
  });

  ipcMain.handle("career-hub:updates:install", async (event) => {
    if (!isOwnedAppPageSender(event) || backupFlow.busy || backupScreen || recoveryFlow.busy || restoreFlow.busy || restoreScreen) {
      throw new Error("DESKTOP_UPDATE_REQUEST_NOT_ALLOWED");
    }
    return desktopUpdater.installUpdate();
  });

  desktopUpdater.start();

  // Browser extension enablement (ADR SEC-250 item 4, amended). The owner's
  // choice is read back to the window only; the window cannot inject an origin
  // that fails the host's own validation, and a change takes effect only after
  // this host restarts the sidecar with the new origin list.
  function extensionState() {
    const desired = effectiveExtensionOrigins({
      launchOrigins: launchConfiguration.extensionOrigins,
      preferences: extensionPreferences,
    });
    const running = runningExtensionOrigins;
    return Object.freeze({
      bundledOrigin: BUNDLED_EXTENSION_ORIGIN,
      enabled: extensionPreferences.enabled,
      origins: [...extensionPreferences.origins],
      launchOrigins: [...launchConfiguration.extensionOrigins],
      readable: extensionPreferencesReadable,
      restartRequired: running !== null
        && (running.length !== desired.length || running.some((origin, index) => origin !== desired[index])),
      runningEnabled: running !== null && running.length > 0,
    });
  }

  ipcMain.handle("career-hub:extension:get-state", async (event, ...args) => {
    if (!isOwnedAppPageSender(event) || args.length !== 0) {
      throw new Error("DESKTOP_EXTENSION_REQUEST_NOT_ALLOWED");
    }
    return extensionState();
  });

  ipcMain.handle("career-hub:extension:set", async (event, request, ...args) => {
    if (!isOwnedAppPageSender(event) || args.length !== 0) {
      throw new Error("DESKTOP_EXTENSION_REQUEST_NOT_ALLOWED");
    }
    try {
      if (extensionPreferenceStore === null) {
        throw new ExtensionPreferencesError("DESKTOP_EXTENSION_PREFERENCES_WRITE_FAILED");
      }
      extensionPreferences = extensionPreferenceStore.write({
        enabled: request?.enabled,
        origins: request?.origins,
      });
      extensionPreferencesReadable = true;
    } catch (error) {
      if (error instanceof ExtensionPreferencesError) {
        return Object.freeze({ ...extensionState(), error: error.code });
      }
      throw error;
    }
    return extensionState();
  });

  ipcMain.handle("career-hub:extension:relaunch", async (event, ...args) => {
    if (!isOwnedAppPageSender(event) || args.length !== 0 || backupFlow.busy || backupScreen
      || recoveryFlow.busy || restoreFlow.busy || restoreScreen) {
      throw new Error("DESKTOP_EXTENSION_REQUEST_NOT_ALLOWED");
    }
    // The same owned shutdown as an update install, then a plain relaunch of
    // this executable with its original arguments.
    return lifecycle.beginShutdown(() => {
      app.relaunch();
      app.quit();
    });
  });

  app.on("second-instance", () => {
    // Arguments and working directory from the second process are untrusted.
    void lifecycle.handleSecondInstance().catch(() => {});
  });

  app.on("activate", () => {
    void lifecycle.handleActivate().catch(() => {});
  });

  app.on("window-all-closed", () => {
    lifecycle.handleWindowAllClosed();
  });

  app.on("before-quit", (event) => {
    lifecycle.handleBeforeQuit(event);
  });

  app.on("will-quit", () => {
    desktopUpdater?.destroy();
  });

  void lifecycle.handleActivate().catch(() => {
    if (!lifecycle.isQuitting) {
      dialog.showErrorBox(
        "Career Hub could not start",
        "The desktop window could not be initialized (DESKTOP_HOST_INIT_FAILED).",
      );
      app.quit();
    }
  });
}
