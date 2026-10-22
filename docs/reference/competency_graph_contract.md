# Competency Graph Contract

Status: implemented MVP contract. The user-facing surface is the **Skills
Registry**. Graph fields remain compatibility metadata and must not complicate
the intake workflow.

## Purpose and scope

A **Competency** is a stable, curated identity for a named technical capability,
practice, concept, or related professional domain. It is not an unreviewed
keyword, a claim about the candidate, or a job requirement.

The graph provides one vocabulary that later consumers may use to:

- reference approved Career Vault evidence by stable id;
- resolve exact names and approved aliases deterministically;
- project candidate evidence independently from job-description demand; and
- export already-approved data to a consumer such as Obsidian.

The canonical Career Vault remains the source of truth for candidate facts.
The Competency catalog is a reference catalog, not a second Career Vault.

## Vocabulary model

Newly curated records use the following logical shape. The compatibility schema
accepts these fields, but adding or curating catalog records remains a separate
reviewed action; this example is normative for their meaning, not an
instruction to edit a catalog now.

```yaml
id: model_context_protocol
name: Model Context Protocol
aliases:
  - MCP
category: ai
kind: protocol
primary_domain: ai_systems
primary_group_id: api_integrations
relations: []
archived: false
```

### Stable ids

- `id` is the canonical machine identity. It is lower-case `snake_case`, starts
  with a letter, and contains only letters, digits, and underscores.
- An id is globally unique after the shared catalog and the user overlay are
  merged. It is never recycled, including after archival.
- Renaming a display name or changing aliases never changes the id.
- A record refers to another competency only by its `id`, never by a display
  string or an alias.
- Catalog serialization, resolver candidates, and projected output sort by id
  unless a consumer explicitly defines another display order.

### Names and aliases

`name` is the one human-facing canonical name. `aliases` are exact approved
spellings or acronyms for the same identity; they are not semantic synonyms or
guesses. A name and every alias must be non-empty after trimming.

Resolver normalization is deliberately narrow and deterministic:

```text
normalize(value) = collapse_internal_whitespace(casefold(value))
```

It does not remove punctuation, stem words, translate terms, apply fuzzy
matching, use embeddings, or ask a model to infer equivalence. Duplicate names
or aliases within one competency are invalid. Across the merged Skills
Registry, every normalized name and alias must belong to exactly one skill. A
conflicting create or update is rejected with `SKILL_NAME_CONFLICT` before
either catalog is written. The compatibility reader may remove a redundant
legacy spelling from its in-memory alias list only; it never rewrites the
legacy catalog automatically. If a later application update introduces a core
name or alias that collides with an already-valid private record, the private
record remains canonical for that Data Home: a colliding core skill is
shadowed, or only its colliding alias is omitted from the effective in-memory
catalog. This update-compatibility path returns a notice and never rewrites
private ids, names, aliases, or evidence references. It does not relax conflict
validation for new user creates or edits.

### Primary skill group

`primary_group_id` assigns a skill to exactly one user-facing group. Groups have
a stable `snake_case` id, a display name, an `archived` flag, and an origin:
`core` or `custom`.

- Core groups and skills ship with the application and are read-only.
- Custom groups and skills are stored in the private data home and may be
  created, edited, archived, and restored by the user.
- New custom ids use the reserved `user_` prefix so a future core catalog update
  cannot silently claim the same machine id. Existing legacy custom ids remain
  readable and are never renamed as a load side effect.
- `user_my_custom_skills` / **My custom skills** is the private fallback group
  for an explicitly reviewed skill whose group has not been curated yet.
- A new or edited skill must reference one active group.
- Archiving a custom group transactionally archives its currently active custom
  skills. The group records exactly which skills were changed by that cascade;
  restoring the group restores only those skills, while skills archived
  separately remain archived.
- Existing Experience and Project evidence keeps archived competency ids as
  historical links and remains editable. The editor marks those pills as
  archived and allows explicit removal, but the same archived id cannot be
  attached to a different record as new evidence.
- Legacy records without `primary_group_id` receive a deterministic effective
  group from existing classification fields, with `unclassified` as the safe
  fallback. Loading does not rewrite them.
- Group origin is presentation metadata. Core and custom groups use different
  muted colors; origin never affects matching or scoring.

### Kinds

New records must select one of these `kind` values:

```text
technology, language, framework, library, platform, service, tool,
protocol, standard, database, methodology, practice, concept, domain
```

`technology` is the intentionally broad compatibility value for a legacy
technology term whose finer classification has not yet been curated. `kind`
describes what the competency is; it does not claim the candidate has used it.

During the compatibility transition, `category` remains a required legacy
field for every serialized catalog record because current JD consumers use it
to route technologies into skills and platforms. `kind`, `primary_domain`,
`relations`, and `archived` extend that representation; they do not replace
`category`. Removing or deriving `category` requires a separately reviewed
migration after all current consumers stop depending on it.

### Primary domain

`primary_domain` is a single stable domain id used for filtering and grouping.
New records use one of:

```text
ai_systems, software_engineering, data_and_analytics,
cloud_and_infrastructure, security_and_identity, web_and_api,
business_systems, product_and_delivery, other
```

The field is optional only for a legacy-compatible record that has not yet been
curated. Absence means **unclassified**, not `other`; a migration must not
invent a domain from a legacy category or from a job description. `other` is a
deliberate curator choice for a reviewed record.

### Typed relations

Each relation is an object with exactly `type` and `target_id`. `target_id`
must resolve in the merged catalog, must not equal the source id, and duplicate
`(type, target_id)` pairs are invalid.

Allowed relation types and directions are:

| Type | Meaning of `source -> target` | Rules |
| --- | --- | --- |
| `member_of` | Source belongs to a broader competency family. | The `member_of` graph is acyclic. |
| `uses` | Source normally uses or depends directly on target. | Directional; it does not create evidence for target. |
| `implements` | Source implements target, such as a protocol or standard. | Directional. |
| `replaces` | Source is a curated successor to target. | Directional; it does not archive the target. |
| `related_to` | Source has a curator-approved non-hierarchical association with target. | Query semantics are symmetric; either endpoint may store the relation. The merged graph canonicalizes the unordered id pair and rejects a mirrored duplicate. |

New graph relations provide navigation and explanation. They never create
candidate evidence, satisfy a job requirement, add a skill to a Resume Plan, or
produce a score without a separately approved rule. The existing legacy
`parent` requirement expansion remains the narrow compatibility exception
defined below; it must not be generalized to new `member_of` relations.

## Catalog merge and resolution

The application loads one Skills Registry from four files:

| Origin | Groups | Skills |
| --- | --- | --- |
| Core | `src/data/skill_groups.yaml` | `src/data/job_technologies.yaml` |
| Custom | `career_vault/taxonomy/skill_groups.yaml` | `career_vault/taxonomy/technologies.yaml` |

Core and custom records are merged by stable id. The custom record may extend
the same id for legacy compatibility, while preserving the shared canonical
name as an accepted spelling. A private group with the same legacy id controls
that group's effective archived state; a core group with a different id but a
colliding display name receives a visibly disambiguated in-memory built-in
label. Every normalized name and alias across different ids remains unique in
the effective catalog. Update-introduced collisions use the non-destructive
compatibility rules above; all new conflicting private writes are rejected.

A custom record is vocabulary, not proof that the candidate has used the
skill. It is created only by an explicit user action: direct Skills Registry
management, or final approval of an unknown skill label the user added to an
eligible Resume Digest evidence card. The review draft stores only the proposed
name and group; save/preview/reject never changes the registry. Neither intake
path creates or changes vocabulary merely because unknown prose appears in a
source, and JD intake never creates vocabulary under any approval path.

New private skill records also retain creation-channel metadata. `created_via`
is one of `manual_registry`, `vault_editor`, `resume_digest`, or
`legacy_migration`; `created_at` is an ISO 8601 UTC timestamp when the creating
workflow has a real event time. A legacy record may omit either field and is
not rewritten on load. These fields explain how vocabulary entered the private
catalog. They are not candidate evidence and do not change matching.

The resolver indexes active canonical names and aliases using the normalization
above. Because uniqueness is a write/load invariant, operational results are:

| Result | Condition | Automatic action |
| --- | --- | --- |
| `exact_match` | One active canonical name matches. | Emit the stable id and retain source wording. |
| `alias_match` | One active approved alias matches. | Emit the stable id and retain source wording. |
| `unmatched` | No active id matches. | No entity creation and no guessed substitution. |
| `invalid_reference` | A stored id does not exist in the merged registry. | Validation failure; it is not converted to a text match. |

The low-level compatibility resolver can still represent `ambiguous` when a
caller supplies an invalid ad-hoc term list. The application-owned Skills
Registry prevents that state before Resume or JD intake runs.

Archived records remain resolvable by id for historical display and validation,
but are excluded from active matching and from new references. An archived-only
textual match is surfaced as an archival diagnostic and never selected.

## Active and archived lifecycle

The logical lifecycle field is `archived: boolean`, defaulting to `false` for
new records and for legacy records that omit it.

- Active competencies may be referenced by new approved records and may appear
  in normal resolver, navigation, and demand views.
- Archiving is reversible and preserves the id, name history, aliases,
  relations, and existing canonical references.
- Existing references to an archived competency remain valid and display its
  canonical name; archival must not produce dangling references.
- Cascaded group archival changes availability, not ownership or evidence
  history: stored references remain resolvable, while active matching and new
  selection exclude the group and its archived skills.
- New intake approvals, migration writes, and manual editors may not attach an
  archived competency. Reactivation is an explicit curator action.
- Permanent deletion and id reuse are outside the MVP.

## Evidence and demand are separate projections

### Candidate evidence

Candidate evidence comes only from active, canonical Career Vault records with
explicit approved `competency_ids`. In the MVP, eligible source records are
Experience, Project, and Certification records. The separately Planned
`CAP-REPOSITORY-IMPACT-INTAKE` may propose Repository Evidence and related
Experience or Project records, but they may participate only after the existing
explicit canonical-reference, provenance, review, and approval contract has
made them Career Vault records.

Legacy `technologies` strings remain valid source data and display input during
the transition. They do not become canonical competency evidence merely because
they can be resolved. A record acquires a `competency_id` only through explicit
user approval or a separately approved, exact, reviewable migration.

An evidence context is the source record, not the number of matched strings:

- Each active Experience is one professional context.
- A Project linked to an Experience is additional evidence attached to that
  professional context; it is never a second independent professional context.
- A Project without an Experience link is one independent project context.
- Each active Certification is one certification context.

This rule prevents a project that belongs to an experience from inflating
professional-context counts. Future analytics may count source types,
first/last-used dates, and proof counts, but must not manufacture a composite
candidate score.

An active canonical Vault record that already references an archived competency
still projects historical evidence for that id. The reference is valid and is
not rewritten or dropped. Active-by-default explorer, resolver, migration, and
coverage views omit archived competencies; a consumer must request an explicit
archived scope to display their historical evidence. New references to the
archived id remain prohibited.

### Candidate graph and analytics projection

The implemented Candidate Evidence Graph is a pure, rebuildable in-memory/JSON
projection. It contains active Experience, Project, and Certification nodes;
explicit Competency-to-evidence edges; active Project-to-Experience edges; and
Competency-to-domain edges. Only stored `competency_ids` create competency
evidence. Legacy strings, job records, intake drafts, review files, and Resume
Plans are not graph inputs. Archived competencies require an explicit
historical-scope option, while archived evidence records are always excluded.

Evidence analytics consumes that graph and applies these deterministic rules:

- professional contexts are distinct Experience ids referenced directly or by
  a linked Project carrying the competency;
- an active Project is independent only when its canonical
  `related_experience` list is empty;
- certification count is distinct active Certification evidence;
- proof count is the number of non-empty proof points on direct evidence nodes;
- source types describe direct Experience, Project, and Certification evidence;
- first/last used and `active_now` use dated Experience contexts and dated
  Project evidence, but a certification completion date is not treated as a
  usage interval;
- overlapping or adjacent month intervals are unioned and clipped to an
  explicit analytics `as_of` month before inclusive months are counted; and
- no composite or arbitrary candidate score is produced.

The projector stores no cache or graph database. Identical Vault, catalog,
archival scope, and `as_of` inputs produce identical revisions and JSON.
The Competency Explorer payload exposes its Vault, private-registry, and
effective-catalog revisions plus `include_archived_competencies`. The browser
compares the effective catalog revision with the independently loaded Skills
Registry and retries once if concurrent reads crossed a mutation. Archived
skill detail requests the explicit historical scope on demand; the normal
**All skills** projection remains active-only.

### Job-description demand

Job-description demand is a separate, read-only projection from saved job
records. It records exact canonical or approved-alias matches in the retained
job source. Each occurrence keeps the skill id, canonical name, exact source
wording, primary group, origin, required/preferred classification, source line,
and character span. Exact duplicate job descriptions are deduplicated by the
existing job identity/fingerprint rule before demand frequency is counted.
Only validated normalized cards already present in the application ledger enter
the projection. An inclusive captured-date period may be selected; required
and preferred job counts remain separate, while mentioned-only occurrences do
not count. Cards that predate occurrence storage use only exact active
canonical names or approved aliases from structured technology fields.

A job description may create demand observations only. It must never:

- create a competency;
- create or modify a candidate evidence record;
- add `competency_ids` to Career Vault data;
- upgrade an alias or a draft suggestion into an approved candidate fact; or
- change a candidate analytic result.

Candidate Evidence and Job Demand are joined by the implemented read-only
Coverage Matrix. Its statuses are `supported`, `thin_evidence`,
`evidence_not_found`, and `not_requested`. A result of `evidence_not_found`
means only that the Career Vault projection contains no qualifying evidence;
it must not say that the candidate does not know the competency.

The **Jobs / Demand & coverage** view is a read-only presentation of that join.
It may summarize an inclusive saved-ledger period, filter domain,
required/preferred demand and coverage status, show the saved jobs contributing
to one skill, and navigate to the existing Skills evidence view. It must not
edit the catalog or Vault, display a composite score, claim a historical trend,
or turn an absence result into a candidate-knowledge claim.

### Intake and approval boundary

Resume and JD intake use the same active Skills Registry and exact matcher.
Resume extraction stores source-backed mentions for review and highlighting;
JD digestion stores source-backed occurrences for matching and demand
analytics. A resolver result is not candidate evidence. Unapplied extraction
reports, editable `review.yaml`, queued review cards, rejected suggestions, and
draft Resume Plans are excluded from candidate-evidence analytics.

Only explicit approval stores a canonical id. Approval keeps the original
source wording in intake/audit provenance. A rejected suggestion is not
retained as candidate evidence. Legacy extraction artifacts may still display
an ambiguity, but the validated operational registry cannot create a new one.

For an eligible Resume Digest card, existing exact matches are stored compactly
in the review as stable ids. Unknown user-entered labels are stored separately
as pending `{name, primary_group_id}` values. Approval pins both the Vault and
registry revisions, resolves the pending values again, and writes any new
private custom terms together with the Experience, Project, or Certification
`competency_ids` in one rollback-capable transaction. If the Skills field is
not selected for the card, no pending term is created.

## Compatibility with `TechnologyTerm`

The legacy-compatible `TechnologyTerm` shape is:

```yaml
id: string
name: string
aliases: []
category: string
parent: string | null
primary_group_id: string | null
created_via: string | null
created_at: string | null
```

It remains accepted without rewrites during the transition.

- `id`, `name`, and `aliases` retain their current meaning and are the
  compatible resolver inputs.
- `category` remains a legacy classification. It is not silently converted to
  `primary_domain`.
- A missing `kind` is interpreted as the compatibility kind `technology`.
- A missing `primary_domain` is unclassified.
- A missing `primary_group_id` receives an in-memory effective group and is not
  rewritten merely by loading.
- A missing `relations` is an empty explicit relation list.
- Missing creation metadata means a legacy or unknown creation channel and
  does not trigger a backfill write.
- When present, legacy `parent` has the effective graph meaning of a
  `member_of` relation from the term to that parent. The serialized `parent`
  field remains supported until a separately reviewed migration can preserve it
  safely.
- Current parent-based JD review continues to expand a parent requirement with
  the exact names and approved aliases of its descendants. This is a legacy
  compatibility behavior, not the default semantics of `member_of`. New graph
  relations must not enter requirement expansion until a separate coverage
  contract explicitly authorizes and tests that behavior.

The compatibility layer preserves old reads, existing PhraseMatcher inputs,
current job-review behavior, and overlay precedence by id. It does not rename
the skill catalog files, delete legacy fields, or rewrite private data as a side
effect of loading.

## MVP boundary

The MVP includes one core/custom Skills Registry, one primary group per skill,
exact deterministic resolution, direct registry management, reviewed skill
pills on eligible Resume Digest cards, optional canonical references from
approved Vault records, and rebuildable read-only evidence/demand projections.

It excludes:

- fuzzy, semantic, embedding, vector, or model-assisted matching;
- automatic competency creation or candidate-skill inference;
- a graph database, scoring model, role recommendations, or a network graph
  visualization;
- repository scanning and GitHub API integration;
- bi-directional Obsidian editing;
- resume generation, rendering, or changes to user-provided templates; and
- any automatic write to Career Vault data from a job description or draft
  intake artifact.

Obsidian is only a one-way consumer/export of generated, approved data. It is
not a source of truth and cannot edit the catalog, evidence, or demand ledger.

## Acceptance capabilities

The implementation is accepted when it can prove all of the following:

- a skill has a stable id, name, aliases, exactly one effective primary group,
  core/custom origin, and active/archived lifecycle semantics;
- old `TechnologyTerm` catalogs remain valid without a data rewrite;
- core and custom catalogs merge deterministically, normalized name/alias
  collisions are rejected before a user write, and collisions introduced only
  by a later core update preserve private identity without a load-time write;
- Resume and JD intake use the same merged registry and preserve exact source
  spans for recognized skills;
- neither intake path creates vocabulary from source prose automatically;
  direct registry management or final approval of a user-entered Resume Digest
  skill is required;
- every stored canonical reference validates against an existing id, while an
  archived referenced id remains displayable and does not dangle;
- candidate evidence is built only from active approved canonical Vault
  records, with linked projects not double-counted as independent professional
  contexts and historical references to archived competencies preserved only
  in explicit archived scope;
- draft intake, rejected suggestions, and draft Resume Plans are absent from
  candidate analytics; and
- job demand remains read-only and cannot create or modify candidate evidence.
