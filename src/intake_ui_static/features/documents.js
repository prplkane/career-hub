import { api } from "../ui/api.js";
import { escapeHtml, formatBytes, formatRequestError } from "../ui/primitives.js";

const state = { documents: [], revision: "", selectedId: null, visibility: "active", editingId: null };
const el = {};

function selected() { return state.documents.find((item) => item.id === state.selectedId) || null; }
function metadata() {
  return {
    title: el.title.value,
    category: el.category.value,
    issued_on: el.issuedOn.value,
    expires_on: el.expiresOn.value,
    notes: el.notes.value,
  };
}

function visibleDocuments() {
  const archived = state.visibility === "archived";
  const query = el.search.value.trim().toLocaleLowerCase();
  const category = el.categoryFilter.value;
  return state.documents.filter((item) => {
    if ((item.archived === true) !== archived || (category && item.category !== category)) return false;
    return !query || [item.title, item.filename, item.category, item.notes].join(" ").toLocaleLowerCase().includes(query);
  });
}

function renderProfileSummary() {
  if (!el.profileSummary) return;
  const active = state.documents.filter((item) => item.archived !== true);
  const expiring = active.filter((item) => item.expires_on).sort((a, b) => a.expires_on.localeCompare(b.expires_on))[0];
  el.profileSummary.innerHTML = active.length
    ? `<div class="document-summary-count"><strong>${active.length}</strong><span>active document${active.length === 1 ? "" : "s"}</span></div>${expiring ? `<p><span>Next expiry</span><strong>${escapeHtml(expiring.title)}</strong><small>${escapeHtml(expiring.expires_on)}</small></p>` : '<p class="muted-copy">No expiration dates recorded.</p>'}`
    : '<div class="empty-state compact">No documents yet. Add sensitive records only when you need the ledger.</div>';
}

function render() {
  const activeCount = state.documents.filter((item) => item.archived !== true).length;
  const archivedCount = state.documents.length - activeCount;
  el.archivedCount.textContent = archivedCount;
  el.archivedFilter.classList.toggle("active", state.visibility === "archived");
  el.archivedFilter.setAttribute("aria-pressed", String(state.visibility === "archived"));
  const archiveLabel = state.visibility === "archived" ? "Show active documents" : "Show archived documents";
  el.archivedFilter.setAttribute("aria-label", archiveLabel);
  el.archivedFilter.dataset.tooltip = archiveLabel;
  const categories = [...new Set(state.documents.map((item) => item.category).filter(Boolean))].sort();
  const prior = el.categoryFilter.value;
  el.categoryFilter.innerHTML = '<option value="">All categories</option>' + categories.map((value) => `<option value="${escapeHtml(value)}">${escapeHtml(value)}</option>`).join("");
  if (categories.includes(prior)) el.categoryFilter.value = prior;
  const rows = visibleDocuments();
  if (!rows.some((item) => item.id === state.selectedId)) state.selectedId = rows[0]?.id || null;
  el.list.innerHTML = rows.length ? rows.map((item) => `<button type="button" class="document-row ${item.id === state.selectedId ? "is-selected" : ""}" data-document-id="${escapeHtml(item.id)}"><span><strong>${escapeHtml(item.title)}</strong><small>${escapeHtml(item.filename)}</small></span><span>${escapeHtml(item.category)}</span><span>${escapeHtml(item.expires_on ? `Expires ${item.expires_on}` : item.issued_on || "—")}</span><span>${formatBytes(item.size_bytes)}</span></button>`).join("") : `<div class="empty-state">No ${state.visibility} documents match these filters.</div>`;
  renderDetail();
  renderProfileSummary();
}

function renderDetail() {
  const item = selected();
  if (!item) { el.detail.innerHTML = '<div class="empty-state">Select a document to inspect it.</div>'; return; }
  const action = item.archived ? "Restore" : "Archive";
  el.detail.innerHTML = `<div class="document-detail-head"><p class="section-kicker">${escapeHtml(item.category)}</p><h2>${escapeHtml(item.title)}</h2><small>${escapeHtml(item.filename)}</small></div><dl class="document-metadata"><div><dt>Issued</dt><dd>${escapeHtml(item.issued_on || "Not recorded")}</dd></div><div><dt>Expires</dt><dd>${escapeHtml(item.expires_on || "Not recorded")}</dd></div><div><dt>Size</dt><dd>${formatBytes(item.size_bytes)}</dd></div><div><dt>Status</dt><dd>${item.archived ? "Archived" : "Active"}</dd></div></dl><p class="document-notes">${escapeHtml(item.notes || "No notes")}</p><div class="editor-actions"><button type="button" class="secondary-button" data-document-archive>${action}</button><button type="button" class="secondary-button" data-document-edit>Edit details</button><button type="button" class="primary-button" data-document-open>Open file</button></div>`;
}

export async function loadDocuments({ keepMessage = false } = {}) {
  const payload = await api.listDocuments();
  state.documents = payload.documents || [];
  state.revision = payload.revision;
  render();
  if (!keepMessage) el.message.textContent = "";
}

function openDialog(item = null) {
  state.editingId = item?.id || null;
  el.dialogTitle.textContent = item ? "Edit document details" : "Add document";
  el.fileField.hidden = Boolean(item);
  el.file.required = !item;
  el.file.value = "";
  el.title.value = item?.title || "";
  el.category.value = item?.category || "";
  el.issuedOn.value = item?.issued_on || "";
  el.expiresOn.value = item?.expires_on || "";
  el.notes.value = item?.notes || "";
  el.dialogMessage.textContent = "";
  el.dialog.showModal();
  el.title.focus();
}

async function save(event) {
  event.preventDefault();
  el.save.disabled = true;
  el.dialogMessage.textContent = "Encrypting and saving locally…";
  try {
    const result = state.editingId
      ? await api.updateDocument(state.editingId, metadata(), state.revision)
      : await api.createDocument(el.file.files[0], metadata(), state.revision);
    state.selectedId = result.record.id;
    el.dialog.close();
    await loadDocuments({ keepMessage: true });
    el.message.textContent = state.editingId ? "Document details saved." : "Document encrypted and added.";
  } catch (error) {
    el.dialogMessage.className = "form-message error";
    el.dialogMessage.textContent = formatRequestError(error);
  } finally { el.save.disabled = false; }
}

export function initDocuments() {
  Object.assign(el, {
    profileSummary: document.querySelector("#profile-documents-summary"), search: document.querySelector("#document-search"), categoryFilter: document.querySelector("#document-category-filter"), archivedFilter: document.querySelector("#document-archived-filter"), archivedCount: document.querySelector("#document-archived-count"), list: document.querySelector("#document-list"), detail: document.querySelector("#document-detail"), message: document.querySelector("#document-message"), newButton: document.querySelector("#new-document-button"), dialog: document.querySelector("#document-dialog"), form: document.querySelector("#document-form"), dialogTitle: document.querySelector("#document-dialog-title"), fileField: document.querySelector("#document-file-field"), file: document.querySelector("#document-file"), title: document.querySelector("#document-title"), category: document.querySelector("#document-category"), issuedOn: document.querySelector("#document-issued-on"), expiresOn: document.querySelector("#document-expires-on"), notes: document.querySelector("#document-notes"), dialogMessage: document.querySelector("#document-dialog-message"), save: document.querySelector("#save-document-button")
  });
  el.newButton.addEventListener("click", () => openDialog());
  document.querySelector("#close-document-dialog").addEventListener("click", () => el.dialog.close());
  document.querySelector("#cancel-document-dialog").addEventListener("click", () => el.dialog.close());
  el.form.addEventListener("submit", save);
  [el.search, el.categoryFilter].forEach((node) => node.addEventListener("input", render));
  el.archivedFilter.addEventListener("click", () => { state.visibility = state.visibility === "archived" ? "active" : "archived"; render(); });
  el.list.addEventListener("click", (event) => { const row = event.target.closest("[data-document-id]"); if (row) { state.selectedId = row.dataset.documentId; render(); } });
  el.detail.addEventListener("click", async (event) => {
    const item = selected(); if (!item) return;
    if (event.target.closest("[data-document-edit]")) return openDialog(item);
    if (event.target.closest("[data-document-open]")) {
      const preview = window.open("", "_blank");
      if (preview) preview.opener = null;
      try {
        const response = await api.documentFile(item.id);
        const url = URL.createObjectURL(await response.blob());
        if (preview) preview.location.href = url;
        else el.message.textContent = "Allow local pop-ups to open the encrypted document.";
        setTimeout(() => URL.revokeObjectURL(url), 60000);
      } catch (error) {
        if (preview) preview.close();
        el.message.textContent = formatRequestError(error);
      }
    }
    if (event.target.closest("[data-document-archive]")) {
      try { await api.setDocumentArchived(item.id, !item.archived, state.revision); state.selectedId = null; await loadDocuments({ keepMessage: true }); } catch (error) { el.message.textContent = formatRequestError(error); }
    }
  });
}
