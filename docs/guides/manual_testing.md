# Career Vault Manual Testing

> **Status: needs rewrite.** The intent below is still accurate, but its `python -m src.*` commands belong to the retired Python runtime and no longer run.
> Until this document is rewritten, use the desktop workflows in [START_HERE](../../START_HERE.md) and the checks in the [README](../../README.md#verification). See the
> [documentation index](../README.md); historical comparisons use the
> pinned reference export.

This guide covers the current Career Vault source-intake stage in VS Code. Manual runs append diagnostics to `career_vault/logs/intake.jsonl` for later review by the user and Codex.

## One-Time Setup

Follow [native local development](local_development.md) with the pinned Go and
Node toolchains. VS Code's **Career Hub: Native Desktop** launches the same Go
host as `scripts/start_career_hub.ps1`; Python debugging is no longer a product
entrypoint. Use a disposable synthetic Home for qualification. The ordinary dev
Home is `%LOCALAPPDATA%\CareerHub\dev`; do not use it for destructive testing
if it contains personal data. Installed synthetic qualification and its exact
scope are recorded in the cutover evidence.

The packaged Applications/Workspace acceptance harness uses fictional data and
an independent artifact pin file, rather than the ordinary dev Home. After a
clean Windows package build, create a local JSON pin file from the inspected
build record with `sourceCommit`, `asarSHA`, `sidecarSHA` and `agentSHA` SHA256
values. The current accepted values are in the linked cutover evidence. Run
from the repository root in a normal Windows user context:

```powershell
$env:CAREERHUB_QA_EXPECTATIONS = (Resolve-Path ./tmp/trusted-build-pins.json).Path
node tests/applications_go_package_smoke.cjs
```

`CAREERHUB_QA_PACKAGE_ROOT` optionally selects an absolute ordinary-package
directory; the default is `desktop/out/career-hub-win32-x64`. The harness
rejects mismatched hashes before launch, creates a disposable Temp Home,
sets English through Settings, and adds fictional contact fields through
Profile. Material drafts require a candidate email. It tests 1440px and 390px
list/search/status/Workspace, persisted assessment decisions, desktop resume
upload/submission and mobile retained references, unchanged Profile, HTTP/
console health and normal shutdown. Screenshots stay in its synthetic Temp
directory. This is not real-mailbox or installed-upgrade acceptance.

## Browser UI

1. Select **Career Hub: Native Desktop** in Run and Debug. This configuration and
   `scripts/start_career_hub.ps1` both use `%LOCALAPPDATA%\CareerHub\dev`.
   Use the synthetic qualification harnesses for disposable tests; this
   configuration does not select the historical `sandbox` or `main` profile.
2. Press `F5`. VS Code starts Electron, which owns the Go service and opens its
   assigned loopback address. Confirm the expected `dev` profile, channel,
   and absolute data path before changing records. Rebuild the Go sidecar after
   changing backend code, then restart the desktop; it does not hot-reload the
   packaged backend or static assets.
   Open **Settings** beside the footer refresh control. Confirm the panel opens
   above the lower-right corner, closes with its close control, Escape, and a
   click outside, and does not cover the footer actions. Use its **Theme**
   control to check **System**, **Light**, and **Dark**.
   Reload after each explicit choice and confirm it returns without a flash of
   the other theme. With **System** selected, change the operating-system color
   preference and confirm the open page follows it. Theme preference is stored
   only in browser local storage and does not change Career Vault data.
   Use the adjacent **Language** control to check **System**, **English**, and
   **Русский**. Reload after each explicit choice and confirm it persists without
   briefly showing the other language. With **Русский** active, visit every
   primary view and open at least one dynamically rendered detail panel. UI
   labels, help text, controls, and visible status labels should be Russian.
   Canonical skill/group names, company and role names, JD source text, Career
   Vault evidence, form values, and saved records must remain byte-for-byte
   unchanged. Switch back to **English** and confirm the original UI copy is
   restored. Language preference is browser-local presentation state only; it
   must not change API payloads, matching, sorting, workflow stages, or data.
   Open **Help & setup** beside Settings. Confirm cross-browser extension instructions,
   the two truthful email paths, and FAQ content are readable without leaving
   the app. **Open Email setup** must switch to **Jobs > Email**, and **Open
   Diagnostics** must open Diagnostics. Website watchers must remain clearly
   labelled Planned with no working controls. Repeat at a narrow viewport and
   confirm the panel stays inside the viewport and scrolls internally.
3. Open **Quick start / Add resume** and select **Update Career Vault**. Confirm
   there is no separate Source document selector: choose a UTF-8 TXT or valid
   DOCX and use the single **Extract and review** action.
4. For existing unfinished sources, confirm the bounded **Resume imports in progress** area appears. Select **Continue review** for one and confirm its existing Review opens without extracting it again. Reload once and confirm the unfinished item remains. For a new source, choose the file and select the single **Extract and review** action. Confirm there is no separate Upload step. The operation does not modify approved Career Vault records.
5. Confirm the app opens contextual **Review** only after the source inventory and deterministic card detection are ready; no second Analyze action is required. The sidebar keeps **Add resume** active and the URL contains `#review/<source_id>`. Reload it once or use browser Back/Forward and confirm the same Review restores. Make one disposable unsaved edit, use Back or select another source, and confirm the discard prompt appears. Cancel it and confirm the original review, URL, and edit remain. Accept it and confirm the newly requested view opens without showing data from the former source. On desktop, select several cards and confirm the sticky right inspector updates without moving the main review position. At a narrower viewport, confirm it opens as a dismissible drawer and closes with its close button, backdrop, and Escape. Select **Add resume** to return without losing the review context.
6. Expand **Extraction details** inside Review to inspect counts, section hints, technologies, all non-empty document blocks, and source bullets. These diagnostics no longer have a separate primary navigation page.
7. Open **Resume library**. Confirm it contains only reusable application **Resume Versions**, not processed-source rows or Career Vault review status. Each version shows its display name, added time, file type and size, Active/Archived state, and a bounded application-reference summary. Paths, hashes, internal IDs, and source-extraction workflow details must not appear.
8. Confirm active Resume Versions are the default view. Search by display name,
   filter by digest state, and sort by added date/name without changing any
   record. On a disposable active Resume Version, select **Archive**, confirm
   the named warning, and accept it. Confirm it disappears from the default
   view and the application Resume picker; the single archive-box toggle shows
   it and toggles back to active versions.
9. Open an existing application that used the archived version. Confirm its history still names the version and **Open file** remains available while the local file exists. Return to the archived Resume Library view, select **Restore**, and confirm the success message plus the version's return to the active Resume picker and default search.
10. Upload a synthetic disposable Resume Version that is not selected by any application. Confirm its card offers **Delete**. Select it and confirm the warning names the version, says the removal is permanent, and says it cannot be undone. Accept it, then confirm the card and file disappear while Career Vault records remain unchanged.
11. On a Resume Version used by an application, confirm its reference summary is non-zero and no permanent-delete action is offered. A stale or direct delete request must fail with `RESUME_VERSION_IN_USE`; the application history and private file must remain intact.
12. Select **Add resume** from the library. Confirm **Save for applications** is selected in Add resume, and that choosing it has not uploaded anything yet.
13. Open a **Saved to Vault** source in contextual **Review** and confirm it is read-only. Open an unfinished source and confirm its unresolved card work remains editable. Confirm neither path creates an application Resume Version automatically, and completed imports do not appear in **Resume imports in progress**.
14. On an active TXT or DOCX Resume Version marked **Not digested**, select
    **Digest**. Confirm a new review opens without another file upload and the
    library then offers **Continue review**. Complete and save that review,
    return to the library, and confirm the state is **Digested**.
15. Add or archive a disposable custom skill, or run against a build with a
    changed core vocabulary. Confirm the prior version becomes **Update
    available**. Select **Digest again**, accept the warning, and confirm a new
    review is created while the older review and approved Vault data remain.
    Confirm PDF, DOC, and RTF Resume Versions explain that digest is unavailable
    instead of silently attempting an unsupported parse.
16. Use **Diagnostics** to inspect all events for one `run_id`.

To save an application Resume Version, open **Quick start / Add resume**, select
**Save for applications**, choose a DOC, DOCX, PDF, RTF, or TXT file, optionally
name it, and select **Save resume** once. Confirm success appears without a new
`career_vault/raw/` file or intake run. Then open any application drawer and
confirm the new name is available in its Resume picker and in the default
**Resume library / Active** list.

After applying a complete Career Vault review, open that read-only Review and
select **Keep for applications**. Confirm the original approved file becomes
selectable in Applications without changing the Vault, extraction, or approval
audit. Select it again to confirm it reports the existing Resume Version rather
than creating another. In a synthetic test only, modify or remove the original
file and confirm the action fails with a stable error while the existing Resume
Version remains available.

After a Vault approval, test **Remove original file** only with a disposable
synthetic source. Confirm its warning explains that the removal is permanent and
that Career Vault, the review, and the approval audit remain. Confirm the raw
file disappears while `extracted.yaml`, `review.yaml`, `approval_result.yaml`,
and `python -m src.validate_vault` remain unchanged. Repeat the request and
confirm it reports that the original file was already removed. A changed,
unapproved, or out-of-root source must fail without deleting anything.

DOCX source bullets are detected from visible text markers, list styles, and native Word numbering metadata.

The bottom log line remains visible while switching views and polls the append-only Career Vault intake JSONL log. Closing the browser does not stop the server; stop the VS Code debug session with `Shift+F5`.

The server binds to `127.0.0.1`, accepts only TXT/DOCX uploads up to 10 MB, stores them under `career_vault/raw/`, and does not expose arbitrary local paths. Existing source files are never replaced by upload.

## Windows/macOS Local-Data Parity Check

Use disposable synthetic homes only for this check.

1. On Windows, confirm the default installed channel root resolves below
   `%LOCALAPPDATA%\CareerHub\<channel>`. On macOS, confirm it resolves below
   `~/Library/Application Support/CareerHub/<channel>`. The desktop host
   still passes the resolved resource and data roots explicitly.
2. Create a synthetic Data Home whose path contains a space and a non-ASCII
   character. Bootstrap it twice and confirm the second run preserves existing
   bytes. On macOS/POSIX, confirm the root and application-owned top-level
   directories are owned by the current account and have mode `0700`. On a
   disposable macOS APFS fixture, first add an explicit and inheritable allow
   ACE for a second test account. After bootstrap, use `ls -led` and an actual
   traverse/read attempt as that account to confirm no ACE remains effective.
   Run the native ACL ABI smoke test on every supported macOS architecture;
   Linux mock tests do not satisfy this release gate.
3. In a case-sensitive synthetic fixture, create names differing only by case
   and then names differing only by NFC/NFD Unicode normalization. Run
   `python -m unittest tests.test_runtime_paths -v` and confirm the portable
   collision checks reject both without changing either file.
4. Start the browser against the synthetic home. Confirm Email and Documents
   show wording derived from the actual platform: Windows names its implemented
   protected stores, while macOS or a development-only platform states that the
   unimplemented secure operation is unavailable and preserves the manual
   fallback. Switch to Russian and confirm the dynamic wording translates.
5. Export one synthetic resume and cover letter as PDF. Confirm both are valid,
   selectable-text PDFs using the pinned ReportLab-bundled Bitstream Vera Sans
   faces. Do not claim equivalent DOCX pagination until it is visually checked
   on both supported target platforms.

## Manual Career Vault Editor Check

The Career Vault screen is separate from Resume Digest. It edits canonical
records directly and does not require a source document.

1. Start the browser UI and open **Career Vault / Experience**.
2. Switch between **List** and **Cards**, reload after each selection, and
   confirm the shared Career Vault preference returns. In List, confirm the
   left rail shows companies, roles, dates, categories, technology counts, and
   item counts beside the editor. In Cards, confirm each whole card opens a
   full-width canonical editor, **All experience** returns to the overview,
   and no field values or actions change between views.
3. Search by a company and then by a technology. Confirm the list narrows while
   the canonical record remains unchanged.
4. Select a category filter and then return to **All categories**.
5. Open an existing record, change a field, and select **Cancel**. Reopen the
   record and confirm the canonical value was restored.
6. Select **New experience**. Confirm the ID says it will be created on save.
7. Fill Company, Role, Dates, and Category. Enter technologies and
   responsibilities or achievements one per line.
8. To test validation without saving, remove Company and select **Save
   experience**. Confirm `VAULT_RECORD_VALIDATION_FAILED` appears.
9. Restore Company and save only if this is a real experience you intend to add.
   Confirm the new record appears in the list with **Manual record** provenance.
10. Reload the page, reopen the record, edit one item, save, and confirm the ID
    stays unchanged.
11. Open the same record in two tabs. Save in one tab, then save the stale form
    in the other. Confirm `VAULT_EDITOR_REVISION_CONFLICT` appears instead of
    overwriting the newer value.
12. Select **Archive** and confirm the record leaves the **Active** list and
    appears under **Archived** with the same ID and content.
13. Select **Restore** and confirm it returns to **Active** unchanged.
14. Run `python -m src.validate_vault` and confirm all eight YAML files pass.
15. In **Diagnostics**, confirm `vault_editor_run` records only action, record
    ID, status, and timing; field content must not appear in the log.

Archive and restore are reversible and preserve the complete canonical record.
Archived experience is excluded from new matching and selection suggestions,
while historical references remain valid. Permanent delete is not implemented.
Imported records retain existing metadata when edited; new records use
`source: manual`.

### Project Editor Check

1. Open **Career Vault / Projects** and confirm the same saved **List / Cards**
   preference is selected. Cards must show project name, summary, category,
   technology preview, evidence and Experience-link counts; clicking anywhere
   on a card opens the unchanged editor and **All projects** returns.
2. Open an existing project. Confirm every stored `related_experience` value is
   selected and that unrelated Experience records remain unselected.
3. Change the summary or links and select **Cancel**. Confirm the canonical
   values return without writing YAML.
4. Select **New project**. Fill Name, Category, Canonical summary, technologies,
   optional Experience links, and source-backed project evidence.
5. Save only when the project is factual. Confirm the generated ID is stable
   after later edits and the record shows **Manual record** provenance.
6. Archive and restore the project. Confirm its content and Experience links
   remain unchanged and that it moves between the status lists.
7. Select a nonexistent Experience ID only through a disposable API fixture;
   the server must return `VAULT_RECORD_VALIDATION_FAILED` and leave YAML intact.

`related_experience` is an optional many-to-many reference, not ownership. A
project may link to zero, one, or several Experience records. Archived projects
are excluded from new evidence matching while historical references remain valid.

### Skills Page Check

1. Open **Career Vault / Skills**. Confirm **Groups** and **All skills** are tabs
   inside one page and the left navigation contains no separate Competency
   Explorer destination. Switch Groups between the icon-only List and Cards
   controls. Confirm each complete row/card opens the same group.
2. Sort groups by name, skill count, and source. Press the single archive-box
   toggle and confirm it switches between active and archived groups.
3. Open a group, sort its skills, and use its separate archive-box toggle to
   switch between active and archived skills. An archived skill must never
   appear in the active skill list.
4. Archive a disposable custom group with active skills. Confirm the warning
   gives the cascade count and explains that existing evidence/history remains
   linked while new intake and matching exclude the skills.
5. Confirm the group and its formerly active skills move to archived views in
   one update. Restore the group and confirm only the skills archived by that
   cascade return; a skill archived separately must stay archived.
6. Before restoring, open an Experience or Project that already referenced one
   of the cascaded skills. Confirm the historical pill is visibly marked
   **Archived**, the record can still be saved with that link, and a different
   record cannot attach that archived skill as new evidence.
7. Confirm core groups and skills remain read-only and custom edits still use
   the shared registry revision guard.

A registry entry is vocabulary, not proof. Existing canonical evidence
references to an archived skill remain resolvable for history, but archived
skills cannot be attached by new approvals or used by active matching.

### Education Editor Check

1. Open **Career Vault / Education** and confirm the shared **List / Cards**
   preference is selected and the default subtype shows Schools.
2. In Cards, confirm each school shows institution, degree or major, dates,
   item count, and honors preview without exposing its internal ID. Open a card
   and use **All education** to return. In List/editor, confirm stable ID and
   provenance remain available as before.
3. Switch to **Certifications** and confirm the list and form change without
   mixing school fields into credential fields.
4. Search each subtype and confirm only the visible subtype is filtered.
5. Change a field and select **Cancel**. Confirm the canonical value returns.
6. Select **New school**, enter Institution and Degree, then save only if the
   record is factual. Optional dates, status, GPA, location, major, and honors
   may remain empty.
7. Select **New certification**, enter Certification or issuer, Credential, and
   Completed. Add only source-backed credential evidence.
8. Open the same Education page in two tabs. Save a school in one tab, then try
   saving the stale certification form in the other. Confirm
   `VAULT_EDITOR_REVISION_CONFLICT` appears; schools and certifications share
   one `education.yaml` revision.
9. Archive and restore one disposable record. Confirm all values remain intact.
10. Confirm an archived certification no longer appears as active evidence in a
    newly run job assessment, while the record remains under **Archived**.
11. Run `python -m src.validate_vault` and confirm all eight YAML files pass.
12. In **Diagnostics**, confirm `vault_editor_run` records the subtype as
    `school` or `certification` without logging field content.

Archive is the current reversible removal operation. Permanent delete remains
out of scope, and the editor never infers a degree, GPA, completion date, or
credential.

### Profile Editor Check

1. Open **Career Vault / Profile** and confirm there is one form rather than a
   list of cards.
2. Confirm the stored identity, location, work authorization, contact details,
   and role targets are loaded from `career_vault/profile.yaml`. The work
   authorization control offers common U.S. statuses, including distinct F-1
   OPT and STEM OPT extension options; **Other** is the final menu option.
3. Select **Other**, enter a truthful custom value, save, and reload. Confirm
   the custom field is shown with the exact stored value. Select a listed value
   and confirm the custom field hides and does not overwrite the selected value.
4. Change one field and select **Cancel**. Confirm the canonical value returns.
5. Clear one optional known contact field in a disposable Vault fixture, save,
   and confirm that key is removed while unrelated metadata remains unchanged.
6. Open Profile in two tabs. Save in one tab, then try the stale form in the
   other. Confirm `VAULT_EDITOR_REVISION_CONFLICT` appears.
7. Confirm there are no New, Archive, Restore, or Delete actions for Profile.
8. Run `python -m src.validate_vault` and confirm all eight YAML files pass.
9. In **Diagnostics**, confirm profile saves and failures identify
   `record_type: profile` without logging field content.
10. In a disposable Data Profile, set the exact values under **Confirmed
    eligibility facts** and save them separately from the general Profile.
    Reload and confirm the values persist. Leave an optional conflict field at
    **Unknown** and confirm an explicit matching JD restriction stays Review.
11. Under **Clear-conflict policy**, disable one class and save. Open a complete
    synthetic JD with exact evidence for that conflict and confirm it stays
    visible as Review. Re-enable the class, reopen the assessment, and confirm
    the same source/profile pair becomes a clear conflict without disabling any
    capture, Save, Applied, or Continue action.
12. Repeat step 11 in two tabs and confirm the stale second save returns
    `HARD_BLOCKER_POLICY_REVISION_CONFLICT`. Confirm a stale eligibility save
    returns `VAULT_EDITOR_REVISION_CONFLICT`.
13. With Career Hub started using `-EnableExtension`, confirm the unpacked
    extension can still receive the bounded assessment receipt but direct GET
    or PUT attempts for `/api/hard-blocker-policy` and
    `/api/vault/eligibility` return `LOCAL_API_ACCESS_DENIED`.
14. Under **Application helper**, confirm the allowlist is empty on a new
    disposable profile. Open **Configure fields**, change the synthetic name, email, and
    future-sponsorship selection, close without saving, and confirm the summary
    remains unchanged. Save the selection, reload, and confirm only those
    choices persist.
15. Request the helper projection with its helper token and confirm it contains
    only enabled fields with present values. An enabled empty source field must
    be absent. Confirm the configuration API contains labels and enablement but
    no candidate values.
16. Confirm a capture token and browser token cannot call the helper projection,
    and a helper token cannot call capture, application, document, Career Vault,
    or configuration routes. A hostile or missing Origin cannot bootstrap the
    helper token.
17. Submit an unknown or forbidden field id and expect a validation error;
    submit a stale revision and expect
    `APPLICATION_HELPER_REVISION_CONFLICT`. Confirm neither error contains a
    candidate value.
18. Run `node tests/browser_hard_blocker_policy_smoke.cjs` and inspect the
    application-helper desktop and mobile screenshots. Confirm the dialog is
    keyboard-operable, internally scrollable on mobile, and produces no page
    overflow or console error.

Profile is a singleton because one Career Vault represents one candidate. The
editor updates source-of-truth facts only and does not infer a headline or
other new resume content. Eligibility and policy changes are private and
advisory; they invalidate cached assessments but do not rewrite saved JDs. The
application-helper allowlist is a privacy control: only enabled, present values
can appear in the attended side panel, and only explicit reviewed prefill can
transmit selected values to revalidated visible empty inputs.

### Experience Evidence Items Check

1. Open **Career Vault / Experience** and select an existing record. Confirm
   **Responsibilities and achievements** renders every saved bullet as its own
   numbered evidence card, rather than a single multiline field.
2. Edit one card and confirm the surrounding bullets remain unchanged. Use the
   up/down controls to reorder a card and the remove control to delete one.
3. Add a source-backed item, then save and reload. Confirm the same item order
   is retained in the canonical experience record. Add duplicate or blank text
   and confirm save retains only one non-empty canonical item.
4. Select **Cancel** after an edit, reorder, or removal. Confirm the saved
   evidence cards are restored and no Career Vault data changes.

## Detailed Debug Mode

For normal runs, use:

```powershell
python -m src.run_intake_ui --port 8766
```

When reproducing an unexpected failure, stop the server with `Ctrl+C` and restart it with:

```powershell
python -m src.run_intake_ui --port 8766 --debug
```

`--debug` adds terminal tracebacks for unexpected request failures. Expected validation and conflict errors remain structured instead of producing noisy stack traces. Every API error shows a stable error code, a summary, the technical message, a suggested next action, and a run id when the operation created one.

In **Diagnostics**, select that run id. Failed events include `schema_version`, per-run `sequence`, `stage`, `error_code`, exception type, technical message, and guidance. Logs intentionally omit request bodies and extracted resume blocks. `career_vault/logs/intake.jsonl` is private local data and must not be committed.

## Happy Path

1. Select **Career Vault: Extract Current Resume** in Run and Debug.
2. Press `F5`.
3. Follow the stage events in the integrated terminal.
4. Confirm the final run status is `success` or `success_with_warnings`, not `error`.
5. Inspect `career_vault/intake_runs/<source_id>/extracted.yaml`.
6. Create the review inventory:

```powershell
python -m src.build_intake_review career_vault/intake_runs/<source_id>/extracted.yaml
```

7. Confirm `review.yaml` contains one `block_reviews` item per non-empty extracted block. A new review should report `success_with_warnings` because all blocks start as `unassigned`.
8. Run `python -m src.validate_intake_review` and confirm there are no errors.
9. Open `career_vault/logs/intake.jsonl`. The last line contains the latest `run_id`; search for that id to see only the current run.

Expected review result:

- reviewed block count equals the number of non-empty extracted blocks;
- new reviews contain no validation errors;
- unassigned blocks produce one summary warning.

Extraction counts vary by source and taxonomy. Unexpected changes should be reviewed against the matching `run_id`.

## Review Refresh Safety Check

1. In a generated `review.yaml`, change one block to `ignored` and add a `user_note`.
2. Run the same `build_intake_review` command again.
3. Confirm the disposition and note remain unchanged and the terminal reports preserved decisions.

The browser Review view edits `review.yaml` through the local validated API. Structurally unique ownership can be assigned automatically by **Detect cards**; ambiguous candidates remain suggestions until accepted. **Accept this card** changes only the selected card's pending unambiguous links, and **Accept all links** is the bulk action. Save is explicit. A stale tab receives `REVIEW_REVISION_CONFLICT` instead of overwriting a newer draft. No action promotes draft cards into canonical Vault files.

## Approval Decision Check

Use a source whose review already has detected cards. Preparing and saving decisions changes only its local `review.yaml` draft.

1. In **Review**, resolve every unassigned source block. Restore or ungroup every queued card.
2. Select **Prepare approval**. Confirm the card hierarchy is replaced by **Career Vault comparison**.
3. Confirm a card with exactly one deterministic match defaults to **Update**, a card with no match defaults to **Create**, and ambiguous, unsupported, and category-only content defaults to **Do not add**. A candidate is labelled **Possible existing record**, never Duplicate, and shows its company, role, and dates without a confidence percentage. With two ExampleCorp positions from different periods, confirm both remain visible and **Create new Career Vault record** remains available. Re-open the comparison after saving and confirm an existing saved decision did not change automatically. Create must select all available reviewed-source fields by default.
4. Confirm each Update/Create card has one row per field. Exact normalized matches are green and locked, partial overlap is yellow, and one-sided or different values are neutral. These are comparison states, not confidence scores.
5. On an Update card, confirm the Career Vault side is selected by default. Click one scalar source field, such as Role, and confirm only that source side becomes selected.
6. Click one list source field, such as Technologies or Proof points. Its action should say **Add source values**, not replace the Career Vault list.
7. Click the same row's Career Vault side and confirm the source change is deselected without deleting the existing Vault value.
8. Select **Use all source fields** and confirm all available non-identical source fields are selected while exact fields remain locked as Same.
9. On a Create card, confirm all available source fields are initially selected and the empty Career Vault side is not selected. Deselect any field that should not be added.
10. Confirm a Project Create decision cannot become approval-ready without selected source Name and Summary; no generic summary should be invented.
11. Confirm Professional Summary and category-only blocks show **Do not add to Career Vault**.
12. Select **Save decisions**. Confirm no Career Vault YAML changed.
13. Reload, prepare approval again, and confirm the action plus every field choice is restored.
14. Open `career_vault/intake_runs/<source_id>/review.yaml`. New Update/Create decisions must contain `field_resolutions`; only Update (`merge` internally) may have a non-null `target_id`.
15. Confirm **Apply to Career Vault** is disabled while decisions are unsaved or required Create fields are missing.
16. Cancel the Apply confirmation once and verify no Career Vault file changes.
17. Apply only when the source is intended to update the real local Vault. Confirm unselected scalar fields are unchanged and selected list values are added without duplicates.
18. Open `approval_result.yaml`. Confirm it records the review and Vault revisions, field resolutions, decisions, and exactly the files changed.
19. In **Diagnostics**, confirm `approval_apply_run` completed. A second apply must fail with `APPROVAL_ALREADY_APPLIED`.

Failure checks:

- Open the same review in two tabs. Save in the first, then save the stale second tab; it must fail with `REVIEW_REVISION_CONFLICT`.
- Move a decided card to the review queue. Approval preview must remain unavailable until the card is restored or ungrouped.
- Manually add an unknown field resolution to a disposable review copy. Apply must reject it as `INVALID_FIELD_RESOLUTION` without changing Career Vault YAML.

## Debugging With Breakpoints

Useful breakpoint locations:

- `src/extract_vault_source.py`: source validation, output guard, serialization, and atomic write.
- `src/vault_intake.py`: document parsing and the signal-extraction loop.
- `src/intake_observability.py`: stage completion and failure recording.

Use `F10` to step over, `F11` to step into, `Shift+F11` to step out, and inspect local variables in the Run and Debug sidebar. Do not edit canonical Vault YAML while paused inside a run.

## Selected Source

Select **Career Vault: Extract Selected Source**, press `F5`, and enter a path under `career_vault/raw/`. TXT and DOCX are supported. The run is rejected before parsing if the path is outside that directory.

## Controlled Failure Checks

Run these in the VS Code integrated terminal. These failures are expected and must also appear in the JSONL log.

Existing-output guard, without `--overwrite`:

```powershell
python -m src.extract_vault_source career_vault/raw/example_resume.txt
```

Expected error code: `OUTPUT_GUARD_FAILED`.

Missing source:

```powershell
python -m src.extract_vault_source career_vault/raw/manual_missing_file.txt
```

Expected error code: `SOURCE_VALIDATION_FAILED`.

Quiet console with persistent logging:

```powershell
python -m src.extract_vault_source career_vault/raw/example_resume.txt --overwrite --quiet
```

The terminal hides stage events, but the latest JSONL run must still contain them.

## Focused Automated Check In The IDE

Select **Career Vault: Focused Tests** and press `F5`. This is supplementary to manual review; it checks stage contracts, error codes, log validity, and extraction behavior.
## UI Foundation Smoke Check

Run this after starting the local server. It does not create a job or change
Career Vault records unless you intentionally use the optional toast check.

1. Confirm the sidebar navigation and buttons are present after a reload. Move
   between **Jobs**, **Applications**, **Resumes**, **Career Vault**, and **Insights**;
   the active label and page heading must stay in sync.
   From Jobs, confirm **Job Feed** and **Job Workspace** are nested and available.
   From Career Vault, confirm **Experience**, **Profile**, **Projects**, **Skills**, **Education**, **Documents**, and **Contacts** are nested under Career Vault.
   Confirm the fixed **+ Add** quick-action button stays pinned at the bottom of the sidebar and never jumps between views.
   At a narrow viewport, the header **+** must open the same Add job
   and Add resume quick actions rather than navigating directly to Add job.
2. Open **Jobs > Job workspace**. Tab to **Selected JD**, press ArrowDown, then
   End. Confirm the complete list opens and the active option moves to its last
   visible record. Press Escape; the list must close and the previously selected
   JD name must return to the input.
3. With the selector focused, remove the selected text and type part of a known
   company or role. Confirm only matching records remain. Press Escape to
   restore the previous value without changing the selected job.
4. Confirm the application-status area uses one compact status badge and a
   single **Open in Applications** action. It must remain disabled when no saved
   application record exists.
5. In **Applications**, temporarily use a disposable job only if you want to
   verify feedback: save a stage change and confirm one dismissible toast appears
   without blocking the drawer or keyboard focus. Restore the disposable record
   afterward.
6. At a narrow viewport, confirm action bars stack cleanly and the toast stays
   inside the viewport.

## Skills Registry And Quick Actions Check

Use disposable custom names for this check.

1. Open **Career Vault > Skills**. Switch between **List** and **Cards**, reload
   after each selection, and confirm the last selected view returns. In List,
   confirm the page uses a two-pane registry with full-width group rows on the
   left and the selected group's skills on the right. Click the name, count,
   source label, chevron, and empty space within a row; every target must open
   the same group. In Cards, confirm the responsive grid shows source, active
   count, skill preview, and **Open group**; clicking anywhere on a card must
   open its full-width detail, and **All groups** must return to the grid.
   Core must use the blue square **built-in** treatment and Custom the amber
   circle **user-defined** treatment, so source remains clear without color
   alone.
2. From **Quick start > Add resume**, select **Add group or skill**. Create one
   custom group, then paste two comma-separated skill names. Confirm they become
   separate removable pills and are written together with exactly one selected
   primary group. Repeat with one skill and a distinctive alias.
3. Open **Quick start > Add job** and confirm the same Quick Action sees the
   group and skill without a separate JD vocabulary setup.
4. Try to add the same skill name or alias with different case and extra spaces.
   Confirm the request is rejected with a visible conflict and neither custom
   catalog file changes.
5. Paste `We need a candidate with strong Excel VBA skills.` and digest it.
   Confirm Excel and VBA are highlighted as two skills. In the normalized job
   YAML, confirm two `digest.skill_occurrences` entries retain separate source
   text, line numbers, spans, primary groups, and origins.
6. Extract a disposable resume containing the custom alias. Confirm the exact
   source span is highlighted and resolves to the same custom skill id used by
   JD intake.
7. Confirm unknown prose in either intake flow does not create a group, skill,
   alias, or candidate-evidence record automatically.
8. In Resume Digest, open an Experience, Project, or Certification card. Confirm
   recognized skills are prepopulated as removable pills. Type an existing
   alias and press comma or Enter; confirm it becomes the canonical pill rather
   than a duplicate custom label.
9. Enter two unknown labels separated by a comma. Confirm each remains visibly
   marked as a pending custom skill and defaults to **My custom skills**. Change
   one group, then use **+ Create a new group** for the other.
10. Save the review draft and prepare approval. Confirm neither custom taxonomy
    file changed and the comparison labels the field **Skills**.
11. Deselect Skills (or choose Do not add) and apply a disposable review;
    confirm no custom term is created. In a second disposable review, approve
    Skills and confirm the new custom term plus the card's `competency_ids` are
    written together. `approval_result.yaml` must record Vault and registry
    revisions and the created skill ids.
12. Confirm the Skills page opens group/skill details, permits custom edits,
    and does not render the deprecated whole-group Evidence Groups editor.
    Existing legacy YAML remains available to the explicit migration below;
    it is not a second vocabulary-management surface.
13. Open one custom skill created from each available workflow. Confirm its
    detail explains whether it came from Skills Registry, a Career Vault
    editor, Resume Digest approval, or the legacy migration. A pre-existing
    record without metadata must say that its creation source was not recorded;
    merely opening it must not rewrite private YAML.
14. Link a disposable skill to an Experience, archive the skill, switch to the
    archived registry scope, and open it. Confirm **Historical evidence** loads
    the existing Experience link and shows **Archived scope**. Confirm there is
    no **Open in All skills** action because **All skills** is active-only.
15. In a disposable Data Home, create a private skill, then add a colliding
    canonical name or alias only to a copied core catalog. Reload the registry:
    the private id and YAML bytes must remain unchanged, matching must stay
    unambiguous, and the UI must report a catalog compatibility adjustment.
    Creating the same conflict through the UI must still be rejected.

For an existing private data home, first dry-run the literal legacy keyword
migration. Review every unresolved label before applying its exact plan id:

```powershell
python -m src.migrate_legacy_skills plan --data-root C:\path\to\CareerHubData --resource-root C:\path\to\career_hub
python -m src.migrate_legacy_skills apply --data-root C:\path\to\CareerHubData --resource-root C:\path\to\career_hub --confirm-plan sha256:<reviewed-id>
```

The migration may create private vocabulary only. It must not infer a group
beyond **My custom skills**, create `competency_ids`, or change core catalogs.

After vocabulary has been reviewed, preview canonical references for existing
Experience, Project, and Certification records. The preview is read-only and
must show every record plus each exact, alias, unmatched, archived, or ambiguous
observation:

```powershell
python -m src.migrate_vault_competencies plan --data-root C:\path\to\CareerHubData --resource-root C:\path\to\career_hub
```

Review the complete output, then apply only its exact Plan ID:

```powershell
python -m src.migrate_vault_competencies apply --data-root C:\path\to\CareerHubData --resource-root C:\path\to\career_hub --confirm-plan sha256:<reviewed-id>
```

Confirm that apply creates a verified Career Vault recovery ZIP below
`backups/`, writes a content-minimized receipt below
`logs/competency_migrations/`, and adds only `competency_ids`. Existing
`technologies`, certification text, archived records, and unmatched observations
must remain unchanged. Run plan/apply again and confirm the second apply is a
no-op without another backup.

Build the read-only Candidate Evidence Graph and its analytics with an explicit
month so repeated output is directly comparable:

```powershell
python -m src.candidate_evidence_graph --data-root C:\path\to\CareerHubData --resource-root C:\path\to\career_hub
python -m src.evidence_analytics --data-root C:\path\to\CareerHubData --resource-root C:\path\to\career_hub --as-of 2026-08
```

Confirm that graph edges come only from active records with explicit
`competency_ids`; changing a saved JD or draft review must not change the graph
revision. For one competency shared by an Experience and its linked Project,
confirm one professional context, both direct evidence records, the combined
proof count, and no duplicate months. A Project with no Experience links counts
as one independent project. Certification evidence contributes certification
and proof counts but not a usage interval. Normal output excludes archived
competencies; use `--include-archived-competencies` only to inspect historical
references. Neither command writes files or emits a composite score.

Build saved-JD demand for an inclusive captured-date period, then join it to
the same Candidate Evidence projection:

```powershell
python -m src.jd_demand_index --data-root C:\path\to\CareerHubData --resource-root C:\path\to\career_hub --from 2026-01-01 --to 2026-08-31
python -m src.coverage_matrix --data-root C:\path\to\CareerHubData --resource-root C:\path\to\career_hub --as-of 2026-08 --from 2026-01-01 --to 2026-08-31
```

Confirm that only normalized cards already registered in
`applications/career_hub.sqlite3` are counted. Two ledger records with the same
non-empty content fingerprint must contribute one distinct JD. Required and
preferred counts remain separate; mentioned-only occurrences do not count.
Older cards without `digest.skill_occurrences` may use only exact active names
or approved aliases from the same Skills Registry. Neither command may write a
database, job card, catalog, or Career Vault file.

For Coverage Matrix, confirm all four statuses on controlled data:
`supported`, `thin_evidence`, `evidence_not_found`, and `not_requested`.
Absence wording must be exactly **Evidence not found in Career Vault.** It must
not claim that the candidate lacks or does not know the competency, and output
must not contain an overall score.

Open **Jobs > Demand & coverage** and confirm:

1. **Evidence coverage** shows the requested-skill distribution for the current
   scope with separate `supported`, `thin_evidence`, and
   `evidence_not_found` counts. **Most requested skills** keeps required and
   preferred counts visible, and **Priority evidence gaps** ranks only by the
   documented required/preferred ordering; no fit score or trend claim appears.
   Exact duplicate JD fingerprints do not inflate any total.
2. Confirm **All time** is the default period. Select 30 days, 90 days, and six
   months and compare each result with the CLI projection for the same inclusive
   dates. Select **Custom dates**, apply a valid From/To pair, reload, and confirm
   the choice returns. A period with no demand must say that no saved-job demand
   exists *in that period* and offer **View all saved jobs**.
3. Select a Domain from **Filters** and confirm the coverage band and both
   insight lists update with the detailed list. Then combine search, Required /
   Preferred, coverage tabs, and sort controls. Every visible row must still be
   requested by at least one saved job in the selected period.
4. Confirm the detailed columns are Skill, Demand, Evidence, and Coverage.
   Required and preferred counts remain separate within Demand;
   `supported`, `thin_evidence`, and `evidence_not_found` remain visibly distinct.
   The list uses page scrolling rather than a second internal scrollbar.
5. Select AWS or another **Evidence not found** row. Confirm a right drawer
   shows only the safe absence wording, separate required/preferred counts, and
   the contributing saved-job titles. Expand the complete related-job list and
   open one job; it must select that record in Job workspace without triggering
   automatic reanalysis from this read-only path.
6. From the same drawer choose **Open in Skills**. Confirm **Skills > All
   skills** opens that canonical entry and says only that evidence was not found
   in Career Vault; it must not say the candidate lacks the skill.
7. Select coverage legend entries and the two insight-list actions. Confirm they
   update and scroll to the same detailed list rather than creating a second
   analytics surface.
8. At a narrow viewport, confirm insights stack, rows become readable summaries,
   the drawer becomes full width, there is no horizontal page overflow, and all
   controls remain keyboard reachable. Escape and the backdrop must close the
   drawer and restore focus to the triggering skill.

Open **Career Vault > Skills > All skills** and confirm:

1. Every active registry skill is listed, including skills with no evidence.
   Search matches canonical names, aliases, and groups; Group, Source, Evidence,
   Domain, and Kind filters combine.
2. The list shows Core/Custom ownership, evidence count, and last-used state.
3. Selecting the full row updates the evidence detail. **Manage skill** opens the
   same skill in **Groups**, where vocabulary changes remain available.
4. Aliases, classification, professional contexts, dated usage, and active-now
   state match the CLI projection.
5. Experience, Project, and Certification sections show canonical records;
   **Open record** navigates to the corresponding editor and selects that id.
6. Narrow and mobile layouts stack the detail beneath the list without
   horizontal page overflow. Keyboard focus and row selection remain visible.
7. Evidence itself has no edit, archive, demand, gap, or score control. In
   **Groups**, open a skill and confirm its compact evidence section links back
   to **All skills** and opens the same canonical evidence records.
8. Load `#competencies` directly and confirm it redirects to `#skills/all`
   without losing the evidence view.

Run the isolated synthetic rendered check after changing either surface. From
an ordinary shell, first install the declared maintainer dependency and its
matching browser (`npm ci --ignore-scripts` and `npx playwright install
chromium`); this setup is also documented in `docs/guides/local_development.md` and
enforced in CI. The check
uses only a temporary Career Vault and saved-job projection, covers desktop and
390-pixel layouts, search/filter/detail navigation, safe gap wording, drawer
focus restoration, the legacy redirect, canonical-record navigation, and
horizontal overflow. It also proves these interactions do not change the
synthetic Vault or job sources and fails on browser console/page errors:

```powershell
npm run smoke:competency
```

Review the six screenshots in the printed temporary artifact directory.

## JD Intake Browser Check

1. Start the server with `python -m src.run_intake_ui --port 8766 --debug`.
2. Open **Quick start > Add job** and load a UTF-8 `.txt` or `.md` posting, or paste the complete text.
3. Optionally enter an absolute HTTP/HTTPS posting URL. Leave **Optional source overrides** closed for the first run.
4. Select **Save and digest** and confirm the UI opens **Job workspace** with the new draft selected, application status **Saved**, and a stored assessment already visible. Repeat with **Save as applied** and confirm it opens directly as **Applied** with the same immediate assessment.
5. For the Applied record, select a saved resume and use **Record submitted resume**. Change the selection and record it again; confirm both entries are retained in the submitted-resume history.
6. Switch between two records with **Selected JD** and confirm **Selected job**, the digest, assessment, and current application status update immediately.
7. For a stale `draft`, confirm the UI runs reanalysis once and changes the metadata to `analysis current`.
8. Select **Reanalyze** and confirm the run completes without creating a second source or card.
9. Select **View original JD** and confirm the exact saved text opens inside Job Workspace without changing the saved record or navigating to Add job. Restart the local server while the page remains open, then retry; the browser must refresh its capability token once instead of showing `LOCAL_API_ACCESS_DENIED`.
10. Select the pencil **Edit** action and confirm the displayed identity facts become inputs in the same cells. Change the company or role, save, and confirm the digest updates and a later **Reanalyze** keeps the explicit override. Confirm **Cancel** and Escape restore display mode without changing the selected record. Repeat for the eligibility and requirements editors.
11. Confirm a `ready` record shows **Review protected** instead of allowing automatic reanalysis.
12. Confirm there is no second job search, queue, or saved-jobs table on this page; switching jobs happens through the searchable **Selected JD** control.
13. Confirm the detail view shows company/title, location/workplace, the retained **Open posting** URL, source-backed compensation or an explicit **Not stated**, eligibility gates, technology signals with contextual **Add job skill**, typed **Requirements**, and collapsed **Parser details**. Add a disposable canonical skill and confirm it appears on this JD at the selected requirement level; expanding Parser details must use a bounded internal scroll region.
14. Hover a detected field, gate, or classified requirement and compare its line evidence against the pasted source.
15. Confirm each classified requirement shows cleaned display text, retains the original source line in evidence, and uses one of `technical`, `capability`, `constraint`, or `education` with a required/preferred level.
16. Expand **Needs review** and compare every listed line with the source. It should contain relevant responsibility or qualification text that produced no structured fact, not headings, EEO language, accommodations, company descriptions, or compensation boilerplate.
17. Confirm `Relevant = Structured + Needs review` and `Source = Relevant + Context`. These are line counts, not confidence percentages.
18. Compare the created file under `jobs/incoming/` with the submitted text. The content must be unchanged.
19. Open the corresponding YAML under `jobs/normalized/`. Confirm `content_fingerprint`, `compensation`, `digest.analyzer_version`, `classified_requirements`, `analysis_coverage`, and `raw_jd_path` exist. Compare each compensation range and evidence line with the original posting.
20. Immediately after saving, confirm **Job saved assessment** shows one conclusion-led card rather than a processing state. It must contain an Apply, Review, or Skip recommendation, visa compatibility, citizenship/clearance, experience comparison, seniority mismatch, location conflict, and important requirements.
21. For every conclusion based on a detected JD fact, compare its displayed **JD evidence** line with the saved source. For absence findings, confirm the UI explicitly says no relevant language was detected instead of inventing a source line.
22. Switch between a JD with an eligibility conflict and one with only technical gaps. Confirm the selected assessment updates and no stale result from the previous JD remains.
23. Open **Review** on warning, informational, and successful conclusions. Confirm all are actionable, warnings default to **Proceed / accept risk**, and only **Resolved / confirmed** plus **Proceed / accept risk** are offered. Select **Correct saved job data** and confirm the dialog closes, the page scrolls to the relevant facts, and their in-place editor opens. Record a note and confirm the automated text remains, the decision appears beside it, and a later changed assessment marks that decision for review again. Open the same record in **Applications** and confirm the drawer shows only the compact recommendation plus a link back to the full analysis.
24. For a `ready` card, run `python -m src.review_job jobs/normalized/<job_id>.yaml`. Confirm `application_allowed: true` even when the UI recommends Skip or Review.
25. Confirm every digest `classified_requirements` item appears in `classified_requirement_review` and, after building model context, in `evidence_matrix.yaml` with its source evidence.
26. Paste the same posting again with different whitespace or casing. The UI must open the existing record and state that it was already captured; no second raw file may remain.
27. Test a posting that explicitly says sponsorship is unavailable. The sponsorship gate must be `incompatible` for the current local visa profile.
28. Test a posting with no sponsorship language. The gate must be `not stated`, not an invented rejection.
29. Run `python -m src.validate_jobs`.
30. Delete uniquely named manual-test files only after deciding they are disposable test data.

This check validates storage, deterministic digest, provenance, and exact deduplication. It does not validate semantic matching, scoring, URL fetching, or resume generation.

## Application History CLI Check

These commands create private local state under `applications/`; they do not
modify Career Vault YAML or normalized JD cards.

1. Run `python -m src.application_history init` and confirm the database path is
   `applications/career_hub.sqlite3`.
2. Run `python -m src.application_history sync` and confirm the number matches
   the valid YAML cards under `jobs/normalized/`.
3. Run python -m src.application_history list. Every newly registered job
   should have status captured and no user action is required to create its
   ledger record.
4. Choose one disposable test job and run
   python -m src.application_history status <job_id> preparing.
5. Run python -m src.application_history status <job_id> applied --note
   "Manual test".
6. Run python -m src.application_history show <job_id> and confirm the
   captured, preparing, and applied events retain occurred and recorded
   timestamps.
7. Run sync again. Confirm the current status, notes, and history remain
   unchanged.
8. Confirm `git status --short -- applications` remains empty because the
   database is private local data.

The CLI does not enforce a status sequence. This is intentional: assessment is
advisory and the candidate owns the application decision.

## Application Tracking UI Check

1. Start the local UI with scripts/start_career_hub.ps1. See
   docs/guides/local_development.md for first-time setup and alternatives.
2. Open the local URL and choose **Jobs > Job workspace**.
3. Select a disposable valid saved job. Confirm its application status is
   **Saved** and it already appears under **Applications**.
4. Select **Open in Applications**. Confirm the job opens without any
   decision or start-application control.
5. Open the application drawer while the job is still **Saved**. Confirm its
   summary shows current stage, applied date, compact assessment result, and
   **Submitted resume** as **Not recorded**,
   while **Open job record** returns to the full findings in Job Workspace.
6. In Job Workspace, confirm Application materials offers **Tailor resume** and
   **Create cover letter** after the assessment. The resume area separates the immutable **Submitted
   resume** record from the **Resume for tailoring** picker. Upload controls are
   collapsed under **Add a new resume version**; no paths, hashes, IDs, or
   version mechanics are visible. With no
   selection there is no redundant state action. After selecting a version,
   confirm **Clear selection** appears.
7. Upload a disposable .docx or .pdf resume with a display name. Confirm it is
   immediately selected and appears in the same picker. Select **Open file**
   and confirm the same local file opens.
8. Type a distinctive part of that display name into the picker. Confirm it can
   be found without scrolling a native dropdown. Open a second disposable job
   and confirm the same saved resume can be selected there.
9. Change the first job's stage to **Applied**. Confirm it saves with no resume
   selected. When a resume was selected, confirm the application row and drawer
   state which resume was used when Applied, and the drawer can open the
   immutable file.
10. After Applied, upload and select a second disposable resume. Confirm the
    current selection changes but the Applied resume statement still names the
    original version.
11. While the job is Saved, confirm **Tailor resume** is available. Change the
    job to **Preparing** and confirm both its embedded content selection and the
    Resume Version picker remain available; the stage records progress and
    grants no permission.
12. Search by company or role and combine it with the Stage filter. Confirm the
    result count updates without changing persistent data.
    Confirm the desktop list keeps Application, Stage, Location, Updated, and
    action columns aligned across rows with and without a posting URL. In Dark
    theme, switch between Current, Closed, and All and confirm the selected
    scope has both contrasting text and a visible accent marker.
13. Switch between **Current**, **Closed**, and **All applications**. Saved
    jobs must be included in Current and All; only closed statuses appear in
    Closed.
14. Add a dated status update with a short disposable note in the drawer.
    Confirm the timeline includes the initial captured event as Saved.
15. Close the drawer with its close button, backdrop, and Escape key. Each
    action should return to the same ledger position.
16. Open **Job workspace**, reanalyze the same JD, and return to **Applications**.
    Confirm the application stage, history, current selection, and Applied
    resume statement are unchanged.

Use a disposable job when changing decisions or statuses. These controls write
persistent private data to `applications/career_hub.sqlite3`; search, filters,
scope, and drawer state remain browser-only.

### Cross-Browser Extension Package Check

Use a disposable Data Profile and synthetic HTTP pages. Build version `0.10.0`
with `python -m scripts.build_browser_extension` and follow
`docs/guides/browser_extension_compatibility.md` for exact installation steps.

1. Confirm the builder creates unpacked Chrome, Edge, Firefox, and Safari
   directories plus their archives, and refuses to overwrite them without
   `--overwrite`.
2. In Chrome 116+ and current Edge, load the matching generated directory.
   Capture a synthetic page, open its exact Job Feed review, and confirm
   **Application helper** opens the browser side panel.
3. In Firefox Desktop 140+, load the Firefox manifest from
   `about:debugging#/runtime/this-firefox`. Copy the popup's exact
   `moz-extension://...` origin, start Career Hub with `-ExtensionOrigin`, and
   repeat capture, Job Feed review, sidebar helper, reviewed fill, and attended
   submission tracking.
4. On macOS with Safari 17+, temporarily load the generated Safari directory.
   Copy the popup's current `safari-web-extension://...` origin and start Career
   Hub with `-ExtensionOrigin`. Repeat capture and Job Feed review; confirm the
   helper opens in a dedicated extension tab. Quit/reopen Safari, re-pair the
   changed origin if needed, and repeat the connection check.
5. On macOS, run `xcrun safari-web-extension-packager` against the generated
   Safari directory. Treat every converter warning as a release blocker until
   reviewed, then build the macOS container in Xcode and repeat the Safari flow.
6. In every browser, confirm only `http://127.0.0.1/*` is requested as a host,
   no page is scanned before an explicit click, and helper values remain absent
   from extension local/session storage after the helper closes.
7. Run the compatibility, worker, build, origin, and launcher test commands in
   `docs/guides/browser_extension_compatibility.md`. Run Mozilla `web-ext lint` against
   the Firefox directory and confirm there are no desktop errors or warnings.

Safari cannot be signed or declared release-ready from a non-macOS host. Record
the Safari/macOS version, Xcode version, converter output, and manual evidence
with the release candidate.

### Source-Backed Application Checklist Check

Use a disposable Data Profile and synthetic JDs only. Reload unpacked extension
version 0.10.0 after changing its popup or worker.

1. Capture one complete synthetic JD with a **How to apply** section containing
   an email destination, a requested portfolio or short video, **Write "I READ"
   in your cover letter**, a named external form, and an explicit deadline.
   Confirm the popup shows only the conditional extra-step count and no source
   wording.
2. Open the Discovery review. Confirm every item shows exact retained wording,
   line number, confidence, and extractor version. Capture the same text as a
   selection or link only and confirm it says **Full JD required** rather than
   **No extra steps**.
3. Save the complete JD and open **Job workspace**. Mark one item **Done**,
   reload, and confirm it remains Done. Change it to **Not applicable**, then
   back to **Pending**; all three actions must remain reversible and must not
   hide Save, Applied, posting, resume, or cover-letter actions.
4. Expand **Add a missed step from exact JD text**, open **Original job
   description**, select one exact sentence, choose its category, and add it.
   Confirm **Added by you** retains that exact wording. Empty selection,
   selection outside the source preview, and invented text must fail without a
   new item.
5. Modify the disposable retained JD, reanalyze it, and confirm earlier action
   state is labelled stale and the current item returns to Pending. No stale
   state may silently apply to the new source fingerprint.
6. Repeat with ordinary responsibilities, qualifications, benefits, EEO,
   accommodation, privacy/legal text, marketing, and generic **Apply now**.
   Confirm none becomes an extra application step. Absence means only that the
   narrow rules found none.
7. Run the isolated rendered check. It uses a temporary Data Home, covers
   desktop and 390-pixel layouts, persistence and exact-selection add, checks
   horizontal overflow, and fails on console/page errors:

   ```powershell
   node tests/browser_application_instructions_smoke.cjs
   ```

### Source-Backed Contact Candidate Check

Use a disposable Data Profile and synthetic JDs only. Reload unpacked extension
version 0.10.0 after changing its popup or worker.

1. Capture one complete JD containing an explicitly named recruiter or hiring
   manager, exact role and company wording, and one visibly associated email,
   phone, or professional-profile URL. Confirm the popup shows only the
   conditional contact-candidate count—never identity fields or evidence.
2. Open the Discovery review. Confirm the candidate shows only exact stated
   fields, channels, context, line range, and source wording. Select **Save
   contact** and confirm the dialog preserves immutable exact evidence while
   allowing name, role, company, context, and channels to be edited. Close it
   and confirm no contact was created.
3. Save the complete JD and open **Job workspace**. Confirm the same immutable
   candidate and exact evidence are projected there. Capture the text as a
   partial selection or link only and confirm it says **Full JD required**.
4. Repeat with `jobs@` or `careers@`. Confirm it is labelled an application
   channel with no invented person. Repeat with company stories, customer
   quotes, leadership biographies, generic signatures, copyright,
   privacy/legal, EEO, and accommodation text; confirm no person candidate is
   created.
5. Confirm one synthetic candidate. Confirm an exact email/profile match offers
   an explicit existing-contact link and a similar name/company offers a
   review-only choice rather than an automatic merge. Repeat the confirmation
   after the first response and confirm it returns the same contact. Change the
   retained JD before confirming an old dialog and confirm the stale candidate
   is rejected.
6. Open **Jobs > Contacts**. Search the ledger, edit stated facts, archive and
   restore the contact, follow its Job/Discovery link, then unlink it. Confirm
   unlinking preserves both the contact and its exact provenance. Promote,
   dismiss, restore, and reanalyze an unconfirmed Discovery and confirm none of
   those actions creates a contact or relationship.
7. Run the isolated rendered checks. They use temporary data, cover desktop and
   390-pixel layouts plus the packaged MV3 popup, check horizontal overflow,
   and fail on console/page errors:

   ```powershell
   node tests/browser_application_instructions_smoke.cjs
   node tests/browser_extension_smoke.cjs
   ```

   Review `contact-candidates-desktop.png`, `contact-candidates-mobile.png`,
   `contact-confirmation-desktop.png`, `contacts-ledger-desktop.png`,
   `contacts-ledger-mobile.png`, and `extension-popup-success.png` in the
   temporary artifact directories.

### Application Assistant Shelf And Copy Check

Use synthetic helper values only. Start Career Hub with `-EnableExtension`,
then reload unpacked extension version 0.10.0.

1. Under **Career Vault > Profile > Application helper**, enable a synthetic
   full name, email, and future-sponsorship answer. Save one synthetic active
   Resume Version in Resume Library. Confirm a disabled field and an
   enabled-but-empty field are both absent from the Profile projection.
2. Open the extension popup and choose **Application helper**. Confirm the
   popup remains compact and the browser opens its supported helper surface:
   Chrome/Edge side panel, Firefox sidebar, or Safari extension tab. Verify its
   desktop and 300-pixel layouts have no horizontal overflow.
3. Confirm the panel separates **Profile autofill**, **Resume shelf**, and
   **Reusable answers**, with accurate ready counts. Profile rows use canonical
   labels and masked or preview-safe values. The raw synthetic email must not
   appear in the DOM before Copy. Resume shelf exposes only the active display
   label, never file bytes or the private source filename.
4. Choose **Copy** for the full name and inspect the clipboard. Confirm only
   that one exact value was written and accessible success feedback names the
   field; there must be no **Copy all** action.
5. While the panel remains open, change one enabled Profile value or the
   allowlist. Choose Copy on the old row and confirm the panel refreshes, says
   nothing was copied, and requires another explicit click on the current row.
6. Open the synthetic Resume Version. Confirm a local Career Hub tab opens the
   exact `#resume-library/<resume_id>` route, focuses/highlights that record, and
   offers **Download**. Archive the record, refresh, and confirm it disappears
   from the shelf; the stale Open command must fail closed.
7. Close the panel and inspect local and session extension storage. Confirm no
   helper label, preview, or raw value is present. Existing attended-session
   metadata may remain in session storage but must contain no helper value.
8. Disable every Profile field and reopen the panel. Confirm the Profile empty
   state links to fixed settings while active Resume Versions remain available.
   Stop Career Hub, reopen the panel, and confirm the actionable offline state
   contains no previously loaded value or shelf label.
9. Run the isolated packaged-extension smoke:

   ```powershell
   node tests/browser_extension_smoke.cjs
   ```

   Review `application-helper-side-panel-desktop.png`,
   `application-helper-side-panel-compact.png`,
   `application-helper-side-panel-offline.png`, and
   `extension-popup-default.png` in the reported temporary artifact directory.

### Application Assistant Reusable-Answer Check

Use one disposable local Data Profile and a synthetic application form. Keep
the answer bank absent at the start; a fresh checkout or Data Profile must not
contain `career_vault/application_answers.yaml`.

1. Complete three short safe synthetic controls: one text input, one select,
   and one yes/no radio group. Also complete synthetic password, financial,
   demographic, eligibility/sponsorship, compensation, upload/consent,
   job-specific, and open-ended prompts.
2. Choose **Find completed answers**. Confirm only the three safe rows and their
   current values appear. No page scan occurs before this click, and no answer
   is saved automatically.
3. Choose **Save** for one row. Confirm only that exact normalized question and
   value are written to the private lazy answer bank, the row appears under
   **Saved Q&A**, and the extension stores no value. Run Find again and confirm
   the saved question is excluded while the other safe candidates remain.
4. Clear the original page control and choose **Review saved answers**. Confirm
   exact-question matching proposes only the supported empty control and shows
   no value in the mapping. Choose **Fill selected answers** and verify the
   original value and native input/change events return without a page click or
   submit.
5. Repeat after changing the value, question label, active URL, or local answer
   revision between review and action. Each one-shot attempt must fail with zero
   partial writes. Non-empty, ambiguous, and incompatible targets remain
   disabled.
6. Copy the saved answer explicitly, then Delete it through the confirmation.
   Confirm the row and local record disappear, and local/session extension
   storage contains neither question nor value.
7. Run `node tests/browser_extension_worker_harness.cjs` and the isolated
   packaged smoke. Review
   `application-helper-reusable-answer-complete.png`; the screenshot must show
   the saved local row and safety boundary without exposing any excluded value.

### Application Helper Reviewed-Prefill Check

Use only disposable Workday-like, Greenhouse-like, Lever-like, and plain HTML
forms with synthetic helper values. Reload unpacked extension version 0.10.0.

1. Enable synthetic full-name, split-name, address, email, phone, LinkedIn,
   authorization text, and eligibility boolean values.
2. With each disposable form active, choose **Review profile fields on page**.
   Confirm the review lists only a canonical field, page label, and empty
   state—never the Profile value—and that the injected target is the main frame
   only.
3. Confirm first/last-name splits are not mapped to Full name, a pre-populated
   email is disabled as non-empty, duplicate same-confidence LinkedIn inputs
   are disabled as ambiguous, and hidden/password/file, screening,
   demographic, unrelated free-text, and consent controls are absent. Confirm
   exact supported authorization textarea, address/country select, and yes/no
   eligibility radio controls are proposed with the correct kinds.
   Put more than 240 hidden framework inputs before a visible email and phone;
   both visible fields must still be reviewed. Add a newsletter form beside an
   application form and confirm the uniquely broader application form wins.
   With only Website enabled, a field labeled **LinkedIn profile or personal
   website** must not be misclassified as Website.
4. Deselect one safe proposal. Choose **Fill selected fields** and confirm only
   checked visible empty supported text/email/tel/url, textarea, select, and
   yes/no radio controls receive values and native input/change events. Confirm
   no checkbox, file, unrelated free-text, or non-empty control changes.
5. Review again, type into one selected field, then Fill. Confirm the entire
   attempt produces zero helper writes and asks for another review. Repeat
   after changing the active URL and after changing an allowed Profile value.
6. Review a plain form, replace its controls with an identical dynamic render,
   then Fill. Confirm the fresh controls are revalidated and filled. Alter one
   label/name instead and confirm the review fails closed.
7. Instrument Continue, Next, Submit, terms, and form-submit handlers. Confirm
   all counters remain zero through Review and Fill and no application status
   changes. Confirm helper values and review tokens are absent from local and
   session extension storage after the panel closes.
8. Explicitly enable synthetic US demographic values. Confirm each appears as
   a masked one-field Copy row, the Review button remains unavailable when only
   those fields are enabled, and matching page demographic controls are never
   proposed or changed.
9. Run `node tests/browser_extension_worker_harness.cjs` and the isolated
   packaged smoke:

   ```powershell
   node tests/browser_extension_smoke.cjs
   ```

   Review `application-helper-prefill-review.png` and
   `application-helper-prefill-complete.png` in the reported artifact
   directory. The review must remain legible at 300 pixels without horizontal
   page overflow or console errors.

### Exact-URL Submission Tracking Check

Use only a disposable synthetic job and a synthetic same-Origin form. Start
Career Hub with `-EnableExtension` and reload extension version 0.10.0 before
this check.

1. Keep one disposable Saved job with an exact source URL. Open that posting,
   open the extension, and choose **Track submission** without first visiting
   Applications. Confirm the stage becomes Preparing and the popup reports
   **Tracking on**. A URL with no saved match must show **Prepare this job
   first** and must not identify or change any application.
2. Give two disposable startable jobs the same canonical source URL. Confirm
   **Track submission** fails closed as ambiguous. Then use **Create tracking
   link** from one Applications drawer as the explicit fallback and confirm only
   that exact pending session can be claimed from the posting.
3. Trigger native validation failure on a required synthetic field. Confirm no
   submission confirmation appears. Complete a Continue/Next step that replaces
   the form DOM on the same Origin; confirm the session remains armed and the
   application remains Preparing.
4. Complete a final native form submit and render the synthetic success heading
   **Your application has been successfully submitted**. Repeat with a
   JavaScript-only `type=button` whose exact visible label is **Submit
   application**. Confirm each run creates one
   **Submission confirmations** row at the top of Applications with the
   exact company/role and asks **Mark this application as applied?**.
5. Reload Applications before answering. Confirm the review remains. Check both
   desktop and 640-pixel widths: the question and all three actions must remain
   visible without horizontal page overflow.
6. Choose **Not submitted** once and confirm Preparing remains unchanged. Start
   a fresh session, repeat the synthetic success, choose **Mark Applied**, and
   confirm exactly one Applied event is appended. Repeating the same signal or
   confirmation must not append another event.
7. In separate disposable runs, close the tab, navigate it to another Origin,
   reuse it for a different armed application, and let a session expire. Confirm
   each session shows a recoverable ended state and no application becomes
   Applied.
8. Inspect `applications/career_hub.sqlite3` only in the disposable profile.
   Confirm `application_sessions.session_token_sha256` contains a 64-character
   hash, not the capability returned to the extension, and session events contain
   only rule/timestamp identifiers—never form values, DOM/page text, resume
   contents, cookies, credentials, or screenshots.

## Tailor Resume Content Selection Browser Check

Use a disposable application at Preparing or a later status and current Career Vault data.

1. Open **Job Workspace** and confirm Application materials offers exactly two
   primary choices: **Tailor resume** and **Create cover letter**. Confirm there
   is no separate evidence-plan option.
2. Select **Tailor resume**. Confirm the Resume editor opens for the same company
   and role, with **Content and evidence** embedded as a collapsed first section.
   Expanding or collapsing it must not change saved state.
3. Confirm every experience defaults to Exclude. Use the Include/Exclude buttons;
   an exclusion reason is optional. For included experience, choose a 2-5 bullet
   target and at least that many owned proof points. Confirm selecting additional
   candidate proof points remains allowed.
4. Select at least one skill group and one or more canonical keywords, or
   disable **Technical skills** in Section order.
5. Confirm the Projects picker is hidden while Projects is excluded in Section
   order. Enable Projects, select a project, then disable Projects and confirm its
   project selections are cleared and the picker disappears. A selected project
   with no proof must remain listed under Readiness while the section is enabled.
6. Reorder sections with the up/down controls and choose one or two target pages.
7. Select **Save progress** while an included record lacks enough evidence. Confirm
   the save succeeds, Readiness lists the unresolved item, and reload restores it.
8. Before completing the evidence count, select **Use this selection**. Confirm the
   page scrolls to the affected experience, highlights it, focuses the first
   unselected proof, and shows the missing count on the card.
9. Confirm proof points that directly contain canonical JD technologies show
   **Suggested** labels without becoming selected automatically.
10. Complete every listed issue and select **Use this selection**. Confirm the
    internal status is `approved`, Readiness says **Ready to use**, the section
    collapses, and the tailored-resume wording is rebuilt from that selection.
11. Open the same plan in two tabs. Save tab one, then save tab two. Confirm the
   stale tab is rejected and must reload before writing.
12. After loading a draft, make a disposable Career Vault edit in another tab.
    Confirm approval rejects stale data until the current Vault is reviewed.
13. Return to Applications. Confirm the saved selection status and revision appear.
    Application stage and attached resume must remain unchanged.

This validates selection, truthfulness references, stale-write guards, and local
persistence. The Resume Plan remains an internal revisioned contract, not a
separate product choice. The check below validates application-only prose and exports.

## Application Materials Browser Check

Use a disposable application with a complete, approved Resume Plan.

1. In Job Workspace, confirm the user can either select an immutable Resume
   Version or choose **Tailor resume**.
2. Open the tailored resume. Confirm the header is prefilled, **Content and
   evidence** is available in the same workspace, and skills, education, record
   names, dates, and selected sections are read-only in the wording editor.
3. Edit only the professional summary and one bullet. Confirm evidence labels
   remain visible and Career Vault YAML is unchanged.
4. Save a blank required summary and confirm saving a draft is allowed but
   export remains blocked. Add a concise evidence-backed summary, save, and
   approve; confirm Readiness becomes ready.
5. Export DOCX, PDF, and TXT. Confirm each downloads locally. Open DOCX and
   verify single-column reading order, standard headings, 0.5-inch margins,
   ordinary bullets, empty header/footer, and no layout tables or graphics.
6. Change the Resume Plan or a referenced Vault record. Reload the material and
   confirm stale validation blocks saving/export until **Rebuild from current
   plan** is explicitly chosen.
7. Open the same draft in two tabs. Save one, then confirm the stale tab receives
   a revision conflict.
8. Open the cover-letter tab. Confirm company, role, contact header, salutation,
   evidence lead-in, reusable closing, sign-off, and credential are prefilled.
   In the evidence catalog, confirm each Experience or Project appears once and
   starts collapsed. Expand a group and confirm its proof points appear as nested
   bullets beneath that source. Add one proof and confirm the group stays open
   after rerender while its summary reports the selected count. Collapse it and
   confirm the proof points are hidden. Search for a proof claim and confirm only
   matching source groups appear and open automatically; searching must not
   duplicate the source group.
   Confirm there are no separate **Save draft** or **Approve** buttons. Add two
   distinctly labeled evidence points and export each format directly. Each
   export must first save the current fields, rerun validation, and only then
   download the file.
9. Open the DOCX or PDF and confirm the canonical order: two-row contact header,
   date, salutation, concise opening, lead-in, two to four labeled bullets,
   direct closing, sign-off, name, and credential line. Confirm a black divider
   spans the full content width under the name/email row. Add both LinkedIn and
   GitHub and confirm they remain separate clickable, underlined links on the
   second row, display without protocol or `www.`, use a tight `|` separator,
   and neither wraps nor shifts the location or centered phone. Confirm bullets
   are solid-circle real list items with hanging indents and regular-weight text.
10. Confirm neither editor changes application status, attaches an export, calls
   an AI provider, or records Applied.

DOCX structural checks are automated. Because LibreOffice is not installed in
the current runtime, visual pagination remains a manual check.

## Terra Real-Application Smoke Check

Use one real job and keep its records. This check validates the human-assisted
application loop; it is not authorization for broad product changes or automatic
submission.

1. Open a new Terra task at the repository and use
   `docs/guides/application_session_prompt.md`. Supply a complete JD or saved job id,
   the intended action, URL when known, and `Registry mode: exact-auto` or `off`.
2. Confirm the `career-hub-assess-job` repo skill starts with
   `python -m src.agent_workflow`, resolves the selected `main` profile, and does
   not preload a broad private-file inventory.
3. For an opinion-only role, run ephemeral `analyze` and confirm no source, job
   card, application row, or output is created. For Prepare, run `prepare` and
   confirm the exact JD and URL are stored once and stage becomes only
   `Preparing`. Repeat the command and expect `no_change_needed` with no duplicate.
4. Review the compact packet's current JD fingerprint, required/preferred rows,
   eligibility, direct evidence, taxonomy hints, evidence-not-found items, and
   manual checks. Confirm a requirement or registry label never becomes candidate
   evidence and the recommendation remains advisory.
5. In `exact-auto`, batch-check exact terms first. Confirm an already known name
   is reused, one unambiguous saved-JD technology may enter private custom
   vocabulary, and ambiguous prose remains an observation. Confirm the write
   receipt says candidate evidence was unchanged.
6. Ask whether the existing resume needs tailoring. Confirm
   `career-hub-tailor-resume` accepts `no_change_needed` with no edit quota. Any
   proposed delta must cite a requirement and direct evidence. A Resume Plan
   must not be described as a generated file.
7. Supply one exact employer text prompt and limit. Confirm
   `career-hub-write-application` returns copy-ready text inline by default,
   avoids invented facts, and writes a validated private draft only for a
   prepared application. Repeat the same draft and expect `no_change_needed`.
8. Confirm no Google Doc is created without an explicit request. When an
   artifact is requested, confirm the agent uses `material-context`,
   `save-material`, and `render-material` instead of assembling DOCX manually.
   Confirm draft creation does not upload, submit, or set `Applied`.
9. Before real submission, call `record-submission` without confirmation and
   confirm it fails safely. After the actual submission, use
   `--confirm-submitted` and verify the real Applied date and Resume Version when
   known; preserve unknown resume state rather than guessing.
10. Run `python -m src.agent_workflow audit --project-root . --changed --pretty`.
    `review_required` should prompt a semantic check, not force an edit; there
    must be no `outdated` or `broken` skill contract before handoff.

The browser still does not edit or generate application prose. The local agent
adapter now validates the private workbench file; the absence of a browser text
editor remains an intentional dogfood boundary.

## JD Intake Hardening Check

Use disposable data for any step that changes a JD, status, or resume selection.

1. Save one posting through **Save and digest** and confirm the immediate result contains the stored assessment and Saved ledger state. Use **Save as applied** for another posting and confirm it records Applied immediately.
2. Paste the exact same posting with the same idempotency action. Confirm it opens the existing record, creates no second source/card, and presents the already-captured result. Use the same text with a different posting URL or requisition ID and confirm it creates a distinct record.
3. For a posting with an ambiguous identity, correct company and title through **Edit details**, reanalyze, and confirm the corrections remain.
4. Mark separate disposable records **Preparing** and **Applied**. The Applied path must save with **Not recorded** selected when the actual resume is unknown. For a third record, set **Interview** directly; confirm an unknown submitted-resume snapshot appears and no Applied status is fabricated.
5. Upload a disposable resume, find it via the search field in the Resume picker, select it, and use **Record submitted resume**. Select another version and record it again; confirm both submitted-resume entries remain visible and the latest one opens.
6. With only the keyboard, tab to **Selected JD**, type a search term, use ArrowDown and End to move through options, and Escape to close the list. Repeat the search and Escape behavior in the Resume picker and application drawer.
7. Run `python -m src.intake_metrics --days 30` and `python -m src.application_history integrity`. Confirm metrics give counts plus p50/p95 only when eligible terminal observations exist; treat `null` as no sample, and confirm the integrity report has no invalid cards, orphaned ledger entries, or fingerprint mismatches.
8. Review the full definitions and operating guardrails in `docs/history/intake_hardening.md` before interpreting the numbers.


## Registry-backed Experience and Project Skills

1. Open Experience, choose an existing card, and confirm Technologies are
   rendered as removable registry pills rather than a multiline text field.
2. Type one exact canonical name or alias and one deliberately new name,
   separated by a comma. Confirm the exact match becomes a core/custom pill and
   the unknown name remains under **New custom skills on save** with a group.
3. Save. Confirm both pills remain, the new entry appears in Skill groups, and
   the Vault record contains canonical `competency_ids` plus compatible display
   names under `technologies`.
4. Repeat on a Project and test cancel: proposed vocabulary must not be created
   until the Project itself is saved.

## Private Documents Ledger

Use only synthetic files for this check.

1. Open Documents and add a small valid PDF, PNG, or JPEG with a category and
   optional issued/expiry dates. Confirm the detail panel opens the saved entry.
2. Search and filter it, edit only its metadata, then use **Open file**. Confirm
   the original bytes open in a new local browser tab.
3. Archive it with the detail action. Confirm the default list no longer shows
   it, the single archive-box toggle shows it, and pressing that same toggle
   again returns to active documents. Restore it from the archived view.
4. Open Profile and confirm the right-hand Documents summary reflects the
   active count and nearest recorded expiry without exposing file contents.
5. Inspect `documents/` in the selected Data Home. Confirm filenames are random
   IDs ending in `.chdoc`, and neither metadata nor source bytes are readable as
   plaintext. Confirm `key_state.json` contains only the algorithm, Data Home
   identity, and opaque key id—never key bytes. Do not move this test backup to
   another account/device and expect it to decrypt: the AES-GCM master key is
   intentionally held in Windows Credential Manager or the macOS
   data-protection Keychain.
6. Confirm the page has no per-view Refresh button; the compact refresh icon in
   the persistent bottom status bar reloads the current view.
7. Confirm an expiry before the issued date is rejected. The future-model
   projection is title-only, but remember that OS-backed encryption protects
   data at rest; it does not sandbox arbitrary code already running as the same
   user.

## Backup Restore Interruption Check

Use disposable synthetic homes and the test-only checkpoints; never interrupt a
restore of real data for acceptance testing.

1. Interrupt after the journal is published, after an original root moves, and
   after its staged replacement activates. Confirm normal Data Home inspection
   reports `DATA_HOME_RECOVERY_REQUIRED` in every case.
2. Run `python -m src.data_backup recover --data-root <synthetic-home>` and
   confirm every pre-restore root and marker byte is restored, including a root
   that did not exist before the restore.
3. Interrupt recovery after it preserves the activated candidate, run recovery
   again, and confirm the second run completes idempotently.
4. Confirm another `restore` refuses an active journal rather than rolling back
   a transaction that could still belong to a running process.

## Job Feed And Explicit Email Import

Use only the synthetic `.eml` fixtures under `tests/fixtures/email_alerts/` for
this check. Do not upload a private message during the acceptance run.

1. Open **Jobs > Job Feed**. Confirm the page has exactly three presentation
   modes: **Feed**, **Email**, and **Watchers**. Change the mode, reload, and
   confirm the browser-local preference is restored.
2. In **Email**, select `single_great_match_html.eml`,
   `plain_text_alert.eml`, `linkedin_digest_html.eml`, and
   `linkedin_rejection_with_suggestions.eml`, and
   `handshake_application_sent_with_suggestions.eml`. Confirm the UI shows the
   selected count and the limits: ten files, 10 MB each, and 25 MB total.
3. Import. Confirm each file has its own result and job candidates appear only in
   the shared Feed, and neither Email nor Watchers renders a second job list.
4. Reimport the same three files. Confirm the result reports them as already
   known, the Feed still renders one row per posting, and no `.eml` file exists
   under the selected Data Home.
5. Confirm the LinkedIn fixture creates exactly six rows, not fifteen. Each row
   shows a plain **LinkedIn** source label with **Email** as its transport, a link
   indicator, separate company/location fields, and a query-free posting URL.
   Confirm `Manage job alerts`, `Install LinkedIn Widgets`, and the alert-search
   heading do not appear in Feed.
6. Import one malformed synthetic email together with one valid fixture.
   Confirm the malformed result fails visibly while the valid sibling remains
   imported.
7. Confirm the rejection fixture adds exactly its two recommended jobs to the
   active Feed, not the `Data Engineer` outcome card. Under **Applications >
   Email tracking > Application updates**, confirm
   `Data Engineer · Synthetic Commerce` shows
   **Rejected** and **Not linked to a saved application**. No saved job or
   application should be created. Reimport the fixture and confirm there is
   still one status update and no duplicate status event.
8. Return to **Feed**. Search by title/company/location; filter by Email,
   decision queue, and observed period; change first/last-seen or
   decision-priority sorting. Confirm filters and pagination are applied across
   the full result set rather than only the visible page. Change page size and
   move between pages; the selected detail must always belong to the visible
   page. Confirm rows show every retained producer badge, first/last seen, and
   the decision queue without a numeric fit score.
9. Open the `great match` candidate. Confirm **Recommended by source** repeats
   only the source wording and explicitly says it is not a Career Hub fit
   assessment. Confirm **Full JD still needed** remains visible.
10. Confirm this link-only candidate has neither **Save job** nor **Record as
   applied**. Open the posting in a normal browser tab and confirm the action
   does not fetch the page, create `jobs/incoming` or `jobs/normalized`, create
   an application, change application status, or claim a complete assessment.
11. Open a synthetic capture whose extracted role is intentionally generic.
    Confirm **Job details** labels title, company, and location as **Extracted**,
    and source URL, source text, provider, observation time, and posting identity
    have no edit controls. Choose **Edit details**, correct all three values, and
    save. Confirm both the detail header and Feed row use the corrected effective
    values while the captured values remain visible and unchanged.
12. Reload the page and repeat the same capture with richer extracted content.
    Confirm **Edited by you** values survive. Choose **Use extracted** for one
    field, save, and confirm only that field returns to the latest extracted
    value. In a second tab, attempt a different correction from the stale
    override revision and confirm it is rejected. Corrections must not create a
    Job, Application, assessment, skill, or contact.
13. Start Career Hub with `-EnableExtension`, reload the
    unpacked extension, and capture a synthetic page with one exact citizenship
    conflict plus one deliberately unknown phrase. Confirm the popup shows
    **Clear conflict found**, the exact JD line, and the compared profile fact,
    but no fit percentage and no disabled action. Confirm the unknown phrase is
    counted for review and is absent from both custom vocabulary and candidate
    evidence. Capture a selection and a link; both must show **Assessment
    unavailable** and state that no gaps were inferred. Repeat the page capture
    and confirm one Discovery has both observations and one current preview.
    Change one relevant sandbox Career Vault fact, repeat, and confirm the
    preview refreshes. Closing any popup must leave application status unchanged.
14. From the successful page-capture popup, choose **Review in Career Hub**.
   Confirm it opens the exact `#job-feed/<discovery_id>` route and shows retained
   text, capture scope, identity, provenance, and assessment context before the
   verification controls. **Save job** and **Record as applied** must be absent.
   Choose **Review later** and confirm the item remains in Feed with no Job or
   Application. Then choose **Confirm complete JD**, accept its prompt, and
   confirm the item becomes **Ready** while still creating no Job/Application.
15. Choose **Save job**, accept its separate prompt, and confirm Career Hub
    creates one saved JD, runs the existing deterministic assessment, and opens
    that exact record in Job Workspace. A repeated server request must reuse the
    same job. Repeat with a second verified discovery and choose **Record as
    applied**; confirm the prompt says to proceed only after actual submission
    and the exact Job Workspace opens with status **Applied**. Neither action is
    available for link-only, unverified, stale, or dismissed content.
16. Shortlist, move back to Inbox, and dismiss the candidate. Confirm the
    archive-box icon reveals dismissed items and the same control restores the
    active view. In a second tab, attempt a stale lifecycle edit and confirm it
    is rejected rather than overwriting the newer revision.
16a. Open synthetic Indeed- and LinkedIn-shaped split panes containing a selected
    result card, one detail pane, unrelated results, and recommendations. **Save
    page** must retain the detail description while excluding unrelated card and
    recommendation markers. Repeat with a generic split pane whose provider
    selectors do not match; title-anchored ranking must isolate its detail pane.
    A Handshake-shaped search shell without a complete detail description must
    fail with **One job description wasn't clear** and create no Feed item. For
    the accepted generic fixture, confirm the complete JD and save it; the
    resulting immutable incoming source must still exclude every unrelated
    result-card marker.
17. Open **Watchers**. Confirm it is a bounded **Coming soon** explanation of
    website registry, layered retrieval, and run history, with no fake working
    controls or parallel list.
18. Repeat at a narrow browser width. Confirm the list becomes a compact mobile
    row layout and the selected detail moves below it without horizontal page
    overflow.

### Mixed Handshake application message

Use `handshake_application_sent_with_suggestions.eml` and a disposable saved
application whose company is `Synthetic AI`.

1. Import the fixture. Confirm it produces one unlinked **Applied** update under
   **Applications > Email tracking > Application updates** and three separate
   link-only Handshake recommendations in Feed.
2. Confirm the update is titled **Application submission**, names only the
   company, and does not create or guess a Job, role, application, or contact.
   `View application`, preferences, and unsubscribe links must create no cards.
3. Expand **Link to an existing application**, select the disposable
   application, and link the update. Confirm the existing application moves to
   **Applied** and no second application is created.
4. Repeat the link request from a stale tab. Confirm the event revision rejects
   it rather than repeating the transition. An existing **Interview** or
   **Offer** application must remain at its later status.
5. Reimport the fixture. Confirm there is still one submission event, one
   Applied history transition, and one Feed item per recommended role.
6. In live-sync sender policy, choose **Application updates only** and confirm
   the same message creates the event without Feed recommendations. Choose
   **Updates + included job suggestions** to enable both typed outputs.
7. Sync an unrelated generic message from the exact Handshake tracking sender.
   Confirm it creates no job, contact, or application event.

### Dedicated Gmail connector (private dogfood)

Do this only with a separate personal Gmail account used exclusively for job
alerts. Never paste an App Password into a task, terminal command, screenshot,
log, issue, or chat. The live check is optional until the account is ready;
automated tests use synthetic mailboxes.

1. On the dedicated Google account, enable two-step verification, open
   [Google App Passwords](https://myaccount.google.com/apppasswords), create one
   named `Career Hub`, and keep the generated value in that local browser
   session only. If the page is unavailable, stop and keep using `.eml` import.
2. Open **Jobs > Job Feed > Email**. Enter the dedicated Gmail address, paste
   the generated App Password into the local field, and add at least one exact
   `From` address under **Job opportunity senders**. Add confirmation,
   interview, rejection, or status-only addresses under **Application tracking
   senders**. Add a mixed sender, such as one message containing both a status
   and recommendations, to both lists. Leave **Sync while Career Hub is open**
   off for the first check. Save. Confirm the password field clears and no
   password appears in the selected Data Home, browser storage, terminal, or
   response payload. Confirm the success message says the connection was saved
   locally and that **Sync now** is the separate Gmail credential test.
3. Click **Sync now**. Confirm Gmail messages remain unread/unchanged and are
   not moved or deleted. Confirm only allowed senders produce Feed items. A new
   sender may appear in **Needs approval**, but its body must not be imported
   until **Allow & sync** is chosen. A sender or Subject with deterministic
   job-alert wording may show **Suggested job alert** and a short reason. The
   exact `handshake@notifications.joinhandshake.com` sender or deterministic
   application-status wording may show **Suggested application tracking**.
   Select a route and click **Allow & sync**; confirm the reusable rotating
   hourglass placeholder remains visible until the operation completes. A
   tracking-only sender must create no Feed item. A service/security Subject
   must suppress job/recurrence recommendations. Confirm the UI,
   API response, SQLite receipt, and diagnostics do not expose the raw Subject.
   In a synthetic two-message batch, force the first allowed message import to
   fail and let the second succeed. Confirm the second message is still fetched
   and imported in the same run. The UI must say **Sync incomplete**, retain
   **Connected · import issues**, show imported-email and created-card counts,
   and link to the same Diagnostics run. Its terminal `email_sync_run` event
   must be `success_with_warnings` with `EMAIL_SYNC_MESSAGE_RETRY`, safe numeric
   counters, and aggregate `failure_code_counts`; it must contain no sender,
   Subject, body, or credential. After the retry limit, confirm the code changes
   to `EMAIL_SYNC_MESSAGE_SKIPPED` without relabeling Gmail as disconnected.
   On Windows, also launch a development install once from a package-virtualized
   host. Confirm the sidecar uses the redirected write-probe parent as its Data
   Home, starts successfully, and reopens the same data without copying or
   moving it.
4. Click **Sync now** again. Confirm the result imports no duplicate email or
   duplicate Feed row. Use **Rescan recent email**, sync again, and confirm it
   rechecks the recent 14-day window while receipt and Discovery deduplication
   still preserve one opportunity. In the synthetic
   recurrence fixture, confirm a neutral sender becomes **Recurring — review**
   only at four messages on three distinct dates spanning at least 14 days;
   the badge must not allow the sender automatically.
5. Enable **Sync while Career Hub is open**, keep the 30-minute default, and
   save. Reload Career Hub: one startup check should occur. Keep it open through
   one interval and confirm a second bounded check occurs without creating
   duplicates. In the settings form, make an unsaved sender, schedule, or App
   Password edit and run a manual sync; confirm the server snapshot refreshes
   without clearing any unsaved field. A transient failure should show **Needs
   attention** and defer automatic retries; manual **Sync now** must remain
   available.
6. Force one synthetic credential-store save to exceed ten seconds. Confirm the
   request returns `EMAIL_CONNECTION_SAVE_TIMEOUT`, the form becomes interactive
   again, and **Open Diagnostics** selects the same run ID. The final
   `email_connection_run` event must name the last safe checkpoint without an
   account, sender, Subject, or credential value. Separately reject one
   synthetic mailbox login and confirm `EMAIL_SYNC_AUTH_FAILED` appears in both
   the UI and a terminal `email_sync_run` event with the same run ID.
   Separately make the local service unreachable during Save settings. Confirm
   the form recovers after its browser deadline and Diagnostics shows an
   `email_connection_client` failure with outcome `unconfirmed`; the event must
   contain no account, sender, Subject, or credential value.
7. Restart the packaged app, open one Job Feed discovery that has no cached
   preview, then return to Email and save the connection settings. Confirm the
   Jobs view, `/api/health`, and Diagnostics remain responsive. This verifies
   that spaCy and its native dependencies were initialized on the sidecar owner
   thread before any threaded HTTP request was accepted.
8. Open the same Data Home in a second app tab. Confirm only one tab owns the
   startup/interval schedule. Trigger both manual sync buttons and confirm at
   most one run proceeds while the other reports an already-running conflict
   without data corruption. In **Applications > Email updates**, confirm
   unresolved items appear before resolved history and **Load more email
   updates** pages through every retained outcome without duplicates.
9. Uncheck the schedule and save. Reload and confirm no startup sync occurs.
   Choose **Disconnect** and confirm local Feed items/receipts remain while the
   Credential Manager secret is removed. Then use **Forget email setup** in a
   disposable profile: confirm it removes the mailbox address, senders, sync
   position, receipts, and credential; it must retain existing Feed items,
   saved jobs, and applications. Confirm a different mailbox can then be
   configured only by completing setup again. Revoke the App Password in Google
   as the authoritative remote disconnect.

After one allowed LinkedIn digest arrives, run **Sync now** twice. Confirm the
first run creates one Feed row per numeric posting id, every row shows
**LinkedIn** as provider and **Email** as transport, stored posting URLs contain
no query string, and navigation/service links do not appear. Confirm the second
run creates no duplicate Feed row. Do not copy the private message body into a
test, log, issue, or repository file.

### Optional Windows Task Scheduler run

Configure the mailbox in the UI first and explicitly enable its schedule. Under
the same Windows account that owns the Credential Manager entry, verify one
non-interactive development run from the Career Hub resource directory:

```powershell
python -m src.email_sync sync-once --data-root "C:\path\to\CareerHubData\main" --channel stable --pretty
```

The command must emit sanitized JSON and exit; it must not prompt, open a
browser, or accept a secret argument. In Task Scheduler, create one task whose
action runs the same Python executable and arguments, whose **Start in** path is
the Career Hub resource directory, and whose trigger is the user's preferred
time (for example 06:00). Do not create a second overlapping task.

Repeat once with a missing or inaccessible disposable path. Confirm the command
returns `EMAIL_SYNC_DATA_HOME_UNAVAILABLE` as sanitized JSON, without a Python
traceback or the inaccessible path in the output.

To disable it, first uncheck the in-app schedule and save, then disable or
delete the Windows task. Before uninstalling, also choose **Disconnect** and
revoke the Google App Password. Uninstalling Career Hub does not silently
delete a user-created Windows task or remote Google credential. A missed
sleeping/offline run is intentionally recovered by the next enabled startup
sync; no tray daemon is required.

### Email hardening closeout

Run the bounded parser and repair regressions with synthetic data only:

```powershell
python -m unittest tests.test_email_alerts tests.test_linkedin_discovery_repair -v
```

Confirm the exact 500-link boundary succeeds, 501 links fail with
`EMAIL_LINK_LIMIT_EXCEEDED`, repeated LinkedIn card suffixes fail closed, a
post-preview discovery edit survives an aborted repair, and a sole retained
source remains attached after duplicate repair.

## Packaged Sidecar Smoke

Run this only against disposable synthetic data. Build on the target operating
system; never copy a Linux result and label it a Windows or macOS build.

1. Install `requirements.txt` and `packaging/requirements-build.txt` with
   `--require-hashes` in a fresh virtual environment.
2. From a clean checkout, run `packaging/scripts/build_sidecar.ps1 onedir` on
   Windows x64 or `packaging/scripts/build_sidecar.sh onedir` on macOS arm64.
3. Confirm `sidecar-manifest.json` and `sidecar-manifest.sha256` exist beside
   the executable, every path is relative, the version equals `VERSION`, and no
   private/developer path appears.
4. Confirm `.generated/career-hub` and the packaged `resources/career-hub`
   destination each contain exactly `build-metadata.json`. In a disposable
   checkout, add a sibling sentinel and confirm both preparation and direct
   Forge package preflight fail without deleting the sentinel. Also confirm a
   linked `.generated` parent or `career-hub` stage is rejected.
5. Confirm the automatic smoke reaches readiness, health, session bootstrap,
   and an empty Job Feed without the virtual environment, then shuts down with
   no retained sidecar process.
6. Temporarily omit one declared static/catalog/font resource in a disposable
   checkout and confirm the build or byte-verifier fails before an artifact can
   be accepted. Restore the checkout; never perform this step against private
   data or a signed artifact.
7. On each release target, build twice and compare manifests, run ten cold and
   ten warm samples, and complete malware or Gatekeeper/notarization checks.
   Record any symlink, traceback, or `extraResource` issue; do not switch to
   `onefile` or relax a budget inside the same run.

The Linux three-sample comparison in
`docs/history/benchmarks/side220_linux_exploratory_comparison.md` is useful development
evidence only. It selected `onedir` for integration because `onefile` added more
than five seconds plus temporary extraction.

## Packaged Electron Lifecycle And Offline Smoke

Run this with synthetic data on each native release target: Windows x64 and
macOS arm64. A Linux run is useful during development but cannot satisfy either
target row. Build the native sidecar first, then from `desktop/` run `npm ci`,
`node node_modules/electron/install.js`, `npm test`, `npm run check`, and the
appropriate `npm run package:<channel>`. Electron 42+ no longer downloads its
binary during npm installation; direct-executable rendered smoke needs this
explicit pinned-runtime initialization before it hashes or launches the binary.
Verify the fuse readback and ASAR/resource manifest before launching.

1. Launch with a fresh OS-account-scoped temporary app-data location. Confirm
   one main owner starts exactly one packaged sidecar, the existing Career Hub
   UI becomes usable without installed Python or Node, and no private or
   developer path appears in process arguments, URLs, console output, or the
   package manifest.
2. Start a second instance simultaneously and again after readiness, passing a
   disposable URL, path, and command-looking argument. Confirm the original
   window alone is focused/restored and no argument is acted upon, no second
   sidecar starts, and only one SQLite writer owns the selected channel/data
   identity. Repeat with distinct dev and stable identities and confirm their
   data roots do not overlap.
3. Kill only the retained synthetic sidecar. Confirm the main window shows a
   local-service error—not **Offline**—and does not restart automatically. Use
   **Retry startup** once and confirm the dead handle was cleared before one
   fresh child starts and the existing data reloads.
   Separately start from a disposable format-v1 Data Home, including one that
   predates Documents and has no `documents` directory. Confirm startup shows
   the actionable migration state rather than a generic sidecar wrapper code.
   Choose **Review update** and verify that only versions, document count, and
   encrypted byte count appear. Choose **Update workspace**, confirm a verified
   recovery directory is retained, format v2 opens, and no renderer-supplied
   path or plan id is accepted. Interrupt one synthetic migration after its
   durable journal is written and confirm normal startup stops for explicit
   recovery instead of silently rolling forward.
4. On Windows, close the last window and confirm the versioned shutdown runs,
   the exact sidecar exits within the bound, and no Career Hub process remains.
   Also test Windows shutdown/logout, where Electron may not emit
   `before-quit`; confirm parent-pipe closure still ends the sidecar.
   In a disposable instrumented build, make exact-child termination exceed its
   bound. Confirm Electron remains open with a sanitized error, retains the
   exact child as `stopping`, refuses startup/retry, and completes a later Quit
   only after that child has emitted `exit`. At no point may a second sidecar
   start.
5. On macOS, close the red window and confirm the app remains active with one
   sidecar and no tray/background polling. Activate the Dock icon and confirm
   exactly one new window opens against that same child. Then use Cmd-Q and
   confirm the bounded shutdown leaves no Career Hub process. Repeat after a
   forced renderer close and after a sidecar crash.
6. Start while the network interface is disabled. Confirm the local app still
   reaches Jobs and other local views, the accessible banner says **Offline —
   local features remain available. Internet actions will wait for a
   connection.**, and external/network-only actions are disabled without a
   remote reachability beacon.
7. While online, begin disposable Gmail sync and public-JD resolution cases,
   then cut the interface. Confirm classified transport failures explain that
   the action needs a connection without mislabelling auth, TLS, generic API,
   or sidecar failures as offline. Local editing, documents, export, backup,
   and manual `.eml` import remain usable.
8. Restore the interface. Confirm the banner says **Connection available —
   retry internet actions when ready.** and disappears after its bounded
   announcement. Confirm no sync or resolution replays automatically; invoke
   the action explicitly and verify exactly one request.
9. Cleanly quit, relaunch, and verify data persistence. Repeat with spaces,
   non-ASCII paths, and macOS Unicode-normalization collision fixtures. Inspect
   for orphan sidecars after normal close, parent loss, crash/retry, and failed
   startup.
   On Windows, separately bootstrap a safely pre-created markerless empty root
   and an allowlisted markerless partial root. Confirm the process-token SID and
   `SYSTEM` ACL is applied before the first candidate-content write. Give one
   existing partial child a protected broad ACL and confirm recursive reset plus
   read-back leaves only inherited current-SID and `SYSTEM` access. Confirm the
   operation is retried after an injected ACL failure; an initialized
   marker-bearing root must not be recursively re-ACL'd.
10. Before distribution, complete the native toolchain, real Credential
    Manager/Keychain/ACL, installer, Authenticode or Developer ID hardened-
    runtime/notarization/stapling, Gatekeeper/SmartScreen, migration, rollback,
    and clean-machine gates in `docs/reference/desktop_release_gates.md`. Never promote a
    Linux, unsigned, unnotarized, or cross-built result based on this smoke.

## Review Handoff

Do not clear or rewrite `career_vault/logs/intake.jsonl` before review. In a later Codex task, ask to inspect the latest Career Vault intake run or provide a specific `run_id`. Codex can read the local log and correlate errors, durations, counts, and warnings without requiring pasted terminal output.
