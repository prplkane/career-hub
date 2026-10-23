# Custom group rename (CH-35 / MIG-150)

This section documents the earlier rename use case. The package now also
implements native-owned archive/restore and single-record group/skill edits,
using the fixed recoverable catalog journal. Their contracts and qualification
status are in catalog mutation contract.
None is selected by the installed product runtime yet.

`New(resourceRoot, dataRoot).RenameCustomGroup(ctx, id, payload, revision)` ports
`update_custom_group` in `src/skills_registry.py`. It is **not connected to the
product HTTP server or installed sidecar**. Tests use owned synthetic roots only.

## Use-case contract

1. Reject unknown payload keys before filesystem access.
2. Read the effective registry and its exact raw custom inputs together.
3. Check the raw revision before core/not-found/name validation.
4. Locate a raw custom group; preserve its ID, position, archived state and
   cascade IDs. A custom ID shadowing a core ID remains editable.
5. Strip the new name with pinned Python whitespace semantics and enforce
   1–160 Unicode scalar values. Validate the **current effective core groups**
   plus candidate **raw custom groups**, using current effective skills.
6. Encode only private groups. Recheck both private files' raw revision.
7. Replace the existing group file through retained managed directory handles;
   independently compare its expected bytes again before replacement.
8. Reload and check exact written groups and unchanged skills before returning
   the raw updated record, `origin: custom`, and actual saved revision.

An identical cleaned name still rewrites the file, as Python does. Core files,
skills, evidence and ledger records are not modified. Unlike Python's mutable
loader, this operation never incidentally creates missing directories on an
invalid/missing request. It requires an existing custom groups file.

## Revisions and serialization

The optimistic token is `catalog.RawRevision(rawGroups, rawSkills)`, not the
effective `catalog_revision`. Whitespace/comments/newline changes can make an
edit stale. Editable models and the initial token share one owned byte capture.

Go emits conventional LF YAML with ordered `id`, `name`, `archived` and optional
nonempty `cascade_archived_skill_ids`; strings are explicitly quoted. This
preserves validated models, not PyYAML's exact formatting. Python's single-file
`write_text` also translates LF to CRLF on Windows. Tests compare decoded records
and independently hash each runtime's **actual output bytes**, never normalized
bytes. Different serializers can return different valid post-save tokens. Only
the explicitly edited file is rewritten; there is no blanket normalization.

Existing reader byte/node/Unicode limits remain explicit adapter limits. A
source-valid catalog can exceed them or grow past them during encoding: failure
must precede replacement. Unrelated existing names need not satisfy the new
name's 160-character limit. NUL, line breaks, non-BMP text and YAML-looking names
remain strings after both Go and actual Python loading.

## Writes, errors and cutover limits

- Calls retain the per-instance gate and also acquire `operationgate`, shared
  with the cache-owning assessment service, before observing any catalog. The
  lease is held through replacement, readback and cleanup. It is non-reentrant;
  nested service calls must not reacquire it. Other future runtime writers must
  participate before cutover. The host must still exclude noncooperating writers
  across the entire operation. Python has per-server
  `threading.Lock` objects, not a global Data Home writer lease. Electron instance
  and child ownership and port exclusivity do not exclude a separate backend.
- Raw preconditions are observations, not cross-process CAS. The second file can
  change between its check and group replacement without host coordination.
  Readback refusal detects selected later changes; it is not rollback.
- `runtimepaths.Root.ReplaceFile` owns bounded single-file staging. Its package
  contract records path/hardlink checks, exclusive temp creation, flush/close,
  metadata, cleanup and platform limits. File flush plus successful replace is
  **not** power-loss durability or multi-file recovery proof.
- `Error.Committed` stays true if replacement succeeded but verification or final
  close failed. Do not claim the old file survived or auto-retry the old token.
  The product adapter must offer reload and content-minimized diagnostics. The
  default error string is safe; nested registry/model details can contain private
  labels and must not be logged wholesale.
- Cancellation is cooperative between synchronous filesystem calls, not hard
  native I/O preemption. Before replacement it can refuse the request; after
  replacement verification still runs to report the committed outcome honestly.
- Data Home bootstrap/admission, ACL enforcement, cross-process ownership,
  recovery, HTTP mapping and installed upgrade acceptance remain separate gates.

This earlier one-file rename result does not qualify the newer archive/edit
services. Evidence/Vault writes and product cutover remain separate gates; use
the linked mutation contract for the implemented catalog operations.

## Verification

With pinned Go and the offline module cache:

```text
go test -buildvcs=false -mod=readonly ./internal/skills/catalogwrite ./internal/skills/catalogstore ./internal/storage/runtimepaths -count=1 -cover
go vet -buildvcs=false -mod=readonly ./internal/skills/catalogwrite ./internal/skills/catalogstore ./internal/storage/runtimepaths
```

Fixtures cover
validation, reconciliation, archive/cascade preservation, raw tokens and a
Python → Go → Python write sequence. Filesystem assertions compare target and
unrelated content/mtime/mode effects, not OS access-time invariance. Actual run
results and native limitations belong in progress.
