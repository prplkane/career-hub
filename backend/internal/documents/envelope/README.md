# Document envelope compatibility pilot

This isolated package ports `src/document_encryption.py` at source commit
`3f4f125` using only Go's standard AES-GCM, JSON, and secure-random facilities.
It is not wired into the product and never reads or writes files, accesses a
credential store, creates keys, or contacts the network.

`Encrypt` and `Decrypt` use the caller-supplied 32-byte key and validated object
context. `Inspect` returns **unauthenticated** metadata; never derive an expected
decryption context from an inspection result. All failed decryptions return a
zero result, not partial plaintext. Input slices remain caller-owned and must
not be modified concurrently while an operation is using them.

The version-1 layout is big-endian `>6sBI` (`CHDOC\0`, version, header length),
canonical JSON header, a fresh 12-byte nonce, and AES-256-GCM ciphertext with a
16-byte tag. Prefix plus header is authenticated additional data. Headers are
bounded to 4,096 bytes and plaintext to 20 MiB. No nonce injection API is exposed.
Typed `*Error` values carry the source error code and content-minimized message.

Canonical byte equality after decoding rejects unknown, missing, duplicate,
case-variant, reordered, or escaped fields and noncanonical JSON whitespace.
Unlike plain Go struct decoding, this check cannot silently accept duplicates or
unknown fields. Valid header values are restricted to ASCII opaque IDs and fixed
format/purpose values, preserving Python's exact canonical serialization.

Tests include a pinned synthetic Python fixed-nonce vector, empty/blob/ledger/
binary round trips, concurrent nonce freshness, tampering, AAD context binding,
typed errors and precedence, malformed JSON/framing, and exact size boundaries.
The fuzz target bounds input to 64 KiB; deterministic tests cover large objects.

From `backend`, run:

```text
go test ./internal/documents/envelope
go vet ./internal/documents/envelope
go test ./internal/documents/envelope -run=^$ -fuzz=FuzzEnvelope -fuzztime=5s -parallel=1
go test -race ./internal/documents/envelope
```

Use the pinned Go toolchain and `GOTOOLCHAIN=local`. A race run requires a native
C toolchain; absence is a missing verification, not a pass.

Known source error-contract gap: Python's JSON header with `purpose: []` or
`purpose: {}` raises an unsanitized `TypeError` from set membership. This Go codec
rejects those inputs with `DOCUMENT_ENVELOPE_INVALID`. It does not accept any
additional envelope or return plaintext. Cross-language evidence must record
this discrepancy explicitly, not silently normalize exception classes.

Parent-owned bidirectional Python/Go comparison, independent security review,
OS key-store integration, and native packaged-runtime acceptance remain separate
gates before replacing the production implementation or touching real ciphertext.
