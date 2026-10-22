"use strict";

/**
 * Install one privacy-minimized observer in the current main frame.
 *
 * This function is serialized by the WebExtensions scripting API, so every
 * dependency intentionally lives inside its body. It observes event/state
 * shape only: no input values, raw HTML, screenshots, cookies, or page text is
 * sent to the service worker.
 */
function installCareerHubApplicationSessionObserver(sessionId) {
  const SESSION_PATTERN = /^application_session_[a-f0-9]{32}$/;
  const CONTENT_COMMAND = "career-hub.application-session.content-signal.v1";
  const STATE_KEY = "__careerHubApplicationSessionObserverV1";
  const MAX_SCAN_CHARS = 30000;
  const MAX_SCAN_NODES = 80;
  const CHECK_DELAY_MS = 180;
  const FINAL_ACTION_PATTERN = /^(?:submit(?: (?:my )?application)?|send (?:my )?application|complete (?:my )?application)$/i;
  const SUCCESS_RULES = [
    {
      id: "application_submitted",
      pattern: /\b(?:your\s+)?application\s+(?:has\s+been|was|is)\s+(?:successfully\s+)?submitted\b/i,
    },
    {
      id: "application_received",
      pattern: /\b(?:we(?:'ve|\s+have)|application\s+team\s+has)\s+received\s+your\s+application\b/i,
    },
    {
      id: "successfully_applied",
      pattern: /\byou(?:'ve|\s+have)\s+successfully\s+applied\b/i,
    },
    {
      id: "thank_you_for_applying",
      pattern: /\bthank\s+you\s+for\s+(?:your\s+interest\s+and\s+)?applying\b/i,
    },
  ];
  const SIGNAL_SELECTORS = [
    "main h1",
    "main h2",
    "main [role='status']",
    "main [role='alert']",
    "[role='main'] h1",
    "[role='main'] h2",
    "[role='status']",
    "[data-automation-id*='success' i]",
    "[data-testid*='success' i]",
    "[class*='confirmation' i]",
    "[class*='application-success' i]",
  ];
  const CAPTCHA_SELECTORS = [
    "iframe[src*='recaptcha' i]",
    "iframe[src*='hcaptcha' i]",
    "[data-sitekey]",
    "[class*='captcha' i]",
    "[id*='captcha' i]",
  ];

  if (typeof sessionId !== "string" || !SESSION_PATTERN.test(sessionId)) {
    return { ok: false, error_code: "APPLICATION_SESSION_INVALID" };
  }
  if (!/^https?:$/.test(globalThis.location?.protocol || "")) {
    return { ok: false, error_code: "ACTIVE_TAB_UNSUPPORTED" };
  }

  const previous = globalThis[STATE_KEY];
  if (previous?.sessionId === sessionId && typeof previous.check === "function") {
    previous.check();
    return { ok: true, origin: globalThis.location.origin, reused: true };
  }
  if (previous && typeof previous.dispose === "function") {
    previous.dispose();
  }

  let disposed = false;
  let checkTimer = 0;
  let finalActionTimer = 0;
  let lastSubmitCandidateAt = 0;

  function send(signal) {
    if (disposed) return;
    try {
      const runtime = globalThis.browser?.runtime || globalThis.chrome?.runtime;
      if (!runtime?.sendMessage) return;
      const pending = runtime.sendMessage({
        command: CONTENT_COMMAND,
        session_id: sessionId,
        page_origin: globalThis.location.origin,
        ...signal,
      });
      if (pending && typeof pending.catch === "function") pending.catch(() => {});
    } catch {
      // A stopped/reloaded worker leaves the durable application unchanged.
    }
  }

  function visible(element) {
    if (!(element instanceof Element)) return false;
    const style = globalThis.getComputedStyle(element);
    if (style.display === "none" || style.visibility === "hidden") return false;
    const rect = element.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  }

  function blockedContext() {
    const password = document.querySelector("input[type='password']");
    if (password && visible(password)) return true;
    return CAPTCHA_SELECTORS.some((selector) => {
      try {
        return Array.from(document.querySelectorAll(selector)).some(visible);
      } catch {
        return false;
      }
    });
  }

  function successRule() {
    if (blockedContext()) return "";
    const nodes = [];
    for (const selector of SIGNAL_SELECTORS) {
      let matches = [];
      try {
        matches = Array.from(document.querySelectorAll(selector));
      } catch {
        continue;
      }
      for (const node of matches) {
        if (nodes.length >= MAX_SCAN_NODES) break;
        if (visible(node) && !nodes.includes(node)) nodes.push(node);
      }
      if (nodes.length >= MAX_SCAN_NODES) break;
    }
    if (!nodes.length && document.body && visible(document.body)) nodes.push(document.body);

    let remaining = MAX_SCAN_CHARS;
    const chunks = [];
    for (const node of nodes) {
      if (remaining <= 0) break;
      const text = typeof node.innerText === "string" ? node.innerText : "";
      if (!text) continue;
      const bounded = text.replace(/\s+/g, " ").trim().slice(0, remaining);
      if (!bounded) continue;
      chunks.push(bounded);
      remaining -= bounded.length;
    }
    const visibleText = chunks.join(" ");
    return SUCCESS_RULES.find((rule) => rule.pattern.test(visibleText))?.id || "";
  }

  function check() {
    if (disposed) return;
    const rule = successRule();
    if (rule) {
      send({
        signal: "success_state",
        success_rule: rule,
        observed_at: new Date().toISOString(),
      });
    }
  }

  function scheduleCheck() {
    if (disposed || checkTimer) return;
    checkTimer = globalThis.setTimeout(() => {
      checkTimer = 0;
      check();
    }, CHECK_DELAY_MS);
  }

  function recordSubmitCandidate(signal) {
    if (disposed || blockedContext()) return;
    const now = Date.now();
    if (now - lastSubmitCandidateAt < 750) return;
    lastSubmitCandidateAt = now;
    send({
      signal,
      event_key: crypto.randomUUID(),
      observed_at: new Date(now).toISOString(),
    });
    scheduleCheck();
  }

  function onSubmit(event) {
    const form = event.target;
    if (event.isTrusted !== true || !(form instanceof HTMLFormElement)) return;
    globalThis.queueMicrotask(() => {
      if (disposed || blockedContext()) return;
      if (!form.noValidate && typeof form.checkValidity === "function" && !form.checkValidity()) {
        return;
      }
      if (finalActionTimer) {
        globalThis.clearTimeout(finalActionTimer);
        finalActionTimer = 0;
      }
      recordSubmitCandidate("native_submit");
    });
  }

  function onClick(event) {
    if (event.isTrusted !== true || !(event.target instanceof Element)) return;
    const action = event.target.closest("button, input[type='submit'], [role='button']");
    if (
      !action
      || !visible(action)
      || action.disabled === true
      || action.getAttribute("aria-disabled")?.toLowerCase() === "true"
    ) {
      return;
    }
    const label = (
      action instanceof HTMLInputElement
        ? action.value
        : action.getAttribute("aria-label") || action.innerText || ""
    ).replace(/\s+/g, " ").trim().slice(0, 120);
    if (!FINAL_ACTION_PATTERN.test(label) || finalActionTimer) return;
    finalActionTimer = globalThis.setTimeout(() => {
      finalActionTimer = 0;
      recordSubmitCandidate("final_action_click");
    }, 0);
  }

  const observer = new MutationObserver(scheduleCheck);
  document.addEventListener("submit", onSubmit, true);
  document.addEventListener("click", onClick, true);
  globalThis.addEventListener("pageshow", scheduleCheck);
  globalThis.addEventListener("popstate", scheduleCheck);
  if (document.documentElement) {
    observer.observe(document.documentElement, {
      childList: true,
      characterData: true,
      subtree: true,
    });
  }

  const state = {
    sessionId,
    check: scheduleCheck,
    dispose() {
      if (disposed) return;
      disposed = true;
      if (checkTimer) globalThis.clearTimeout(checkTimer);
      if (finalActionTimer) globalThis.clearTimeout(finalActionTimer);
      observer.disconnect();
      document.removeEventListener("submit", onSubmit, true);
      document.removeEventListener("click", onClick, true);
      globalThis.removeEventListener("pageshow", scheduleCheck);
      globalThis.removeEventListener("popstate", scheduleCheck);
      if (globalThis[STATE_KEY] === state) delete globalThis[STATE_KEY];
    },
  };
  globalThis[STATE_KEY] = state;
  scheduleCheck();
  return { ok: true, origin: globalThis.location.origin, reused: false };
}
