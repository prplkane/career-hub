// Helpers shared by the Career Vault editors: snapshot cache, archive toggles, category matching, evidence highlighting and the loading boundary.
import { api } from "../ui/api.js";
import { escapeHtml } from "../ui/primitives.js";

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

// "Security Engineering", "security-engineering" and "security engineering "
// collapse to one key, so a typo in case or punctuation cannot create a
// second category that only differs in spelling.
export function normalizeCategoryKey(value) {
  return String(value || "").toLocaleLowerCase().replace(/[^\p{L}\p{N}]+/gu, "");
}

// The existing category the typed value matches after normalisation, or null
// when there is none or the typed value already is that category.
export function matchingCategory(typed, categories) {
  const key = normalizeCategoryKey(typed);
  if (!key) return null;
  const match = (categories || []).find((category) => normalizeCategoryKey(category) === key);
  return match && match !== String(typed || "").trim() ? match : null;
}

// Shows the matching existing category under the input and applies it on click.
export function bindCategorySuggestion(input, hint, getCategories, onApply) {
  if (!input || !hint) return;
  const render = () => {
    const match = matchingCategory(input.value, getCategories());
    hint.hidden = !match;
    hint.innerHTML = match
      ? `Existing category: <button type="button" class="text-button" data-apply-category="${escapeHtml(match)}">${escapeHtml(match)}</button>`
      : "";
  };
  input.addEventListener("input", render);
  hint.addEventListener("click", (event) => {
    const apply = event.target.closest("[data-apply-category]");
    if (!apply) return;
    input.value = apply.dataset.applyCategory;
    hint.hidden = true;
    hint.innerHTML = "";
    onApply?.();
  });
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// Marks the given registry skills inside evidence text. The vault snapshot
// carries no mention offsets (only intake extraction does), so the match runs
// here against the names and aliases already loaded for the registry. Longer
// names win over shorter ones and matches never overlap.
export function createSkillHighlighter(skills) {
  const originByTerm = new Map();
  (skills || []).forEach((skill) => {
    [skill?.name, ...(skill?.aliases || [])].forEach((term) => {
      const text = String(term || "").trim();
      if (text && !originByTerm.has(text.toLocaleLowerCase())) {
        originByTerm.set(text.toLocaleLowerCase(), skill.origin === "custom" ? "custom" : "core");
      }
    });
  });
  const terms = [...originByTerm.keys()].sort((left, right) => right.length - left.length);
  if (!terms.length) return (text) => escapeHtml(String(text || ""));
  const pattern = new RegExp(
    `(?<![\\p{L}\\p{N}])(?:${terms.map(escapeRegExp).join("|")})(?![\\p{L}\\p{N}])`,
    "giu",
  );
  return (text) => {
    const value = String(text || "");
    let cursor = 0;
    const parts = [];
    for (const match of value.matchAll(pattern)) {
      parts.push(escapeHtml(value.slice(cursor, match.index)));
      const origin = originByTerm.get(match[0].toLocaleLowerCase()) || "core";
      parts.push(`<mark class="skill-highlight registry-skill-${origin}">${escapeHtml(match[0])}</mark>`);
      cursor = match.index + match[0].length;
    }
    parts.push(escapeHtml(value.slice(cursor)));
    return parts.join("");
  };
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

// Card mode and its saved layout preference were removed (CH-170): the vault
// pages have one navigation model, the pinned list beside the editor.
export function initVaultShared() {}
