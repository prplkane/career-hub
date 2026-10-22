package agentmaterials

import (
	"bytes"
	"context"
	"encoding/json"
	"unicode/utf8"

	"careerhub.local/backend/internal/agentcontext"
	"careerhub.local/backend/internal/jobcard"
)

type PrepareInput struct {
	ContextInput
	Source                *agentcontext.SourceRequest
	SourceName, PostingID string
}

// Pointers distinguish absent facts from a confirmed incomplete stage.
type PreparationStages struct {
	IntakePhase         string `json:"intake_phase"`
	IntakeComplete      *bool  `json:"intake_complete"`
	PreparationComplete *bool  `json:"preparation_complete"`
	ContextComplete     *bool  `json:"context_complete"`
}

func validPreparationStages(s *PreparationStages) bool {
	if s == nil || s.IntakeComplete == nil || s.PreparationComplete == nil || s.ContextComplete == nil {
		return false
	}
	switch s.IntakePhase {
	case "none", "namespace", "duplicate", "files", "captured", "assessed", "terminal":
	default:
		return false
	}
	return (!*s.PreparationComplete || *s.IntakeComplete) && (!*s.ContextComplete || *s.PreparationComplete)
}

func (c *Client) prepare(ctx context.Context, r Request) ([]byte, *Error) {
	v := r.Preparation
	invalid := func() ([]byte, *Error) { return nil, problem("AGENT_PREPARE_REQUEST_INVALID") }
	if c == nil || v == nil || r.Analysis != nil || r.Context != nil || r.Submission != nil || r.Vocabulary != nil || r.Kind != "" || r.FileFormat != "" || r.Approve || r.ExpectedRevision != 0 || len(r.Payload) > 0 || !agentcontext.ValidPurpose(v.Purpose) || v.RegistryMode != "off" && v.RegistryMode != "exact-auto" {
		return invalid()
	}
	wire := map[string]any{"purpose": v.Purpose, "registry_mode": v.RegistryMode}
	if v.Source == nil {
		if !jobcard.ValidID(r.JobID) || v.SourceName != "" || v.PostingID != "" {
			return invalid()
		}
		wire["job_id"] = r.JobID
	} else {
		if r.JobID != "" || agentcontext.ValidateSourceRequest(*v.Source) != nil || !jobcard.ValidIntakeSourceName(v.SourceName) || !utf8.ValidString(v.PostingID) || len(v.PostingID) > 4096 {
			return invalid()
		}
		wire["source_text"], wire["company"], wire["title"], wire["source_url"] = v.Source.Text, v.Source.Company, v.Source.Title, v.Source.SourceURL
		wire["source_name"], wire["posting_id"] = v.SourceName, v.PostingID
	}
	var task *agentcontext.TaskRequest
	if len(v.TaskRequest) > 0 {
		var e *agentcontext.Error
		task, e = agentcontext.ParseTaskJSON(ctx, v.TaskRequest, v.Purpose)
		if e != nil {
			return nil, problem(e.Code)
		}
		rawTask, e := agentcontext.TaskWireJSON(task)
		if e != nil {
			return nil, problem(e.Code)
		}
		wire["task_request"] = rawTask
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(wire) != nil {
		return invalid()
	}
	token, p := c.sessionFor(ctx, "agent_prepare")
	if p != nil {
		return nil, p
	}
	raw, p := c.request(ctx, "POST", "/api/agent/prepare", token, body.Bytes(), 4<<20)
	if p != nil {
		return nil, p // Never retry: an absent response is not a rollback.
	}
	if e := agentcontext.ValidatePreparationResponse(ctx, raw, r.JobID, v.Purpose, v.RegistryMode, task, v.Source); e != nil {
		return nil, problem("AGENT_PREPARE_RESPONSE_INVALID") // Write state remains unknown.
	}
	return raw, nil
}
