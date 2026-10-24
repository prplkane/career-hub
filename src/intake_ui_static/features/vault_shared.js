// Helpers shared by the Career Vault editors: snapshot cache, layout preference, card detail state, archive toggles and the loading boundary.
import { elements, state } from "../app_context.js";
import { api } from "../ui/api.js";
import { escapeHtml } from "../ui/primitives.js";

const CAREER_VAULT_LAYOUT_KEY = "careerHub.careerVaultLayout";
let vaultSnapshot = null;
let vaultSnapshotPromise = null;

export async function getVaultSnapshot({ refresh = false } = {}) {
  if (refresh) {
    vaultSnapshot = null;
    vaultSnapshotPromise = null;
  }
  if (vaultSnapshot) return vaultSnapshot;
  if (!vaultSnapshotPromise) {
    vaultSnapshotPromise = api.getVaultSnapshot()
      .then((payload) => {
        vaultSnapshot = payload;
        return payload;
      })
      .catch((error) => {
        vaultSnapshot = null;
        throw error;
      })
      .finally(() => {
        vaultSnapshotPromise = null;
      });
  }
  return vaultSnapshotPromise;
}

export function vaultLines(value, splitCommas = false) {
  const pattern = splitCommas ? /[\n,]+/ : /\r?\n/;
  return [...new Set(
    String(value || "").split(pattern).map((item) => item.trim()).filter(Boolean)
  )];
}

function applyCareerVaultLayoutState() {
  const layout = state.careerVaultLayout === "cards" ? "cards" : "list";
  elements.careerVaultWorkspaces.forEach((workspace) => {
    const surface = workspace.dataset.vaultSurface;
    workspace.dataset.vaultLayout = layout;
    workspace.classList.toggle(
      "is-card-detail-open",
      layout === "cards" && state.vaultCardDetailOpen[surface] === true,
    );
  });
  elements.careerVaultLayoutButtons.forEach((button) => {
    const selected = button.dataset.vaultLayout === layout;
    button.classList.toggle("is-active", selected);
    button.setAttribute("aria-pressed", String(selected));
  });
}

export function loadCareerVaultLayoutPreference() {
  let storedLayout = "";
  try {
    storedLayout = window.localStorage.getItem(CAREER_VAULT_LAYOUT_KEY) || "";
  } catch (_error) {
    storedLayout = "";
  }
  state.careerVaultLayout = ["list", "cards"].includes(storedLayout) ? storedLayout : "list";
  Object.keys(state.vaultCardDetailOpen).forEach((surface) => {
    state.vaultCardDetailOpen[surface] = false;
  });
  applyCareerVaultLayoutState();
}

function setCareerVaultLayout(layout, { persist = true } = {}) {
  if (!["list", "cards"].includes(layout)) return;
  state.careerVaultLayout = layout;
  Object.keys(state.vaultCardDetailOpen).forEach((surface) => {
    state.vaultCardDetailOpen[surface] = false;
  });
  if (persist) {
    try {
      window.localStorage.setItem(CAREER_VAULT_LAYOUT_KEY, layout);
    } catch (_error) {
      // The visual preference remains usable when browser storage is unavailable.
    }
  }
  applyCareerVaultLayoutState();
}

export function openCareerVaultCardDetail(surface) {
  if (state.careerVaultLayout !== "cards" || !(surface in state.vaultCardDetailOpen)) return;
  Object.keys(state.vaultCardDetailOpen).forEach((name) => {
    state.vaultCardDetailOpen[name] = name === surface;
  });
  applyCareerVaultLayoutState();
}

export function closeCareerVaultCardDetail(surface) {
  if (!(surface in state.vaultCardDetailOpen)) return;
  state.vaultCardDetailOpen[surface] = false;
  applyCareerVaultLayoutState();
}

export function renderVaultCardPreview(items, fallback) {
  const values = (items || []).map((item) => String(item || "").trim()).filter(Boolean).slice(0, 4);
  const rows = values.length
    ? values.map((item) => `<span>${escapeHtml(item)}</span>`).join("")
    : `<span class="is-empty">${escapeHtml(fallback)}</span>`;
  return `<span class="vault-card-preview vault-card-only">${rows}</span>`;
}

export function renderVaultCardOpenLabel() {
  return `<span class="vault-card-open vault-card-only"><span>Open record</span>${registryChevronIcon()}</span>`;
}

export function setArchiveActionButton(button, restoring, noun) {
  const label = `${restoring ? "Restore" : "Archive"} ${noun}`;
  button.classList.add("icon-action-button");
  button.setAttribute("aria-label", label);
  button.innerHTML = restoring
    ? '<svg aria-hidden="true" viewBox="0 0 24 24"><path d="M4 7h16v13H4zM3 4h18v3H3zm5 7 4-3 4 3M12 8v7"></path></svg><span class="visually-hidden">Restore</span>'
    : '<svg aria-hidden="true" viewBox="0 0 24 24"><path d="M4 7h16v13H4zM3 4h18v3H3zm5 5h8"></path></svg><span class="visually-hidden">Archive</span>';
}

export function updateArchiveScopeToggle(button, showingArchived, noun) {
  if (!button) return;
  const label = showingArchived ? `Show active ${noun}` : `Show archived ${noun}`;
  button.classList.toggle("active", showingArchived);
  button.setAttribute("aria-pressed", String(showingArchived));
  button.setAttribute("aria-label", label);
  button.dataset.tooltip = `${label}.`;
}

const vaultPendingLoads = new Map();

// A late initial load must never turn a newly opened draft back into an edit of
// the previously selected record. Keep the whole editor surface non-interactive
// until all of its current loads settle; navigation outside it remains usable.
export async function withVaultLoading(viewName, load) {
  const view = document.getElementById(`view-${viewName}`);
  vaultPendingLoads.set(viewName, (vaultPendingLoads.get(viewName) || 0) + 1);
  view.inert = true;
  view.setAttribute("aria-busy", "true");
  try {
    return await load();
  } finally {
    const pending = vaultPendingLoads.get(viewName) - 1;
    vaultPendingLoads.set(viewName, pending);
    if (!pending) {
      view.inert = false;
      view.removeAttribute("aria-busy");
    }
  }
}

export function registryChevronIcon(direction = "right") {
  const path = direction === "left" ? "m15 18-6-6 6-6" : "m9 18 6-6-6-6";
  return `<svg class="registry-row-icon" aria-hidden="true" viewBox="0 0 24 24" focusable="false"><path d="${path}"></path></svg>`;
}

export function initVaultShared() {
  elements.careerVaultLayoutButtons.forEach((button) => {
    button.addEventListener("click", () => setCareerVaultLayout(button.dataset.vaultLayout));
  });
  document.querySelectorAll("[data-close-vault-card-detail]").forEach((button) => {
    button.addEventListener("click", () => closeCareerVaultCardDetail(button.dataset.closeVaultCardDetail));
  });
}
