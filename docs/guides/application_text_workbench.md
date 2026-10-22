# Application Text Workbench

This is the dogfood contract for job-specific writing created during a real
application session. It gives a Terra session one predictable place and format
for application answers, recruiter messages, follow-ups, legacy inline cover
letters, and unusual cases discovered while writing them. Career Hub does not
yet expose this general text workbench in the browser and does not generate
these texts itself. New structured cover-letter documents use the separate
browser and renderer contract in [`application_materials.md`](../reference/application_materials.md).

## Boundary

Application text is derived, job-specific working material. It is not Career
Vault evidence, a Skills Registry entry, an application status, or proof that a
form was submitted.

The application ledger remains authoritative for stage, dates, and the Resume
Version actually submitted. The normalized job card remains authoritative for
the preserved JD. Career Vault remains authoritative for candidate facts.

Do not store passwords, security-question answers, government identifiers,
birth dates, payment data, voluntary demographic answers, or document contents
in a text workbench. These YAML files use the normal private data-home ACL and
backup boundary; they are not independently encrypted.

## Private Storage

When a Preparing or Applied job needs job-specific writing, use:

```text
<active-data-root>/outputs/application_texts/<job_id>/application_texts.yaml
```

The directory is private mutable data, belongs outside the Git checkout, and is
covered by the existing `outputs/` ignore and recovery-backup rules. Never create
a fallback copy in tracked source files when the selected data home is not
writable. Return the complete copy-ready text in the task and report `artifact
not written` instead.

Create no workbench for a job with no saved normalized card. Anchor the file to
the normalized card's current `content_fingerprint`; if the fingerprint changes,
review every text item before reuse.

Agents should write through the validated adapter rather than assembling YAML
manually. Supply task intent inline, then supply one JSON draft on stdin or with
`--payload-file`:

```powershell
& $agent context --server $server --job-id <job_id> --purpose application_text --task-request-json '<json>'
Get-Content -Raw .\draft.json | & $agent save-draft --server $server --job-id <job_id> --stdin
```

`$agent` and `$server` come from the desktop app's **Settings > Local agent
connection** (the verified `career-hub-agent` path and the current server).

The bounded task request contains `schema_version: 1`, purpose
`application_text`, one allowed `kind`, the exact employer prompt, and only
limits the employer actually supplied. It may also contain the id of the one
workbench item being revised. `--task-request-file` remains available for
backward compatibility. The returned `task` projection preserves the exact
intent inside the packet.

Without a related item id or exact-prompt match, the adapter returns at most
three recent workbench summaries and omits their response text. For a related
item it returns only that item and caps prior response text at 6,000 characters.
This prevents unrelated application answers from consuming context or becoming
accidental source material.

The command validates allowed fields, limits, item ids, application stage, and
JD freshness; creates or updates only `draft` items; and returns
`no_change_needed` for identical content. Approved and submitted items are
immutable through this agent command.

## Version 1 Shape

```yaml
schema_version: 1
job_id: example_role
jd_fingerprint: 64-character-lowercase-sha256
items:
  - id: why_this_role
    kind: short_answer
    source: application_form
    exact_prompt: Why are you interested in this role?
    limits:
      characters: 500
      words: null
    response: |
      Copy-ready answer goes here.
    evidence_refs:
      - experience:example_experience
      - job:responsibilities
      - user_confirmation:reason_for_interest
    manual_checks: []
    state: draft
observations:
  - id: observation_001
    kind: vocabulary_candidate
    source: jd
    source_ref: job:example_role#line:12
    exact_source_text: Named term as it appears in the source.
    reason: Why it could not be resolved safely.
    recommended_action: Review the canonical name and primary group.
    state: open
```

Allowed `kind` values for text items:

- `cover_letter`
- `short_answer`
- `long_answer`
- `recruiter_message`
- `follow_up`
- `other`

Allowed item states are `draft`, `user_approved`, and `submitted`. A model may
create or revise `draft`; only an explicit user confirmation permits
`user_approved`, and only the real external action permits `submitted`.

Use one item per exact employer prompt. Keep an unknown limit as `null`; never
invent one. `evidence_refs` should point to canonical Vault ids when possible,
the saved job field for employer-specific context, or an explicit user
confirmation from the current task. `manual_checks` must contain every claim or
placeholder that still needs confirmation.

Allowed observation kinds:

- `vocabulary_candidate`
- `parser_miss`
- `candidate_fact_candidate`
- `reusable_answer_candidate`
- `product_defect`
- `workflow_edge_case`

An observation is a review queue. It never mutates the core catalog, custom
catalog, Career Vault, parser rules, or application ledger by itself. Preserve
the exact source wording plus a job/resume/form and line reference when
available, and explain the proposed next action. Observation states are `open`,
`resolved`, and `dismissed`; resolve one only after the corresponding explicit
workflow succeeds.

## Writing Rules

- Answer the exact question instead of pasting a generic biography.
- Respect the strictest stated character, word, format, and attachment limit.
- Prefer verified examples over adjectives and copied JD prose.
- Do not infer company admiration or research. Ask for one honest reason for
  interest when it materially affects the answer.
- A full cover letter complements the selected resume; it does not repeat every
  resume bullet. Use three direct body paragraphs: confirmed motivation,
  verified evidence, and a concise close. Never infer motivation; use a clear
  personalization placeholder plus `manual_checks` until it is confirmed. An
  inline letter stays in `prepare`/`context` plus `save-draft`; use
  `material-context` only when an editable or exportable document was requested.
- Applicant prose must not contain em dashes, en dashes, or the explicit stock
  phrases enforced by `backend/internal/applicationtexts` (`draft.go`).
- Preserve a submitted item as history. A later revision becomes another item
  with a distinct id; do not silently rewrite what was sent.

## Token-Efficient Reuse

A later task should reuse the adapter's context snapshot and request only its
exact workbench item before reopening broad raw sources. Reusable wording is
still reviewed against the current JD and candidate facts. A prior answer is
never evidence merely because it was previously sent.
