package agentmaterials

import (
	"context"
	"encoding/json"

	"careerhub.local/backend/internal/agentcontext"
	"careerhub.local/backend/internal/jobcard"
)

type AnalyzeInput struct {
	Source       *agentcontext.SourceRequest
	RegistryMode string
}

func (c *Client) analyze(ctx context.Context, r Request) ([]byte, *Error) {
	v := r.Analysis
	if c == nil || v == nil || r.Context != nil || r.Submission != nil || r.Vocabulary != nil || r.Kind != "" || r.FileFormat != "" || r.Approve || r.ExpectedRevision != 0 || len(r.Payload) > 0 || v.RegistryMode != "off" && v.RegistryMode != "exact-auto" {
		return nil, problem("AGENT_CONTEXT_REQUEST_INVALID")
	}
	wire := map[string]string{"registry_mode": v.RegistryMode}
	if v.Source != nil {
		if r.JobID != "" {
			return nil, problem("AGENT_CONTEXT_REQUEST_INVALID")
		}
		if e := agentcontext.ValidateSourceRequest(*v.Source); e != nil {
			return nil, problem(e.Code)
		}
		wire["source_text"], wire["company"], wire["title"], wire["source_url"] = v.Source.Text, v.Source.Company, v.Source.Title, v.Source.SourceURL
	} else {
		if !jobcard.ValidID(r.JobID) {
			return nil, problem("AGENT_CONTEXT_REQUEST_INVALID")
		}
		wire["job_id"] = r.JobID
	}
	body, e := json.Marshal(wire)
	if e != nil {
		return nil, problem("AGENT_CONTEXT_REQUEST_INVALID")
	}
	token, p := c.sessionFor(ctx, "agent_analyze")
	if p != nil {
		return nil, p
	}
	raw, p := c.request(ctx, "POST", "/api/agent/analyze", token, body, 4<<20)
	if p != nil {
		return nil, p
	}
	var validation *agentcontext.Error
	if v.Source != nil {
		validation = agentcontext.ValidateSourceResponse(ctx, raw, *v.Source, v.RegistryMode)
	} else {
		validation = agentcontext.ValidateSavedResponse(ctx, raw, r.JobID, "assessment", v.RegistryMode, nil)
	}
	if validation != nil {
		return nil, problem(validation.Code)
	}
	return raw, nil
}
