// Quick-add dialog for registry skills and groups, reachable from the registry, job workspace and intake review.
import { elements, state, toast } from "../app_context.js";
import { api } from "../ui/api.js";
import { escapeHtml, formatRequestError } from "../ui/primitives.js";
import {
  currentReview,
  markReviewDirty,
  normalizeReviewSkillName,
  renderCardEditor,
} from "./intake_review.js";
import {
  loadSkillsRegistry,
  populateSkillQuickGroups,
  registrySkill,
  renderSkillsRegistry,
} from "./skills_hub.js";
import { vaultLines } from "./vault_shared.js";

// Bootstrap hooks supplied by app.js through initSkillQuickAction().
let persistJobFacts = null;
let reanalyzeSelectedJob = null;

function renderSkillQuickNames() {
  const creatingGroup = state.skillQuickMode === "group";
  const names = state.skillQuickNames || [];
  const multiple = names.length > 1;
  elements.skillQuickPills.hidden = creatingGroup || names.length === 0;
  elements.skillQuickPills.innerHTML = creatingGroup ? "" : names.map((name, index) => `
    <span class="review-skill-pill registry-skill-custom">
      <span>${escapeHtml(name)}</span>
      <button type="button" data-remove-skill-quick-name="${index}" aria-label="Remove ${escapeHtml(name)}">&times;</button>
    </span>`).join("");
  elements.skillQuickName.required = creatingGroup || names.length === 0;
  elements.skillQuickNameHelp.hidden = creatingGroup;
  elements.skillQuickAliasesField.hidden = creatingGroup || multiple;
  elements.skillQuickAliases.disabled = creatingGroup || multiple;
  if (multiple) elements.skillQuickAliases.value = "";
  elements.saveSkillQuickAction.textContent = creatingGroup
    ? "Add group"
    : names.length > 1
      ? `Add ${names.length} skills`
      : "Add skill";
}

function addSkillQuickNames(rawValue) {
  const values = String(rawValue || "")
    .split(/[,;\n]+/)
    .map((value) => value.trim().replace(/\s+/g, " "))
    .filter(Boolean);
  if (!values.length) return false;
  const tooLong = values.find((value) => value.length > 160);
  if (tooLong) {
    elements.skillQuickMessage.className = "form-message error";
    elements.skillQuickMessage.textContent = `Skill labels must be 160 characters or fewer: ${tooLong.slice(0, 40)}…`;
    return false;
  }
  const names = [...(state.skillQuickNames || [])];
  const normalized = new Set(names.map(normalizeReviewSkillName));
  values.forEach((value) => {
    const key = normalizeReviewSkillName(value);
    if (!normalized.has(key)) {
      normalized.add(key);
      names.push(value);
    }
  });
  if (names.length > 100) {
    elements.skillQuickMessage.className = "form-message error";
    elements.skillQuickMessage.textContent = "Add no more than 100 skills at once.";
    return false;
  }
  state.skillQuickNames = names;
  elements.skillQuickName.value = "";
  elements.skillQuickMessage.className = "form-message";
  elements.skillQuickMessage.textContent = "";
  renderSkillQuickNames();
  return true;
}

function setSkillQuickMode(mode) {
  state.skillQuickMode = mode === "group" ? "group" : "skill";
  const creatingGroup = state.skillQuickMode === "group";
  elements.skillQuickTitle.textContent = creatingGroup
    ? "Add skill group"
    : state.skillQuickContext?.jobId
      ? "Add skill to this job"
      : "Add skill";
  elements.skillQuickNameLabel.textContent = creatingGroup ? "Group name" : "Skill name(s)";
  elements.skillQuickName.placeholder = creatingGroup ? "Office Automation" : "Type a skill, then comma or Enter";
  elements.skillQuickGroupField.hidden = creatingGroup;
  elements.skillQuickGroup.required = !creatingGroup;
  elements.skillQuickJobLevelField.classList.toggle("hidden", creatingGroup || !state.skillQuickContext?.jobId);
  renderSkillQuickNames();
  document.querySelectorAll("[data-skill-quick-mode]").forEach((button) => {
    button.classList.toggle("active", button.dataset.skillQuickMode === state.skillQuickMode);
  });
}

export async function openSkillQuickAction(mode = "skill", context = null) {
  state.skillQuickContext = context;
  const attachingToJob = Boolean(context?.jobId);
  elements.skillQuickJobLevelField.classList.toggle("hidden", !attachingToJob);
  elements.skillQuickMessage.className = "form-message";
  elements.skillQuickMessage.textContent = "Loading Skills Registry...";
  elements.skillQuickName.value = "";
  elements.skillQuickAliases.value = "";
  state.skillQuickNames = [];
  setSkillQuickMode(mode);
  if (attachingToJob) elements.skillQuickTitle.textContent = "Add skill to this job";
  if (!elements.skillQuickDialog.open) elements.skillQuickDialog.showModal();
  try {
    await loadSkillsRegistry({ keepMessage: true });
    populateSkillQuickGroups(context?.preferredGroupId || "");
    elements.skillQuickMessage.textContent = "";
    elements.skillQuickName.focus();
  } catch (error) {
    elements.skillQuickMessage.className = "form-message error";
    elements.skillQuickMessage.textContent = formatRequestError(error);
  }
}

function closeSkillQuickAction() {
  state.skillQuickContext = null;
  elements.skillQuickJobLevelField.classList.add("hidden");
  if (elements.skillQuickDialog.open) elements.skillQuickDialog.close();
}

async function attachRegistrySkillsToJob(jobId, records, level) {
  const job = state.jobs.find((item) => item.id === jobId);
  if (!job?.valid) throw new Error("The selected job is no longer available.");
  const field = level === "required"
    ? "required_skills"
    : level === "preferred"
      ? "preferred_skills"
      : "mentioned_technologies";
  const existing = field === "mentioned_technologies"
    ? (job.digest?.mentioned_technologies || [])
    : (job[field] || []);
  const names = records.map((record) => record.name).filter(Boolean);
  const values = [...new Set([...existing, ...names])];
  await persistJobFacts(job, { [field]: values });
}

async function saveSkillQuickAction(event) {
  event.preventDefault();
  if (state.skillQuickSaving || !state.skillsRegistry) return;
  state.skillQuickSaving = true;
  elements.saveSkillQuickAction.disabled = true;
  elements.skillQuickMessage.className = "form-message";
  elements.skillQuickMessage.textContent = "Validating Skills Registry...";
  try {
    let result;
    if (state.skillQuickMode === "group") {
      result = await api.createSkillGroup(
        state.skillsRegistry.revision,
        { name: elements.skillQuickName.value },
      );
      await loadSkillsRegistry({ keepMessage: true });
      const context = state.skillQuickContext;
      if (context?.reviewEntityId && Number.isInteger(context.pendingSkillIndex)) {
        const entity = currentReview()?.entities.find((item) => item.id === context.reviewEntityId);
        const pending = entity?.pending_skills?.[context.pendingSkillIndex];
        if (pending) {
          pending.primary_group_id = result.record.id;
          markReviewDirty();
          closeSkillQuickAction();
          renderCardEditor();
          return;
        }
      }
      setSkillQuickMode("skill");
      populateSkillQuickGroups(result.record.id);
      elements.skillQuickName.value = "";
      elements.skillQuickMessage.textContent = `Created ${result.record.name}. Add its first skill now.`;
      elements.skillQuickName.focus();
    } else {
      if (elements.skillQuickName.value.trim() && !addSkillQuickNames(elements.skillQuickName.value)) return;
      const names = [...state.skillQuickNames];
      if (!names.length) {
        elements.skillQuickMessage.className = "form-message error";
        elements.skillQuickMessage.textContent = "Add at least one skill name.";
        return;
      }
      const aliases = vaultLines(elements.skillQuickAliases.value, true);
      if (names.length === 1 && aliases.length) {
        const single = await api.createRegistrySkill(
          state.skillsRegistry.revision,
          {
            name: names[0],
            aliases,
            primary_group_id: elements.skillQuickGroup.value,
          },
        );
        result = { records: [single.record], created_skill_ids: [single.record.id], revision: single.revision };
      } else {
        result = await api.createRegistrySkills(
          state.skillsRegistry.revision,
          {
            names,
            primary_group_id: elements.skillQuickGroup.value,
          },
        );
      }
      await loadSkillsRegistry({ keepMessage: true });
      const selectedRecord = result.records.at(-1);
      const selectedSkill = selectedRecord ? registrySkill(selectedRecord.id) : null;
      state.selectedRegistryGroupId = selectedSkill?.group?.id || selectedRecord?.primary_group_id || elements.skillQuickGroup.value;
      state.selectedRegistrySkillId = selectedRecord?.id || null;
      renderSkillsRegistry();
      const context = state.skillQuickContext;
      if (context?.jobId) {
        await attachRegistrySkillsToJob(
          context.jobId,
          result.records,
          elements.skillQuickJobLevel.value,
        );
        const count = result.records.length;
        closeSkillQuickAction();
        toast.show({ message: `${count} skill${count === 1 ? "" : "s"} attached to this job.` });
        return;
      }
      const createdCount = result.created_skill_ids?.length || 0;
      const existingCount = result.records.length - createdCount;
      elements.skillQuickMessage.textContent = createdCount
        ? `Added ${createdCount} skill${createdCount === 1 ? "" : "s"}.${existingCount ? ` ${existingCount} already matched existing skills.` : ""} They are ready for resume and job matching.`
        : "All entered names already matched existing canonical skills.";
      if (state.activeView === "job-library") {
        const selected = state.jobs.find((job) => job.id === state.selectedJobId);
        if (selected?.status === "draft") await reanalyzeSelectedJob();
      }
      state.skillQuickNames = [];
      elements.skillQuickName.value = "";
      elements.skillQuickAliases.value = "";
      renderSkillQuickNames();
      elements.skillQuickName.focus();
    }
  } catch (error) {
    elements.skillQuickMessage.className = "form-message error";
    elements.skillQuickMessage.textContent = formatRequestError(error);
    if (error?.payload?.error_code === "SKILL_REGISTRY_REVISION_CONFLICT") {
      await loadSkillsRegistry({ keepMessage: true }).catch(() => null);
    }
  } finally {
    state.skillQuickSaving = false;
    elements.saveSkillQuickAction.disabled = false;
  }
}

export function initSkillQuickAction(deps = {}) {
  ({ persistJobFacts, reanalyzeSelectedJob } = deps);
  document.querySelectorAll("[data-open-skill-quick-action]").forEach((button) => {
    button.addEventListener("click", () => {
      void openSkillQuickAction(button.dataset.openSkillQuickAction);
    });
  });
  document.querySelectorAll("[data-skill-quick-mode]").forEach((button) => {
    button.addEventListener("click", () => setSkillQuickMode(button.dataset.skillQuickMode));
  });
  elements.skillQuickName.addEventListener("input", () => {
    if (state.skillQuickMode === "skill" && /[,;\n]/.test(elements.skillQuickName.value)) {
      addSkillQuickNames(elements.skillQuickName.value);
    }
  });
  elements.skillQuickName.addEventListener("keydown", (event) => {
    if (state.skillQuickMode !== "skill" || event.key !== "Enter" || !elements.skillQuickName.value.trim()) return;
    event.preventDefault();
    addSkillQuickNames(elements.skillQuickName.value);
  });
  elements.skillQuickPills.addEventListener("click", (event) => {
    const remove = event.target.closest("[data-remove-skill-quick-name]");
    if (!remove) return;
    state.skillQuickNames = state.skillQuickNames.filter(
      (_, index) => index !== Number(remove.dataset.removeSkillQuickName),
    );
    renderSkillQuickNames();
    elements.skillQuickName.focus();
  });
  elements.skillQuickForm.addEventListener("submit", saveSkillQuickAction);
  elements.closeSkillQuickDialog.addEventListener("click", closeSkillQuickAction);
  elements.cancelSkillQuickDialog.addEventListener("click", closeSkillQuickAction);
  elements.skillQuickDialog.addEventListener("click", (event) => {
    if (event.target === elements.skillQuickDialog) closeSkillQuickAction();
  });
}
