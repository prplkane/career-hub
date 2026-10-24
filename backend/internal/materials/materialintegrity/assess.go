package materialintegrity

import (
	"context"

	"careerhub.local/backend/internal/materials"
)

// DraftAssessment binds the complete typed claim projection and result to the
// manifest sources. It contains no draft or proof prose. It is still not a
// publication receipt or proof that a rendered artifact matches the draft.
type DraftAssessment struct {
	Sources Sources    `json:"sources"`
	Facts   FactResult `json:"facts"`
}

func assess(ctx context.Context, policy *FactPolicy, snapshot Sources, projection *DraftClaims, catalog []materials.Evidence, checks []ManualFactCheck) (*DraftAssessment, *Error) {
	proofs := make([]FactProof, 0, len(catalog))
	selected := map[string]bool{}
	for _, id := range projection.ProofIDs {
		selected[id] = true
	}
	for _, proof := range catalog {
		if selected[proof.ID] {
			proofs = append(proofs, FactProof{ID: proof.ID, Text: proof.Claim})
		}
	}
	snapshot.DraftSHA256, snapshot.ProofIDs, snapshot.ManualCheckIDs = projection.SHA256, projection.ProofIDs, []string{}
	snapshot.PolicySHA256 = policy.SHA256()
	facts, e := CheckFacts(ctx, policy, snapshot, projection.Claims, proofs, checks)
	if e != nil {
		return nil, e
	}
	snapshot.ManualCheckIDs = facts.ManualCheckIDs
	return &DraftAssessment{Sources: snapshot, Facts: *facts}, nil
}

// AssessResume requires sources built by the current Data Home owner and the
// actual stored revision. It revalidates freshness, selection and all controlled
// metadata before deriving claims; callers cannot provide a handpicked list.
func AssessResume(ctx context.Context, policy *FactPolicy, payload []byte, revision int64, source materials.Sources, checks []ManualFactCheck) (*DraftAssessment, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("MATERIAL_INTEGRITY_CANCELED")
	}
	if revision < 1 {
		return nil, failure("MATERIAL_FACT_DRAFT_UNSAVED")
	}
	d, e := materials.ParseResume(payload)
	if e != nil {
		return nil, failure("MATERIAL_FACT_DRAFT_INVALID")
	}
	validation, e := materials.ValidateResume(ctx, *d, source)
	if e != nil || !validation.Ready || d.JobID != source.JobID {
		return nil, failure("MATERIAL_FACT_DRAFT_NOT_READY")
	}
	projection, problem := ResumeClaims(payload)
	if problem != nil {
		return nil, problem
	}
	catalog, e := materials.EvidenceCatalog(source.Catalog)
	if e != nil {
		return nil, failure("MATERIAL_FACT_SOURCE_INVALID")
	}
	planRevision := d.PlanRevision
	snapshot := Sources{JobID: d.JobID, Kind: d.Kind, MaterialID: d.ID, DraftRevision: revision, JobFingerprint: d.JobFingerprint, ProfileRevision: d.ProfileRevision, EvidenceRevision: d.EvidenceRevision, PlanRevision: &planRevision, PlanFingerprint: d.PlanFingerprint, TemplateID: d.TemplateID}
	return assess(ctx, policy, snapshot, projection, catalog, checks)
}

// AssessCover similarly derives every editable field and current credential
// defaults. Existing free-form manual checks satisfy no deterministic token check.
func AssessCover(ctx context.Context, policy *FactPolicy, payload []byte, revision int64, source materials.Sources, checks []ManualFactCheck) (*DraftAssessment, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("MATERIAL_INTEGRITY_CANCELED")
	}
	if revision < 1 {
		return nil, failure("MATERIAL_FACT_DRAFT_UNSAVED")
	}
	d, e := materials.ParseCover(payload)
	if e != nil {
		return nil, failure("MATERIAL_FACT_DRAFT_INVALID")
	}
	catalog, e := materials.EvidenceCatalog(source.Catalog)
	if e != nil {
		return nil, failure("MATERIAL_FACT_SOURCE_INVALID")
	}
	validation, e := materials.ValidateCover(ctx, *d, materials.CoverRevisions{Job: source.JobFingerprint, Profile: source.ProfileRevision, Evidence: source.Catalog.VaultRevision}, catalog)
	if e != nil || !validation.Ready || d.JobID != source.JobID {
		return nil, failure("MATERIAL_FACT_DRAFT_NOT_READY")
	}
	if _, e := materials.CoverPlainText(*d); e != nil {
		return nil, failure("MATERIAL_FACT_DRAFT_INVALID")
	}
	defaults, e := materials.DefaultCover(source, d.Content.Date)
	if e != nil {
		return nil, failure("MATERIAL_FACT_DEFAULTS_STALE")
	}
	rawDefaults, e := materials.JSON(defaults)
	if e != nil {
		return nil, failure("MATERIAL_FACT_DEFAULTS_STALE")
	}
	projection, problem := CoverClaims(payload, rawDefaults)
	if problem != nil {
		return nil, problem
	}
	snapshot := Sources{JobID: d.JobID, Kind: d.Kind, MaterialID: d.ID, DraftRevision: revision, JobFingerprint: d.JobFingerprint, ProfileRevision: d.ProfileRevision, EvidenceRevision: d.EvidenceRevision, TemplateID: d.TemplateID}
	return assess(ctx, policy, snapshot, projection, catalog, checks)
}
