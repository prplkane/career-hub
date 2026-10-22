# Sidecar protocol interoperability slice

MIG-120 / CH-21 is an isolated standard-library port of the **pure primitives**
in `src/sidecar_protocol.py`, not a Go sidecar runtime or production cutover.
Python and Electron are unchanged. The package has no files, credentials,
environment/home discovery, network access, goroutines, listeners or dependencies.
The caller injects application version and optional home expansion.

## API and test hooks

- `ReadRecord(io.Reader)` reads exactly one record, without consuming a later
  record. It accepts a binary UTF-8 byte stream, with at most 8194 bytes read and
  an 8192-byte payload after stripping LF and an optional preceding CR.
- `ParseStart(map[string]any, PathOptions)` and `ParseShutdown(map[string]any)`
  enforce the Python field sets and validation order. `StartRecord` contains
  the normalized paths, zero port and channel. No existence, overlap, resource
  identity or permission check is performed by the Python or Go primitive.
- `ParseRecoveryMaintenance` preserves the exact new-job plan/advance
  request, while `ParseDocumentMigrationMaintenance` independently preserves
  the installed Python document plan/apply/recover field sets and validation
  order. Neither parser opens or claims a Data Home.
- Document-migration plan/complete/recovered record helpers expose only the
  installed content-minimized fields. They omit paths, migration/key identity,
  document metadata and recovery receipts.
- `ReadyRecord(appVersion, port, channel)`, `HealthRecord(appVersion)` and
  `ErrorRecord(code)` return the same fields, without extra validation or data.
- `EncodeRecord(map[string]any)` emits compact, sorted, ASCII-escaped JSON plus
  one LF. `WriteRecord(io.Writer, payload)` writes those bytes and calls
  `Flush() error` if present; a plain unbuffered Go writer needs no flush.
- `*Error.Code` contains the stable `SIDECAR_*` failure. I/O errors are returned
  unchanged, as the Python primitive propagates them; the future lifecycle
  adapter must map them to content-free process errors. Never print raw I/O errors
  or input records to the protocol stream.
- `*CompatibilityError.Reason` explicitly identifies the unsupported cases
  below. This is **not** a new production protocol code or a parity pass.

A maintainer probe can base64-decode a synthetic input into `bytes.NewReader`,
call `ReadRecord`, optionally `ParseStart`/`ParseShutdown`, then compare an
`Error.Code`, normalized start fields, or exact `EncodeRecord` bytes. Distinguish
`CompatibilityError` from both protocol errors and success. No executable or
generic server placeholder belongs in this package.

## Preserved semantics

The C-02/C-03/C-13 migration hazards have focused table tests:

- Exact LF/CRLF byte bounds; empty/closed/truncated/oversize/malformed/invalid
  UTF-8/invalid-shape errors and their ordering; no read-ahead across records.
- Duplicate keys are **last-wins**, including replacing a nested object rather
  than merging it. This differs from the strict encrypted-envelope contract.
- JSON integer tokens are `json.Number`; decimal/exponent tokens become
  `float64`. `-0` is an integer zero, but `0.0`, `0e0`, booleans, null and strings
  do not meet Python's exact `int` requirement. Large supported integers remain
  lexical and are never rounded into a supported version or port.
- Python `NaN`, `Infinity`, `-Infinity` and float overflow are accepted at read,
  but do not pass version or port validation. The small lexical adaptation uses
  collision-free placeholder numbers outside quoted strings, then the standard
  JSON decoder validates the entire grammar. Strings containing those words are
  unchanged. This is characterization, not a recommendation for new protocols.
- Start validation order is fields, protocol version, command, port, channel,
  resource path, data path. Channels are exactly `dev`, `beta`, `stable`.
- Path length is at most 4096 Unicode code points **before** expansion, not
  UTF-8 bytes or UTF-16 units. Empty strings, NUL and relative paths fail.
- `PathOptions{}` selects Windows semantics on Windows and POSIX elsewhere.
  Explicit `Windows` / `POSIX` styles allow lexical fixture replay on either
  host; they do not prove native OS behavior. Like `pathlib`, separators and `.`
  are normalized but `..` is preserved. Windows drive roots, UNC shares and
  device syntax are distinct from POSIX roots; POSIX preserves exactly two
  leading slashes. The implementation is deliberately not `filepath.Clean`,
  which would collapse `..`, or a universal POSIX absolute-path check.
- Expansion applies only to a leading `~`/`~user` component after parsing a
  path without drive/root. The injected resolver receives that component, not
  the full path. Its result is combined lexically with the remaining components.
  Tests use only synthetic homes. Nothing implicitly consults the current user.
- Actual ready/health/error serialization is tested as bytes: sorted fields,
  ASCII escaping (including surrogate pairs), no HTML escaping, compact spacing
  and one trailing LF. The output cap is measured **after** ASCII escaping.

## Explicit unsupported behavior and remaining gates

These limits are not approved production behavior changes:

1. Python accepts escaped lone UTF-16 surrogates in JSON strings/keys. Go's
   standard JSON decoder silently replaces them. This slice detects them and
   returns `CompatibilityError("escaped lone surrogate")`, including a surrogate
   in a duplicate value later overwritten. Non-scalar direct Go strings also
   return a compatibility exception. Valid scalar Unicode and paired escapes
   are supported. Do not compare replacement characters as parity.
2. Home expansion without an injected resolver is explicitly unsupported. A
   failing resolver returns the content-free `home expansion unavailable` reason;
   Python can raise an unwrapped `RuntimeError` when expansion is unavailable.
   Production user/profile discovery and named-user behavior need a separate
   platform adapter and tests.
3. More than 4300 lexical integer digits returns a compatibility exception;
   Python 3.12's default integer limit raises an unwrapped `ValueError`, and the
   limit is interpreter-configurable. Check even overwritten duplicate values.
4. More than 900 nested input containers is explicitly unsupported. Python's
   eventual `RecursionError` threshold depends on interpreter/stack state; the
   pilot does not silently accept arbitrary nesting that Python cannot read.
5. Output supports only JSON wire values: `map[string]any`, `[]any`, nil, bool,
   scalar strings, Go signed/unsigned integer kinds, `json.Number`, and
   `float64`. It is not a port of arbitrary Python mappings/custom classes or
   Go structs. Nesting/cycles beyond 1000 return a compatibility exception.
   Tested float formatting includes signed zero, decimal/exponent thresholds
   and non-finite values; helpers emit no floats, and exhaustive IEEE float
   representation parity is not claimed. Output is encoded before the size
   check, matching the Python primitive; callers should supply bounded values.
6. Binary byte streams are the Go contract. Python also accepts `TextIO`
   `readline`, whose limit counts characters before UTF-8 conversion, and text
   writers. Go callers must supply byte readers/writers; text-stream read-ahead
   behavior is not a parity claim. Framing has no timeout/cancellation itself.

This protocol package still owns none of the runtime concerns above. The
separate `sidecar/maintenance` composition now dispatches the two exact command
families in a one-record Go child, claims the requested Home and selects the
Windows binary secret backend only for document apply/recover. Electron remains
the timeout/reap owner. Packaging, installed-runtime selection and native
lifecycle acceptance remain required before replacing the Python runtime.

## Source evidence and verification

Source baseline is the protocol branch base `bd20c4d`; SHA-256 file evidence:

| Reference | SHA-256 |
| --- | --- |
| `src/sidecar_protocol.py` | `f5539b4f3521852c1435410a4a08d706b3ab09437b3f28a6aa28b3659f71b04f` |
| `desktop/src/sidecar.js` | `16436c3e866ba57d67e13903061a56441266fefa4748f6709fdf0c2cdca3d618` |
| `src/runtime_paths.py` | `09b3831adba45c98830043eb59b08b8d7d74aea442272ae442ed2b003094374e` |

Primary behavioral references: `read_protocol_record`, `parse_start_record`,
`parse_shutdown_record`, `_encoded_protocol_record`, `ready_record`,
`health_record`, `error_record`; `RELEASE_CHANNELS`; Electron
`parseReadinessLine`, `validateReadyRecord`, `_verifyHealth`, `_stopOwnedChild`.
Pure path behavior was characterized with local Python 3.12.14
`pathlib.PureWindowsPath`, `pathlib.PurePosixPath` and `Path.expanduser` source.

From `backend/`, using the pinned Go 1.27.1 toolchain with `GOTOOLCHAIN=local`
and per-worktree `GOCACHE`/`GOTMPDIR`:

```text
go test ./internal/sidecar/protocol
go vet ./internal/sidecar/protocol
```

Native Windows focused tests/vet passed during implementation. Table tests are
synthetic and touch no Data Home. Parent-owned differential fixture evidence is
separate; a fixture match does not establish complete runtime/lifecycle parity.
