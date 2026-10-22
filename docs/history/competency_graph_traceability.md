# Competency Graph Task Traceability

Traceability schema: **1** · Roadmap version: **CG-1.0** · Last reconciled:
**2026-08-24**

This document is the durable definition and evidence map for the historical
`CG-*` task IDs. The [Capability Acceptance Registry](../process/acceptance_registry.md)
remains authoritative for current product status and boundaries. A task ID
describes one bounded delivery goal; it does not authorize implementation of a
Planned capability.

Global invariants for every task:

- existing Career Vault data remains backward compatible;
- JD demand never creates candidate evidence;
- draft intake never affects evidence analytics;
- canonical resolution uses exact names or approved aliases, never fuzzy or
  inferred candidate skills;
- private ignored data is never added to Git;
- every mutation is explicit, revision-guarded, and fail-closed.

## CG-000 — Baseline checkpoint

**Outcome.** Establish the pre-Competency-Graph resume-intake baseline before
schema or projection work begins.

**Requirements.** Run the then-current full tests and Vault validation; inspect
the diff; checkpoint the resume-intake work; exclude private Vault and raw
files.

**Evidence.** Historical baseline commit `8f149de` (`feat: complete CV-first
resume management`) immediately precedes the contract commit. Current release
proof is provided by the full suite and `scripts/check_public_tree.py`; it does
not retroactively replace the historical command transcript.

**Status.** Historical checkpoint complete; command-level historical evidence
is necessarily limited to reachable Git state.

## CG-001 — Competency Graph contract

**Outcome.** Define the architecture before production implementation.

**Requirements.** Define Competency kinds, stable IDs, aliases, primary domain,
typed relations, alias collisions, candidate evidence versus JD demand,
active/archive behavior, independent evidence contexts, TechnologyTerm
compatibility, MVP boundaries, and non-goals. Obsidian is only a downstream
consumer; JDs and resume-review drafts cannot create candidate evidence.

**Evidence.** `docs/reference/competency_graph_contract.md`, contract commit `2fc3246`, and
`CAP-VAULT-SKILL-EDITOR` through `CAP-COMPETENCY-EXPLORER`.

**Status.** Implemented.

## CG-101 — Taxonomy schema and resolver

**Outcome.** Extend the existing TechnologyTerm model into a compatible,
deterministic competency catalog and resolver.

**Requirements.** Preserve legacy `id/name/aliases/category/parent`; support
optional kind, primary domain, and typed relations; merge shared catalog and
user overlay; validate relation targets and cycles; build an alias index;
return every candidate for ambiguous aliases without auto-matching; emit stable
ordering.

**Evidence.** `src/vault_intake_models.py`, `src/vault_intake.py`,
`src/digest_job.py`, `src/skills_registry.py`, and
`tests/test_competency_graph.py`.

**Status.** Implemented.

## CG-102 — Read-only vocabulary audit

**Outcome.** Classify existing vocabulary without changing canonical data.

**Requirements.** Inspect Experience and Project technologies, Skill Group
keywords, certifications, and both taxonomy catalogs; report `exact_match`,
`alias_match`, `ambiguous`, `unmatched`, and `invalid_reference`; write no
entities or private report into Git.

**Evidence.** `src/vocabulary_audit.py` and
`tests/test_vocabulary_audit.py` cover deterministic read-only library and CLI
behavior.

**Status.** Implemented.

## CG-103 — Seed competency curation

**Outcome.** Provide a small reviewed core catalog rather than turning every
observed phrase into an entity.

**Requirements.** Keep clear recurring technical entities; exclude
achievements, metrics, long bullet fragments, overly broad terms, and terms
observed only in a JD. Seed changes require user review.

**Evidence.** `src/data/job_technologies.yaml`,
`src/data/skill_groups.yaml`,
`VaultCompetencyMigrationTests.test_curated_core_seed_contains_reviewed_named_competencies`,
and `SkillsRegistryTests.test_repository_seed_has_unique_names_and_one_effective_group`.
The curated allowlist is durable; private audit inputs are intentionally not.

**Status.** Implemented with an explicit historical user-review checkpoint.

## CG-201 — Canonical competency references

**Outcome.** Allow candidate evidence records to reference stable competency
IDs while preserving legacy wording.

**Requirements.** Add optional `competency_ids` to Experience, Project, and
Certification records; keep `technologies`; validate every ID; prevent archive
operations from creating dangling active references; render canonical names
through the resolver.

**Evidence.** `src/vault_schema.py`, `src/skills_registry.py`,
`src/migrate_vault_competencies.py`, `tests/test_skills_registry.py`, and
`tests/test_migrate_vault_competencies.py`.

**Status.** Implemented under `CAP-COMPETENCY-REFERENCES`.

## CG-202 — Migration preview

**Outcome.** Produce a deterministic per-record, no-write migration plan.

**Requirements.** Resolve exact canonical names and approved aliases only;
preserve ambiguous and unmatched observations; use no fuzzy or model guesses;
show every proposed record change; pin Vault and catalog revisions.

**Evidence.** `plan_vault_competency_migration()` and
`VaultCompetencyMigrationTests.test_plan_is_read_only_and_reports_each_record`.

**Status.** Implemented.

## CG-203 — Migration apply

**Outcome.** Apply one user-confirmed preview without risking the canonical
Vault or its recovery point.

**Requirements.** Require the confirmed plan ID and optimistic revisions;
create and checksum-verify a pre-migration recovery ZIP before installation;
validate all replacements in memory; use atomic replacement and complete
rollback; retain legacy strings; write an audit receipt; make repeat apply a
no-op.

**Evidence.** `src/migrate_vault_competencies.py`, `src/data_backup.py`,
`tests/test_migrate_vault_competencies.py`, and `tests/test_data_backup.py`.
The change-then-revert regression proves that a checksum-invalid archive cannot
be installed and migration cannot write canonical IDs or an audit receipt.

**Status.** Implemented and independently reverified on 2026-08-24 after the
CG-900 recovery remediation.

## CG-204 — Resume intake and approval integration

**Outcome.** Turn reviewed extraction suggestions into canonical references.

**Requirements.** Match aliases into draft suggestions; keep ambiguous aliases
in review; persist only approved IDs; discard rejected suggestions; retain exact
source wording and provenance; prevent drafts from entering analytics.

**Evidence.** `src/vault_intake.py`, `src/approval_apply.py`,
`tests/test_vault_intake.py`, and `tests/test_approval_apply.py`.

**Status.** Implemented under `CAP-VAULT-SKILL-EDITOR`.

## CG-301 — Candidate Evidence Graph

**Outcome.** Build a pure, rebuildable projection from canonical active
candidate evidence.

**Requirements.** Emit Competency→Experience, Competency→Project,
Competency→Certification, Project→Experience, and Competency→Domain edges in
deterministic JSON/Python form; use no graph database; exclude jobs, drafts,
legacy strings, and archived records unless historical scope is explicit.

**Evidence.** `src/candidate_evidence_graph.py` and
`tests/test_candidate_evidence.py`.

**Status.** Implemented under `CAP-CANDIDATE-EVIDENCE-GRAPH`.

## CG-302 — Evidence analytics

**Outcome.** Derive evidence counts and usage intervals without an arbitrary
candidate score.

**Requirements.** Report professional contexts, independent projects,
certifications, proof count, first/last used, active-now, merged inclusive
intervals, and source types; do not double-count a Project linked to an
Experience as another professional context; do not treat certification dates as
usage.

**Evidence.** `src/evidence_analytics.py` and the `EvidenceAnalyticsTests` in
`tests/test_candidate_evidence.py`.

**Status.** Implemented under `CAP-EVIDENCE-ANALYTICS`.

## CG-401 — JD Demand Index

**Outcome.** Build a separate, read-only demand projection from saved jobs.

**Requirements.** Count distinct ledger jobs; deduplicate exact JD
fingerprints; separate required and preferred occurrences; apply an inclusive
period; aggregate by competency and domain; retain exact source provenance;
never write to the candidate graph or vocabulary.

**Evidence.** `src/jd_demand_index.py` and `JDDemandIndexTests` in
`tests/test_competency_demand.py`.

**Status.** Implemented under `CAP-JD-DEMAND-INDEX`.

## CG-402 — Coverage matrix

**Outcome.** Join candidate evidence and JD demand as two read-only projections.

**Requirements.** Emit `supported`, `thin_evidence`, `evidence_not_found`, and
`not_requested`; keep required/preferred and evidence counts separate; say only
that evidence was not found in Career Vault, never that the candidate lacks a
skill; produce no composite score.

**Evidence.** `src/coverage_matrix.py` and `CoverageMatrixTests` in
`tests/test_competency_demand.py`.

**Status.** Implemented under `CAP-COVERAGE-MATRIX`.

## CG-501 — Competency Explorer UI

**Outcome.** Provide one searchable Skills evidence surface with canonical
record navigation.

**Requirements.** Search and filter by domain/kind; show evidence counts,
aliases, latest use, and related Experience, Project, and Certification records;
open selected skill detail using the existing drawer/detail pattern; keep
vocabulary mutation in Skills / Groups and evidence projection read-only.

**Evidence.** `CAP-COMPETENCY-EXPLORER`, the competency endpoint tests in
`tests/test_competency_demand.py`, source-contract checks in
`tests/test_intake_ui.py`, and synthetic rendered browser checks in
`tests/competency_surfaces_smoke.cjs`.

**Status.** Implemented; rendered evidence is a required release check.

## CG-502 — Demand & Gaps UI

**Outcome.** Present coverage as a controlled table and summaries, not a radar
or opaque score.

**Requirements.** Show Competency, evidence, required jobs, preferred jobs, and
coverage; filter by period, domain, required/preferred, and supported/gap;
support detail navigation; preserve evidence-safe language; defer radar charts
until a dogfooded formula exists.

**Evidence.** `CAP-DEMAND-GAPS-UI`, `DemandGapsPayloadTests` in
`tests/test_competency_demand.py`, source-contract checks in
`tests/test_intake_ui.py`, and synthetic rendered browser checks in
`tests/competency_surfaces_smoke.cjs`.

**Status.** Implemented; rendered evidence is a required release check.

## CG-601 — One-way Obsidian export

**Outcome.** Export approved data into an explicitly generated Obsidian tree.

**Requirements.** Stable ID-based filenames, YAML frontmatter, generated-only
wikilinks, an explicit generated root, no overwrite of manual notes, no raw
resume export, no public-Git leakage, and idempotent repeat export. Obsidian
edits never become canonical input.

**Evidence.** `CAP-OBSIDIAN-EXPORT` defines the boundary; no implementation
evidence exists.

**Status.** Planned and explicitly deferred by `docs/guides/mvp_workflow.md`.

## CG-900 — Independent regression and architecture audit

**Outcome.** Independently verify every implemented CG requirement and global
invariant using current source, tests, rendered artifacts, and release gates.

**Requirements.** Audit exact ID-level traceability; canonical IDs; archived
scope; deterministic/read-only projections; JD-demand separation; migration
preview, recovery, rollback, and idempotency; Skills and Demand rendered
behavior; public-tree and diff gates. Use only synthetic/public fixtures. The
auditor must not author the implementation under review.

**Evidence.** The 2026-08-23 independent audit returned **FAIL** because backup
creation did not checksum-verify stored payloads and identified traceability and
rendered-UI evidence gaps. The bounded remediation was independently rerun on
2026-08-24. That rerun reproduced the race as blocked before writes, verified
all 17 definitions, both rendered surfaces, 717 tests, and the functional and
security gates, and returned **PASS WITH GAPS** with no acceptance-blocking
finding. The remediation and reproducible release-evidence commands are
recorded in `docs/history/cg900_remediation_2026-08-23.md`.

**Status.** Complete. Independent verdict: **PASS WITH GAPS**. The reported
gaps concern release-evidence reproducibility and hygiene rather than Competency
Graph behavior; their post-audit remediation remains part of the release checks.

## Deferred beyond this roadmap

Repository scanning and GitHub/provider integration remain outside CG-1.0 and
are tracked separately as the Planned `CAP-REPOSITORY-IMPACT-INTAKE`. Full
network visualization, radar charts, role suggestions, bidirectional Obsidian
editing, embeddings/vector databases, and composite candidate scoring remain
outside CG-1.0.
