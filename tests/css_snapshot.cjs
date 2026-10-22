"use strict";

// Computed-style snapshot of the static UI, used to prove CSS refactors are
// visually neutral. Serves src/intake_ui_static with the production CSP and a
// fictional API ({} for every call), captures computed styles for every
// element in every view, both themes, desktop and mobile.
//
//   node tests/css_snapshot.cjs capture <out.json> [--screenshots <dir>]
//   node tests/css_snapshot.cjs compare <before.json> <after.json> [--allow color,font,radius,layout,markup]
//
// Playwright resolves from node_modules; from a worktree without it, set
// NODE_PATH to the main checkout's node_modules.

const fs = require("node:fs");
const http = require("node:http");
const path = require("node:path");

const staticRoot = path.resolve(__dirname, "..", "src", "intake_ui_static");
const CSP = "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'";
const COMBOS = [
  { theme: "light", width: 1440, height: 1000 },
  { theme: "dark", width: 1440, height: 1000 },
  { theme: "light", width: 390, height: 844 },
  { theme: "dark", width: 390, height: 844 },
];
const GROUPS = {
  color: ["color", "background-color", "border-top-color", "border-right-color", "border-bottom-color", "border-left-color",
    "outline-color", "box-shadow", "fill", "stroke", "background-image", "text-decoration-color"],
  font: ["font-size", "line-height", "font-family", "font-weight", "letter-spacing", "text-transform"],
  radius: ["border-top-left-radius", "border-top-right-radius", "border-bottom-right-radius", "border-bottom-left-radius"],
  layout: ["display", "position", "padding-top", "padding-right", "padding-bottom", "padding-left", "margin-top", "margin-right",
    "margin-bottom", "margin-left", "border-top-width", "border-bottom-width", "border-left-width", "border-right-width",
    "border-top-style", "gap", "grid-template-columns", "opacity", "visibility", "overflow-x", "overflow-y"],
};
const PROPS = Object.values(GROUPS).flat();
const PSEUDO_PROPS = ["content", "background-color", "color"];
const SCREENSHOT_ROUTES = ["#job-feed", "#applications", "#vault", "#skills/all"];

function groupOf(prop) {
  const base = prop.replace(/^::(before|after) /, "");
  if (base === "content") return "markup";
  return Object.keys(GROUPS).find((group) => GROUPS[group].includes(base)) || "color";
}

function startServer() {
  const server = http.createServer((request, response) => {
    const url = new URL(request.url || "/", "http://127.0.0.1");
    const json = (payload) => {
      response.writeHead(200, { "content-type": "application/json", "cache-control": "no-store" });
      response.end(JSON.stringify(payload));
    };
    if (url.pathname === "/api/session") return json({ client: "browser", capabilities: {} });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.resolve(staticRoot, `.${url.pathname === "/" ? "/index.html" : url.pathname}`);
    if (!file.startsWith(`${staticRoot}${path.sep}`) || !fs.existsSync(file) || !fs.statSync(file).isFile()) {
      response.writeHead(404);
      return response.end();
    }
    const type = file.endsWith(".js") ? "text/javascript" : file.endsWith(".css") ? "text/css"
      : file.endsWith(".html") ? "text/html" : file.endsWith(".woff2") ? "font/woff2" : "image/svg+xml";
    response.writeHead(200, { "content-type": type, "content-security-policy": CSP, "cache-control": "no-store" });
    response.end(fs.readFileSync(file));
  });
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve(server));
  });
}

// Runs in the page: returns { key: [values in PROPS order + pseudo values] } for one root.
function collect([rootSelector, scope, props, pseudoProps]) {
  const root = document.querySelector(rootSelector);
  if (!root) return {};
  const keyOf = (element) => {
    const parts = [];
    for (let node = element; node && node !== root.parentElement; node = node.parentElement) {
      if (node.id) { parts.unshift(`#${node.id}`); break; }
      const tag = node.tagName.toLowerCase();
      let index = 1;
      for (let sibling = node.previousElementSibling; sibling; sibling = sibling.previousElementSibling) {
        if (sibling.tagName === node.tagName) index += 1;
      }
      parts.unshift(`${tag}:${index}`);
    }
    return `${scope}|${parts.join(">")}`;
  };
  const out = {};
  for (const element of [root, ...root.querySelectorAll("*")]) {
    if (scope === "shell" && element.closest("section.view, [popover]")) continue;
    const style = getComputedStyle(element);
    const values = props.map((prop) => style.getPropertyValue(prop));
    for (const pseudo of ["::before", "::after"]) {
      const ps = getComputedStyle(element, pseudo);
      for (const prop of pseudoProps) values.push(ps.getPropertyValue(prop));
    }
    out[keyOf(element)] = values;
  }
  return out;
}

async function capture(outFile, screenshotDir) {
  const { chromium } = require("playwright");
  const server = await startServer();
  const origin = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch({ headless: true });
  const result = { props: [...PROPS, ...["::before", "::after"].flatMap((p) => PSEUDO_PROPS.map((q) => `${p} ${q}`))], combos: {} };
  const violations = [];
  try {
    for (const combo of COMBOS) {
      const name = `${combo.theme}-${combo.width}`;
      const context = await browser.newContext({
        viewport: { width: combo.width, height: combo.height },
        reducedMotion: "reduce",
        timezoneId: "UTC",
        colorScheme: combo.theme,
      });
      await context.addInitScript(() => {
        window.careerHubDesktop = Object.freeze({ session: Object.freeze({ token: async () => "fictional-snapshot-token" }) });
      });
      await context.addInitScript((theme) => {
        localStorage.setItem("careerHub.theme", theme);
        localStorage.setItem("careerHub.locale", "en");
        document.addEventListener("securitypolicyviolation", (event) => {
          (window.__cspViolations = window.__cspViolations || []).push(`${event.violatedDirective} ${event.blockedURI}`);
        });
      }, combo.theme);
      const page = await context.newPage();
      await page.goto(`${origin}/`, { waitUntil: "networkidle" });
      await page.waitForFunction(() => !document.documentElement.hasAttribute("data-locale-pending"));
      await page.waitForTimeout(300);

      const snapshot = {};
      Object.assign(snapshot, await page.evaluate(collect, ["body", "shell", PROPS, PSEUDO_PROPS]));
      const views = await page.evaluate(() => [...document.querySelectorAll("section.view")].map((v) => v.id));
      for (const id of views) {
        await page.evaluate((target) => {
          for (const view of document.querySelectorAll("section.view")) view.classList.toggle("active", view.id === target);
        }, id);
        await page.waitForTimeout(30);
        Object.assign(snapshot, await page.evaluate(collect, [`#${id}`, id, PROPS, PSEUDO_PROPS]));
      }
      for (const id of ["interface-settings-panel", "help-center-panel"]) {
        const shown = await page.evaluate((target) => {
          const panel = document.getElementById(target);
          if (!panel || typeof panel.showPopover !== "function") return false;
          panel.showPopover();
          return true;
        }, id);
        if (!shown) continue;
        await page.waitForTimeout(30);
        Object.assign(snapshot, await page.evaluate(collect, [`#${id}`, id, PROPS, PSEUDO_PROPS]));
        await page.evaluate((target) => document.getElementById(target).hidePopover(), id);
      }
      result.combos[name] = snapshot;
      violations.push(...(await page.evaluate(() => window.__cspViolations || [])).map((v) => `${name}: ${v}`));

      if (screenshotDir) {
        fs.mkdirSync(screenshotDir, { recursive: true });
        for (const route of SCREENSHOT_ROUTES) {
          await page.goto(`${origin}/${route}`, { waitUntil: "networkidle" });
          await page.waitForTimeout(250);
          await page.screenshot({ path: path.join(screenshotDir, `${name}-${route.replace(/[#/]/g, "_")}.png`), fullPage: true });
        }
        await page.evaluate(() => document.getElementById("interface-settings-panel")?.showPopover());
        await page.waitForTimeout(150);
        await page.screenshot({ path: path.join(screenshotDir, `${name}-settings.png`) });
      }
      await context.close();
    }
  } finally {
    await browser.close();
    await new Promise((resolve) => server.close(resolve));
  }
  fs.mkdirSync(path.dirname(path.resolve(outFile)), { recursive: true });
  fs.writeFileSync(outFile, JSON.stringify(result));
  const counts = Object.entries(result.combos).map(([name, snap]) => `${name}=${Object.keys(snap).length}`).join(" ");
  console.log(`captured ${counts} elements -> ${outFile}`);
  if (violations.length) {
    console.error(`CSP violations (${violations.length}):\n  ${violations.slice(0, 20).join("\n  ")}`);
    process.exitCode = 1;
  }
}

function compare(beforeFile, afterFile, allow) {
  const before = JSON.parse(fs.readFileSync(beforeFile, "utf8"));
  const after = JSON.parse(fs.readFileSync(afterFile, "utf8"));
  if (JSON.stringify(before.props) !== JSON.stringify(after.props)) throw new Error("snapshots use different property lists; recapture both");
  const counts = {};
  const samples = [];
  const bump = (group, line) => {
    counts[group] = (counts[group] || 0) + 1;
    if (samples.length < 50) samples.push(`[${group}] ${line}`);
  };
  for (const combo of Object.keys({ ...before.combos, ...after.combos })) {
    const a = before.combos[combo] || {};
    const b = after.combos[combo] || {};
    for (const key of new Set([...Object.keys(a), ...Object.keys(b)])) {
      if (!a[key] || !b[key]) {
        bump("markup", `${combo} ${key} ${a[key] ? "removed" : "added"}`);
        continue;
      }
      before.props.forEach((prop, index) => {
        if (a[key][index] !== b[key][index]) bump(groupOf(prop), `${combo} ${key} ${prop}: ${a[key][index]} -> ${b[key][index]}`);
      });
    }
  }
  const total = Object.values(counts).reduce((sum, n) => sum + n, 0);
  console.log(`differences: ${total}${total ? " " + JSON.stringify(counts) : ""}`);
  for (const line of samples) console.log(`  ${line}`);
  const disallowed = Object.keys(counts).filter((group) => !allow.includes(group));
  if (disallowed.length) {
    console.error(`not allowed: ${disallowed.join(", ")}`);
    process.exitCode = 1;
  }
}

async function main() {
  const [mode, first, second, ...rest] = process.argv.slice(2);
  const option = (flag) => {
    const all = [first, second, ...rest];
    const index = all.indexOf(flag);
    return index >= 0 ? all[index + 1] : undefined;
  };
  if (mode === "capture" && first) return capture(first, option("--screenshots"));
  if (mode === "compare" && first && second) return compare(first, second, (option("--allow") || "").split(",").filter(Boolean));
  console.error("usage: css_snapshot.cjs capture <out.json> [--screenshots <dir>] | compare <a.json> <b.json> [--allow groups]");
  process.exitCode = 2;
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
