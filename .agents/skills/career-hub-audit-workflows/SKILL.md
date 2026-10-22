---
name: career-hub-audit-workflows
description: Audit Career Hub repo skills against executable contracts after relevant code, schema, or workflow changes or suspected staleness. Explicit use only; changed files alone do not justify a rewrite.
---

# Audit Career Hub Agent Workflows

Run `python -m scripts.agent_workflow_audit --project-root . --changed --pretty` after changing an agent adapter or a service listed by a skill contract.

Interpret statuses conservatively:

- `current`: the contract and tracked paths exist; no relevant change was detected.
- `review_required`: relevant code or the skill changed; inspect compatibility, but do not assume an edit is needed.
- `outdated`: a tool version, command, or tracked path no longer matches.
- `broken`: required metadata or contract structure is invalid.

Change only the mismatched instruction, command, contract version, or test mapping. Preserve `no_change_needed` and zero edit quotas. After a real change, run the mapped tests and skill validator.
