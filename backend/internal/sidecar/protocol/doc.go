// Package protocol implements the bounded sidecar v1 stdio records. It does
// not open files, discover a Data Home, start listeners, or own processes.
//
// ReadRecord reads exactly one LF-terminated record (optional preceding CR)
// from a byte stream without consuming the next one: at most 8194 bytes are
// read and the payload is at most 8192 bytes. ParseStart and ParseShutdown
// enforce the fixed field sets in a fixed order: fields, protocol version,
// command, port, channel, resource path, data path. Channels are exactly dev,
// beta and stable. ParseRecoveryMaintenance and
// ParseDocumentMigrationMaintenance keep the maintenance field sets, and the
// maintenance record helpers emit only content-minimized fields without
// paths, identities or receipts. ReadyRecord, HealthRecord and ErrorRecord
// build the outbound records; EncodeRecord emits compact, key-sorted,
// ASCII-escaped JSON plus one LF, and WriteRecord flushes when the writer
// supports it.
//
// Decoding rules: duplicate keys are last-wins, including replacing a nested
// object; integer tokens become json.Number and decimal or exponent tokens
// float64; NaN, Infinity and float overflow are read but fail version and
// port validation. Paths are at most 4096 code points before expansion, must
// be absolute, and may not be empty or contain NUL. PathOptions selects
// Windows or POSIX semantics; separators and "." are normalized but ".." is
// preserved, and a leading ~ or ~user expands only through an injected
// resolver.
//
// Error.Code is a stable SIDECAR_* code. I/O errors are returned unchanged
// and must be mapped to content-free process errors by the caller; raw I/O
// errors or input records are never written to the protocol stream. Inputs
// the package cannot represent faithfully (escaped lone surrogates, more than
// 4300 integer digits, more than 900 nested containers, output nesting beyond
// 1000, non-JSON output values) return *CompatibilityError with a fixed
// reason.
package protocol
