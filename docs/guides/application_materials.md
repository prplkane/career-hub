# Application Materials

Career Hub has one job-scoped, local-first workspace for a tailored resume and
cover letter. The workspace is intentionally a constrained content editor, not
a general-purpose word processor. Renderers own layout; the user and an agent
edit typed fields.

## Product Flow

From Job Workspace, a user may either select an immutable Resume Version or
open **Tailor resume**. Evidence and section selection is the first collapsible
part of that same workspace, not a second product option. Internally the
revisioned Resume Plan remains the source-valid selection contract; the user
experiences one continuous flow from Career Vault selection to application-only
wording and export. Cover letters start from a reusable professional template
with current company, role, profile, and education facts already filled.

Both editors provide a collapsed plain-text preview. Exports are explicit and
available as DOCX, PDF, or UTF-8 TXT. In the cover-letter editor, Export is the
commit boundary: it saves the current typed draft, reruns source/evidence
validation, and creates the file only when the checks pass. Nothing in this
workflow uploads a file, submits an application, or records `Applied`.

## Ownership Boundary

For resumes, code owns:

- job and Career Vault fingerprints;
- Resume Plan revision, selected sections, and page intent;
- experience, project, skill, and education inventory;
- canonical skill keywords;
- document typography and layout.

The application draft owns:

- application-only contact overrides;
- professional-summary wording and evidence references;
- application-only experience and project bullet wording and evidence references.

Edits never mutate Career Vault, a Resume Version, or the Resume Plan. A changed
JD, profile, Vault revision, or plan invalidates the material until it is
rebuilt from current sources. Every resume summary and bullet must cite proof
IDs selected in the approved plan. JD demand and registry vocabulary never
become candidate evidence.

For cover letters, the deterministic template owns the field structure and the
renderer owns layout. Its canonical order is: two-row contact header, date,
salutation, three direct body paragraphs, sign-off, name, and credential line.
Those paragraphs contain confirmed motivation, verified evidence, and a concise
close. Motivation is never inferred; an unresolved default stays a visible
personalization placeholder until the applicant confirms it. Two to four typed
evidence items retain source references while the renderer combines their
claims into the single evidence paragraph. Factual claims cite Career Vault
evidence or carry a specific manual check. In
the evidence picker, proof points remain nested under their source Experience or
Project. Source groups start collapsed, preserve their expanded state while the
draft rerenders, and open automatically when a search matches one of their proof
points. The collapsed summary retains the total and selected-proof counts;
selecting a proof does not flatten or change its Career Vault ownership.

## Storage

Private material state lives under the selected Data Home:

```text
outputs/application_materials/<job_id>/
  materials.yaml       # cover-letter draft and optimistic revision
  resume.yaml          # tailored-resume draft and optimistic revision
  exports/             # generated DOCX, PDF, and TXT files
```

Files are job-specific derived artifacts. They are excluded from Git and are
not separately encrypted from the Data Home. Do not store passwords, government
IDs, demographic answers, or identity-document contents in them.

## Renderer Contract

The canonical resume renderer uses US Letter, one column, 0.5-inch margins,
standard section headings, normal body paragraphs, and real list paragraphs.
DOCX uses Calibri-compatible text; PDF uses the deterministic bundled sans-serif
face described below. Contact information stays in the document body.
DOCX output must contain no layout tables, graphics, populated headers, or
populated footers. PDF is generated directly from the same typed draft and TXT
provides the parser-order representation. Direct PDF output embeds the pinned
ReportLab-bundled Bitstream Vera Sans faces and fails if either face or its
redistribution license is unavailable; it never selects an operating-system
font. DOCX retains the Calibri-compatible family contract, so equivalent DOCX
pagination across Windows and macOS is not claimed until target-host visual QA
and a reviewed embedding or installation strategy exist.

Every export is reopened or structurally inspected before it is returned. DOCX
checks margins, body text, bullet count, tables, graphics, headers, and footers;
PDF checks its header and complete trailer; TXT checks UTF-8 and expected text.
LibreOffice is not bundled, so visual DOCX rendering remains a manual QA step on
this machine even when structural validation passes.

The canonical cover-letter renderer follows the retained MBDM template family:
US Letter, 0.5-inch margins, 10.5-point body text, a 15.5-point bold name,
tab-aligned contact rows, and a 1.5-point black divider under the first contact
row. DOCX uses its Calibri-compatible family and PDF embeds the selected
Bitstream Vera Sans faces. LinkedIn and GitHub are separate clickable links
with compact display labels rather than one unbounded text run; a tight `|`
separator matches the retained source. The three body paragraphs use the normal
content width with no list formatting. Headers and footers remain empty. The
renderer uses no layout tables, graphics, or arbitrary rich-text freedom. DOCX
and PDF share the same 0.5-inch content boundary and contact geometry.

## Agent Interface

Agents should not reread a broad Vault inventory or assemble OOXML manually.
Use the verified `career-hub-agent` client. Set `$agent` to its `executablePath`
and `$server` to its current `server` from the desktop app's **Settings > Local
agent connection** (the server address changes after a restart):

```powershell
& $agent material-context --server $server --job-id <job_id> --kind <resume|cover_letter>
& $agent save-material --server $server --job-id <job_id> --kind <resume|cover_letter> --expected-revision <n> --payload-file <json>
& $agent render-material --server $server --job-id <job_id> --kind <resume|cover_letter> --format <docx|pdf|txt>
```

`material-context` returns the complete typed draft, a bounded evidence catalog,
validation, preview, and an explicit editing contract. For resumes it exposes
only proof IDs already selected in Resume Plan. `no_change_needed` remains a
successful outcome: agents should preserve strong wording and change only what
materially improves relevance or clarity.

The browser's **Build with AI** action currently copies this bounded command.
Career Hub does not call an AI API and does not silently write a draft.

## Current Limits

- There is one canonical ATS template, not themes or arbitrary formatting.
- Resume selection remains a separate typed contract internally, but is embedded in the Tailor Resume workspace rather than exposed as another Job Workspace option.
- Visual pagination still needs a human check, especially for dense two-page drafts.
- The editor does not offer collaborative comments, cloud documents, or live ATS scoring.
- Export is local; attaching the chosen artifact and submitting remain explicit user actions.
