"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

const PROJECT_ROOT = path.resolve(__dirname, "..");
const EXTENSION_ROOT = path.join(PROJECT_ROOT, "extension");
const EXTENSION_ID = "jhgaecfochimmafoopcpodncoeobecfg";
const APP_ORIGIN = "http://127.0.0.1:8766";
// ADR SEC-250 item 4. Built at runtime so no credential-shaped literal exists.
const TEST_CREDENTIAL = Buffer.alloc(32, 0x3c).toString("base64url");
const CREDENTIAL_STORAGE_KEY = "careerHubExtensionCredential.v1";
const PANEL_SENDER = { id: EXTENSION_ID, url: `chrome-extension://${EXTENSION_ID}/sidepanel.html` };
const PAGE_SENDER = { id: EXTENSION_ID, tab: { id: 41 }, url: "https://jobs.example.test/apply" };
const LEGACY_APPLICATION_SESSION_STORAGE_KEY = "application_sessions_by_tab_v1";
const APPLICATION_SESSION_STORAGE_KEY_PREFIX = "application_session_by_tab_v2:";
const APPLICATION_SESSION_ID =
  "application_session_0123456789abcdef0123456789abcdef";

const browserApiSource = fs.readFileSync(
  path.join(EXTENSION_ROOT, "browser-api.js"),
  "utf8",
);
const extractorSource = fs.readFileSync(
  path.join(EXTENSION_ROOT, "page-extractor.js"),
  "utf8",
);
const applicationObserverSource = fs.readFileSync(
  path.join(EXTENSION_ROOT, "application-session-observer.js"),
  "utf8",
);
const applicationFormHelperSource = fs.readFileSync(
  path.join(EXTENSION_ROOT, "application-form-helper.js"),
  "utf8",
);
const applicationAnswerHelperSource = fs.readFileSync(
  path.join(EXTENSION_ROOT, "application-answer-helper.js"),
  "utf8",
);
const workerSource = fs.readFileSync(
  path.join(EXTENSION_ROOT, "service-worker.js"),
  "utf8",
);

let activeTab;
let fetchPlan;
let fetchCalls;
let messageListener;
let scriptPlan;
let scriptCalls;
let createdTabs;
let sidePanelCalls;
let storageState;
let localStorageState;
let tabGetCalls;
let tabUpdatedListener;
let tabRemovedListener;
let tabsById;

function response(status, payload) {
  return {
    ok: status >= 200 && status < 300,
    status,
    async json() {
      return payload;
    },
  };
}

function reset() {
  activeTab = {
    id: 41,
    windowId: 7,
    title: "Platform Engineer — Example Systems",
    url: "https://jobs.example.test/platform-engineer?ref=mail",
  };
  fetchPlan = [];
  fetchCalls = [];
  scriptPlan = [];
  scriptCalls = [];
  createdTabs = [];
  sidePanelCalls = [];
  storageState = {};
  // Paired by default; the pairing tests start from an unpaired worker.
  localStorageState = { [CREDENTIAL_STORAGE_KEY]: TEST_CREDENTIAL };
  tabGetCalls = [];
  tabsById = new Map([[activeTab.id, activeTab]]);
}

const sandbox = {
  AbortController,
  DOMException,
  URL,
  clearTimeout,
  console,
  crypto: { randomUUID: () => "9f635457-7814-4e5d-9695-5dbf2530589e" },
  fetch: async (url, options = {}) => {
    fetchCalls.push({ options, url });
    if (!fetchPlan.length) {
      throw new Error(`Unexpected fetch: ${url}`);
    }
    const planned = fetchPlan.shift();
    if (planned instanceof Error) {
      throw planned;
    }
    return planned;
  },
  importScripts: () => {},
  setTimeout,
};
sandbox.globalThis = sandbox;
sandbox.chrome = {
  runtime: {
    id: EXTENSION_ID,
    getURL(resource = "") {
      return `chrome-extension://${EXTENSION_ID}/${resource}`;
    },
    onMessage: {
      addListener(listener) {
        messageListener = listener;
      },
    },
  },
  scripting: {
    async executeScript(options) {
      scriptCalls.push({
        args: Array.from(options.args || []),
        func: options.func,
        target: { ...options.target },
      });
      if (!scriptPlan.length) {
        throw new Error("Unexpected executeScript call");
      }
      const planned = scriptPlan.shift();
      if (planned instanceof Error) {
        throw planned;
      }
      return [{ frameId: 0, result: planned }];
    },
  },
  sidePanel: {
    async open(options) {
      sidePanelCalls.push({ ...options });
    },
  },
  storage: {
    local: {
      async get(keys) {
        const requested = Array.isArray(keys) ? keys : [keys];
        return Object.fromEntries(requested
          .filter((key) => Object.hasOwn(localStorageState, key))
          .map((key) => [key, structuredClone(localStorageState[key])]));
      },
      async set(values) {
        Object.assign(localStorageState, structuredClone(values));
      },
      async remove(keys) {
        for (const key of Array.isArray(keys) ? keys : [keys]) delete localStorageState[key];
      },
    },
    session: {
      async get(keys) {
        const requested = Array.isArray(keys) ? keys : [keys];
        return Object.fromEntries(
          requested
            .filter((key) => Object.hasOwn(storageState, key))
            .map((key) => [key, structuredClone(storageState[key])]),
        );
      },
      async set(values) {
        Object.assign(storageState, structuredClone(values));
      },
      async remove(keys) {
        for (const key of Array.isArray(keys) ? keys : [keys]) delete storageState[key];
      },
    },
  },
  tabs: {
    async query(query) {
      assert.deepEqual({ ...query }, { active: true, currentWindow: true });
      return [activeTab];
    },
    async get(tabId) {
      tabGetCalls.push(tabId);
      const tab = tabsById.get(tabId);
      if (!tab) throw new Error("Unknown tab");
      return { ...tab };
    },
    async create(options) {
      createdTabs.push({ ...options });
      return { id: 99, ...options };
    },
    onUpdated: {
      addListener(listener) {
        tabUpdatedListener = listener;
      },
    },
    onRemoved: {
      addListener(listener) {
        tabRemovedListener = listener;
      },
    },
  },
};

const context = vm.createContext(sandbox);
vm.runInContext(browserApiSource, context, { filename: "browser-api.js" });
vm.runInContext(extractorSource, context, { filename: "page-extractor.js" });
vm.runInContext(applicationObserverSource, context, {
  filename: "application-session-observer.js",
});
vm.runInContext(applicationFormHelperSource, context, {
  filename: "application-form-helper.js",
});
vm.runInContext(applicationAnswerHelperSource, context, {
  filename: "application-answer-helper.js",
});
vm.runInContext(workerSource, context, { filename: "service-worker.js" });
assert.equal(typeof messageListener, "function");
assert.equal(typeof tabUpdatedListener, "function");
assert.equal(typeof tabRemovedListener, "function");

function send(message, sender = { id: EXTENSION_ID }) {
  return new Promise((resolve, reject) => {
    let responded = false;
    const timeout = setTimeout(
      () => reject(new Error("Worker response timed out")),
      1000,
    );
    const sendResponse = (payload) => {
      responded = true;
      clearTimeout(timeout);
      resolve(JSON.parse(JSON.stringify(payload)));
    };
    const normalizedSender = typeof sender === "string" ? { id: sender } : sender;
    const asynchronous = messageListener(message, normalizedSender, sendResponse);
    if (asynchronous !== true && !responded) {
      clearTimeout(timeout);
      reject(new Error("Worker did not retain or answer the message channel"));
    }
  });
}

function queueSuccessfulSessionAndCapture({
  captureScope,
  contentChars,
  outcome = "created",
  readiness,
}) {
  const receipt = {
    schema_version: 3,
    status: captureScope === "page_visible" ? "no_clear_conflict" : "unavailable",
    label: captureScope === "page_visible"
      ? "No clear conflict found"
      : "Assessment unavailable",
    availability: captureScope === "page_visible" ? "available" : "partial_capture",
    scope: captureScope,
    blocker_count: 0,
    blockers: [],
    review_count: 0,
    review_items: [],
    unknown_vocabulary_count: 0,
    extra_step_count: captureScope === "page_visible" ? 2 : 0,
    contact_candidate_count: captureScope === "page_visible" ? 1 : 0,
    truncated: false,
  };
  fetchPlan.push(
    response(200, { client: "extension", token: "extension-token" }),
    response(200, {
      capture_scope: captureScope,
      content_chars: contentChars,
      contract_version: 1,
      outcome,
      readiness,
      receipt,
      review: {
        discovery_id: "discovery_0123456789abcdef0123456789abcdef",
      },
    }),
  );
  return receipt;
}

function queueApplicationClaim({
  origin = "https://jobs.example.test",
  sessionId = APPLICATION_SESSION_ID,
} = {}) {
  fetchPlan.push(
    response(200, { client: "extension", token: "extension-token" }),
    response(200, {
      bound_origin: origin,
      contract_version: 1,
      expires_at: new Date(Date.now() + (60 * 60 * 1000)).toISOString(),
      outcome: "armed",
      session_id: sessionId,
      session_token: "session-token-with-at-least-thirty-two-characters",
    }),
  );
  scriptPlan.push({ ok: true, origin });
}

function storedSession(tabId = 41) {
  return storageState[`${APPLICATION_SESSION_STORAGE_KEY_PREFIX}${tabId}`] || null;
}

function requestBody() {
  assert.equal(fetchCalls.length, 2);
  assert.equal(fetchCalls[0].url, `${APP_ORIGIN}/api/session`);
  assert.equal(fetchCalls[0].options.method, "POST");
  assert.equal(fetchCalls[0].options.credentials, "omit");
  assert.equal(fetchCalls[1].url, `${APP_ORIGIN}/api/discoveries/captures`);
  assert.equal(fetchCalls[1].options.method, "POST");
  assert.equal(
    fetchCalls[1].options.headers["X-Career-Hub-Token"],
    "extension-token",
  );
  return JSON.parse(fetchCalls[1].options.body);
}

async function run() {
  // Strict version allowlist and Unicode scalar bounds, shared by both hosts.
  reset();
  const unicodeReceipt = queueSuccessfulSessionAndCapture({
    captureScope: "page_visible", contentChars: 0, readiness: "needs_review",
  });
  unicodeReceipt.schema_version = 4;
  unicodeReceipt.label = "🚀".repeat(500);
  context.receiptUnderTest = unicodeReceipt;
  assert.equal(vm.runInContext('validReceipt(receiptUnderTest, "page_visible")', context), true);
  unicodeReceipt.label += "x";
  assert.equal(vm.runInContext('validReceipt(receiptUnderTest, "page_visible")', context), false);
  unicodeReceipt.label = "Review";
  unicodeReceipt.schema_version = 5;
  assert.equal(vm.runInContext('validReceipt(receiptUnderTest, "page_visible")', context), false);
  reset();
  assert.deepEqual(
    await send({
      command: "career-hub.capture-active-tab.v2",
      mode: "link",
      url: "https://attacker.example/replace-active-tab",
    }),
    { ok: false, error_code: "UNSUPPORTED_COMMAND" },
  );
  assert.deepEqual(
    await send(
      { command: "career-hub.capture-active-tab.v2", mode: "link" },
      "different-extension-id",
    ),
    { ok: false, error_code: "UNSUPPORTED_COMMAND" },
  );
  assert.equal(fetchCalls.length, 0);
  assert.deepEqual(
    await send({ command: "career-hub.open-discovery-review.v1" }),
    { ok: false, error_code: "REVIEW_TARGET_UNAVAILABLE" },
  );
  assert.deepEqual(
    await send({
      command: "career-hub.open-discovery-review.v1",
      url: "https://attacker.example/",
    }),
    { ok: false, error_code: "UNSUPPORTED_COMMAND" },
  );
  assert.deepEqual(
    await send({
      command: "career-hub.prepare-application.v1",
      discovery_id: "discovery_attacker_controlled",
    }),
    { ok: false, error_code: "UNSUPPORTED_COMMAND" },
  );

  assert.deepEqual(
    await send({ command: "career-hub.open-application-helper.v1" }),
    { ok: false, error_code: "UNSUPPORTED_COMMAND" },
  );

  assert.deepEqual(
    await send({
      command: "career-hub.application-helper.load.v1",
      field_id: "email",
    }),
    { ok: false, error_code: "UNSUPPORTED_COMMAND" },
  );
  const helperRevision = `sha256:${"a".repeat(64)}`;
  const helperAnswerRevision = `sha256:${"c".repeat(64)}`;
  fetchPlan.push(
    response(200, {
      client: "application_helper",
      contract_version: 2,
      token: "purpose-specific-helper-token-1234567890",
    }),
    response(200, {
      contract_version: 2,
      revision: helperRevision,
      answer_revision: helperAnswerRevision,
      answers: [],
      resumes: [],
      fields: [
        {
          id: "email",
          label: "Email",
          source: "Profile · Contact · Email",
          value_kind: "text",
          value: "person@example.com",
        },
        {
          id: "requires_future_sponsorship",
          label: "Future sponsorship needed",
          source: "Eligibility · Future sponsorship needed",
          value_kind: "boolean",
          value: true,
        },
      ],
    }),
  );
  assert.deepEqual(
    await send({ command: "career-hub.application-helper.load.v1" }),
    {
      contract_version: 2,
      answer_revision: helperAnswerRevision,
      answers: [],
      fields: [
        {
          id: "email",
          label: "Email",
          source: "Profile · Contact · Email",
          value_kind: "text",
          value: "person@example.com",
        },
        {
          id: "requires_future_sponsorship",
          label: "Future sponsorship needed",
          source: "Eligibility · Future sponsorship needed",
          value_kind: "boolean",
          value: true,
        },
      ],
      ok: true,
      revision: helperRevision,
      resumes: [],
    },
  );
  assert.equal(fetchCalls[0].url, `${APP_ORIGIN}/api/application-helper/session`);
  assert.equal(fetchCalls[0].options.method, "POST");
  assert.equal(fetchCalls[1].url, `${APP_ORIGIN}/api/application-helper/fields`);
  assert.equal(fetchCalls[1].options.method, "POST");
  assert.equal(fetchCalls[1].options.body, undefined);
  assert.equal(
    fetchCalls[1].options.headers["X-Career-Hub-Token"],
    "purpose-specific-helper-token-1234567890",
  );
  assert.equal(JSON.stringify(storageState).includes("helper-token"), false);
  assert.equal(scriptCalls.length, 0);

  reset();
  fetchPlan.push(
    response(200, {
      client: "application_helper",
      contract_version: 2,
      token: "purpose-specific-helper-token-1234567890",
    }),
    response(200, {
      contract_version: 2,
      revision: helperRevision,
      answer_revision: helperAnswerRevision,
      answers: [],
      resumes: [],
      fields: [
        {
          id: "email",
          label: "Email",
          source: "Profile · Contact · Email",
          value_kind: "text",
          value: "person@example.com",
        },
        {
          id: "requires_future_sponsorship",
          label: "Future sponsorship needed",
          source: "Eligibility · Future sponsorship needed",
          value_kind: "boolean",
          value: true,
        },
      ],
    }),
  );
  scriptPlan.push({
    ok: true,
    contract_version: 2,
    controls_examined: 4,
    mappings: [
      {
        field_id: "email",
        target_label: "Email address",
        target_kind: "email",
        empty: true,
        selectable: true,
        skip_reason: "",
        target_key: "0123456789abcdef", // gitleaks:allow -- deterministic fixture key
      },
    ],
    skipped: { ambiguous: 0, excluded: 3, non_empty: 0 },
  });
  const prefillReview = await send({
    command: "career-hub.application-helper.review-form.v1",
  });
  assert.equal(prefillReview.ok, true);
  assert.equal(prefillReview.contract_version, 2);
  assert.equal(prefillReview.review_token, "9f635457-7814-4e5d-9695-5dbf2530589e"); // gitleaks:allow -- synthetic fixture token
  assert.equal(prefillReview.page_host, "jobs.example.test");
  assert.deepEqual(prefillReview.mappings, [
    {
      mapping_id: "mapping_1",
      field_id: "email",
      field_label: "Email",
      target_label: "Email address",
      target_kind: "email",
      empty: true,
      selectable: true,
      skip_reason: "",
    },
  ]);
  assert.equal(JSON.stringify(prefillReview).includes("person@example.com"), false);
  assert.deepEqual(scriptCalls[0].target, { tabId: 41, allFrames: false });
  assert.equal(scriptCalls[0].func.name, "runCareerHubApplicationForm");
  assert.equal(scriptCalls[0].args[0], "inspect");
  assert.deepEqual(
    JSON.parse(JSON.stringify(scriptCalls[0].args[1])),
    ["email", "requires_future_sponsorship"],
  );
  assert.equal(scriptCalls[0].args[2], 240);
  assert.equal(JSON.stringify(storageState).includes("0123456789abcdef"), false);

  assert.deepEqual(
    await send({
      command: "career-hub.application-helper.fill-reviewed.v1",
      review_token: prefillReview.review_token,
      mapping_ids: ["mapping_1"],
      value: "attacker supplied",
    }),
    { ok: false, error_code: "UNSUPPORTED_COMMAND" },
  );
  fetchPlan.push(
    response(200, {
      client: "application_helper",
      contract_version: 2,
      token: "purpose-specific-helper-token-1234567890",
    }),
    response(200, {
      contract_version: 2,
      revision: helperRevision,
      answer_revision: helperAnswerRevision,
      answers: [],
      resumes: [],
      fields: [
        {
          id: "email",
          label: "Email",
          source: "Profile · Contact · Email",
          value_kind: "text",
          value: "person@example.com",
        },
        {
          id: "requires_future_sponsorship",
          label: "Future sponsorship needed",
          source: "Eligibility · Future sponsorship needed",
          value_kind: "boolean",
          value: true,
        },
      ],
    }),
  );
  scriptPlan.push({ ok: true, filled_mapping_ids: ["mapping_1"] });
  const filled = await send({
    command: "career-hub.application-helper.fill-reviewed.v1",
    review_token: prefillReview.review_token,
    mapping_ids: ["mapping_1"],
  });
  assert.deepEqual(filled, {
    ok: true,
    contract_version: 2,
    filled_count: 1,
    filled: [
      {
        mapping_id: "mapping_1",
        field_id: "email",
        field_label: "Email",
        target_label: "Email address",
      },
    ],
  });
  assert.equal(JSON.stringify(filled).includes("person@example.com"), false);
  assert.equal(scriptCalls[1].func.name, "runCareerHubApplicationForm");
  assert.deepEqual(scriptCalls[1].target, { tabId: 41, allFrames: false });
  assert.equal(scriptCalls[1].args[0], "fill");
  assert.deepEqual(JSON.parse(JSON.stringify(scriptCalls[1].args[1])), [
    {
      mapping_id: "mapping_1",
      field_id: "email",
      target_key: "0123456789abcdef", // gitleaks:allow -- deterministic fixture key
      value_kind: "text",
      value: "person@example.com",
    },
  ]);
  assert.deepEqual(
    await send({
      command: "career-hub.application-helper.fill-reviewed.v1",
      review_token: prefillReview.review_token,
      mapping_ids: ["mapping_1"],
    }),
    { ok: false, error_code: "APPLICATION_HELPER_REVIEW_EXPIRED" },
  );
  assert.equal(JSON.stringify(storageState).includes("person@example.com"), false);

  const queueEmailProjection = (revision) => fetchPlan.push(
    response(200, {
      client: "application_helper",
      contract_version: 2,
      token: "purpose-specific-helper-token-1234567890",
    }),
    response(200, {
      contract_version: 2,
      revision,
      answer_revision: helperAnswerRevision,
      answers: [],
      resumes: [],
      fields: [
        {
          id: "email",
          label: "Email",
          source: "Profile · Contact · Email",
          value_kind: "text",
          value: "person@example.com",
        },
      ],
    }),
  );
  const emailInspection = () => ({
    ok: true,
    contract_version: 2,
    controls_examined: 1,
    mappings: [
      {
        field_id: "email",
        target_label: "Email address",
        target_kind: "email",
        empty: true,
        selectable: true,
        skip_reason: "",
        target_key: "0123456789abcdef", // gitleaks:allow -- deterministic fixture key
      },
    ],
    skipped: { ambiguous: 0, excluded: 0, non_empty: 0 },
  });
  const reusableAnswerId = "answer_0123456789abcdef0123456789abcdef";
  const reusableResumeId = "resume_0123456789abcdef0123456789abcdef";
  const reusableQuestion = "Are you willing to relocate?";
  const assistantProjection = ({
    answerRevision = helperAnswerRevision,
    answers = [],
    fields = [],
    resumes = [],
    revision = helperRevision,
  } = {}) => ({
    contract_version: 2,
    revision,
    answer_revision: answerRevision,
    fields,
    resumes,
    answers,
  });
  const queueAssistantProjection = (projection) => fetchPlan.push(
    response(200, {
      client: "application_helper",
      contract_version: 2,
      token: "purpose-specific-helper-token-1234567890",
    }),
    response(200, projection),
  );

  reset();
  const applicationTab = activeTab;
  const helperTabUrl = `chrome-extension://${EXTENSION_ID}/sidepanel.html?target_tab_id=${applicationTab.id}`;
  activeTab = {
    id: 91,
    windowId: applicationTab.windowId,
    title: "Career Hub Application Helper",
    url: helperTabUrl,
  };
  tabsById.set(activeTab.id, activeTab);
  queueEmailProjection(helperRevision);
  scriptPlan.push(emailInspection());
  const fallbackTabReview = await send(
    { command: "career-hub.application-helper.review-form.v1" },
    { id: EXTENSION_ID, tab: activeTab, url: helperTabUrl },
  );
  assert.equal(fallbackTabReview.ok, true);
  assert.equal(fallbackTabReview.page_host, "jobs.example.test");
  assert.deepEqual(tabGetCalls, [applicationTab.id]);
  assert.deepEqual(scriptCalls[0].target, { tabId: applicationTab.id, allFrames: false });
  queueEmailProjection(helperRevision);
  scriptPlan.push({ ok: true, filled_mapping_ids: ["mapping_1"] });
  const fallbackTabFill = await send(
    {
      command: "career-hub.application-helper.fill-reviewed.v1",
      review_token: fallbackTabReview.review_token,
      mapping_ids: ["mapping_1"],
    },
    { id: EXTENSION_ID, tab: activeTab, url: helperTabUrl },
  );
  assert.equal(fallbackTabFill.ok, true);
  assert.deepEqual(tabGetCalls, [applicationTab.id, applicationTab.id]);
  assert.deepEqual(scriptCalls[1].target, { tabId: applicationTab.id, allFrames: false });

  reset();
  const movedApplicationTab = { ...activeTab, windowId: 8 };
  tabsById.set(movedApplicationTab.id, movedApplicationTab);
  const wrongWindowHelperUrl = `chrome-extension://${EXTENSION_ID}/sidepanel.html?target_tab_id=${movedApplicationTab.id}`;
  const wrongWindowHelperTab = {
    id: 92,
    windowId: 7,
    title: "Career Hub Application Helper",
    url: wrongWindowHelperUrl,
  };
  activeTab = wrongWindowHelperTab;
  tabsById.set(wrongWindowHelperTab.id, wrongWindowHelperTab);
  assert.deepEqual(
    await send(
      { command: "career-hub.application-helper.review-form.v1" },
      { id: EXTENSION_ID, tab: wrongWindowHelperTab, url: wrongWindowHelperUrl },
    ),
    { ok: false, error_code: "ACTIVE_TAB_UNAVAILABLE" },
  );
  assert.equal(fetchCalls.length, 0);
  assert.equal(scriptCalls.length, 0);

  reset();
  const malformedHelperUrl = `chrome-extension://${EXTENSION_ID}/sidepanel.html?target_tab_id=41&extra=1`;
  const malformedHelperTab = {
    id: 93,
    windowId: 7,
    title: "Career Hub Application Helper",
    url: malformedHelperUrl,
  };
  activeTab = malformedHelperTab;
  tabsById.set(malformedHelperTab.id, malformedHelperTab);
  assert.deepEqual(
    await send(
      { command: "career-hub.application-helper.review-form.v1" },
      { id: EXTENSION_ID, tab: malformedHelperTab, url: malformedHelperUrl },
    ),
    { ok: false, error_code: "ACTIVE_TAB_UNSUPPORTED" },
  );
  assert.equal(tabGetCalls.length, 0);
  assert.equal(fetchCalls.length, 0);
  assert.equal(scriptCalls.length, 0);

  reset();
  queueEmailProjection(helperRevision);
  scriptPlan.push(emailInspection());
  const pageChangeReview = await send({
    command: "career-hub.application-helper.review-form.v1",
  });
  activeTab.url = "https://jobs.example.test/different-application";
  assert.deepEqual(
    await send({
      command: "career-hub.application-helper.fill-reviewed.v1",
      review_token: pageChangeReview.review_token,
      mapping_ids: ["mapping_1"],
    }),
    { ok: false, error_code: "APPLICATION_HELPER_PAGE_CHANGED" },
  );
  assert.equal(fetchCalls.length, 2);
  assert.equal(scriptCalls.length, 1);

  reset();
  queueEmailProjection(helperRevision);
  scriptPlan.push(emailInspection());
  const dataChangeReview = await send({
    command: "career-hub.application-helper.review-form.v1",
  });
  queueEmailProjection(`sha256:${"b".repeat(64)}`);
  assert.deepEqual(
    await send({
      command: "career-hub.application-helper.fill-reviewed.v1",
      review_token: dataChangeReview.review_token,
      mapping_ids: ["mapping_1"],
    }),
    { ok: false, error_code: "APPLICATION_HELPER_DATA_CHANGED" },
  );
  assert.equal(fetchCalls.length, 4);
  assert.equal(scriptCalls.length, 1);

  reset();
  queueAssistantProjection(assistantProjection({
    resumes: [{ id: reusableResumeId, display_name: "General Resume" }],
  }));
  assert.deepEqual(
    await send({
      command: "career-hub.application-helper.open-resume.v1",
      resume_id: reusableResumeId,
    }),
    { ok: true },
  );
  assert.deepEqual(createdTabs, [{
    active: true,
    url: `${APP_ORIGIN}/#resume-library/${reusableResumeId}`,
  }]);
  assert.deepEqual(
    await send({
      command: "career-hub.application-helper.open-resume.v1",
      resume_id: reusableResumeId,
      url: "https://attacker.example/",
    }),
    { ok: false, error_code: "UNSUPPORTED_COMMAND" },
  );

  reset();
  const savedReusableAnswer = {
    id: reusableAnswerId,
    question: reusableQuestion,
    value_kind: "boolean",
    value: true,
  };
  queueAssistantProjection(assistantProjection({ answers: [savedReusableAnswer] }));
  scriptPlan.push({
    ok: true,
    contract_version: 2,
    controls_examined: 3,
    mappings: [{
      answer_id: reusableAnswerId,
      question: reusableQuestion,
      target_label: reusableQuestion,
      target_kind: "radio",
      empty: true,
      selectable: true,
      skip_reason: "",
      target_key: "fedcba9876543210", // gitleaks:allow -- deterministic fixture key
    }],
    skipped: { ambiguous: 0, non_empty: 0 },
  });
  const answerReview = await send({
    command: "career-hub.application-helper.review-answers.v1",
  });
  assert.equal(answerReview.ok, true);
  assert.equal(answerReview.mappings[0].mapping_id, "answer_mapping_1");
  assert.equal(JSON.stringify(answerReview).includes('"value":true'), false);
  assert.equal(scriptCalls[0].func.name, "runCareerHubReusableAnswers");
  assert.equal(scriptCalls[0].args[0], "inspect_saved");
  assert.deepEqual(JSON.parse(JSON.stringify(scriptCalls[0].args[1])), [{
    id: reusableAnswerId,
    question: reusableQuestion,
    value_kind: "boolean",
  }]);
  queueAssistantProjection(assistantProjection({ answers: [savedReusableAnswer] }));
  scriptPlan.push({
    ok: true,
    contract_version: 2,
    filled_mapping_ids: ["answer_mapping_1"],
  });
  const answerFill = await send({
    command: "career-hub.application-helper.fill-reviewed-answers.v1",
    review_token: answerReview.review_token,
    mapping_ids: ["answer_mapping_1"],
  });
  assert.deepEqual(answerFill, {
    ok: true,
    contract_version: 2,
    filled_count: 1,
    filled: [{
      mapping_id: "answer_mapping_1",
      answer_id: reusableAnswerId,
      question: reusableQuestion,
      target_label: reusableQuestion,
    }],
  });
  assert.equal(scriptCalls[1].args[0], "fill_saved");
  assert.deepEqual(JSON.parse(JSON.stringify(scriptCalls[1].args[1])), [{
    mapping_id: "answer_mapping_1",
    answer_id: reusableAnswerId,
    question: reusableQuestion,
    target_key: "fedcba9876543210", // gitleaks:allow -- deterministic fixture key
    value_kind: "boolean",
    value: true,
  }]);
  assert.equal(JSON.stringify(storageState).includes(reusableQuestion), false);

  reset();
  queueAssistantProjection(assistantProjection());
  scriptPlan.push({
    ok: true,
    contract_version: 2,
    controls_examined: 2,
    candidates: [{
      candidate_id: "candidate_1",
      question: "Are you willing to travel?",
      target_kind: "text",
      target_key: "abcdef0123456789", // gitleaks:allow -- deterministic fixture key
      value_kind: "text",
      value: "Up to 25%",
    }],
  });
  const candidateReview = await send({
    command: "career-hub.application-helper.find-reusable-answers.v1",
  });
  assert.equal(candidateReview.ok, true);
  assert.equal(candidateReview.candidates[0].value, "Up to 25%");
  assert.deepEqual(
    await send({
      command: "career-hub.application-helper.save-reviewed-answer.v1",
      review_token: candidateReview.review_token,
      candidate_id: "candidate_1",
      value: "attacker supplied",
    }),
    { ok: false, error_code: "UNSUPPORTED_COMMAND" },
  );
  scriptPlan.push({
    ok: true,
    contract_version: 2,
    candidate: {
      question: "Are you willing to travel?",
      target_key: "abcdef0123456789", // gitleaks:allow -- deterministic fixture key
      value_kind: "text",
      value: "Up to 25%",
    },
  });
  queueAssistantProjection(assistantProjection());
  const savedAnswerRevision = `sha256:${"d".repeat(64)}`;
  fetchPlan.push(
    response(200, {
      client: "application_helper",
      contract_version: 2,
      token: "purpose-specific-helper-token-1234567890",
    }),
    response(200, {
      contract_version: 2,
      revision: savedAnswerRevision,
      answers: [{
        id: reusableAnswerId,
        question: "Are you willing to travel?",
        value_kind: "text",
        value: "Up to 25%",
      }],
    }),
  );
  queueAssistantProjection(assistantProjection({
    answerRevision: savedAnswerRevision,
    answers: [{
      id: reusableAnswerId,
      question: "Are you willing to travel?",
      value_kind: "text",
      value: "Up to 25%",
    }],
    revision: `sha256:${"e".repeat(64)}`,
  }));
  const savedProjection = await send({
    command: "career-hub.application-helper.save-reviewed-answer.v1",
    review_token: candidateReview.review_token,
    candidate_id: "candidate_1",
  });
  assert.equal(savedProjection.ok, true);
  assert.equal(savedProjection.answers[0].value, "Up to 25%");
  const saveCall = fetchCalls.find((call) => (
    call.url === `${APP_ORIGIN}/api/application-helper/answers/save`
  ));
  assert.deepEqual(JSON.parse(saveCall.options.body), {
    contract_version: 2,
    expected_revision: helperAnswerRevision,
    question: "Are you willing to travel?",
    value_kind: "text",
    value: "Up to 25%",
  });
  assert.equal(JSON.stringify(storageState).includes("Up to 25%"), false);

  reset();
  queueAssistantProjection(assistantProjection({
    answerRevision: savedAnswerRevision,
    answers: [{
      id: reusableAnswerId,
      question: "Are you willing to travel?",
      value_kind: "text",
      value: "Up to 25%",
    }],
  }));
  fetchPlan.push(
    response(200, {
      client: "application_helper",
      contract_version: 2,
      token: "purpose-specific-helper-token-1234567890",
    }),
    response(200, {
      contract_version: 2,
      revision: helperAnswerRevision,
      answers: [],
    }),
  );
  queueAssistantProjection(assistantProjection({ answers: [] }));
  const deletedProjection = await send({
    command: "career-hub.application-helper.delete-answer.v1",
    answer_id: reusableAnswerId,
    answer_revision: savedAnswerRevision,
  });
  assert.equal(deletedProjection.ok, true);
  assert.deepEqual(deletedProjection.answers, []);
  const deleteCall = fetchCalls.find((call) => (
    call.url === `${APP_ORIGIN}/api/application-helper/answers/delete`
  ));
  assert.deepEqual(JSON.parse(deleteCall.options.body), {
    contract_version: 2,
    expected_revision: savedAnswerRevision,
    answer_id: reusableAnswerId,
  });

  assert.deepEqual(
    await send({ command: "career-hub.application-helper.open-settings.v1" }),
    { ok: true },
  );
  assert.deepEqual(createdTabs, [{
    active: true,
    url: `${APP_ORIGIN}/#profile/application-helper`,
  }]);

  reset();
  fetchPlan.push(response(200, {
    client: "application_helper",
    contract_version: 1,
    token: "purpose-specific-helper-token-1234567890",
  }));
  assert.deepEqual(
    await send({ command: "career-hub.application-helper.load.v1" }),
    { ok: false, error_code: "APPLICATION_HELPER_CONTRACT_UNSUPPORTED" },
  );

  reset();
  fetchPlan.push(new Error("connection refused"));
  assert.deepEqual(
    await send({ command: "career-hub.application-helper.load.v1" }),
    { ok: false, error_code: "APP_UNAVAILABLE" },
  );

  reset();
  scriptPlan.push({ selection_chars: 318, selection_too_large: false });
  assert.deepEqual(
    await send({ command: "career-hub.inspect-active-tab.v1" }),
    {
      ok: true,
      page_capture_available: true,
      selection_chars: 318,
      selection_too_large: false,
      application_session: null,
    },
  );
  assert.equal(scriptCalls.length, 1);
  assert.deepEqual(scriptCalls[0].target, { tabId: 41, allFrames: false });
  assert.equal(scriptCalls[0].func.name, "inspectCareerHubSelection");

  reset();
  let expectedReceipt = queueSuccessfulSessionAndCapture({
    captureScope: "link_only",
    contentChars: 0,
    readiness: "needs_content",
  });
  assert.deepEqual(
    await send({ command: "career-hub.capture-active-tab.v2", mode: "link" }),
    {
      capture_scope: "link_only",
      content_chars: 0,
      ok: true,
      outcome: "created",
      readiness: "needs_content",
      receipt: expectedReceipt,
      review_available: true,
    },
  );
  let body = requestBody();
  assert.equal(body.capture_kind, "link");
  assert.equal(body.destination, "feed");
  assert.equal(body.observed_url, activeTab.url);
  assert.equal(body.page_title, activeTab.title);
  assert.equal(body.source_text, "");
  assert.deepEqual(body.structured, {
    company: "",
    location: "",
    posting_id: "",
    title: "",
  });
  assert.deepEqual(body.extractor, { strategy: "link", version: 1 });
  assert.equal(Object.hasOwn(body, "raw_html"), false);
  assert.equal(scriptCalls.length, 0);
  assert.deepEqual(
    await send({ command: "career-hub.open-discovery-review.v1" }),
    { ok: true },
  );
  assert.deepEqual(createdTabs, [{
    active: true,
    url: `${APP_ORIGIN}/#job-feed/discovery_0123456789abcdef0123456789abcdef`,
  }]);
  activeTab = {
    id: 42,
    title: "Different job tab",
    url: "https://jobs.example.test/different-job",
  };
  assert.deepEqual(
    await send({ command: "career-hub.open-discovery-review.v1" }),
    { ok: false, error_code: "REVIEW_TARGET_UNAVAILABLE" },
  );

  reset();
  const selectedText = "Responsibilities\nBuild reliable systems.\nQualifications\nPython and SQL.";
  scriptPlan.push({
    ok: true,
    source_text: selectedText,
    structured: { title: "", company: "", location: "", posting_id: "" },
    extractor: { version: 1, strategy: "selection" },
  });
  expectedReceipt = queueSuccessfulSessionAndCapture({
    captureScope: "selection",
    contentChars: selectedText.length,
    readiness: "needs_review",
  });
  assert.deepEqual(
    await send({
      command: "career-hub.capture-active-tab.v2",
      mode: "selection",
    }),
    {
      capture_scope: "selection",
      content_chars: selectedText.length,
      ok: true,
      outcome: "created",
      readiness: "needs_review",
      receipt: expectedReceipt,
      review_available: true,
    },
  );
  body = requestBody();
  assert.equal(body.capture_kind, "selection");
  assert.equal(body.source_text, selectedText);
  assert.deepEqual(body.extractor, { strategy: "selection", version: 1 });
  assert.deepEqual(scriptCalls[0].target, { tabId: 41, allFrames: false });
  assert.equal(scriptCalls[0].func.name, "extractCareerHubSelection");

  reset();
  const pageText = "About the role\nBuild platform services.\nRequirements\nPython and SQL.";
  scriptPlan.push({
    ok: true,
    source_text: pageText,
    structured: {
      title: "Platform Engineer",
      company: "Example Systems",
      location: "New York, NY, US",
      posting_id: "platform-42",
    },
    extractor: { version: 2, strategy: "jobposting_jsonld" },
  });
  expectedReceipt = queueSuccessfulSessionAndCapture({
    captureScope: "page_visible",
    contentChars: pageText.length,
    readiness: "needs_review",
  });
  expectedReceipt.status = "review";
  expectedReceipt.label = "Review needed";
  expectedReceipt.review_count = 1;
  expectedReceipt.review_items = [{
    kind: "policy_review",
    line: 4,
    text: "Citizenship restriction needs review.",
    source_evidence: {
      line: 4,
      text: "Candidates must be U.S. citizens.",
    },
    candidate_fact: "Career profile compatibility is unknown.",
  }];
  assert.deepEqual(
    await send({ command: "career-hub.capture-active-tab.v2", mode: "page" }),
    {
      capture_scope: "page_visible",
      content_chars: pageText.length,
      ok: true,
      outcome: "created",
      readiness: "needs_review",
      receipt: expectedReceipt,
      review_available: true,
    },
  );
  body = requestBody();
  assert.equal(body.capture_kind, "page");
  assert.equal(body.source_text, pageText);
  assert.equal(body.structured.title, "Platform Engineer");
  assert.equal(body.structured.posting_id, "platform-42");
  assert.deepEqual(body.extractor, {
    strategy: "jobposting_jsonld",
    version: 2,
  });
  assert.equal(scriptCalls[0].func.name, "extractCareerHubVisiblePage");

  reset();
  scriptPlan.push({
    ok: true,
    source_text: pageText,
    structured: {
      title: "Platform Engineer",
      company: "Example Systems",
      location: "New York, NY, US",
      posting_id: "platform-42",
    },
    extractor: { version: 2, strategy: "jobposting_jsonld" },
  });
  queueSuccessfulSessionAndCapture({
    captureScope: "page_visible",
    contentChars: pageText.length,
    readiness: "needs_review",
  });
  assert.deepEqual(
    await send({ command: "career-hub.prepare-application.v1" }),
    {
      ok: true,
      outcome: "created",
      readiness: "needs_review",
    },
  );
  body = requestBody();
  assert.equal(body.capture_kind, "page");
  assert.deepEqual(createdTabs, [{
    active: true,
    url: `${APP_ORIGIN}/#job-feed/discovery_0123456789abcdef0123456789abcdef/prepare`,
  }]);

  reset();
  activeTab.url = "chrome://extensions";
  assert.deepEqual(
    await send({ command: "career-hub.capture-active-tab.v2", mode: "link" }),
    { ok: false, error_code: "ACTIVE_TAB_UNSUPPORTED" },
  );
  assert.equal(fetchCalls.length, 0);

  reset();
  fetchPlan.push(new Error("connection refused"));
  assert.deepEqual(
    await send({ command: "career-hub.capture-active-tab.v2", mode: "link" }),
    { ok: false, error_code: "APP_UNAVAILABLE" },
  );

  reset();
  scriptPlan.push(new Error("Cannot access contents of the page"));
  assert.deepEqual(
    await send({ command: "career-hub.inspect-active-tab.v1" }),
    {
      ok: true,
      page_capture_available: false,
      selection_chars: 0,
      selection_too_large: false,
      application_session: null,
    },
  );

  reset();
  const concurrentExpiry = new Date(Date.now() + (60 * 60 * 1000)).toISOString();
  const firstStoredSession = {
    session_id: APPLICATION_SESSION_ID,
    session_token: "session-token-with-at-least-thirty-two-characters",
    tab_id: 41,
    origin: "https://jobs.example.test",
    expires_at: concurrentExpiry,
    status: "armed",
    pending_submit: null,
  };
  const secondStoredSession = {
    ...firstStoredSession,
    session_id: "application_session_fedcba9876543210fedcba9876543210",
    tab_id: 42,
    origin: "https://second.example.test",
  };
  await Promise.all([
    context.saveApplicationSession(firstStoredSession),
    context.saveApplicationSession(secondStoredSession),
  ]);
  assert.equal(storedSession(41).session_id, firstStoredSession.session_id);
  assert.equal(storedSession(42).session_id, secondStoredSession.session_id);
  await context.removeApplicationSession(41);
  assert.equal(storedSession(41), null);
  assert.equal(storedSession(42).session_id, secondStoredSession.session_id);

  reset();
  storageState[LEGACY_APPLICATION_SESSION_STORAGE_KEY] = {
    41: structuredClone(firstStoredSession),
    42: structuredClone(secondStoredSession),
  };
  assert.equal(storedSession(41), null);
  assert.equal(
    (await context.storedApplicationSession(41)).session_id,
    firstStoredSession.session_id,
  );
  assert.equal(storedSession(41).session_id, firstStoredSession.session_id);
  assert.equal(storedSession(42).session_id, secondStoredSession.session_id);
  assert.equal(storageState[LEGACY_APPLICATION_SESSION_STORAGE_KEY], undefined);
  await context.removeApplicationSession(41);
  assert.equal(await context.storedApplicationSession(41), null);
  assert.equal(storedSession(42).session_id, secondStoredSession.session_id);

  reset();
  queueApplicationClaim();
  assert.deepEqual(
    await send({ command: "career-hub.arm-application-session.v1" }),
    {
      expires_at: storedSession().expires_at,
      ok: true,
      status: "armed",
    },
  );
  assert.equal(fetchCalls[0].url, `${APP_ORIGIN}/api/session`);
  assert.equal(
    fetchCalls[1].url,
    `${APP_ORIGIN}/api/application-sessions/claim`,
  );
  const claimBody = JSON.parse(fetchCalls[1].options.body);
  assert.deepEqual(claimBody, {
    contract_version: 1,
    current_url: activeTab.url,
    request_id: "9f635457-7814-4e5d-9695-5dbf2530589e",
    tab_id: 41,
  });
  assert.equal(storedSession().status, "armed");
  assert.equal(storedSession().origin, "https://jobs.example.test");
  assert.equal(scriptCalls[0].func.name, "installCareerHubApplicationSessionObserver");

  const submitAt = new Date().toISOString();
  const successAt = new Date(Date.parse(submitAt) + 1000).toISOString();
  const eventKey = "f061112b-688b-4ca7-a64b-45bb37e92a44"; // gitleaks:allow -- synthetic fixture token
  const finalActionKey = "1274fcd7-8cc5-44b2-b310-e376caa5f96e"; // gitleaks:allow -- synthetic fixture token
  assert.deepEqual(
    await send(
      {
        command: "career-hub.application-session.content-signal.v1",
        event_key: finalActionKey,
        observed_at: submitAt,
        page_origin: "https://jobs.example.test",
        session_id: APPLICATION_SESSION_ID,
        signal: "final_action_click",
      },
      { id: EXTENSION_ID, tab: { id: 41 } },
    ),
    { ok: true, outcome: "submit_candidate_recorded" },
  );
  assert.equal(storedSession().pending_submit.event_key, finalActionKey);
  assert.deepEqual(
    await send(
      {
        command: "career-hub.application-session.content-signal.v1",
        event_key: eventKey,
        observed_at: submitAt,
        page_origin: "https://jobs.example.test",
        session_id: APPLICATION_SESSION_ID,
        signal: "native_submit",
      },
      { id: EXTENSION_ID, tab: { id: 41 } },
    ),
    { ok: true, outcome: "submit_candidate_recorded" },
  );
  assert.equal(fetchCalls.length, 2);
  assert.equal(storedSession().pending_submit.event_key, eventKey);

  fetchPlan.push(
    response(200, { client: "extension", token: "extension-token" }),
    response(200, {
      contract_version: 1,
      outcome: "submission_detected",
      review_required: true,
    }),
  );
  assert.deepEqual(
    await send(
      {
        command: "career-hub.application-session.content-signal.v1",
        observed_at: successAt,
        page_origin: "https://jobs.example.test",
        session_id: APPLICATION_SESSION_ID,
        signal: "success_state",
        success_rule: "application_submitted",
      },
      { id: EXTENSION_ID, tab: { id: 41 } },
    ),
    {
      ok: true,
      outcome: "submission_detected",
      review_required: true,
    },
  );
  assert.equal(
    fetchCalls[3].url,
    `${APP_ORIGIN}/api/application-sessions/submission-signal`,
  );
  const signalBody = JSON.parse(fetchCalls[3].options.body);
  assert.equal(signalBody.event_key, eventKey);
  assert.equal(signalBody.success_rule, "application_submitted");
  assert.equal(Object.hasOwn(signalBody, "form_values"), false);
  assert.equal(Object.hasOwn(signalBody, "dom"), false);
  assert.equal(storedSession().status, "submission_detected");
  assert.equal(storedSession().session_token, "");

  reset();
  queueApplicationClaim();
  await send({ command: "career-hub.arm-application-session.v1" });
  const beforeUpdateFetches = fetchCalls.length;
  scriptPlan.push({ ok: true, origin: "https://jobs.example.test" });
  tabUpdatedListener(41, { status: "complete" }, {
    id: 41,
    title: "Application step 2",
    url: "https://jobs.example.test/apply/step-2",
  });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(fetchCalls.length, beforeUpdateFetches);
  assert.equal(scriptCalls.at(-1).func.name, "installCareerHubApplicationSessionObserver");
  assert.equal(storedSession().status, "armed");

  fetchPlan.push(
    response(200, { client: "extension", token: "extension-token" }),
    response(200, {
      contract_version: 1,
      outcome: "interrupted",
      review_required: false,
    }),
  );
  tabUpdatedListener(41, { status: "complete" }, {
    id: 41,
    title: "Unrelated page",
    url: "https://unrelated.example.test/home",
  });
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(
    fetchCalls.at(-1).url,
    `${APP_ORIGIN}/api/application-sessions/interrupt`,
  );
  assert.equal(JSON.parse(fetchCalls.at(-1).options.body).reason, "origin_changed");
  assert.equal(storedSession(), null);

  // ADR SEC-250 item 4: pairing, credential header and revocation.
  reset();
  queueApplicationClaim();
  await send({ command: "career-hub.arm-application-session.v1" });
  assert.equal(
    fetchCalls[0].options.headers["X-Career-Hub-Extension-Credential"],
    TEST_CREDENTIAL,
  );

  reset();
  delete localStorageState[CREDENTIAL_STORAGE_KEY];
  assert.deepEqual(
    await send({ command: "career-hub.arm-application-session.v1" }),
    { ok: false, error_code: "EXTENSION_PAIRING_REQUIRED" },
  );
  assert.equal(fetchCalls.length, 0, "an unpaired worker must not contact the app");
  assert.deepEqual(
    await send({ command: "career-hub.extension.pairing-status.v1" }, PANEL_SENDER),
    { ok: true, paired: false },
  );
  fetchPlan.push(response(200, {
    contract_version: 1, client: "extension", credential: TEST_CREDENTIAL,
  }));
  assert.deepEqual(
    await send({ command: "career-hub.extension.pair.v1", code: "abcd-efgh" }, PANEL_SENDER),
    { ok: true, paired: true },
  );
  assert.equal(fetchCalls[0].url, `${APP_ORIGIN}/api/extension/pair`);
  assert.deepEqual(JSON.parse(fetchCalls[0].options.body), { code: "abcd-efgh" });
  assert.equal(localStorageState[CREDENTIAL_STORAGE_KEY], TEST_CREDENTIAL);
  assert.equal(storageState[CREDENTIAL_STORAGE_KEY], undefined, "never in session storage");

  reset();
  delete localStorageState[CREDENTIAL_STORAGE_KEY];
  fetchPlan.push(response(200, {
    contract_version: 1, client: "extension", credential: TEST_CREDENTIAL, token: "extra",
  }));
  assert.deepEqual(
    await send({ command: "career-hub.extension.pair.v1", code: "ABCDEFGH" }, PANEL_SENDER),
    { ok: false, error_code: "APP_RESPONSE_INVALID" },
  );
  assert.equal(localStorageState[CREDENTIAL_STORAGE_KEY], undefined);
  fetchPlan.push(response(403, { error_code: "EXTENSION_PAIRING_CODE_INVALID" }));
  assert.deepEqual(
    await send({ command: "career-hub.extension.pair.v1", code: "ABCD-EFGH" }, PANEL_SENDER),
    { ok: false, error_code: "EXTENSION_PAIRING_CODE_INVALID" },
  );
  for (const [message, sender] of [
    [{ command: "career-hub.extension.pair.v1", code: "ABCD-EFGH" }, PAGE_SENDER],
    [{ command: "career-hub.extension.pairing-status.v1" }, PAGE_SENDER],
    [{ command: "career-hub.extension.pair.v1", code: "ABCD-EFGH" }, { id: EXTENSION_ID }],
    [{ command: "career-hub.extension.pair.v1", code: "ABCD-EFGH" },
      { id: EXTENSION_ID, url: "chrome-extension://another-extension-id/sidepanel.html" }],
    [{ command: "career-hub.extension.pair.v1", code: "not a code" }, PANEL_SENDER],
    [{ command: "career-hub.extension.pair.v1", code: "ABCD-EFGH", extra: 1 }, PANEL_SENDER],
  ]) {
    assert.deepEqual(await send(message, sender), { ok: false, error_code: "UNSUPPORTED_COMMAND" });
  }
  assert.equal(fetchCalls.length, 2);

  // Revoked in Career Hub: the worker forgets the credential.
  reset();
  fetchPlan.push(response(403, { error_code: "EXTENSION_PAIRING_REQUIRED" }));
  assert.deepEqual(
    await send({ command: "career-hub.arm-application-session.v1" }),
    { ok: false, error_code: "EXTENSION_PAIRING_REQUIRED" },
  );
  assert.equal(localStorageState[CREDENTIAL_STORAGE_KEY], undefined);

  console.log("worker protocol harness: OK");
}

run().catch((error) => {
  console.error(error.stack || error);
  process.exitCode = 1;
});
