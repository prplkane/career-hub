package materialintegrity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const reviewTime = "2026-09-12T12:00:00Z"

func reviewFixture(t *testing.T) (*FactPolicy, *DraftAssessment, *DraftClaims, []FactProof) {
	t.Helper()
	p, s, proofs := factFixture(t)
	claims := []FactClaim{{Path: "summary", Text: "Résumé used Java with 99% throughput and 99% coverage.", ProofIDs: []string{"proof_a"}}}
	projection := &DraftClaims{SHA256: s.DraftSHA256, ProofIDs: s.ProofIDs, Claims: claims}
	result, e := CheckFacts(context.Background(), p, s, claims, proofs, nil)
	if e != nil {
		t.Fatal(e.Code)
	}
	return p, &DraftAssessment{s, *result}, projection, proofs
}

func reviewRequest(op string, view *ReviewView, ids ...string) ReviewRequest {
	return ReviewRequest{Operation: op, JobID: "synthetic_job", Kind: "resume", ExpectedRevision: view.Revision, ReviewSHA256: view.ReviewSHA256, TargetIDs: ids}
}

func TestExplicitManualReviewLifecycleAndHistory(t *testing.T) {
	p, baseline, projection, proofs := reviewFixture(t)
	inspect := ReviewRequest{Operation: "inspect", JobID: "synthetic_job", Kind: "resume"}
	initial, e := planReview(context.Background(), inspect, baseline, projection, nil, false, reviewTime)
	if e != nil || initial.bytes != nil || initial.view.Revision != 0 || len(initial.view.Targets) != 2 || len(initial.view.Fields) != 1 {
		t.Fatalf("inspect: %v", e)
	}
	ids := []string{}
	spans := 0
	for _, target := range initial.view.Targets {
		ids = append(ids, target.ID)
		spans += len(target.Spans)
	}
	if spans != 3 {
		t.Fatal("repeated token occurrences hidden from reviewer")
	}
	confirm := reviewRequest("confirm", initial.view, ids...)
	saved, e := planReview(context.Background(), confirm, baseline, projection, nil, false, reviewTime)
	if e != nil || len(saved.bytes) == 0 || saved.view.Revision != 1 || len(saved.checks) != 2 {
		t.Fatalf("confirm: %v", e)
	}
	result, e := CheckFacts(context.Background(), p, baseline.Sources, projection.Claims, proofs, saved.checks)
	if e != nil || result.Validation.Status != "pass" || len(result.CitedDemandIDs) != 0 || len(result.ManualCheckIDs) != 2 {
		t.Fatal("persisted checks not scoped to actual baseline")
	}
	for _, secret := range []string{"Résumé", "Java", "throughput", "99%"} {
		if bytes.Contains(saved.bytes, []byte(secret)) || strings.Contains(fmt.Sprintf("%+v", saved.view), secret) {
			t.Fatal("review prose leaked into store/formatting")
		}
	}
	retry, e := planReview(context.Background(), confirm, baseline, projection, saved.bytes, true, "2026-09-12T12:00:01Z")
	if e != nil || retry.bytes != nil || retry.view.Revision != 1 || retry.checks[0] != saved.checks[0] {
		t.Fatal("exact retry changed store or approval ID")
	}
	revoke := reviewRequest("revoke", retry.view, ids[0])
	revoked, e := planReview(context.Background(), revoke, baseline, projection, saved.bytes, true, "2026-09-12T12:00:02Z")
	if e != nil || revoked.view.Revision != 2 || len(revoked.checks) != 1 {
		t.Fatal("revocation failed")
	}
	result, e = CheckFacts(context.Background(), p, baseline.Sources, projection.Claims, proofs, revoked.checks)
	if e != nil || result.Validation.Status != "block" {
		t.Fatal("revoked fact still authorized")
	}
	reconfirmed, e := planReview(context.Background(), reviewRequest("confirm", revoked.view, ids[0]), baseline, projection, revoked.bytes, true, "2026-09-12T12:00:03Z")
	if e != nil || reconfirmed.view.Revision != 3 || len(reconfirmed.checks) != 2 {
		t.Fatal("reconfirmation failed")
	}
	store, e := decodeReviewStore(reconfirmed.bytes, "synthetic_job", "resume")
	if e != nil || len(store.Records) != 3 {
		t.Fatal("original confirmation history replaced")
	}
	found := false
	for _, record := range store.Records {
		if record.RevokedRevision == 2 && record.ConfirmedRevision == 1 && record.ConfirmedAt == reviewTime {
			found = true
		}
	}
	if !found {
		t.Fatal("revoked approval lost original identity/date")
	}
	stale := *baseline
	stale.Sources.DraftRevision++
	current, e := planReview(context.Background(), inspect, &stale, projection, reconfirmed.bytes, true, reviewTime)
	if e != nil || current.view.StaleChecks != 2 || len(current.checks) != 0 {
		t.Fatal("stale approval reused")
	}
	if out, e := planReview(context.Background(), confirm, &stale, projection, reconfirmed.bytes, true, reviewTime); out != nil || e == nil || e.Code != "MATERIAL_REVIEW_STALE" {
		t.Fatal("stale user confirmation accepted")
	}
}

func TestReviewRefusesConflictsInvalidStoresAndPlaceholders(t *testing.T) {
	_, base, projection, _ := reviewFixture(t)
	inspect := ReviewRequest{Operation: "inspect", JobID: "synthetic_job", Kind: "resume"}
	initial, _ := planReview(context.Background(), inspect, base, projection, nil, false, reviewTime)
	request := reviewRequest("confirm", initial.view, initial.view.Targets[0].ID)
	saved, e := planReview(context.Background(), request, base, projection, nil, false, reviewTime)
	if e != nil {
		t.Fatal(e.Code)
	}
	other := reviewRequest("confirm", initial.view, initial.view.Targets[1].ID)
	if out, e := planReview(context.Background(), other, base, projection, saved.bytes, true, reviewTime); out != nil || e == nil || e.Code != "MATERIAL_REVIEW_REVISION_CONFLICT" {
		t.Fatal("stale store update accepted")
	}
	for _, raw := range [][]byte{nil, []byte(`{}`), append(bytes.Clone(saved.bytes), ' '), bytes.Replace(saved.bytes, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_version":1`), 1), bytes.Replace(saved.bytes, []byte(`"confirmed":true`), []byte(`"confirmed":false`), 1)} {
		if s, e := decodeReviewStore(raw, "synthetic_job", "resume"); s != nil || e == nil {
			t.Fatal("noncanonical/invalid review store accepted")
		}
	}
	if out, e := planReview(context.Background(), reviewRequest("confirm", saved.view, initial.view.Targets[1].ID), base, projection, saved.bytes, true, "2026-09-11T12:00:00Z"); out != nil || e == nil || e.Code != "MATERIAL_REVIEW_CLOCK_STALE" {
		t.Fatal("approval history clock went backwards")
	}
	for _, op := range []string{"apply", "save", ""} {
		bad := request
		bad.Operation = op
		if ValidateReviewRequest(bad) == nil {
			t.Fatal("implicit review action accepted")
		}
	}
	placeholder := *base
	placeholder.Facts.Validation.Findings = []Finding{{Code: "UNRESOLVED_PLACEHOLDER", DraftPath: "summary", DraftStart: 0, DraftEnd: 4}}
	view, e := planReview(context.Background(), inspect, &placeholder, projection, nil, false, reviewTime)
	if e != nil || len(view.view.Targets) != 0 {
		t.Fatal("placeholder offered manual permission")
	}
	if out, e := planReview(context.Background(), request, &placeholder, projection, nil, false, reviewTime); out != nil || e == nil {
		t.Fatal("placeholder approved by old token selector")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, e := planReview(ctx, inspect, base, projection, nil, false, reviewTime); out != nil || e == nil {
		t.Fatal("canceled review returned partial success")
	}
	var decoded map[string]any
	if json.Unmarshal(saved.bytes, &decoded) != nil {
		t.Fatal("wire decode")
	}
}
