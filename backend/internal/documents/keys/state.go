// Package keys implements nonsecret document-key state and injected key-store
// operations. It has no filesystem, network, or native credential-store adapter.
package keys

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

const (
	Version       = 1
	Algorithm     = "AES-256-GCM"
	KeyBytes      = 32
	MaxStateBytes = 4096

	CodeStateInvalid     = "DOCUMENT_KEY_STATE_INVALID"
	CodeUnavailable      = "DOCUMENT_KEY_UNAVAILABLE"
	CodeKeyInvalid       = "DOCUMENT_KEY_INVALID"
	CodeGenerationFailed = "DOCUMENT_KEY_GENERATION_FAILED"
	CodeInvalidChannel   = "INVALID_CHANNEL"
	// CodeBackendRequired is a pilot configuration error, not a Python domain error.
	CodeBackendRequired = "DOCUMENT_KEY_BACKEND_REQUIRED"
)

// State contains identifiers, never a key. Fields are in canonical JSON order.
// Its typed boundary excludes arbitrary Python objects and non-integer versions.
type State struct {
	ActiveKeyID string `json:"active_key_id"`
	Algorithm   string `json:"algorithm"`
	DataHomeID  string `json:"data_home_id"`
	Version     int    `json:"version"`
}

// Error contains a source-compatible code and fixed content-minimized prose.
type Error struct{ Code string }

func (e *Error) Error() string {
	if e != nil {
		switch e.Code {
		case CodeUnavailable:
			return "The document encryption key is unavailable. Restore access using the original account and device."
		case CodeKeyInvalid:
			return "The stored document encryption key is invalid."
		case CodeGenerationFailed:
			return "Secure random key generation failed."
		case CodeInvalidChannel:
			return "Unsupported Career Hub release channel."
		case CodeBackendRequired:
			return "An explicit document credential backend is required."
		}
	}
	return "Document key state is invalid."
}

// Format prevents diagnostic formatting from exposing internal representations.
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }

func invalidState() error { return &Error{Code: CodeStateInvalid} }

func validID(value, prefix string) bool {
	if len(value) != len(prefix)+32 || value[:len(prefix)] != prefix {
		return false
	}
	for _, c := range value[len(prefix):] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func validateIdentity(homeID, keyID string) error {
	if !validID(homeID, "dh_") || !validID(keyID, "key_") {
		return invalidState()
	}
	return nil
}

func (s State) validate() error {
	if s.Version != Version || s.Algorithm != Algorithm {
		return invalidState()
	}
	return validateIdentity(s.DataHomeID, s.ActiveKeyID)
}

// CanonicalBytes mirrors the Python dataclass serializer: compact sorted JSON,
// ensure_ascii=True, and one LF. It does not validate the semantic state fields.
// Strings must be scalar UTF-8; arbitrary Python objects/lone surrogates are not
// part of this typed API. ParseState and manager operations validate the state.
func (s State) CanonicalBytes() ([]byte, error) {
	if !utf8.ValidString(s.ActiveKeyID) || !utf8.ValidString(s.Algorithm) || !utf8.ValidString(s.DataHomeID) {
		return nil, invalidState()
	}
	data := []byte(`{"active_key_id":`)
	data = appendASCIIString(data, s.ActiveKeyID)
	data = append(data, `,"algorithm":`...)
	data = appendASCIIString(data, s.Algorithm)
	data = append(data, `,"data_home_id":`...)
	data = appendASCIIString(data, s.DataHomeID)
	data = append(data, `,"version":`...)
	data = strconv.AppendInt(data, int64(s.Version), 10)
	return append(data, '}', '\n'), nil
}

// appendASCIIString reproduces Python json.dumps(ensure_ascii=True) for Unicode
// scalar strings. Go's default JSON encoder differs for HTML, DEL and non-ASCII.
func appendASCIIString(dst []byte, value string) []byte {
	dst = append(dst, '"')
	for _, r := range value {
		switch r {
		case '"', '\\':
			dst = append(dst, '\\', byte(r))
		case '\b':
			dst = append(dst, `\b`...)
		case '\f':
			dst = append(dst, `\f`...)
		case '\n':
			dst = append(dst, `\n`...)
		case '\r':
			dst = append(dst, `\r`...)
		case '\t':
			dst = append(dst, `\t`...)
		default:
			if r >= 0x20 && r <= 0x7e {
				dst = append(dst, byte(r))
			} else if r <= 0xffff {
				dst = appendHexEscape(dst, uint16(r))
			} else {
				r -= 0x10000
				dst = appendHexEscape(dst, uint16(0xd800+(r>>10)))
				dst = appendHexEscape(dst, uint16(0xdc00+(r&0x3ff)))
			}
		}
	}
	return append(dst, '"')
}

func appendHexEscape(dst []byte, value uint16) []byte {
	const digits = "0123456789abcdef"
	return append(dst, '\\', 'u', digits[value>>12], digits[(value>>8)&15], digits[(value>>4)&15], digits[value&15])
}

// ParseState accepts only the exact canonical, bounded, nonsecret state bytes.
// Equality after decoding also rejects duplicate/unknown/wrong-case fields,
// alternate escaping, field order, and all noncanonical whitespace.
func ParseState(content []byte) (State, error) {
	if len(content) > MaxStateBytes || !utf8.Valid(content) {
		return State{}, invalidState()
	}
	var state State
	if err := json.Unmarshal(content, &state); err != nil {
		return State{}, invalidState()
	}
	if err := state.validate(); err != nil {
		return State{}, err
	}
	canonical, err := state.CanonicalBytes()
	if err != nil || !bytes.Equal(content, canonical) {
		return State{}, invalidState()
	}
	return state, nil
}
