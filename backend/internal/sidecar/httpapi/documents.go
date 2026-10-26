package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/storage/documents/ledger"
	"careerhub.local/backend/internal/textcompat"
)

// Documents returns an owned, authenticated metadata listing, not blob contents
// or model context. The trusted callback must enforce the 20 MiB source-ledger
// bound and must not mutate the returned records during encoding.
type Documents func(context.Context, bool) (*ledger.Listing, error)

// JSON with HTML escaping disabled expands valid source JSON by at most 2x
// (literal U+2028/U+2029 use 6 bytes instead of 3). The extra 128 covers replacing
// version with the ciphertext revision and the encoder newline. This separate
// limit admits the source's 20 MiB ledgers without increasing other routes' 4 MiB
// limit. It is a serialized-output bound, not a peak-memory or hostile-callback
// guarantee.
const MaxDocumentResponseBytes = 2*ledger.MaxDocumentBytes + 128

func (h *handler) documentsJSON(ctx context.Context, includeArchived bool) (body []byte, code string, status int) {
	defer func() {
		if recover() != nil {
			body, code, status = nil, "UNEXPECTED_ERROR", http.StatusInternalServerError
		}
	}()
	if ctx == nil || ctx.Err() != nil {
		return nil, "DOCUMENT_READ_CANCELED", http.StatusInternalServerError
	}
	payload, err := h.documents(ctx, includeArchived)
	if ctx.Err() != nil {
		return nil, "DOCUMENT_READ_CANCELED", http.StatusInternalServerError
	}
	if err != nil {
		var typed *readonly.Error
		if errors.As(err, &typed) && typed != nil {
			switch typed.Code {
			case "DOCUMENT_STORAGE_INVALID", "DOCUMENT_FILE_INVALID", "DOCUMENT_MIGRATION_REQUIRED",
				"DOCUMENT_KEY_STATE_MISSING", "DOCUMENT_KEY_UNAVAILABLE", "DOCUMENT_ENCRYPTION_INVALID",
				"DOCUMENT_ENCRYPTION_UNAVAILABLE", "DOCUMENT_ENCRYPTION_FAILED", "DOCUMENT_LEDGER_INVALID":
				return nil, typed.Code, http.StatusBadRequest
			case "DOCUMENT_STORAGE_UNSUPPORTED", "DOCUMENT_STORAGE_LIMIT", "DOCUMENT_READ_CANCELED", "DOCUMENT_LEDGER_INPUT_UNSUPPORTED":
				return nil, typed.Code, http.StatusInternalServerError
			}
		}
		return nil, "UNEXPECTED_ERROR", http.StatusInternalServerError
	}
	if payload == nil || payload.Documents == nil {
		return nil, "DOCUMENT_RESPONSE_UNSUPPORTED", http.StatusInternalServerError
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false) // application/json + nosniff; never embedded in HTML.
	if encoder.Encode(payload) != nil {
		return nil, "DOCUMENT_RESPONSE_UNSUPPORTED", http.StatusInternalServerError
	}
	if encoded.Len() > MaxDocumentResponseBytes {
		return nil, "DOCUMENT_RESPONSE_LIMIT", http.StatusInternalServerError
	}
	if ctx.Err() != nil {
		return nil, "DOCUMENT_READ_CANCELED", http.StatusInternalServerError
	}
	return encoded.Bytes(), "", http.StatusOK
}

// Preserve only the existing include_archived selector, not a general query API.
// Python parse_qs drops blanks, keeps the first remaining duplicate, tolerates
// malformed percent escapes and splits on & (not ;). Other fields cannot select
// a file, home, key namespace or projection. The handler already bounds the URI.
func documentArchiveQuery(raw string) bool {
	for _, field := range strings.Split(raw, "&") {
		name, value, hasValue := strings.Cut(field, "=")
		if !hasValue || value == "" || documentQueryBytes(name) != "include_archived" {
			continue
		}
		value = textcompat.Fold(documentQueryBytes(value))
		return value == "1" || value == "true"
	}
	return true
}

// Decode percent bytes permissively. Invalid UTF-8 cannot match the ASCII key
// or accepted values, so its exact replacement grouping is immaterial here.
func documentQueryBytes(raw string) string {
	var decoded strings.Builder
	decoded.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] == '+' {
			decoded.WriteByte(' ')
		} else if raw[i] == '%' && i+2 < len(raw) {
			hi, hiOK := queryHex(raw[i+1])
			lo, loOK := queryHex(raw[i+2])
			if hiOK && loOK {
				decoded.WriteByte(hi<<4 | lo)
				i += 2
			} else {
				decoded.WriteByte(raw[i])
			}
		} else {
			decoded.WriteByte(raw[i])
		}
	}
	return decoded.String()
}

func queryHex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}
