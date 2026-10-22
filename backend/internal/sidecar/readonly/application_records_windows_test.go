//go:build windows

package readonly

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/sidecar/protocol"
)

type fullReadServiceCase struct {
	Name, Fixture, Operation, Error string
	Arguments                       struct {
		Status, Decision *string
		JobID            string `json:"job_id"`
		Search           string
		IncludeArchived  bool `json:"include_archived"`
	}
	Expected json.RawMessage
}

type fullReadServiceRequest struct {
	Start          protocol.StartRecord
	Mode, WantCode string
	Cases          []fullReadServiceCase
}

func nativeReadJSON(t *testing.T, raw []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value, trailing any
	if decoder.Decode(&value) != nil || decoder.Decode(&trailing) != io.EOF {
		t.Fatal("invalid synthetic JSON")
	}
	return value
}

func TestFullApplicationReadServiceHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_FULL_READ_SERVICE_HELPER") != "1" {
		return
	}
	var request fullReadServiceRequest
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 16<<20))
	if decoder.Decode(&request) != nil {
		t.Fatal("invalid synthetic helper request")
	}
	var w *Workspace
	var err error
	if request.Mode == "unowned" {
		w, err = Open(request.Start)
	} else {
		w, err = OpenForProcess(request.Start)
	}
	if err != nil {
		t.Fatal("synthetic workspace admission failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	switch request.Mode {
	case "identity":
		writeFixture(t, request.Start.DataRoot, datahome.MarkerPath, marker("dev", "dh_22222222222222222222222222222222"))
	case "channel":
		writeFixture(t, request.Start.DataRoot, datahome.MarkerPath, marker("stable", fixtureID))
	case "recovery":
		writeFixture(t, request.Start.DataRoot, datahome.RestoreRecoveryPath, "synthetic recovery")
	case "resource":
		writeFixture(t, request.Start.ResourceRoot, "VERSION", "invalid")
	case "canceled":
		cancel()
	}
	if request.Mode != "reference" {
		for _, operation := range []string{"list", "detail", "plan", "resumes", "digest_identities"} {
			c := fullReadServiceCase{Operation: operation}
			c.Arguments.JobID = "job-applied"
			request.Cases = append(request.Cases, c)
		}
	}
	for _, c := range request.Cases {
		before := tree(t, filepath.Dir(request.Start.DataRoot))
		var payload any
		var err error
		switch c.Operation {
		case "list":
			payload, err = w.Applications(ctx, applications.Filters{Status: c.Arguments.Status, Decision: c.Arguments.Decision})
		case "detail":
			payload, err = w.Application(ctx, c.Arguments.JobID)
		case "plan":
			payload, err = w.ResumePlan(ctx, c.Arguments.JobID)
		case "resumes":
			payload, err = w.ResumeLibrary(ctx, applications.ResumeFilters{Search: c.Arguments.Search, IncludeArchived: c.Arguments.IncludeArchived})
		case "digest_identities":
			payload, err = w.ResumeDigestIdentities(ctx)
		default:
			t.Fatal("unreviewed operation")
		}
		if !reflect.DeepEqual(before, tree(t, filepath.Dir(request.Start.DataRoot))) {
			t.Fatal("service read changed synthetic files")
		}
		if err != nil && errors.Unwrap(err) != nil {
			t.Fatal("service retained a private driver cause")
		}
		wantCode := request.WantCode
		if c.Error != "" {
			wantCode = "APPLICATION_" + c.Error
		}
		raw, encodeErr := json.Marshal(payload)
		if encodeErr != nil {
			t.Fatal("invalid service payload")
		}
		if wantCode != "" {
			requireCode(t, err, wantCode)
			if string(raw) != "null" {
				t.Fatal("failed read returned private partial content")
			}
		} else {
			if err != nil {
				t.Fatalf("native full read unexpectedly failed: %s %s", request.Mode, c.Operation)
			}
			if request.Mode == "reference" && !reflect.DeepEqual(nativeReadJSON(t, raw), nativeReadJSON(t, c.Expected)) {
				t.Fatalf("complete native source payload mismatch: %s; private values suppressed", c.Name)
			}
		}
	}
	if request.Mode == "fresh" {
		writeApplicationSQL(t, request.Start.DataRoot, "UPDATE job_applications SET notes='new synthetic note' WHERE job_id='job-applied'")
		before := tree(t, filepath.Dir(request.Start.DataRoot))
		payload, err := w.Application(ctx, "job-applied")
		if err != nil {
			t.Fatal("fresh full read failed")
		}
		raw, err := json.Marshal(payload)
		if err != nil || nativeReadJSON(t, raw).(map[string]any)["notes"] != "new synthetic note" {
			t.Fatal("full reader served cached state")
		}
		if !reflect.DeepEqual(before, tree(t, filepath.Dir(request.Start.DataRoot))) {
			t.Fatal("fresh read mutated files")
		}
	}
}
