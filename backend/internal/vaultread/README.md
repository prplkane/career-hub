# Career Vault profile read migration — CH-38

The fixed loader, manual/calendar/reference validators, final contact check and
bounded JSON projection are implemented and tested. The authenticated Profile
HTTP route is wired and verified in the opt-in Go sidecar. This is **not an
installed-backend switch**. `ReadProfile(ctx, root)` is a read-only service.
The workspace rechecks Data Home identity before and after reading, but this
does not replace caller-owned cross-file writer coordination.

Sources:

- `src/vault_schema.py`: ordered eight-file contract.
- `src/vault_editor.py:_load_vault/_validate_loaded_vault/load_profile_editor`.
- `src/validate_vault.py`: manual validation, not Pydantic/catalog coercion.
- `src/date_normalization.py`: calendar interpretation without stored-value edits.
- `src/intake_ui_routes/reads.py` and `src/run_intake_ui.py`: HTTP dispatch/errors.

The loader reads profile, education, eligibility, experiences, projects, legacy
skill blocks, proof blocks and style rules in that order. Every file must exist
and decode to its declared mapping or sequence. Null/empty YAML is not a default
document. The first path/read/YAML/root-shape failure stops loading; manual
validation belongs after all eight loads. Even malformed unrelated evidence or
style rules can therefore block a profile read in the source.

Paths belong only to the explicitly supplied Data Home. There is no resource
fallback, default-file creation, profile discovery or migration. Managed readers
reject links/aliases/file-type violations according to runtimepaths' existing
contract. Each file uses the existing 2 MiB YAML bound and parser budgets. Eight
sequential files have finite aggregate work, not an exact process memory limit.
Native file-size refusal is an input-limit result, not a YAML validation success.

Each file's revision is hashed from the same read used for decoding, including
CRLF and BOM. No extra raw-file copy is retained and no path is reopened or YAML
re-encoded to calculate a later revision. The
source revision reopens the profile after validation. Same-byte revision and
not creating parent directories are declared read-safety improvements, not
exact source side-effect parity. This does not provide a cross-file transaction
or writer lease. Context cancellation is checked between reads/parses; synchronous
filesystem/parser work still requires the caller's process/lifecycle bounds.

`Error` keeps fixed stage/source/code, sanitized YAML stage/coordinates and
ordered issues containing only fixed schema fields and integer positions.
Formatting never includes the absolute root, native cause, private text or raw
parser prose. Unsupported YAML, uncaught source-constructor exceptions,
unhashable validation values and resource limits are separate from supported
validation failures. At most 256 issues are retained; exceeding that bound
refuses the result explicitly, while later source exceptions still take priority.
The HTTP route maps known ordinary failures to a fixed 409 validation response;
unsupported/limit/canceled states keep separate fixed 500 codes. Internal issues
are not exposed as raw private error prose.

The JSON projection retains unknown fields, original values, insertion order
and Python scalar-key conversion (including duplicate JSON names). It does not
insert contact/default values. Dates, datetimes, bytes and nonfinite numbers are
explicitly refused if present in the profile response. The retained response is
bounded to 4 MiB, not silently truncated; standard-library string encoding has a
documented bounded temporary expansion, not an exact whole-process memory cap.

Verification so far:

- Source-order and competing failures, absence/null/wrong shape, raw-byte and
  decoded-value preservation, cancellation, safe errors and limit classification.
- Native synthetic root reads preserve bytes, identity, file modes, modification
  times and membership. Missing Vault directories remain absent.
- Actual pinned Python 3.12.14 / PyYAML 6.0.3 lower-level loader and complete
  `load_profile_editor` comparisons pass on synthetic success/failure fixtures.
  Successful payload/revision and ordered issue Source/Code are compared;
  exact Loc/Fields have focused tests, not full source-projection parity.
  Source hashes and synthetic file bytes/identity/modes/mtimes are checked.
  Reference TypeError classification is restricted to characterized source raise
  sites; an injected revision-helper failure must remain unexpected.
  Fixture-only stdin/output and process waits are bounded.
- Native full package passed in 6.521 s with 94.3% coverage; vet passed.
  Tightened reference/error-location tests subsequently passed in 6.658 s.
  Independent static review found and prompted corrections to builtin YAML
  exception classification, calendar subfield locations and reference-harness
  exception handling. No installed runtime or real-profile acceptance is claimed.
- The final composed readonly/HTTP/CLI tests and vet pass. The fingerprinted
  Windows process suite passes 11/11 with none skipped (23.439 s), including
  actual Python Profile parity, fresh revision, missing-profile refusal, private
  error redaction and identity drift through the unchanged Electron supervisor.
  See the progress register for the executable SHA-256 and exact evidence.

Reproduce from `backend` with the pinned offline toolchain:

```text
go test -buildvcs=false -mod=readonly -count=1 -timeout=40s -cover ./internal/vaultread
go vet -buildvcs=false -mod=readonly ./internal/vaultread
```

Windows native managed-path tests may need the
normal host because sandbox ancestor metadata is unavailable. No user data is
required. Remaining read contract details are in
`docs/history/migration/vault_profile_contract.md`.

## CH-39 remaining read models

`ReadEligibility` and `ReadEducation` use shared eight-file validation without
the standalone Profile contact-null restriction. Eligibility retains its record;
Education returns only schools/certifications, preserving record unknown fields
and defaulting an absent certifications collection to an empty list. Non-JSON
values in an unreturned document/unknown Education root field are not blanket
rejected, matching source projection behavior.

These services are exposed through six additional fixed authenticated routes.
`Read(ctx, root, View)` dispatches a closed enum; unknown values are refused
before opening a root. Thirty-two additional
actual-Python comparisons pass alongside the existing Profile/loader corpus;
the full native Vault package passes in 10.562 s with 95.2% coverage, and vet
passes at that checkpoint. Experiences, projects, skill options and aggregate
snapshot projection are now implemented with 108 passing offline fixture
evaluations and 286 passing actual-Python display/truthiness comparisons. The
new native read-model comparisons pass all 108 cases (11.245 s); full native
Vault tests pass in 21.992 s at 96.0% coverage. The composed workspace/HTTP/CLI
checks and vet pass; the native Windows process suite passes 11/11 without
skips, including all seven routes compared with actual Python through Electron's
unchanged supervisor. This remains a maintainer-only partial backend.
See `docs/history/migration/vault_read_models_contract.md`.
