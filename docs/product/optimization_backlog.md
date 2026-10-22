# Optimization Backlog

This backlog reduces false decisions and repeated model context before adding new product surfaces.

## Closed

### OPT-001: Separate Direct Evidence From Taxonomy

- Status: closed
- Outcome: skill-family matches are hints only; experience, project, and certification blocks are direct evidence.
- Verification: taxonomy-only matches cannot satisfy required platforms or enter `relevant_evidence_ids`.

### OPT-002: Compact Model Context

- Status: closed
- Outcome: one command regenerates a job review and writes a compact requirement matrix plus model-facing context.
- Verification: context references validate, contact data is excluded, and only reviewed direct evidence blocks are copied.

### OPT-003: Requirement Groups And Aliases

- Status: closed
- Outcome: JD intake stores source-backed `any_of` technology groups, validation recognizes required groups, and deterministic review/model context accept one directly evidenced alternative. Reviewed requirement concepts expand only exact aliases into explicit evidence terms.
- Verification: alternatives are not duplicated, taxonomy-only matches remain non-direct, concept provenance is retained, and malformed groups fail validation.

### OPT-010: Evidence Selection Worksheet

- Status: closed
- Outcome: every experience receives an undecided worksheet row with compact proof previews and deterministic requirement coverage. Full model context is capped at six ranked evidence blocks and skips projects that duplicate selected experience.
- Verification: shortlist scoring is fixed and tested, all experiences remain visible, and Twitch/Solace context size is measured during generation.

### OPT-005: Task-Specific Agent Context

- Status: closed
- Outcome: one local adapter returns bounded assessment, resume-delta, and application-text packets; discoverable repo skills route agents without preloading all governance and resume-standard documents.
- Verification: opinion-only analysis is read-only, preparation is idempotent, no-change is an explicit success state, and the Registry-off synthetic assessment fixture is 2,832 characters (about 708 tokens by the documented estimate).

## Remaining

### OPT-004: Proof Source Of Truth And Short IDs

- Priority: P1
- Remove duplicated claim text between source blocks and proof blocks; use stable short proof ids.
- Done when claims have one editable source and all references still validate.

### OPT-006: Base Plus Delta Application Packages

- Priority: P2
- Store shared resume content once and keep job-specific selection and wording as a small delta.
- Done when similar applications do not duplicate nearly identical resume drafts.

### OPT-007: Validated Vault Snapshot

- Priority: P2
- Load and validate Vault YAML once per command, then pass a read-only in-memory snapshot.
- Done when commands stop rereading the same YAML during one run.

### OPT-008: Targeted Package Validation

- Priority: P2
- Allow validation of one application package while retaining a separate all-packages command.
- Done when iterative edits avoid scanning unrelated packages.

### OPT-009: Context Metrics And Fingerprints

- Priority: P2
- Context character counts, approximate tokens, and shortlist counts are implemented. Input fingerprints and unchanged-output reuse remain.
- Done when unchanged inputs can be reused without rebuilding equivalent artifacts.

## Order

Implement `OPT-004` next. The remaining tickets are useful after the evidence-selection workflow is stable.
