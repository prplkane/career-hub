// Package ledger validates decrypted document-index metadata and supplies its
// two permitted read projections. It performs no I/O, key lookup or decryption.
// Successful parsing does not imply authentication; callers must authenticate
// the encrypted ledger and validate storage membership before publishing it.
package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/storage/documents/envelope"
	"careerhub.local/backend/internal/textcompat"
	"careerhub.local/backend/internal/vault/calendarinput"
)

const (
	CodeInvalid      = "DOCUMENT_LEDGER_INVALID"
	CodeUnsupported  = "DOCUMENT_LEDGER_INPUT_UNSUPPORTED"
	MaxDocumentBytes = envelope.MaxObjectBytes
)

// Error never retains input, field names, parser exceptions, paths or secrets.
type Error struct{ Code string }

func (e *Error) Error() string {
	if e != nil && e.Code == CodeUnsupported {
		return "Document ledger contains unsupported text."
	}
	return "Encrypted document ledger is invalid."
}
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func invalid() *Error                       { return &Error{Code: CodeInvalid} }

// Document preserves the stored scalar values, not upload/editor cleanup.
// Do not put this metadata structure in model context; use Titles instead.
type Document struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Category    string `json:"category"`
	IssuedOn    string `json:"issued_on"`
	ExpiresOn   string `json:"expires_on"`
	Notes       string `json:"notes"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int    `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	Archived    bool   `json:"archived"`
}

type Listing struct {
	Revision  string     `json:"revision"`
	Documents []Document `json:"documents"`
}

// Parse validates the exact v1 JSON schema. Duplicate keys at any potentially
// valid object level are rejected. Nested objects/arrays in scalar fields are
// invalid regardless of their contents. Records remain in source order.
// Input is byte-bounded before decoding and must not be mutated concurrently.
func Parse(content []byte) ([]Document, *Error) {
	if len(content) == 0 || len(content) > envelope.MaxObjectBytes || !utf8.Valid(content) {
		return nil, invalid()
	}
	root, e := object(content, 2)
	if e != nil || !integer(root["version"], 1, 1) {
		return nil, invalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(root["documents"]))
	if !delimiter(decoder, '[') {
		return nil, invalid()
	}
	documents := []Document{}
	seen := make(map[string]bool)
	for decoder.More() {
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return nil, invalid()
		}
		document, e := parseDocument(raw)
		if e != nil {
			return nil, e
		}
		if seen[document.ID] {
			return nil, invalid()
		}
		seen[document.ID] = true
		documents = append(documents, document)
	}
	if !delimiter(decoder, ']') || !finished(decoder) {
		return nil, invalid()
	}
	return documents, nil
}

func parseDocument(raw []byte) (Document, *Error) {
	fields, e := object(raw, 13)
	if e != nil {
		return Document{}, e
	}
	var d Document
	for _, field := range []struct {
		name     string
		target   *string
		min, max int
	}{
		{"id", &d.ID, 36, 36}, {"title", &d.Title, 1, 500},
		{"category", &d.Category, 1, 120}, {"notes", &d.Notes, 0, 5000},
		{"issued_on", &d.IssuedOn, 0, 10}, {"expires_on", &d.ExpiresOn, 0, 10},
		{"filename", &d.Filename, 1, 255}, {"content_type", &d.ContentType, 1, 64},
		{"sha256", &d.SHA256, 64, 64}, {"created_at", &d.CreatedAt, 1, 64},
		{"updated_at", &d.UpdatedAt, 1, 64},
	} {
		value, e := scalarString(fields[field.name], field.min, field.max)
		if e != nil {
			return Document{}, e
		}
		*field.target = value
	}
	if !strings.HasPrefix(d.ID, "doc_") || !lowerHex(d.ID[4:], 32) ||
		!storedDate(d.IssuedOn) || !storedDate(d.ExpiresOn) ||
		(d.IssuedOn != "" && d.ExpiresOn != "" && d.ExpiresOn < d.IssuedOn) ||
		!metadataFilename(d.Filename, runtime.GOOS == "windows") ||
		(d.ContentType != "application/pdf" && d.ContentType != "image/png" && d.ContentType != "image/jpeg") ||
		!integer(fields["size_bytes"], 1, MaxDocumentBytes) || !lowerHex(d.SHA256, 64) ||
		!calendarinput.ValidAwareISODateTime(d.CreatedAt) || !calendarinput.ValidAwareISODateTime(d.UpdatedAt) {
		return Document{}, invalid()
	}
	// Lexical integer validation above excludes bool, float/exponent and overflow.
	d.SizeBytes, _ = strconv.Atoi(string(fields["size_bytes"]))
	archived := string(fields["archived"])
	if archived != "true" && archived != "false" {
		return Document{}, invalid()
	}
	d.Archived = archived == "true"
	return d, nil
}

func delimiter(decoder *json.Decoder, want json.Delim) bool {
	token, e := decoder.Token()
	return e == nil && token == want
}
func finished(decoder *json.Decoder) bool {
	var extra json.RawMessage
	return decoder.Decode(&extra) == io.EOF
}
func object(raw []byte, count int) (map[string]json.RawMessage, *Error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if !delimiter(decoder, '{') {
		return nil, invalid()
	}
	fields := make(map[string]json.RawMessage, count)
	for decoder.More() {
		token, e := decoder.Token()
		name, ok := token.(string)
		if e != nil || !ok || fields[name] != nil || len(fields) == count {
			return nil, invalid()
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, invalid()
		}
		fields[name] = value
	}
	if len(fields) != count || !delimiter(decoder, '}') || !finished(decoder) {
		return nil, invalid()
	}
	return fields, nil
}

func integer(raw []byte, min, max int) bool {
	if len(raw) == 0 || bytes.ContainsAny(raw, ".eE") {
		return false
	}
	n, e := strconv.Atoi(string(raw))
	return e == nil && n >= min && n <= max
}
func lowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
func storedDate(value string) bool {
	if value == "" {
		return true
	}
	if len(value) != 10 || value[4] != '-' || value[7] != '-' {
		return false
	}
	return calendarinput.ValidISODate(value)
}

// Metadata-only Path(name).name equality, not filesystem admission. Python
// accepts reserved names and ".." here; the blob's actual path uses only ID.
// Windows and POSIX source behavior differ for backslashes and drive prefixes.
func metadataFilename(value string, windows bool) bool {
	if value == "" || value == "." || strings.ContainsRune(value, '/') {
		return false
	}
	for _, r := range value {
		if r < 32 {
			return false
		}
	}
	if windows {
		if strings.ContainsRune(value, '\\') {
			return false
		}
		_, size := utf8.DecodeRuneInString(value)
		if size < len(value) && value[size] == ':' {
			return false
		}
	}
	return true
}

// Metadata returns owned records; nil input serializes as [], not null. The
// revision is of the original ciphertext (nil and empty have the same digest).
func Metadata(documents []Document, encrypted []byte, includeArchived bool) Listing {
	hash := sha256.Sum256(encrypted)
	result := Listing{Revision: "sha256:" + hex.EncodeToString(hash[:]), Documents: []Document{}}
	for _, document := range documents {
		if includeArchived || !document.Archived {
			result.Documents = append(result.Documents, document)
		}
	}
	return result
}

// Titles is the sole model-context projection: active, stripped, exact-deduped
// titles only. Case-fold-equal titles remain distinct. A scalar tie-break makes
// Python's set-iteration sort ties deterministic without changing membership.
func Titles(documents []Document) []string {
	seen := make(map[string]bool)
	titles := []string{}
	for _, document := range documents {
		title := textcompat.Strip(document.Title)
		if !document.Archived && title != "" && !seen[title] {
			seen[title] = true
			titles = append(titles, title)
		}
	}
	folds := make(map[string]string, len(titles))
	for _, title := range titles {
		folds[title] = textcompat.Fold(title)
	}
	sort.Slice(titles, func(i, j int) bool {
		if folds[titles[i]] == folds[titles[j]] {
			return titles[i] < titles[j]
		}
		return folds[titles[i]] < folds[titles[j]]
	})
	return titles
}
