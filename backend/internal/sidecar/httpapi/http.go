// Package httpapi provides the authenticated browser boundary for the opt-in Go
// sidecar pilot. It owns no listener, filesystem, runtime configuration or logs.
package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/textcompat"
	"careerhub.local/backend/internal/vault/vaultread"
)

const (
	CapabilityHeader      = "X-Career-Hub-Token"
	MaxRequestTargetBytes = 8192
	MaxResponseBytes      = 4 * 1024 * 1024
	CodeConfiguration     = "SIDECAR_HTTP_CONFIGURATION_INVALID"
	CodeToken             = "SIDECAR_HTTP_TOKEN_FAILED"
	contentSecurityPolicy = "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
)

// Registry returns a trusted, JSON-compatible catalog snapshot. The callback
// must not concurrently mutate returned maps/slices while they are encoded.
// Calls are serialized; runtime/supervisor owns filesystem/process deadlines.
type Registry func() (map[string]any, error)

// Vault returns an owned, validated JSON object for a fixed view. The request
// context is passed through; runtime still owns synchronous I/O deadlines.
type Vault func(context.Context, vaultread.View) (json.RawMessage, error)

// Error contains only a fixed construction category, never an underlying cause.
type Error struct{ Code string }

func (e *Error) Error() string {
	return "The read-only sidecar HTTP boundary could not be initialized."
}

type handler struct {
	port        string
	appVersion  string
	token       string
	agent       string // separate scoped capability for career-hub-agent; see agent_admission.go
	registry    Registry
	vault       Vault
	documents   Documents
	materials   *MaterialActions
	application *ApplicationActions
	extensions  *extensionClients
	pairing     *extensionPairing
	attended    func(context.Context, ExtensionRequest) (*ApplicationOutcome, *ApplicationError)
	mu          sync.Mutex
}

// New creates a private 32-byte browser capability and fixed GET routes.
// The token is exposed only by a successfully authorized /api/session response.
func New(port int, appVersion string, registry Registry, vault Vault, documents Documents) (http.Handler, error) {
	return newHandler(port, appVersion, registry, vault, documents, rand.Reader)
}

func newHandler(port int, appVersion string, registry Registry, vault Vault, documents Documents, random io.Reader) (http.Handler, error) {
	if port < 1 || port > 65535 || !validVersion(appVersion) || registry == nil || vault == nil || documents == nil {
		return nil, &Error{Code: CodeConfiguration}
	}
	var seed [32]byte
	if random == nil {
		return nil, &Error{Code: CodeToken}
	}
	if _, err := io.ReadFull(random, seed[:]); err != nil {
		clear(seed[:])
		return nil, &Error{Code: CodeToken}
	}
	token := base64.RawURLEncoding.EncodeToString(seed[:])
	clear(seed[:])
	return &handler{port: strconv.Itoa(port), appVersion: appVersion, token: token, registry: registry, vault: vault, documents: documents}, nil
}

func validVersion(version string) bool {
	if len(version) == 0 || len(version) > 128 {
		return false
	}
	for _, c := range version {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '+') {
			return false
		}
	}
	return true
}

// oneHeader also handles non-canonical Header map keys used by in-process
// clients. Go's wire parser independently refuses duplicated Host authorities.
func oneHeader(r *http.Request, name string) (string, bool) {
	var value string
	count := 0
	for key, values := range r.Header {
		if strings.EqualFold(key, name) {
			count += len(values)
			if len(values) != 0 {
				value = values[0]
			}
		}
	}
	return value, count <= 1
}

func (h *handler) localHost(host string) bool {
	return host == "127.0.0.1:"+h.port || host == "localhost:"+h.port || host == "[::1]:"+h.port
}

func (h *handler) localOrigin(origin string) bool {
	return origin == "http://127.0.0.1:"+h.port || origin == "http://localhost:"+h.port || origin == "http://[::1]:"+h.port
}

func (h *handler) authorize(w http.ResponseWriter, r *http.Request, path string) bool {
	deny := func(message string) bool {
		sendError(w, http.StatusForbidden, "LOCAL_API_ACCESS_DENIED", message)
		return false
	}
	hostHeader, hostSingle := oneHeader(r, "Host")
	if !hostSingle || hostHeader != "" || !h.localHost(textcompat.Fold(textcompat.Strip(r.Host))) {
		return deny("Request Host is not an allowed loopback authority")
	}
	if handled, allowed := h.authorizeExtension(w, r, path); handled {
		return allowed
	}
	if r.Method == http.MethodOptions {
		// There are no configured extension/CORS routes in this slice.
		return deny("Preflight target is not allowed")
	}
	if !strings.HasPrefix(path, "/api/") {
		return true
	}
	origin, originSingle := oneHeader(r, "Origin")
	fetchSite, fetchSingle := oneHeader(r, "Sec-Fetch-Site")
	token, tokenSingle := oneHeader(r, CapabilityHeader)
	if !originSingle {
		return deny("Request Origin is not allowed")
	}
	if !fetchSingle {
		return deny("Mutation fetch metadata is invalid")
	}
	if !tokenSingle {
		return deny("A valid local capability token is required")
	}
	origin = textcompat.Strip(origin)
	fetchSite = textcompat.Fold(textcompat.Strip(fetchSite))
	localOrigin := h.localOrigin(origin)
	if path == "/api/health" {
		if r.Method != http.MethodGet {
			return deny("Health accepts GET only")
		}
		if origin != "" && !localOrigin {
			return deny("Health Origin is not allowed")
		}
		if fetchSite == "cross-site" {
			return deny("Cross-site health requests are not allowed")
		}
		return true
	}
	// /api/session has no exception: like every other /api/ route it needs a
	// capability token, and it returns capabilities only. No route discloses
	// a token (ADR SEC-250); the window gets its token from the desktop host.
	if h.isAgentToken(token) {
		// The agent client is a command-line program: it never sends browser
		// fetch metadata or an Origin, so their presence means a page is
		// replaying a leaked agent token and is refused.
		if origin != "" || fetchSite != "" {
			return deny("The agent token is not accepted from a browser")
		}
		if !agentRoute(r.Method, path) {
			return deny("The request is outside the agent client's scope")
		}
		return true
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(h.token)) != 1 {
		return deny("A valid local capability token is required")
	}
	if origin != "" && !localOrigin {
		return deny("Request Origin is not allowed for the local UI")
	}
	if r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH" || r.Method == "DELETE" {
		if !materialMutationSite(fetchSite) {
			return deny("Cross-site browser mutations are not allowed")
		}
	}
	return true
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := ""
	if r.URL != nil {
		path = r.URL.Path
	}
	if !h.authorize(w, r, path) {
		return
	}
	material, isMaterial := materialRoute(r)
	isMaterial = isMaterial && h.materials != nil
	edit, isVaultEdit := vaultEditRoute(r)
	isVaultEdit = isVaultEdit && h.application != nil
	feed, isFeed := feedRoute(r)
	isFeed = isFeed && h.application != nil
	intake, isIntake := intakeRoute(r)
	isIntake = isIntake && h.application != nil
	job, isJob := jobRoute(r)
	isJob = isJob && h.application != nil
	// Do not accept alternate encoded/absolute targets, normalize dot segments,
	// redirect paths or infer filesystem paths. Static assets use an exact
	// application-mode allowlist below. Query strings do not select a route.
	if r.URL == nil || len(r.RequestURI) > MaxRequestTargetBytes || len(r.URL.RequestURI()) > MaxRequestTargetBytes ||
		r.URL.RawPath != "" && !isMaterial && !isVaultEdit && !isFeed && !isIntake && !isJob || r.URL.IsAbs() || r.URL.Host != "" || r.URL.Fragment != "" ||
		strings.ContainsAny(path, "%\\") && !isMaterial && !isVaultEdit && !isFeed && !isIntake && !isJob || (r.RequestURI != "" && !strings.HasPrefix(r.RequestURI, "/")) {
		sendError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	if r.Method == http.MethodPost && path == extensionPairPath && h.extensions != nil {
		if origin, single := oneHeader(r, "Origin"); single && h.extensionOrigin(textcompat.Strip(origin)) {
			h.serveExtensionPair(w, r)
			return
		}
	}
	if h.serveExtensionBootstrap(w, r) {
		return
	}
	if h.servePairingAdmin(w, r) {
		return
	}
	if h.attended != nil && extensionPurpose(path) != "" {
		h.serveExtension(w, r)
		return
	}
	if isMaterial {
		h.serveMaterial(w, r, material)
		return
	}
	if isVaultEdit {
		h.serveVaultEdit(w, r, edit)
		return
	}
	if isFeed {
		h.serveFeed(w, r, feed)
		return
	}
	if isIntake {
		h.serveIntake(w, r, intake)
		return
	}
	if isJob {
		h.serveJob(w, r, job)
		return
	}
	if h.application != nil {
		_, static := staticFiles[path]
		profileRead := r.Method == "GET" && (path == "/api/vault/profile" || path == "/api/vault/eligibility")
		if static || applicationRoute(path) && !profileRead {
			h.serveApplication(w, r)
			return
		}
	}
	if r.Method != http.MethodGet {
		sendError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	switch path {
	case "/api/health":
		sendJSON(w, protocol.HealthRecord(h.appVersion))
	case "/api/session":
		session := map[string]any{"client": "browser"}
		if h.materials != nil {
			session["capabilities"] = map[string]any{"material_integrity": 1}
			if h.materials.ApplicationText != nil {
				session["capabilities"].(map[string]any)["application_texts"] = 1
			}
		}
		if h.application != nil {
			if h.application.AgentPrepare != nil {
				session["capabilities"].(map[string]any)["agent_prepare"] = 1
			}
			if h.application.AgentContext != nil {
				session["capabilities"].(map[string]any)["agent_context"] = 1
			}
			if h.application.AgentAnalyze != nil {
				session["capabilities"].(map[string]any)["agent_analyze"] = 1
			}
			if h.application.RecordSubmission != nil {
				session["capabilities"].(map[string]any)["record_submission"] = 1
			}
			if h.application.ExactVocabulary != nil {
				session["capabilities"].(map[string]any)["exact_vocabulary"] = 1
			}
			// Application admission requires every owned email callback. Physical
			// root binding and native credentials are qualified only on Windows.
			session["capabilities"].(map[string]any)["email_connection"] = runtime.GOOS == "windows"
			// Native digest, Review build/suggestion and guarded readback are composed.
			session["capabilities"].(map[string]any)["resume_digest"] = true
			session["capabilities"].(map[string]any)["vault_setup_import"] = true
			session["capabilities"].(map[string]any)["vault_setup_empty"] = true
			// Only the Go preview accepts a revision-bound assessment decision.
			session["capabilities"].(map[string]any)["assessment_resolution_revision"] = true
			// Only the Go preview accepts a revision-bound saved-JD correction.
			session["capabilities"].(map[string]any)["job_details_revision"] = true
			// Only the Go preview accepts a revision-bound saved-JD rescan.
			session["capabilities"].(map[string]any)["job_reanalysis_revision"] = true
		}
		sendJSON(w, session)
	case "/api/skills-registry":
		h.mu.Lock()
		body, ok := h.registryJSON()
		h.mu.Unlock()
		if !ok {
			sendError(w, http.StatusInternalServerError, "UNEXPECTED_ERROR", "The local operation could not be completed safely.")
			return
		}
		sendBytes(w, http.StatusOK, body)
	case "/api/documents":
		h.mu.Lock()
		body, code, status := h.documentsJSON(r.Context(), documentArchiveQuery(r.URL.RawQuery))
		h.mu.Unlock()
		if code != "" {
			sendError(w, status, code, "Document metadata could not be read safely.")
			return
		}
		sendBytes(w, http.StatusOK, body)
	default:
		view, exists := vaultView(path)
		if !exists {
			sendError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Route not found")
			return
		}
		h.mu.Lock()
		body, code, status := h.vaultJSON(r.Context(), view)
		h.mu.Unlock()
		if code != "" {
			detail := "Career Vault records could not be read safely."
			if status == http.StatusConflict {
				detail = "Career Vault records could not be validated."
			}
			sendError(w, status, code, detail)
			return
		}
		sendBytes(w, http.StatusOK, body)
	}
}

// No path segment, filename, prefix match or query parameter is a view selector.
func vaultView(path string) (vaultread.View, bool) {
	switch path {
	case "/api/vault/setup":
		return vaultread.SetupView, true
	case "/api/vault/profile":
		return vaultread.ProfileView, true
	case "/api/vault/eligibility":
		return vaultread.EligibilityView, true
	case "/api/vault/education":
		return vaultread.EducationView, true
	case "/api/vault/experiences":
		return vaultread.ExperiencesView, true
	case "/api/vault/projects":
		return vaultread.ProjectsView, true
	case "/api/vault/skills":
		return vaultread.SkillsView, true
	case "/api/vault/snapshot":
		return vaultread.SnapshotView, true
	default:
		return 0, false
	}
}

func (h *handler) vaultJSON(ctx context.Context, view vaultread.View) (body []byte, code string, status int) {
	// Never log callback panic text or private schema/ID/native details.
	defer func() {
		if recover() != nil {
			body, code, status = nil, "UNEXPECTED_ERROR", http.StatusInternalServerError
		}
	}()
	if ctx.Err() != nil {
		return nil, "VAULT_READ_CANCELED", http.StatusInternalServerError
	}
	payload, err := h.vault(ctx, view)
	if err != nil {
		var typed *vaultread.Error
		if errors.As(err, &typed) && typed != nil {
			switch typed.Code {
			case "VAULT_FILE_MISSING", "VAULT_READ_FAILED", "VAULT_YAML_INVALID", "VAULT_ROOT_TYPE", "VAULT_RECORD_VALIDATION_FAILED":
				return nil, "VAULT_RECORD_VALIDATION_FAILED", http.StatusConflict
			case "VAULT_INPUT_UNSUPPORTED", "VAULT_INPUT_LIMIT", "VAULT_VALIDATION_UNSUPPORTED", "VAULT_VALIDATION_LIMIT",
				"VAULT_RESPONSE_UNSUPPORTED", "VAULT_RESPONSE_LIMIT", "VAULT_READ_CANCELED", "VAULT_PATH_INVALID", "VAULT_CLOSE_FAILED",
				"VAULT_PROJECTION_UNSUPPORTED", "VAULT_PROJECTION_LIMIT":
				return nil, typed.Code, http.StatusInternalServerError
			}
		}
		return nil, "UNEXPECTED_ERROR", http.StatusInternalServerError
	}
	if ctx.Err() != nil {
		return nil, "VAULT_READ_CANCELED", http.StatusInternalServerError
	}
	if len(payload) > MaxResponseBytes {
		return nil, "VAULT_RESPONSE_LIMIT", http.StatusInternalServerError
	}
	if len(payload) == 0 || payload[0] != '{' || !json.Valid(payload) {
		return nil, "VAULT_RESPONSE_UNSUPPORTED", http.StatusInternalServerError
	}
	return payload, "", http.StatusOK
}

func (h *handler) registryJSON() (body []byte, ok bool) {
	// Never let callback/encoding panic text reach net/http's error logger.
	defer func() {
		if recover() != nil {
			body, ok = nil, false
		}
	}()
	payload, err := h.registry()
	if err != nil || payload == nil {
		return nil, false
	}
	body, err = json.Marshal(payload)
	return body, err == nil && len(body) <= MaxResponseBytes
}

func sendJSON(w http.ResponseWriter, payload map[string]any) {
	body, err := json.Marshal(payload)
	if err != nil || len(body) > MaxResponseBytes {
		sendError(w, http.StatusInternalServerError, "UNEXPECTED_ERROR", "The local operation could not be completed safely.")
		return
	}
	sendBytes(w, http.StatusOK, body)
}

func sendError(w http.ResponseWriter, status int, code, detail string) {
	summary := "The local operation could not be completed."
	suggestion := "Open Diagnostics and inspect the failed stage. If it repeats, report the error code and run ID."
	retryable := false
	if code == "RESOURCE_NOT_FOUND" {
		summary = "The requested local resource was not found."
		suggestion = "Reload the application and select an available source or review."
		retryable = true
	}
	// These fixed strings cannot fail JSON encoding. They match the source's
	// describe_error fallback; LOCAL_API_ACCESS_DENIED has no dedicated entry.
	body, _ := json.Marshal(map[string]any{
		"error": summary, "technical_message": detail, "error_code": code,
		"summary": summary, "suggestion": suggestion, "severity": "error", "retryable": retryable,
	})
	sendBytes(w, status, body)
}

func sendBytes(w http.ResponseWriter, status int, body []byte) {
	sendTypedBytes(w, status, body, "application/json; charset=utf-8")
}

func sendTypedBytes(w http.ResponseWriter, status int, body []byte, contentType string) {
	header := w.Header()
	header.Set("Content-Type", contentType)
	header.Set("Content-Length", strconv.Itoa(len(body)))
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", contentSecurityPolicy)
	header.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	// A failed/partial network write cannot be replaced with another response.
	// Do not retry, append diagnostics, or disclose the underlying write error.
	_, _ = w.Write(body)
}
