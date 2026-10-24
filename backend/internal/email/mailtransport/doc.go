// Package mailtransport implements the read-only Gmail IMAP transport. It has
// no credential store, persistence, MIME parser or sync coordinator.
//
// Open(ctx, account, password, folder) connects only to imap.gmail.com:993
// over verified implicit TLS (minimum 1.2), then LOGIN and EXAMINE. There is
// no plaintext or STARTTLS fallback, proxy, URL fetch or other provider; the
// account and password are caller supplied and never stored or enumerated.
// The facade exposes UIDVALIDITY, UID search, header/body PEEK and close
// only, never STORE, APPEND, MOVE, COPY, writable SELECT, EXPUNGE or CLOSE,
// so reading mail cannot mark a message read or create a job or application.
//
// Bounds: commands are serialized, each with a 20-second absolute deadline,
// and a session has a 120-second ceiling inherited from the opening context;
// cancelling an active command closes the whole session. Header payloads are
// capped at 256 KiB and message literals at 10 MiB; control ingress at 1 MiB
// per operation, cumulative session ingress at 256 MiB, protocol lines at
// 64 KiB and FETCH structure at 32 nesting levels. Search results are sorted
// unique UIDs; compressed ESEARCH ranges are counted before expansion and
// refused above 100,000 UIDs. A FETCH reply must carry the exact UID, the
// requested body section and RFC822.SIZE; a literal that precedes its UID is
// refused as missing, never credited as a capture. ESEARCH without an ALL set
// is a protocol failure and must not advance a cursor.
//
// Errors expose only fixed EMAIL_* codes (AUTH_FAILED for an explicit LOGIN
// refusal, PROTOCOL_FAILED, NETWORK_FAILED, CANCELED, RESPONSE_TOO_LARGE); no
// provider text, parser diagnostic, TLS detail or credential is retained.
// Credentials are bounded to 320/4096 UTF-8 bytes with no NUL, CR or LF, and
// a PREAUTH greeting is refused rather than bypassing the requested LOGIN.
package mailtransport
