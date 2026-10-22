import { escapeHtml, renderEmptyState } from "../ui/primitives.js";

// Saving a review renders before the refreshed comparison request completes.
// Do not let the old preview authorize an Apply in that interval.
export function hasCurrentIntakeApprovalPreview(preview, detail) {
  const revision = detail?.review_revision;
  return typeof revision === "string" && revision.length > 0
    && preview?.review_revision === revision;
}

export const RESUME_SECTION_LABELS = {
  professional_summary: "Professional summary",
  technical_skills: "Technical skills",
  professional_experience: "Professional experience",
  projects: "Projects",
  education: "Education",
  certifications: "Certifications",
};

export function resumePlanRecord(editor, collection, blockId) {
  return editor?.catalog?.[collection]?.find((item) => item.id === blockId);
}

export function renderResumePlanIssues(issues) {
  if (!issues.length) {
    return renderEmptyState({
      title: "All required decisions are complete.",
      compact: true,
    });
  }
  return `<ul>${issues.map((issue) => `<li><strong>${escapeHtml(issue.message)}</strong>${issue.path ? `<code>${escapeHtml(issue.path)}</code>` : ""}</li>`).join("")}</ul>`;
}

export function renderResumePlanSections(sections) {
  return sections.map((section, index) => `
    <div class="resume-plan-section-row">
      <label><input type="checkbox" data-plan-section="${escapeHtml(section.key)}"${section.included ? " checked" : ""}> <strong>${escapeHtml(RESUME_SECTION_LABELS[section.key] || section.key)}</strong></label>
      <div class="resume-plan-order-actions">
        <button class="icon-button" type="button" data-plan-section-move="up" data-plan-section-key="${escapeHtml(section.key)}" aria-label="Move section up" data-tooltip="Move section up."${index === 0 ? " disabled" : ""}>&uarr;</button>
        <button class="icon-button" type="button" data-plan-section-move="down" data-plan-section-key="${escapeHtml(section.key)}" aria-label="Move section down" data-tooltip="Move section down."${index === sections.length - 1 ? " disabled" : ""}>&darr;</button>
      </div>
    </div>`).join("");
}

/** Pure rendering for the evidence-selection portion of the Resume Plan. */
export function renderResumePlanExperiences({ catalog, plan, attentionBlockId = null } = {}) {
  const experiencePlan = new Map((plan.experiences || []).map((item) => [item.block_id, item]));
  const experiences = catalog.experiences || [];
  return {
    count: `${experiences.length} records`,
    html: experiences.map((source) => {
      const item = experiencePlan.get(source.id);
      const included = item?.decision === "include";
      const selectedProofs = item?.proof_ids?.length || 0;
      const bulletTarget = Number(item?.target_bullets || 2);
      const missingProofs = included ? Math.max(bulletTarget - selectedProofs, 0) : 0;
      const needsAttention = attentionBlockId === source.id && missingProofs > 0;
      const shortageMessage = source.proofs.length < bulletTarget
        ? `Target is ${bulletTarget}, but this experience has ${source.proofs.length} proof points. Lower the target or add Career Vault evidence.`
        : `Select ${missingProofs} more proof point${missingProofs === 1 ? "" : "s"} for this experience.`;
      const suggestedProofs = source.proofs.filter((proof) => (proof.matched_job_skills || []).length).length;
      const supportedTargets = [2, 3, 4, 5].filter((count) => count <= Math.max(2, source.proofs.length));
      if (!supportedTargets.includes(bulletTarget)) supportedTargets.push(bulletTarget);
      supportedTargets.sort((left, right) => left - right);
      const targetOptions = supportedTargets.map((count) => (
        `<option value="${count}"${bulletTarget === count ? " selected" : ""}>${count} bullets${count === 2 ? " (compact)" : ""}${count > source.proofs.length ? " (not enough evidence)" : ""}</option>`
      )).join("");
      return `<article class="resume-plan-record${included ? " is-included" : " is-excluded"}${needsAttention ? " has-error" : ""}" data-plan-experience-card="${escapeHtml(source.id)}">
        <div class="resume-plan-record-heading"><div><strong>${escapeHtml(source.company || "Company not stated")}</strong><span>${escapeHtml([source.role, source.dates].filter(Boolean).join(" / "))}</span></div><code>${escapeHtml(source.id)}</code></div>
        <div class="resume-plan-control-grid">
          <div class="resume-plan-decision-field">
            <span>Resume decision</span>
            <div class="resume-plan-decision-group" role="group" aria-label="Resume decision for ${escapeHtml(source.company || source.id)}">
              <button type="button" data-plan-experience-decision="${escapeHtml(source.id)}" data-plan-decision-value="include" aria-pressed="${included}" class="${included ? "is-active" : ""}">Include</button>
              <button type="button" data-plan-experience-decision="${escapeHtml(source.id)}" data-plan-decision-value="exclude" aria-pressed="${!included}" class="${included ? "" : "is-active"}">Exclude</button>
            </div>
          </div>
          ${included ? `<label><span>Resume bullet target</span><select data-plan-experience-target="${escapeHtml(source.id)}">${targetOptions}</select></label>` : ""}
        </div>
        ${needsAttention ? `<p class="resume-plan-inline-error" role="alert">${escapeHtml(shortageMessage)}</p>` : ""}
        ${included ? `<div class="resume-plan-proof-list"><span>Candidate evidence <small>${selectedProofs} selected for ${bulletTarget} resume bullets${suggestedProofs ? ` / ${suggestedProofs} JD suggested` : ""}</small></span>${source.proofs.length ? source.proofs.map((proof) => {
          const matches = proof.matched_job_skills || [];
          return `<label class="${matches.length ? "is-suggested" : ""}"><input type="checkbox" data-plan-experience-proof="${escapeHtml(source.id)}" value="${escapeHtml(proof.id)}"${item.proof_ids.includes(proof.id) ? " checked" : ""}> <span>${escapeHtml(proof.claim)}</span>${matches.length ? `<small>Suggested: ${escapeHtml(matches.join(", "))}</small>` : ""}</label>`;
        }).join("") : renderEmptyState({ title: "No proof points are available for this experience.", compact: true })}</div>` : ""}
        ${!included ? `<label class="resume-plan-reason"><span>Exclusion reason <small>optional</small></span><input data-plan-experience-reason="${escapeHtml(source.id)}" maxlength="1000" placeholder="Optional note" value="${escapeHtml(item?.reason || "")}"></label>` : ""}
      </article>`;
    }).join(""),
  };
}

export function renderResumePlanProjects({ catalog, plan, enabled } = {}) {
  const projects = catalog.projects || [];
  if (!enabled) return { count: `${(plan.projects || []).length} selected / ${projects.length}`, html: "" };
  const selectedProjects = new Map((plan.projects || []).map((item) => [item.block_id, item]));
  return {
    count: `${(plan.projects || []).length} selected / ${projects.length}`,
    html: projects.map((source) => {
      const item = selectedProjects.get(source.id);
      return `<article class="resume-plan-record${item ? " is-included" : ""}">
        <div class="resume-plan-record-heading"><label data-tooltip="Select this project for the resume plan."><input type="checkbox" data-plan-project="${escapeHtml(source.id)}"${item ? " checked" : ""}> <strong>${escapeHtml(source.name)}</strong></label><code>${escapeHtml(source.id)}</code></div>
        <p>${escapeHtml(source.summary || source.category || "No summary")}</p>
        ${item ? `<div class="resume-plan-proof-list"><span>Proof points</span>${source.proofs.length ? source.proofs.map((proof) => `<label><input type="checkbox" data-plan-project-proof="${escapeHtml(source.id)}" value="${escapeHtml(proof.id)}"${item.proof_ids.includes(proof.id) ? " checked" : ""}> ${escapeHtml(proof.claim)}</label>`).join("") : renderEmptyState({ title: "No project proof points are available.", compact: true })}</div>` : ""}
      </article>`;
    }).join("") || renderEmptyState({ title: "No active project records.", compact: true }),
  };
}

export function renderResumePlanSkills({ catalog, plan } = {}) {
  const skills = catalog.skills || [];
  const selectedSkills = new Map((plan.skills || []).map((item) => [item.block_id, item]));
  return {
    count: `${(plan.skills || []).length} selected / ${skills.length}`,
    html: skills.map((source) => {
      const item = selectedSkills.get(source.id);
      return `<article class="resume-plan-record${item ? " is-included" : ""}">
        <div class="resume-plan-record-heading"><label><input type="checkbox" data-plan-skill="${escapeHtml(source.id)}"${item ? " checked" : ""}> <strong>${escapeHtml(source.skill_family)}</strong></label><code>${escapeHtml(source.id)}</code></div>
        ${item ? `<div class="resume-plan-keyword-list">${source.keywords.map((keyword) => `<label><input type="checkbox" data-plan-skill-keyword="${escapeHtml(source.id)}" value="${escapeHtml(keyword)}"${item.keywords.includes(keyword) ? " checked" : ""}> ${escapeHtml(keyword)}</label>`).join("")}</div>` : `<p>${escapeHtml(source.keywords.join(", "))}</p>`}
      </article>`;
    }).join("") || renderEmptyState({ title: "No active skill groups.", compact: true }),
  };
}

export function renderResumePlanEducation({ catalog, plan } = {}) {
  const education = catalog.education || [];
  const selectedEducation = new Set((plan.education || []).map((item) => `${item.record_type}:${item.block_id}`));
  return {
    count: `${(plan.education || []).length} selected / ${education.length}`,
    html: education.map((source) => {
      const key = `${source.record_type}:${source.id}`;
      return `<article class="resume-plan-record${selectedEducation.has(key) ? " is-included" : ""}"><div class="resume-plan-record-heading"><label><input type="checkbox" data-plan-education="${escapeHtml(source.id)}" data-plan-education-type="${escapeHtml(source.record_type)}"${selectedEducation.has(key) ? " checked" : ""}> <strong>${escapeHtml(source.name)}</strong></label><span>${escapeHtml(source.record_type)}</span></div><p>${escapeHtml(source.detail || "")}</p></article>`;
    }).join("") || renderEmptyState({ title: "No active education records.", compact: true }),
  };
}

export function firstResumePlanApprovalIssue(editor) {
  const experiencePlan = new Map(editor.plan.experiences.map((item) => [item.block_id, item]));
  for (const source of editor.catalog.experiences) {
    const item = experiencePlan.get(source.id);
    if (item?.decision !== "include") continue;
    const target = Number(item.target_bullets || 2);
    const selected = item.proof_ids?.length || 0;
    if (selected < target) {
      const missing = target - selected;
      return {
        blockId: source.id,
        message: source.proofs.length < target
          ? `Target is ${target}, but ${source.company} / ${source.role} has ${source.proofs.length} proof points. Lower the target or add Career Vault evidence.`
          : `Select ${missing} more proof point${missing === 1 ? "" : "s"} for ${source.company} / ${source.role}.`,
      };
    }
  }
  return null;
}
