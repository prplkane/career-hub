package applicationtexts

import (
	"context"
	"database/sql"
	"encoding/json"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/materials"
)

// CopyDraft validates even caller-constructed Draft values and detaches lists.
func CopyDraft(d Draft) (Draft, *Error) {
	if len(d.EvidenceRefs) > 100 || len(d.ManualChecks) > 100 {
		return Draft{}, invalid()
	}
	list := func(values []string) []any {
		out := make([]any, len(values))
		for i, v := range values {
			out[i] = v
		}
		return out
	}
	number := func(n *json.Number) any {
		if n == nil {
			return nil
		}
		return *n
	}
	return NormalizeDraft(map[string]any{"id": d.ID, "kind": d.Kind, "source": d.Source, "exact_prompt": d.ExactPrompt,
		"response": d.Response, "state": d.State, "limits": map[string]any{"characters": number(d.Limits.Characters), "words": number(d.Limits.Words)},
		"evidence_refs": list(d.EvidenceRefs), "manual_checks": list(d.ManualChecks)})
}

// PrepareSave reads only through the owning service's observed file/SQL snapshot.
// The returned replacement is not publication authority outside that owner.
func PrepareSave(ctx context.Context, tx *sql.Tx, source jobcard.Source, job string, input Draft) (*materials.Proposal, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, problem("CANCELED")
	}
	if tx == nil || source == nil {
		return nil, problem("SOURCE_REQUIRED")
	}
	if materials.ValidateRequest(materials.Request{JobID: job, Kind: "cover_letter", Operation: "load"}) != nil {
		return nil, problem("JOB_ID_INVALID")
	}
	draft, err := CopyDraft(input)
	if err != nil {
		return nil, err
	}
	card, ce := jobcard.Read(ctx, source, "jobs/normalized/"+job+".yaml")
	if ce != nil || !card.Report.SchemaValid() || !stringIs(field(card.Card, "id"), job) {
		return nil, problem("JOB_INVALID")
	}
	status, _, ae := applications.ResumePlanApplicationInTransaction(ctx, tx, job)
	if ae != nil {
		return nil, problem("APPLICATION_UNAVAILABLE")
	}
	if status != "preparing" && status != "applied" && status != "interview" && status != "offer" {
		return nil, problem("APPLICATION_NOT_READY")
	}
	fingerprint := field(card.Card, "content_fingerprint")
	if fingerprint.Kind != "string" || fingerprint.Text == "" {
		return nil, problem("JOB_INVALID")
	}
	path := "outputs/application_texts/" + job + "/application_texts.yaml"
	raw, present, re := source.ReadFile(path, MaxStoreBytes)
	if re != nil {
		return nil, problem("STORE_UNAVAILABLE")
	}
	after, result, err := updateWorkbench(ctx, raw, present, job, fingerprint.Text, draft)
	if err != nil {
		return nil, err
	}
	receipt, e := json.Marshal(map[string]any{"schema_version": 1, "result": result, "job_id": job,
		"item_id": draft.ID, "kind": draft.Kind, "state": draft.State, "artifact_path": path})
	if e != nil {
		return nil, problem("RESPONSE_INVALID")
	}
	proposal := &materials.Proposal{Result: receipt}
	if result != "no_change_needed" {
		proposal.Path, proposal.Bytes = path, after
	}
	return proposal, nil
}
