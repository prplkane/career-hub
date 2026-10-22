// Package envelope implements Career Hub's version-1 authenticated document
// envelope. It performs no filesystem, credential-store, or network operations.
// Inspect returns untrusted metadata; only Decrypt authenticates it.
package envelope

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

const (
	Algorithm        = "AES-256-GCM"
	Version          = 1
	KeyBytes         = 32
	NonceBytes       = 12
	TagBytes         = 16
	MaxHeaderBytes   = 4096
	MaxObjectBytes   = 20 * 1024 * 1024
	prefixBytes      = 11 // Python struct format >6sBI.
	MaxEnvelopeBytes = prefixBytes + MaxHeaderBytes + NonceBytes + MaxObjectBytes + TagBytes
	magic            = "CHDOC\x00"

	CodeInvalid              = "DOCUMENT_ENVELOPE_INVALID"
	CodeKeyInvalid           = "DOCUMENT_KEY_INVALID"
	CodeSizeInvalid          = "DOCUMENT_SIZE_INVALID"
	CodeContextMismatch      = "DOCUMENT_CONTEXT_MISMATCH"
	CodeAuthenticationFailed = "DOCUMENT_AUTHENTICATION_FAILED"
)

// Context identifies the expected local object, independently of its envelope.
// Do not populate an expected Context from Inspect's untrusted result.
type Context struct {
	DataHomeID string `json:"data_home_id"`
	KeyID      string `json:"key_id"`
	Purpose    string `json:"purpose"`
	ObjectID   string `json:"object_id"`
}

// Header contains only opaque identity and format metadata. Fields are ordered
// lexically by JSON name to reproduce Python's sorted canonical header bytes.
// Every valid string value is ASCII, so JSON escaping cannot change the encoding.
type Header struct {
	Algorithm  string `json:"algorithm"`
	DataHomeID string `json:"data_home_id"`
	KeyID      string `json:"key_id"`
	ObjectID   string `json:"object_id"`
	Purpose    string `json:"purpose"`
	Version    int    `json:"version"`
}

// Decrypted is returned only after the header and plaintext are authenticated.
type Decrypted struct {
	Header    Header
	Plaintext []byte
}

// Error exposes a stable code and a content-minimized message. It never retains
// key bytes, document content, caller-supplied identifiers, or parser errors.
type Error struct {
	Code string
}

func (e *Error) Error() string {
	if e != nil {
		switch e.Code {
		case CodeKeyInvalid:
			return "Document encryption key must be exactly 32 bytes."
		case CodeSizeInvalid:
			return "Document content exceeds the supported encryption size."
		case CodeContextMismatch:
			return "Encrypted document envelope does not match the requested context."
		case CodeAuthenticationFailed:
			return "Encrypted document authentication failed."
		}
	}
	return "Encrypted document envelope is invalid."
}

// Encrypt seals one blob or ledger with a fresh cryptographically random 96-bit
// nonce. The caller supplies the key; this package never creates or stores keys.
// Input slices are not modified. Nil plaintext is an empty document.
func Encrypt(plaintext, key []byte, context Context) ([]byte, error) {
	if len(key) != KeyBytes {
		return nil, &Error{Code: CodeKeyInvalid}
	}
	if len(plaintext) > MaxObjectBytes {
		return nil, &Error{Code: CodeSizeInvalid}
	}
	header, err := validatedHeader(context)
	if err != nil {
		return nil, err
	}
	headerBytes, err := canonicalBytes(header)
	if err != nil || len(headerBytes) > MaxHeaderBytes {
		return nil, &Error{Code: CodeInvalid}
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	headerEnd := prefixBytes + len(headerBytes)
	payloadStart := headerEnd + NonceBytes
	output := make([]byte, payloadStart, payloadStart+len(plaintext)+TagBytes)
	copy(output, magic)
	output[len(magic)] = Version
	binary.BigEndian.PutUint32(output[len(magic)+1:prefixBytes], uint32(len(headerBytes)))
	copy(output[prefixBytes:headerEnd], headerBytes)
	nonce := output[headerEnd:payloadStart]
	if _, err := rand.Read(nonce); err != nil {
		return nil, &Error{Code: CodeInvalid}
	}
	return gcm.Seal(output, nonce, plaintext, output[:headerEnd]), nil
}

// Decrypt checks key and expected context before parsing, then checks the header
// context before authentication, matching the Python reference's error order.
// Failure returns a zero Decrypted value and never exposes partial plaintext.
func Decrypt(data, key []byte, expected Context) (Decrypted, error) {
	if len(key) != KeyBytes {
		return Decrypted{}, &Error{Code: CodeKeyInvalid}
	}
	expectedHeader, err := validatedHeader(expected)
	if err != nil {
		return Decrypted{}, err
	}
	parsed, err := parse(data)
	if err != nil {
		return Decrypted{}, err
	}
	if parsed.header != expectedHeader {
		return Decrypted{}, &Error{Code: CodeContextMismatch}
	}
	gcm, err := newGCM(key)
	if err != nil {
		return Decrypted{}, err
	}
	plaintext, err := gcm.Open(nil, parsed.nonce, parsed.ciphertext, parsed.additionalData)
	if err != nil {
		return Decrypted{}, &Error{Code: CodeAuthenticationFailed}
	}
	return Decrypted{Header: parsed.header, Plaintext: plaintext}, nil
}

// Inspect checks bounded framing and canonical header syntax, not authenticity.
// A successful result does not establish integrity or permission to access data.
func Inspect(data []byte) (Header, error) {
	parsed, err := parse(data)
	if err != nil {
		return Header{}, err
	}
	return parsed.header, nil
}

func validatedHeader(context Context) (Header, error) {
	validObject := context.Purpose == "ledger" && context.ObjectID == "ledger" ||
		context.Purpose == "blob" && opaqueID(context.ObjectID, "doc_")
	if !opaqueID(context.DataHomeID, "dh_") || !opaqueID(context.KeyID, "key_") || !validObject {
		return Header{}, &Error{Code: CodeInvalid}
	}
	return Header{
		Algorithm: Algorithm, Version: Version,
		DataHomeID: context.DataHomeID, KeyID: context.KeyID,
		Purpose: context.Purpose, ObjectID: context.ObjectID,
	}, nil
}

func opaqueID(value, prefix string) bool {
	if len(value) != len(prefix)+32 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for i := len(prefix); i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' && value[i] < 'a' || value[i] > 'f' {
			return false
		}
	}
	return true
}

func canonicalBytes(header Header) ([]byte, error) {
	encoded, err := json.Marshal(header)
	if err != nil {
		return nil, &Error{Code: CodeInvalid}
	}
	return encoded, nil
}

func parseHeader(data []byte) (Header, error) {
	// encoding/json replaces malformed UTF-8 in strings; Python rejects it.
	if !utf8.Valid(data) {
		return Header{}, &Error{Code: CodeInvalid}
	}
	var header Header
	if err := json.Unmarshal(data, &header); err != nil || header.Algorithm != Algorithm || header.Version != Version {
		return Header{}, &Error{Code: CodeInvalid}
	}
	validated, err := validatedHeader(Context{
		DataHomeID: header.DataHomeID, KeyID: header.KeyID,
		Purpose: header.Purpose, ObjectID: header.ObjectID,
	})
	if err != nil {
		return Header{}, err
	}
	canonical, err := canonicalBytes(validated)
	// Equality also rejects duplicate, unknown, missing and case-folded field
	// names, whitespace, reordered fields, escaped spellings and extra values.
	// Parsing into a struct alone would silently accept several of those forms.
	if err != nil || !bytes.Equal(data, canonical) {
		return Header{}, &Error{Code: CodeInvalid}
	}
	return validated, nil
}

type parsedEnvelope struct {
	header         Header
	nonce          []byte
	ciphertext     []byte
	additionalData []byte
}

func parse(data []byte) (parsedEnvelope, error) {
	invalid := func() (parsedEnvelope, error) { return parsedEnvelope{}, &Error{Code: CodeInvalid} }
	if len(data) < prefixBytes+NonceBytes+TagBytes || len(data) > MaxEnvelopeBytes {
		return invalid()
	}
	if string(data[:len(magic)]) != magic || data[len(magic)] != Version {
		return invalid()
	}
	headerLength := binary.BigEndian.Uint32(data[len(magic)+1 : prefixBytes])
	if headerLength < 2 || headerLength > MaxHeaderBytes {
		return invalid()
	}
	headerEnd := prefixBytes + int(headerLength) // Bounded above before conversion.
	payloadStart := headerEnd + NonceBytes
	if payloadStart+TagBytes > len(data) || len(data)-payloadStart-TagBytes > MaxObjectBytes {
		return invalid()
	}
	header, err := parseHeader(data[prefixBytes:headerEnd])
	if err != nil {
		return parsedEnvelope{}, err
	}
	return parsedEnvelope{
		header: header, nonce: data[headerEnd:payloadStart],
		ciphertext: data[payloadStart:], additionalData: data[:headerEnd],
	}, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, &Error{Code: CodeKeyInvalid}
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, &Error{Code: CodeInvalid}
	}
	return gcm, nil
}
