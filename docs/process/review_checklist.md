# Review Checklist

- [ ] Stayed within scope.
- [ ] No unnecessary dependencies.
- [ ] No future-phase leakage.
- [ ] No accidental network, cloud, or API usage.
- [ ] No destructive file changes.
- [ ] Tests or validation were run.
- [ ] Test tier and commands follow `docs/process/workflow_rules.md#test-selection` and
      cover the affected behavior, owner and consumers.
- [ ] Relevant rejection/recovery paths and current-runtime user flows were
      checked; unavailable coverage and gated/platform skips remain explicit.
- [ ] Broad or exploratory work records its scope, budgets and continuation;
      assertions were not weakened and failures were not hidden by retries.
- [ ] Errors are handled clearly.
- [ ] Docs were updated if behavior changed.
- [ ] Technology choice is justified if a new dependency or service was added.

- [ ] Every Career Vault experience has an explicit full, compact, or excluded decision.
- [ ] Selected experience has enough source-backed bullets; no one-line token entries.
