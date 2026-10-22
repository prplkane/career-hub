# Base Resume Versions

`CAP-BASE-RESUME` (historically Phase 4) stores manually reviewable resume variants under `resume_versions/`. It does not generate content with AI and does not render DOCX or PDF files. See the [Capability Acceptance Registry](../process/acceptance_registry.md) for its current foundation boundary.

## Folder Contract

Each version has one folder whose name is its stable id:

```text
resume_versions/
  full_stack_solutions_engineer/
    metadata.yaml
    resume.md
```

- `resume.md` is readable, layout-neutral resume content.
- `metadata.yaml` records the target role family, Vault references, and review state.
- `resume_version_id` must match the folder name.
- `review_status: approved` requires `manual_reviewed: true`.
- A Codex-created draft stays unapproved until the user reviews its facts and wording.

## Validation

The standalone Python validator (`src.validate_resume_versions`) is retired.
Resume Versions are now read and written by the Go sidecar
(`backend/internal/resumelibrary`). The retired validator checked the Career
Vault first, then required files, metadata fields, review-state invariants, and
Vault references.

## Current Version

`full_stack_solutions_engineer` is the first base draft. It targets full-stack, solutions, SaaS integration, and implementation roles. Its content is source-backed but still requires manual review before use.

## Manual Approval

After reviewing `resume.md`:

1. Correct any inaccurate or awkward wording in the resume and its Vault source first.
2. Set `manual_reviewed: true`.
3. Set `review_status: approved` and `quality_status: approved`.
4. Run the validator again.

Do not approve a version merely because the YAML passes validation. Structural validity is not editorial approval.
