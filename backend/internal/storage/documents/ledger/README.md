# Document index validation and read projections

This pure package ports `parse_document_ledger` and the projections in
`src/career_documents.py`. It has no filesystem, credentials, encryption,
logging, document upload or model API. The production Python reader is unchanged.

`Parse` accepts nonempty scalar-UTF-8 JSON of at most 20 MiB, the exact two-field
v1 root and exactly 13 fields per document. It preserves record order and raw
values; it does not apply upload/editor cleanup. Duplicate object fields, IDs,
missing/unknown/wrong-case fields, wrong scalar types, invalid bounds and date
ranges fail with no partial records. Invalid nested structures cannot enter a
valid scalar field. Parsing uses bounded raw-message decoding, not a recursive
general-purpose object graph.

`Metadata` returns an owned record slice and the SHA-256 revision of the exact
encrypted bytes. Pass `includeArchived=true` for the source default. `Titles`
returns only active, nonblank, Python-whitespace-stripped, exact-deduplicated
titles; Unicode15 casefold-equal titles remain distinct and sort by original
scalar value within the tied group. Neither projection authenticates anything.
Only use them after parsing, authenticated decryption, admitted-home checks and
exact ciphertext-file membership validation. Do not concurrently mutate inputs.

## Important distinctions

- String bounds count Unicode codepoints, not UTF-8 bytes or UTF-16 code units.
  Escaped surrogate pairs are supported. Python can accept unpaired surrogate
  strings; Go explicitly returns `DOCUMENT_LEDGER_INPUT_UNSUPPORTED` instead of
  silently replacing them with U+FFFD.
- Numeric versions/sizes require lexical integers, not booleans, float/exponent
  spellings or coercion. The stored MIME must be PDF, PNG or JPEG. Python's
  otherwise-valid record with a list/object MIME leaks `TypeError`; the target
  deliberately returns the safe `DOCUMENT_LEDGER_INVALID` error.
- Stored dates are empty or exact valid calendar `YYYY-MM-DD`. Stored timestamps
  use the pinned CPython-aware ISO predicate, not Go's narrower RFC3339 parser.
  The source's unusual separators, basic/week dates and accepted terminal-byte
  behavior remain possible. These predicates are not security sanitizers.
- `Path(filename).name == filename` is a **metadata** check. Its Windows/POSIX
  differences are preserved. The source even accepts `..`, Windows reserved
  names and trailing dots as metadata. Never use the original filename as a
  managed blob path; storage uses the validated document ID.
- Python set iteration makes equal-casefold title order nondeterministic. The
  target adds a scalar tie-break only; it does not merge titles or normalize
  other fields. The reference check verifies source title order and membership
  before canonicalizing only this title-array tie.
- Source failures involving arbitrary Python objects, deeply recursive invalid
  JSON and integer digit-limit exceptions are not covered by this bounded
  ordinary-JSON corpus. Do not claim exhaustive parser equivalence.

Errors contain only a code and fixed text; formatting never exposes parser
causes, fields or document values. `Metadata` is intentionally **not** a model
context type. The title-only API is an application boundary, not an OS sandbox
preventing a separately authorized shell or agent from reading local files.

## Verification

From `backend`, with the pinned Go toolchain/offline module cache:

```powershell
go test -buildvcs=false -mod=readonly -count=1 -cover ./internal/storage/documents/ledger ./internal/vault/calendarinput
go vet -buildvcs=false -mod=readonly ./internal/storage/documents/ledger ./internal/vault/calendarinput
```

The optional reference imports the actual source using an explicitly selected
Python3.12.14/UCD15 interpreter; it uses only synthetic data. Only `_read_ledger`
is replaced for the two projection checks, so they do not prove storage/key
composition. A 20-second subprocess context, 2-second wait delay, 4 MiB stdout
and 4 KiB stderr limits bound it. Unexpected exceptions fail; stderr is not
printed. Source and corpus hashes detect drift; exact JSON field/type/value
comparison retains unknown response fields rather than silently discarding them.

The first Windows checkpoint has 212 equivalent cases and five explicitly
separate cases: three unsupported surrogate strings, two safe-error deltas for
unhashable MIME values. One fixture exercises nondeterministic source title ties.
Ledger statement coverage is 97.2%; this is not migration completeness. Exact
20 MiB byte admission, codepoint boundaries, ownership, redaction and fuzz seeds
have separate tests. No real documents, native keys or installed runtime used.
