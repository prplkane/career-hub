# Workflow Rules

1. Inspect relevant files.
2. Make a small plan for non-trivial tasks, including test selection below.
3. Implement the smallest useful change.
4. Run the selected checks and inspect their actual outcomes.
5. Review for scope creep.
6. Report exactly what changed.

## Test Selection

Before editing, record behavior, owner/consumers, highest applicable tier,
commands and expected outcomes in the Backlog plan (a brief update for trivial work).

| Tier | Trigger | Required checks |
| --- | --- | --- |
| Focused | Bounded rule or small fix | Reproduce the defect; regression, owning tests and affected consumers. |
| Workflow | Form, route, loading, UI/API or persistence change | Focused checks plus the affected current-runtime browser flow, rejection/recovery and saved/reloaded outcome as applicable. |
| Broad | Major update; shared contract, credentials, encryption, schema/migration, deletion/recovery, locking, IPC/lifecycle, updater or runtime dependency change | Focused first; applicable full suites and platform/package gates; named owner, scope, deadlines, synthetic fixtures and gaps. |

Run narrow checks while iterating; invoke the selected checks before reporting
changed behavior complete, without waiting for a user test request. A test-now
request runs relevant checks on current state. Stop after passing unless inputs
or unresolved evidence change. Documentation-only edits use relevant validation.
Before push, use the [local hook](../guides/local_development.md#local-checks-before-push);
its path-selected minimum complements the task's affected-flow checks. CI runs
the smaller [independent set](testing_strategy.md#local-and-github-responsibilities).

Choose examples by affected behavior; these are not an every-task checklist:

| Area | Example command |
| --- | --- |
| UI cancellation | `node --test --test-name-pattern="abort" tests/ui_api_abort.test.mjs`, then that file |
| Shared UI/helpers | `npm run test:fast` |
| Profile save/retry | `node --test --test-name-pattern="failed save" tests/workflows/profile.test.mjs` |
| Go owner | From `backend/`: `go test ./internal/materialintegrity`; include affected consumers |
| Desktop navigation | `node --test desktop/tests/navigation.test.js` |
| Browser extension | `npm run test:extension-compat` |
| Agent audit tooling | `python -m unittest tests.test_agent_workflow_audit -q` |

Profile scenarios do not qualify other flows. Add a missing regression or record
manual validation and gaps. Put Node options before file paths. For mapped skill
or agent-service changes, run mapped tests and the separate workflow audit;
`review_required` means inspect, not rewrite. Audit success is not a test pass.

Broad testing defaults to a 30-minute ceiling; tighter command limits still apply.
Read [broader qualification](testing_strategy.md#controlled-broader-testing) for
suite inventory/prerequisites and [exploration rules](testing_strategy.md#controlled-agentic-testing-for-major-changes)
when needed: 20 minutes, 40 actions, one replay per suspected defect. These are
unmeasured ceilings, not targets. Preserve expectations; never weaken a failing
assertion to get green. Missing prerequisites/timeouts leave the phase incomplete.

Report a compact receipt; inspect verbose logs/traces only for relevant failures:

```text
Revision/diff: <tested state>
Tier/reason: <Focused / Workflow / Broad; behavior at risk>
Checks: <command -> outcome/count; skipped or pending is not passed>
Evidence: <relative to OS temp; no personal paths or private contents>
Failures: <product / test / environment / unresolved; disposition>
Gaps: <unrun qualification and why>
Budget/continuation: <for broad or incomplete work>
```

## Clarification

Ask for clarification when the task could overwrite user data, change approved templates, add a paid or hosted service, or force an architectural choice that is not recoverable.

Make a reasonable assumption when it keeps the change local, reversible, and aligned with existing project patterns.

## Failing Checks

- Fix failures caused by the current change.
- Report unrelated failures clearly.
- Do not claim validation passed if the command was not run or failed.

## Future Ideas

Future-phase ideas can be noted as recommendations. Do not implement them unless explicitly requested.

## Final Response

Include:

1. Summary
2. Files changed
3. Commands run / results
4. Notes or limitations
5. Suggested next step
