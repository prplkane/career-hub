// Manual skill selector shared by the experience and project editors.
import { elements, state } from "../app_context.js";
import { escapeHtml } from "../ui/primitives.js";
import { registrySkillIndex } from "./skills_hub.js";

function manualSkillNodes(kind) {
  return kind === "experience"
    ? { pills: elements.vaultSkillPills, input: elements.vaultSkillInput, options: elements.vaultSkillOptions, pending: elements.vaultPendingSkills, count: elements.vaultSkillCount }
    : { pills: elements.projectSkillPills, input: elements.projectSkillInput, options: elements.projectSkillOptions, pending: elements.projectPendingSkills, count: elements.projectSkillCount };
}

function manualSkillNameIndex() {
  const index = new Map();
  registrySkillIndex().forEach((skill) => {
    [skill.name, ...(skill.aliases || [])].forEach((name) => index.set(String(name).trim().toLocaleLowerCase(), skill));
  });
  return index;
}

export function setManualSkillsFromRecord(kind, record) {
  const selection = state.manualSkills[kind];
  const skills = registrySkillIndex();
  const names = manualSkillNameIndex();
  selection.ids = [...new Set((record?.competency_ids || []).filter((id) => skills.has(id)))];
  selection.pending = [];
  (record?.technologies || []).forEach((name) => {
    const matched = names.get(String(name).trim().toLocaleLowerCase());
    if (matched && !selection.ids.includes(matched.id)) selection.ids.push(matched.id);
    if (!matched) selection.pending.push({ name, primary_group_id: state.skillsRegistry?.default_custom_group_id || null });
  });
  renderManualSkillSelector(kind);
}

function renderManualSkillSelector(kind) {
  const nodes = manualSkillNodes(kind);
  const selection = state.manualSkills[kind];
  const skills = registrySkillIndex();
  const groups = (state.skillsRegistry?.groups || []).filter((group) => !group.archived);
  nodes.count.textContent = `${selection.ids.length + selection.pending.length} selected`;
  nodes.options.innerHTML = [...new Set([...skills.values()].filter((skill) => !skill.archived).flatMap((skill) => [skill.name, ...(skill.aliases || [])]))].sort().map((name) => `<option value="${escapeHtml(name)}"></option>`).join("");
  nodes.pills.innerHTML = selection.ids.length ? selection.ids.map((id) => {
    const skill = skills.get(id);
    const archived = skill?.archived === true;
    const title = archived
      ? `${skill?.group?.name || "Skills Registry"} · archived historical link`
      : skill?.group?.name || "Skills Registry";
    return `<span class="review-skill-pill registry-skill-${escapeHtml(skill?.origin || "core")}${archived ? " is-archived" : ""}" title="${escapeHtml(title)}"><span>${escapeHtml(skill?.name || id)}${archived ? '<small>Archived</small>' : ""}</span><button type="button" data-manual-skill-remove="${escapeHtml(id)}" aria-label="Remove ${escapeHtml(skill?.name || id)}">&times;</button></span>`;
  }).join("") : (selection.pending.length ? "" : '<span class="empty-state compact">No skills selected.</span>');
  nodes.pending.innerHTML = selection.pending.length ? `<p class="section-kicker">New custom skills on save</p>${selection.pending.map((item, index) => `<div class="review-pending-skill"><span class="review-skill-pill registry-skill-custom"><span>${escapeHtml(item.name)}</span><button type="button" data-manual-pending-remove="${index}" aria-label="Remove ${escapeHtml(item.name)}">&times;</button></span><label><span>Group</span><select data-manual-pending-group="${index}">${groups.map((group) => `<option value="${escapeHtml(group.id)}" ${group.id === item.primary_group_id ? "selected" : ""} translate="no">${escapeHtml(group.name)} · ${escapeHtml(group.origin)}</option>`).join("")}</select></label></div>`).join("")}` : "";
}

function addManualSkills(kind) {
  const nodes = manualSkillNodes(kind);
  const selection = state.manualSkills[kind];
  const names = manualSkillNameIndex();
  String(nodes.input.value || "").split(/[,;\n]+/).map((value) => value.trim().replace(/\s+/g, " ")).filter(Boolean).forEach((name) => {
    const matched = names.get(name.toLocaleLowerCase());
    if (matched) {
      if (!selection.ids.includes(matched.id)) selection.ids.push(matched.id);
      selection.pending = selection.pending.filter((item) => item.name.toLocaleLowerCase() !== name.toLocaleLowerCase());
    } else if (!selection.pending.some((item) => item.name.toLocaleLowerCase() === name.toLocaleLowerCase())) {
      selection.pending.push({ name, primary_group_id: state.skillsRegistry?.default_custom_group_id || null });
    }
  });
  nodes.input.value = "";
  if (kind === "experience") state.vaultFormDirty = true; else state.projectFormDirty = true;
  renderManualSkillSelector(kind);
}

export function manualSkillRecordFields(kind) {
  const selection = state.manualSkills[kind];
  const skills = registrySkillIndex();
  return {
    competency_ids: [...selection.ids],
    pending_skills: selection.pending.map((item) => ({ ...item })),
    registry_revision: state.skillsRegistry?.revision || "",
    technologies: selection.ids.map((id) => skills.get(id)?.name).filter(Boolean),
  };
}

function bindManualSkillSelector(kind, addButton) {
  const nodes = manualSkillNodes(kind);
  addButton.addEventListener("click", () => addManualSkills(kind));
  nodes.input.addEventListener("keydown", (event) => {
    if (event.key === "Enter" || event.key === ",") { event.preventDefault(); addManualSkills(kind); }
  });
  const root = nodes.pills.closest("[data-vault-skill-selector]");
  root.addEventListener("click", (event) => {
    const removeId = event.target.closest("[data-manual-skill-remove]");
    const removePending = event.target.closest("[data-manual-pending-remove]");
    if (removeId) state.manualSkills[kind].ids = state.manualSkills[kind].ids.filter((id) => id !== removeId.dataset.manualSkillRemove);
    if (removePending) state.manualSkills[kind].pending.splice(Number(removePending.dataset.manualPendingRemove), 1);
    if (removeId || removePending) { if (kind === "experience") state.vaultFormDirty = true; else state.projectFormDirty = true; renderManualSkillSelector(kind); }
  });
  root.addEventListener("change", (event) => {
    const select = event.target.closest("[data-manual-pending-group]");
    if (!select) return;
    state.manualSkills[kind].pending[Number(select.dataset.manualPendingGroup)].primary_group_id = select.value;
    if (kind === "experience") state.vaultFormDirty = true; else state.projectFormDirty = true;
  });
}

export function initVaultManualSkills() {
  bindManualSkillSelector("experience", elements.addVaultSkills);
  bindManualSkillSelector("project", elements.addProjectSkills);
}
