package readonly

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestApplicationSummaryEarlyReturnsDoNotAccessWorkspace(t *testing.T) {
	for _, w := range []*Workspace{nil, {}} {
		if payload, err := w.ApplicationSummaries(nil, nil); err != nil || payload == nil || len(payload) != 0 {
			t.Fatal("empty request accessed context or workspace")
		}
		for _, tc := range []struct {
			ids  []string
			code string
		}{
			{make([]string, applications.MaxRequestedIDs+1), "APPLICATION_INPUT_LIMIT"},
			{[]string{strings.Repeat("x", applications.MaxIDBytes+1)}, "APPLICATION_INPUT_LIMIT"},
			{[]string{"bad\xff"}, "APPLICATION_INPUT_UNSUPPORTED"},
		} {
			payload, err := w.ApplicationSummaries(nil, tc.ids)
			requireCode(t, err, tc.code)
			if payload != nil {
				t.Fatal("invalid request returned content")
			}
		}
		_, err := w.ApplicationSummaries(nil, []string{"one"})
		requireCode(t, err, "APPLICATION_READ_CANCELED")
		_, err = w.ApplicationSummaries(context.Background(), []string{"one"})
		requireCode(t, err, "DATA_HOME_UNAVAILABLE")
	}
}

func TestApplicationSummaryAdmissionAndDrift(t *testing.T) {
	for _, tc := range []struct {
		name string
		info *datahome.Info
		err  *datahome.Error
		code string
	}{
		{"missing", nil, nil, "DATA_HOME_UNAVAILABLE"},
		{"uninitialized", &datahome.Info{}, nil, "DATA_HOME_UNAVAILABLE"},
		{"identity", &datahome.Info{Initialized: true, DataHomeID: "different"}, nil, "DATA_HOME_IDENTITY_CHANGED"},
		{"channel", &datahome.Info{Initialized: true, DataHomeID: fixtureID, FormatVersion: 2, Channel: "stable"}, nil, "DATA_HOME_IDENTITY_CHANGED"},
		{"format", &datahome.Info{Initialized: true, DataHomeID: fixtureID, FormatVersion: 3, Channel: "dev"}, nil, "DATA_HOME_IDENTITY_CHANGED"},
		{"newer", nil, &datahome.Error{Code: "DATA_HOME_NEWER_THAN_APP"}, "DATA_HOME_NEWER_THAN_APP"},
		{"recovery", nil, &datahome.Error{Code: "DATA_HOME_RECOVERY_REQUIRED"}, "DATA_HOME_RECOVERY_REQUIRED"},
		{"unknown", nil, &datahome.Error{Code: "private native cause"}, "DATA_HOME_UNAVAILABLE"},
	} {
		for _, post := range []bool{false, true} {
			// At initial admission Inspect enforces channel/format itself. These
			// rows intentionally test drift between two successful observations.
			if !post && (tc.name == "channel" || tc.name == "format") {
				continue
			}
			for _, failedLoad := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/post=%t/load_error=%t", tc.name, post, failedLoad), func(t *testing.T) {
					reads, loads := 0, 0
					payload, err := admittedApplicationSummaries(context.Background(), fixtureID,
						func() (*datahome.Info, *datahome.Error) {
							reads++
							if post && reads == 1 {
								return profileInfo(), nil
							}
							return tc.info, tc.err
						}, func() (map[string]applications.Summary, error) {
							loads++
							if failedLoad {
								return nil, fail("APPLICATION_READ_FAILED")
							}
							return map[string]applications.Summary{"one": {}}, nil
						})
					requireCode(t, err, tc.code)
					wantReads, wantLoads := 1, 0
					if post {
						wantReads, wantLoads = 2, 1
					}
					if payload != nil || reads != wantReads || loads != wantLoads {
						t.Fatal("admission returned stale content or used the wrong read sequence")
					}
				})
			}
		}
	}
}

func TestApplicationSummaryCancellationBetweenOperations(t *testing.T) {
	for _, stage := range []string{"before", "inspect_before", "load", "inspect_after"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if stage == "before" {
				cancel()
			}
			reads, loads := 0, 0
			payload, err := admittedApplicationSummaries(ctx, fixtureID,
				func() (*datahome.Info, *datahome.Error) {
					reads++
					if stage == "inspect_before" && reads == 1 || stage == "inspect_after" && reads == 2 {
						cancel()
					}
					return profileInfo(), nil
				}, func() (map[string]applications.Summary, error) {
					loads++
					if stage == "load" {
						cancel()
					}
					return map[string]applications.Summary{}, nil
				})
			requireCode(t, err, "APPLICATION_READ_CANCELED")
			want := map[string][2]int{"before": {0, 0}, "inspect_before": {1, 0}, "load": {1, 1}, "inspect_after": {2, 1}}[stage]
			if payload != nil || reads != want[0] || loads != want[1] {
				t.Fatal("cancellation did not stop at the operation boundary")
			}
		})
	}
	payload, err := admittedApplicationSummaries(context.Background(), "", nil, nil)
	requireCode(t, err, "DATA_HOME_UNAVAILABLE")
	if payload != nil {
		t.Fatal("empty home identity returned data")
	}
}

func TestApplicationSummaryEmptyResultsAndSafeFailures(t *testing.T) {
	for _, tc := range []struct {
		payload map[string]applications.Summary
		err     error
		code    string
	}{
		{map[string]applications.Summary{}, nil, ""},
		{nil, nil, "APPLICATION_READ_FAILED"},
		{map[string]applications.Summary{"partial": {}}, fail("APPLICATION_SCHEMA_TOO_NEW"), "APPLICATION_SCHEMA_TOO_NEW"},
	} {
		reads := 0
		payload, err := admittedApplicationSummaries(context.Background(), fixtureID,
			func() (*datahome.Info, *datahome.Error) {
				reads++
				return profileInfo(), nil
			}, func() (map[string]applications.Summary, error) { return tc.payload, tc.err })
		if reads != 2 {
			t.Fatal("empty/failed read skipped final admission")
		}
		if tc.code == "" {
			if err != nil || payload == nil || len(payload) != 0 {
				t.Fatal("valid empty summary result refused")
			}
		} else {
			requireCode(t, err, tc.code)
			if payload != nil {
				t.Fatal("failed read returned partial content")
			}
		}
	}
	for _, tc := range []struct{ input, code string }{
		{"INPUT_LIMIT", "APPLICATION_INPUT_LIMIT"},
		{"INPUT_UNSUPPORTED", "APPLICATION_INPUT_UNSUPPORTED"},
		{"SCHEMA_TOO_NEW", "APPLICATION_SCHEMA_TOO_NEW"},
		{"RESULT_LIMIT", "APPLICATION_RESULT_LIMIT"},
		{"DATABASE_VALUE_UNSUPPORTED", "APPLICATION_VALUE_UNSUPPORTED"},
		{"DATABASE_QUERY_FAILED", "APPLICATION_READ_FAILED"},
		{"private native cause", "APPLICATION_READ_FAILED"},
	} {
		err := applicationReadError(&applications.Error{Stage: "private input", Code: tc.input})
		requireCode(t, err, tc.code)
		if errors.Unwrap(err) != nil {
			t.Fatal("private query cause retained")
		}
	}
	for _, tc := range []struct{ input, code string }{
		{"OWNERSHIP_REQUIRED", "DATA_HOME_OWNERSHIP_REQUIRED"},
		{"SQLITE_FILES_UNSUPPORTED", "APPLICATION_STORAGE_UNSUPPORTED"},
		{"SQLITE_VOLUME_REJECTED", "APPLICATION_STORAGE_UNSUPPORTED"},
		{"private path", "APPLICATION_PATH_REJECTED"},
	} {
		err := applicationPathError(&runtimepaths.Error{Code: tc.input})
		requireCode(t, err, tc.code)
		if errors.Unwrap(err) != nil {
			t.Fatal("native path cause retained")
		}
	}
}
