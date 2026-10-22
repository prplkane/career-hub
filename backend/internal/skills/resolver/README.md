# Exact competency resolver

This is the in-memory `CompetencyResolver` portion of MIG-130, not the complete
Skills Registry, phrase extractor or Go product runtime. It imports the shared
Python-compatible `textcompat.Normalize` adapter instead of substituting Go
lowercasing or whitespace rules.

## Behavior

- `New([]Term)` snapshots the indexed name, aliases, ID and archived state.
  Duplicate IDs fail with `ErrDuplicateID` (`Duplicate technology id`), without
  exposing catalog contents in the error or returning a partial resolver.
- `Resolve(source)` compares the entire normalized string. Canonical and alias
  collisions remain ambiguous; a canonical name does not outrank another term's
  alias. Repeated occurrences of one ID do not create duplicate candidates.
- Active and archived candidates have separate sorted ID lists. Archived-only
  text is unmatched. An archived candidate cannot make one active candidate
  ambiguous or become the selected ID.
- `ResolveReference(id)` uses the exact ID without casefolding or trimming.
  A known archived ID remains an exact historical reference and appears in both
  candidate lists. Unknown IDs remain invalid references.
- Every result preserves the original source string, has all six source-model
  JSON fields, returns empty candidate arrays rather than null, and uses null
  for an absent competency ID. JSON key order is not a persistence contract here.
- Construction and output slices do not alias caller-owned memory. Concurrent
  reads are supported after construction. The caller must not mutate inputs
  while `New` is running. The zero-value resolver behaves like an empty catalog.

Matching vocabulary is not candidate evidence. This package never creates a
skill, evidence link, JD, application or contact, and cannot read a Data Home.

## Deliberate boundaries

The Python constructor receives `TechnologyTerm` models, so model validation is
upstream: ID patterns, nonblank names, alias uniqueness, coercion, extra-field
policy, relations, groups and catalog merge rules are not reimplemented here.
`Term` is the already-validated projection, not an external API request schema.
The result methods likewise assume validated UTF-8 Unicode scalar strings.
Malformed UTF-8 and Python lone-surrogate strings are outside this package's
supported boundary; a JSON/input adapter must detect them before calling it.
They must not be replaced silently and reported as parity or ordinary unmatched
skills. This also avoids inventing an encoding error in the source status enum.

The Python resolver exposes catalog/index inspection properties. Those are not
part of this bounded Go package API; future registry readers can provide their
own immutable snapshots once the catalog model/merge slice is ported. This port
does not implement spaCy `TechnologyMatcher`, substring search, fuzzy matching,
source offsets, YAML persistence, HTTP, or the enriched `skill_lookup` response.
It provides no implicit input-size limit; external adapters own request bounds.

Reference files at the reviewed source commit `965ba60`:

- `src/vault_intake.py`, SHA-256
  `a062c50179876c3a43106fb999548f180c3f785abac06ce6e11b3fb1e91083f8`.
- `src/vault_intake_models.py`, SHA-256
  `c3bdd344285a03da17ba8bca986de461b1abfecf76c1cb2118d8e49df251caa2`.

## Verification

From `backend/`, after the shared text adapter is present:

```text
go test ./internal/skills/resolver -count=1 -cover
go vet -buildvcs=false ./internal/skills/resolver
go test ./internal/skills/resolver -run=^$ -fuzz=FuzzResolutionInvariants -fuzztime=5s -parallel=2
```

Tests cover all resolution outcomes, active/archive collision and ordering,
strict historical IDs, immutable input/result ownership, concurrent reads,
source preservation, JSON null/array shape and non-ASCII normalization fixtures.
Fuzzing checks bounded valid-scalar inputs and result invariants; it is not a
proof of Python equivalence. The parent migration harness supplies independent
cross-language evidence. A Windows race-detector run also needs a supported C
toolchain and must not be claimed from the ordinary concurrency test.

Measured on 2026-09-07 with Go 1.27.1 / Windows amd64 after the pinned text adapter
commit `0ad7190` was integrated:

- Focused Go tests passed with 100.0% statement coverage; focused vet passed.
- The bounded fuzz run passed after 136,698 executions with two workers.
- The original Python resolver regression in
  `tests.test_competency_graph.CompetencyGraphTests` passed on Python 3.12.14.
- An independent source-only review found no actionable mismatch within this
  package's declared scope. That review did not claim execution or full registry
  parity.
- Native `-race` was not run: CGO is disabled and no `gcc`/`clang` is discoverable
  on this host. `-buildvcs=false` avoids sandbox Git-ownership stamping during
  maintainer-only verification; it is not the release provenance policy.

The independent Python-Go resolver comparison remains a separate parent gate;
these package checks do not establish full MIG-130 or backend completion.
