package readonly

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/jobs/assessmentcontext"
	"careerhub.local/backend/internal/skills/cataloginput"
)

func nativeCacheInput() assessmentcontext.CacheInputs {
	return assessmentcontext.CacheInputs{
		Month:    assessmentcontext.Month{Year: 2026, Number: 9},
		Versions: assessmentcontext.EngineVersions{Analyzer: 11, Evaluator: 4, Preview: 4, Receipt: 3, Instructions: 1, Contacts: 1, Validation: 1},
		Capture:  assessmentcontext.CacheCapture{DiscoveryID: "discovery_synthetic", ContentSHA256: strings.Repeat("a", 64), Scope: "confirmed_full", Title: "Engineer", Company: "Example", ObservedURL: "https://example.test/job/1"},
	}
}

func TestAssessmentInvalidInputsPrecedeStorage(t *testing.T) {
	var w *Workspace
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := w.AssessmentCandidateProjection(ctx); err == nil || !reflect.DeepEqual(got, cataloginput.Value{}) {
		t.Fatal("canceled projection was not atomic")
	}
	if got, err := w.AssessmentCandidateRevision(nil); err == nil || got != "" {
		t.Fatal("nil context accepted")
	}
	if got, err := w.AssessmentContextRevision(context.Background(), assessmentcontext.Month{}); err == nil || got != "" {
		t.Fatal("invalid month accepted")
	}
	if got, err := w.AssessmentRevisions(context.Background(), assessmentcontext.Month{Year: 2026, Number: 9}); err == nil || !reflect.DeepEqual(got, assessmentcontext.Revisions{}) {
		t.Fatal("nil workspace accepted")
	}
	for _, input := range []assessmentcontext.CacheInputs{{}, nativeCacheInput()} {
		if got, err := w.AssessmentCacheIdentity(context.Background(), input); err == nil || got != (assessmentcontext.CacheIdentity{}) {
			t.Fatal("invalid cache input/workspace returned identity")
		}
	}
	for _, code := range []string{"ASSESSMENT_CACHE_VERSION_INVALID", "ASSESSMENT_CACHE_SCOPE_INELIGIBLE", "ASSESSMENT_CACHE_CAPTURE_INVALID", "ASSESSMENT_CACHE_INPUT_LIMIT"} {
		if got := assessmentReadError(&assessmentcontext.Error{Code: code}); got.(*Error).Code != code {
			t.Fatal("fixed cache error code lost")
		}
	}
}
func TestAssessmentAdmissionAndCleanupDiscardPartialResults(t *testing.T) {
	for _, mode := range []string{"before", "after", "load", "cancel-before", "cancel-after", "success"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			checks, loaded := 0, false
			got, err := admittedAssessment(ctx, func() error {
				checks++
				if checks == 2 && !loaded {
					t.Fatal("rechecked before load/cleanup completed")
				}
				if mode == "before" || mode == "after" && checks == 2 {
					return fail("DATA_HOME_IDENTITY_CHANGED")
				}
				if mode == "cancel-before" || mode == "cancel-after" && checks == 2 {
					cancel()
				}
				return nil
			}, func() (assessmentcontext.Revisions, error) {
				loaded = true
				if mode == "load" {
					return assessmentcontext.Revisions{Candidate: "partial"}, fail("ASSESSMENT_CLEANUP_FAILED")
				}
				return assessmentcontext.Revisions{Candidate: "candidate", Context: "context"}, nil
			})
			if mode == "success" {
				if err != nil || got.Candidate != "candidate" {
					t.Fatal("success lost")
				}
			} else if err == nil || !reflect.DeepEqual(got, assessmentcontext.Revisions{}) {
				t.Fatal("partial revision escaped")
			}
		})
	}
	if err := assessmentReadError(&assessmentcontext.Error{Code: "private-cause"}); err.(*Error).Code != "ASSESSMENT_READ_FAILED" {
		t.Fatal("unknown domain diagnostic forwarded")
	}
}
