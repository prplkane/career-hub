// Package envelope implements Career Hub's version-1 authenticated document
// envelope with standard AES-256-GCM. It performs no filesystem,
// credential-store or network operation and never creates keys.
//
// Layout: a big-endian prefix of the 6-byte magic "CHDOC\x00", one version
// byte and a uint32 header length; a canonical JSON header; a fresh 12-byte
// nonce; and the ciphertext with its 16-byte tag. Prefix plus header is the
// authenticated additional data. Headers are bounded to 4,096 bytes and
// plaintext to 20 MiB. No nonce injection is exposed.
//
// Encrypt and Decrypt take the caller's 32-byte key and a validated object
// context. Inspect returns unauthenticated metadata only; never derive an
// expected decryption context from it. A failed decryption returns a zero
// result, never partial plaintext. Input slices stay caller-owned and must
// not change during a call.
//
// The header is validated by canonical byte equality after decoding, so
// unknown, missing, duplicate, case-variant, reordered or escaped fields and
// non-canonical whitespace are rejected. Header values are ASCII opaque IDs
// and fixed format and purpose values; a purpose that is not a string is
// rejected with DOCUMENT_ENVELOPE_INVALID. Typed *Error values carry the
// stable code and a content-minimized message.
package envelope
