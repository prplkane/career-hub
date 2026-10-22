# Snapshot, revisions and read-only lookup

This is the pure payload portion of CH-24. It does not load catalogs, select a
Data Home, parse YAML, access credentials, write data or start a runtime. `Build`
owns reconciliation, sorting and validation before calling private `newSnapshot`.
Inputs are already-validated model projections with valid UTF-8 scalar strings;
Pydantic coercion, malformed encodings and iterable-vs-string errors belong to
the external adapters, not this typed API.

## Ownership and API

`Snapshot` copies every input group/term, optional string, nested relation,
aliases/cascade/FieldsSet slice and core-ID set. Notices are copied values. Raw
catalog bytes are hashed and not retained. Inputs must not be mutated during
construction; after construction callers may mutate their originals safely.

- `Groups`, `Skills`, `Notices` return independent copies; `CoreGroupIDs` and
  `CoreSkillIDs` return sorted copies. In constructor `map[string]bool` sets,
  only true entries represent members.
- `GroupOrigin` / `SkillOrigin` return `core` for member IDs and `custom`
  otherwise, including unknown IDs, matching the Python snapshot.
- `Revision` and `CatalogRevision` return the two distinct source fingerprints.
- `Payload` returns the Python grouped registry shape with all model fields,
  effective primary group IDs, origins and ordered compatibility notices.
- `Lookup([]string)` strips Python whitespace, rejects the first blank term,
  rejects an empty batch, then uses the existing exact resolver. It returns all
  five status counts, enriched matches/candidates/archived candidates and the
  original recommended actions. It never interprets a vocabulary match as
  candidate experience. A clean term's internal spacing is preserved as source
  text; only the resolver's separate normalized key collapses whitespace.

`CleanLookupTerms([]string)` exposes that same input stage for the external
adapter to call **before** Build/loading. `Lookup` repeats the pure check for
direct callers. A caller that builds first would change compound-error ordering.

Payload/Lookup values are JSON-native maps, `[]any` arrays, strings, booleans,
integers or nil. Each invocation owns all nested maps/slices. Even one result's
match and candidate descriptions are separate objects. Empty arrays are not
null. Error Class/Code/Message preserve the source's typed-domain behavior;
content-bearing domain messages must be sanitized by diagnostic probe adapters.

## Two revision contracts

`RawRevision(groups, skills []byte)` hashes exact supplied custom-file bytes:
`groups` + NUL + group bytes + NUL + `skills` + NUL + skill bytes + NUL.
The external loader supplies empty bytes for an absent file. Whitespace, line
endings and field layout are deliberately significant in this mutation revision.

Catalog revision hashes the ordered effective group and skill model projections,
using compact sorted-key UTF-8 JSON without a trailing newline. It is independent
of origin IDs, notices, `FieldsSet` and YAML layout. Null `created_via` and null
`created_at` are omitted **independently and only in this revision projection**;
all other model nulls remain. Payload still includes both provenance fields.

The small private serializer covers just those model JSON types. It follows
`ensure_ascii=False`: no HTML escaping, literal non-ASCII scalars including
U+2028/U+2029, and exact JSON escaping for controls, quotes and backslashes.
Literal backslash-u text is never rewritten after encoding. Invalid UTF-8 fails
with a content-free internal error rather than being replaced during hashing.
This is not the ASCII sidecar encoder and is not a general-purpose JSON codec.

Snapshot group/skill order is not silently re-sorted. Python's builder determines
that order and catalog hashes include it. `Payload` only sorts skills within each
group by Unicode15 casefold of the name, **stably**, without collapsing whitespace
or adding an ID tie-breaker. Effective group IDs are a payload enrichment; they
do not overwrite a null source field used by catalog revision.

## Evidence and verification scope

Reference base: `9e95af8`; SHA-256 of the inspected Python source files:

| Source | SHA-256 |
| --- | --- |
| `src/skills_registry.py` | `7377bcc9ccffc70cf2191c032f00f4c0820a00b8ec7503386b5671ddd139a026` |
| `src/skill_lookup.py` | `145c5378902ab25fee750e4ec13ff5cd0d504d1e2f27c046e6fc2bd4e890be45` |
| `src/vault_intake.py` | `a062c50179876c3a43106fb999548f180c3f785abac06ce6e11b3fb1e91083f8` |
| `src/vault_intake_models.py` | `c3bdd344285a03da17ba8bca986de461b1abfecf76c1cb2118d8e49df251caa2` |

Symbols: `SkillRegistrySnapshot.to_payload`, `_catalog_revision`,
`_registry_revision`, `_clean_terms`, `_term_payload`, `_recommended_action`,
`lookup_skill_terms`, and the actual `CompetencyResolver` implementation.

`payload_test.go` contains independent Python 3.12.14 raw/catalog hash vectors,
including absent/partial/full provenance and non-ASCII/control escaping. It also
checks exact model shape, nil/empty distinctions, source order, stable ties,
origin/fallback behavior, input and output aliasing, lookup error order and
concurrent reads. A few tests deliberately construct a Python-style snapshot
directly to inspect defensive ambiguity/unknown-group/duplicate-ID behavior;
these do not assert that the full validated `Build` accepts those catalogs.

Run `go test ./internal/skills/catalog` and `go vet ./internal/skills/catalog`
from `backend` with pinned Go 1.27.1 and `GOTOOLCHAIN=local`. Differential Build
and lookup replay is owned by the parent harness. These checks do not establish
YAML, filesystem, mutation, native-race or production-runtime parity.
