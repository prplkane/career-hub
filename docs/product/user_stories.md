# User Stories

This document captures historical product intent by phase. It does not authorize implementation by itself; current capability status and acceptance are defined by the [Capability Acceptance Registry](../process/acceptance_registry.md).

## MVP / Foundation Stories

These stories support the local-first foundation before resume generation, AI calls, dashboard, or automation.

### Phase 1: Career Vault

#### Manage Career Vault Without A Document

As a user, I want to create and edit Career Vault records manually, so that my
source of truth does not depend on importing a resume.

Acceptance criteria:

- The user can create, edit, archive, and restore a record.
- Changes are validated before canonical save.
- Manual records are identified as user-entered.
- System record types remain stable while category labels are user-managed.
- Experience and project items can be edited without exposing storage details.

#### Store Career Profile

As a user, I want my career profile to be stored locally, so that I can reuse consistent personal information across resumes and applications.

Acceptance criteria:

- Profile data is stored in `career_vault/profile.yaml`.
- Required profile fields are validated.
- Missing or malformed profile data produces a clear validation error.

#### Split Resume Into Experience Blocks

As a user, I want my resume experience to be split into reusable experience blocks, so that I can manage my work history without editing many resume files manually.

Acceptance criteria:

- Experience blocks are stored in `career_vault/experience_blocks.yaml`.
- Each block has an id, company, role, dates, category, technologies, and proof points.
- The validator reports missing required fields.

#### Manage Project Blocks

As a user, I want my projects to be stored as reusable project blocks, so that I can select relevant projects for different job types later.

Acceptance criteria:

- Project blocks are stored in `career_vault/project_blocks.yaml`.
- Each block has an id, name, category, summary, technologies, and proof points.
- Project data remains separate from generated resume text.

#### Manage Skill Vault

As a user, I want to have a skill vault, so that I can manage, edit, archive, organize, and filter skill categories.

Acceptance criteria:

- Skills are stored in `career_vault/skill_blocks.yaml`.
- Each skill block has an id, skill family, related experience, and keywords.
- Future filtering can use skill family, keywords, and related experience ids.

#### Track Proof And Evidence

As a user, I want proof blocks connected to my experience, projects, and skills, so that future resume drafts stay traceable to source evidence.

Acceptance criteria:

- Proof blocks are stored in `career_vault/proof_blocks.yaml`.
- Each proof block has an id, claim, evidence type, and source block.
- Claims remain traceable to their source blocks and source notes.

#### Validate Career Vault

As a user, I want the system to validate all Career Vault files, so that I can catch missing or malformed data before later workflows use it.

Acceptance criteria:

- Validation runs locally.
- Validation loads all required Career Vault YAML files.
- Validation reports missing files, malformed YAML, wrong top-level types, and missing required fields.

### Phase 2: Manual Job Intake

#### Save Job Description Locally

As a user, I want to save a job description locally, so that I can review and process it without scraping or browser automation.

Acceptance criteria:

- Raw job descriptions are saved under `jobs/incoming/`.
- The original text is preserved.
- No scraper is required.

#### Normalize Job Information

As a user, I want a job description normalized into a consistent job card, so that later scoring and matching can use predictable fields.

Acceptance criteria:

- Normalized job cards are saved under `jobs/normalized/`.
- The job card separates required skills, platforms, capabilities, experience, education, preferred skills, visa signals, source URL, and raw JD path.
- Fields that cannot be determined safely are left blank or marked for manual review.

#### Review A Job Against Career Vault

As a user, I want deterministic Career Vault matches shown beside a reviewed
job, so that I can prepare an application without requiring AI.

Acceptance criteria:

- Found evidence, missing evidence, and unresolved requirements are separate.
- Eligibility and technical findings are advisory.
- No result prevents the user from choosing to apply.

#### Attach The Resume Used

As a user, I want to attach the exact resume used for an application, so that I
can later identify which version was submitted.

Acceptance criteria:

- The private library records immutable file metadata, while the UI exposes a searchable display name rather than storage details.
- Attaching an existing resume does not generate or rewrite it.
- The job can remain in preparation or be marked Applied with no resume selected; the ledger records that truthful state.

#### Track Application Outcome

As a user, I want to record application status changes, so that I can see what I
submitted and what happened afterward.

Acceptance criteria:

- Job and application history persists locally.
- Status changes retain their dates rather than overwriting all history.
- The user can record applied, interview, offer, rejected, withdrawn, and
  closed outcomes.

### Phase 3: Scoring Foundation

#### Identify Material Risks

As a user, I want the system to flag material job risks, so that I can prioritize applications without losing the option to apply.

Acceptance criteria:

- Eligibility blockers include citizenship requirements, active clearance requirements, and explicit no-visa/no-OPT language.
- Missing required evidence is reported separately as a requirement gap.
- Every signal has an explanation, and no signal disables the application workflow.
- No AI API call is required for the first version.

#### Assign Review Lane

As a user, I want each job to receive a review lane, so that I can decide whether to prioritize, review, draft, archive, or reject it.

Acceptance criteria:

- Lane options are documented.
- The score categories are explainable.
- The result includes the biggest gap, matched and missing requirements, and Career Vault evidence ids.

### Phase 4: Base Resume Versions

#### Store Approved Resume Versions

As a user, I want to store approved resume versions, so that I can reuse strong resumes instead of starting from scratch.

Acceptance criteria:

- Each resume version has a folder under `resume_versions/`.
- Each version has resume content and metadata.
- Metadata records role family, seniority, target keywords, covered skills, used evidence blocks, review status, quality status, and outcome.

#### Build AI-Ready Resume Foundations

As a user, I want resume versions and vault blocks to be structured for future AI-assisted drafting, so that generated resumes can be grounded in approved evidence.

Acceptance criteria:

- Resume version metadata references Career Vault block ids.
- Approved and draft content are clearly distinguishable.
- Future AI drafting can trace bullets back to evidence blocks.
- No AI generation is required in Phase 4 foundation work.

## Long-Term Stories

These stories are useful later, after the MVP foundation is stable.

### Phase 5: Reuse Decision

As a user, I want the system to decide whether to reuse, modify, or create a resume draft, so that I do not rewrite everything for each job.

Acceptance criteria:

- The decision considers role family, seniority, required skills, preferred skills, domain, and manual approval status.
- The result explains why a resume version was selected or rejected.
- The system prefers reuse before generation.

### Phase 6: Resume Composer

As a user, I want the system to assemble a targeted resume draft from approved evidence, so that I can review a relevant draft faster.

Acceptance criteria:

- Draft content is traceable to Career Vault and resume version metadata.
- Unsupported skills or missing evidence are flagged instead of hidden.
- Drafts are marked as not manually approved until reviewed.

### Dynamic Career Vault Intake

As a user, I want to paste a resume, notes, or raw career material and have the system suggest YAML blocks, so that I can build my vault faster.

Acceptance criteria:

- Suggested blocks require manual review before saving.
- The system does not overwrite existing vault data silently.
- Weak evidence remains marked as weak or draft.

### Guided Vault Editor

As a user, I want a guided form for editing vault blocks, so that I can manage YAML data without manually editing every file.

Acceptance criteria:

- The editor supports create, update, archive, delete, organize, and filter workflows.
- Changes are validated before saving.
- The original local-first data model remains the source of truth.

### UI And Service Layer

As a user, I want a modern local or hosted interface later, so that I can manage the workflow more comfortably when the core logic is stable.

Acceptance criteria:

- UI work follows the architecture direction in `docs/reference/architecture.md`.
- API/UI layers reuse core logic instead of duplicating it.
- Hosted services are added only when explicitly approved.

### Technology Demand Analytics

As a user, I want recurring JD technologies compared with my Career Vault, so
that I can see which strengths to emphasize and which market skills I lack.

### User-Extended Skill Dictionary

As a user, I want to add reviewed skills, aliases, and stacks, so that the local
dictionary improves without silently learning incorrect terms.

### Contacts And Referrals

As a user, I want optional company contacts, leads, and referrals linked to an
application, so that I can remember how I approached the company.
