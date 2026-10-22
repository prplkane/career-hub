/**
 * Local API boundary for the browser UI.
 *
 * All fetch calls go through this client so transport failures and structured
 * local API errors have one predictable shape for feature modules.
 */

export class CareerHubApiError extends Error {
  constructor(message, { status = 0, payload = null } = {}) {
    super(message);
    this.name = "CareerHubApiError";
    this.status = status;
    this.payload = payload || {};
  }
}

function jobPath(jobId, suffix = "") {
  return `/api/jobs/${encodeURIComponent(jobId)}${suffix}`;
}

export class CareerHubApiClient {
  constructor() {
    this.sessionPromise = null;
    this.capabilities = {};
  }

  async sessionToken(signal = AbortSignal.timeout(30_000)) {
    if (!this.sessionPromise) {
      // The token comes only from the Electron host through the preload (ADR
      // SEC-250); no HTTP route returns it. /api/session supplies capabilities.
      const desktopSession = typeof window === "undefined" ? undefined : window.careerHubDesktop?.session;
      const pending = desktopSession?.token
        ? this.desktopSession(desktopSession, signal)
        : Promise.reject(new CareerHubApiError("Open Career Hub from the desktop app.", { status: 0, payload: {} }));
      this.sessionPromise = pending.catch((error) => {
        this.sessionPromise = null;
        this.capabilities = {};
        throw error;
      });
    }
    return this.sessionPromise;
  }

  async desktopSession(desktopSession, signal) {
    const token = await desktopSession.token();
    if (typeof token !== "string" || token.length === 0) {
      throw new CareerHubApiError("Local API session could not be established", { status: 0, payload: {} });
    }
    const response = await fetch("/api/session", {
      cache: "no-store", signal, headers: { "X-Career-Hub-Token": token },
    });
    const payload = await response.json();
    if (!response.ok) {
      throw new CareerHubApiError(
        payload?.error || "Local API session could not be established",
        { status: response.status, payload }
      );
    }
    this.capabilities = payload.capabilities || {};
    return token;
  }


  async authorizedFetch(path, options = {}, allowRefresh = true) {
    const headers = new Headers(options.headers || {});
    headers.set("X-Career-Hub-Token", await this.sessionToken(options.signal));
    const response = await fetch(path, { ...options, headers, cache: "no-store" });
    if (allowRefresh && response.status === 403) {
      let details = {};
      try { details = await response.clone().json(); } catch { /* not a structured boundary response */ }
      if (details?.error_code === "LOCAL_API_ACCESS_DENIED") {
        this.sessionPromise = null;
        return this.authorizedFetch(path, options, false);
      }
    }
    return response;
  }

  async request(path, options = {}) {
    const response = await this.authorizedFetch(path, options);
    const contentType = response.headers.get("content-type") || "";
    let payload;
    try {
      payload = contentType.includes("application/json")
        ? await response.json()
        : await response.text();
    } catch {
      payload = null;
    }
    if (!response.ok) {
      const details = payload && typeof payload === "object" ? payload : {};
      throw new CareerHubApiError(
        details.error || `Request failed: ${response.status}`,
        { status: response.status, payload: details }
      );
    }
    return payload;
  }

  async raw(path, options = {}) {
    const response = await this.authorizedFetch(path, options);
    if (!response.ok) {
      let payload = {};
      try { payload = await response.json(); } catch { /* structured errors are best-effort */ }
      throw new CareerHubApiError(payload.error || `Request failed: ${response.status}`, { status: response.status, payload });
    }
    return response;
  }

  json(path, method, body, options = {}) {
    return this.request(path, {
      ...options,
      method,
      headers: { "Content-Type": "application/json", ...(options.headers || {}) },
      body: JSON.stringify(body),
    });
  }

  collection(path, { cursor = null, limit = 20 } = {}) {
    const query = new URLSearchParams({ limit: String(limit) });
    if (cursor) query.set("cursor", cursor);
    return this.request(`${path}?${query}`);
  }

  getJobs(options = {}) {
    return this.collection("/api/jobs", options);
  }

  getSources(options = {}) {
    return this.collection("/api/sources", options);
  }

  getExtractions(options = {}) {
    return this.collection("/api/extractions", options);
  }

  getVaultSnapshot() {
    return this.request("/api/vault/snapshot");
  }

  getRuntimeInfo() {
    return this.request("/api/runtime");
  }

  getVaultEditor(collection) {
    return this.request(`/api/vault/${encodeURIComponent(collection)}`);
  }

  getHardBlockerPolicy() {
    return this.request("/api/hard-blocker-policy");
  }

  updateHardBlockerPolicy(classes, expectedRevision) {
    return this.json("/api/hard-blocker-policy", "PUT", {
      contract_version: 1,
      expected_revision: expectedRevision,
      classes,
    });
  }

  getApplicationHelperConfiguration() {
    return this.request("/api/application-helper/configuration");
  }

  updateApplicationHelperConfiguration(enabledFields, expectedRevision) {
    return this.json("/api/application-helper/configuration", "PUT", {
      contract_version: 2,
      expected_revision: expectedRevision,
      enabled_fields: enabledFields,
    });
  }

  getApplicationDemographics() {
    return this.request("/api/application-demographics");
  }

  updateApplicationDemographics(answers, expectedRevision) {
    return this.json("/api/application-demographics", "PUT", {
      contract_version: 1,
      expected_revision: expectedRevision,
      section: "US",
      answers,
    });
  }

  updateEligibilityProfile(record, expectedRevision) {
    return this.json("/api/vault/eligibility", "PUT", {
      expected_revision: expectedRevision,
      record,
    });
  }

  getSkillsRegistry() {
    return this.request("/api/skills-registry");
  }

  getCompetencyExplorer({ includeArchived = false } = {}) {
    if (!includeArchived) return this.request("/api/competency-explorer");
    return this.request(
      "/api/competency-explorer?include_archived_competencies=true"
    );
  }

  getDemandGaps({ startDate = "", endDate = "", asOf = "" } = {}) {
    const query = new URLSearchParams();
    if (startDate) query.set("from", startDate);
    if (endDate) query.set("to", endDate);
    if (asOf) query.set("as_of", asOf);
    const suffix = query.size ? `?${query.toString()}` : "";
    return this.request(`/api/demand-gaps${suffix}`);
  }

  getDiscoveries({
    states = [],
    sourceKinds = [],
    readiness = [],
    queues = [],
    search = "",
    observedAfter = "",
    sort = "last_desc",
    cursor = null,
    limit = 20,
  } = {}) {
    const query = new URLSearchParams({ limit: String(limit) });
    if (states.length) query.set("state", states.join(","));
    if (sourceKinds.length) query.set("source_kind", sourceKinds.join(","));
    if (readiness.length) query.set("readiness", readiness.join(","));
    if (queues.length) query.set("queue", queues.join(","));
    if (search) query.set("q", search);
    if (observedAfter) query.set("observed_after", observedAfter);
    if (sort) query.set("sort", sort);
    if (cursor) query.set("cursor", cursor);
    return this.request(`/api/discoveries?${query.toString()}`);
  }

  getDiscovery(discoveryId) {
    return this.request(`/api/discoveries/${encodeURIComponent(discoveryId)}`);
  }

  setDiscoveryState(discoveryId, state, expectedRevision) {
    return this.json(`/api/discoveries/${encodeURIComponent(discoveryId)}`, "PUT", {
      state,
      expected_revision: expectedRevision,
    });
  }

  deleteDiscovery(discoveryId, expectedRevision = null) {
    const path = `/api/discoveries/${encodeURIComponent(discoveryId)}`;
    const options = { method: "DELETE" };
    if (typeof expectedRevision === "number") {
      options.headers = { "Content-Type": "application/json" };
      options.body = JSON.stringify({ expected_revision: expectedRevision });
    }
    return this.request(path, options);
  }

  flushArchivedDiscoveries() {
    return this.json("/api/discoveries/flush-archived", "POST", {});
  }

  setDiscoveryFieldOverrides(discoveryId, overrides, expectedOverrideRevision) {
    return this.json(
      `/api/discoveries/${encodeURIComponent(discoveryId)}/field-overrides`,
      "PUT",
      {
        contract_version: 1,
        expected_override_revision: expectedOverrideRevision,
        overrides,
      },
    );
  }

  confirmDiscoveryContent(discoveryId, expectedRevision) {
    return this.json(
      `/api/discoveries/${encodeURIComponent(discoveryId)}/confirm-content`,
      "POST",
      {
        contract_version: 1,
        expected_revision: expectedRevision,
      },
    );
  }

  resolvePublicJobLink(discoveryId, expectedRevision) {
    return this.json(
      `/api/discoveries/${encodeURIComponent(discoveryId)}/resolve-public-link`,
      "POST",
      {
        contract_version: 1,
        expected_revision: expectedRevision,
      },
    );
  }

  promoteDiscovery(discoveryId, action, expectedRevision) {
    return this.json(
      `/api/discoveries/${encodeURIComponent(discoveryId)}/promote`,
      "POST",
      {
        contract_version: 1,
        action,
        expected_revision: expectedRevision,
      },
    );
  }

  importEmailFiles(files) {
    return this.json("/api/discoveries/email-imports", "POST", {
      contract_version: 1,
      files,
    });
  }

  getEmailConnection() {
    return this.request("/api/email-connection");
  }

  async hasEmailConnection() {
    await this.sessionToken();
    // Older Python sessions did not advertise this capability.
    return this.capabilities.email_connection !== false;
  }

  getApplicationEmailUpdates() {
    return this.getApplicationEmailUpdatesPage();
  }

  getApplicationEmailUpdatesPage({ cursor = null, limit = 10 } = {}) {
    const query = new URLSearchParams({ limit: String(limit) });
    if (cursor) query.set("cursor", cursor);
    return this.request(`/api/application-email-updates?${query.toString()}`);
  }

  linkApplicationEmailUpdate(outcomeId, jobId, expectedRevision) {
    return this.json("/api/application-email-updates/link", "POST", {
      contract_version: 1,
      outcome_id: outcomeId,
      job_id: jobId,
      expected_revision: expectedRevision,
    });
  }

  getApplicationSessionReviews() {
    return this.request("/api/application-sessions/reviews");
  }

  getContacts({ includeArchived = false } = {}) {
    const query = includeArchived ? "?include_archived=true" : "";
    return this.request(`/api/contacts${query}`);
  }

  getContact(contactId) {
    return this.request(`/api/contacts/${encodeURIComponent(contactId)}`);
  }

  previewContactCandidate(source) {
    return this.json("/api/contacts/candidate-preview", "POST", {
      contract_version: 1,
      source_kind: source.sourceKind,
      source_id: source.sourceId,
      candidate_id: source.candidateId,
      expected_content_sha256: source.contentSha256,
    });
  }

  confirmContactCandidateCreate(source, contact) {
    return this.json("/api/contacts/confirm-candidate", "POST", {
      contract_version: 1,
      source_kind: source.sourceKind,
      source_id: source.sourceId,
      candidate_id: source.candidateId,
      expected_content_sha256: source.contentSha256,
      action: "create",
      contact,
    });
  }

  confirmContactCandidateLink(source, contactId, expectedRevision) {
    return this.json("/api/contacts/confirm-candidate", "POST", {
      contract_version: 1,
      source_kind: source.sourceKind,
      source_id: source.sourceId,
      candidate_id: source.candidateId,
      expected_content_sha256: source.contentSha256,
      action: "link_existing",
      contact_id: contactId,
      expected_revision: expectedRevision,
    });
  }

  updateContact(contactId, expectedRevision, updates) {
    return this.json(`/api/contacts/${encodeURIComponent(contactId)}`, "PUT", {
      contract_version: 1,
      expected_revision: expectedRevision,
      updates,
    });
  }

  setContactArchived(contactId, archived, expectedRevision) {
    const action = archived ? "archive" : "restore";
    return this.json(
      `/api/contacts/${encodeURIComponent(contactId)}/${action}`,
      "POST",
      { contract_version: 1, expected_revision: expectedRevision },
    );
  }

  unlinkContact(contactId, expectedRevision, { jobIds = [], discoveryIds = [] } = {}) {
    return this.json(`/api/contacts/${encodeURIComponent(contactId)}/unlink`, "POST", {
      contract_version: 1,
      expected_revision: expectedRevision,
      job_ids: jobIds,
      discovery_ids: discoveryIds,
    });
  }

  getApplicationSession(jobId) {
    return this.request(jobPath(jobId, "/application-session"));
  }

  startApplicationSession(jobId) {
    return this.json(jobPath(jobId, "/application-session"), "POST", {
      contract_version: 1,
    });
  }

  resolveApplicationSession(sessionId, action, expectedRevision) {
    if (!["confirm", "dismiss", "cancel"].includes(action)) {
      throw new CareerHubApiError("Application-session action is invalid");
    }
    return this.json(
      `/api/application-sessions/${encodeURIComponent(sessionId)}/${action}`,
      "POST",
      {
        contract_version: 1,
        expected_revision: expectedRevision,
      },
    );
  }

  configureEmailConnection({
    accountAddress,
    appPassword,
    senderAddresses,
    applicationSenderAddresses,
    scheduleEnabled,
    intervalMinutes,
    expectedRevision,
    reconnect = false,
  }, options = {}) {
    // Reconnect is an explicit route, never an implicit retry of ordinary save.
    return this.json(reconnect ? "/api/email-connection/reconnect" : "/api/email-connection", reconnect ? "POST" : "PUT", {
      contract_version: 2,
      account_address: accountAddress,
      app_password: appPassword || null,
      sender_addresses: senderAddresses,
      application_sender_addresses: applicationSenderAddresses,
      schedule_enabled: scheduleEnabled,
      interval_minutes: intervalMinutes,
      expected_revision: expectedRevision,
    }, options);
  }

  syncEmailConnection(trigger = "manual", options = {}) {
    return this.json("/api/email-connection/sync", "POST", {
      contract_version: 2,
      trigger,
    }, options);
  }

  disconnectEmailConnection(expectedRevision) {
    return this.json("/api/email-connection/disconnect", "POST", {
      contract_version: 2,
      expected_revision: expectedRevision,
    });
  }

  forgetEmailConnection(expectedRevision) {
    return this.json("/api/email-connection/forget", "POST", {
      contract_version: 2,
      expected_revision: expectedRevision,
    });
  }

  resetEmailCursor(expectedRevision) {
    return this.json("/api/email-connection/reset-cursor", "POST", {
      contract_version: 2,
      expected_revision: expectedRevision,
    });
  }

  setEmailSender(senderAddress, disposition, purpose, expectedRevision) {
    return this.json("/api/email-connection/senders", "POST", {
      contract_version: 2,
      sender_address: senderAddress,
      disposition,
      purpose,
      expected_revision: expectedRevision,
    });
  }

  listDocuments() {
    return this.request("/api/documents?include_archived=true");
  }

  createDocument(file, metadata, expectedRevision) {
    const headers = {
      "Content-Type": file.type,
      "X-Document-Name": encodeURIComponent(file.name),
      "X-Document-Revision": expectedRevision,
    };
    Object.entries(metadata).forEach(([key, value]) => {
      const header = `X-Document-${key.split("_").map((part) => part[0].toUpperCase() + part.slice(1)).join("-")}`;
      headers[header] = encodeURIComponent(value || "");
    });
    return this.request("/api/documents", { method: "POST", headers, body: file });
  }

  updateDocument(documentId, metadata, expectedRevision) {
    return this.json(`/api/documents/${encodeURIComponent(documentId)}`, "PUT", { expected_revision: expectedRevision, record: metadata });
  }

  setDocumentArchived(documentId, archived, expectedRevision) {
    return this.json(`/api/documents/${encodeURIComponent(documentId)}/${archived ? "archive" : "restore"}`, "POST", { expected_revision: expectedRevision });
  }

  documentFile(documentId) {
    return this.raw(`/api/documents/${encodeURIComponent(documentId)}/file`);
  }

  createSkillGroup(expectedRevision, record) {
    return this.json("/api/skills-registry/groups", "POST", {
      expected_revision: expectedRevision,
      record,
    });
  }

  createRegistrySkill(expectedRevision, record) {
    return this.json("/api/skills-registry/skills", "POST", {
      expected_revision: expectedRevision,
      record,
    });
  }

  createRegistrySkills(expectedRevision, record) {
    return this.json("/api/skills-registry/skills/batch", "POST", {
      expected_revision: expectedRevision,
      record,
    });
  }

  updateSkillGroup(groupId, expectedRevision, record) {
    return this.json(`/api/skills-registry/groups/${encodeURIComponent(groupId)}`, "PUT", {
      expected_revision: expectedRevision,
      record,
    });
  }

  setSkillGroupArchived(groupId, archived, expectedRevision) {
    const action = archived ? "archive" : "restore";
    return this.json(
      `/api/skills-registry/groups/${encodeURIComponent(groupId)}/${action}`,
      "POST",
      { expected_revision: expectedRevision },
    );
  }

  updateRegistrySkill(skillId, expectedRevision, record) {
    return this.json(`/api/skills-registry/skills/${encodeURIComponent(skillId)}`, "PUT", {
      expected_revision: expectedRevision,
      record,
    });
  }

  setRegistrySkillArchived(skillId, archived, expectedRevision) {
    const action = archived ? "archive" : "restore";
    return this.json(
      `/api/skills-registry/skills/${encodeURIComponent(skillId)}/${action}`,
      "POST",
      { expected_revision: expectedRevision },
    );
  }

  getJobAssessment(jobId) {
    return this.request(jobPath(jobId, "/assessment"));
  }

  getJobSource(jobId) {
    return this.request(jobPath(jobId, "/source"));
  }

  getApplicationInstructions(jobId) {
    return this.request(jobPath(jobId, "/application-instructions"));
  }

  addManualApplicationInstruction(jobId, expectedSourceFingerprint, category, selectedText) {
    return this.json(jobPath(jobId, "/application-instructions/manual"), "POST", {
      contract_version: 1,
      expected_source_fingerprint: expectedSourceFingerprint,
      category,
      selected_text: selectedText,
    });
  }

  setApplicationInstructionState(jobId, expectedSourceFingerprint, instructionFingerprint, state) {
    return this.json(jobPath(jobId, "/application-instructions/action"), "POST", {
      contract_version: 1,
      expected_source_fingerprint: expectedSourceFingerprint,
      instruction_fingerprint: instructionFingerprint,
      state,
    });
  }

  getJob(jobId) {
    return this.request(jobPath(jobId));
  }

  async saveJobDetails(jobId, fields, expectedCardRevision) {
    await this.sessionToken();
    const payload = { fields };
    if (this.capabilities.job_details_revision === true) {
      if (!/^sha256:[0-9a-f]{64}$/.test(expectedCardRevision || "")) {
        throw new CareerHubApiError("Saved job revision unavailable. Reload the job before editing it.");
      }
      payload.expected_card_revision = expectedCardRevision;
    }
    return this.json(jobPath(jobId, "/details"), "PUT", payload);
  }

  async reanalyzeJob(jobId, expectedCardRevision) {
    await this.sessionToken();
    if (this.capabilities.job_reanalysis_revision === true) {
      if (!/^sha256:[0-9a-f]{64}$/.test(expectedCardRevision || "")) {
        throw new CareerHubApiError("Saved job revision unavailable. Reload the job before refreshing analysis.");
      }
      return this.json(jobPath(jobId, "/reanalyze"), "POST", { expected_card_revision: expectedCardRevision });
    }
    // The installed Python handler retains its existing bodyless request.
    return this.request(jobPath(jobId, "/reanalyze"), { method: "POST" });
  }

  createJob(payload) {
    return this.requestWithTransientRetry(
      "/api/jobs",
      "POST",
      payload,
      { requiresIdempotencyKey: true },
    );
  }

  async requestWithTransientRetry(path, method, body, { requiresIdempotencyKey = false } = {}) {
    if (requiresIdempotencyKey && !body?.idempotency_key) {
      throw new CareerHubApiError("A safe retry requires an idempotency key");
    }
    try {
      return await this.json(path, method, body);
    } catch (error) {
      // A response can be lost after the local server has persisted the job.
      // Retrying the exact request is safe only because job intake is idempotent.
      if (!(error instanceof TypeError) || !/failed to fetch/i.test(error.message)) {
        throw error;
      }
      await new Promise((resolve) => setTimeout(resolve, 250));
      return this.json(path, method, body);
    }
  }

  getApplication(jobId) {
    return this.request(jobPath(jobId, "/application"));
  }

  setApplicationDecision(jobId, decision) {
    return this.json(jobPath(jobId, "/application/decision"), "PUT", { decision });
  }

  startApplication(jobId, occurredAt) {
    return this.json(jobPath(jobId, "/application/start"), "POST", {
      occurred_at: occurredAt,
    });
  }

  saveApplicationStatus(jobId, { status, occurredAt, note }) {
    return this.json(jobPath(jobId, "/application/status"), "POST", {
      status,
      occurred_at: occurredAt,
      note,
    });
  }

  attachApplicationResume(jobId, file, label) {
    return this.request(jobPath(jobId, "/application/resume"), {
      method: "POST",
      headers: {
        "Content-Type": "application/octet-stream",
        "X-Resume-Name": encodeURIComponent(file.name),
        "X-Resume-Label": encodeURIComponent(label),
      },
      body: file,
    });
  }

  listResumeVersions({ search = "", includeArchived = false } = {}) {
    const query = new URLSearchParams();
    if (search) query.set("search", search);
    if (includeArchived) query.set("include_archived", "true");
    const suffix = query.size ? `?${query.toString()}` : "";
    return this.request(`/api/resumes${suffix}`);
  }

  uploadResumeVersion(file, label) {
    return this.request("/api/resumes", {
      method: "POST",
      headers: {
        "Content-Type": "application/octet-stream",
        "X-Resume-Name": encodeURIComponent(file.name),
        "X-Resume-Label": encodeURIComponent(label),
      },
      body: file,
    });
  }

  async downloadResumeVersion(resumeId) {
    const response = await this.raw(
      `/api/resumes/${encodeURIComponent(resumeId)}/file`
    );
    const disposition = response.headers.get("content-disposition") || "";
    const encodedName = disposition.match(/filename\*=UTF-8''([^;]+)/i)?.[1];
    return {
      blob: await response.blob(),
      filename: encodedName
        ? decodeURIComponent(encodedName)
        : "resume",
    };
  }

  submittedResumeFile(jobId) {
    return this.raw(jobPath(jobId, "/application/submitted-resume/file"));
  }

  getResumePlan(jobId) {
    return this.request(jobPath(jobId, "/resume-plan"));
  }

  saveResumePlan(jobId, { action, expectedRevision, plan }) {
    return this.json(jobPath(jobId, "/resume-plan"), "POST", {
      action,
      expected_revision: expectedRevision,
      plan,
    });
  }

  getCoverLetter(jobId) {
    return this.request(jobPath(jobId, "/materials/cover-letter"));
  }

  async hasMaterialIntegrity() {
    await this.sessionToken();
    return this.capabilities.material_integrity === 1;
  }

  materialPath(jobId, kind, suffix = "") {
    if (!["resume", "cover_letter"].includes(kind)) {
      throw new CareerHubApiError("Material kind is invalid");
    }
    return jobPath(jobId, `/materials/${kind.replaceAll("_", "-")}${suffix}`);
  }

  getMaterialReview(jobId, kind, options = {}) {
    return this.request(this.materialPath(jobId, kind, "/reviews"), options);
  }

  updateMaterialReview(jobId, kind, { operation, expectedRevision, reviewSha256, targetIds }, options = {}) {
    if (!["confirm", "revoke"].includes(operation)) {
      throw new CareerHubApiError("Material review action is invalid");
    }
    return this.json(this.materialPath(jobId, kind, "/reviews"), "POST", {
      operation, expected_revision: expectedRevision,
      review_sha256: reviewSha256, target_ids: targetIds,
    }, options);
  }

  async downloadVerifiedMaterial(jobId, kind, format) {
    const metadata = await this.json(this.materialPath(jobId, kind, "/exports"), "POST", { format }, { signal: AbortSignal.timeout(30_000) });
    if (!/^material_[a-f0-9]{64}$/.test(metadata?.artifact_id || "") || metadata?.provenance !== "versioned"
      || metadata.job_id !== jobId || metadata.kind !== kind || metadata.format !== format
      || typeof metadata.filename !== "string" || !/^[^\\/\x00-\x1f]{1,255}$/u.test(metadata.filename) || !metadata.filename.endsWith(`.${format}`)
      || !Number.isSafeInteger(metadata.size_bytes) || metadata.size_bytes <= 0 || !/^[a-f0-9]{64}$/.test(metadata.sha256 || "")) {
      throw new CareerHubApiError("The export did not return a verified artifact identity");
    }
    const response = await this.raw(`${this.materialPath(jobId, kind, "/file")}?artifact_id=${metadata.artifact_id}`, { signal: AbortSignal.timeout(30_000) });
    if (response.headers.get("x-career-hub-artifact-id") !== metadata.artifact_id || response.headers.get("x-career-hub-provenance") !== "versioned") {
      throw new CareerHubApiError("The downloaded artifact identity could not be verified");
    }
    const blob = await response.blob();
    const digest = Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", await blob.arrayBuffer())), (byte) => byte.toString(16).padStart(2, "0")).join("");
    if (blob.size !== metadata.size_bytes || digest !== metadata.sha256) {
      throw new CareerHubApiError("The downloaded bytes did not match the verified export");
    }
    return { blob, filename: metadata.filename, provenance: "versioned" };
  }

  saveCoverLetter(jobId, { action = "save_draft", expectedRevision, draft }) {
    return this.json(jobPath(jobId, "/materials/cover-letter"), "POST", {
      action,
      expected_revision: expectedRevision,
      draft,
    });
  }

  async downloadCoverLetter(jobId, format) {
    if (await this.hasMaterialIntegrity()) return this.downloadVerifiedMaterial(jobId, "cover_letter", format);
    const suffix = jobPath(jobId, "/materials/cover-letter/file");
    const response = await this.raw(
      `${suffix}?format=${encodeURIComponent(format)}`
    );
    const disposition = response.headers.get("content-disposition") || "";
    const encodedName = disposition.match(/filename\*=UTF-8''([^;]+)/i)?.[1];
    return {
      blob: await response.blob(),
      filename: encodedName
        ? decodeURIComponent(encodedName)
        : `cover_letter.${format}`,
    };
  }

  getResumeMaterial(jobId) {
    return this.request(jobPath(jobId, "/materials/resume"));
  }

  saveResumeMaterial(jobId, { action = "save_draft", expectedRevision, draft }) {
    return this.json(jobPath(jobId, "/materials/resume"), "POST", {
      action,
      expected_revision: expectedRevision,
      draft,
    });
  }

  async downloadResumeMaterial(jobId, format) {
    if (await this.hasMaterialIntegrity()) return this.downloadVerifiedMaterial(jobId, "resume", format);
    const suffix = jobPath(jobId, "/materials/resume/file");
    const response = await this.raw(
      `${suffix}?format=${encodeURIComponent(format)}`
    );
    const disposition = response.headers.get("content-disposition") || "";
    const encodedName = disposition.match(/filename\*=UTF-8''([^;]+)/i)?.[1];
    return {
      blob: await response.blob(),
      filename: encodedName
        ? decodeURIComponent(encodedName)
        : `resume.${format}`,
    };
  }

  // ADR SEC-250 item 4: only this window's token can manage extension pairing.
  getExtensionPairing() {
    return this.request("/api/extension/pairing");
  }

  createExtensionPairingCode() {
    return this.request("/api/extension/pairing-code", { method: "POST" });
  }

  revokeExtensionPairing() {
    return this.request("/api/extension/pairing", { method: "DELETE" });
  }

  getLogs(cursor) {
    return this.request(`/api/logs?after=${encodeURIComponent(cursor)}`);
  }
}

export const api = new CareerHubApiClient();
