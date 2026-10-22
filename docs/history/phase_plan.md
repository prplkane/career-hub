# Historical Phase Plan

This plan records the historical local-first build sequence. Current implementation status and acceptance are defined by the named [Capability Acceptance Registry](../process/acceptance_registry.md); a phase authorizes work only when a task explicitly requests it.

## Current State

Completed:

- Phase 0 repository structure and project rules.
- Phase 1 Career Vault YAML foundation, validation, and cross-reference checks.
- Phase 1 dynamic intake extraction, source-complete review inventory, and editable Experience draft cards.
- Phase 2 manual job intake and normalized job-card validation.
- Phase 3 deterministic review foundation with non-enforcing eligibility blockers, requirement gaps, exact evidence matching, and review lanes.
- Phase 4 base resume schema, validation, and first source-backed draft.
- Application-package validation for complete experience-selection decisions and proof ownership.
- Local Career Vault editors, JD library, application ledger, and revisioned Resume Plan editor.
- Job-scoped constrained resume/cover-letter composition and canonical local
  DOCX/PDF/TXT rendering.
- First real workflow: a sample Lead IT Analyst - Pricing posting normalized and reviewed.

Not implemented:

- Semantic or AI-assisted matching.
- Automated resume selection, reuse decisions, provider-driven tailoring, or generation.
- Hosted dashboard, scraping, auto-apply, cloud services, or AI API calls. The implemented browser workflow and SQLite history remain local-only.

The current user-facing MVP contract is defined in `docs/guides/mvp_workflow.md`.
Older phase numbers below describe implementation history, not the stages a
user must repeat for each application.

## Phase 1: Career Vault

Status: source intake MVP complete; all core canonical record types now have a
manual editor. Resume Library derives review and Career Vault save status from
existing local artifacts. Search, permanent delete, and broader usability
polish remain.

```powershell
python -m src.validate_vault
```

Future vault changes must stay source-backed and preserve stable block ids when possible.

TXT/DOCX extraction is MVP-capable. `review.yaml` tracks every non-empty source block without forcing ownership. The local Review UI supports source-derived Profile, Experience, Education, Summary, Skill Group, and Project cards; editable fields; flexible block assignment; ignored and structural decisions; validated local saves; and a canonical comparison that persists explicit approval choices. It does not create facts or user-authored cards outside the source document.

The approval transition now applies complete saved decisions to canonical Vault files with review/Vault revision guards, in-memory validation, rollback-capable file replacement, an audit result, and a read-only applied review. Cross-document matching remains deterministic and user-controlled; broader fuzzy deduplication is deferred until benchmark evidence justifies it.

Manual Profile, Experience, Project, Skill Group, and Education editors are
implemented without requiring a source document. Profile is a singleton and
supports guarded edits only. The record editors support generated ids, editable
canonical fields, whole-Vault validation, atomic save, stale-write guards, and
reversible archive/restore. Projects and Skill Groups support validated
many-to-many evidence links. Skill keywords remain taxonomy; only linked
Experience, Project, or Certification records are direct evidence. Schools and
certifications share one guarded `education.yaml` revision. Permanent delete is
still pending.

## Phase 2: Manual Job Intake

Status: deterministic intake and local review UI implemented; calibration remains.

```powershell
python -m src.ingest_job jobs/incoming/example.md
python -m src.validate_jobs
```

Detailed rules are in `docs/guides/job_intake.md`.

## Phase 3: Deterministic Job Review

Status: implemented as a conservative foundation; calibration remains.

```powershell
python -m src.review_job jobs/normalized/example.yaml
```

Current behavior:

- Named required platforms use exact evidence and become high-severity requirement gaps when evidence is not found.
- Eligibility blockers use the local visa profile but never block an application action.
- Required and preferred matches include evidence ids.
- Domain-specific years and education stay manual review.
- No numeric score or semantic inference is used.

Detailed rules are in `docs/guides/job_review.md`.

## Phase 3 Calibration

Status: next.

Process several different job families before changing lane rules. Review false negatives, overly broad terms, missing schema fields, and whether exact matching needs explicit aliases.

Do not add embeddings or AI matching during calibration.

## Phase 4: Base Resume Versions

Status: foundation implemented; first draft awaits manual review.

Goal: store manually approved base resume variants with metadata linked to Career Vault evidence.

Current implementation includes `full_stack_solutions_engineer`, stored as Markdown plus YAML metadata. No resume rendering or automated generation is authorized by this plan alone.

Job-specific application packages now require every Career Vault experience to be selected with a presentation depth or explicitly excluded with a reason. The package validator checks this contract; evidence ranking and resume prose remain agent-assisted and manually reviewed.

## Phase 5 And Later

The local SQLite repository and CLI store append-only application status
history. Every valid saved JD appears in the Applications ledger at Saved, with
inline stages, a history drawer, a reusable private resume picker, and a
historical Applied-resume snapshot. Later phases may cover evidence-grounded
resume composition, rendering, optional UI layers, and dynamic manually
reviewed intake. Each requires a separate task.

## Recommended Next Task

Use the current local workflow for a two-day dogfood checkpoint. Record concrete
digest, selection, navigation, and status-history failures without broadening
scope. Calibrate only exact JD vocabulary under `docs/process/small_model_playbook.md`.

After that checkpoint, map one approved ATS-friendly DOCX template. Implement
rendering and visual/output QA as separate tasks; do not combine them with AI
rewriting or browser automation.

The selected reference roles and template boundary are documented in
docs/reference/resume_template_strategy.md.
