// Skills hub: the skills registry (groups and skills) and the competency explorer.
import { elements, state } from "../app_context.js";
import { api } from "../ui/api.js";
import { escapeHtml, formatRequestError } from "../ui/primitives.js";
import { registryChevronIcon, updateArchiveScopeToggle, vaultLines } from "./vault_shared.js";

// Bootstrap hooks supplied by app.js through initSkillsHub().
let loadVaultEducation = null;
let loadVaultExperiences = null;
let loadVaultProjects = null;
let localRouteForView = null;
let navigateToView = null;
let openSkillQuickAction = null;
let selectEducationRecord = null;
let selectVaultExperience = null;
let selectVaultProject = null;

const SKILLS_HUB_MODE_KEY = "careerHub.skillsHubMode";
const SKILLS_REGISTRY_LAYOUT_KEY = "careerHub.skillsRegistryLayout";
let competencyExplorerLoaded = false;
let historicalCompetencyExplorerLoaded = false;
let historicalCompetencyExplorerPromise = null;

export function registrySkillIndex() {
  const index = new Map();
  (state.skillsRegistry?.groups || []).forEach((group) => {
    (group.skills || []).forEach((skill) => index.set(skill.id, { ...skill, group }));
  });
  return index;
}

function registryOriginClass(origin) {
  return origin === "core" ? "core" : "custom";
}

function registryOriginLabel(origin) {
  return origin === "core" ? "Core" : "Custom";
}

function registryOriginOwnership(origin) {
  return origin === "core" ? "built-in" : "user-defined";
}

function registryCreationLabel(skill) {
  if (skill?.origin !== "custom") return "";
  const labels = {
    manual_registry: "Added in Skills Registry",
    vault_editor: "Added with a Career Vault record",
    resume_digest: "Approved from Resume Digest",
    legacy_migration: "Imported by the legacy vocabulary migration",
    unspecified: "Creation source not specified",
  };
  const source = labels[skill.created_via] || "Legacy entry · creation source not recorded";
  if (!skill.created_at) return source;
  const parsed = new Date(skill.created_at);
  if (Number.isNaN(parsed.getTime())) return source;
  const date = new Intl.DateTimeFormat(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  }).format(parsed);
  return `${source} · ${date}`;
}

function applySkillsHubModeState() {
  const mode = state.skillsHubMode === "all" ? "all" : "groups";
  elements.skillsHubModeButtons.forEach((button) => {
    const selected = button.dataset.skillsHubMode === mode;
    button.classList.toggle("is-active", selected);
    button.setAttribute("aria-selected", String(selected));
    button.tabIndex = selected ? 0 : -1;
  });
  elements.skillsHubSurfaces.forEach((surface) => {
    surface.hidden = surface.dataset.skillsHubSurface !== mode;
  });
}

export function loadSkillsHubModePreference() {
  let storedMode = "";
  try {
    storedMode = window.localStorage.getItem(SKILLS_HUB_MODE_KEY) || "";
  } catch (_error) {
    storedMode = "";
  }
  state.skillsHubMode = ["groups", "all"].includes(storedMode) ? storedMode : "groups";
  applySkillsHubModeState();
}

export function setSkillsHubMode(mode, { persist = true, writeHistory = true } = {}) {
  if (!["groups", "all"].includes(mode)) return;
  state.skillsHubMode = mode;
  if (persist) {
    try {
      window.localStorage.setItem(SKILLS_HUB_MODE_KEY, mode);
    } catch (_error) {
      // The page still works when browser storage is unavailable.
    }
  }
  applySkillsHubModeState();
  if (state.activeView === "skills" && writeHistory) {
    const route = localRouteForView("skills");
    if (window.location.hash !== route) {
      window.history.pushState({ careerHubView: "skills", skillsMode: mode }, "", route);
    }
  }
  if (mode === "groups") {
    renderSkillsRegistry();
  } else {
    populateCompetencyFilters();
    renderCompetencyExplorer();
  }
}

function applySkillsRegistryLayoutState() {
  if (!elements.skillsRegistryPanel) return;
  const layout = state.skillsRegistryLayout === "cards" ? "cards" : "list";
  elements.skillsRegistryPanel.dataset.registryLayout = layout;
  elements.skillsRegistryPanel.classList.toggle(
    "is-card-detail-open",
    layout === "cards" && state.registryCardDetailOpen,
  );
  elements.skillsRegistryLayoutButtons.forEach((button) => {
    const selected = button.dataset.registryLayout === layout;
    button.classList.toggle("is-active", selected);
    button.setAttribute("aria-pressed", String(selected));
  });
}

export function loadSkillsRegistryLayoutPreference() {
  let storedLayout = "";
  try {
    storedLayout = window.localStorage.getItem(SKILLS_REGISTRY_LAYOUT_KEY) || "";
  } catch (_error) {
    storedLayout = "";
  }
  state.skillsRegistryLayout = ["list", "cards"].includes(storedLayout) ? storedLayout : "list";
  state.registryCardDetailOpen = false;
  applySkillsRegistryLayoutState();
}

function setSkillsRegistryLayout(layout, { persist = true } = {}) {
  if (!["list", "cards"].includes(layout)) return;
  state.skillsRegistryLayout = layout;
  state.registryCardDetailOpen = false;
  state.registryGroupEditing = false;
  if (persist) {
    try {
      window.localStorage.setItem(SKILLS_REGISTRY_LAYOUT_KEY, layout);
    } catch (_error) {
      // The view still works when browser storage is unavailable.
    }
  }
  applySkillsRegistryLayoutState();
}

function registryAliasSummary(skill) {
  const aliases = skill.aliases || [];
  if (!aliases.length) return '<span class="registry-empty-value">No aliases</span>';
  const visible = aliases.slice(0, 3);
  const remaining = aliases.length - visible.length;
  return `<span title="${escapeHtml(aliases.join(", "))}">${escapeHtml(visible.join(", "))}${remaining ? ` +${remaining}` : ""}</span>`;
}

function sortRegistryGroups(groups) {
  const sorted = [...groups];
  const skillCount = (group) => (group.skills || []).filter(
    (skill) => (skill.archived === true) === (state.registryVisibility === "archived")
  ).length;
  sorted.sort((left, right) => {
    if (state.registryGroupSort === "name_desc") return right.name.localeCompare(left.name);
    if (state.registryGroupSort === "skills_desc") return skillCount(right) - skillCount(left) || left.name.localeCompare(right.name);
    if (state.registryGroupSort === "skills_asc") return skillCount(left) - skillCount(right) || left.name.localeCompare(right.name);
    if (state.registryGroupSort === "source") return left.origin.localeCompare(right.origin) || left.name.localeCompare(right.name);
    return left.name.localeCompare(right.name);
  });
  return sorted;
}

function sortRegistrySkills(skills) {
  return [...skills].sort((left, right) => {
    if (state.registrySkillSort === "name_desc") return right.name.localeCompare(left.name);
    if (state.registrySkillSort === "source") return left.origin.localeCompare(right.origin) || left.name.localeCompare(right.name);
    if (state.registrySkillSort === "aliases_desc") return (right.aliases || []).length - (left.aliases || []).length || left.name.localeCompare(right.name);
    return left.name.localeCompare(right.name);
  });
}

function renderRegistryGroupCard(group) {
  const activeSkills = (group.skills || []).filter((skill) => skill.archived !== true);
  const archivedSkills = (group.skills || []).filter((skill) => skill.archived === true).length;
  const shownSkills = group.archived
    ? (group.skills || []).filter((skill) => skill.archived === true)
    : activeSkills;
  const origin = registryOriginClass(group.origin);
  const countLabel = `${activeSkills.length} active skill${activeSkills.length === 1 ? "" : "s"}${archivedSkills ? `, ${archivedSkills} archived` : ""}`;
  const previewSkills = shownSkills.slice(0, 4);
  const preview = previewSkills.length
    ? previewSkills.map((skill) => `<span class="registry-card-skill">${escapeHtml(skill.name)}</span>`).join("")
    : '<span class="registry-card-skill is-empty">No active skills yet</span>';
  return `<button type="button" class="registry-group-card-button origin-${origin}${group.archived ? " is-archived" : ""}"
    data-registry-group-id="${escapeHtml(group.id)}" aria-label="Open ${escapeHtml(group.name)}, ${escapeHtml(countLabel)}">
    <span class="registry-card-header">
      <span class="registry-card-origin is-${origin}"><i aria-hidden="true"></i>${registryOriginLabel(group.origin)} <small>${registryOriginOwnership(group.origin)}</small></span>
      <span class="registry-card-count" title="${escapeHtml(countLabel)}">${group.archived ? `${archivedSkills} archived` : `${activeSkills.length} active`}</span>
    </span>
    <span class="registry-card-body">
      <strong class="registry-card-title">${escapeHtml(group.name)}</strong>
      <span class="registry-card-skills">${preview}</span>
    </span>
    <span class="registry-card-footer"><span>Open group</span>${registryChevronIcon()}</span>
  </button>`;
}

export function renderSkillsRegistry() {
  if (!elements.skillsRegistryGrid) return;
  applySkillsRegistryLayoutState();
  const query = (elements.skillsRegistrySearch?.value || "").trim().toLocaleLowerCase();
  const allGroups = state.skillsRegistry?.groups || [];
  const archivedGroupCount = allGroups.filter((group) => group.archived === true).length;
  const scopedGroups = allGroups.filter(
    (group) => (group.archived === true) === (state.registryVisibility === "archived")
  );
  const groups = sortRegistryGroups(scopedGroups.filter((group) => {
    if (!query) return true;
    const values = [
      group.name,
      ...(group.skills || []).flatMap((skill) => [skill.name, ...(skill.aliases || [])]),
    ];
    return values.some((value) => String(value || "").toLocaleLowerCase().includes(query));
  }));
  elements.skillsRegistryArchivedCount.textContent = archivedGroupCount;
  updateArchiveScopeToggle(
    elements.skillsRegistryArchivedFilter,
    state.registryVisibility === "archived",
    "skill groups",
  );
  if (
    groups.length
    && !groups.some((group) => group.id === state.selectedRegistryGroupId)
  ) {
    state.selectedRegistryGroupId = groups[0].id;
    state.selectedRegistrySkillId = null;
    state.registryGroupEditing = false;
  }
  if (elements.skillsRegistryGroupCount) {
    elements.skillsRegistryGroupCount.textContent = query
      ? `${groups.length} of ${scopedGroups.length}`
      : String(scopedGroups.length);
  }
  if (!groups.length) {
    const scope = state.registryVisibility === "archived" ? "archived" : "active";
    elements.skillsRegistryGrid.innerHTML = `<div class="registry-list-empty">No ${scope} groups or skills match this view.</div>`;
    elements.skillsRegistryDetail.innerHTML = `<div class="registry-detail-empty"><strong>No matching ${scope} groups</strong><span>Try a broader skill name or switch the archive view.</span></div>`;
    return;
  }
  elements.skillsRegistryGrid.innerHTML = groups.map((group) => {
    if (state.skillsRegistryLayout === "cards") return renderRegistryGroupCard(group);
    const activeSkills = (group.skills || []).filter((skill) => skill.archived !== true);
    const archivedSkills = (group.skills || []).filter((skill) => skill.archived === true).length;
    const selected = group.id === state.selectedRegistryGroupId;
    const origin = registryOriginClass(group.origin);
    const countLabel = `${activeSkills.length} active skill${activeSkills.length === 1 ? "" : "s"}${archivedSkills ? `, ${archivedSkills} archived` : ""}`;
    return `<button type="button" class="registry-group-row origin-${origin}${selected ? " is-selected" : ""}${group.archived ? " is-archived" : ""}"
      data-registry-group-id="${escapeHtml(group.id)}" aria-current="${selected ? "page" : "false"}" aria-label="Open ${escapeHtml(group.name)}, ${escapeHtml(countLabel)}">
      <span class="registry-group-row-name"><strong>${escapeHtml(group.name)}</strong>${group.archived ? "<small>Archived</small>" : ""}</span>
      <span class="registry-group-row-count" title="${escapeHtml(countLabel)}">${group.archived ? archivedSkills : activeSkills.length}</span>
      <span class="registry-origin-text is-${origin}">${registryOriginLabel(group.origin)}</span>
      ${registryChevronIcon()}
    </button>`;
  }).join("");
  renderSkillsRegistryDetail();
}

function registryGroup(groupId) {
  return (state.skillsRegistry?.groups || []).find((group) => group.id === groupId) || null;
}

export function registrySkill(skillId) {
  return registrySkillIndex().get(skillId) || null;
}

function registryGroupOptions(selectedId) {
  return (state.skillsRegistry?.groups || [])
    .filter((group) => group.archived !== true || group.id === selectedId)
    .map((group) => `<option value="${escapeHtml(group.id)}"${group.id === selectedId ? " selected" : ""} translate="no">${escapeHtml(group.name)} (${escapeHtml(group.origin)})</option>`)
    .join("");
}

function renderRegistrySkillEvidence(skill) {
  const historical = skill.archived === true;
  const projection = historical
    ? state.historicalCompetencyExplorer
    : state.competencyExplorer;
  const item = (projection?.competencies || []).find(
    (candidate) => candidate.id === skill.id
  );
  const loading = !projection;
  const evidence = item?.evidence || { experiences: [], projects: [], certifications: [] };
  const evidenceCount = Number(item?.evidence_count || 0);
  const contextCount = Number(item?.professional_context_count || 0);
  return `<section class="registry-skill-evidence" aria-label="Evidence for ${escapeHtml(skill.name)}">
    <div class="registry-skill-evidence-heading">
      <div><span>${historical ? "Historical evidence" : "Linked evidence"}</span><strong>${loading ? "Loading…" : `${evidenceCount} record${evidenceCount === 1 ? "" : "s"}`}</strong></div>
      ${historical
        ? '<span class="registry-historical-scope">Archived scope</span>'
        : `<button type="button" class="text-button" data-open-skill-evidence="${escapeHtml(skill.id)}">Open in All skills</button>`}
    </div>
    ${loading ? `<p class="competency-evidence-empty">Loading the ${historical ? "historical " : ""}evidence projection…</p>` : `
      <div class="registry-skill-evidence-metrics">
        <span><strong>${evidenceCount.toLocaleString()}</strong> evidence</span>
        <span><strong>${contextCount.toLocaleString()}</strong> professional contexts</span>
        <span><strong>${escapeHtml(competencyLastUsed(item || {}))}</strong> last used</span>
      </div>
      ${evidenceCount ? `
        ${renderCompetencyEvidenceSection("Experience", evidence.experiences || [])}
        ${renderCompetencyEvidenceSection("Projects", evidence.projects || [])}
        ${renderCompetencyEvidenceSection("Certifications", evidence.certifications || [])}
      ` : `<p class="competency-evidence-empty">No ${historical ? "historical " : ""}Career Vault evidence is linked to this registry entry.</p>`}
    `}
  </section>`;
}

function renderSkillsRegistryDetail() {
  if (!elements.skillsRegistryDetail) return;
  const selectedSkill = state.selectedRegistrySkillId
    ? registrySkill(state.selectedRegistrySkillId)
    : null;
  if (selectedSkill) {
    const parentArchived = selectedSkill.group.archived === true;
    const editable = selectedSkill.origin === "custom" && !parentArchived;
    const origin = registryOriginClass(selectedSkill.origin);
    const creationLabel = registryCreationLabel(selectedSkill);
    const header = `
      <button type="button" class="registry-back-button" data-registry-back-group="${escapeHtml(selectedSkill.primary_group_id)}">
        ${registryChevronIcon("left")}<span>${escapeHtml(selectedSkill.group.name)}</span>
      </button>
      <header class="registry-detail-header registry-skill-detail-header">
        <div>
          <span class="registry-detail-eyebrow is-${origin}">${registryOriginLabel(selectedSkill.origin)} <small>${registryOriginOwnership(selectedSkill.origin)}</small></span>
          <h2>${escapeHtml(selectedSkill.name)}</h2>
          <p><code>${escapeHtml(selectedSkill.id)}</code>${selectedSkill.archived ? " <span class=\"registry-archived-label\">Archived</span>" : ""}</p>
          ${creationLabel ? `<p class="registry-provenance">${escapeHtml(creationLabel)}</p>` : ""}
        </div>
      </header>`;
    if (!editable) {
      const managedNote = selectedSkill.origin === "core"
        ? "Core vocabulary is maintained by Career Hub and can be extended with your own custom skills."
        : "This custom skill is locked while its group is archived. Restore the group to edit or restore the skill.";
      elements.skillsRegistryDetail.innerHTML = `<div class="registry-detail-shell">
        ${header}
        <dl class="registry-readonly-fields">
          <div><dt>Canonical name</dt><dd>${escapeHtml(selectedSkill.name)}</dd></div>
          <div><dt>Aliases</dt><dd>${(selectedSkill.aliases || []).length ? escapeHtml(selectedSkill.aliases.join(", ")) : "None"}</dd></div>
          <div><dt>Primary group</dt><dd>${escapeHtml(selectedSkill.group.name)}</dd></div>
        </dl>
        <p class="registry-managed-note">${managedNote}</p>
        ${renderRegistrySkillEvidence(selectedSkill)}
      </div>`;
      return;
    }
    elements.skillsRegistryDetail.innerHTML = `<form class="registry-detail-form registry-detail-shell" data-registry-detail-form="skill" data-registry-record-id="${escapeHtml(selectedSkill.id)}">
      ${header}
      <div class="registry-editor-grid">
        <label class="editor-field"><span>Canonical name</span><input name="name" maxlength="160" value="${escapeHtml(selectedSkill.name)}" required></label>
        <label class="editor-field"><span>Primary group</span><select name="primary_group_id" required>${registryGroupOptions(selectedSkill.primary_group_id)}</select></label>
        <label class="editor-field registry-editor-wide"><span>Aliases</span><input name="aliases" value="${escapeHtml((selectedSkill.aliases || []).join(", "))}" placeholder="Comma-separated exact aliases"></label>
      </div>
      <div class="registry-detail-actions">
        <button type="button" class="secondary-button registry-danger-button" data-registry-archive-kind="skill" data-registry-archive-id="${escapeHtml(selectedSkill.id)}" data-registry-archived="${selectedSkill.archived ? "true" : "false"}">${selectedSkill.archived ? "Restore skill" : "Archive skill"}</button>
        <button type="submit" class="primary-button">Save changes</button>
      </div>
      ${renderRegistrySkillEvidence(selectedSkill)}
    </form>`;
    return;
  }

  const selectedGroup = registryGroup(state.selectedRegistryGroupId);
  if (!selectedGroup) {
    elements.skillsRegistryDetail.innerHTML = '<div class="empty-state">Select a group or skill to inspect and manage it.</div>';
    return;
  }
  const editable = selectedGroup.origin === "custom";
  const skills = selectedGroup.skills || [];
  const detailQuery = (elements.skillsRegistrySearch?.value || "").trim().toLocaleLowerCase();
  const groupMatchesQuery = detailQuery
    && String(selectedGroup.name || "").toLocaleLowerCase().includes(detailQuery);
  const scopedSkills = skills.filter(
    (skill) => (skill.archived === true) === (state.registrySkillVisibility === "archived")
  );
  const matchingDetailSkills = detailQuery && !groupMatchesQuery
    ? scopedSkills.filter((skill) => (
      [skill.name, ...(skill.aliases || [])].some(
        (value) => String(value || "").toLocaleLowerCase().includes(detailQuery)
      )
    ))
    : scopedSkills;
  const sortedDetailSkills = sortRegistrySkills(matchingDetailSkills);
  const visibleDetailSkills = sortedDetailSkills.slice(0, 100);
  const activeSkillCount = skills.filter((skill) => !skill.archived).length;
  const archivedSkillCount = skills.length - activeSkillCount;
  const origin = registryOriginClass(selectedGroup.origin);
  const cardBack = state.skillsRegistryLayout === "cards" ? `
    <button type="button" class="registry-back-button registry-all-groups-button" data-close-registry-card-detail>
      ${registryChevronIcon("left")}<span>All groups</span>
    </button>` : "";
  const editForm = editable && state.registryGroupEditing ? `
    <form class="registry-group-edit-form" data-registry-detail-form="group" data-registry-record-id="${escapeHtml(selectedGroup.id)}">
      <label class="editor-field"><span>Group name</span><input name="name" maxlength="160" value="${escapeHtml(selectedGroup.name)}" required></label>
      <div class="registry-group-edit-actions">
        <button type="button" class="secondary-button" data-cancel-registry-group-edit>Cancel</button>
        <button type="submit" class="primary-button">Save group</button>
      </div>
    </form>` : "";
  elements.skillsRegistryDetail.innerHTML = `<div class="registry-detail-shell registry-group-detail">
    ${cardBack}
    <header class="registry-detail-header">
      <div>
        <span class="registry-detail-eyebrow is-${origin}">${registryOriginLabel(selectedGroup.origin)} <small>${registryOriginOwnership(selectedGroup.origin)}</small></span>
        <h2>${escapeHtml(selectedGroup.name)}</h2>
        <p><code>${escapeHtml(selectedGroup.id)}</code> <span aria-hidden="true">&middot;</span> ${activeSkillCount} active skill${activeSkillCount === 1 ? "" : "s"}${selectedGroup.archived ? " <span class=\"registry-archived-label\">Archived</span>" : ""}</p>
      </div>
      <div class="registry-header-actions">
        ${editable ? `<button type="button" class="secondary-button icon-action-button registry-danger-button" data-registry-archive-kind="group" data-registry-archive-id="${escapeHtml(selectedGroup.id)}" data-registry-archived="${selectedGroup.archived ? "true" : "false"}" aria-label="${selectedGroup.archived ? "Restore" : "Archive"} ${escapeHtml(selectedGroup.name)}" data-tooltip="${selectedGroup.archived ? "Restore this group and skills archived with it." : "Archive this group and cascade archive its active skills."}"><svg aria-hidden="true" viewBox="0 0 24 24"><path d="M4 7h16v13H4zM3 4h18v3H3zm5 5h8"></path></svg></button>` : ""}
        ${editable ? `<button type="button" class="secondary-button" data-edit-registry-group ${state.registryGroupEditing ? "disabled" : ""}>Edit group</button>` : ""}
        <button type="button" class="primary-button" data-add-skill-to-group="${escapeHtml(selectedGroup.id)}" ${selectedGroup.archived ? "disabled" : ""}>Add skill</button>
      </div>
    </header>
    ${editForm}
    <div class="registry-skill-toolbar">
      <label class="compact-select-field"><span class="visually-hidden">Sort skills</span><select data-registry-skill-sort aria-label="Sort skills"><option value="name_asc" ${state.registrySkillSort === "name_asc" ? "selected" : ""}>Name A–Z</option><option value="name_desc" ${state.registrySkillSort === "name_desc" ? "selected" : ""}>Name Z–A</option><option value="source" ${state.registrySkillSort === "source" ? "selected" : ""}>Source</option><option value="aliases_desc" ${state.registrySkillSort === "aliases_desc" ? "selected" : ""}>Most aliases</option></select></label>
      <button type="button" class="filter-button archive-filter-button${state.registrySkillVisibility === "archived" ? " active" : ""}" data-registry-skills-archive-filter aria-label="${state.registrySkillVisibility === "archived" ? "Show active skills" : "Show archived skills"}" aria-pressed="${state.registrySkillVisibility === "archived"}" data-tooltip="${state.registrySkillVisibility === "archived" ? "Show active skills." : "Show archived skills."}"><svg aria-hidden="true" viewBox="0 0 24 24"><path d="M4 7h16v13H4zM3 4h18v3H3zm5 5h8"></path></svg><span>${archivedSkillCount}</span></button>
    </div>
    <div class="registry-skill-table" aria-label="Skills in ${escapeHtml(selectedGroup.name)}">
      <div class="registry-skill-table-header">
        <span>Skill</span>
        <span>Aliases</span>
        <span>Source</span>
        <span aria-hidden="true"></span>
      </div>
      <div class="registry-detail-skill-list">
        ${visibleDetailSkills.length ? visibleDetailSkills.map((skill) => {
          const skillOrigin = registryOriginClass(skill.origin);
          return `<button type="button" class="registry-detail-skill${skill.archived ? " is-archived" : ""}" data-registry-skill-id="${escapeHtml(skill.id)}" aria-label="Open ${escapeHtml(skill.name)} skill details">
            <strong>${escapeHtml(skill.name)}${skill.archived ? '<small class="registry-archived-label">Archived</small>' : ""}</strong>
            <span class="registry-alias-cell">${registryAliasSummary(skill)}</span>
            <span class="registry-origin-text is-${skillOrigin}">${registryOriginLabel(skill.origin)}</span>
            ${registryChevronIcon()}
          </button>`;
        }).join("") : '<div class="registry-detail-empty"><strong>No matching skills</strong><span>Try a broader search or add a new skill to this group.</span></div>'}
      </div>
      ${sortedDetailSkills.length > visibleDetailSkills.length ? `<p class="registry-list-limit">Showing ${visibleDetailSkills.length} of ${sortedDetailSkills.length}. Narrow the search to find another skill.</p>` : ""}
    </div>
  </div>`;
}

async function saveSkillsRegistryDetail(event) {
  const form = event.target.closest("[data-registry-detail-form]");
  if (!form || !state.skillsRegistry) return;
  event.preventDefault();
  const formData = new FormData(form);
  const recordId = form.dataset.registryRecordId;
  elements.skillsRegistryMessage.className = "form-message";
  elements.skillsRegistryMessage.textContent = "Validating private Skills Registry...";
  try {
    if (form.dataset.registryDetailForm === "group") {
      await api.updateSkillGroup(recordId, state.skillsRegistry.revision, {
        name: String(formData.get("name") || ""),
      });
      state.selectedRegistryGroupId = recordId;
      state.registryGroupEditing = false;
    } else {
      const primaryGroupId = String(formData.get("primary_group_id") || "");
      await api.updateRegistrySkill(recordId, state.skillsRegistry.revision, {
        name: String(formData.get("name") || ""),
        aliases: vaultLines(formData.get("aliases"), true),
        primary_group_id: primaryGroupId,
      });
      state.selectedRegistrySkillId = recordId;
      state.selectedRegistryGroupId = primaryGroupId;
    }
    await loadSkillsRegistry({ keepMessage: true });
    await loadCompetencyExplorer({ force: true }).catch(() => null);
    elements.skillsRegistryMessage.textContent = "Custom registry entry saved in your local Data Home.";
  } catch (error) {
    elements.skillsRegistryMessage.className = "form-message error";
    elements.skillsRegistryMessage.textContent = formatRequestError(error);
    if (error?.payload?.error_code === "SKILL_REGISTRY_REVISION_CONFLICT") {
      await loadSkillsRegistry({ keepMessage: true }).catch(() => null);
    }
  }
}

async function toggleSkillsRegistryArchive(kind, recordId, archived) {
  if (!state.skillsRegistry) return;
  const verb = archived ? "restore" : "archive";
  let confirmation = `${verb.charAt(0).toUpperCase() + verb.slice(1)} this custom ${kind}?`;
  if (kind === "group") {
    const group = registryGroup(recordId);
    const activeCount = (group?.skills || []).filter((skill) => !skill.archived).length;
    const cascadeCount = (group?.cascade_archived_skill_ids || []).length;
    confirmation = archived
      ? `Restore ${group?.name || "this group"}? ${cascadeCount} skill${cascadeCount === 1 ? "" : "s"} archived with the group will be restored. Skills archived separately will stay archived.`
      : `Archive ${group?.name || "this group"}? This will also archive ${activeCount} active skill${activeCount === 1 ? "" : "s"} in the group. Existing evidence and history stay linked, but these skills will be excluded from new intake and matching.`;
  }
  if (!window.confirm(confirmation)) return;
  try {
    let result;
    if (kind === "group") {
      result = await api.setSkillGroupArchived(recordId, !archived, state.skillsRegistry.revision);
      state.selectedRegistryGroupId = recordId;
      state.selectedRegistrySkillId = null;
      state.registryGroupEditing = false;
      state.registryVisibility = archived ? "active" : "archived";
      state.registrySkillVisibility = archived ? "active" : "archived";
    } else {
      result = await api.setRegistrySkillArchived(recordId, !archived, state.skillsRegistry.revision);
      state.selectedRegistrySkillId = recordId;
      state.registrySkillVisibility = archived ? "active" : "archived";
    }
    historicalCompetencyExplorerLoaded = false;
    state.historicalCompetencyExplorer = null;
    await loadSkillsRegistry({ keepMessage: true });
    await loadCompetencyExplorer({ force: true }).catch(() => null);
    const affected = Number(result?.affected_skill_count || 0);
    elements.skillsRegistryMessage.textContent = kind === "group"
      ? `Group ${archived ? "restored" : "archived"}. ${affected} skill${affected === 1 ? "" : "s"} ${archived ? "restored with it" : "archived with it"}.`
      : `Skill ${archived ? "restored" : "archived"}.`;
  } catch (error) {
    elements.skillsRegistryMessage.className = "form-message error";
    elements.skillsRegistryMessage.textContent = formatRequestError(error);
  }
}

export function populateSkillQuickGroups(preferredGroupId = "") {
  const groups = (state.skillsRegistry?.groups || []).filter((group) => group.archived !== true);
  const effectiveGroupId = preferredGroupId || state.skillsRegistry?.default_custom_group_id || "";
  elements.skillQuickGroup.innerHTML = groups.map((group) => (
    `<option value="${escapeHtml(group.id)}" translate="no">${escapeHtml(group.name)} (${escapeHtml(group.origin)})</option>`
  )).join("");
  if (effectiveGroupId && groups.some((group) => group.id === effectiveGroupId)) {
    elements.skillQuickGroup.value = effectiveGroupId;
  }
}

export async function loadSkillsRegistry({ keepMessage = false } = {}) {
  try {
    state.skillsRegistry = await api.getSkillsRegistry();
    const groups = state.skillsRegistry.groups || [];
    if (state.selectedRegistrySkillId && !registrySkill(state.selectedRegistrySkillId)) {
      state.selectedRegistrySkillId = null;
    }
    if (!state.selectedRegistryGroupId || !registryGroup(state.selectedRegistryGroupId)) {
      state.selectedRegistryGroupId = groups.find(
        (group) => (group.archived === true) === (state.registryVisibility === "archived")
      )?.id
        || state.skillsRegistry.default_custom_group_id
        || null;
    }
    renderSkillsRegistry();
    populateSkillQuickGroups(elements.skillQuickGroup?.value || "");
    if (state.competencyExplorer) {
      populateCompetencyFilters();
      renderCompetencyExplorer();
    }
    loadHistoricalEvidenceIfNeeded(registrySkill(state.selectedRegistrySkillId));
    if (!keepMessage && elements.skillsRegistryMessage) {
      const notices = state.skillsRegistry.compatibility_notices || [];
      const activeCount = groups
        .filter((group) => !group.archived)
        .reduce(
          (count, group) => count + (group.skills || []).filter((skill) => !skill.archived).length,
          0,
        );
      elements.skillsRegistryMessage.className = notices.length
        ? "form-message warning"
        : "form-message";
      elements.skillsRegistryMessage.textContent = notices.length
        ? `${activeCount} active registry entries. ${notices.length} catalog compatibility adjustment${notices.length === 1 ? " was" : "s were"} applied without changing private vocabulary.`
        : `${activeCount} active registry entries available to both intake workflows.`;
    }
    return state.skillsRegistry;
  } catch (error) {
    if (elements.skillsRegistryMessage) {
      elements.skillsRegistryMessage.className = "form-message error";
      elements.skillsRegistryMessage.textContent = formatRequestError(error);
    }
    throw error;
  }
}

export function competencyLabel(value) {
  if (!value) return "Unclassified";
  return String(value)
    .replaceAll("_", " ")
    .replace(/\b\w/g, (character) => character.toLocaleUpperCase());
}

function competencyLastUsed(item) {
  if (item.active_now) return "Active now";
  if (!item.last_used) return "Not dated";
  const [year, month] = String(item.last_used).split("-").map(Number);
  if (!year || !month) return String(item.last_used);
  return new Intl.DateTimeFormat(undefined, { month: "short", year: "numeric" })
    .format(new Date(Date.UTC(year, month - 1, 1)));
}

function allSkillRecords() {
  const evidenceById = new Map(
    (state.competencyExplorer?.competencies || []).map((item) => [item.id, item])
  );
  const records = [];
  (state.skillsRegistry?.groups || []).forEach((group) => {
    if (group.archived === true) return;
    (group.skills || []).forEach((skill) => {
      if (skill.archived === true) return;
      const evidenceItem = evidenceById.get(skill.id) || {};
      records.push({
        ...evidenceItem,
        ...skill,
        primary_group_id: skill.primary_group_id || group.id,
        group_name: group.name,
        group_origin: group.origin,
        primary_domain: skill.primary_domain || evidenceItem.primary_domain || "",
        kind: skill.kind || evidenceItem.kind || "",
        evidence_count: Number(evidenceItem.evidence_count || 0),
        professional_context_count: Number(evidenceItem.professional_context_count || 0),
        project_context_count: Number(evidenceItem.project_context_count || 0),
        certification_count: Number(evidenceItem.certification_count || 0),
        proof_count: Number(evidenceItem.proof_count || 0),
        experience_months: Number(evidenceItem.experience_months || 0),
        first_used: evidenceItem.first_used || null,
        last_used: evidenceItem.last_used || null,
        active_now: evidenceItem.active_now === true,
        evidence: evidenceItem.evidence || { experiences: [], projects: [], certifications: [] },
      });
    });
  });
  return records.sort((left, right) => left.name.localeCompare(right.name) || left.id.localeCompare(right.id));
}

function selectedCompetency() {
  return allSkillRecords().find(
    (item) => item.id === state.selectedCompetencyId
  ) || null;
}

function filteredCompetencies() {
  const query = (elements.competencySearch?.value || "").trim().toLocaleLowerCase();
  const group = elements.competencyGroupFilter?.value || "";
  const origin = elements.competencyOriginFilter?.value || "";
  const evidence = elements.competencyEvidenceFilter?.value || "";
  const domain = elements.competencyDomainFilter?.value || "";
  const kind = elements.competencyKindFilter?.value || "";
  return allSkillRecords().filter((item) => {
    if (group && item.primary_group_id !== group) return false;
    if (origin && item.origin !== origin) return false;
    if (evidence === "with" && !item.evidence_count) return false;
    if (evidence === "without" && item.evidence_count) return false;
    if (domain && item.primary_domain !== domain) return false;
    if (kind && item.kind !== kind) return false;
    if (!query) return true;
    return [item.name, item.id, item.group_name, ...(item.aliases || [])]
      .some((value) => String(value || "").toLocaleLowerCase().includes(query));
  });
}

function renderCompetencySummary() {
  if (!elements.competencySummary) return;
  const records = allSkillRecords();
  const summary = state.competencyExplorer?.summary || {};
  const metrics = [
    [records.length, "active registry entries"],
    [records.filter((item) => item.evidence_count > 0).length, "with evidence"],
    [summary.professional_context_count || 0, "professional contexts"],
    [records.filter((item) => item.active_now).length, "active now"],
  ];
  elements.competencySummary.innerHTML = metrics.map(([value, label]) => (
    `<div><strong>${Number(value).toLocaleString()}</strong><span>${escapeHtml(label)}</span></div>`
  )).join("");
}

function renderCompetencyEvidenceSection(title, items) {
  const rows = (items || []).map((item) => {
    const relationship = item.direct ? "Direct" : "Via project";
    const context = item.context || (
      item.is_independent_project ? "Independent project" : "Canonical Career Vault record"
    );
    return `<div class="competency-evidence-row">
      <div class="competency-evidence-copy">
        <strong>${escapeHtml(item.title)}</strong>
        <span>${escapeHtml(context)}</span>
      </div>
      <span class="competency-evidence-relationship">${relationship}</span>
      <button type="button" class="text-button competency-open-record" data-competency-record-view="${escapeHtml(item.view)}" data-competency-record-id="${escapeHtml(item.record_id)}">
        <span>Open record</span><svg aria-hidden="true" viewBox="0 0 24 24"><path d="M5 12h13m-5-5 5 5-5 5"></path></svg>
      </button>
    </div>`;
  }).join("");
  return `<section class="competency-evidence-section">
    <div class="competency-evidence-heading"><h3>${escapeHtml(title)}</h3><span>${items.length}</span></div>
    ${rows || '<p class="competency-evidence-empty">No linked records.</p>'}
  </section>`;
}

function renderCompetencyDetail() {
  if (!elements.competencyDetail) return;
  const item = selectedCompetency();
  if (!item) {
    elements.competencyDetail.innerHTML = `<div class="competency-detail-empty">
      <strong>No skill selected</strong>
      <span>Adjust the filters or choose a skill from the list.</span>
    </div>`;
    return;
  }
  const aliases = (item.aliases || []).length
    ? item.aliases.map((alias) => `<span>${escapeHtml(alias)}</span>`).join("")
    : '<span class="is-empty">No aliases</span>';
  const months = Number(item.experience_months || 0);
  const duration = months ? `${months.toLocaleString()} month${months === 1 ? "" : "s"}` : "Not dated";
  const origin = registryOriginClass(item.origin);
  const evidence = item.evidence || { experiences: [], projects: [], certifications: [] };
  elements.competencyDetail.innerHTML = `<div class="competency-detail-shell">
    <button type="button" class="competency-list-return" data-competency-list-return>
      ${registryChevronIcon("left")}<span>All skills</span>
    </button>
    <header class="competency-detail-header">
      <div>
        <span class="competency-origin is-${origin}"><i aria-hidden="true"></i>${registryOriginLabel(item.origin)} <small>${registryOriginOwnership(item.origin)}</small></span>
        <h2>${escapeHtml(item.name)}</h2>
        <code>${escapeHtml(item.id)}</code>
      </div>
      <div class="competency-detail-header-actions">
        ${item.active_now ? '<span class="competency-active-state"><i aria-hidden="true"></i>Active now</span>' : ""}
        <button type="button" class="secondary-button" data-manage-registry-skill="${escapeHtml(item.id)}">Manage skill</button>
      </div>
    </header>
    <div class="competency-aliases" aria-label="Aliases">${aliases}</div>
    <dl class="competency-classification">
      <div><dt>Domain</dt><dd>${escapeHtml(competencyLabel(item.primary_domain))}</dd></div>
      <div><dt>Kind</dt><dd>${escapeHtml(competencyLabel(item.kind))}</dd></div>
      <div><dt>Primary group</dt><dd class="competency-group-value"><strong>${escapeHtml(item.group_name || "Unassigned")}</strong><code>${escapeHtml(item.primary_group_id)}</code></dd></div>
    </dl>
    <div class="competency-detail-metrics">
      <div><strong>${Number(item.evidence_count || 0).toLocaleString()}</strong><span>evidence records</span></div>
      <div><strong>${Number(item.professional_context_count || 0).toLocaleString()}</strong><span>professional contexts</span></div>
      <div><strong>${escapeHtml(duration)}</strong><span>dated evidence</span></div>
      <div><strong>${escapeHtml(competencyLastUsed(item))}</strong><span>last used</span></div>
    </div>
    ${renderCompetencyEvidenceSection("Experience", evidence.experiences || [])}
    ${renderCompetencyEvidenceSection("Projects", evidence.projects || [])}
    ${renderCompetencyEvidenceSection("Certifications", evidence.certifications || [])}
  </div>`;
}

function renderCompetencyExplorer() {
  if (!elements.competencyList) return;
  renderCompetencySummary();
  const items = filteredCompetencies();
  if (!items.some((item) => item.id === state.selectedCompetencyId)) {
    state.selectedCompetencyId = items[0]?.id || null;
  }
  elements.competencyList.innerHTML = items.length ? items.map((item) => {
    const selected = item.id === state.selectedCompetencyId;
    const origin = registryOriginClass(item.origin);
    const evidenceCount = Number(item.evidence_count || 0);
    const evidenceLabel = evidenceCount
      ? `${evidenceCount} evidence record${evidenceCount === 1 ? "" : "s"}`
      : "No Career Vault evidence";
    return `<button type="button" class="competency-row${selected ? " is-selected" : ""}" role="option" aria-selected="${selected}" data-competency-id="${escapeHtml(item.id)}">
      <span class="competency-row-name"><strong>${escapeHtml(item.name)}</strong><span class="competency-origin is-${origin}"><i aria-hidden="true"></i>${registryOriginLabel(item.origin)}</span></span>
      <span class="competency-row-classification"><strong>${escapeHtml(item.group_name || "Unassigned")}</strong><span>${escapeHtml(competencyLabel(item.kind))}</span></span>
      <span class="competency-row-evidence${evidenceCount ? "" : " is-empty"}" title="${escapeHtml(evidenceLabel)}">${evidenceCount ? evidenceCount.toLocaleString() : "No evidence"}</span>
      <span class="competency-row-last-used">${escapeHtml(competencyLastUsed(item))}</span>
      ${registryChevronIcon()}
    </button>`;
  }).join("") : `<div class="competency-list-empty">
    <strong>No skills match</strong><span>Try a broader search or remove one of the filters.</span>
  </div>`;
  const total = allSkillRecords().length;
  elements.competencyResultCount.textContent = items.length === total
    ? `${total.toLocaleString()} active registry entries`
    : `${items.length.toLocaleString()} of ${total.toLocaleString()} active registry entries`;
  renderCompetencyDetail();
}

function populateCompetencyFilters() {
  if (!elements.competencyDomainFilter || !elements.competencyKindFilter) return;
  const records = allSkillRecords();
  const currentGroup = elements.competencyGroupFilter.value;
  const currentDomain = elements.competencyDomainFilter.value;
  const currentKind = elements.competencyKindFilter.value;
  const groups = [...new Map(records.map((item) => [item.primary_group_id, item.group_name])).entries()]
    .sort((left, right) => String(left[1]).localeCompare(String(right[1])));
  const domains = [...new Set(records.map((item) => item.primary_domain).filter(Boolean))].sort();
  const kinds = [...new Set(records.map((item) => item.kind).filter(Boolean))].sort();
  elements.competencyGroupFilter.innerHTML = '<option value="">All groups</option>'
    + groups.map(([id, name]) => `<option value="${escapeHtml(id)}" translate="no">${escapeHtml(name)}</option>`).join("");
  elements.competencyDomainFilter.innerHTML = '<option value="">All domains</option>'
    + domains.map((value) => `<option value="${escapeHtml(value)}" translate="no">${escapeHtml(competencyLabel(value))}</option>`).join("");
  elements.competencyKindFilter.innerHTML = '<option value="">All kinds</option>'
    + kinds.map((value) => `<option value="${escapeHtml(value)}" translate="no">${escapeHtml(competencyLabel(value))}</option>`).join("");
  if (groups.some(([id]) => id === currentGroup)) elements.competencyGroupFilter.value = currentGroup;
  if (domains.includes(currentDomain)) elements.competencyDomainFilter.value = currentDomain;
  if (kinds.includes(currentKind)) elements.competencyKindFilter.value = currentKind;
}

async function loadCompetencyExplorer({ force = false } = {}) {
  if (competencyExplorerLoaded && !force) {
    renderCompetencyExplorer();
    return state.competencyExplorer;
  }
  elements.competencyMessage.className = "form-message";
  elements.competencyMessage.textContent = "Linking Career Vault evidence to the shared skills registry...";
  try {
    state.competencyExplorer = await api.getCompetencyExplorer();
    competencyExplorerLoaded = true;
    const items = allSkillRecords();
    if (!items.some((item) => item.id === state.selectedCompetencyId)) {
      state.selectedCompetencyId = items[0]?.id || null;
    }
    populateCompetencyFilters();
    renderCompetencyExplorer();
    elements.competencyMessage.textContent = `Evidence projection as of ${state.competencyExplorer.as_of_month}. Registry definitions remain editable in Groups.`;
    return state.competencyExplorer;
  } catch (error) {
    competencyExplorerLoaded = false;
    elements.competencyMessage.className = "form-message error";
    elements.competencyMessage.textContent = formatRequestError(error);
    throw error;
  }
}

function competencyProjectionMatchesRegistry(projection) {
  return Boolean(
    projection
    && state.skillsRegistry
    && projection.catalog_revision === state.skillsRegistry.catalog_revision
  );
}

async function loadHistoricalCompetencyExplorer({ force = false } = {}) {
  if (
    historicalCompetencyExplorerLoaded
    && !force
    && competencyProjectionMatchesRegistry(state.historicalCompetencyExplorer)
  ) {
    renderSkillsRegistryDetail();
    return state.historicalCompetencyExplorer;
  }
  if (
    historicalCompetencyExplorerLoaded
    && !competencyProjectionMatchesRegistry(state.historicalCompetencyExplorer)
  ) {
    historicalCompetencyExplorerLoaded = false;
    state.historicalCompetencyExplorer = null;
  }
  if (historicalCompetencyExplorerPromise && !force) {
    return historicalCompetencyExplorerPromise;
  }
  historicalCompetencyExplorerPromise = api.getCompetencyExplorer({ includeArchived: true })
    .then((projection) => {
      if (projection.include_archived_competencies !== true) {
        throw new Error("Historical evidence scope was not returned by the local API.");
      }
      if (!competencyProjectionMatchesRegistry(projection)) {
        throw new Error(
          "Skills Registry changed while historical evidence was loading. Refresh and try again."
        );
      }
      state.historicalCompetencyExplorer = projection;
      historicalCompetencyExplorerLoaded = true;
      renderSkillsRegistryDetail();
      return projection;
    })
    .catch((error) => {
      historicalCompetencyExplorerLoaded = false;
      state.historicalCompetencyExplorer = null;
      elements.skillsRegistryMessage.className = "form-message error";
      elements.skillsRegistryMessage.textContent = formatRequestError(error);
      return null;
    })
    .finally(() => {
      historicalCompetencyExplorerPromise = null;
    });
  return historicalCompetencyExplorerPromise;
}

function loadHistoricalEvidenceIfNeeded(skill) {
  if (skill?.archived === true) void loadHistoricalCompetencyExplorer();
}

export async function loadSkillsHub({ force = false } = {}) {
  await Promise.all([
    loadSkillsRegistry(),
    loadCompetencyExplorer({ force: force || competencyExplorerLoaded }),
  ]);
  if (!competencyProjectionMatchesRegistry(state.competencyExplorer)) {
    await loadSkillsRegistry();
    await loadCompetencyExplorer({ force: true });
  }
  if (!competencyProjectionMatchesRegistry(state.competencyExplorer)) {
    throw new Error(
      "Skills Registry changed while evidence was loading. Refresh the view and try again."
    );
  }
  populateCompetencyFilters();
  renderSkillsRegistry();
  renderCompetencyExplorer();
}

function clearCompetencyFilters() {
  elements.competencySearch.value = "";
  elements.competencyGroupFilter.value = "";
  elements.competencyOriginFilter.value = "";
  elements.competencyEvidenceFilter.value = "";
  elements.competencyDomainFilter.value = "";
  elements.competencyKindFilter.value = "";
}

export function openSkillEvidence(skillId) {
  if (!allSkillRecords().some((item) => item.id === skillId)) return;
  clearCompetencyFilters();
  state.selectedCompetencyId = skillId;
  setSkillsHubMode("all");
}

function manageRegistrySkill(skillId) {
  const skill = registrySkill(skillId);
  if (!skill) return;
  state.registryVisibility = "active";
  state.registrySkillVisibility = "active";
  state.selectedRegistryGroupId = skill.primary_group_id;
  state.selectedRegistrySkillId = skill.id;
  state.registryGroupEditing = false;
  state.registryCardDetailOpen = state.skillsRegistryLayout === "cards";
  setSkillsHubMode("groups");
}

function selectCompetency(competencyId) {
  if (!allSkillRecords().some((item) => item.id === competencyId)) return;
  state.selectedCompetencyId = competencyId;
  renderCompetencyExplorer();
  if (window.matchMedia("(max-width: 980px)").matches) {
    elements.competencyDetail.scrollIntoView({ behavior: "smooth", block: "start" });
  }
}

async function openCompetencyRecord(viewName, recordId) {
  if (!(await navigateToView(viewName))) return;
  try {
    if (viewName === "vault") {
      state.vaultVisibility = "active";
      elements.vaultSearch.value = "";
      elements.vaultCategoryFilter.value = "";
      await loadVaultExperiences();
      selectVaultExperience(recordId);
    } else if (viewName === "projects") {
      state.projectVisibility = "active";
      elements.projectSearch.value = "";
      elements.projectCategoryFilter.value = "";
      await loadVaultProjects();
      selectVaultProject(recordId);
    } else if (viewName === "education") {
      state.educationType = "certification";
      state.educationVisibility = "active";
      elements.educationSearch.value = "";
      await loadVaultEducation();
      selectEducationRecord(recordId);
    }
  } catch (error) {
    elements.runMessage.className = "form-message error";
    elements.runMessage.textContent = formatRequestError(error);
  }
}

function applyCompetencyFilters() {
  state.selectedCompetencyId = null;
  renderCompetencyExplorer();
}

export function initSkillsHub(deps = {}) {
  ({ loadVaultEducation, loadVaultExperiences, loadVaultProjects, localRouteForView, navigateToView, openSkillQuickAction, selectEducationRecord, selectVaultExperience, selectVaultProject } = deps);
  elements.skillsHubModeButtons.forEach((button, index) => {
    button.addEventListener("click", () => setSkillsHubMode(button.dataset.skillsHubMode));
    button.addEventListener("keydown", (event) => {
      if (!["ArrowLeft", "ArrowRight"].includes(event.key)) return;
      event.preventDefault();
      const offset = event.key === "ArrowRight" ? 1 : -1;
      const next = elements.skillsHubModeButtons[(index + offset + elements.skillsHubModeButtons.length) % elements.skillsHubModeButtons.length];
      setSkillsHubMode(next.dataset.skillsHubMode);
      next.focus();
    });
  });
  elements.competencySearch.addEventListener("input", applyCompetencyFilters);
  elements.competencyGroupFilter.addEventListener("change", applyCompetencyFilters);
  elements.competencyOriginFilter.addEventListener("change", applyCompetencyFilters);
  elements.competencyEvidenceFilter.addEventListener("change", applyCompetencyFilters);
  elements.competencyDomainFilter.addEventListener("change", applyCompetencyFilters);
  elements.competencyKindFilter.addEventListener("change", applyCompetencyFilters);
  elements.competencyList.addEventListener("click", (event) => {
    const row = event.target.closest("[data-competency-id]");
    if (row) selectCompetency(row.dataset.competencyId);
  });
  elements.competencyDetail.addEventListener("click", (event) => {
    const manageSkill = event.target.closest("[data-manage-registry-skill]");
    if (manageSkill) {
      manageRegistrySkill(manageSkill.dataset.manageRegistrySkill);
      return;
    }
    const record = event.target.closest("[data-competency-record-id]");
    if (record) {
      void openCompetencyRecord(
        record.dataset.competencyRecordView,
        record.dataset.competencyRecordId,
      );
      return;
    }
    if (event.target.closest("[data-competency-list-return]")) {
      elements.competencySearch.focus();
      elements.competencyList.scrollIntoView({ behavior: "smooth", block: "start" });
    }
  });
  elements.skillsRegistryLayoutButtons.forEach((button) => {
    button.addEventListener("click", () => {
      setSkillsRegistryLayout(button.dataset.registryLayout);
      renderSkillsRegistry();
    });
  });
  elements.skillsRegistrySort.addEventListener("change", () => {
    state.registryGroupSort = elements.skillsRegistrySort.value;
    renderSkillsRegistry();
  });
  elements.skillsRegistryArchivedFilter.addEventListener("click", () => {
    state.registryVisibility = state.registryVisibility === "archived" ? "active" : "archived";
    state.registrySkillVisibility = state.registryVisibility;
    state.selectedRegistrySkillId = null;
    state.selectedRegistryGroupId = null;
    state.registryCardDetailOpen = false;
    renderSkillsRegistry();
  });
  elements.skillsRegistrySearch.addEventListener("input", () => {
    state.selectedRegistrySkillId = null;
    state.registryGroupEditing = false;
    state.registryCardDetailOpen = false;
    renderSkillsRegistry();
  });
  elements.skillsRegistryGrid.addEventListener("click", (event) => {
    const skillButton = event.target.closest("[data-registry-skill-id]");
    if (skillButton) {
      const skill = registrySkill(skillButton.dataset.registrySkillId);
      state.selectedRegistrySkillId = skill?.id || null;
      state.selectedRegistryGroupId = skill?.primary_group_id || state.selectedRegistryGroupId;
      state.registryGroupEditing = false;
      renderSkillsRegistry();
      loadHistoricalEvidenceIfNeeded(skill);
      return;
    }
    const groupButton = event.target.closest("[data-registry-group-id]");
    if (groupButton) {
      state.selectedRegistryGroupId = groupButton.dataset.registryGroupId;
      state.registrySkillVisibility = registryGroup(groupButton.dataset.registryGroupId)?.archived
        ? "archived"
        : "active";
      state.selectedRegistrySkillId = null;
      state.registryGroupEditing = false;
      state.registryCardDetailOpen = state.skillsRegistryLayout === "cards";
      renderSkillsRegistry();
    }
  });
  elements.skillsRegistryDetail.addEventListener("submit", saveSkillsRegistryDetail);
  elements.skillsRegistryDetail.addEventListener("click", (event) => {
    const openEvidence = event.target.closest("[data-open-skill-evidence]");
    if (openEvidence) {
      openSkillEvidence(openEvidence.dataset.openSkillEvidence);
      return;
    }
    const evidenceRecord = event.target.closest("[data-competency-record-id]");
    if (evidenceRecord) {
      void openCompetencyRecord(
        evidenceRecord.dataset.competencyRecordView,
        evidenceRecord.dataset.competencyRecordId,
      );
      return;
    }
    const archiveFilter = event.target.closest("[data-registry-skills-archive-filter]");
    if (archiveFilter) {
      state.registrySkillVisibility = state.registrySkillVisibility === "archived" ? "active" : "archived";
      state.selectedRegistrySkillId = null;
      renderSkillsRegistryDetail();
      return;
    }
    const closeCardDetailButton = event.target.closest("[data-close-registry-card-detail]");
    if (closeCardDetailButton) {
      state.registryCardDetailOpen = false;
      state.selectedRegistrySkillId = null;
      state.registryGroupEditing = false;
      renderSkillsRegistry();
      return;
    }
    const skillButton = event.target.closest("[data-registry-skill-id]");
    if (skillButton) {
      const skill = registrySkill(skillButton.dataset.registrySkillId);
      state.selectedRegistrySkillId = skill?.id || null;
      state.selectedRegistryGroupId = skill?.primary_group_id || state.selectedRegistryGroupId;
      state.registryGroupEditing = false;
      renderSkillsRegistry();
      loadHistoricalEvidenceIfNeeded(skill);
      return;
    }
    const backButton = event.target.closest("[data-registry-back-group]");
    if (backButton) {
      state.selectedRegistryGroupId = backButton.dataset.registryBackGroup;
      state.selectedRegistrySkillId = null;
      state.registryGroupEditing = false;
      renderSkillsRegistry();
      return;
    }
    const editGroupButton = event.target.closest("[data-edit-registry-group]");
    if (editGroupButton) {
      state.registryGroupEditing = true;
      renderSkillsRegistryDetail();
      elements.skillsRegistryDetail.querySelector('input[name="name"]')?.focus();
      return;
    }
    const cancelGroupEditButton = event.target.closest("[data-cancel-registry-group-edit]");
    if (cancelGroupEditButton) {
      state.registryGroupEditing = false;
      renderSkillsRegistryDetail();
      return;
    }
    const addButton = event.target.closest("[data-add-skill-to-group]");
    if (addButton) {
      void openSkillQuickAction("skill", {
        preferredGroupId: addButton.dataset.addSkillToGroup,
      });
      return;
    }
    const archiveButton = event.target.closest("[data-registry-archive-kind]");
    if (archiveButton) {
      void toggleSkillsRegistryArchive(
        archiveButton.dataset.registryArchiveKind,
        archiveButton.dataset.registryArchiveId,
        archiveButton.dataset.registryArchived === "true",
      );
    }
  });
  elements.skillsRegistryDetail.addEventListener("change", (event) => {
    const sort = event.target.closest("[data-registry-skill-sort]");
    if (!sort) return;
    state.registrySkillSort = sort.value;
    renderSkillsRegistryDetail();
  });
}
