"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const http = require("node:http");
const os = require("node:os");
const path = require("node:path");
const { spawn, spawnSync } = require("node:child_process");
const { chromium } = require("playwright");
const { captureStartupDiagnostic } = require("./helpers/packaged_startup_diagnostic.cjs");

const PROJECT_ROOT = path.resolve(__dirname, "..");
const EXTENSION_ROOT = path.join(PROJECT_ROOT, "extension");
const DEFAULT_APP_ORIGIN = "http://127.0.0.1:8766";
const DEV_EXTENSION_ID = "jhgaecfochimmafoopcpodncoeobecfg";
const GO_PACKAGE_ROOT = path.join(PROJECT_ROOT, "desktop/out/career-hub-win32-x64");
let goEnvironmentRoot;

// Hosted Windows runners start PowerShell and the CIM service cold: the first
// Win32_Process query can take longer than ten seconds. Each call therefore
// gets a generous bound, and primeNativeTools pays the cold-start cost once,
// before the packaged app is launched and the readiness clock starts.
const NATIVE_RECORDS_TIMEOUT_MS = 30000;

function nativeRecords(command, { timeoutMs = NATIVE_RECORDS_TIMEOUT_MS } = {}) {
  const env = { ...process.env };
  for (const key of Object.keys(env)) if (key.toLowerCase() === "psmodulepath") delete env[key];
  const result = spawnSync("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", command],
    { encoding: "utf8", windowsHide: true, timeout: timeoutMs, env });
  assert.ifError(result.error);
  assert.equal(result.status, 0, result.stderr);
  return result.stdout.trim();
}

function primeNativeTools() {
  const started = Date.now();
  nativeRecords(
    `@(Get-CimInstance Win32_Process -Filter "ProcessId = ${process.pid}").Count; `
    + "@(Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue).Count | Out-Null",
    { timeoutMs: 120000 },
  );
  console.log(`extension smoke: native tools primed in ${Date.now() - started} ms`);
}
const OUTPUT_ROOT = path.resolve(
  process.env.CAREER_HUB_EXTENSION_ARTIFACTS
    || path.join(os.tmpdir(), "career-hub-extension-smoke-artifacts"),
);

function delay(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

async function withTimeout(promise, milliseconds, label) {
  let timeout;
  try {
    return await Promise.race([
      promise,
      new Promise((_, reject) => {
        timeout = setTimeout(() => {
          reject(new Error(`${label} timed out after ${milliseconds} ms`));
        }, milliseconds);
      }),
    ]);
  } finally {
    clearTimeout(timeout);
  }
}

async function listen(server) {
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  assert(address && typeof address === "object");
  return `http://127.0.0.1:${address.port}`;
}

async function closeServer(server) {
  if (!server.listening) {
    return;
  }
  await new Promise((resolve, reject) => {
    server.close((error) => (error ? reject(error) : resolve()));
  });
}

function startCareerHub(dataRoot, extensionOrigin, appPort) {
  {
    assert.equal(process.platform, "win32");
    assert.equal(appPort, 8766);
    const metadata = JSON.parse(fs.readFileSync(path.join(GO_PACKAGE_ROOT, "resources/career-hub/build-metadata.json")));
    assert.equal(metadata.schema_version, 4);
    assert.equal(metadata.sidecar_runtime, "go");
    assert.equal(metadata.channel, "dev");
    assert.equal(metadata.update_feed_url, null);
    // A loopback-only DevTools port lets the smoke read the window token from
    // the real preload (ADR SEC-250); no HTTP route returns it.
    const child = spawn(path.join(GO_PACKAGE_ROOT, "career-hub.exe"), ["--extension-origin", extensionOrigin, "--remote-debugging-port=0", "--remote-debugging-address=127.0.0.1"], {
      cwd: GO_PACKAGE_ROOT, windowsHide: true, stdio: ["ignore", "pipe", "pipe"],
      env: { ...process.env, LOCALAPPDATA: path.join(goEnvironmentRoot, "Local"), APPDATA: path.join(goEnvironmentRoot, "Roaming") },
    });
    let logs = "";
    let resolveDevtools;
    child.devtoolsURL = new Promise((resolve) => { resolveDevtools = resolve; });
    for (const stream of [child.stdout, child.stderr]) stream.on("data", (chunk) => {
      logs = (logs + chunk).slice(-16000);
      const match = logs.match(/DevTools listening on (ws:\/\/127\.0\.0\.1:\d+\/\S+)/);
      if (match) resolveDevtools(match[1]);
    });
    child.smokeLogs = () => logs;
    child.goPackaged = true;
    return child;
  }
}

async function stopProcess(child) {
  if (!child || child.exitCode !== null) {
    return;
  }
  if (child.goPackaged) {
    let closeRequested = "";
    try {
      closeRequested = nativeRecords(`(Get-Process -Id ${child.pid}).CloseMainWindow()`);
    } catch (error) {
      nativeRecords(`taskkill.exe /PID ${child.pid} /T /F | Out-Null`);
      throw new Error(`Go packaged app close request failed; its test process tree was terminated: ${error.message}`);
    }
    assert.equal(closeRequested, "True");
    const deadline = Date.now() + 12000;
    while (child.exitCode === null && Date.now() < deadline) await delay(100);
    if (child.exitCode === null) {
      nativeRecords(`taskkill.exe /PID ${child.pid} /T /F | Out-Null`);
      throw new Error("Go packaged app did not close normally; only its test process tree was terminated");
    }
    assert.equal(child.exitCode, 0);
    if (child.sidecarPID) {
      assert.equal(nativeRecords(`@(Get-CimInstance Win32_Process -Filter "ProcessId = ${child.sidecarPID}").Count`), "0");
    }
    return;
  }
  child.kill();
  await Promise.race([
    new Promise((resolve) => child.once("exit", resolve)),
    delay(3000),
  ]);
}

let windowSessionToken = null;

// Reads the window token the way the desktop UI does, from the real preload
// over a loopback DevTools session, then detaches.
async function windowToken(child) {
  const url = await child.devtoolsURL;
  const browser = await chromium.connectOverCDP(url, { timeout: 15000 });
  try {
    const context = browser.contexts()[0];
    const page = context.pages().find((candidate) => candidate.url().startsWith("http://127.0.0.1:")) || await context.waitForEvent("page");
    await page.waitForFunction(() => Boolean(window.careerHubDesktop?.session?.token), null, { timeout: 15000 });
    const token = await page.evaluate(() => window.careerHubDesktop.session.token());
    assert.match(token, /^[A-Za-z0-9_-]{43}$/);
    return token;
  } finally {
    await browser.close();
  }
}

async function waitForCareerHub(child, appOrigin) {
  const started = Date.now();
  const deadline = Date.now() + 60000;
  let stage = "waiting for owned sidecar";
  const failure = async (message) => {
    const diagnostic = await captureStartupDiagnostic({ child, browserType: chromium, outputRoot: OUTPUT_ROOT, stage });
    return new Error(`${message}\nStartup diagnostic: ${JSON.stringify(diagnostic)}\n${child.smokeLogs()}`);
  };
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw await failure("Career Hub exited before becoming ready.");
    }
    if (child.goPackaged && !child.sidecarPID) {
      const raw = nativeRecords(`Get-CimInstance Win32_Process -Filter "ParentProcessId = ${child.pid} AND Name = 'career-hub-sidecar.exe'" | Select-Object ProcessId,ExecutablePath | ConvertTo-Json -Compress`);
      const owned = raw ? [].concat(JSON.parse(raw)) : [];
      assert.ok(owned.length <= 1);
      if (!owned.length) { await delay(200); continue; }
      stage = "waiting for owned sidecar listener";
      assert.equal(path.resolve(owned[0].ExecutablePath).toLowerCase(), path.join(GO_PACKAGE_ROOT, "resources/career-hub-sidecar/career-hub-sidecar.exe").toLowerCase());
      const port = nativeRecords(`Get-NetTCPConnection -OwningProcess ${owned[0].ProcessId} -State Listen -ErrorAction SilentlyContinue | Where-Object { $_.LocalAddress -eq '127.0.0.1' } | Select-Object -ExpandProperty LocalPort`);
      if (!port) { await delay(200); continue; }
      assert.equal(port, "8766");
      child.sidecarPID = owned[0].ProcessId;
    }
    try {
      stage = "waiting for health response";
      const response = await fetch(`${appOrigin}/api/health`, {
        cache: "no-store",
        signal: AbortSignal.timeout(750),
      });
      if (response.ok) {
        stage = "reading desktop window session";
        windowSessionToken = await windowToken(child);
        console.log(`extension smoke: packaged startup ready after ${Date.now() - started} ms`);
        return;
      }
      stage = `health returned HTTP ${response.status}`;
    } catch {
      // The local server is still starting.
    }
    await delay(200);
  }
  throw await failure("Career Hub did not become ready.");
}

function prepareAssessmentVault(dataRoot) {
  const fixture = JSON.parse(fs.readFileSync(path.join(__dirname, "fixtures/saved_assessment_home.json"), "utf8"));
  for (const [key, content] of Object.entries(fixture.files)) {
    // The packaged catalogs remain authoritative; install only synthetic Home data.
    if (!key.startsWith("data:")) continue;
    const relative = key.slice(5);
    assert.match(relative, /^career_vault\/[a-z_]+\.yaml$/);
    const target = path.join(dataRoot, relative);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    const text = relative === "career_vault/profile.yaml"
      ? content.replace(/^name: Synthetic$/m, "name: Example Person")
      : content;
    if (relative === "career_vault/profile.yaml") assert.match(text, /^name: Example Person$/m);
    fs.writeFileSync(target, text, "utf8");
  }
  fs.appendFileSync(path.join(dataRoot, "career_vault/profile.yaml"), [
    "given_name: Example", "family_name: Person", "preferred_name: Example",
    "contact:", "  email: person@example.com", "  phone: '555-0100'",
    "  linkedin: https://www.linkedin.com/in/example", "  address_line1: 100 Example Street",
    "  city: Atlanta", "  region: GA", "  postal_code: '30303'", "  country: United States", "",
  ].join("\n"), "utf8");
}

async function uploadSyntheticResume(appOrigin) {
  const response = await fetch(`${appOrigin}/api/resumes`, {
    method: "POST",
    headers: {
      "X-Career-Hub-Token": windowSessionToken,
      "Content-Type": "application/octet-stream",
      "X-Resume-Name": "general.pdf",
      "X-Resume-Label": encodeURIComponent("General Resume"),
    },
    body: Buffer.from("%PDF-1.4 synthetic resume"),
    signal: AbortSignal.timeout(20000),
  });
  const body = await response.text();
  assert.equal(response.status, 201, body);
  assert.equal(JSON.parse(body).display_name, "General Resume");
}

async function browserApi(appOrigin, pathname, options = {}) {
  const method = options.method || "GET";
  const expectedStatus = options.expectedStatus || 200;
  console.log(`extension smoke: browser API ${pathname} session`);
  const sessionResponse = await fetch(`${appOrigin}/api/session`, {
    cache: "no-store", headers: { "X-Career-Hub-Token": windowSessionToken },
  });
  assert.equal(sessionResponse.status, 200);
  const session = await sessionResponse.json();
  assert.equal(session.client, "browser");
  assert.equal(session.token, undefined, "no route may return a token");
  console.log(`extension smoke: browser API ${pathname} request`);
  const headers = { "X-Career-Hub-Token": windowSessionToken };
  if (options.payload !== undefined) {
    headers["Content-Type"] = "application/json";
  }
  const response = await fetch(`${appOrigin}${pathname}`, {
    cache: "no-store",
    body: options.payload === undefined
      ? undefined
      : JSON.stringify(options.payload),
    headers,
    method,
  });
  const responseText = await response.text();
  assert.equal(response.status, expectedStatus, responseText);
  const payload = JSON.parse(responseText);
  console.log(`extension smoke: browser API ${pathname} complete`);
  return payload;
}

async function openExtensionPopupPage(
  context,
  worker,
  extensionOrigin,
  activePage,
) {
  const activeUrl = activePage.url();
  const popupUrl = `${extensionOrigin}/popup.html`;
  const popupPromise = context.waitForEvent("page", { timeout: 10000 });
  await worker.evaluate(async ({ activeUrl, popupUrl }) => {
    const tabs = await chrome.tabs.query({});
    const activeTab = tabs.find((tab) => tab.url === activeUrl);
    if (!activeTab || typeof activeTab.id !== "number") {
      throw new Error("Synthetic job tab is unavailable");
    }
    await chrome.tabs.update(activeTab.id, { active: true });
    await chrome.tabs.create({ active: false, url: popupUrl });
  }, { activeUrl, popupUrl });
  const popup = await popupPromise;
  await popup.waitForLoadState("domcontentloaded");
  await popup.locator("#capture-view").waitFor({ state: "visible" });
  await popup.locator("#link-button").waitFor({ state: "visible" });
  return popup;
}

async function activateBrowserPage(worker, page) {
  const activeUrl = page.url();
  await worker.evaluate(async (url) => {
    const tabs = await chrome.tabs.query({});
    const tab = tabs.find((candidate) => candidate.url === url);
    if (!tab || typeof tab.id !== "number") {
      throw new Error("Synthetic job tab is unavailable");
    }
    await chrome.tabs.update(tab.id, { active: true });
  }, activeUrl);
}

async function sendExtensionCommand(worker, extensionPage, activePage, message) {
  await activateBrowserPage(worker, activePage);
  return extensionPage.evaluate(
    async (payload) => chrome.runtime.sendMessage(payload),
    message,
  );
}

async function clickPopupAction(popup, worker, activePage, selector) {
  await popup.waitForFunction(
    (buttonSelector) => document.querySelector(buttonSelector)?.disabled === false,
    selector,
  );
  await activateBrowserPage(worker, activePage);
  await popup.locator(selector).evaluate((button) => button.click());
}

async function screenshotPopup(popup, filename) {
  await popup.bringToFront();
  await popup.evaluate(() => window.scrollTo(0, 0));
  await withTimeout(
    popup.locator("body").screenshot({
      path: path.join(OUTPUT_ROOT, filename),
    }),
    10000,
    `Screenshot ${filename}`,
  );
}

async function waitForResult(popup, expectedTitle) {
  await popup.locator("#result-view").waitFor({ state: "visible" });
  assert.equal(
    await popup.locator("#result-title").textContent(),
    expectedTitle,
    await popup.locator("#result-message").textContent(),
  );
}

function removeSafeTemporaryDirectory(target, requiredPrefix) {
  const resolved = path.resolve(target);
  const tempRoot = path.resolve(os.tmpdir());
  if (!resolved.startsWith(`${tempRoot}${path.sep}`)) {
    throw new Error(`Refusing to remove non-temporary path: ${resolved}`);
  }
  if (!path.basename(resolved).startsWith(requiredPrefix)) {
    throw new Error(`Refusing to remove unexpected temporary path: ${resolved}`);
  }
  fs.rmSync(resolved, { recursive: true, force: true });
}

function fixtureHtml(pathname) {
  if (pathname === "/job/jsonld") {
    const posting = {
      "@context": "https://schema.org",
      "@type": "JobPosting",
      identifier: { value: "platform-42" },
      title: "Platform Engineer",
      hiringOrganization: { name: "Example Systems" },
      jobLocation: {
        address: {
          addressCountry: "US",
          addressLocality: "New York",
          addressRegion: "NY",
        },
      },
      description: `
        <h2>About the role</h2>
        <p>Build and operate reliable internal platform services used by product teams across the company.</p>
        <h2>Responsibilities</h2>
        <p>Own production services, improve observability, review designs, and partner with engineers on safe releases.</p>
        <h2>Requirements</h2>
        <p>Practical Python and SQL experience, sound API design skills, and clear written communication.</p>
        <h2>How to apply</h2>
        <p>Recruiter: Jane Doe — jane.doe@example.com</p>
      `,
    };
    return `<!doctype html><html lang="en"><head>
      <title>Platform Engineer — Example Systems</title>
      <script type="application/ld+json">${JSON.stringify(posting)}</script>
      </head><body><h1>Platform Engineer</h1><p>Example Systems</p></body></html>`;
  }
  if (pathname === "/job/semantic") {
    return `<!doctype html><html lang="en"><head><title>Data Engineer — Northstar</title></head>
      <body><h1>Data Engineer</h1><main><section id="job-description">
      <h2>Job description</h2><p>Build dependable data products and maintain batch and streaming pipelines for business teams.</p>
      <h2>Responsibilities</h2><p>Improve data quality, document interfaces, monitor production workloads, and review changes.</p>
      <h2>Qualifications</h2><p>Experience with Python, SQL, orchestration, testing, and collaborative software delivery.</p>
      </section></main></body></html>`;
  }
  if (pathname === "/job/body") {
    return `<!doctype html><html lang="en"><head><title>Backend Engineer — Atlas</title></head><body>
      <h1>Backend Engineer</h1><div>About the role</div>
      <div>Design reliable services and APIs used by customers and internal teams. Work closely with product and operations partners.</div>
      <div>Responsibilities include production ownership, code review, incident follow-up, automated testing, and clear documentation.</div>
      <div>Requirements include Python experience, relational database knowledge, API design, and strong communication.</div>
      </body></html>`;
  }
  if (pathname === "/job/generic-split") {
    return `<!doctype html><html lang="en"><head><title>Integration Engineer — Northstar</title></head><body>
      <header><a href="/jobs">Search jobs</a></header>
      <main>
        <aside aria-label="Job search results">
          <a href="/job/generic-split" aria-current="page"><h2>Integration Engineer</h2><p>UNRELATED SELECTED CARD SUMMARY</p></a>
          <a href="/job/other-role"><h2>Analytics Engineer</h2><p>UNRELATED SEARCH RESULT</p></a>
          <a href="/job/third-role"><h2>Support Engineer</h2><p>ANOTHER UNRELATED SEARCH RESULT</p></a>
        </aside>
        <section class="detail-pane">
          <h1>Integration Engineer</h1>
          <div><h2>About the role</h2><p>GENERIC DETAIL ONLY. Build reliable integrations used by partner and operations teams across the company.</p>
          <h2>Responsibilities</h2><p>Own API integrations, production monitoring, incident follow-up, automated testing, and concise technical documentation.</p>
          <h2>Qualifications</h2><p>Experience with Python, REST APIs, SQL, debugging distributed systems, and collaborative software delivery.</p></div>
        </section>
      </main>
      <footer>Company careers and privacy links</footer>
      </body></html>`;
  }
  if (pathname === "/provider/indeed-split") {
    return `<!doctype html><html lang="en"><head><title>Precision Platform Engineer - Example Systems | Indeed</title></head><body>
      <header><a href="/jobs">Find jobs</a><span>Company reviews</span></header>
      <main class="jobsearch-ViewJobLayout">
        <aside aria-label="Job search results">
          <a href="/viewjob?vjk=precision-123" aria-current="page"><h2>Precision Platform Engineer</h2><p>UNRELATED INDEED CARD SUMMARY</p></a>
          <a href="/viewjob?vjk=unrelated-456"><h2>Data Platform Manager</h2><p>UNRELATED INDEED RESULT</p></a>
          <a href="/viewjob?vjk=unrelated-789"><h2>Cloud Architect</h2><p>ANOTHER UNRELATED INDEED RESULT</p></a>
        </aside>
        <section data-testid="jobsearch-ViewJobLayout-jobDisplay">
          <header><h1 data-testid="jobsearch-JobInfoHeader-title">Precision Platform Engineer</h1><div>Example Systems · New York, NY</div></header>
          <div id="jobDescriptionText">
            <h2>About the role</h2><p>INDEED DETAIL ONLY. Build and operate dependable platform services used by product engineering teams.</p>
            <h2>Responsibilities</h2><p>Own production services, improve observability, review designs, automate testing, and document operational decisions.</p>
            <h2>Requirements</h2><p>Experience with Python, SQL, API design, cloud infrastructure, and clear cross-functional communication.</p>
          </div>
          <section aria-label="Similar jobs"><a href="/viewjob?vjk=similar-999">SIMILAR INDEED ROLE</a></section>
        </section>
      </main></body></html>`;
  }
  if (pathname === "/provider/linkedin-split") {
    return `<!doctype html><html lang="en"><head><title>Applied AI Engineer | Synthetic Labs | LinkedIn</title></head><body>
      <nav>Home My Network Jobs Messaging</nav>
      <main>
        <section class="jobs-search-results-list" aria-label="Job search results">
          <a href="/jobs/view/9876543210/" aria-current="page"><h2>Applied AI Engineer</h2><p>UNRELATED LINKEDIN CARD SUMMARY</p></a>
          <a href="/jobs/view/1111111111/"><h2>Machine Learning Manager</h2><p>UNRELATED LINKEDIN RESULT</p></a>
          <a href="/jobs/view/2222222222/"><h2>Data Scientist</h2><p>ANOTHER UNRELATED LINKEDIN RESULT</p></a>
        </section>
        <section class="job-details-jobs-unified-top-card">
          <h1 class="job-details-jobs-unified-top-card__job-title">Applied AI Engineer</h1><div>Synthetic Labs · Remote</div>
          <div id="job-details">
            <h2>About the job</h2><p>LINKEDIN DETAIL ONLY. Build reliable applied machine-learning services and evaluation tooling for customer-facing products.</p>
            <h2>What you'll do</h2><p>Ship Python services, improve model evaluation, monitor production behavior, and partner with product engineers.</p>
            <h2>Qualifications</h2><p>Experience with Python, APIs, SQL, testing, cloud deployment, and written technical communication.</p>
          </div>
        </section>
        <section aria-label="People also viewed"><a href="/jobs/view/3333333333/">PEOPLE ALSO VIEWED ROLE</a></section>
      </main></body></html>`;
  }
  if (pathname === "/provider/handshake-shell") {
    return `<!doctype html><html lang="en"><head><title>Jobs | Handshake</title></head><body>
      <header>Handshake Jobs Messages Career center</header>
      <main>
        <h1>Jobs for you</h1>
        <section aria-label="Job search results">
          <a href="/job-search/555000"><h2>Software Engineer</h2><p>Responsibilities include application development.</p></a>
          <a href="/job-search/555001"><h2>Systems Analyst</h2><p>Requirements include SQL experience.</p></a>
          <a href="/job-search/555002"><h2>Data Engineer</h2><p>Qualifications include Python experience.</p></a>
        </section>
      </main></body></html>`;
  }
  if (pathname === "/job/malformed-jsonld") {
    return `<!doctype html><html lang="en"><head><title>QA Engineer — Signal</title>
      <script type="application/ld+json">{"@type":"JobPosting",broken</script></head>
      <body><h1>QA Engineer</h1><section data-testid="job-description">
      <h2>Job description</h2><p>Improve release confidence for a growing web platform through focused automation and exploratory testing.</p>
      <h2>Responsibilities</h2><p>Design test cases, investigate regressions, document risks, and work with engineers on durable fixes.</p>
      <h2>Requirements</h2><p>Experience with browser testing, API testing, JavaScript, and concise defect reporting.</p>
      </section></body></html>`;
  }
  if (pathname === "/job/selection") {
    return `<!doctype html><html lang="en"><head><title>Selected role — Example</title></head><body>
      <h1>Search results</h1><aside>Navigation and unrelated recommendations</aside>
      <section id="selection-target"><h2>Responsibilities</h2><p>Build reliable Python services and document APIs.</p>
      <h2>Qualifications</h2><p>SQL experience and careful production debugging.</p></section>
      </body></html>`;
  }
  if (pathname === "/job/link") {
    return `<!doctype html><html lang="en"><head><title>Jobs — Example</title></head>
      <body><h1>Find jobs</h1><p>Choose a listing to see its details.</p></body></html>`;
  }
  if (pathname === "/application/prefill-workday") {
    return `<!doctype html><html lang="en"><head><title>Workday-like application</title>
      <style>body{font:16px system-ui;max-width:720px;margin:30px auto}label{display:block;margin:12px 0}input,select,textarea{display:block;width:100%;padding:8px;margin-top:4px}</style></head>
      <body><main><h1>Candidate information</h1><form id="workday-form">
        <label>First name<input name="firstName" autocomplete="given-name"></label>
        <label>Last name<input name="lastName" autocomplete="family-name"></label>
        <label>Email address<input type="email" name="email" autocomplete="email"></label>
        <label>Phone number<input type="tel" name="phone" autocomplete="tel"></label>
        <label>Will you require sponsorship?<select name="sponsorship"><option></option><option>Yes</option><option>No</option></select></label>
        <label>Resume<input type="file" name="resume"></label>
        <label>Why are you interested?<textarea name="interest"></textarea></label>
        <button type="submit">Next</button>
      </form></main><script>document.querySelector("#workday-form").addEventListener("submit",event=>event.preventDefault())</script></body></html>`;
  }
  if (pathname === "/application/prefill-greenhouse") {
    return `<!doctype html><html lang="en"><head><title>Greenhouse-like application</title>
      <style>body{font:16px system-ui;max-width:720px;margin:30px auto}label{display:block;margin:12px 0}input,textarea{display:block;width:100%;padding:8px;margin-top:4px}</style></head>
      <body><main><h1>Apply for Synthetic Engineer</h1><form id="greenhouse-form">
        <label>Full name<input name="name" autocomplete="name"></label>
        <label>Email<input type="email" name="email" value="candidate-already-entered@example.test"></label>
        <label>Phone<input type="tel" name="phone"></label>
        <label>LinkedIn profile<input type="url" name="linkedin"></label>
        <label>Resume<input type="file" name="resume"></label>
        <label>Cover letter<textarea name="cover_letter"></textarea></label>
        <button type="submit">Submit application</button>
      </form></main><script>document.querySelector("#greenhouse-form").addEventListener("submit",event=>event.preventDefault())</script></body></html>`;
  }
  if (pathname === "/application/prefill-us-demographics") {
    return `<!doctype html><html lang="en"><head><title>US demographic application</title>
      <style>body{font:16px system-ui;max-width:760px;margin:30px auto}.field{display:block;margin:12px 0}.field input{display:block;width:100%;padding:8px;margin-top:4px}[role=listbox]{border:1px solid #aaa;padding:4px;list-style:none}[role=option]{padding:6px;cursor:pointer}[hidden]{display:none}</style></head>
      <body><main><h1>US voluntary self-identification</h1><form id="demographic-form">
        <label class="field">Gender Identity: The following question is completely voluntary and will not affect your application in any way.<input role="combobox" aria-label="Gender Identity: The following question is completely voluntary and will not affect your application in any way." aria-controls="gender-identity-options" aria-expanded="false" data-options="gender-identity-options"></label>
        <ul id="gender-identity-options" role="listbox" hidden><li role="option">Male</li><li role="option">Female</li><li role="option">Prefer not to say</li></ul>
        <label class="field">Gender<input role="combobox" aria-label="Gender" aria-controls="gender-options" aria-expanded="false" data-options="gender-options"></label>
        <ul id="gender-options" role="listbox" hidden><li role="option">Male</li><li role="option">Female</li><li role="option">Decline To Self Identify</li></ul>
        <label class="field">Are you Hispanic/Latino?<input role="combobox" aria-label="Are you Hispanic/Latino?" aria-controls="hispanic-options" aria-expanded="false" data-options="hispanic-options"></label>
        <ul id="hispanic-options" role="listbox" hidden><li role="option">Yes</li><li role="option">No</li><li role="option">Decline To Self Identify</li></ul>
        <label class="field">Veteran Status<input role="combobox" aria-label="Veteran Status" aria-controls="veteran-options" aria-expanded="false" data-options="veteran-options"></label>
        <ul id="veteran-options" role="listbox" hidden><li role="option">I am not a protected veteran</li><li role="option">I identify as one or more of the classifications of a protected veteran</li><li role="option">I don't wish to answer</li></ul>
        <label class="field">Disability Status<input role="combobox" aria-label="Disability Status" aria-controls="disability-options" aria-expanded="false" data-options="disability-options"></label>
        <ul id="disability-options" role="listbox" hidden><li role="option">Yes, I have a disability, or have had one in the past</li><li role="option">No, I do not have a disability and have not had one in the past</li><li role="option">I do not want to answer</li></ul>
        <button type="submit">Submit application</button>
      </form></main><script>
        window.__submitCount = 0;
        const controls = [...document.querySelectorAll('[role="combobox"]')];
        const closeAll = () => controls.forEach((input) => {
          document.getElementById(input.dataset.options).hidden = true;
          input.setAttribute('aria-expanded', 'false');
        });
        controls.forEach((input) => {
          const listbox = document.getElementById(input.dataset.options);
          input.addEventListener('click', () => {
            closeAll();
            listbox.hidden = false;
            input.setAttribute('aria-expanded', 'true');
          });
          for (const option of listbox.querySelectorAll('[role="option"]')) {
            option.addEventListener('click', (event) => {
              event.stopPropagation();
              input.value = option.textContent.trim();
              closeAll();
            });
          }
        });
        document.querySelector('#demographic-form').addEventListener('submit', (event) => {
          event.preventDefault();
          window.__submitCount += 1;
        });
      </script></body></html>`;
  }
  if (pathname === "/application/prefill-us-demographics-unowned-option") {
    return `<!doctype html><html lang="en"><head><title>Hostile option fixture</title>
      <style>body{font:16px system-ui;max-width:760px;margin:30px auto}.field{display:block;margin:12px 0}.field input{display:block;width:100%;padding:8px;margin-top:4px}</style></head>
      <body><main><h1>US voluntary self-identification</h1><form id="hostile-form">
        <label class="field">Veteran Status<input role="combobox" aria-label="Veteran Status" aria-expanded="false"></label>
        <button id="unowned-option" type="submit" role="option">I am not a protected veteran</button>
      </form></main><script>
        window.__submitCount = 0;
        document.querySelector('#hostile-form').addEventListener('submit', (event) => {
          event.preventDefault();
          window.__submitCount += 1;
        });
      </script></body></html>`;
  }
  if (pathname === "/application/prefill-us-demographics-nested-option") {
    return `<!doctype html><html lang="en"><head><title>Nested option fixture</title>
      <style>body{font:16px system-ui;max-width:760px;margin:30px auto}.field{display:block;margin:12px 0}.field input{display:block;width:100%;padding:8px;margin-top:4px}[role=listbox]{border:1px solid #aaa;padding:4px}[role=option]{padding:6px;cursor:pointer}[hidden]{display:none}</style></head>
      <body><main><h1>US voluntary self-identification</h1>
        <label class="field">Veteran Status<input role="combobox" aria-label="Veteran Status" aria-controls="owned-listbox" aria-expanded="false"></label>
        <div id="owned-listbox" role="listbox" hidden>
          <div id="unowned-nested-listbox" role="listbox">
            <div id="nested-option" role="option">I am not a protected veteran</div>
          </div>
        </div>
      </main><script>
        window.__nestedClickCount = 0;
        const input = document.querySelector('[role="combobox"]');
        input.addEventListener('click', () => {
          document.querySelector('#owned-listbox').hidden = false;
          input.setAttribute('aria-expanded', 'true');
        });
        document.querySelector('#nested-option').addEventListener('click', () => {
          window.__nestedClickCount += 1;
          input.value = 'I am not a protected veteran';
        });
      </script></body></html>`;
  }
  if (pathname === "/application/prefill-lever") {
    return `<!doctype html><html lang="en"><head><title>Lever-like application</title>
      <style>body{font:16px system-ui;max-width:720px;margin:30px auto}.field{display:block;margin:12px 0}.field input{display:block;width:100%;padding:8px;margin-top:4px}</style></head>
      <body><main><h1>Submit your application</h1><form id="lever-form">
        <label class="field">Your name<input name="candidate_name" autocomplete="name"></label>
        <label class="field">Email address<input type="email" name="candidate_email"></label>
        <label class="field">LinkedIn profile<input type="url" name="linkedin_primary"></label>
        <label class="field">LinkedIn profile<input type="url" name="linkedin_secondary"></label>
        <input type="email" name="hidden_email" aria-hidden="true" value="hidden@example.test">
        <button type="submit">Submit application</button>
      </form></main><script>document.querySelector("#lever-form").addEventListener("submit",event=>event.preventDefault())</script></body></html>`;
  }
  if (pathname === "/application/prefill-plain") {
    return `<!doctype html><html lang="en"><head><title>Plain synthetic application</title>
      <style>body{font:16px system-ui;max-width:720px;margin:30px auto}label{display:block;margin:12px 0}input,textarea{display:block;width:100%;padding:8px;margin-top:4px}[hidden]{display:none}</style></head>
      <body><main><h1>Plain synthetic application</h1><form id="plain-form">
        <fieldset id="identity-fields"><legend>Contact details</legend></fieldset>
        <label hidden>Hidden email<input type="email" name="hidden_email"></label>
        <label>Password<input type="password" name="password"></label>
        <label>Resume<input type="file" name="resume"></label>
        <label>Why should we hire you?<textarea name="screening_answer"></textarea></label>
        <button id="submit-application" type="submit">Submit application</button>
      </form></main><script>
        window.__submitCount = 0;
        window.__inputEvents = 0;
        window.__renderIdentityFields = () => {
          document.querySelector("#identity-fields").innerHTML = [
            '<legend>Contact details</legend>',
            '<label>Full name<input name="full_name" autocomplete="name"></label>',
            '<label>Email address<input type="email" name="email" autocomplete="email"></label>',
            '<label>Phone number<input type="tel" name="phone" autocomplete="tel"></label>',
            '<label>LinkedIn profile<input type="url" name="linkedin_profile"></label>',
          ].join('');
        };
        window.__renderIdentityFields();
        document.querySelector("#plain-form").addEventListener("input", () => { window.__inputEvents += 1; });
        document.querySelector("#plain-form").addEventListener("submit", (event) => {
          event.preventDefault();
          window.__submitCount += 1;
        });
      </script></body></html>`;
  }
  if (pathname === "/application/prefill-hidden-heavy") {
    const hiddenInputs = Array.from(
      { length: 300 },
      (_, index) => `<input type="hidden" name="framework_state_${index}" value="synthetic">`,
    ).join("");
    return `<!doctype html><html lang="en"><head><title>Hidden-heavy application</title>
      <style>body{font:16px system-ui;max-width:720px;margin:30px auto}label,input{display:block;margin:10px 0}</style></head>
      <body><main><h1>Candidate details</h1>${hiddenInputs}<form id="application-form">
        <label>Email address<input type="email" name="email" autocomplete="email"></label>
        <label>Phone number<input type="tel" name="phone" autocomplete="tel"></label>
      </form></main></body></html>`;
  }
  if (pathname === "/application/prefill-multiple-forms") {
    return `<!doctype html><html lang="en"><head><title>Multiple-form application</title>
      <style>body{font:16px system-ui;max-width:720px;margin:30px auto}label,input{display:block;margin:10px 0}</style></head>
      <body><aside><form id="newsletter-form"><h2>Newsletter</h2>
        <label>Email<input type="email" name="newsletter_email"></label>
      </form></aside><main><h1>Apply</h1><form id="application-form">
        <label>Full name<input name="full_name" autocomplete="name"></label>
        <label>Email address<input type="email" name="application_email" autocomplete="email"></label>
        <label>Phone number<input type="tel" name="phone" autocomplete="tel"></label>
      </form></main></body></html>`;
  }
  if (pathname === "/application/prefill-disabled-winner") {
    return `<!doctype html><html lang="en"><head><title>Disabled winner application</title>
      <style>body{font:16px system-ui;max-width:720px;margin:30px auto}label,input{display:block;margin:10px 0}</style></head>
      <body><main><h1>Profile links</h1><form id="application-form">
        <label>LinkedIn profile or personal website<input type="url" name="profile_url"></label>
      </form></main></body></html>`;
  }
  if (pathname === "/application/prefill-structured") {
    return `<!doctype html><html lang="en"><head><title>Structured application</title>
      <style>body{font:16px system-ui;max-width:720px;margin:30px auto}label,fieldset{display:block;margin:10px 0}input,select,textarea{margin:4px 0;padding:7px}</style></head>
      <body><main><h1>Candidate profile</h1><form id="structured-form">
        <label>First name<input name="given_name" autocomplete="given-name"></label>
        <label>Last name<input name="family_name" autocomplete="family-name"></label>
        <label>Address line 1<input name="address_line1" autocomplete="address-line1"></label>
        <label>City<input name="city" autocomplete="address-level2"></label>
        <label>State<select name="region" autocomplete="address-level1"><option value=""></option><option value="GA">GA</option><option value="NY">NY</option></select></label>
        <label>Postal code<input name="postal_code" autocomplete="postal-code"></label>
        <label>Country<select name="country" autocomplete="country-name"><option value=""></option><option>Canada</option><option>United States</option></select></label>
        <label>Work authorization statement<textarea name="work_authorization"></textarea></label>
        <fieldset><legend>Will you require sponsorship in the future?</legend><label><input type="radio" name="future_sponsorship" value="yes">Yes</label><label><input type="radio" name="future_sponsorship" value="no">No</label></fieldset>
      </form></main></body></html>`;
  }
  if (pathname === "/application/reusable-answers") {
    return `<!doctype html><html lang="en"><head><title>Reusable answer application</title>
      <style>body{font:16px system-ui;max-width:720px;margin:30px auto}label,fieldset{display:block;margin:12px 0}input,select{margin:4px 0;padding:7px}</style></head>
      <body><main><h1>Application questions</h1><form id="answers-form">
        <label>How did you hear about us?<select name="referral"><option value=""></option><option value="referral" selected>Referral</option><option value="event">Career event</option></select></label>
        <label>What is your preferred work setup?<input name="work_setup" value="Remote"></label>
        <fieldset><legend>Are you willing to travel occasionally?</legend><label><input type="radio" name="travel" value="yes" checked>Yes</label><label><input type="radio" name="travel" value="no">No</label></fieldset>
        <label>Will you require sponsorship?<select name="sponsorship"><option>No</option><option selected>Yes</option></select></label>
        <label>Desired salary<input name="salary" value="100000"></label>
        <label>Why do you want this role?<input name="motivation" value="Synthetic motivation"></label>
        <label>Password<input type="text" name="password" value="not-a-secret"></label>
        <button type="submit">Submit application</button>
      </form></main><script>
        window.__submitCount = 0;
        document.querySelector("#answers-form").addEventListener("submit", (event) => {
          event.preventDefault();
          window.__submitCount += 1;
        });
      </script></body></html>`;
  }
  if (pathname === "/application/session") {
    return `<!doctype html><html lang="en"><head><title>Attended Engineer — Session Labs</title></head>
      <body><main id="application-shell">
        <h1>Attended Engineer application</h1>
        <form id="application-form">
          <label>Required name <input id="required-name" required></label>
          <button id="continue-button" type="submit">Continue</button>
        </form>
      </main>
      <script>
        document.querySelector("#application-form").addEventListener("submit", (event) => {
          event.preventDefault();
          history.pushState({}, "", "/application/session?step=final");
          document.querySelector("#application-shell").innerHTML =
            '<h1>Review application</h1><button id="final-submit" type="button">Submit application</button>';
          document.querySelector("#final-submit").addEventListener("click", () => {
            history.pushState({}, "", "/application/session?submitted=1");
            document.querySelector("#application-shell").innerHTML =
              '<h1>Your application has been successfully submitted</h1><p>Session Labs received your application.</p>';
          });
        });
      </script></body></html>`;
  }
  if (pathname === "/login") {
    return `<!doctype html><html lang="en"><head><title>Sign in</title></head>
      <body><h1>Sign in</h1><form><label>Email <input></label><label>Password <input type="password"></label></form>
      <p>Forgot password? Create account.</p></body></html>`;
  }
  if (pathname === "/ambiguous") {
    return `<!doctype html><html lang="en"><head><title>Company portal</title></head><body>
      <h1>Welcome to our company portal</h1><p>${"Company news, office stories, community events, and team updates. ".repeat(8)}</p>
      </body></html>`;
  }
  if (pathname === "/search-results") {
    return `<!doctype html><html lang="en"><head><title>Job Search</title></head><body>
      <h1>Find jobs</h1><div>Software Engineer</div><div>Responsibilities include service ownership and testing.</div>
      <div>Data Engineer</div><div>Requirements include Python and SQL experience.</div>
      <p>${"Browse recommended openings and company updates. ".repeat(5)}</p></body></html>`;
  }
  if (pathname === "/two-jobs") {
    const first = `<section class="job-description"><h2>Job description</h2><p>${"Build reliable platform services and own production operations. ".repeat(4)}</p><h2>Requirements</h2><p>Python experience.</p></section>`;
    const second = `<section class="job-description"><h2>Job description</h2><p>${"Build dependable analytics pipelines and maintain data quality. ".repeat(4)}</p><h2>Requirements</h2><p>SQL experience.</p></section>`;
    return `<!doctype html><html lang="en"><head><title>Two open roles</title></head><body><h1>Open roles</h1>${first}${second}</body></html>`;
  }
  if (pathname === "/oversized") {
    return `<!doctype html><html lang="en"><head><title>Very large posting</title></head><body>
      <h1>Very large posting</h1><div>Requirements ${"x".repeat(500100)}</div></body></html>`;
  }
  return null;
}

async function main() {
  fs.mkdirSync(OUTPUT_ROOT, { recursive: true });
  const browserProfile = fs.mkdtempSync(
    path.join(os.tmpdir(), "career-hub-extension-profile-"),
  );
  const dataContainer = fs.mkdtempSync(
    path.join(os.tmpdir(), "career-hub-extension-data-"),
  );
  goEnvironmentRoot = dataContainer;
  const dataRoot = path.join(dataContainer, "Local/CareerHub/dev");
  const extensionCopy = fs.mkdtempSync(
    path.join(os.tmpdir(), "career-hub-extension-build-"),
  );
  const jobServer = http.createServer((request, response) => {
    const pathname = new URL(request.url, "http://127.0.0.1").pathname;
    const html = fixtureHtml(pathname);
    if (html === null) {
      response.writeHead(404, { "Content-Type": "text/plain; charset=utf-8" });
      response.end("Not found");
      return;
    }
    response.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
    response.end(html);
  });

  let context;
  let careerHub;
  let primaryError = null;
  try {
    const appPort = 8766;
    const appOrigin = `http://127.0.0.1:${appPort}`;
    primeNativeTools();
    {
      // Refuse a live user's service. Do not probe it, attach or stop it.
      const reservation = http.createServer();
      await new Promise((resolve, reject) => {
        reservation.once("error", reject);
        reservation.listen({ host: "127.0.0.1", port: appPort, exclusive: true }, resolve);
      });
      await closeServer(reservation);
      careerHub = startCareerHub(dataRoot, `chrome-extension://${DEV_EXTENSION_ID}`, appPort);
      await waitForCareerHub(careerHub, appOrigin);
      await stopProcess(careerHub);
      careerHub = null;
      // Fixture generation is offline and only after the native owner stops.
      prepareAssessmentVault(dataRoot);
    }
    fs.cpSync(EXTENSION_ROOT, extensionCopy, { recursive: true });
    const workerPath = path.join(extensionCopy, "service-worker.js");
    const workerSource = fs.readFileSync(workerPath, "utf8");
    assert(workerSource.includes(DEFAULT_APP_ORIGIN));

    const jobOrigin = await listen(jobServer);
    const browserExecutable = process.env.CAREER_HUB_BROWSER_EXECUTABLE;
    context = await chromium.launchPersistentContext(browserProfile, {
      ...(browserExecutable
        ? { executablePath: path.resolve(browserExecutable) }
        : { channel: "chromium" }),
      headless: true,
      args: [
        `--disable-extensions-except=${extensionCopy}`,
        `--load-extension=${extensionCopy}`,
      ],
    });
    await context.addInitScript(() => {
      globalThis.__careerHubSmokeErrors = [];
      globalThis.addEventListener("error", (event) => {
        globalThis.__careerHubSmokeErrors.push(
          `${event.message || "error"} at ${event.filename || "unknown"}:${event.lineno || 0}:${event.colno || 0}`,
        );
      });
      globalThis.addEventListener("unhandledrejection", (event) => {
        globalThis.__careerHubSmokeErrors.push(
          `unhandled rejection: ${event.reason?.stack || event.reason || "unknown"}`,
        );
      });
    });
    await context.route("https://www.indeed.com/**", (route) => route.fulfill({
      body: fixtureHtml("/provider/indeed-split"),
      contentType: "text/html; charset=utf-8",
      status: 200,
    }));
    await context.route("https://www.linkedin.com/**", (route) => route.fulfill({
      body: fixtureHtml("/provider/linkedin-split"),
      contentType: "text/html; charset=utf-8",
      status: 200,
    }));
    await context.route("https://app.joinhandshake.com/**", (route) => route.fulfill({
      body: fixtureHtml("/provider/handshake-shell"),
      contentType: "text/html; charset=utf-8",
      status: 200,
    }));

    let [worker] = context.serviceWorkers();
    if (!worker) {
      worker = await context.waitForEvent("serviceworker", { timeout: 15000 });
    }
    console.log("extension smoke: service worker ready");
    const extensionId = new URL(worker.url()).host;
    assert.equal(extensionId, DEV_EXTENSION_ID);
    const extensionOrigin = `chrome-extension://${extensionId}`;

    careerHub = startCareerHub(dataRoot, extensionOrigin, appPort);
    await waitForCareerHub(careerHub, appOrigin);
    await uploadSyntheticResume(appOrigin);
    console.log(`extension smoke: Career Hub ready at ${appOrigin}`);
    // ADR SEC-250: app pages opened in this plain browser get the window
    // token the way Electron's preload supplies it; only on the app origin.
    await context.addInitScript(([origin, token]) => {
      if (globalThis.location?.origin !== origin) return;
      Object.defineProperty(globalThis, "careerHubDesktop", {
        value: Object.freeze({ session: Object.freeze({ token: async () => token }) }),
      });
    }, [appOrigin, windowSessionToken]);

    // ADR SEC-250 item 4: the configured Origin alone no longer bootstraps.
    const bootstrap = (origin, credential) => worker.evaluate(async ([target, value]) => {
      const response = await fetch(`${target}/api/session`, {
        cache: "no-store",
        credentials: "omit",
        method: "POST",
        headers: value ? { "X-Career-Hub-Extension-Credential": value } : {},
      });
      return {
        allowOrigin: response.headers.get("access-control-allow-origin"),
        payload: await response.json(),
        status: response.status,
      };
    }, [origin, credential]);
    const unpaired = await bootstrap(appOrigin, null);
    assert.equal(unpaired.status, 403);
    assert.equal(unpaired.payload.error_code, "EXTENSION_PAIRING_REQUIRED");

    // Pair through the real side panel with a code only the window token can create.
    const pairingPage = await context.newPage();
    await pairingPage.goto(`${extensionOrigin}/sidepanel.html`);
    await pairingPage.waitForSelector("#pairing-section:not([hidden])");
    const issued = await (await fetch(`${appOrigin}/api/extension/pairing-code`, {
      method: "POST", headers: { "X-Career-Hub-Token": windowSessionToken },
    })).json();
    assert.match(issued.code, /^[A-Z2-9]{4}-[A-Z2-9]{4}$/);
    await pairingPage.fill("#pairing-code-input", issued.code.toLowerCase());
    await pairingPage.click("#pairing-submit-button");
    await pairingPage.waitForSelector("#pairing-section[hidden]", { state: "attached" }).catch(async (error) => {
      const status = await pairingPage.locator("#pairing-status").textContent();
      throw new Error(`pairing did not complete: ${status}`, { cause: error });
    });
    await pairingPage.close();
    const credential = await worker.evaluate(async () => (
      await chrome.storage.local.get("careerHubExtensionCredential.v1")
    )["careerHubExtensionCredential.v1"]);
    assert.match(credential, /^[A-Za-z0-9_-]{43}$/);
    const extensionSession = await bootstrap(appOrigin, credential);
    assert.equal(extensionSession.status, 200);
    assert.equal(extensionSession.payload.client, "extension");
    assert.equal(extensionSession.allowOrigin, extensionOrigin);
    console.log("extension smoke: extension session bootstrap verified");

    let helperConfiguration = await browserApi(
      appOrigin,
      "/api/application-helper/configuration",
    );
    helperConfiguration = await browserApi(
      appOrigin,
      "/api/application-helper/configuration",
      {
        method: "PUT",
        payload: {
          contract_version: 2,
          enabled_fields: [
            "full_name",
            "email",
            "requires_future_sponsorship",
          ],
          expected_revision: helperConfiguration.revision,
        },
      },
    );
    const helperPage = await context.newPage();
    await helperPage.setViewportSize({ width: 420, height: 900 });
    await helperPage.goto(`${extensionOrigin}/sidepanel.html`);
    await helperPage.waitForFunction(() => (
      document.querySelector("#profile-field-count")?.textContent === "3 ready"
      && document.querySelector("#resume-count")?.textContent === "1 ready"
    ));
    await helperPage.evaluate(() => {
      globalThis.__careerHubCopiedValues = [];
      Object.defineProperty(navigator, "clipboard", {
        configurable: true,
        value: {
          writeText: async (value) => {
            globalThis.__careerHubCopiedValues.push(value);
          },
        },
      });
    });
    assert.equal(await helperPage.locator(".field-row").count(), 3);
    assert.equal(await helperPage.locator("#resume-list .shelf-row").count(), 1);
    assert.match(await helperPage.locator("#resume-list").innerText(), /General Resume/);
    assert.equal(
      (await helperPage.locator("body").innerText()).includes("person@example.com"),
      false,
    );
    assert.equal(
      await helperPage.locator(".field-row").filter({ hasText: "Email" })
        .locator(".field-preview").textContent(),
      "p•••@example.com",
    );
    await helperPage.screenshot({
      path: path.join(OUTPUT_ROOT, "application-helper-side-panel-desktop.png"),
      fullPage: true,
    });
    await helperPage.setViewportSize({ width: 300, height: 760 });
    await helperPage.screenshot({
      path: path.join(OUTPUT_ROOT, "application-helper-side-panel-compact.png"),
      fullPage: true,
    });

    const resumeVersions = await browserApi(appOrigin, "/api/resumes");
    const expectedResume = resumeVersions.resumes.find(
      (resume) => resume.display_name === "General Resume",
    );
    assert(expectedResume, "General Resume is missing from the Resume Library API");
    const resumePagePromise = context.waitForEvent("page", { timeout: 10000 });
    await helperPage.getByRole("button", {
      name: "Open General Resume in Resume Library",
      exact: true,
    }).click();
    const resumePage = await resumePagePromise;
    await resumePage.waitForLoadState("networkidle");
    assert.equal(resumePage.url(), `${appOrigin}/#resume-library`);
    const focusedResume = resumePage.locator(
      `[data-resume-library-record-id="${expectedResume.id}"]`,
    );
    try {
      await focusedResume.waitFor({ state: "visible", timeout: 10000 });
    } catch (error) {
      const diagnostic = await resumePage.evaluate(() => ({
        activeView: document.querySelector(".view.active")?.id || "",
        message: document.querySelector("#resume-library-message")?.textContent || "",
        records: document.querySelector("#resume-library-record-list")?.innerText || "",
        runMessage: document.querySelector("#run-message")?.textContent || "",
        scriptErrors: globalThis.__careerHubSmokeErrors || [],
        url: window.location.href,
      }));
      throw new Error(`Resume Library deep-link failed: ${JSON.stringify(diagnostic)}\n${error.message}`);
    }
    assert.match(await focusedResume.innerText(), /General Resume/);
    assert.equal(
      await focusedResume.getByRole("button", { name: "Download", exact: true }).isVisible(),
      true,
    );
    await resumePage.close();
    await helperPage.locator("#profile-value-shelf summary").click();
    const copyFullName = helperPage.getByRole("button", { name: "Copy Full name", exact: true });
    try {
      await copyFullName.waitFor({ state: "visible", timeout: 10000 });
    } catch (error) {
      const diagnostic = helperPage.isClosed()
        ? { closed: true }
        : await helperPage.evaluate(() => ({
          body: document.body?.innerText || "",
          closed: false,
          url: window.location.href,
        }));
      throw new Error(`Application helper disappeared after Resume Library open: ${JSON.stringify(diagnostic)}\n${error.message}`);
    }
    await copyFullName.click();
    await helperPage.getByText("Full name copied.", { exact: true }).waitFor();
    assert.deepEqual(
      await helperPage.evaluate(() => globalThis.__careerHubCopiedValues),
      ["Example Person"],
    );

    helperConfiguration = await browserApi(
      appOrigin,
      "/api/application-helper/configuration",
      {
        method: "PUT",
        payload: {
          contract_version: 2,
          enabled_fields: ["email"],
          expected_revision: helperConfiguration.revision,
        },
      },
    );
    await helperPage.evaluate(() => { globalThis.__careerHubCopiedValues = []; });
    await helperPage.getByRole("button", { name: "Copy Full name", exact: true }).click();
    await helperPage.getByText(
      "Nothing was copied because the displayed value was stale.",
      { exact: true },
    ).waitFor();
    assert.deepEqual(
      await helperPage.evaluate(() => globalThis.__careerHubCopiedValues),
      [],
    );
    assert.equal(await helperPage.locator(".field-row").count(), 1);
    await helperPage.getByRole("button", { name: "Copy Email", exact: true }).click();
    await helperPage.getByText("Email copied.", { exact: true }).waitFor();
    assert.deepEqual(
      await helperPage.evaluate(() => globalThis.__careerHubCopiedValues),
      ["person@example.com"],
    );

    helperConfiguration = await browserApi(
      appOrigin,
      "/api/application-helper/configuration",
      {
        method: "PUT",
        payload: {
          contract_version: 2,
          enabled_fields: ["full_name", "email", "phone", "linkedin"],
          expected_revision: helperConfiguration.revision,
        },
      },
    );
    await helperPage.getByRole("button", { name: "Refresh application assistant" }).click();
    await helperPage.waitForFunction(() => (
      document.querySelector("#profile-field-count")?.textContent === "4 ready"
    ));

    const providerReviews = [];
    for (const [route, expected] of [
      [
        "/application/prefill-workday",
        { selectable: ["email", "phone"], nonEmpty: [], ambiguous: [] },
      ],
      [
        "/application/prefill-greenhouse",
        { selectable: ["full_name", "phone", "linkedin"], nonEmpty: ["email"], ambiguous: [] },
      ],
      [
        "/application/prefill-lever",
        { selectable: ["full_name", "email"], nonEmpty: [], ambiguous: ["linkedin"] },
      ],
    ]) {
      const formPage = await context.newPage();
      await formPage.goto(`${jobOrigin}${route}`);
      const review = await sendExtensionCommand(worker, helperPage, formPage, {
        command: "career-hub.application-helper.review-form.v1",
      });
      assert.equal(review.ok, true, JSON.stringify(review));
      assert.equal(JSON.stringify(review).includes("person@example.com"), false);
      assert.deepEqual(
        review.mappings.filter((mapping) => mapping.selectable).map((mapping) => mapping.field_id).sort(),
        expected.selectable.slice().sort(),
      );
      assert.deepEqual(
        review.mappings.filter((mapping) => mapping.skip_reason === "non_empty").map((mapping) => mapping.field_id).sort(),
        expected.nonEmpty.slice().sort(),
      );
      assert.deepEqual(
        review.mappings.filter((mapping) => mapping.skip_reason === "ambiguous").map((mapping) => mapping.field_id).sort(),
        expected.ambiguous.slice().sort(),
      );
      providerReviews.push(review);
      await formPage.close();
    }
    assert.equal(providerReviews.length, 3);

    const helperScriptPath = path.join(extensionCopy, "application-form-helper.js");
    const hiddenHeavyPage = await context.newPage();
    await hiddenHeavyPage.goto(`${jobOrigin}/application/prefill-hidden-heavy`);
    await hiddenHeavyPage.addScriptTag({ path: helperScriptPath });
    const hiddenHeavyReview = await hiddenHeavyPage.evaluate(() => (
      runCareerHubApplicationForm("inspect", ["email", "phone"], 240)
    ));
    assert.equal(hiddenHeavyReview.controls_examined, 2);
    assert.deepEqual(
      hiddenHeavyReview.mappings.map((mapping) => mapping.field_id).sort(),
      ["email", "phone"],
    );
    await hiddenHeavyPage.close();

    const multipleFormsPage = await context.newPage();
    await multipleFormsPage.goto(`${jobOrigin}/application/prefill-multiple-forms`);
    await multipleFormsPage.addScriptTag({ path: helperScriptPath });
    const multipleFormsReview = await multipleFormsPage.evaluate(() => (
      runCareerHubApplicationForm("inspect", ["full_name", "email", "phone"], 240)
    ));
    assert.deepEqual(
      multipleFormsReview.mappings.map((mapping) => mapping.field_id).sort(),
      ["email", "full_name", "phone"],
    );
    assert.equal(
      multipleFormsReview.mappings.find((mapping) => mapping.field_id === "email").target_label,
      "Email address",
    );
    const multipleFormsFill = await multipleFormsPage.evaluate((review) => {
      const values = {
        full_name: "Example Person",
        email: "person@example.com",
        phone: "555-0100",
      };
      const instructions = review.mappings.map((mapping, index) => ({
        mapping_id: `mapping_${index + 1}`,
        field_id: mapping.field_id,
        target_key: mapping.target_key,
        value_kind: "text",
        value: values[mapping.field_id],
      }));
      return runCareerHubApplicationForm("fill", instructions, 240);
    }, multipleFormsReview);
    assert.equal(multipleFormsFill.ok, true);
    assert.deepEqual(await multipleFormsPage.evaluate(() => ({
      applicationEmail: document.querySelector("#application-form input[type='email']").value,
      newsletterEmail: document.querySelector("#newsletter-form input").value,
    })), {
      applicationEmail: "person@example.com",
      newsletterEmail: "",
    });
    await multipleFormsPage.close();

    const disabledWinnerPage = await context.newPage();
    await disabledWinnerPage.goto(`${jobOrigin}/application/prefill-disabled-winner`);
    await disabledWinnerPage.addScriptTag({ path: helperScriptPath });
    const disabledWinnerReview = await disabledWinnerPage.evaluate(() => (
      runCareerHubApplicationForm("inspect", ["website"], 240)
    ));
    assert.deepEqual(disabledWinnerReview.mappings, []);
    await disabledWinnerPage.close();

    const structuredPage = await context.newPage();
    await structuredPage.goto(`${jobOrigin}/application/prefill-structured`);
    await structuredPage.addScriptTag({ path: helperScriptPath });
    const structuredValues = {
      given_name: "Example",
      family_name: "Person",
      address_line1: "100 Example Street",
      city: "Atlanta",
      region: "GA",
      postal_code: "30303",
      country: "United States",
      work_authorization: "Authorized",
      requires_future_sponsorship: true,
    };
    const structuredReview = await structuredPage.evaluate((fieldIds) => (
      runCareerHubApplicationForm("inspect", fieldIds, 240)
    ), Object.keys(structuredValues));
    assert.deepEqual(
      structuredReview.mappings.map((mapping) => mapping.field_id).sort(),
      Object.keys(structuredValues).sort(),
    );
    assert.deepEqual(
      structuredReview.mappings.map((mapping) => mapping.target_kind).sort(),
      ["radio", "select", "select", "text", "text", "text", "text", "text", "textarea"].sort(),
    );
    const structuredFill = await structuredPage.evaluate(({ review, values }) => (
      runCareerHubApplicationForm("fill", review.mappings.map((mapping, index) => ({
        mapping_id: `mapping_${index + 1}`,
        field_id: mapping.field_id,
        target_key: mapping.target_key,
        value_kind: typeof values[mapping.field_id] === "boolean" ? "boolean" : "text",
        value: values[mapping.field_id],
      })), 240)
    ), { review: structuredReview, values: structuredValues });
    assert.equal(structuredFill.ok, true);
    assert.deepEqual(await structuredPage.evaluate(() => ({
      givenName: document.querySelector('[name="given_name"]').value,
      familyName: document.querySelector('[name="family_name"]').value,
      addressLine1: document.querySelector('[name="address_line1"]').value,
      city: document.querySelector('[name="city"]').value,
      region: document.querySelector('[name="region"]').value,
      postalCode: document.querySelector('[name="postal_code"]').value,
      country: document.querySelector('[name="country"]').value,
      authorization: document.querySelector('[name="work_authorization"]').value,
      sponsorship: document.querySelector('[name="future_sponsorship"]:checked')?.value,
    })), {
      givenName: "Example",
      familyName: "Person",
      addressLine1: "100 Example Street",
      city: "Atlanta",
      region: "GA",
      postalCode: "30303",
      country: "United States",
      authorization: "Authorized",
      sponsorship: "yes",
    });
    await structuredPage.close();

    let demographics = await browserApi(
      appOrigin,
      "/api/application-demographics",
    );
    demographics = await browserApi(
      appOrigin,
      "/api/application-demographics",
      {
        method: "PUT",
        payload: {
          answers: {
            gender: "Female",
            hispanic_latino: "No",
            veteran_status: "Not a protected veteran",
            disability_status: "No",
          },
          contract_version: 1,
          expected_revision: demographics.revision,
          section: "US",
        },
      },
    );
    assert.equal(demographics.sections[0].fields.length, 4);
    const demographicFieldIds = [
      "us_gender_identity",
      "us_eeo_gender",
      "us_hispanic_latino",
      "us_veteran_status",
      "us_disability_status",
    ];
    helperConfiguration = await browserApi(
      appOrigin,
      "/api/application-helper/configuration",
      {
        method: "PUT",
        payload: {
          contract_version: 2,
          enabled_fields: demographicFieldIds,
          expected_revision: helperConfiguration.revision,
        },
      },
    );
    await helperPage.getByRole("button", { name: "Refresh application assistant" }).click();
    await helperPage.waitForFunction(() => (
      document.querySelector("#profile-field-count")?.textContent === "5 ready"
    ));
    assert.equal(await helperPage.locator("#field-list .field-row").count(), 5);
    assert.equal(await helperPage.locator("#review-fields-button").isDisabled(), true);

    const demographicPage = await context.newPage();
    await demographicPage.goto(`${jobOrigin}/application/prefill-us-demographics`);
    const demographicReview = await sendExtensionCommand(
      worker,
      helperPage,
      demographicPage,
      { command: "career-hub.application-helper.review-form.v1" },
    );
    assert.deepEqual(demographicReview, {
      error_code: "APPLICATION_HELPER_NO_PREFILL_FIELDS",
      ok: false,
    });
    assert.deepEqual(await demographicPage.evaluate(() => ({
      disability: document.querySelector('[aria-controls="disability-options"]').value,
      eeoGender: document.querySelector('[aria-controls="gender-options"]').value,
      genderIdentity: document.querySelector('[aria-controls="gender-identity-options"]').value,
      hispanic: document.querySelector('[aria-controls="hispanic-options"]').value,
      submitCount: window.__submitCount,
      veteran: document.querySelector('[aria-controls="veteran-options"]').value,
    })), {
      disability: "",
      eeoGender: "",
      genderIdentity: "",
      hispanic: "",
      submitCount: 0,
      veteran: "",
    });
    await demographicPage.close();

    helperConfiguration = await browserApi(
      appOrigin,
      "/api/application-helper/configuration",
      {
        method: "PUT",
        payload: {
          contract_version: 2,
          enabled_fields: ["full_name", "email", "phone", "linkedin"],
          expected_revision: helperConfiguration.revision,
        },
      },
    );
    await helperPage.getByRole("button", { name: "Refresh application assistant" }).click();
    await helperPage.waitForFunction(() => (
      document.querySelector("#profile-field-count")?.textContent === "4 ready"
    ));
    console.log("extension smoke: bounded form scoping, structured controls, and canonical winner checks verified");

    const prefillPage = await context.newPage();
    await prefillPage.goto(`${jobOrigin}/application/prefill-plain`);
    await activateBrowserPage(worker, prefillPage);
    await helperPage.evaluate(() => document.querySelector("#review-fields-button").click());
    await helperPage.getByText("4 fields ready for review", { exact: true }).waitFor();
    assert.equal(await helperPage.locator(".mapping-row").count(), 4);
    assert.equal(await helperPage.locator(".mapping-checkbox:checked").count(), 4);
    assert.equal(
      (await helperPage.locator("body").innerText()).includes("person@example.com"),
      false,
    );
    await helperPage.setViewportSize({ width: 420, height: 1050 });
    await helperPage.screenshot({
      path: path.join(OUTPUT_ROOT, "application-helper-prefill-review.png"),
      fullPage: true,
    });

    await prefillPage.evaluate(() => {
      document.querySelector('input[name="email"]').value = "user-entered@example.test";
    });
    await activateBrowserPage(worker, prefillPage);
    await helperPage.evaluate(() => document.querySelector("#fill-selected-button").click());
    await helperPage.getByText("Form changed after review", { exact: true }).waitFor();
    assert.deepEqual(
      await prefillPage.evaluate(() => ({
        fullName: document.querySelector('input[name="full_name"]').value,
        email: document.querySelector('input[name="email"]').value,
        phone: document.querySelector('input[name="phone"]').value,
        linkedin: document.querySelector('input[name="linkedin_profile"]').value,
        submitCount: window.__submitCount,
      })),
      {
        fullName: "",
        email: "user-entered@example.test",
        phone: "",
        linkedin: "",
        submitCount: 0,
      },
    );
    await prefillPage.evaluate(() => {
      document.querySelector('input[name="email"]').value = "";
    });
    await activateBrowserPage(worker, prefillPage);
    await helperPage.evaluate(() => document.querySelector("#review-fields-button").click());
    await helperPage.getByText("4 fields ready for review", { exact: true }).waitFor();
    await prefillPage.evaluate(() => window.__renderIdentityFields());
    await activateBrowserPage(worker, prefillPage);
    await helperPage.evaluate(() => document.querySelector("#fill-selected-button").click());
    await helperPage.getByText("4 fields filled", { exact: true }).waitFor();
    const filledForm = await prefillPage.evaluate(() => ({
      fullName: document.querySelector('input[name="full_name"]').value,
      email: document.querySelector('input[name="email"]').value,
      phone: document.querySelector('input[name="phone"]').value,
      linkedin: document.querySelector('input[name="linkedin_profile"]').value,
      password: document.querySelector('input[name="password"]').value,
      freeText: document.querySelector('textarea[name="screening_answer"]').value,
      submitCount: window.__submitCount,
      inputEvents: window.__inputEvents,
    }));
    assert.deepEqual(filledForm, {
      fullName: "Example Person",
      email: "person@example.com",
      phone: "555-0100",
      linkedin: "https://www.linkedin.com/in/example",
      password: "",
      freeText: "",
      submitCount: 0,
      inputEvents: 4,
    });
    await helperPage.screenshot({
      path: path.join(OUTPUT_ROOT, "application-helper-prefill-complete.png"),
      fullPage: true,
    });
    const storageAfterPrefill = JSON.stringify(await worker.evaluate(async () => ({
      local: await chrome.storage.local.get(null),
      session: await chrome.storage.session.get(null),
    })));
    assert.equal(storageAfterPrefill.includes("person@example.com"), false);
    assert.equal(storageAfterPrefill.includes("Example Person"), false);
    await prefillPage.close();
    console.log("extension smoke: reviewed prefill fixtures and no-submit boundary verified");

    const answerPage = await context.newPage();
    await answerPage.goto(`${jobOrigin}/application/reusable-answers`);
    await activateBrowserPage(worker, answerPage);
    await helperPage.getByRole("button", { name: "Find completed answers", exact: true }).click();
    await helperPage.getByText("3 completed answers found", { exact: true }).waitFor();
    assert.equal(await helperPage.locator("#answer-candidate-list .candidate-row").count(), 3);
    const candidateText = await helperPage.locator("#answer-candidate-list").innerText();
    assert.match(candidateText, /How did you hear about us\?/);
    assert.match(candidateText, /What is your preferred work setup\?/);
    assert.match(candidateText, /Are you willing to travel occasionally\?/);
    assert.doesNotMatch(candidateText, /sponsorship|salary|password|Why do you want/i);
    await helperPage.getByRole("button", {
      name: "Save reusable answer for How did you hear about us?",
      exact: true,
    }).click();
    await helperPage.getByText("Reusable answer saved", { exact: true }).waitFor();
    assert.equal(await helperPage.locator("#answer-count").textContent(), "1 saved");
    assert.match(await helperPage.locator("#answer-list").innerText(), /Referral/);

    await answerPage.locator('[name="referral"]').selectOption("");
    await activateBrowserPage(worker, answerPage);
    await helperPage.getByRole("button", { name: "Review saved answers", exact: true }).click();
    await helperPage.getByText("1 saved answer ready", { exact: true }).waitFor();
    assert.equal(await helperPage.locator(".answer-mapping-checkbox:checked").count(), 1);
    await helperPage.getByRole("button", { name: "Fill 1 selected answer", exact: true }).click();
    await helperPage.getByText("1 saved answer filled", { exact: true }).waitFor();
    assert.deepEqual(await answerPage.evaluate(() => ({
      referral: document.querySelector('[name="referral"]').value,
      salary: document.querySelector('[name="salary"]').value,
      sponsorship: document.querySelector('[name="sponsorship"]').value,
      submitCount: window.__submitCount,
    })), {
      referral: "referral",
      salary: "100000",
      sponsorship: "Yes",
      submitCount: 0,
    });
    await helperPage.evaluate(() => { globalThis.__careerHubCopiedValues = []; });
    await helperPage.getByRole("button", {
      name: "Copy saved answer for How did you hear about us?",
      exact: true,
    }).click();
    await helperPage.getByText("Answer copied", { exact: true }).waitFor();
    assert.deepEqual(
      await helperPage.evaluate(() => globalThis.__careerHubCopiedValues),
      ["Referral"],
    );
    await helperPage.screenshot({
      path: path.join(OUTPUT_ROOT, "application-helper-reusable-answer-complete.png"),
      fullPage: true,
    });
    await answerPage.close();
    console.log("extension smoke: explicit reusable-answer save, exact refill, and no-submit boundary verified");

    helperConfiguration = await browserApi(
      appOrigin,
      "/api/application-helper/configuration",
      {
        method: "PUT",
        payload: {
          contract_version: 2,
          enabled_fields: [],
          expected_revision: helperConfiguration.revision,
        },
      },
    );
    await helperPage.getByRole("button", { name: "Refresh application assistant" }).click();
    await helperPage.waitForFunction(() => (
      document.querySelector("#profile-field-count")?.textContent === "0 ready"
    ));
    await helperPage.getByText("No profile values enabled", { exact: true }).waitFor();
    assert.equal(await helperPage.locator("#resume-count").textContent(), "1 ready");
    assert.equal(await helperPage.locator("#answer-count").textContent(), "1 saved");
    const extensionStorage = await worker.evaluate(async () => ({
      local: await chrome.storage.local.get(null),
      session: await chrome.storage.session.get(null),
    }));
    const serializedStorage = JSON.stringify(extensionStorage);
    assert.equal(serializedStorage.includes("person@example.com"), false);
    assert.equal(serializedStorage.includes("Example Person"), false);
    assert.equal(serializedStorage.includes("Referral"), false);
    await helperPage.close();
    console.log("extension smoke: helper copy, stale guard, masking, and storage boundary verified");

    const jobPage = await context.newPage();
    await jobPage.goto(`${jobOrigin}/job/jsonld`);
    assert.equal(await jobPage.title(), "Platform Engineer — Example Systems");

    const popup = await openExtensionPopupPage(
      context,
      worker,
      extensionOrigin,
      jobPage,
    );
    console.log("extension smoke: popup page opened");
    await popup.locator("#tab-title").waitFor({
      state: "visible",
    });
    assert.equal(
      await popup.locator("#tab-title").textContent(),
      "Platform Engineer — Example Systems",
    );
    await popup.waitForFunction(
      () => document.querySelector("#page-button")?.disabled === false,
    );
    assert.equal(await popup.locator("#selection-button").isDisabled(), true);
    console.log("extension smoke: default popup content verified");
    await screenshotPopup(popup, "extension-popup-default.png");
    console.log("extension smoke: default screenshot written");

    await clickPopupAction(popup, worker, jobPage, "#page-button");
    await waitForResult(popup, "Page saved for review");
    assert.equal(await popup.locator("#success-icon").isVisible(), true);
    assert.equal(await popup.locator("#error-icon").isVisible(), false);
    assert.equal(await popup.locator("#assessment-receipt").isVisible(), true);
    assert(
      ["No clear conflict found", "Review needed", "Clear conflict found"].includes(
        await popup.locator("#receipt-status").textContent(),
      ),
    );
    assert.equal(/\b\d+%\b/.test(await popup.locator("body").innerText()), false);
    assert.equal(await popup.locator("#receipt-contact-candidate-count").isVisible(), true);
    assert.equal(
      await popup.locator("#receipt-contact-candidate-count").textContent(),
      "1 contact candidate flagged",
    );
    assert.equal(await popup.locator("#review-button").isVisible(), true);
    await screenshotPopup(popup, "extension-popup-success.png");
    console.log("extension smoke: JSON-LD page capture created");

    const firstFeed = await browserApi(appOrigin, "/api/discoveries?limit=20");
    assert.equal(firstFeed.items.length, 1);
    assert.deepEqual(firstFeed.items[0].source_kinds, ["extension"]);
    assert.equal(firstFeed.items[0].needs_content, true);
    assert.equal(firstFeed.items[0].content_scope, "page_visible");
    assert.equal(firstFeed.items[0].title, "Platform Engineer");
    assert.equal(firstFeed.items[0].company, "Example Systems");
    assert.equal(firstFeed.items[0].canonical_url, `${jobOrigin}/job/jsonld`);

    const exactReviewPromise = context.waitForEvent("page", { timeout: 10000 });
    await clickPopupAction(popup, worker, jobPage, "#review-button");
    const exactReviewPage = await exactReviewPromise;
    await exactReviewPage.waitForLoadState("networkidle");
    assert.equal(
      exactReviewPage.url(),
      `${appOrigin}/#job-feed/${firstFeed.items[0].discovery_id}`,
    );
    await exactReviewPage.getByRole("heading", {
      name: "Platform Engineer",
      exact: true,
    }).waitFor();
    assert.equal(
      await exactReviewPage.getByRole("button", {
        name: "Confirm complete JD",
        exact: true,
      }).isVisible(),
      true,
    );
    assert.equal(
      await exactReviewPage.getByRole("button", {
        name: "Save job",
        exact: true,
      }).count(),
      0,
    );
    const contactPanel = exactReviewPage.locator(".contact-candidate-panel");
    await contactPanel.waitFor();
    assert.match(await contactPanel.textContent(), /Jane Doe/);
    assert.equal(
      await contactPanel.getByRole("button", { name: /Save contact/ }).isDisabled(),
      true,
    );
    console.log("extension smoke: exact server-issued review handoff verified");

    const repeatPopup = await openExtensionPopupPage(
      context,
      worker,
      extensionOrigin,
      jobPage,
    );
    await clickPopupAction(repeatPopup, worker, jobPage, "#page-button");
    await waitForResult(repeatPopup, "Page capture updated");
    assert.equal(await repeatPopup.locator("#success-icon").isVisible(), true);

    const secondFeed = await browserApi(appOrigin, "/api/discoveries?limit=20");
    assert.equal(secondFeed.items.length, 1);

    const preparePopup = await openExtensionPopupPage(
      context,
      worker,
      extensionOrigin,
      jobPage,
    );
    const prepareReviewPromise = context.waitForEvent("page", { timeout: 10000 });
    await clickPopupAction(
      preparePopup,
      worker,
      jobPage,
      "#prepare-application-button",
    );
    const prepareReviewPage = await prepareReviewPromise;
    await prepareReviewPage.waitForLoadState("networkidle");
    assert.equal(
      prepareReviewPage.url(),
      `${appOrigin}/#job-feed/${firstFeed.items[0].discovery_id}/prepare`,
    );
    prepareReviewPage.once("dialog", (dialog) => dialog.accept());
    await prepareReviewPage.getByRole("button", {
      name: "Confirm complete JD",
      exact: true,
    }).click();
    await prepareReviewPage.getByRole("button", {
      name: "Prepare application",
      exact: true,
    }).waitFor();
    await prepareReviewPage.screenshot({
      path: path.join(OUTPUT_ROOT, "extension-prepare-review.png"),
      fullPage: true,
    });
    console.log("extension smoke: direct Prepare review intent verified");

    for (const [route, expectedTitle] of [
      ["/job/semantic", "Data Engineer — Northstar"],
      ["/job/body", "Backend Engineer — Atlas"],
      ["/job/malformed-jsonld", "QA Engineer — Signal"],
    ]) {
      const fixturePage = await context.newPage();
      await fixturePage.goto(`${jobOrigin}${route}`);
      assert.equal(await fixturePage.title(), expectedTitle);
      const fixturePopup = await openExtensionPopupPage(
        context,
        worker,
        extensionOrigin,
        fixturePage,
      );
      await fixturePopup.waitForFunction(
        () => document.querySelector("#page-button")?.disabled === false,
      );
      await clickPopupAction(
        fixturePopup,
        worker,
        fixturePage,
        "#page-button",
      );
      await waitForResult(fixturePopup, "Page saved for review");
    }
    console.log("extension smoke: semantic and guarded-body fallbacks verified");

    const selectionPage = await context.newPage();
    await selectionPage.goto(`${jobOrigin}/job/selection`);
    await selectionPage.evaluate(() => {
      const target = document.querySelector("#selection-target");
      const range = document.createRange();
      range.selectNodeContents(target);
      const selection = globalThis.getSelection();
      selection.removeAllRanges();
      selection.addRange(range);
    });
    const selectionPopup = await openExtensionPopupPage(
      context,
      worker,
      extensionOrigin,
      selectionPage,
    );
    await selectionPopup.waitForFunction(
      () => document.querySelector("#selection-button")?.disabled === false,
    );
    await clickPopupAction(
      selectionPopup,
      worker,
      selectionPage,
      "#selection-button",
    );
    await waitForResult(selectionPopup, "Selection saved");
    assert.equal(
      await selectionPopup.locator("#receipt-status").textContent(),
      "Assessment unavailable",
    );
    assert.match(
      await selectionPopup.locator("#receipt-note").textContent(),
      /No gaps were inferred/,
    );

    const linkPage = await context.newPage();
    await linkPage.goto(`${jobOrigin}/job/link`);
    const linkPopup = await openExtensionPopupPage(
      context,
      worker,
      extensionOrigin,
      linkPage,
    );
    await clickPopupAction(linkPopup, worker, linkPage, "#link-button");
    await waitForResult(linkPopup, "Added to Job Feed");
    assert.equal(
      await linkPopup.locator("#receipt-status").textContent(),
      "Assessment unavailable",
    );

    const completeFeed = await browserApi(appOrigin, "/api/discoveries?limit=20");
    assert.equal(completeFeed.items.length, 6);
    const scopeByUrl = new Map(
      completeFeed.items.map((item) => [item.canonical_url, item.content_scope]),
    );
    assert.equal(scopeByUrl.get(`${jobOrigin}/job/selection`), "selection");
    assert.equal(scopeByUrl.get(`${jobOrigin}/job/link`), "link_only");
    assert.equal(scopeByUrl.get(`${jobOrigin}/job/semantic`), "page_visible");
    assert.equal(scopeByUrl.get(`${jobOrigin}/job/body`), "page_visible");
    assert.equal(
      scopeByUrl.get(`${jobOrigin}/job/malformed-jsonld`),
      "page_visible",
    );

    const genericSplitUrl = `${jobOrigin}/job/generic-split`;
    for (const [url, expectedTitle, included, excluded, strategy] of [
      [
        genericSplitUrl,
        "Integration Engineer — Northstar",
        "GENERIC DETAIL ONLY",
        "UNRELATED SEARCH RESULT",
        "ranked_container",
      ],
      [
        "https://www.indeed.com/viewjob?vjk=precision-123",
        "Precision Platform Engineer - Example Systems | Indeed",
        "INDEED DETAIL ONLY",
        "UNRELATED INDEED RESULT",
        "provider_container",
      ],
      [
        "https://www.linkedin.com/jobs/view/9876543210/",
        "Applied AI Engineer | Synthetic Labs | LinkedIn",
        "LINKEDIN DETAIL ONLY",
        "UNRELATED LINKEDIN RESULT",
        "provider_container",
      ],
    ]) {
      const splitPage = await context.newPage();
      await splitPage.goto(url);
      assert.equal(await splitPage.title(), expectedTitle);
      await splitPage.addScriptTag({
        path: path.join(extensionCopy, "page-extractor.js"),
      });
      const extraction = await splitPage.evaluate(() => extractCareerHubVisiblePage(
        500000,
        160,
        50000,
        1000000,
      ));
      assert.equal(extraction.ok, true);
      assert.equal(extraction.extractor.version, 2);
      assert.equal(extraction.extractor.strategy, strategy);
      assert.match(extraction.source_text, new RegExp(included));
      assert.doesNotMatch(extraction.source_text, new RegExp(excluded));
      if (url === genericSplitUrl) {
        const splitPopup = await openExtensionPopupPage(
          context,
          worker,
          extensionOrigin,
          splitPage,
        );
        await clickPopupAction(splitPopup, worker, splitPage, "#page-button");
        await waitForResult(splitPopup, "Page saved for review");
      }
      await splitPage.close();
    }
    console.log("extension smoke: provider and title-anchored split panes isolated");

    const splitFeed = await browserApi(appOrigin, "/api/discoveries?limit=20");
    assert.equal(splitFeed.items.length, 7);
    const splitSummary = splitFeed.items.find(
      (item) => item.canonical_url === genericSplitUrl,
    );
    assert(splitSummary, "Generic split-pane capture is missing from Job Feed");
    const splitDetail = await browserApi(
      appOrigin,
      `/api/discoveries/${splitSummary.discovery_id}`,
    );
    assert.match(splitDetail.source_text, /GENERIC DETAIL ONLY/);
    assert.doesNotMatch(splitDetail.source_text, /UNRELATED SELECTED CARD SUMMARY/);
    assert.doesNotMatch(splitDetail.source_text, /UNRELATED SEARCH RESULT/);
    assert.equal(splitDetail.content_scope, "page_visible");

    const jobsBeforePromotion = await browserApi(appOrigin, "/api/jobs?limit=20");
    assert.equal(jobsBeforePromotion.items.length, 0);
    const correctedSplit = await browserApi(
      appOrigin,
      `/api/discoveries/${splitSummary.discovery_id}/field-overrides`,
      {
        method: "PUT",
        payload: {
          contract_version: 1,
          expected_override_revision: 0,
          overrides: {
            company: "Northstar",
            location: "Remote",
            title: "Integration Engineer",
          },
        },
      },
    );
    assert.equal(correctedSplit.company, "Northstar");
    const confirmedSplit = await browserApi(
      appOrigin,
      `/api/discoveries/${splitSummary.discovery_id}/confirm-content`,
      {
        method: "POST",
        payload: {
          contract_version: 1,
          expected_revision: correctedSplit.revision,
        },
      },
    );
    assert.equal(confirmedSplit.content_scope, "confirmed_full");
    const promotedSplit = await browserApi(
      appOrigin,
      `/api/discoveries/${splitSummary.discovery_id}/promote`,
      {
        method: "POST",
        payload: {
          action: "save_job",
          contract_version: 1,
          expected_revision: confirmedSplit.revision,
        },
      },
    );
    assert.equal(promotedSplit.outcome, "promoted");
    assert.equal(promotedSplit.job.company, "Northstar");
    assert.equal(promotedSplit.job.application.current_status, "captured");
    const incomingFiles = fs.readdirSync(path.join(dataRoot, "jobs", "incoming"));
    assert.equal(incomingFiles.length, 1);
    const promotedSource = fs.readFileSync(
      path.join(dataRoot, "jobs", "incoming", incomingFiles[0]),
      "utf8",
    );
    assert.match(promotedSource, /GENERIC DETAIL ONLY/);
    assert.doesNotMatch(promotedSource, /UNRELATED SELECTED CARD SUMMARY/);
    assert.doesNotMatch(promotedSource, /UNRELATED SEARCH RESULT/);
    console.log("extension smoke: isolated split-pane source promoted through JD intake");

    const handshakePage = await context.newPage();
    await handshakePage.goto("https://app.joinhandshake.com/job-search/555000");
    await handshakePage.addScriptTag({
      path: path.join(extensionCopy, "page-extractor.js"),
    });
    const handshakeExtraction = await handshakePage.evaluate(() => (
      extractCareerHubVisiblePage(500000, 160, 50000, 1000000)
    ));
    assert.deepEqual(handshakeExtraction, {
      error_code: "CAPTURE_AMBIGUOUS",
      ok: false,
    });
    await handshakePage.close();

    for (const [route, expectedErrorTitle] of [
      ["/login", "No job text found"],
      ["/ambiguous", "One job description wasn’t clear"],
      ["/search-results", "One job description wasn’t clear"],
      ["/two-jobs", "One job description wasn’t clear"],
      ["/oversized", "Page is too large"],
    ]) {
      const errorPage = await context.newPage();
      await errorPage.goto(`${jobOrigin}${route}`);
      const errorPopup = await openExtensionPopupPage(
        context,
        worker,
        extensionOrigin,
        errorPage,
      );
      await clickPopupAction(errorPopup, worker, errorPage, "#page-button");
      await waitForResult(errorPopup, expectedErrorTitle);
      assert.equal(await errorPopup.locator("#error-icon").isVisible(), true);
    }
    const feedAfterErrors = await browserApi(appOrigin, "/api/discoveries?limit=20");
    assert.equal(feedAfterErrors.items.length, 7);

    const jobs = await browserApi(appOrigin, "/api/jobs?limit=20");
    assert.equal(jobs.items.length, 1);
    assert.equal(jobs.items[0].company, "Northstar");
    console.log("extension smoke: repeat capture merged into one Feed item");
    console.log("extension smoke: selection, page, and link boundaries verified");
    console.log("extension smoke: capture alone created no Job; explicit promotion created one");

    const attendedUrl = `${jobOrigin}/application/session`;
    const attendedCreated = await browserApi(appOrigin, "/api/jobs", {
      expectedStatus: 201,
      method: "POST",
      payload: {
        company: "Session Labs",
        source_name: "attended_engineer.md",
        source_text: "Build reliable Python services and document application workflows.",
        source_url: attendedUrl,
        title: "Attended Engineer",
        // Match the first-party intake UI's stable retry identity. Native
        // intake deliberately refuses the older keyless request envelope.
        idempotency_key: "extension-smoke-attended-job-v1",
      },
    });
    const attendedJobId = attendedCreated.job.id;

    const attendedPage = await context.newPage();
    await attendedPage.goto(attendedUrl);
    const attendedPopup = await openExtensionPopupPage(
      context,
      worker,
      extensionOrigin,
      attendedPage,
    );
    await clickPopupAction(
      attendedPopup,
      worker,
      attendedPage,
      "#arm-application-button",
    );
    await waitForResult(attendedPopup, "Submission tracking is on");

    const directTrackingApplication = await browserApi(
      appOrigin,
      `/api/jobs/${encodeURIComponent(attendedJobId)}/application`,
    );
    assert.equal(directTrackingApplication.current_status, "preparing");

    await attendedPage.locator("#continue-button").click();
    await delay(350);
    let submissionReviews = await browserApi(
      appOrigin,
      "/api/application-sessions/reviews",
    );
    assert.equal(submissionReviews.total, 0);
    await attendedPage.locator("#required-name").fill("Synthetic Candidate");
    await attendedPage.locator("#continue-button").click();
    await attendedPage.locator("#final-submit").waitFor();
    submissionReviews = await browserApi(
      appOrigin,
      "/api/application-sessions/reviews",
    );
    assert.equal(submissionReviews.total, 0);

    await attendedPage.locator("#final-submit").click();
    await attendedPage.getByRole("heading", {
      name: "Your application has been successfully submitted",
      exact: true,
    }).waitFor();
    const reviewDeadline = Date.now() + 8000;
    while (Date.now() < reviewDeadline) {
      submissionReviews = await browserApi(
        appOrigin,
        "/api/application-sessions/reviews",
      );
      if (submissionReviews.total === 1) break;
      await delay(150);
    }
    assert.equal(submissionReviews.total, 1);
    assert.equal(submissionReviews.items[0].job_id, attendedJobId);
    const beforeConfirmation = await browserApi(
      appOrigin,
      `/api/jobs/${encodeURIComponent(attendedJobId)}/application`,
    );
    assert.equal(beforeConfirmation.current_status, "preparing");

    const applicationsPage = await context.newPage();
    await applicationsPage.goto(`${appOrigin}/#applications`);
    const confirmationSection = applicationsPage.locator(
      "#application-session-reviews",
    );
    await confirmationSection.waitFor({ state: "visible" });
    await applicationsPage.getByText(
      "Mark this application as applied?",
      { exact: true },
    ).waitFor();
    await applicationsPage.screenshot({
      path: path.join(OUTPUT_ROOT, "application-session-confirmation-desktop.png"),
      fullPage: true,
    });
    await applicationsPage.setViewportSize({ width: 640, height: 900 });
    await applicationsPage.screenshot({
      path: path.join(OUTPUT_ROOT, "application-session-confirmation-mobile.png"),
      fullPage: true,
    });
    await applicationsPage.getByRole("button", {
      name: "Mark Applied",
      exact: true,
    }).click();
    await confirmationSection.waitFor({ state: "hidden" });
    const afterConfirmation = await browserApi(
      appOrigin,
      `/api/jobs/${encodeURIComponent(attendedJobId)}/application`,
    );
    assert.equal(afterConfirmation.current_status, "applied");
    console.log("extension smoke: attended multi-step submit and explicit confirmation verified");

    await stopProcess(careerHub);
    careerHub = null;
    console.log("extension smoke: capture contract and dedup verified");

    const unavailableHelper = await context.newPage();
    await unavailableHelper.setViewportSize({ width: 360, height: 760 });
    await unavailableHelper.goto(`${extensionOrigin}/sidepanel.html`);
    await unavailableHelper.getByText("Career Hub isn’t reachable", { exact: true }).waitFor();
    assert.equal(
      (await unavailableHelper.locator("body").innerText()).includes("person@example.com"),
      false,
    );
    await unavailableHelper.screenshot({
      path: path.join(OUTPUT_ROOT, "application-helper-side-panel-offline.png"),
      fullPage: true,
    });
    await unavailableHelper.close();

    const unavailablePopup = await openExtensionPopupPage(
      context,
      worker,
      extensionOrigin,
      jobPage,
    );
    await clickPopupAction(unavailablePopup, worker, jobPage, "#link-button");
    await waitForResult(unavailablePopup, "Career Hub isn’t reachable");
    assert.equal(await unavailablePopup.locator("#success-icon").isVisible(), false);
    assert.equal(await unavailablePopup.locator("#error-icon").isVisible(), true);
    assert.equal(await unavailablePopup.locator(".brand").isVisible(), true);
    await unavailablePopup.getByText("Not running", { exact: true }).waitFor();
    assert.equal(await unavailablePopup.locator("#connection-badge").isVisible(), true);
    assert.equal(await unavailablePopup.getByRole("button", { name: "Retry", exact: true }).isEnabled(), true);
    assert.equal(await unavailablePopup.getByRole("button", { name: "Back", exact: true }).isVisible(), true);
    await screenshotPopup(unavailablePopup, "extension-popup-error.png");
    await unavailablePopup.getByRole("button", { name: "Back", exact: true }).click();
    await unavailablePopup.locator("#capture-view").waitFor({ state: "visible" });
    assert.equal(await unavailablePopup.locator("#connection-card").isVisible(), true);
    assert.match(await unavailablePopup.locator("#connection-message").innerText(), /Start Career Hub/);

    console.log(`extension smoke: OK (${extensionOrigin})`);
    console.log(`artifacts: ${OUTPUT_ROOT}`);
  } catch (error) {
    primaryError = error;
    throw error;
  } finally {
    try {
      await stopProcess(careerHub);
    } catch (cleanupError) {
      // Report the cleanup problem, but keep the failure that stopped the smoke.
      if (!primaryError) throw cleanupError;
      console.error(`extension smoke: cleanup after failure also failed: ${cleanupError.stack || cleanupError}`);
    }
    if (context) {
      await withTimeout(
        context.close(),
        10000,
        "Browser context cleanup",
      );
    }
    await closeServer(jobServer);
    removeSafeTemporaryDirectory(browserProfile, "career-hub-extension-profile-");
    removeSafeTemporaryDirectory(dataContainer, "career-hub-extension-data-");
    removeSafeTemporaryDirectory(extensionCopy, "career-hub-extension-build-");
  }
}

main().catch((error) => {
  console.error(error.stack || error);
  process.exitCode = 1;
});
