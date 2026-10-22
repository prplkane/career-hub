# Application Packages

Application packages are reviewable, job-specific Markdown and YAML artifacts under `outputs/application_packages/<job_id>/`. They remain supported as a legacy review contract. The interactive application-material workflow now stores typed drafts separately under `outputs/application_materials/<job_id>/`; repository code still does not call an AI API.

Resume wording and selection must follow `career_vault/style_rules.yaml`; rationale and source review dates are in `docs/reference/resume_standards.md`.

## Interactive Resume Plan

Every persisted job/application record may have one editable Resume Plan stored in local SQLite.
It is the product-facing successor to manually assembling
`selected_evidence.yaml`, while existing file-based application packages remain
valid review artifacts.

The plan records:

- one- or two-page intent and ordered section visibility;
- two-button Include/Exclude state for every active experience; Exclude is the default and may keep an optional review note;
- a 2-5 bullet target that deterministically derives compact (2) or full (3-5) presentation; the editor offers only targets supportable by available proof points and flags older unsupported targets;
- selected proof ids that belong to each included experience; the candidate evidence pool may be larger than the bullet target;
- read-only JD suggestions on proof points that directly name a canonical job technology; suggestions never select evidence automatically;
- targeted approval feedback that scrolls to an included experience when its evidence count is below the bullet target;
- optional projects and their deterministic proof ids;
- a Projects picker that remains hidden while the Projects section is excluded;
- selected skill groups and exact canonical keywords;
- selected schools and certifications.

A draft may remain incomplete. Approval fails when included records lack enough evidence,
proofs belong to another record, bullet targets are unsupported, referenced
Vault records changed, or the JD fingerprint changed. Approval selects evidence;
it does not itself create prose or render a document. The separate Application
Materials editor may create application-only wording from an approved plan and
render it through the canonical local exporter.

## Package Files

Each package contains:

- `job_analysis.md`: agent-written fit analysis and risks.
- `selected_evidence.yaml`: structured selection decisions linked to Career Vault ids.
- `resume_draft.md`: layout-neutral, agent-written draft for manual review.
- `tailoring_notes.md`: supported terms, excluded claims, and manual checks.

Application-form answers, cover letters, recruiter messages, and follow-ups are
not additional resume-package requirements. During active dogfooding they use
the separate private job-specific convention in
[`application_text_workbench.md`](../guides/application_text_workbench.md). This keeps a
screening answer from becoming resume evidence or making an otherwise unused
application package fail validation.

## Experience Selection Contract

Every experience block in `career_vault/experience_blocks.yaml` must receive exactly one decision:

- Selected as `presentation: full` with a target of 3-5 bullets.
- Selected as `presentation: compact` with a target of 2 bullets.
- Listed under `excluded_experience` with a specific reason.

One-line experience mentions are not an accepted presentation mode. An experience may still be excluded when stronger evidence deserves the available resume space, but the omission must be visible and reviewable.

Each selected entry also records its exact `resume_heading`. Selected proof ids must exist, must belong to the selected experience block, and must be numerous enough to support the target bullet count. The standalone Python validator that enforced this is retired with the
legacy package format. It checked package files, Vault references, proof ownership, selection completeness, bullet targets, and the actual bullet count beneath each selected Markdown heading. It did not judge prose quality or verify that the draft visually fits a page.

## Current Workflow Boundary

Repository code currently automates:

1. Career Vault and cross-reference validation.
2. Creation of a draft normalized job card from a local source file.
3. Job-card validation.
4. Conservative exact-phrase job review, non-enforcing eligibility blockers, requirement gaps, and review lanes.
5. Base resume metadata validation.
6. Application-package structure and evidence-selection validation.

Agent reasoning in the Codex session currently handles:

1. Manual normalization of pasted job descriptions.
2. Interpretation of alternatives, ambiguous wording, and role fit.
3. Ranking evidence and writing explicit inclusion or exclusion reasons.
4. Drafting source-backed resume language and tailoring notes.

The user handles final factual approval, application answers, visual pagination
review, artifact attachment, and submission. Career Hub handles the canonical
layout and local export for typed application materials.

Codex is an AI-assisted working environment, but no product code in this repository calls an AI API. The YAML contract and validators exist so future sessions and lighter models have explicit constraints instead of relying only on conversational memory.

## Known Limits

- Exact matching misses synonyms and requirements written as alternatives.
- Evidence ranking is not yet a deterministic scoring algorithm.
- Markdown structure and bullet depth are checked, but page-level visual quality still requires manual review.
- One constrained DOCX/PDF/TXT material renderer exists; arbitrary layout,
  dashboard, scraper, and auto-apply workflows do not.
