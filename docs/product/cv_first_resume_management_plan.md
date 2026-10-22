# CV-First Resume Management Delivery Plan

This document is the bounded implementation plan for simplifying resume intake
and management around one product truth:

> Career Vault is the canonical career record. Uploaded resumes are either
> temporary inputs used to update Career Vault or durable resume versions used
> for applications.

The plan is written for smaller coding models. Assign one ticket id per goal.
Numbers express dependency order only; current product status remains governed
by the [Capability Acceptance Registry](../process/acceptance_registry.md).

## Target User Model

The user sees one **Add resume** entry point with two intents:

```text
Add resume
  -> Update Career Vault
       -> upload
       -> extract and review
       -> approve selected changes
       -> remove the uploaded file, or keep it for applications

  -> Save for applications
       -> upload
       -> choose a display name
       -> save one immutable Resume Version
```

The application workflow also has two visible choices, but both converge on a
concrete immutable Resume Version:

```text
Application
  -> Build from Career Vault
       -> Resume Plan
       -> future generated or manually finalized Resume Version

  -> Use an existing resume
       -> uploaded or previously generated Resume Version
```

Career Vault itself is never recorded as the document submitted to an employer.
The final application audit points to a file version or explicitly records
**Not recorded**.

## Fixed Product Decisions

Implementation tickets must preserve these decisions:

1. Career Vault is the source of truth after explicit user approval.
2. An import-only resume is not a durable Career Vault dependency.
3. Deleting an import file never deletes or invalidates approved Vault records.
4. **Resume Library** means only reusable application materials.
5. There is no user-facing **Source documents** library in the main navigation.
6. Existing extraction, review, and approval artifacts may remain internal for
   audit and diagnostics; they are not a primary product object.
7. Add resume has two intents: **Update Career Vault** and
   **Save for applications**.
8. **Assessment** is not a third Add resume intent.
9. Vault import includes its existing review/change-preview step.
10. Resume-to-JD comparison belongs to a specific application and is deferred
    until separately authorized.
11. A generated resume may be uploaded into intake again.
12. Re-import never updates Career Vault automatically; the user retains
    `Update`, `Create`, and `Do not add` choices.
13. Deduplication is advisory and permissive. Similarity never blocks creation.
14. Exact structural validation and stale-write protection remain enforcing.
15. No AI API, resume generation, rendering, cloud service, Tauri work, or
    browser extension is part of this plan.

## Minimal Domain Model

Do not introduce a generic document-management architecture.

The implementation should rely on four concepts already present in the product:

| Concept | Lifetime | Product responsibility |
| --- | --- | --- |
| Career Vault record | Canonical and editable | Approved candidate facts |
| Import session | Temporary workflow/audit | Extract, review, and apply selected changes |
| Resume Version | Durable and immutable | Reusable application file |
| Application resume event | Append-only audit | Record the resume actually submitted |

The existing paths remain valid during this plan:

- import inputs: `career_vault/raw/`;
- import artifacts: `career_vault/intake_runs/`;
- canonical data: `career_vault/*.yaml`;
- application resume bytes: `applications/resumes/library/`;
- Resume Version metadata and application references: SQLite.

Physical content-addressed storage or a generic blob table may be reconsidered
only after measured duplication becomes a problem. It is not an MVP prerequisite.

## Acceptance Mapping

Ticket `RM-000` must confirm whether these changes extend existing capability
rows or require one new capability. The preferred minimal mapping is:

- extend `CAP-VAULT` for two-intent routing and import-file cleanup;
- extend `CAP-APPLICATION-LEDGER` for Resume Version archive/delete lifecycle;
- do not add a capability solely to describe an internal Import Session.

No row may become **Implemented** until its relevant exit gate passes.

## Contract For Smaller Models

Each goal must name exactly one ticket. A model must stop after that ticket,
even when the next dependency appears easy.

Every task prompt must include:

- one observable result;
- exact allowed files;
- exact required checks;
- explicit non-goals;
- the expected rollback/failure behavior;
- whether the task may write synthetic private data;
- an instruction never to stage real ignored data.

Required reading for every ticket:

- `AGENTS.md`;
- `START_HERE.md`;
- `docs/process/engineering_rules.md`;
- `docs/process/workflow_rules.md`;
- this document's Fixed Product Decisions and Global Safety Gates;
- only the selected ticket and its named task-specific files.

Copyable prompt skeleton:

```text
Read AGENTS.md, START_HERE.md, docs/process/engineering_rules.md,
docs/process/workflow_rules.md, the Fixed Product Decisions and Global Safety Gates,
and ticket <TICKET-ID> in docs/product/cv_first_resume_management_plan.md.

Goal: complete only <TICKET-ID>.
Allowed files: <copy exact paths from the ticket>.
Required checks: <copy commands from the ticket>.
Out of scope: every later ticket, resume generation, rendering, AI, cloud,
Tauri, extension work, and changes to real private data.

Use temporary/synthetic roots for mutating tests. Never stage ignored Career
Vault, application, job, template, output, or temporary data. Stop and report
if the ticket requires a product decision not fixed by the plan. Do not begin
the next ticket after this one passes.
```

## Global Safety Gates

- Never run deletion or cleanup tests against the user's real
  `career_vault/raw/`, `career_vault/intake_runs/`, application database, or
  resume library.
- Use `TemporaryDirectory` or a clearly synthetic fixture root for mutations.
- Never silently delete an uploaded file.
- A cleanup action requires an applied approval result and an explicit user
  action.
- Archive is reversible. Delete is guarded and separately tested.
- A Resume Version used by an Applied/submitted event cannot be hard-deleted by
  the MVP flow.
- A failed database/filesystem mutation must leave a valid prior state or a
  recoverable quarantine file.
- Never make Career Vault validity depend on the continued presence of an import
  file.
- Never put import-only files into the application resume picker.
- Never show archived Resume Versions in the default picker.
- Never auto-apply inferred duplicates, generated wording, or extracted facts.
- No test or log may expose resume text, contact information, file bytes, full
  private paths, or tokens.
- Existing application Applied-resume history remains append-only.
- Existing browser behavior outside the selected ticket must remain supported.
- Documentation claims implementation only after tests and the relevant human
  gate pass.

## Model Routing

Use the least expensive model that matches the risk of one ticket.

| Ticket type | Recommended model | Reasoning |
| --- | --- | --- |
| Documentation, copy, focused rendering tests | `gpt-5.6-terra` | high |
| One bounded API or UI behavior with existing patterns | `gpt-5.6-terra` | high or max |
| SQLite migration, guarded delete, cross-layer retention | `gpt-5.6-terra` | max |
| Architecture contract or final integration audit | `gpt-5.6-sol` | max |
| Security/privacy review of delete and retention behavior | `gpt-5.6-sol` | max |

Best overall ROI:

- use Terra high for `RM-010`, `RM-200`, `RM-210`, `RM-220`, `RM-310`,
  `RM-500`, and documentation tickets;
- use Terra max for `RM-100`, `RM-110`, `RM-120`, `RM-230`, `RM-400`,
  `RM-410`, and `RM-430`;
- use Sol max for `RM-000`, `RM-300`, `RM-420`, and the final `RM-720` audit.

Do not assign the whole plan to one long-running goal. A detailed plan lowers
implementation cost only when the model receives a bounded state transition.

---

## Delivery Stage 0: Contract And Baseline

Purpose: make the intended product behavior executable without modifying the
runtime yet.

### `RM-000` Align Capability And Product Contracts

Recommended model: `gpt-5.6-sol`, max.

Goal: make authoritative documentation describe the CV-first two-intent model.

Files to inspect:

- `docs/process/acceptance_registry.md`;
- `docs/guides/mvp_workflow.md`;
- `docs/guides/vault_intake.md`;
- `docs/reference/application_history.md`;
- `README.md`;
- `START_HERE.md`;
- `tests/test_acceptance_registry.py`.

Files allowed to modify:

- the files above only.

Required decisions to encode:

1. Career Vault is canonical after approval.
2. Resume Library contains application materials, not processed import sources.
3. Add resume has the two named intents.
4. Import files may be explicitly removed after successful approval without
   changing Vault records.
5. Assessment is not an Add resume path.
6. Generated-resume re-import remains allowed and manually reviewed.
7. Dedup suggestions are advisory and non-blocking.

Acceptance:

- No document calls processed import sources the application Resume Library.
- Current behavior is distinguished from planned behavior.
- Capability status remains truthful; planned behavior is not called
  Implemented.
- Historical phase numbers are not reintroduced as status.

Checks:

```powershell
python -m unittest tests.test_acceptance_registry -v
git diff --check
```

Rollback: documentation-only; revert only this ticket if terminology conflicts
with the acceptance registry.

### `RM-010` Capture A Synthetic Baseline

Recommended model: `gpt-5.6-terra`, high.

Goal: document and test the current import/library boundaries before behavior
changes.

Files to inspect:

- `src/run_intake_ui.py`;
- `src/application_history.py`;
- `src/intake_ui_static/navigation.js`;
- `src/intake_ui_static/index.html`;
- `tests/test_intake_ui.py`;
- `tests/test_application_history.py`.

Files allowed to modify:

- one new document under `docs/history/benchmarks/`;
- focused tests only when required to freeze a missing current contract.

Record:

- current source upload and extraction routes;
- current application resume upload/list routes;
- current SQLite schema version;
- current source/library navigation labels;
- exact behavior when an uploaded resume is selected and later Applied;
- current absence of archive/delete APIs.

Acceptance:

- Baseline contains no real resume text or private path.
- Existing tests pass unchanged unless a missing contract test is intentionally
  added.

Checks:

```powershell
python -m unittest tests.test_application_history tests.test_intake_ui -v
git diff --check
```

### Stage 0 Exit Gate

- Authoritative docs agree on the two intents.
- Current and target behavior are distinguishable.
- No runtime behavior changed.

---

## Delivery Stage 1: Resume Version Backend Lifecycle

Purpose: make the existing SQLite Resume Library independently usable before
changing the Add resume UI.

### `RM-100` Add Standalone Resume Library Upload

Recommended model: `gpt-5.6-terra`, max.

Goal: upload a reusable Resume Version without first opening an application.

Files to inspect:

- `src/application_history.py`;
- `src/run_intake_ui.py`;
- `tests/test_application_history.py`;
- `tests/test_intake_ui.py`.

Files allowed to modify:

- the files above only.

Contract:

- add one authenticated local endpoint for library upload;
- reuse `upload_resume()` validation and immutable storage;
- accept the existing supported application-resume formats and size limit;
- require/display a user-facing name with the existing fallback;
- do not attach the new version to any job;
- return the same public summary shape used by `GET /api/resumes`;
- do not expose path, hash, or internal storage mechanics to the browser.

Acceptance:

- Upload creates exactly one library row and one immutable file.
- Failure leaves neither row nor file.
- Same bytes may be uploaded as a distinct explicit version; no hidden global
  deduplication is introduced.
- Existing job-specific upload remains backward compatible.

Checks:

```powershell
python -m unittest tests.test_application_history -v
python -m unittest tests.test_intake_ui -v
git diff --check
```

Rollback: route and tests can be removed without a schema rollback.

### `RM-110` Add Reversible Resume Version Archive

Recommended model: `gpt-5.6-terra`, max.

Goal: hide obsolete Resume Versions without deleting files or history.

Files allowed to modify:

- `src/application_history.py`;
- `src/run_intake_ui.py`;
- `tests/test_application_history.py`;
- `tests/test_intake_ui.py`;
- `docs/reference/application_history.md`.

Contract:

- advance SQLite schema by exactly one documented version;
- add nullable `archived_at` to `resume_library`;
- default `list_resume_library()` excludes archived rows;
- explicit include-archived listing remains possible for management UI;
- archive and restore are idempotent;
- current attachments and Applied-resume events remain valid;
- archive never changes an application's current selection or history.

Acceptance:

- Migration preserves all existing resume ids and references.
- Archived version disappears from default search and picker data.
- Archived version remains resolvable for existing application/history views.
- Restore returns it to default listing.
- Re-running schema initialization is safe.

Checks:

```powershell
python -m unittest tests.test_application_history -v
python -m unittest tests.test_intake_ui -v
python -m src.application_history init
git diff --check
```

Safety: the CLI initialization check may use the current private database only
if it performs the normal forward migration and the user has approved that
specific run. Automated mutation tests must use temporary databases.

### `RM-120` Add Resume Usage Summary

Recommended model: `gpt-5.6-terra`, max.

Goal: answer whether a Resume Version is safe to delete without guessing.

Files allowed to modify:

- `src/application_history.py`;
- `src/run_intake_ui.py`;
- `tests/test_application_history.py`;
- `tests/test_intake_ui.py`.

Usage summary must count separately:

- current application selections;
- `applied_resume_id` snapshots;
- append-only submitted/corrected resume events.

Public UI shape may expose counts and a simple deletion eligibility flag, but
must not expose job ids in the library list by default.

Acceptance:

- `deletable` is true only when all three reference counts are zero.
- Archived state alone never makes a referenced version deletable.
- `Not recorded` events do not reference a Resume Version.
- Queries are parameterized and indexed patterns remain bounded.

Checks:

```powershell
python -m unittest tests.test_application_history -v
python -m unittest tests.test_intake_ui -v
git diff --check
```

### Stage 1 Exit Gate

- A standalone reusable Resume Version can be uploaded.
- Archive/restore is safe and reversible.
- Deletion eligibility is deterministic and tested.
- No UI delete action exists yet.

---

## Delivery Stage 2: Two-Intent Add Resume UI

Purpose: expose the simple user model while reusing current backend workflows.

### `RM-200` Add The Intent Chooser

Recommended model: `gpt-5.6-terra`, high.

Goal: make Add resume start with two clear actions and no assessment option.

Files to inspect:

- `src/intake_ui_static/index.html`;
- `src/intake_ui_static/app.js`;
- `src/intake_ui_static/navigation.js`;
- `src/intake_ui_static/styles.css`;
- `src/intake_ui_static/ui/primitives.js`;
- `tests/test_intake_ui.py`.

Files allowed to modify:

- the files above only.

Required copy:

- **Update Career Vault** — "Review this resume and choose which career facts
  to add or update."
- **Save for applications** — "Store this resume as-is so it can be selected
  for job applications."

Acceptance:

- Keyboard and screen-reader users can choose either intent.
- Intent change does not upload or mutate data.
- No Assessment card/button is added.
- Current intake controls remain available under Update Career Vault.
- Narrow viewport stacks both actions cleanly.

Checks:

```powershell
python -m unittest tests.test_intake_ui -v
git diff --check
```

Rollback: UI-only; existing intake remains callable.

### `RM-210` Connect Update Career Vault To Existing Intake

Recommended model: `gpt-5.6-terra`, high.

Goal: make the first intent a thin entry into the existing
upload → extraction → review → approval workflow.

Files allowed to modify:

- `src/intake_ui_static/index.html`;
- `src/intake_ui_static/app.js`;
- `src/intake_ui_static/navigation.js` only if route state requires it;
- `tests/test_intake_ui.py`;
- `docs/guides/manual_testing.md`.

Acceptance:

- Upload still validates TXT/DOCX and local size/path rules.
- Existing extraction/review/approval behavior is unchanged.
- User can resume an incomplete import from Add resume.
- Applied review stays read-only.
- No import-only file is added to application Resume Library automatically.

Checks:

```powershell
python -m unittest tests.test_intake_ui -v
python -m unittest tests.test_approval_apply tests.test_approval_preview -v
git diff --check
```

### `RM-220` Connect Save For Applications

Recommended model: `gpt-5.6-terra`, high.

Goal: upload and name an application Resume Version without extraction.

Files allowed to modify:

- `src/intake_ui_static/index.html`;
- `src/intake_ui_static/app.js`;
- `src/intake_ui_static/ui/api.js`;
- `src/intake_ui_static/styles.css`;
- `tests/test_intake_ui.py`;
- `docs/guides/manual_testing.md`.

Acceptance:

- Calls the standalone endpoint from `RM-100`.
- Shows progress, success, and stable error codes.
- Does not create `career_vault/raw/` or intake-run artifacts.
- Newly saved version appears in Resume Library and existing application picker.
- Double click/repeated request does not silently create versions after the UI
  has already entered success state; explicit second upload remains allowed.

Checks:

```powershell
python -m unittest tests.test_intake_ui -v
python -m unittest tests.test_application_history -v
git diff --check
```

### `RM-230` Offer Keep For Applications After Vault Approval

Recommended model: `gpt-5.6-terra`, max.

Goal: reuse an approved import file as a Resume Version only on explicit action.

Files allowed to modify:

- `src/run_intake_ui.py`;
- `src/application_history.py` only if a small reusable helper is required;
- `src/intake_ui_static/app.js`;
- `src/intake_ui_static/index.html`;
- `tests/test_intake_ui.py`;
- `tests/test_application_history.py`;
- `docs/guides/manual_testing.md`.

Contract:

- action appears only after approval application succeeded;
- backend resolves the exact approved extraction source path internally;
- path must remain below `career_vault/raw/`;
- current bytes must match the extraction SHA-256;
- upload creates a new immutable Resume Version through existing validation;
- action is idempotent for the same approval and resulting library id, or
  clearly asks for a new explicit version rather than duplicating on retry;
- source file is not deleted in this ticket.

Acceptance:

- One approved import becomes one selectable Resume Version.
- Tampered/missing source fails without a partial library row/file.
- Unapproved import cannot invoke the action.
- Vault records and approval audit do not change.

Checks:

```powershell
python -m unittest tests.test_intake_ui -v
python -m unittest tests.test_application_history -v
python -m unittest tests.test_approval_apply -v
git diff --check
```

### Stage 2 Exit Gate

- Both intents work independently.
- Import-only files never enter the application picker.
- Application-only uploads never enter Career Vault intake.
- An approved import can explicitly be kept for applications.

---

## Delivery Stage 3: Permissive Intake And Re-Import

Purpose: characterize and guard user control before changing any matching rule.

### `RM-300` Build A Synthetic Dedup Decision Matrix

Recommended model: `gpt-5.6-sol`, max.

Goal: freeze advisory behavior for realistic ambiguous imports without adding
fuzzy matching.

Files to inspect:

- `src/approval_preview.py`;
- `src/approval_apply.py`;
- `src/vault_intake_review.py`;
- `tests/test_approval_preview.py`;
- `tests/test_approval_apply.py`;
- `docs/guides/vault_intake.md`;
- `docs/process/small_model_playbook.md`.

Files allowed to modify:

- focused tests above;
- one synthetic fixture helper if required;
- `docs/guides/vault_intake.md` for clarified behavior.

Required cases:

1. Exact same company, role, and dates.
2. Same company and role, different non-overlapping dates.
3. Same company, related role, overlapping dates.
4. Two valid positions at ExampleCorp in different periods.
5. Same proof text.
6. Rephrased proof with the same metric.
7. Generated-like resume containing existing Vault facts.
8. One genuinely new fact mixed with many existing facts.

Acceptance:

- Candidate suggestions never apply automatically.
- `Create` remains available for similar roles.
- Existing exact fields remain protected from silent overwrite.
- Rephrased text is not declared equivalent without an explicit reviewed rule.
- Tests describe conservative deterministic behavior rather than inventing a
  semantic matcher.

Checks:

```powershell
python -m unittest tests.test_approval_preview tests.test_approval_apply -v
git diff --check
```

### `RM-310` Make Advisory Defaults Explicit In UI

Recommended model: `gpt-5.6-terra`, high.

Goal: explain possible duplicates without turning them into blockers.

Files allowed to modify:

- `src/intake_ui_static/app.js`;
- `src/intake_ui_static/index.html` only when needed for static copy;
- `src/intake_ui_static/styles.css` only for existing component states;
- `tests/test_intake_ui.py`;
- `docs/guides/manual_testing.md`.

UI contract:

- label candidate as **Possible existing record**, not Duplicate;
- show company, role, and dates needed to distinguish positions;
- keep `Update`, `Create`, and `Do not add` reachable;
- never display a confidence percentage;
- default follows current deterministic exact-match policy;
- generated-resume language receives no special blocking copy.

Acceptance:

- Two ExampleCorp positions can be created separately.
- User can choose Create after seeing a candidate.
- No candidate silently changes a saved decision.
- Keyboard focus and error summary remain intact.

Checks:

```powershell
python -m unittest tests.test_intake_ui -v
python -m unittest tests.test_approval_preview -v
git diff --check
```

### `RM-320` Add Exact Re-Import Awareness Without Blocking

Recommended model: `gpt-5.6-terra`, max.

Goal: detect a previously processed exact source hash and offer reuse while
allowing a new explicit import.

Files allowed to modify:

- `src/run_intake_ui.py`;
- `src/vault_intake_models.py` only if response typing requires it;
- `src/intake_ui_static/app.js`;
- `tests/test_intake_ui.py`;
- `docs/guides/vault_intake.md`.

Contract:

- compare SHA-256 against existing extraction metadata;
- return bounded prior import summaries without resume text or paths;
- offer **Open previous import** as primary convenience;
- offer **Process again** as explicit secondary action;
- same filename conflict remains clear and does not overwrite the prior file;
- different file bytes with the same display name require a new safe name or
  explicit user action, never silent replacement.

Acceptance:

- Exact generated-resume re-upload can still be processed again.
- Detection creates no Vault/library mutation.
- No fuzzy content equivalence is added.

Checks:

```powershell
python -m unittest tests.test_intake_ui -v
git diff --check
```

### Stage 3 Exit Gate

- Synthetic ambiguous-role matrix passes.
- Re-import is allowed and review-gated.
- No automatic semantic/fuzzy matching was introduced.

---

## Delivery Stage 4: Resume Library Management UI

Purpose: make application materials easy to find, hide, restore, and inspect.

### `RM-400` Replace Processed-Source Library With Application Resume Library

Recommended model: `gpt-5.6-terra`, max.

Goal: make the existing **Resume library** page list durable Resume Versions,
not intake-source workflow rows.

Files allowed to modify:

- `src/intake_ui_static/navigation.js`;
- `src/intake_ui_static/index.html`;
- `src/intake_ui_static/app.js`;
- `src/intake_ui_static/ui/api.js`;
- `src/intake_ui_static/styles.css`;
- `tests/test_intake_ui.py`;
- `docs/guides/manual_testing.md`.

Visible fields:

- display name;
- added date;
- file type and content-minimized size;
- Active/Archived state;
- simple usage summary;
- actions appropriate to state.

Do not show:

- relative path;
- hash;
- internal resume id as primary UI;
- processed source/extraction workflow status.

Acceptance:

- Default list shows active Resume Versions only.
- Search uses display name.
- Archived filter is explicit.
- Empty state points to Add resume → Save for applications.
- Existing application picker continues to use the same active library.

Checks:

```powershell
python -m unittest tests.test_intake_ui -v
python -m unittest tests.test_application_history -v
git diff --check
```

### `RM-410` Add Archive And Restore Controls

Recommended model: `gpt-5.6-terra`, max.

Goal: expose the reversible lifecycle implemented by `RM-110`.

Files allowed to modify:

- `src/run_intake_ui.py`;
- `src/intake_ui_static/app.js`;
- `src/intake_ui_static/index.html`;
- `tests/test_intake_ui.py`;
- `docs/guides/manual_testing.md`.

Acceptance:

- Archive requires an explicit click and visible confirmation result.
- Archived version disappears from active library and new pickers.
- Existing application history can still name/open it when the file exists.
- Restore returns it to search/pickers.
- Concurrent stale actions return a stable conflict or idempotent current state.

Checks:

```powershell
python -m unittest tests.test_intake_ui -v
python -m unittest tests.test_application_history -v
git diff --check
```

### `RM-420` Implement Guarded Delete For Unused Resume Versions

Recommended model: `gpt-5.6-sol`, max.

Goal: permanently remove only a Resume Version proven unreferenced.

Files allowed to modify:

- `src/application_history.py`;
- `src/run_intake_ui.py`;
- `src/intake_ui_static/app.js`;
- `src/intake_ui_static/index.html`;
- `tests/test_application_history.py`;
- `tests/test_intake_ui.py`;
- `docs/reference/application_history.md`;
- `docs/guides/manual_testing.md`.

Required server sequence:

1. Resolve the row and validate stored path below the library root.
2. Recompute usage summary inside the mutation lock/transaction boundary.
3. Reject when any current, Applied snapshot, or submitted-event reference
   exists.
4. Move the exact file to a library-local quarantine name before deleting the
   row, or use an equivalently recoverable sequence.
5. Delete the row transactionally.
6. Remove the quarantined file only after commit.
7. Restore/quarantine safely if the database mutation fails.

Acceptance:

- Unused version row and file are removed.
- Referenced version is rejected with stable `RESUME_VERSION_IN_USE`.
- Missing/tampered file is reported; no unrelated path is touched.
- Concurrent selection/delete cannot create a dangling reference.
- Tests use temporary synthetic roots only.
- UI confirmation names the display name and states that deletion is permanent.

Checks:

```powershell
python -m unittest tests.test_application_history -v
python -m unittest tests.test_intake_ui -v
git diff --check
```

Rollback: disable/remove the UI action first. Do not reverse a schema migration
or reconstruct deleted synthetic files outside tests.

### Stage 4 Exit Gate

- Resume Library contains application materials only.
- Archive/restore works for referenced and unreferenced versions.
- Permanent delete works only for unreferenced versions.
- Application audit cannot be orphaned through UI actions.

---

## Delivery Stage 5: Import Cleanup And Navigation Simplification

Purpose: allow import files to disappear after approval and remove processed
sources from the main product navigation.

### `RM-430` Add Explicit Remove Uploaded Import File

Recommended model: `gpt-5.6-terra`, max.

Goal: delete the original import file after successful Vault approval without
changing canonical data or internal approval artifacts.

Files allowed to modify:

- `src/run_intake_ui.py`;
- `src/intake_ui_static/app.js`;
- `src/intake_ui_static/index.html`;
- `tests/test_intake_ui.py`;
- `docs/guides/vault_intake.md`;
- `docs/guides/manual_testing.md`.

Server preconditions:

- source id/path are validated through existing intake models;
- `approval_result.yaml` exists and is valid;
- source resolves below `career_vault/raw/`;
- current bytes match the extraction's stored SHA-256;
- action is explicit and idempotently reports already removed;
- no Career Vault YAML, review, extraction, or approval result is deleted.

Acceptance:

- Approved Vault records remain byte-for-byte unchanged.
- `python -m src.validate_vault` still passes in the synthetic fixture.
- Applied review remains readable without the raw file.
- Unapproved, tampered, or out-of-root source is rejected.
- Tests never target real private files.

Checks:

```powershell
python -m unittest tests.test_intake_ui -v
python -m unittest tests.test_approval_apply -v
python -m src.validate_vault
git diff --check
```

The validator command must use a synthetic project root when the ticket's tests
exercise cleanup. Do not remove the user's real source to demonstrate success.

### `RM-500` Remove Resume Sources From Main Navigation

Recommended model: `gpt-5.6-terra`, high.

Goal: remove the user-facing architecture confusion while preserving unfinished
import access.

Files allowed to modify:

- `src/intake_ui_static/navigation.js`;
- `src/intake_ui_static/index.html`;
- `src/intake_ui_static/app.js`;
- `src/intake_ui_static/styles.css` only if layout needs it;
- `tests/test_intake_ui.py`;
- `README.md`;
- `START_HERE.md`;
- `docs/guides/mvp_workflow.md`;
- `docs/guides/manual_testing.md`.

Target navigation:

```text
Quick start
  Add job
  Add resume

Jobs
  Job library
  Applications

Resumes
  Resume library

Career Vault
  Profile
  Experience
  Projects
  Skill groups
  Education
```

Unfinished imports:

- appear in a bounded **Resume imports in progress** area on Add resume;
- open the existing Review view through a contextual action;
- Review need not remain a permanent sidebar item;
- completed imports need not remain in the primary UI.

Acceptance:

- No main navigation group is called Resume sources.
- Resume Library means application Resume Versions everywhere.
- Incomplete import remains reachable after reload.
- Deep-link/back behavior returns to Add resume.
- Current diagnostics still expose processing failures.

Checks:

```powershell
python -m unittest tests.test_intake_ui -v
python -m unittest tests.test_acceptance_registry -v
git diff --check
```

### Stage 5 Exit Gate

- User can remove an approved import file explicitly.
- Vault survives cleanup independently.
- Import sources no longer occupy a user-facing library/navigation concept.
- Incomplete imports remain recoverable.

---

## Delivery Stage 6: Application Resume Choice

Purpose: present the two application choices without implementing composition or
generation.

### `RM-600` Clarify Application Resume Choice

Recommended model: `gpt-5.6-terra`, high.

Goal: clearly separate building from Career Vault and selecting an existing
Resume Version in the application drawer.

Files allowed to modify:

- `src/intake_ui_static/index.html`;
- `src/intake_ui_static/app.js`;
- `src/intake_ui_static/features/applications.js`;
- `src/intake_ui_static/styles.css` only if required;
- `tests/test_intake_ui.py`;
- `docs/guides/manual_testing.md`;
- `docs/guides/mvp_workflow.md`.

Required UX:

- **Build from Career Vault** opens the existing Resume Plan;
- searchable Resume Version picker remains available;
- upload-new and **Not recorded** remain available;
- no generated file is promised by this ticket;
- explain that the final manually produced file must be uploaded/selected until
  `CAP-RESUME-COMPOSE` and `CAP-RESUME-RENDER` exist.

Implementation note (2026-08-13): the later Application Materials slice now
implements those two capabilities. This historical ticket still explains the
separate internal contracts. In the current UI, Career Vault selection is
embedded inside **Tailor resume** so it is no longer a competing top-level
control; the immutable Resume Version picker remains available independently.

Acceptance:

- Both choices are visible without an additional wizard.
- Build from Career Vault remains available only at Preparing or later under
  current rules.
- Existing Resume Version may be selected at any current supported stage.
- Archived versions do not appear in the default picker.
- Applied snapshot/history behavior is unchanged.

Checks:

```powershell
python -m unittest tests.test_intake_ui -v
python -m unittest tests.test_application_history -v
python -m unittest tests.test_resume_plan -v
git diff --check
```

### `RM-610` Preserve Origin Metadata For Future Generated Versions

Recommended model: `gpt-5.6-terra`, max.

Goal: add minimal internal origin metadata without implementing generation.

Entry condition: implement only if the schema review shows that adding it now
avoids a second near-term migration. Otherwise record a deferred decision and
stop the ticket without code.

Permitted values:

- `uploaded`;
- `generated`.

Contract:

- existing rows migrate to `uploaded`;
- value is internal/supporting metadata, not a different user-facing library;
- generated rows may later carry Vault/plan revisions in a separate authorized
  capability;
- origin never blocks re-import into Career Vault;
- no composer, renderer, or generated file is created here.

Files allowed to modify if implementation is justified:

- `src/application_history.py`;
- `tests/test_application_history.py`;
- `docs/reference/application_history.md`;
- `docs/process/acceptance_registry.md` only for truthful boundary notes.

Checks:

```powershell
python -m unittest tests.test_application_history -v
git diff --check
```

### Stage 6 Exit Gate

- Application UX offers Build from Career Vault or existing Resume Version.
- Both paths preserve the final-file audit boundary.
- No generation/rendering leakage exists.

---

## Delivery Stage 7: Regression And Privacy Review

Purpose: validate the complete change before real files are used for cleanup or
delete actions.

### `RM-700` Run The Full Synthetic Regression Suite

Recommended model: `gpt-5.6-terra`, max.

Goal: validate all touched behavior in one clean synthetic run.

Required scenarios:

1. Import-only resume updates Vault and never enters Resume Library.
2. Library-only resume enters picker and creates no intake artifacts.
3. Approved import is explicitly kept for applications.
4. Approved import original is explicitly removed; Vault remains valid.
5. Generated-like resume is re-imported and remains review-gated.
6. Similar company/role can still be created separately.
7. Active Resume Version archives/restores.
8. Unused Resume Version deletes.
9. Referenced/submitted Resume Version delete is rejected.
10. Applied-resume history remains unchanged across archive.
11. No source/library path escapes its allowed root.
12. Logs and responses contain no file bytes, text, hash, token, or full path.

Commands:

```powershell
python -m unittest discover -s tests -v
python -m src.validate_vault
python -m src.validate_jobs
python -m src.validate_resume_versions
python -m src.validate_application_packages
git diff --check
git status --short
```

Acceptance:

- Full suite passes.
- Validator runs do not modify tracked or private data.
- Git status contains no newly staged/trackable private artifacts.
- Any unrelated pre-existing dirty files are reported, not changed.

### `RM-710` Security And Data-Lifecycle Audit

Recommended model: `gpt-5.6-sol`, max.

Goal: review only the completed diff for data loss, path traversal, dangling
references, race conditions, and misleading UX.

Audit questions:

- Can any endpoint delete outside its exact allowed root?
- Can a stale browser delete/archive the wrong version?
- Can selection race with deletion?
- Can a referenced Resume Version become inaccessible unexpectedly?
- Does cleanup require valid approval and matching hash?
- Can deleting import bytes affect Vault validity?
- Can import-only data enter application picker?
- Can archived versions disappear from historical views?
- Are raw text/hash/path values exposed to UI or logs?
- Does any generated-like import bypass user approval?

Allowed modifications:

- none during the first audit pass;
- fixes require separate ticket ids or an explicitly bounded follow-up.

### Stage 7 Exit Gate

- Synthetic regression passes.
- Security/lifecycle audit has no unresolved P0/P1 finding.
- Real private-data dogfood may begin only after human approval.

---

## Delivery Stage 8: Human Dogfood Gates

These are human-assisted acceptance sessions, not autonomous implementation
goals. Never give a model blanket permission to mutate or delete real files.

### `RM-720` Two-Resume Golden Intake

Recommended model: `gpt-5.6-sol`, max for independent review; Terra high may
execute already-approved mechanical comparisons.

Preparation:

- user provides two resumes and identifies which is newer and their intended
  role families;
- strong model creates an independent gold inventory and conflict matrix before
  the app processes either file;
- make a recoverable backup of current private Vault/import/application data;
- do not put the files or extracted content in Git.

Run:

1. Import Resume A through Update Career Vault.
2. Review every block and approval decision.
3. Validate and record content-minimized counts/timing.
4. Import Resume B.
5. Verify existing roles are updated only by explicit decision.
6. Verify separate same-company positions can remain separate.
7. Compare final Vault to the gold inventory.
8. Keep one selected file for applications; remove another original only after
   explicit user confirmation.

Hard acceptance:

- invented facts: 0;
- silent overwrites: 0;
- lost approved facts: 0;
- unintended duplicate canonical roles/projects: 0;
- unresolved non-empty blocks: 0 before approval;
- deleting the approved import original changes no Vault record;
- the kept Resume Version appears once in the active application library.

Record directional metrics without inventing pass thresholds on the first run:

- minutes to review each resume;
- cards created/updated/skipped;
- possible-duplicate prompts;
- manual field corrections;
- extraction/review failures;
- user confusion points.

### `RM-730` Three-To-Five-Day JD/Application Dogfood

Recommended model: `gpt-5.6-terra`, high for daily diagnostics; Sol max only for
architecture-changing findings.

Suggested volume:

- 15–30 saved JDs;
- 5–10 real applications;
- both Save and digest and Save as applied paths;
- at least two Resume Versions;
- at least one archive/restore;
- multiple app restarts;
- one duplicate JD;
- one correction to title/company;
- submitted-resume history verification.

Record:

- Add job completion time;
- application status/resume friction;
- incorrect assessment/digest findings;
- stale UI or persistence failures;
- Resume Library search/selection friction;
- any need for a general resume assessment mode.

Do not add a feature merely because one isolated session suggests it. Separate
P0/P1 correctness from convenience requests and collect repeated evidence.

### `RM-740` Web Stability Go/No-Go For Desktop

Recommended model: `gpt-5.6-sol`, max.

Goal: decide whether the local web workflow is stable enough to start the
desktop delivery roadmap.

Go criteria:

- no data loss or unhandled P0/P1 issue;
- Career Vault remains canonical and independently valid;
- both Add resume intents are understandable;
- application Resume Version audit is reliable;
- archive/delete/cleanup behavior is explainable and recoverable;
- repeated JD/application workflow survives restart;
- known limitations are documented;
- full tests pass on the release candidate commit.

Possible decisions:

- **Go**: begin `DESK-000` in `docs/product/desktop_delivery_roadmap.md`;
- **Conditional go**: one or two bounded fixes, then recheck;
- **No-go**: evidence-backed blocker with a separate remediation plan.

## Explicitly Deferred

- general resume quality assessment during Add resume;
- automatic resume-to-JD assessment outside an application;
- AI/semantic/fuzzy deduplication;
- auto-merge of Career Vault records;
- wording-variant schema;
- resume composition or generation;
- DOCX/PDF rendering;
- cloud sync, auth, or hosted storage;
- Tauri packaging;
- browser extension capture;
- hard deletion of Resume Versions referenced by application history;
- generic source-document or blob-management product UI.

## Recommended Execution Sequence

Assign one goal at a time:

1. `RM-000`
2. `RM-010`
3. `RM-100`
4. `RM-110`
5. `RM-120`
6. `RM-200`
7. `RM-210`
8. `RM-220`
9. `RM-230`
10. `RM-300`
11. `RM-310`
12. `RM-320`
13. `RM-400`
14. `RM-410`
15. `RM-420`
16. `RM-430`
17. `RM-500`
18. `RM-600`
19. `RM-610` only if its entry condition is met
20. `RM-700`
21. `RM-710`
22. `RM-720` human gate
23. `RM-730` human dogfood
24. `RM-740` desktop go/no-go

Do not let a successful ticket automatically authorize the next one. The user
or coordinating reviewer advances each gate.
