# Read-only Gmail transport pilot

CH-33 ports the narrow `GmailReadOnlyMailbox` boundary from `src/email_sync.py`.
It is not wired into the shipped service, Go HTTP sidecar, credential store or
sync coordinator. No mailbox is contacted by ordinary validation: integration
tests use ephemeral in-memory certificates and a local synthetic TLS server.

`Open(ctx, account, password, folder)` connects only to `imap.gmail.com:993` using
verified implicit TLS (minimum TLS 1.2), then LOGIN and EXAMINE INBOX. Endpoint
and test-CA injection are private to package tests. There is no plaintext,
STARTTLS fallback, debug stream, URL fetch, system proxy or new-provider option.
Account/password are caller supplied; no secret enumeration/storage is performed.

The public facade provides UIDVALIDITY, UID search, header/body PEEK and socket
close only. It never exposes STORE, APPEND, MOVE, COPY, writable SELECT, EXPUNGE or
IMAP CLOSE. Opening/reading mail does not create a job/application or mark a
message read. The library can issue automatic CAPABILITY requests.

## Safety and compatibility decisions

- Pin `go-imap/v2 v2.0.0-beta.8` and its declared dependencies. Stream FETCH
  literals; never call its unbounded `Collect`/`AllUIDs` helpers. The prerelease
  label and upstream parser behavior remain release-review considerations;
  exact MIT notices/provenance are in `backend/licenses/imap-pilot`.
- Each command is serialized and has a 20-second absolute operation deadline;
  the whole session has a 120-second ceiling and inherits the opening context.
  Waiting for a busy command gate is cancelable. Canceling an active command
  closes the entire session, preventing accidental reuse of an abandoned reply.
- Library deadline updates, including zero/deadline clearing, cannot extend
  the active bound. After a completed command, restore the session deadline so
  the background reader does not kill a healthy idle session after 20 seconds.
- Cancellation closes the owned underlying socket, not `tls.Conn.Close`'s
  best-effort close-notify exchange (which has its own five-second write bound).
  It does not issue an IMAP shutdown command. The active FETCH consumer closes
  the socket before draining/releasing the command on refusal: awaiting client
  shutdown before releasing the literal consumer can deadlock the decoder.
- Header payloads: 256 KiB; message/literal payloads: 10 MiB. Control ingress:
  1 MiB per operation/idle budget, FETCH ingress: payload limit + 1 MiB overhead,
  cumulative session ingress: 256 MiB. Existing library-buffered bytes can cross
  a budget reset; framing and cumulative counters are never reset with it.
- Physical protocol lines: 64 KiB, excluding literal octets. A constant-stack
  pre-decoder guard limits FETCH structure to 32 levels. It handles quotes,
  escapes, literal lengths and network fragmentation; arbitrary status text is
  not structure and cannot hide subsequent replies using a `{length}` suffix.
  Unnumbered FETCH and unresolved prefixes over 64 bytes are refused. Other
  library response paths retain their own limits (for example List depth 1000).
  This is not a universal heap, parser or goroutine bound, or a library sandbox.
- UID/UIDVALIDITY use valid protocol uint32 values. Search filters below-start
  results and returns sorted unique UIDs. Before expanding compressed ESEARCH
  ranges, count them with uint64 and reject dynamic or more than 100,000 UIDs.
  The wire-line bound can constrain actual returned UID count further.
- Require exact UID, requested body section, non-duplicated expected data items
  and successful tagged completion. Headers require RFC822.SIZE. Body content
  remains byte-exact; do not assume size metadata proves content completeness.
- The library assumes UID precedes a FETCH literal. Reversed order is refused
  as message missing, never credited as a successful capture. This differs from
  the Python tuple scanner and remains an activation compatibility limit.
- ESEARCH without an ALL UID set, including `UID COUNT 0`, is explicitly refused
  as protocol failure. Its public library projection loses some field-presence
  information; nil must not silently mean a verified empty result. Ordinary
  empty SEARCH succeeds. Resolve/characterize this supported-provider question
  before widening capability claims; a failed search must not advance a cursor.

Errors expose only fixed `EMAIL_*` codes, not wrapped provider text, parser
diagnostics, TLS errors or credentials. Explicit LOGIN NO/BAD remains AUTH_FAILED;
other tagged command refusals remain PROTOCOL_FAILED. Network timeout/cancellation
during LOGIN is no longer mislabeled a rejected password: network failure uses
NETWORK_FAILED, explicit context cancellation uses CANCELED. Oversized responses
use RESPONSE_TOO_LARGE; malformed framing uses PROTOCOL_FAILED. New categories
are pilot-only until their diagnostics/UI contract is integrated and tested.

Other explicit stricter deltas: credential transport bounds of 320/4096 UTF-8
bytes, no NUL/CR/LF; PREAUTH refusal rather than bypassing requested account LOGIN;
uint32 identities; bounded wire/header/search inputs; absolute cancelable
deadlines. Python's coordinator checked its 120-second budget only between
messages and did not provide an IMAP cancellation hook. Date formatting uses
the protocol's English month names, not Python's process locale. Source command
intent is preserved even though quoting and zero-padding differ on the wire.

## Validation and remaining scope

Run with pinned Go 1.27.1 and the documented offline cache environment:

```text
go test -buildvcs=false -mod=readonly -count=1 -timeout=60s -cover ./internal/mailtransport
go vet -buildvcs=false -mod=readonly ./internal/mailtransport
go test -buildvcs=false -mod=readonly -run=^$ -fuzz=^FuzzFrameGuardChunking$ -fuzztime=5s -parallel=2 ./internal/mailtransport
```

Tests use only synthetic account strings and bytes. No native credentials,
existing Data Home, remote provider or email write is needed. Static source
characterization plus native fake-server tests are not a real Gmail acceptance
test or a whole-application security scan. Evidence and review findings/closure
are recorded in `docs/history/migration/progress.md`.

MIG-170 still owns MIME parsing, mixed email routing, sender policies, receipts,
UIDVALIDITY resets, lease/cursor fairness, durable dedup, backoff, diagnostics and
per-message partial-success accounting. This adapter does not claim those exist
in Go. Dedicated-mailbox acceptance stays on the user checklist, not in automated
tests requiring a real App Password. macOS/Linux native transport runs, race
testing and the installed build remain separate verification gates.
