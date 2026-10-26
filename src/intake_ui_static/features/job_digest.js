// Job workspace digest: identity, gates, requirements and evidence summary for the selected job.
import { elements, state } from "../app_context.js";
import { escapeHtml, statusClass } from "../ui/primitives.js";
import { formatCompensationRange as formatJobCompensationRange, renderJobIdentityFields } from "./jobs.js";

// Bootstrap hooks supplied by app.js through initJobDigest().
let hideJobEditForm = null;

export function renderJobDigest() {
  const job = state.jobs.find((item) => item.id === state.selectedJobId);
  if (!job) {
    elements.jobDigestSection.classList.add("hidden");
    elements.jobAssessment.classList.add("hidden");
    elements.jobDigestSourceLink.classList.add("hidden");
    elements.jobSourcePreview.classList.add("hidden");
    hideJobEditForm({ render: false });
    state.jobGatesEditing = false;
    state.jobRequirementsEditing = false;
    return;
  }
  const digest = job.digest || {};
  const fields = digest.fields || {};
  const role = [job.company, job.title].filter(Boolean).join(" / ") || job.id;
  elements.jobDigestSection.classList.remove("hidden");
  elements.editJobDetailsButton.disabled = !job.valid || state.jobEditSaving;
  elements.editJobDetailsButton.classList.toggle("hidden", state.jobIdentityEditing);
  elements.cancelJobEditButton.classList.toggle("hidden", !state.jobIdentityEditing);
  elements.saveJobEditButton.classList.toggle("hidden", !state.jobIdentityEditing);
  elements.jobDigestTitle.textContent = role;
  elements.jobDigestStatus.textContent = job.valid ? job.status : "invalid";
  elements.jobDigestStatus.className = `status-text ${statusClass(job.valid ? job.status : "error")}`;
  if (job.source_url && !state.jobIdentityEditing) {
    elements.jobDigestSourceLink.href = job.source_url;
    elements.jobDigestSourceLink.classList.remove("hidden");
  } else {
    elements.jobDigestSourceLink.href = "#";
    elements.jobDigestSourceLink.classList.add("hidden");
  }
  const sourcePreview = state.jobSourcePreview?.jobId === job.id
    ? state.jobSourcePreview
    : null;
  elements.jobSourcePreview.classList.toggle("hidden", !sourcePreview);
  elements.jobSourcePreviewMeta.textContent = sourcePreview
    ? `${sourcePreview.sourceName} | ${sourcePreview.sourceText.length.toLocaleString()} characters`
    : "";
  elements.jobSourcePreviewText.textContent = sourcePreview?.sourceText || "";

  const positionFacts = [
    ["Company", "company"],
    ["Role", "title"],
    ["Seniority", "seniority"],
  ];
  const contextFacts = [
    ["Location", "location"],
    ["Workplace", "remote_policy"],
    ["Source URL", "source_url"],
  ];
  elements.jobFactGrid.innerHTML = renderJobIdentityFields({
    fields,
    facts: positionFacts,
    values: job,
    editing: state.jobIdentityEditing,
    manualOverrides: job.manual_overrides || [],
  });
  elements.jobContextGrid.innerHTML = renderJobIdentityFields({
    fields,
    facts: contextFacts,
    values: job,
    editing: state.jobIdentityEditing,
    manualOverrides: job.manual_overrides || [],
  });

  const compensationRanges = job.compensation?.ranges || digest.compensation?.ranges || [];
  elements.jobCompensationList.innerHTML = compensationRanges.length
    ? compensationRanges.map((range) => {
      const evidence = range.evidence?.[0];
      const evidenceTitle = evidence ? `Line ${evidence.line}: ${evidence.text}` : "Source-backed compensation";
      const context = [
        range.location && range.location !== fields.location?.value ? range.location : "",
        range.period && range.period !== "unspecified" ? `per ${range.period}` : "",
      ].filter(Boolean).join(" | ");
      return `<div class="job-compensation-range" title="${escapeHtml(evidenceTitle)}">
        <span>Compensation</span>
        <strong>${escapeHtml(formatJobCompensationRange(range))}</strong>
        ${context ? `<small>${escapeHtml(context)}</small>` : ""}
      </div>`;
    }).join("")
    : `<div class="job-compensation-empty">
        <span>Compensation</span>
        <strong>Not stated in this posting</strong>
      </div>`;

  const gates = digest.eligibility_gates || {};
  elements.editJobGatesButton.classList.toggle("hidden", state.jobGatesEditing);
  elements.cancelJobGatesButton.classList.toggle("hidden", !state.jobGatesEditing);
  elements.saveJobGatesButton.classList.toggle("hidden", !state.jobGatesEditing);
  elements.jobGateList.innerHTML = Object.entries(gates).map(([name, gate]) => {
    const reason = gate.reasons?.join(" ") || "No explicit requirement found.";
    const evidence = gate.evidence?.[0];
    const evidenceTitle = evidence ? `Line ${evidence.line}: ${evidence.text}` : reason;
    const statusControl = state.jobGatesEditing
      ? `<select data-job-gate-name="${escapeHtml(name)}" aria-label="${escapeHtml(name.replaceAll("_", " "))} status">${["not_stated", "compatible", "unclear", "incompatible"].map((status) => `<option value="${status}"${gate.status === status ? " selected" : ""}>${escapeHtml(status.replaceAll("_", " "))}</option>`).join("")}</select>`
      : `<strong class="job-gate-status ${escapeHtml(gate.status)}">${escapeHtml(gate.status.replaceAll("_", " "))}</strong>`;
    return `<div class="job-gate" title="${escapeHtml(evidenceTitle)}">
      <div class="job-gate-head">
        <span>${escapeHtml(name)}</span>
        ${statusControl}
      </div>
      <div>${escapeHtml(reason)}</div>
    </div>`;
  }).join("");

  const signalGroups = [
    ["Required", job.required_skills || [], job.required_platforms || []],
    ["Preferred", job.preferred_skills || []],
    ["Mentioned", digest.mentioned_technologies || []],
  ];
  const occurrenceByName = new Map();
  (digest.skill_occurrences || []).forEach((occurrence) => {
    if (!occurrenceByName.has(occurrence.canonical_name)) {
      occurrenceByName.set(occurrence.canonical_name, occurrence);
    }
  });
  const manuallyReviewedSignals = new Set();
  if ((job.manual_overrides || []).includes("required_skills")) (job.required_skills || []).forEach((name) => manuallyReviewedSignals.add(name));
  if ((job.manual_overrides || []).includes("required_platforms")) (job.required_platforms || []).forEach((name) => manuallyReviewedSignals.add(name));
  if ((job.manual_overrides || []).includes("preferred_skills")) (job.preferred_skills || []).forEach((name) => manuallyReviewedSignals.add(name));
  if ((job.manual_overrides || []).includes("mentioned_technologies")) (digest.mentioned_technologies || []).forEach((name) => manuallyReviewedSignals.add(name));
  const renderSignalSkill = (technology) => {
    const occurrence = occurrenceByName.get(technology);
    const origin = manuallyReviewedSignals.has(technology) ? "manual" : occurrence?.origin;
    const originClass = origin ? ` registry-skill-${escapeHtml(origin)}` : "";
    const detail = occurrence
      ? `Line ${occurrence.line}: ${occurrence.source_text} | ${occurrence.primary_group_id} | ${origin}`
      : origin === "manual" ? "Manually attached to this job" : "Detected registry skill";
    return `<span class="tag${originClass}" title="${escapeHtml(detail)}">${escapeHtml(technology)}</span>`;
  };
  const technologyHtml = signalGroups.map(([level, ...groups]) => {
    const technologies = [...new Set(groups.flat())];
    if (!technologies.length) return "";
    return `<div class="job-technology-group">
      <span>${escapeHtml(level)}</span>
      <div>${technologies.map(renderSignalSkill).join("")}</div>
    </div>`;
  }).filter(Boolean);
  const alternativeHtml = (job.technology_groups || digest.technology_groups || []).map((group) => {
    const technologies = [...new Set(group.technologies || [])];
    if (!technologies.length) return "";
    const evidence = group.evidence?.[0];
    const evidenceTitle = evidence ? `Line ${evidence.line}: ${evidence.text}` : "Alternative requirement";
    const requirementLabel = group.requirement === "required"
      ? "Required: choose at least one"
      : group.requirement === "preferred"
        ? "Preferred alternatives"
        : "Mentioned alternatives";
    return `<div class="job-technology-group job-technology-alternative" title="${escapeHtml(evidenceTitle)}">
      <span>${escapeHtml(requirementLabel)}</span>
      <div>${technologies.map(renderSignalSkill).join("")}</div>
    </div>`;
  }).filter(Boolean);
  elements.jobTechnologyList.innerHTML = technologyHtml.length
    || alternativeHtml.length
    ? [...technologyHtml, ...alternativeHtml].join("")
    : '<span class="empty-state">No technology signals detected.</span>';

  const experienceHtml = (job.experience_requirements || []).map((requirement) => {
    const years = requirement.maximum_years === undefined
      ? `${requirement.minimum_years}+ years`
      : `${requirement.minimum_years}-${requirement.maximum_years} years`;
    return `<li><strong>${escapeHtml(years)}</strong><span translate="no">${escapeHtml(requirement.area)}</span></li>`;
  });
  const educationHtml = (job.education_requirements || []).map((requirement) => (
    `<li><strong>Education</strong><span translate="no">${escapeHtml(requirement)}</span></li>`
  ));
  const capabilityHtml = (job.required_capabilities || []).map((requirement) => (
    `<li><strong>Required capability</strong><span translate="no">${escapeHtml(requirement)}</span></li>`
  ));
  const classifiedHtml = (digest.classified_requirements || []).map((requirement) => {
    const level = requirement.requirement === "preferred" ? "Preferred" : "Required";
    const category = requirement.category || "requirement";
    const evidence = requirement.evidence?.[0];
    const evidenceTitle = evidence ? `Line ${evidence.line}: ${evidence.text}` : "Source evidence unavailable";
    return `<li class="job-classified-requirement" title="${escapeHtml(evidenceTitle)}">
      <strong>${escapeHtml(`${level} ${category}`)}</strong>
      <span translate="no"><b>${escapeHtml(requirement.name)}</b><small>${escapeHtml(requirement.text)}</small></span>
    </li>`;
  });
  const requirementHtml = [...experienceHtml, ...educationHtml, ...capabilityHtml, ...classifiedHtml];
  elements.editJobRequirementsButton.classList.toggle("hidden", state.jobRequirementsEditing);
  elements.cancelJobRequirementsButton.classList.toggle("hidden", !state.jobRequirementsEditing);
  elements.saveJobRequirementsButton.classList.toggle("hidden", !state.jobRequirementsEditing);
  if (state.jobRequirementsEditing) {
    const experienceRows = [...(job.experience_requirements || []), { minimum_years: "", maximum_years: "", area: "" }]
      .map((requirement) => `<div class="job-experience-edit-row" data-job-experience-row>
        <label><span>Min years</span><input type="number" min="0" step="0.5" data-experience-min value="${escapeHtml(requirement.minimum_years ?? "")}"></label>
        <label><span>Max</span><input type="number" min="0" step="0.5" data-experience-max value="${escapeHtml(requirement.maximum_years ?? "")}"></label>
        <label><span>Area</span><input data-experience-area value="${escapeHtml(requirement.area || "")}" placeholder="Example: data engineering"></label>
      </div>`).join("");
    const classifiedOptions = (digest.classified_requirements || []).map((requirement, index) => (
      `<label class="job-classified-edit-row"><input type="checkbox" data-classified-requirement-index="${index}" checked><span><strong>${escapeHtml(requirement.name)}</strong><small>${escapeHtml(requirement.text)}</small></span></label>`
    )).join("");
    elements.jobRequirementList.innerHTML = `<div class="job-requirement-editor">
      <div class="job-requirement-editor-grid">
        <label><span>Required skills</span><textarea id="job-edit-required-skills" rows="3">${escapeHtml((job.required_skills || []).join("\n"))}</textarea></label>
        <label><span>Required platforms</span><textarea id="job-edit-required-platforms" rows="3">${escapeHtml((job.required_platforms || []).join("\n"))}</textarea></label>
        <label><span>Required capabilities</span><textarea id="job-edit-required-capabilities" rows="3">${escapeHtml((job.required_capabilities || []).join("\n"))}</textarea></label>
        <label><span>Preferred skills</span><textarea id="job-edit-preferred-skills" rows="3">${escapeHtml((job.preferred_skills || []).join("\n"))}</textarea></label>
        <label class="wide"><span>Education requirements</span><textarea id="job-edit-education-requirements" rows="3">${escapeHtml((job.education_requirements || []).join("\n"))}</textarea></label>
      </div>
      <div class="job-experience-editor"><strong>Experience requirements</strong>${experienceRows}</div>
      ${classifiedOptions ? `<div class="job-classified-editor"><strong>Parser-classified requirements to keep</strong>${classifiedOptions}</div>` : ""}
    </div>`;
  } else {
    elements.jobRequirementList.innerHTML = requirementHtml.length
      ? `<ul>${requirementHtml.join("")}</ul>`
      : '<span class="empty-state">No explicit requirement detected.</span>';
  }

  const responsibilities = job.responsibilities || [];
  elements.jobResponsibilityList.innerHTML = responsibilities.length
    ? responsibilities.map((responsibility) => `<li translate="no">${escapeHtml(responsibility)}</li>`).join("")
    : '<li class="empty-state">No responsibility section detected.</li>';

  const coverage = digest.analysis_coverage;
  if (coverage) {
    const reviewItems = coverage.review_items || [];
    elements.jobCoverageSection.classList.remove("hidden");
    elements.jobCoverageSection.open = false;
    elements.jobCoverageSummary.textContent = reviewItems.length
      ? `${reviewItems.length} line${reviewItems.length === 1 ? "" : "s"} need review`
      : "No unstructured relevant lines";
    const metrics = [
      ["Source", coverage.source_lines],
      ["Structured", coverage.structured_lines],
      ["Needs review", coverage.needs_review_lines],
      ["Context", coverage.context_lines],
    ];
    elements.jobCoverageMetrics.innerHTML = metrics.map(([label, value]) => (
      `<div><strong>${Number(value || 0).toLocaleString()}</strong><span>${escapeHtml(label)}</span></div>`
    )).join("");
    elements.jobReviewLineList.innerHTML = reviewItems.length
      ? reviewItems.map((item) => `
          <div class="job-review-line">
            <div><span>Line ${Number(item.line)}</span><strong>${escapeHtml(item.section)}</strong></div>
            <p translate="no">${escapeHtml(item.text)}</p>
          </div>`).join("")
      : '<span class="empty-state">All relevant section lines produced at least one structured fact.</span>';
  } else {
    elements.jobCoverageSection.classList.add("hidden");
    elements.jobCoverageMetrics.innerHTML = "";
    elements.jobReviewLineList.innerHTML = "";
  }

  const unresolved = digest.unresolved_fields || [];
  elements.jobReviewNotice.textContent = unresolved.length
    ? `Needs review: ${unresolved.join(", ")}. Open the YAML draft before marking it ready.`
    : "No required identity fields are unresolved. Manual review is still required before ready status.";
}

export function initJobDigest(deps = {}) {
  ({ hideJobEditForm } = deps);
}
