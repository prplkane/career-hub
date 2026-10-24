"use strict";

const fs = require("node:fs");
const path = require("node:path");
const { parseTrustedLoadingUrl } = require("../../desktop/src/loading_protocol");

async function bounded(promise, milliseconds) {
  let timer;
  try {
    return await Promise.race([promise, new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error("Diagnostic deadline")), milliseconds);
    })]);
  } finally {
    clearTimeout(timer);
  }
}

// Only synthetic test windows are inspected. Never record the preload token,
// raw URLs, page text, or connection files in the uploaded diagnostic.
async function captureStartupDiagnostic({ child, browserType, outputRoot, stage, timeout = 2000 }) {
  const result = { stage, exit_code: child.exitCode, sidecar_seen: Boolean(child.sidecarPID), windows: [] };
  let browser;
  try {
    const endpoint = await bounded(child.devtoolsURL, timeout);
    browser = await browserType.connectOverCDP(endpoint, { timeout });
    const pages = browser.contexts().flatMap((context) => context.pages()).slice(0, 2);
    for (const [index, page] of pages.entries()) {
      const loading = parseTrustedLoadingUrl(page.url());
      result.windows.push(loading ? { surface: "startup", ...loading } : { surface: "application_or_other" });
      if (loading) {
        const filename = `startup-failure-${index}.png`;
        try {
          await page.screenshot({ path: path.join(outputRoot, filename), timeout });
          result.windows.at(-1).screenshot = filename;
        } catch { result.windows.at(-1).screenshot_unavailable = true; }
      }
    }
  } catch { result.inspection_unavailable = true; }
  finally {
    if (browser) {
      try { await bounded(browser.close(), timeout); }
      catch { result.disconnect_incomplete = true; }
    }
  }
  try {
    fs.writeFileSync(path.join(outputRoot, "startup-failure.json"), JSON.stringify(result, null, 2));
  } catch { result.report_unavailable = true; }
  return result;
}

module.exports = { captureStartupDiagnostic };
