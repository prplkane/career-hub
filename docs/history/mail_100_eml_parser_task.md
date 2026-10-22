# MAIL-100 Task: Pure `.eml` Parser

This file is a ready-to-assign implementation contract for a lower-cost model.
It implements only the first read-only email slice from
[`job_discovery_capture_plan.md`](../product/job_discovery_capture_plan.md).

## Goal

Given one bounded synthetic `.eml` byte sequence, return a deterministic,
privacy-minimized description of zero or more job-alert candidates. The task
must perform no network call, credential access, database write, Data Home
write, job intake, assessment, or vocabulary mutation.

## Prerequisite Evidence

- `CAP-EMAIL-ALERT-INGEST` is **Planned**; this ticket does not mark it
  Implemented.
- `requirements.in` has no email/HTML parsing dependency and must remain
  unchanged.
- The output will later feed `DISC-100`/`MAIL-110`; those capabilities are not
  prerequisites for this pure parser.

## Required Reading

- `AGENTS.md`
- `START_HERE.md`
- `docs/process/engineering_rules.md`
- `docs/process/workflow_rules.md`
- `docs/process/small_model_playbook.md`
- `docs/product/job_discovery_capture_plan.md`, only **Email Intake Design**,
  **Security And Privacy Gates**, and ticket `MAIL-100`
- `docs/guides/email_alert_setup.md`, only **What Career Hub Will Read** and **How
  Messages Become Todos**

## Files Allowed To Inspect

- all required-reading files above;
- `requirements.in`;
- `src/ingest_job.py` only for existing HTTP/HTTPS URL validation style;
- `src/intake_error_catalog.py` only for stable-error style; and
- two small representative unit-test modules selected after inspection for
  repository `unittest` conventions.

## Files Allowed To Modify

- new `src/email_alerts.py`;
- new `tests/test_email_alerts.py`; and
- new synthetic files below `tests/fixtures/email_alerts/`.

No other file may change. If one of these paths already has unowned or
overlapping work, stop before editing.

## Authorization

- Dependencies: **no**
- Downloads or network: **no**
- Real private email/data: **no**
- Gmail/provider settings: **no**
- Secrets or Credential Manager: **no**
- Database/Data Home writes: **no**
- UI/API changes: **no**
- Git stage/commit/push: **no**

## Public Python Contract

Create these names in `src/email_alerts.py`:

```python
EMAIL_ALERT_SCHEMA_VERSION = 1
MAX_EMAIL_BYTES = 10 * 1024 * 1024
MAX_MIME_PARTS = 200
MAX_NESTED_MESSAGE_DEPTH = 10
MAX_DECODED_TEXT_CHARS = 500_000
MAX_CANDIDATES_PER_MESSAGE = 100

class EmailAlertError(ValueError):
    code: str

def parse_email_alert(content: bytes) -> dict[str, object]: ...
```

`EmailAlertError` must accept and expose one stable `code`. Do not print or log
message content.

The result shape is:

```json
{
  "schema_version": 1,
  "message_fingerprint": "sha256:<lowercase hex>",
  "message_id_hash": "sha256:<lowercase hex or empty>",
  "sender": {
    "address": "alerts@example.test",
    "domain": "example.test"
  },
  "subject": "One synthetic alert subject",
  "received_at": "normalized RFC/ISO value or empty",
  "candidates": [
    {
      "ordinal": 1,
      "title": "Synthetic role or empty",
      "company": "Synthetic company or empty",
      "location": "Synthetic location or empty",
      "source_url": "https://example.test/jobs/123",
      "source_text": "bounded exact candidate block",
      "content_scope": "link_only|snippet|description_candidate",
      "source_recommendation": {
        "kind": "great_match|top_match|strong_match|recommended_for_you|none",
        "evidence_text": "exact bounded phrase or empty"
      }
    }
  ],
  "ignored_link_count": 0,
  "warnings": [
    {"code": "STABLE_CODE", "message": "content-free guidance"}
  ]
}
```

Returning ordinary dictionaries keeps the boundary JSON-ready. Internal frozen
dataclasses are optional; do not add Pydantic for this one pure module.

## Parsing Rules

1. Require non-empty `bytes`. Reject more than `MAX_EMAIL_BYTES` before MIME
   parsing.
2. Parse with `email.parser.BytesParser(policy=email.policy.default)` and an
   explicit policy.
3. Count all MIME parts and nested `message/rfc822` depth. Reject the whole
   message when either structural limit is exceeded.
4. Ignore parts with attachment disposition or a filename. Do not return
   attachment bytes, names, or metadata.
5. Prefer one HTML representation for candidate links in `multipart/alternative`;
   use plain text as the fallback. Do not concatenate equivalent HTML and plain
   bodies into duplicate candidates.
6. Decode declared charsets defensively. A recoverable malformed charset emits
   a content-free warning; an unrecoverable message raises a stable error.
7. Sum decoded text before candidate extraction. Reject the message rather than
   silently truncating when `MAX_DECODED_TEXT_CHARS` is exceeded.
8. Use only the standard-library `html.parser.HTMLParser`. Ignore script, style,
   form, input, textarea, select, SVG/image/pixel content, comments, and hidden
   non-text metadata. Never render or execute HTML.
9. Accept only absolute HTTP/HTTPS candidate URLs. Preserve the observed URL;
   do not fetch, resolve, canonicalize away unknown query parameters, or follow
   redirects.
10. Ignore obvious unsubscribe, preferences, privacy, account, login, help,
    social, image-only, `mailto:`, `javascript:`, and data links using narrow,
    reviewable rules. A tracking redirect with a visible job title must remain a
    candidate link because it may be the only usable posting URL.
11. Create at most one candidate per unambiguous job block/link. Multiple job
    blocks become multiple candidates in source order.
12. If a title/company/location is not explicit in that bounded block, return an
    empty field. Never infer it from sender identity or unrelated email prose.
13. `source_text` preserves the normalized exact visible candidate block. It is
    not a generated summary.
14. Use `description_candidate` only when a bounded block contains substantial
    qualification/responsibility text; this is still not proof of a complete JD.
    Use `snippet` for short descriptive text and `link_only` when only label/link
    metadata exists.
15. Detect a source recommendation only from exact, case-insensitive visible
    phrases in the same candidate block: `great match`, `top match`, `strong
    match`, or `recommended for you`. Preserve the exact matched source phrase
    in `evidence_text`.
16. A source recommendation never changes `content_scope`, never becomes a fit
    score, and never invokes Career Hub assessment.
17. Do not classify hard blockers in this module. Preserve exact blocker-relevant
    wording in `source_text`; the existing eligibility/experience evaluator owns
    later decisions.
18. Never return To/Cc/Bcc/recipient addresses, raw headers, full MIME bytes,
    attachment data, cookies, tracking pixels, or secrets.
19. `message_id_hash` hashes the normalized Message-ID value and never returns
    it verbatim. Use whole-message SHA-256 as the always-present fingerprint.
20. Parse `From` to one normalized address/domain for source-policy review. A
    missing or malformed sender produces a warning and empty values rather than
    invented identity.

## Stable Error Codes

At minimum implement and test:

- `EMAIL_EMPTY`
- `EMAIL_TYPE_INVALID`
- `EMAIL_TOO_LARGE`
- `EMAIL_MIME_LIMIT_EXCEEDED`
- `EMAIL_NESTING_LIMIT_EXCEEDED`
- `EMAIL_TEXT_LIMIT_EXCEEDED`
- `EMAIL_CANDIDATE_LIMIT_EXCEEDED`
- `EMAIL_PARSE_FAILED`

Error messages must identify the violated contract without copying subject,
sender, body, URL, or other message content.

## Synthetic Fixtures

Use only reserved/example identities and domains. No real user address, company,
job description, tracking token, or private message may enter Git.

Create the minimum fixture set:

1. `single_great_match_html.eml`: one HTML job block and explicit `great match`.
2. `multi_job_digest_html.eml`: two unambiguous jobs plus account/unsubscribe
   links.
3. `plain_text_alert.eml`: one absolute job URL and a short snippet.
4. `multipart_alternative.eml`: equivalent text/plain and text/html bodies,
   yielding one candidate rather than two.
5. `attachment_and_tracking.eml`: ignored attachment, pixel, preferences, and
   an allowed tracked job link.
6. `malformed_charset.eml`: recoverable decode problem with stable warning.
7. `no_job_links.eml`: valid message producing zero candidates.

Generate oversize, nesting, MIME-part, and candidate-count boundary inputs in
the test module instead of committing large fixture files.

## Required Tests

Tests must cover:

- every fixture outcome and source ordering;
- all stable error codes;
- exact limit values and one-over-limit failures;
- deterministic repeated parsing;
- Message-ID never returned verbatim;
- recipient headers never returned;
- attachment names/content never returned;
- script/style/form/hidden/pixel text excluded;
- source recommendation preserves exact evidence but does not alter scope;
- `great match` outside a candidate block does not automatically label every
  candidate;
- unknown fields remain empty rather than inferred;
- job tracking redirect retained while unsubscribe/account links are ignored;
- no filesystem mutation, network call, SQLite access, logging, or stdout.

Do not monkeypatch away the parser's core behavior. Small constructed messages
are acceptable for limit tests.

## Required Commands

```powershell
python -m unittest tests.test_email_alerts -v
git diff --check
git status --short
```

Review the final diff and confirm only the allowed new paths changed. Do not run
the full suite unless a focused failure indicates a shared regression.

## Stop Conditions

Stop without editing or stop at the current safe point when:

- an allowed path contains ambiguous pre-existing work;
- behavior requires sender-specific production knowledge not represented by a
  synthetic fixture;
- a dependency, browser, network request, credential, provider setting, real
  email, database, Data Home, UI/API change, or architecture decision is needed;
- a candidate cannot be extracted without guessing title/company/location; or
- the focused test fails for unrelated existing work after the failure is
  isolated and reported.

## Expected Final Response

1. Status: completed or blocked.
2. Summary.
3. Files changed.
4. Commands and exact results.
5. Acceptance evidence, including fixture cases.
6. Notes/limitations.
7. One suggested next ticket: `DISC-100` if completed.

Do not claim `CAP-EMAIL-ALERT-INGEST` is Implemented. `MAIL-100` is only a pure
parsing foundation.
