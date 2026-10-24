package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/materials"
	"careerhub.local/backend/internal/materials/materialintegrity"
	"careerhub.local/backend/internal/textcompat"
)

const MaxMaterialRequestBytes = 1024 * 1024 // existing MAX_JSON_BYTES, including envelope

func materialJSONObject(raw []byte) bool {
	value := bytes.TrimSpace(raw)
	return len(value) > 0 && value[0] == '{' && utf8.Valid(value) && json.Valid(value)
}

func materialJSON(r *http.Request) (map[string]json.RawMessage, string, int) {
	return materialJSONChecked(r, nil)
}

// Optional lexical checks run before map decoding can replace Unicode or
// discard duplicate keys. Existing material-domain validation stays unchanged.
func materialJSONChecked(r *http.Request, valid func([]byte) bool) (map[string]json.RawMessage, string, int) {
	return materialJSONCheckedLimit(r, valid, MaxMaterialRequestBytes)
}

// Limits are selected only by fixed server routes, never client input.
func materialJSONCheckedLimit(r *http.Request, valid func([]byte) bool, limit int64) (map[string]json.RawMessage, string, int) {
	contentType, single := oneHeader(r, "Content-Type")
	base, _, _ := strings.Cut(contentType, ";")
	if !single || textcompat.Fold(textcompat.Strip(base)) != "application/json" {
		return nil, "MATERIAL_REQUEST_CONTENT_TYPE", http.StatusUnsupportedMediaType
	}
	_, lengthSingle := oneHeader(r, "Content-Length")
	transfer, transferSingle := oneHeader(r, "Transfer-Encoding")
	if !lengthSingle || !transferSingle || transfer != "" || len(r.TransferEncoding) != 0 || r.ContentLength <= 0 || r.Body == nil {
		return nil, "MATERIAL_REQUEST_INVALID", http.StatusBadRequest
	}
	if r.ContentLength > limit {
		return nil, "MATERIAL_REQUEST_TOO_LARGE", http.StatusRequestEntityTooLarge
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if e != nil || int64(len(raw)) != r.ContentLength {
		return nil, "MATERIAL_REQUEST_INVALID", http.StatusBadRequest
	}
	if !materialJSONObject(raw) || valid != nil && !valid(raw) {
		return nil, "MATERIAL_REQUEST_INVALID", http.StatusBadRequest
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(raw, &payload) != nil {
		return nil, "MATERIAL_REQUEST_INVALID", http.StatusBadRequest
	}
	return payload, "", http.StatusOK
}

func materialKeys(payload map[string]json.RawMessage, allowed ...string) bool {
	for key := range payload {
		found := false
		for _, name := range allowed {
			if key == name {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func materialRevision(raw json.RawMessage) (int64, bool) {
	// Null, booleans, fractions and exponent spelling are not Python integers.
	// Explicit -0 remains accepted; revisions must fit the database's int64.
	v, e := strconv.ParseInt(string(bytes.TrimSpace(raw)), 10, 64)
	return v, e == nil && v >= 0
}

func materialDraftRequest(target materialTarget, payload map[string]json.RawMessage) (materials.Request, bool) {
	r := materials.Request{JobID: target.job, Kind: target.kind, Operation: "save"}
	if !materialKeys(payload, "action", "expected_revision", "draft") {
		return r, false
	}
	var action string
	if json.Unmarshal(payload["action"], &action) != nil || action != "approve" && action != "save_draft" {
		return r, false
	}
	r.Approve = action == "approve"
	var ok bool
	r.ExpectedRevision, ok = materialRevision(payload["expected_revision"])
	if !ok || !materialJSONObject(payload["draft"]) {
		return r, false
	}
	r.Payload = payload["draft"]
	return r, materials.ValidateRequest(r) == nil
}

func materialReviewRequest(target materialTarget, payload map[string]json.RawMessage) (materialintegrity.ReviewRequest, bool) {
	r := materialintegrity.ReviewRequest{JobID: target.job, Kind: target.kind}
	if !materialKeys(payload, "operation", "expected_revision", "review_sha256", "target_ids") {
		return r, false
	}
	if json.Unmarshal(payload["operation"], &r.Operation) != nil || r.Operation != "confirm" && r.Operation != "revoke" {
		return r, false
	}
	var ok bool
	r.ExpectedRevision, ok = materialRevision(payload["expected_revision"])
	if !ok || json.Unmarshal(payload["review_sha256"], &r.ReviewSHA256) != nil || json.Unmarshal(payload["target_ids"], &r.TargetIDs) != nil {
		return r, false
	}
	return r, materialintegrity.ValidateReviewRequest(r) == nil
}

func materialExportFormat(payload map[string]json.RawMessage) (string, bool) {
	if !materialKeys(payload, "format") {
		return "", false
	}
	if raw, ok := payload["format"]; ok {
		var format string
		if json.Unmarshal(raw, &format) != nil {
			return "", false
		}
		format = strings.ToLower(textcompat.Strip(format))
		return format, format == "docx" || format == "pdf" || format == "txt"
	}
	return "pdf", true
}
