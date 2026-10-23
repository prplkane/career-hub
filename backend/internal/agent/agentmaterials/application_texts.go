package agentmaterials

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"unicode/utf8"

	"careerhub.local/backend/internal/materials"
	"careerhub.local/backend/internal/textcompat"
)

var textItemID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,79}$`)

// This client forwards the original bounded JSON bytes to the owner. It neither
// imports the application-text writer nor rewrites/repairs the draft, so the
// server can reject lossy Unicode even in overwritten duplicate fields.
func (c *Client) saveApplicationText(ctx context.Context, r Request) ([]byte, *Error) {
	if r.Approve || r.ExpectedRevision != 0 || r.FileFormat != "" || r.Kind != "" ||
		materials.ValidateRequest(materials.Request{JobID: r.JobID, Kind: "cover_letter", Operation: "load"}) != nil ||
		len(r.Payload) == 0 || len(r.Payload) > MaxPayloadBytes || !utf8.Valid(r.Payload) || !json.Valid(r.Payload) {
		return nil, problem("AGENT_MATERIAL_REQUEST_INVALID")
	}
	var fields map[string]json.RawMessage
	var id, kind string
	if json.Unmarshal(r.Payload, &fields) != nil || fields == nil || json.Unmarshal(fields["id"], &id) != nil || json.Unmarshal(fields["kind"], &kind) != nil {
		return nil, problem("AGENT_MATERIAL_REQUEST_INVALID")
	}
	id, kind = textcompat.Strip(id), textcompat.Strip(kind)
	if !textItemID.MatchString(id) || !textKind(kind) {
		return nil, problem("AGENT_MATERIAL_REQUEST_INVALID")
	}
	token, e := c.sessionFor(ctx, "application_texts")
	if e != nil {
		return nil, e
	}
	raw, e := c.request(ctx, "POST", "/api/jobs/"+url.PathEscape(r.JobID)+"/application-texts", token, r.Payload, 8192)
	if e != nil {
		return nil, e
	} // Never retry an uncertain publication.
	var receipt struct {
		SchemaVersion int    `json:"schema_version"`
		Result        string `json:"result"`
		JobID         string `json:"job_id"`
		ItemID        string `json:"item_id"`
		Kind          string `json:"kind"`
		State         string `json:"state"`
		ArtifactPath  string `json:"artifact_path"`
	}
	if json.Unmarshal(raw, &receipt) != nil || receipt.SchemaVersion != 1 || receipt.JobID != r.JobID || receipt.ItemID != id || receipt.Kind != kind || receipt.State != "draft" || receipt.ArtifactPath != "outputs/application_texts/"+r.JobID+"/application_texts.yaml" ||
		(receipt.Result != "created" && receipt.Result != "updated" && receipt.Result != "no_change_needed") {
		return nil, problem("AGENT_MATERIAL_RESPONSE_INVALID")
	}
	// Source receipt has no material-editor contract_version or revision envelope.
	return compactUTF8(receipt)
}

func textKind(kind string) bool {
	switch kind {
	case "cover_letter", "short_answer", "long_answer", "recruiter_message", "follow_up", "other":
		return true
	}
	return false
}
