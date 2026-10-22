# Career Hub Application Session Prompt

Use one task per job. Career Hub remains local-first; the agent is a reasoning
and writing layer over deterministic local commands, not an application bot.

Paste this block with the complete JD or a saved job id:

```text
Read and follow docs/guides/application_session_prompt.md.

Intended action: [assess / prepare materials / apply / update submitted record]
Posting URL: [URL or unknown]
JD source: [complete JD, attached text file, or saved job id]
Application questions or limits: [optional]
Registry mode: exact-auto

Use $career-hub-assess-job for a fit assessment,
$career-hub-tailor-resume for resume review or tailoring, and
$career-hub-write-application for requested application copy. Start directly
with the requested skill; assess first only when I ask for a fit opinion or the
selected skill reports that required job context is unavailable. Keep the
response concise and put copy-ready employer text inline unless I explicitly
request a Google Doc.
```

Set `Registry mode: off` when the session must not change private vocabulary.
`exact-auto` is permission to add only exact, unambiguous named technologies
from a saved JD to private custom vocabulary after the skill's semantic checks.
It is not permission to infer candidate experience or bulk-expand the core
catalog.

## Execution Contract

1. Do not preload broad Vault, resume, or governance files. Invoke the named
   repo skill and start with `career-hub-agent` against the live service address
   from Settings > Local agent connection; it returns a bounded JSON packet and
   accepts no profile or Data Home argument (see
   [agent workflows](agent_workflows.md)). Report the channel, never a broad
   private inventory. Pass the supplied
   Registry mode through `--registry-mode`; the command defaults safely to `off`.
2. For `assess`, use ephemeral `analyze`; it must not save the JD merely to form
   an opinion. For resume or application-text preparation, use `prepare`; it
   preserves/reuses the exact JD and URL and moves the local record only to
   `Preparing`. Pass the requested `--purpose` so preparation and the next
   writing or resume context are returned in one pass. For an exact application
   prompt or Resume Version, pass a bounded schema-version-1 task request with
   `--task-request-json`. The `--task-request-file` form remains supported.
3. If only a URL is supplied, Career Hub does not scrape it. Use a visible
   browser page only when the user explicitly authorizes browser work;
   otherwise request the complete JD.
4. Separate eligibility, required and preferred demand, direct Career Vault
   evidence, taxonomy hints, evidence not found, and manual review. A JD term or
   registry record is never candidate evidence. Matching is advisory.
5. Resume work is non-forcing. `no_change_needed` is a successful result, there
   is no edit quota, and every proposed delta needs a concrete JD requirement,
   direct evidence, and material benefit. A Resume Plan is not a generated file.
   After **Use this selection** succeeds (the internal Resume Plan is approved),
   use `material-context --kind resume` for application-only wording rather than
   reopening broad Vault or resume files.
6. Draft only the employer text requested. Preserve the exact question and
   limit. Never invent experience, metrics, research, enthusiasm, referrals,
   authorization facts, availability, or motivations. Put unresolved facts in
   `Manual checks`. A full cover letter has three direct body paragraphs:
   confirmed motivation, verified evidence, and a concise close.
7. Default to inline text. Create or edit a Google Doc only after an explicit
   request. Never assemble a Word document manually; when the user requests a
   local application artifact, save the typed material and use `render-material`
   for DOCX, PDF, or TXT. A cover letter should be professional, specific, and
   concise; it should not be a biography or copied JD.
8. Save inline cover letters, screening answers, and outreach through the
   validated private text workbench. Use `material-context` and `save-material`
   for a cover letter only when an editable or exportable document was requested.
   The save response is a compact revision/validation receipt, not a second copy
   of the editor. A draft or export is not an upload or submission.
9. Never click final Submit, accept legal attestations, answer voluntary
   self-identification, or handle identity documents without explicit authority.
   Creating a resume or cover letter never sets `Applied`.
10. Call `record-submission --confirm-submitted` only after the real external
    submission occurred or the user explicitly confirms that it already did.
    Record the actual Resume Version when known; do not guess.

## Response Shape

Lead with the requested outcome. For an assessment, return recommendation,
eligibility, strongest direct evidence, important gaps, Resume Version advice,
registry changes, records changed, and one next action. For application copy,
put copy-ready text first and follow it only with essential manual checks and a
compact record receipt. Do not narrate routine file discovery.

The executable commands and maintenance contract are documented in
[`agent_workflows.md`](agent_workflows.md).
