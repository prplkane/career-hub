# Small-Model Playbook

Use this playbook for inexpensive, bounded tasks. `AGENTS.md` remains the
repository authority.

## Good Tasks

- Inspect one saved JD digest and report specific extraction misses.
- Add exact, source-observed technology vocabulary.
- Fix one reproducible UI or validation defect.
- Update one focused document after behavior changes.
- Run a named validation command and report the exact result.

Do not ask a small model to redesign the architecture, infer missing resume
facts, rewrite the entire Career Vault, or implement multiple future phases.

## Required Workflow

1. **Research:** Read `AGENTS.md`, `START_HERE.md`, and only the relevant source,
   schema, test, and task document.
2. **Plan:** State the files, one pass/fail result and the test tier/commands from
   `docs/process/workflow_rules.md#test-selection`; explain why they cover the behavior.
3. **Execute:** Make the smallest reversible change. Preserve local-first storage.
4. **Verify:** Run the selected regression and owning tests, plus the affected
   browser flow when relevant. A small diff in storage, security or lifecycle
   still requires a bounded Broad plan. Record gaps; never claim an unrun check
   passed. Stop repeating completed checks when their inputs have not changed.
5. **Report:** List changes, validation, unresolved ambiguity, and no more than
   one recommended next task.

## Skills Registry Rules

Resume and JD intake use the same deterministic Skills Registry:

- core groups: `src/data/skill_groups.yaml`;
- core skills: `src/data/job_technologies.yaml`;
- custom groups: `career_vault/taxonomy/skill_groups.yaml`; and
- custom skills: `career_vault/taxonomy/technologies.yaml`.

Core records ship with the application. Custom records are private,
user-managed vocabulary. Neither origin is candidate evidence: a custom skill
means the user chose to recognize that term, not that the candidate has proven
experience with it.

For every vocabulary calibration task:

1. Copy the exact source phrase and identify its source line or job id.
2. Search both skill catalogs case-insensitively before adding anything.
3. Add a core term only when it is an unambiguous named language, framework,
   library, platform, tool, protocol, methodology, or standard.
4. Use a stable `snake_case` id, the official display name, exactly one active
   primary group, and only exact spelling/acronym variants as aliases.
5. Do not add job titles, years, degrees, soft skills, responsibilities, broad
   prose, or inferred synonyms as technologies.
6. Names and aliases must be globally unique after whitespace collapsing and
   case-folding. Do not add an ambiguous alias such as `MDM` when it can name
   multiple concepts.
7. Add custom vocabulary only through an explicit user instruction, the product
   Quick Action, or `career-hub-agent registry-add-exact --server <current-server>` when the current
   session explicitly enables `exact-auto`. That command still requires a saved
   JD, exact source occurrence, one active group, and a completed semantic check.
   Never harvest unknown prose from a JD or resume.
8. Reanalyze the source JD and confirm the intended phrase is recognized without
   creating unrelated matches.

Before adding vocabulary, use the native connection from Settings described in
`docs/guides/agent_workflows.md`. Read the `server` and scoped `token` from the
owner-only `connection` file and read `GET <server>/api/registry` with
`X-Career-Hub-Token`; keep the token local and out of any output.
Search canonical names and aliases in the grouped `groups[].skills` payload,
including both `core` and `custom` origins and archive state. Return only the
relevant matches. Reuse an active exact/alias match; an unmatched, archived or
ambiguous result requires semantic review before a write. Do not infer a new
candidate skill. Use `rg` only for reviewed source-catalog development work.

For routine application work, use the native `career-hub-agent` with its explicit
running `--server` and the repo skills. `registry-add-exact` requires the reviewed
saved JD and existing group and returns validation/reanalysis receipts. Inspect
those receipts before continuing; do not replace failed writes by editing the
private catalog directly. Core-catalog development also requires the focused
Go catalog tests and validation through the running service after rebuilding.

If a phrase is plausible but not safe to add, include it in the final response
under `Vocabulary candidates` with source phrase, proposed canonical name,
proposed category, and reason review is needed. Do not silently teach the parser.

## Truthfulness And Privacy

- Resume and Career Vault claims require a user source or explicit confirmation.
- Never turn a job requirement into candidate experience.
- Never expose or force-add ignored local resumes, Vault files, jobs, outputs, or
  the SQLite application database to Git.
- Matching output is advisory. Missing evidence means the matcher did not find
  evidence; it does not prove the candidate lacks the capability.

## Final Response

1. Summary
2. Files changed
3. Vocabulary added or candidates reported
4. Commands run / results
5. Uncertainty or limitations
6. Suggested next step
