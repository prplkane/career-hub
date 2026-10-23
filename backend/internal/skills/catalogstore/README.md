# Managed catalog reads (CH-26)

`Load(resourceRoot, dataRoot)` composes read-only managed paths, raw catalog
decoding and effective registry construction. Both roots are explicit absolute
existing directories chosen by the host. The adapter does not find the current
profile, inspect credentials, create a Data Home, rewrite catalogs, or activate
an application route. The Python/Electron product remains unchanged.

| Root | Fixed file | Role |
| --- | --- | --- |
| Resources | `src/data/skill_groups.yaml` | Core groups |
| Resources | `src/data/job_technologies.yaml` | Core skills |
| Data | `career_vault/taxonomy/skill_groups.yaml` | Custom groups |
| Data | `career_vault/taxonomy/technologies.yaml` | Custom skills |

Both custom paths are checked before any catalog decoding. Reads then follow
source order: core groups, custom groups, core skills, custom skills. Missing
optional files become absent inputs; empty/falsey core groups use the existing
default groups. Corrupt or invalid files never become empty successful results.
After all inputs validate, `catalog.Build` performs the already-tested overlay,
archive, graph, origin and revision behavior.

CH-35 adds `ReadState(resources, data)` for borrowed already-open managed roots.
It returns the same immutable effective snapshot together with owned raw custom
bytes and raw custom group models from those exact reads. It remains read-only;
the caller owns synchronization and handle lifetime. This prevents a mutation
service from serializing reconciled core records or forming editable models from
a separate reread with a different raw revision.

## Safety deltas and limits

- A file's decoded models and raw revision use the **same owned bytes**. The
  Python loader currently rereads custom files later for revision hashing; this
  port deliberately removes that mismatch opportunity. Neither operation is a
  transactional snapshot of both files. The host must coordinate cross-file
  writers; do not treat individually stable reads as a multi-file transaction.
- Resource and data roots must exist. An invalid configured root never falls
  back to a new empty profile, cwd or another deployment's public catalog.
- The managed reader rejects nonregular files and link-like components instead
  of treating directory leaves or dangling links as missing. These are explicit
  hardening changes, not identical behavior to Python's `is_file()`/`exists()`.
- Each catalog read is bounded by the YAML adapter's 2 MiB input limit. Path,
  directory enumeration, Unicode-compatibility and parser limits remain explicit
  failures, not source-equivalent missing files. Refer to the path and YAML
  package contracts before production adoption.
- Read-only does not establish OS ownership/ACL validation, Data Home marker or
  schema acceptance, backup consistency, secret policy, safe writes, or installed
  application identity. Those are separate host/persistence responsibilities.

`Error` carries a fixed stage and file-role label, plus either a sanitized path
code, structured input failure, or existing registry failure. Its default string
does not reveal absolute paths or content. Nested model locations and registry
messages can contain skill labels; a product diagnostic adapter must not log
the entire structure as if it were redacted. Native source exception prose and
path-bearing group wrappers are not the maintainer comparison contract.

## Verification

Unit fixtures cover exact check/read order, early failure, no partial snapshots,
same-byte revision behavior, explicit root separation, untouched file trees,
default catalogs and absence of bootstrapping.

Run from `backend`:

```text
go test ./internal/storage/runtimepaths ./internal/skills/catalogstore -count=1
go vet ./internal/storage/runtimepaths ./internal/skills/catalogstore
```

The Python-to-Go filesystem comparison harness and its `catalog-store-probe`
command were retired with the Python runtime.

Use `-buildvcs=false` only for the documented sandbox Git-ownership build issue,
not as a replacement for release provenance. Actual results, native limitations
and pending gates are recorded in migration progress.
