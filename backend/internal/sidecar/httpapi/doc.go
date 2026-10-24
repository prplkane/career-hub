// Package httpapi is the authenticated loopback HTTP boundary of the sidecar.
// It owns no listener, filesystem, runtime configuration or logs; the runtime
// supplies the listener, timeouts and shutdown.
//
// New(port, appVersion, registry, vault, documents) serves the fixed read
// routes: /api/health, /api/session, /api/skills-registry, the seven
// /api/vault/* views and /api/documents. NewWithMaterials adds the owned
// material routes under /api/jobs/<job>/materials/{resume,cover-letter}
// (draft, reviews, exports, file, legacy-file); NewApplication composes the
// first-party frontend and the remaining owners. Callbacks are required and
// trusted; a missing callback, invalid port or version, or a failed random
// read refuses construction.
//
// Admission order is Host, then route-specific rules or capability, then
// browser Origin, then mutation fetch metadata. Host must be 127.0.0.1,
// localhost or [::1] with the exact configured port; Origin, when present,
// must exactly match one of the three http:// loopback origins. The
// capability is 32 random bytes in unpadded URL-safe base64, compared in
// constant time, held only by the handler and returned by no route, error,
// log or header. Request targets are limited to 8192 bytes; absolute-form
// URLs, raw encoded paths, fragments and alternate spellings never select a
// route and get a fixed 404 after authorization. Duplicated Origin,
// Sec-Fetch-Site, capability or Host headers are rejected. OPTIONS grants no
// CORS, paths are never redirected and no HEAD alias exists.
//
// Responses carry JSON content type, Content-Length, no-store, nosniff and
// the fixed Content-Security-Policy. Encoded responses are capped at 4 MiB,
// the document listing at 2*20 MiB + 128 bytes and material payloads at
// 8 MiB; these are transport limits, not heap bounds. Registry, Vault and
// document callbacks share one serialization lock. Callback errors, nil
// payloads and recovered panics return a fixed UNEXPECTED_ERROR; known domain
// failures keep their allowlisted codes with fixed guidance. No dynamic
// message, native cause, path or token is reflected; a failed write is never
// retried. Material POST bodies require application/json, a positive
// Content-Length and at most 1 MiB, with unknown outer fields and non-integer
// revisions refused; callbacks run under a 20-second context with a 25-second
// write deadline, and error envelopes carry operation_state facts because a
// disconnect is not evidence that publication did not occur. Downloads verify
// job, kind, artifact ID, filename, format, byte count and SHA-256 before
// headers are committed and label provenance in X-Career-Hub-Provenance.
package httpapi
