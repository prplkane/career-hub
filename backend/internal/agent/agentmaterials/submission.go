package agentmaterials

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"unicode/utf8"

	"careerhub.local/backend/internal/jobs/jobcard"
)

type SubmissionInput struct {
	ConfirmedSubmitted bool    `json:"confirmed_submitted"`
	ResumeID           *string `json:"resume_id,omitempty"`
	OccurredAt         *string `json:"occurred_at,omitempty"`
	Note               string  `json:"note"`
}

type submittedResume struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}
type submittedResumeEvent struct {
	Action     string           `json:"action"`
	OccurredAt string           `json:"occurred_at"`
	RecordedAt string           `json:"recorded_at"`
	Note       string           `json:"note"`
	Resume     *submittedResume `json:"resume"`
}
type submissionReceipt struct {
	Schema        int    `json:"schema_version"`
	Contract      int    `json:"contract_version"`
	Result        string `json:"result"`
	JobID         string `json:"job_id"`
	Status        string `json:"status"`
	AppliedResume struct {
		Recorded *bool                  `json:"recorded"`
		Resume   *submittedResume       `json:"resume"`
		History  []submittedResumeEvent `json:"history"`
	} `json:"applied_resume"`
	ExternalAction *bool          `json:"external_action_performed_by_tool"`
	State          OperationState `json:"operation_state"`
}

func (c *Client) recordSubmission(ctx context.Context, r Request) ([]byte, *Error) {
	v := r.Submission
	if c == nil || v == nil || !v.ConfirmedSubmitted || !jobcard.ValidID(r.JobID) || r.Vocabulary != nil || r.Kind != "" || r.FileFormat != "" || r.Approve || r.ExpectedRevision != 0 || len(r.Payload) > 0 || !utf8.ValidString(v.Note) {
		return nil, problem("AGENT_SUBMISSION_REQUEST_INVALID")
	}
	for _, f := range []struct {
		value *string
		limit int
	}{{v.ResumeID, 160}, {v.OccurredAt, 256}} {
		if f.value != nil && (!utf8.ValidString(*f.value) || len(*f.value) > f.limit) {
			return nil, problem("AGENT_SUBMISSION_REQUEST_INVALID")
		}
	}
	body, err := json.Marshal(v)
	if err != nil || len(body) > MaxPayloadBytes {
		return nil, problem("AGENT_SUBMISSION_REQUEST_INVALID")
	}
	token, p := c.sessionFor(ctx, "record_submission")
	if p != nil {
		return nil, p
	}
	raw, p := c.request(ctx, "POST", "/api/jobs/"+url.PathEscape(r.JobID)+"/application/record-submission", token, body, 8<<20)
	if p != nil {
		return nil, p
	}
	return checkedSubmissionReceipt(raw, r.JobID)
}

func checkedSubmissionReceipt(raw []byte, jobID string) ([]byte, *Error) {
	var receipt submissionReceipt
	p := problem("AGENT_SUBMISSION_RESPONSE_INVALID")
	if json.Unmarshal(raw, &receipt) != nil {
		return nil, p
	}
	s := receipt.State
	if s.Partial != nil && s.CleanupFailed != nil && (s.Committed != nil || !s.Known) {
		p.State = s
	}
	if receipt.Schema != 1 || receipt.Contract != ContractVersion || receipt.JobID != jobID ||
		!slices.Contains([]string{"recorded", "no_change_needed"}, receipt.Result) ||
		!slices.Contains([]string{"captured", "reviewed", "preparing", "applied", "interview", "offer", "rejected", "withdrawn", "closed"}, receipt.Status) ||
		receipt.ExternalAction == nil || *receipt.ExternalAction || receipt.AppliedResume.Recorded == nil || receipt.AppliedResume.History == nil || len(receipt.AppliedResume.History) > 10000 ||
		!s.Known || s.Committed == nil || s.Partial == nil || *s.Partial || s.CleanupFailed == nil || *s.CleanupFailed || s.RecoveryRequired || s.RevisionRefreshRequired || receipt.Result == "no_change_needed" && *s.Committed {
		return nil, p
	}
	validResume := func(r *submittedResume) bool { return r == nil || len(r.ID) > 0 && len(r.ID) <= 160 }
	if !validResume(receipt.AppliedResume.Resume) {
		return nil, p
	}
	for _, event := range receipt.AppliedResume.History {
		if !slices.Contains([]string{"initial_snapshot", "recorded", "corrected"}, event.Action) || !validResume(event.Resume) || event.OccurredAt == "" || event.RecordedAt == "" {
			return nil, p
		}
	}
	out, err := json.Marshal(receipt)
	if err != nil {
		return nil, p
	}
	return out, nil
}
