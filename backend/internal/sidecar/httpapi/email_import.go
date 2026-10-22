package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"careerhub.local/backend/internal/emailimport"
	"careerhub.local/backend/internal/textcompat"
)

func (h *handler) serveEmailImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.ForceQuery {
		sendError(w, 404, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	invalid := func(code string) { applicationFailure(w, &ApplicationOutcome{}, code, "email_import") }
	typeValue, typeSingle := oneHeader(r, "Content-Type")
	base, _, _ := strings.Cut(typeValue, ";")
	_, lengthSingle := oneHeader(r, "Content-Length")
	transfer, transferSingle := oneHeader(r, "Transfer-Encoding")
	if !typeSingle || textcompat.Fold(textcompat.Strip(base)) != "application/json" || !lengthSingle ||
		!transferSingle || transfer != "" || len(r.TransferEncoding) != 0 || r.Body == nil || r.ContentLength <= 0 {
		invalid("EMAIL_IMPORT_REQUEST_INVALID")
		return
	}
	if r.ContentLength > emailimport.MaxBatchJSONBytes {
		invalid("LOCAL_REQUEST_TOO_LARGE")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, emailimport.MaxBatchJSONBytes+1))
	if err != nil || int64(len(raw)) != r.ContentLength || !materialJSONObject(raw) || !applicationScalarJSON(raw) || !uniqueJobDetailsJSON(raw) {
		invalid("EMAIL_IMPORT_REQUEST_INVALID")
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 2 || !materialKeys(fields, "contract_version", "files") {
		invalid("EMAIL_IMPORT_PAYLOAD_INVALID")
		return
	}
	if string(fields["contract_version"]) != "1" {
		invalid("EMAIL_IMPORT_VERSION_UNSUPPORTED")
		return
	}
	var files []json.RawMessage
	if json.Unmarshal(fields["files"], &files) != nil {
		invalid("EMAIL_IMPORT_FILE_COUNT_INVALID")
		return
	}
	batch, problem := emailimport.PrepareFiles(files)
	if problem != nil {
		invalid(problem.Code)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	h.applicationCall(w, ctx, http.StatusCreated, func() (*ApplicationOutcome, *ApplicationError) {
		return h.application.EmailImport(ctx, batch)
	})
}
