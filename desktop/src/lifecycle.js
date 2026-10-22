"use strict";

const RUNNING = "running";
const STOPPING = "stopping";
const FINALIZING = "finalizing";

function createLifecycleController({
  app,
  ensureWindow,
  onShutdownError = () => {},
  platform = process.platform,
  supervisor,
}) {
  if (
    !app ||
    typeof app.quit !== "function" ||
    typeof ensureWindow !== "function" ||
    !supervisor ||
    typeof supervisor.stop !== "function" ||
    typeof onShutdownError !== "function"
  ) {
    throw new TypeError("Desktop lifecycle adapters are required");
  }

  let state = RUNNING;
  let shutdownPromise = null;

  function beginShutdown(finalize = () => app.quit()) {
    if (typeof finalize !== "function") {
      throw new TypeError("Desktop shutdown finalizer is required");
    }
    if (shutdownPromise !== null) return shutdownPromise;

    state = STOPPING;
    const operation = Promise.resolve()
      .then(() => supervisor.stop())
      .then(() => {
        state = FINALIZING;
        finalize();
        return true;
      })
      .catch(async (error) => {
        state = RUNNING;
        if (shutdownPromise === operation) shutdownPromise = null;
        try {
          onShutdownError(error);
        } catch (_callbackError) {
          // The ownership boundary still fails closed if diagnostics fail.
        }
        try {
          await ensureWindow();
        } catch (_windowError) {
          // The exact child remains owned even when the window cannot reopen.
        }
        return false;
      });
    shutdownPromise = operation;
    return shutdownPromise;
  }

  function handleBeforeQuit(event) {
    if (state === FINALIZING) return;
    event.preventDefault();
    void beginShutdown(() => app.quit());
  }

  function handleWindowAllClosed() {
    if (state === RUNNING && platform !== "darwin") {
      app.quit();
    }
  }

  async function requestWindow() {
    if (state !== RUNNING) return null;
    return ensureWindow();
  }

  return Object.freeze({
    beginShutdown,
    get isQuitting() {
      return state !== RUNNING;
    },
    get shutdownPromise() {
      return shutdownPromise;
    },
    get state() {
      return state;
    },
    handleActivate: requestWindow,
    handleBeforeQuit,
    handleSecondInstance: requestWindow,
    handleWindowAllClosed,
  });
}

module.exports = {
  FINALIZING,
  RUNNING,
  STOPPING,
  createLifecycleController,
};
