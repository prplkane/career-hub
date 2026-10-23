# Document key-state compatibility pilot

This isolated package ports the typed boundary of `src/document_keys.py` at
reference commit `0629caf`. It is not wired into the application. It does not
discover a Data Home, open files, access native credentials, log keys, perform
network operations, or change the existing CH-20 document envelope format.

## Contract

`State` contains only `active_key_id`, `algorithm`, `data_home_id`, and `version`.
`CanonicalBytes` reproduces Python's sorted compact JSON, `ensure_ascii=True`,
and exactly one final LF. Like the Python dataclass serializer, it serializes
typed but semantically invalid fields; it does not validate a state. Its string
inputs must be Unicode-scalar UTF-8. It allocates proportionally to the supplied
strings and has no serializer-specific source size limit; adapters must bound
untrusted inputs before calling it.

`ParseState` accepts at most 4,096 bytes and requires exact canonical byte equality,
version integer 1, algorithm `AES-256-GCM`, and lowercase opaque identifiers
`dh_[0-9a-f]{32}` / `key_[0-9a-f]{32}`. It rejects unknown, missing, duplicate and
wrong-case fields, alternate escapes/order/whitespace, invalid UTF-8, and a
missing or extra final LF. Decoding success alone is never acceptance. The parser
does not modify or retain its input. Callers must not mutate it concurrently.

`NewManager(channel, backend, random)` requires an explicit `Backend` and exactly
`dev`, `beta`, or `stable`. A nil random function uses `crypto/rand`; there is no
default native backend. The target is exactly:

```text
CareerHub/{channel}/document-key/{data_home_id}/{key_id}
```

`Create` validates the home before any adapter operation, asks for 16 random ID
bytes and then 32 key bytes, validates **both lengths after both successful calls**,
invokes the optional before-write callback, and finally writes the credential.
The first RNG exception stops the sequence immediately; a second RNG exception
is not masked by a wrong first-result length. Callback failure prevents a write.
No collision lookup, retry, rollback, cleanup, or regeneration is added: a
successful write to an existing identical target retains the source overwrite
behavior. A backend may have completed side effects before returning an error;
an operation error is not proof that nothing happened.

`Load` validates state and returns an independently owned key. `LoadKey` supports
an explicitly requested retained rotation key and never falls back to another
key, channel, home, or a newly generated key. `Backend.Read` returns `found` apart
from its bytes, so absent and existing-empty keys remain distinct. Every backend
error takes precedence over its accompanying return value. `DiscardUnpublished`
deletes exactly the validated target and treats an absent result as success; it
does **not** establish that a key is unpublished. The calling transaction must
already know this. It must not be exposed as a general deletion command.

ID bytes are copied before the second RNG call can reuse their buffer. Key bytes
are copied before callbacks and independently for backend writes and results.
Read results are copied before return. Thus adapters/callers can retain or mutate
their own buffers after a call without aliasing other owners. No key cache is
kept. Concurrent manager calls require concurrency-safe injected adapters and
callbacks. An adapter must not mutate a returned buffer concurrently while the
manager snapshots it. Go garbage collection is not guaranteed secret zeroization.

## Errors and supported input boundary

`*Error` carries source domain codes and exact fixed source messages:
`DOCUMENT_KEY_STATE_INVALID`, `DOCUMENT_KEY_UNAVAILABLE`, `DOCUMENT_KEY_INVALID`,
`DOCUMENT_KEY_GENERATION_FAILED`, and `INVALID_CHANNEL`.
`DOCUMENT_KEY_BACKEND_REQUIRED` is a **pilot configuration refusal**, not a new
Python product error or a source-compatible default backend.

RNG/callback/backend errors become `*OperationError` with one fixed safe message
and operation `random`, `before_write`, `write`, `read`, or `delete`. These are
infrastructure failures, not missing credentials or domain parity. The private
cause is deliberately available through `Unwrap` for typed inspection; never
log the unwrapped cause. Standard diagnostic formatting of package errors never
prints that cause, key bytes, or a credential target. The package emits no logs.
Do not serialize `MasterKey` or key-bearing adapter state into diagnostics.
Adapter panics are not converted to successful or missing-key results.

Python arbitrary object types, lone-surrogate strings, falsey backend objects,
non-byte protocol violations, and dataclass versions such as `True`/`1.0` are
outside this typed API. Parse tests still exercise boolean/float versions in raw
JSON and reject them exactly as the Python parser does. Go `int` bounds the
direct state version; a probe may impose a narrower documented transport bound.
No implicit trimming, numeric-to-string conversion, or case normalization occurs.

## Verification and stop line

Focused tests cover canonical bytes and actual-Python Unicode escaping, malformed
state/typed-invalid fields, exact target namespace, RNG/callback/backend order and
failure precedence, collision overwrite, mutable-buffer ownership, retained keys,
safe errors, and one synthetic round trip through the existing envelope package.

From `backend`, using the pinned toolchain and `GOTOOLCHAIN=local`:

```text
go test -count=1 -cover ./internal/storage/documents/keys
go vet ./internal/storage/documents/keys
go test ./internal/storage/documents/keys -run=^$ -fuzz=FuzzParseState -fuzztime=3s -parallel=1
```

Actual Python/Go synthetic replay is a separate parent-owned gate. Native Windows
Credential Manager and macOS Keychain policy/identity, missing/locked/denied
access, publication/rotation/recovery journals, and installed-runtime behavior
remain separate manual/native gates. Linux credential support remains explicitly
unsupported until implemented. Fakes prove sequencing, not OS protection. Do not
activate this pilot against real credentials or private documents.
