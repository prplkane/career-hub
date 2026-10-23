# Schema Reference

This document describes the current `CAP-VAULT` Career Vault YAML structure. It reflects the current validator and sample data. It does not define future job, scoring, or resume generation schemas; see the [Capability Acceptance Registry](../process/acceptance_registry.md) for scope status.

## How To Read This

- `required` means the current validator expects the field.
- `optional` means the field exists in sample data or is allowed for extension, but is not currently required by the validator.
- The validator enforces a minimum contract, not a full strict schema.

## `career_vault/profile.yaml`

Top-level type: mapping

Required fields:

- `name`: string
- `target_location`: string
- `work_authorization`: string
- `major`: string

Optional fields:

- `current_location`: current city/state used for resume contact information
- `location_flexibility`: geographic flexibility stated without replacing the current location
- `role_targets`: list of strings
- `contact`: mapping with email, phone, address, and linkedin fields when available

Purpose:

- Stores stable profile facts reused across future workflows.

Editor behavior:

- Profile is one canonical mapping, not a collection of cards.
- The editor allows update only; it cannot create, archive, or delete Profile.
- `contact` keys and values must be strings when present.
- `role_targets` must be a list of strings when present.
- Save preserves unknown metadata, removes explicitly cleared known contact
  fields, validates the complete Vault, and uses the `profile.yaml` revision to
  reject stale writes.

## `career_vault/education.yaml`

Top-level type: mapping

Required fields:

- `schools`: list of mappings

Required school item fields:

- `id`: string
- `institution`: string
- `degree`: string

Optional school item fields:

- `major`: string
- `location`: string
- `dates`: string
- `graduation_date`: string
- `gpa`: string
- `status`: string
- `honors`: list of strings
- `notes`: list of strings
- `source`: `manual` for records created directly in the Career Vault editor
- `archived`: boolean; `true` preserves history but excludes the school from new active workflows

Purpose:

- Stores verified education records separately from resume prose.
Optional top-level fields:

- `certifications`: list of mappings

Required certification item fields when `certifications` is present:

- `id`: string
- `name`: string
- `credential`: string
- `completed`: string

Optional certification item fields:

- `completed_month`: normalized `YYYY-MM` completion value
- `competency_ids`: explicit canonical Skills Registry ids
- `proof_points`: list of strings
- `notes`: list of strings
- `source`: `manual` for records created directly in the Career Vault editor
- `archived`: boolean; `true` preserves history but excludes the certification from new active workflows

Schools and certifications share one file revision. Any editor save validates
the complete Career Vault and atomically replaces `education.yaml`; stale forms
are rejected instead of overwriting a newer school or certification change.

## `career_vault/visa_profile.yaml`

Top-level type: mapping

Required fields:

- `current_status`: string
- `work_authorization`: string
- `requires_future_sponsorship`: boolean
- `stem_opt_eligible`: boolean

Optional fields:

- `requires_sponsorship_now`: boolean
- `citizenship_required_blocker`: boolean
- `clearance_required_blocker`: boolean

The Profile editor exposes these as confirmed private eligibility facts. The two
legacy `*_blocker` names express candidate incompatibility for deterministic
comparison. Omitting an optional comparison fact means **unknown**, which keeps
an explicit JD restriction in Review rather than classifying it as a clear
conflict. Saves validate the complete Career Vault, atomically replace only
`visa_profile.yaml`, preserve unrelated metadata, and reject stale revisions.

Purpose:

- Stores immigration and work authorization facts that may affect job screening.

## `career_vault/hard_blocker_policy.yaml`

Top-level type: mapping. This is an application-owned, private Data Home
configuration file rather than a candidate-evidence record.

Required fields:

- `schema_version`: currently `1`
- `classes.citizenship_work_authorization`: boolean
- `classes.active_clearance`: boolean
- `classes.minimum_experience_years`: boolean

All three classes default to enabled, but a finding becomes a clear conflict
only when both exact JD wording and an exact confirmed candidate fact are
available. Disabling a class preserves the finding as Review. The browser API
returns only these booleans and their SHA-256 revision; it never includes
candidate facts, and the general extension capability cannot read or update
this file.

## `career_vault/application_helper.yaml`

Top-level type: mapping. This is private Data Home configuration, not candidate
evidence and not extension storage.

Required fields:

- `schema_version`: currently `2`
- `enabled_fields`: ordered list of canonical application-helper field ids

The allowlist is empty on a new profile. Every contact, education, eligibility,
or voluntary US demographic field remains disabled until the user explicitly
enables it. Accepted ids come from the application-owned closed catalog; callers
cannot submit an arbitrary Career Vault path, display label, or value. Unknown
and duplicate ids are rejected. A schema-1 migration preserves its exact legacy
allowlist and never adds newly introduced demographic fields. Updates atomically
replace this file and require its current SHA-256 revision, so a stale
configuration cannot overwrite a newer choice.

The purpose-specific projection reads only the source documents needed by the
enabled ids and returns only fields whose canonical value is present. A
disabled or empty field is absent rather than represented by a blank. The
catalog currently limits sources to reviewed Profile, contact, education,
eligibility, and explicitly enabled voluntary US demographic facts. Passwords,
one-time codes, government identifiers, payment data, and document contents are
outside the contract. Voluntary US demographic values are exposed only for
one-field Copy; the reviewed form matcher never maps or selects demographic
page controls.

## `career_vault/experience_blocks.yaml`

Top-level type: list of mappings

Required item fields:

- `id`: string
- `company`: string
- `role`: string
- `dates`: string
- `category`: string
- `technologies`: list of strings
- `proof_points`: list of strings

Optional item fields:

- `date_range`: normalized `{start, end, ongoing}` month range
- `competency_ids`: explicit canonical Skills Registry ids
- `resume_title_options`: truthful presentation variants for the same official role; the canonical `role` remains the source of truth
- `source`: `manual` for records created directly in the Career Vault editor; imported records retain their existing provenance metadata
- `archived`: boolean; `true` excludes the Experience from active projections

Purpose:

- Stores reusable work-history blocks for later matching and resume assembly.

## `career_vault/project_blocks.yaml`

Top-level type: list of mappings

Required item fields:

- `id`: string
- `name`: string
- `category`: string
- `summary`: string
- `technologies`: list of strings
- `proof_points`: list of strings

Optional item fields:

- `dates`: legacy-compatible display range
- `date_range`: normalized `{start, end, ongoing}` month range
- `competency_ids`: explicit canonical Skills Registry ids
- `related_experience`: list of valid experience block ids; zero or more allowed
- `source`: provenance string; manual UI records use `manual`
- `archived`: boolean; `true` excludes the project from new active workflows

Purpose:

- Stores reusable project evidence separate from job experience blocks.

## `career_vault/skill_blocks.yaml`

Top-level type: list of mappings

Required item fields:

- `id`: string
- `skill_family`: string
- `related_experience`: list of experience block ids
- `keywords`: list of strings

Optional item fields:

- `related_projects`: list of valid project block ids
- `related_certifications`: list of valid certification ids
- `source`: provenance string; manual UI records use `manual`
- `archived`: boolean; `true` excludes the group from new active workflows

Purpose:

- Stores organized skill families, supporting keywords, and links to experience, project, or certification evidence.

## `career_vault/proof_blocks.yaml`

Top-level type: list of mappings

Required item fields:

- `id`: string
- `claim`: string
- `evidence_type`: string
- `source_block`: string

Recommended values:

- `evidence_type`: `experience`, `project`, or `certification`

Purpose:

- Stores traceable claims linked back to source experience, project, or certification blocks. It should index reusable claims without adding subjective scoring.

## `career_vault/style_rules.yaml`

Top-level type: mapping

Required fields:

- `standards_metadata`: mapping
- `resume_voice`: mapping
- `bullet_rules`: mapping
- `document_rules`: mapping
- `ats_rules`: mapping
- `selection_rules`: mapping
- `quality_gates`: mapping
- `forbidden`: list of strings

Recommended nested fields:

- `resume_voice.tense`
- `resume_voice.tone`
- `resume_voice.avoid`
- `bullet_rules.max_lines`
- `bullet_rules.prefer`
- `bullet_rules.require_source_evidence`
- `standards_metadata.reviewed_on` and `review_due`
- `document_rules.layout`, typography ranges, standard sections, and avoided structures
- `ats_rules` for text formats and parseability
- `selection_rules` for full, compact, and excluded decisions
- `quality_gates` for factual, editorial, and visual review

Purpose:

- Stores dated, machine-readable writing, ATS, selection, and quality constraints without generating resume content.

## Current Validation Behavior

The current validator checks:

- required Career Vault files exist
- YAML loads successfully
- top-level type matches the expected mapping or list
- required fields exist
- list items are mappings when a list schema is expected
- duplicate experience, project, proof, and certification ids
- skill references to experience, project, and certification ids
- proof source ids for each evidence type

The current validator does not yet check:

- nested field types in detail
- allowed enum values
- date format consistency

Those are good next-step validation improvements, but they are not required for the current MVP.

## Derived normalized-job application instructions

This is a validated derived envelope under
`jobs/normalized/<job-id>.yaml` at `digest.application_instructions`; it is not
Career Vault evidence or user-authored resume content. Older normalized cards
may omit it.

- `schema_version`: currently `1`
- `extractor_version`: currently `1`
- `source_fingerprint`: normalized lowercase SHA-256 of the complete retained JD
- `status`: `analyzed`
- `item_count`: number of bounded items in `items`
- `source_chars_scanned`, `source_lines_scanned`, `truncated`: scan-boundary
  metadata
- `items`: at most 24 mappings with `fingerprint`, `category`, `confidence`,
  exact `source_text`, `line_start`, `line_end`, `rule`, `extractor_version`,
  and `evidence_truncated`

Allowed categories are `email_destination`, `exact_phrase`,
`required_artifact`, `cover_letter_format`, `external_step`, and `deadline`;
confidence is `explicit` or `review`. Checklist state is intentionally absent
from the normalized job card. Manual exact-source additions and reversible
`pending` / `done` / `not_applicable` events live in the private application
SQLite database so reanalysis never rewrites user history.

## Derived normalized-job contact candidates

This is a review-only derived envelope under
`jobs/normalized/<job-id>.yaml` at `digest.contact_candidates`; it is not a
contact ledger and older normalized cards may omit it.

- `schema_version`: currently `1`
- `extractor_version`: currently `1`
- `source_fingerprint`: normalized lowercase SHA-256 of the complete retained JD
- `content_sha256`: SHA-256 of the exact retained UTF-8 source bytes
- `status`: `analyzed`
- `candidate_count`: number of bounded entries in `candidates`
- `source_chars_scanned`, `source_lines_scanned`, `truncated`: scan-boundary
  metadata
- `candidates`: at most 24 mappings with a stable `candidate_id`, `kind`,
  exact stated `name`, `role`, `company`, `context`, zero or more exact
  `channels`, exact `source_text`, line range, rule, confidence, extractor
  version, and the same immutable `content_sha256`

Allowed person contexts are `application_recipient`, `recruiter`,
`hiring_manager`, and `possible_team_contact`. A generic `careers@` or `jobs@`
destination may instead be retained as `kind: application_channel` with blank
person fields. Names, roles, employers, profiles, and channels are never
inferred or enriched. Complete Discovery previews and Job Workspace reviews may
project this envelope; partial captures return **Full JD required**. The
extension receives only the conditional candidate count. Promotion, dismissal,
and reanalysis create no permanent contact or relationship.

## Private application database (SQLite)

`applications/career_hub.sqlite3` is the transactional store for everything
that changes often: the application ledger, discovery feed, email intake, and
their histories. Its schema is owned by the hand-maintained step list in
[`backend/internal/applications/schema/steps_generated.go`](../../backend/internal/applications/schema/steps_generated.go);
`schema.Version` in `steps.go` is the single version constant that every
reader and writer gate compares against `PRAGMA user_version`. The current
version is **28**. An older database is upgraded once at sidecar startup in
one immediate transaction after a verified pre-migration backup is written to
`backups/sqlite-schema-recovery/`; reads never migrate, and a database that
still needs the upgrade is reported as requiring preparation (never as empty).

The 32 tables, grouped by owner, with the version that introduced each:

| Table | Since | Purpose |
| --- | --- | --- |
| `job_applications` | 1 | One ledger row per saved job: JD snapshot, `decision`, `current_status`, `notes`, applied-resume snapshot fields (v5), and the optimistic `revision` (v28). |
| `application_status_events` | 1 | Append-only dated stage history with notes. |
| `application_resume_attachments` | 2 | Current Resume Version selection per application (`resume_id` since v5, trigger-guarded). |
| `application_resume_plans` | 3 | One revisioned resume selection plan (`plan_json`) per application. |
| `job_assessments` | 4 | Deterministic assessment cache keyed by job fingerprint, analyzer, candidate and context revisions. |
| `resume_library` | 5 | Immutable Resume Version metadata; reversible `archived_at` (v7) and `origin` (v11). |
| `application_applied_resume_events` | 6 | Initial and corrected submitted-resume history. |
| `approved_import_resume_versions` | 9 | Idempotent mapping from an approved import source to its Resume Version. |
| `job_assessment_resolution_events` | 12 | Append-only user handling of one assessment finding. |
| `job_discoveries` | 13 | Effective opportunity record: identity, content scope and hash, lifecycle state, promotion link, `revision`, and content verification (v19). |
| `job_discovery_observations` | 13 | Producer provenance and idempotent request receipt per observation. |
| `job_discovery_assessments` | 13 | **Reserved/unused**: a complete-content preview cache that no Go code writes; previews are currently cached as files under `cache/`. |
| `job_discovery_field_overrides` | 18 | Explicit title/company/location corrections with their own revision. |
| `job_email_connections` | 14 | Receive-only mailbox configuration (secret name only), test status, scheduling (v15), revision. |
| `job_email_sender_rules` | 14 | Exact sender allow/disable, stream purpose (v20), `subject_policy_json` (v27). |
| `job_email_sync_state` | 14 | Per-folder UID cursor, lease (v15), retry/backoff state and result. |
| `job_email_message_receipts` | 14 | Content-minimized idempotency and retry receipt per message, subject signals (v16, v20) and `subject_route` (v27). |
| `job_email_application_outcomes` | 17 | Observed application-status evidence with optional discovery/application links and `revision` (v25). |
| `application_sessions` | 21 | Attended application session bound to one tab/Origin, with state and revision. |
| `application_session_events` | 21 | Append-only verified submit/success signals. |
| `application_instruction_manual_items` | 22 | Exact user-selected instruction evidence from the retained JD. |
| `application_instruction_action_events` | 22 | Append-only checklist actions. |
| `application_contacts` | 23 | Canonical stated contact identity, archive state, revision. |
| `application_contact_channels` | 23 | Exact email, phone, or professional-profile channels. |
| `application_contact_provenance` | 23 | Append-only, trigger-protected source and stated-field snapshot. |
| `application_contact_job_links` | 23 | Many-to-many contact/application links. |
| `application_contact_discovery_links` | 23 | Many-to-many contact/discovery links. |
| `reusable_answer_families` | 26 | **Reserved/unused**: reviewed answer identity; no Go reader or writer exists yet. |
| `reusable_answer_aliases` | 26 | **Reserved/unused**: normalized question variants. |
| `reusable_answer_revisions` | 26 | **Reserved/unused**: immutable answer values. |
| `reusable_answer_representations` | 26 | **Reserved/unused**: control and option mappings. |
| `reusable_answer_use_events` | 26 | **Reserved/unused**: append-only usage log. |

Schema version `28` adds, without rewriting or removing any data:

- `job_applications.revision` (`INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0)`),
  incremented by every update to the row. Application records and summaries
  return it, and the decision and status routes accept an optional
  `expected_revision` that is refused with `JOB_REVISION_CONFLICT` when stale;
- indexes `job_email_application_outcomes_matched_discovery` and
  `job_email_application_outcomes_matched_job`, so the `ON DELETE SET NULL`
  actions from discoveries and applications no longer scan every outcome;
- indexes `application_contact_provenance_job` and
  `application_contact_provenance_discovery` for the non-foreign-key
  provenance references checked before an orphan ledger row is removed;
- index `job_discoveries_last_seen` on `(last_seen_at DESC, discovery_id)`,
  the default Job Feed order when no state filter applies.

## Private application-session storage

Schema version `21` adds attended application-session tracking under
`applications/career_hub.sqlite3`:

- `application_sessions`: expiring binding between an exact saved job and one browser tab/Origin, hashed capability token, state (`pending_claim`, `armed`, `submission_detected`, `confirmed_applied`, `dismissed`, `interrupted`, `expired`, `cancelled`), timestamps, and optimistic revision;
- `application_session_events`: append-only events recording verified native submit and success signals with paired timestamps and rule id.

## Private application-instruction storage

Schema version `22` adds source-backed checklist storage under
`applications/career_hub.sqlite3`:

- `application_instruction_manual_items`: exact user-selected source excerpts from the retained JD, category, line numbers, and immutable analyzer snapshot;
- `application_instruction_action_events`: append-only log of user checklist actions (`pending`, `done`, `not_applicable`) preserving origin and source fingerprint.

## Private application-contact storage

Schema version `23` adds five private SQLite tables under the existing
`applications/career_hub.sqlite3` owner:

- `application_contacts`: stable contact id, stated name/role/company/context,
  an internal identity-channel guard, archive state, optimistic revision, and
  timestamps;
- `application_contact_channels`: exact email, phone, or professional-profile
  value plus its conservative normalized value;
- `application_contact_provenance`: append-only source reference, immutable JD
  hash/evidence/line range when applicable, and the exact stated-field snapshot;
- `application_contact_job_links` and
  `application_contact_discovery_links`: many-to-many links whose source-side
  cascade removes only the link.

A contact requires a name or one validated exact channel. Normalized email and
professional-profile values are globally unique and may identify an existing
contact; name/role/company similarity produces only bounded review candidates.
Every create, fact update, link, unlink, archive, and restore is transactional;
existing-contact mutations require the current positive revision. Unlinking or
deleting a linked source preserves the contact and immutable provenance.
Archived contacts remain readable but are excluded from default selection and
cannot receive new links until restored. Schema version `24` adds the partial
unique index `application_contact_candidate_provenance` over exact
`jd_candidate` source reference and content hash, so a confirmation retry is a
no-op for its original contact and cannot claim the same immutable candidate for
another contact. The browser adapter revalidates that provenance against the
current retained Discovery or Job source before an explicit create/link action.
There is no delete, send, enrichment, social search, score, cloud sync, or CRM
pipeline.

## Private email-alert and tracking storage

Schema versions `14` through `17`, `20`, `25`, and `27` provide private local email connection and sync tables:

- `job_email_connections`: dedicated mailbox configuration, lookback days, sync limits, test status, and optimistic revision;
- `job_email_sender_rules`: exact sender addresses, routing disposition (`allowed`, `disabled`), purpose (`job_alerts`, `application_tracking`, `both`), and `subject_policy_json` (Schema `27`);
- `job_email_message_receipts`: sync receipts, deduplication keys, subject application signals, and `subject_route` (Schema `27`);
- `job_email_application_outcomes`: observed outcome events (rejection, interview, submission), resolution status, and optimistic `revision` (Schema `25`).

## Private reusable-answer storage

Schema version `26` adds five private SQLite tables for reviewed neutral answer reuse. They are reserved: no Go code reads or writes them yet, and the orphan-repair check only confirms that `reusable_answer_use_events` references no deleted job.

- `reusable_answer_families`: stable answer id, source kind (`literal` or `canonical_field`), canonical question, value kind (`text`, `boolean`, `single_choice`), safe prefill reference or current revision FK, use policy (`suggest_only`), confirmation time, 180-day review due time, archive timestamp, and optimistic revision;
- `reusable_answer_aliases`: globally unique normalized question wordings and display aliases;
- `reusable_answer_revisions`: immutable revision numbers, serialized JSON values, fingerprints, and replay request IDs;
- `reusable_answer_representations`: remembered control kind (`text_like`, `textarea`, `select_one`, `radio_group`, `checkbox_boolean`), option mappings, verification state (`user_approved`, `fill_verified`), and optimistic revision;
- `reusable_answer_use_events`: append-only usage log with outcome (`filled`, `reverted`, `event_failed`, `copied`, `used_once`), paired timestamps, and optional job link.

## Read-only competency projection schemas

These JSON shapes are calculated on demand and are never canonical storage.

- Candidate Evidence Graph (`schema_version: 1`) contains competency, domain,
  and active evidence nodes plus `competency_evidence`, `project_experience`,
  and `competency_domain` edges.
- Evidence Analytics (`schema_version: 1`) contains one row per projected
  competency with evidence/context counts, first/last use, active-now state,
  proof count, and merged month intervals.
- JD Demand Index (`schema_version: 1`) contains the selected inclusive period,
  application-ledger source counts, distinct saved jobs, exact source
  occurrences, and separate required/preferred competency and domain counts.
  `duplicate_job_ids` identifies later ledger records excluded by the same
  non-empty content fingerprint.
- Coverage Matrix (`schema_version: 1`) joins analytics and demand revisions.
  Each row has one of `supported`, `thin_evidence`, `evidence_not_found`, or
  `not_requested` and retains evidence and required/preferred counts separately.
- Demand & Coverage (`schema_version: 1`) is the compact browser projection over
  the same graph, analytics, demand, and coverage revisions. It retains compact
  display metadata for counted jobs, the inclusive period, ledger source
  counts, the safe-language contract, filter
  values, and every coverage row; the UI defaults to rows requested by at
  least one saved job.
- Competency Explorer (`schema_version: 1`) is a compact UI projection with
  filters, summary counts, aliases, classification, analytics, and navigation
  targets for related Experience, Project, and Certification records. It also
  returns `vault_revision`, `registry_revision`, `catalog_revision`, and the
  boolean `include_archived_competencies`. The default scope is active-only;
  archived competency history requires the explicit historical scope.

Every projection includes `read_only: true` and a deterministic revision. None
is a cache, graph database, ranking, or source of Career Vault writes.

## Private Skills Registry taxonomy

`career_vault/taxonomy/skill_groups.yaml` and
`career_vault/taxonomy/technologies.yaml` are private user data layered over the
read-only catalogs shipped under `src/data/`. A private technology record uses
the `TechnologyTerm` fields documented in
[`competency_graph_contract.md`](competency_graph_contract.md). It may also
contain:

- `created_via`: `manual_registry`, `vault_editor`, `resume_digest`,
  `legacy_migration`, or `unspecified`;
- `created_at`: ISO 8601 UTC creation time ending in `Z`, when the workflow has
  a real event timestamp.

Both fields are vocabulary provenance, not Career Vault evidence. Older records
may omit them and remain valid without a load-time rewrite. Core updates never
write these private files: if a new core name, alias, or legacy id collides with
an existing private record, the effective read applies the documented
private-wins compatibility rule and reports a notice.
