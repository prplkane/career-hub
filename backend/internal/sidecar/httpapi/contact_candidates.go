package httpapi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"careerhub.local/backend/internal/applications/contactcandidate"
	"careerhub.local/backend/internal/textcompat"
)

// contactCandidateBody keeps the transport closed before the pure contract
// decodes any user-controlled value. Errors intentionally collapse to the
// frozen request-invalid code rather than exposing parser details.
func contactCandidateBody(r *http.Request) ([]byte, bool) {
	contentType, contentTypeSingle := oneHeader(r, "Content-Type")
	base, _, _ := strings.Cut(contentType, ";")
	_, lengthSingle := oneHeader(r, "Content-Length")
	transfer, transferSingle := oneHeader(r, "Transfer-Encoding")
	if !contentTypeSingle || textcompat.Fold(textcompat.Strip(base)) != "application/json" ||
		!lengthSingle || !transferSingle || transfer != "" || len(r.TransferEncoding) != 0 ||
		r.ContentLength <= 0 || r.ContentLength > contactcandidate.MaxRequestBytes || r.Body == nil {
		return nil, false
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, contactcandidate.MaxRequestBytes+1))
	if err != nil || int64(len(raw)) != r.ContentLength || !applicationScalarJSON(raw) {
		return nil, false
	}
	return raw, true
}

func (h *handler) serveContactCandidate(w http.ResponseWriter, r *http.Request, request JobRequest) {
	raw, ok := contactCandidateBody(r)
	if !ok {
		applicationFailure(w, &ApplicationOutcome{}, "CONTACT_CANDIDATE_REQUEST_INVALID")
		return
	}
	if request.Operation == "contact.candidate_preview" {
		decoded, problem := contactcandidate.DecodePreview(raw)
		if problem != nil {
			applicationFailure(w, &ApplicationOutcome{}, problem.Code)
			return
		}
		request.ContactCandidate = &decoded
	} else {
		decoded, problem := contactcandidate.DecodeConfirmation(raw)
		if problem != nil {
			applicationFailure(w, &ApplicationOutcome{}, problem.Code)
			return
		}
		request.ContactConfirmation = &decoded
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	h.applicationCallByOutcome(w, ctx, func(out *ApplicationOutcome) int {
		if out != nil && out.Created {
			return http.StatusCreated
		}
		return http.StatusOK
	}, func() (*ApplicationOutcome, *ApplicationError) {
		return h.application.Jobs(ctx, request)
	})
}
