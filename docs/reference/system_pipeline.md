# Current System Pipeline

> **Status: needs rewrite.** The intent below is still accurate, but its file paths point at retired `src/*.py` modules.
> Until this document is rewritten, use [architecture](architecture.md) and the [architecture atlas](architecture_atlas.md). See the
> [documentation index](../README.md); historical comparisons use the
> pinned reference export.

This document describes what the repository code does today, what Codex supplies through model reasoning, and which changes would reduce model token usage.

## Data Flow

```text
Career source material
  + unified core/custom Skills Registry
  -> Career Vault YAML
  -> deterministic Vault validation

Local job description
  + the same unified Skills Registry
  -> deterministic digest with source provenance
  -> draft normalized job card and exact fingerprint
  -> deterministic job-card validation
  -> manual review of unresolved or ambiguous facts
  -> deterministic exact-phrase review
  -> embedded per-application content/evidence selection (revisioned Resume Plan internally)
  -> deterministic selection validation
  -> deterministic evidence matrix and compact model context when needed
  -> constrained application-only wording and deterministic DOCX/PDF/TXT rendering
  -> user factual and visual approval
```

No repository module calls an AI API. Codex supplies the language-model layer
outside the Go desktop application. The domain descriptions below retain some
historical Python module names as provenance; those modules and product commands
are retired. Current native owners and retained API coverage are mapped in
`migration/retained_api_inventory.md`.

### Runtime Entry Points

- The Electron desktop starts and verifies the packaged Go sidecar. Development
  uses `scripts/start_career_hub.ps1` and the same native artifact contract.
- The sidecar reads one bounded versioned startup record from an owning stdin
  pipe, validates resource/data roots, admits one native owner, binds a loopback
  port, and emits machine-readable readiness. Extension mode uses its explicit
  local port; ordinary desktop mode uses an assigned port.
- The sidecar health route is content-minimal. `/api/session` remains the owner
  of the separate in-memory browser capability token.
- A versioned shutdown record or parent-pipe closure stops the listener. The
  desktop owns child shutdown; the Windows installer packages the native
  sidecar and agent client with their manifests. No Python fallback exists.

See [`sidecar_protocol.md`](sidecar_protocol.md) for the exact pipe and exit
contract.

### Agent Workflow Adapter

- The packaged `career-hub-agent` client accesses JD intake, evidence review,
  application history, Skills Registry, Resume Version, and text-workbench
  services through the running native owner, using bounded JSON commands.
- Ephemeral `analyze` does not persist a discussion-only JD. `prepare` reuses an
  exact fingerprint and stops at `Preparing`; submission recording requires an
  explicit confirmation that the external action already occurred.
- Task contexts expose hard-capped requirements and direct evidence plus an
  explicit no-forcing policy. Strict task requests carry one exact writing
  prompt or Resume Version id. Resume tailoring accepts `no_change_needed` and
  application text defaults to inline delivery.
- Purpose-specific retrieval omits Resume Digest work from assessment and
  application writing, bounds resume/registry/experience/workbench projections,
  and returns only one related prior response when explicitly identified.
- Stable context-snapshot and packet fingerprints let an orchestrator recognize
  unchanged deterministic inputs. Material writes return compact receipts
  instead of echoing the full editor packet.
- Native application-text services validate private draft writes and JD freshness.
- `.agents/skills/*/references/contract.yaml` maps each repo skill to commands,
  implementation paths, and focused tests. The developer-only
  `python -m src.agent_workflow audit` command reports
  changed skills for review without assuming that every code change requires a
  rewrite.
- These commands call no model or external service. See
  `docs/guides/agent_workflows.md` for the executable contract.

### Career Vault Source Intake

- `src.extract_vault_source` validates local paths, runs each extraction stage with a stable error code, and writes output atomically.
- `src.vault_intake` parses TXT/DOCX blocks and extracts section, date, exact
  skill mentions, source-backed archival resolver issues, and source-bullet
  candidates. Exact mentions preserve block-relative character spans.
- `src.skills_registry` supplies the same merged core/custom names and aliases
  to Resume and JD intake. Unknown source prose never becomes registry data
  automatically.
- `src.intake_observability` appends content-minimized stage timing, counters, failures, and temporary efficiency warnings to local JSONL.
- Detailed diagnostics do not promote extracted data to draft or approved Vault records.
- `src.approval_preview` creates a deterministic comparison without writing canonical data and pins both Vault and Skills Registry revisions.
- `src.approval_apply` applies only complete saved decisions, resolves selected
  existing/pending skill pills, validates the resulting Vault and atomic skill
  references in memory, writes changed Vault YAML plus private custom taxonomy
  through one rollback-capable local transaction, and records
  `approval_result.yaml`. Rejected cards and unselected Skills fields do not
  create custom vocabulary.

## Code-Level Behavior

### `src/vault_editor.py`

- Loads and validates the complete Career Vault before exposing canonical
  Profile, Experience, Project, Skill Group, School, or Certification records.
- Updates the singleton Profile without create, archive, or delete operations.
- Creates other manual records with a generated stable id and `source: manual`.
- Updates only editable fields while preserving existing metadata.
- Validates Project-to-Experience and Skill-to-evidence references across
  canonical files.
- Uses a SHA-256 per-file revision to reject stale browser forms.
- Validates the complete in-memory Vault before atomically replacing the target
  YAML file.
- Does not call AI, infer facts, or write form content to diagnostics.

The local UI supports guarded editing for the singleton Profile and manual
create, edit, archive, and restore for Experience, Projects, Skill Groups,
Schools, and Certifications. Skill groups carry optional explicit links to
Experience, Project, and Certification evidence; keyword membership alone is
not treated as proof. Schools and certifications share one Education-file
revision so stale edits across either subtype are rejected. Archived records
remain valid historical references but are omitted from new matching and
selection suggestions. Permanent delete remains deferred.

Experience, Projects, and Education expose one shared, browser-local List/Cards
preference. List preserves the side-by-side canonical editor; Cards provides a
responsive overview and opens the same editor as a full-width drill-in. This
preference changes presentation only and never enters the Career Hub Data Home.

The manual Experience and Project editors use the same exact-name/alias
resolver as resume review. Existing registry skills are stored as canonical
`competency_ids`; an unknown label remains a visible custom-skill proposal with
an explicit group until Save. The evidence record and any new private taxonomy
entries are validated and committed in one rollback-capable transaction.

Profile also surfaces a compact summary of the separate Documents ledger. The
full ledger stores DPAPI-encrypted metadata and PDF/image blobs under the local
Data Home, supports metadata edits and reversible archive state, and does not
feed resume/JD matching or content extraction. The only approved projection for
a future model context is the sorted title list from
`list_document_titles_for_model()`; the model must not receive the full ledger,
document bytes, a read-document tool, or the browser capability token.

The **Skills Registry** shown on the Skills page is the shared vocabulary
surface. It preserves groups as first-class containers and combines read-only
core groups/skills with private custom groups/skills. Each skill has one primary
group; newly generated custom ids use `user_`, and **My custom skills** is the
safe fallback. The browser exposes the same registry through a persisted List
view (two-pane master/detail) and a Cards view (responsive overview with
full-width drill-in). Both use fully clickable group surfaces, shape-and-color
Core/Custom ownership labels, atomic skill details, and custom edits. The
view preference is UI-only browser state and does not enter the Career Hub Data
Home. The older whole-group evidence editor is deprecated and no
longer rendered; legacy YAML remains readable for explicit migration while new
evidence attaches atomically to skill ids on Experience, Project, and
Certification records.

Every newly created private skill records its explicit creation channel;
interactive workflows also record a UTC creation timestamp. Legacy records
without provenance remain readable and are not backfilled during load. A core
catalog update that collides with existing private vocabulary is reconciled
only in memory with private identity taking precedence, and the registry
returns compatibility notices. New conflicting user writes still fail before
either private taxonomy file changes.

### Competency evidence, demand, and coverage projections

- `src.candidate_evidence_graph` rebuilds active Competency-to-evidence,
  Project-to-Experience, and Competency-to-domain edges only from explicit
  canonical `competency_ids` in Career Vault.
- `src.evidence_analytics` calculates evidence counts, professional contexts,
  independent projects, certifications, proof counts, first/last use,
  active-now state, and merged inclusive month intervals without a score.
- `src.jd_demand_index` independently reads validated normalized cards and
  existing application-ledger summaries. It applies an inclusive captured-date
  period, deduplicates non-empty JD fingerprints, preserves exact occurrences,
  separates required/preferred counts, and never registers a job or writes to
  Candidate Evidence.
- Older cards without `digest.skill_occurrences` use only exact active
  canonical names or approved aliases from their structured technology lists;
  classified requirement prose and mentioned-only terms do not become demand.
- `src.coverage_matrix` joins the two immutable projections using
  `supported`, `thin_evidence`, `evidence_not_found`, and `not_requested`.
  The absence contract is **Evidence not found in Career Vault.** It is not a
  claim about candidate knowledge and it does not generate a rank or score.
- `src.demand_gaps` packages those projections for the read-only **Jobs /
  Demand & coverage** view. The browser can select an inclusive ledger period,
  inspect coverage and top-demand summaries, filter the detailed requested-skill
  list by domain, required/preferred demand and coverage, and inspect the saved
  jobs contributing to one skill. The explicit **Open in Skills** action reuses
  the canonical evidence view rather than creating a second vocabulary editor.
- `src.competency_explorer` supplies a compact read-only browser payload. The
  unified **Skills** page joins that projection to every active registry skill.
  **Groups** manages vocabulary; **All skills** adds group, source, evidence,
  domain, and kind filters, shows aliases and evidence detail, and navigates to
  canonical Experience, Project, and Certification editors. The legacy
  `#competencies` route redirects to `#skills/all`.
- Registry and explorer responses expose the same effective-catalog revision.
  The browser verifies that handshake and retries crossed concurrent reads
  once. Normal **All skills** stays active-only; opening an archived registry
  skill lazily requests an explicit historical projection so existing evidence
  remains visible without pretending the skill is active.

### `src/digest_job.py` and `src/ingest_job.py`

- Accepts an existing UTF-8 job-description file under `jobs/incoming/`.
- Extracts labeled identity fields, section-scoped taxonomy technologies, responsibilities, experience ranges, education, explicit visa or clearance phrases, and explicit source-backed compensation ranges.
- Classifies otherwise-unstructured qualification lines as technical, capability, constraint, or education requirements with cleaned display text and source-line evidence; unmatched lines remain in `Needs review`.
- Stops requirement sections at known company, EEO, accommodation, and compensation boundaries while preserving those lines in the immutable source.
- Stores required and preferred technology alternatives as validated top-level `any_of` groups; flat lists do not turn every alternative into a separate requirement.
- Stores source line, source text, and detection rule for derived fields.
- Loads the unified Skills Registry, then uses its exact resolver and spaCy
  `PhraseMatcher`. Global normalized name/alias uniqueness is validated before
  digestion. It does not use a trained NLP or generative model.
- Stores every detected skill instance in `digest.skill_occurrences`, including
  the canonical id/name, exact source wording, primary group, core/custom
  origin, required/preferred context, source line, and character span. These
  occurrences are inputs for future matching and demand analytics; they do not
  assert candidate evidence.
- Calculates a normalized SHA-256 fingerprint and reopens the existing card for exact duplicate content without retaining a second raw source.
- Keeps multiple location-specific compensation ranges separate and does not infer unstated currency, period, equity, or total compensation.
- Keeps unresolved identity fields explicit and always creates `draft`, never `ready`.

### `src/application_instructions.py`

- Scans at most 500,000 characters / 10,000 lines once with bounded,
  non-catastrophic rules and returns at most 24 exact-source items.
- Recognizes only email/send destinations, exact phrase or subject requests,
  required artifacts, named question or cover-letter formats, external steps,
  and explicit deadlines. A recognized instruction section or concrete action
  is required; ordinary qualifications, responsibilities, generic **Apply
  now**, EEO, accommodation, privacy/legal, benefits, and marketing copy are
  excluded by fixtures.
- Stores the exact source wording, line range, category, confidence, stable item
  fingerprint, normalized source fingerprint, and extractor version. It does
  not call a network or AI model, follow a link, rescan a DOM, or paraphrase a
  requirement.
- Complete Discovery previews retain the same projection. Partial or link-only
  captures return **Full JD required** rather than claiming the checklist is
  empty. The extension receipt contains only a conditional item count.
- `src.application_instruction_checklist` merges detected items with immutable
  exact-selection additions and append-only user actions. A changed source
  fingerprint makes prior state stale; it never blocks Save, Applied, or any
  navigation action.

### `src/contact_candidates.py`

- Scans only a complete retained JD, with the same 500,000-character /
  10,000-line boundary, and returns at most 24 review-only candidates.
- Keeps exact stated names, roles, companies, email/phone/professional-profile
  channels, context, line range, source wording, rule, confidence, extractor
  version, normalized source fingerprint, and exact-source SHA-256. It does not
  infer missing identity fields or use network search, enrichment, or an AI
  model.
- Excludes company stories, testimonials, leadership biographies, generic
  signatures, copyright, EEO/accommodation, privacy, and legal text. Generic
  application mailboxes remain non-person `application_channel` candidates.
- Complete Discovery previews and normalized Job cards retain the exact
  projection; partial captures say **Full JD required**. The extension receipt
  contains only a conditional count.
- Extraction, Discovery promotion, dismissal, and reanalysis never create,
  merge, link, update, or archive a permanent contact. Only the browser's
  explicit source-backed confirmation may call the contact domain.

### `src/job_schema.py` and `src/validate_jobs.py`

- Check required fields, types, allowed job status, URL shape, visa-signal shape, and raw-source path safety.
- Validate optional digest version, classified requirement types and evidence,
  application-instruction versions/categories/exact evidence, source-coverage
  arithmetic, field evidence states, eligibility shape, and SHA-256 format
  while preserving compatibility with older cards.
- Require a `ready` card to have company, title, responsibilities, and at least one explicit requirement or required technology group.

### `src/application_contacts.py`

- Owns the headless local contact domain introduced with schema version 24 in
  the existing application SQLite database. It creates no separate database or
  Data Home.
- Requires a stable generated contact id plus a stated name or validated exact
  email, phone, or professional-profile channel. It never derives identity from
  a role, company, candidate name, URL neighborhood, or network result.
- Exact normalized email/profile channels can return one existing identity.
  Similar name/role/company fields return only bounded potential duplicates;
  they never auto-merge or rewrite either record.
- Contacts are optional related entities, not fields required by an
  application. One saved JD may have zero or more explicit contact links, and
  one canonical contact may be reused across jobs only through an exact-match
  confirmation.
- Create, fact update, link, unlink, archive, and restore use one immediate
  transaction and optimistic contact revision. Archived contacts are excluded
  from default selection and reject new links until restored.
- Immutable provenance keeps the source reference, exact JD hash/evidence/line
  range when explicitly supplied, and stated-field snapshot. Job/Discovery
  cascades remove only link rows; provenance stores its source ids as historical
  text and survives unlinking or source deletion.
- Exact JD-candidate provenance is globally retry-safe: the same candidate can
  return its original contact after a lost response, but cannot be attached to
  a different contact.
- The module has no extractor import, promotion hook, sending, enrichment,
  scraping, relationship score, or cloud call. `src/run_intake_ui.py` owns the
  separate browser-only adapter: it reloads the current retained source,
  re-extracts the exact candidate, rejects stale hashes/ids, and exposes only an
  explicit create/link confirmation plus ledger operations.

### `src/application_helper.py`

- Owns the revisioned `career_vault/application_helper.yaml` privacy control.
  Its fixed catalog maps canonical helper ids to exact existing Profile,
  contact, education, or eligibility paths; configuration cannot introduce a
  path or value.
- Defaults to no enabled Profile fields. The browser configuration response
  contains labels and enablement only, while the attended purpose projection
  returns only enabled, present typed values, active Resume Version ids/display
  labels, and minimized explicitly saved reusable answers. Disabled and empty
  Profile fields are omitted; resume bytes and private filenames are excluded.
- The local server issues an application-helper capability distinct from the
  capture capability and enforces independent fixed route/method allowlists in
  both directions. Neither token can become an arbitrary Career Vault,
  document, resume, application, or diagnostics reader.
- Profile values are computed on demand and are not written to logs, URLs,
  diagnostics, extension storage, or fixtures. The side panel masks previews,
  revalidates before one explicit field Copy, and clears its in-memory
  projection on close. Resume opening uses a fixed deep link after a fresh
  shelf check; the browser-only Resume Library route owns verified download.
- Reviewed Profile fill is a separate two-action boundary. Review injects a
  bounded main-frame-only inspector and returns labels plus empty state without
  values. The worker retains only an expiring one-shot token, exact tab/URL,
  Profile revision, and opaque target metadata in memory. Fill accepts selected
  mapping ids only, re-fetches the projection, and revalidates every
  page/form/empty control before inserting short text, textarea, select, or
  yes/no radio values atomically.
- `career_vault/application_answers.yaml` is lazy, private, Git-ignored, and
  independently revisioned. Find reads bounded completed safe short answers
  only after an explicit action; Save re-reads one reviewed candidate before a
  local write. Review/Fill uses exact normalized question matching and a
  one-shot memory token. Credentials, financial/demographic/eligibility data,
  compensation, uploads, consent, job-specific and open-ended prompts,
  non-empty controls, and unattended activity remain excluded. Neither flow
  clicks navigation or submission controls.

### `src/application_history.py`

- Uses the standard-library sqlite3 module and stores private local state in
  `<active-data-root>/applications/career_hub.sqlite3`.
- Registers every valid normalized JD as one local ledger record with initial
  status captured (shown as Saved) and refreshes its snapshot without replacing
  status history, notes, resume data, or plans.
- Stores status changes as append-only events with occurred and recorded UTC
  timestamps; repeating the current status without a note is a no-op.
- Stores one cached deterministic assessment artifact per job. Its validity
  keys are the complete normalized card, analyzer version, active evidence and
  visa facts used by assessment, the revisioned hard-blocker policy, assessment
  data catalogs, the evaluation month, and an explicit assessment version.
- Invalidates the cached artifact if a JD fingerprint or relevant assessment projection changes. The next targeted
  job or application request computes and saves a replacement synchronously;
  unchanged inputs return the stored artifact without a full Vault scan.
- Records user-corrected JD facts as explicit normalized-card
  `manual_overrides`. Identity, eligibility, requirement, and contextual skill
  corrections remain effective after reanalysis and therefore produce a new
  assessment fingerprint without pretending to be parser provenance.
- Stores uploaded resume bytes once under the private `applications/resumes/library`
  directory as immutable versions. Each application may select a version; no
  selection is the implicit default at every stage. Its first Applied update—or
  a direct Interview/Offer entry after a missed Applied transition—snapshots that
  selection for history without inventing an Applied event.
- SQLite schema version 4 adds the assessment artifact, schema version 5 adds
  the resume library and current Applied-resume snapshot fields, and schema
  version 6 adds append-only Applied-resume history. Resume Plan JSON begins at schema version 3.
  Schema version 19 adds explicit Discovery content-verification time and
  original-scope provenance; page/selection capture remains unverified until a
  browser-local confirmation, while older trusted `confirmed_full` records are
  preserved during migration.
  Schema version 21 adds job-bound attended application sessions and minimal
  idempotent submission-signal events.
  Schema version 22 adds immutable manual application-instruction evidence and
  append-only reversible checklist actions.
  Schema version 23 adds revisioned application contacts, exact channels,
  append-only provenance, and job/discovery links. Schema version 24 adds the
  partial uniqueness index on exact candidate provenance. Schema version 25 adds
  optimistic revisions to email application outcomes. Schema version 26 adds
  reusable answer families, aliases, immutable revisions, representations, and
  append-only use events. Schema version 27 adds subject routing policies to email
  sender rules and subject routes to message receipts.
  Draft resume plans may be incomplete; approval requires a complete, current,
  source-valid selection.
- Keeps legacy decision and start operations available for old local records and
  CLI compatibility, but the browser does not use them.
- Registers valid normalized cards in one transaction when the state endpoint
  loads. Add job returns success only after its ledger record and assessment
  have been stored.
- The browser treats Job Workspace as the one-JD review and tailoring surface and
  Applications as the ledger for every saved JD. The ledger filters summaries
  client-side, saves status changes inline, and requests one full history plus
  assessment only when its side drawer opens.
- Uses parameterized queries, foreign keys, focused indexes, and PRAGMA
  user_version without an ORM or new dependency.

### `src/application_sessions.py`

- Canonicalizes the current posting URL and either claims an explicit pending
  session or creates one only when exactly one Saved/Preparing application has
  the same canonical source URL. No match writes nothing; multiple exact
  matches fail closed.
- The extension route accepts no Job id, company, title, contact, or arbitrary
  next route. **Create tracking link** remains a browser-owned fallback.
- Binds one opaque, four-hour session to one tab and HTTP(S) Origin. SQLite
  stores only the per-session token hash; transient extension state uses only
  `chrome.storage.session`.
- Accepts only a trusted-native-submit timestamp paired within 15 minutes with
  one enumerated visible success rule. The event stores no DOM, page text,
  field value, resume content, cookie, credential, or screenshot.
- Exposes `submission_detected` as a browser review queue. It never changes the
  application stage itself; explicit **Mark Applied** reuses the existing
  ledger status transition, while dismissal/interruption/expiry leaves the
  stage unchanged.
- Keeps extension access on three fixed POST routes behind both the configured
  extension Origin and app-wide extension capability. The per-session
  capability adds tab/Origin isolation and cannot read unrelated application
  data.

### `src/review_job.py`

- Builds an in-memory evidence index from experience, project, certification, and skill YAML.
- `normalize_text()` applies Unicode-aware `casefold()`, replaces non-word characters with spaces using regex, collapses whitespace, and preserves words.
- `find_evidence()` searches for the entire normalized requirement as a contiguous phrase with artificial spaces around both the needle and haystack.
- Matching starts with exact phrase containment after normalization. Reviewed exact aliases in `src/data/requirement_concepts.yaml` can expand a requirement into explicit evidence terms. It is not semantic similarity.
- The technology catalogs (`src/data/job_technologies.yaml` shared, `career_vault/taxonomy/technologies.yaml` user overlay) merge with overlay precedence: on id conflicts the overlay's name and category win and the shared name stays as an alias. A taxonomy `parent` field builds skill families; `build_requirement_expansions()` rolls child evidence up to parent requirements.
- `experience_durations()` parses experience-block `dates` ranges into month counts so years-of-experience requirements get a derived, advisory answer (`derived_match` / `derived_shortfall`) instead of always staying manual.
- Experience, project, and certification matches are direct evidence. Skill-family matches are separate taxonomy hints and never satisfy requirements.
- Current private eligibility facts and the revisioned
  `career_vault/hard_blocker_policy.yaml` classify only exact citizenship/work
  authorization, required active-clearance, and verified comparable-duration
  conflicts. Missing facts, missing exact JD evidence, or disabled classes
  produce structured Review findings instead. A stale saved digest cannot
  override current Career Vault facts.
- Every clear conflict carries the exact JD excerpt and exact confirmed profile
  statement. Eligibility findings remain advisory and never enforce workflow
  restrictions.
- Missing required skills, platforms, capabilities, and technology groups become requirement gaps; `evidence_not_found` is a matcher result, not a claim about the candidate.
- The review contract always keeps `application_allowed: true`; prioritization never prevents the candidate from proceeding.
- Missing other required evidence and education checks remain manual review.
- Lane selection is a fixed `if/elif` decision tree.

The policy and eligibility editors are browser-only local APIs with optimistic
revisions. They are intentionally absent from the fixed extension route/method
allowlist, so the extension receives only the already bounded server-issued
assessment receipt.

There is no stemming, lemmatization, fuzzy matching, BM25, embeddings, vector database, trained NLP model, NLM, or LLM in this code path. The reviewed concept YAML is a small explicit alias bridge, not a learned model.

Known consequence: `Python` does not automatically satisfy `general-purpose programming language`, and differently worded capabilities can become false negatives. Taxonomy hints can help a later reviewer find candidates, but only direct block matches are promoted to evidence.

### `src/build_model_context.py`

- Regenerates the deterministic review for one ready job card.
- Writes one requirement row for every required, preferred, experience, education, and classified condition.
- Copies full source content only for direct evidence ids already present in the review.
- Keeps taxonomy skill ids separate and excludes their related-experience fan-out.
- Includes non-contact candidate facts, unresolved decisions, and four applicable style-rule sections.
- Does not read raw CV files, previous resumes, or raw job prose and does not call an AI API.

### `src/resume_plan.py`

- Builds an editable plan from active Career Vault experience, project, skill,
  school, certification, and proof records.
- Keeps every active experience visible with Exclude as the safe default; Include
  is explicit and exclusion notes are optional.
- Derives compact/full presentation from the 2-5 bullet target and enforces proof
  ownership without limiting the larger candidate-evidence pool or rewriting claims.
- Uses deterministic proof ids for project claims and stable canonical ids for
  all other selections.
- Fingerprints the relevant Vault files and the normalized JD so stale plans
  cannot be silently approved.
- Allows incomplete drafts but approves only a plan with no validation issues.
- Uses optimistic SQLite revisions to reject stale saves from another tab.
- Does not call an AI API and does not itself render DOCX or PDF.

The browser opens this editor from any persisted job/application record. Section order,
experience decisions, proof points, projects, skill keywords, education, and
one/two-page intent are persistent application-owned state rather than browser
memory.

### `src/resume_materials.py` and `src/application_materials.py`

- Build typed, job-scoped drafts from current job, profile, evidence, and Resume
  Plan state without mutating Career Vault or immutable Resume Versions.
- Use optimistic file revisions and source fingerprints to reject stale writes.
- Keep resume sections, record metadata, skills, education, and layout code-owned;
  application-only summary and bullet wording retains selected proof references.
- Render DOCX, PDF, and UTF-8 TXT locally from one content model and validate
  structural invariants before returning an artifact.
- Never call an AI API, attach a file externally, submit an application, or set
  application status to Applied.

### Resume And Package Validation

- `src/validate_resume_versions.py` validates base-resume metadata and Vault references.
- `src/application_package_schema.py` requires every experience to be selected or excluded explicitly.
- Selected experience is marked `full` or `compact`, linked to owned proof ids, and assigned an exact Markdown heading and target bullet count.
- The validator scans Markdown lines under each heading and counts `- ` bullets until the next heading.
- It validates structure and traceability, not editorial quality or visual page layout.

## What Codex Currently Does

Codex consumes the bounded agent packet by default and opens an exact source
artifact only for an unresolved fact identified by that packet. Within that
boundary it still supplies judgment to:

- classify ambiguous job language;
- interpret alternatives and synonyms;
- compare adjacent experience;
- rank experience and projects;
- write inclusion and exclusion reasons;
- draft summaries and bullets;
- identify unsupported claims and application questions.

Language judgment remains the main variable cost. Reopening full raw files or
unrelated prior application text is an exception rather than the default.

## Token-Efficient Direction

The goal is not to remove judgment. It is to make code prepare a small, deterministic context so the model only handles ambiguity and writing.

Current implementation:

1. `src.build_model_context` produces a requirement matrix, selection worksheet, and bounded model-facing bundle from deterministic review output.
2. Requirement concepts are calibrated against saved JD families without fuzzy or generative matching.
3. Every experience remains visible in compact form while full proof content is limited to six ranked blocks.
4. Generated context records approximate size, explicit budgets, shortlist counts, reusable snapshot identity, and a packet fingerprint.
5. A strict task request carries one exact prompt/limit set or target Resume Version without relying on ambient conversation reconstruction.
6. Assessment and application writing skip Resume Digest derivation; resume comparison bounds it to the relevant Resume Version projection.
7. Application-material mutations return compact validation/revision receipts.

Recommended next sequence:

1. Reuse approved bullets verbatim when they already cover the requirement; ask the model to rewrite only unmatched or weak sections.
2. Persist an optional snapshot cache only after measured repeated-task hit rates justify the invalidation complexity.
3. Route small-model tasks to only the generated bundle and typed task request, and track budget exceedance without logging private content.

The model should not reread the complete raw CV, all proof blocks, every old resume, and the full job description once these deterministic artifacts exist.

The ordered implementation tickets are tracked in docs/product/optimization_backlog.md.

## Appropriate Use Of Regex And Language Models

Regex is appropriate for ids, whitespace normalization, stable headings, dates with known formats, and exact boundary checks. It is not reliable for understanding arbitrary job prose, nested alternatives, implied requirements, or equivalent technologies.

For the MVP, prefer explicit YAML structures and a small reviewed alias map before adding an NLP library or embeddings. Later, one constrained model call can resolve genuinely ambiguous language after cheap deterministic filtering. This keeps the project local-first, explainable, and inexpensive.
