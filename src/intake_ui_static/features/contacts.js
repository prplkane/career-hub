import { api } from "../ui/api.js";
import {
  createToastManager,
  escapeHtml,
  formatRequestError,
  formatTime,
  renderLoadingState,
} from "../ui/primitives.js";

const CHANNEL_LABELS = {
  email: "Email",
  phone: "Phone",
  professional_profile: "Professional profile",
};

const state = {
  contacts: [],
  selectedId: null,
  visibility: "active",
  loading: false,
  editing: false,
  initialized: false,
  confirmationEpoch: 0,
  confirmationSource: null,
  confirmationPreview: null,
  confirmationSelection: "",
  confirmationChannels: [],
  confirmationSaving: false,
  confirmationTrigger: null,
};

const el = {};
let toast = null;

function selectedContact() {
  return state.contacts.find((contact) => contact.id === state.selectedId) || null;
}

function contactName(contact) {
  return contact?.name || contact?.channels?.[0]?.value || "Unnamed contact";
}

function initials(contact) {
  const words = contactName(contact).split(/\s+/).filter(Boolean);
  return words.slice(0, 2).map((word) => word[0]?.toLocaleUpperCase() || "").join("") || "?";
}

function visibleContacts() {
  const archived = state.visibility === "archived";
  const query = el.search.value.trim().toLocaleLowerCase();
  return state.contacts.filter((contact) => {
    if ((contact.archived === true) !== archived) return false;
    if (!query) return true;
    const searchable = [
      contact.name,
      contact.role,
      contact.company,
      contact.context,
      ...(contact.channels || []).flatMap((channel) => [channel.kind, channel.value]),
    ].join(" ").toLocaleLowerCase();
    return searchable.includes(query);
  });
}

function channelLink(channel) {
  const value = String(channel.value || "");
  if (channel.kind === "email") return `mailto:${encodeURIComponent(value)}`;
  if (channel.kind === "phone") return `tel:${encodeURIComponent(value)}`;
  return value;
}

function renderChannels(contact) {
  const channels = Array.isArray(contact.channels) ? contact.channels : [];
  if (!channels.length) {
    return '<div class="contact-detail-empty">No direct channel was saved.</div>';
  }
  return `<div class="contact-detail-channels">${channels.map((channel) => {
    const external = channel.kind === "professional_profile"
      ? ' target="_blank" rel="noopener noreferrer" data-network-action="external-link"'
      : "";
    return `<a href="${escapeHtml(channelLink(channel))}"${external}>
      <span>${escapeHtml(CHANNEL_LABELS[channel.kind] || channel.kind)}</span>
      <strong translate="no">${escapeHtml(channel.value)}</strong>
      <svg aria-hidden="true" viewBox="0 0 24 24"><path d="M9 5h10v10M19 5 8 16M5 9v10h10"></path></svg>
    </a>`;
  }).join("")}</div>`;
}

function renderSourceLinks(contact) {
  const jobs = Array.isArray(contact.jobs) ? contact.jobs : [];
  const discoveries = Array.isArray(contact.discoveries) ? contact.discoveries : [];
  if (!jobs.length && !discoveries.length) {
    return '<div class="contact-detail-empty">No active job links. Provenance is still retained.</div>';
  }
  const jobRows = jobs.map((job) => `<div class="contact-source-link">
    <button type="button" data-contact-job-id="${escapeHtml(job.id)}"><span>Saved job</span><strong>${escapeHtml(job.title || job.id)}</strong><small>${escapeHtml(job.company || "")}</small></button>
    <button type="button" class="icon-button" data-contact-unlink-job="${escapeHtml(job.id)}" aria-label="Unlink ${escapeHtml(job.title || job.id)}" data-tooltip="Remove this job link; keep the contact and provenance.">&times;</button>
  </div>`);
  const discoveryRows = discoveries.map((discovery) => `<div class="contact-source-link">
    <button type="button" data-contact-discovery-id="${escapeHtml(discovery.id)}"><span>Job Feed</span><strong>${escapeHtml(discovery.title || discovery.id)}</strong><small>${escapeHtml(discovery.company || "")}</small></button>
    <button type="button" class="icon-button" data-contact-unlink-discovery="${escapeHtml(discovery.id)}" aria-label="Unlink ${escapeHtml(discovery.title || discovery.id)}" data-tooltip="Remove this Job Feed link; keep the contact and provenance.">&times;</button>
  </div>`);
  return `<div class="contact-source-links">${[...jobRows, ...discoveryRows].join("")}</div>`;
}

function renderProvenance(contact) {
  const records = Array.isArray(contact.provenance) ? [...contact.provenance].reverse() : [];
  if (!records.length) return '<div class="contact-detail-empty">No provenance recorded.</div>';
  return `<div class="contact-provenance-list">${records.map((record, index) => {
    const label = record.origin === "jd_candidate" ? "JD candidate" : "Manual confirmation";
    const source = record.job_id ? "Saved job" : record.discovery_id ? "Job Feed" : "Contact ledger";
    const evidence = record.source_text
      ? `<blockquote translate="no">${escapeHtml(record.source_text)}</blockquote><small>Lines ${escapeHtml(record.line_start)}–${escapeHtml(record.line_end)} · ${escapeHtml(source)}</small>`
      : `<p>Contact facts were explicitly confirmed in the ledger.</p><small>${escapeHtml(source)}</small>`;
    return `<details ${index === 0 ? "open" : ""}><summary><span>${escapeHtml(label)}</span><time>${escapeHtml(formatTime(record.recorded_at))}</time></summary>${evidence}</details>`;
  }).join("")}</div>`;
}

function renderEditForm(contact) {
  return `<form class="contact-detail-edit" data-contact-edit-form>
    <div class="contact-detail-section-heading"><div><span class="section-kicker">Contact facts</span><h3>Edit contact</h3></div></div>
    <div class="contact-fact-grid">
      <label class="editor-field"><span>Name</span><input name="name" maxlength="200" value="${escapeHtml(contact.name)}"></label>
      <label class="editor-field"><span>Role</span><input name="role" maxlength="300" value="${escapeHtml(contact.role)}"></label>
      <label class="editor-field"><span>Company</span><input name="company" maxlength="300" value="${escapeHtml(contact.company)}"></label>
      <label class="editor-field"><span>Context</span><input name="context" maxlength="1000" value="${escapeHtml(contact.context)}"></label>
    </div>
    <p class="contact-edit-note">Source-stated channels and provenance remain exact. Edit them before first save if extraction needs correction.</p>
    <div class="editor-actions"><button type="button" class="secondary-button" data-contact-edit-cancel>Cancel</button><button type="submit" class="primary-button">Save changes</button></div>
  </form>`;
}

function renderDetail() {
  const contact = selectedContact();
  if (!contact) {
    el.detail.innerHTML = '<div class="empty-state">Select a contact to inspect it.</div>';
    return;
  }
  if (state.editing) {
    el.detail.innerHTML = renderEditForm(contact);
    return;
  }
  const status = contact.archived ? "Archived" : "Active";
  const sourceCount = (contact.jobs?.length || 0) + (contact.discoveries?.length || 0);
  el.detail.innerHTML = `<article class="contact-detail">
    <header class="contact-detail-header">
      <div class="contact-avatar contact-avatar--large" aria-hidden="true">${escapeHtml(initials(contact))}</div>
      <div><span class="contact-status ${contact.archived ? "is-archived" : ""}">${escapeHtml(status)}</span><h2>${escapeHtml(contactName(contact))}</h2><p>${escapeHtml([contact.role, contact.company].filter(Boolean).join(" · ") || contact.context || "No role or company recorded")}</p></div>
      <button type="button" class="secondary-button compact-button" data-contact-edit ${contact.archived ? "disabled" : ""}>Edit</button>
    </header>
    <section class="contact-detail-section"><div class="contact-detail-section-heading"><div><span class="section-kicker">Reach</span><h3>Channels</h3></div><span>${contact.channels?.length || 0}</span></div>${renderChannels(contact)}</section>
    <section class="contact-detail-section"><div class="contact-detail-section-heading"><div><span class="section-kicker">Context</span><h3>Linked opportunities</h3></div><span>${sourceCount}</span></div>${renderSourceLinks(contact)}</section>
    <section class="contact-detail-section"><div class="contact-detail-section-heading"><div><span class="section-kicker">Audit trail</span><h3>Provenance</h3></div><span>${contact.provenance?.length || 0}</span></div>${renderProvenance(contact)}</section>
    <footer class="contact-detail-actions"><button type="button" class="secondary-button" data-contact-archive>${contact.archived ? "Restore contact" : "Archive contact"}</button></footer>
  </article>`;
}

function renderLedger() {
  const activeCount = state.contacts.filter((contact) => !contact.archived).length;
  const archivedCount = state.contacts.length - activeCount;
  el.archivedCount.textContent = String(archivedCount);
  el.totalCount.textContent = `${activeCount} active`;
  const archived = state.visibility === "archived";
  el.archivedFilter.classList.toggle("active", archived);
  el.archivedFilter.setAttribute("aria-pressed", String(archived));
  const archiveLabel = archived ? "Show active contacts" : "Show archived contacts";
  el.archivedFilter.setAttribute("aria-label", archiveLabel);
  el.archivedFilter.dataset.tooltip = archiveLabel;
  if (state.loading) {
    el.list.innerHTML = renderLoadingState({ label: "Loading contacts…", compact: true });
    el.resultCount.textContent = "Loading";
    renderDetail();
    return;
  }
  const rows = visibleContacts();
  el.resultCount.textContent = `${rows.length} result${rows.length === 1 ? "" : "s"}`;
  if (!rows.some((contact) => contact.id === state.selectedId)) {
    state.selectedId = rows[0]?.id || null;
    state.editing = false;
  }
  el.list.innerHTML = rows.length ? rows.map((contact) => {
    const primaryChannel = contact.channels?.[0]?.value || contact.context || "No direct channel";
    const linkCount = (contact.jobs?.length || 0) + (contact.discoveries?.length || 0);
    return `<button type="button" role="listitem" class="contact-row ${contact.id === state.selectedId ? "is-selected" : ""}" data-contact-id="${escapeHtml(contact.id)}">
      <span class="contact-avatar" aria-hidden="true">${escapeHtml(initials(contact))}</span>
      <span class="contact-row-copy"><strong>${escapeHtml(contactName(contact))}</strong><small>${escapeHtml([contact.role, contact.company].filter(Boolean).join(" · ") || primaryChannel)}</small></span>
      <span class="contact-row-meta"><small>${linkCount} linked</small><svg aria-hidden="true" viewBox="0 0 24 24"><path d="m9 5 7 7-7 7"></path></svg></span>
    </button>`;
  }).join("") : `<div class="empty-state-card empty-state-card--compact"><strong>No ${archived ? "archived" : "active"} contacts</strong><span>${el.search.value ? "Try a broader search." : "Save an explicit candidate from a complete job description."}</span></div>`;
  renderDetail();
}

export async function loadContacts({ selectId = null, keepMessage = false } = {}) {
  state.loading = true;
  renderLedger();
  try {
    const payload = await api.getContacts({ includeArchived: true });
    state.contacts = Array.isArray(payload.items) ? payload.items : [];
    if (selectId) state.selectedId = selectId;
    if (!keepMessage) el.message.textContent = "";
  } catch (error) {
    el.message.className = "form-message error";
    el.message.textContent = formatRequestError(error);
  } finally {
    state.loading = false;
    renderLedger();
  }
}

function confirmationMatches(preview) {
  const matches = [];
  const seen = new Set();
  (preview.matches?.exact_matches || []).forEach((contact) => {
    if (!contact?.id || seen.has(contact.id)) return;
    seen.add(contact.id);
    matches.push({ ...contact, matchKind: "exact" });
  });
  (preview.matches?.potential_duplicates || []).forEach((contact) => {
    const id = contact?.contact_id;
    if (!id || seen.has(id)) return;
    seen.add(id);
    matches.push({ ...contact, id, matchKind: "potential" });
  });
  return matches;
}

function selectedConfirmationContact() {
  return confirmationMatches(state.confirmationPreview || {})
    .find((contact) => contact.id === state.confirmationSelection) || null;
}

function syncConfirmationMode() {
  const creating = state.confirmationSelection === "create";
  const hasSelection = Boolean(state.confirmationSelection);
  [el.confirmName, el.confirmRole, el.confirmCompany, el.confirmContext]
    .forEach((input) => { input.disabled = !creating || state.confirmationSaving; });
  el.addChannel.disabled = !creating || state.confirmationSaving;
  el.channelList.querySelectorAll("input, select, button").forEach((node) => {
    node.disabled = !creating || state.confirmationSaving;
  });
  el.saveConfirmation.disabled = !hasSelection || state.confirmationSaving;
  el.saveConfirmation.textContent = state.confirmationSaving
    ? "Saving…"
    : state.confirmationPreview?.claimed_contact
      ? "View contact"
      : creating ? "Save contact" : "Link contact";
}

function renderConfirmationChannels() {
  el.channelList.innerHTML = state.confirmationChannels.length
    ? state.confirmationChannels.map((channel, index) => `<div class="contact-channel-row" data-contact-channel-index="${index}">
      <label><span class="visually-hidden">Channel type</span><select data-contact-channel-kind><option value="email" ${channel.kind === "email" ? "selected" : ""}>Email</option><option value="phone" ${channel.kind === "phone" ? "selected" : ""}>Phone</option><option value="professional_profile" ${channel.kind === "professional_profile" ? "selected" : ""}>Professional profile</option></select></label>
      <label><span class="visually-hidden">Channel value</span><input data-contact-channel-value maxlength="2048" value="${escapeHtml(channel.value || "")}" placeholder="Exact email, phone, or profile URL"></label>
      <button type="button" class="icon-button" data-contact-channel-remove aria-label="Remove channel" data-tooltip="Remove this channel from the contact.">&times;</button>
    </div>`).join("")
    : '<div class="contact-channel-empty">No direct channel detected. A clearly stated name can still be saved.</div>';
  syncConfirmationMode();
}

function renderConfirmationMatches() {
  const preview = state.confirmationPreview;
  const matches = confirmationMatches(preview);
  if (preview.claimed_contact) {
    state.confirmationSelection = preview.claimed_contact.id;
    el.matchOptions.classList.remove("hidden");
    el.matchGuidance.textContent = "This exact JD candidate is already saved. No duplicate will be created.";
    el.matchList.innerHTML = `<div class="contact-already-saved"><span>Saved contact</span><strong>${escapeHtml(contactName(preview.claimed_contact))}</strong><small>${escapeHtml([preview.claimed_contact.role, preview.claimed_contact.company].filter(Boolean).join(" · "))}</small></div>`;
    return;
  }
  if (!matches.length) {
    state.confirmationSelection = "create";
    el.matchOptions.classList.add("hidden");
    el.matchList.replaceChildren();
    return;
  }
  const exact = matches.filter((contact) => contact.matchKind === "exact");
  state.confirmationSelection = exact.find((contact) => !contact.archived)?.id || "";
  el.matchOptions.classList.remove("hidden");
  el.matchGuidance.textContent = exact.length
    ? "An email or professional profile matches an existing contact. Linking is the safest default."
    : "These records look similar. Choose deliberately; Career Hub will not merge them automatically.";
  const createOption = `<label class="contact-match-option"><input type="radio" name="contact-match" value="create" ${state.confirmationSelection === "create" ? "checked" : ""}><span><strong>Create a separate contact</strong><small>Use the edited fields below. Exact channels must remain unique.</small></span></label>`;
  const rows = matches.map((contact) => `<label class="contact-match-option ${contact.archived ? "is-disabled" : ""}"><input type="radio" name="contact-match" value="${escapeHtml(contact.id)}" ${state.confirmationSelection === contact.id ? "checked" : ""} ${contact.archived ? "disabled" : ""}><span><strong>${escapeHtml(contactName(contact))}</strong><small>${escapeHtml([contact.role, contact.company].filter(Boolean).join(" · ") || "No role or company")}</small><em>${contact.matchKind === "exact" ? "Exact channel" : `Similar ${escapeHtml((contact.matched_fields || []).join(", "))}`}${contact.archived ? " · Archived" : ""}</em></span></label>`).join("");
  el.matchList.innerHTML = `${rows}${createOption}`;
}

function renderConfirmationPreview() {
  const preview = state.confirmationPreview;
  const candidate = preview.candidate;
  const source = preview.source;
  const defaults = preview.defaults;
  el.progress.replaceChildren();
  el.content.classList.remove("hidden");
  el.sourceKind.textContent = source.kind === "job" ? "Saved job" : "Job Feed capture";
  el.sourceTitle.textContent = [source.title, source.company].filter(Boolean).join(" · ") || source.id;
  el.sourceEvidence.textContent = candidate.source_text || "";
  el.sourceLines.textContent = candidate.line_start === candidate.line_end
    ? `Line ${candidate.line_start}`
    : `Lines ${candidate.line_start}–${candidate.line_end}`;
  el.confirmName.value = defaults.name || "";
  el.confirmRole.value = defaults.role || "";
  el.confirmCompany.value = defaults.company || "";
  el.confirmContext.value = defaults.context || "";
  state.confirmationChannels = (defaults.channels || []).map((channel) => ({ ...channel }));
  renderConfirmationMatches();
  renderConfirmationChannels();
  el.confirmationMessage.className = "form-message";
  el.confirmationMessage.textContent = "";
  syncConfirmationMode();
}

export async function openContactCandidateConfirmation(source, { trigger = null } = {}) {
  if (!state.initialized) initContacts();
  const epoch = ++state.confirmationEpoch;
  state.confirmationSource = { ...source };
  state.confirmationPreview = null;
  state.confirmationSelection = "";
  state.confirmationChannels = [];
  state.confirmationSaving = false;
  state.confirmationTrigger = trigger;
  el.content.classList.add("hidden");
  el.progress.innerHTML = renderLoadingState({ label: "Checking source and possible duplicates…" });
  el.saveConfirmation.disabled = true;
  el.saveConfirmation.textContent = "Save contact";
  el.confirmationMessage.textContent = "";
  if (!el.dialog.open) el.dialog.showModal();
  try {
    const preview = await api.previewContactCandidate(state.confirmationSource);
    if (epoch !== state.confirmationEpoch || !el.dialog.open) return;
    state.confirmationPreview = preview;
    renderConfirmationPreview();
  } catch (error) {
    if (epoch !== state.confirmationEpoch || !el.dialog.open) return;
    el.progress.replaceChildren();
    el.content.classList.remove("hidden");
    el.confirmationMessage.className = "form-message error";
    el.confirmationMessage.textContent = formatRequestError(error);
    el.saveConfirmation.disabled = true;
  }
}

function confirmationContactPayload() {
  return {
    name: el.confirmName.value.trim(),
    role: el.confirmRole.value.trim(),
    company: el.confirmCompany.value.trim(),
    context: el.confirmContext.value.trim(),
    channels: state.confirmationChannels
      .map((channel) => ({ kind: channel.kind, value: String(channel.value || "").trim() }))
      .filter((channel) => channel.value),
  };
}

async function saveConfirmation(event) {
  event.preventDefault();
  if (!state.confirmationPreview || !state.confirmationSource || state.confirmationSaving) return;
  if (state.confirmationPreview.claimed_contact) {
    state.selectedId = state.confirmationPreview.claimed_contact.id;
    el.dialog.close();
    window.dispatchEvent(new CustomEvent("careerhub:opencontacts", { detail: { contactId: state.selectedId } }));
    return;
  }
  if (!state.confirmationSelection) {
    el.confirmationMessage.className = "form-message error";
    el.confirmationMessage.textContent = "Choose whether to link an existing contact or create a separate one.";
    return;
  }
  const contactPayload = confirmationContactPayload();
  if (state.confirmationSelection === "create" && !contactPayload.name && !contactPayload.channels.length) {
    el.confirmationMessage.className = "form-message error";
    el.confirmationMessage.textContent = "Keep a stated name or at least one exact channel.";
    return;
  }
  state.confirmationSaving = true;
  syncConfirmationMode();
  el.confirmationMessage.className = "form-message";
  el.confirmationMessage.textContent = "Saving explicit contact…";
  try {
    const match = selectedConfirmationContact();
    const result = state.confirmationSelection === "create"
      ? await api.confirmContactCandidateCreate(state.confirmationSource, contactPayload)
      : await api.confirmContactCandidateLink(
        state.confirmationSource,
        state.confirmationSelection,
        Number(match?.revision || 0),
      );
    const contact = result.contact;
    state.selectedId = contact.id;
    if (state.confirmationTrigger) {
      state.confirmationTrigger.textContent = "Saved";
      state.confirmationTrigger.disabled = true;
    }
    await loadContacts({ selectId: contact.id, keepMessage: true });
    el.dialog.close();
    toast?.show({ message: result.outcome === "created" ? "Contact saved from exact JD evidence." : "Existing contact linked to this opportunity." });
    window.dispatchEvent(new CustomEvent("careerhub:contactsaved", {
      detail: { contact, source: state.confirmationSource },
    }));
  } catch (error) {
    el.confirmationMessage.className = "form-message error";
    el.confirmationMessage.textContent = formatRequestError(error);
  } finally {
    state.confirmationSaving = false;
    syncConfirmationMode();
  }
}

async function saveContactEdit(form) {
  const contact = selectedContact();
  if (!contact) return;
  const formData = new FormData(form);
  el.message.textContent = "Saving contact…";
  try {
    const result = await api.updateContact(contact.id, contact.revision, {
      name: String(formData.get("name") || ""),
      role: String(formData.get("role") || ""),
      company: String(formData.get("company") || ""),
      context: String(formData.get("context") || ""),
    });
    state.contacts = state.contacts.map((item) => item.id === contact.id ? result.contact : item);
    state.editing = false;
    el.message.className = "form-message success";
    el.message.textContent = "Contact updated.";
    renderLedger();
  } catch (error) {
    el.message.className = "form-message error";
    el.message.textContent = formatRequestError(error);
  }
}

async function setArchived(contact) {
  const verb = contact.archived ? "restore" : "archive";
  if (!window.confirm(`${verb === "archive" ? "Archive" : "Restore"} ${contactName(contact)}? Opportunity links and provenance will remain intact.`)) return;
  try {
    await api.setContactArchived(contact.id, !contact.archived, contact.revision);
    state.selectedId = null;
    state.editing = false;
    await loadContacts({ keepMessage: true });
    el.message.className = "form-message success";
    el.message.textContent = `Contact ${verb === "archive" ? "archived" : "restored"}.`;
  } catch (error) {
    el.message.className = "form-message error";
    el.message.textContent = formatRequestError(error);
  }
}

async function unlink(contact, { jobId = "", discoveryId = "" } = {}) {
  if (!window.confirm("Remove this opportunity link? The contact and its provenance will remain saved.")) return;
  try {
    const result = await api.unlinkContact(contact.id, contact.revision, {
      jobIds: jobId ? [jobId] : [],
      discoveryIds: discoveryId ? [discoveryId] : [],
    });
    state.contacts = state.contacts.map((item) => item.id === contact.id ? result.contact : item);
    el.message.className = "form-message success";
    el.message.textContent = "Opportunity link removed; contact retained.";
    renderLedger();
  } catch (error) {
    el.message.className = "form-message error";
    el.message.textContent = formatRequestError(error);
  }
}

export function selectContact(contactId) {
  state.selectedId = contactId || null;
  state.editing = false;
  renderLedger();
}

export function initContacts() {
  if (state.initialized) return;
  Object.assign(el, {
    search: document.querySelector("#contact-search"),
    archivedFilter: document.querySelector("#contact-archived-filter"),
    archivedCount: document.querySelector("#contact-archived-count"),
    totalCount: document.querySelector("#contact-total-count"),
    resultCount: document.querySelector("#contact-result-count"),
    list: document.querySelector("#contact-list"),
    detail: document.querySelector("#contact-detail"),
    message: document.querySelector("#contact-message"),
    dialog: document.querySelector("#contact-confirmation-dialog"),
    form: document.querySelector("#contact-confirmation-form"),
    progress: document.querySelector("#contact-confirmation-progress"),
    content: document.querySelector("#contact-confirmation-content"),
    sourceKind: document.querySelector("#contact-source-kind"),
    sourceTitle: document.querySelector("#contact-source-title"),
    sourceEvidence: document.querySelector("#contact-source-evidence"),
    sourceLines: document.querySelector("#contact-source-lines"),
    matchOptions: document.querySelector("#contact-match-options"),
    matchGuidance: document.querySelector("#contact-match-guidance"),
    matchList: document.querySelector("#contact-match-list"),
    confirmName: document.querySelector("#contact-confirm-name"),
    confirmRole: document.querySelector("#contact-confirm-role"),
    confirmCompany: document.querySelector("#contact-confirm-company"),
    confirmContext: document.querySelector("#contact-confirm-context"),
    channelList: document.querySelector("#contact-channel-list"),
    addChannel: document.querySelector("#add-contact-channel"),
    confirmationMessage: document.querySelector("#contact-confirmation-message"),
    saveConfirmation: document.querySelector("#save-contact-confirmation"),
  });
  toast = createToastManager(document.querySelector("#toast-region"));
  state.initialized = true;
  el.search.addEventListener("input", renderLedger);
  el.archivedFilter.addEventListener("click", () => {
    state.visibility = state.visibility === "archived" ? "active" : "archived";
    state.selectedId = null;
    state.editing = false;
    renderLedger();
  });
  el.list.addEventListener("click", (event) => {
    const row = event.target.closest("[data-contact-id]");
    if (!row) return;
    state.selectedId = row.dataset.contactId;
    state.editing = false;
    renderLedger();
  });
  el.detail.addEventListener("click", (event) => {
    const contact = selectedContact();
    if (!contact) return;
    if (event.target.closest("[data-contact-edit]")) {
      state.editing = true;
      renderDetail();
      return;
    }
    if (event.target.closest("[data-contact-edit-cancel]")) {
      state.editing = false;
      renderDetail();
      return;
    }
    const job = event.target.closest("[data-contact-job-id]");
    if (job) {
      window.dispatchEvent(new CustomEvent("careerhub:openjob", { detail: { jobId: job.dataset.contactJobId } }));
      return;
    }
    const discovery = event.target.closest("[data-contact-discovery-id]");
    if (discovery) {
      window.dispatchEvent(new CustomEvent("careerhub:opendiscovery", { detail: { discoveryId: discovery.dataset.contactDiscoveryId } }));
      return;
    }
    const unlinkJob = event.target.closest("[data-contact-unlink-job]");
    if (unlinkJob) { void unlink(contact, { jobId: unlinkJob.dataset.contactUnlinkJob }); return; }
    const unlinkDiscovery = event.target.closest("[data-contact-unlink-discovery]");
    if (unlinkDiscovery) { void unlink(contact, { discoveryId: unlinkDiscovery.dataset.contactUnlinkDiscovery }); return; }
    if (event.target.closest("[data-contact-archive]")) void setArchived(contact);
  });
  el.detail.addEventListener("submit", (event) => {
    if (!event.target.matches("[data-contact-edit-form]")) return;
    event.preventDefault();
    void saveContactEdit(event.target);
  });
  el.matchList.addEventListener("change", (event) => {
    const input = event.target.closest('input[name="contact-match"]');
    if (!input) return;
    state.confirmationSelection = input.value;
    syncConfirmationMode();
  });
  el.addChannel.addEventListener("click", () => {
    state.confirmationChannels.push({ kind: "email", value: "" });
    renderConfirmationChannels();
    el.channelList.querySelector("[data-contact-channel-index]:last-child input")?.focus();
  });
  el.channelList.addEventListener("input", (event) => {
    const row = event.target.closest("[data-contact-channel-index]");
    if (!row) return;
    const index = Number(row.dataset.contactChannelIndex);
    if (event.target.matches("[data-contact-channel-kind]")) state.confirmationChannels[index].kind = event.target.value;
    if (event.target.matches("[data-contact-channel-value]")) state.confirmationChannels[index].value = event.target.value;
  });
  el.channelList.addEventListener("click", (event) => {
    const remove = event.target.closest("[data-contact-channel-remove]");
    if (!remove) return;
    const row = remove.closest("[data-contact-channel-index]");
    state.confirmationChannels.splice(Number(row.dataset.contactChannelIndex), 1);
    renderConfirmationChannels();
  });
  el.form.addEventListener("submit", saveConfirmation);
  ["#close-contact-confirmation", "#cancel-contact-confirmation"].forEach((selector) => {
    document.querySelector(selector).addEventListener("click", () => el.dialog.close());
  });
  el.dialog.addEventListener("close", () => {
    state.confirmationEpoch += 1;
    state.confirmationSaving = false;
    state.confirmationTrigger = null;
  });
  renderLedger();
}
