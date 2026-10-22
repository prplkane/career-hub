# Local Application History

> **Status: needs rewrite.** The intent below is still accurate, but its `python -m src.*` commands belong to the retired Python runtime and no longer run.
> Until this document is rewritten, use the Applications workflow in [START_HERE](../../START_HERE.md) and `career-hub-agent record-submission` ([agent workflows](../guides/agent_workflows.md)). See the
> [documentation index](../README.md); historical comparisons use the
> pinned reference export.

This module stores local application status history and cached deterministic
assessments in SQLite. It does not generate resumes, submit applications, call AI, or fetch
job URLs.

## Why SQLite

Career Vault facts remain in portable YAML. Application history and cached
assessment artifacts change frequently and need filtering, so they use the private local
file:

```text
<active-data-root>/applications/career_hub.sqlite3
```

The database and private resume copies under `applications/resumes/` are
excluded from Git. See [`data_home.md`](data_home.md) for the explicit
development-versus-installed data-root contract.

## Resume Library Boundary

**Resume Library** means durable application materials only. It does not mean
the processed Career Vault import list, and import-only files are not added to
the application picker automatically.

Current implemented behavior creates immutable Resume Versions through **Add
resume > Save for applications**, the application picker, the standalone local
upload API, or the `attach-resume` CLI command. Save for applications does not
require a job and does not create Career Vault intake artifacts. A Vault source
may also become a Resume Version only through explicit **Keep for applications**
after approval succeeds.

A version can be archived or restored through the Resume Library; archiving only
hides it from the default picker and preserves its private file, application
selection, and Applied-resume history. An unused version also has an explicit
permanent-delete action. The server recomputes all durable references inside a
write transaction and rejects deletion with `RESUME_VERSION_IN_USE` when the
version is selected by an application, retained by an Applied snapshot, or
named by a submitted-resume event. Assessment is not an Add resume intent.
Capability status and remaining work stay tracked in the
[Capability Acceptance Registry](../process/acceptance_registry.md).

The management view derives digest state by matching the immutable Resume
Version hash to existing Career Vault intake audit runs. It also compares the
extractor version and a semantic fingerprint of the effective core + custom
Skills Registry, so either a product vocabulary update or a user vocabulary
change can surface **Update available**. TXT and DOCX versions can start a new
deterministic digest directly; each run receives a new raw source and review
context, so repeat analysis never overwrites a prior review or approved Vault
data. PDF, DOC, and RTF stay valid application materials but are marked
unsupported for digest until their parsers are implemented. Digest status is
derived data, not another Resume Library table or source of truth.

Permanent delete validates the canonical file below
`applications/resumes/library/`, moves that exact file to a library-local
quarantine name, verifies and removes that quarantined file, and commits the
SQLite-row deletion only after the bytes are gone. Link-like/reparse storage
components, missing files, moved files, and tampered bytes fail closed. Any
failure rolls back the row deletion and restores the verified bytes when it is
safe to do so. This operation never changes Career Vault YAML or source-intake
artifacts.

## Separate State

Three concepts remain distinct:

- normalized JD draft / ready describes deterministic source analysis review;
- application status is the only user-facing job-tracking workflow state;
- the assessment is cached derived data, never a second workflow state.

Every valid normalized job receives a SQLite ledger record with status captured,
shown in the browser as **Saved**. The user can move that same record directly
to Preparing, Applied, and later statuses. The browser has no separate decision
or start-application gate.

Schema version 4 adds one cached assessment artifact per job, keyed by the
normalized-card fingerprint, analyzer version, candidate revision, assessment
context revision, assessment version, and calculation time. Schema version 5
adds the private immutable resume library and the current Applied-resume
snapshot fields. Schema version 6 adds append-only Applied-resume events, so a
missing initial resume can be recorded or corrected later without rewriting
history. Schema version 7 adds nullable `resume_library.archived_at` and an
active-version picker index; archive is reversible and does not alter existing
references. Schema version 8 adds bounded indexes for every Resume Version
reference check. Schema version 9 records an approved source-to-version mapping
for idempotent **Keep for applications** actions. Schema version 10 adds
database-level triggers that reject dangling current selections, Applied
snapshots, and referenced-version deletion even when multiple local processes
race. Schema version 11 adds internal Resume Version origin metadata: existing
and current versions are `uploaded`; `generated` is reserved for a separately
authorized future capability. Legacy decision and start operations remain for
older local records and CLI compatibility, but are not the browser workflow.
Schema versions 12 through 20 add the staged Job Feed and email-alert records
documented in the discovery pipeline. Schema version 21 adds attended
application sessions and their minimal, idempotent submission-signal events.
Schema version 22 adds immutable user-selected instruction evidence and
append-only checklist actions for one saved JD. Schema version 23 adds the
revisioned local contact identity, exact-channel, append-only provenance, and
many-to-many Discovery/Job link tables. Schema version 24 adds a partial global
uniqueness guard for exact JD-candidate provenance, making confirmation retries
idempotent across contacts as well as within one contact. Schema version 25 adds
an optimistic revision to email application outcomes so later attended linking
cannot overwrite a concurrently changed event. Schema version 26 adds
reusable answer families, aliases, immutable revisions, representation mappings,
and append-only usage event tracking. Schema version 27 adds subject-routing
policy configuration to email sender rules and subject routes to message
receipts. Each migration creates a named
recovery backup before changing any existing database.

## Stored Records

`job_applications` stores one current row per normalized `job_id`:

- company, title, source URL, and JD fingerprint snapshot;
- optional legacy CLI decision: `apply`, `consider`, or `skip`;
- current workflow status and user notes;
- created and updated timestamps.

`application_status_events` stores append-only status events with the status,
occurred time, recorded time, and optional note.

`application_sessions` stores one expiring binding between an exact saved-job
URL and, after extension claim, one browser tab and Origin. A pending binding
may be created from the Applications fallback or directly by **Track
submission** only when one startable saved job matches the canonical URL.
Only the SHA-256 hash of the per-session capability is stored in SQLite.
`application_session_events` stores one random event key, enumerated success
rule, and paired submit/success timestamps. It stores no form values, page text,
DOM, cookies, credentials, resume contents, or screenshot. A
`submission_detected` session is a review request, not an application-status
event; only the browser confirmation action appends Applied.

`application_instruction_manual_items` stores only an exact source excerpt that
the user selected inside the retained JD, its category, source line, source
fingerprint, and immutable analyzer snapshot. It cannot contain invented or
free-typed prose. `application_instruction_action_events` appends each
`pending`, `done`, or `not_applicable` choice. The latest action controls the
current projection; changing the source fingerprint makes the prior action
stale instead of rewriting or silently carrying it forward.

`application_contacts` stores canonical local identities with stated
name/role/company/context, archive state, and an optimistic revision. A contact
must have a name or one validated exact channel. `application_contact_channels`
keeps exact email, phone, and professional-profile values; normalized email and
profile values are unique enough to return an existing exact match, while
name/role/company similarity never merges records. `application_contact_provenance`
is append-only and retains the exact stated-field snapshot plus immutable JD
hash/evidence when the user later confirms a derived candidate.
`application_contact_job_links` and `application_contact_discovery_links` are
many-to-many and optional: one application can have zero contacts without an
incomplete state. Unlinking or deleting the linked source removes only the link and
preserves the contact and provenance. Archived contacts stay readable but are
excluded from default selection and cannot receive new links until restored.
The browser contact adapter re-resolves the candidate from the current retained
JD before an explicit create/link confirmation. Opening or closing the review,
capturing, promoting, and reanalyzing remain non-mutating. Schema version 24's
candidate-provenance index makes a lost-response retry return the already linked
contact and rejects claiming the same exact candidate for a different contact.

`job_assessments` stores one cached assessment artifact per job:

- normalized-card fingerprint and analyzer version;
- revisions for validated Career Vault data and deterministic assessment context;
- explicit assessment version, serialized result, and calculation time.

`resume_library` stores each uploaded private resume as an immutable version. The
document bytes live under `applications/resumes/library/`; SQLite stores the
version's file identity, integrity metadata, nullable archive timestamp, and
internal origin. Origin is `uploaded` for every current writer; `generated` is
reserved metadata only and does not change the user-facing library or picker.
An archived version remains readable through an existing application attachment
or Applied-resume event, but is not returned by the default picker listing and
cannot become a new selection through a stale browser request. An idempotent
**Keep for applications** retry verifies that its mapped version is active and
its stored bytes still match before reporting that it is available.

`application_resume_attachments` stores only the current selection for a job.
The browser exposes it as a display name, never as a path, hash, internal ID, or
version mechanism. No selection is the default; the browser shows **Clear
selection** only after a Resume Version is selected.

When a job is first set to Applied, the ledger creates an initial Applied-resume
event containing the selected version ID or an explicit no-resume snapshot.
Later selection changes do not alter that event. The user can explicitly record
the resume actually submitted or append a correction; all of those events stay
auditable while the current picker remains useful for ongoing preparation.

`application_resume_plans` stores one JSON plan per persisted job/application record:

- `draft` or `approved` status;
- optimistic revision and timestamps;
- Career Vault and JD fingerprints;
- ordered sections and explicit evidence selections.

Incomplete drafts are valid. Approval is enforced by `src.resume_plan`, not by
SQLite alone, and requires current references plus complete selection decisions.

Visible application stages are Saved (captured), Reviewed (legacy), Preparing,
Applied, Interview, Offer, Rejected, Withdrawn, and Closed. The repository
validates values but does not force a sequence. Assessment results never block
a candidate action.

## Commands

Initialize the database and synchronize validated local jobs:

```powershell
python -m src.application_history init
python -m src.application_history sync
```

Inspect records or update a saved job status:

```powershell
python -m src.application_history list
python -m src.application_history show <job_id>
python -m src.application_history status <job_id> preparing
```

Upload and select a resume at any application stage, then record later stages:

```powershell
python -m src.application_history attach-resume <job_id> C:\path\resume.pdf --label "Backend v1"
python -m src.application_history status <job_id> applied --occurred-at 2026-07-31
```

Sync refreshes only the JD snapshot. It does not replace decisions, notes,
current status, or history. Repeating the current status with no note is a
no-op; adding a note records another event.

## Browser Workflow

The sidebar starts with **Quick start**, which places **Add job** and
**Add resume** first.

In the current UI, **Add resume** offers **Update Career Vault** for source
intake and explicit approval or **Save for applications** for a reusable
immutable Resume Version. The two collections remain separate: import-only
sources never enter the application picker automatically.

The **Jobs** section then contains:

- **Job workspace**, the one-job-at-a-time surface for the searchable job
  selector, digest, Career Vault assessment, source-backed application
  checklist, user resolutions, and materials;
- **Applications**, the ledger containing every saved job.

**Add job** saves and digests a source, creates its ledger record, and stores
its assessment before opening Job Workspace.

The ledger starts each valid job as **Saved**; **Save as applied** moves the new record to **Applied** before the intake response and supports search, stage filters,
Current/Closed/All scopes, inline stage updates, source links, and a side drawer
with company, role, current stage, applied date, a compact assessment result,
dated notes, and history. The drawer links back to Job Workspace; the full
assessment and resume controls are not duplicated there. Job Workspace reuses
the private resume library, allows a Resume Plan at every persisted stage, and
can record or correct the submitted resume without changing the current selection. Search and filters are
browser-only; stages, plans, selections, and assessment artifacts write to SQLite.

For a saved posting with an exact source URL, **Track submission** in the
extension can create and arm the four-hour session directly when exactly one
startable application matches. The drawer's **Create tracking link** is an
explicit fallback. The extension binds only the exact URL to one tab and Origin.
A trusted native form submit followed by
one recognized success state creates a **Submission confirmations** item at the
top of Applications. **Mark Applied** records the existing Applied transition;
**Not submitted**, timeout, tab reuse, tab closure, or instrumentation loss
leaves the application stage unchanged. Resume and cover-letter drafts are
already job-scoped, so confirmation keeps their saved revisions attached to the
same JD. The Applied event snapshots only the explicitly selected submitted
Resume Version; it never invents a missing file or contact.

Automated assessment JSON remains derived data. The browser exposes two current
decisions for every conclusion: **Resolved / confirmed** and **Proceed / accept
risk**, with an optional note. Legacy decision values remain readable. A
separate correction action scrolls to the effective saved JD facts and opens
their in-place editor. Corrected facts are explicit normalized-card overrides,
survive reanalysis, and invalidate the cached assessment; the automated text is
never silently rewritten by the decision overlay.

## Current Boundary

The repository, CLI, API, browser controls, immutable private resume library,
current application selection, append-only Applied-resume history, attended
submission confirmation, headless revisioned contact storage/linking, and
separate job-scoped resume/cover-letter material editor are implemented.
Portfolios, confirmed contact-save UI, general form-answer UI, automatic
attachment, form filling, and submission automation remain separate product
slices. A Terra session may keep form answers and outreach in
the private convention from `docs/guides/application_text_workbench.md`; these drafts
do not become ledger events. The ledger uses Python's standard `sqlite3`,
parameterized queries, foreign keys, and `PRAGMA user_version`; there is no ORM.
The separate PDF renderer uses the pinned ReportLab dependency.
