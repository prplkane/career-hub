# Browser HTTP boundary: read-only default, explicit owned workflows

CH-32/CH-38/CH-39/CH-40 connect eleven fixed routes for the separately selected Go sidecar:

| Request | Authorization | Result |
| --- | --- | --- |
| `GET /api/health` | Allowed loopback Host; absent/local Origin; not cross-site | Existing `protocol.HealthRecord(appVersion)` |
| `GET /api/session` | Allowed loopback Host; browser (or scoped agent) token; absent/local Origin | `{client: "browser", capabilities}`; never a token (ADR SEC-250) |
| `GET /api/skills-registry` | Allowed loopback Host, exact browser capability, absent/local Origin | Injected registry snapshot |
| `GET /api/vault/profile` | Same authenticated browser boundary | Fresh validated profile and same-read revision |
| `GET /api/vault/eligibility` | Same | Full eligibility record and revision |
| `GET /api/vault/education` | Same | Schools, certifications and revision |
| `GET /api/vault/experiences` | Same | Records, stable categories and revision |
| `GET /api/vault/projects` | Same | Records, categories, experience options and revision |
| `GET /api/vault/skills` | Same | Legacy records, evidence label options and revision |
| `GET /api/vault/snapshot` | Same | Five editor sections from one validated eight-file load |
| `GET /api/documents` | Same | Authenticated document metadata and ciphertext revision; archive selector only |

`New(port int, appVersion string, registry Registry, vault Vault, documents Documents) (http.Handler, error)`
takes the actual listener port and trusted callbacks. `Registry` returns
`(map[string]any, error)`; `Vault` accepts the request context plus a closed
`vaultread.View` and returns
`(json.RawMessage, error)`. `Documents` accepts context and include-archived bool,
returning an owned `*ledger.Listing`. All three callbacks are required.
It does not create a listener, discover configuration/DataHome, select a product
backend, write files, or connect to SQLite, credentials or an extension. All
static, mutation and other API routes are unavailable with `New`. The separate
`NewWithMaterials` composition below adds the owned material workflow. OPTIONS never grants
CORS; paths are never redirected or normalized to another route.

`NewApplication` additionally composes the fixed first-party frontend and
Profile/Eligibility, advisory policy and helper settings owners. It is enabled
only by maintainer bootstrap, not the installed/default backend. Exact assets,
envelopes, limits, publication facts and remaining product gates are described in
the Profile HTTP contract.

The browser capability uses 32 bytes from `crypto/rand`, encoded with unpadded
URL-safe base64. It lives only in the private handler and is returned only from
an authorized session response. It is not included in health, errors, logs,
configuration, cookies, protocol readiness, or an exported token accessor.
Separate handler instances get separate random capabilities. Construction
refuses a missing callback, invalid port/version or failed random read, without
retaining the underlying cause. There is no insecure randomness fallback.

## Source behavior

The routing/security contract comes from `src/run_intake_ui.py`:
`_allowed_hosts`, `_local_origins`, `_authorize_request`, `_handle_session`,
`_handle_health`, `_send_bytes` and `_send_error_json`; registry payload routing
comes from `src/intake_ui_routes/reads.py`. Capability spelling is the existing
`X-Career-Hub-Token` from `src/intake_ui_routes/contracts.py`.

Host accepts `127.0.0.1`, `localhost` or `[::1]` with the exact configured port,
using the source's trim/case-fold behavior via existing pinned `textcompat`.
Origin is trimmed but remains case-sensitive and must exactly match one of the
three `http://` local origins. Capability comparison is constant-time for equal
length inputs and does not trim the supplied token.

Authorization preserves the ordering Host → route-specific bootstrap rules or
capability → browser Origin → mutation fetch metadata. In particular, the
source does **not** reject `Sec-Fetch-Site: cross-site` on an authenticated GET
registry read with an absent/local Origin. Health/session do reject cross-site
fetch metadata. This package does not silently expand that source policy.

Responses preserve JSON content type, Content-Length, no-store, nosniff and the
source Content-Security-Policy. Fixed public guidance comes from
`src/intake_error_catalog.py`; `LOCAL_API_ACCESS_DENIED` deliberately uses its
existing default guidance. JSON meaning is preserved, not whitespace, key
order or Go's extra escaping of HTML-sensitive characters. Underlying HTTP
transport-generated errors/headers remain owned by `net/http` and runtime.

## Explicit pilot restrictions and error deltas

- Ports must be `1..65535`. Version labels must be nonempty, at most 128 ASCII
  bytes, using letters, digits, `.`, `-` or `+`. These are construction bounds,
  not a new product version parser.
- Request targets are limited to 8192 bytes. Absolute-form URLs, explicit raw
  encoded paths, fragments, percent/backslash paths and alternate path spellings
  do not select the eleven fixed read routes. Material routes permit encoding
  only within a validated job-ID segment, as documented below. Query strings are ignored for route selection
  but count toward the limit. Rejection is fixed `RESOURCE_NOT_FOUND`; normal
  authorization runs first. No input path is reflected.
- Duplicated sensitive headers (Origin, Sec-Fetch-Site, capability, or an
  ambiguous Host) are rejected instead of selecting a first value. The native
  Go HTTP parser also refuses duplicate Host fields before handler dispatch.
- No extension capability or helper routes exist here. Thus browser requests
  to excluded extension routes are unavailable, rather than fully reproducing
  extension-specific purpose errors. Unsupported methods cannot mutate data;
  health/session method violations use source boundary errors, while other
  authenticated non-GET methods return fixed404. No HEAD alias is added.
- All registry callback errors, encoding failures, nil payloads and recovered
  callback/encoding panics return fixed500 `UNEXPECTED_ERROR` without dynamic
  message or error type. This deliberately replaces source-specific400 domain
  errors that may expose catalog names or local paths. There is no diagnostics
  file writer in this slice; inherited generic guidance is not a claim that a
  run was recorded.
- Encoded registry responses are limited to 4MiB. The trusted, input-bounded
  catalog is encoded with standard `encoding/json` before the byte limit is
  checked. This is a transport limit, **not a peak heap bound** or an arbitrary
  hostile-object validator. A callback must return ordinary JSON-compatible
  maps and slices that are not concurrently mutated during encoding. Callback
  calls and encoding are serialized; registry errors do not retain that lock.
- All seven Vault views share that serialization lock and authorization-before-read order.
  Its callback returns a validated, owned JSON object of at most 4 MiB. Canceled
  requests do not dispatch reads or publish a returned projection. Cancellation
  while awaiting the shared lock or synchronous native work is still bounded
  by the enclosing process, not by an invented interruptible filesystem.
  Known loader/manual validation failures return 409
  `VAULT_RECORD_VALIDATION_FAILED` with fixed, non-private text.
  Unsupported/limit/read-cancellation states retain allowlisted fixed 500 codes;
  native admission failures and unknown errors/panics return `UNEXPECTED_ERROR`.
  Structured internal issues never leak IDs/values or become a raw HTTP error.
  The seven-field error envelope is retained; no diagnostics log is written.
- A literal route switch selects the closed Vault view enum; prefixes, path
  segments, query parameters and filenames never become reader/root selectors.
  Other Vault APIs, including demographics and mutations, remain unavailable.
  `VAULT_PROJECTION_LIMIT` and `VAULT_PROJECTION_UNSUPPORTED` are fixed 500
  categories, not private formatting exceptions. Snapshot shares callback
  serialization, not an interprocess writer lease or filesystem transaction.
- Failed/partial response writes cannot be replaced after response commitment.
  They are not retried, logged with their cause, or followed by another response.

## Runtime ownership and evidence

Runtime must bind only the intended loopback listener, provide bounded
ReadHeaderTimeout, ReadTimeout, WriteTimeout, IdleTimeout and MaxHeaderBytes
(16KiB in this pilot), and suppress raw server ErrorLog output. This handler
reads no bodies in the default read-only mode; the HTTP server owns body
lifecycle and deadlines. Explicit material POST bodies have the bounds below.
The registry callback has no invented filesystem cancellation guarantee:
supervisor/process shutdown and external bounded verification own that gate.

Ordinary tests are memory-only, using `httptest` recorders, fake RNG and injected
catalogs rather than listeners or private storage. The matrix covers boundary
precedence, source headers/error shape, health/session payloads, token privacy,
duplicates, raw paths, unavailable methods/routes, output size/encoding/panic
failures, callback serialization and failed writes. Actual subprocess, listener,
Electron supervisor, initialized synthetic DataHome and unchanged-files checks
remain parent-owned integration evidence; package tests alone do not prove them.

CH-39 verification: the full HTTP package passes at 99.4% coverage (0.365 s),
including every-route authorization/context/freshness, unknown selectors, privacy,
post-panic lock release and registry/Vault shared serialization. The composed
readonly and CLI suites plus vet pass. The fingerprinted Windows process suite
passes 11/11 with no skips (26.226 s), comparing all seven routes with actual
Python through the unchanged Electron supervisor. Reproduction, binary hash
and native limitations are recorded in `docs/history/migration/progress.md`.

## CH-40 document route

The exact metadata route uses the existing capability, Host/Origin checks,
headers and callback-serialization lock. Authorization precedes all storage.
It does not expose blob download, uploads, writes, keys, raw-ledger parsing or a
new title-context API. Cancellation before dispatch or after reading/encoding
discards the result. The callback supplies already authenticated, schema-validated
metadata; this adapter is not an arbitrary object validator or model projection.

`include_archived` matches the source: absent/blank defaults to true; first
nonblank duplicate wins; casefolded `1` and `true` mean true. Parsing is
percent-byte-tolerant, splits on `&` rather than `;`, and does not trim values.
Other query keys cannot select a file, credential namespace or projection.
2,772 comparisons execute the actual Python route branch with only the data
reader and response sink replaced by spies. This proves query semantics, not
whole Python request authorization. The corpus and source hashes are in the
document read contract.

The document-specific encoded response cap is `2*20 MiB + 128 bytes`.
HTML-sensitive characters are not unnecessarily escaped in this standalone
`application/json` response. Valid input JSON can contain literal U+2028/U+2029,
which Go encodes as six bytes instead of three: at most 2x expansion, with the
extra allowance for replacing `version` with `revision` and the trailing newline.
A validated exactly-20-MiB fixture exercises that expansion. Other routes remain
at 4 MiB. Encoding finishes before headers/body are published; oversize returns
`DOCUMENT_RESPONSE_LIMIT`, nil projection `DOCUMENT_RESPONSE_UNSUPPORTED`.
The standard encoder buffers first; these are output limits, not peak heap bounds.

Known document storage/key/encryption/ledger failures retain source 400 status
and their allowlisted code, with fixed content-minimized guidance rather than
dynamic exception text. Target unsupported/resource-limit/cancellation cases
are fixed 500; admission failures, unknown errors and panics are fixed
`UNEXPECTED_ERROR`. No partial metadata, native cause or diagnostics file is
returned/written. Existing generic diagnostics guidance does not imply a run
was recorded.

After CH-40, the HTTP suite passes at 98.3% statement coverage (1.345 s), including
all prior routes, document authorization/query/error/size tests and shared
serialization. Vet and CLI regression pass. The native supervisor document case
passes against actual Python empty/malformed stores without accessing credentials;
populated authenticated reads are separately proven at the service boundary.
This does not establish installed or real-credential acceptance.
The complete native supervisor suite also passes 15/15 with no skips in
40.601 s; its fingerprinted report and interrupted-run history are recorded
in the document read contract. No production supervisor code changed.

## Owned material workflow

`NewWithMaterials` adds five required, trusted callbacks (`Draft`, `Review`,
`Export`, `Download`, `Legacy`). The executable selects these only after explicit
`--sidecar-bootstrap` admission and owned SQLite preparation. The default
`--sidecar` remains read-only; the installed Python backend is not switched.
Callbacks use the existing material owner, including its context-aware operation
gate, SQL reservation, observed source set and synchronous cleanup. They do not
borrow the older registry/Vault serialization mutex. The composition rechecks
bound workspace identity/channel/resources before and after each operation.

Let `B = /api/jobs/<job>/materials/resume` or
`/api/jobs/<job>/materials/cover-letter`:

| Request | Behavior |
| --- | --- |
| `GET B` | Existing editor payload from current sources |
| `POST B` | Existing `action`, `expected_revision`, `draft` envelope; save or approve |
| `GET B/reviews` | Inspect factual findings; never persist confirmation |
| `POST B/reviews` | Explicit human confirm/revoke using current revision, review SHA and target IDs |
| `POST B/exports` | Export saved primary draft; `format` is docx/pdf/txt, default pdf; return metadata |
| `GET B/file?artifact_id=...` | Read the exact verified existing artifact; no render |
| `GET B/file?format=pdf` | Existing Python download URL: render/export then download |
| `GET B/legacy-file?filename=...` | Confined opaque original export, labeled `legacy_unverified` |

The compatibility GET `/file` **can publish an export**, just as Python does.
It additionally enforces mutation fetch-metadata rules; it cannot use the normal
authenticated-GET cross-site exception. Prefer POST export + explicit artifact
download for new clients. A request cannot supply both a nonblank artifact ID
and format. Query decoding retains the source's first-nonblank `parse_qs` behavior.
Only the job segment is percent-decoded and domain-validated (including bounded
Unicode/space IDs); encoded route names or separators do not select an operation.

Authorization precedes body reading and callbacks. POST requires one
`application/json` content type, a positive known Content-Length and at most
1 MiB including the envelope. Chunked/ambiguous headers, trailing JSON, unknown
outer fields and invalid shapes are refused. Revisions are nonnegative int64
JSON integers, not booleans/fractions/exponents. Duplicate JSON keys remain
last-wins like the source. Input is UTF-8 only: Python's incidental UTF-16/32/BOM
autodetection is not supported. Inner draft/selector validation remains with the
typed owner; callers never supply a Data Home, resource root, filesystem path,
manual-check record, or claim that can authorize export.

Material callbacks use a 20-second operation context. After body admission the
actual response controller sets a 25-second write deadline, replacing this
request's default 10-second deadline. The server's 5-second body-read and startup
deadlines are unchanged. No abandoned callback goroutine is introduced. Native
calls remain cooperatively cancelable, and process exit is the final shutdown
boundary. A disconnect or failed response is not evidence that publication did
not occur; clients must reload or perform the exact idempotent retry.

Successful JSON must be an owned valid UTF-8 object within the existing 8 MiB
material response bound. Downloads independently check job/kind, requested
artifact ID, safe filename/format, provenance, byte count and SHA-256 before
headers are committed. MIME, attachment filename, no-store, CSP and nosniff are
preserved; `X-Career-Hub-Provenance` distinguishes versioned and legacy files,
and only versioned downloads have `X-Career-Hub-Artifact-ID`.

Fixed errors include `operation_state` with known/committed/partial/cleanup_failed
facts. Unknown state after a callback panic is null, not false. Late owner or
workspace errors preserve publication facts while suppressing all result bytes.
Unknown internal codes, native causes, paths, token values and prose are not
reflected. No diagnostics record/run ID is invented. The transport does not
retry a failed response write. Review confirmation is **not** registered as an
autonomous agent permission; the explicit review UI/agent integration and
installed acceptance remain separate required work.

Memory tests cover authorization-before-body/dispatch, source request mapping,
invalid/ambiguous bodies and paths, exact download selection, hashes/headers,
late failure facts, panic recovery and cancellation. The Windows CLI native
test uses Python-generated fictional material fixtures, actual `execute` and
process-owned startup, real pipes/listener and all five real material callbacks.
It exercises both material kinds/all formats, save conflicts, inspect/explicit
confirm, export/download/exact retry, legacy reads, identity drift refusal,
EOF shutdown, and read-only restart. The helper must run in a fresh process. This is not
an installed Electron/GUI test or permission to activate a real profile.
