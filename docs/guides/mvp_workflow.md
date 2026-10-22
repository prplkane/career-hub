# MVP Workflow

This document defines the user-facing MVP. Development phase numbers in older
documents describe implementation history; they are not the steps a user must
complete for every application.

## Product Boundary

The MVP is local-first and works without a language model, scraper, browser
extension, cloud service, or generated resume.

The MVP has one foundation and three application stages:

```text
Career Vault
  -> 1. Job intake
  -> 2. Review and prepare
  -> 3. Track application
```

## Resume Entry Contract

The current implemented UI keeps two durable collections separate:

- **Add resume > Update Career Vault** enters the existing source-intake,
  review, and approval flow.
- **Add resume > Save for applications** stores an immutable Resume Version
  without changing Career Vault or creating intake artifacts.
- **Applications** uploads or selects active immutable Resume Versions from its
  searchable picker.

**Add resume** displays a bounded list of unfinished imports as contextual
continuations. It is not a source-document library. **Resume Library** means
reusable application materials only.

The implemented entry contract is recorded in the [Capability Acceptance
Registry](../process/acceptance_registry.md):

```text
Add resume
  -> Update Career Vault
       -> extract -> review -> explicitly approve -> apply canonical facts
  -> Save for applications
       -> save one immutable application Resume Version
```

These target rules are fixed:

- Career Vault becomes canonical only after explicit approval is applied.
- Assessment is not a third Add resume intent.
- An import-only file is not a durable Career Vault dependency.
- After approval has been successfully applied, **Keep for applications** may
  explicitly retain the original source as a Resume Version without changing
  approved Career Vault records or the approval audit.
- After approval, the user may explicitly remove only the verified original file
  under `career_vault/raw/`. The action is idempotent and cannot remove Vault
  YAML, `review.yaml`, `extracted.yaml`, or `approval_result.yaml`.
- A generated resume may be imported again and receives the same manual review;
  it never updates Career Vault automatically.
- Duplicate suggestions are advisory and non-blocking. The user keeps the final
  Update, Create, or Do not add decision.
- Processed imports do not become application Resume Library items unless the
  user explicitly saves one for applications.
- No generic Source Documents library is part of the user-facing target model.

Resume Library management and guarded permanent deletion of unused Resume
Versions are implemented. Review remains contextual to Add resume and is not a
permanent sidebar item; completed imports stay in their local audit artifacts.
An open review uses the local route `#review/<source_id>` so browser Back and a
reloaded or pasted local review link restore the same contextual review.

## Foundation: Career Vault

Career Vault is the editable source of truth for candidate facts.

Required MVP behavior:

- Browse, search, and filter existing records.
- Create, edit, archive, and restore records without importing a document.
- Validate every change before it becomes canonical.
- Preserve document-derived provenance when it exists.
- Show a bounded list of unfinished resume imports, with status derived from
  extraction, review, and approval artifacts, on Add resume.
- Keep extraction diagnostics available inside Review without making them a separate workflow.
- Mark manually entered records with `source: manual`.
- Never invent or silently infer career facts.

System record types remain stable:

- Profile
- Experience
- Project
- Education
- Skill
- Certification

Implemented manual editor coverage includes Profile, Experience, Project, Skill,
Education, and Certification. Profile is a guarded singleton; the other record
types support manual creation and reversible archive.

Users may create category labels within those record types. Examples include
`Backend Engineering`, `Automation`, or `Cloud and Delivery`. A category is an
organizational label, not a new schema.

Experience and project records may contain editable responsibility,
achievement, or evidence items. The UI may call these items rather than
requiring the user to understand internal proof-block terminology.

## Stage 1: Job Intake

The user pastes a job description and may add its source URL.

The system:

- preserves the original job description;
- extracts a draft company, title, location, requirements, technologies, and
  eligibility signals;
- keeps unresolved text visible for review;
- detects exact duplicate source content;
- extracts explicit source-backed compensation ranges without guessing missing currency or period;
- saves a normalized JD draft, its local ledger record, and its deterministic assessment before returning success in Job Library.

No match result may prevent the user from continuing.

## Stage 2: Review And Prepare

The system compares the saved job with Career Vault and stores the result as
advisory data, not as a workflow status.

The user can see:

- evidence found in Career Vault;
- skills or requirements not found;
- eligibility or location notes;
- fields that still need manual review.

Every valid saved job is already an application-ledger record. The user may
prepare materials while it is Saved and changes status only to record workflow
progress; there is no separate consider, skip, or start-application gate.

Job Workspace has two direct material choices, not a separate workflow phase:
**Tailor resume** and **Create cover letter**. Tailor resume embeds a
deterministic **Content and evidence** section in the same workspace as wording,
preview, and export. The user chooses section order, one- or two-page intent,
experience inclusion, proof points, projects, skill keywords, and education.
Incomplete selection may be saved; only a complete current selection can feed
the tailored document. The revisioned Resume Plan remains an internal contract
and neither it nor application-only wording changes Career Vault.

**Resume Version** is available at every supported application stage. Job
Workspace searches saved private resumes, uploads into the same picker, or
records **Not recorded**. The user may instead build a job-scoped tailored
resume from an approved plan and export it locally; selecting or attaching that
generated file remains an explicit step. When the record first becomes Applied, or is first
recorded at Interview/Offer after a missed Applied transition, it keeps the
selected immutable version as historical evidence without inventing an Applied
event.

## Stage 3: Track Application

The user records what happened using the same local ledger record.

Visible application statuses:

- Saved (stored internally as captured)
- Preparing
- Applied
- Interview
- Offer
- Rejected
- Withdrawn
- Closed

The application record stores status history, relevant dates, the resume used,
source URL, and user notes. Match results are advisory and never disable a
status change.

Current implementation: the local SQLite repository, CLI, and browser
Applications ledger stores current stage and append-only status history. It
supports search, stage filters, Current/Closed/All scopes, inline updates,
source links, a detail drawer with compact assessment and applied-date summaries,
and a searchable optional resume picker at every application stage. Every valid
saved JD appears in the ledger.

## Storage Boundary

- Career Vault remains portable YAML for the MVP.
- Job and application history should use local SQLite because it changes
  frequently and needs filtering, deduplication, and status history.
- Resume files are copied once into the private ignored
  `applications/resumes/library/` directory as immutable versions. SQLite stores
  private file metadata, each job's current selection, and its Applied snapshot.
- Per-application Resume Plans are revisioned JSON snapshots in SQLite because
  they are mutable workflow state, not canonical career facts.
- Canonical data and application history must not live only in browser memory.
- Unsaved form state may remain temporary until the user selects Save.

## Current MVP Acceptance

The MVP is ready for active use when the user can:

1. Manage Career Vault records manually without a source document.
2. Add category labels and record items.
3. Save only changes that pass local validation.
4. Paste and retain a job description.
5. Review deterministic Career Vault matches and unresolved requirements.
6. Build, save, and approve a source-valid Resume Plan.
7. Attach an existing resume to the job being prepared.
8. Record an application and update its outcome.
9. View prior jobs and their status history.

## Current Dogfood Boundary

The MVP may now be used for real, human-reviewed applications. The user can
manage canonical facts, retain and digest a JD, start and track an application,
approve a deterministic Resume Plan, compose application-only resume/cover-letter
wording, export DOCX/PDF/TXT locally, and attach the exact resume used. Final
visual review, attachment, and submission remain manual. The shared Skills Registry,
Candidate Evidence projection, saved-JD demand, and **Jobs / Demand & coverage**
comparison are read-only with respect to candidate facts; see the [Capability
Acceptance Registry](../process/acceptance_registry.md).

Manual JD taxonomy calibration is active: an agent may add exact, source-observed
technology terms under `docs/process/small_model_playbook.md`. User-facing custom groups
and skills are implemented through explicit actions. Automatic vocabulary
learning remains deferred.

Real application sessions may use the structured Application Materials page for
resume and cover-letter documents, and the private text workbench in
`docs/guides/application_text_workbench.md` for exact employer prompts, copy-ready
answers, and dogfood observations. Neither path calls an AI provider or turns
draft text into Vault facts, Skills Registry entries, or proof of submission.

## Deferred Backlog

Do not implement these items as part of the current MVP:

- AI or semantic matching.
- Unattended or AI-driven resume generation and rewriting.
- A canonical role catalog, structured Profile role targets, and local
  role-to-skill demand observations (`CAP-ROLE-CATALOG`).
- One-way Obsidian export (`CAP-OBSIDIAN-EXPORT` / CG-601).
- Contact enrichment, social search, outreach automation, relationship scoring,
  and a general CRM beyond the optional confirmed job-contact ledger.
- Recurring scraping/watchers, protected-page crawling, unattended browsing,
  and automatic application submission. The implemented extension capture and
  reviewed field helper remain explicit, attended actions.

Future dictionary learning must be explicit: user-approved terms may extend the
local taxonomy, but the system must not silently learn incorrect aliases.
