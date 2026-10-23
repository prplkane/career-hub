package resumeplan

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/intake/resumelibrary"
)

type Request struct {
	Operation        string
	JobID            string
	Payload          json.RawMessage `json:"Payload,omitempty"`
	ExpectedRevision int64
	Approve          bool
}

func (Request) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted resume plan request]")
}

type storedPlan struct {
	JSON     string `json:"plan_json"`
	Status   string `json:"status"`
	Revision int64  `json:"revision"`
}

// ApplyInTransaction never commits or opens a nested connection. The owner
// captures all source reads and builds this complete response before committing.
// A missing database must be prepared explicitly, not by an editor read.
func ApplyInTransaction(ctx context.Context, tx *sql.Tx, source Source, r Request, clock string, entropy resumelibrary.Entropy) (*applications.MutationResult, *Error) {
	if !slices.Contains([]string{"load", "default", "save"}, r.Operation) || r.Operation != "save" && (len(r.Payload) > 0 || r.Approve || r.ExpectedRevision != 0) {
		return nil, failure("request", "RESUME_PLAN_REQUEST_INVALID")
	}
	if e := applications.ValidateSummaryRequest([]string{r.JobID}); e != nil {
		return nil, &Error{Stage: e.Stage, Code: e.Code}
	}
	status, fp, ae := applications.ResumePlanApplicationInTransaction(ctx, tx, r.JobID)
	if ae != nil {
		return nil, &Error{Stage: ae.Stage, Code: ae.Code}
	}
	if !slices.Contains([]string{"captured", "reviewed", "preparing", "applied", "interview", "offer", "rejected", "withdrawn", "closed"}, status) {
		return nil, failure("application", "RESUME_PLAN_APPLICATION_NOT_READY")
	}
	if !hash(fp, "") {
		return nil, failure("application", "RESUME_PLAN_JOB_FINGERPRINT_INVALID")
	}
	c, e := ReadCatalog(ctx, source, r.JobID)
	if e != nil {
		return nil, e
	}
	if r.Operation == "default" {
		raw, e := JSON(Default(r.JobID, fp, *c))
		if e != nil {
			return nil, e
		}
		return &applications.MutationResult{Result: raw}, nil
	}
	command := func(operation string, fields map[string]any) (*resumelibrary.Result, *Error) {
		out, e := resumelibrary.ApplyInTransaction(ctx, tx, resumelibrary.Request{Operation: operation, Fields: fields}, clock, entropy, source)
		if e != nil {
			return nil, &Error{Stage: e.Stage, Code: e.Code, SourceClass: e.SourceClass}
		}
		return out, nil
	}
	out, e := command("get_plan", map[string]any{"job_id": r.JobID})
	if e != nil {
		return nil, e
	}
	var record *storedPlan
	if json.Unmarshal(out.Mutation.Result, &record) != nil {
		return nil, failure("record", "RESUME_PLAN_RECORD_INVALID")
	}
	revision := int64(0)
	if record != nil {
		revision = record.Revision
	}
	var p *Plan
	if r.Operation == "save" {
		p, e = Parse(r.Payload)
	} else if record != nil {
		p, e = Parse([]byte(record.JSON))
		if e != nil {
			e = failure("record", "RESUME_PLAN_STORED_INVALID")
		}
	} else {
		initial := Default(r.JobID, fp, *c)
		p = &initial
	}
	if e != nil {
		return nil, e
	}
	if p.JobID != r.JobID {
		return nil, failure("validation", "RESUME_PLAN_JOB_MISMATCH")
	}
	if r.Operation == "save" {
		p.Status = "draft"
		if r.Approve {
			p.Status = "approved"
		}
	}
	v, e := Validate(ctx, *p, *c, fp)
	if e != nil {
		return nil, e
	}
	if r.Operation == "save" {
		if v.BlockingCount > 0 {
			return nil, failure("validation", "RESUME_PLAN_REFERENCES_INVALID")
		}
		if r.Approve && !v.Ready {
			return nil, failure("validation", "RESUME_PLAN_NOT_READY")
		}
		planJSON, e := PlanJSON(*p)
		if e != nil {
			return nil, e
		}
		if len(planJSON) > MaxPlanBytes {
			return nil, failure("input", "RESUME_PLAN_INPUT_LIMIT")
		}
		out, e = command("save_plan", map[string]any{"job_id": r.JobID, "plan_json": string(planJSON), "status": p.Status, "expected_revision": r.ExpectedRevision})
		if e != nil {
			return nil, e
		}
		if json.Unmarshal(out.Mutation.Result, &record) != nil || record == nil {
			return nil, failure("record", "RESUME_PLAN_RECORD_INVALID")
		}
		if record.JSON != string(planJSON) || record.Status != p.Status || record.Revision != r.ExpectedRevision+1 {
			return nil, failure("record", "RESUME_PLAN_SAVE_NOT_APPLIED")
		}
		revision = record.Revision
	}
	raw, e := JSON(NewEditor(*p, *c, fp, revision, *v))
	if e != nil {
		return nil, e
	}
	return &applications.MutationResult{Result: raw, Changed: out.Mutation.Changed}, nil
}
