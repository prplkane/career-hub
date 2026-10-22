"use strict";

const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld(
  "careerHubDesktop",
  Object.freeze({
    session: Object.freeze({
      token: () => ipcRenderer.invoke("career-hub:session:token"),
    }),
    agent: Object.freeze({
      getConnection: () => ipcRenderer.invoke("career-hub:agent:get-connection"),
      copyConnection: () => ipcRenderer.invoke("career-hub:agent:copy-connection"),
    }),
    advanceDataHomeRecovery: (request) =>
      ipcRenderer.invoke("career-hub:advance-data-home-recovery", request),
    applyDataHomeMigration: () =>
      ipcRenderer.invoke("career-hub:apply-data-home-migration"),
    getDataHomeRecoveryAvailability: () =>
      ipcRenderer.invoke("career-hub:get-data-home-recovery-availability"),
    planDataHomeRecovery: () =>
      ipcRenderer.invoke("career-hub:plan-data-home-recovery"),
    planDataHomeMigration: () =>
      ipcRenderer.invoke("career-hub:plan-data-home-migration"),
    retryStartup: () => ipcRenderer.invoke("career-hub:retry-startup"),
    restores: Object.freeze({
      getState: () => ipcRenderer.invoke("career-hub:restores:get-state"),
      choose: () => ipcRenderer.invoke("career-hub:restores:choose"),
      start: (request) => ipcRenderer.invoke("career-hub:restores:start", request),
      returnToApp: () => ipcRenderer.invoke("career-hub:restores:return"),
      onState: (callback) => {
        if (typeof callback !== "function") throw new TypeError("Restore state callback is required");
        const listener = (_event, state) => callback(state);
        ipcRenderer.on("career-hub:restores:state", listener);
        return () => ipcRenderer.removeListener("career-hub:restores:state", listener);
      },
    }),
    backups: Object.freeze({
      getState: () => ipcRenderer.invoke("career-hub:backups:get-state"),
      start: (request) => ipcRenderer.invoke("career-hub:backups:start", request),
      returnToApp: () => ipcRenderer.invoke("career-hub:backups:return"),
      onState: (callback) => {
        if (typeof callback !== "function") throw new TypeError("Backup state callback is required");
        const listener = (_event, state) => callback(state);
        ipcRenderer.on("career-hub:backups:state", listener);
        return () => ipcRenderer.removeListener("career-hub:backups:state", listener);
      },
    }),
    updates: Object.freeze({
      check: () => ipcRenderer.invoke("career-hub:updates:check"),
      getState: () => ipcRenderer.invoke("career-hub:updates:get-state"),
      install: () => ipcRenderer.invoke("career-hub:updates:install"),
      onState: (callback) => {
        if (typeof callback !== "function") {
          throw new TypeError("Update state callback is required");
        }
        const listener = (_event, state) => callback(state);
        ipcRenderer.on("career-hub:updates:state", listener);
        return () => ipcRenderer.removeListener("career-hub:updates:state", listener);
      },
      setAutomatic: (enabled) =>
        ipcRenderer.invoke("career-hub:updates:set-automatic", enabled),
    }),
  }),
);

function interceptExternalAnchor(event) {
  if (
    !event.isTrusted ||
    event.defaultPrevented ||
    (event.type === "click" && event.button !== 0) ||
    (event.type === "auxclick" && event.button !== 1)
  ) {
    return;
  }
  const anchor = event.composedPath().find(
    (candidate) => candidate?.tagName === "A" && typeof candidate.href === "string",
  );
  if (!anchor?.href) return;

  let target;
  try {
    target = new URL(anchor.href, window.location.href);
  } catch (_error) {
    return;
  }
  if (
    (target.protocol === "http:" || target.protocol === "https:") &&
    target.origin === window.location.origin
  ) {
    return;
  }

  event.preventDefault();
  event.stopImmediatePropagation();
  void ipcRenderer.invoke("career-hub:open-external", target.toString()).catch(() => {});
}

window.addEventListener("click", interceptExternalAnchor, true);
window.addEventListener("auxclick", interceptExternalAnchor, true);
