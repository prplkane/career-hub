package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"careerhub.local/backend/internal/applications/applicationtexts"
	"careerhub.local/backend/internal/materials"
	"careerhub.local/backend/internal/materials/materialintegrity"
	"careerhub.local/backend/internal/materials/materialwrite"
	"careerhub.local/backend/internal/textcompat"
)

// MaterialActions are trusted owner-backed callbacks, not caller-defined tools.
// Their owner retains SQL/source/operation gates through synchronous cleanup.
// Review confirmation belongs only to explicit first-party human actions; it
// must not be registered as an autonomous agent permission.
type MaterialActions struct {
	ApplicationText func(context.Context, string, applicationtexts.Draft) (*materialwrite.Outcome, *materialwrite.Error)
	Draft           func(context.Context, materials.Request) (*materialwrite.Outcome, *materialwrite.Error)
	Review          func(context.Context, materialintegrity.ReviewRequest) (*materialwrite.Outcome, *materialwrite.Error)
	Export          func(context.Context, materialwrite.ExportRequest) (*materialwrite.Outcome, *materialwrite.Error)
	Download        func(context.Context, materialwrite.ExportReadRequest) (*materialwrite.Outcome, *materialwrite.Error)
	Legacy          func(context.Context, materialwrite.LegacyExportRequest) (*materialwrite.Outcome, *materialwrite.Error)
}

// NewWithMaterials is selected only after explicit process-owned bootstrap.
// New retains its read-only surface. A copied action set cannot be replaced by
// mutating the construction argument later; callbacks must return owned results.
func NewWithMaterials(port int, version string, registry Registry, vault Vault, documents Documents, actions MaterialActions) (http.Handler, error) {
	if actions.Draft == nil || actions.Review == nil || actions.Export == nil || actions.Download == nil || actions.Legacy == nil {
		return nil, &Error{Code: CodeConfiguration}
	}
	raw, e := New(port, version, registry, vault, documents)
	if e != nil {
		return nil, e
	}
	h := raw.(*handler)
	h.materials = &actions
	return h, nil
}

type materialTarget struct{ job, kind, operation string }

// Only the job segment is percent-decoded. Route names/slashes are literal;
// encoded separators, traversal and alternate route spellings cannot select an
// operation. The domain validator admits the same bounded Unicode job IDs.
func materialRoute(r *http.Request) (materialTarget, bool) {
	if r.URL == nil || len(r.RequestURI) > MaxRequestTargetBytes || len(r.URL.RequestURI()) > MaxRequestTargetBytes {
		return materialTarget{}, false
	}
	parts := strings.Split(r.URL.EscapedPath(), "/")
	if len(parts) == 5 && parts[0] == "" && parts[1] == "api" && parts[2] == "jobs" && parts[4] == "application-texts" {
		job, e := url.PathUnescape(parts[3])
		if e == nil && materials.ValidateRequest(materials.Request{JobID: job, Kind: "cover_letter", Operation: "load"}) == nil {
			return materialTarget{job: job, operation: "application-texts"}, true
		}
		return materialTarget{}, false
	}
	if len(parts) < 6 || len(parts) > 7 || parts[0] != "" || parts[1] != "api" || parts[2] != "jobs" || parts[4] != "materials" {
		return materialTarget{}, false
	}
	kind := parts[5]
	if kind == "cover-letter" {
		kind = "cover_letter"
	} else if kind != "resume" {
		return materialTarget{}, false
	}
	job, e := url.PathUnescape(parts[3])
	if e != nil || materials.ValidateRequest(materials.Request{JobID: job, Kind: kind, Operation: "load"}) != nil {
		return materialTarget{}, false
	}
	op := "editor"
	if len(parts) == 7 {
		op = parts[6]
		if op != "file" && op != "exports" && op != "reviews" && op != "legacy-file" {
			return materialTarget{}, false
		}
	}
	return materialTarget{job, kind, op}, true
}

func materialMutationSite(site string) bool {
	return site == "" || site == "none" || site == "same-origin" || site == "same-site"
}

// Query decoding matches Python's first-nonblank parse_qs selection. It does
// not trim values or permit any query to select resources/Data Home paths.
func materialQuery(raw, key, fallback string) string {
	for _, part := range strings.Split(raw, "&") {
		name, value, ok := strings.Cut(part, "=")
		if ok && value != "" && documentQueryBytes(name) == key {
			return documentQueryBytes(value)
		}
	}
	return fallback
}

func (h *handler) serveMaterial(w http.ResponseWriter, r *http.Request, target materialTarget) {
	get, post := r.Method == http.MethodGet, r.Method == http.MethodPost
	if !(target.operation == "application-texts" && post && h.materials.ApplicationText != nil || target.operation == "editor" && (get || post) || target.operation == "reviews" && (get || post) || target.operation == "exports" && post || (target.operation == "file" || target.operation == "legacy-file") && get) {
		sendError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	query := r.URL.RawQuery
	id := materialQuery(query, "artifact_id", "")
	compatibilityExport := target.operation == "file" && id == ""
	// Python's existing GET /file generates an export. Keep the working URL but
	// apply mutation fetch-metadata rules, not the ordinary authenticated-GET
	// exception. New clients should POST /exports then GET /file?artifact_id=...
	if compatibilityExport {
		site, _ := oneHeader(r, "Sec-Fetch-Site")
		if !materialMutationSite(textcompat.Fold(textcompat.Strip(site))) {
			sendError(w, http.StatusForbidden, "LOCAL_API_ACCESS_DENIED", "Cross-site browser mutations are not allowed")
			return
		}
	}
	if target.operation == "file" && id != "" && materialQuery(query, "format", "") != "" {
		materialInputError(w, "MATERIAL_REQUEST_INVALID", http.StatusBadRequest)
		return
	}
	if r.Context().Err() != nil {
		materialFailure(w, nil, nil, "MATERIAL_REQUEST_CANCELED", http.StatusRequestTimeout)
		return
	}
	var call func(context.Context) (*materialwrite.Outcome, *materialwrite.Error)
	downloading := target.operation == "file" || target.operation == "legacy-file"
	switch target.operation {
	case "application-texts":
		draft, code, status := applicationTextRequest(r)
		if code != "" {
			materialInputError(w, code, status)
			return
		}
		call = func(ctx context.Context) (*materialwrite.Outcome, *materialwrite.Error) {
			return h.materials.ApplicationText(ctx, target.job, draft)
		}
	case "editor":
		request := materials.Request{JobID: target.job, Kind: target.kind, Operation: "load"}
		if post {
			payload, code, status := materialJSON(r)
			if code != "" {
				materialInputError(w, code, status)
				return
			}
			var ok bool
			request, ok = materialDraftRequest(target, payload)
			if !ok {
				materialInputError(w, "MATERIAL_REQUEST_INVALID", http.StatusBadRequest)
				return
			}
		}
		call = func(ctx context.Context) (*materialwrite.Outcome, *materialwrite.Error) {
			return h.materials.Draft(ctx, request)
		}
	case "reviews":
		request := materialintegrity.ReviewRequest{JobID: target.job, Kind: target.kind, Operation: "inspect"}
		if post {
			payload, code, status := materialJSON(r)
			if code != "" {
				materialInputError(w, code, status)
				return
			}
			var ok bool
			request, ok = materialReviewRequest(target, payload)
			if !ok {
				materialInputError(w, "MATERIAL_REQUEST_INVALID", http.StatusBadRequest)
				return
			}
		}
		call = func(ctx context.Context) (*materialwrite.Outcome, *materialwrite.Error) {
			return h.materials.Review(ctx, request)
		}
	case "exports":
		payload, code, status := materialJSON(r)
		if code != "" {
			materialInputError(w, code, status)
			return
		}
		format, ok := materialExportFormat(payload)
		if !ok {
			materialInputError(w, "MATERIAL_REQUEST_INVALID", http.StatusBadRequest)
			return
		}
		call = func(ctx context.Context) (*materialwrite.Outcome, *materialwrite.Error) {
			return h.materials.Export(ctx, materialwrite.ExportRequest{JobID: target.job, Kind: target.kind, FileFormat: format})
		}
	case "file":
		if compatibilityExport {
			format := materialQuery(query, "format", "pdf")
			call = func(ctx context.Context) (*materialwrite.Outcome, *materialwrite.Error) {
				return h.materials.Export(ctx, materialwrite.ExportRequest{JobID: target.job, Kind: target.kind, FileFormat: format})
			}
		} else {
			call = func(ctx context.Context) (*materialwrite.Outcome, *materialwrite.Error) {
				return h.materials.Download(ctx, materialwrite.ExportReadRequest{JobID: target.job, Kind: target.kind, ArtifactID: id})
			}
		}
	case "legacy-file":
		name := materialQuery(query, "filename", "")
		call = func(ctx context.Context) (*materialwrite.Outcome, *materialwrite.Error) {
			return h.materials.Legacy(ctx, materialwrite.LegacyExportRequest{JobID: target.job, Kind: target.kind, Filename: name})
		}
	}
	// The read-only listener's default write deadline is 10s. This authorized
	// route has the existing owner's 20s operation budget plus response allowance;
	// body reading retains the server's 5s bound. Never widen startup timeouts.
	if e := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(25 * time.Second)); e != nil && !errors.Is(e, http.ErrNotSupported) {
		materialInputError(w, "MATERIAL_REQUEST_DEADLINE_FAILED", http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	out, problem, panicked := invokeMaterial(ctx, call)
	if panicked {
		materialFailure(w, nil, nil, "UNEXPECTED_ERROR", http.StatusInternalServerError)
		return
	}
	if ctx.Err() != nil {
		materialFailure(w, out, problem, "MATERIAL_REQUEST_CANCELED", http.StatusRequestTimeout)
		return
	}
	if problem != nil {
		code, status := materialError(problem)
		if target.operation == "application-texts" {
			code, status = applicationTextError(problem)
		}
		materialFailure(w, out, problem, code, status)
		return
	}
	if out == nil || out.Partial || out.CleanupFailed || len(out.Result) == 0 || len(out.Result) > materials.MaxResponseBytes || !materialJSONObject(out.Result) {
		materialFailure(w, out, nil, "MATERIAL_RESPONSE_INVALID", http.StatusInternalServerError)
		return
	}
	if downloading {
		sendMaterialFile(w, target, id, out)
		return
	}
	sendBytes(w, http.StatusOK, out.Result)
}

func invokeMaterial(ctx context.Context, call func(context.Context) (*materialwrite.Outcome, *materialwrite.Error)) (out *materialwrite.Outcome, problem *materialwrite.Error, panicked bool) {
	defer func() {
		if recover() != nil {
			out, problem, panicked = nil, nil, true
		}
	}()
	out, problem = call(ctx)
	return
}
