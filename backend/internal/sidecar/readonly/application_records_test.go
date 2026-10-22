package readonly

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/applications"
)

func TestFullApplicationServiceValidationAndSanitization(t *testing.T) {
	var w *Workspace
	bad := "private invalid status"
	_, err := w.Applications(nil, applications.Filters{Status: &bad})
	requireCode(t, err, "APPLICATION_INVALID_STATUS")
	_, err = w.Applications(nil, applications.Filters{Decision: &bad})
	requireCode(t, err, "APPLICATION_INVALID_DECISION")
	_, err = w.Application(nil, "\xff")
	requireCode(t, err, "APPLICATION_INPUT_UNSUPPORTED")
	_, err = w.ResumePlan(nil, "\xff")
	requireCode(t, err, "APPLICATION_INPUT_UNSUPPORTED")
	_, err = w.ResumeLibrary(nil, applications.ResumeFilters{Search: "\xff"})
	requireCode(t, err, "APPLICATION_INPUT_UNSUPPORTED")
	_, err = w.ResumeDigestIdentities(nil)
	requireCode(t, err, "APPLICATION_READ_CANCELED")
	_, err = w.Applications(context.Background(), applications.Filters{})
	requireCode(t, err, "DATA_HOME_UNAVAILABLE")
	for _, code := range []string{"SCHEMA_PREPARATION_REQUIRED", "NOT_FOUND", "INVALID_STATUS", "INVALID_DECISION", "READ_CANCELED"} {
		got := applicationReadError(&applications.Error{Stage: "PRIVATE", Code: code})
		requireCode(t, got, "APPLICATION_"+code)
		if errors.Unwrap(got) != nil {
			t.Fatal("retained raw domain error")
		}
	}
	requireCode(t, applicationReadError(&applications.Error{Code: "DATABASE_CLEANUP_FAILED"}), "APPLICATION_CLEANUP_FAILED")
}

func TestFullApplicationServiceAdmissionAfterCleanup(t *testing.T) {
	for _, failedLoad := range []bool{false, true} {
		for _, changed := range []bool{false, true} {
			calls := []string{}
			payload, err := admittedApplicationModel(context.Background(), func() error {
				calls = append(calls, "check")
				if changed && len(calls) == 3 {
					return fail("DATA_HOME_IDENTITY_CHANGED")
				}
				return nil
			}, func() ([]string, error) {
				calls = append(calls, "load-finished-cleanup")
				if failedLoad {
					return []string{"partial"}, fail("APPLICATION_CLEANUP_FAILED")
				}
				return []string{"complete"}, nil
			})
			if !reflect.DeepEqual(calls, []string{"check", "load-finished-cleanup", "check"}) {
				t.Fatal("post-cleanup admission skipped")
			}
			if changed {
				requireCode(t, err, "DATA_HOME_IDENTITY_CHANGED")
			} else if failedLoad {
				requireCode(t, err, "APPLICATION_CLEANUP_FAILED")
			} else if err != nil || len(payload) != 1 {
				t.Fatal("complete result lost")
			}
			if err != nil && payload != nil {
				t.Fatal("failed read returned content")
			}
		}
	}
	// nil is the source's valid no-plan result, unlike a missing application.
	payload, err := admittedApplicationModel(context.Background(), func() error { return nil }, func() (*applications.PlanRecord, error) { return nil, nil })
	if err != nil || payload != nil {
		t.Fatal("missing plan contract changed")
	}
}

func TestFullApplicationServiceCanceledAtEveryBoundary(t *testing.T) {
	for _, stage := range []int{0, 1, 2, 3} {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		next := func() {
			calls++
			if calls == stage {
				cancel()
			}
		}
		if stage == 0 {
			cancel()
		}
		payload, err := admittedApplicationModel(ctx, func() error { next(); return nil }, func() ([]string, error) { next(); return []string{"private"}, nil })
		cancel()
		requireCode(t, err, "APPLICATION_READ_CANCELED")
		if payload != nil || calls != stage {
			t.Fatal("canceled read continued or returned content")
		}
	}
}
