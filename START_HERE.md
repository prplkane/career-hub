# Career Hub: Start Here

Career Hub is a local-first desktop app for a human-assisted job search. It can manage
Career Vault facts, digest saved job descriptions, review deterministic matches,
build an evidence-backed tailored resume, attach the resume actually used, and track
application status. It also provides constrained, evidence-backed resume and
cover-letter editors with local DOCX, PDF, and TXT export. It does not call an
AI API or submit applications.

## Start The App

The Windows desktop application now uses the native Go backend. Launch the
installed Career Hub application, or use the development launcher after the
[local setup](docs/guides/local_development.md):

```powershell
.\scripts\start_career_hub.ps1
```

The desktop owns the local server and stops it when the window closes. The
ordinary development Home is `%LOCALAPPDATA%\CareerHub\dev`; the stable app
uses `stable`. The sidebar shows the active path.
The optional `-GoPreview` launch keeps its separate `dev-go-preview` Home.

Homes created by earlier releases are not opened implicitly. Back up existing
data and use the explicit supported migration/restore controls; document
conversion and recovery require confirmation. Never run another data-editing
application against the same Home during maintenance.

The browser extension is turned on in **Settings → Browser extension** and the
choice persists across restarts. For development, `-EnableExtension` admits the
bundled extension for one launch and `-ExtensionOrigin` one explicit supported
extension origin. Pairing is always required; ordinary startup uses an
ephemeral loopback port. See [local development](docs/guides/local_development.md).

The Settings panel controls Theme and Language. Localization changes display
only; records, identifiers and API values retain their stored form. Settings
also provides the verified native agent client path and current service address.
Refresh that connection after every service restart.

## Open A Job Posting From The Feed

In **Jobs > Job Feed**, use the prominent **Open posting** link to open the
retained source URL. A link-only Feed item is a gateway, not a saved job or an
application: capture the complete JD with the attended browser extension,
review the retained text, and choose **Confirm complete JD** before **Save job**
or **Record as applied** becomes available.

The guarded local public-link resolver remains a tested backend contract, but
it is not offered as a Job Feed control. Career Hub never logs in, searches for
a replacement posting, writes Skills, creates an Application, or marks content
complete merely because the source link was opened.

## Resume Entry: Current And Planned

**Quick start > Add resume** offers exactly two implemented intents:

- **Update Career Vault** keeps the existing extraction, manual review, and
  explicit-approval flow.
- **Save for applications** stores an immutable Resume Version without changing
  Career Vault or creating intake artifacts.

Assessment is not a third path. The searchable application picker still lists
active Resume Versions in **Applications**. After a Vault approval succeeds,
**Keep for applications** can explicitly retain that approved source as a
Resume Version. A separate confirmed action can then remove only the verified
original import file; the Vault, review, and approval audit remain. **Add
resume** shows only unfinished imports and opens Review contextually, while
**Resumes > Resume library** remains the application-material library.

TXT and DOCX versions in Resume Library can start a fresh deterministic
**Digest** without another upload. Each repeat creates a new review run and
never overwrites earlier review or approved Vault data. The library keeps its
card layout and can filter by digest state or sort by added date, name, or
digest state. PDF, DOC, and RTF remain application materials but require a
future parser before they can be digested.

Resume Library management, guarded deletion of unused Resume Versions, and
guarded approved-import cleanup are performed locally.

For one prepared job, Job Workspace may also open **Tailor resume** or
**Create cover letter**. Tailor resume contains its Career Vault evidence and
section selection in the same workspace as application-only wording and export.
These drafts reuse revisioned source contracts and Career Vault facts without
changing either source. Formatting is
deterministic rather than a browser word processor. See
[`docs/guides/application_materials.md`](docs/guides/application_materials.md).

## Skills Registry

Resume and JD intake share one exact-match Skills Registry. Core groups and
skills ship with Career Hub; custom groups and skills stay in the private data
home. Use the Skills page to manage groups and standalone skills; comma-separated
names become individually validated pills and are saved in one registry update.
Aliases remain available when adding one canonical skill. In Resume
Digest, Experience, Project, and Certification cards show recognized skills as
pills; an explicitly entered unknown label remains a draft proposal and becomes
custom only if that card and its Skills field are finally approved. The safe
fallback is **My custom skills**, or the reviewer may select/create another
group. Each skill belongs to one primary group. The browser never turns unknown
JD or resume prose into vocabulary automatically. A real application task may
use the explicitly enabled `exact-auto` agent mode for one exact, unambiguous
technology from a saved JD; that writes private vocabulary only. A recognized
term is never proof of candidate experience.

For a bounded agent check before a vocabulary decision, copy the native client
path and connection file from **Settings > Local agent connection** into `$agent`
and `$connection`, then request the saved JD context:

```powershell
& $agent context --connection $connection --job-id '<saved-job-id>' --purpose assessment
```

The bounded JSON packet includes source-backed requirements and registry
context. Reading context does not create skills or candidate evidence. Check
both catalogs and exact JD wording before any explicit vocabulary write.

For an end-to-end agent session, use the compact adapter instead of manually
opening every artifact:

```powershell
& $agent --help
```

Its task-specific commands, no-op behavior, submission guard, repo skills, and
maintenance audit are documented in
[`docs/guides/agent_workflows.md`](docs/guides/agent_workflows.md).

Custom skill detail records the explicit creation channel when known. Older
private terms remain valid without backfill. Application updates never rewrite
private taxonomy to resolve a new core collision; the private identity wins in
the effective local registry, while new conflicting user edits remain blocked.

Archiving a custom group warns about and transactionally archives its active skills;
restoring the group restores only the skills affected by that cascade. Existing
evidence references remain historical, visible, and editable, while archived
skills are excluded from new intake, new evidence selection, and matching.

Existing Career Vault records can be linked to canonical skill ids through a
separate reviewed migration. Its preview is read-only; confirmed apply creates
a recovery backup, preserves every legacy technology string, and records only
exact-name or approved-alias references.

The read-only Candidate Evidence Graph and evidence analytics projections
rebuild from canonical Vault records on demand,
separate linked-project evidence from independent projects, merge overlapping
experience months, and never read JD demand or create an overall candidate
score. The unified **Skills** page exposes the same projection under **All
skills**, with group/source/evidence/domain/kind filters, aliases, evidence
counts, last-used dates, and links back to canonical Experience, Project, and
Certification records. Its **Groups** mode manages the same shared vocabulary,
so evidence inspection no longer requires a separate navigation destination.

Saved JD demand remains a separate read-only projection. It counts required
and preferred competencies only for records already present in the application
ledger, applies an optional captured-date period, and deduplicates exact JD
fingerprints. The Coverage Matrix joins demand to Candidate Evidence with
`supported`, `thin_evidence`, `evidence_not_found`, and `not_requested`; it
never infers that a candidate does not know a competency and never emits a
composite score. **Jobs > Demand & coverage** presents that comparison as an
evidence-coverage band, compact demand insights, and a searchable detailed
list over distinct saved jobs. Selecting a skill opens read-only demand and job
context before the explicit link to its existing Skills evidence view; the page
does not edit vocabulary or Career Vault records.

## Daily Application Loop

1. Confirm the relevant facts in **Career Vault**. Under Profile, keep the
   private eligibility facts current and choose which of the three exact
   conflict classes may be labelled clear; unknown facts and disabled classes
   stay Review. The **Application helper** configuration is an empty-by-default
   privacy allowlist. Enabling a field makes it available to the extension's
   separate application assistant; every one-field Copy rechecks the current
   local projection. **Review profile fields** inspects only supported visible
   empty controls after your click, shows proposed mappings, and fills only
   selected rows after another current-state check. The same attended panel
   opens exact active Resume Versions and can explicitly learn/reuse bounded
   safe short answers by exact question. Sensitive, eligibility, job-specific,
   and open-ended answers remain manual, and the helper never submits the form.
   Add missing explicit vocabulary through the Skills Registry Quick Action
   only when needed.
2. Paste the complete posting and URL into **Quick start > Add job**, or choose
   **Prepare application** in the browser extension. The extension retains the
   current bounded page capture, opens its exact local review, and requires the
   complete-JD confirmation before it can create a Preparing application.
3. Choose **Save and digest** for a Saved record, or **Save as applied** when logging an application already sent. Both create the ledger record and store the deterministic assessment before returning you to **Job workspace**.
4. Review the digest and assessment when useful. Every conclusion can be reviewed; correct a saved JD fact in place, confirm it as resolved, or proceed with an accepted risk. Findings are advisory and never block an application. **Incomplete requirement basis** means some headings were not recognized; compare their quoted lines with the original posting before relying on the assessment. **No requirement basis** means no requirement section was recognized. Re-analyze older saved jobs to refresh these warnings.
5. In **Job workspace**, review the exact-source **Application checklist**. Mark
   an item Pending, Done, or Not applicable; if a narrow rule missed something,
   select its exact wording in **Original job description** and add it. A zero
   count is not a guarantee, changed JD content makes prior actions stale, and
   the checklist never blocks continuing. Review any source-backed contact
   candidates beside it: identity fields, channels, and context are exact JD
   evidence only. **Save contact** opens a non-mutating confirmation where you
   can edit stated fields and explicitly create or link an identity; confirmed
   records appear under **Jobs > Contacts**. The extension exposes only a
   conditional count and cannot save contacts. Contacts are optional: an
   application remains one saved JD with zero or more explicit contact links.
   Exact email/profile matches reuse the existing identity only after the user
   confirms the link; similar names never populate the application.
6. Select an existing resume with the searchable picker or upload and select a
   new one. No selection is the implicit default; **Clear selection** appears
   only after a version is selected.
7. Open **Tailor resume** and expand **Content and evidence** when the reusable
   resume needs job-specific work. Choose source-backed sections and proof, use
   that selection, then edit only the application summary and bullets. Preview
   when useful and export DOCX, PDF, or TXT. Alternatively, keep the immutable
   Resume Version selected in step 5 when it is already strong enough.
8. For an agent-assisted application, start from
   [`docs/guides/application_session_prompt.md`](docs/guides/application_session_prompt.md).
   The prompt routes the agent through compact repo skills and the local
   native agent client. Preserve each exact employer question and
   limit. Use the structured browser editor for a cover-letter document and the
   validated private text workbench for screening answers, recruiter messages,
   and follow-ups. See
   [`docs/guides/application_materials.md`](docs/guides/application_materials.md) and
   [`docs/guides/application_text_workbench.md`](docs/guides/application_text_workbench.md).
9. Set **Applied** when the submission happens, or choose **Track submission**
   from the exact posting. The extension starts only when one startable saved
   application matches the canonical URL; **Create tracking link** in the
   Applications drawer is an optional fallback. It may then surface a
   submission confirmation, but it never submits or changes the stage until you
   choose **Mark Applied**. The ledger preserves the initial
   resume state without blocking an unknown resume. If tracking starts only
   after an **Interview** or **Offer**, it records the same truthful
   unknown-resume snapshot without inventing an Applied date. Select the actual
   resume later and use **Record submitted resume**; corrections remain in its
   history. Update the same status and dated notes as it progresses.

10. Inspect **Diagnostics** for recent intake outcomes and failure codes. Use
   them as operational signals, not suitability scores.

While using the app, retain the original JD and record concrete failures: job id,
screen, expected result, actual result, and any displayed error code. Also
surface exact vocabulary candidates, parser misses, candidate-fact proposals,
and reusable-answer candidates without promoting them automatically. Do not add
invented Career Vault facts merely to improve a match.
