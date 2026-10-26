package readonly

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestDiscoveryServiceValidationAndErrorBoundary(t *testing.T) {
	var w *Workspace
	_, err := w.Discoveries(nil, discovery.PageOptions{})
	requireCode(t, err, "DISCOVERY_INVALID_LIMIT")
	_, err = w.DiscoveryCount(nil, discovery.Filters{States: []string{"private"}})
	requireCode(t, err, "DISCOVERY_INVALID_STATES")
	_, err = w.Discovery(nil, "")
	requireCode(t, err, "DISCOVERY_INVALID_ID")
	for _, id := range []string{"é:job", "🧭:job", "1:job", "::job"} {
		_, err = w.Discovery(nil, id)
		requireCode(t, err, "DISCOVERY_INVALID_ID")
	}
	_, err = w.DiscoveryAssessment(nil, "\xff")
	requireCode(t, err, "DISCOVERY_INPUT_UNSUPPORTED")
	_, err = w.DiscoveryAssessment(nil, "valid")
	requireCode(t, err, "DISCOVERY_READ_CANCELED")
	_, err = w.DiscoveryAssessment(context.Background(), "valid")
	requireCode(t, err, "DATA_HOME_UNAVAILABLE")
	for _, code := range []string{"INPUT_LIMIT", "INPUT_UNSUPPORTED", "INVALID_ID", "INVALID_TEXT", "INVALID_TIMESTAMP", "INVALID_STATES", "INVALID_SOURCES", "INVALID_READINESS", "INVALID_QUEUES", "INVALID_SORT", "INVALID_LIMIT", "INVALID_OFFSET", "SCHEMA_TOO_NEW", "SCHEMA_PREPARATION_REQUIRED", "READ_CANCELED", "NOT_FOUND", "RESULT_LIMIT", "SOURCE_PATH_REJECTED", "SOURCE_UNREADABLE", "SOURCE_LENGTH_MISMATCH", "SOURCE_HASH_MISMATCH", "METADATA_INVALID", "ASSESSMENT_INVALID"} {
		e := discoveryReadError(&discovery.Error{Stage: "PRIVATE", Code: code})
		requireCode(t, e, "DISCOVERY_"+code)
		if errors.Unwrap(e) != nil || fmt.Sprintf("%+v", e) != "Read-only workspace validation failed." {
			t.Fatal("private diagnostic leaked")
		}
	}
	for native, public := range map[string]string{"DATABASE_VALUE_UNSUPPORTED": "DISCOVERY_VALUE_UNSUPPORTED", "DATABASE_CLEANUP_FAILED": "DISCOVERY_CLEANUP_FAILED", "PRIVATE": "DISCOVERY_READ_FAILED"} {
		requireCode(t, discoveryReadError(&discovery.Error{Code: native}), public)
	}
	for native, public := range map[string]string{"OWNERSHIP_REQUIRED": "DATA_HOME_OWNERSHIP_REQUIRED", "SQLITE_FILES_UNSUPPORTED": "DISCOVERY_STORAGE_UNSUPPORTED", "SQLITE_VOLUME_REJECTED": "DISCOVERY_STORAGE_UNSUPPORTED", "PRIVATE": "DISCOVERY_PATH_REJECTED"} {
		requireCode(t, discoveryPathError(&runtimepaths.Error{Code: native}), public)
	}
}
func TestDiscoveryServicePostCleanupAdmission(t *testing.T) {
	for _, loadFailure := range []bool{false, true} {
		for _, drift := range []bool{false, true} {
			calls := []string{}
			payload, err := admittedDiscovery(context.Background(), func() error {
				calls = append(calls, "check")
				if drift && len(calls) == 3 {
					return fail("DATA_HOME_IDENTITY_CHANGED")
				}
				return nil
			}, func() ([]string, error) {
				calls = append(calls, "cleanup-finished")
				if loadFailure {
					return []string{"partial"}, fail("DISCOVERY_READ_FAILED")
				}
				return []string{"complete"}, nil
			})
			if !reflect.DeepEqual(calls, []string{"check", "cleanup-finished", "check"}) {
				t.Fatal("admission order changed")
			}
			if drift {
				requireCode(t, err, "DATA_HOME_IDENTITY_CHANGED")
			} else if loadFailure {
				requireCode(t, err, "DISCOVERY_READ_FAILED")
			} else if err != nil || len(payload) != 1 {
				t.Fatal("complete result lost")
			}
			if err != nil && payload != nil {
				t.Fatal("failure retained data")
			}
		}
	}
	for stage := 0; stage < 4; stage++ {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		next := func() {
			calls++
			if stage == calls {
				cancel()
			}
		}
		if stage == 0 {
			cancel()
		}
		payload, err := admittedDiscovery(ctx, func() error { next(); return nil }, func() ([]string, error) { next(); return []string{"private"}, nil })
		cancel()
		requireCode(t, err, "DISCOVERY_READ_CANCELED")
		if calls != stage || payload != nil {
			t.Fatal("cancellation continued/retained data")
		}
	}
	value, err := admittedDiscovery(context.Background(), func() error { return nil }, func() (*discovery.Assessment, error) { return nil, nil })
	if value != nil || err != nil {
		t.Fatal("missing artifact ceased being valid null")
	}
}
