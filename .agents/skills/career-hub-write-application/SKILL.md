---
name: career-hub-write-application
description: Draft or revise requested Career Hub cover letters, screening answers, recruiter messages, or follow-ups from evidence. Create documents only on request; never claim an external submission.
---

# Write Career Hub Application Text

Use the running Go desktop's **Settings > Local agent connection** details. Set
`$agent` to its verified `executablePath` and `$connection` to its `connection`
file path. The file holds a scoped token that only your Windows account can read.
Keep that app open; refresh the connection after a restart. Commands below use
PowerShell invocation syntax. If no running connection is available, obtain it
before working with private data; do not start a second writer or fall back to
Python product commands or direct Data Home writes.

Use the smallest route:

- For any inline copy, including a full cover letter, screening answer, recruiter message, or follow-up, use the inline workflow below.
- Only when an editable or exportable cover-letter document was requested, read [references/cover_letter.md](references/cover_letter.md).

## Quick inline text

1. Put the exact employer prompt, any known word or character limit, and writing kind in a bounded schema-version-1 JSON object. Add `workbench_item_id` only to revise that item.
2. For a new JD being prepared, run `& $agent prepare --connection $connection --registry-mode <off|exact-auto> --purpose application_text --task-request-json '<json>' ...`. For a saved job, run `& $agent context --connection $connection --registry-mode <off|exact-auto> --job-id <job_id> --purpose application_text --task-request-json '<json>'`. The backward-compatible `--task-request-file <path>` form remains available.
3. Draft from `task`, candidate facts, direct evidence, gaps, and style rules. A related workbench item may include one prior response; unrelated items are metadata. Open broad Vault files only for an unresolved fact that could change the result.
4. For a full inline cover letter, write three direct body paragraphs: confirmed motivation, verified evidence, and a concise close. Never invent motivation; use a clear personalization placeholder and matching `manual_checks` item when it is unresolved.
5. Deliver inline. Save useful prepared-job copy with `& $agent save-draft --connection $connection --job-id <job_id> --stdin`; reuse an item id for draft revisions, but create a new id after approval or submission. Do not call `material-context` for inline copy.

## Writing rules

- Preserve the exact prompt and hard limit.
- Use plain, specific language; avoid generic praise, repeated JD prose, fake enthusiasm, biography dumps, and formulaic transitions.
- Never invent experience, metrics, research, referrals, authorization, availability, or motivations. Put unresolved facts in `manual_checks`; JD vocabulary is not evidence.
- Do not use em dashes, en dashes, or these exact case-insensitive stock phrases: "I am writing to apply", "I am writing to express my interest", "I am excited to apply", and "I would welcome the opportunity".
- Create or export a document only when requested.

Saving creates a private draft; it does not upload, send, attest, submit, or mark `Applied`. Run `record-submission` only after the user confirms the external submission.

## Return

Put copy-ready text first, followed only by essential manual checks and the compact receipt. Do not narrate routine gathering.
