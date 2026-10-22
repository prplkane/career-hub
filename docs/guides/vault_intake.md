# Career Vault Intake

> **Status: needs rewrite.** The intent below is still accurate, but its `python -m src.*` commands belong to the retired Python runtime and no longer run.
> Until this document is rewritten, use the resume-entry workflow in [START_HERE](../../START_HERE.md). See the
> [documentation index](../README.md); historical comparisons use the
> pinned reference export.

Career Vault intake converts a local TXT or DOCX source into reviewable extraction signals. It does not call an AI API and does not modify approved Career Vault records.

## Current Stage

The intake now has two implemented local-only artifacts:

- schema-v2 `extracted.yaml` preserves document blocks and deterministic signals;
- schema-v1 `review.yaml` inventories every non-empty block and stores editable draft cards.

For a new source, **Extract and review** performs upload, extraction, review inventory creation, and deterministic detection before opening Review. The Review UI suggests Profile & Contact, Experience, Education, Certification, Professional Summary, Skill Group, and Project cards. Recognized section headings are classified separately as structural markers. A source paste with no recognized section headings at all (for example a single new project) is treated as unsectioned project content: title-plus-body ranges still become draft Project cards under the `projects_unsectioned_fallback_v1` rule instead of leaving every block unassigned. These are reversible draft results, not approved Career Vault cards. The approval preview compares a complete review with canonical Career Vault records and stores explicit approval decisions in `review.yaml`. A separate explicit apply action validates both review and Vault revisions, applies the saved decisions to canonical YAML, and then locks the source review as read-only.

```text
source document
  -> document blocks
  -> section/date/technology/bullet signals
  -> extracted.yaml
  -> review.yaml (complete block inventory)
  -> editable Profile, Experience, Education, Certification, Summary, Skill Group, and Project cards
  -> deterministic approval comparison and saved decisions
  -> validated application to canonical Career Vault YAML
```

## Canonical And File-Lifecycle Boundary

The original input remains under `career_vault/raw/`, and the intake artifacts
remain under `career_vault/intake_runs/`. Neither is canonical. Career Vault
becomes canonical only when the user completes review, saves approval decisions,
and explicitly applies them. After that approval, the user may explicitly remove
the original input file. This cleanup never removes canonical Vault YAML,
`extracted.yaml`, `review.yaml`, or `approval_result.yaml`.

The implemented `CAP-VAULT` behavior in the [Capability Acceptance
Registry](../process/acceptance_registry.md) is:

- **Update Career Vault** is one of exactly two Add resume intents and continues
  to use this manual review and explicit-approval pipeline.
- After approval has been successfully applied, **Keep for applications** is an
  explicit action that may retain the matching original source as a Resume
  Version without changing or invalidating approved Career Vault records.
- After approval has been successfully applied, **Remove original file** is a
  separate confirmed action. It validates the source id, approved audit,
  `career_vault/raw/` containment, file type, and SHA-256 before deletion. A
  missing file is a successful idempotent retry; missing, altered, unapproved,
  or out-of-root sources otherwise cannot be removed.
- Import and review artifacts may remain internal for audit and diagnostics;
  they are not an application Resume Version or a primary user-facing Source
  Documents library.
- A generated resume may be imported again. Re-import always creates reviewable
  input and never changes Career Vault automatically.
- Before a local source is copied, the UI checks its SHA-256 against existing
  extraction metadata only. When it finds an exact prior import, it returns at
  most five content-minimized summaries (import id, workflow state, and time),
  never resume text, paths, or the hash. This check writes no Career Vault or
  Resume Library data.
- **Open previous import** is the primary exact-match action. **Process again**
  is an explicit alternative: it creates a safely suffixed local file when the
  name already exists, then starts a separate review. It never replaces the
  prior file. A same-name file with different bytes remains a clear conflict;
  rename it before adding it as a new import.
- Exact or possible duplicate findings are advisory and non-blocking. They may
  suggest a default, but the user retains Update, Create, and Do not add.

The other Add resume intent, **Save for applications**, bypasses this Vault
mutation pipeline and creates an immutable application Resume Version.
Assessment is not an Add resume intent.

## Extraction Command

Place a UTF-8 TXT or DOCX file under `career_vault/raw/`, then run:

```powershell
python -m src.extract_vault_source career_vault/raw/example.txt
```

The report is written to `career_vault/intake_runs/<source_id>/extracted.yaml`. Existing reports are not replaced unless `--overwrite` is supplied.

## Intake Review

Build or refresh a review after extraction:

```powershell
python -m src.build_intake_review career_vault/intake_runs/<source_id>/extracted.yaml
```

The sibling `review.yaml` contains:

- source identity and extraction schema version;
- editable Profile & Contact, Experience, Education, Certification, Summary, Skill Group, and Project entities;
- an independent category for each source block;
- exactly one classification decision for every non-empty extracted block;
- optional approval decisions for active cards and category-only blocks.

Each block decision is `unassigned`, `categorized`, `assigned`, `structural`, or `ignored`. Content blocks may have a category such as `profile`, `summary`, `experience`, `education`, `project`, `skill`, or `certification`. Category answers what the source block is; `owner_id` optionally answers which more specific detected card it belongs to.

`categorized` is an explicit category-only review decision. It allows a source block to remain directly under Skills, Education, Experience, or another category without inventing a child-card structure. It completes source review; approval then defaults to **Do not add**, or the user may return to review and attach the block to a card. `assigned` requires an existing entity id whose type matches the block category. `structural` identifies a recognized section heading without treating it as career content. `ignored` records an explicit exclusion. A new review is intentionally fully `unassigned` and validates as `success_with_warnings`. Every decision retains its source reference, and the original text remains unchanged in `extracted.yaml`.

Running the build command again merges by stable block id. Existing dispositions, owner ids, suggestions, and user notes are preserved. The command refuses a refresh that would silently discard a decision for a missing block.

Validate every review, or provide selected review files or intake-run directories:

```powershell
python -m src.validate_intake_review
python -m src.validate_intake_review career_vault/intake_runs/<source_id>
```

Validation fails on missing or duplicate block decisions, broken source references, source/schema mismatches, duplicate entity ids, and invalid assigned owners. Unassigned blocks are warnings because incomplete review is a valid work-in-progress state. The Review UI exposes every unresolved block id, source text, and suggested owner.

Two readiness gates are separate:

- **Review complete / ready for preview:** validation has no errors, every source block has a decision, and no card is queued.
- **Ready for approval:** the review is complete, every active card and category-only block has an explicit approval decision, and every new record has its required source fields selected.

Professional Summary and category-only blocks have no Career Vault destination in this phase, so the preview defaults them to **Do not add**. Certification cards can be compared with or written to `education.yaml.certifications`. Unsupported content remains available in the source review history.

## Card Suggestions

Start the local server and open the **Review** view:

```powershell
python -m src.run_intake_ui
```

The profile rule reads only the leading document area and recognized contact sections, stopping at the first section or dated entry. Experience uses section boundaries, date anchors, contiguous bullets, and a conservative company-first slash layout for compound titles. Re-running detection preserves reviewed fields except for an exact legacy machine output with an empty organization and a combined `Company / Role` title; that representation is migrated to the separate organization and title fields. Education groups entries inside recognized Education sections. Certification sections create one source-backed card per entry. Summary preserves the text inside a recognized Summary section. Skills split each source line into an editable group name, item list, and matched technology ids. Projects group a non-bullet title with its following source items until the next title. A block with exactly one supported owner is linked automatically; competing owners remain in the review queue. Recognized section headings become `structural`. Unambiguous Profile fields in the header are linked automatically. LinkedIn `/in/` URLs and personal `github.com/<user>` URLs are treated as candidate profiles; repository and generic website URLs remain unassigned for later classification. Every classification and link is editable and reversible.

The editor supports:

- showing the identified candidate as the hierarchy root;
- showing a source-derived Professional Summary under the candidate while keeping it as a separately linked record;
- showing Experience cards as a horizontally scrollable work-context row;
- separating Education, Skill Group, and Project records from work contexts;
- showing Certification cards as their own source-backed records;
- keeping the selected card editor in a sticky right-side inspector on desktop and a dismissible drawer on narrower screens;
- grouping the review queue into suggested, needs-child-card, category-only, unclassified, linked, ignored, and separately filtered section-marker blocks;
- using cards as the primary review unit while keeping source blocks as expandable provenance;
- moving a card to a persisted review queue without breaking its source-block links;
- restoring or editing a queued card, or explicitly dissolving its grouping into category-only blocks;
- assigning an expanded source block either directly to its category or optionally to another child card through grouped dropdowns;
- editing a compact Profile & Contact card;
- editing organization, role title, dates, location, technologies, and notes;
- editing institution, degree, major, graduation date, location, GPA, honors, Summary text, Skill Group items, Project titles and evidence, and notes;
- viewing only the selected card's source blocks;
- accepting links for one card, accepting all unambiguous links, or assigning blocks individually;
- keeping queued-card source blocks collapsed until granular reassignment is needed;
- marking a loose or expanded source block `ignored`;
- validated, atomic local saves to `review.yaml`;
- revision checks that reject stale browser writes with HTTP 409;
- preparing a Career Vault comparison after the review is complete;
- choosing **Update**, **Create**, or **Do not add** for each reviewed card;
- selecting source or Career Vault values field by field without deleting unselected Vault data;
- adding and deduplicating selected technologies, skills, honors, and proof points;
- explicitly applying a complete saved decision set to canonical Career Vault YAML;
- writing `approval_result.yaml` as a local audit artifact and locking the applied review as read-only.

Manual Vault mutations and approval application share one in-process Vault
mutation lock. Approval holds that lock across its Vault revision check, load,
validation, and atomic transaction write, while also holding the review mutation
lock. A manual edit therefore cannot commit between approval's stale-state check
and write and then be overwritten by the approval payload.

A review entity has a small local review state: `active` or `queued`. Queued cards retain their owner links and editable fields but block completion until the user restores or ungroups the card. Ungrouping removes only the system-created card and keeps its source blocks available for reassignment. This state controls review workflow only; it is not canonical Career Vault data.
Career Vault intake does not create new career facts. The UI only exposes cards detected from the
source document and allows the user to correct their fields, links, or classification.
Manual creation belongs to the later canonical Career Vault editor.

Suggestions never overwrite an assigned, structural, or ignored block. Re-running the grouper preserves edited card fields and existing decisions. The deterministic approval preview includes exact source text, Career Vault candidates, category-only blocks, structured reason codes, and hashes of both the review and Vault state. A later optional AI matcher can return the same comparison contract.

With one deterministic match, **Update** is the default and existing Career Vault fields remain selected; with no match, **Create** is the default and all available reviewed-source fields are selected; ambiguous or unsupported cards default to **Do not add**. A scalar source selection replaces that one Vault value. A selected list adds normalized values and deduplicates them. Unselected fields never delete existing Vault data. Exact fields are locked as `same`. New decisions store `field_resolutions`; `null` is reserved for backward-compatible whole-card decisions created before granular approval.

Green means normalized equality, yellow means deterministic partial text overlap, and neutral means different or one-sided data. These are comparison states, not confidence scores. Preview and Save decisions do not write Career Vault data; only the separately confirmed Apply action may do so. Apply rejects stale review or Vault revisions, invalid targets, incomplete decisions, and a second application of the same review.

The deterministic duplicate boundary is intentionally permissive. Matching
company names produce advisory experience candidates; shared role terms, years,
and technologies explain the suggestion but do not establish identity. Therefore
the same company and role in non-overlapping periods, related roles with
overlapping dates, and multiple positions at one employer remain distinct
user-review cases. **Create** remains a valid explicit decision even when a
candidate is shown.

Proof points use literal normalized deduplication only. Repeating the same proof
text is treated as the same list value, while a rephrased statement remains a
different value even when it repeats a metric. A resume assembled from existing
Career Vault facts receives the same review as any other import: the system does
not infer that it is generated or automatically consume its facts. Mixed imports
may add a genuinely new fact only after the user explicitly selects the source
proof-point list. Candidate discovery, preview generation, and saving a decision
do not mutate canonical Career Vault data; only explicit Apply can do so.

## Temporary Detailed Diagnostics

Career Vault intake records stages to `career_vault/logs/intake.jsonl`. Each JSONL event contains a schema version, run id, per-run sequence, UTC timestamp, stage, status, duration, counters, and a stable error code on failure. Failed events also include a short summary, suggested next action, retryability, exception type, and technical message when available. Unknown future error codes use safe fallback guidance rather than disappearing from diagnostics.

The browser API returns the same error contract and correlation run id. `python -m src.run_intake_ui --debug` adds terminal tracebacks for unexpected request failures while preserving the structured local log. Normal success events do not include resume text, request bodies, or extracted bullets. Failure messages can contain local paths and diagnostic context, so the log must remain local.

Extraction stages cover source validation, runtime import, source read, document parsing, taxonomy loading, NLP initialization, signal extraction, report validation, output guard, serialization, atomic output write, and efficiency review. Review builds add input validation, load, merge, integrity validation, serialization, and atomic output-write events to the same log.

The temporary efficiency review reports `success_with_warnings` for measurable concerns without failing valid extraction. Current warnings cover large output expansion, high blank-block ratio, and missing recognized section headings. These thresholds are diagnostics, not product rules.

The VS Code happy path, breakpoints, controlled failures, and review handoff are documented in `docs/guides/manual_testing.md`.

## How Matching Works

- Python preserves source blocks and source locations.
- Small regular expressions identify bullet markers and supported date formats.
- A blank English spaCy pipeline tokenizes text without downloading a trained model.
- `PhraseMatcher` finds approved names and aliases, then the exact resolver
  maps a unique active spelling to its canonical technology id. The resolver
  only case-folds and collapses whitespace; it never uses fuzzy matching or
  inferred equivalence.
- A collision remains a source-backed `technology_resolution_issue` with all
  id-sorted candidates, not a `TechnologyMention`. Archived-only text is also
  retained as a diagnostic and is never selected as a new match.
- `EntityRuler` recognizes exact section-heading variants.
- Every extracted fact retains a source block reference.

Source bullet candidates are not proof blocks. A bullet becomes canonical evidence only after it is assigned to a reviewed experience or project and explicitly approved in a later stage.

The blank spaCy pipeline does not infer meaning. Its value is maintainable token-based matching and a stable extension point for optional statistical or local extraction engines later.

## Taxonomy Growth

User-added terms must be reviewed before they are added as canonical names or aliases. Once approved in the local taxonomy, later runs recognize them without AI calls or model training.

Legacy technology records remain compatible with `id`, `name`, `aliases`,
`category`, and optional `parent`. Curators may additionally use `kind`,
`primary_domain`, typed `relations`, and `archived`. The shared JD catalog and
the private taxonomy overlay merge by id; the overlay's explicit fields win,
while shared names and aliases remain accepted spellings. Different ids that
share a spelling remain ambiguous rather than receiving a precedence rule. A
redundant legacy alias that differs from its own canonical name only by case or
whitespace is removed in memory for compatibility and never rewrites the
catalog; new or extended records reject that redundancy.

## Vocabulary Audit

Run a read-only audit before curating new terms:

```powershell
python -m src.vocabulary_audit --data-root "$env:USERPROFILE\CareerHubData\main"
```

The command writes a deterministic JSON report only to standard output. It
checks every Experience/Project `technologies` value, Skill Group `keywords`,
Certification `name` and `credential`, and existing taxonomy `parent`/
`relations` references against the merged shared and private catalogs. Results
are `exact_match`, `alias_match`, `ambiguous`, `unmatched`, or
`invalid_reference`; no entities are created and no Vault or catalog file is
modified. If a report must be retained, redirect it only to an ignored local
data location.

Fuzzy matching and local NLP models are not part of the current production path. They require a benchmark before becoming dependencies.

## Boundary

Neither an `extracted` report nor an unapplied `review.yaml` is a Career Vault card. Only records written by the explicit approval application are canonical resume evidence. Applied reviews retain their source blocks and audit result but are read-only.
