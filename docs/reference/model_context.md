# Compact Model Context

> **Status: needs rewrite.** The intent below is still accurate, but its `python -m src.*` commands belong to the retired Python runtime and no longer run.
> Until this document is rewritten, use `career-hub-agent context` in [agent workflows](../guides/agent_workflows.md). See the
> [documentation index](../README.md); historical comparisons use the
> pinned reference export.

`src.build_model_context` creates a local, deterministic context bundle for one normalized job. It does not call an AI API or write resume prose.

## Command

```powershell
python -m src.build_model_context jobs/normalized/example_role.yaml
```

The command first regenerates the deterministic job review, then writes version 2 context artifacts:

```text
outputs/model_contexts/<job_id>/
  evidence_matrix.yaml
  selection_worksheet.yaml
  model_context.yaml
```

## Evidence Matrix

Every required, preferred, experience, education, and classified requirement receives one row with:

- a stable row id;
- requirement priority and category;
- `matched_direct`, `missing_direct`, or `manual_review` status;
- source text and line evidence for classified requirements;
- direct experience, project, or certification ids;
- separate taxonomy skill ids;
- reviewed requirement-concept ids when an explicit alias bridge was used.

Taxonomy ids are navigation hints. They never satisfy a requirement.

## Selection Worksheet

The worksheet keeps every Career Vault experience with `selection: undecided`,
dates, role, a two-proof preview, requirement coverage, and a visible score.
Required matches are worth 3 points and preferred matches are worth 1 point.
The worksheet also creates a maximum six-block shortlist for full model context.
Projects linked to an already shortlisted experience are skipped as duplicates.

The score organizes review; it does not approve, exclude, or write resume claims.

## Model Context

The compact context contains:

- job identity, responsibilities, and visa signals;
- review lane and biggest gap;
- non-contact candidate location, authorization, and relevant education facts;
- full source-backed content only for the bounded deterministic shortlist;
- compact taxonomy labels;
- non-enforcing eligibility blockers, requirement gaps, preferred gaps, and manual checks;
- an explicit `application_allowed: true` policy;
- resume voice, bullet, selection, and truthfulness rules;
- included and omitted evidence ids plus approximate character/token metrics.

It intentionally excludes raw CV files, raw job prose, contact details, unrelated Vault blocks, previous resumes, and document-rendering rules. A model should read the generated matrix, worksheet, and context instead of loading the full repository context.

## Boundary

The matcher first attempts normalized whole-phrase matching. A small reviewed
`src/data/requirement_concepts.yaml` bridge can then expand an exact requirement
alias into explicit evidence terms. The bridge is deterministic and inspectable;
it is not fuzzy matching, embeddings, or semantic inference. Taxonomy-only
matches remain non-direct and require review.
