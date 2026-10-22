"use strict";

// Actual Electron main + native Go bootstrap. No HTTP mocks, Python server,
// mailbox, private Home, packaged cutover or sandbox-disabling switch.
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawn } = require("node:child_process");
const { chromium } = require("playwright");
const repo = path.resolve(__dirname, "..");
const originalElectron = path.join(repo, "desktop/node_modules/electron/dist/electron.exe");
const executable = process.env.CAREERHUB_ELECTRON_EXECUTABLE || originalElectron;
const testVaultImport = process.env.CAREERHUB_DESKTOP_TEST_VAULT_IMPORT === "1";
const testEmptyProfile = process.env.CAREERHUB_DESKTOP_TEST_EMPTY_PROFILE === "1";
assert.ok(!(testVaultImport && testEmptyProfile), "run setup paths with separate disposable Homes");
const hash = (file) => crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex");

async function captureHidden(page, screenshot) {
  await page.evaluate(() => Promise.race([
    new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))),
    new Promise((_, reject) => setTimeout(() => reject(new Error("hidden window did not repaint")), 5000)),
  ]));
  // Native capturePage keeps the hidden QA window awake without displaying it.
  fs.writeFileSync(screenshot + ".request", "capture", { flag: "wx" });
  const deadline = Date.now() + 15000;
  while (!fs.existsSync(screenshot) && Date.now() < deadline) {
    if (fs.existsSync(screenshot + ".error")) throw new Error(fs.readFileSync(screenshot + ".error", "utf8"));
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  assert.ok(fs.existsSync(screenshot), "hidden Electron capture did not finish");
}

async function run(root, index) {
  const quitFile = path.join(root, `quit-${index}`);
  const screenshot = path.join(root, `desktop-${index}.png`);
  const wrapper = path.join(root, "host.cjs");
  const env = { ...process.env, CAREERHUB_TEST_REPO: repo, CAREERHUB_TEST_QUIT: quitFile,
    CAREERHUB_TEST_SCREENSHOT: screenshot,
    LOCALAPPDATA: path.join(root, "Local"), APPDATA: path.join(root, "Roaming") };
  delete env.ELECTRON_RUN_AS_NODE;
  delete env.NODE_OPTIONS;
  // Software rendering lets a deliberately hidden QA window produce frames.
  // This test-only switch does not disable any browser/renderer sandbox.
  const child = spawn(executable, [wrapper, "--go-preview", "--disable-gpu",
    "--remote-debugging-port=0", "--remote-debugging-address=127.0.0.1"], {
    cwd: repo, env, windowsHide: true, stdio: ["ignore", "pipe", "pipe"],
  });
  let logs = "", browser, origin, forced = false;
  const exited = new Promise((resolve) => {
    child.once("exit", resolve);
    child.once("error", () => resolve(-1));
  });
  const debuggerURL = new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`Electron startup timeout: ${logs}`)), 15000);
    child.once("error", (error) => { clearTimeout(timer); reject(error); });
    child.once("exit", (code) => { clearTimeout(timer); reject(new Error(`Electron startup exit ${code}: ${logs}`)); });
    child.stdout.on("data", (chunk) => { logs = (logs + chunk).slice(-10000); });
    child.stderr.on("data", (chunk) => {
      logs = (logs + chunk).slice(-10000);
      const match = logs.match(/DevTools listening on (ws:\/\/127\.0\.0\.1:\d+\/\S+)/);
      if (match) { clearTimeout(timer); resolve(match[1]); }
    });
  });
  try {
    browser = await chromium.connectOverCDP(await debuggerURL, { timeout: 15000 });
    const context = browser.contexts()[0];
    const page = context.pages()[0] || await context.waitForEvent("page", { timeout: 15000 });
    page.setDefaultTimeout(15000);
    const problems = [];
    page.on("pageerror", (error) => problems.push(error.message));
    page.on("console", (message) => {
      if (["error", "warning"].includes(message.type())) problems.push(message.text());
    });
    await page.waitForURL("http://127.0.0.1:*/**", { timeout: 20000 });
    origin = new URL(page.url()).origin;
    await page.goto(origin + "/#job-feed", { waitUntil: "domcontentloaded" });
    await page.locator("#job-feed-workflow").waitFor({ state: "visible" });
    assert.match(await page.title(), /Career Hub/);
    // ADR SEC-250: the real preload supplies the token; /api/session needs it
    // and returns capabilities only.
    const token = await page.evaluate(() => window.careerHubDesktop.session.token());
    assert.match(token, /^[A-Za-z0-9_-]{43}$/);
    // Checked from Node: an in-page 403 would log a console error the
    // problems gate below must keep treating as a failure.
    assert.equal((await fetch(origin + "/api/session")).status, 403);
    const session = await page.evaluate(async (value) => (await fetch("/api/session", { headers: { "X-Career-Hub-Token": value } })).json(), token);
    assert.equal(session.token, undefined, "no route may return a token");
    assert.equal(session.capabilities.email_connection, true);
    const setup = await page.evaluate(async (token) => {
      const response = await fetch("/api/vault/setup", { headers: { "X-Career-Hub-Token": token } });
      return { status: response.status, body: await response.json() };
    }, token);
    assert.equal(setup.status, 200);
    assert.equal(setup.body.state, (testVaultImport || testEmptyProfile) && index === 1 ? "configured" : "not_configured", "do not silently fabricate a Profile");
    if (testVaultImport) {
      await page.goto(origin + "/#profile");
      if (index === 0) {
        await page.locator("#vault-setup-panel").waitFor({ state: "visible" });
        assert.equal(await page.locator("#profile-form").isVisible(), false);
        await captureHidden(page, screenshot);
        const directory = path.join(root, "vault-input");
        const files = fs.readdirSync(directory).map((name) => path.join(directory, name));
        await page.locator("#vault-setup-files").setInputFiles(files.slice(1));
        await page.locator("#vault-setup-preview").click();
        await page.locator("#vault-setup-message").filter({ hasText: "Select each of the eight" }).waitFor();
        assert.equal(await page.locator("#vault-setup-apply").isDisabled(), true);
        await page.locator("#vault-setup-files").setInputFiles(files);
        const [checked] = await Promise.all([
          page.waitForResponse((item) => item.url() === origin + "/api/vault/setup/preview"),
          page.locator("#vault-setup-preview").click(),
        ]);
        const plan = await checked.json();
        assert.equal(checked.status(), 200, JSON.stringify(plan));
        assert.equal(plan.create.length, 8);
        const [saved] = await Promise.all([
          page.waitForResponse((item) => item.url() === origin + "/api/vault/setup/import"),
          page.locator("#vault-setup-apply").click(),
        ]);
        assert.equal(saved.status(), 200, JSON.stringify(await saved.json()));
      }
      await page.locator("#profile-name").waitFor({ state: "visible" });
      await page.waitForFunction(() => document.querySelector("#profile-name")?.value === "Synthetic Setup Candidate");
      assert.equal(await page.locator("#vault-setup-panel").isVisible(), false);
      await page.goto(origin + "/#job-feed");
      await page.locator("#job-feed-workflow").waitFor({ state: "visible" });
    }

    if (testEmptyProfile) {
      assert.equal(session.capabilities.vault_setup_empty, true);
      await page.goto(origin + "/#profile");
      if (index === 0) {
        await page.locator("#vault-setup-new").waitFor({ state: "visible" });
        assert.equal(await page.locator("#profile-form").isVisible(), false);
        assert.equal(await page.locator("#vault-setup-create").isDisabled(), true);
        await captureHidden(page, screenshot);
        await page.locator("#vault-setup-name").fill("Synthetic Empty Candidate");
        const [preview] = await Promise.all([
          page.waitForResponse((item) => item.url() === origin + "/api/vault/setup/preview"),
          page.locator("#vault-setup-create").click(),
        ]);
        assert.equal(preview.status(), 200);
        assert.equal((await preview.json()).create.length, 8);
        await page.locator("#vault-setup-name").fill("Synthetic New Candidate");
        assert.equal(await page.locator("#vault-setup-apply").isDisabled(), true);
        await page.locator("#vault-setup-create").click();
        await page.locator("#vault-setup-apply").click();
        await page.waitForFunction(() => document.querySelector("#profile-name")?.value === "Synthetic New Candidate");
        assert.equal(await page.locator("#vault-setup-panel").isVisible(), false);
        await page.locator("#edit-profile-button").click();
        assert.equal(await page.locator("#profile-major").getAttribute("required"), null);
        await page.locator("#profile-name").fill("Synthetic Empty Edited");
        await page.locator("#save-profile-button").click();
        await page.locator("#profile-message").filter({ hasText: "Profile saved." }).waitFor();
        await page.locator("#edit-profile-button").click();
        for (const value of ["true", ""]) {
          await page.locator("#eligibility-sponsorship-future").selectOption(value);
          await page.locator("#eligibility-stem-opt").selectOption(value);
          const [saved] = await Promise.all([
            page.waitForResponse((item) => item.url() === origin + "/api/vault/eligibility" && item.request().method() === "PUT"),
            page.locator("#save-eligibility-profile-button").click(),
          ]);
          assert.equal(saved.status(), 200);
          const payload = await saved.json();
          assert.equal(payload.vault_schema_version, 2);
          assert.equal(payload.record.requires_future_sponsorship ?? null, value === "" ? null : true);
          assert.equal(payload.record.stem_opt_eligible ?? null, value === "" ? null : true);
          // Runtime localization may translate the message before the assertion.
          await page.locator("#eligibility-profile-message.success").waitFor();
          assert.ok((await page.locator("#eligibility-profile-message").textContent()).trim());
        }
      } else {
        await page.waitForFunction(() => document.querySelector("#profile-name")?.value === "Synthetic Empty Edited");
        assert.doesNotMatch(await page.locator("#eligibility-current-status").getAttribute("placeholder"), /F-1|OPT/);
        assert.doesNotMatch(await page.locator("#eligibility-work-authorization").getAttribute("placeholder"), /Authorized|United States|Разрешено/);
      }
      assert.equal(await page.locator("#eligibility-stem-opt").inputValue(), "");
      assert.equal(await page.locator("#eligibility-sponsorship-future").inputValue(), "");
      await page.goto(origin + "/#job-feed");
      await page.locator("#job-feed-workflow").waitFor({ state: "visible" });
    }
    await page.locator('[data-discovery-mode="email"]').click();
    assert.equal(await page.locator("#email-connection-save").isDisabled(), false);
    await page.locator(".email-import-fallback > summary").click();
    await page.locator("#email-import-files").setInputFiles(path.join(repo, "tests/fixtures/email_alerts/capture_quality_drift.eml"));
    const [response] = await Promise.all([
      page.waitForResponse((item) => item.url() === origin + "/api/discoveries/email-imports"),
      page.locator("#email-import-button").click(),
    ]);
    const imported = await response.json();
    assert.equal(response.status(), 201, JSON.stringify(imported));
    assert.equal(imported.summary.discoveries_created, index === 0 ? 1 : 0);
    if (index === 1) assert.equal(imported.summary.discoveries_duplicate, 1);
    await page.locator('[data-discovery-mode="feed"]').click();
    const row = page.locator("#discovery-list [data-discovery-id]").filter({ hasText: "Platform Engineer" }).first();
    await row.waitFor({ state: "visible" });
    const discoveryID = await row.getAttribute("data-discovery-id");
    await row.click();
    await page.locator("#discovery-detail h2").filter({ hasText: "Platform Engineer" }).waitFor();
    assert.equal(await page.locator('.job-feed-posting-link').getAttribute('href'), 'https://jobs.example.test/jobs/quality-101');
    assert.equal(await page.locator("vite-error-overlay, #webpack-dev-server-client-overlay").count(), 0);
    assert.deepEqual(problems, []);
    if (testVaultImport) {
      await page.goto(origin + "/#profile");
      await page.waitForFunction(() => document.querySelector("#profile-name")?.value === "Synthetic Setup Candidate");
      assert.deepEqual(problems, []);
    }
    if (testEmptyProfile) {
      await page.goto(origin + "/#profile");
      await page.waitForFunction(() => document.querySelector("#profile-name")?.value === "Synthetic Empty Edited");
      assert.deepEqual(problems, []);
    }
    if (!fs.existsSync(screenshot)) await captureHidden(page, screenshot);
    const security = logs.split(/\r?\n/).filter((line) => line.startsWith("QA_SECURITY ")).map((line) => JSON.parse(line.slice(12)));
    assert.ok(security.length > 0, "actual BrowserWindow security must be observed");
    for (const preferences of security) assert.deepEqual(preferences, {
      sandbox: true, contextIsolation: true, nodeIntegration: false, webSecurity: true,
    });
    assert.doesNotMatch(logs, /QA_PROCESS_GONE/);
    return { origin, discoveryID, screenshot, viewport: await page.evaluate(() => [innerWidth, innerHeight]) };
  } finally {
    if (child.exitCode === null) fs.writeFileSync(quitFile, "quit", { flag: "wx" });
    const timer = setTimeout(() => { forced = true; child.kill(); }, 12000);
    const code = await exited;
    clearTimeout(timer);
    if (browser) await browser.close();
    assert.equal(forced, false, `normal shutdown did not complete: ${logs}`);
    assert.equal(code, 0, `Electron failed: ${logs}`);
    if (origin) await assert.rejects(fetch(origin + "/api/health", { signal: AbortSignal.timeout(1500) }), "owned sidecar listener survived app quit");
  }
}

async function main() {
  assert.equal(process.platform, "win32");
  assert.ok(path.isAbsolute(executable));
  assert.equal(hash(executable), hash(originalElectron), "alternate QA runtime must be the same Electron binary");
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "career-hub-electron-go-ui-"));
  if (testVaultImport) {
    const directory = path.join(root, "vault-input");
    fs.mkdirSync(directory);
    const files = {
      "profile.yaml": "name: Synthetic Setup Candidate\ntarget_location: US\nwork_authorization: Synthetic only\nmajor: Computing\n",
      "education.yaml": "schools: []\n",
      "visa_profile.yaml": "current_status: Synthetic\nwork_authorization: Synthetic only\nrequires_future_sponsorship: false\nstem_opt_eligible: false\n",
      "experience_blocks.yaml": "- {id: exp, company: Synthetic, role: Engineer, dates: '2025', category: technical, technologies: [], proof_points: []}\n",
      "project_blocks.yaml": "- {id: project, name: Synthetic, category: technical, summary: Test, technologies: [], proof_points: []}\n",
      "skill_blocks.yaml": "- {id: skill, skill_family: Technical, related_experience: [exp], keywords: []}\n",
      "proof_blocks.yaml": "- {id: proof, claim: Synthetic, evidence_type: experience, source_block: exp}\n",
      "style_rules.yaml": "standards_metadata: {reviewed_on: '2026-09-01', review_due: '2027-09-01', market: US, authority_order: [user]}\nresume_voice: {}\nbullet_rules: {}\ndocument_rules: {layout: single_column, standard_sections: [Experience]}\nats_rules: {}\nselection_rules: {allowed_decisions: [full, compact, excluded]}\nquality_gates: {}\nforbidden: []\n",
    };
    for (const [name, content] of Object.entries(files)) fs.writeFileSync(path.join(directory, name), content, { flag: "wx" });
  }
  // Local fixture control only: prevent popping up a window; app.quit still
  // traverses the real lifecycle/supervisor. No renderer escape hatch is added.
  fs.writeFileSync(path.join(root, "host.cjs"), [
    'const { app, BrowserWindow } = require("electron");',
    'const fs = require("node:fs");',
    'BrowserWindow.prototype.show = function () {};',
    'let capturing = false;',
    'const captureTimer = setInterval(async () => {',
    '  const target = process.env.CAREERHUB_TEST_SCREENSHOT;',
    '  if (capturing || !fs.existsSync(target + ".request")) return;',
    '  capturing = true; clearInterval(captureTimer);',
    '  try {',
    '    const window = BrowserWindow.getAllWindows()[0];',
    '    const image = await window.webContents.capturePage(undefined, { stayHidden: true, stayAwake: true });',
    '    if (image.isEmpty()) throw new Error("empty Electron screenshot");',
    '    fs.writeFileSync(target, image.toPNG(), { flag: "wx" });',
    '  } catch (error) { fs.writeFileSync(target + ".error", error.message, { flag: "wx" }); }',
    '}, 100);',
    'const timer = setInterval(() => { if (fs.existsSync(process.env.CAREERHUB_TEST_QUIT)) { clearInterval(timer); app.quit(); } }, 100);',
    'app.on("browser-window-created", (_event, window) => {',
    '  window.webContents.setBackgroundThrottling(false); // QA hidden-window painting only',
    '  const p = window.webContents.getLastWebPreferences();',
    '  console.log("QA_SECURITY " + JSON.stringify({ sandbox: p.sandbox, contextIsolation: p.contextIsolation, nodeIntegration: p.nodeIntegration, webSecurity: p.webSecurity }));',
    '});',
    'app.on("render-process-gone", (_e, _c, d) => console.error("QA_PROCESS_GONE " + JSON.stringify(d)));',
    'app.on("child-process-gone", (_e, d) => console.error("QA_PROCESS_GONE " + JSON.stringify(d)));',
    'require(process.env.CAREERHUB_TEST_REPO + "/desktop/src/main.js");',
  ].join("\n"), { flag: "wx" });
  const first = await run(root, 0);
  const home = path.join(root, "Local/CareerHub/dev-go-preview");
  const marker = fs.readFileSync(path.join(home, ".career_hub_data_home.json"));
  const second = await run(root, 1);
  assert.deepEqual(fs.readFileSync(path.join(home, ".career_hub_data_home.json")), marker);
  assert.equal(first.discoveryID, second.discoveryID, "restart/reimport must retain the same discovery");
  if (testVaultImport) {
    const directory = path.join(root, "vault-input");
    for (const name of fs.readdirSync(directory)) {
      assert.equal(hash(path.join(directory, name)), hash(path.join(home, "career_vault", name)), "Vault file changed during import/restart");
    }
  }
  if (testEmptyProfile) {
    for (const name of ["experience_blocks", "project_blocks", "skill_blocks", "proof_blocks"]) {
      assert.equal(fs.readFileSync(path.join(home, "career_vault", name + ".yaml"), "utf8"), "[]\n");
    }
    assert.doesNotMatch(fs.readFileSync(path.join(home, "career_vault/visa_profile.yaml"), "utf8"), /\b(true|false)\b/);
  }
  console.log(JSON.stringify({ result: "PASS", vaultImport: testVaultImport, emptyProfile: testEmptyProfile, boundary: "unpackaged actual Electron + Go, hidden software-rendered QA window, sandbox unchanged, native email-file import/dedup/restart/quit; optional explicit Vault import or empty-profile editing/restart; not installed cutover or GPU qualification", root, runs: [first, second] }));
}
main().catch((error) => { console.error(error); process.exitCode = 1; });
