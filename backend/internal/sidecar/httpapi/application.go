package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"careerhub.local/backend/internal/discovery/publicjobresolver"
	"careerhub.local/backend/internal/email/emaildiagnostics"
	"careerhub.local/backend/internal/email/emailimport"
	"careerhub.local/backend/internal/intake/intakeview"
	"careerhub.local/backend/internal/skills/demandgaps"
)

type ApplicationOutcome struct {
	Result json.RawMessage
	// Content is verified binary output for an explicit file route only.
	Content                         []byte
	Committed, CleanupFailed        bool
	Partial                         bool
	RunID                           string
	ReviewValidation                *intakeview.ReviewValidation
	Write                           *ApplicationWriteState
	CommitUnknown, RecoveryRequired bool
	RevisionRefreshRequired         bool
	VocabularyStages                *VocabularyStages
	PreparationStages               *PreparationStages
	PublicResolutionStage           string
	// Created selects the fresh-intake 201 response. It is ignored by every
	// fixed-status application route and never implies that an application was
	// submitted.
	Created bool
}

// Closed facts only; source text, paths, internal receipts and SQL are excluded.
type PreparationStages struct {
	IntakePhase         string `json:"intake_phase"`
	IntakeComplete      bool   `json:"intake_complete"`
	PreparationComplete bool   `json:"preparation_complete"`
	ContextComplete     bool   `json:"context_complete"`
}

// Publication facts contain counts and booleans only, never private paths or
// journal contents. An interrupted multi-file save is not a rolled-back save.
type ApplicationWriteState struct {
	IntentRecorded bool `json:"intent_recorded"`
	AppliedFiles   int  `json:"applied_files"`
	FilesComplete  bool `json:"files_complete"`
	JournalRemoved bool `json:"journal_removed"`
	Completed      bool `json:"completed"`
}
type ApplicationError struct{ Code, Category string }

// ApplicationActions contains fixed first-party callbacks, not a general RPC or
// file-serving capability. The existing native services remain the only writers.
type ApplicationActions struct {
	Static       func(context.Context, string) ([]byte, error)
	Runtime      func(context.Context) (json.RawMessage, error)
	Profile      func(context.Context, string, map[string]any, string) (*ApplicationOutcome, *ApplicationError)
	Policy       func(context.Context, bool, any, string) (*ApplicationOutcome, *ApplicationError)
	Helper       func(context.Context, string, map[string]any) (*ApplicationOutcome, *ApplicationError)
	VaultEdit    func(context.Context, VaultEditRequest) (*ApplicationOutcome, *ApplicationError)
	Competencies func(context.Context, bool) (json.RawMessage, error)
	Demand       func(context.Context, demandgaps.Request) (json.RawMessage, error)
	Feed         func(context.Context, FeedRequest) (*ApplicationOutcome, *ApplicationError)
	Intake       func(context.Context, IntakeRequest) (*ApplicationOutcome, *ApplicationError)
	Jobs         func(context.Context, JobRequest) (*ApplicationOutcome, *ApplicationError)
	// Optional, advertised only when the complete two-owner workflow is wired.
	ExactVocabulary  func(context.Context, ExactVocabularyRequest) (*ApplicationOutcome, *ApplicationError)
	RecordSubmission func(context.Context, RecordSubmissionRequest) (*ApplicationOutcome, *ApplicationError)
	AgentContext     func(context.Context, AgentContextRequest) (*ApplicationOutcome, *ApplicationError)
	AgentAnalyze     func(context.Context, AgentContextRequest) (*ApplicationOutcome, *ApplicationError)
	AgentPrepare     func(context.Context, AgentPrepareRequest) (*ApplicationOutcome, *ApplicationError)
	JobIntake        func(context.Context, JobIntakeRequest) (*ApplicationOutcome, *ApplicationError)
	EmailConnection  func(context.Context) (*ApplicationOutcome, *ApplicationError)
	EmailImport      func(context.Context, *emailimport.Batch) (*ApplicationOutcome, *ApplicationError)
	VaultSetup       func(context.Context, VaultSetupRequest) (*ApplicationOutcome, *ApplicationError)
	EmailConfigure   func(context.Context, EmailConfigureRequest, string) (*ApplicationOutcome, *ApplicationError)
	EmailRetire      func(context.Context, string, int64) (*ApplicationOutcome, *ApplicationError)
	EmailSync        func(context.Context, string, string) (*ApplicationOutcome, *ApplicationError)
	EmailUpdates     func(context.Context, int64, int) (*ApplicationOutcome, *ApplicationError)
	EmailUpdateLink  func(context.Context, EmailUpdateLinkRequest) (*ApplicationOutcome, *ApplicationError)
	EmailCursorReset func(context.Context, int64) (*ApplicationOutcome, *ApplicationError)
	EmailSenderRule  func(context.Context, EmailSenderRuleRequest) (*ApplicationOutcome, *ApplicationError)
	Logs             func(context.Context, string) (*ApplicationOutcome, *ApplicationError)
}

type EmailSenderRuleRequest struct {
	SenderAddress, Disposition, Purpose string
	ExpectedRevision                    int64
}

type EmailConfigureRequest struct {
	Reconnect                      bool // Set only by the explicit reconnect route, never from JSON.
	AccountAddress, Password       string
	PasswordSupplied               bool
	JobSenders, ApplicationSenders []string
	ScheduleEnabled                bool
	IntervalMinutes                int
	ExpectedRevision               int64
}

type EmailUpdateLinkRequest struct {
	OutcomeID, JobID string
	ExpectedRevision int64
}

func NewApplication(port int, version string, registry Registry, vault Vault, documents Documents, materials MaterialActions, actions ApplicationActions) (http.Handler, error) {
	if actions.EmailImport == nil || actions.VaultSetup == nil || actions.Demand == nil {
		return nil, &Error{Code: CodeConfiguration}
	}
	if actions.Static == nil || actions.Runtime == nil || actions.Profile == nil || actions.Policy == nil || actions.Helper == nil || actions.VaultEdit == nil || actions.Competencies == nil || actions.Feed == nil || actions.Intake == nil || actions.Jobs == nil || actions.JobIntake == nil || actions.EmailConnection == nil || actions.EmailConfigure == nil || actions.EmailRetire == nil || actions.EmailSync == nil || actions.EmailUpdates == nil || actions.EmailUpdateLink == nil || actions.EmailCursorReset == nil || actions.EmailSenderRule == nil || actions.Logs == nil {
		return nil, &Error{Code: CodeConfiguration}
	}
	h, e := NewWithMaterials(port, version, registry, vault, documents, materials)
	if e != nil {
		return nil, e
	}
	h.(*handler).application = &actions
	if err := h.(*handler).configureAgent(rand.Reader); err != nil {
		return nil, err
	}
	return h, nil
}

func applicationRoute(path string) bool {
	switch path {
	case "/api/demand-gaps":
		return true
	case "/api/email-connection/reconnect", "/api/discoveries/email-imports", "/api/vault/setup/preview", "/api/vault/setup/import":
		return true
	case "/api/runtime", "/api/competency-explorer", "/api/email-connection", "/api/email-connection/sync", "/api/email-connection/disconnect", "/api/email-connection/forget", "/api/application-email-updates", "/api/application-email-updates/link", "/api/email-connection/reset-cursor", "/api/email-connection/senders", "/api/logs", "/api/vault/profile", "/api/vault/eligibility", "/api/hard-blocker-policy", "/api/application-helper/configuration", "/api/application-demographics":
		return true
	}
	return false
}

func (h *handler) serveApplication(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/api/vault/setup/preview" || path == "/api/vault/setup/import" {
		h.serveVaultSetup(w, r)
		return
	}
	if path == "/api/discoveries/email-imports" {
		h.serveEmailImport(w, r)
		return
	}
	if asset, ok := staticFiles[path]; ok {
		if r.Method != "GET" {
			sendError(w, 404, "RESOURCE_NOT_FOUND", "Route not found")
			return
		}
		h.serveStatic(w, r, asset)
		return
	}
	if path == "/api/email-connection/reconnect" {
		if r.Method != http.MethodPost {
			sendError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Route not found")
			return
		}
		h.serveEmailConfigure(w, r)
		return
	}
	if path == "/api/email-connection" && r.Method == http.MethodPut {
		h.serveEmailConfigure(w, r)
		return
	}
	if path == "/api/email-connection" && (r.Method != http.MethodGet || r.URL.RawQuery != "" || r.URL.ForceQuery) {
		sendError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	if path == "/api/email-connection/reset-cursor" || path == "/api/email-connection/senders" {
		h.serveEmailMutation(w, r)
		return
	}
	if path == "/api/email-connection/disconnect" || path == "/api/email-connection/forget" {
		h.serveEmailRetire(w, r)
		return
	}
	if path == "/api/email-connection/sync" {
		h.serveEmailSync(w, r)
		return
	}
	if path == "/api/logs" {
		h.serveLogs(w, r)
		return
	}
	if path == "/api/application-email-updates" {
		h.serveEmailUpdates(w, r)
		return
	}
	if path == "/api/application-email-updates/link" {
		h.serveEmailUpdateLink(w, r)
		return
	}
	if r.Method != "GET" && r.Method != "PUT" || r.Method == "PUT" && (path == "/api/runtime" || path == "/api/competency-explorer" || path == "/api/demand-gaps") {
		sendError(w, 404, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	// Existing Vault GET routes already have their source-compatible reader.
	if r.Method == "GET" && (path == "/api/vault/profile" || path == "/api/vault/eligibility") {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var fields map[string]json.RawMessage
	if r.Method == "PUT" {
		limit := int64(MaxMaterialRequestBytes)
		if path == "/api/application-helper/configuration" || path == "/api/application-demographics" {
			limit = 16384
		}
		if r.ContentLength > limit {
			applicationFailure(w, &ApplicationOutcome{}, "LOCAL_REQUEST_TOO_LARGE")
			return
		}
		var code string
		fields, code, _ = materialJSONChecked(r, applicationScalarJSON)
		if code != "" {
			applicationFailure(w, &ApplicationOutcome{}, strings.Replace(code, "MATERIAL_REQUEST", "LOCAL_REQUEST", 1))
			return
		}
	}
	var call func() (*ApplicationOutcome, *ApplicationError)
	switch path {
	case "/api/demand-gaps":
		request := demandgaps.Request{From: strings.TrimSpace(materialQuery(r.URL.RawQuery, "from", "")), To: strings.TrimSpace(materialQuery(r.URL.RawQuery, "to", "")), AsOf: strings.TrimSpace(materialQuery(r.URL.RawQuery, "as_of", ""))}
		if request.Validate() != nil {
			sendError(w, 400, "DEMAND_PERIOD_INVALID", "Use valid dates with from on or before to.")
			return
		}
		call = func() (*ApplicationOutcome, *ApplicationError) {
			raw, e := h.application.Demand(ctx, request)
			if e != nil {
				return &ApplicationOutcome{}, &ApplicationError{Code: "DEMAND_PROJECTION_UNAVAILABLE"}
			}
			return &ApplicationOutcome{Result: raw}, nil
		}
	case "/api/email-connection":
		call = func() (*ApplicationOutcome, *ApplicationError) { return h.application.EmailConnection(ctx) }
	case "/api/competency-explorer":
		value := strings.ToLower(materialQuery(r.URL.RawQuery, "include_archived_competencies", "false"))
		call = func() (*ApplicationOutcome, *ApplicationError) {
			raw, e := h.application.Competencies(ctx, value == "true" || value == "1")
			if e != nil {
				return &ApplicationOutcome{}, &ApplicationError{Code: "COMPETENCY_PROJECTION_UNAVAILABLE"}
			}
			return &ApplicationOutcome{Result: raw}, nil
		}
	case "/api/runtime":
		call = func() (*ApplicationOutcome, *ApplicationError) {
			raw, err := h.application.Runtime(ctx)
			if err != nil {
				return nil, &ApplicationError{Code: "WORKSPACE_UNAVAILABLE"}
			}
			return &ApplicationOutcome{Result: raw}, nil
		}
	case "/api/vault/profile", "/api/vault/eligibility":
		var revision string
		var record map[string]any
		if !materialKeys(fields, "expected_revision", "record") || json.Unmarshal(fields["expected_revision"], &revision) != nil || string(fields["expected_revision"]) == "null" || json.Unmarshal(fields["record"], &record) != nil || record == nil {
			applicationFailure(w, &ApplicationOutcome{}, "VAULT_RECORD_VALIDATION_FAILED")
			return
		}
		call = func() (*ApplicationOutcome, *ApplicationError) {
			return h.application.Profile(ctx, strings.TrimPrefix(path, "/api/vault/"), record, revision)
		}
	case "/api/hard-blocker-policy":
		var revision string
		var classes any
		if r.Method == "PUT" && (!materialKeys(fields, "contract_version", "expected_revision", "classes") || string(fields["contract_version"]) != "1" || json.Unmarshal(fields["expected_revision"], &revision) != nil || json.Unmarshal(fields["classes"], &classes) != nil) {
			applicationFailure(w, &ApplicationOutcome{}, "HARD_BLOCKER_POLICY_INVALID")
			return
		}
		call = func() (*ApplicationOutcome, *ApplicationError) {
			return h.application.Policy(ctx, r.Method == "PUT", classes, revision)
		}
	default:
		name, version, value, invalid := "configuration", "2", "enabled_fields", "APPLICATION_HELPER_CONFIGURATION_INVALID"
		if path == "/api/application-demographics" {
			name, version, value, invalid = "demographics", "1", "answers", "APPLICATION_DEMOGRAPHICS_INVALID"
		}
		op := name + ".load"
		payload := map[string]any{}
		if r.Method == "PUT" {
			keys := []string{"contract_version", "expected_revision", value}
			if name == "demographics" {
				keys = append(keys, "section")
				var section string
				if json.Unmarshal(fields["section"], &section) != nil || section != "US" {
					applicationFailure(w, &ApplicationOutcome{}, invalid)
					return
				}
			}
			if !materialKeys(fields, keys...) || string(fields["contract_version"]) != version {
				applicationFailure(w, &ApplicationOutcome{}, invalid)
				return
			}
			for _, key := range []string{"expected_revision", value} {
				var v any
				if json.Unmarshal(fields[key], &v) != nil {
					applicationFailure(w, &ApplicationOutcome{}, invalid)
					return
				}
				payload[key] = v
			}
			op = name + ".save"
		}
		call = func() (*ApplicationOutcome, *ApplicationError) { return h.application.Helper(ctx, op, payload) }
	}
	h.applicationCall(w, ctx, http.StatusOK, call)
}

func (h *handler) serveLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.ForceQuery || r.ContentLength > 0 || len(r.TransferEncoding) != 0 || len(r.URL.RawQuery) > 160 {
		sendError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "Route not found")
		return
	}
	after := "0"
	if r.URL.RawQuery != "" {
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(values) != 1 || len(values["after"]) != 1 || values["after"][0] == "" || len(values["after"][0]) > 128 {
			applicationFailure(w, &ApplicationOutcome{}, "DIAGNOSTIC_CURSOR_INVALID", "diagnostic_read")
			return
		}
		after = values["after"][0]
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	h.applicationCallUnlocked(w, ctx, http.StatusOK, func() (*ApplicationOutcome, *ApplicationError) {
		return h.application.Logs(ctx, after)
	})
}

func (h *handler) applicationCall(w http.ResponseWriter, ctx context.Context, status int, call func() (*ApplicationOutcome, *ApplicationError)) {
	if !applicationResponseDeadline(w, ctx) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.applicationCallUnlockedWithStatus(w, ctx, func(*ApplicationOutcome) int { return status }, call)
}

// Long-running email sync owns its own email gate and must not hold the UI's
// shared read/mutation mutex while a mailbox is being checked.
func (h *handler) applicationCallUnlocked(w http.ResponseWriter, ctx context.Context, status int, call func() (*ApplicationOutcome, *ApplicationError)) {
	if !applicationResponseDeadline(w, ctx) {
		return
	}
	h.applicationCallUnlockedWithStatus(w, ctx, func(*ApplicationOutcome) int { return status }, call)
}

func (h *handler) applicationCallByOutcome(w http.ResponseWriter, ctx context.Context, status func(*ApplicationOutcome) int, call func() (*ApplicationOutcome, *ApplicationError)) {
	if !applicationResponseDeadline(w, ctx) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.applicationCallUnlockedWithStatus(w, ctx, status, call)
}

// Called after route/auth/body validation and before waiting for the UI gate.
// Routes supply bounded contexts (currently at most 150s); the listener's 10s
// default would otherwise discard a successful long operation's response.
// Body/startup limits stay unchanged. Reserve 5s to return cancellation/partial
// publication facts, and refuse a deadline setup failure before any effects.
func applicationResponseDeadline(w http.ResponseWriter, ctx context.Context) bool {
	if ctx.Err() != nil {
		applicationFailure(w, &ApplicationOutcome{}, "LOCAL_OPERATION_CANCELED")
		return false
	}
	deadline, bounded := ctx.Deadline()
	if !bounded {
		applicationFailure(w, &ApplicationOutcome{}, "LOCAL_REQUEST_DEADLINE_FAILED")
		return false
	}
	if err := http.NewResponseController(w).SetWriteDeadline(deadline.Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		applicationFailure(w, &ApplicationOutcome{}, "LOCAL_REQUEST_DEADLINE_FAILED")
		return false
	}
	return true
}

func (h *handler) applicationCallUnlockedWithStatus(w http.ResponseWriter, ctx context.Context, status func(*ApplicationOutcome) int, call func() (*ApplicationOutcome, *ApplicationError)) {
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
	out, err := call()
	if err != nil {
		applicationFailure(w, out, err.Code, err.Category)
		return
	}
	if ctx.Err() != nil {
		applicationFailure(w, out, "LOCAL_OPERATION_CANCELED")
		return
	}
	if out == nil || len(out.Result) > MaxResponseBytes || !materialJSONObject(out.Result) {
		applicationFailure(w, out, "LOCAL_RESPONSE_INVALID")
		return
	}
	selected := status(out)
	if selected != http.StatusOK && selected != http.StatusCreated {
		applicationFailure(w, out, "LOCAL_RESPONSE_INVALID")
		return
	}
	sendBytes(w, selected, out.Result)
}

func applicationFailure(w http.ResponseWriter, out *ApplicationOutcome, code string, category ...string) {
	status := http.StatusInternalServerError
	switch code {
	case "PUBLIC_JOB_RESOLUTION_STALE", "PUBLIC_JOB_RESOLUTION_NOT_ELIGIBLE":
		status = 409
	case "PUBLIC_JOB_RESOLUTION_DIAGNOSTIC_FAILED":
		status = 503
	case "PUBLIC_JOB_RESOLUTION_FAILED":
		status = 500
		if out != nil && (out.PublicResolutionStage == "http" || out.PublicResolutionStage == "response") {
			status = 422
		}
	case "PUBLIC_JOB_RESOLUTION_URL_REJECTED", "PUBLIC_JOB_RESOLUTION_PROTECTED_SOURCE", "PUBLIC_JOB_RESOLUTION_DNS_REJECTED", "PUBLIC_JOB_RESOLUTION_CONNECT_FAILED", "PUBLIC_JOB_RESOLUTION_TLS_FAILED", "PUBLIC_JOB_RESOLUTION_TIMEOUT", "PUBLIC_JOB_RESOLUTION_REDIRECT_REJECTED", "PUBLIC_JOB_RESOLUTION_TOO_MANY_REDIRECTS", "PUBLIC_JOB_RESOLUTION_HTTP_UNAVAILABLE", "PUBLIC_JOB_RESOLUTION_RATE_LIMITED", "PUBLIC_JOB_RESOLUTION_RESPONSE_TOO_LARGE", "PUBLIC_JOB_RESOLUTION_CONTENT_UNSUPPORTED", "PUBLIC_JOB_RESOLUTION_CONTENT_NOT_FOUND", "PUBLIC_JOB_RESOLUTION_CONTENT_AMBIGUOUS":
		status = 422
	case "AGENT_CONTEXT_REQUEST_INVALID", "AGENT_PREPARE_REQUEST_INVALID", "AGENT_PREPARE_SOURCE_BINDING_REQUIRED":
		status = 400
	case "AGENT_PREPARE_UNAVAILABLE", "AGENT_PREPARE_CONTEXT_UNAVAILABLE", "AGENT_PREPARE_CONTEXT_CLEANUP_FAILED":
		status = 503
	case "AGENT_PREPARE_SOURCE_CHANGED", "AGENT_PREPARE_CONTEXT_CHANGED", "AGENT_PREPARE_INTAKE_UNCONFIRMED":
		status = 409
	case "AGENT_CONTEXT_UNAVAILABLE", "AGENT_CONTEXT_CLEANUP_FAILED":
		status = 503
	case "AGENT_CONTEXT_SOURCE_CHANGED", "AGENT_CONTEXT_SCHEMA_UNSUPPORTED", "AGENT_CONTEXT_JOB_CARD_INVALID", "AGENT_CONTEXT_EVIDENCE_VAULT_INVALID", "AGENT_CONTEXT_APPLICATION_TEXT_STORE_IDENTITY_INVALID", "AGENT_CONTEXT_POLICY_INVALID":
		status = 409
	case "AGENT_CONTEXT_JOB_CARD_NOT_FOUND":
		status = 404
	case "AGENT_CONTEXT_CANCELED":
		status = 408
	case "AGENT_SUBMISSION_REQUEST_INVALID", "AGENT_VOCABULARY_REQUEST_INVALID", "AGENT_VOCABULARY_CONFIRMATION_REQUIRED", "AGENT_VOCABULARY_NAME_INVALID", "AGENT_VOCABULARY_LINE_INVALID":
		status = 400
	case "AGENT_VOCABULARY_JOB_INVALID", "AGENT_VOCABULARY_SOURCE_INVALID", "AGENT_VOCABULARY_SOURCE_LIMIT", "AGENT_VOCABULARY_NOT_FOUND", "AGENT_VOCABULARY_GROUP_UNAVAILABLE", "AGENT_VOCABULARY_VOCABULARY_COLLISION":
		status = 409
	case "AGENT_VOCABULARY_HOME_UNAVAILABLE", "AGENT_VOCABULARY_UNAVAILABLE":
		status = 503
	case "AGENT_VOCABULARY_CANCELED":
		status = 408
	case "VAULT_SETUP_INVALID", "VAULT_SETUP_SCOPE_INVALID", "VAULT_SETUP_CONFIRMATION_REQUIRED", "VAULT_SETUP_NAME_INVALID":
		status = 400
	case "VAULT_SETUP_FILE_CONFLICT", "VAULT_SETUP_PLAN_CHANGED", "VAULT_SETUP_HOME_CHANGED", "VAULT_SETUP_DESTINATION_CHANGED":
		status = 409
	case "VAULT_SETUP_DESTINATION_UNAVAILABLE", "VAULT_SETUP_PUBLICATION_UNVERIFIED", "VAULT_SETUP_CLOSE_FAILED":
		status = 503
	case "VAULT_SETUP_CANCELED":
		status = 408
	case "EMAIL_IMPORT_PAYLOAD_INVALID", "EMAIL_IMPORT_VERSION_UNSUPPORTED", "EMAIL_IMPORT_FILE_COUNT_INVALID", "EMAIL_IMPORT_TOTAL_TOO_LARGE", "EMAIL_IMPORT_REQUEST_INVALID":
		status = 400
	case "EMAIL_IMPORT_WRITE_UNVERIFIED":
		status = 503
	case "EMAIL_IMPORT_CANCELED":
		status = 408
	case "EXTENSION_CAPTURE_INVALID", "EXTENSION_CAPTURE_CONTRACT_UNSUPPORTED", "EXTENSION_CAPTURE_PROTOCOL_UNSUPPORTED", "APPLICATION_SESSION_INVALID", "APPLICATION_HELPER_REQUEST_INVALID", "APPLICATION_ANSWER_INVALID":
		status = 400
	case "EXTENSION_CAPTURE_IDEMPOTENCY_CONFLICT", "APPLICATION_SESSION_NOT_PENDING", "APPLICATION_SESSION_CLAIM_AMBIGUOUS", "APPLICATION_SESSION_REVISION_CONFLICT", "APPLICATION_SESSION_EVENT_CONFLICT", "APPLICATION_ANSWER_REVISION_CONFLICT", "APPLICATION_HELPER_SOURCE_UNAVAILABLE":
		status = 409
	case "APPLICATION_SESSION_UNAUTHORIZED":
		status = 403
	case "APPLICATION_SESSION_NOT_FOUND":
		status = 404
	case "APPLICATION_SESSION_EXPIRED":
		status = 410
	case "EXTENSION_CAPTURE_FAILED", "EXTENSION_CAPTURE_RESPONSE_INVALID", "APPLICATION_SESSION_FAILED", "APPLICATION_ANSWER_SAVE_FAILED", "APPLICATION_ANSWER_DELETE_FAILED":
		status = 500
	case "EMAIL_CONNECTION_CANCELED":
		status = 408
	case "EMAIL_SYNC_CANCELED":
		status = 408
	case "EMAIL_SYNC_ALREADY_RUNNING":
		status = 409
	case "EMAIL_CREDENTIAL_MISSING", "EMAIL_SYNC_DISABLED", "EMAIL_SYNC_AUTH_FAILED", "EMAIL_SYNC_NETWORK_FAILED", "EMAIL_SYNC_PROTOCOL_FAILED", "EMAIL_SYNC_UIDVALIDITY_INVALID":
		status = 400
	case "EMAIL_SYNC_COMMIT_UNKNOWN":
		status = 503
	case "EMAIL_SYNC_LOCAL_WRITE_FAILED", "EMAIL_SYNC_RESPONSE_TOO_LARGE", "EMAIL_SYNC_FAILED":
		status = 500
	case "EMAIL_DIAGNOSTIC_CANCELED":
		status = 408
	case "EMAIL_DIAGNOSTIC_HOME_CHANGED", "EMAIL_DIAGNOSTIC_DIRECTORY_INVALID", "EMAIL_DIAGNOSTIC_RECOVERY_REQUIRED":
		status = 409
	case "EMAIL_DIAGNOSTIC_OWNERSHIP_REQUIRED", "EMAIL_DIAGNOSTIC_STORE_UNAVAILABLE", "EMAIL_DIAGNOSTIC_COMMIT_UNKNOWN", "EMAIL_DIAGNOSTIC_LIMIT":
		status = 503
	case "EMAIL_DIAGNOSTIC_EVENT_INVALID", "EMAIL_DIAGNOSTIC_CLOSE_FAILED", "EMAIL_DIAGNOSTIC_POSTCOMMIT_FAILED":
		status = 500
	case "JOB_DIAGNOSTIC_CANCELED":
		status = 408
	case "JOB_DIAGNOSTIC_HOME_CHANGED", "JOB_DIAGNOSTIC_DIRECTORY_INVALID", "JOB_DIAGNOSTIC_RECOVERY_REQUIRED", "JOB_DIAGNOSTIC_SEQUENCE_INVALID", "JOB_DIAGNOSTIC_CURSOR_EXPIRED", "JOB_DIAGNOSTIC_CURSOR_INVALID":
		status = 409
	case "JOB_DIAGNOSTIC_OWNERSHIP_REQUIRED", "JOB_DIAGNOSTIC_STORE_UNAVAILABLE", "JOB_DIAGNOSTIC_READ_UNAVAILABLE", "JOB_DIAGNOSTIC_COMMIT_UNKNOWN", "JOB_DIAGNOSTIC_LIMIT":
		status = 503
	case "JOB_DIAGNOSTIC_EVENT_INVALID", "JOB_DIAGNOSTIC_CLOSE_FAILED", "JOB_DIAGNOSTIC_POSTCOMMIT_FAILED":
		status = 500
	case "JOB_REANALYSIS_CANCELED":
		status = 408
	case "JOB_REANALYSIS_REQUEST_INVALID":
		status = 400
	case "JOB_REANALYSIS_REVISION_CONFLICT", "JOB_REANALYSIS_INPUT_CHANGED", "JOB_REANALYSIS_NOT_DRAFT", "JOB_REANALYSIS_RECOVERY_REQUIRED":
		status = 409
	case "JOB_REANALYSIS_SOURCE_INVALID":
		status = 422
	case "JOB_REANALYSIS_HOME_UNAVAILABLE", "JOB_REANALYSIS_PREPARATION_FAILED", "JOB_REANALYSIS_DIAGNOSTIC_FAILED", "JOB_REANALYSIS_WRITE_UNCERTAIN", "JOB_REANALYSIS_COMMITTED_UNVERIFIED", "JOB_REANALYSIS_RESULT_INVALID":
		status = 503
	case "JOB_INTAKE_REQUEST_INVALID":
		status = 400
	case "JOB_INTAKE_SOURCE_INVALID":
		status = 422
	case "JOB_INTAKE_RETRY_CONFLICT", "JOB_INTAKE_DUPLICATE_AMBIGUOUS", "JOB_INTAKE_RECOVERY_REQUIRED":
		status = 409
	case "JOB_INTAKE_DIAGNOSTIC_FAILED", "JOB_INTAKE_RESULT_INVALID", "JOB_INTAKE_UNAVAILABLE", "JOB_INTAKE_STORAGE_UNAVAILABLE":
		status = 503
	case "DIAGNOSTIC_CURSOR_INVALID":
		status = 400
	case "DIAGNOSTIC_CANCELED":
		status = 408
	case "DIAGNOSTIC_HOME_CHANGED", "DIAGNOSTIC_CURSOR_EXPIRED", "DIAGNOSTIC_LEGACY_CHANGED", "DIAGNOSTIC_LEGACY_CORRUPT", "DIAGNOSTIC_LEGACY_LINE_TOO_LARGE", "EMAIL_DIAGNOSTIC_CURSOR_EXPIRED", "EMAIL_DIAGNOSTIC_CURSOR_INVALID":
		status = 409
	case "DIAGNOSTIC_OWNERSHIP_REQUIRED", "DIAGNOSTIC_LEGACY_UNAVAILABLE", "DIAGNOSTIC_LEGACY_LIMIT", "EMAIL_DIAGNOSTIC_READ_UNAVAILABLE":
		status = 503
	case "INTAKE_DIAGNOSTIC_CURSOR_INVALID":
		status = 400
	case "INTAKE_DIAGNOSTIC_CANCELED":
		status = 408
	case "INTAKE_DIAGNOSTIC_HOME_CHANGED", "INTAKE_DIAGNOSTIC_CURSOR_EXPIRED", "INTAKE_DIAGNOSTIC_DIRECTORY_INVALID", "INTAKE_DIAGNOSTIC_RECOVERY_REQUIRED":
		status = 409
	case "INTAKE_DIAGNOSTIC_OWNERSHIP_REQUIRED", "INTAKE_DIAGNOSTIC_READ_UNAVAILABLE":
		status = 503
	case "INTAKE_DIAGNOSTIC_CLOSE_FAILED":
		status = 500
	case "EMAIL_CONNECTION_REVISION_CONFLICT":
		status = 409
	case "EMAIL_CONNECTION_ACCOUNT_IMMUTABLE", "EMAIL_CONNECTION_ALREADY_DISABLED":
		status = 409
	case "EMAIL_CREDENTIAL_RECONNECT_REQUIRED", "EMAIL_CREDENTIAL_RECONNECT_NOT_REQUIRED", "EMAIL_CREDENTIAL_SCOPE_CHANGED", "EMAIL_CREDENTIAL_INTENT_SCOPE_CHANGED":
		status = 409
	case "EMAIL_CREDENTIAL_SCOPE_UNAVAILABLE":
		status = 503
	case "EMAIL_CREDENTIAL_LEGACY_OWNERSHIP_REQUIRED", "EMAIL_CREDENTIAL_DATABASE_STATE_CHANGED", "EMAIL_CREDENTIAL_HOME_CHANGED", "EMAIL_CREDENTIAL_INTENT_CHANGED", "EMAIL_CREDENTIAL_INTENT_HOME_CHANGED", "EMAIL_CREDENTIAL_INTENT_INVALID", "EMAIL_CREDENTIAL_RECOVERY_REQUIRED", "EMAIL_CREDENTIAL_TARGET_COLLISION":
		status = 409
	case "EMAIL_CREDENTIAL_COMMIT_UNCONFIRMED", "EMAIL_CREDENTIAL_OPERATION_UNCONFIRMED", "EMAIL_CREDENTIAL_PUBLICATION_UNCONFIRMED", "EMAIL_CREDENTIAL_INTENT_REMOVE_UNCONFIRMED", "EMAIL_CREDENTIAL_RECOVERY_UNCONFIRMED":
		status = 503
	case "EMAIL_CREDENTIAL_DATABASE_PATH_CHANGED", "EMAIL_CREDENTIAL_DATABASE_UNAVAILABLE", "EMAIL_CREDENTIAL_INTENT_UNAVAILABLE", "EMAIL_CREDENTIAL_PATH_UNAVAILABLE":
		status = 503
	case "EMAIL_CREDENTIAL_OPERATION_CANCELED":
		status = 408
	case "EMAIL_CREDENTIAL_DATABASE_WRITE_FAILED", "EMAIL_CREDENTIAL_PUBLICATION_INVALID", "EMAIL_SECRET_TARGET_GENERATION_FAILED":
		status = 500
	case "EMAIL_CONNECTION_NOT_CONFIGURED":
		status = 400
	case "EMAIL_CONNECTION_COMMIT_UNCONFIRMED", "EMAIL_CONNECTION_OPERATION_UNCONFIRMED":
		status = 503
	case "EMAIL_APPLICATION_UPDATE_INVALID":
		status = 400
	case "EMAIL_APPLICATION_UPDATE_READ_CANCELED":
		status = 408
	case "EMAIL_APPLICATION_UPDATE_READ_UNAVAILABLE":
		status = 503
	case "EMAIL_APPLICATION_UPDATE_READ_FAILED":
		status = 500
	case "EMAIL_APPLICATION_UPDATE_NOT_FOUND":
		status = 404
	case "EMAIL_APPLICATION_UPDATE_REVISION_CONFLICT":
		status = 409
	case "EMAIL_APPLICATION_UPDATE_LINK_CANCELED":
		status = 408
	case "EMAIL_APPLICATION_UPDATE_LINK_UNAVAILABLE", "EMAIL_APPLICATION_UPDATE_LINK_COMMIT_UNCONFIRMED":
		status = 503
	case "EMAIL_APPLICATION_UPDATE_LINK_FAILED":
		status = 500
	case "EMAIL_CONNECTION_INVALID", "EMAIL_CONNECTION_SCHEMA_TOO_NEW", "EMAIL_CONNECTION_PATH_CHANGED", "DATA_HOME_CHANGED", "DATA_HOME_CHANNEL_MISMATCH", "DATA_HOME_NEWER_THAN_APP", "DATA_HOME_MIGRATION_REQUIRED", "DATA_HOME_INPUT_UNSUPPORTED", "INVALID_DATA_HOME":
		status = 409
	case "EMAIL_CONNECTION_DATABASE_UNAVAILABLE", "EMAIL_CONNECTION_SCHEMA_PREPARATION_REQUIRED", "DATA_HOME_OWNERSHIP_REQUIRED", "DATA_HOME_UNAVAILABLE", "DATA_HOME_RECOVERY_REQUIRED", "EMAIL_SECRET_STORE_FAILED":
		status = 503
	case "EMAIL_CONNECTION_FAILED", "EMAIL_CONNECTION_CLEANUP_FAILED", "EMAIL_CONNECTION_RESPONSE_INVALID", "EMAIL_CONNECTION_RESPONSE_LIMIT":
		status = 500
	case "RESUME_MANAGEMENT_INVALID":
		status = 400
	case "RESUME_MANAGEMENT_LIMIT":
		status = 413
	case "RESUME_MANAGEMENT_CANCELED":
		status = 408
	case "RESUME_MANAGEMENT_CHANGED":
		status = 409
	case "RESUME_MANAGEMENT_UNAVAILABLE":
		status = 503
	case "RESUME_MANAGEMENT_UNSAFE":
		status = 422
	case "RESUME_MANAGEMENT_CLEANUP_FAILED", "RESUME_MANAGEMENT_READ_FAILED":
		status = 500
	case "RESUME_DIGEST_REQUEST_INVALID":
		status = 400
	case "RESUME_DIGEST_NOT_FOUND":
		status = 404
	case "RESUME_DIGEST_ARCHIVED", "RESUME_DIGEST_SOURCE_INVALID", "RESUME_DIGEST_INPUT_CHANGED", "RESUME_DIGEST_RECOVERY_REQUIRED":
		status = 409
	case "RESUME_DIGEST_UNSUPPORTED", "RESUME_DIGEST_UNSAFE":
		status = 422
	case "RESUME_DIGEST_LIMIT":
		status = 413
	case "RESUME_DIGEST_CANCELED":
		status = 408
	case "RESUME_DIGEST_UNAVAILABLE", "RESUME_DIGEST_COMPLETED_UNCONFIRMED":
		status = 503
	case "RESUME_DIGEST_CLEANUP_FAILED", "RESUME_DIGEST_FAILED":
		status = 500
	case "SOURCE_UPLOAD_FAILED", "SOURCE_REIMPORT_NOT_FOUND", "SOURCE_UPLOAD_CHECK_FAILED", "INTAKE_QUERY_INVALID", "INTAKE_REVIEW_BUILD_REQUEST_INVALID", "INTAKE_REVIEW_SUGGEST_REQUEST_INVALID":
		status = 400
	case "SOURCE_ALREADY_EXISTS":
		status = 409
	case "SOURCE_UPLOAD_CANCELED":
		status = 408
	case "SOURCE_UPLOAD_UNAVAILABLE", "SOURCE_UPLOAD_DIAGNOSTIC_FAILED", "SOURCE_UPLOAD_WRITE_UNCERTAIN", "SOURCE_UPLOAD_COMMITTED_UNVERIFIED":
		status = 503
	case "SOURCE_UPLOAD_CLEANUP_FAILED":
		status = 500
	case "SOURCE_EXTRACTION_INVALID":
		status = 400
	case "SOURCE_EXTRACTION_LIMIT":
		status = 413
	case "SOURCE_EXTRACTION_CANCELED":
		status = 408
	case "OUTPUT_GUARD_FAILED", "SOURCE_EXTRACTION_CHANGED":
		status = 409
	case "SOURCE_EXTRACTION_UNAVAILABLE", "SOURCE_EXTRACTION_WRITE_UNCERTAIN", "SOURCE_EXTRACTION_COMMITTED_UNVERIFIED", "SOURCE_EXTRACTION_DIAGNOSTIC_FAILED":
		status = 503
	case "SOURCE_EXTRACTION_CLEANUP_FAILED":
		status = 500
	case "REVIEW_UI_SAVE_FAILED", "REVIEW_INTEGRITY_FAILED":
		status = 400
	case "REVIEW_REVISION_CONFLICT", "REVIEW_UI_SAVE_APPLIED_READ_ONLY":
		status = 409
	case "REVIEW_UI_SAVE_CANCELED":
		status = 408
	case "REVIEW_UI_SAVE_UNAVAILABLE", "REVIEW_UI_SAVE_DIAGNOSTIC_FAILED", "REVIEW_UI_SAVE_WRITE_UNCERTAIN", "REVIEW_UI_SAVE_COMMITTED_UNVERIFIED":
		status = 503
	case "REVIEW_UI_SAVE_CLEANUP_FAILED":
		status = 500
	case "INTAKE_EXTRACTION_NOT_FOUND", "INTAKE_REVIEW_NOT_FOUND":
		status = 404
	case "APPROVAL_PREVIEW_INCOMPLETE", "APPROVAL_PREVIEW_INPUT_CHANGED":
		status = 409
	case "APPROVAL_PREVIEW_UNAVAILABLE":
		status = 503
	case "APPROVAL_INVALID", "APPROVAL_NOT_READY", "APPROVAL_TARGET_INVALID", "INVALID_MERGE_TARGET", "INVALID_FIELD_RESOLUTION", "MISSING_APPROVAL_FIELD", "INVALID_COMPETENCY_REFERENCE":
		status = 400
	case "APPROVAL_ALREADY_APPLIED", "APPROVAL_SOURCE_CHANGED", "APPROVAL_RECOVERY_REQUIRED", "VAULT_REVISION_CONFLICT":
		status = 409
	case "APPROVAL_CANCELED":
		status = 408
	case "APPROVED_IMPORT_SOURCE_INVALID", "APPROVED_IMPORT_RESUME_INVALID":
		status = 400
	case "APPROVED_IMPORT_APPROVAL_INVALID", "APPROVED_IMPORT_NOT_APPROVED", "APPROVED_IMPORT_SOURCE_TAMPERED", "APPROVED_IMPORT_RESUME_ARCHIVED", "APPROVED_IMPORT_RESUME_UNAVAILABLE":
		status = 409
	case "APPROVED_IMPORT_SOURCE_MISSING":
		status = 404
	case "APPROVED_IMPORT_CANCELED":
		status = 408
	case "APPROVED_IMPORT_UNAVAILABLE", "APPROVED_IMPORT_DIAGNOSTIC_FAILED":
		status = 503
	case "APPROVED_IMPORT_SOURCE_REMOVE_FAILED", "APPROVED_IMPORT_RESUME_SAVE_FAILED", "APPROVED_IMPORT_CLEANUP_FAILED":
		status = 500
	case "APPROVAL_UNAVAILABLE", "APPROVAL_DIAGNOSTIC_FAILED", "APPROVAL_WRITE_UNCERTAIN", "APPROVAL_COMMITTED_UNVERIFIED", "APPROVAL_JOURNAL_INVALID":
		status = 503
	case "INTAKE_REVIEW_BUILD_REVISION_REQUIRED", "INTAKE_REVIEW_BUILD_REVISION_CONFLICT", "INTAKE_REVIEW_BUILD_INPUT_CHANGED", "INTAKE_REVIEW_BUILD_APPLIED_READ_ONLY",
		"INTAKE_REVIEW_SUGGEST_REVISION_CONFLICT", "INTAKE_REVIEW_SUGGEST_INPUT_CHANGED", "INTAKE_REVIEW_SUGGEST_APPLIED_READ_ONLY", "INTAKE_RUNS_CHANGED":
		status = 409
	case "INTAKE_EXTRACTION_INVALID", "INTAKE_REVIEW_INVALID":
		status = 422
	case "INTAKE_REVIEW_BUILD_CANCELED", "INTAKE_REVIEW_SUGGEST_CANCELED", "INTAKE_READ_CANCELED":
		status = 408
	case "INTAKE_VIEW_RESULT_LIMIT":
		status = 413
	case "SOURCE_UPLOAD_CHECK_UNAVAILABLE", "INTAKE_SOURCES_UNAVAILABLE", "INTAKE_SOURCES_CHANGED", "INTAKE_EXTRACTION_UNAVAILABLE", "INTAKE_REVIEW_UNAVAILABLE", "INTAKE_RUNS_UNAVAILABLE", "INTAKE_REVIEW_BUILD_UNAVAILABLE", "INTAKE_REVIEW_BUILD_WRITE_UNCERTAIN", "INTAKE_REVIEW_BUILD_COMMITTED_UNVERIFIED",
		"INTAKE_REVIEW_SUGGEST_UNAVAILABLE", "INTAKE_REVIEW_SUGGEST_WRITE_UNCERTAIN", "INTAKE_REVIEW_SUGGEST_COMMITTED_UNVERIFIED":
		status = 503
	case "INTAKE_RUNS_CLEANUP_FAILED", "INTAKE_REVIEW_BUILD_CLEANUP_FAILED", "INTAKE_REVIEW_SUGGEST_CLEANUP_FAILED":
		status = 500
	case "CONTACT_REQUEST_INVALID", "CONTACT_CANDIDATE_REQUEST_INVALID", "CONTACT_CANDIDATE_PROTOCOL_UNSUPPORTED", "APPLICATION_RESUME_INVALID":
		status = 400
	case "CONTACT_NOT_FOUND", "CONTACT_CANDIDATE_SOURCE_NOT_FOUND", "APPLICATION_JOB_NOT_FOUND", "APPLICATION_SUBMITTED_RESUME_NOT_FOUND", "APPLICATION_RESUME_NOT_FOUND":
		status = 404
	case "CONTACT_CONFLICT", "CONTACT_CANDIDATE_SOURCE_INCOMPLETE", "CONTACT_CANDIDATE_SOURCE_INVALID", "CONTACT_CANDIDATE_STALE", "CONTACT_EXACT_MATCH_REQUIRES_LINK", "RESUME_PLAN_INVALID", "APPLICATION_SUBMITTED_RESUME_INTEGRITY_FAILED", "APPLICATION_RESUME_INTEGRITY_FAILED":
		status = 409
	case "CONTACT_CANDIDATE_SAVE_UNCONFIRMED":
		status = 503
	case "CONTACT_OPERATION_FAILED", "CONTACT_CANDIDATE_PREVIEW_FAILED", "CONTACT_CANDIDATE_SAVE_FAILED", "RESUME_PLAN_SAVE_FAILED", "APPLICATION_RESUME_SAVE_FAILED", "APPLICATION_SUBMITTED_RESUME_READ_FAILED", "APPLICATION_RESUME_READ_FAILED":
		status = 500
	case "RESUME_LIBRARY_READ_INVALID", "RESUME_LIBRARY_ARCHIVE_INVALID", "RESUME_LIBRARY_RESTORE_INVALID", "RESUME_LIBRARY_UPLOAD_INVALID", "RESUME_VERSION_DELETE_INVALID":
		status = 400
	case "RESUME_LIBRARY_NOT_FOUND":
		status = 404
	case "RESUME_VERSION_FILE_INVALID", "RESUME_VERSION_FILE_MISSING", "RESUME_VERSION_IN_USE", "RESUME_VERSION_IDENTITY_COLLISION":
		status = 409
	case "RESUME_LIBRARY_READ_FAILED", "RESUME_LIBRARY_ARCHIVE_FAILED", "RESUME_LIBRARY_RESTORE_FAILED", "RESUME_LIBRARY_UPLOAD_FAILED", "RESUME_VERSION_DELETE_FAILED":
		status = 500
	case "JOB_QUERY_INVALID", "JOB_REQUEST_INVALID", "JOB_DETAILS_REQUEST_INVALID", "JOB_CARD_INVALID":
		status = 400
	case "JOB_CARD_NOT_FOUND":
		status = 404
	case "JOB_REVISION_CONFLICT", "JOB_DETAILS_REVISION_CONFLICT":
		status = 409
	case "JOB_LIBRARY_UNAVAILABLE", "JOB_LIBRARY_CHANGED", "JOB_DETAILS_UNAVAILABLE", "JOB_DETAILS_SAVE_UNVERIFIED":
		status = 503
	case "JOB_OPERATION_FAILED":
		status = 500
	case "JOB_OPERATION_UNAVAILABLE":
		status = 501
	case "JOB_QUERY_TOO_BROAD":
		status = 422
	case "DISCOVERY_QUERY_INVALID", "DISCOVERY_UPDATE_INVALID":
		status = 400
	case "DISCOVERY_QUERY_TOO_BROAD":
		status = 422
	case "DISCOVERY_REVISION_CONFLICT", "DISCOVERY_FIELD_OVERRIDE_REVISION_CONFLICT", "DISCOVERY_VERIFICATION_NOT_READY":
		status = 409
	case "DISCOVERY_NOT_FOUND":
		status = 404
	case "DISCOVERY_READ_UNAVAILABLE", "DISCOVERY_PROMOTION_DIAGNOSTIC_FAILED":
		status = 503
	case "DISCOVERY_WRITE_FAILED", "DISCOVERY_PROMOTION_FAILED":
		status = 500
	case "VAULT_EDITOR_REVISION_CONFLICT", "HARD_BLOCKER_POLICY_REVISION_CONFLICT", "APPLICATION_HELPER_REVISION_CONFLICT", "APPLICATION_DEMOGRAPHICS_REVISION_CONFLICT":
		status = 409
	case "SKILL_REGISTRY_REVISION_CONFLICT", "CORE_GROUP_READ_ONLY", "CORE_SKILL_READ_ONLY", "SKILL_GROUP_NOT_EMPTY":
		status = 409
	case "VAULT_RECORD_NOT_FOUND":
		status = 404
	case "SKILL_GROUP_NOT_FOUND", "SKILL_NOT_FOUND":
		status = 404
	case "SKILL_REGISTRY_INVALID", "SKILL_GROUP_ARCHIVED", "SKILL_NAME_CONFLICT", "CATALOG_MODEL_INVALID":
		status = 400
	case "VAULT_RECORD_VALIDATION_FAILED", "HARD_BLOCKER_POLICY_INVALID", "APPLICATION_HELPER_CONFIGURATION_INVALID", "APPLICATION_DEMOGRAPHICS_INVALID", "LOCAL_REQUEST_INVALID":
		status = 400
	case "LOCAL_REQUEST_CONTENT_TYPE":
		status = 415
	case "LOCAL_REQUEST_TOO_LARGE":
		status = 413
	case "WORKSPACE_UNAVAILABLE", "HARD_BLOCKER_POLICY_UNAVAILABLE", "COMPETENCY_PROJECTION_UNAVAILABLE", "DEMAND_PROJECTION_UNAVAILABLE":
		status = 503
	case "LOCAL_OPERATION_CANCELED":
		status = 408
	case "VAULT_YAML_INVALID", "VAULT_INPUT_UNSUPPORTED", "VAULT_INPUT_LIMIT", "VAULT_ROOT_TYPE", "VAULT_VALIDATION_UNSUPPORTED", "VAULT_VALIDATION_LIMIT", "VAULT_READ_CANCELED", "VAULT_SCHEMA_VERSION_UNSUPPORTED":
		// Only the setup owner exposes uploaded-input validation failures.
		if len(category) == 0 || category[0] != "vault_setup" {
			code = "LOCAL_OPERATION_FAILED"
		}
	case "VAULT_OPERATION_FAILED", "SKILL_REGISTRY_OPERATION_FAILED", "HARD_BLOCKER_POLICY_SAVE_FAILED", "APPLICATION_HELPER_OPERATION_FAILED", "LOCAL_RESPONSE_INVALID", "LOCAL_OPERATION_FAILED", "LOCAL_REQUEST_DEADLINE_FAILED":
	default:
		code = "LOCAL_OPERATION_FAILED"
	}
	if len(category) != 0 && (category[0] == "email_connection_mutation" || category[0] == "email_sync") && code == "EMAIL_CONNECTION_INVALID" {
		status = http.StatusBadRequest
	}
	if len(category) != 0 && category[0] == "vault_setup" {
		switch code {
		case "VAULT_YAML_INVALID", "VAULT_INPUT_UNSUPPORTED", "VAULT_INPUT_LIMIT", "VAULT_ROOT_TYPE", "VAULT_VALIDATION_UNSUPPORTED", "VAULT_VALIDATION_LIMIT", "VAULT_RECORD_VALIDATION_FAILED", "VAULT_SCHEMA_VERSION_UNSUPPORTED":
			status = http.StatusUnprocessableEntity
		case "VAULT_READ_CANCELED":
			status = http.StatusRequestTimeout
		}
	}
	state := applicationOperationState(out)
	message := "The local operation could not be completed."
	suggestion := "Reload the saved data and review it before retrying. A failed response does not prove that nothing was saved."
	if code == "DEMAND_PROJECTION_UNAVAILABLE" {
		message = "Saved-job demand and Career Vault coverage could not be read safely."
		suggestion = "Reload the saved jobs and Career Vault before retrying. This read does not update either source."
	}
	if strings.HasPrefix(code, "AGENT_CONTEXT_") {
		message = "The saved job context could not be read and verified."
		suggestion = "Review the saved job and inputs before retrying. This read does not update applications or Vault records."
		if code == "AGENT_CONTEXT_CLEANUP_FAILED" {
			suggestion = "Restart Career Hub before retrying this read because resource cleanup could not be confirmed."
		}
	}
	if code == "JOB_OPERATION_UNAVAILABLE" {
		message = "This workflow is unavailable in the running application."
		suggestion = "Restart Career Hub and retry. If it remains unavailable, retain the diagnostic code for investigation."
	}
	if code == "JOB_QUERY_TOO_BROAD" {
		message = "The selected job page exceeds the local response limit."
		suggestion = "Request fewer jobs per page. No truncated cards or partial totals were returned."
	}
	if code == "JOB_DETAILS_REVISION_CONFLICT" {
		message = "The saved job changed before this correction was applied."
		suggestion = "Reload the job and review its latest details before saving again. Do not retry the unseen edit automatically."
	}
	if code == "JOB_DETAILS_SAVE_UNVERIFIED" {
		message = "The job correction did not return a fully verified result."
		suggestion = "Reload the job before taking another action. It may already have been saved; do not retry blindly."
	}
	if strings.HasPrefix(code, "JOB_REANALYSIS_") {
		message = "The saved job analysis could not return a verified result."
		suggestion = "Reload this job and inspect its diagnostic run before trying again. A failed response does not prove that the card was unchanged."
	}
	if code == "JOB_REANALYSIS_REVISION_CONFLICT" || code == "JOB_REANALYSIS_INPUT_CHANGED" {
		message = "The saved job or its analysis inputs changed."
		suggestion = "Reload the job and review the latest card before starting a new rescan."
	}
	if code == "JOB_REANALYSIS_WRITE_UNCERTAIN" || code == "JOB_REANALYSIS_RECOVERY_REQUIRED" {
		message = "Saved job analysis needs recovery review."
		suggestion = "Check the diagnostic run and current saved card. The write state is not safe to infer or retry automatically."
	}
	if code == "JOB_REANALYSIS_COMMITTED_UNVERIFIED" || code == "JOB_REANALYSIS_DIAGNOSTIC_FAILED" && out != nil && out.Committed {
		message = "The saved job may already have been updated."
		suggestion = "Reload the saved card and diagnostic run before any further edit. Do not rescan blindly."
	}
	if strings.HasPrefix(code, "JOB_INTAKE_") {
		message = "The job description could not be saved with a verified result."
		suggestion = "Review this intake run and reload the Job Library before retrying. A failed response may follow partial local changes."
	}
	if code == "JOB_INTAKE_REQUEST_INVALID" || code == "JOB_INTAKE_SOURCE_INVALID" {
		message = "The job description request is invalid."
		suggestion = "Check the source text and intake fields, then submit a new request with one stable retry key."
	}
	if code == "JOB_INTAKE_STORAGE_UNAVAILABLE" {
		message = "Career Hub could not prepare local job storage."
		suggestion = "Open this diagnostic run and check access to the active Data Home. The job description text is not the cause; empty storage folders may have been created."
	}
	if code == "JOB_INTAKE_RETRY_CONFLICT" || code == "JOB_INTAKE_DUPLICATE_AMBIGUOUS" {
		message = "The job description conflicts with existing saved data."
		suggestion = "Review the existing Job Library entries before choosing a new posting or retry identity."
	}
	if code == "JOB_INTAKE_RECOVERY_REQUIRED" || code == "JOB_INTAKE_DIAGNOSTIC_FAILED" && out != nil && out.RecoveryRequired {
		message = "Job intake needs recovery review."
		suggestion = "Inspect the diagnostic run and saved files before any retry. Do not submit the same posting blindly."
	}
	if code == "DISCOVERY_QUERY_TOO_BROAD" {
		message = "Too many matching opportunities to calculate one decision view."
		suggestion = "Narrow the search, source or observed-date filter. No partial counts were returned."
	}
	if code == "DISCOVERY_PROMOTION_DIAGNOSTIC_FAILED" || code == "DISCOVERY_PROMOTION_FAILED" {
		message = "Job promotion did not return a verified completion."
		suggestion = "Check this promotion run and the Job Library before retrying. Intake or application changes may already be saved; a successful child intake does not confirm the complete promotion."
	}
	if strings.HasPrefix(code, "RESUME_MANAGEMENT_") {
		message = "The resume library could not return a verified list."
		suggestion = "Review the diagnostic category and retry the read. No partial list or resume change was returned."
	}
	if strings.HasPrefix(code, "RESUME_DIGEST_") {
		message = "The Resume Version review did not return a verified result."
		suggestion = "Reload the Resume Library and review its current digest state before another action. Do not retry this digest blindly."
	}
	if code == "RESUME_DIGEST_REQUEST_INVALID" || code == "RESUME_DIGEST_UNSUPPORTED" {
		message = "This Resume Version cannot be reviewed with the supplied request."
		suggestion = "Use an active TXT or DOCX Resume Version and start Review again from the Resume Library."
	}
	if code == "RESUME_DIGEST_INPUT_CHANGED" || code == "RESUME_DIGEST_ARCHIVED" || code == "RESUME_DIGEST_SOURCE_INVALID" {
		message = "The Resume Version or its review inputs changed."
		suggestion = "Reload the Resume Library and inspect the current version before starting a new review."
	}
	if code == "RESUME_DIGEST_RECOVERY_REQUIRED" {
		message = "Resume review needs local recovery."
		suggestion = "Restart Career Hub and let the recovery screen finish this exact review. Do not start another digest."
	}
	if code == "RESUME_DIGEST_COMPLETED_UNCONFIRMED" {
		message = "The Resume Version may already have a complete review."
		suggestion = "Reload the Resume Library and open the newest review if it is present. Do not run the digest again blindly."
	}
	if strings.HasPrefix(code, "INTAKE_") {
		message = "The Resume Version review could not return a verified result."
		suggestion = "Reload this review before another action. A failed response does not prove that the local review was unchanged."
	}
	if code == "INTAKE_REVIEW_BUILD_REQUEST_INVALID" {
		message = "The review build request is invalid."
		suggestion = "Reload the Resume Version and submit its current review revision, if one exists."
	}
	if code == "INTAKE_REVIEW_SUGGEST_REQUEST_INVALID" {
		message = "The review suggestion request is invalid."
		suggestion = "Reload the Resume Version and submit the exact current review revision."
	}
	if code == "INTAKE_REVIEW_BUILD_COMMITTED_UNVERIFIED" || code == "INTAKE_REVIEW_BUILD_WRITE_UNCERTAIN" {
		message = "The review may already have been updated."
		suggestion = "Reload the review and inspect its current revision. Do not build it again blindly."
	}
	if code == "INTAKE_REVIEW_SUGGEST_COMMITTED_UNVERIFIED" || code == "INTAKE_REVIEW_SUGGEST_WRITE_UNCERTAIN" {
		message = "Suggested review cards may already have been saved."
		suggestion = "Reload the review and inspect its current revision. Do not suggest cards again blindly."
	}
	if len(category) != 0 && category[0] == "email_connection_read" {
		message = "Email connection settings could not be read safely."
		suggestion = "Review the diagnostic category and retry this read. No email or settings change was made by this request."
	}
	if len(category) != 0 && category[0] == "email_connection_mutation" {
		message = "Email connection settings could not be updated or verified."
		suggestion = "Reload the connection before retrying; this response may follow a committed change."
	}
	if code == "EMAIL_CREDENTIAL_RECONNECT_REQUIRED" {
		message = "This connection needs a new App Password for this local workspace."
		suggestion = "Use Reconnect explicitly. The previous workspace's credential will not be read or removed."
	}
	if code == "EMAIL_CREDENTIAL_RECONNECT_NOT_REQUIRED" {
		message = "This connection is already bound to this workspace."
		suggestion = "Reload the connection. Use the normal settings save to edit it or replace its password."
	}
	if len(category) != 0 && category[0] == "email_application_updates_read" {
		message = "Application email updates could not be read safely."
		suggestion = "Review the diagnostic category and retry this read. No application status was changed by this request."
	}
	if len(category) != 0 && category[0] == "email_application_update_link" {
		message = "The email update could not be linked to a verified application."
		suggestion = "Reload the update and application before retrying. A commit-attempted response does not prove the link was rolled back."
	}
	if len(category) != 0 && category[0] == "email_sync" {
		message = "Email sync did not return a fully verified result."
		suggestion = "Refresh Job Feed and Applications before retrying; some messages may already be imported. Review local diagnostics using the run ID if available."
	}
	if len(category) != 0 && category[0] == "diagnostic_read" {
		message = "Diagnostics could not return a verified page."
		suggestion = "Review the diagnostic error code before restarting this view. No log data was changed by this read."
	}
	if strings.HasPrefix(code, "AGENT_PREPARE_") || len(category) != 0 && category[0] == "agent_prepare" {
		message = "Preparation did not return a verified context packet."
		suggestion = "Inspect the saved job, diagnostic run and operation state before retrying. Earlier preparation stages may already have completed; no application submission is inferred."
	}
	payload := map[string]any{"error": message, "summary": message, "technical_message": "The operation did not return a verified result.", "error_code": code, "severity": "error", "retryable": false, "suggestion": suggestion, "operation_state": state}
	if strings.HasPrefix(code, "PUBLIC_JOB_RESOLUTION_") {
		payload["error"] = "The public posting could not be resolved safely."
		payload["summary"] = "The public posting could not be resolved safely."
		payload["suggestion"] = "Reload this opportunity and inspect the diagnostic run. Open the posting in your browser for attended capture."
		if out != nil && publicjobresolver.ValidStage(out.PublicResolutionStage) {
			payload["stage"] = out.PublicResolutionStage
		}
	}
	if out != nil && out.VocabularyStages != nil {
		payload["stages"] = vocabularyStageState(out.VocabularyStages)
	}
	if out != nil && out.PreparationStages != nil {
		payload["preparation_stages"] = out.PreparationStages
	}
	if code == "REVIEW_INTEGRITY_FAILED" && out != nil && out.ReviewValidation != nil {
		validation, e := json.Marshal(out.ReviewValidation)
		if e == nil && len(validation) <= MaxResponseBytes-4096 {
			payload["validation"] = json.RawMessage(validation)
		} else {
			payload["validation_truncated"] = true
		}
	}
	if out != nil && emaildiagnostics.ValidRunID(out.RunID) {
		payload["run_id"] = out.RunID
	}
	if len(category) != 0 && category[0] != "" {
		payload["diagnostic_category"] = category[0]
	}
	raw, _ := json.Marshal(payload)
	sendBytes(w, status, raw)
}
