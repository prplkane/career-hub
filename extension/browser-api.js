"use strict";

/**
 * Small WebExtensions compatibility boundary shared by every extension view.
 *
 * Chrome and Chromium expose `chrome`, while Firefox and Safari prefer the
 * Promise-based `browser` namespace. All supported targets implement the APIs
 * used by Career Hub; the helper surface is the only deliberately divergent
 * UI capability.
 */
((scope) => {
  const api = scope.browser || scope.chrome;
  if (!api?.runtime || !api?.tabs) {
    throw new Error("Career Hub requires the WebExtensions runtime and tabs APIs.");
  }

  function extensionOrigin() {
    const root = api.runtime.getURL("");
    const parsed = new URL(root);
    return `${parsed.protocol}//${parsed.host}`;
  }

  function openHelperTab(targetTabId) {
    if (!Number.isSafeInteger(targetTabId) || targetTabId < 0) {
      return Promise.reject(new Error("Career Hub requires an application tab target."));
    }
    const helperUrl = new URL(api.runtime.getURL("sidepanel.html"));
    helperUrl.searchParams.set("target_tab_id", String(targetTabId));
    return api.tabs.create({
      active: true,
      url: helperUrl.href,
    }).then(() => ({ surface: "tab" }));
  }

  /**
   * This function must be called directly from the popup click handler.
   * Firefox does not carry user activation through runtime messaging or an
   * awaited Promise before `sidebarAction.open()`.
   */
  function openApplicationHelperSurface(windowId, targetTabId) {
    const openTargetTab = () => openHelperTab(targetTabId);
    if (api.sidePanel?.open && Number.isInteger(windowId)) {
      return api.sidePanel.open({ windowId })
        .then(() => ({ surface: "side_panel" }))
        .catch(openTargetTab);
    }
    if (api.sidebarAction?.open) {
      return api.sidebarAction.open()
        .then(() => ({ surface: "sidebar" }))
        .catch(openTargetTab);
    }
    return openTargetTab();
  }

  scope.CareerHubBrowser = Object.freeze({
    api,
    extensionOrigin,
    openApplicationHelperSurface,
  });
})(globalThis);
