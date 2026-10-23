import {
  escapeHtml,
  renderEmptyState,
  renderStatusBadge,
} from "../ui/primitives.js";

export function jobRoleLabel(job) {
  return [job?.company, job?.title].filter(Boolean).join(" / ") || job?.id || "Job record";
}

const SAVED_APPLICATION_STATUSES = new Set(["captured", "reviewed"]);
const CLOSED_APPLICATION_STATUSES = new Set(["rejected", "withdrawn", "closed"]);

export function isJobInReviewQueue(job) {
  return Boolean(
    job?.valid
    && job.status === "draft"
    && SAVED_APPLICATION_STATUSES.has(job.application?.current_status)
  );
}

export function filterJobLibraryJobs(jobs, {
  query = "",
  filter = "all",
} = {}) {
  const normalizedQuery = query.trim().toLocaleLowerCase();
  return (jobs || []).filter((job) => {
    const status = job.application?.current_status || "";
    const queued = isJobInReviewQueue(job);
    const filterMatches = filter === "all"
      || (filter === "review" && queued)
      || (filter === "saved" && SAVED_APPLICATION_STATUSES.has(status))
      || (filter === "preparing" && status === "preparing")
      || (filter === "applied" && status === "applied")
      || (filter === "closed" && CLOSED_APPLICATION_STATUSES.has(status));
    if (!filterMatches || !normalizedQuery) return filterMatches;
    return [job.company, job.title, job.location, job.remote_policy, job.id]
      .filter(Boolean)
      .join(" ")
      .toLocaleLowerCase()
      .includes(normalizedQuery);
  });
}

/** Pure rendering for the compact JobIdentityFields primitive. */
export function renderJobIdentityFields({ fields = {}, facts = [], values = {}, editing = false, manualOverrides = [] } = {}) {
  return facts.map(([label, key]) => {
    const field = fields[key] || { value: values[key] || "", state: "not_stated", evidence: [] };
    const effectiveValue = values[key] ?? field.value ?? "";
    const manual = manualOverrides.includes(key);
    const evidence = field.evidence?.[0];
    const evidenceTitle = evidence
      ? `Line ${evidence.line}: ${evidence.text}`
      : key === "source_url" && effectiveValue
        ? effectiveValue
        : "No source evidence";
    if (editing) {
      const inputType = key === "source_url" ? "url" : "text";
      const placeholder = key === "remote_policy" ? "remote, hybrid, onsite" : key === "source_url" ? "https://..." : "";
      return `<label class="job-fact job-identity-fields__item is-editing">
        <span>${escapeHtml(label)}</span>
        <input id="job-edit-${escapeHtml(key.replaceAll("_", "-"))}" type="${inputType}" value="${escapeHtml(effectiveValue)}" placeholder="${escapeHtml(placeholder)}" autocomplete="off">
        <small class="fact-state provided">manual correction</small>
      </label>`;
    }
    return `<div class="job-fact job-identity-fields__item" title="${escapeHtml(evidenceTitle)}">
      <span>${escapeHtml(label)}</span>
      <strong${key !== "source_url" && effectiveValue ? ' translate="no"' : ""}>${escapeHtml(key === "source_url" && effectiveValue ? "Saved" : effectiveValue || "Not stated")}</strong>
      <small class="fact-state ${manual ? "provided" : escapeHtml(field.state)}">${manual ? "manual override" : escapeHtml(String(field.state).replaceAll("_", " "))}</small>
    </div>`;
  }).join("");
}

function sourceEvidence(items) {
  return Array.isArray(items) ? items.filter((item) => item && typeof item === "object") : [];
}

function fieldFor(job, name) {
  return job?.digest?.fields?.[name] || {};
}

function gateFor(job, name) {
  return job?.digest?.eligibility_gates?.[name] || {};
}

function fieldEvidence(job, name) {
  return sourceEvidence(fieldFor(job, name).evidence);
}

function requirementEvidence(job, requirement, source = "") {
  const digest = job?.digest || {};
  const normalizedRequirement = String(requirement || "").toLocaleLowerCase();
  const technology = sourceEvidence(digest.provenance?.technologies).find((item) => (
    String(item.name || "").toLocaleLowerCase() === normalizedRequirement
  ));
  if (technology) return sourceEvidence(technology.evidence);
  const group = (Array.isArray(digest.technology_groups) ? digest.technology_groups : []).find((item) => {
    const alternatives = Array.isArray(item?.technologies) ? item.technologies : [];
    return alternatives.some((name) => String(name).toLocaleLowerCase() === normalizedRequirement)
      || alternatives.join(" or ").toLocaleLowerCase() === normalizedRequirement;
  });
  if (group) return sourceEvidence(group.evidence);
  if (source === "experience_requirements") {
    const index = sourceEvidence(digest.experience_requirements).findIndex((item) => (
      String(item.area || "").toLocaleLowerCase() === normalizedRequirement
    ));
    return index >= 0 ? [sourceEvidence(digest.provenance?.experience_requirements)[index]].filter(Boolean) : [];
  }
  const classified = sourceEvidence(digest.classified_requirements).find((item) => (
    String(item.name || "").toLocaleLowerCase() === normalizedRequirement
  ));
  return sourceEvidence(classified?.evidence);
}

function evidenceHtml(evidence, absentMessage) {
  const sources = sourceEvidence(evidence).slice(0, 2);
  if (!sources.length) {
    return `<details class="assessment-summary__evidence"><summary>JD scan</summary><p>${escapeHtml(absentMessage)}</p></details>`;
  }
  return `<details class="assessment-summary__evidence"><summary>Posting evidence</summary><ul>${sources.map((item) => (
    `<li translate="no">Line ${escapeHtml(item.line)}: ${escapeHtml(item.text)}</li>`
  )).join("")}</ul></details>`;
}

const RESOLUTION_LABELS = {
  confirmed: "Resolved",
  resolved: "Resolved",
  not_applicable: "Resolved",
  accepted_risk: "Proceeding with risk",
};

function editTargetForFinding(findingKey) {
  if (["visa", "restrictions"].includes(findingKey)) return "eligibility";
  if (findingKey === "seniority" || findingKey === "location") return "identity";
  if (findingKey.startsWith("experience:") || findingKey.startsWith("requirement")) return "requirements";
  return "";
}

function conclusionHtml({
  label,
  status,
  tone,
  detail,
  evidence,
  absentMessage,
  findingKey = "",
  resolution = null,
  editTarget = "",
}) {
  const stale = Boolean(resolution?.stale);
  const reviewed = Boolean(resolution && !stale);
  const resolutionNote = resolution?.note
    ? `<p class="assessment-summary__resolution-note">${escapeHtml(resolution.note)}</p>`
    : "";
  const resolutionHtml = resolution
    ? `<div class="assessment-summary__resolution${stale ? " is-stale" : ""}"><span>${stale ? "Review again" : escapeHtml(RESOLUTION_LABELS[resolution.resolution] || "Reviewed")}</span><small>${stale ? "The automated assessment changed after this decision." : `Recorded ${escapeHtml(resolution.recorded_at || "")}`}</small>${resolutionNote}</div>`
    : "";
  const resolvedEditTarget = editTarget || editTargetForFinding(findingKey);
  const reviewAction = findingKey
    ? `<button type="button" class="text-button assessment-summary__review-button" data-assessment-finding-key="${escapeHtml(findingKey)}" data-assessment-finding-label="${escapeHtml(label)}" data-assessment-finding-tone="${escapeHtml(tone)}" data-assessment-edit-target="${escapeHtml(resolvedEditTarget)}">${reviewed ? "Edit decision" : stale ? "Review again" : "Review"}</button>`
    : "";
  if (status === "Not stated" && !resolution) {
    return `<details class="assessment-summary__conclusion assessment-summary__conclusion--${escapeHtml(tone)} assessment-summary__conclusion--collapsed">
      <summary class="assessment-summary__conclusion-heading">
        <h4>${escapeHtml(label)}</h4>
        ${renderStatusBadge({ label: status, tone })}
      </summary>
      <div class="assessment-summary__conclusion-body">
        <p>${escapeHtml(detail)}</p>
        ${evidenceHtml(evidence, absentMessage)}
        ${reviewAction}
      </div>
    </details>`;
  }
  return `<article class="assessment-summary__conclusion assessment-summary__conclusion--${escapeHtml(tone)}${reviewed ? " is-user-resolved" : ""}">
    <div class="assessment-summary__conclusion-heading">
      <h4>${escapeHtml(label)}</h4>
      ${renderStatusBadge({ label: status, tone })}
    </div>
    <p>${escapeHtml(detail)}</p>
    ${evidenceHtml(evidence, absentMessage)}
    ${resolutionHtml}
    ${reviewAction}
  </article>`;
}

function recommendationFor(assessment) {
  const blockers = Array.isArray(assessment?.eligibility_blockers) ? assessment.eligibility_blockers : [];
  if (blockers.length) {
    return { label: "Skip", tone: "danger", detail: `Skip unless an exception is confirmed: ${blockers[0].message}`, evidence: blockers[0].source_evidence };
  }
  if (assessment?.lane === "REVIEW_RECOMMENDED") {
    return { label: "Review", tone: "warning", detail: `Review before applying: ${assessment.biggest_gap || "one or more requirements need confirmation."}` };
  }
  return { label: "Apply", tone: "success", detail: "Apply: no recorded blocker or direct-evidence gap requires review." };
}

export function assessmentRecommendation(assessment) {
  return recommendationFor(assessment).label;
}

function assessmentFinding(assessment, codes) {
  const acceptedCodes = new Set(codes);
  const blocker = (assessment?.eligibility_blockers || []).find((item) => acceptedCodes.has(item.code));
  if (blocker) return { finding: blocker, tone: "danger", status: "Clear conflict" };
  const review = (assessment?.policy_reviews || []).find((item) => acceptedCodes.has(item.code));
  if (review) return { finding: review, tone: "warning", status: "Review" };
  return null;
}

function visaConclusion(assessment, job) {
  const sponsorship = gateFor(job, "sponsorship");
  const workAuthorization = gateFor(job, "work_authorization");
  const evidence = [...sourceEvidence(sponsorship.evidence), ...sourceEvidence(workAuthorization.evidence)];
  const policy = assessmentFinding(assessment, ["visa_or_opt_exclusion"]);
  if (policy) {
    return { status: policy.status, tone: policy.tone, detail: policy.finding.message, evidence: policy.finding.source_evidence };
  }
  if (sponsorship.status === "incompatible") {
    return { status: "Likely incompatible", tone: "danger", detail: sponsorship.reasons?.[0] || "The posting excludes sponsorship or OPT candidates.", evidence };
  }
  if (sponsorship.status === "compatible") {
    return { status: "Compatible", tone: "success", detail: sponsorship.reasons?.[0] || "The posting's sponsorship language is compatible with the saved profile.", evidence };
  }
  if (sponsorship.status === "unclear" || workAuthorization.status === "unclear") {
    return { status: "Verify", tone: "warning", detail: sponsorship.reasons?.[0] || workAuthorization.reasons?.[0] || "Verify work authorization before applying.", evidence };
  }
  return { status: "Not stated", tone: "info", detail: "Visa or sponsorship language is not stated in the saved posting.", evidence };
}

function restrictionConclusion(assessment, job) {
  const citizenship = gateFor(job, "citizenship");
  const clearance = gateFor(job, "clearance");
  const evidence = [...sourceEvidence(citizenship.evidence), ...sourceEvidence(clearance.evidence)];
  const policy = assessmentFinding(assessment, ["citizenship_requirement", "clearance_requirement"]);
  if (policy) {
    return { status: policy.status, tone: policy.tone, detail: policy.finding.message, evidence: policy.finding.source_evidence };
  }
  if ([citizenship.status, clearance.status].includes("incompatible")) {
    return { status: "Likely conflict", tone: "danger", detail: citizenship.reasons?.[0] || clearance.reasons?.[0] || "The posting has a citizenship or clearance restriction.", evidence };
  }
  if ([citizenship.status, clearance.status].includes("unclear")) {
    return { status: "Verify", tone: "warning", detail: citizenship.reasons?.[0] || clearance.reasons?.[0] || "Verify the citizenship or clearance condition before applying.", evidence };
  }
  return { status: "Not stated", tone: "info", detail: "No explicit citizenship or active-clearance restriction was detected.", evidence };
}

function experienceConclusions(assessment, job, compact, resolutions) {
  const reviews = Array.isArray(assessment?.experience_review) ? assessment.experience_review : [];
  if (!reviews.length) {
    return [conclusionHtml({
      label: "Experience requirement comparison",
      status: "Not stated",
      tone: "info",
      detail: "No explicit years-of-experience requirement was detected.",
      evidence: [],
      absentMessage: "No explicit experience-duration language was detected in the saved posting.",
      findingKey: "experience:none",
      resolution: resolutions.get("experience:none"),
    })];
  }
  return reviews.slice(0, compact ? 1 : reviews.length).map((item, index) => {
    const findingKey = `experience:${index}`;
    const years = Number.isFinite(item.derived_years) ? `${item.derived_years} derived` : "No derived duration";
    const required = item.maximum_years === undefined
      ? `${item.minimum_years}+ required`
      : `${item.minimum_years}-${item.maximum_years} required`;
    const shortfall = item.status === "derived_shortfall";
    const policyReview = shortfall && (assessment?.policy_reviews || []).some(
      (finding) => finding.code === "experience_minimum_shortfall"
        && finding.message?.includes(item.area),
    );
    const match = item.status === "derived_match";
    return conclusionHtml({
      label: "Experience requirement comparison",
      status: shortfall ? (policyReview ? "Review" : "Below requirement") : match ? "Compare" : "Verify",
      tone: shortfall ? (policyReview ? "warning" : "danger") : match ? "success" : "warning",
      detail: `${item.area}: ${years}; ${required}. ${shortfall ? "Confirm before applying." : match ? "Confirm the derived duration before applying." : "Career Vault has no verified duration for this area."}`,
      evidence: requirementEvidence(job, item.area, "experience_requirements"),
      absentMessage: "The experience requirement could not be linked to a retained JD line.",
      findingKey,
      resolution: resolutions.get(findingKey),
    });
  });
}

function seniorityConclusion(assessment, job) {
  const seniorityField = fieldFor(job, "seniority");
  const seniority = String(seniorityField.value || job?.seniority || "").toLocaleLowerCase();
  const shortfall = (assessment?.experience_review || []).some((item) => item.status === "derived_shortfall");
  if (!seniority) {
    return { status: "Not stated", tone: "info", detail: "No seniority signal was detected in the saved posting.", evidence: [] };
  }
  if (["senior", "lead"].includes(seniority) && shortfall) {
    return { status: "Possible mismatch", tone: "danger", detail: `The JD is marked ${seniority}, and a related experience requirement is below the derived duration.`, evidence: fieldEvidence(job, "seniority") };
  }
  if (["senior", "lead"].includes(seniority)) {
    return { status: "Review scope", tone: "warning", detail: `The JD is marked ${seniority}; compare its scope and related experience requirements before applying.`, evidence: fieldEvidence(job, "seniority") };
  }
  return { status: "No mismatch inferred", tone: "success", detail: `The JD is marked ${seniority}; no seniority mismatch is inferred from the saved evidence.`, evidence: fieldEvidence(job, "seniority") };
}

function isUsLocation(value) {
  const text = String(value || "");
  return /united states|u\.?s\.?a?|\b(?:AL|AK|AZ|AR|CA|CO|CT|DC|DE|FL|GA|HI|IA|ID|IL|IN|KS|KY|LA|MA|MD|ME|MI|MN|MO|MS|MT|NC|ND|NE|NH|NJ|NM|NV|NY|OH|OK|OR|PA|RI|SC|SD|TN|TX|UT|VA|VT|WA|WI|WV|WY)\b/.test(text);
}

function locationConclusion(job, profile) {
  const locationField = fieldFor(job, "location");
  const remoteField = fieldFor(job, "remote_policy");
  const location = String(locationField.value || job?.location || "").trim();
  const remote = String(remoteField.value || job?.remote_policy || "").toLocaleLowerCase();
  const target = String(profile?.target_location || profile?.location_flexibility || "").trim();
  const evidence = [...fieldEvidence(job, "remote_policy"), ...fieldEvidence(job, "location")];
  if (remote === "remote") {
    return { status: "Compatible", tone: "success", detail: "The saved posting is marked remote.", evidence };
  }
  if (!location) {
    return { status: "Not stated", tone: "info", detail: "The posting does not state a work location.", evidence };
  }
  if (target && ((target.toLocaleLowerCase().includes("united states") && isUsLocation(location)) || location.toLocaleLowerCase().includes(target.toLocaleLowerCase()))) {
    return { status: "Compatible", tone: "success", detail: `The JD location (${location}) fits the saved target location (${target}).`, evidence };
  }
  if (target.toLocaleLowerCase().includes("united states")) {
    return { status: "Verify location", tone: "warning", detail: `The JD states ${location}; verify that it is within the saved United States target before applying.`, evidence };
  }
  return { status: "Review location", tone: "warning", detail: target ? `Potential location conflict: JD states ${location}; saved target is ${target}.` : `The JD states ${location}; compare it with your location preference.`, evidence };
}

function requirementConclusions(assessment, job, compact, resolutions) {
  const gaps = Array.isArray(assessment?.requirement_gaps) ? assessment.requirement_gaps : [];
  const manual = (assessment?.classified_requirement_review || []).filter((item) => (
    item.requirement === "required" && item.status === "manual_review"
  ));
  const items = [
    ...gaps.map((item) => ({ ...item, detail: item.message, evidence: requirementEvidence(job, item.requirement, item.source) })),
    ...manual.map((item) => ({ ...item, requirement: item.name, detail: `Manual verification needed: ${item.source_text}`, evidence: item.source_evidence })),
  ];
  if (!items.length) {
    return [conclusionHtml({
      label: "Important requirements",
      status: "No open gap",
      tone: "success",
      detail: "No required evidence gap or required manual verification is recorded.",
      evidence: sourceEvidence(assessment?.matched_required).flatMap((item) => requirementEvidence(job, item.requirement)).slice(0, 2),
      absentMessage: "No unresolved required requirement was detected in the saved posting.",
      findingKey: "requirements:none",
      resolution: resolutions.get("requirements:none"),
    })];
  }
  return items.slice(0, compact ? 2 : items.length).map((item, index) => (item.category === "coverage"
    ? conclusionHtml({
      label: "Important requirements",
      status: "No requirement basis",
      tone: "warning",
      detail: item.detail,
      evidence: sourceEvidence(job?.digest?.analysis_coverage?.unrecognized_headings),
      absentMessage: "No requirement heading was recognized in the saved posting.",
      findingKey: `requirement:${index}`,
      resolution: resolutions.get(`requirement:${index}`),
    })
    : conclusionHtml({
      label: "Important requirements",
      status: item.status === "evidence_not_found" ? "Evidence gap" : "Verify",
      tone: "warning",
      detail: `${item.requirement}: ${item.detail}`,
      evidence: item.evidence,
      absentMessage: "The requirement could not be linked to a retained JD line.",
      findingKey: `requirement:${index}`,
      resolution: resolutions.get(`requirement:${index}`),
    })));
}

/**
 * Pure, reusable AssessmentSummary component. Its conclusions are derived from
 * the stored deterministic assessment and retained JD evidence only.
 */
export function renderAssessmentSummary(assessment, {
  job = {},
  profile = {},
  title = "Application assessment",
  compact = false,
} = {}) {
  if (!assessment) {
    return `<section class="assessment-summary assessment-summary--unavailable" aria-label="${escapeHtml(title)}">
      <h3>${escapeHtml(title)}</h3>
      ${renderEmptyState({ title: "Assessment unavailable", message: "Open this saved job again to refresh its deterministic assessment.", compact: true })}
    </section>`;
  }
  const recommendation = recommendationFor(assessment);
  const resolutions = new Map((assessment.user_resolutions || []).map((item) => [item.finding_key, item]));
  const fallbackEvidence = [
    ...sourceEvidence(recommendation.evidence),
    ...fieldEvidence(job, "seniority"),
    ...fieldEvidence(job, "title"),
  ];
  const visa = visaConclusion(assessment, job);
  const restrictions = restrictionConclusion(assessment, job);
  const seniority = seniorityConclusion(assessment, job);
  const location = locationConclusion(job, profile);
  const recommendationEditTarget = assessment.eligibility_blockers?.length
    ? "eligibility"
    : (assessment.requirement_gaps?.length || assessment.experience_review?.length || assessment.classified_requirement_review?.length)
      ? "requirements"
      : "identity";
  return `<section class="assessment-summary${compact ? " assessment-summary--compact" : ""}" aria-label="${escapeHtml(title)}">
    <div class="assessment-summary__heading">
      <div><p class="section-kicker">Career Vault fit</p><h3>${escapeHtml(title)}</h3></div>
      ${renderStatusBadge({ label: recommendation.label, tone: recommendation.tone })}
    </div>
    <div class="assessment-summary__grid">
      ${conclusionHtml({ label: "Recommendation", status: recommendation.label, tone: recommendation.tone, detail: recommendation.detail, evidence: fallbackEvidence, absentMessage: "This recommendation uses the recorded assessment findings; no separate JD line is available.", findingKey: "recommendation", resolution: resolutions.get("recommendation"), editTarget: recommendationEditTarget })}
      ${conclusionHtml({ label: "Visa compatibility", ...visa, absentMessage: "No sponsorship or work-authorization language was detected in the saved posting.", findingKey: "visa", resolution: resolutions.get("visa") })}
      ${conclusionHtml({ label: "Citizenship and clearance", ...restrictions, absentMessage: "No citizenship or active-clearance restriction was detected in the saved posting.", findingKey: "restrictions", resolution: resolutions.get("restrictions") })}
      ${experienceConclusions(assessment, job, compact, resolutions).join("")}
      ${conclusionHtml({ label: "Seniority mismatch", ...seniority, absentMessage: "No seniority signal was detected in the saved posting.", findingKey: "seniority", resolution: resolutions.get("seniority") })}
      ${conclusionHtml({ label: "Location conflict", ...location, absentMessage: "No location or remote-workplace evidence was detected in the saved posting.", findingKey: "location", resolution: resolutions.get("location") })}
      ${requirementConclusions(assessment, job, compact, resolutions).join("")}
    </div>
  </section>`;
}

export function formatCompensationRange(range) {
  const minimum = Number(range.minimum).toLocaleString();
  const maximum = Number(range.maximum).toLocaleString();
  const currency = range.currency || "";
  if (currency.length === 1) return `${currency}${minimum} - ${currency}${maximum}`;
  return `${minimum} - ${maximum}${currency ? ` ${currency}` : ""}`;
}
