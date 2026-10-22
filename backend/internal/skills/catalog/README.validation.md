# Typed catalog validation foundation

This portion of MIG-130 ports the in-memory graph/registry invariants and effective
group mappings. It is not a YAML reader, Pydantic coercion replacement, catalog
writer, backend activation, or evidence-creation mechanism.

`Term`, `Group`, `Relation`, and `Notice` preserve the source field projections.
Nullable term fields are independent pointers; cloned collections are nonnil,
including empty aliases/relations/cascade state. `FieldsSet` retains explicit
input-field presence for overlays but is excluded from product JSON. Model
adapters must provide defaults (including `kind: technology`), valid enums, IDs,
scalar UTF-8 strings and source-specific field validation. These functions do
not infer missing facts or silently fill model fields.

`EffectiveGroupID` preserves explicit-group, domain, then exact legacy-category
precedence. `DefaultGroups` returns fresh fallback groups in the Python order;
the separate `My custom skills` identity is not automatically inserted as core.
Unknown legacy categories use `unclassified`. Invalid domain enums are outside
the typed precondition and must be rejected by the upstream model adapter.

`ValidateTerms` checks duplicate IDs first, then relations in input order, with
the legacy parent appended after explicit relations. It preserves duplicate,
unknown-target, self-reference, mirrored-related-pair and member-cycle messages.
An iterative DFS follows sorted roots and sorted outgoing member edges, retaining
the Python first-cycle node. It does not reproduce the Python interpreter's
recursion-limit exception on unusually deep graphs: the deep-chain test proves
Go stack robustness, not compatibility at that runtime-limit boundary. The
migration's differential register must keep that distinction explicit.

`Validate` checks group identity/names, the entire graph, skill groups/names in
input order, then group cascade state. Archived objects remain subject to global
name and graph constraints. An archived group rejects active members; historical
archived members and valid cascade IDs remain accepted.

`Error` retains `Class`, `Code`, and exact `Message`. Graph failures are
`ValueError` with an empty code; registry failures preserve `SkillRegistryError`
and the existing stable code. Unlike a sanitized diagnostic record, these exact
messages can contain catalog text. Future API/log adapters must preserve their
own existing disclosure and redaction policies.

Reference files at `9e95af8`:

- `src/skills_registry.py`: SHA-256
  `7377bcc9ccffc70cf2191c032f00f4c0820a00b8ec7503386b5671ddd139a026`.
- `src/vault_intake.py`: SHA-256
  `a062c50179876c3a43106fb999548f180c3f785abac06ce6e11b3fb1e91083f8`.
- `src/vault_intake_models.py`: SHA-256
  `c3bdd344285a03da17ba8bca986de461b1abfecf76c1cb2118d8e49df251caa2`.

Verification commands from `backend/`:

```text
go test ./internal/skills/catalog -count=1 -cover
go vet -buildvcs=false ./internal/skills/catalog
```

The VCS flag is a maintainer sandbox ownership workaround, not a release policy.
Parent-owned reconciliation/payload tests and independent Python–Go fixtures are
separate evidence. Full registry migration, managed persistence and user-data
cutover are not established by these package tests.

Measured 2026-09-07 on Go 1.27.1 / Windows amd64: focused tests passed with 99.4%
statement coverage; focused vet passed. An independent Python 3.12.14 oracle over
all 64 legacy-category pairs, 9 domain pairs and 14 fallback groups matches the
Go-computed compact JSON SHA-256
`68381ef59c58eff1b0c64975009dd19ca258e5c8ecdfbb0a86d25a07bc7b19c8`.
The test retains that source-derived fingerprint and tests mapping priority,
ordered error classes/codes/messages, archived invariants and clone ownership.
Native race-detector execution and full cross-language catalog replay remain
separate gates; this host has no enabled CGO/C compiler toolchain.
