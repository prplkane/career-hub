# Effective skills catalog

This package is the pure, in-memory Skills Registry migration slice. It composes
the [typed validation](README.validation.md) and [immutable snapshot/read
payloads](README.payload.md) with the original core/custom reconciliation rules.
It is not yet a managed YAML loader, catalog writer or replacement application.

`Build(coreGroups, customGroups, coreSkills, customSkills, rawGroups, rawSkills)`
accepts **already validated model projections**. The raw byte arguments provide
the private-file revision only; they are not parsed, compared with the projections
or retained. A future loader must supply both from the same owned file snapshot.
Never expose this constructor directly as an unvalidated HTTP/extension input.

Build preserves these source contracts:

- Empty/missing core groups use the 14 default groups. Private groups replace
  same-ID core groups; a private name collision instead disambiguates the core
  display name with the first unused `built-in` suffix. Private IDs stay stable.
- Same-ID skills keep the custom name/category. Earlier aliases and the earlier
  canonical name remain deduplicated aliases. Only explicitly supplied extension
  fields override core values; omitted defaults differ from explicit null/false/[]
  through `Term.FieldsSet`.
- A private name/alias matching a core canonical name shadows that core ID and
  redirects relations. Other core alias conflicts remove only the conflicting
  alias. Notices retain source order and explain these non-destructive decisions.
- Redirected self-relations and repeated explicit relations are removed exactly
  where Python removes them; remaining graph/name/group/cascade validation still
  runs. Private archive state also applies in memory to newly supplied core
  members, without rewriting custom files or inventing cascade-history entries.
- Source `model_copy` changes retain field presence as well as values. Automatic
  archive marks `archived`; a changed parent or relation marks both `parent` and
  `relations`; no-op changes do not invent presence. This matters when a returned
  effective term is reused as an overlay.
- Per-catalog duplicate skill IDs fail before reconciliation, as in the Python
  file loader. An overlay cannot hide duplicate IDs in its input core catalog.
- Effective groups and skills are sorted by ID before snapshot construction.
  Grouped display payloads then use stable Unicode15 casefold-name order.

Build and snapshot reads do not mutate caller memory, create skills/evidence,
open files, discover a Data Home, access secrets or start processes. A vocabulary
match remains vocabulary, not proof that the candidate has a skill.

The parent maintainer command `scripts.migration_registry_check` compares actual
Python orchestration with Go through a bounded synthetic probe. In addition to
visible payloads and both revisions, it observes effective skill field presence;
payload-only equality would miss future overlay-reuse regressions. Error class,
code, message, ordering, nulls, arrays and origins are compared exactly.

Remaining boundaries: Pydantic/YAML decoding and coercion, byte/presence lineage
from real files, managed paths and concurrent writer ownership, catalog writes,
archive/restore mutations, enriched HTTP/agent adapters and installed runtime.
Very deep graph behavior also needs an explicit compatibility decision: Go's
iterative traversal is not Python's interpreter recursion-limit exception.
No existing private file is normalized or rewritten by this package.
