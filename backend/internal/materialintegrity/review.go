package materialintegrity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"

	"careerhub.local/backend/internal/materials"
)

type ReviewRequest struct {
	Operation        string   `json:"operation"`
	Kind             string   `json:"kind"`
	JobID            string   `json:"job_id"`
	ExpectedRevision int64    `json:"expected_revision"`
	ReviewSHA256     string   `json:"review_sha256"`
	TargetIDs        []string `json:"target_ids"`
}

func (ReviewRequest) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted material review request]")
}

func ValidateReviewRequest(r ReviewRequest) *Error {
	if e := materials.ValidateRequest(materials.Request{Kind: r.Kind, JobID: r.JobID, Operation: "load"}); e != nil {
		return failure("MATERIAL_REVIEW_REQUEST_INVALID")
	}
	if r.Operation == "inspect" {
		if r.ExpectedRevision != 0 || r.ReviewSHA256 != "" || len(r.TargetIDs) != 0 {
			return failure("MATERIAL_REVIEW_REQUEST_INVALID")
		}
		return nil
	}
	if (r.Operation != "confirm" && r.Operation != "revoke") || r.ExpectedRevision < 0 ||
		!hash(r.ReviewSHA256, "") || len(r.TargetIDs) == 0 || len(r.TargetIDs) > 1000 {
		return failure("MATERIAL_REVIEW_REQUEST_INVALID")
	}
	seen := map[string]bool{}
	for _, id := range r.TargetIDs {
		if len(id) != 69 || id[:5] != "fact_" || !hash(id[5:], "") || seen[id] {
			return failure("MATERIAL_REVIEW_REQUEST_INVALID")
		}
		seen[id] = true
	}
	return nil
}

type ReviewSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Fields are returned once for explicit human review, not duplicated per token.
// They contain private prose and must not be placed in diagnostics or manifests.
type ReviewField struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

func (ReviewField) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted material review field]")
}

type ReviewTarget struct {
	ID            string       `json:"id"`
	DraftPath     string       `json:"draft_path"`
	ClaimSHA256   string       `json:"claim_sha256"`
	TokenSHA256   string       `json:"token_sha256"`
	Spans         []ReviewSpan `json:"spans"`
	State         string       `json:"state"`
	ManualCheckID string       `json:"manual_check_id"`
}

type ReviewView struct {
	ReviewSHA256 string          `json:"review_sha256"`
	Revision     int64           `json:"revision"`
	Policy       PolicyInfo      `json:"policy"`
	Assessment   DraftAssessment `json:"assessment"`
	Fields       []ReviewField   `json:"fields"`
	Targets      []ReviewTarget  `json:"targets"`
	StaleChecks  int             `json:"stale_checks"`
}

func (ReviewView) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "[redacted material review]") }

type reviewChange struct {
	view   *ReviewView
	checks []ManualFactCheck
	bytes  []byte
}

// planReview uses freshly derived findings, not caller-provided claims/checks.
// It prepares at most one canonical local store replacement; it performs no I/O.
func planReview(ctx context.Context, r ReviewRequest, baseline *DraftAssessment, projection *DraftClaims, raw []byte, present bool, clock string) (*reviewChange, *Error) {
	if e := ValidateReviewRequest(r); e != nil {
		return nil, e
	}
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("MATERIAL_INTEGRITY_CANCELED")
	}
	if baseline == nil || projection == nil || baseline.Sources.JobID != r.JobID || baseline.Sources.Kind != r.Kind ||
		baseline.Sources.DraftSHA256 != projection.SHA256 || !equalStrings(baseline.Sources.ProofIDs, projection.ProofIDs) {
		return nil, failure("MATERIAL_REVIEW_SOURCE_INVALID")
	}
	identity, e := ReviewIdentity(baseline.Sources)
	if e != nil {
		return nil, e
	}
	if r.Operation != "inspect" && r.ReviewSHA256 != identity {
		return nil, failure("MATERIAL_REVIEW_STALE")
	}
	store := &reviewStore{SchemaVersion: 1, JobID: r.JobID, Kind: r.Kind, Records: []reviewRecord{}}
	if present {
		store, e = decodeReviewStore(raw, r.JobID, r.Kind)
		if e != nil {
			return nil, e
		}
	} else if len(raw) != 0 {
		return nil, failure("MATERIAL_REVIEW_STORE_INVALID")
	}
	claims := map[string]FactClaim{}
	claimHashes := map[string]string{}
	for _, claim := range projection.Claims {
		claims[claim.Path] = claim
		claimHashes[claim.Path] = digest([]byte(claim.Text))
	}
	view := &ReviewView{ReviewSHA256: identity, Revision: store.Revision, Assessment: *baseline, Fields: []ReviewField{}, Targets: []ReviewTarget{}}
	targets := map[string]int{}
	fields := map[string]bool{}
	for _, finding := range baseline.Facts.Validation.Findings {
		if finding.Code != "UNSUPPORTED_FACT_TOKEN" {
			continue // Placeholders and final-format blocks are never waivable.
		}
		claim, ok := claims[finding.DraftPath]
		if !ok || claim.sourceOnly || finding.DraftStart < 0 || finding.DraftEnd > len(claim.Text) || finding.DraftEnd <= finding.DraftStart {
			return nil, failure("MATERIAL_REVIEW_SOURCE_INVALID")
		}
		check := ManualFactCheck{DraftPath: claim.Path, ClaimSHA256: claimHashes[claim.Path], TokenSHA256: finding.TokenSHA256, ReviewSHA256: identity, Confirmed: true}
		id := reviewTarget(check)
		at, exists := targets[id]
		if !exists {
			at, targets[id] = len(view.Targets), len(view.Targets)
			view.Targets = append(view.Targets, ReviewTarget{ID: id, DraftPath: claim.Path, ClaimSHA256: check.ClaimSHA256, TokenSHA256: check.TokenSHA256, Spans: []ReviewSpan{}, State: "unreviewed"})
		}
		view.Targets[at].Spans = append(view.Targets[at].Spans, ReviewSpan{finding.DraftStart, finding.DraftEnd})
		if !fields[claim.Path] {
			fields[claim.Path] = true
			view.Fields = append(view.Fields, ReviewField{claim.Path, claim.Text})
		}
	}
	active, historical := map[string]int{}, map[string]bool{}
	for i, record := range store.Records {
		target := reviewTarget(record.Check)
		if record.RevokedRevision == 0 && record.Check.ReviewSHA256 == identity {
			if _, exists := targets[target]; !exists {
				return nil, failure("MATERIAL_REVIEW_STORE_INVALID")
			}
		}
		historical[target] = true
		if record.RevokedRevision == 0 {
			active[target] = i
		}
	}
	changes := []string{}
	for _, id := range r.TargetIDs {
		if _, ok := targets[id]; !ok {
			return nil, failure("MATERIAL_REVIEW_TARGET_STALE")
		}
		_, approved := active[id]
		if r.Operation == "confirm" && !approved || r.Operation == "revoke" && approved {
			changes = append(changes, id)
		} else if r.Operation == "revoke" && !historical[id] {
			return nil, failure("MATERIAL_REVIEW_TARGET_UNKNOWN")
		}
	}
	if r.Operation != "inspect" && (r.ExpectedRevision > store.Revision || len(changes) > 0 && r.ExpectedRevision != store.Revision) {
		return nil, failure("MATERIAL_REVIEW_REVISION_CONFLICT")
	}
	var updated []byte
	if len(changes) != 0 {
		if store.Revision == math.MaxInt64 || !reviewClock(clock) {
			return nil, failure("MATERIAL_REVIEW_CLOCK_OR_REVISION_INVALID")
		}
		for _, record := range store.Records {
			if clock < record.ConfirmedAt || clock < record.RevokedAt {
				return nil, failure("MATERIAL_REVIEW_CLOCK_STALE")
			}
		}
		store.Revision++
		for _, id := range changes {
			if r.Operation == "confirm" {
				target := view.Targets[targets[id]]
				check := ManualFactCheck{checkID(id, store.Revision), target.DraftPath, target.ClaimSHA256, target.TokenSHA256, identity, true}
				store.Records = append(store.Records, reviewRecord{Check: check, ConfirmedRevision: store.Revision, ConfirmedAt: clock})
			} else {
				at := active[id]
				store.Records[at].RevokedRevision, store.Records[at].RevokedAt = store.Revision, clock
			}
		}
		sort.Slice(store.Records, func(i, j int) bool { return store.Records[i].Check.ID < store.Records[j].Check.ID })
		updated, e = encodeReviewStore(store)
		if e != nil {
			return nil, e
		}
	}
	checks, stale := currentChecks(store, identity)
	if len(checks) > 1000 {
		return nil, failure("MATERIAL_REVIEW_CHECK_LIMIT")
	}
	view.Revision, view.StaleChecks = store.Revision, stale
	states, ids := map[string]string{}, map[string]string{}
	for _, record := range store.Records {
		target := reviewTarget(record.Check)
		if record.RevokedRevision == 0 {
			states[target], ids[target] = "confirmed", record.Check.ID
		} else if states[target] == "" {
			states[target] = "revoked"
		}
	}
	for i := range view.Targets {
		target := &view.Targets[i]
		if states[target.ID] != "" {
			target.State, target.ManualCheckID = states[target.ID], ids[target.ID]
		}
	}
	if ctx.Err() != nil {
		return nil, failure("MATERIAL_INTEGRITY_CANCELED")
	}
	return &reviewChange{view, checks, updated}, nil
}

func encodeReviewView(view *ReviewView) (json.RawMessage, *Error) {
	raw, e := json.Marshal(view)
	if e != nil || len(raw) > materials.MaxResponseBytes {
		return nil, failure("MATERIAL_REVIEW_RESPONSE_LIMIT")
	}
	return raw, nil
}
