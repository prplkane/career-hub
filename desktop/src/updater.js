"use strict";

const { EventEmitter } = require("node:events");

const AUTOMATIC_FIRST_CHECK_DELAY_MS = 30_000;
const AUTOMATIC_RECHECK_DELAY_MS = 6 * 60 * 60 * 1_000;
const BUSY_PHASES = new Set(["checking", "downloading", "installing"]);
const SUPPORTED_PLATFORMS = new Set(["darwin", "win32"]);
const SOURCE_COMMIT = /^[0-9a-f]{40}$/;

class DesktopUpdateError extends Error {
  constructor(code) {
    super(code);
    this.name = "DesktopUpdateError";
    this.code = code;
  }
}

function sanitizedReleaseName(value) {
  if (typeof value !== "string") return null;
  const normalized = value.trim();
  if (normalized.length === 0 || normalized.length > 80) return null;
  return /^[A-Za-z0-9][A-Za-z0-9._+ -]*$/.test(normalized) ? normalized : null;
}

class DesktopUpdater extends EventEmitter {
  constructor({
    appVersion,
    autoUpdater,
    isPackaged,
    onInstall,
    platform = process.platform,
    preferenceStore,
    setTimer = setTimeout,
    clearTimer = clearTimeout,
    updateFeedUrl = null,
  }) {
    super();
    if (
      typeof appVersion !== "string" ||
      !/^\d+\.\d+\.\d+$/.test(appVersion) ||
      !autoUpdater ||
      typeof autoUpdater.setFeedURL !== "function" ||
      typeof autoUpdater.checkForUpdates !== "function" ||
      typeof autoUpdater.on !== "function" ||
      typeof autoUpdater.removeListener !== "function" ||
      typeof isPackaged !== "boolean" ||
      typeof onInstall !== "function" ||
      !preferenceStore ||
      typeof preferenceStore.read !== "function" ||
      typeof preferenceStore.write !== "function" ||
      typeof setTimer !== "function" ||
      typeof clearTimer !== "function"
    ) {
      throw new TypeError("Desktop updater adapters are required");
    }

    this._appVersion = appVersion;
    this._autoUpdater = autoUpdater;
    this._clearTimer = clearTimer;
    this._onInstall = onInstall;
    this._preferenceStore = preferenceStore;
    this._setTimer = setTimer;
    this._timer = null;
    this._started = false;
    this._releaseName = null;
    this._sourceCommit = null;
    this._errorCode = null;
    this._supported = Boolean(isPackaged && SUPPORTED_PLATFORMS.has(platform));
    this._configured = Boolean(this._supported && updateFeedUrl);
    this._phase = this._configured ? "idle" : "unavailable";

    try {
      this._automaticUpdates = preferenceStore.read().automaticUpdates;
    } catch (_error) {
      this._automaticUpdates = true;
      this._errorCode = "DESKTOP_UPDATE_PREFERENCES_INVALID";
    }

    this._listeners = Object.freeze({
      checking: () => this._setPhase("checking"),
      available: () => this._setPhase("downloading"),
      notAvailable: () => {
        this._setPhase("up_to_date");
        this._schedule(AUTOMATIC_RECHECK_DELAY_MS);
      },
      downloaded: (_event, _notes, releaseName) => {
        this._releaseName = sanitizedReleaseName(releaseName);
        this._setPhase("ready");
      },
      error: () => this._handleCheckFailure(),
    });

    if (this._configured) {
      autoUpdater.on("checking-for-update", this._listeners.checking);
      autoUpdater.on("update-available", this._listeners.available);
      autoUpdater.on("update-not-available", this._listeners.notAvailable);
      autoUpdater.on("update-downloaded", this._listeners.downloaded);
      autoUpdater.on("error", this._listeners.error);
      try {
        autoUpdater.setFeedURL({ url: updateFeedUrl });
      } catch (_error) {
        this._detachListeners();
        this._configured = false;
        this._phase = "unavailable";
        this._errorCode = "DESKTOP_UPDATE_CONFIGURATION_FAILED";
      }
    }
  }

  getState() {
    return Object.freeze({
      automaticUpdates: this._automaticUpdates,
      canCheck:
        this._configured &&
        !BUSY_PHASES.has(this._phase) &&
        this._phase !== "ready",
      canInstall: this._phase === "ready",
      configured: this._configured,
      currentCommit: this._sourceCommit === null ? null : this._sourceCommit.slice(0, 7),
      currentVersion: this._appVersion,
      errorCode: this._errorCode,
      phase: this._phase,
      progressPercent: this._phase === "ready" ? 100 : null,
      releaseName: this._releaseName,
      supported: this._supported,
    });
  }

  start() {
    if (this._started) return this.getState();
    this._started = true;
    this._schedule(AUTOMATIC_FIRST_CHECK_DELAY_MS);
    return this.getState();
  }

  checkForUpdates() {
    if (!this._configured) {
      throw new DesktopUpdateError("DESKTOP_UPDATE_UNAVAILABLE");
    }
    if (BUSY_PHASES.has(this._phase) || this._phase === "ready") {
      return this.getState();
    }
    this._cancelTimer();
    this._errorCode = null;
    this._setPhase("checking");
    try {
      Promise.resolve(this._autoUpdater.checkForUpdates()).catch(() => {
        this._handleCheckFailure();
      });
    } catch (_error) {
      this._handleCheckFailure();
    }
    return this.getState();
  }

  setAutomaticUpdates(enabled) {
    if (typeof enabled !== "boolean") {
      throw new DesktopUpdateError("DESKTOP_UPDATE_PREFERENCE_INVALID");
    }
    this._preferenceStore.write(enabled);
    this._automaticUpdates = enabled;
    if (enabled) {
      this._schedule(AUTOMATIC_FIRST_CHECK_DELAY_MS);
    } else {
      this._cancelTimer();
    }
    this._emitState();
    return this.getState();
  }

  async installUpdate() {
    if (this._phase !== "ready") {
      throw new DesktopUpdateError("DESKTOP_UPDATE_NOT_READY");
    }
    this._setPhase("installing");
    const accepted = await this._onInstall();
    if (accepted === false) this._setPhase("ready");
    return Object.freeze({ accepted: accepted !== false });
  }

  destroy() {
    this._cancelTimer();
    if (!this._configured) return;
    this._detachListeners();
  }

  _detachListeners() {
    this._autoUpdater.removeListener("checking-for-update", this._listeners.checking);
    this._autoUpdater.removeListener("update-available", this._listeners.available);
    this._autoUpdater.removeListener("update-not-available", this._listeners.notAvailable);
    this._autoUpdater.removeListener("update-downloaded", this._listeners.downloaded);
    this._autoUpdater.removeListener("error", this._listeners.error);
  }

  _handleCheckFailure() {
    if (this._phase === "ready" || this._phase === "installing") return;
    this._errorCode = "DESKTOP_UPDATE_CHECK_FAILED";
    this._setPhase("error");
    this._schedule(AUTOMATIC_RECHECK_DELAY_MS);
  }

  // The commit comes from the verified sidecar manifest once the sidecar starts;
  // anything but a full lowercase SHA-1 shows the version alone.
  setSourceCommit(commit) {
    const next = typeof commit === "string" && SOURCE_COMMIT.test(commit) ? commit : null;
    if (next === this._sourceCommit) return;
    this._sourceCommit = next;
    this._emitState();
  }

  _setPhase(phase) {
    this._phase = phase;
    this._emitState();
  }

  _emitState() {
    this.emit("state", this.getState());
  }

  _cancelTimer() {
    if (this._timer === null) return;
    this._clearTimer(this._timer);
    this._timer = null;
  }

  _schedule(delayMs) {
    this._cancelTimer();
    if (
      !this._started ||
      !this._automaticUpdates ||
      !this._configured ||
      this._phase === "ready" ||
      this._phase === "installing"
    ) {
      return;
    }
    this._timer = this._setTimer(() => {
      this._timer = null;
      try {
        this.checkForUpdates();
      } catch (_error) {
        this._handleCheckFailure();
      }
    }, delayMs);
  }
}

function createDesktopUpdater(options) {
  return new DesktopUpdater(options);
}

module.exports = {
  AUTOMATIC_FIRST_CHECK_DELAY_MS,
  AUTOMATIC_RECHECK_DELAY_MS,
  DesktopUpdateError,
  DesktopUpdater,
  createDesktopUpdater,
  sanitizedReleaseName,
};
