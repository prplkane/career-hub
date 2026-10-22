# Manual Job Intake

> **Status: needs rewrite.** The intent below is still accurate, but its `python -m src.*` commands belong to the retired Python runtime and no longer run.
> Until this document is rewritten, use the Jobs workflow in [START_HERE](../../START_HERE.md) and `career-hub-agent analyze`/`prepare` ([agent workflows](agent_workflows.md)). See the
> [documentation index](../README.md); historical comparisons use the
> pinned reference export.

`CAP-JD-INTAKE` (historically Phase 2) converts a locally saved job description into a predictable YAML card and conservative deterministic digest. The browser Add job flow then calculates and stores one advisory Career Vault assessment before reporting success. It does not fetch posting URLs, call AI APIs, or generate a resume. See the [Capability Acceptance Registry](../process/acceptance_registry.md) for its current acceptance boundary.

## Workflow

### Browser

1. Run `python -m src.run_intake_ui` and open **Jobs**.
2. Paste a complete job description or load a local `.txt`/`.md` file.
3. Optionally add the original posting URL.
4. Use **Optional source overrides** only when the posting does not identify itself clearly.
5. Select **Save and digest**. The action returns after the validated card, Saved ledger record, and advisory assessment have all been stored.
6. Use **Selected JD** to switch saved records; the selected digest and stored assessment update immediately.
7. A stale `draft` is regenerated automatically from its immutable local source. Use **Reanalyze** to force the current deterministic analyzer.
8. Use **Edit details** on the selected job to correct the company, role, seniority, location, workplace, or source URL in the side panel that slides in from the right. Edits are stored as candidate-provided values, so a later reanalysis keeps the corrected company and role while other digest facts regenerate from the saved posting.
9. A `ready` record is review-protected and is never rewritten automatically.
10. Use **View original JD** to inspect the exact saved text inside Job Library without changing the record; use **Open posting** for the retained source URL.
11. Expand **Show local file details** only when you need paths and validation status.
12. Review the digest, eligibility gates, source under `jobs/incoming/`, and YAML under `jobs/normalized/`.

The browser stores the exact submitted UTF-8 source text and never overwrites it. Every valid saved card receives one ledger record with initial status Saved; its assessment is reused unless the card, Career Vault, deterministic assessment context, or evaluator version changes. Normalized job-card writes are atomic, so an interrupted write leaves the preceding complete card intact. It creates only a `draft` and never marks the card `ready`. Generated draft cards include an analyzer version so stale analysis can be rebuilt; reviewed `ready` cards remain protected. Company, title, location, technology names, experience requirements, responsibilities, and explicit visa/clearance language are extracted conservatively. Every derived field keeps its source line and rule.

An SHA-256 fingerprint is calculated from normalized casing and whitespace. An exact duplicate reopens the existing local record without retaining a second source file. If a distinct posting would reuse the same source name or detected company/title, the new immutable source and its card receive a deterministic fingerprint suffix instead of replacing the first posting.

### CLI

1. Save a complete, non-empty UTF-8 job description under `jobs/incoming/`.
2. Create a draft card:

```powershell
python -m src.ingest_job jobs/incoming/example_role.md --company "Example Co" --title "Implementation Engineer" --source-url "https://example.com/job"
```

3. Review and fill the generated YAML under `jobs/normalized/`.
4. Change `status` from `draft` to `ready` only after manual review.
5. Run `python -m src.validate_jobs`.

The CLI refuses empty source files and refuses to overwrite an existing card unless `--overwrite` is explicit. Relative input paths are resolved from `--project-root`.

## Job Card Schema

```yaml
id: example_role
status: draft
company: ""
title: ""
source_url: ""
location: ""
remote_policy: ""
seniority: ""
required_skills: []
required_platforms: []
required_capabilities: []
preferred_skills: []
technology_groups:
  - requirement: required
    operator: any_of
    technologies: [Python, Java, TypeScript]
    evidence:
      - line: 12
        text: "Experience with Python, Java, or TypeScript"
        rule: technology_alternatives
experience_requirements:
  - minimum_years: 3
    area: SaaS implementation
education_requirements: []
responsibilities: []
compensation:
  status: explicit
  ranges:
    - minimum: 90000
      maximum: 120000
      currency: USD
      period: year
      location: ""
      display_text: "$90,000-$120,000 USD annually"
      evidence:
        - line: 8
          text: "$90,000-$120,000 USD annually"
          rule: compensation_range
visa_signals:
  citizenship_required: null
  clearance_required: null
  sponsorship_mentioned: null
  no_visa_or_opt: null
content_fingerprint: <64-character lowercase SHA-256>
digest:
  schema_version: 1
  fields:
    title:
      value: Implementation Engineer
      state: explicit
      evidence:
        - line: 2
          text: "Job Title: Implementation Engineer"
          rule: labeled_title
  mentioned_technologies: []
  eligibility_gates: {}
  classified_requirements:
    - category: capability
      name: Communication
      requirement: preferred
      text: "Excellent written communication skills"
      evidence:
        - line: 15
          text: "Excellent written communication skills"
          rule: capability_communication
  provenance:
    technologies: []
    experience_requirements: []
  analysis_coverage:
    source_lines: 20
    relevant_lines: 8
    structured_lines: 7
    needs_review_lines: 1
    context_lines: 12
    review_items:
      - line: 16
        section: preferred
        text: "Strong written communication skills"
        rule: unstructured_relevant_line
  unresolved_fields: []
raw_jd_path: jobs/incoming/example_role.md
notes:
  - Draft created from the raw job description; manual review required.
```

Use the fields consistently:

- `required_skills`: concrete technical or domain skills.
- `required_platforms`: named products, vendors, frameworks, or systems. Missing evidence becomes a high-severity requirement gap and never blocks the application workflow.
- `required_capabilities`: leadership, delivery, communication, or ownership expectations.
- `experience_requirements`: minimum years and the applicable area; years are never treated as keywords.
- `education_requirements`: degree or education language that needs explicit review.
- `compensation`: source-backed ranges only. Multiple location-specific ranges remain separate; currency and period stay unspecified unless the posting states them.
- `preferred_skills`: non-required qualifications.
- `technology_groups`: source-backed required, preferred, or mentioned alternatives. `any_of` means one directly evidenced alternative satisfies the group.

Rules:

- `raw_jd_path` must remain inside `jobs/incoming/` and point to an existing file.
- `source_url` is blank or an absolute HTTP/HTTPS URL.
- Visa signals use `true`, `false`, or `null`. Keep `null` when the posting is silent.
- Digest states are `provided`, `explicit`, `derived`, `needs_review`, or `not_stated`; they are evidence states, not confidence percentages.
- Eligibility gate statuses are `compatible`, `incompatible`, `unclear`, or `not_stated` and use the local visa profile only.
- Technology extraction uses the local taxonomy and exact phrase aliases. Unrecognized terms remain in the source for review.
- `digest.classified_requirements` stores source-backed requirements that do not belong in technology tags. Allowed categories are `technical`, `capability`, `constraint`, and `education`; the requirement level is `required` or `preferred`.
- Classification stores cleaned display text plus the original source-line evidence. A narrow rule may assign a type or normalized display name, but it may not rewrite or strengthen the requirement.
- Explicit `preferred` wording inside a required section remains preferred. Known EEO, accommodation, company, and compensation boilerplate is context, not a qualification.
- Compensation extraction requires an explicit pay signal and range on the source line. A single unambiguous location prefix on the sole location-specific range may also populate the posting location with the same evidence; multiple locations remain unresolved. The card retains the cleaned source text and line evidence and does not infer missing currency, period, equity value, or total compensation.
- Older reviewed cards without `compensation` receive a read-only display fallback from their immutable raw JD. Listing the card never rewrites its YAML.
- Source coverage is line-based and limited to responsibilities, required qualifications, and preferred qualifications. `Relevant = Structured + Needs review`; all other non-empty source lines are counted as context and remain available in the immutable raw JD.
- A line in `Needs review` is preserved because no structured fact was extracted from it. It is not an error or a confidence score, and the analyzer must not guess a category merely to remove it from the queue.
- `draft` permits blank fields.
- `ready` requires company, title, responsibilities, and at least one explicit skill, platform, capability, experience, education, or required technology-group requirement.
- Unicode filenames are supported; symbol-only filenames receive a deterministic hashed id.

## Instructions For Codex

- Preserve the complete source text.
- Extract only requirements stated in the posting.
- Do not put years, degrees, or leadership expectations into `required_skills`.
- Do not infer sponsorship, citizenship, clearance, location, or seniority from silence.
- Record ambiguity in `notes` and keep incomplete cards as `draft`.
- Run `python -m src.validate_jobs` after every normalized-card edit.
