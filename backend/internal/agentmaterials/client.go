package agentmaterials

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/materials"
)

const MaxPayloadBytes = 1 << 20

type OperationState struct {
	Known                   bool             `json:"known"`
	Committed               *bool            `json:"committed"`
	Partial                 *bool            `json:"partial"`
	CleanupFailed           *bool            `json:"cleanup_failed"`
	RecoveryRequired        bool             `json:"recovery_required,omitempty"`
	RevisionRefreshRequired bool             `json:"revision_refresh_required,omitempty"`
	Write                   *VocabularyWrite `json:"write,omitempty"`
}

type Client struct {
	origin         string
	http           *http.Client
	vocabularyHTTP *http.Client
	// token is the scoped agent token from the owner-only connection file
	// (ADR SEC-250). No HTTP route returns a token.
	token string
}

// New accepts only one explicit IPv4 loopback origin. It never consults proxy
// environment variables, resolves DNS, follows redirects or discovers profiles.
func New(origin string) (*Client, *Error) {
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, problem("AGENT_LOCAL_ORIGIN_INVALID")
	}
	port, e := strconv.Atoi(u.Port())
	if e != nil || port < 1 || port > 65535 || origin != "http://127.0.0.1:"+strconv.Itoa(port) {
		return nil, problem("AGENT_LOCAL_ORIGIN_INVALID")
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 25 * time.Second, MaxResponseHeaderBytes: 64 << 10}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	longTransport := transport.Clone()
	longTransport.ResponseHeaderTimeout = 40 * time.Second
	longClient := *client
	longClient.Transport, longClient.Timeout = longTransport, 40*time.Second
	return &Client{origin: origin, http: client, vocabularyHTTP: &longClient}, nil
}

var errorCode = regexp.MustCompile(`^(?:MATERIAL|APPLICATION_MATERIAL|APPLICATION_TEXT|RESUME|LOCAL_API|RESOURCE|AGENT|JOB_INTAKE)_[A-Z0-9_]{1,80}$`)

func apiFailure(raw []byte) *Error {
	p := problem("AGENT_MATERIAL_API_FAILED")
	var body struct {
		Code        string             `json:"error_code"`
		State       OperationState     `json:"operation_state"`
		Stages      *VocabularyStages  `json:"stages"`
		Preparation *PreparationStages `json:"preparation_stages"`
		RunID       string             `json:"run_id"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return p
	}
	if errorCode.MatchString(body.Code) {
		p.Code = body.Code
	}
	switch body.Code {
	case "JOB_CARD_NOT_FOUND", "JOB_CARD_INVALID", "JOB_REVISION_CONFLICT", "JOB_REQUEST_INVALID", "JOB_OPERATION_FAILED", "LOCAL_OPERATION_CANCELED", "LOCAL_OPERATION_FAILED", "LOCAL_REQUEST_DEADLINE_FAILED", "LOCAL_RESPONSE_INVALID":
		p.Code = body.Code
	}
	if body.State.Partial != nil && body.State.CleanupFailed != nil && (body.State.Known && body.State.Committed != nil || !body.State.Known && (body.State.Committed == nil || *body.State.Committed)) {
		if w := body.State.Write; w != nil && (w.AppliedFiles < 0 || w.AppliedFiles > 1000 || w.Completed && (!w.IntentRecorded || !w.FilesComplete || !w.JournalRemoved)) {
			body.State.Write = nil
		}
		p.State = body.State
	}
	if validVocabularyStages(body.Stages) {
		p.Stages = body.Stages
	}
	if validPreparationStages(body.Preparation) {
		p.PreparationStages = body.Preparation
	}
	if vocabularyRunID.MatchString(body.RunID) {
		p.RunID = body.RunID
	}
	return p
}

func (c *Client) request(ctx context.Context, method, path, token string, body []byte, limit int64) ([]byte, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, problem("AGENT_MATERIAL_CANCELED")
	}
	req, e := http.NewRequestWithContext(ctx, method, c.origin+path, bytes.NewReader(body))
	if e != nil {
		return nil, problem("AGENT_MATERIAL_REQUEST_INVALID")
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("X-Career-Hub-Token", token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, e := c.http.Do(req)
	if e != nil {
		return nil, problem("AGENT_MATERIAL_TRANSPORT_FAILED")
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil || int64(len(raw)) > limit || !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apiFailure(raw)
	}
	mediaType, _, parseErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if parseErr != nil || mediaType != "application/json" {
		return nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
	}
	return raw, nil
}

func (c *Client) session(ctx context.Context) (string, *Error) {
	return c.sessionFor(ctx, "material_integrity")
}

func (c *Client) sessionFor(ctx context.Context, capability string) (string, *Error) {
	if c.token == "" {
		return "", problem("AGENT_CONNECTION_REQUIRED")
	}
	raw, e := c.request(ctx, "GET", "/api/session", c.token, nil, 8192)
	if e != nil {
		return "", e
	}
	var session struct {
		Client       string                     `json:"client"`
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	var enabled int
	if json.Unmarshal(raw, &session) != nil || session.Client != "browser" || json.Unmarshal(session.Capabilities[capability], &enabled) != nil || enabled != 1 {
		return "", problem("AGENT_MATERIAL_CAPABILITY_REQUIRED")
	}
	return c.token, nil
}

type Request struct {
	Command, JobID, Kind, FileFormat string
	ExpectedRevision                 int64
	Approve                          bool
	Payload                          []byte
	Vocabulary                       *VocabularyInput
	Submission                       *SubmissionInput
	Context                          *ContextInput
	Analysis                         *AnalyzeInput
	Preparation                      *PrepareInput
}

func (Request) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted material-agent request]")
}

// Run never offers review mutation, application submission, a provider call or
// filesystem writes. Only the owner-backed service can perform domain changes.
func (c *Client) Run(ctx context.Context, r Request) ([]byte, *Error) {
	if r.Command == "prepare" {
		return c.prepare(ctx, r)
	}
	if r.Preparation != nil {
		return nil, problem("AGENT_PREPARE_REQUEST_INVALID")
	}
	if r.Command == "analyze" {
		return c.analyze(ctx, r)
	}
	if r.Analysis != nil {
		return nil, problem("AGENT_CONTEXT_REQUEST_INVALID")
	}
	if r.Command == "context" {
		return c.jobContext(ctx, r)
	}
	if r.Context != nil {
		return nil, problem("AGENT_CONTEXT_REQUEST_INVALID")
	}
	if r.Command == "record-submission" {
		return c.recordSubmission(ctx, r)
	}
	if r.Submission != nil {
		return nil, problem("AGENT_SUBMISSION_REQUEST_INVALID")
	}
	if r.Command == "registry-add-exact" {
		return c.addExactVocabulary(ctx, r)
	}
	if r.Vocabulary != nil {
		return nil, problem("AGENT_VOCABULARY_REQUEST_INVALID")
	}
	if r.Command == "save-draft" {
		return c.saveApplicationText(ctx, r)
	}
	if materials.ValidateRequest(materials.Request{JobID: r.JobID, Kind: r.Kind, Operation: "load"}) != nil {
		return nil, problem("AGENT_MATERIAL_REQUEST_INVALID")
	}
	base := "/api/jobs/" + url.PathEscape(r.JobID) + "/materials/" + strings.ReplaceAll(r.Kind, "_", "-")
	method, endpoint := "GET", base
	var body []byte
	switch r.Command {
	case "material-context":
		if r.Approve || len(r.Payload) > 0 || r.FileFormat != "" {
			return nil, problem("AGENT_MATERIAL_REQUEST_INVALID")
		}
	case "save-material":
		if r.FileFormat != "" || r.ExpectedRevision < 0 || len(r.Payload) == 0 || len(r.Payload) > MaxPayloadBytes || !utf8.Valid(r.Payload) || !json.Valid(r.Payload) {
			return nil, problem("AGENT_MATERIAL_REQUEST_INVALID")
		}
		var payload map[string]json.RawMessage
		if json.Unmarshal(r.Payload, &payload) != nil || payload == nil {
			return nil, problem("AGENT_MATERIAL_REQUEST_INVALID")
		}
		draft := json.RawMessage(r.Payload)
		if nested, exists := payload["draft"]; exists {
			draft = nested
		}
		if len(bytes.TrimSpace(draft)) == 0 || bytes.TrimSpace(draft)[0] != '{' {
			return nil, problem("AGENT_MATERIAL_REQUEST_INVALID")
		}
		action := "save_draft"
		if r.Approve {
			action = "approve"
		}
		var e error
		body, e = json.Marshal(map[string]any{"action": action, "expected_revision": r.ExpectedRevision, "draft": draft})
		if e != nil || len(body) > MaxPayloadBytes {
			return nil, problem("AGENT_MATERIAL_REQUEST_INVALID")
		}
		method = "POST"
	case "render-material":
		if r.Approve || len(r.Payload) > 0 || r.FileFormat != "docx" && r.FileFormat != "pdf" && r.FileFormat != "txt" {
			return nil, problem("AGENT_MATERIAL_REQUEST_INVALID")
		}
		body, _ = json.Marshal(map[string]string{"format": r.FileFormat})
		method, endpoint = "POST", base+"/exports"
	default:
		return nil, problem("AGENT_MATERIAL_COMMAND_UNSUPPORTED")
	}
	token, e := c.session(ctx)
	if e != nil {
		return nil, e
	}
	raw, e := c.request(ctx, method, endpoint, token, body, materials.MaxResponseBytes)
	if e != nil {
		return nil, e
	} // Never auto-repeat an uncertain mutation.
	switch r.Command {
	case "material-context":
		return Context(ctx, r.JobID, r.Kind, raw)
	case "save-material":
		return SaveReceipt(ctx, r.JobID, r.Kind, r.ExpectedRevision, raw)
	default:
		return RenderReceipt(r.JobID, r.Kind, r.FileFormat, raw)
	}
}

var artifactID = regexp.MustCompile(`^material_[a-f0-9]{64}$`)
var digest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var filename = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_]*\.(?:pdf|docx|txt)$`)

func RenderReceipt(job, kind, format string, raw []byte) ([]byte, *Error) {
	if materials.ValidateRequest(materials.Request{JobID: job, Kind: kind, Operation: "load"}) != nil || (format != "docx" && format != "pdf" && format != "txt") || len(raw) > materials.MaxResponseBytes || !utf8.Valid(raw) {
		return nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
	}
	// Transport DTO only: the CLI does not link the native writer/renderer.
	var m struct {
		JobID        string          `json:"job_id"`
		Kind         string          `json:"kind"`
		FileFormat   string          `json:"format"`
		Revision     int64           `json:"revision"`
		Filename     string          `json:"filename"`
		ArtifactID   string          `json:"artifact_id"`
		ArtifactPath string          `json:"artifact_path"`
		ManifestPath string          `json:"manifest_path"`
		CreatedAt    string          `json:"created_at"`
		SizeBytes    int64           `json:"size_bytes"`
		SHA256       string          `json:"sha256"`
		Provenance   string          `json:"provenance"`
		Validation   json.RawMessage `json:"validation"`
		Pagination   json.RawMessage `json:"pagination"`
	}
	var validation struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(raw, &m) != nil || json.Unmarshal(m.Validation, &validation) != nil || m.JobID != job || m.Kind != kind || m.FileFormat != format || m.Provenance != "versioned" || !artifactID.MatchString(m.ArtifactID) || !digest.MatchString(m.SHA256) || !filename.MatchString(m.Filename) || !strings.HasSuffix(m.Filename, "."+format) || m.SizeBytes <= 0 || m.Revision < 1 || validation.Status != "pass" && validation.Status != "warning" {
		return nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
	}
	var pagination map[string]json.RawMessage
	if _, e := time.Parse(time.RFC3339Nano, m.CreatedAt); e != nil || !strings.HasSuffix(m.CreatedAt, "Z") || json.Unmarshal(m.Pagination, &pagination) != nil || pagination == nil {
		return nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
	}
	prefix := "outputs/application_materials/" + job + "/exports/v1/" + m.ArtifactID + "/"
	if m.ArtifactPath != prefix+kind+"."+format || m.ManifestPath != prefix+"manifest.json" {
		return nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
	}
	visual := "not_applicable"
	if format == "docx" {
		visual = "unavailable_without_libreoffice"
	}
	// Keep the source receipt's structural/visual keys; expose the stronger Go
	// lineage/fact result separately, without claiming native Word visual approval.
	return compactUTF8(map[string]any{"schema_version": 1, "contract_version": ContractVersion, "result": "rendered", "job_id": job, "kind": kind, "format": format, "revision": m.Revision, "filename": m.Filename, "artifact_path": m.ArtifactPath, "size_bytes": m.SizeBytes, "sha256": m.SHA256, "validation": map[string]string{"structural": "passed", "visual_docx_qa": visual}, "artifact_id": m.ArtifactID, "manifest_path": m.ManifestPath, "provenance": m.Provenance, "created_at": m.CreatedAt, "integrity_validation": m.Validation, "pagination": m.Pagination})
}
