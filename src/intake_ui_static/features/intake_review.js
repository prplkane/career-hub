// Intake review: extraction details, entity cards, block assignment, completion gate and approval preview.
import {
  elements,
  requestJson,
  state,
  toast,
} from "../app_context.js";
import { escapeHtml, formatRequestError } from "../ui/primitives.js";
import { pollLogs } from "./diagnostics.js";
import { invalidateResumeLibraryManagement, loadResumeLibrary, renderExtractions } from "./resume_library.js";
import { hasCurrentIntakeApprovalPreview } from "./resumes.js";
import { loadSkillsRegistry, registrySkillIndex } from "./skills_hub.js";

// Bootstrap hooks supplied by app.js through initIntakeReview().
let loadResumeCollections = null;
let navigateToView = null;
let openSkillQuickAction = null;

let reviewLoadEpoch = 0;

export function beginReviewLoad(sourceId) {
  reviewLoadEpoch += 1;
  state.reviewSourceId = sourceId;
  state.reviewDetail = null;
  state.approvalPreview = null;
  state.reviewDirty = false;
  state.selectedEntityId = null;
  state.reviewInspectorOpen = false;
  elements.keepApprovedResumeMessage.textContent = "";
  elements.removeApprovedImportMessage.className = "form-message";
  elements.removeApprovedImportMessage.textContent = "This is permanent. Keep the file for applications first if you may need its exact original bytes later.";
  clearExtractionDetails();
  return reviewLoadEpoch;
}

function isCurrentReviewLoad(sourceId, epoch) {
  return (
    state.activeView === "review"
    && state.reviewSourceId === sourceId
    && reviewLoadEpoch === epoch
  );
}

function activeReviewLoadRequest() {
  const sourceId = state.reviewSourceId;
  if (!sourceId || state.activeView !== "review") return null;
  return { sourceId, epoch: reviewLoadEpoch };
}

function currentReviewRequest() {
  const request = activeReviewLoadRequest();
  if (!request || state.reviewDetail?.source_id !== request.sourceId) return null;
  return request;
}

function isCurrentReviewRequest(request) {
  return Boolean(request) && isCurrentReviewLoad(request.sourceId, request.epoch)
    && state.reviewDetail?.source_id === request.sourceId;
}

function currentApprovedReviewSourceId() {
  const request = currentReviewRequest();
  if (state.reviewDetail?.approval_application?.status !== "applied") return null;
  return request?.sourceId || null;
}

function highlightedSkillBlock(block, mentions) {
  const matches = (mentions || [])
    .filter((mention) => mention.source_ref?.block_id === block.id)
    .filter((mention) => Number.isInteger(mention.source_ref?.start) && Number.isInteger(mention.source_ref?.end))
    .sort((left, right) => left.source_ref.start - right.source_ref.start);
  if (!matches.length) return escapeHtml(block.text);
  const registry = registrySkillIndex();
  let cursor = 0;
  const parts = [];
  matches.forEach((mention) => {
    const start = mention.source_ref.start;
    const end = mention.source_ref.end;
    if (start < cursor || end <= start || end > block.text.length) return;
    parts.push(escapeHtml(block.text.slice(cursor, start)));
    const origin = registry.get(mention.technology_id)?.origin || "core";
    parts.push(`<mark class="skill-highlight registry-skill-${escapeHtml(origin)}" title="${escapeHtml(mention.canonical_name)}">${escapeHtml(block.text.slice(start, end))}</mark>`);
    cursor = end;
  });
  parts.push(escapeHtml(block.text.slice(cursor)));
  return parts.join("");
}

function renderExtractionDetails(extraction) {
  elements.extractionPath.textContent = extraction.source_name || "Selected source";
  elements.metricStrip.innerHTML = Object.entries(extraction.counts).map(([label, value]) => (
    `<div class="metric"><strong>${value}</strong><span>${escapeHtml(label.replaceAll("_", " "))}</span></div>`
  )).join("");
  const registry = registrySkillIndex();
  elements.technologyList.innerHTML = extraction.technology_ids.length
    ? extraction.technology_ids.map((skillId) => {
      const skill = registry.get(skillId);
      const mention = (extraction.technology_mentions || []).find((item) => item.technology_id === skillId);
      const name = skill?.name || mention?.canonical_name || skillId;
      const origin = skill?.origin || "core";
      return `<span class="tag registry-skill-${escapeHtml(origin)}" title="${escapeHtml(mention?.source_text || name)}">${escapeHtml(name)}</span>`;
    }).join("")
    : '<span class="empty-state">No technologies detected.</span>';
  elements.sectionList.innerHTML = extraction.section_hints.length
    ? extraction.section_hints.map((section) => `
      <div class="plain-row">
        <code>${escapeHtml(section.source_ref.block_id)}</code>
        <span>${escapeHtml(section.section_type)}: ${escapeHtml(section.source_text)}</span>
      </div>`).join("")
    : '<div class="empty-state">No section hints detected.</div>';
  const documentBlocks = extraction.blocks.filter((block) => block.text);
  elements.documentBlockCount.textContent = `${documentBlocks.length} non-empty blocks`;
  elements.documentBlockList.innerHTML = documentBlocks.length
    ? documentBlocks.map((block) => `
      <div class="plain-row">
        <code>${escapeHtml(block.id)} / ${escapeHtml(block.kind)}${block.is_bullet ? " / bullet" : ""}</code>
        <span>${highlightedSkillBlock(block, extraction.technology_mentions)}</span>
      </div>`).join("")
    : '<div class="empty-state">No document text extracted.</div>';
  elements.bulletCount.textContent = `${extraction.bullet_candidates.length} candidates`;
  elements.bulletList.innerHTML = extraction.bullet_candidates.length
    ? extraction.bullet_candidates.map((bullet) => `
      <div class="bullet-row">
        <code>${escapeHtml(bullet.source_ref.block_id)}</code>
        <p>${escapeHtml(bullet.text)}</p>
      </div>`).join("")
    : '<div class="empty-state">No source bullets detected.</div>';
}

function clearExtractionDetails() {
  elements.extractionPath.textContent = "Loading source details…";
  elements.metricStrip.innerHTML = "";
  elements.technologyList.innerHTML = "";
  elements.sectionList.innerHTML = "";
  elements.documentBlockCount.textContent = "";
  elements.documentBlockList.innerHTML = "";
  elements.bulletCount.textContent = "";
  elements.bulletList.innerHTML = "";
}

async function loadExtraction(sourceId, { isCurrent = () => true } = {}) {
  if (!sourceId) return;
  if (!state.skillsRegistry) {
    await loadSkillsRegistry({ keepMessage: true }).catch(() => null);
  }
  const extraction = await requestJson(`/api/extractions/${encodeURIComponent(sourceId)}`);
  if (!isCurrent()) return null;
  renderExtractionDetails(extraction);
  return extraction;
}

function setReviewMessage(message, error = false) {
  elements.reviewMessage.className = error ? "form-message error" : "form-message";
  elements.reviewMessage.textContent = message;
}

export function syncReviewInspector() {
  const open = Boolean(
    state.reviewInspectorOpen
    && state.activeView === "review"
    && state.selectedEntityId
  );
  elements.reviewInspector.classList.toggle("open", open);
  elements.reviewInspectorBackdrop.hidden = !open;
  document.body.classList.toggle("review-inspector-open", open);
}

function setReviewInspectorOpen(open) {
  state.reviewInspectorOpen = Boolean(open);
  syncReviewInspector();
}

export function currentReview() {
  return state.reviewDetail?.review || null;
}

const CATEGORY_LABELS = {
  profile: "Profile",
  summary: "Professional Summary",
  experience: "Work Experience",
  education: "Education",
  project: "Projects",
  skill: "Skills",
  certification: "Certifications",
  other: "Other",
};

const CATEGORY_GROUPS = [
  ["Candidate", ["profile", "summary"]],
  ["Career records", ["experience", "education", "project", "skill", "certification"]],
  ["Other", ["other"]],
];

const APPROVAL_REASON_LABELS = {
  name_match: "Name match",
  shared_contact: "Shared contact",
  singleton_profile_destination: "Profile destination",
  organization_match: "Organization match",
  shared_role_terms: "Shared role terms",
  shared_dates: "Shared dates",
  shared_technologies: "Shared technologies",
  institution_match: "Institution match",
  degree_match: "Degree match",
  major_match: "Major match",
  certification_name_match: "Certification name match",
  credential_match: "Credential match",
  project_name_match: "Project name match",
  skill_family_match: "Skill family match",
  shared_keywords: "Shared keywords",
};

const APPROVAL_FIELD_LABELS = {
  name: "Name",
  organization: "Organization",
  role: "Role",
  dates: "Dates",
  graduation: "Graduation",
  credential: "Credential",
  completed: "Completed",
  location: "Location",
  target_location: "Target location",
  work_authorization: "Work authorization",
  institution: "Institution",
  degree: "Degree",
  major: "Major",
  gpa: "GPA",
  honors: "Honors",
  skill_family: "Skill family",
  keywords: "Keywords",
  technologies: "Skills",
  category: "Category",
  summary: "Summary",
  content: "Content",
  contact: "Contact",
  email: "Email",
  phone: "Phone",
  linkedin: "LinkedIn",
  github: "GitHub",
  website: "Website",
  proof_points: "Proof points",
};

const APPROVAL_FIELD_ALIASES = {
  organization: "organization",
  company: "organization",
  title: "role",
  role: "role",
  date_text: "dates",
  dates: "dates",
  emails: "contact",
  phones: "contact",
  links: "contact",
  contact: "contact",
};

const APPROVAL_FIELD_ORDER = [
  "name",
  "organization",
  "role",
  "dates",
  "location",
  "contact",
  "institution",
  "degree",
  "major",
  "graduation",
  "credential",
  "completed",
  "gpa",
  "honors",
  "skill_family",
  "keywords",
  "technologies",
  "category",
  "summary",
  "content",
  "target_location",
  "work_authorization",
  "email",
  "phone",
  "linkedin",
  "github",
  "website",
  "proof_points",
];

const APPROVAL_COLLECTION_FIELDS = new Set(["honors", "keywords", "technologies", "proof_points"]);

function entityCategory(entity) {
  return Object.hasOwn(CATEGORY_LABELS, entity.entity_type)
    ? entity.entity_type
    : "other";
}

function entityIsQueued(entity) {
  return entity.review_state === "queued";
}

function entityLabel(entity) {
  if (entity.entity_type === "profile") {
    return entity.name || "Profile & contact";
  }
  if (entity.entity_type === "education") {
    return entity.institution || entity.degree || "Untitled education";
  }
  if (entity.entity_type === "certification") {
    return entity.credential || entity.name || "Untitled certification";
  }
  if (entity.entity_type === "project") {
    return entity.title || "Untitled project";
  }
  if (entity.entity_type === "summary") {
    return entity.name || "Professional Summary";
  }
  if (entity.entity_type === "skill") {
    return entity.name || "Untitled skill group";
  }
  const title = entity.title || "Untitled experience";
  return entity.organization ? entity.organization + " / " + title : title;
}

function approvalDecisions(review = currentReview()) {
  if (!review) return [];
  if (!Array.isArray(review.approval_decisions)) review.approval_decisions = [];
  return review.approval_decisions;
}

function approvalDecision(subjectKind, subjectId, review = currentReview()) {
  return approvalDecisions(review).find((decision) => (
    decision.subject_kind === subjectKind && decision.subject_id === subjectId
  )) || null;
}

function approvalPreviewEntity(subjectId) {
  return state.approvalPreview?.entities.find((entity) => entity.source_entity_id === subjectId) || null;
}

function approvalCandidateForDecision(entity, decision) {
  if (!entity || decision?.action !== "merge") return null;
  return entity.candidates.find((candidate) => candidate.target_id === decision.target_id) || null;
}

function candidateIsDeterministicMatch(candidate) {
  return candidate?.reasons?.some(
    (reason) => reason.code !== "singleton_profile_destination"
  ) || false;
}

function approvalCandidateField(candidate, field) {
  const value = candidate?.fields?.[field];
  if (Array.isArray(value)) return value.join(", ").trim();
  return String(value || "").trim();
}

function approvalCandidatePositionLabel(candidate) {
  const company = approvalCandidateField(candidate, "organization");
  const role = approvalCandidateField(candidate, "role");
  const dates = approvalCandidateField(candidate, "dates");
  return [company, role, dates].filter(Boolean).join(" | ")
    || String(candidate?.target_label || "Career Vault record");
}

function approvalCandidatesHtml(candidates) {
  if (!candidates?.length) return "";
  return `
    <section class="approval-candidate-panel" aria-label="Possible existing records">
      <div>
        <strong>${candidates.length === 1 ? "Possible existing record" : "Possible existing records"}</strong>
        <p>Advisory only. Compare the company, role, and dates, then choose Update, Create, or Do not add. Your saved decision will not change automatically.</p>
      </div>
      <ul class="approval-candidate-list">
        ${candidates.map((candidate) => {
          const company = approvalCandidateField(candidate, "organization");
          const role = approvalCandidateField(candidate, "role");
          const dates = approvalCandidateField(candidate, "dates");
          const details = [["Company", company], ["Role", role], ["Dates", dates]]
            .filter(([, value]) => value)
            .map(([label, value]) => `<span><strong>${escapeHtml(label)}:</strong> ${escapeHtml(value)}</span>`)
            .join("");
          return `<li><strong>${escapeHtml(candidate.target_label)}</strong>${details ? `<div>${details}</div>` : ""}</li>`;
        }).join("")}
      </ul>
    </section>`;
}

function defaultFieldResolutions(entity, candidate, action) {
  if (!entity || !["merge", "create"].includes(action)) return null;
  const source = approvalFieldMap(entity.source_fields);
  const target = approvalFieldMap(candidate?.fields || {});
  const keys = new Set([...source.keys(), ...target.keys()]);
  const resolutions = {};
  keys.forEach((key) => {
    const sourceValues = source.get(key) || [];
    const targetValues = target.get(key) || [];
    if (action === "create") {
      resolutions[key] = sourceValues.length
        ? (APPROVAL_COLLECTION_FIELDS.has(key) ? "add_source" : "source")
        : "excluded";
      return;
    }
    const status = approvalComparisonStatus(sourceValues, targetValues);
    if (status.key === "exact") resolutions[key] = "same";
    else if (!sourceValues.length && targetValues.length) resolutions[key] = "vault";
    else resolutions[key] = "excluded";
  });
  return resolutions;
}

function setApprovalDecision(subjectKind, subjectId, rawValue) {
  const review = currentReview();
  if (!review) return;
  review.approval_decisions = approvalDecisions(review).filter((decision) => !(
    decision.subject_kind === subjectKind && decision.subject_id === subjectId
  ));
  if (rawValue) {
    const mergePrefix = "merge:";
    const action = rawValue.startsWith(mergePrefix) ? "merge" : rawValue;
    const targetId = rawValue.startsWith(mergePrefix) ? rawValue.slice(mergePrefix.length) : null;
    const entity = subjectKind === "entity" ? approvalPreviewEntity(subjectId) : null;
    const candidate = entity?.candidates.find((item) => item.target_id === targetId) || null;
    review.approval_decisions.push({
      subject_kind: subjectKind,
      subject_id: subjectId,
      action,
      target_id: targetId,
      field_resolutions: defaultFieldResolutions(entity, candidate, action),
    });
  }
  state.reviewDirty = true;
  renderReviewToolbar();
  renderCompletionGate();
}

function setApprovalFieldResolution(subjectId, field, choice) {
  const decision = approvalDecision("entity", subjectId);
  const entity = approvalPreviewEntity(subjectId);
  if (!decision || !entity || !["merge", "create"].includes(decision.action)) return;
  const candidate = approvalCandidateForDecision(entity, decision);
  if (!decision.field_resolutions) {
    decision.field_resolutions = defaultFieldResolutions(entity, candidate, decision.action);
  }
  decision.field_resolutions[field] = choice === "source"
    ? (APPROVAL_COLLECTION_FIELDS.has(field) ? "add_source" : "source")
    : (candidate ? "vault" : "excluded");
  state.reviewDirty = true;
  renderReviewToolbar();
  renderCompletionGate();
}

function useAllApprovalSourceFields(subjectId) {
  const decision = approvalDecision("entity", subjectId);
  const entity = approvalPreviewEntity(subjectId);
  if (!decision || !entity || !["merge", "create"].includes(decision.action)) return;
  const candidate = approvalCandidateForDecision(entity, decision);
  const source = approvalFieldMap(entity.source_fields);
  const target = approvalFieldMap(candidate?.fields || {});
  if (!decision.field_resolutions) {
    decision.field_resolutions = defaultFieldResolutions(entity, candidate, decision.action);
  }
  source.forEach((sourceValues, field) => {
    const status = approvalComparisonStatus(sourceValues, target.get(field) || []);
    decision.field_resolutions[field] = status.key === "exact"
      ? "same"
      : (APPROVAL_COLLECTION_FIELDS.has(field) ? "add_source" : "source");
  });
  state.reviewDirty = true;
  renderReviewToolbar();
  renderCompletionGate();
}

function pruneApprovalDecisions(review) {
  const entityIds = new Set(review.entities.map((entity) => entity.id));
  const categoryOnlyBlockIds = new Set(
    review.block_reviews
      .filter((item) => item.disposition === "categorized")
      .map((item) => item.source_ref.block_id)
  );
  review.approval_decisions = approvalDecisions(review).filter((decision) => (
    decision.subject_kind === "entity"
      ? entityIds.has(decision.subject_id)
      : categoryOnlyBlockIds.has(decision.subject_id)
  ));
}

function applyApprovalDefaults(preview) {
  const review = currentReview();
  if (!review) return 0;
  let changed = 0;
  preview.category_only_blocks.forEach((block) => {
    if (approvalDecision("block", block.block_id, review)) return;
    approvalDecisions(review).push({
      subject_kind: "block",
      subject_id: block.block_id,
      action: "exclude",
      target_id: null,
      field_resolutions: null,
    });
    changed += 1;
  });
  preview.entities.forEach((entity) => {
    const current = approvalDecision("entity", entity.source_entity_id, review);
    if (current) {
      if (["merge", "create"].includes(current.action) && current.field_resolutions == null) {
        current.field_resolutions = defaultFieldResolutions(
          entity,
          approvalCandidateForDecision(entity, current),
          current.action
        );
        changed += 1;
      }
      return;
    }
    const candidate = (
      entity.outcome === "possible_match"
      && entity.candidates.length === 1
      && candidateIsDeterministicMatch(entity.candidates[0])
    ) ? entity.candidates[0] : null;
    const action = candidate ? "merge" : entity.outcome === "new" ? "create" : "exclude";
    approvalDecisions(review).push({
      subject_kind: "entity",
      subject_id: entity.source_entity_id,
      action,
      target_id: candidate?.target_id || null,
      field_resolutions: defaultFieldResolutions(entity, candidate, action),
    });
    changed += 1;
  });
  if (changed) state.reviewDirty = true;
  return changed;
}

function selectedCardSuggestionCount() {
  const review = currentReview();
  if (!review || !state.selectedEntityId) return 0;
  return review.block_reviews.filter((item) => (
    item.disposition === "unassigned"
    && item.suggested_owner_ids.length === 1
    && item.suggested_owner_ids[0] === state.selectedEntityId
  )).length;
}

function reviewStats() {
  const review = currentReview();
  if (!review) return { assigned: 0, categorized: 0, unassigned: 0, ignored: 0, structural: 0, suggested: 0 };
  return review.block_reviews.reduce((counts, item) => {
    counts[item.disposition] += 1;
    if (item.suggested_owner_ids.length) counts.suggested += 1;
    return counts;
  }, { assigned: 0, categorized: 0, unassigned: 0, ignored: 0, structural: 0, suggested: 0 });
}

function renderReviewToolbar() {
  const review = currentReview();
  if (!review) {
    elements.reviewStatus.textContent = "Review not built";
    elements.reviewStatus.className = "status-warning";
    elements.reviewCounts.textContent = "Build a source-complete review before detecting cards.";
    elements.buildReviewButton.textContent = "Analyze resume";
    elements.buildReviewButton.disabled = !state.reviewSourceId;
    elements.suggestCardsButton.disabled = true;
    elements.acceptSuggestionsButton.disabled = true;
    elements.acceptCardButton.disabled = true;

    elements.saveDraftButton.disabled = true;
    elements.prepareApprovalButton.disabled = true;
    return;
  }
  const stats = reviewStats();
  const application = state.reviewDetail.approval_application;
  if (application) {
    elements.reviewStatus.textContent = "Applied to Career Vault";
    elements.reviewStatus.className = "status-success";
    elements.reviewCounts.textContent = `${application.changes.length} decisions applied / ${application.changed_files.length} Career Vault files changed`;
    elements.buildReviewButton.disabled = true;
    elements.suggestCardsButton.disabled = true;
    elements.acceptSuggestionsButton.disabled = true;
    elements.acceptCardButton.disabled = true;
    elements.saveDraftButton.disabled = true;
    elements.prepareApprovalButton.disabled = true;
    return;
  }
  const queuedCardCount = review.entities.filter(entityIsQueued).length;
  const activeCardCount = review.entities.length - queuedCardCount;
  const needsAttention = stats.unassigned + queuedCardCount;
  elements.reviewStatus.textContent = needsAttention ? "Needs attention" : "Review ready";
  elements.reviewStatus.className = needsAttention ? "status-warning" : "status-success";
  elements.reviewCounts.textContent = `${activeCardCount} detected records · ${needsAttention} need attention · ${stats.assigned} source blocks linked`;
  elements.buildReviewButton.textContent = review.entities.length ? "Refresh source blocks" : "Analyze resume";
  elements.buildReviewButton.disabled = false;
  elements.suggestCardsButton.disabled = false;
  elements.acceptSuggestionsButton.disabled = stats.suggested === 0;
  elements.acceptCardButton.disabled = selectedCardSuggestionCount() === 0;

  elements.saveDraftButton.disabled = !state.reviewDirty;
  const completion = liveCompletionReport();
  elements.prepareApprovalButton.disabled = !completion?.ready_for_preview || Boolean(state.approvalPreview);
}

function entityCardHtml(entity, counts, variant = "") {
  const isProfile = entity.entity_type === "profile";
  const isExperience = entity.entity_type === "experience";
  const isEducation = entity.entity_type === "education";
  const isCertification = entity.entity_type === "certification";
  const isSummary = entity.entity_type === "summary";
  const isSkill = entity.entity_type === "skill";
  const isProject = entity.entity_type === "project";
  let typeLabel = entity.entity_type || "Record";
  let primary = entity.name || entity.title || "Untitled record";
  let secondary = "";
  let detail = "";

  if (isProfile) {
    typeLabel = "Candidate";
    primary = entity.name || "Candidate not named";
    secondary = [
      entity.headline,
      entity.location,
      (entity.emails || [])[0],
    ].filter(Boolean).join(" / ") || "Contact details need review";
  } else if (isExperience) {
    typeLabel = "Experience";
    primary = entity.organization || "Organization not set";
    secondary = entity.title || "Role title not set";
    detail = entity.date_text || "";
  } else if (isEducation) {
    typeLabel = "Education";
    primary = entity.institution || "Institution not set";
    secondary = [entity.degree, entity.major].filter(Boolean).join(", ") || "Degree not set";
    detail = entity.date_text || "";
  } else if (isCertification) {
    typeLabel = "Certification";
    primary = entity.name || "Certification name not set";
    secondary = entity.credential || "Credential not set";
    detail = entity.date_text || "";
  } else if (isProject) {
    typeLabel = "Project";
    primary = entity.title || "Project title not set";
    secondary = (entity.items || [])[0] || "Project evidence not set";
    detail = entity.date_text || "";
  } else if (isSummary) {
    typeLabel = "Summary";
    primary = entity.name || "Professional Summary";
    secondary = entity.content || "Summary text not set";
  } else if (isSkill) {
    typeLabel = "Skill group";
    primary = entity.name || "Skill group not named";
    secondary = (entity.items || []).join(", ") || "Skills not set";
  } else {
    typeLabel = typeLabel.charAt(0).toUpperCase() + typeLabel.slice(1);
  }

  const classes = [
    "draft-card-button",
    variant,
    entity.id === state.selectedEntityId ? "active" : "",
  ].filter(Boolean).join(" ");
  return `
    <button class="${classes}" data-entity-id="${escapeHtml(entity.id)}" data-tooltip="Open ${escapeHtml(primary)} card details and source links." aria-pressed="${entity.id === state.selectedEntityId}">
      <span class="card-type">${escapeHtml(typeLabel)}</span>
      <strong>${escapeHtml(primary)}</strong>
      <span class="card-secondary">${escapeHtml(secondary)}</span>
      ${detail ? `<span class="card-detail">${escapeHtml(detail)}</span>` : ""}
      <span class="card-source-counts">${counts.assigned} linked / ${counts.suggested} to review</span>
    </button>`;
}

function renderEntityHierarchy() {
  const review = currentReview();
  const allEntities = review?.entities || [];
  const entities = allEntities.filter((entity) => !entityIsQueued(entity));
  const statsByEntity = new Map(
    entities.map((entity) => [entity.id, { assigned: 0, suggested: 0 }])
  );
  (review?.block_reviews || []).forEach((item) => {
    if (item.owner_id && statsByEntity.has(item.owner_id)) {
      statsByEntity.get(item.owner_id).assigned += 1;
    }
    item.suggested_owner_ids.forEach((entityId) => {
      if (statsByEntity.has(entityId)) statsByEntity.get(entityId).suggested += 1;
    });
  });

  const profiles = entities.filter((entity) => entity.entity_type === "profile");
  const experiences = entities.filter((entity) => entity.entity_type === "experience");
  const summaries = entities.filter((entity) => entity.entity_type === "summary");
  const education = entities.filter((entity) => entity.entity_type === "education");
  const certifications = entities.filter((entity) => entity.entity_type === "certification");
  const projects = entities.filter((entity) => entity.entity_type === "project");
  const skills = entities.filter((entity) => entity.entity_type === "skill");

  const candidateStatus = profiles.length ? "1 identified person" : "Candidate not detected";
  const summaryStatus = summaries.length === 1
    ? "1 source summary"
    : `${summaries.length} source summaries`;
  elements.candidateCount.textContent = `${candidateStatus} / ${summaryStatus}`;
  elements.candidateCard.innerHTML = profiles.length
    ? entityCardHtml(profiles[0], statsByEntity.get(profiles[0].id), "candidate-card-button")
    : '<div class="hierarchy-placeholder">No candidate profile detected.</div>';
  elements.candidateSummary.innerHTML = summaries.length
    ? summaries.map((entity) => (
      entityCardHtml(entity, statsByEntity.get(entity.id), "candidate-summary-card-button")
    )).join("")
    : "";

  elements.experienceCardCount.textContent = `${experiences.length} contexts`;
  elements.experienceCardRow.innerHTML = experiences.length
    ? experiences.map((entity) => entityCardHtml(entity, statsByEntity.get(entity.id))).join("")
    : '<div class="hierarchy-placeholder">No work contexts detected yet.</div>';

  elements.educationCardCount.textContent = `${education.length} records`;
  elements.educationCardRow.innerHTML = education.length
    ? education.map((entity) => entityCardHtml(entity, statsByEntity.get(entity.id))).join("")
    : '<div class="hierarchy-placeholder">No education detected yet.</div>';

  elements.certificationCardCount.textContent = `${certifications.length} records`;
  elements.certificationCardRow.innerHTML = certifications.length
    ? certifications.map((entity) => entityCardHtml(entity, statsByEntity.get(entity.id))).join("")
    : '<div class="hierarchy-placeholder">No certifications detected yet.</div>';

  elements.projectCardCount.textContent = `${projects.length} records`;
  elements.projectCardRow.innerHTML = projects.length
    ? projects.map((entity) => entityCardHtml(entity, statsByEntity.get(entity.id))).join("")
    : '<div class="hierarchy-placeholder">No projects detected yet.</div>';

  elements.skillCardCount.textContent = `${skills.length} groups`;
  elements.skillCardRow.innerHTML = skills.length
    ? skills.map((entity) => entityCardHtml(entity, statsByEntity.get(entity.id))).join("")
    : '<div class="hierarchy-placeholder">No skill groups detected yet.</div>';
}

export function normalizeReviewSkillName(value) {
  return String(value || "").trim().replace(/\s+/g, " ").toLocaleLowerCase("en-US");
}

function activeRegistryNameIndex() {
  const index = new Map();
  registrySkillIndex().forEach((skill) => {
    if (skill.archived) return;
    [skill.name, ...(skill.aliases || [])].forEach((value) => {
      index.set(normalizeReviewSkillName(value), skill);
    });
  });
  return index;
}

function reviewSkillGroupOptions(selectedId) {
  const groups = (state.skillsRegistry?.groups || []).filter((group) => !group.archived);
  const fallbackId = state.skillsRegistry?.default_custom_group_id || groups[0]?.id || "";
  const effectiveId = groups.some((group) => group.id === selectedId) ? selectedId : fallbackId;
  return [
    ...groups.map((group) => `<option value="${escapeHtml(group.id)}"${group.id === effectiveId ? " selected" : ""} translate="no">${escapeHtml(group.name)} · ${escapeHtml(group.origin)}</option>`),
    '<option value="__create_group__">+ Create a new group…</option>',
  ].join("");
}

function reviewSkillsEditorHtml(entity) {
  const skills = registrySkillIndex();
  const existingIds = [...new Set(entity.technologies || [])];
  const pending = entity.pending_skills || [];
  const optionValues = [];
  skills.forEach((skill) => {
    if (skill.archived) return;
    optionValues.push(skill.name, ...(skill.aliases || []));
  });
  const datalistId = `review-skill-options-${entity.id.replace(/[^A-Za-z0-9_-]/g, "-")}`;
  return `
    <section class="review-skills-editor" data-review-skills-entity="${escapeHtml(entity.id)}">
      <div class="review-skills-heading">
        <div><label for="review-skill-input-${escapeHtml(entity.id)}">Skills</label><p>Exact registry matches link now. Unknown labels remain proposals until final approval.</p></div>
        <span>${existingIds.length + pending.length} selected</span>
      </div>
      <div class="review-skill-pills">
        ${existingIds.map((skillId) => {
          const skill = skills.get(skillId);
          const name = skill?.name || skillId;
          const origin = skill?.origin || "unknown";
          const groupName = skill?.group?.name || "Missing registry reference";
          return `<span class="review-skill-pill registry-skill-${escapeHtml(origin)}" title="${escapeHtml(groupName)}"><span>${escapeHtml(name)}</span><button type="button" data-remove-review-skill-id="${escapeHtml(skillId)}" aria-label="Remove ${escapeHtml(name)}">&times;</button></span>`;
        }).join("")}
        ${existingIds.length || pending.length ? "" : '<span class="empty-state">No skills selected for this evidence card.</span>'}
      </div>
      <div class="review-skill-add-row">
        <input id="review-skill-input-${escapeHtml(entity.id)}" data-review-skill-input list="${escapeHtml(datalistId)}" maxlength="500" autocomplete="off" placeholder="Type a skill; use comma for several">
        <datalist id="${escapeHtml(datalistId)}">${[...new Set(optionValues)].map((value) => `<option value="${escapeHtml(value)}"></option>`).join("")}</datalist>
        <button type="button" class="secondary-button" data-add-review-skills>Add</button>
      </div>
      ${pending.length ? `<div class="review-pending-skills"><p class="section-kicker">New custom skills on approval</p>${pending.map((item, index) => `
        <div class="review-pending-skill" data-pending-skill-index="${index}">
          <span class="review-skill-pill registry-skill-custom"><span>${escapeHtml(item.name)}</span><button type="button" data-remove-pending-skill="${index}" aria-label="Remove ${escapeHtml(item.name)}">&times;</button></span>
          <label><span>Group</span><select data-pending-skill-group="${index}">${reviewSkillGroupOptions(item.primary_group_id)}</select></label>
        </div>`).join("")}</div>` : ""}
    </section>`;
}

function addReviewSkills(entity, rawValue) {
  const values = String(rawValue || "")
    .split(/[,;\n]+/)
    .map((value) => value.trim().replace(/\s+/g, " "))
    .filter(Boolean);
  if (!values.length) return false;
  entity.technologies = [...new Set(entity.technologies || [])];
  entity.pending_skills = entity.pending_skills || [];
  const names = activeRegistryNameIndex();
  let changed = false;
  values.forEach((value) => {
    if (value.length > 160) {
      setReviewMessage(`Skill labels must be 160 characters or fewer: ${value.slice(0, 40)}…`, true);
      return;
    }
    const normalized = normalizeReviewSkillName(value);
    const matched = names.get(normalized);
    if (matched) {
      if (!entity.technologies.includes(matched.id)) {
        entity.technologies.push(matched.id);
        changed = true;
      }
      const before = entity.pending_skills.length;
      entity.pending_skills = entity.pending_skills.filter(
        (item) => normalizeReviewSkillName(item.name) !== normalized
      );
      changed = changed || entity.pending_skills.length !== before;
      return;
    }
    const duplicate = entity.pending_skills.some(
      (item) => normalizeReviewSkillName(item.name) === normalized
    );
    if (duplicate) return;
    entity.pending_skills.push({
      name: value,
      primary_group_id: state.skillsRegistry?.default_custom_group_id || null,
    });
    changed = true;
  });
  if (changed) {
    markReviewDirty();
    renderEntityHierarchy();
    renderCardEditor();
  }
  return changed;
}

export function renderCardEditor() {
  const review = currentReview();
  const entity = review?.entities.find((item) => item.id === state.selectedEntityId);
  const queued = Boolean(entity && entityIsQueued(entity));
  syncReviewInspector();
  elements.deleteCardButton.hidden = !entity || queued || entity.entity_type === "profile";
  elements.restoreCardButton.hidden = !queued;
  elements.ungroupCardButton.hidden = !queued;
  elements.acceptCardButton.hidden = !entity || queued;
  if (!entity) {
    elements.cardEditor.innerHTML = '<div class="review-placeholder">Select a detected card.</div>';
    return;
  }
  const suggestionCount = selectedCardSuggestionCount();
  elements.restoreCardButton.textContent = queued ? `Restore ${entityLabel(entity)}` : "Restore card";
  elements.acceptCardButton.textContent = suggestionCount
    ? `Link ${suggestionCount} source blocks`
    : "No links to review";
  elements.acceptCardButton.disabled = suggestionCount === 0;

  let fields;
  if (entity.entity_type === "profile") {
    fields = [
      ["name", "Name", entity.name],
      ["headline", "Professional headline", entity.headline],
      ["location", "Location", entity.location],
      ["emails", "Email", (entity.emails || []).join(", ")],
      ["phones", "Phone", (entity.phones || []).join(", ")],
      ["links", "LinkedIn / GitHub / website", (entity.links || []).join(", ")],
    ];
  } else if (entity.entity_type === "summary") {
    fields = [
      ["name", "Section name", entity.name],
      ["content", "Summary text", entity.content],
    ];
  } else if (entity.entity_type === "skill") {
    fields = [
      ["name", "Skill group", entity.name],
      ["items", "Skills", (entity.items || []).join(", ")],
      ["technologies", "Matched technology IDs", (entity.technologies || []).join(", ")],
    ];
  } else if (entity.entity_type === "project") {
    fields = [
      ["title", "Project title", entity.title],
      ["date_text", "Dates", entity.date_text],
      ["items", "Project evidence (one item per line)", (entity.items || []).join("\n")],
    ];
  } else if (entity.entity_type === "education") {
    fields = [
      ["institution", "Institution", entity.institution],
      ["degree", "Degree", entity.degree],
      ["major", "Major / field of study", entity.major],
      ["date_text", "Graduation / dates", entity.date_text],
      ["location", "Location", entity.location],
      ["gpa", "GPA", entity.gpa],
      ["honors", "Honors / awards", (entity.honors || []).join(", ")],
    ];
  } else if (entity.entity_type === "certification") {
    fields = [
      ["name", "Certification name / issuer", entity.name],
      ["credential", "Credential", entity.credential],
      ["date_text", "Completed", entity.date_text],
      ["items", "Proof points (one item per line)", (entity.items || []).join("\n")],
    ];
  } else {
    fields = [
      ["organization", "Organization", entity.organization],
      ["title", "Role title", entity.title],
      ["date_text", "Dates", entity.date_text],
      ["location", "Location", entity.location],
    ];
  }
  const multilineFields = new Set(["content", "items"]);
  const skillsEditor = ["experience", "project", "certification"].includes(entity.entity_type)
    ? reviewSkillsEditorHtml(entity)
    : "";
  elements.cardEditor.innerHTML = fields.map(([name, label, value]) => multilineFields.has(name) ? `
    <div class="editor-field">
      <label for="card-${escapeHtml(name)}">${escapeHtml(label)}</label>
      <textarea id="card-${escapeHtml(name)}" data-card-field="${escapeHtml(name)}">${escapeHtml(value)}</textarea>
    </div>` : `
    <div class="editor-field">
      <label for="card-${escapeHtml(name)}">${escapeHtml(label)}</label>
      <input id="card-${escapeHtml(name)}" data-card-field="${escapeHtml(name)}" value="${escapeHtml(value)}">
    </div>`).join("") + skillsEditor + `
    <div class="editor-field">
      <label for="card-user-note">Notes</label>
      <textarea id="card-user-note" data-card-field="user_note">${escapeHtml(entity.user_note || "")}</textarea>
    </div>
    <div class="editor-field">
      <label>Detection rule</label>
      <input value="${escapeHtml(entity.suggestion_rule || "Manual card")}" disabled>
    </div>`;
}

function classificationOptions(item) {
  const current = item.disposition === "ignored"
    ? "__ignored"
    : item.disposition === "structural"
      ? "__structural"
      : item.category || "";
  const categoryGroups = CATEGORY_GROUPS.map(([groupLabel, categories]) => `
    <optgroup label="${escapeHtml(groupLabel)}">
      ${categories.map((category) => `
        <option value="${escapeHtml(category)}"${current === category ? " selected" : ""}>${escapeHtml(CATEGORY_LABELS[category])}</option>
      `).join("")}
    </optgroup>`).join("");
  return `
    <option value=""${current === "" ? " selected" : ""}>Unclassified</option>
    ${categoryGroups}
    <optgroup label="Review decision">
      <option value="__structural"${current === "__structural" ? " selected" : ""}>Section marker</option>
      <option value="__ignored"${current === "__ignored" ? " selected" : ""}>Ignored</option>
    </optgroup>`;
}

function ownerOptions(item, entities) {
  const current = item.disposition === "categorized"
    ? "__category__"
    : item.disposition === "assigned" ? item.owner_id : "";
  const rootOption = item.category
    ? `<option value="__category__"${current === "__category__" ? " selected" : ""}>Keep directly under ${escapeHtml(CATEGORY_LABELS[item.category] || item.category)}</option>`
    : "";
  const categories = CATEGORY_GROUPS.flatMap(([, values]) => values);
  const groups = categories.map((category) => {
    if (item.category && category !== item.category) return "";
    const categoryEntities = entities.filter(
      (entity) => entityCategory(entity) === category
    );
    const activeEntities = categoryEntities.filter((entity) => !entityIsQueued(entity));
    const queuedEntities = categoryEntities.filter(entityIsQueued);
    const activeGroup = activeEntities.length ? `
      <optgroup label="${escapeHtml(CATEGORY_LABELS[category])} cards">
        ${activeEntities.map((entity) => `
          <option value="${escapeHtml(entity.id)}"${current === entity.id ? " selected" : ""}>${escapeHtml(entityLabel(entity))}</option>
        `).join("")}
      </optgroup>` : "";
    const queuedGroup = queuedEntities.length ? `
      <optgroup label="Queued ${escapeHtml(CATEGORY_LABELS[category])} cards">
        ${queuedEntities.map((entity) => `
          <option value="${escapeHtml(entity.id)}"${current === entity.id ? " selected" : ""}>${escapeHtml(entityLabel(entity))}</option>
        `).join("")}
      </optgroup>` : "";
    return activeGroup + queuedGroup;
  }).join("");
  return `<option value="">Needs card</option>${rootOption}${groups}`;
}

function blockReviewGroup(item) {
  if (item.disposition === "assigned") return "linked";
  if (item.disposition === "categorized") return "categoryOnly";
  if (item.disposition === "ignored") return "ignored";
  if (item.disposition === "structural") return "structural";
  if (item.suggested_owner_ids.length) return "suggested";
  if (item.category) return "categorized";
  return "unclassified";
}

function sourceBlockHtml(block, item, review, entityById) {
  const suggested = item.suggested_owner_ids
    .map((entityId) => entityById.get(entityId))
    .filter(Boolean)
    .map(entityLabel)
    .join(", ");
  const group = blockReviewGroup(item);
  return `
    <div class="review-source-block" data-block-id="${escapeHtml(block.id)}">
      <div class="block-meta">
        <span>${escapeHtml(block.id)} / ${escapeHtml(block.kind)}${block.is_bullet ? " / bullet" : ""}</span>
        <span>${escapeHtml(group)}</span>
      </div>
      <p class="block-text">${escapeHtml(block.text)}</p>
      <div class="block-assignment">
        <label class="block-select-field">
          <span>Type</span>
          <select class="category-select" data-block-id="${escapeHtml(block.id)}" aria-label="Type for ${escapeHtml(block.id)}">
            ${classificationOptions(item)}
          </select>
        </label>
        <label class="block-select-field">
          <span>Attach to</span>
          <select class="owner-select" data-block-id="${escapeHtml(block.id)}" aria-label="Owner for ${escapeHtml(block.id)}"${["ignored", "structural"].includes(item.disposition) ? " disabled" : ""}>
            ${ownerOptions(item, review.entities)}
          </select>
        </label>
        ${suggested ? `<span class="suggestion-label" title="${escapeHtml(suggested)}">Suggested: ${escapeHtml(suggested)}</span>` : ""}
      </div>
    </div>`;
}

function approvalIssuesForReview(review) {
  const assignedCounts = new Map();
  review.block_reviews.forEach((item) => {
    if (item.disposition === "assigned" && item.owner_id) {
      assignedCounts.set(item.owner_id, (assignedCounts.get(item.owner_id) || 0) + 1);
    }
  });
  const legacyRequiredFields = {
    profile: [["name", "name"]],
    experience: [["organization", "organization"], ["title", "title"]],
    education: [["institution", "institution"]],
    certification: [["name", "name"], ["credential", "credential"], ["date_text", "completion date"]],
    project: [["title", "title"]],
    skill: [["name", "skill family"], ["items", "skill items"]],
  };
  const granularRequiredFields = {
    experience: [["organization", "organization"], ["role", "role"], ["dates", "dates"]],
    education: [["institution", "institution"]],
    certification: [["name", "name"], ["credential", "credential"], ["completed", "completion date"]],
    project: [["name", "name"], ["summary", "summary"]],
    skill: [["skill_family", "skill family"], ["keywords", "skill items"]],
  };
  const issues = [];
  review.block_reviews
    .filter((item) => item.disposition === "categorized")
    .forEach((item) => {
      if (!approvalDecision("block", item.source_ref.block_id, review)) {
        issues.push({
          code: "MISSING_APPROVAL_DECISION",
          message: "Choose Do not add, or return to card review and attach this block.",
          entity_id: null,
          block_id: item.source_ref.block_id,
        });
      }
    });
  review.entities.filter((entity) => !entityIsQueued(entity)).forEach((entity) => {
    if (!assignedCounts.get(entity.id)) {
      issues.push({
        code: "ENTITY_WITHOUT_SOURCE_BLOCKS",
        message: "Active card has no assigned source blocks.",
        entity_id: entity.id,
      });
      return;
    }
    const decision = approvalDecision("entity", entity.id, review);
    if (!decision) {
      issues.push({
        code: "MISSING_APPROVAL_DECISION",
        message: "Choose how this reviewed card should be handled.",
        entity_id: entity.id,
      });
      return;
    }
    if (["source_only", "exclude"].includes(decision.action)) return;
    if (!legacyRequiredFields[entity.entity_type]) {
      issues.push({
        code: "UNSUPPORTED_APPROVAL_DESTINATION",
        message: `${CATEGORY_LABELS[entity.entity_type] || entity.entity_type} cannot be added to the Career Vault in this phase.`,
        entity_id: entity.id,
      });
      return;
    }
    if (decision.action !== "create") return;
    const missingLabels = decision.field_resolutions == null
      ? legacyRequiredFields[entity.entity_type]
        .filter(([field]) => {
          const value = entity[field];
          return Array.isArray(value) ? value.length === 0 : !String(value || "").trim();
        })
        .map(([, label]) => label)
      : (granularRequiredFields[entity.entity_type] || [])
        .filter(([field]) => !["source", "add_source"].includes(decision.field_resolutions[field]))
        .map(([, label]) => label);
    missingLabels.forEach((label) => {
      issues.push({
        code: "MISSING_APPROVAL_FIELD",
        message: `${CATEGORY_LABELS[entity.entity_type] || entity.entity_type} requires source ${label} before creating a Career Vault record.`,
        entity_id: entity.id,
      });
    });
  });
  return issues;
}

function liveCompletionReport() {
  const review = currentReview();
  if (!review) return null;
  const unresolved = review.block_reviews.filter(
    (item) => item.disposition === "unassigned"
  );
  const validationErrorCodes = state.reviewDetail?.completion?.validation_error_codes || [];
  const suggestedBlockCount = unresolved.filter(
    (item) => item.suggested_owner_ids.length > 0
  ).length;
  const categorizedBlockCount = unresolved.filter((item) => item.category).length;
  const queuedEntityIds = review.entities
    .filter(entityIsQueued)
    .map((entity) => entity.id);
  const status = validationErrorCodes.length
    ? "invalid"
    : unresolved.length || queuedEntityIds.length ? "incomplete" : "ready";
  const approvalIssues = approvalIssuesForReview(review);
  const readyForPreview = status === "ready";
  return {
    status,
    ready_for_preview: readyForPreview,
    ready_to_approve: readyForPreview && approvalIssues.length === 0,
    unresolved_block_count: unresolved.length,
    suggested_block_count: suggestedBlockCount,
    categorized_block_count: categorizedBlockCount,
    unclassified_block_count: unresolved.length - categorizedBlockCount,
    queued_entity_count: queuedEntityIds.length,
    queued_entity_ids: queuedEntityIds,
    unresolved_blocks: unresolved.map((item) => ({
      block_id: item.source_ref.block_id,
      category: item.category,
      suggested_owner_ids: item.suggested_owner_ids,
    })),
    validation_error_codes: validationErrorCodes,
    approval_issue_count: approvalIssues.length,
    approval_issues: approvalIssues,
  };
}

function renderCompletionGate() {
  const detail = state.reviewDetail;
  const review = currentReview();
  const completion = liveCompletionReport();
  if (!detail || !review || !completion) {
    elements.completionGate.hidden = true;
    return;
  }

  elements.completionGate.hidden = false;
  elements.completionGate.className = `completion-gate ${completion.status}`;
  const blocksById = new Map(detail.blocks.map((block) => [block.id, block]));
  const entityById = new Map(review.entities.map((entity) => [entity.id, entity]));

  if (completion.status === "ready") {
    elements.completionStatus.textContent = completion.ready_to_approve
      ? "Ready for approval"
      : "Source review complete";
    elements.completionCounts.textContent = completion.ready_to_approve
      ? `${review.block_reviews.length} source decisions complete`
      : `${completion.approval_issue_count} cards need an approval choice`;
    elements.completionBlockList.innerHTML = completion.approval_issues.map((issue) => {
      const entity = entityById.get(issue.entity_id);
      return `
        <div class="completion-block-row approval-issue-row">
          <code>Approval choice</code>
          <span class="completion-block-text">${escapeHtml(entity ? entityLabel(entity) : blocksById.get(issue.block_id)?.text || issue.block_id || "Review")}</span>
          <span class="completion-suggestion">${escapeHtml(issue.message)}</span>
        </div>`;
    }).join("");
    return;
  }

  if (completion.status === "invalid") {
    elements.completionStatus.textContent = "Review has validation errors";
    elements.completionCounts.textContent = `${completion.validation_error_codes.length} errors`;
    elements.completionBlockList.innerHTML = completion.validation_error_codes.map((code) => `
      <div class="completion-block-row validation-error-row">
        <code>${escapeHtml(code)}</code>
        <span>Resolve this validation error before approval.</span>
      </div>`).join("");
    return;
  }

  elements.completionStatus.textContent = "Decisions still required";
  elements.completionCounts.textContent = `${completion.queued_entity_count} queued cards / ${completion.unresolved_block_count} unresolved blocks / ${completion.suggested_block_count} with suggestions`;
  const queuedRows = completion.queued_entity_ids.map((entityId) => {
    const entity = entityById.get(entityId);
    return `
      <div class="completion-block-row queued-completion-row">
        <code>CARD</code>
        <span class="completion-block-text">${escapeHtml(entity ? entityLabel(entity) : entityId)}</span>
        <span class="completion-suggestion">Restore or ungroup this card.</span>
      </div>`;
  }).join("");
  const blockRows = completion.unresolved_blocks.map((item) => {
    const block = blocksById.get(item.block_id);
    const suggestions = item.suggested_owner_ids
      .map((entityId) => entityById.get(entityId))
      .filter(Boolean)
      .map(entityLabel);
    const suggestionText = suggestions.length
      ? `Suggested: ${suggestions.join(", ")}`
      : "No automatic suggestion";
    return `
      <div class="completion-block-row">
        <code>${escapeHtml(item.block_id)}</code>
        <span class="completion-block-text">${escapeHtml(block?.text || "Source block unavailable")}</span>
        <span class="completion-suggestion${suggestions.length ? " has-suggestion" : ""}">
          ${item.category ? `<strong>${escapeHtml(CATEGORY_LABELS[item.category] || item.category)}</strong>` : ""}
          ${escapeHtml(suggestionText)}
        </span>
      </div>`;
  }).join("");
  elements.completionBlockList.innerHTML = queuedRows + blockRows;
}

function queuedCardHtml(entity, review, blocksById, entityById) {
  const owned = review.block_reviews.filter((item) => item.owner_id === entity.id);
  const category = entityCategory(entity);
  return `
    <details class="queued-card-item" data-queued-card-id="${escapeHtml(entity.id)}">
      <summary>
        <span class="queued-card-type">${escapeHtml(CATEGORY_LABELS[category] || category)}</span>
        <strong>${escapeHtml(entityLabel(entity))}</strong>
        <span>${owned.length} source ${owned.length === 1 ? "block" : "blocks"}</span>
      </summary>
      <div class="queued-card-body">
        <div class="queued-card-actions">
          <button class="secondary-button compact-button" data-edit-queued-card="${escapeHtml(entity.id)}" data-tooltip="Open this queued card in the details editor.">Edit details</button>
          <button class="primary-button compact-button" data-restore-card="${escapeHtml(entity.id)}" data-tooltip="Return this card to the active card hierarchy.">Restore card</button>
          <button class="text-button" data-ungroup-card="${escapeHtml(entity.id)}" data-tooltip="Remove this system-created card, but keep its source blocks for reassignment.">Ungroup card</button>
        </div>
        <div class="queued-source-blocks">
          ${owned.length ? owned.map((item) => {
            const block = blocksById.get(item.source_ref.block_id);
            return block ? sourceBlockHtml(block, item, review, entityById) : "";
          }).join("") : '<div class="review-placeholder">This card has no attached source blocks.</div>'}
        </div>
      </div>
    </details>`;
}

function renderReviewBlocks() {
  const detail = state.reviewDetail;
  const review = currentReview();
  if (!detail || !review) {
    elements.reviewBlockList.innerHTML = '<div class="review-placeholder">Build the review to inspect source blocks.</div>';
    return;
  }

  const reviewByBlock = new Map(
    review.block_reviews.map((item) => [item.source_ref.block_id, item])
  );
  const entityById = new Map(review.entities.map((entity) => [entity.id, entity]));
  const blocksById = new Map(detail.blocks.map((block) => [block.id, block]));
  const queuedEntities = review.entities.filter(entityIsQueued);
  const queuedEntityIds = new Set(queuedEntities.map((entity) => entity.id));
  const visibleQueuedEntities = state.blockFilter === "all"
    ? queuedEntities
    : state.blockFilter === "current"
      ? queuedEntities.filter((entity) => entity.id === state.selectedEntityId)
      : [];
  const groups = {
    suggested: [],
    categorized: [],
    categoryOnly: [],
    unclassified: [],
    linked: [],
    ignored: [],
    structural: [],
  };

  detail.blocks.forEach((block) => {
    const item = reviewByBlock.get(block.id);
    if (!item || (item.owner_id && queuedEntityIds.has(item.owner_id))) return;
    const group = blockReviewGroup(item);
    const belongsToCurrent = Boolean(state.selectedEntityId) && (
      item.owner_id === state.selectedEntityId
      || item.suggested_owner_ids.includes(state.selectedEntityId)
    );
    if (state.blockFilter === "all" && group === "structural") return;
    if (state.blockFilter === "current" && !belongsToCurrent) return;
    if (state.blockFilter === "unassigned" && group !== "unclassified") return;
    if (state.blockFilter === "categorized" && group !== "categorized") return;
    if (state.blockFilter === "suggested" && group !== "suggested") return;
    if (state.blockFilter === "structural" && group !== "structural") return;
    groups[group].push({ block, item });
  });

  const definitions = [
    ["suggested", "Needs review"],
    ["categorized", "Needs card"],
    ["categoryOnly", "Category only"],
    ["unclassified", "Unclassified"],
    ["linked", "Linked"],
    ["ignored", "Ignored"],
    ["structural", "Section markers"],
  ];
  const sections = definitions
    .filter(([group]) => groups[group].length)
    .map(([group, label]) => `
      <section class="source-block-group" data-review-group="${group}">
        <div class="queue-group-heading">
          <h3>${label}</h3>
          <span>${groups[group].length}</span>
        </div>
        ${groups[group].map(({ block, item }) => (
          sourceBlockHtml(block, item, review, entityById)
        )).join("")}
      </section>`);

  const queuedSection = visibleQueuedEntities.length ? `
    <section class="queued-card-group">
      <div class="queue-group-heading">
        <h3>Cards needing review</h3>
        <span>${visibleQueuedEntities.length}</span>
      </div>
      ${visibleQueuedEntities.map((entity) => (
        queuedCardHtml(entity, review, blocksById, entityById)
      )).join("")}
    </section>` : "";
  const rendered = [queuedSection, ...sections].filter(Boolean);
  elements.reviewBlockList.innerHTML = rendered.length
    ? rendered.join("")
    : '<div class="review-placeholder">No cards or blocks match this view.</div>';
}

function approvalFieldKey(field) {
  return APPROVAL_FIELD_ALIASES[field] || field;
}

function approvalValueItems(value) {
  if (Array.isArray(value)) return value.flatMap(approvalValueItems);
  const rendered = String(value || "").trim();
  return rendered ? [rendered] : [];
}

function approvalFieldMap(fields) {
  const mapped = new Map();
  Object.entries(fields || {}).forEach(([field, value]) => {
    const key = approvalFieldKey(field);
    const values = approvalValueItems(value);
    if (!values.length) return;
    const existing = mapped.get(key) || [];
    values.forEach((item) => {
      if (!existing.includes(item)) existing.push(item);
    });
    mapped.set(key, existing);
  });
  return mapped;
}

function normalizeApprovalValue(value) {
  return String(value || "")
    .normalize("NFKD")
    .toLocaleLowerCase("en-US")
    .replace(/[\u2010-\u2015]/g, "-")
    .replace(/[_/\\|,.;:()[\]{}]+/g, " ")
    .replace(/[^a-z0-9+#-]+/g, " ")
    .replace(/\s+/g, " ")
    .trim();
}

function approvalComparisonStatus(sourceValues, targetValues) {
  if (!sourceValues.length) return { key: "target-only", label: "Vault only" };
  if (!targetValues.length) return { key: "source-only", label: "Source only" };
  const source = [...new Set(sourceValues.map(normalizeApprovalValue).filter(Boolean))];
  const target = [...new Set(targetValues.map(normalizeApprovalValue).filter(Boolean))];
  if (
    source.length === target.length
    && source.every((value) => target.includes(value))
  ) {
    return { key: "exact", label: "Exact" };
  }
  const phrasesOverlap = source.some((left) => target.some((right) => (
    left.length > 2 && right.length > 2 && (left.includes(right) || right.includes(left))
  )));
  const sourceTokens = new Set(source.flatMap((value) => value.split(" ")).filter((value) => value.length > 1));
  const targetTokens = new Set(target.flatMap((value) => value.split(" ")).filter((value) => value.length > 1));
  const tokensOverlap = [...sourceTokens].some((value) => targetTokens.has(value));
  if (phrasesOverlap || tokensOverlap) return { key: "partial", label: "Partial overlap" };
  return { key: "different", label: "Different" };
}

function approvalFieldValueHtml(values) {
  return values.length
    ? escapeHtml(values.join(", "))
    : '<span class="approval-missing-value">Not present</span>';
}

function approvalComparisonRowsHtml(entity, candidate, decision) {
  const source = approvalFieldMap(entity.source_fields);
  const target = approvalFieldMap(candidate?.fields || {});
  const keys = [...new Set([...source.keys(), ...target.keys()])].sort((left, right) => {
    const leftIndex = APPROVAL_FIELD_ORDER.indexOf(left);
    const rightIndex = APPROVAL_FIELD_ORDER.indexOf(right);
    if (leftIndex === -1 && rightIndex === -1) return left.localeCompare(right);
    if (leftIndex === -1) return 1;
    if (rightIndex === -1) return -1;
    return leftIndex - rightIndex;
  });
  if (!keys.length) return '<div class="approval-empty">No populated fields</div>';
  const editable = ["merge", "create"].includes(decision?.action);
  return keys.map((key) => {
    const sourceValues = source.get(key) || [];
    const targetValues = target.get(key) || [];
    const status = approvalComparisonStatus(sourceValues, targetValues);
    const resolution = decision?.field_resolutions?.[key] || (status.key === "exact" ? "same" : "excluded");
    const sourceSelected = ["source", "add_source"].includes(resolution);
    const vaultSelected = ["vault", "excluded"].includes(resolution);
    const exact = status.key === "exact";
    const sourceAction = APPROVAL_COLLECTION_FIELDS.has(key)
      ? (candidate ? "Add source values" : "Include values")
      : (candidate ? "Use source" : "Include");
    const vaultAction = candidate ? "Keep Career Vault" : "Do not add";
    return `
      <div class="approval-diff-row approval-diff-${escapeHtml(status.key)}">
        <strong class="approval-diff-field">${escapeHtml(APPROVAL_FIELD_LABELS[key] || key.replaceAll("_", " "))}</strong>
        <button type="button"
          class="approval-field-choice${sourceSelected ? " is-selected" : ""}${exact ? " is-same" : ""}"
          data-approval-field-subject="${escapeHtml(entity.source_entity_id)}"
          data-approval-field="${escapeHtml(key)}"
          data-approval-field-choice="source"
          aria-pressed="${sourceSelected || exact}"
          ${!editable || !sourceValues.length || exact ? "disabled" : ""}>
          <span>${approvalFieldValueHtml(sourceValues)}</span>
          <small>${exact ? "Same" : sourceAction}</small>
        </button>
        <button type="button"
          class="approval-field-choice${vaultSelected ? " is-selected" : ""}${exact ? " is-same" : ""}"
          data-approval-field-subject="${escapeHtml(entity.source_entity_id)}"
          data-approval-field="${escapeHtml(key)}"
          data-approval-field-choice="vault"
          aria-pressed="${vaultSelected || exact}"
          ${!editable || exact ? "disabled" : ""}>
          <span>${approvalFieldValueHtml(targetValues)}</span>
          <small>${exact ? "Same" : vaultAction}</small>
        </button>
        <span class="approval-diff-status">${escapeHtml(status.label)}</span>
      </div>`;
  }).join("");
}

function approvalSourceBlocksHtml(blocks) {
  if (!blocks?.length) return '<div class="approval-empty">No linked source blocks</div>';
  return `
    <ol class="approval-source-blocks">
      ${blocks.map((block) => `
        <li>
          <div><code>${escapeHtml(block.block_id)}</code><span>${escapeHtml(block.kind)}</span></div>
          <p>${escapeHtml(block.text)}</p>
        </li>`).join("")}
    </ol>`;
}

function approvalDecisionValue(decision) {
  if (!decision) return "";
  if (["source_only", "exclude"].includes(decision.action)) return "exclude";
  return decision.action === "merge" ? `merge:${decision.target_id || ""}` : decision.action;
}

function approvalDecisionSelectHtml(subjectKind, subjectId, options, helpText, showUseAll = false) {
  const current = approvalDecisionValue(approvalDecision(subjectKind, subjectId));
  const renderedOptions = options.map(([value, label]) => `
    <option value="${escapeHtml(value)}"${current === value ? " selected" : ""}>${escapeHtml(label)}</option>`).join("");
  return `
    <section class="approval-decision-panel">
      <label>
        <span>Career Vault action</span>
        <select
          class="approval-decision-select"
          data-approval-subject-kind="${escapeHtml(subjectKind)}"
          data-approval-subject-id="${escapeHtml(subjectId)}"
        >${renderedOptions}</select>
      </label>
      ${showUseAll ? `<button type="button" class="secondary approval-use-all" data-approval-use-all="${escapeHtml(subjectId)}">Use all source fields</button>` : ""}
      <p>${escapeHtml(helpText)}</p>
    </section>`;
}

function approvalCategoryOnlyHtml(blocks) {
  if (!blocks?.length) return "";
  return `
    <article class="approval-item approval-unsupported approval-category-only">
      <header class="approval-item-heading">
        <div><span>Category-only content</span><h3>${blocks.length} source ${blocks.length === 1 ? "block" : "blocks"}</h3></div>
        <strong>Needs a card</strong>
      </header>
      <div class="approval-category-blocks">
        ${blocks.map((block) => `
          <section class="approval-category-block">
            ${approvalDecisionSelectHtml(
              "block", block.block_id, [["exclude", "Do not add to Career Vault"]],
              "Return to card review to attach this block before adding it."
            )}
            ${approvalSourceBlocksHtml([block])}
          </section>`).join("")}
      </div>
    </article>`;
}

function approvalTargetHtml(candidate, entity, decision) {
  const reasons = candidate?.reasons?.length
    ? candidate.reasons.map((reason) => `
      <li><strong>${escapeHtml(APPROVAL_REASON_LABELS[reason.code] || reason.code.replaceAll("_", " "))}</strong>
      ${reason.values.length ? `<span>${escapeHtml(reason.values.join(", "))}</span>` : ""}</li>`).join("")
    : "";
  return `
    <section class="approval-target">
      <div class="approval-target-heading">
        <strong>${candidate ? escapeHtml(candidate.target_label) : "New Career Vault record"}</strong>
        ${candidate ? `<code>${escapeHtml(candidate.target_id)}</code>` : ""}
      </div>
      <div class="approval-diff-grid">
        <div class="approval-diff-header">
          <span>Field</span><strong>Reviewed source</strong><strong>Career Vault</strong><span>Comparison</span>
        </div>
        ${approvalComparisonRowsHtml(entity, candidate, decision)}
      </div>
      ${reasons ? `<ul class="approval-reasons">${reasons}</ul>` : ""}
      <details class="approval-source-details">
        <summary>Source evidence (${entity.source_blocks.length})</summary>
        ${approvalSourceBlocksHtml(entity.source_blocks)}
      </details>
    </section>`;
}

function approvalEntityHtml(entity) {
  const hasDeterministicMatch = entity.candidates.some(candidateIsDeterministicMatch);
  const displayOutcome = entity.outcome === "possible_match" && !hasDeterministicMatch
    ? "no_match"
    : entity.outcome;
  const outcomeLabels = { possible_match: "Possible existing record", no_match: "No identity match", new: "New card", unsupported: "Not supported" };
  const decision = approvalDecision("entity", entity.source_entity_id);
  const candidate = approvalCandidateForDecision(entity, decision);
  const decisionOptions = [];
  if (entity.outcome === "possible_match") {
    entity.candidates.forEach((item) => {
      decisionOptions.push([`merge:${item.target_id}`, `Update ${approvalCandidatePositionLabel(item)}`]);
    });
    if (entity.entity_type !== "profile") decisionOptions.push(["create", "Create new Career Vault record"]);
  } else if (entity.outcome === "new") {
    decisionOptions.push(["create", "Create new Career Vault record"]);
  }
  decisionOptions.push(["exclude", "Do not add to Career Vault"]);
  const editable = ["merge", "create"].includes(decision?.action);
  const targetContent = editable
    ? approvalTargetHtml(candidate, entity, decision)
    : `<div class="approval-not-added"><strong>Not selected for Career Vault</strong>
        <details class="approval-source-details"><summary>Source evidence (${entity.source_blocks.length})</summary>${approvalSourceBlocksHtml(entity.source_blocks)}</details></div>`;
  const helpText = entity.outcome === "possible_match"
    ? "This is an advisory default. Choose Update, Create, or Do not add. An existing saved decision is never changed automatically."
    : decision?.action === "merge"
      ? "Career Vault values remain unchanged until a source field is selected."
      : decision?.action === "create"
        ? "Select the source fields that belong in the new record."
        : "This reviewed card will remain only in the intake history.";
  return `
    <article class="approval-item approval-${escapeHtml(displayOutcome)}">
      <header class="approval-item-heading">
        <div><span>${escapeHtml(CATEGORY_LABELS[entity.entity_type] || entity.entity_type)}</span><h3>${escapeHtml(entity.source_label)}</h3></div>
        <strong>${escapeHtml(outcomeLabels[displayOutcome] || displayOutcome)}</strong>
      </header>
      ${entity.outcome === "possible_match" ? approvalCandidatesHtml(entity.candidates) : ""}
      ${approvalDecisionSelectHtml(
        "entity", entity.source_entity_id, decisionOptions, helpText, editable
      )}
      <div class="approval-comparison">${targetContent}</div>
    </article>`;
}

function renderApprovalPreview() {
  const preview = state.approvalPreview;
  const visible = Boolean(preview);
  elements.approvalPreview.hidden = !visible;
  elements.entityHierarchy.hidden = visible;
  elements.reviewWorkspace.hidden = visible;
  if (!preview) return;

  const counts = preview.entities.reduce((result, entity) => {
    const outcome = entity.outcome === "possible_match" && !entity.candidates.some(candidateIsDeterministicMatch)
      ? "no_match"
      : entity.outcome;
    result[outcome] = (result[outcome] || 0) + 1;
    return result;
  }, {});
  elements.approvalEngine.textContent = `${preview.engine.kind} / ${preview.engine.name} v${preview.engine.version}`;
  elements.approvalPreviewSummary.innerHTML = `
    <strong>${preview.entities.length} reviewed cards</strong>
    <span>${counts.possible_match || 0} possible existing records</span>
    <span>${counts.no_match || 0} no identity match</span>
    <span>${counts.new || 0} new</span>
    <span>${counts.unsupported || 0} not promotable</span>
    <span>${preview.category_only_blocks.length} category-only</span>
    <code>review ${escapeHtml(preview.review_revision.slice(0, 15))}...</code>
    <code>vault ${escapeHtml(preview.vault_revision.slice(0, 15))}...</code>`;
  const application = state.reviewDetail?.approval_application;
  elements.saveApprovalDecisionsButton.disabled = !state.reviewDirty || Boolean(application);
  elements.applyApprovalButton.disabled = (
    Boolean(application)
    || state.reviewDirty
    || !hasCurrentIntakeApprovalPreview(preview, state.reviewDetail)
    || !liveCompletionReport()?.ready_to_approve
  );
  elements.applyApprovalButton.textContent = application ? "Applied" : "Apply to Career Vault";
  elements.approvalPreviewList.innerHTML = preview.entities.map(approvalEntityHtml).join("")
    + approvalCategoryOnlyHtml(preview.category_only_blocks);
}

async function prepareApprovalPreview() {
  const completion = liveCompletionReport();
  const request = currentReviewRequest();
  if (!completion?.ready_for_preview || !request) {
    setReviewMessage("Complete every source decision before preparing the comparison.", true);
    return;
  }
  if (state.reviewDirty && !(await saveDraft(true))) return;
  elements.prepareApprovalButton.disabled = true;
  setReviewMessage("Comparing reviewed cards with the Career Vault...");
  try {
    const preview = await requestJson(
      `/api/reviews/${encodeURIComponent(request.sourceId)}/approval-preview`
    );
    if (!isCurrentReviewRequest(request)) return;
    state.approvalPreview = preview;
    const defaultCount = applyApprovalDefaults(preview);
    setReviewMessage(defaultCount
      ? `${defaultCount} advisory default${defaultCount === 1 ? "" : "s"} prepared. Review or change them, then save decisions.`
      : "Approval preview ready. No Career Vault files were changed."
    );
    renderReview();
    elements.approvalPreview.scrollIntoView({ behavior: "smooth", block: "start" });
  } catch (error) {
    if (!isCurrentReviewRequest(request)) return;
    state.approvalPreview = null;
    setReviewMessage(formatRequestError(error), true);
    renderReview();
  }
}

async function saveApprovalDecisions() {
  const initialRequest = currentReviewRequest();
  if (!initialRequest || !state.approvalPreview || !state.reviewDirty) return;
  elements.saveApprovalDecisionsButton.disabled = true;
  setReviewMessage("Saving approval decisions to the local review draft...");
  if (!(await saveDraft(true))) {
    if (isCurrentReviewRequest(initialRequest)) renderApprovalPreview();
    return;
  }
  const request = currentReviewRequest();
  if (!request) return;
  try {
    const preview = await requestJson(
      `/api/reviews/${encodeURIComponent(request.sourceId)}/approval-preview`
    );
    if (!isCurrentReviewRequest(request)) return;
    state.approvalPreview = preview;
    setReviewMessage("Approval decisions saved to review.yaml. No Career Vault files were changed.");
    renderReview();
  } catch (error) {
    if (!isCurrentReviewRequest(request)) return;
    state.approvalPreview = null;
    setReviewMessage(formatRequestError(error), true);
    renderReview();
  }
}

async function applyApprovalDecisions() {
  const preview = state.approvalPreview;
  const completion = liveCompletionReport();
  const request = currentReviewRequest();
  if (!request || !hasCurrentIntakeApprovalPreview(preview, state.reviewDetail)
      || !completion?.ready_to_approve || state.reviewDirty) {
    setReviewMessage("Save every approval decision before applying it.", true);
    return;
  }
  const canonicalCount = (currentReview()?.approval_decisions || []).filter(
    (decision) => decision.action === "merge" || decision.action === "create"
  ).length;
  const decisions = new Map(
    (currentReview()?.approval_decisions || [])
      .filter((decision) => decision.subject_kind === "entity")
      .map((decision) => [decision.subject_id, decision])
  );
  const pendingNames = new Set();
  (currentReview()?.entities || []).forEach((entity) => {
    const decision = decisions.get(entity.id);
    const applies = decision
      && ["merge", "create"].includes(decision.action)
      && (
        !decision.field_resolutions
        || ["source", "add_source"].includes(decision.field_resolutions.technologies)
      );
    if (applies) {
      (entity.pending_skills || []).forEach((item) => pendingNames.add(normalizeReviewSkillName(item.name)));
    }
  });
  const pendingMessage = pendingNames.size
    ? ` Up to ${pendingNames.size} new custom skill${pendingNames.size === 1 ? "" : "s"} will be created after exact-match validation.`
    : "";
  const confirmed = window.confirm(
    `Apply this completed review to the local Career Vault? ${canonicalCount} card action${canonicalCount === 1 ? "" : "s"} will be written.${pendingMessage} The source review will become read-only.`
  );
  if (!confirmed) return;
  elements.applyApprovalButton.disabled = true;
  setReviewMessage("Validating and applying approval decisions...");
  try {
    const result = await requestJson(
      `/api/reviews/${encodeURIComponent(request.sourceId)}/apply-approval`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          expected_review_revision: preview.review_revision,
          expected_vault_revision: preview.vault_revision,
          expected_registry_revision: preview.registry_revision,
        }),
      }
    );
    if (!isCurrentReviewRequest(request)) return;
    state.approvalPreview = null;
    await loadReview(request.sourceId);
    if (state.reviewSourceId !== request.sourceId || state.activeView !== "review") return;
    const libraryItem = state.extractions.find((item) => item.id === request.sourceId);
    if (libraryItem) {
      Object.assign(libraryItem, {
        workflow_status: "saved_to_vault",
        saved_at: result.applied_at,
        review_entity_count: result.changes.length,
        unresolved_block_count: 0,
        approval_issue_count: 0,
      });
      renderExtractions();
    }
    setReviewMessage(
      `Career Vault updated: ${result.changed_files.length} file${result.changed_files.length === 1 ? "" : "s"} changed${result.created_skill_ids?.length ? `, ${result.created_skill_ids.length} custom skill${result.created_skill_ids.length === 1 ? "" : "s"} created` : ""}. This source review is now read-only.`
    );
  } catch (error) {
    if (!isCurrentReviewRequest(request)) return;
    setReviewMessage(formatRequestError(error), true);
    renderApprovalPreview();
  } finally {
    await pollLogs();
  }
}

function renderApprovedImportResumeAction() {
  const sourceId = currentApprovedReviewSourceId();
  elements.keepApprovedResume.hidden = !sourceId;
  if (!sourceId) return;
  const loading = state.keepApprovedResumeLoadingSourceId === sourceId;
  elements.keepApprovedResumeButton.disabled = loading;
  elements.keepApprovedResumeButton.textContent = loading
    ? "Saving..."
    : "Keep for applications";
}

async function keepApprovedImportForApplications() {
  const sourceId = currentApprovedReviewSourceId();
  if (!sourceId || state.keepApprovedResumeLoadingSourceId) return;
  const request = { sourceId, epoch: reviewLoadEpoch };
  state.keepApprovedResumeLoadingSourceId = sourceId;
  elements.keepApprovedResumeMessage.className = "form-message";
  elements.keepApprovedResumeMessage.textContent = "Saving the approved source as an application Resume Version...";
  renderApprovedImportResumeAction();
  try {
    const result = await requestJson(
      `/api/reviews/${encodeURIComponent(sourceId)}/keep-for-applications`,
      { method: "POST" }
    );
    if (!isCurrentReviewRequest(request)) return;
    invalidateResumeLibraryManagement();
    await loadResumeLibrary();
    if (!isCurrentReviewRequest(request)) return;
    const resume = result.resume;
    elements.keepApprovedResumeMessage.textContent = result.already_saved
      ? `${resume.display_name} is already available in Applications.`
      : `Saved ${resume.display_name}. It is now available in Applications.`;
    toast.show({
      message: result.already_saved
        ? `Resume already saved: ${resume.display_name}.`
        : `Resume saved: ${resume.display_name}.`,
    });
  } catch (error) {
    if (!isCurrentReviewRequest(request)) return;
    elements.keepApprovedResumeMessage.className = "form-message error";
    elements.keepApprovedResumeMessage.textContent = formatRequestError(error);
  } finally {
    if (state.keepApprovedResumeLoadingSourceId === sourceId) {
      state.keepApprovedResumeLoadingSourceId = null;
      renderApprovedImportResumeAction();
    }
  }
}

function renderApprovedImportRemovalAction() {
  const sourceId = currentApprovedReviewSourceId();
  elements.removeApprovedImport.hidden = !sourceId;
  if (!sourceId) return;
  const loading = state.removeApprovedImportLoadingSourceId === sourceId;
  elements.removeApprovedImportButton.disabled = loading;
  elements.removeApprovedImportButton.textContent = loading
    ? "Removing..."
    : "Remove original file";
}

async function removeApprovedImportSource() {
  const sourceId = currentApprovedReviewSourceId();
  if (!sourceId || state.removeApprovedImportLoadingSourceId) return;
  if (!window.confirm(
    "Permanently remove the verified original import file? Career Vault, the review, and its approval audit will remain. You cannot keep this exact file for applications afterwards unless it was saved first."
  )) return;
  const request = { sourceId, epoch: reviewLoadEpoch };
  state.removeApprovedImportLoadingSourceId = sourceId;
  elements.removeApprovedImportMessage.className = "form-message";
  elements.removeApprovedImportMessage.textContent = "Verifying the approved original before removing it...";
  renderApprovedImportRemovalAction();
  try {
    const result = await requestJson(
      `/api/reviews/${encodeURIComponent(sourceId)}/source`,
      { method: "DELETE" }
    );
    if (!isCurrentReviewRequest(request)) return;
    await loadResumeCollections({ force: true });
    if (!isCurrentReviewRequest(request)) return;
    elements.removeApprovedImportMessage.textContent = result.already_removed
      ? "The original import file was already removed. Career Vault and the approval audit remain available."
      : "Original import file removed. Career Vault, the review, and the approval audit remain available.";
    toast.show({
      message: result.already_removed
        ? "Original import file was already removed."
        : "Original import file removed.",
    });
  } catch (error) {
    if (!isCurrentReviewRequest(request)) return;
    elements.removeApprovedImportMessage.className = "form-message error";
    elements.removeApprovedImportMessage.textContent = formatRequestError(error);
  } finally {
    if (state.removeApprovedImportLoadingSourceId === sourceId) {
      state.removeApprovedImportLoadingSourceId = null;
      renderApprovedImportRemovalAction();
    }
  }
}

function closeApprovalPreview() {
  state.approvalPreview = null;
  setReviewMessage("");
  renderReview();
}

export function renderReview() {
  const review = currentReview();
  const extraction = state.extractions.find((item) => item.id === state.reviewSourceId);
  elements.reviewPath.textContent = state.reviewDetail?.source_name || extraction?.source_name || "Select an extraction.";
  if (review && !review.entities.some((item) => item.id === state.selectedEntityId)) {
    state.selectedEntityId = review.entities[0]?.id || null;
  }
  document.querySelectorAll("[data-block-filter]").forEach((button) => {
    button.classList.toggle("active", button.dataset.blockFilter === state.blockFilter);
  });
  renderReviewToolbar();
  renderApprovedImportResumeAction();
  renderApprovedImportRemovalAction();
  renderCompletionGate();
  renderApprovalPreview();
  renderEntityHierarchy();
  renderCardEditor();
  renderReviewBlocks();
  if (state.reviewDetail?.approval_application) {
    elements.cardEditor.querySelectorAll("input, textarea, select, button").forEach((control) => {
      control.disabled = true;
    });
    elements.reviewBlockList.querySelectorAll("input, textarea, select, button").forEach((control) => {
      control.disabled = true;
    });
    elements.deleteCardButton.disabled = true;
    elements.restoreCardButton.disabled = true;
    elements.ungroupCardButton.disabled = true;
    elements.acceptCardButton.disabled = true;
  }
}

export async function loadReview(sourceId, { epoch = null } = {}) {
  if (!sourceId) return;
  const requestEpoch = epoch ?? beginReviewLoad(sourceId);
  const isCurrent = () => isCurrentReviewLoad(sourceId, requestEpoch);
  renderReview();
  try {
    await loadExtraction(sourceId, { isCurrent });
  } catch (error) {
    if (isCurrent()) setReviewMessage(formatRequestError(error), true);
  }
  if (!isCurrent()) return;
  try {
    const detail = await requestJson(`/api/reviews/${encodeURIComponent(sourceId)}`);
    if (!isCurrent() || detail.source_id !== sourceId) return;
    state.reviewDetail = detail;
    if (!state.selectedEntityId) {
      state.selectedEntityId = state.reviewDetail.review.entities[0]?.id || null;
    }
    setReviewMessage(state.reviewDetail.approval_application
      ? "This intake review was applied and is now read-only."
      : "");
  } catch (error) {
    if (!isCurrent()) return;
    if (error.status !== 404) {
      setReviewMessage(formatRequestError(error), true);
    } else {
      setReviewMessage("No review exists yet. Build it to begin card work.");
    }
    state.reviewDetail = null;
    state.selectedEntityId = null;
  }
  if (isCurrent()) renderReview();
}

export function markReviewDirty() {
  if (state.reviewDetail?.approval_application) return;
  state.reviewDirty = true;
  state.approvalPreview = null;
  renderReviewToolbar();
}

async function buildReview() {
  const request = activeReviewLoadRequest();
  if (!request) return;
  state.approvalPreview = null;
  elements.buildReviewButton.disabled = true;
  setReviewMessage("Building a complete source-block review...");
  try {
    const detail = await requestJson("/api/reviews/build", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        source_id: request.sourceId,
        expected_review_revision: state.reviewDetail?.review_revision || null,
      }),
    });
    if (!isCurrentReviewLoad(request.sourceId, request.epoch) || detail.source_id !== request.sourceId) return;
    state.reviewDetail = detail;
    state.reviewDirty = false;
    state.selectedEntityId = state.reviewDetail.review.entities[0]?.id || null;
    const operation = state.reviewDetail.operation;
    const needsInitialDetection = state.reviewDetail.review.entities.length === 0;
    setReviewMessage(`Source inventory ready: ${operation.new_decision_count} new and ${operation.preserved_decision_count} preserved block decisions.`);
    await loadResumeCollections({ force: true });
    if (!isCurrentReviewLoad(request.sourceId, request.epoch)) return;
    if (needsInitialDetection) await suggestCards();
  } catch (error) {
    if (!isCurrentReviewLoad(request.sourceId, request.epoch)) return;
    setReviewMessage(formatRequestError(error), true);
  } finally {
    if (isCurrentReviewLoad(request.sourceId, request.epoch)) renderReview();
    await pollLogs();
  }
}

async function saveDraft(silent = false) {
  const review = currentReview();
  const request = currentReviewRequest();
  if (!review || !request) return false;
  pruneApprovalDecisions(review);
  elements.saveDraftButton.disabled = true;
  if (!silent) setReviewMessage("Saving draft...");
  try {
    const detail = await requestJson(`/api/reviews/${encodeURIComponent(request.sourceId)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        review,
        expected_review_revision: state.reviewDetail.review_revision,
      }),
    });
    if (!isCurrentReviewRequest(request) || detail.source_id !== request.sourceId) return false;
    state.reviewDetail = detail;
    state.reviewDirty = false;
    if (!silent) setReviewMessage("Draft saved locally.");
    renderReview();
    return true;
  } catch (error) {
    if (!isCurrentReviewRequest(request)) return false;
    setReviewMessage(formatRequestError(error), true);
    renderReviewToolbar();
    return false;
  } finally {
    await pollLogs();
  }
}

async function suggestCards() {
  if (!currentReview()) return;
  state.approvalPreview = null;
  if (state.reviewDirty && !(await saveDraft(true))) return;
  const request = currentReviewRequest();
  if (!request) return;
  elements.suggestCardsButton.disabled = true;
  setReviewMessage("Detecting cards and linking structurally clear resume sections...");
  try {
    const detail = await requestJson(`/api/reviews/${encodeURIComponent(request.sourceId)}/suggest`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        expected_review_revision: state.reviewDetail.review_revision,
      }),
    });
    if (!isCurrentReviewRequest(request) || detail.source_id !== request.sourceId) return;
    state.reviewDetail = detail;
    state.reviewDirty = false;
    state.selectedEntityId = state.reviewDetail.review.entities[0]?.id || null;
    const stats = state.reviewDetail.suggestion;
    setReviewMessage(
      stats.card_count + " cards found ("
      + stats.profile_card_count + " profile, "
      + stats.experience_card_count + " experience, "
      + stats.education_card_count + " education, "
      + stats.project_card_count + " projects, "
      + stats.summary_card_count + " summary, "
      + stats.skill_card_count + " skill groups, "
      + stats.certification_card_count + " certifications); "
      + stats.structural_block_count + " section markers classified; "
      + stats.new_card_count + " new cards; "
      + stats.auto_assigned_link_count + " structurally clear links added and "
      + stats.suggested_link_count + " links left for review."
    );
  } catch (error) {
    if (!isCurrentReviewRequest(request)) return;
    setReviewMessage(formatRequestError(error), true);
  } finally {
    if (isCurrentReviewRequest(request)) renderReview();
    await pollLogs();
  }
}

function acceptSuggestions() {
  const review = currentReview();
  if (!review) return;
  const entityById = new Map(review.entities.map((entity) => [entity.id, entity]));
  let accepted = 0;
  review.block_reviews.forEach((item) => {
    const valid = item.suggested_owner_ids.filter((id) => entityById.has(id));
    if (item.disposition === "unassigned" && valid.length === 1) {
      item.disposition = "assigned";
      item.owner_id = valid[0];
      item.category = entityCategory(entityById.get(valid[0]));
      item.suggested_owner_ids = [];
      accepted += 1;
    }
  });
  markReviewDirty();
  setReviewMessage(`${accepted} suggested block links accepted. Save the review to persist them.`);
  renderReview();
}

function acceptSelectedCardSuggestions() {
  const review = currentReview();
  const entity = review?.entities.find((item) => item.id === state.selectedEntityId);
  if (!review || !entity) return;
  let accepted = 0;
  review.block_reviews.forEach((item) => {
    if (
      item.disposition === "unassigned"
      && item.suggested_owner_ids.length === 1
      && item.suggested_owner_ids[0] === entity.id
    ) {
      item.disposition = "assigned";
      item.owner_id = entity.id;
      item.category = entityCategory(entity);
      item.suggested_owner_ids = [];
      accepted += 1;
    }
  });
  markReviewDirty();
  setReviewMessage(
    accepted + " links accepted for " + entityLabel(entity)
    + ". Save draft to persist them."
  );
  renderReview();
}

function moveSelectedCardToReview() {
  const review = currentReview();
  const entity = review?.entities.find((item) => item.id === state.selectedEntityId);
  if (!review || !entity || entity.entity_type === "profile") return;
  entity.review_state = "queued";
  state.blockFilter = "current";
  markReviewDirty();
  setReviewMessage(`${entityLabel(entity)} moved to the review queue with its source blocks intact.`);
  renderReview();
}

function restoreCard(entityId) {
  const review = currentReview();
  const entity = review?.entities.find((item) => item.id === entityId);
  if (!review || !entity) return;
  entity.review_state = "active";
  state.selectedEntityId = entity.id;
  state.blockFilter = "current";
  markReviewDirty();
  setReviewMessage(`${entityLabel(entity)} restored to the active hierarchy.`);
  renderReview();
}

function ungroupCard(entityId) {
  const review = currentReview();
  const entity = review?.entities.find((item) => item.id === entityId);
  if (!review || !entity || !entityIsQueued(entity)) return;
  const category = entityCategory(entity);
  if (!window.confirm(`Ungroup ${entityLabel(entity)}? The card will be removed, but its source blocks will stay under ${CATEGORY_LABELS[category] || category} for reassignment.`)) return;
  review.entities = review.entities.filter((item) => item.id !== entity.id);
  review.block_reviews.forEach((item) => {
    if (item.owner_id === entity.id) {
      item.disposition = item.category ? "categorized" : "unassigned";
      item.owner_id = null;
    }
    item.suggested_owner_ids = item.suggested_owner_ids.filter((id) => id !== entity.id);
  });
  state.selectedEntityId = review.entities.find((item) => !entityIsQueued(item))?.id || null;
  state.blockFilter = "all";
  markReviewDirty();
  setReviewMessage(`Card ungrouped. Its source blocks remain under ${CATEGORY_LABELS[category] || category} for reassignment.`);
  renderReview();
}

export function initIntakeReview(deps = {}) {
  ({ loadResumeCollections, navigateToView, openSkillQuickAction } = deps);
  elements.reviewBackButton.addEventListener("click", () => { void navigateToView("intake"); });
  elements.buildReviewButton.addEventListener("click", buildReview);
  elements.suggestCardsButton.addEventListener("click", suggestCards);
  elements.acceptSuggestionsButton.addEventListener("click", acceptSuggestions);
  elements.acceptCardButton.addEventListener("click", acceptSelectedCardSuggestions);
  elements.saveDraftButton.addEventListener("click", () => saveDraft(false));
  elements.prepareApprovalButton.addEventListener("click", prepareApprovalPreview);
  elements.closeApprovalButton.addEventListener("click", closeApprovalPreview);
  elements.saveApprovalDecisionsButton.addEventListener("click", saveApprovalDecisions);
  elements.applyApprovalButton.addEventListener("click", applyApprovalDecisions);
  elements.keepApprovedResumeButton.addEventListener("click", keepApprovedImportForApplications);
  elements.removeApprovedImportButton.addEventListener("click", removeApprovedImportSource);
  elements.approvalPreviewList.addEventListener("change", (event) => {
    const select = event.target.closest(".approval-decision-select");
    if (!select) return;
    setApprovalDecision(
      select.dataset.approvalSubjectKind,
      select.dataset.approvalSubjectId,
      select.value
    );
    setReviewMessage("Approval decision changed. Save decisions to persist it.");
    renderApprovalPreview();
  });
  elements.approvalPreviewList.addEventListener("click", (event) => {
    const useAll = event.target.closest("[data-approval-use-all]");
    if (useAll) {
      useAllApprovalSourceFields(useAll.dataset.approvalUseAll);
      setReviewMessage("All available source fields selected. Save decisions to persist them.");
      renderApprovalPreview();
      return;
    }
    const fieldChoice = event.target.closest("[data-approval-field-choice]");
    if (!fieldChoice || fieldChoice.disabled) return;
    setApprovalFieldResolution(
      fieldChoice.dataset.approvalFieldSubject,
      fieldChoice.dataset.approvalField,
      fieldChoice.dataset.approvalFieldChoice
    );
    setReviewMessage("Field decision changed. Save decisions to persist it.");
    renderApprovalPreview();
  });
  elements.deleteCardButton.addEventListener("click", moveSelectedCardToReview);
  elements.restoreCardButton.addEventListener("click", () => restoreCard(state.selectedEntityId));
  elements.ungroupCardButton.addEventListener("click", () => ungroupCard(state.selectedEntityId));
  elements.entityHierarchy.addEventListener("click", (event) => {
    const card = event.target.closest("[data-entity-id]");
    if (!card) return;
    state.selectedEntityId = card.dataset.entityId;
    state.blockFilter = "current";
    setReviewInspectorOpen(true);
    renderReview();
  });
  elements.closeReviewInspector.addEventListener("click", () => setReviewInspectorOpen(false));
  elements.reviewInspectorBackdrop.addEventListener("click", () => setReviewInspectorOpen(false));
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && state.reviewInspectorOpen) {
      setReviewInspectorOpen(false);
    }
  });
  elements.cardEditor.addEventListener("input", (event) => {
    const field = event.target.dataset.cardField;
    const entity = currentReview()?.entities.find((item) => item.id === state.selectedEntityId);
    if (!field || !entity) return;
    const listFields = new Set(["technologies", "emails", "phones", "links", "honors", "items"]);
    const rawItems = field === "items" && ["project", "certification"].includes(entity.entity_type)
      ? event.target.value.split(/\r?\n/)
      : event.target.value.split(",");
    entity[field] = listFields.has(field)
      ? [...new Set(rawItems.map((item) => item.trim()).filter(Boolean))]
      : event.target.value || (field === "user_note" ? null : "");
    markReviewDirty();
    renderEntityHierarchy();
  });
  elements.cardEditor.addEventListener("click", (event) => {
    const entity = currentReview()?.entities.find((item) => item.id === state.selectedEntityId);
    if (!entity) return;
    const addButton = event.target.closest("[data-add-review-skills]");
    if (addButton) {
      const input = addButton.closest("[data-review-skills-entity]")?.querySelector("[data-review-skill-input]");
      if (input && addReviewSkills(entity, input.value)) input.value = "";
      return;
    }
    const removeSkill = event.target.closest("[data-remove-review-skill-id]");
    if (removeSkill) {
      entity.technologies = (entity.technologies || []).filter(
        (skillId) => skillId !== removeSkill.dataset.removeReviewSkillId
      );
      markReviewDirty();
      renderEntityHierarchy();
      renderCardEditor();
      return;
    }
    const removePending = event.target.closest("[data-remove-pending-skill]");
    if (removePending) {
      entity.pending_skills = (entity.pending_skills || []).filter(
        (_, index) => index !== Number(removePending.dataset.removePendingSkill)
      );
      markReviewDirty();
      renderEntityHierarchy();
      renderCardEditor();
    }
  });
  elements.cardEditor.addEventListener("keydown", (event) => {
    const input = event.target.closest("[data-review-skill-input]");
    if (!input || !["Enter", "Tab", ","].includes(event.key) || !input.value.trim()) return;
    const entity = currentReview()?.entities.find((item) => item.id === state.selectedEntityId);
    if (!entity) return;
    event.preventDefault();
    if (addReviewSkills(entity, input.value)) input.value = "";
  });
  elements.cardEditor.addEventListener("change", (event) => {
    const select = event.target.closest("[data-pending-skill-group]");
    if (!select) return;
    const entity = currentReview()?.entities.find((item) => item.id === state.selectedEntityId);
    const index = Number(select.dataset.pendingSkillGroup);
    const pending = entity?.pending_skills?.[index];
    if (!pending) return;
    if (select.value === "__create_group__") {
      select.value = pending.primary_group_id || state.skillsRegistry?.default_custom_group_id || "";
      void openSkillQuickAction("group", {
        reviewEntityId: entity.id,
        pendingSkillIndex: index,
      });
      return;
    }
    pending.primary_group_id = select.value;
    markReviewDirty();
  });
  elements.reviewBlockList.addEventListener("click", (event) => {
    const editButton = event.target.closest("[data-edit-queued-card]");
    const restoreButton = event.target.closest("[data-restore-card]");
    const ungroupButton = event.target.closest("[data-ungroup-card]");
    if (editButton) {
      state.selectedEntityId = editButton.dataset.editQueuedCard;
      state.blockFilter = "current";
      setReviewInspectorOpen(true);
      renderReview();
    } else if (restoreButton) {
      restoreCard(restoreButton.dataset.restoreCard);
    } else if (ungroupButton) {
      ungroupCard(ungroupButton.dataset.ungroupCard);
    }
  });
  elements.reviewBlockList.addEventListener("change", (event) => {
    const categorySelect = event.target.closest(".category-select");
    const ownerSelect = event.target.closest(".owner-select");
    const select = categorySelect || ownerSelect;
    if (!select) return;
    const review = currentReview();
    const item = review?.block_reviews.find(
      (reviewItem) => reviewItem.source_ref.block_id === select.dataset.blockId
    );
    if (!review || !item) return;
    const entityById = new Map(review.entities.map((entity) => [entity.id, entity]));

    if (categorySelect) {
      if (select.value === "__structural") {
        item.disposition = "structural";
        item.owner_id = null;
        item.suggested_owner_ids = [];
      } else if (select.value === "__ignored") {
        item.disposition = "ignored";
        item.owner_id = null;
        item.suggested_owner_ids = [];
      } else {
        item.disposition = "unassigned";
        item.category = select.value || null;
        const owner = item.owner_id ? entityById.get(item.owner_id) : null;
        if (!owner || entityCategory(owner) !== item.category) item.owner_id = null;
        item.suggested_owner_ids = item.suggested_owner_ids.filter((entityId) => {
          const entity = entityById.get(entityId);
          return entity && (!item.category || entityCategory(entity) === item.category);
        });
        if (item.owner_id) item.disposition = "assigned";
      }
    } else if (!select.value) {
      item.disposition = "unassigned";
      item.owner_id = null;
    } else if (select.value === "__category__") {
      item.disposition = "categorized";
      item.owner_id = null;
      item.suggested_owner_ids = [];
    } else {
      const owner = entityById.get(select.value);
      if (!owner) return;
      item.disposition = "assigned";
      item.owner_id = owner.id;
      item.category = entityCategory(owner);
      item.suggested_owner_ids = [];
    }

    const scrollTop = elements.reviewBlockList.scrollTop;
    markReviewDirty();
    renderReview();
    elements.reviewBlockList.scrollTop = scrollTop;
  });
  document.querySelectorAll("[data-block-filter]").forEach((button) => {
    button.addEventListener("click", () => {
      state.blockFilter = button.dataset.blockFilter;
      document.querySelectorAll("[data-block-filter]").forEach((item) => {
        item.classList.toggle("active", item === button);
      });
      renderReviewBlocks();
    });
  });
}
