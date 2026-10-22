---
name: career-hub-tailor-resume
description: Review or tailor one resume for a Career Hub job when asked about sufficiency, a job-specific delta, or evidence-backed wording. Do not trigger from assessment alone or force edits.
---

# Tailor a Career Hub Resume

Use the running Go desktop's **Settings > Local agent connection** details. Set
`$agent` to its verified `executablePath` and `$connection` to its `connection`
file path. The file holds a scoped token that only your Windows account can read.
Keep that app open; refresh the connection after a restart. Commands below use
PowerShell invocation syntax. If no running connection is available, obtain it
before working with private data; do not start a second writer or fall back to
Python product commands or direct Data Home writes.

## Get bounded context

For a saved job with a selected resume, put its exact Resume Version id in a bounded schema-version-1 `resume_delta` task request and run `& $agent context --connection $connection --registry-mode <off|exact-auto> --job-id <job_id> --purpose resume_delta --task-request-json '<json>'`. For a new JD, use `& $agent prepare --connection $connection --registry-mode <off|exact-auto> --purpose resume_delta --task-request-json '<json>' ...` to save and return context in one pass. The backward-compatible `--task-request-file <path>` form remains available. Without an exact selection, omit both task-request options rather than guessing.

Use the returned coverage, evidence shortlist, Resume Version state, style rules, and success states before opening a resume. Verify the selection and digest freshness; if exact text is still needed, inspect only that document or ask for it.

## Decide before editing

- Compare key JD rows with the selected resume and direct Career Vault evidence.
- Return `no_change_needed` when edits would not materially improve clarity, relevance, or ATS retrieval. There is no edit quota.
- Otherwise propose the smallest useful delta, tying each change to a requirement id and direct evidence ids.

## Typed application resume

After the Resume Plan is reviewed, use `& $agent material-context --connection $connection --job-id <job_id> --kind resume` only when the user asks to build or edit the artifact. Follow the returned editing contract:

- Preserve code-owned fields; edit only the summary and application-only bullets.
- Tie each edit to proof IDs selected in Resume Plan, and retain strong wording when rewriting adds no benefit.
- Save with `& $agent save-material --connection $connection --job-id <job_id> --kind resume --expected-revision <revision> --stdin`; add `--approve` only when validation is ready. Reuse the receipt revision and reload context only when needed.
- Export with `& $agent render-material --connection $connection --job-id <job_id> --kind resume --format <docx|pdf|txt>`.

## Boundaries

- Never turn JD vocabulary into experience, infer facts, or weaken natural wording to repeat keywords.
- Resume Plan is selection intent, not proof of a rendered or submitted resume.
- Google Docs require an explicit request; all other exports use the renderer, not a hand-built document.
- Material work may move an application to `Preparing`, never `Applied`.

## Return

Lead with `no_change_needed` or `changes_proposed`. For each change give its location, concise replacement or action, requirement id, evidence ids, and benefit. End with manual checks and the recommended Resume Version.
