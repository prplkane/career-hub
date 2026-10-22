# Deterministic Job Review

`CAP-JD-ASSESSMENT` (historically Phase 3) reviews one `ready` job card against the Career Vault without AI calls or invented experience. See the [Capability Acceptance Registry](../process/acceptance_registry.md) for its current acceptance boundary.

## How To Run

Assessment runs in the desktop (**Jobs**) and through `career-hub-agent analyze`
(see [agent workflows](agent_workflows.md)). The Python `src.review_job`
command is retired.

The derived report is written to:

```text
outputs/job_reviews/<job_id>.yaml
```

## Rules

- Matching starts with normalized whole phrases, not fuzzy semantic inference.
- Reviewed aliases in `src/data/requirement_concepts.yaml` may expand one exact
  requirement phrase into explicit evidence terms such as `SQL` to
  `PostgreSQL`, `MySQL`, and `SQLite`.
- Skill families form a hierarchy via the taxonomy `parent` field (for example
  `postgresql -> sql`, `django -> python`). Direct evidence of a child term
  also satisfies a requirement for its parent; the reverse direction stays
  manual. Expansions are derived from the merged catalogs by
  `build_requirement_expansions`.
- Concept ids are included in review output so expanded matches remain explainable.
- Direct evidence comes from experience, project, and certification blocks.
- Skill blocks produce taxonomy hints only; they never satisfy a requirement or expand into all linked blocks.
- Missing required skills, platforms, capabilities, or technology groups become `requirement_gaps`; an evidence gap is not proof that the candidate lacks the requirement.
- Citizenship, clearance, and explicit no-visa/no-OPT conflicts become `eligibility_blockers`. These are strong assessment signals, not workflow enforcement.
- `digest.classified_requirements` are preserved with source evidence and reviewed separately. Long capability, technical, constraint, and education wording defaults to manual review when direct evidence is absent.
- Years-of-experience requirements are derived from active experience blocks whose
  text matches the requirement area (concept and family expansion included):
  parsed `dates` ranges are summed into `derived_years`. The status becomes
  `derived_match` or `derived_shortfall`; without parseable dates or matching
  blocks it remains `manual_review`. Derived durations are naive sums that can
  double-count overlapping roles, so they stay advisory and ask for confirmation.
- Education requirements remain manual review.
- Adjacent experience is not promoted to an exact platform match.

## Lanes

- `REVIEW_RECOMMENDED`: one or more eligibility blockers, requirement gaps, or manual checks exist. This lane never disables applying.
- `PRIORITY_MANUAL`: all required evidence matches and at least half of preferred evidence matches.
- `ASSISTED_AUTO_DRAFT`: all required evidence matches with a weaker preferred match.

`ARCHIVE` is reserved for a later prioritization phase and is not assigned by the current engine.

## Report Fields

- `schema_version: 2`
- `application_allowed` (always `true`; the candidate owns the decision)
- `eligibility_blockers` with status, message, source, and source evidence
- `requirement_gaps` with severity, category, requirement, and `evidence_not_found` status
- `matched_required`
- `matched_preferred`
- `missing_preferred`
- `experience_review` with `status` (`derived_match`, `derived_shortfall`, or
  `manual_review`) and `derived_years` when duration could be parsed
- `classified_requirement_review`
- `manual_review`
- `taxonomy_hints`
- `relevant_evidence_ids`
- `biggest_gap`

The browser requests the same deterministic assessment only for the selected JD, including drafts, through `GET /api/jobs/<job_id>/assessment`. This read-only endpoint does not write review artifacts.

## Assessment Presentation

`AssessmentSummary` presents the stored deterministic result in the Add Job success state, Job Library, and application detail drawer. It does not introduce a second scoring pipeline or an in-progress assessment state. The same pure renderer is intended for a future browser extension.

The component shows a recommendation (`Apply`, `Review`, or `Skip`), visa compatibility, citizenship and clearance restrictions, experience comparison, seniority mismatch, location compatibility, and important requirements. Every positive finding links to retained JD line evidence. When the relevant wording is absent, the UI says that the JD scan found no explicit language rather than fabricating evidence.

`Skip` is advisory: it means a stored eligibility conflict exists and an exception should be confirmed before applying. `Review` is advisory too; `application_allowed` remains true.

The CLI report is derived data and may be regenerated after either the job card or Career Vault changes.
