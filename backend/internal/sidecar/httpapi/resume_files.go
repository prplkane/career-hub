package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/intake/resumelibrary"
)

// File routes retain the first-party lock and verified binary response.
// The submitted route lets the owner select the latest event and its bytes in
// one operation; it never substitutes the current picker or a client resume ID.
func (h *handler) serveResumeFile(w http.ResponseWriter, ctx context.Context, request JobRequest) {
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(25 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		applicationFailure(w, &ApplicationOutcome{}, "LOCAL_OPERATION_FAILED")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if ctx.Err() != nil {
		applicationFailure(w, &ApplicationOutcome{}, "LOCAL_OPERATION_CANCELED")
		return
	}
	var out *ApplicationOutcome
	defer func() {
		if recover() != nil {
			applicationFailure(w, nil, "LOCAL_OPERATION_FAILED")
		}
	}()
	out, problem := h.application.Jobs(ctx, request)
	if problem != nil {
		applicationFailure(w, out, problem.Code)
		return
	}
	if ctx.Err() != nil {
		applicationFailure(w, out, "LOCAL_OPERATION_CANCELED")
		return
	}
	var meta struct {
		ID        string `json:"id"`
		ResumeID  string `json:"resume_id"`
		FileName  string `json:"file_name"`
		SHA256    string `json:"sha256"`
		SizeBytes int64  `json:"size_bytes"`
	}
	submitted := request.Operation == "resume.submitted_file"
	attachment := request.Operation == "resume.attachment_file"
	// Old attachments may have a NULL resume_id. A missing field, a version
	// response keyed by id, or an empty string is not that legacy record shape.
	var fields map[string]json.RawMessage
	if out != nil {
		_ = json.Unmarshal(out.Result, &fields)
	}
	attachmentID, hasAttachmentID := fields["resume_id"]
	if out == nil || !materialJSONObject(out.Result) || len(out.Result) > MaxResponseBytes ||
		json.Unmarshal(out.Result, &meta) != nil ||
		(submitted && (meta.ResumeID == "" || meta.ID != "")) ||
		(attachment && (meta.ID != "" || !hasAttachmentID || string(attachmentID) != "null" && meta.ResumeID == "")) ||
		(!submitted && !attachment && (meta.ID != request.ID || meta.ResumeID != "")) ||
		meta.FileName == "" || len(meta.FileName) > 4096 || !utf8.ValidString(meta.FileName) ||
		meta.SizeBytes < 1 || meta.SizeBytes > resumelibrary.MaxResumeBytes ||
		len(out.Content) != int(meta.SizeBytes) {
		applicationFailure(w, out, "LOCAL_RESPONSE_INVALID")
		return
	}
	sum := sha256.Sum256(out.Content)
	if !strings.EqualFold(meta.SHA256, hex.EncodeToString(sum[:])) {
		applicationFailure(w, out, "LOCAL_RESPONSE_INVALID")
		return
	}
	contentType := "application/octet-stream"
	if dot := strings.LastIndexByte(meta.FileName, '.'); dot >= 0 {
		switch strings.ToLower(meta.FileName[dot:]) {
		case ".doc":
			contentType = "application/msword"
		case ".docx":
			contentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		case ".pdf":
			contentType = "application/pdf"
		case ".rtf":
			contentType = "application/rtf"
		case ".txt":
			contentType = "text/plain; charset=utf-8"
		}
	}
	disposition := "attachment"
	if submitted || attachment {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", disposition+"; filename*=UTF-8''"+encodeResumeFilename(meta.FileName))
	sendTypedBytes(w, http.StatusOK, out.Content, contentType)
}

// Upload and job attach accept only the existing browser's bounded binary
// contract. The immutable file and SQL row are published by
// applicationwrite.Resume, never by the HTTP adapter.
func (h *handler) serveResumeUpload(w http.ResponseWriter, r *http.Request, request JobRequest) {
	invalidCode := "RESUME_LIBRARY_UPLOAD_INVALID"
	status := http.StatusCreated
	if request.Operation == "resume.attach" {
		invalidCode = "APPLICATION_RESUME_INVALID"
		status = http.StatusOK
	}
	invalid := func() { applicationFailure(w, &ApplicationOutcome{}, invalidCode) }
	media, singleMedia := oneHeader(r, "Content-Type")
	name, singleName := oneHeader(r, "X-Resume-Name")
	label, singleLabel := oneHeader(r, "X-Resume-Label")
	if !singleMedia || !singleName || !singleLabel ||
		!strings.EqualFold(strings.TrimSpace(strings.SplitN(media, ";", 2)[0]), "application/octet-stream") ||
		name == "" || len(name) > 4096 || len(label) > 4096 ||
		r.ContentLength < 1 || r.ContentLength > resumelibrary.MaxResumeBytes ||
		len(r.TransferEncoding) != 0 || r.Body == nil {
		invalid()
		return
	}
	name, nameErr := url.PathUnescape(name)
	label, labelErr := url.PathUnescape(label)
	if nameErr != nil || labelErr != nil || !utf8.ValidString(name) || !utf8.ValidString(label) {
		invalid()
		return
	}
	content, err := io.ReadAll(io.LimitReader(r.Body, r.ContentLength+1))
	if err != nil || int64(len(content)) != r.ContentLength {
		invalid()
		return
	}
	request.Fields = map[string]any{"file_name": name, "display_name": label}
	if request.Operation == "resume.attach" {
		// Validate the complete user-controlled upload before the composition
		// registers a saved job. The owner repeats the same validation inside its
		// transaction; this gate only prevents an invalid request from creating a
		// Captured ledger row.
		upload, problem := resumelibrary.NormalizeUpload(name, content, label)
		if problem != nil {
			invalid()
			return
		}
		request.Fields = map[string]any{"job_id": request.ID, "file_name": upload.FileName, "label": upload.DisplayName}
	}
	request.Content = content
	if !resumeWriteDeadline(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	h.applicationCall(w, ctx, status, func() (*ApplicationOutcome, *ApplicationError) {
		return h.application.Jobs(ctx, request)
	})
}

// Digest accepts no client document bytes or options. The native owner reads
// the exact immutable Resume Version and effective catalogs from the claimed
// Home, then publishes a distinct recoverable intake run.
func (h *handler) serveResumeDigest(w http.ResponseWriter, r *http.Request, request JobRequest) {
	if r.URL == nil || r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		applicationFailure(w, &ApplicationOutcome{}, "RESUME_DIGEST_REQUEST_INVALID", "resume_digest")
		return
	}
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(70 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		applicationFailure(w, &ApplicationOutcome{}, "RESUME_DIGEST_UNAVAILABLE", "resume_digest")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 65*time.Second)
	defer cancel()
	h.applicationCall(w, ctx, http.StatusCreated, func() (*ApplicationOutcome, *ApplicationError) {
		return h.application.Jobs(ctx, request)
	})
}

func resumeWriteDeadline(w http.ResponseWriter) bool {
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(25 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		applicationFailure(w, &ApplicationOutcome{}, "LOCAL_OPERATION_FAILED")
		return false
	}
	return true
}

// Python's quote(..., safe="") preserves only unreserved ASCII characters.
// Percent-encoding every other UTF-8 byte keeps the filename in one header.
func encodeResumeFilename(name string) string {
	const digits = "0123456789ABCDEF"
	var out strings.Builder
	for i := 0; i < len(name); i++ {
		b := name[i]
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.IndexByte("-_.~", b) >= 0 {
			out.WriteByte(b)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(digits[b>>4])
		out.WriteByte(digits[b&15])
	}
	return out.String()
}
