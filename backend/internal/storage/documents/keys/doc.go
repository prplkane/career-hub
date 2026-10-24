// Package keys implements document-key state and the injected key-store
// operations. It has no filesystem, network or native credential adapter and
// never logs.
//
// State holds active_key_id, algorithm, data_home_id and version.
// CanonicalBytes produces sorted compact JSON with ASCII escaping and exactly
// one final LF; it serializes but does not validate. ParseState accepts at
// most 4,096 bytes and requires exact canonical equality: version 1,
// algorithm AES-256-GCM, lowercase identifiers dh_[0-9a-f]{32} and
// key_[0-9a-f]{32}, no unknown, missing, duplicate or wrong-case fields, and
// no alternate escaping, order or whitespace.
//
// NewManager(channel, backend, random) requires an explicit Backend and a
// channel of dev, beta or stable; a nil random function uses crypto/rand and
// there is no default native backend. Credentials are addressed as
// CareerHub/{channel}/document-key/{data_home_id}/{key_id}. Create validates
// the home, draws 16 ID bytes and then 32 key bytes, checks both lengths
// after both calls, runs the optional before-write callback and then writes;
// the first failure stops the sequence, a callback failure prevents the
// write, and an identical existing target is overwritten without lookup,
// retry or rollback. Load validates state and returns an independently owned
// key; LoadKey reads one explicitly requested retained key and never falls
// back to another key, channel, home or a new key. Backend.Read reports
// found separately from bytes so absent and empty remain distinct.
// DiscardUnpublished deletes exactly the validated target, treats absence as
// success and must not be exposed as general deletion.
//
// All ID and key bytes are copied before callbacks, writes and returns, so
// no caller buffer aliases another owner. No key cache is kept, and garbage
// collection is not secret zeroization.
//
// *Error carries DOCUMENT_KEY_STATE_INVALID, DOCUMENT_KEY_UNAVAILABLE,
// DOCUMENT_KEY_INVALID, DOCUMENT_KEY_GENERATION_FAILED, INVALID_CHANNEL or
// the configuration refusal DOCUMENT_KEY_BACKEND_REQUIRED. RNG, callback and
// backend failures become *OperationError with a fixed message and the
// operation name; the private cause is reachable through Unwrap for typed
// inspection and must never be logged.
package keys
