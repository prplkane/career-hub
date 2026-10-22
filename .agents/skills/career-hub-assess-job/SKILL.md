---
name: career-hub-assess-job
description: Assess a supplied or saved Career Hub JD for fit, eligibility, evidence, gaps, verdict, resume choice, or exact vocabulary. Do not use for generic advice or submission tracking.
---

# Assess a Career Hub Job

Use the running Go desktop's **Settings > Local agent connection** details. Set
`$agent` to its verified `executablePath` and `$connection` to its `connection`
file path. The file holds a scoped token that only your Windows account can read.
Keep that app open; refresh the connection after a restart. Commands below use
PowerShell invocation syntax. If no running connection is available, obtain it
before working with private data; do not start a second writer or fall back to
Python product commands or direct Data Home writes.

Use the bounded adapter before reading broad Vault, resume, or governance files.

## Choose the operation

- Unsaved or opinion-only JD: run `& $agent analyze --connection $connection --registry-mode <off|exact-auto> --stdin` or use `--source-file`. This is read-only; `exact-auto` cannot write without a persisted job.
- Saved JD: run `& $agent analyze --connection $connection --registry-mode <off|exact-auto> --job-id <job_id>`.
- Only when the user intends to prepare materials or apply: run `& $agent prepare --connection $connection --registry-mode <off|exact-auto> ...`. It reuses an identical JD and moves the application at most to `Preparing`.

Use the returned verdict, eligibility, requirements, evidence, gaps, Resume Version metadata, and manual-review items. Assessment omits Resume Digest state; use `career-hub-tailor-resume` for an exact resume comparison. Open a source artifact only for a packet-identified unresolved fact that could change the result.

## Evidence and verdict

- Treat the packet's `GO`, `CONSIDER`, or `NO_GO` as advisory. Explain concrete constraints and the largest evidence gap.
- A JD requirement, registry match, or taxonomy hint is never candidate evidence.
- Do not invent scores or improvement work; no change may be the correct outcome.

## Exact vocabulary

With `exact-auto`, review only unresolved named technologies that appear verbatim in a saved JD. Check both core and custom catalogs before adding a term. Use the live service: read `server` and `token` from the `$connection` file, keep the token local and out of any output, and read `GET <server>/api/registry` with the `X-Career-Hub-Token` header. Search canonical names and aliases in `groups[].skills` and inspect origins and archive state; return only relevant matches. Reuse existing matches and group IDs. Leave ambiguous matches for review rather than writing a guessed duplicate. Run `& $agent registry-add-exact --connection $connection --job-id <job_id> --name <exact_name> --primary-group-id <group_id> --source-line <line> --confirm-exact` only when the exact name and one existing group are unambiguous. Confirmation records the semantic check; it creates no candidate evidence.

Leave ambiguous aliases, duties, degrees, years, role names, and prose as observations. Never create a candidate skill from a JD.

## Return

Give the recommendation first, then eligibility, strongest direct evidence, key gaps, suitable Resume Version, exact registry changes, and one next action. Report any writes in `write_receipt`; never record `Applied` or claim submission.
