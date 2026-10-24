# Base Resume Versions

Base Resume Versions are manually reviewable resume variants stored under `resume_versions/` in the Data Home. The feature does not generate content with AI and does not render DOCX or PDF files.

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
- An agent-created draft stays unapproved until the user reviews its facts and wording.

## Validation

Resume Versions are read and validated by the sidecar
(`backend/internal/intake/resumelibrary`). Validation checks the Career Vault
first, then required files, metadata fields, review-state invariants, and
Vault references.

## Manual Approval

After reviewing `resume.md`:

1. Correct any inaccurate or awkward wording in the resume and its Vault source first.
2. Set `manual_reviewed: true`.
3. Set `review_status: approved` and `quality_status: approved`.
4. Reopen **Resumes > Resume library** so the sidecar validates the version again.

Do not approve a version merely because the YAML passes validation. Structural validity is not editorial approval.
