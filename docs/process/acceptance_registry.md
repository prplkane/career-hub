# Capability Acceptance Registry

This is the authoritative registry for what Career Hub currently does. Use a
capability identifier and its acceptance criteria in product, engineering, and
release discussions. Historical phase numbers may remain in older documents,
but they do not define implementation status or authorize work.

Historical Competency Graph task IDs and their exact requirement-to-evidence
mapping are versioned in [Competency Graph Task Traceability](../history/competency_graph_traceability.md).
That map does not override the capability statuses below.

## Product continuity invariant

Career Hub is a longitudinal career record, not a disposable application
campaign. Employment, an offer, or a pause in searching never deletes or
resets approved Career Vault evidence, application and outcome history, Resume
Versions, or provenance. New work enters through the same source-backed review
and approval boundary, so a later search can begin from the accumulated record.
Future assisted decisions must consume that deterministic evidence and history;
model reasoning may rank or draft, but it does not replace the source of truth.

## Status vocabulary

- **Implemented**: the capability has a user-facing or callable contract and
  automated validation appropriate to its risk.
- **Foundation**: storage or deterministic validation exists, but the complete
  user-facing outcome is not yet delivered.
- **Planned**: product intent only; it is not authorization to implement.

## Current registry

| Capability | Status | Acceptance evidence | Boundary |
| --- | --- | --- | --- |
| `CAP-VAULT` Career Vault and source intake | Implemented | Validated local Vault, TXT/DOCX extraction, review, approval, guarded manual editors, contextual review recovery, and approved-import file cleanup | No cloud or AI extraction; no generic Source Documents library |
| `CAP-JD-INTAKE` Fast JD capture | Implemented | A pasted or local JD becomes one immutable source/card, ledger record, and immediate advisory assessment | No URL fetching or scraping |
| `CAP-JD-ASSESSMENT` Deterministic fit assessment | Implemented | Evidence-backed, advisory visa, seniority, requirement, location, and experience conclusions are stored and shown synchronously; user-reviewed alert decisions are append-only and version-aware | No semantic or AI matching; never blocks action or silently rewrites the automated finding |
| `CAP-APPLICATION-LEDGER` Application tracking | Implemented | Local status history, Applied-resume history, searchable resume picker, standalone Save for applications, approved-import retention, Resume Library management, archive/restore, guarded deletion, and non-destructive TXT/DOCX digest/redigest from the library | `Reviewed` is legacy-readable only; PDF/DOC/RTF digest and automatic attachment/submission are not implemented |
| `CAP-OUTCOME-INSIGHTS` Application outcome insights | Foundation | The local application ledger already retains status history, submitted-resume links, dated events, and source provenance needed for descriptive analysis | No denominator-safe product insights, causal claims, predictive score, hosted telemetry, or cross-user benchmark are implemented |
| `CAP-APP-DATA-ROOT` Private data-home boundary | Implemented | Immutable resource root, separate native channel Homes, visible Settings path, pinned identities and containment checks, allowlisted bootstrap, explicit staged import, Windows Credential Manager secret boundary, SQLite migration recovery, and verified ZIP backup/restore are regression-tested; historical Python selections are not adopted implicitly | No automatic migration, cloud sync, custom cryptography, or whole-file YAML/SQLite encryption |
| `CAP-DESKTOP-FOUNDATION` Local Electron desktop foundation | Foundation | Versioned resource/data identities, a reproducible Go `onedir` sidecar and native agent client, platform-backed AES-GCM document protection, locked Electron 44 host/fuses, exact loopback navigation, offline/connection-available UX, one-child supervision, single-instance ownership, bounded quit, parent-pipe closure and crash/error/retry are present; Windows x64 packages, actual Credential Manager/DPAPI/ACL checks, isolated installed lifecycle/restore, and real Chromium extension/helper qualification have recorded evidence | macOS arm64 native build, Keychain and lifecycle qualification, signing/notarization, production update/release rehearsal and dedicated real-mailbox acceptance remain release gates; unsigned Windows development installers are not a production release; Linux is not a supported product target |
| `CAP-DESKTOP-DIAGNOSTICS` Installed-app diagnostics | Planned | Requires bounded cross-process logs, incident correlation, privacy-tested diagnostic export, and installed-runtime health evidence | The minimal sidecar health endpoint is foundation only; it exposes no user data and does not make diagnostic export implemented |
| `CAP-DESKTOP-DISTRIBUTION` Signed desktop distribution | Planned | Requires reproducible per-platform sidecar/application builds, supported-platform CI, signing/notarization, production update verification, and release/recovery rehearsals | Unsigned Windows development installers and isolated lifecycle evidence exist; no signed/notarized production release or production update-feed qualification is claimed |
| `CAP-RESUME-PLAN` Evidence selection | Implemented | Any persisted job/application record can save and approve a revisioned, source-valid Resume Plan | It selects evidence; it does not write resume prose, and application stage is not a permission gate |
| `CAP-BASE-RESUME` Base resume variants | Foundation | Source-linked Markdown/metadata variants and validation exist | These legacy variants are not the job-scoped material composer or renderer |
| `CAP-APPLICATION-PACKAGE` Reviewable package contract | Foundation | Package schema validates explicit selection/exclusion and provenance | Draft prose remains human or agent authored outside the app |
| `CAP-RESUME-REUSE` Reuse decision engine | Planned | Requires explicit similarity inputs, explainable decision policy, and manual-approval safeguards | Product-spec Phase 5 is historical intent only |
| `CAP-RESUME-COMPOSE` Resume composer | Implemented | A constrained job-scoped editor builds application-only header, summary, and bullet wording from an approved revisioned Resume Plan; every claim retains selected proof ids and Vault records remain unchanged | One canonical structure only; no arbitrary formatting, automatic AI call, reuse decision, or source-record mutation |
| `CAP-RESUME-RENDER` DOCX/PDF/TXT renderer | Implemented | One canonical ATS renderer exports the typed draft, reopens or inspects every artifact, and tests text order, margins, bullet count, tables, graphics, headers, footers, PDF completeness, and UTF-8 text | DOCX visual pagination remains a human gate where LibreOffice is unavailable; export does not attach or submit the file |
| `CAP-MATERIAL-INTEGRITY` Durable material lineage and final fact gate | Foundation | Current renderers reject stale drafts, validate proof ownership and document structure, reopen outputs, and return revision plus SHA-256 metadata | No immutable persisted export manifest, claim-to-cited-proof token validation, recorded page-budget result, or durable artifact lineage exists |
| `CAP-COVER-LETTER-COMPOSE` Structured cover-letter material | Implemented | A reusable typed template prefills current candidate/job facts, requires reviewed evidence or manual checks, uses optimistic revisions, and exports structurally validated DOCX/PDF/TXT | General screening answers and outreach remain in the private Text Workbench; no AI call, upload, or submission |
| `CAP-APPLICATION-NARRATIVE-BRIEF` Evidence-backed application narrative | Planned | Requires one revisioned job-scoped brief with typed JD, Career Vault, user-statement, and optional external-source provenance for motivation, problem, approach, tone, vocabulary, evidence, and gap decisions | No mandatory research, company score, automatic writing, contact enrichment, or promotion of strategy notes into Career Vault |
| `CAP-AGENT-WORKFLOW-RUNS` Auditable agent workflow runs | Foundation | The agent adapter already emits bounded typed packets, snapshot and packet fingerprints, context metrics, compact mutation receipts, no-op success, and guarded submission commands | No persistent run state machine, checkpoint recovery, approval history, or cross-step validation ledger is implemented; no AI provider is part of the product |
| `CAP-JD-INTAKE-HARDENING` Scale and reliability guardrails | Implemented | Idempotent intake, posting identity, cache behavior, pagination, and intake metrics are regression-tested | This is a capability, not a product-spec phase |
| `CAP-VAULT-SKILL-EDITOR` Registry-backed manual evidence editing | Implemented | Experience and Project editors select canonical Skills Registry entries as validated pills, can explicitly create a custom skill in a chosen group, and preserve compatible legacy wording during migration | Vault evidence and any new custom vocabulary commit transactionally with rollback; free-form prose never becomes a skill automatically |
| `CAP-REPOSITORY-IMPACT-INTAKE` Repository impact analyzer | Planned | A user-selected local repository or explicit provider export supplies bounded commit, pull-request, issue, release, and file-change evidence; deterministic extraction plus optional BYOK reasoning produces source-linked draft achievements, Experience/Project cards, and exact or proposed competency references for the existing review-and-approval flow | Repository activity is evidence to review, not proof of ownership, scope, or business impact; no inferred metrics, secrets or source-code transmission by default, background repository access, arbitrary GitHub access, or automatic Career Vault/catalog write; only explicitly approved proposals may create canonical records or custom skills |
| `CAP-COMPETENCY-REFERENCES` Canonical legacy-reference migration | Implemented | A deterministic per-record preview accepts only exact canonical names and approved aliases; confirmed apply pins Vault and registry revisions, checksum-verifies every stored recovery-ZIP payload before archive installation, validates replacements in memory, uses per-file atomic replacements with transaction rollback and an audit receipt, preserves legacy text, and is idempotent | It never creates vocabulary, uses fuzzy/LLM inference, migrates archived records, or treats JD demand as candidate evidence |
| `CAP-CANDIDATE-EVIDENCE-GRAPH` Rebuildable evidence projection | Implemented | A deterministic read-only Go/JSON projector emits Competency→Experience/Project/Certification, Project→Experience, and Competency→Domain edges only from active canonical Vault records and explicit `competency_ids` | No graph database or cache; JD, drafts, legacy strings, and archived evidence do not create candidate evidence; archived competencies require explicit historical scope |
| `CAP-EVIDENCE-ANALYTICS` Context and interval analytics | Implemented | Per-competency output reports distinct professional contexts, independent projects, certifications, direct proof counts, source types, first/last used, active-now state, and merged inclusive month intervals without double counting | Certification dates are not usage; linked Projects do not create a second professional context; no composite candidate score |
| `CAP-JD-DEMAND-INDEX` Saved JD demand projection | Implemented | A deterministic read-only Go/JSON index counts required and preferred competencies and domains for jobs already present in the application ledger, applies an inclusive captured-date period, retains exact source occurrences, and deduplicates exact JD fingerprints | Unregistered or mentioned-only terms do not count; legacy cards use exact active name/alias resolution only; no candidate evidence or vocabulary writes |
| `CAP-COVERAGE-MATRIX` Evidence-to-demand coverage | Implemented | A deterministic read-only join reports `supported`, `thin_evidence`, `evidence_not_found`, and `not_requested` with separate evidence and required/preferred demand counts | `evidence_not_found` means only that evidence was not found in Career Vault; no candidate-knowledge inference, ranking, or score |
| `CAP-DEMAND-GAPS-UI` Saved-job demand and evidence comparison | Implemented | **Jobs / Demand & coverage** renders the existing read-only projections as a period-scoped coverage band, top-demand and priority-gap summaries, and a searchable/filterable detail list; selected skills open related saved-job context plus an explicit link to the canonical Skills evidence view | No radar, composite score, inferred candidate knowledge, trend claim, vocabulary write, or Career Vault mutation |
| `CAP-COMPETENCY-EXPLORER` Unified skill evidence navigation | Implemented | The **Skills / All skills** master/detail view joins every active registry skill to a catalog-revision-matched evidence projection, filters by group/source/evidence/domain/kind, and opens linked Experience, Project, and Certification records; archived registry detail loads explicit historical evidence | Evidence remains a read-only projection while **Skills / Groups** owns vocabulary changes; **All skills** stays active-only, the legacy `#competencies` route redirects to `#skills/all`, and JD demand/gap visualization remain outside this page |
| `CAP-ROLE-CATALOG` Canonical role catalog and observations | Planned | Core read-only roles and aliases support structured Profile targets; saved JDs retain their exact source title plus a deterministic canonical-role observation and local role-to-skill demand counts | A role never implies candidate skills, and observed JD titles never silently modify the core catalog |
| `CAP-EDUCATION-PATHWAYS` Evidence-backed education planning | Planned | Requires a local programme-and-intake workspace that maps sourced admission requirements and full-cost scenarios to exact private Career Vault evidence with explicit missing, ambiguous, stale, and confirmation-required states | No admission probability, universal score, programme-source-to-Vault promotion, automatic essay generation, upload, submission, or requirement that a remote catalog exist |
| `CAP-PROGRAM-CATALOG` Provenance-first public programme catalog | Planned | After local Education Pathways dogfood, a versioned read-only public catalog may distribute normalized intake-specific programme claims, sources, freshness, and correction state through integrity-checked local caching | The catalog stores no transcript, resume, identity, private comparison, saved target, or Career Vault evidence; no arbitrary crawling, protected-page access, paid personalized placement, cross-user prediction, or private cloud sync is implied |
| `CAP-OBSIDIAN-EXPORT` One-way approved-data export (CG-601) | Planned | Requires stable ID-based filenames, YAML frontmatter, generated-only wikilinks, an explicit generated root, and idempotent repeat export | Never exports raw resumes or private files to Git, overwrites manual notes, or accepts Obsidian edits as canonical data |
| `CAP-DOCUMENT-LEDGER` Private career-document ledger | Implemented | A dedicated local ledger manages encrypted personal document files and metadata, type, validated dates, archive state, file opening, a compact Profile summary, and a title-only future-model projection | Windows DPAPI binds recovery to the same Windows account/device but does not sandbox arbitrary same-account code; no model runtime, JD/resume matching, cloud sync, OCR, or content extraction |
| `CAP-JOB-DISCOVERY-INBOX` Staged job discovery | Implemented | Versioned local tables store bounded captures and observations; conservative identity, exact-retry idempotency, containment, lifecycle transitions, recovery migration, deterministic LinkedIn/Indeed/Handshake/MeeBoss/12twenty posting identities where exact evidence exists, provider-attributed source provenance, transaction-guarded source-preserving parser repair, explicit verified complete-content promotion through existing JD intake, versioned extension link/selection/visible-page capture, content/candidate/context-versioned advisory page previews, and the `INT-300` recovery matrix are automated | Discoveries do not enter the application ledger, Demand & coverage, or saved-job analytics before promotion; promotion requires an explicit browser confirmation, generic provider/search pages are never guessed into a posting, link/selection captures are unassessed, and link-only/unverified/stale/dismissed captures cannot promote |
| `CAP-JOB-FEED-UI` Unified job discovery feed | Implemented | **Jobs / Job Feed** presents one deduplicated Feed plus Email and Watchers modes, exact Discovery deep links, source/readiness/search/sort controls, retained text and assessment context before controls, reviewable effective title/company/location corrections, shortlist, dismiss/restore, no-write **Review later**, explicit complete-JD verification, confirmed **Save job** / **Record as applied** actions, local Email connection/schedule controls, and the passed `INT-300` end-to-end matrix | List/cards preference and remaining producers/enrichers remain planned; Email/Watchers never own duplicate job lists, capture never implies Applied, and verification alone never creates a Job or Application |
| `CAP-EXTENSION` Browser extension capture | Implemented | One reviewed MV3 client builds least-privilege Chrome, Edge, Firefox Desktop, and Safari packages; browser adapters select Chrome/Edge side panels, the Firefox sidebar, or Safari's dedicated helper tab without changing the bounded capture/helper protocol. It explicitly saves the active HTTP/HTTPS page, exact selection, or link-only fallback through the exact Origin/token boundary; **Save and open workspace** opens only its server-issued `/prepare` review intent; main-frame bounded extraction, contamination rejection, ambiguity/size fallback, fixed worker-owned endpoints, request-local tokens, bounded no-score receipts, browser contract tests, Mozilla lint/runtime loading, rendered UI checks, and the `INT-300` security/privacy matrix provide release evidence | No persistent site access, background crawling, form/hidden/raw-HTML capture, credential/cookie access, model/API call, auto-apply, implicit Applied transition, arbitrary route/id/JD input, broad host wildcard, or helper-data storage; Firefox for Android and mobile browsers are out of scope, Chromium variants beyond Chrome/Edge are best effort, and Safari still requires its macOS/Xcode live release gate before distribution |
| `CAP-DISCOVERY-FIELD-CORRECTIONS` Reviewable capture corrections | Implemented | A separate optimistic-revision override record provides effective title/role, company, and location while preserving every extracted value, source snapshot, extractor metadata, and observation as read-only evidence; UI labels **Extracted** versus **Edited by you**, survives richer recapture/reload, and clears each field back to the latest extraction | Empty/oversized/unknown/stale writes are rejected, ambiguous generic pages remain reviewable, the extension capability cannot call the correction API, and corrections alone create no Job, Application, assessment, skill, or contact |
| `CAP-APPLICATION-SUBMISSION-TRACKING` Attended application-session tracking | Implemented | **Track submission** claims an explicit pending session or creates one only when exactly one startable saved application matches the current canonical posting URL, then binds the four-hour session to one tab/Origin through a hashed per-session capability and one session-storage key per tab; a trusted native submit or exact final-action click plus an enumerated success state records one idempotent submission-detected event, and Applications asks **Mark this application as applied?** before reusing the existing Applied transition | No title/company guess, no-match population, ambiguous-URL choice, form-value/page-text/credential capture, extension-driven Submit click, button-label-only transition, silent auto-apply, or cross-application update; cross-Origin navigation, tab reuse, timeout, closure, instrumentation loss, and dismissed signals leave the application stage unchanged |
| `CAP-HARD-BLOCKER-POLICY` User-controlled clear-conflict policy | Implemented | Profile owns revisioned private eligibility facts plus three revisioned policy classes: citizenship/work authorization or sponsorship, required active clearance, and explicit minimum years above verified comparable experience; saved-JD and Discovery assessments reuse one evaluator, retain exact JD/profile evidence, and refresh when facts or policy change | Unknown facts, unavailable exact evidence, disabled classes, location, work arrangement, relocation, missing skills, title similarity, and silence stay Review; findings are advisory, every action remains available, and the extension cannot read policy or profile facts |
| `CAP-APPLICATION-INSTRUCTIONS` Source-backed application-step checklist | Implemented | Complete captured JDs produce a bounded deterministic list of explicit email destination, exact phrase/subject, required artifact, question/cover-letter format, external-step, and deadline instructions with exact evidence, extractor version, append-only reversible checklist actions, stale-source handling, and one exact-selection manual-add path; the extension receives only a conditional count | Partial/link captures say **Full JD required**; there is no network/AI extraction, link following, completion inference, or workflow blocking, and no finding means only that the narrow rules found none |
| `CAP-JOB-CONTACTS` Confirmed job contacts and links | Implemented | Complete retained JDs produce bounded review-only candidates with exact evidence; schema-v24 private storage provides optional canonical contacts, exact channels, append-only and retry-safe candidate provenance, zero-or-more explicit contact links per Job/Discovery, backup-first recovery, optimistic revisions, archive/restore, unlink-without-delete, exact email/profile deduplication, and bounded potential-duplicate review; **Save contact** revalidates the current source and requires an explicit create/link choice, while the compact Contacts ledger supports search, detail, edit, active/archived scope, and source navigation | An application never requires or force-populates a contact; opening/closing candidate review, capture, promotion, dismissal, and reanalysis create no contact; partial captures require the full JD, similarity never auto-merges, extension capabilities cannot call contact routes, and there is no deletion, enrichment, social search, outreach automation, relationship score, or CRM pipeline |
| `CAP-APPLICATION-HELPER` Attended application assistant | Implemented | An empty-by-default closed Profile allowlist drives masked one-field Copy and reviewed atomic autofill across supported text, textarea, select, and yes/no radio controls. Explicitly enabled voluntary US demographic answers are available only through the same one-field Copy action and are never mapped to page controls. The same side panel exposes only active Resume Version ids/display labels and opens one freshly verified exact Resume Library deep link for browser-only download. A lazy private Git-ignored answer bank learns only after **Find completed answers** and one explicit row-level **Save**; exact-question Review/Fill, Copy, and Delete use independent optimistic revisions and one-shot memory-only review tokens. Stale data/form/page, hidden saturation, multi-form choice, dynamic rerender, exact resume focus, sensitive-question exclusion, no-storage, rollback, and no-submit cases have focused protocol/domain coverage plus a packaged rendered smoke path | No Copy all, background/persistent scan, cross-origin frame, arbitrary selector/value/Vault path, resume bytes or private filename in the extension, extension storage of helper data, credentials/OTP/SSN/payment, financial or demographic autofill, eligibility/compensation/upload/consent learning, job-specific/open-ended learning, non-empty overwrite, checkbox/file fill, Continue/Next/Submit click, or unattended form activity |
| `CAP-EMAIL-ALERT-INGEST` Job-alert email intake | Implemented | **Job Feed / Email** accepts bounded `.eml` files and provides a synthetic-verified, read-only dedicated-Gmail connector with exact-sender header-first filtering, explicit per-sender **Job opportunities only** / **Application updates only** / **Updates + included job suggestions** routing, advisory sender/Subject/recurrence suggestions that never store raw Subject or auto-allow a sender, layered receipts/deduplication, manual resync, opt-in startup/interval sync, bounded backoff, a cross-process lease, and a one-shot local CLI; exact service labels on opaque Handshake and SuccessFactors tracking links are suppressed before candidate creation, tracking-only senders cannot create Feed items, and application updates live under **Applications**; provider-attributed LinkedIn cards merge by posting id, while supported LinkedIn rejection and Handshake submission events remain separate from same-email recommendations, use protected status transitions, and allow revision-guarded explicit linking when Handshake supplies no posting identity; original emails, opaque application redirects, and credentials are not retained in the Data Home | Link/snippet alerts remain **Needs JD** even when the source calls them a match; no attachment ingestion, message mutation, cloud webhook, protected-link traversal, site search, automatic job/application creation, company-only auto-linking, or user-authored CSS/XPath/regex parser rules; Offer/Withdrawn/Closed conflicts require review |
| `CAP-JOB-LINK-RESOLUTION` Public job-link enrichment | Implemented | The guarded one-shot local resolver route can enrich the already-retained URL on one active link-only Discovery; per-hop public-address validation, validated-address socket pinning with normal TLS hostname checks, redirect/response/parser limits, optimistic target revision, idempotent retry, and an unverified `page_visible` result are covered by focused domain/API tests and the completed `LINK-130` security diff. It is not offered as a Job Feed control: the current user workflow opens the retained posting and uses attended extension capture | No login automation, request-body URL, IP literal, proxy inheritance, protected-page traversal, JavaScript/browser automation, recurring crawl, related-link traversal, search fallback, credential/cookie access, automatic completeness, skill/profile mutation, silent job/application promotion, or background invocation |
| `CAP-JOB-SOURCE-TRUST` Source-backed posting trust signals | Foundation | Discovery observations already retain provider, posting identity, canonical/observed URL, capture channel, source hash, observed time, and bounded resolver security results | No unified advisory source-trust projection, exact risk-signal evidence, legitimacy score, safe/scam verdict, external reputation lookup, or automatic blocking exists |
| `CAP-JOB-WATCHING` Precision public-source watcher | Planned | Requires explicit provider/board configuration, documented unauthenticated read endpoints, versioned adapter conformance, source filters, rate limits, storage/privacy policy, and a dogfood exit report | No scraper or watcher is present; future adapters may not bypass authentication, CAPTCHA, access controls, or provider restrictions; arbitrary crawling, protected pages, login, browser automation, auto-promotion, and application submission remain outside the capability |
| `CAP-INTERVIEW-PREP` Evidence-backed interview preparation | Planned | Requires revisioned proof-linked STAR+R stories and bounded job-specific preparation packets built from exact JD requirements, the narrative brief, and approved Career Vault evidence | No recording, biometric/emotion analysis, answer score, live coaching, or automatic Career Vault update |

Agent-workflow and watcher plans do not weaken the no-model, no-scraper,
attended-helper, or no-submit boundaries of the currently Implemented
capabilities. In particular, unattended browser filling, submission, and batch
application runs are not accepted product scope.

## CV-first resume-management capabilities

**Implemented resume-entry slices.** The implemented `CAP-VAULT` and
`CAP-APPLICATION-LEDGER` rows cover the current callable behavior below.

`CAP-VAULT` provides this contract:

- Career Vault becomes canonical only after the user explicitly applies an
  approved intake review.
- **Add resume** offers exactly two intents: **Update Career Vault** and
  **Save for applications**. Assessment is not a third Add resume intent.
- An import-only resume is temporary workflow input, not a durable dependency
  of approved Career Vault records and not an item in the application Resume
  Library.
- After approval has been successfully applied, **Keep for applications** may
  create one immutable Resume Version from the matching original source without
  changing Vault records or the approval audit.
- After approval, **Remove original file** is a separate confirmed, idempotent
  cleanup action. It verifies the approval audit, raw-root containment, file
  type, and SHA-256 before removing only the original input file. It does not
  remove canonical Vault YAML, `extracted.yaml`, `review.yaml`, or
  `approval_result.yaml`.
- A generated resume may be imported again, but it follows the same manual
  review and explicit-approval path as every other import.
- Duplicate suggestions are advisory and non-blocking. They never auto-merge,
  auto-update, or prevent the user from choosing Update, Create, or Do not add.

`CAP-APPLICATION-LEDGER` provides this contract:

- **Resume Library** means reusable application materials only.
- **Save for applications** creates a durable immutable Resume Version without
  updating Career Vault.
- Resume Versions retain internal `uploaded`/`generated` origin metadata. All
  current writers create `uploaded`; this is not a separate user-facing library
  or a generation capability.
- Active Resume Versions appear in the searchable application picker; archived
  versions do not appear there by default.
- Archive is reversible. Hard deletion is guarded, and a Resume Version named
  by Applied/submitted history cannot be hard-deleted by the MVP flow.

The Add resume page shows a bounded list of unfinished imports as contextual
recovery actions. Review is not a permanent sidebar destination: its local
`#review/<source_id>` route restores the review and returns to Add resume. No
generic user-facing Source Documents capability is implied.

**Implemented lifecycle slices.** Resume Library management, archive/restore,
guarded deletion of unused Resume Versions, approved-import file cleanup,
job-scoped composition, canonical local rendering, and navigation simplification
preserve the two-intent entry contract. Explainable resume reuse remains a
separate planned capability.

## Acceptance rules

1. A change may claim **Implemented** only after its registry row and the
   relevant automated/manual acceptance evidence are updated.
2. A feature can be a **Foundation** without implying that a later capability
   is implemented. Resume Plan approval still does not itself constitute a
   rendered artifact; the separate material draft must pass its own checks.
3. Documentation must link to this registry when describing current scope.
   It may use a historical phase only when explicitly marked historical.
4. New work should add or update a named capability row before it relies on a
   phase label in a plan, UI, or release note.

Prioritized future capabilities and their implementation boundaries are
maintained in the [Product Backlog](../product/product_backlog.md). Backlog order does not
change the statuses above.

## Historical mapping

The product spec's Phase 5, Phase 6, and Phase 7 map respectively to
`CAP-RESUME-REUSE`, `CAP-RESUME-COMPOSE`, and `CAP-RESUME-RENDER`. The former
intake-hardening Phase 6 label maps to `CAP-JD-INTAKE-HARDENING`; it is not
the same roadmap stage as the product-spec composer.

## Verification

Run the documentation contract check with:

```powershell
python -m unittest tests.test_acceptance_registry -v
```
