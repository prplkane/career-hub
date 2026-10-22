package agentmaterials

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"

	"careerhub.local/backend/internal/agentcontext"
	"careerhub.local/backend/internal/jobcard"
)

type ContextInput struct {
	Purpose      string          `json:"purpose"`
	RegistryMode string          `json:"registry_mode"`
	TaskRequest  json.RawMessage `json:"task_request,omitempty"`
}

func (c *Client) jobContext(ctx context.Context, r Request) ([]byte, *Error) {
	v := r.Context
	if c == nil || v == nil || !jobcard.ValidID(r.JobID) || r.Submission != nil || r.Vocabulary != nil || r.Kind != "" || r.FileFormat != "" || r.Approve || r.ExpectedRevision != 0 || len(r.Payload) > 0 || !agentcontext.ValidPurpose(v.Purpose) || v.RegistryMode != "off" && v.RegistryMode != "exact-auto" {
		return nil, problem("AGENT_CONTEXT_REQUEST_INVALID")
	}
	var task *agentcontext.TaskRequest
	if len(v.TaskRequest) > 0 {
		var e *agentcontext.Error
		task, e = agentcontext.ParseTaskJSON(ctx, v.TaskRequest, v.Purpose)
		if e != nil {
			return nil, problem(e.Code)
		}
	}
	wire := *v
	var taskErr *agentcontext.Error
	wire.TaskRequest, taskErr = agentcontext.TaskWireJSON(task)
	if taskErr != nil {
		return nil, problem(taskErr.Code)
	}
	var bodyBuffer bytes.Buffer
	encoder := json.NewEncoder(&bodyBuffer)
	encoder.SetEscapeHTML(false)
	e := encoder.Encode(wire)
	body := bytes.TrimSuffix(bodyBuffer.Bytes(), []byte{'\n'})
	if e != nil {
		return nil, problem("AGENT_CONTEXT_REQUEST_INVALID")
	}
	token, p := c.sessionFor(ctx, "agent_context")
	if p != nil {
		return nil, p
	}
	raw, p := c.request(ctx, "POST", "/api/jobs/"+url.PathEscape(r.JobID)+"/agent-context", token, body, 4<<20)
	if p != nil {
		return nil, p
	}
	if e := agentcontext.ValidateSavedResponse(ctx, raw, r.JobID, v.Purpose, v.RegistryMode, task); e != nil {
		return nil, problem(e.Code)
	}
	return raw, nil // Do not reserialize: metrics/hash refer to exact source ordering.
}
