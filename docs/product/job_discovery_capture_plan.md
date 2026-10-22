# Job Discovery Capture Plan

This is the ordered implementation plan for a user-triggered browser extension
and job-alert email intake. It is deliberately separate from website watching,
scraping, browser automation, and auto-apply.

The [Capability Acceptance Registry](../process/acceptance_registry.md) remains the source
of truth for implementation status. Every capability in this document remains
**Planned** until its own acceptance evidence is complete.

## Outcome

Career Hub should have one local **Discovery Inbox** behind the user-facing Job
Feed. Discovery producers are:

1. explicitly imported or locally synced job-alert email;
2. future precision watchers over configured public sources; and
3. explicit manual link/JD capture.

The browser extension and public link resolver primarily enrich those records.
A standalone extension capture becomes a manual producer only when the user
explicitly chooses **Add to Job Feed**.

A discovery is not an application. It must not enter the application ledger,
Demand & coverage, or saved-job analytics until the user explicitly promotes
it to **Saved job** or **Applied**. Promotion must reuse the existing immutable
JD intake, normalized card, application ledger, and deterministic assessment.

## Scope

### In scope

- A shared Discovery Inbox, storage contract, duplicate handling, and promotion.
- Three extension capture modes:
  - save the current URL only;
  - save explicitly selected text; and
  - capture the visible job page after a click.
- Immediate advisory assessment for a usable page capture, using the existing
  deterministic analyzer and evaluator.
- A compact, source-backed hard-blocker summary for complete captures; no new
  numeric extension score.
- A later attended application-helper track: explicit quick copy first, then
  reviewed field-by-field prefill without submission.
- Explicit `.eml` import without mailbox credentials.
- A recommended dedicated-mailbox workflow and a separately approved local
  pull connector for one actual mail provider.
- Local scheduling for that connector after manual sync is reliable.

### Out of scope

- Recurring website crawling or scraping (`CAP-JOB-WATCHING`).
- Selenium, ChromeDriver, Playwright-driven unattended browsing, CAPTCHA
  handling, login automation, or anti-bot evasion.
- Following protected email links in the background.
- Searching the web for an email candidate whose link is inaccessible.
- Auto-apply, automatic submission, unattended/bulk form filling, credential
  access, or background page modification. The later attended helper remains a
  separately reviewed, user-triggered capability.
- Cloud mail webhooks, hosted queues, or a continuously running cloud service.
- Automatic Skills Registry writes from a page or email.
- AI API calls.

If an email link requires LinkedIn, Handshake, or another logged-in page, the
MVP keeps a link-only discovery. The user opens it in the normal browser and
uses the extension on that visible page. Public-ATS resolution and exact-title
web search remain a later, separately reviewed resolver capability.

## Where To Start

The selected delivery direction is email first. Start with the pure `.eml`
parser because it is read-only, requires no account, credential, database,
network, or provider registration, and produces fixtures that the later mailbox
connector must continue to satisfy.

Recommended first implementation goal: **`MAIL-100`**.

Then implement `DISC-100` and `MAIL-110`. The first useful product slice is:

```text
synthetic .eml
  -> deterministic candidate extraction
  -> Discovery Inbox
  -> Needs JD or Ready to review
```

The shared Discovery Inbox still precedes any live mailbox connector. Email and
extension captures need the same identity, provenance, deduplication,
assessment, and promotion behavior; a provider-specific inbox must not become a
second job-intake implementation.

## Fixed Architecture Decisions

Implementation tickets must not reopen these decisions without new evidence and
a separate architecture review.

1. Use one Discovery Inbox for extension and email candidates.
2. Store mutable discovery metadata in the existing
   `applications/career_hub.sqlite3` database. Do not introduce a second
   database.
3. Store retained capture text below `jobs/discovery/` in the selected private
   Data Home. Never write captured content into the Git checkout in installed
   mode.
4. Keep discoveries outside `job_applications` until explicit promotion.
5. Reuse the existing JD analyzer, evaluator, and `create_job_intake` behavior.
   Do not copy extraction, matching, vocabulary, or scoring rules into an
   extension or email parser.
6. A page capture may receive a **Discovery preview assessment**. It is not a
   new numeric score and it does not affect analytics.
7. URL-only captures have no assessment. Selection captures are partial unless
   the user explicitly confirms that the selection is the complete JD. Missing
   content must never be presented as a candidate gap.
8. Use Chrome Manifest V3 with packaged JavaScript only. Start with plain
   HTML/CSS/JavaScript; do not add React, Vite, CRXJS, or another Node toolchain
   for the initial extension.
9. Extension permissions are limited to `activeTab`, `scripting`, and the exact
   loopback host permission needed to call Career Hub. Do not request
   `<all_urls>`, cookies, history, downloads, web request interception, or
   persistent access to job-board domains.
10. The extension acts only after a click. It does not run a persistent content
    script or background crawl.
11. The extension token remains in memory and is reacquired from `/api/session`.
    It is never placed in extension storage, logs, URLs, or build artifacts.
12. Before real extension dogfood, restrict the extension capability token to an
    explicit endpoint/method allowlist. A distinct token alone is not sufficient
    least privilege.
13. Parse `.eml` with the Python standard library first. No mailbox wrapper or
    provider SDK is required to validate the product flow.
14. For private dogfood, recommend a separate mailbox used only for job alerts.
    Connecting a personal mailbox is an advanced, non-default path because even
    if code selects one label, the credential can authorize broader mailbox
    access.
15. The no-GCP Gmail dogfood path is local IMAP over TLS with a revocable app
    password, only when that dedicated personal Gmail account is eligible. The
    app password is stored in Windows Credential Manager; Career Hub never
    stores the main Google password and never enables SMTP/send behavior.
16. Gmail Pub/Sub and Microsoft Graph webhooks are not the local-first default:
    they add cloud infrastructure or a public HTTPS callback. The first
    connector is pull-based and runs locally.
17. Unknown skill phrases remain review items. They never become core or custom
    vocabulary automatically, and a job requirement never becomes candidate
    evidence.
18. Email snippets may produce an explicit blocker but cannot establish a
    complete fit. A high-priority todo requires a complete JD and the existing
    deterministic assessment.
19. Capture and ledger mutation are two stages. The extension first creates or
    enriches one discovery; Save job and Record as applied happen only after
    the retained JD is reviewed in the local Career Hub UI.
20. The compact extension result never introduces a fit percentage. It may show
    only the narrow hard-blocker policy defined below, with exact JD evidence
    and the confirmed profile fact used for comparison. Ambiguity is Review,
    not Clear conflict.
21. Keep the capture popup focused. The longer-lived application helper belongs
    in a separate side panel after its own capability review; it must not turn
    the capture popup into a miniature Career Hub.
22. Quick copy precedes prefill. Helper values come from an explicit,
    user-curated local field set, are never stored by the extension, and are
    released through a purpose-specific endpoint rather than general Career
    Vault access.
23. Prefill is always initiated on the current page, never overwrites a
    non-empty field without review, and never clicks Submit. Passwords, one-time
    codes, SSNs, financial data, and voluntary demographic/disability/veteran
    answers are outside the helper contract.
24. A hard-blocker label is advisory, not an enforcement state. It never
    disables **Review later**, **Save job**, **Record as applied**, **Open
    posting**, or the user's ability to continue; the UI offers a clear
    **Continue / accept risk** path and preserves that acknowledgement.
25. Extra application instructions are a separate source-backed checklist, not
    fit blockers. Detect them with one bounded deterministic scan of the
    captured JD, never a network or AI call, and never claim that no special
    instruction exists merely because the scanner found none.
26. A person name in a JD is not automatically a contact. Extraction creates a
    reviewable **Contact candidate** only when the name is tied to explicit
    application/recruiting context or a direct contact channel; permanent
    contact creation always requires user confirmation.
27. Public URL resolution is a separate attended enrichment capability. It may
    fetch only the already-retained URL of one active link-only Discovery after
    an explicit browser action, must pin each connection to a validated public
    address while preserving TLS hostname checks, and always returns
    unverified `page_visible` content. The full contract and one-ticket order
    live in [`LINK-000`](../adr/LINK-000_public_job_link_resolver.md).

Official implementation references:

- [Chrome Manifest V3](https://developer.chrome.com/docs/extensions/develop/migrate/what-is-mv3)
- [Chrome `activeTab`](https://developer.chrome.com/docs/extensions/develop/concepts/activeTab)
- [Chrome `scripting`](https://developer.chrome.com/docs/extensions/reference/api/scripting)
- [Python `email.parser`](https://docs.python.org/3/library/email.parser.html)
- [Python `imaplib`](https://docs.python.org/3/library/imaplib.html)
- [Gmail selective forwarding](https://support.google.com/mail/answer/10957)
- [Gmail app-password limitations](https://support.google.com/accounts/answer/185833)
- [Gmail IMAP behavior](https://support.google.com/mail/answer/7126229)
- [Gmail push prerequisites](https://developers.google.com/workspace/gmail/api/guides/push)
- [Microsoft Graph webhook requirements](https://learn.microsoft.com/en-us/graph/change-notifications-delivery-webhooks)

## User Journeys

### Extension: choose the destination explicitly

Extension invocation does not imply `Applied` or `Skipped`.

1. If the posting already exists in Job Feed, URL/posting identity merges the
   capture into that discovery and adds extension provenance.
2. Every extension capture first creates or enriches a discovery. The current
   popup exposes **Save page**, **Save selection**, and **Save link only**;
   link-only produces **Needs JD**, while text captures remain reviewable.
3. Link-only and partial selection captures cannot proceed to the ledger. A
   usable page capture or a selection explicitly confirmed as the complete JD
   is required before either ledger path is offered.
4. After complete capture, the popup shows a bounded capture receipt and offers
   two next-step intents: **Review later** and **Review & record applied**. Both
   open the local discovery review; neither changes application state inside
   the extension.
5. The local review shows the exact retained JD, extracted identity, capture
   scope, provenance, and narrow hard-blocker summary. Only after the user
   verifies that source does Career Hub offer **Save job** or **Record as
   applied** through the existing promotion path.
6. The extension sends only one bounded capture request. The server owns merge,
   intake, assessment, and any explicit ledger transition.
7. Closing the extension does nothing. `Skipped`/`Dismissed` is never inferred
   from a click or close action.

### Extension: capture current page

1. The user opens a job in their normal logged-in Chrome profile.
2. The user clicks the Career Hub extension.
3. The popup shows the current title/domain and three actions.
4. **Save page** extracts bounded visible job text and structured `JobPosting`
   data when present, then sends one capture payload to loopback.
5. Career Hub merges an existing discovery, computes an assessment when
   applicable, and returns a bounded receipt: identity, scope, character count,
   readiness, and hard-blocker count/status.
6. The popup shows the receipt and opens the discovery in Career Hub for full
   source verification and the selected local next step.

### Extension: save selection

1. The user selects the exact job text they want to retain.
2. The popup enables **Save selection** and shows its character count.
3. Career Hub stores it as `selection`, visibly labels it partial, and does not
   turn absent requirements into gaps.
4. The user can later replace it with a page capture or confirm it as a complete
   JD before preview assessment and promotion.

### Extension: save link only

1. The user selects **Save link only** and **Add to Job Feed**.
2. Career Hub stores URL, page title, observed time, and provenance.
3. The discovery is marked **Needs job description** and has no assessment.

### Extension: hard-blocker receipt

1. Link-only and partial captures show **Assessment unavailable** rather than a
   compatibility conclusion.
2. Complete captures reuse the server-owned deterministic assessment and the
   high-accuracy blocker policy below. The extension does not implement its own
   matching rules.
3. The compact result is one of **No clear conflict found**, **Review needed**,
   or **Clear conflict found**. Every conflict links to exact JD wording and the
   confirmed local profile fact used for comparison.
4. Skills, title similarity, general seniority, and location may inform the
   full Career Hub review, but they do not become extension hard blockers.
5. Every result keeps **Continue / accept risk** available. A warning may be
   acknowledged and recorded, but it never prevents capture, promotion, or an
   application status transition.
6. Matching/fit ranking remains a later product decision and must not be hidden
   inside this summary.

### Extension: extra application instructions

1. Complete page capture runs one bounded, local, deterministic scan after JD
   extraction. It performs no network request, navigation, model call, or
   second pass over unrelated page content.
2. High-confidence candidates require either a recognized section heading such
   as **How to apply**, **Application instructions**, **To apply**, or **Next
   steps**, or an imperative phrase with a concrete action/target. Initial
   categories are:
   - send or email materials to an explicit destination;
   - include an exact phrase/code word or subject line;
   - provide a video, portfolio, writing/code sample, repository, or other
     named artifact;
   - answer a named question or use a specified cover-letter format; and
   - complete an explicit external step or stated deadline.
3. The scanner prioritizes the application-instruction section and terminal JD
   lines, while still recognizing a clearly headed section elsewhere. It
   excludes EEO/accommodation/privacy/legal boilerplate, benefits, company
   marketing, generic application encouragement, and ordinary qualification
   verbs.
4. Each result keeps the exact source line/range, normalized category, and
   `explicit` or `review` state. It never invents an action from a paraphrase.
5. The popup shows only **Extra application step detected** plus a count. The
   local review shows the exact checklist and lets the user mark each item
   **Done**, **Not applicable**, or return it to **Pending**.
6. No finding blocks application. **No explicit extra step detected** means
   only that the deterministic rules found none; it is not a guarantee.
7. The user may add a missed checklist item from exact selected JD text. A
   dismissed false positive and a completed item remain tied to the current
   content fingerprint so changed JD text is reviewed again.

### Extension: contact candidates

1. Contact extraction reuses the same captured JD and source evidence; it does
   not search the web, inspect social networks, or infer an email address.
2. A high-confidence candidate requires a visible name plus at least one of:
   - an adjacent recruiter, talent-acquisition, hiring-manager, or explicit
     application-contact role;
   - an explicit **send/apply/email/contact** instruction naming that person;
     or
   - a visible direct email, phone number, or professional-profile link tied to
     that name.
3. A named manager in a clear **reports to / hiring team** sentence may appear
   as **Possible contact — review**, but never auto-saves. Executives, quoted
   customers, authors, legal/privacy officers, accommodation contacts, and
   names in generic company copy are excluded unless the application context is
   explicit.
4. Generic mailboxes such as `careers@` or `jobs@` are stored as application
   channels, not people. A name without a role/channel/context is ignored.
5. The local review shows name, stated role, company, available channel,
   context kind, and exact JD evidence. **Save contact** is an explicit action;
   closing or promoting the discovery never creates one.
6. Exact normalized email or professional-profile URL may support deduplication.
   Name + company alone is only a potential-duplicate suggestion and never an
   automatic merge.

### Application helper: quick copy

1. The capture popup offers a quiet **Open application helper** entry after the
   capture workflow is stable.
2. A separate side panel lists only user-enabled canonical fields such as name,
   email, phone, location, LinkedIn, GitHub, portfolio, and reviewed
   work-authorization answers.
3. One click copies one value. The panel shows which local field supplied it;
   it never reads the current form merely to copy data.
4. Values are fetched on demand from a narrow local endpoint and remain out of
   extension storage, logs, URLs, and diagnostics.

### Application helper: explicit prefill

1. The user opens the helper on one application page and chooses **Review
   fields**.
2. Career Hub detects only supported visible empty controls and displays the
   proposed field-to-value mapping before writing anything.
3. The user can deselect individual proposals and then explicitly chooses
   **Fill selected fields**. Existing non-empty values remain untouched unless
   individually approved.
4. The helper reports filled, skipped, and unsupported controls. It never
   submits the application, uploads a document, answers free text, or handles
   credentials/one-time codes.

### Email: explicit import

1. The user saves or exports one or more job-alert messages as `.eml`.
2. In Discovery Inbox, **Import alert emails** accepts the files.
3. The local parser ignores attachments and extracts one or more candidate
   blocks, visible link labels, source URLs, sender domain, and received time.
4. Exact duplicates add provenance/last-seen information instead of new cards.
5. Link-only candidates remain **Needs job description**. The user opens one and
   completes it with the extension.

### Email: recommended dedicated Gmail setup

1. The user creates a separate personal Gmail address used only as an alert
   sink, using a neutral and non-identifying account name.
2. A job board uses that address directly only when it exposes a separate alert
   destination. Do not replace the user's recruiter/contact address merely to
   route alerts; recruiter messages and account recovery must remain visible to
   the user.
3. Otherwise, alerts arriving in the personal mailbox are forwarded by filters
   that match the exact alert sender and, where useful, a subject pattern after
   forwarding-address verification. Do not forward the entire personal inbox.
4. The dedicated account uses a unique password, two-step verification, and a
   recovery address. It contains no personal correspondence or files.
5. If Google exposes App Passwords for that account, the user creates one only
   for Career Hub. Career Hub stores it in Windows Credential Manager.
6. Career Hub connects to `imap.gmail.com:993` with TLS, reads unseen messages
   without marking them read, and never connects to SMTP.
7. The user approves exact senders or sender domains. Unknown senders remain in
   a small source-review queue and do not create job todos automatically.
8. Sync runs manually first, then on app start, then optionally as a one-shot
   Windows Task Scheduler job after the same flow is proven reliable.

### Promotion

1. The user reviews the source, title/company, capture scope, and preview.
2. **Promote** offers **Save job** and **Record as applied**.
3. The service calls the existing immutable JD intake with a stable
   discovery-derived idempotency key.
4. Only after intake, ledger registration, and assessment succeed does the
   discovery become `promoted` and retain `promoted_job_id`.
5. A retry returns the same job id and never creates a second source/card.

## Architecture

```text
Chrome active tab                     Explicit .eml files
  -> extension capture client           -> local email parser
              \                         /
               -> bounded capture API <-
                         |
                         v
                 Discovery domain service
                  - identity and dedupe
                  - provenance/observations
                  - source snapshot
                  - preview assessment
                         |
                         v
                   Discovery Inbox UI
                         |
                  explicit promotion
                         |
                         v
          existing JD intake -> card -> ledger -> assessment
                         |
                         v
                 Demand & coverage analytics
```

Route handlers, extension code, and email parsers remain adapters. Identity,
merge, state transition, assessment eligibility, and promotion belong in one
Python domain module.

## Discovery Contract

### Lifecycle state

Use one small primary state machine:

- `inbox`: newly captured and not yet decided;
- `shortlisted`: user marked it worth pursuing;
- `dismissed`: hidden from the default inbox but recoverable; and
- `promoted`: linked to one immutable saved job/application record.

Do not add archive, delete, snooze, pipeline, or application statuses to this
model. Application state remains owned by the application ledger.

### Content scope

- `link_only`: URL/title metadata only;
- `selection`: explicit user-selected text;
- `page_visible`: bounded visible job-page text; and
- `confirmed_full`: selection or corrected text explicitly confirmed complete.

Derived UI readiness may be `needs_content`, `needs_review`, or `ready`, but it
should not become another persisted user-controlled lifecycle.

### Suggested SQLite records

`job_discoveries`:

- `discovery_id` stable UUID/opaque id;
- `state`;
- `title`, `company`, `location` as best available source values;
- `observed_url` and `canonical_url`;
- `posting_identity` when an exact source posting id is available;
- `content_scope`;
- `source_relative_path`, `content_sha256`, and `content_chars` when text exists;
- `first_seen_at`, `last_seen_at`, and `updated_at`;
- nullable `promoted_job_id` and `promoted_at`;
- integer `revision` for stale-editor protection.

`job_discovery_observations`:

- append-only `observation_id`;
- `discovery_id` foreign key;
- `source_kind`: `extension`, `email_file`, or a future approved connector;
- `source_channel`: `link`, `selection`, `page`, or named alert adapter;
- client request/message fingerprint for idempotency;
- observed URL, observed time, and content fingerprint;
- content-minimized extractor/parser metadata; and
- `recorded_at` from server time.

`job_discovery_field_overrides`:

- one nullable title, company, and location correction set per discovery;
- a separate integer revision for optimistic correction edits;
- created/updated timestamps; and
- a retained all-null row after clearing, so a stale editor cannot exploit a
  reset revision. Effective fields read the override when present and otherwise
  use the latest extracted value from `job_discoveries`.

`job_discovery_assessments`:

- one current versioned preview per discovery;
- `content_sha256`, analyzer/evaluator versions, candidate revision, and
  assessment-context revision;
- `scope`: only `page_visible` or `confirmed_full`;
- assessment JSON and computed time.

### Derived instruction and contact data

Extra instructions and contact candidates are versioned derivations of retained
source, not new discovery lifecycle states and not Skills Registry data.

Each `application_instruction` value contains only:

- stable fingerprint from normalized exact source text plus category;
- category such as `email_materials`, `exact_phrase`, `required_artifact`,
  `question_or_format`, `external_step`, or `deadline`;
- `explicit` or `review` detection state;
- exact source text and bounded line/range evidence;
- current `content_sha256` and extractor version; and
- no inferred completion state.

On promotion, the source-backed instruction list becomes part of the saved JD
analysis. Mutable user checklist actions belong in the application ledger, not
the immutable JD card. Store append-only events keyed by `job_id`, instruction
fingerprint, action (`done`, `not_applicable`, or `pending`), timestamp, and an
optional bounded note. A changed source fingerprint makes the prior action
visibly stale instead of silently applying it to new wording.

Each `contact_candidate` contains only:

- derived candidate id and current `content_sha256`;
- exact stated name, role, company, and available channel values;
- context kind such as `application_recipient`, `recruiter`, `hiring_manager`,
  or `possible_team_contact`;
- `explicit` or `review` state; and
- exact source evidence plus extractor version.

Candidates remain inside the versioned discovery/job analysis until the user
selects and explicitly confirms **Save contact**. `CONTACT-090` provides the
small canonical contact and many-to-many source-link domain; `CONTACT-100`
provides current-source revalidation, editable confirmation, explicit
create/link choice, and the compact ledger. Both preserve exact provenance and
use only exact email/profile identity for deduplication; name/company similarity
is review-only.

The schema migration belongs to the existing application SQLite schema owner so
backup-before-migration and `PRAGMA user_version` remain consistent.

### Source files

Use paths similar to:

```text
jobs/discovery/captures/<discovery-id>/source.txt
```

Writes must use the existing containment boundary and atomic replacement
pattern. A discovery source is private user data, not a packaged resource.

For email, retain extracted candidate text and the source URL. By default do
not retain recipients, full transport headers, tracking pixels, attachments, or
the original MIME message after a successful parse. A future explicit
**Retain original email for troubleshooting** option may store the `.eml` under
the private Data Home with a clear unencrypted-backup warning.

### Duplicate policy

Apply conservative identity in this order:

1. exact source-specific posting id;
2. normalized canonical HTTP/HTTPS URL;
3. exact normalized content SHA-256 when neither posting id nor URL exists;
4. exact title + company + location only as a potential-duplicate suggestion,
   never an automatic merge.

Preserve the observed URL even when a canonical form is available. Remove URL
fragments and known tracking parameters conservatively; do not discard an
unknown query parameter that may contain the posting id.

An email link-only observation and a later extension page capture with the same
identity update one discovery. The later complete source increases content
quality; it does not erase earlier provenance.

## Capture API Contract

The planned extension endpoint is:

```text
POST /api/discoveries/captures
```

Version 1 payload:

```json
{
  "contract_version": 1,
  "request_id": "uuid-created-on-one-user-action",
  "capture_kind": "page",
  "destination": "feed",
  "observed_url": "https://example.com/jobs/123",
  "page_title": "Implementation Engineer | Example",
  "captured_at_client": "2026-08-12T12:00:00Z",
  "source_text": "",
  "structured": {
    "title": "",
    "company": "",
    "location": "",
    "posting_id": ""
  },
  "extractor": {
    "version": 2,
    "strategy": "ranked_container"
  }
}
```

Rules:

- Accept only HTTP/HTTPS observed URLs.
- Link and selection captures use extractor version 1. Legacy page extractor
  version 1 remains accepted; current page captures use version 2 and its
  provider/ranked/guarded strategies.
- Reuse the current 1 MiB JSON request boundary and 500,000-character JD text
  boundary unless measurement justifies a smaller documented limit.
- Reject unknown fields and unsupported contract versions.
- Use server time for canonical `recorded_at`; client time is advisory.
- Do not accept raw HTML, cookies, request headers, local-storage data, form
  values, or screenshots.
- `request_id` makes a retry of one click idempotent. Semantic duplicate rules
  remain separate.
- Extension capture accepts only `destination=feed`. Complete content changes
  readiness and preview eligibility, but it does not bypass source review or
  mutate the application ledger.
- **Save job** and **Record as applied** remain explicit local-UI promotion
  actions. `record_as_applied` reuses the existing intake/ledger transition;
  no extension request may set application state directly.
- Return stable outcomes and error codes: `created`, `updated`, `duplicate`,
  `needs_content`, `assessment_ready`, `app_unavailable`,
  `unsupported_protocol`, and validation failures.

Additional local UI routes:

```text
GET  /api/discoveries?state=inbox&limit=20&cursor=offset:20
GET  /api/discoveries/<id>
PUT  /api/discoveries/<id>
POST /api/discoveries/<id>/promote
POST /api/discoveries/email-imports
```

The implemented promotion request is deliberately smaller than ordinary JD
intake:

```json
{
  "contract_version": 1,
  "action": "save_job|record_as_applied",
  "expected_revision": 1
}
```

The server owns the source text, title/company metadata, canonical URL, and the
stable `discovery:<id>` intake key. Clients cannot replace those fields during
promotion. Only `confirmed_full` content can pass; incomplete, dismissed, or
stale discoveries remain unchanged. Intake, ledger registration, deterministic
assessment, and the optional explicit Applied transition finish before the
discovery records `promoted_job_id`. Repeating a successful request returns the
same saved job. The extension capability is denied access to this general
promotion route.

Mutation fields and methods must be independently allowlisted for the local UI
and extension. The extension token may call session/runtime health and the one
bounded capture endpoint only; it may not read Vault, resumes, documents,
applications, or arbitrary discoveries, or call the general promotion route.
The capture endpoint may honor its validated explicit destination through the
server-owned domain service.

## Extension Extraction Rules

Use this order for **Save page**:

1. Read valid `application/ld+json` objects whose `@type` includes
   `JobPosting`; take only allowlisted text fields.
2. Collect provider-hinted, explicit job-description, title-anchored, and
   semantic (`main`, `article`, or `role=main`) candidates.
3. Rank those candidates through one validator that rejects job-list/search
   shells, multiple linked role titles, recommendations, and contaminated
   whole-page regions. Provider selectors are hints and never bypass it.
4. Fall back to bounded visible body text only for a clean, single-title page
   without list/search/recommendation/multi-role signals.
5. If no unique candidate wins, or the page is too large, ask the user to save
   an exact selection.

Always exclude scripts, styles, forms, inputs, textareas, select values, hidden
content, and extension/browser pages. Run in the main frame only. Do not click,
scroll, expand accordions, follow links, or inspect cross-origin frames.

Treat page text as untrusted data. It may contain instructions intended for a
model; the deterministic parser stores it as source and never executes it.

### Popup UX

The popup should remain a compact action surface:

- current page title and domain;
- **Save page** as the primary action;
- **Save selection**, enabled only when a non-empty selection exists;
- **Save link only** as the quiet fallback;
- a single status/result area; and
- a bounded receipt after success: capture scope, character count/readiness,
  and at most the advisory hard-blocker status;
- conditional compact indicators only when present: **Extra steps · N** and
  **Contact candidates · N**; exact evidence stays in Career Hub;
- **Review later** and **Review & record applied** as local Career Hub handoffs
  only when complete content was captured; and
- **Open in Career Hub** for link-only/partial captures.

Required results:

- Saved and preview ready.
- Saved; review captured text.
- Saved; needs job description.
- Already in Discovery Inbox.
- Career Hub is not running; open it and retry.
- This browser page cannot be captured.
- Capture is too large; select the JD text instead.
- Extension/server protocol versions are incompatible.

No refresh button, account UI, filters, or discovery list belongs in the
extension popup. The full retained JD and editable application helper do not
belong there either; they use the local discovery review and the later side
panel respectively. An advisory warning never replaces or disables the next
actions.

## Email Intake Design

User setup is documented in
[`email_alert_setup.md`](../guides/email_alert_setup.md). The exact first implementation
assignment for a lower-cost model is
[`mail_100_eml_parser_task.md`](../history/mail_100_eml_parser_task.md).

### Recommended no-worker topology

```text
Job boards -----------------------> dedicated Gmail
Personal Gmail sender filters ----> dedicated Gmail
                                         |
                              local IMAP TLS pull
                                         |
                              sender policy + parser
                                         |
                                  Discovery Inbox
                              /          |          \
                         Needs JD   Review first   Clear conflict
```

No GCP worker, Pub/Sub topic, webhook, custom mail server, public endpoint, or
owned domain is required. A custom domain such as `jobs@example.com` may later
forward to the same dedicated mailbox, but it adds cost/DNS/account recovery
work and does not simplify Career Hub. A Gmail `+jobs` alias is also not an
isolation boundary because it still grants access to the personal mailbox.

Supported onboarding modes should be:

| Mode | Product position | Access boundary |
| --- | --- | --- |
| Dedicated Gmail | Recommended dogfood | Account contains job alerts only; local IMAP pull |
| `.eml` import | Safest no-credential fallback | User explicitly supplies files |
| Existing personal inbox | Advanced, later | OAuth/approved connector plus one label and sender policy; credential may still reach more mail |
| Custom domain | Optional convenience, not MVP | Forwarding destination still needs a mailbox connector |

The dedicated account recommendation contains the impact of a broad IMAP app
password, but does not turn it into a technically read-only or label-scoped
credential. Google itself recommends OAuth over app passwords. Therefore this
is appropriate for private dogfood, not the final default for a broadly
distributed product.

### First slice: `.eml` files

Use `email.parser.BytesParser(policy=policy.default)`. Initial limits are 10 MiB
per file, 200 MIME parts, 10 nested message levels, 500,000 decoded text
characters, 500 visible HTML links, and 100 candidates per message. Reject the
whole file when any limit is exceeded; never silently truncate links,
candidates, or a job description. Ignore attachments.

Extract:

- a hashed `Message-ID` when present, plus whole-message SHA-256 fallback;
- sender domain, subject, and received date;
- text/plain or sanitized visible HTML text;
- HTTP/HTTPS anchors and their visible labels; and
- repeated job-card blocks that contain a plausible title/link pair.

Reject or ignore tracking pixels, unsubscribe/preferences/account links,
`mailto:`, `javascript:`, data URLs, and attachments. Do not render email HTML
in the browser.

Implement parsers as deterministic adapters:

```text
generic alert parser
  + sender/template adapters backed by synthetic fixtures
```

An adapter may identify a title, company, location, posting id, and source link.
It may not infer candidate skills, follow the link, or create a saved job.

### Mailbox prerequisite

For automated retrieval, the recommended setup is:

- one separate personal Gmail account used only for job alerts;
- direct alert delivery to that address only where the board exposes a separate
  notification destination; and
- exact sender/subject forwarding filters from a personal Gmail account where
  the primary job-board address must remain the recruiter/contact address.

Connecting an existing personal mailbox remains possible only as an advanced
future option. A `Career Hub Alerts` label and sender allowlist limit intended
processing, but do not narrow what a broad IMAP credential can technically
access. The connector does not scan Sent, Contacts, Calendar, Drive, or other
folders. It never deletes, moves, marks read, replies, or forwards.

### Sender policy

The connector first fetches only immutable id and selected headers for unseen
messages: `Message-ID`, `From`, `Subject`, and `Date`. It fetches the MIME body
with a non-mutating `BODY.PEEK` operation only when the source policy allows the
sender.

Each current sender policy has a normalized exact address, `allowed` or
`disabled` disposition, and one explicit route: **Job opportunities**,
**Application tracking**, or **Both streams**. Domain-wide authorization is not
supported.

Unknown senders appear in **New email senders** with the exact address, derived
header-signal reasons, message count, and received dates only. The user chooses
a route and then **Allow & sync** or **Ignore**. Unknown bodies are not
downloaded automatically. A shipped sender list may make suggestions, but it
must not silently authorize a source because job-board sending domains change.

### High-accuracy todo policy

Email alerts often contain only a title, company, location, link, and short
snippet. That is enough to create a discovery but not enough to claim a strong
fit. The system uses these queues instead of an opaque email score:

- **Needs JD**: link/snippet only; open the posting and complete it through the
  extension.
- **Review first**: complete JD available, no explicit hard blocker, fresh
  observation, and the existing deterministic assessment supports proceeding.
- **Review**: complete JD but material facts remain unclear or evidence is thin.
- **Clear conflict**: at least one source-backed JD fact is deterministically
  incompatible with an explicit Career Vault/profile fact. This is a strong
  advisory warning, not a disabled application path.

A single-job alert whose source explicitly says **great match**, **top match**,
or equivalent may create a todo immediately. The badge must read
**Recommended by source** and retain the exact phrase; it must never be rendered
as a Career Hub fit conclusion. Until a complete JD is available, that todo
still belongs to **Needs JD**. A multi-job digest creates one discovery per
unambiguous job block and applies the same rule independently.

Initial hard-blocker classes are deliberately narrow:

- incompatible citizenship, work-authorization, sponsorship, or explicitly
  defined U.S.-person requirement;
- required active clearance that the verified profile does not satisfy;
- an explicit minimum years requirement that exceeds verified relevant
  experience when the area is comparable.

The phrase `U.S. person` is not automatically equivalent to `U.S. citizen` and
must be retained exactly for the existing eligibility evaluator. Likewise,
`10+ years` is a blocker only when it is an explicit minimum, the relevant
experience area is comparable, and verified duration is lower. Ambiguity stays
in **Review**. Missing skills reduce coverage or create review items; they are
not hard blockers. Absence of a fact in an email snippet never counts as
evidence that the posting is compatible.

Location, on-site, hybrid, and relocation language is never a hard blocker for
this user because relocation is allowed. Preserve it as a filterable planning
fact only; do not demote or suppress a todo on that basis.

Freshness uses the posting date only when the source states it. Otherwise the
system stores `alert_received_at`/`first_seen_at` and never relabels that as the
posting date.

### Job Feed And Configuration Modes

**Job Feed** is the user-facing aggregate todo page. `Discovery Inbox` remains
the internal capability/domain name. Email and future Watchers are producers;
they do not own separate job lists.

Use one page with a compact mode switch:

```text
Jobs / Job Feed
  [ Feed ] [ Email ] [ Watchers ]
```

- **Feed** is the job-board-like aggregate list.
- **Email** configures the dedicated mailbox, forwarding guide, approved
  senders, sync schedule, and source health. It does not duplicate the feed.
- **Watchers** is an honest `Coming soon` configuration placeholder until
  `CAP-JOB-WATCHING` ships. Its empty state explains that the future mode will
  manage source URLs, permissions/terms, schedules, fallbacks, last runs, and
  health. It exposes no working URL field or start button before implementation.

Do not add an Extension mode. Distinguish **producers** from **enrichers**:

| Kind | Component | Feed behavior |
| --- | --- | --- |
| Producer | Email alerts | Creates a discovery from one alert job block |
| Producer | Precision watchers | Later creates discoveries from configured public sources |
| Producer | Manual capture | Explicitly creates one discovery from a link/JD |
| Enricher | Public link resolver | Explicitly fills one public corporate/ATS JD on an existing link-only discovery |
| Enricher | Browser extension | Fills a visible/logged-in JD or performs an explicit standalone action |

When the extension is invoked from a Feed item, it must enrich that same
discovery rather than create another card. A standalone extension capture must
ask for an explicit destination such as **Add to Job Feed**, **Save job**, or
**Record as applied**. Clicking the extension is never evidence that an
application was submitted. `Applied` remains an explicit ledger action;
**Dismiss** removes a discovery from the default Feed without inventing an
application record.

The primary Feed source remains `email`, `watcher`, or `manual`. Extension and
resolver appear as provenance/enrichment badges and optional filters, not as
competing source origins. When the same posting arrives from email and a
watcher, deduplication produces one card with both producer badges and retains
all observations.

#### Feed layout

Default to a vertically scanned job-board card list, with an optional compact
list preference. Each result shows only decision-useful fields:

- title, company, location/workplace facts, and source-observed age;
- producer badge(s) and why it entered the Feed;
- **Recommended by source** only when the source used that exact wording;
- `Needs JD`, `Review first`, `Review`, or `Clear conflict` queue;
- content completeness and assessment availability;
- up to one primary conflict/evidence line for **Clear conflict**; and
- **Open posting**, **Shortlist**, and overflow actions.

Selecting a result opens one detail surface with full provenance, captured JD,
assessment, all blockers/review items, and explicit **Dismiss**, **Save job**,
and **Record as applied** actions. Do not make every field visible on the card.

The default filter row contains:

- search across title/company;
- producer: `All`, `Email`, `Watchers`, `Manual`;
- queue/readiness;
- observed period/freshness; and
- sort: newest, last seen, or assessment priority where a complete assessment
  exists.

Location/workplace, role, skill, completeness, and enrichment-channel filters
belong in a collapsible **More filters** panel. Location is a planning filter,
never a hard blocker. Do not rank a snippet-only source recommendation above a
complete Career Hub assessment.

The implemented attended enrichment order for an email discovery is:

```text
email link/snippet
  -> user opens the retained source link
  -> attended browser capture
  -> complete JD
  -> existing deterministic assessment
  -> Review first / Review / Clear conflict
```

Protected login pages are never handed to the public resolver. If deterministic
resolution cannot isolate one complete-looking JD and attended extension
capture has not supplied it, the source recommendation remains a useful
**Needs JD** todo rather than being discarded.

### Connector decision gate

The recommended private-dogfood provider is a dedicated personal Gmail account.
After `.eml` dogfood, record an ADR confirming whether that account exposes App
Passwords and comparing exactly these options:

1. `IMAP4_SSL` with a dedicated-account app password and UID-based incremental
   sync, with its broad-access limitation explicitly accepted;
2. the provider's official API with local OAuth and the narrowest available
   read scope; or
3. keep explicit `.eml` import if either option would require disproportionate
   permission or dependency weight.

Use `ssl.create_default_context()` for IMAP TLS and UIDs rather than mutable
message sequence numbers. Store the account address as configuration and the
app password/token only through `src.secret_store` / Windows Credential
Manager. Store folder, UIDVALIDITY, last successful UID, and sync time as
non-secret SQLite metadata. If UIDVALIDITY changes, perform a bounded rescan and
deduplicate by hashed `Message-ID`/message fingerprint rather than trusting the
old cursor.

Gmail push requires Cloud Pub/Sub, and Microsoft Graph webhook delivery requires
a public HTTPS endpoint. Those push architectures are deliberately deferred for
the local-first release. A local pull sync is sufficient for daily alerts.

### Scheduling

Implement in this order:

1. **Sync now** while Career Hub is open.
2. One startup sync plus a user-controlled interval while the app remains open;
   30 minutes is the proposed default for dogfood.
3. A one-shot CLI invoked by Windows Task Scheduler only after manual sync is
   proven reliable and non-interactive auth works.

The one-shot command must resolve the remembered Data Home, acquire a
single-run lock, fetch only unseen UID/message ids, write content-minimized logs,
and exit with stable codes. The recommended scheduled time is user-selected,
for example 06:00 local, with a startup sync catching a missed sleeping/offline
run. It must run under the same Windows account that owns the credential, must
not start a hidden browser, and must not keep a tray process alive.

## Security And Privacy Gates

Every ticket must preserve these gates:

- Exact loopback authority; never bind the Career Hub API to `0.0.0.0`.
- Exact configured extension origin; no wildcard extension origins.
- Extension endpoint/method allowlist enforced after token validation.
- `activeTab` access only after an explicit action; no persistent site access.
- No credential, cookie, history, DOM HTML, form value, email recipient, or
  attachment in payloads or logs.
- Bounded request size, string length, MIME complexity, and candidate count.
- HTTP/HTTPS URL validation and conservative canonicalization.
- Parameterized SQL, foreign keys, revision checks, and backup-before-migration.
- Atomic source writes below the selected Data Home.
- Synthetic extension pages and `.eml` fixtures in tests; no real private mail,
  job content, tokens, or extension ids in Git.
- No automatic Skills Registry or Career Vault mutation.
- `evidence_not_found` and incomplete capture remain honest, scoped statements.
- Discovery content is included in normal local recovery unless a later
  documented retention policy says otherwise; normal recovery ZIPs are not
  generally encrypted.

## Prerequisites

### Repository prerequisites already present

- Explicit private Data Home with containment and recovery behavior.
- Existing immutable JD intake, posting identity, content fingerprint, and
  idempotency support.
- Existing deterministic digest and advisory assessment.
- Existing application SQLite migration/recovery pattern.
- Exact extension-origin CORS configuration and a distinct in-memory extension
  token.

### Required before extension dogfood

- `DISC-100` through `DISC-120` accepted.
- Extension token restricted to the capture endpoint allowlist.
- Chrome developer mode and an unpacked extension id.
- Server started with that exact `chrome-extension://<id>` origin.
- Sandbox Data Home for first tests.
- Synthetic job pages covering JSON-LD, semantic container, selection, oversized
  page, login page, and unsupported Chrome page.

### Required before automated email sync

- `.eml` parser and import dogfooded with representative synthetic fixtures.
- Dedicated Gmail account created, secured with two-step verification, and used
  for no content other than job alerts.
- Direct alert destinations or selective forwarding filters configured by the
  user; whole-inbox forwarding is disabled.
- Connector ADR confirms whether App Passwords are available and explicitly
  accepts IMAP for private dogfood or selects the fallback.
- Initial sender allowlist reviewed by the user.
- Narrow read permission and non-interactive refresh path proven.
- Secret storage, disconnect/revoke, cursor reset, and retry behavior tested.
- Network access and any new dependency explicitly authorized in that ticket.

## Browser Extension Execution Checkpoint (2026-08-16)

The email producer, shared discovery storage, Feed, deduplication, and explicit
promotion path are now usable. The extension is the next producer/enricher
track. Its least-privilege boundary and bounded link-capture endpoint are
implemented, and the link-only client is ready for one manual toolbar dogfood.

Current code evidence:

- `src.job_discovery.capture_discovery()` already owns conservative identity,
  request idempotency, source snapshots, and observation merging.
- The Job Feed and email import already prove that one discovery can retain
  multiple observations without entering the application ledger.
- The server already recognizes one exact configured extension Origin and
  issues a distinct in-memory extension token.
- `EXT-090` restricts extension-origin routes to session bootstrap plus the
  exact future capture POST. A valid extension token receives `403` for
  unrelated Vault, resume, document, application, email, discovery, PUT, and
  DELETE requests; capture preflight advertises only POST and two headers.
- `EXT-095` implements extension-only `POST /api/discoveries/captures` with an
  exact version-1 link/feed schema, UUID retry idempotency, conservative merge,
  stable outcome/error codes, and no JD/application/assessment/skill writes.
- The manifest public development key fixes the unpacked Chrome ID at
  `jhgaecfochimmafoopcpodncoeobecfg`; `scripts/start_career_hub.ps1
  -EnableExtension` maps explicitly to that Origin, while `-ExtensionOrigin`
  remains available for alternate builds. Neither path persists or enables
  extension access by default.
- `extension/` now contains the plain MV3 worker, bounded extractor, and
  three-action popup; the manifest requests only `activeTab`, `scripting`, and
  exact loopback host access.
- Focused protocol tests and an isolated Chromium smoke verify manifest loading,
  fixed worker routes, extension Origin/token capture, semantic deduplication,
  and default/success/error popup states without touching the user's Chrome
  profile or selected Data Home.

Product decision update (2026-08-18): extension findings remain advisory and
never prevent an application. The accepted product sequence now also includes
a high-precision extra-step checklist, review-only contact candidates followed
by an explicitly confirmed local contact ledger, and a separately scoped
quick-copy/prefill helper. The checklist, confirmed contact ledger, and explicit
reviewed helper are now accepted behavior.

Locked execution order:

```text
EXT-090  least-privilege token and CORS boundary [complete]
  -> EXT-095  bounded link-capture API and launcher setup [complete]
  -> EXT-100  unpacked link-only MV3 extension + toolbar dogfood [complete]
  -> EXT-110  selection and visible-page extraction [complete]
  -> EXT-115  field corrections and provider identity precision [complete]
  -> EXT-120  preview assessment, hard-blocker receipt, email merge [complete]
  -> EXT-125  verified local review and Saved/Applied handoff [complete]
  -> EXT-110R split-pane JD isolation hardening [complete]
  -> EXT-127  job-bound application session and submission confirmation [complete]
  -> EXT-130  configurable hard-blocker policy [complete]
  -> EXT-135  source-backed extra application steps [complete]
  -> EXT-140  source-backed contact candidates [complete]
  -> CONTACT-090 contact storage and linking domain [complete]
  -> CONTACT-100 confirmed-save UI and contact ledger [complete]
  -> HELP-090 application-helper capability boundary [complete]
  -> HELP-100 quick-copy side panel [complete]
  -> HELP-110 explicit reviewed prefill [complete]
  -> INT-300  security/privacy/recovery dogfood matrix [complete]
```

Do not combine these tickets. `scripting` belongs only to the explicit
`EXT-110` capture action, and the Saved/Applied handoff waits for accepted
complete-content handling. Submission observation additionally waits for a
stable reviewed Job/Application binding; button text alone is never sufficient.
Helper values use the separate, purpose-specific capability established by
`HELP-090`; the capture capability cannot call that route and the helper
capability cannot capture, inspect applications, or read arbitrary browser
APIs. `EXT-140` extraction still produces reviewable
candidates only; a permanent contact requires the accepted `CONTACT-100`
current-source check and explicit browser confirmation.

Technology remains intentionally small: Chrome Manifest V3, packaged plain
HTML/CSS/JavaScript, `activeTab`, user-triggered `scripting`, and one exact
loopback `host_permissions` entry. No Node build, React, content script
installed at startup, helper-value storage, native messaging, cloud service, or
browser automation is needed for private dogfood. The service worker reacquires
a short-lived token for each explicit action because MV3 workers may be
terminated; the token is never persisted merely to survive that lifecycle.

## Ordered Tickets

Assign one ticket per model run. Do not assign a phase or combine extension,
email, and storage work in one goal.

### `DISC-100` Add Discovery Storage And Domain Rules

**Goal:** Synthetic captures create, merge, list, and transition discoveries
without creating a job card or application row.

**Inspect:** `src/application_history.py`, `src/runtime_paths.py`,
`src/ingest_job.py`, `src/data_backup.py`, `docs/reference/data_home.md`.

**Modify:** `src/application_history.py`, new `src/job_discovery.py`, new
`tests/test_job_discovery.py`, focused additions to
`tests/test_application_history.py`, and this plan only if the implemented
contract requires clarification.

**Acceptance:**

- Migration creates the three planned discovery tables and a pre-migration
  recovery copy for an existing database.
- Source paths cannot escape `jobs/discovery/`.
- Exact request retry and semantic duplicate merge are independently tested.
- State transitions reject stale revisions and illegal transitions.
- Discovery creation does not create `jobs/incoming`, `jobs/normalized`, or a
  `job_applications` row.
- Tests use only temporary synthetic Data Homes.

**Checks:**

```powershell
python -m unittest tests.test_job_discovery tests.test_application_history -v
```

**Out of scope:** HTTP routes, UI, browser extension, email parsing, scheduling.

### `DISC-110` Add Capture, Listing, And Promotion API

**Goal:** The local UI can create/list/review a discovery and explicitly promote
one complete source through existing JD intake.

**Inspect:** `src/job_discovery.py`, `src/run_intake_ui.py`,
`src/ingest_job.py`, `src/application_history.py`, `tests/test_intake_ui.py`.

**Modify:** those implementation modules only as required, plus focused tests
and API documentation in this plan.

**Acceptance:**

- Bounded, versioned capture payload and pagination are enforced.
- URL-only and partial sources cannot accidentally receive a complete
  assessment or promotion.
- Promotion reuses existing intake and a stable idempotency key.
- A failed promotion leaves the discovery unpromoted and retryable.
- A successful retry returns the same `promoted_job_id`.
- Only promoted jobs appear in ledger and demand projections.

**Checks:**

```powershell
python -m unittest tests.test_job_discovery tests.test_intake_ui tests.test_competency_demand -v
```

**Out of scope:** UI, extension files, email.

### `DISC-120` Build The Job Feed UI

**Goal:** The user can use one `Feed · Email · Watchers` page to scan, filter,
inspect, dismiss/restore, shortlist, and promote discoveries without confusing
them with applications.

**Inspect:** existing route/navigation/list-card patterns under
`src/intake_ui_static/`, plus `docs/guides/manual_testing.md`.

**Modify:** one new discovery feature module and the minimum route, locale,
style, and manual-test files required by the existing UI architecture.

**Acceptance:**

- The page exposes the three modes defined above and remembers the selected mode
  in the route or browser-local presentation state.
- Feed defaults to `inbox` and `shortlisted`; dismissed is an icon toggle.
- Email owns setup/sender/sync configuration but never renders a second job
  list. Watchers renders the bounded `Coming soon` configuration placeholder.
- Extension and public resolver are enrichment provenance, not top-level modes
  or primary producer filters.
- Compact filters cover producer, todo queue, readiness, and observed/last-seen
  sorting; secondary filters live under **More filters**.
- Cards/list show title, company, source, first/last seen, capture scope, and
  preview availability without a fake numeric score.
- A duplicate posting observed by email and watcher renders once with both
  producer badges.
- A source phrase such as `great match` appears only as **Recommended by
  source**, never as the Career Hub assessment.
- Detail clearly distinguishes captured source, preliminary assessment, and
  application promotion.
- Extension click never marks Applied. Standalone capture requires an explicit
  destination; capture from a Feed item enriches the same discovery.
- Empty, loading, error, duplicate, and stale-revision states are usable.
- List/cards preference is browser-local presentation only.

**Checks:**

```powershell
python -m unittest tests.test_intake_ui -v
```

**Out of scope:** extension, email parser, application UI redesign.

### `EXT-090` Scope The Extension Capability

**Goal:** A valid extension token can call only session/runtime health and the
capture endpoint; all unrelated API reads and mutations are rejected.

**Inspect/modify:** `src/run_intake_ui.py`, `tests/test_intake_ui.py`, and
`docs/guides/local_development.md`.

**Acceptance:**

- Exact allowed origin and token are both required.
- The extension-origin route/method allowlist is exactly unauthenticated
  `POST /api/session` bootstrap plus the future authenticated
  `POST /api/discoveries/captures`; the token itself is accepted only for that
  POST, and no prefix or broad method match is used.
- Vault, resumes, documents, applications, promotion, PUT, and DELETE remain
  unavailable to the extension token.
- Browser UI access remains unchanged.
- CORS preflight is accepted only for the configured Origin and capture route,
  and advertises only `POST`, `content-type`, and `x-career-hub-token` there.
- Focused tests attempt representative unrelated GET, POST, PUT, and DELETE
  routes with a valid extension token and receive `403`.

**Checks:**

```powershell
python -m unittest tests.test_intake_ui -v
```

**Out of scope:** extension implementation and any authentication redesign.

### `EXT-095` Add The Bounded Extension Capture API

**Status:** Complete on 2026-08-16; `EXT-100` is the next ticket.

**Goal:** One valid extension action can submit a versioned link-only capture
to `POST /api/discoveries/captures` without gaining access to general discovery
or application APIs.

**Inspect/modify:** `src/run_intake_ui.py`, `src/job_discovery.py`,
`scripts/start_career_hub.ps1`, `tests/test_discovery_api.py`, focused boundary
tests in `tests/test_intake_ui.py`, `docs/guides/local_development.md`, and this plan.

**Acceptance:**

- Version 1 rejects unknown fields, unsupported versions, non-HTTP(S) URLs,
  invalid UUID request ids, oversized strings, raw HTML, headers, cookies,
  screenshots, and any client-supplied `source_kind`.
- The server owns `source_kind=extension`, server time, canonicalization,
  identity, merge, provenance, and response shape.
- This first endpoint slice accepts only `capture_kind=link` and
  `destination=feed`; Saved/Applied and page text remain rejected until later
  tickets.
- Repeating one request id is idempotent; reusing it for different content is a
  conflict; the same URL from email and extension remains one discovery with
  both observations.
- Capture creates or enriches a discovery but creates no incoming JD, normalized
  card, application row, assessment, or Skills Registry write.
- The PowerShell launcher accepts one validated `-ExtensionOrigin` value and
  passes it through without persisting it or enabling extension access by
  default.
- Stable success/error values cover created, updated, duplicate, needs content,
  unsupported protocol, incompatible contract, invalid origin/token, and app
  unavailable at the client boundary.

**Checks:**

```powershell
python -m unittest tests.test_discovery_api tests.test_intake_ui tests.test_job_discovery -v
```

**Out of scope:** extension files, page extraction, assessment, promotion,
browser installation, and real private data.

### `EXT-100` Ship The Link-Only Developer Extension

**Status:** Complete. On 2026-08-18 the user reloaded the fixed-ID unpacked
extension, started the extension-enabled sandbox, and created five toolbar
captures from distinct real provider surfaces. The resulting generic
aggregator/search titles are input evidence for `EXT-110` and `EXT-115`, not an
`EXT-100` failure.

**Goal:** An unpacked MV3 extension explicitly adds the active HTTP/HTTPS URL to
Job Feed as a manual `Needs JD` discovery.

**Prerequisites:** accepted `EXT-090` and `EXT-095` evidence.

**Inspect:** `docs/guides/local_development.md`, the capture API tests, and official
Chrome MV3/`activeTab` documentation.

**Modify:** new `extension/manifest.json`, `extension/service-worker.js`,
`extension/popup.html`, `extension/popup.js`, `extension/popup.css`, and new
`docs/guides/extension_development.md`; only focused server docs/tests if a proven
contract mismatch is found.

**Acceptance:**

- This link-only slice requests `activeTab` and loopback host access only;
  `scripting` is added by `EXT-110` when page text is first read.
- **Add to Job Feed** creates one link-only manual discovery; a retry returns
  duplicate/update.
- The popup sends a fixed command to the service worker; it cannot request an
  arbitrary URL. The worker fetches only the configured loopback session and
  capture endpoints.
- The token exists only in request-local/service-worker memory and is
  reacquired after worker restart. The later session-metadata storage
  permission never receives this token or helper values.
- A public manifest development key keeps the unpacked Chrome ID stable across
  directories and temporary profiles; the launcher exposes only an explicit
  `-EnableExtension` shortcut for that exact Origin.
- Offline, wrong origin, special page, timeout, and incompatible protocol errors
  are actionable.
- No page content is read in this ticket.

**Checks:**

```powershell
python -m unittest tests.test_browser_extension tests.test_discovery_api tests.test_intake_ui tests.test_job_discovery -v
```

Then perform the documented unpacked-extension sandbox smoke test.

### `EXT-110` Add Selection And Visible-Page Capture

**Status:** Complete on 2026-08-18. The bounded three-mode contract, isolated
MV3 browser/extractor fixtures, rendered popup inspection, 100 focused tests,
and the full 568-test repository regression pass.

**Goal:** The same popup can save explicit selection or bounded visible job text
without persistent site access.

**Modify:** extension extractor/popup/worker/manifest files, the exact
extension-capture validator/handler in `src/run_intake_ui.py`, synthetic browser
fixtures, and focused documentation/tests only. The server change is required
because the accepted `EXT-095` contract intentionally rejected page text.

**Acceptance:**

- Extraction order and exclusions match this plan.
- Main frame only; no clicks, navigation, scrolling, or cross-frame inspection.
- Selection remains partial unless explicitly confirmed complete.
- Oversized/ambiguous page falls back to selection guidance.
- Raw HTML, form values, hidden text, cookies, and headers are absent from the
  payload.
- JSON-LD, semantic container, plain fallback, login page, malformed JSON-LD,
  oversized page, and ambiguous page fixtures are automatically covered.

**Checks:** focused extension fixture checks plus:

```powershell
python -m unittest tests.test_browser_extension tests.test_discovery_api tests.test_intake_ui tests.test_job_discovery -v
```

Then run `tests/browser_extension_smoke.cjs` in an isolated Chromium profile and
inspect its default, success, and error screenshots.

### `EXT-110R` Harden Split-Pane JD Isolation

**Status:** Complete on 2026-08-18. This is a regression hardening pass over
`EXT-110`, not a new capture capability.

**Goal:** A selected job inside a board/search split view retains only its
description region; the surrounding results, recommendations, and navigation
never become a complete JD merely because they share one `<main>` or `body`.

**Acceptance:**

- Indeed, LinkedIn, Handshake, MeeBoss, and 12twenty hostname adapters provide
  candidate hints only; every candidate passes one shared validator.
- Exact structured data remains first. Provider, explicit, title-anchored, and
  semantic candidates are ranked with list/link/role/shell contamination
  checks. Whole-body fallback is limited to a clean single-job page.
- Two equal independent candidates, a provider selector drift without one
  clear generic winner, and a search shell without a full detail pane fail
  closed to selection guidance.
- Synthetic generic, Indeed, LinkedIn, and Handshake split-view fixtures prove
  retained detail text and excluded unrelated markers without network access or
  real provider content.
- The generic split-view fixture is also captured through the real MV3 popup,
  retained in Job Feed, explicitly confirmed, and promoted through the existing
  JD intake; its immutable incoming source must still exclude list-card markers.

### `EXT-115` Add Reviewable Field Corrections And Provider Identity Precision

**Status:** Complete on 2026-08-18. Schema-v18 migration, separate optimistic
field overrides, exact provider URL identities, local review UI, API boundary
tests, synthetic five-provider fixtures, and desktop/mobile rendered smoke all
pass. One-to-one legacy provider captures are repaired during the backed-up
migration; ambiguous duplicates remain separate for review. The final
repository regression passed 573 tests with three documented skips.

**Goal:** A generic or partially correct capture can be corrected without
destroying extracted evidence, and repeat provider URLs merge by exact posting
identity where deterministic evidence exists.

**Prerequisites:** accepted `EXT-110` selection/page capture and retained source
evidence.

**Inspect/modify:** discovery schema/service, known-provider URL normalization,
Job Feed detail/review UI, exact browser APIs, focused migration/API/UI tests,
and extension success handoff only if an exact local review link is needed.

**Acceptance:**

- The review surface exposes editable effective title/role, company, and
  location. Source URL, retained text, extractor, observation provenance, and
  originally extracted values remain read-only evidence.
- User corrections are stored in a separate revisioned override record. A
  rescan, repeated capture, core update, or richer observation cannot overwrite
  them; clearing one override returns that field to the latest extracted value.
- Empty/whitespace values, stale revisions, oversized values, and unknown fields
  are rejected. Retry is idempotent and concurrent edits cannot silently win.
- UI distinguishes **Extracted** from **Edited by you** and previews the value
  that matching/promotion will use.
- Deterministic identities cover at least LinkedIn `currentJobId`/job paths,
  Indeed `vjk`, Handshake job paths, and explicitly tested MeeBoss/12twenty
  identifiers. Tracking parameters and URL fragments do not become identity.
- A generic search page with no exact posting ID is not guessed into a job. It
  remains reviewable and may be corrected manually.
- Corrections do not create a Job, Application, assessment, skill, or contact.

**Checks:** migration/domain/API/UI tests plus regression fixtures derived from
the five user-observed provider URL shapes, with private values replaced by
synthetic equivalents.

```powershell
python -m unittest tests.test_application_history tests.test_job_discovery tests.test_discovery_api tests.test_acceptance_registry -v
node tests/job_feed_field_corrections_smoke.cjs
```

### `EXT-120` Add Preview Assessment, Hard-Blocker Receipt, And Merge

**Status:** Complete on 2026-08-18. Page/confirmed-full previews now reuse the
existing deterministic analyzer/evaluator and are cached against source,
analyzer/evaluator, candidate, taxonomy-context, and scope revisions. The MV3
popup renders a strict no-score receipt; link/selection captures are explicitly
unassessed, clear conflicts retain exact JD/profile evidence, unknown language
stays review-only, and an email-link plus page capture remains one Discovery.
Focused domain/API/protocol checks and the isolated Chromium render smoke pass.

**Goal:** A usable page capture receives the existing advisory preview plus a
bounded hard-blocker receipt and merges with a matching email/link discovery.

**Inspect/modify:** discovery service, existing assessment composition, capture
route, Discovery UI, and focused tests. Do not copy evaluator rules.

**Acceptance:**

- Page preview is versioned against content and candidate context.
- Partial/link captures never claim absent requirements as gaps.
- Email link followed by extension page capture remains one discovery with two
  observations.
- Every extension capture remains a discovery first. The popup may state an
  intended next step, but Saved/Applied changes require the local review ticket.
- The compact receipt has no numeric score. It reuses the existing narrow
  blocker classes, distinguishes Review from Clear conflict, and includes exact
  source evidence for any clear conflict.
- Extension invocation or closure never infers Applied, Skipped, or Dismissed.
- Unknown vocabulary is reported for review and never auto-created.
- The response exposes only bounded receipt data needed by the popup; it does
  not grant the extension general discovery or assessment reads.

**Checks:**

```powershell
python -m unittest tests.test_job_discovery tests.test_intake_ui tests.test_job_review -v
```

### `EXT-125` Add Verified Review And Saved/Applied Handoff

**Status:** Complete on 2026-08-18 with schema-v19 verification provenance,
an exact server-issued popup handoff, browser-only confirmation/promotion,
focused domain/API/migration coverage, and desktop/mobile Chromium smoke.

**Goal:** After a complete page capture, the user can inspect the retained JD in
Career Hub and explicitly continue as Review later, Save job, or Record as
applied without the extension changing ledger state itself.

**Prerequisites:** accepted `EXT-110` complete-content handling and `EXT-120`
receipt/merge evidence.

**Inspect/modify:** discovery detail routing/UI, the extension success handoff,
existing promotion service/API, focused tests, and extension documentation.

**Acceptance:**

- The popup receipt opens one exact discovery id; it cannot choose an arbitrary
  local route or supply replacement JD data.
- The local review shows exact retained text, capture scope, identity,
  provenance, and assessment context before promotion controls.
- **Review later** leaves the discovery unpromoted and available in Job Feed.
- **Save job** and **Record as applied** call the existing browser-only
  promotion route after explicit confirmation and create exactly one ledger
  record under retries.
- Link-only, partial, stale, dismissed, or unverified content cannot be
  promoted. Closing either surface does not infer a decision.

**Checks:**

```powershell
python -m unittest tests.test_browser_extension tests.test_discovery_api tests.test_intake_ui tests.test_job_discovery -v
```

### `EXT-127` Add A Job-Bound Application Session And Submission Confirmation

**Goal:** When the user starts applying from one reviewed Career Hub job, later
DOM/form changes remain tied to that exact Job/Application and a likely final
submission can be recorded without guessing from the current page title.

**Prerequisites:** accepted `EXT-125` review/promotion handoff and one durable
Job/Application id. This ticket requires a fresh permission and privacy review;
it must not reuse the capture capability implicitly.

**Inspect/modify:** application transition service, a narrow server-side
application-session API, explicitly armed tab instrumentation, extension
session lifecycle, local confirmation UI, synthetic multi-step form fixtures,
and focused security/idempotency tests.

**Acceptance:**

- The user explicitly starts/arms an application session from one exact
  reviewed Job/Application. The binding uses an opaque expiring session id plus
  tab id/origin; it never relies on a later page title or free-text company
  match.
- The binding survives same-tab DOM replacement and supported same-origin form
  navigation, expires on timeout/tab reuse, and cannot be reassigned by popup
  input. Any transient extension state uses `chrome.storage.session`, not local
  persistence, only after its separate permission review.
- Instrumentation records no field values, credentials, resume contents,
  cookies, screenshots, or full form DOM. It never clicks **Submit**.
- A button label or click alone is insufficient. **Continue**, **Next**, login,
  validation failure, CAPTCHA, save draft, withdraw, and retry do not transition
  the ledger.
- A native final-submit signal plus an observed success/thank-you state creates
  an idempotent **submission detected** event for that bound application. The
  initial release asks **Mark this application as applied?** and changes the
  ledger only after confirmation.
- Automatic Applied transition remains a later opt-in policy, gated by benchmark
  evidence and an immediate undo path; it is not silently enabled by this
  ticket.
- Closing the tab, losing the worker, or receiving an ambiguous signal leaves
  the application unchanged and exposes a recoverable review state.

**Checks:** synthetic single-page and multi-step forms covering success,
validation failure, redirect, duplicate event, tab reuse, worker restart,
cross-origin navigation, and two simultaneous job sessions. Confirm that a
signal can update only its bound application.

### `EXT-130` Add User-Configurable Hard-Blocker Policy

**Status:** Complete (2026-08-19). The browser Profile now owns revisioned
private eligibility facts and a separate revisioned three-class policy. Saved
Jobs and Discovery previews use one current-fact evaluator; unknown facts,
missing exact evidence, and disabled classes remain structured Review findings.
The extension receives only the bounded receipt and cannot read either private
API. Focused domain/API/UI tests and the isolated desktop/390 px Chromium smoke
pass without console errors or horizontal overflow.

**Goal:** The user can configure the small set of facts that may classify a
complete JD as a clear conflict, while everything ambiguous remains Review.

**Inspect/modify:** existing visa/profile schema and editors, deterministic job
digest/review composition, discovery preview projection, focused UI/API tests,
and documentation. Reuse current eligibility gates instead of adding an
extension-side evaluator.

**Acceptance:**

- Initial configurable classes are explicit citizenship/work authorization or
  sponsorship conflict, required active clearance, and explicit minimum years
  above verified comparable experience.
- `10+ years` is not a blocker unless it is required, the experience area is
  comparable, and the verified duration is lower.
- Location, on-site/hybrid, relocation, missing skills, title similarity, and
  silence are never hard blockers.
- Every blocker shows the exact JD evidence and exact confirmed profile fact;
  unknown or stale profile facts produce Review.
- **Continue / accept risk** remains available for every finding. The policy
  never disables capture, promotion, opening the posting, or recording an
  application; acknowledgement reuses the existing resolution history.
- Configuration lives in the private Data Home, has conservative defaults, is
  revisioned, and is not readable through general extension access.

**Checks:**

```powershell
python -m unittest tests.test_job_digest tests.test_job_review tests.test_job_discovery tests.test_intake_ui -v
```

Rendered persistence check (with the bundled Playwright runtime):

```powershell
node tests/browser_hard_blocker_policy_smoke.cjs
```

### `EXT-135` Add Source-Backed Extra Application Steps

**Status:** Complete (2026-08-19). Complete Discovery and saved-JD paths now
share one bounded exact-source analyzer; partial/link capture says **Full JD
required**. The extension receipt exposes only a conditional count. Job
Workspace provides exact evidence, reversible append-only state, stale-source
handling, and manual add from exact selected source text. Focused tests and an
isolated desktop/390 px rendered persistence smoke pass without console errors
or horizontal overflow.

**Goal:** A complete captured JD produces a fast, conservative checklist for
explicit instructions that are easy to miss during an application.

**Prerequisites:** accepted `EXT-110` complete-content extraction and the
versioned discovery preview path from `EXT-120`.

**Inspect/modify:** deterministic JD analysis/schema, discovery preview
projection, application ledger events, discovery/job-workspace UI, synthetic
fixtures, focused tests, and documentation. Prefer a small dedicated analyzer
module over adding unrelated rules to the extension worker.

**Acceptance:**

- Only `page_visible`/`confirmed_full` content is eligible. Partial/link capture
  shows **Full JD required** and never claims that instructions are absent.
- One bounded local scan recognizes the initial categories in the user journey:
  explicit email/send destination, exact phrase/subject, required artifact,
  named question/cover-letter format, external step, and deadline.
- High-confidence extraction requires a recognized instruction section or a
  concrete imperative/action target. Results retain exact source evidence and
  an extractor version; no network, AI, DOM rescan, or paraphrase-generated
  requirement is used.
- EEO, accommodation, privacy/legal, benefits, marketing, generic **Apply now**,
  and ordinary responsibility/qualification wording are negative fixtures.
- Positive fixtures include instructions equivalent to **email materials to
  this address**, **record a short video**, and **write I READ in the cover
  letter**, including examples at the end of a long JD.
- The popup renders only a conditional count. Career Hub renders the exact
  checklist with `pending`, `done`, and `not_applicable` actions; every action
  is reversible and stale when the source fingerprint changes.
- The user can add one missed item from exact selected JD text. Detection and
  checklist state never block **Save job**, **Record as applied**, or continuing
  to the posting.
- The analyzer uses bounded inputs and non-catastrophic patterns; a benchmark
  fixture covers the maximum accepted text and records timing without relying
  on a brittle CI wall-clock threshold.

**Checks:**

```powershell
python -m unittest tests.test_application_instructions tests.test_job_discovery tests.test_job_review tests.test_intake_ui -v
```

**Out of scope:** general semantic instruction extraction, LLM calls, following
links, submitting artifacts, or verifying that the employer received a step.

### `EXT-140` Add Source-Backed Contact Candidates

**Goal:** Complete captured JDs surface useful named application/recruiting
contacts for review without filling the contact ledger with arbitrary names.

**Prerequisites:** accepted `EXT-110` complete-content extraction. Permanent
storage is not a prerequisite and remains forbidden in this ticket.

**Inspect/modify:** the new bounded JD-derivation layer, discovery/job preview
schemas, local review UI, synthetic fixtures, focused tests, and documentation.

**Acceptance:**

- Extraction requires the explicit name/context/channel combinations defined in
  the user journey and stores exact evidence plus the current content hash.
- Named application recipients and recruiter/hiring-manager contacts are
  `explicit`; a clear named reporting/hiring-team reference without a direct
  channel is `review` only.
- Names in company stories, customer quotes, leadership biographies, copyright,
  privacy/legal, EEO/accommodation, and generic signatures are excluded by
  negative fixtures.
- Generic `careers@`/`jobs@` values appear as application channels, not people.
- The extractor does not infer missing surnames, roles, email addresses, social
  profiles, or employer identity and performs no network/search action.
- Popup receipt shows only a conditional candidate count. Local review shows
  the candidate fields and source evidence; **Save contact** remains disabled or
  explicitly marked **Coming after contact storage** in this ticket.
- Promoting or closing the discovery creates zero permanent contact records.

**Checks:**

```powershell
python -m unittest tests.test_contact_candidates tests.test_job_discovery tests.test_intake_ui -v
```

### `CONTACT-090` Add Contact Storage And Linking Domain

**Goal:** Provide a minimal local-first contact identity and job-linking domain
that can safely receive an explicitly confirmed candidate later.

**Inspect/modify:** the existing application SQLite schema owner, backup and
migration patterns, runtime/data-home boundaries, a new focused contact domain
module, tests, and schema documentation. Do not add UI in this ticket.

**Acceptance:**

- The existing application database gains canonical contacts, exact channels,
  append-only provenance, and many-to-many discovery/job links through a
  backup-before-migration schema change.
- Required fields are stable contact id plus at least a name or exact channel;
  role/company/context remain editable stated facts, never inferred identity.
- Exact normalized email or professional-profile URL may deduplicate. Name,
  role, or company similarity only returns a potential duplicate for review.
- Create/link/update/archive/restore operations are revisioned and transactional;
  unlinking a job preserves the contact and provenance, while archived contacts
  are excluded from new selection.
- No candidate extractor or promotion path can call create implicitly. There is
  no email sending, enrichment, scraping, relationship score, or cloud sync.
- Tests use only synthetic contacts in temporary Data Homes and verify recovery,
  stale revisions, exact deduplication, ambiguous non-merge, and cascade safety.

**Checks:**

```powershell
python -m unittest tests.test_application_contacts tests.test_application_history -v
```

### `CONTACT-100` Add Confirmed-Save UI And Contact Ledger

**Goal:** The user can review a contact candidate, explicitly save or link it,
and later find the confirmed contact without turning the page into a CRM.

**Prerequisites:** accepted `EXT-140` candidate contract and `CONTACT-090`
storage/domain evidence.

**Inspect/modify:** discovery/job-workspace detail, one compact Contacts ledger
surface using existing list/card patterns, local API adapter, focused UI/API
tests, locale strings, and manual-testing documentation.

**Acceptance:**

- **Save contact** opens a prefilled editable confirmation with the exact source
  evidence and potential-duplicate suggestions; nothing is persisted on open,
  close, capture, or promotion.
- Confirming creates or links one contact idempotently and shows its linked
  company/jobs plus stated role/channel and source provenance.
- The compact ledger supports search, active/archived toggle, detail, edit,
  archive/restore, and links back to the relevant application; it has no sales
  pipeline, reminders, outreach automation, or relationship score.
- Exact channel duplicates suggest linking to the existing contact. Name/company
  ambiguity always requires a choice and never overwrites either record.
- Removing a link does not delete a contact; destructive deletion is deferred
  unless a separately guarded no-reference contract is approved.

**Checks:**

```powershell
python -m unittest tests.test_application_contacts tests.test_intake_ui -v
```

### `HELP-090` Scope The Application-Helper Capability

**Goal:** Define and prove the least-privilege boundary for quick-copy/prefill
data before the extension receives any profile value.

**Inspect/modify:** local API boundary, Career Vault profile/contact schema,
runtime path/data-safety rules, extension threat model in this plan, and focused
negative authorization tests.

**Acceptance:**

- The user explicitly enables a canonical allowlist of helper fields in Career
  Hub; disabled fields are absent, not blank placeholders.
- A purpose-specific endpoint returns only that curated projection. It cannot
  read arbitrary Vault records, documents, resumes, applications, or secrets.
- Capture and helper capabilities are independently route/method allowlisted;
  a valid token for one purpose cannot call the other purpose's unrelated
  routes.
- Passwords, OTPs, SSNs, payment data, document contents, and voluntary EEO,
  disability, or veteran answers are rejected by schema.
- Values never enter logs, URLs, diagnostics, extension storage, or fixtures.

**Checks:** focused API/security tests plus the existing extension boundary
matrix.

### `HELP-100` Add The Quick-Copy Side Panel

**Goal:** While completing an application, the user can keep a small persistent
helper open and copy one enabled common value at a time.

**Prerequisites:** accepted `HELP-090` boundary.

**Modify:** packaged MV3 side-panel UI/worker files, synthetic protocol/render
fixtures, focused tests, and extension documentation. Keep plain
HTML/CSS/JavaScript and request only the minimum new permission.

**Acceptance:**

- The capture popup remains compact and opens the helper as a separate surface.
- Each row shows canonical label, masked/preview-safe value where appropriate,
  and one explicit Copy action with accessible success feedback.
- Values are fetched on demand and discarded when the surface closes; no
  extension storage is introduced.
- Copy does not inspect or modify the application page and never copies all
  fields in one action.
- Offline, disabled-field, stale-revision, and incompatible-protocol states are
  actionable without exposing values in error messages.

**Checks:** focused worker/render tests and a manual side-panel smoke with
synthetic helper values only.

### `HELP-110` Add Explicit Reviewed Prefill

**Goal:** On one active application page, the user can review a bounded mapping
of supported empty controls and explicitly fill selected fields without any
submission behavior.

**Prerequisites:** accepted `HELP-100` quick-copy dogfood and a separate privacy
review for transmitting the selected values to the current page.

**Modify:** active-tab extractor/filler, side-panel review UI, synthetic form
fixtures, focused tests, and manual-testing documentation only.

**Acceptance:**

- One user action inspects only visible supported controls in the main frame;
  no persistent content script, background scan, cross-origin frame access, or
  broad host permission is added.
- The review displays each proposed canonical field, target label, and whether
  the target is empty. Nothing is written before **Fill selected fields**.
- Non-empty, ambiguous, hidden, credential, file-upload, free-text, and excluded
  sensitive controls are skipped by default.
- The filler never clicks Continue/Next/Submit, accepts terms, uploads a resume,
  or answers screening questions.
- Synthetic Workday-like, Greenhouse-like, Lever-like, and plain HTML fixtures
  cover supported, ambiguous, non-empty, and dynamically rerendered fields.

**Checks:** focused fixture/protocol tests plus a manual no-submit sandbox form
smoke.

### `MAIL-100` Implement Pure `.eml` Parsing

**Goal:** A bounded deterministic parser turns synthetic alert emails into zero
or more discovery-candidate values without writes or network access.

**Modify:** new `src/email_alerts.py`, new `tests/test_email_alerts.py`, and
synthetic fixtures under `tests/fixtures/email_alerts/`.

**Acceptance:**

- Plain text, multipart alternative, HTML links, multiple candidates, malformed
  charset, duplicate message, tracking/unsubscribe links, attachment, and limit
  violations are covered.
- The parser explicitly sets `needs_content` when it has no complete JD.
- Recipient addresses, attachments, and tracking pixels are not returned.
- The result preserves exact blocker-relevant wording but does not decide
  eligibility or fit; assessment remains in the existing evaluator.
- No network, credential, SQLite, or Data Home write occurs.

**Checks:**

```powershell
python -m unittest tests.test_email_alerts -v
```

### `MAIL-110` Add Explicit Email Import

**Goal:** Discovery Inbox imports one or more `.eml` files and merges their
candidates through the discovery service.

**Modify:** email import route, Discovery UI, discovery/email services, focused
tests, and manual-test documentation.

**Acceptance:**

- Upload count/size limits and per-file results are visible.
- One malformed email does not discard valid siblings in the same user action.
- Reimport is idempotent and records last seen/provenance without duplicates.
- Link-only results can open in the normal browser for extension completion.
- Incomplete alerts enter **Needs JD** and cannot be promoted or presented as a
  high-confidence match.
- No original `.eml` is retained by default after successful parsing.

**Checks:**

```powershell
python -m unittest tests.test_email_alerts tests.test_job_discovery tests.test_intake_ui -v
```

### `MAIL-200` Confirm Dedicated Gmail Connector

**Goal:** Record whether dedicated Gmail IMAP with an app password is acceptable
and available for private dogfood after `.eml` import is proven.

**Modify:** one new ADR only. No connector code, dependency, secret, or network
call is authorized by this ticket.

**Acceptance:**

- The dedicated account boundary, recovery setup, and no-personal-mail rule are
  explicit.
- App Password eligibility is confirmed without recording the secret.
- IMAP versus official API is compared for auth support, scopes, refresh,
  dependencies, packaging size, revocation, and failure behavior.
- Exact secret-store keys and non-secret account/folder/UIDVALIDITY/cursor fields
  are specified.
- Sender allowlist and unknown-sender review behavior are specified.
- Manual disconnect, cursor reset, and retention behavior are specified.
- User explicitly approves any new dependency and provider registration.
- If App Passwords are unavailable or broad IMAP access is rejected, the ADR
  chooses `.eml` import until a local OAuth connector is separately approved.

### `MAIL-210` Implement Manual Read-Only Mail Sync

**Goal:** **Sync now** imports unseen alerts from the one approved folder without
modifying mailbox state.

**Prerequisite:** accepted `MAIL-200` ADR and explicit authorization for its
network/dependency/secret scope.

**Acceptance:**

- Least available read scope and Windows Credential Manager are used.
- Sync selects only the approved folder, uses UID/provider immutable ids,
  validates UIDVALIDITY, and has bounded pages, timeouts, retry/backoff, and a
  single-run lock.
- It fetches selected headers first and uses non-mutating body retrieval only
  for allowed senders.
- It never marks read, moves, deletes, replies, forwards, or connects to SMTP.
- Cursor advances only after local write succeeds.
- Repeated sync is idempotent.
- Disconnect removes the secret and leaves discoveries intact.
- Logs contain counts/codes/timing only, not subject/body/URL/token/address.

**Checks:** provider adapter unit tests with fakes, discovery integration tests,
and one explicitly authorized manual sandbox sync.

### `MAIL-220` Add Local Scheduling

**Goal:** The approved connector can run once at startup, periodically while the
app is open, and optionally as one Windows Task Scheduler invocation.

**Acceptance:**

- Manual **Sync now** remains available.
- Interval is bounded and user controlled; 30 minutes is the dogfood default
  while the app is open, and failures use backoff.
- One-shot CLI is non-interactive after setup and exits cleanly.
- Concurrent app/Task Scheduler runs do not duplicate or corrupt data.
- Install/update does not silently enable scheduling.
- A missed sleeping/offline scheduled run is recovered by the next startup sync.
- Disable/uninstall steps are documented and tested.

**Out of scope:** site watchers, browser automation, tray daemon, cloud webhook.

### `MAIL-230` Suggest Unknown Job-alert Senders

**Goal:** Help the user triage the header-only sender queue without weakening
exact-sender approval or storing private Subject text.

**Acceptance:**

- The read-only header request includes Subject, but no raw Subject is written
  to SQLite, API output, logs, diagnostics, or UI.
- Deterministic sender/provider and Subject phrases can mark an unknown sender
  **Suggested job alert** with fixed reason codes.
- A neutral sender becomes **Recurring — review** only after four messages on
  three distinct valid dates across at least 14 days.
- Service/security Subject signals suppress both recommendation types.
- Suggestions only order and annotate the pending queue. They never create a
  rule, fetch a body, import a discovery, or bypass **Allow & sync**.

**Checks:** schema migration test, header parser and privacy assertions,
recurrence/service suppression tests, API/static assertions, and rendered
sender-review smoke test.

### `MAIL-240` Harden And Close Email Intake

**Goal:** Close the implemented email slice without expanding it: bound
provider-specific parser work, preserve discovery data during parser-v1 repair,
and replace stale implementation evidence with the completed dogfood result.

**Acceptance:**

- HTML-link processing has an explicit fail-closed limit before provider
  grouping, while normal multi-anchor LinkedIn cards still deduplicate.
- LinkedIn card-marker cleanup has a bounded raw field and iteration budget.
- Parser-v1 repair takes its final plan under a SQLite write transaction and
  rejects a change made after preview instead of overwriting it.
- A duplicate group with exactly one retained source keeps the source-bearing
  discovery and its source file.
- Setup, manual-test, implementation-checkpoint, and acceptance-registry
  evidence describe the implemented connector and real provider-attributed
  Feed cards without storing private message content.

**Checks:**

```powershell
python -m unittest tests.test_email_alerts tests.test_linkedin_discovery_repair -v
python -m unittest discover -s tests
```

**Out of scope:** new providers, link resolution, extension implementation,
watchers, OAuth, AI parsing, or changes to mailbox policy.

### `MAIL-250` Separate Mixed LinkedIn Application Outcomes

**Goal:** Treat one LinkedIn message containing an application result and
recommended jobs as two different entity types without creating a sparse saved
application.

**Acceptance:**

- A rejection requires the exact supported LinkedIn sender/Subject/body pattern
  and a numeric posting id; generic negative text does not change status.
- The rejected posting is excluded from active Feed and an already-active
  unpromoted discovery with that id is dismissed.
- Recommendations from the same message remain normal deduplicated discoveries.
- One exact existing application can move from Captured/Preparing/Applied/
  Interview to Rejected; Offer/Withdrawn/Closed are protected and reported for
  review.
- An unmatched outcome is retained as minimized local status metadata and never
  creates a JD, saved job, or application.
- Repeat import is idempotent and the raw email/rejection paragraph is not
  retained.

**Checks:**

```powershell
python -m unittest tests.test_email_alerts tests.test_email_imports -v
```

**Out of scope:** broad natural-language outcome classification, automatic
application creation, inbox mutation, or outcomes from unverified providers.

### `MAIL-260` Separate Mixed Handshake Submission Confirmations

**Goal:** Treat one supported Handshake message as a transport envelope that
may contain an application-submitted event, embedded job recommendations, and
service links without allowing one section to determine the others.

**Acceptance:**

- A submission event requires the exact
  `handshake@notifications.joinhandshake.com` sender, the supported
  `Application sent to <company> - here's what's next` Subject form, and the
  matching `Your application was sent to <company>` body heading.
- The event maps to the existing **Applied** application status. Company-only
  evidence never auto-matches or creates an application; it remains unlinked
  until the user explicitly selects an existing application.
- Manual linking uses an optimistic event revision, never downgrades Interview
  or Offer to Applied, and reports status conflicts instead of overwriting a
  closed application.
- `Apply to similar jobs` cards are extracted independently with provider,
  company, role, and location. A tracking-only sender creates no Feed records;
  the explicit combined route enables those embedded recommendations.
- `View application`, preference/unsubscribe links, generic Handshake messages,
  and event notices create neither jobs nor contacts.
- Repeat import and repeat linking are idempotent. Opaque application redirect
  tokens and original email bytes are not retained.

**Checks:**

```powershell
python -m unittest tests.test_email_alerts tests.test_email_imports `
  tests.test_email_sync tests.test_email_sync_api -v
python -m unittest discover -s tests
```

**Out of scope:** user-authored CSS/XPath/regex extraction, arbitrary status
phrase classification, automatic company-only linking, following authenticated
Handshake redirects, inbox mutation, or contact creation from generic messages.

### `INT-300` Dogfood And Close Capabilities

**Goal:** Complete an end-to-end sandbox and real-data-approved smoke matrix,
then update capability status only for the slices whose evidence passed.

Required scenarios:

1. extension link -> needs content;
2. extension selection -> partial, no false gap verdict;
3. extension page -> preview assessment;
4. email link -> open -> extension page -> same discovery;
5. duplicate email and duplicate click -> one discovery, multiple observations;
6. promote to Saved -> one source/card/ledger record;
7. promote retry -> same job id;
8. promote to Applied -> one Applied transition;
9. hostile origin/token and extension access to unrelated APIs -> rejected;
10. backup/restore -> discoveries, observations, and source snapshots recover;
11. no discovery appears in Demand & coverage before promotion; and
12. no private source, secret, or token appears in Git or diagnostics;
13. a clear advisory blocker keeps Continue, Save, and Record as applied
    available and preserves an explicit accepted-risk decision;
14. end-of-JD email/video/exact-phrase instructions become exact-source
    checklist items, while EEO/privacy/benefits boilerplate does not;
15. changed JD wording makes prior instruction completion visibly stale and a
    manually added exact-text item survives normal reloads;
16. an explicit named recruiter/application recipient becomes a contact
    candidate, while unrelated names and generic job mailboxes do not create a
    person;
17. capture/promotion alone creates no contact, while confirmed Save contact is
    idempotent and links the reviewed source; and
18. quick copy stores no extension values and explicit prefill leaves non-empty
    fields untouched and never submits the synthetic form;
19. a corrected discovery field survives recapture/rescan, remains visibly
    separate from extracted evidence, and clearing it restores the derived
    value; and
20. a bound application session survives a synthetic multi-step DOM change,
    while validation failure, Continue/Next, tab reuse, and an unrelated job
    cannot mark the application Applied.

## Goal-Mode Completion Record

The persistent objective below completed on 2026-08-19 and is retained as the
roadmap's scope and safety record:

```text
Complete the attended Career Hub browser-extension roadmap in
docs/product/job_discovery_capture_plan.md in its locked order, beginning with the
current INT-300 gate and completing its security/privacy/recovery matrix.
Preserve
the local-first Data Home boundary and existing Discovery/Job/Application
domains. Every blocker and extra-step finding is advisory: never prevent the
user from continuing, saving, or recording an application. Never auto-create a
contact, auto-fill without reviewed user action, submit an application, access
credentials, add AI/network calls, scrape in the background, stage/commit/push,
or use real private data without explicit approval. For each ticket: inspect
dirty work first, verify prerequisites, implement the smallest accepted slice,
run its focused tests/manual gate, update the plan and acceptance registry only
when evidence passes, then proceed to the next ticket. Completion requires the
INT-300 matrix to pass; otherwise report the exact current ticket and blocker.
```

Completion gate: `INT-300` passed. The complete row-by-row evidence and residual
tool limitations are recorded in `docs/history/int_300_evidence.md`. The next product
step is user-attended dogfood of the reviewed helper on representative real
application forms. Public link resolution subsequently completed through
`LINK-130`; Watchers remains a separate planned capability.

## Model Assignment Contract

Every implementation prompt must contain:

- exactly one ticket id and one observable result;
- prerequisite evidence already satisfied;
- exact files allowed to inspect and exact files/directories allowed to modify;
- exact checks, including manual steps the model cannot perform alone;
- `yes` or `no` for dependencies, downloads/network, real private data,
  provider settings, secrets, Git staging/commit/push, and browser installation;
- explicit non-goals; and
- a stop condition for dirty overlapping files, missing prerequisites, or a new
  architecture/security decision.

Copyable prompt:

```text
Read AGENTS.md, START_HERE.md, docs/process/engineering_rules.md,
docs/process/workflow_rules.md, docs/process/small_model_playbook.md, and only ticket
<TICKET-ID> plus its context in docs/product/job_discovery_capture_plan.md.

Goal: <one observable result>.
Prerequisite evidence: <exact accepted tickets/tests>.
Files allowed to inspect: <exact paths>.
Files allowed to modify: <exact paths; no implicit additions>.
Acceptance criteria: <copy from the ticket>.
Required checks: <exact commands and manual checks>.
Authorization: dependencies=<yes/no>; network/downloads=<yes/no>;
real-private-data=<yes/no>; provider-settings=<yes/no>; secrets=<yes/no>;
browser-install=<yes/no>; git-stage/commit/push=<yes/no>.
Out of scope: every other ticket, scraping/watchers, browser login automation,
auto-apply, AI calls, and <ticket-specific exclusions>.

Inspect git status before editing. Preserve all pre-existing work. Stop before
editing if an allowed file has ambiguous overlapping changes or completion
needs unapproved access, dependencies, secrets, external actions, destructive
operations, or architecture changes. Use synthetic fixtures by default.

Final response: completed or blocked; summary; files changed; commands and
exact results; acceptance evidence; notes/limitations; one suggested next
ticket. Never mark a capability Implemented without its full exit evidence and
registry update.
```

## Deferred TODOs

Keep these visible but do not combine them with the tickets above:

- `CAP-JOB-WATCHING`: precision public-source watcher with per-source terms,
  rate limits, freshness, and storage policy. Its reviewed provider-adapter
  direction is defined by `PB-WATCH-100` in
  [`product_backlog.md`](product_backlog.md).
- User-triggered exact title/company web search when an alert link is protected.
- Browser-extension store release and desktop app discovery/pairing.
- Firefox support.
- AI-assisted page/email parsing after deterministic benchmark evidence.
- Auto-apply or unattended authenticated browsing.

Implementation checkpoint (2026-08-16): `MAIL-100`, `DISC-100`, `MAIL-110`,
`DISC-110`, the remaining `DISC-120` promotion/detail slice, `MAIL-200`,
`MAIL-210`, `MAIL-220`, `MAIL-230`, and `MAIL-240` are complete against
synthetic data. The
evidence now covers parser, storage, API, explicit-file import, stale revision,
failed retry, stable promotion idempotency, browser handoff, exact-sender
header-first mailbox filtering, layered email deduplication, manual resync,
opt-in startup/interval scheduling, bounded backoff, a cross-process SQLite
lease, the non-interactive one-shot CLI, and advisory header-only sender
suggestions with recurrence and security suppression.
Promotion accepts only server-retained `confirmed_full` content, reuses the
existing JD intake, ledger, and assessment path, and explicitly offers **Save
job** or **Record as applied**. Link-only, partial, and dismissed discoveries
cannot enter saved JD intake through the Feed.

The Job Feed capability is implemented for its accepted unified-feed and
attended-capture scope. Browser-local list/card preference and remaining
producers/enrichers remain later enhancements, not hidden requirements of that
scope. A user-approved live Gmail smoke on 2026-08-15 verified authentication, header-only quarantine
of unknown service senders, repeat-run deduplication, and startup sync at the
saved 30-minute interval. A later user-approved LinkedIn digest sync produced
provider-attributed Feed cards, discarded service/navigation noise, and merged
duplicate anchors by numeric posting id. Those records correctly remain
**Needs JD** until complete posting text is captured. `LINK-000` through
`LINK-130` completed the guarded local resolver route. The route remains
implemented and tested, but the Feed control was later withdrawn to keep Job
Feed a compact gateway: the visible workflow is **Open posting** followed by
attended extension capture. Title/company web search remains a separate future
capability rather than an implicit resolver fallback.

`MAIL-250` is also complete against the supported synthetic fixture and the
user-approved mixed LinkedIn rejection/recommendation message: the application
outcome is stored separately, the matching unpromoted discovery is dismissed,
same-message recommendations remain in Feed, and repeat processing is
idempotent. `MAIL-260` is complete against a minimized synthetic Handshake
submission fixture: the submitted event remains independent from three
embedded recommendations, tracking-only routing creates no Feed noise, an
unidentified role remains unlinked, and explicit revision-guarded linking can
move one selected application to Applied without inventing an application.
`EXT-090` is complete with an exact extension route/method allowlist,
capture-only preflight, negative GET/POST/PUT/DELETE coverage, and preserved
local-browser behavior. `EXT-095` is also complete with the extension-only
version-1 link/feed handler, exact schema rejection, retry conflict handling,
email/extension identity merge, content-minimized provenance, and non-persistent
launcher Origin passthrough. `EXT-100` is complete with a plain MV3 link-only
client, fixed-command worker boundary, accessible popup, developer guide,
protocol harness, isolated Chromium render/contract smoke, and the user's
five-surface unpacked-toolbar sandbox dogfood on 2026-08-18. `EXT-110` is also
complete: the extension now offers explicit page, selection, and link modes;
its isolated MV3 smoke covers JSON-LD, semantic, guarded-body, malformed
JSON-LD, login, multi-job/search ambiguity, oversized, dedup, and offline paths
without creating a Job or Application. `EXT-110R` adds provider-hinted and
title-anchored ranking with synthetic Indeed, LinkedIn, generic split-pane, and
Handshake shell coverage; selectors cannot bypass the shared contamination
validator. `EXT-115` is complete with schema-v18
field overrides, optimistic edit conflicts, exact LinkedIn/Indeed/Handshake/
MeeBoss/12twenty identity fixtures, one-to-one legacy identity repair, and a
desktop/mobile rendered correction/clear/reload smoke. `EXT-120` is complete
with source/candidate/context-versioned page previews, partial-capture safety,
source-backed narrow conflict receipts, unknown-vocabulary review, a minimized
extension response, email-to-page merge coverage, and an isolated rendered MV3
smoke. `EXT-125` is complete with schema-v19 verification provenance, an exact
server-issued review deep link that cannot accept an arbitrary route or JD,
source/assessment context before controls, no-write **Review later**, explicit
complete-JD confirmation, retry-safe Saved/Applied promotion, and desktop/mobile
rendered handoff smoke. `EXT-127` is complete with schema-v21 session/event
storage, exact URL claim plus tab/Origin capabilities, transient
`chrome.storage.session` lifecycle, paired native-submit/success detection,
browser-only Applied confirmation, interruption/expiry recovery, focused
domain/API/security coverage, worker lifecycle tests, and desktop/mobile
isolated Chromium smoke. `EXT-130` is complete with revisioned private
eligibility facts, a revisioned three-class policy, exact evidence/profile
receipts, current-fact saved-JD and Discovery reclassification, extension API
denial, and an isolated desktop/mobile persistence smoke. `EXT-135` is complete
with the bounded deterministic analyzer, strict normalized-card schema,
complete/partial Discovery projection, conditional extension count, append-only
reversible checklist actions, stale-source reset, exact-selection manual add,
focused analyzer/API/worker coverage, and isolated desktop/mobile rendered
persistence smoke. `EXT-140` is complete with a bounded deterministic analyzer,
exact-source and normalized-source hashes, source-backed
identity/channel/context evidence, generic application-channel handling,
complete/partial Discovery and Job projections, conditional popup count,
explicit disabled future-save control, positive/negative fixtures, and isolated
desktop/mobile plus packaged-extension smoke. `CONTACT-090` is complete with a
named recovery backup before schema v23, canonical contacts and exact channels,
append-only exact-source/manual provenance, many-to-many Discovery/Job links,
exact email/profile deduplication, bounded potential-duplicate review,
transactional optimistic revisions, archive/restore selection rules, and
synthetic stale-write/cascade/recovery coverage. `CONTACT-100` is complete with
browser-only current-source candidate validation, editable non-mutating preview,
explicit retry-safe create/link confirmation, exact/potential duplicate review,
compact searchable active/archived ledger, edit/unlink/source navigation, API
security coverage, and isolated desktop/mobile rendered smoke. `HELP-090` is
complete with an empty-by-default closed field catalog, strict revisioned
configuration, enabled-and-present purpose projection, a capability independent
from capture, negative authorization/omission/forbidden-field coverage, and
desktop/mobile configuration smoke. `HELP-100` is complete with a separate
Chrome side panel, compact popup entry, on-demand closed projection,
preview-safe masking, revision-checked one-field clipboard writes, memory-only
helper values, actionable recovery states, focused protocol/storage-negative
coverage, and isolated desktop/compact/offline rendered smoke. `HELP-110` is
complete with explicit bounded main-frame review, a value-free proposed mapping
list, disabled non-empty and ambiguous targets, one-shot memory-only review
tokens, current source/page/form/empty-state revalidation, selected-field-only
input/change events, no added permission, four provider-style/dynamic fixtures,
stale-form zero-write coverage, and rendered no-submit smoke. `INT-300` is
complete: all twenty required scenarios passed synthetic automation plus the
earlier approved real-browser capture gate. The recovery test now restores the
Discovery, all observations, and its exact source snapshot; security scans and
their reviewed limitations are recorded in `docs/history/int_300_evidence.md`.
