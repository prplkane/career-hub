# Product Backlog: Evidence-Backed Career Operations

Status: approved planning backlog; implementation has not been authorized by
this document.

Last reviewed: 2026-09-03

This backlog records the product capabilities Career Hub should adapt from the
broader [`santifer/career-ops`](https://github.com/santifer/career-ops) product.
It is intentionally narrower than that project. Current implementation status
remains authoritative in the [Capability Acceptance Registry](../process/acceptance_registry.md).
A backlog priority is not an `Implemented` claim.

The current sequencing gate is to finish the active Email, Job Feed, and
browser-extension stabilization work before starting a new capability below.
Existing accepted behavior must keep passing while a backlog item is developed.

## Product Decision

Career Hub should learn from `career-ops`, not become a fork of it. The useful
patterns are its final fact gate, artifact bundling, reuse decision, public ATS
connectors, structured cover-letter discovery, interview preparation, and
job-search outcome loop. Career Hub should implement those patterns through its
existing typed Python domain services, Career Vault evidence, Discovery Inbox,
and narrow agent adapter.

| Upstream pattern | Decision for Career Hub | Reason |
| --- | --- | --- |
| Pre-export fact validation and artifact bundle | **Take and strengthen** | Career Hub already validates proof ownership and rendered structure; it still needs durable artifact lineage and exact claim-to-proof checks. |
| Similar-JD resume reuse decision | **Take and strengthen** | Reuse saves time and tokens when the decision is explainable, source-fresh, and user approved. |
| Public ATS scanner | **Adapt as precision watchers** | Use documented public read APIs and the existing Discovery domain, not arbitrary crawling or a second job database. |
| Agent modes for end-to-end workflows | **Adapt behind typed runs** | Keep narrow skills and commands; add checkpoints, receipts, validation, and recovery instead of one large prompt router. |
| Company/role questions before a cover letter | **Adapt as an optional narrative brief** | Confirmed motivation, problem, approach, and tone improve writing without forcing web research or generating a document. |
| Posting-legitimacy review | **Adapt as source trust signals** | Exact provenance and risk evidence are useful; a trust score or “safe/scam” verdict would be overclaiming. |
| Interview story bank and preparation | **Take later** | Career Vault proof IDs can back reusable stories without inventing accomplishments. |
| Funnel and outcome analysis | **Take later** | The application ledger can support descriptive local insights once enough real observations exist. |
| Composite fit score and apply cutoff | **Do not take** | It conflicts with evidence-specific, advisory Career Hub findings and can hide uncertainty. |
| Automatic resume/cover-letter creation for every job | **Do not take** | It wastes model context and creates unrequested text. `no_change_needed` must remain a valid result. |
| Unattended browser filling, submission, or batch applying | **Do not take** | It conflicts with the attended extension, local review, and explicit submission boundary. |
| One mega-skill, plugin marketplace, or UI rewrite | **Do not take** | These add instruction and maintenance surface without improving the domain model. |

## Product Invariants

Every backlog item must preserve all of these rules:

1. Career Vault is the source of candidate facts; a JD, web page, model, or
   registry term never becomes candidate evidence.
2. External text is untrusted data, never executable agent instruction.
3. Network access is explicit, public-read-only, bounded, and attributable to
   one user-configured source.
4. Deterministic code owns identity, deduplication, state transitions,
   validation, rendering, and write receipts. An agent supplies bounded
   language judgment only.
5. Every mutation uses an existing domain service, optimistic revision or
   idempotency key, and a compact receipt. Agents do not edit private YAML or
   SQLite files directly.
6. Every recommendation is inspectable and advisory. No score, missing-evidence
   result, or agent opinion blocks the user from continuing.
7. No workflow uploads a document, submits an application, or marks `Applied`
   without the existing explicit confirmation.
8. Private content stays in the selected Data Home. Diagnostics retain IDs,
   hashes, states, timings, and error classes rather than raw JD or resume text.
9. A no-op or `no_change_needed` result is successful and must not trigger a
   rewrite quota.
10. Each provider or agent workflow ships with bounded fixtures, negative
    cases, recovery behavior, and a named acceptance-registry boundary.

## Existing Foundation: Do Not Rebuild

The backlog extends these accepted capabilities:

- `CAP-JOB-DISCOVERY-INBOX`, `CAP-JOB-FEED-UI`,
  `CAP-EMAIL-ALERT-INGEST`, `CAP-EXTENSION`, and
  `CAP-JOB-LINK-RESOLUTION` already own capture, provenance, deduplication,
  review, and promotion.
- `CAP-RESUME-PLAN`, `CAP-RESUME-COMPOSE`, `CAP-RESUME-RENDER`, and
  `CAP-COVER-LETTER-COMPOSE` already own evidence selection, typed drafts, and
  local DOCX/PDF/TXT rendering.
- `src.agent_workflow` already provides bounded task packets, fingerprints,
  context metrics, no-op behavior, validated mutations, and submission guards.
- The application ledger, not a watcher or an agent, owns application state.

No ticket below may introduce a parallel feed, database, renderer, evidence
store, agent command implementation, or web UI.

## Priority And Dependency Order

Priority meaning:

- **P0**: integrity foundation required before broadening automated inputs or
  presenting an agent workflow as a durable product capability.
- **P1**: next core product increment after the current stabilization gate.
- **P2**: required for the mature product, but depends on P0/P1 dogfood evidence.
- **P3**: long-horizon product expansion that is recorded for continuity but
  must not displace the current career-application roadmap.

```text
Email / Feed / Extension stabilization
  -> PB-MAT-100
  -> PB-AGT-100

Discovery foundation
  -> PB-SRC-100
  -> PB-WATCH-100
  -> PB-WATCH-110
  -> PB-WATCH-120

PB-MAT-100 + PB-AGT-100
  -> PB-NAR-100
  -> PB-RES-100
  -> PB-INT-100

Application ledger observations
  -> PB-OUT-100

Stable Career Vault evidence + mature application workflows
  -> PB-EDU-100
  -> PB-EDU-110
```

| Order | Ticket | Capability | Priority | Pull condition |
| --- | --- | --- | --- | --- |
| 1 | `PB-MAT-100` | Material integrity and lineage | P0 | Current material workflow is stable |
| 2 | `PB-AGT-100` | Auditable agent workflow runs | P0 | Existing adapter audit is clean |
| 3 | `PB-SRC-100` | Source trust signals | P1 | Discovery provenance remains stable |
| 4 | `PB-WATCH-100` | Watcher policy and adapter contract | P1 | `PB-SRC-100` accepted |
| 5 | `PB-WATCH-110` | Greenhouse watcher pilot | P1 | `PB-WATCH-100` accepted |
| 6 | `PB-NAR-100` | Evidence-backed narrative brief | P1 | `PB-AGT-100` accepted |
| 7 | `PB-RES-100` | Explainable resume reuse | P1 | `PB-MAT-100` accepted; `OPT-004` complete |
| 8 | `PB-WATCH-120` | Second adapter and watcher operations | P2 | Greenhouse dogfood meets its exit gate |
| 9 | `PB-INT-100` | Evidence-backed interview preparation | P2 | Narrative brief is stable |
| 10 | `PB-OUT-100` | Outcome insights and calibration | P2 | Sufficient real ledger observations exist |
| 11 | `PB-EDU-100` | Education Pathways workspace | P3 | Current roadmap is stable; repeated local program-planning demand is demonstrated |
| 12 | `PB-EDU-110` | Public program catalog | P3 | Local Education Pathways dogfood proves the schema and maintenance value |

## P0 — Integrity And Agent Foundation

### `PB-MAT-100` — Material Integrity And Lineage

- **Capability:** `CAP-MATERIAL-INTEGRITY`
- **Status:** foundation exists; durable capability not implemented.
- **User outcome:** the user can prove exactly which job, source revisions, and
  evidence produced every exported resume or cover letter, and the final file
  cannot silently contain an unsupported factual token.
- **Existing foundation:** renderers already reject stale drafts, validate proof
  ownership and document structure, return a SHA-256, and reopen the generated
  artifact. The returned metadata is not yet a durable immutable manifest, and
  evidence-reference validity is not the same as claim-to-proof validity.

Required scope:

1. Persist an immutable manifest beside each export with:
   `artifact_id`, `job_id`, material kind, draft revision, JD fingerprint,
   profile/evidence revisions, Resume Plan revision and fingerprint when
   applicable, cited proof IDs, manual-check IDs, template/renderer/validator
   versions, format, filename, byte count, SHA-256, creation time, page count
   when the format can provide one (otherwise an explicit unavailable reason),
   and validation result.
2. A rerender creates a new artifact version or returns an exact-idempotent
   receipt; it never overwrites a prior manifest ambiguously.
3. Extract final text from DOCX, PDF, and TXT and compare its normalized order
   with the typed draft.
4. For each editable claim, compare exact numbers, percentages, dates,
   employers, titles, named technologies, and other configured high-risk tokens
   with the union of its cited proof text. An explicit manual check may resolve
   only the named token and remains visible in the manifest.
5. Produce `pass`, `warning`, or `block` findings with stable codes and exact
   draft/artifact locations. Unsupported factual tokens and draft/artifact text
   divergence block export. Page-budget or typography concerns warn unless a
   hard resume standard is violated.
6. Report exact supported JD vocabulary coverage as a review aid only. It must
   not add terms, infer candidate skills, create a score, or encourage keyword
   stuffing.

Acceptance criteria:

- Each format has parity fixtures proving manifest persistence, hash
  verification, text order, and cited-proof traceability.
- Known unsupported metric, date, employer, title, and technology fixtures are
  blocked; supported and explicitly checked controls pass.
- A changed JD, Vault revision, Resume Plan, draft revision, manifest, or output
  hash is detected before the artifact is returned.
- Existing exports remain readable; migration does not claim provenance that
  was never recorded.
- The manifest contains no raw resume, JD, or proof prose.
- DOCX visual pagination remains a named human gate until native target-host QA
  is available.

Out of scope: model-based truth scoring, live ATS upload testing, design
templates, document submission, and rewriting failed content automatically.

### `PB-AGT-100` — Auditable Agent Workflow Runs

- **Capability:** `CAP-AGENT-WORKFLOW-RUNS`
- **Status:** adapter foundation exists; persistent guided runs are not
  implemented.
- **User outcome:** a resume, cover letter, application answer, or assessment
  can be resumed and audited as a sequence of bounded steps instead of relying
  on chat memory or one large prompt.

Required scope:

1. Add a typed local run record with `run_id`, task kind, job/material IDs,
   source snapshot and packet fingerprints, allowed adapter commands, current
   step, status, compact mutation receipts, approval checkpoints, validation
   codes, context metrics, timestamps, and bounded error metadata.
2. Use the state machine `ready -> running -> needs_review -> completed`, with
   explicit `failed` and `cancelled` terminal states. A source fingerprint
   change returns the run to `needs_review`; it never continues against stale
   evidence.
3. Every write goes through an allowlisted `src.agent_workflow` command.
   Workflow code orchestrates commands but does not duplicate their domain
   behavior.
4. A run may propose the next step, but saving, exporting, external delivery,
   and submission retain their existing user gates.
5. Deterministic validation runs before an optional editorial review. An
   editorial reviewer receives only the draft, allowed evidence, exact task,
   and validation findings; it is read-only and cannot certify factual truth.
6. Runs are restartable and idempotent from their last valid receipt. Reopening
   the full context packet is required only when a fingerprint or revision has
   changed.
7. Persist counts and hashes, not full prompts or private model transcripts.
   The product remains AI-provider-neutral; this ticket does not add an AI API.

Acceptance criteria:

- Tests cover success, `no_change_needed`, stale source, rejected write,
  interrupted recovery, retry, cancellation, validation block, and attempted
  submission without confirmation.
- No workflow can skip a required domain validation or forge a command receipt.
- A generating agent cannot issue, override, or approve its own validation
  result; only the mapped deterministic service produces that receipt.
- Context remains within the existing task budget, or the run stops with an
  explicit budget finding; facts are never silently truncated.
- Narrow repo skills remain independently discoverable. No combined mega-skill
  is introduced.
- The workflow audit maps every step to its implementation path and focused
  tests.

Out of scope: autonomous background agents, multi-agent batch applications,
provider selection, cloud run history, and direct private-file editing.

## P1 — Better Inputs And Better Decisions

### `PB-SRC-100` — Source Trust Signals

- **Capability:** `CAP-JOB-SOURCE-TRUST`
- **Status:** provenance foundation exists; unified trust projection not
  implemented.
- **User outcome:** the user sees why a posting source is recognized or why it
  needs caution without Career Hub claiming that a posting is safe or a scam.

Required scope:

1. Derive versioned signals from existing observation provenance: canonical
   host and provider, posting ID, capture/fetch channel, redirect chain,
   HTTPS/public-address validation result, content fingerprint, first/last seen,
   and whether the apply/contact domains match the recognized source family.
2. Add bounded exact-text risk signals for requests for payment, credentials,
   or sensitive identity documents before a normal application stage. Retain
   exact source evidence and label ambiguous language `needs_review`.
3. Present `recognized_source`, `needs_review`, and `risk_signal` findings
   separately. There is no aggregate legitimacy score and no `safe` verdict.
4. Findings remain advisory and never dismiss, promote, save, or block a job.

Acceptance criteria:

- Positive, negative, domain-mismatch, redirect, unavailable-source, and
  ambiguous-language fixtures retain exact evidence and stable codes.
- Extension, email, resolver, and future watcher observations produce the same
  projection from the same provenance contract.
- No live reputation service, personal-data lookup, or company-name-only match
  is required.

Out of scope: fraud guarantees, employer verification, social-media research,
domain registration scoring, or automatic reporting/dismissal.

### `PB-WATCH-100` — Precision Watcher Policy And Adapter Contract

- **Capability:** `CAP-JOB-WATCHING`
- **Status:** planned.
- **User outcome:** the user can configure a small set of public company job
  sources and know exactly what Career Hub will request, retain, and show.

Required scope:

1. Write a watcher ADR and threat model before network implementation. It must
   define supported source types, provider terms/API-documentation review,
   public-address and TLS rules, response/time limits, schedule limits,
   retention, schema drift, cancellation, backoff, and recovery.
2. Start only with providers that publish documented, unauthenticated read
   endpoints. Do not start with arbitrary HTML selectors.
3. A watcher configuration records a validated provider/board identity,
   enabled state, explicit include/exclude terms, location filters, schedule,
   last attempt/success, and revision. It stores no credential, cookie, or
   login session.
4. A provider adapter maps external data into the existing versioned Discovery
   observation contract with exact provider posting ID, canonical URL,
   observed/updated times when supplied, source payload hash, normalized
   retained text, adapter version, and parse findings.
5. Matching observations enter the existing Job Feed as `watcher` provenance.
   They are never auto-shortlisted, auto-promoted, or written to the application
   ledger. Full provider text remains reviewable before complete-JD
   confirmation.
6. One adapter conformance suite covers identity, pagination where applicable,
   duplicate retries, changed content, removed jobs, malformed/oversized
   responses, timeouts, `429`, `5xx`, schema drift, and source disablement.
7. Ship manual **Check now** before recurring scheduling. Scheduled runs are
   local, opt-in, lease-protected, rate-limited, and visible in the existing
   Watchers mode.

Initial provider order:

1. **Greenhouse Job Board API**: first pilot. Its documented public GET API
   exposes published job IDs, titles, locations, URLs, update time, and optional
   full content without authentication. Only the read endpoints are in scope.
   See [Greenhouse Job Board API](https://docs.greenhouse.io/job-board.html).
2. **Ashby Public Job Postings API**: second adapter after the Greenhouse exit
   gate. It exposes a documented JSON job-board endpoint and optional published
   compensation data. See [Ashby Job Postings API](https://developers.ashbyhq.com/docs/public-job-posting-api).
3. **Lever Postings API**: candidate third adapter only after its current
   public endpoint behavior and documentation are revalidated. See
   [Lever Postings API](https://github.com/lever/postings-api).

Out of scope: POST/application endpoints, generic web crawling, search-engine
results, protected pages, login, cookies, headless browsers, CAPTCHA handling,
anti-bot bypass, user-authored CSS/XPath/regex, or provider expansion before
measured demand.

### `PB-WATCH-110` — Greenhouse Watcher Pilot

- **Capability:** `CAP-JOB-WATCHING`
- **Status:** planned; depends on `PB-WATCH-100`.
- **User outcome:** the user can add a known Greenhouse board, test it manually,
  review matched postings in Job Feed, and disable it without data loss.

Acceptance criteria:

- Board identity is derived only from a validated Greenhouse board URL/token;
  arbitrary hosts and paths are rejected.
- One bounded list request per board/run returns public published jobs; exact
  content and provider IDs are normalized by the adapter.
- Explicit filters are deterministic and reviewable. No model ranks or hides
  fetched postings.
- Repeat runs are idempotent; changed content appends provenance; a missing job
  becomes a freshness signal and never changes an application status.
- Manual **Check now**, offline, timeout, malformed response, partial failure,
  disable/re-enable, and exact duplicate cases pass synthetic tests.
- Dogfood records request count, response size, duration, matches, new/changed
  observations, duplicate merges, parse findings, and errors without raw JD
  content in diagnostics.
- Recurring scheduling stays disabled until the pilot exit report confirms
  source accuracy, bounded resource use, and zero unexpected promotions.

### `PB-WATCH-120` — Second Adapter And Watcher Operations

- **Capability:** `CAP-JOB-WATCHING`
- **Status:** planned; depends on the Greenhouse pilot exit report.
- **User outcome:** watcher behavior is demonstrably provider-independent and
  manageable from the local product.

Required scope and acceptance:

- Implement Ashby through the same adapter contract and conformance suite;
  provider-specific logic does not enter Discovery or UI services.
- Add opt-in local scheduling with a conservative default, minimum interval,
  per-host serialization, bounded backoff, cross-process lease, and explicit
  retry. Exact values are fixed in the watcher ADR and changed only from
  measured pilot evidence.
- Show last success, last error class, next run, new/changed count, pause, check
  now, and remove configuration in the existing Watchers mode.
- Removing a configuration stops future requests but preserves already retained
  Discovery provenance.
- Two-provider fixtures and one approved live smoke prove that the adapter
  contract, not provider-specific UI code, owns the variation.
- Additional providers require observed user demand and a separate adapter
  ticket; provider count is not a success metric.

### `PB-NAR-100` — Evidence-Backed Application Narrative Brief

- **Capability:** `CAP-APPLICATION-NARRATIVE-BRIEF`
- **Status:** planned.
- **User outcome:** the user confirms the reasoning that should drive a cover
  letter, screening answer, recruiter message, and interview introduction
  before asking an agent to draft text.

Required scope:

1. Store one revisioned job-scoped brief with: why this role/company, the exact
   problem or priority being addressed, the user's proposed approach, audience,
   tone, a bounded confirmed JD vocabulary list, selected Career Vault proof
   IDs, and explicit gap/disclosure decisions.
2. Each factual field identifies one provenance type: exact JD evidence,
   Career Vault proof, explicit user statement, or optional dated external
   source. External sources retain URL, title, access time, and a bounded cited
   excerpt/hash; they never become candidate evidence.
3. Company research is an explicit optional action. The normal path works
   offline and never mandates a fixed number of searches or questions.
4. Agent contexts reuse the brief instead of repeatedly researching or asking
   the same questions. The brief may inform writing but does not create text by
   itself.

Acceptance criteria:

- Stale JD, evidence, or brief revisions are detected before drafting.
- Unsupported company or candidate claims cannot be silently saved as sourced.
- The user may leave optional fields blank, mark a gap for manual handling, or
  approve `no_change_needed`.
- Cover-letter, application-text, and interview contexts receive only the
  bounded fields relevant to their task.

Out of scope: mandatory web research, company scoring, contact enrichment,
automatic document generation, or promotion of user strategy notes into Career
Vault facts.

### `PB-RES-100` — Explainable Resume Reuse

- **Capability:** `CAP-RESUME-REUSE`
- **Status:** planned.
- **User outcome:** the user can choose whether to reuse an approved resume,
  revise its application-only delta, or build a new Resume Plan based on exact
  differences rather than guesswork.

Required scope:

1. Compare only approved Resume Plans and saved/exported material versions.
2. Use deterministic inputs: current and prior JD fingerprints, normalized
   required/preferred requirement sets, explicit alternatives, role/company,
   Career Vault/profile revisions, selected proof IDs, section/page intent, and
   material validation state.
3. Return one advisory action: `reuse_unchanged`, `review_delta`, or
   `build_new_plan`, plus exact reasons and changed requirements/evidence.
4. Never emit a candidate-fit score or infer semantic equivalence. Ambiguous
   requirements force review.
5. The user may choose any path. Reuse never attaches or submits an artifact and
   never mutates the prior approved version.

Acceptance criteria:

- Fixtures cover exact duplicate JD, small requirement delta, material role
  change, stale Vault evidence, changed Resume Plan, unsupported new required
  skill, and explicit user override.
- The same inputs produce the same reasons and recommendation.
- Reused material retains the original manifest and creates a new application
  linkage/receipt rather than copying untracked files.
- `OPT-004` proof source-of-truth work is complete first; `OPT-006` and
  `OPT-009` are implemented or explicitly incorporated into this ticket.

Out of scope: embedding similarity, hidden weighted score, automatic rewriting,
or automatic “best resume” selection.

## P2 — Complete The Career Feedback Loop

### `PB-INT-100` — Evidence-Backed Interview Preparation

- **Capability:** `CAP-INTERVIEW-PREP`
- **Status:** planned.
- **User outcome:** the user can build reusable interview stories and a
  job-specific preparation packet without turning rehearsal notes into
  unverified Career Vault facts.

Required scope:

1. A reusable STAR+R story records situation, task, action, result, reflection,
   selected Career Vault proof IDs, user approval state, and revision.
2. A job-specific packet combines exact JD requirements, selected stories,
   explicit gaps, questions to ask, and user notes. Questions generated by an
   agent remain suggestions until accepted.
3. Practice/debrief notes are private derived data. A proposed new fact enters
   Career Vault only through the existing reviewed intake/editor path.
4. The narrative brief supplies role/company intent; material and application
   drafts are read-only context, never evidence.

Acceptance criteria:

- Every factual story statement has a proof reference or a visible manual
  check; unsupported claims block approved status.
- Changed proof/Vault revisions make affected stories visibly stale.
- Packets are bounded, reproducible, and can be exported locally without audio,
  video, sentiment, or personality scoring.

Out of scope: interview recording, biometric/emotion analysis, answer scoring,
live coaching, or automatic Career Vault updates.

### `PB-OUT-100` — Outcome Insights And Calibration

- **Capability:** `CAP-OUTCOME-INSIGHTS`
- **Status:** ledger foundation exists; product insights are not implemented.
- **User outcome:** the user can see descriptive patterns across source,
  application materials, response time, interview stages, and outcomes without
  Career Hub claiming causation.

Required scope:

1. Use only existing application history, submitted-resume links, watcher/email/
   extension provenance, and explicit user outcome events.
2. Report counts and intervals such as applications, response rate, median time
   to response, stage conversion, source yield, follow-up timing, and material
   version usage. Always show denominator and date range.
3. Suppress or label tiny samples; never state that a resume, source, company,
   demographic attribute, or behavior caused an outcome.
4. Link every aggregate back to the exact local records included and expose
   unknown/missing states.
5. Use findings to prioritize parser, watcher, reuse, and workflow calibration;
   do not automatically alter those systems.

Acceptance criteria:

- Exact-range, unknown-date, duplicate-application, changed-stage, withdrawn,
  and no-response fixtures produce transparent denominators.
- No composite employability score, predictive hiring score, or cross-user
  benchmark is created.
- All computation remains local and records no new private telemetry.

Out of scope: causal claims, cross-user analytics, hosted telemetry, automatic
strategy changes, or model training on private application history.

## P3 — Education And Career Pathways

This is a long-horizon extension of Career Hub's evidence model, not a second
product and not authorization to build a hosted service. Its purpose is to help
a person decide whether a bachelor's, master's, certificate, or other formal
programme will strengthen a target career, prove readiness, and prepare an
application from already verified Career Vault evidence.

The product position is **evidence-backed education planning**, not rankings or
a universal admissions prediction engine. Broad programme directories already
exist; Career Hub's durable advantage is comparing source-backed programme
claims with the user's private, reviewed evidence while preserving uncertainty.

### Architecture boundary

```text
Remote public programme catalog
  institutions -> programmes -> intake-specific public claims and sources
                           |
                           | versioned read-only download and local cache
                           v
Local Education Pathways workspace <-> Private Career Vault
  targets, comparisons, plans,             education, courses, experience,
  readiness findings, applications         projects, skills, documents
```

- The remote catalog, if built, stores only public programme information,
  bounded source evidence, provenance, and maintenance metadata.
- Transcripts, resumes, identity, eligibility, private notes, evaluations,
  application materials, and Career Vault evidence remain in the selected
  local Data Home. A comparison runs locally.
- `institution -> programme -> intake` is the public identity model. Deadline,
  tuition, tests, prerequisites, funding, immigration implications, and outcomes
  are intake-specific claims, not timeless programme fields.
- Every public claim records its source URL, observed date, applicable intake,
  verification state, and stale/unknown condition. Old values remain historical
  rather than being silently overwritten.
- The browser extension may later capture an official page through an attended
  user action and assist with explicitly reviewed safe fields. It does not crawl,
  upload documents, answer sensitive questions, or submit an application.

### `PB-EDU-100` — Local Education Pathways Workspace

- **Capability:** `CAP-EDUCATION-PATHWAYS`
- **Status:** planned; long-horizon.
- **User outcome:** the user can save education targets, compare their real
  requirements and costs, see how verified Career Vault evidence maps to each
  requirement, and prepare a bounded application plan without treating a
  ranking or model opinion as an admissions decision.

Required scope:

1. Start local-only with manually reviewed programme and intake records. Do not
   make a remote service, scraping pipeline, or worldwide coverage a prerequisite.
2. Model institutions, programmes, intake cycles, degrees/credentials, format,
   location, duration, career targets, prerequisites, tests, deadlines, tuition,
   mandatory fees, living-cost assumptions, funding, immigration/work pathway,
   outcome evidence, and exact public sources separately.
3. Map each requirement to one advisory state:
   `satisfied`, `likely_satisfied_needs_confirmation`, `missing`,
   `may_be_in_progress`, `admissions_confirmation_required`, or `unknown`.
   Each positive state cites exact Career Vault evidence; absence means only
   **Evidence not found in Career Vault.**
4. Track a prerequisite/course plan, admissions questions, application status,
   document checklist, and user decisions without promoting programme text into
   candidate evidence.
5. Calculate minimum, expected, and offered net-cost scenarios with currencies,
   assumptions, and date. A per-credit or per-semester figure is never presented
   as a full programme price without an explicit degree-plan calculation.
6. Keep rankings as attributed external observations. Personalized comparisons
   explain exact reasons and uncertainty; they do not emit an admission
   probability, universal programme score, or AI-proof-career promise.
7. Reuse the existing Career Vault, document, material-lineage, and attended
   application-helper boundaries. Safe partial autofill remains field-by-field,
   reviewed, and no-submit.

Acceptance criteria:

- Fixtures cover changed intake, missing or ambiguous prerequisite, transcript
  course plus syllabus confirmation, different currencies, incomplete cost,
  scholarship offer, stale deadline, and unknown employment denominator.
- The same programme claim and Vault revisions produce the same advisory
  requirement mapping and cost calculation.
- No programme source, ranking, or model output creates or edits Career Vault
  evidence.
- Education applications remain distinct from the job application ledger while
  reusing shared evidence, document, provenance, and revision contracts.
- The feature is useful with a small manually curated dataset before any remote
  catalog work begins.

Out of scope: automatic admission decisions, fabricated odds, essay generation
without user request, credential evaluation, document upload, application
submission, institution-paid placement in personalized results, or a social
network.

### `PB-EDU-110` — Provenance-First Public Programme Catalog

- **Capability:** `CAP-PROGRAM-CATALOG`
- **Status:** planned; depends on successful `PB-EDU-100` dogfood.
- **User outcome:** local Career Hub installations can fetch maintained public
  programme data instead of every user repeatedly rebuilding the same facts,
  while candidate information remains private and local.

Required scope:

1. Publish a versioned, read-only catalog protocol with stable public IDs,
   schema/version manifests, incremental updates, integrity verification, local
   caching, explicit refresh, and a usable offline last-known snapshot.
2. Store normalized factual claims and bounded citations/hashes rather than
   complete copied university pages. Resolve source licensing and provider terms
   before ingesting any external dataset.
3. Begin with a narrow, measured collection of programmes for which real users
   need comparisons. Coverage count is not a success metric.
4. Prefer official university pages, documented public/government datasets, and
   explicit institutional feeds. Arbitrary crawling, protected pages, login,
   CAPTCHA bypass, and user-authored scraper rules are not allowed.
5. Add contribution and correction workflows only after the read path is stable.
   Proposed claims remain untrusted until source, identity, and reviewer checks
   pass; conflicts and stale values remain visible.
6. The public read service requires no Career Vault upload. Optional contributor
   identity, cross-device private sync, institution administration, and paid
   services are separate reviewed designs, not implied by this catalog.
7. Diagnostics contain public IDs, versions, counts, timings, and error classes,
   not private comparisons, saved targets, or candidate evidence.

Acceptance criteria:

- Contract tests cover first download, no-change refresh, incremental update,
  stale claim, retracted programme, conflicting source, invalid signature or
  manifest, interrupted update, retry, rollback, and offline use.
- A server or catalog compromise cannot read the user's local Career Vault or
  cause a public claim to become candidate evidence.
- Local programme saves and evaluations survive catalog updates and retain the
  exact public version against which a decision was made.
- A maintenance report exposes claim freshness, source coverage, unresolved
  conflicts, and correction latency without ranking institutions or users.

Out of scope: private Career Vault hosting, remote transcript/resume storage,
cross-user admission prediction, lead selling, sponsored personalized ranking,
unattended application automation, or worldwide launch before bounded dogfood.

### Long-horizon identity direction

Education Pathways may eventually support a selectively publishable
proof profile: the user chooses which verified projects, skills, education, and
outcomes leave the private Career Vault. This can become a private-first career
identity layer, but it is not currently a LinkedIn replacement commitment.
Feeds, messaging, recruiter search, social graphs, identity verification,
moderation, abuse handling, and network-effect growth each require separate
evidence and product decisions. None is implied by `PB-EDU-100` or
`PB-EDU-110`.

## Evidence Required Before Pulling A Ticket

Every implementation task derived from this backlog must include:

1. One named capability and one ticket only.
2. Current-state evidence showing the gap still exists.
3. Exact files and data migrations in scope.
4. A threat/privacy review for network, agent, or document-boundary changes.
5. Synthetic positive, negative, stale, retry, and recovery fixtures.
6. Focused tests plus the relevant repository contract checks.
7. A dogfood exit report before expanding providers, automation, or UI scope.
8. An acceptance-registry update only after all exit evidence exists.

Success is not feature count. Success is fewer unsupported claims, less repeated
context, accurate source provenance, safe recovery, and useful jobs or materials
reaching user review without silent state changes.
