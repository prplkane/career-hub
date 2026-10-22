import { escapeHtml } from "../ui/primitives.js";

export function renderMaterialIssues(validation, { dirty = false } = {}) {
  if (dirty) {
    return '<p class="empty-state">Export will save the current changes and rerun source and evidence checks.</p>';
  }
  const issues = validation?.issues || [];
  if (!issues.length) {
    return '<p class="material-ready-message">No blocking draft issues. Final checks run when exporting.</p>';
  }
  return `<ul>${issues.map((issue) => `
    <li class="${issue.blocking_export ? "is-blocking" : "is-advisory"}">
      <span>${escapeHtml(issue.message)}</span>
      ${issue.path ? `<code>${escapeHtml(issue.path)}</code>` : ""}
    </li>
  `).join("")}</ul>`;
}

export function renderCoverLetterEvidenceCatalog(
  evidence,
  query,
  selectedRefs,
  expandedSourceKeys = [],
) {
  const normalizedQuery = String(query || "").trim().toLocaleLowerCase();
  const selected = new Set(selectedRefs || []);
  const expanded = new Set(expandedSourceKeys || []);
  const filtered = (evidence || []).filter((item) => {
    if (!normalizedQuery) return true;
    return [item.source_label, item.claim, ...(item.matched_job_skills || [])]
      .join(" ")
      .toLocaleLowerCase()
      .includes(normalizedQuery);
  });
  if (!filtered.length) {
    return '<p class="empty-state">No matching evidence.</p>';
  }
  const groups = [];
  const groupsByKey = new Map();
  filtered.forEach((item) => {
    const key = [item.source_type || "evidence", item.source_id || item.source_label || "vault"].join(":");
    let group = groupsByKey.get(key);
    if (!group) {
      group = {
        key,
        sourceType: item.source_type || "evidence",
        sourceLabel: item.source_label || item.source_id || "Career Vault",
        items: [],
      };
      groupsByKey.set(key, group);
      groups.push(group);
    }
    group.items.push(item);
  });
  return groups.map((group) => {
    const selectedCount = group.items.filter((item) => selected.has(item.id)).length;
    const pointLabel = `${group.items.length} point${group.items.length === 1 ? "" : "s"}`;
    const selectedLabel = selectedCount ? ` · ${selectedCount} added` : "";
    const isExpanded = normalizedQuery || expanded.has(group.key);
    const selectedClass = selectedCount ? " has-selection" : "";
    return `
    <details
      class="material-evidence-source-group${selectedClass}"
      data-evidence-source-key="${escapeHtml(group.key)}"
      ${isExpanded ? "open" : ""}
    >
      <summary>
        <span class="material-evidence-source-heading">
          <span>${escapeHtml(group.sourceType)}</span>
          <strong>${escapeHtml(group.sourceLabel)}</strong>
        </span>
        <small>${escapeHtml(pointLabel + selectedLabel)}</small>
        <span class="material-evidence-source-chevron" aria-hidden="true"></span>
      </summary>
      <ul class="material-evidence-proof-list">
        ${group.items.map((item) => {
          const alreadySelected = selected.has(item.id);
          const selectedClass = alreadySelected ? " is-selected" : "";
          const disabled = alreadySelected ? " disabled" : "";
          const action = alreadySelected ? "Added" : "Add";
          return `
            <li class="material-evidence-proof${selectedClass}">
              <div>
                <p>${escapeHtml(item.claim)}</p>
                <button
                  type="button"
                  class="text-button"
                  data-add-cover-letter-evidence="${escapeHtml(item.id)}"
                  ${disabled}
                >${action}</button>
              </div>
            </li>`;
        }).join("")}
      </ul>
    </details>`;
  }).join("");
}

export function renderCoverLetterEvidenceItems(items, evidence) {
  const evidenceById = new Map((evidence || []).map((item) => [item.id, item]));
  if (!(items || []).length) {
    return '<p class="empty-state">Add two or three job-relevant points from Career Vault, or add a manual point that still needs verification.</p>';
  }
  return items.map((item, index) => {
    const sources = (item.evidence_refs || [])
      .map((reference) => evidenceById.get(reference)?.source_label || reference)
      .filter(Boolean);
    return `
      <article class="material-evidence-item" data-cover-letter-item-index="${index}">
        <div class="material-evidence-item-heading">
          <div><span>Point ${index + 1}</span><small>${escapeHtml(sources.join(" / ") || "Manual verification")}</small></div>
          <button type="button" class="icon-button" data-remove-cover-letter-evidence="${index}" aria-label="Remove evidence point">&times;</button>
        </div>
        <label><span>Label</span><input maxlength="80" required data-cover-letter-item-field="label" value="${escapeHtml(item.label || "")}"></label>
        <label><span>Claim</span><textarea rows="4" maxlength="1200" required data-cover-letter-item-field="claim">${escapeHtml(item.claim || "")}</textarea></label>
        <label><span>Manual checks <small>(one per line)</small></span><textarea rows="2" data-cover-letter-item-field="manual_checks" placeholder="Only for a fact that still needs verification">${escapeHtml((item.manual_checks || []).join("\n"))}</textarea></label>
      </article>`;
  }).join("");
}

export function coverLetterIssueSummary(validation, dirty) {
  if (dirty) return "Changes not checked";
  if (validation?.ready_to_export) return "Ready to export";
  const count = Number(validation?.issue_count || 0);
  return `${count} issue${count === 1 ? "" : "s"}`;
}

export function renderResumeEvidenceRefs(refs, evidenceById) {
  return (refs || []).map((reference) => {
    const item = evidenceById.get(reference);
    const label = item?.source_label || reference;
    return `<span class="material-evidence-ref" title="${escapeHtml(reference)}">${escapeHtml(label)}</span>`;
  }).join("");
}

export function renderResumeBlocks(draft, evidence) {
  const evidenceById = new Map((evidence || []).map((item) => [item.id, item]));
  const experience = (draft?.experiences || []).map((record, recordIndex) => `
    <article class="resume-material-record">
      <header>
        <div><strong>${escapeHtml(record.company)}</strong><span>${escapeHtml(record.role)}</span></div>
        <div><span>${escapeHtml(record.dates || "")}</span><span>${escapeHtml(record.location || "")}</span></div>
      </header>
      ${(record.bullets || []).map((bullet, bulletIndex) => `
        <label class="resume-material-bullet">
          <span>Bullet ${bulletIndex + 1}</span>
          <textarea rows="3" maxlength="1200" required data-resume-bullet-collection="experiences" data-resume-record-index="${recordIndex}" data-resume-bullet-index="${bulletIndex}">${escapeHtml(bullet.text || "")}</textarea>
          <small>${renderResumeEvidenceRefs(bullet.evidence_refs, evidenceById)}</small>
        </label>
      `).join("")}
    </article>
  `).join("");
  const projects = (draft?.projects || []).map((record, recordIndex) => `
    <article class="resume-material-record">
      <header><div><strong>${escapeHtml(record.name)}</strong><span>${escapeHtml(record.descriptor || "")}</span></div></header>
      ${(record.bullets || []).map((bullet, bulletIndex) => `
        <label class="resume-material-bullet">
          <span>Bullet ${bulletIndex + 1}</span>
          <textarea rows="3" maxlength="1200" required data-resume-bullet-collection="projects" data-resume-record-index="${recordIndex}" data-resume-bullet-index="${bulletIndex}">${escapeHtml(bullet.text || "")}</textarea>
          <small>${renderResumeEvidenceRefs(bullet.evidence_refs, evidenceById)}</small>
        </label>
      `).join("")}
    </article>
  `).join("");
  return {
    experience: experience || '<p class="empty-state">No experience is included in the current selection.</p>',
    projects: projects || '<p class="empty-state">No projects are included in the current selection.</p>',
  };
}

export function renderResumeSkills(skills) {
  if (!(skills || []).length) {
    return '<p class="empty-state">No skills are selected above.</p>';
  }
  return (skills || []).map((group) => `
    <div class="resume-material-source-row"><strong>${escapeHtml(group.label)}</strong><span>${escapeHtml((group.keywords || []).join(", "))}</span></div>
  `).join("");
}

export function renderResumeEducation(records) {
  if (!(records || []).length) {
    return '<p class="empty-state">No education records are selected above.</p>';
  }
  return (records || []).map((record) => `
    <div class="resume-material-source-row">
      <strong>${escapeHtml(record.heading)}</strong>
      <span>${escapeHtml([record.subheading, record.location, record.date_text].filter(Boolean).join(" | "))}</span>
    </div>
  `).join("");
}

export function materialIssueSummary(validation, dirty) {
  return coverLetterIssueSummary(validation, dirty);
}
