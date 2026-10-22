"use strict";

// Fictional, read-only browser fixture for the shared Python/Go Diagnostics UI.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const http = require("node:http");
const path = require("node:path");
const { chromium } = require("playwright");

const staticRoot = path.resolve(__dirname, "..", "src", "intake_ui_static");

function event(number) {
  return {
    run_id: `run-${String(number).padStart(2, "0")}`,
    source: "gmail_connection",
    stage: "email_connection_run",
    sequence: 1,
    timestamp: new Date(Date.UTC(2026, 8, 14, 0, 0, number)).toISOString(),
    event: "completed",
    status: "success",
    details: {},
  };
}

async function main() {
  let mode = "history";
  let logRequests = 0;
  const server = http.createServer((request, response) => {
    const url = new URL(request.url || "/", "http://127.0.0.1");
    const json = (status, payload) => {
      response.writeHead(status, { "content-type": "application/json", "cache-control": "no-store" });
      response.end(JSON.stringify(payload));
    };
    if (url.pathname === "/api/session") {
      // Like the real sidecar (ADR SEC-250): capabilities only, never a token.
      json(200, { client: "browser", capabilities: {} });
      return;
    }
    if (url.pathname === "/api/logs") {
      logRequests += 1;
      const after = url.searchParams.get("after") || "0";
      if (mode === "expired") {
        json(409, { error_code: "DIAGNOSTIC_CURSOR_EXPIRED", summary: "The diagnostic cursor expired." });
        return;
      }
      if (mode === "corrupt") {
        json(409, { error_code: "DIAGNOSTIC_LEGACY_CORRUPT", summary: "The saved log needs review." });
        return;
      }
      if (mode === "fresh") {
        json(200, { cursor: "fresh:1", events: after === "0" ? [event(30)] : [], has_more: false });
        return;
      }
      if (mode === "new") {
        json(200, { cursor: "p:21", events: after === "p:20" ? [event(21)] : [], has_more: false });
        return;
      }
      const index = after === "0" ? 0 : Number(after.slice(2));
      if (!Number.isInteger(index) || index < 0 || index > 20) {
        json(400, { error_code: "DIAGNOSTIC_CURSOR_INVALID" });
        return;
      }
      setTimeout(() => {
        json(200, {
          cursor: `p:${Math.min(index + 1, 20)}`,
          events: index < 20 ? [event(index + 1)] : [],
          has_more: index < 19,
        });
      }, 25);
      return;
    }
    if (url.pathname.startsWith("/api/")) {
      json(200, {});
      return;
    }
    const file = path.resolve(staticRoot, `.${url.pathname === "/" ? "/index.html" : url.pathname}`);
    if (!file.startsWith(`${staticRoot}${path.sep}`) || !fs.existsSync(file) || !fs.statSync(file).isFile()) {
      response.writeHead(404);
      response.end();
      return;
    }
    const type = file.endsWith(".js") ? "text/javascript"
      : file.endsWith(".css") ? "text/css"
        : file.endsWith(".html") ? "text/html" : "image/svg+xml";
    response.writeHead(200, { "content-type": type });
    response.end(fs.readFileSync(file));
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  assert(address && typeof address === "object");
  let browser;
  try {
    browser = await chromium.launch({ headless: true });
    const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
    await page.addInitScript(() => {
      window.careerHubDesktop = Object.freeze({ session: Object.freeze({ token: async () => "fictional-browser-token" }) });
    });
    const pageErrors = [];
    page.on("pageerror", (error) => pageErrors.push(error.message));
    await page.goto(`http://127.0.0.1:${address.port}/#diagnostics`);
    await page.locator("#view-diagnostics.active").waitFor();
    await page.waitForFunction(() => document.querySelector("#run-select")?.options.length === 20, { timeout: 8000 });
    assert.equal(await page.locator("#run-select").inputValue(), "run-20");
    assert.equal(await page.locator("#diagnostics-history").isHidden(), true);
    await page.locator("#run-select").selectOption("run-05");
    mode = "new";
    await page.waitForFunction(() => document.querySelector("#run-select")?.options.length === 21);
    assert.equal(await page.locator("#run-select").inputValue(), "run-05");

    mode = "expired";
    await page.locator("#diagnostics-history-retry").waitFor({ state: "visible" });
    assert.match(await page.locator("#diagnostics-history-status").textContent(), /DIAGNOSTIC_CURSOR_EXPIRED/);
    mode = "fresh";
    await page.locator("#diagnostics-history-retry").click();
    await page.waitForFunction(() => document.querySelector("#run-select")?.value === "run-30");
    assert.equal(await page.locator("#run-select option").count(), 1);

    mode = "corrupt";
    await page.locator("#diagnostics-history-retry").waitFor({ state: "visible" });
    assert.equal(await page.locator("#diagnostics-history-retry").textContent(), "Retry reading");
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), true);
    const pausedCount = logRequests;
    await page.waitForTimeout(1600);
    assert.equal(logRequests, pausedCount, "corrupt history should pause polling");
    mode = "fresh";
    await page.locator("#diagnostics-history-retry").click();
    await page.locator("#diagnostics-history").waitFor({ state: "hidden" });
    assert.deepEqual(pageErrors, []);
    console.log("diagnostics history render smoke passed");
  } finally {
    if (browser) await browser.close();
    await new Promise((resolve) => server.close(resolve));
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
