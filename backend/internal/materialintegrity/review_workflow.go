package materialintegrity

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"

	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/materials"
)

type savedReview struct {
	payload    json.RawMessage
	revision   int64
	source     *materials.Sources
	policy     *FactPolicy
	info       PolicyInfo
	baseline   *DraftAssessment
	projection *DraftClaims
}

func readReview(ctx context.Context, tx *sql.Tx, resources, data jobcard.Source, r ReviewRequest, clock string) (*savedReview, *Error) {
	if e := ValidateReviewRequest(r); e != nil {
		return nil, e
	}
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("MATERIAL_INTEGRITY_CANCELED")
	}
	if tx == nil || resources == nil || data == nil {
		return nil, failure("MATERIAL_REVIEW_SOURCE_REQUIRED")
	}
	source, me := materials.ReadSources(ctx, tx, data, r.JobID, r.Kind == "resume", clock)
	if me != nil {
		return nil, failure(me.Code)
	}
	payload, revision, me := materials.ReadSavedDraft(ctx, data, r.Kind, r.JobID)
	if me != nil {
		return nil, failure(me.Code)
	}
	job, je := jobcard.Read(ctx, data, "jobs/normalized/"+r.JobID+".yaml")
	if je != nil {
		return nil, failure(je.Code)
	}
	if !job.RawPresent || !job.Report.SchemaValid() {
		return nil, failure("MATERIAL_FACT_JD_REQUIRED")
	}
	registry, ce := catalogstore.ReadState(resources, data)
	if ce != nil {
		return nil, failure("MATERIAL_FACT_REGISTRY_INVALID")
	}
	policy, info, e := DefaultFactPolicy(ctx, registry.Snapshot, *source, job.RawText, nil)
	if e != nil {
		return nil, e
	}
	assessDraft := AssessResume
	if r.Kind == "cover_letter" {
		assessDraft = AssessCover
	}
	baseline, e := assessDraft(ctx, policy, payload, revision, *source, nil)
	if e != nil {
		return nil, e
	}
	var projection *DraftClaims
	if r.Kind == "resume" {
		projection, e = ResumeClaims(payload)
	} else {
		d, _ := materials.ParseCover(payload) // validated by AssessCover above
		defaults, me := materials.DefaultCover(*source, d.Content.Date)
		if me != nil {
			return nil, failure(me.Code)
		}
		raw, me := materials.JSON(defaults)
		if me != nil {
			return nil, failure(me.Code)
		}
		projection, e = CoverClaims(payload, raw)
	}
	if e != nil {
		return nil, e
	}
	return &savedReview{payload, revision, source, policy, info, baseline, projection}, nil
}

func prepareReview(ctx context.Context, tx *sql.Tx, resources, data jobcard.Source, r ReviewRequest, clock string) (*savedReview, *reviewChange, *DraftAssessment, *Error) {
	saved, e := readReview(ctx, tx, resources, data, r, clock)
	if e != nil {
		return nil, nil, nil, e
	}
	path := reviewPath(r.JobID, r.Kind)
	raw, present, pe := data.ReadFile(path, MaxReviewBytes)
	if pe != nil {
		return nil, nil, nil, failure(pe.Code)
	}
	change, e := planReview(ctx, r, saved.baseline, saved.projection, raw, present, clock)
	if e != nil {
		return nil, nil, nil, e
	}
	assessment := saved.baseline
	if len(change.checks) > 0 {
		assessDraft := AssessResume
		if r.Kind == "cover_letter" {
			assessDraft = AssessCover
		}
		assessment, e = assessDraft(ctx, saved.policy, saved.payload, saved.revision, *saved.source, change.checks)
		if e != nil {
			return nil, nil, nil, e
		}
	}
	return saved, change, assessment, nil
}

func reviewPath(job, kind string) string {
	return "outputs/application_materials/" + job + "/fact_reviews_" + kind + ".json"
}

// PrepareReview borrows the material owner's coherent SQL/file lifetime. Sources,
// draft, policy and current checks are loaded here, never supplied by the client.
// Confirm/revoke are explicit first-party user operations, not agent-autonomous
// permissions. HTTP authentication/CSRF and UI confirmation remain adapter duties.
func PrepareReview(ctx context.Context, tx *sql.Tx, resources, data jobcard.Source, r ReviewRequest, clock string) (*materials.Proposal, *Error) {
	saved, change, assessment, e := prepareReview(ctx, tx, resources, data, r, clock)
	if e != nil {
		return nil, e
	}
	change.view.Policy, change.view.Assessment = saved.info, *assessment
	result, e := encodeReviewView(change.view)
	if e != nil {
		return nil, e
	}
	proposal := &materials.Proposal{Result: result}
	if len(change.bytes) != 0 {
		proposal.Path, proposal.Bytes = reviewPath(r.JobID, r.Kind), change.bytes
	}
	return proposal, nil
}

// SavedAssessment is private application data derived under the caller's SQL/
// file observation lifetime, not an API-supplied authorization object.
type SavedAssessment struct {
	Payload    json.RawMessage
	Assessment DraftAssessment
}

func (SavedAssessment) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted saved material assessment]")
}

// ReadAssessment reuses the exact review-store admission and current-token
// selection used by explicit review. It cannot create or confirm any check.
func ReadAssessment(ctx context.Context, tx *sql.Tx, resources, data jobcard.Source, job, kind, clock string) (*SavedAssessment, *Error) {
	r := ReviewRequest{Operation: "inspect", JobID: job, Kind: kind}
	saved, _, assessment, e := prepareReview(ctx, tx, resources, data, r, clock)
	if e != nil {
		return nil, e
	}
	return &SavedAssessment{saved.payload, *assessment}, nil
}
