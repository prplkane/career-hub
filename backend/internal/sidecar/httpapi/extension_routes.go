package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"careerhub.local/backend/internal/discovery"
)

// ExtensionRequest is chosen only by the closed HTTP route, never by JSON RPC.
// Fields contain the source session/helper contract without its version field.
type ExtensionRequest struct {
	Operation        string
	Fields           map[string]any
	Capture          discovery.CaptureRequest
	Readiness, Scope string
	ContentChars     int
}

func (ExtensionRequest) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted attended request]")
}

// NewApplicationWithExtensions requires explicit host configuration and a
// complete attended adapter. Existing callers remain disabled by default.
func NewApplicationWithExtensions(port int, version string, registry Registry, vault Vault, documents Documents, materials MaterialActions, actions ApplicationActions, origins []string, attended func(context.Context, ExtensionRequest) (*ApplicationOutcome, *ApplicationError)) (http.Handler, error) {
	if attended == nil {
		return nil, &Error{Code: CodeConfiguration}
	}
	raw, err := NewApplication(port, version, registry, vault, documents, materials, actions)
	if err != nil {
		return nil, err
	}
	h := raw.(*handler)
	if err := h.configureExtensions(origins, rand.Reader); err != nil {
		return nil, err
	}
	h.attended = attended
	return h, nil
}

func (h *handler) serveExtension(w http.ResponseWriter, r *http.Request) {
	request := ExtensionRequest{}
	limit, version := int64(16384), int64(1)
	code := "APPLICATION_SESSION_INVALID"
	var keys []string
	switch r.URL.Path {
	case "/api/discoveries/captures":
		request.Operation, limit, code = "capture", MaxMaterialRequestBytes, "EXTENSION_CAPTURE_INVALID"
	case "/api/application-sessions/claim":
		request.Operation = "claim"
		keys = []string{"request_id", "current_url", "tab_id"}
	case "/api/application-sessions/submission-signal":
		request.Operation = "signal"
		keys = []string{"session_id", "session_token", "tab_id", "page_origin", "event_key", "success_rule", "submit_observed_at", "success_observed_at"}
	case "/api/application-sessions/interrupt":
		request.Operation = "interrupt"
		keys = []string{"session_id", "session_token", "tab_id", "page_origin", "reason"}
	case "/api/application-helper/fields":
		request.Operation = "projection"
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			applicationFailure(w, &ApplicationOutcome{}, "APPLICATION_HELPER_REQUEST_INVALID")
			return
		}
	case "/api/application-helper/answers/save":
		request.Operation, limit, version, code = "answers.save", 8192, 2, "APPLICATION_ANSWER_INVALID"
		keys = []string{"expected_revision", "question", "value_kind", "value"}
	case "/api/application-helper/answers/delete":
		request.Operation, limit, version, code = "answers.delete", 4096, 2, "APPLICATION_ANSWER_INVALID"
		keys = []string{"expected_revision", "answer_id"}
	default:
		sendError(w, 404, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	if request.Operation != "projection" {
		if r.ContentLength > limit {
			applicationFailure(w, &ApplicationOutcome{}, "LOCAL_REQUEST_TOO_LARGE")
			return
		}
		if r.Body != nil {
			r.Body = struct {
				io.Reader
				io.Closer
			}{io.LimitReader(r.Body, limit+1), r.Body}
		}
		payload, transportCode, _ := materialJSONChecked(r, func(raw []byte) bool {
			return int64(len(raw)) <= limit && applicationScalarJSON(raw) && uniqueJobDetailsJSON(raw)
		})
		if transportCode != "" {
			applicationFailure(w, &ApplicationOutcome{}, strings.Replace(transportCode, "MATERIAL_REQUEST", "LOCAL_REQUEST", 1))
			return
		}
		if request.Operation == "capture" {
			var problem string
			request, problem = extensionCaptureRequest(payload)
			if problem != "" {
				applicationFailure(w, &ApplicationOutcome{}, problem)
				return
			}
		} else {
			keys = append(keys, "contract_version")
			v, valid := materialRevision(payload["contract_version"])
			if !valid || v != version || !materialKeysExact(payload, keys...) {
				applicationFailure(w, &ApplicationOutcome{}, code)
				return
			}
			request.Fields = make(map[string]any, len(keys)-1)
			for key, raw := range payload {
				if key == "contract_version" {
					continue
				}
				decoder := json.NewDecoder(bytes.NewReader(raw))
				decoder.UseNumber()
				var value any
				if decoder.Decode(&value) != nil {
					applicationFailure(w, &ApplicationOutcome{}, code)
					return
				}
				request.Fields[key] = value
			}
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	call := func() (*ApplicationOutcome, *ApplicationError) { return h.attended(ctx, request) }
	if request.Operation == "capture" {
		h.applicationCallByOutcome(w, ctx, func(out *ApplicationOutcome) int {
			var receipt struct {
				Outcome string `json:"outcome"`
			}
			if out == nil || json.Unmarshal(out.Result, &receipt) != nil {
				return 0
			}
			switch receipt.Outcome {
			case "created":
				return 201
			case "updated", "duplicate":
				return 200
			}
			return 0
		}, call)
	} else {
		h.applicationCall(w, ctx, 200, call)
	}
}
