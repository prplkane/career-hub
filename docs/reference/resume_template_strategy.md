# Resume Template Strategy

This document defines how local resume references inform the canonical constructor.
Reference files remain private under `resume_templates/` and are excluded from Git.
They are design evidence, not sources of candidate facts.

## Reference Roles

- `twitch_content_baseline.docx`: approved content and evidence-selection baseline.
- `candidate_visual_baseline_2026.docx`: preferred visual language and section hierarchy.
- `asu_experienced_engineer_reference.docx`: primary structural reference for an experienced technical resume.
- `asu_computer_science_reference.docx`: secondary software-resume structure reference.
- `asu_stem_reference.docx`: secondary general STEM structure reference.
- `asu_alumni_business_reference.pdf`: experienced professional density reference.
- `asu_undergraduate_business_reference.pdf`: early-career one-page density reference.

## Current Decision

The canonical template combines responsibilities rather than copying one file blindly:

- Resume content comes only from an approved Resume Plan and Career Vault evidence.
- Twitch establishes the preferred content quality and targeting depth.
- The single-column experienced-engineer reference provides the safer document structure: normal paragraphs, no layout tables, and 0.5-inch margins.
- The candidate 2026 resume provides the preferred restrained visual language: compact header, clear section rules, and dense technical hierarchy.
- Other imported references are comparison controls, not additional template variants.

The existing candidate visual baseline uses layout tables and very tight margins. It must not be used directly as the rendering engine template until ATS order and overflow behavior are verified.

## Implemented Foundation

1. The editable Resume Plan selects evidence and sections.
2. Exact layout tokens are encoded in one canonical ATS renderer.
3. A typed application-only draft permits summary and bullet wording changes.
4. DOCX, PDF, and TXT exports share the same reviewed content model.
5. Structural output checks run before download.
6. Attachment and real submission remain explicit user actions.

Do not add multiple themes, drag-and-drop layout editing, or unconstrained AI
rewriting. Visual page-count and overflow inspection remains the next renderer
quality gate because LibreOffice is not available in the current local runtime.
