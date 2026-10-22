package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"careerhub.local/backend/internal/materialintegrity"
	"careerhub.local/backend/internal/materialwrite"
)

func materialError(e *materialwrite.Error) (string, int) {
	switch e.Code {
	case "MATERIAL_REVISION_CONFLICT":
		return "APPLICATION_MATERIAL_REVISION_CONFLICT", http.StatusConflict
	case "MATERIAL_EXPORT_FACTS_BLOCKED", "MATERIAL_EXPORT_HARD_PAGE_LIMIT", "MATERIAL_ARTIFACT_SOURCES_STALE",
		"MATERIAL_SOURCE_CHANGED", "MATERIAL_REVIEW_STALE", "MATERIAL_REVIEW_REVISION_CONFLICT", "MATERIAL_REVIEW_TARGET_STALE",
		"MATERIAL_EXPORT_RECEIPT_REQUIRED", "MATERIAL_EXPORT_INCOMPLETE", "MATERIAL_MANIFEST_CHANGED", "MATERIAL_ARTIFACT_CHANGED",
		"MATERIAL_FACT_DRAFT_UNSAVED", "MATERIAL_FACT_DRAFT_NOT_READY", "MATERIAL_LEGACY_EXPORT_UNAVAILABLE":
		return e.Code, http.StatusConflict
	case "MATERIAL_RENDER_FORMAT_UNSUPPORTED", "MATERIAL_EXPORT_REQUEST_INVALID", "MATERIAL_LEGACY_EXPORT_NAME_INVALID":
		return e.Code, http.StatusBadRequest
	case "MATERIAL_WORKSPACE_UNAVAILABLE", "MATERIAL_WORKSPACE_CHANGED", "DATA_HOME_OWNERSHIP_REQUIRED":
		return "MATERIAL_WORKSPACE_UNAVAILABLE", http.StatusServiceUnavailable
	case "MATERIAL_CANCELED", "MATERIAL_INTEGRITY_CANCELED", "MATERIAL_RENDER_CANCELLED":
		return "MATERIAL_REQUEST_CANCELED", http.StatusRequestTimeout
	}
	// Do not reflect an unrecognized internal code, stage, native path or cause.
	// Known domain classes retain actionable category guidance, not raw prose.
	if e.SourceClass == "ApplicationMaterialError" {
		switch e.Stage {
		case "validation", "draft", "input", "editor", "source", "request":
			return "APPLICATION_MATERIAL_INVALID", http.StatusConflict
		case "integrity", "receipt":
			return "MATERIAL_INTEGRITY_FAILED", http.StatusConflict
		case "render":
			return "MATERIAL_RENDER_FAILED", http.StatusConflict
		}
	}
	return "APPLICATION_MATERIAL_OPERATION_FAILED", http.StatusInternalServerError
}

func materialInputError(w http.ResponseWriter, code string, status int) {
	materialFailure(w, &materialwrite.Outcome{}, nil, code, status)
}

// Receipt/draft publication may have happened before a late failure. Unknown
// (e.g. callback panic) is not false. Never fabricate a diagnostic run ID or
// suggest a log entry was recorded by this transport-only boundary.
func materialFailure(w http.ResponseWriter, out *materialwrite.Outcome, _ *materialwrite.Error, code string, status int) {
	state := map[string]any{"known": out != nil, "committed": nil, "partial": nil, "cleanup_failed": nil}
	if out != nil {
		state["committed"], state["partial"], state["cleanup_failed"] = out.Committed, out.Partial, out.CleanupFailed
	}
	summary := "Application materials could not be completed."
	suggestion := "Check the saved draft and current sources. A failed response does not prove that nothing was saved; reload before changing or retrying it."
	switch code {
	case "MATERIAL_EXPORT_FACTS_BLOCKED":
		suggestion = "Review the configured-token findings. Correct the draft or explicitly confirm only the supported manual-review targets."
	case "MATERIAL_EXPORT_HARD_PAGE_LIMIT":
		suggestion = "Shorten the resume to the supported two-page limit before exporting."
	case "MATERIAL_ARTIFACT_SOURCES_STALE", "MATERIAL_SOURCE_CHANGED", "APPLICATION_MATERIAL_REVISION_CONFLICT":
		suggestion = "Reload the current draft and sources, review the changes, then save or export again."
	case "MATERIAL_EXPORT_RECEIPT_REQUIRED", "MATERIAL_EXPORT_INCOMPLETE", "MATERIAL_MANIFEST_CHANGED", "MATERIAL_ARTIFACT_CHANGED", "MATERIAL_INTEGRITY_FAILED":
		suggestion = "The export could not be verified. Do not use it as a verified document; an exact export retry can finish an intact preparation but will not overwrite corruption."
	case "MATERIAL_REQUEST_INVALID", "MATERIAL_REQUEST_CONTENT_TYPE", "MATERIAL_REQUEST_TOO_LARGE":
		suggestion = "Send a bounded application/json request with the documented fields and a non-negative integer revision."
	}
	body, _ := json.Marshal(map[string]any{"error": summary, "technical_message": "The material operation did not return a verified result.", "error_code": code, "summary": summary, "suggestion": suggestion, "severity": "error", "retryable": false, "operation_state": state})
	sendBytes(w, status, body)
}

var materialDownloadName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_]*\.(docx|pdf|txt)$`)
var materialArtifactID = regexp.MustCompile(`^material_[0-9a-f]{64}$`)

func sendMaterialFile(w http.ResponseWriter, target materialTarget, requestedID string, out *materialwrite.Outcome) {
	var meta struct {
		JobID      string `json:"job_id"`
		Kind       string `json:"kind"`
		Format     string `json:"format"`
		Filename   string `json:"filename"`
		SHA256     string `json:"sha256"`
		SizeBytes  int64  `json:"size_bytes"`
		ArtifactID string `json:"artifact_id"`
		Provenance string `json:"provenance"`
	}
	content := out.Content
	if json.Unmarshal(out.Result, &meta) != nil || meta.JobID != target.job || meta.Kind != target.kind || len(content) == 0 || len(content) > materialintegrity.MaxArtifactBytes || meta.SizeBytes != int64(len(content)) || len(meta.Filename) > 254 || !materialDownloadName.MatchString(meta.Filename) {
		materialFailure(w, out, nil, "MATERIAL_RESPONSE_INVALID", http.StatusInternalServerError)
		return
	}
	sum := sha256.Sum256(content)
	if meta.SHA256 != hex.EncodeToString(sum[:]) || !strings.HasSuffix(meta.Filename, "."+meta.Format) || meta.Provenance != "versioned" && meta.Provenance != "legacy_unverified" || meta.Provenance == "versioned" && !materialArtifactID.MatchString(meta.ArtifactID) || (target.operation == "legacy-file") != (meta.Provenance == "legacy_unverified") || target.operation == "file" && requestedID != "" && meta.ArtifactID != requestedID {
		materialFailure(w, out, nil, "MATERIAL_RESPONSE_INVALID", http.StatusInternalServerError)
		return
	}
	contentType := ""
	switch meta.Format {
	case "pdf":
		contentType = "application/pdf"
	case "docx":
		contentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case "txt":
		contentType = "text/plain; charset=utf-8"
	default:
		materialFailure(w, out, nil, "MATERIAL_RESPONSE_INVALID", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(meta.Filename))
	w.Header().Set("X-Career-Hub-Provenance", meta.Provenance)
	if meta.Provenance == "versioned" {
		w.Header().Set("X-Career-Hub-Artifact-ID", meta.ArtifactID)
	}
	sendTypedBytes(w, http.StatusOK, content, contentType)
}
