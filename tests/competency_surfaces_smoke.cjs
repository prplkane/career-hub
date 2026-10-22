"use strict";

const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawn, execFileSync } = require("node:child_process");

const PROJECT_ROOT = path.resolve(__dirname, "..");
const OUTPUT_ROOT = path.resolve(
  process.env.CAREER_HUB_COMPETENCY_ARTIFACTS
    || path.join(os.tmpdir(), "career-hub-competency-surface-artifacts"),
);

// Synthetic files captured from the reviewed reference fixture, not product code.
// Native Go creates the Home and schema; the offline fixture inserts one ledger
// row with stdlib sqlite3 only, after the first owned service has stopped.
async function stopNative(child) {
  if (child.exitCode !== null) { assert.equal(child.exitCode, 0); return; }
  const exited = new Promise((resolve) => child.once("exit", resolve));
  let forced = false;
  const timer = setTimeout(() => { forced = true; child.kill(); }, 10000);
  child.stdin.end();
  const code = await exited;
  clearTimeout(timer);
  assert.equal(forced, false, "owned Go fixture required forced shutdown");
  assert.equal(code, 0);
}

async function startSyntheticServer() {
  const binary = process.env.CAREERHUB_GO_SIDECAR || path.join(PROJECT_ROOT,
    "packaging/dist/go-onedir/career-hub-sidecar/career-hub-sidecar.exe");
  assert.ok(path.isAbsolute(binary) && fs.statSync(binary).isFile());
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-native-competency-"));
  const home = path.join(fs.realpathSync.native(root), "home");
  fs.mkdirSync(home);
  const logs = [];
  async function launch() {
    const child = spawn(binary, ["--sidecar-bootstrap"], {
      cwd: PROJECT_ROOT, stdio: ["pipe", "pipe", "pipe"], windowsHide: true,
    });
    child.stderr.on("data", (chunk) => { logs.push(chunk.toString()); if (logs.length > 20) logs.shift(); });
    const ready = new Promise((resolve, reject) => {
      let pending = "";
      const timer = setTimeout(() => reject(new Error("Native fixture startup timed out: " + logs.join(""))), 30000);
      child.once("error", (error) => { clearTimeout(timer); reject(error); });
      child.once("exit", (code) => { clearTimeout(timer); reject(new Error("Native fixture exited: " + code)); });
      child.stdout.on("data", (chunk) => {
        pending += chunk;
        if (pending.length > 65536) { clearTimeout(timer); reject(new Error("Native fixture output limit")); return; }
        if (!pending.includes("\n")) return;
        clearTimeout(timer);
        try { resolve(JSON.parse(pending.split("\n")[0])); } catch (error) { reject(error); }
      });
    });
    // Like the desktop host (ADR SEC-250): the window token goes only into the
    // start record, and the page receives it from a preload stand-in below.
    const sessionToken = crypto.randomBytes(32).toString("base64url");
    child.stdin.write(JSON.stringify({ command: "start", protocol_version: 1,
      port: 0, channel: "dev", resource_root: fs.realpathSync.native(PROJECT_ROOT), data_root: home,
      session_token: sessionToken }) + "\n");
    try {
      const event = await ready;
      assert.equal(event.event, "ready", JSON.stringify(event));
      return { child, origin: `http://127.0.0.1:${event.port}`, sessionToken };
    } catch (error) { await stopNative(child); throw error; }
  }
  const initial = await launch();
  await stopNative(initial.child);
  const fixture = JSON.parse(fs.readFileSync(path.join(__dirname, "fixtures/competency_home.json")));
  // The native app also loads the selected job's material editor in the
  // background; give this synthetic profile its required contact field.
  fixture.files["career_vault/profile.yaml"] += "contact:\n  email: synthetic@example.test\n";
  for (const [relative, content] of Object.entries(fixture.files)) {
    const target = path.resolve(home, relative);
    assert.ok(target.startsWith(home + path.sep) && !path.isAbsolute(relative));
    fs.mkdirSync(path.dirname(target), { recursive: true });
    if (fs.existsSync(target)) {
      assert.ok(relative.startsWith("career_vault/taxonomy/"));
      const initialCatalog = relative.endsWith("/skill_groups.yaml")
        ? "- id: user_my_custom_skills\n  name: My custom skills\n  archived: false" : "[]";
      assert.equal(fs.readFileSync(target, "utf8").trim(), initialCatalog, "only fresh bootstrap catalogs may be seeded");
      fs.writeFileSync(target, content);
    } else fs.writeFileSync(target, content, { flag: "wx" });
  }
  const db = path.join(home, "applications/career_hub.sqlite3");
  assert.ok(fs.statSync(db).isFile());
  execFileSync(process.env.CAREER_HUB_PYTHON || "python", ["-I", "-c", [
    "import sqlite3,sys",
    "with sqlite3.connect(sys.argv[1]) as db:",
    "    db.execute(\"INSERT INTO job_applications(job_id,company,title,source_url,content_fingerprint,current_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)\", ('synthetic_demand_job','Example Co','Engineer','', 'f'*64,'captured','2026-08-20T12:00:00+00:00','2026-08-20T12:00:00+00:00'))",
  ].join("\n"), db], { encoding: "utf8", timeout: 60000, windowsHide: true }); // a cold Python start on a hosted runner has exceeded 10 s
  const current = await launch();
  return { child: current.child, bootstrap: { origin: current.origin, data_root: home, sessionToken: current.sessionToken }, logs };
}

function canonicalSnapshot(dataRoot) {
  const roots = ["career_vault", path.join("jobs", "incoming"), path.join("jobs", "normalized")];
  const snapshot = {};
  const visit = (absolute, relative) => {
    if (!fs.existsSync(absolute)) return;
    for (const entry of fs.readdirSync(absolute, { withFileTypes: true })
      .sort((left, right) => left.name.localeCompare(right.name))) {
      const childAbsolute = path.join(absolute, entry.name);
      const childRelative = path.join(relative, entry.name);
      if (entry.isDirectory()) visit(childAbsolute, childRelative);
      else if (entry.isFile()) {
        snapshot[childRelative.split(path.sep).join("/")] = crypto
          .createHash("sha256")
          .update(fs.readFileSync(childAbsolute))
          .digest("hex");
      }
    }
  };
  roots.forEach((relative) => visit(path.join(dataRoot, relative), relative));
  return snapshot;
}

async function waitForNoHorizontalOverflow(page) {
  await page.waitForFunction(() => document.documentElement.scrollWidth <= window.innerWidth);
  assert.equal(
    await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    true,
  );
}

async function openDemandDetail(page, name) {
  const row = page.getByRole("button", { name: `Open demand detail for ${name}` });
  await row.waitFor();
  await row.focus();
  await row.click();
  const close = page.locator("#close-demand-gap-drawer");
  await close.waitFor();
  await page.waitForFunction(() => document.activeElement?.id === "close-demand-gap-drawer");
  return { row, close };
}

async function main() {
  const { chromium } = require("playwright");
  fs.mkdirSync(OUTPUT_ROOT, { recursive: true });
  const profileRoot = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-competency-profile-"));
  const { child: server, bootstrap, logs } = await startSyntheticServer();
  const before = canonicalSnapshot(bootstrap.data_root);
  let context;
  try {
    const browserExecutable = process.env.CAREER_HUB_BROWSER_EXECUTABLE;
    context = await chromium.launchPersistentContext(profileRoot, {
      ...(browserExecutable
        ? { executablePath: path.resolve(browserExecutable) }
        : { channel: "chromium" }),
      headless: true,
      viewport: { width: 1440, height: 1000 },
    });
    await context.addInitScript((token) => {
      window.careerHubDesktop = Object.freeze({ session: Object.freeze({ token: async () => token }) });
    }, bootstrap.sessionToken);
    const page = await context.newPage();
    const consoleErrors = [];
    const failedResponses = [];
    const failedResponseReads = [];
    const pageErrors = [];
    page.on("console", (message) => {
      if (message.type() === "error") consoleErrors.push(message.text());
    });
    page.on("pageerror", (error) => pageErrors.push(String(error)));
    page.on("response", (response) => {
      if (response.status() >= 400) {
        failedResponseReads.push(response.text().then((body) => {
          failedResponses.push(`${response.status()} ${response.url()} ${body.slice(0, 1500)}`);
        }));
      }
    });

    await page.goto(`${bootstrap.origin}/#skills/all`, { waitUntil: "networkidle" });
    await page.locator("#skills-all-surface:not([hidden])").waitFor();
    const demandPayload = () => page.evaluate(async () => {
      const { CareerHubApiClient } = await import("/ui/api.js");
      return new CareerHubApiClient().request("/api/demand-gaps?as_of=2026-09");
    });
    const beforeRename = await demandPayload();
    const cardPath = path.join(bootstrap.data_root, "jobs/normalized/synthetic_demand_job.yaml");
    const upperCardPath = cardPath.replace(/\.yaml$/, ".YAML");
    fs.renameSync(cardPath, upperCardPath);
    try { assert.deepEqual(await demandPayload(), beforeRename, "extension casing changed saved-job demand"); }
    finally { fs.renameSync(upperCardPath, cardPath); }
    const search = page.locator("#competency-search");
    await search.fill("Synthetic Thin");
    const thinRow = page.locator('[data-competency-id="user_synthetic_thin"]');
    await thinRow.waitFor();
    assert.equal(await page.locator("#competency-list .competency-row").count(), 1);
    await thinRow.click();
    await page.getByRole("heading", { name: "Synthetic Thin", exact: true }).waitFor();
    assert.match(await page.locator("#competency-detail .competency-aliases").innerText(), /Thin alias/);
    assert.match(await page.locator("#competency-detail").innerText(), /Synthetic Thin Certification/);
    assert.equal(await thinRow.getAttribute("aria-selected"), "true");

    await search.fill("Python");
    const pythonRow = page.locator('[data-competency-id="python"]');
    await pythonRow.waitFor();
    await pythonRow.click();
    await page.getByRole("heading", { name: "Python", exact: true }).waitFor();
    assert.match(await page.locator("#competency-detail").innerText(), /Experience/);
    await page.screenshot({
      path: path.join(OUTPUT_ROOT, "01-skills-desktop.png"),
      fullPage: true,
    });

    await page.locator("#competency-detail .competency-open-record").first().click();
    await page.waitForURL(/#vault$/);
    await page.locator("#vault-experience-form").waitFor();

    await page.goto(`${bootstrap.origin}/#competencies`, { waitUntil: "networkidle" });
    await page.waitForURL(/#skills\/all$/);
    await page.locator("#skills-all-surface:not([hidden])").waitFor();

    await page.goto(`${bootstrap.origin}/#demand-gaps`, { waitUntil: "networkidle" });
    await page.getByRole("heading", { name: "Evidence coverage", exact: true }).waitFor();
    await page.getByRole("button", { name: "Open demand detail for Python" }).waitFor();
    assert.equal((await page.locator("#demand-gaps-result-count").innerText()).trim(), "3 requested skills");
    assert.equal(
      await page.locator("#demand-gaps-coverage-bar svg").getAttribute("aria-label"),
      "Evidence coverage: 1 supported, 1 thin evidence, 1 evidence not found",
    );
    await page.screenshot({
      path: path.join(OUTPUT_ROOT, "02-demand-desktop.png"),
      fullPage: true,
    });

    await page.locator('.demand-gaps-coverage-tabs [data-demand-gap-coverage="evidence_not_found"]').click();
    await page.getByRole("button", { name: "Open demand detail for Synthetic Missing" }).waitFor();
    assert.equal(await page.locator("#demand-gaps-rows .demand-gap-row").count(), 1);
    let detail = await openDemandDetail(page, "Synthetic Missing");
    assert.equal((await page.locator("#demand-gap-drawer-title").innerText()).trim(), "Synthetic Missing");
    assert.match(await page.locator("#demand-gap-drawer-status").innerText(), /Evidence not found in Career Vault\./);
    await page.screenshot({
      path: path.join(OUTPUT_ROOT, "03-demand-drawer-desktop.png"),
      fullPage: true,
    });
    await detail.close.click();
    await page.locator("#demand-gap-drawer").waitFor({ state: "hidden" });
    assert.equal(
      await page.evaluate(() => document.activeElement?.dataset?.demandGapId),
      "user_synthetic_missing",
    );

    detail = await openDemandDetail(page, "Synthetic Missing");
    await page.getByRole("button", { name: "Open in Skills" }).click();
    await page.locator("#skills-all-surface:not([hidden])").waitFor();
    await page.getByRole("heading", { name: "Synthetic Missing", exact: true }).waitFor();
    assert.equal(new URL(page.url()).hash, "#skills/all");
    assert.match(await page.locator("#competency-detail").innerText(), /No linked records/);
    await page.screenshot({
      path: path.join(OUTPUT_ROOT, "04-demand-to-skills.png"),
      fullPage: true,
    });

    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(`${bootstrap.origin}/#demand-gaps`, { waitUntil: "networkidle" });
    await page.getByRole("button", { name: "Open demand detail for Synthetic Missing" }).waitFor();
    await waitForNoHorizontalOverflow(page);
    detail = await openDemandDetail(page, "Synthetic Missing");
    await waitForNoHorizontalOverflow(page);
    const drawerBox = await page.locator("#demand-gap-drawer").boundingBox();
    assert.ok(drawerBox);
    assert.ok(drawerBox.x >= 0 && drawerBox.width <= 390);
    await page.screenshot({
      path: path.join(OUTPUT_ROOT, "05-demand-mobile.png"),
      fullPage: true,
    });
    await page.keyboard.press("Escape");
    await page.locator("#demand-gap-drawer").waitFor({ state: "hidden" });
    assert.equal(
      await page.evaluate(() => document.activeElement?.dataset?.demandGapId),
      "user_synthetic_missing",
    );

    await page.goto(`${bootstrap.origin}/#skills/all`, { waitUntil: "networkidle" });
    await page.locator("#skills-all-surface:not([hidden])").waitFor();
    await page.locator("#competency-search").fill("Synthetic Thin");
    await page.locator('[data-competency-id="user_synthetic_thin"]').click();
    await page.getByRole("heading", { name: "Synthetic Thin", exact: true }).waitFor();
    await waitForNoHorizontalOverflow(page);
    await page.screenshot({
      path: path.join(OUTPUT_ROOT, "06-skills-mobile.png"),
      fullPage: true,
    });

    assert.deepEqual(pageErrors, []);
    await Promise.all(failedResponseReads);
    assert.deepEqual(failedResponses, []);
    assert.deepEqual(consoleErrors, []);
    assert.deepEqual(canonicalSnapshot(bootstrap.data_root), before);
    console.log(`competency surfaces smoke: passed; screenshots: ${OUTPUT_ROOT}`);
  } finally {
    if (context) await context.close();
    await stopNative(server);
    if (logs.length) process.stderr.write(logs.join(""));
  }
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
