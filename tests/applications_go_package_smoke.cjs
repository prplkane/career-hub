"use strict";

// Exact packaged Windows Go workflow; all records and files are synthetic.
// CAREERHUB_QA_EXPECTATIONS must name independently inspected artifact hashes.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const crypto = require("node:crypto");
const {spawn, execFileSync} = require("node:child_process");
const {chromium} = require("playwright");
const repo = path.resolve(__dirname, "..");
const packageRoot = process.env.CAREERHUB_QA_PACKAGE_ROOT || path.join(repo, "desktop/out/career-hub-win32-x64");
const digest = file => crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex");
const expected = JSON.parse(fs.readFileSync(process.env.CAREERHUB_QA_EXPECTATIONS, "utf8").replace(/^\uFEFF/, ""));
const manifest = JSON.parse(fs.readFileSync(path.join(packageRoot, "resources/career-hub-sidecar/sidecar-manifest.json"), "utf8"));
assert.equal(manifest.source_commit, expected.sourceCommit);
assert.equal(digest(path.join(packageRoot, "resources/app.asar")), expected.asarSHA);
assert.equal(digest(path.join(packageRoot, "resources/career-hub-sidecar/career-hub-sidecar.exe")), expected.sidecarSHA);
const root = fs.mkdtempSync(path.join(os.tmpdir(), "careerhub-applications-go-"));
const home = path.join(root, "Local/CareerHub/dev");
const env = {...process.env, LOCALAPPDATA: path.join(root, "Local"), APPDATA: path.join(root, "Roaming")};
delete env.ELECTRON_RUN_AS_NODE;
delete env.NODE_OPTIONS;
const child = spawn(path.join(packageRoot, "career-hub.exe"), ["--remote-debugging-port=0", "--remote-debugging-address=127.0.0.1"], {
  cwd: packageRoot, env, windowsHide: true, stdio: ["ignore", "pipe", "pipe"],
});
let browser, diagnosticPage, log = "", forced = false;
const errors = [], failed = [];
const responseDiagnostics = [];
const exited = new Promise(resolve => child.once("exit", resolve));
function stopOwnProcess() {
  if (child.exitCode === null) {
    forced = true;
    execFileSync("taskkill.exe", ["/PID", String(child.pid), "/T", "/F"], {windowsHide: true, stdio: "ignore"});
  }
}
const watchdog = setTimeout(stopOwnProcess, 180000);
const debuggerURL = new Promise((resolve, reject) => {
  const timer = setTimeout(() => reject(Error("debugger timeout: " + log)), 20000);
  child.once("error", e => {clearTimeout(timer); reject(e);});
  child.once("exit", code => {clearTimeout(timer); reject(Error("startup exit " + code + ": " + log));});
  const read = chunk => {
    log = (log + chunk).slice(-12000);
    const match = log.match(/DevTools listening on (ws:\/\/127\.0\.0\.1:\d+\/\S+)/);
    if (match) {clearTimeout(timer); resolve(match[1]);}
  };
  child.stdout.on("data", read); child.stderr.on("data", read);
});

async function run() {
  browser = await chromium.connectOverCDP(await debuggerURL, {timeout: 15000});
  const context = browser.contexts()[0], page = context.pages()[0] || await context.waitForEvent("page");
  diagnosticPage = page;
  page.setDefaultTimeout(15000);
  page.on("pageerror", error => errors.push(error.message));
  page.on("console", message => {if (["error", "warning"].includes(message.type())) errors.push(message.text());});
  page.on("response", response => {
    if (response.status() >= 400) {
      const failure = {url: response.url(), status: response.status()};
      failed.push(failure);
      responseDiagnostics.push(response.text().then(body => {failure.body = body.slice(0, 2000);}).catch(() => {}));
    }
  });
  await page.waitForURL("http://127.0.0.1:*/**", {timeout: 25000});
  const origin = new URL(page.url()).origin;
  await page.locator("#vault-setup-name").fill("Synthetic Applications Person");
  await page.locator("#vault-setup-create").click(); await page.locator("#vault-setup-apply").click();
  await page.waitForFunction(() => document.querySelector("#profile-name")?.value === "Synthetic Applications Person");
  await page.locator("#open-interface-settings").click();
  await page.locator("#locale-preference").selectOption("en");
  await page.locator("#desktop-agent-settings summary").click();
  const connection = JSON.parse(await page.locator("#desktop-agent-settings textarea").inputValue());
  assert.equal(digest(connection.executablePath), expected.agentSHA);
  await page.locator("#open-interface-settings").click();
  await page.locator("#interface-settings-panel").waitFor({state: "hidden"});
  // Material drafts require candidate contact fields; seed them through the
  // real Profile editor before preparing the job, then prove it stays intact.
  await page.locator("#edit-profile-button").click();
  await page.locator("#profile-email").fill("synthetic@example.invalid");
  await page.locator("#profile-current-location").fill("Synthetic City");
  const profileSaved = page.waitForResponse(response => response.url().endsWith("/api/vault/profile") && response.request().method() === "PUT");
  await page.locator("#save-profile-button").click();
  assert.equal((await profileSaved).status(), 200);
  await page.locator("#profile-message").filter({hasText: "Profile saved."}).waitFor();
  const profileHash = digest(path.join(home, "career_vault/profile.yaml"));
  const prepared = JSON.parse(execFileSync(connection.executablePath, ["prepare", "--connection", connection.connection,
    "--stdin", "--company", "Synthetic Systems", "--title", "Software Engineer", "--source-name", "synthetic-applications.txt"], {
    input: "Synthetic Systems\nSoftware Engineer\nPython and SQL are required.\nBuild reliable APIs.\n",
    encoding: "utf8", timeout: 30000, windowsHide: true, maxBuffer: 2 * 1024 * 1024,
  }));
  const job = prepared.job.id;
  assert.equal(prepared.write_receipt.applied_recorded, false);
  // ADR SEC-250: the window's token comes from the real preload, not HTTP.
  const session = { token: await page.evaluate(() => window.careerHubDesktop.session.token()) };
  assert.match(session.token, /^[A-Za-z0-9_-]{43}$/);
  async function application() {
    const response = await context.request.get(`${origin}/api/jobs/${job}/application`, {headers: {"X-Career-Hub-Token": session.token}});
    assert.equal(response.status(), 200);
    return response.json();
  }
  const before = await application();
  assert.equal(before.current_status, "preparing");
  const screenshots = [];
  async function capture(name) {
    await page.waitForLoadState("networkidle");
    const bounds = await page.evaluate(() => ({width: innerWidth, scroll: document.documentElement.scrollWidth}));
    assert.ok(bounds.scroll <= bounds.width + 1, name + " document overflow: " + JSON.stringify(bounds));
    const file = path.join(root, name + ".png");
    await page.screenshot({path: file, fullPage: false}); screenshots.push(file);
  }
  for (const [index, width] of [1440, 390].entries()) {
    await page.setViewportSize({width, height: 900});
    await page.goto(origin + "/#applications");
    const row = page.locator(`[data-application-job-id="${job}"].application-open-button`);
    await page.locator("#application-ledger-search").fill("Synthetic Systems");
    await row.waitFor({state: "visible"});
    await page.locator("#application-ledger-search").fill("Synthetic unmatched company");
    await row.waitFor({state: "hidden"});
    await page.locator("#application-ledger-search").fill("Synthetic Systems");
    await row.waitFor({state: "visible"});
    await capture(`applications-${width}`);
    await row.click();
    await page.locator("#application-status-select").waitFor({state: "visible"});
    const current = await application();
    assert.equal(current.current_status, index === 0 ? "preparing" : "applied");
    for (const selector of ["#close-application-drawer", "#open-application-job-button", "#application-status-select", "#save-application-status-button"]) {
      const bounds = await page.locator(selector).evaluate(element => {
        const rect = element.getBoundingClientRect(); return {left: rect.left, right: rect.right, width: rect.width, viewport: innerWidth};
      });
      assert.ok(bounds.width > 0 && bounds.left >= -1 && bounds.right <= bounds.viewport + 1, selector + " geometry: " + JSON.stringify(bounds));
    }
    await page.locator("#application-status-select").selectOption(index === 0 ? "applied" : "interview");
    await page.locator("#application-status-note").fill(`Synthetic explicit ${width}px status note`);
    await page.locator("#save-application-status-button").click();
    await page.locator("#application-message").filter({hasText: "Status added:"}).waitFor();
    const changed = await application();
    assert.equal(changed.current_status, index === 0 ? "applied" : "interview");
    assert.ok(changed.status_history.some(event => event.note === `Synthetic explicit ${width}px status note`));
    await capture(`application-drawer-${width}`);
    await page.locator("#open-application-job-button").click();
    await page.locator("#job-library-workflow").waitFor({state: "visible"});
    assert.equal(await page.locator("#job-draft-select").inputValue(), job);
    await page.locator("#job-workspace-details-tab").click();
    await capture(`workspace-details-${width}`);
    await page.locator("#job-workspace-assessment-tab").click();
    await capture(`workspace-assessment-${width}`);
    if (index === 0) {
      await page.locator('#job-assessment [data-assessment-finding-key="recommendation"]').click();
      await page.locator('#assessment-resolution-dialog input[value="accepted_risk"]').check();
      await page.locator("#assessment-resolution-note").fill("Synthetic reviewed uncertainty");
      await page.locator("#save-assessment-resolution").click();
      await page.locator("#assessment-resolution-dialog").waitFor({state: "hidden"});
      const assessmentResponse = await context.request.get(`${origin}/api/jobs/${job}/assessment`, {headers: {"X-Career-Hub-Token": session.token}});
      assert.equal(assessmentResponse.status(), 200);
      const assessment = await assessmentResponse.json();
      assert.ok(assessment.user_resolutions.some(item => item.finding_key === "recommendation"
        && item.resolution === "accepted_risk" && item.note === "Synthetic reviewed uncertainty"));
      await page.reload({waitUntil: "domcontentloaded"});
      await page.waitForFunction(id => document.querySelector("#job-draft-select")?.value === id, job);
      await page.locator("#job-workspace-assessment-tab").click();
      await page.locator('#job-assessment [data-assessment-finding-key="recommendation"]').click();
      assert.equal(await page.locator('#assessment-resolution-dialog input[value="accepted_risk"]').isChecked(), true);
      assert.equal(await page.locator("#assessment-resolution-note").inputValue(), "Synthetic reviewed uncertainty");
      await page.locator("#close-assessment-resolution-dialog").click();
      await page.locator("#job-workspace-prepare-tab").click();
      await page.locator(".application-resume-upload summary").click();
      await page.locator("#application-resume-file").setInputFiles({name: "synthetic-resume.txt", mimeType: "text/plain", buffer: Buffer.from("Synthetic Applications Person\nSynthetic reviewed resume fixture.\n")});
      await page.locator("#application-resume-display-name").fill("Synthetic retained resume");
      const uploadResponse = page.waitForResponse(response => response.url().endsWith(`/api/jobs/${job}/application/resume`) && response.request().method() === "POST");
      await page.locator("#attach-application-resume-button").click();
      const uploaded = await uploadResponse;
      assert.equal(uploaded.status(), 200, "resume upload: " + await uploaded.text());
      await page.locator("#application-resume-message").filter({hasText: "Added and selected"}).waitFor();
      const selected = await application();
      assert.equal(selected.resume_selection.resume.display_name, "Synthetic retained resume");
      await page.locator("#record-submitted-resume-button").click();
      await page.locator("#application-resume-message").filter({hasText: "Submitted resume recorded"}).waitFor();
      const submitted = await application();
      assert.equal(submitted.applied_resume.resume.id, selected.resume_selection.resume.id);
    } else {
      await page.locator("#job-workspace-prepare-tab").click();
      assert.equal((await application()).applied_resume.resume.display_name, "Synthetic retained resume");
    }
    await capture(`workspace-prepare-${width}`);
  }
  assert.equal(digest(path.join(home, "career_vault/profile.yaml")), profileHash);
  assert.deepEqual(failed, []); assert.deepEqual(errors, []);
  await page.close({runBeforeUnload: true});
  assert.equal(await exited, 0); assert.equal(forced, false);
  await assert.rejects(fetch(origin + "/api/health", {signal: AbortSignal.timeout(1000)}));
  console.log(JSON.stringify({result: "PASS", sourceCommit: manifest.source_commit, root, screenshots,
    scope: "Actual Go package, native CLI fixture, desktop/390px Applications and Workspace, explicit status/notes/review/resume references, unchanged Profile, zero HTTP/console errors, normal shutdown"}));
}
run().catch(async error => {
  console.error(error);
  if (diagnosticPage && !diagnosticPage.isClosed()) {
    const screenshot = path.join(root, "failure.png");
    await diagnosticPage.screenshot({path: screenshot}).catch(() => {});
    await Promise.allSettled(responseDiagnostics);
    console.error(JSON.stringify({root, screenshot, failed, errors,
      resumeMessage: await diagnosticPage.locator("#application-resume-message").textContent().catch(() => null)}));
  }
  process.exitCode = 1; stopOwnProcess();
}).finally(async () => {
  clearTimeout(watchdog); if (browser) await browser.close();
});
