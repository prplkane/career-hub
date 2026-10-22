//go:build windows

package applicationwrite

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/runtimepaths"
)

type maintenanceRequest struct {
	Data, Mode, Op, Orphan, Survivor string
	Unconfirmed, OmitTables          bool
}

func TestMaintenanceProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_MAINTENANCE_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var r maintenanceRequest
	if json.NewDecoder(os.Stdin).Decode(&r) != nil {
		t.Fatal("request")
	}
	root, e := runtimepaths.Open(r.Data)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if r.Mode != "unowned" {
		if e := root.ClaimForProcess(); e != nil {
			t.Fatal(e)
		}
	}
	nativeSetup(t, nativeRequest{Data: r.Data, Mode: r.Mode})
	s := New(r.Data)
	s.now = func() string { return "2026-09-09T12:00:00Z" }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	installFaults(t, s, nativeRequest{Data: r.Data, Mode: r.Mode}, ctx, cancel)
	prior := s.checkpoint
	s.checkpoint = func(stage string) *Error {
		if e := prior(stage); e != nil {
			return e
		}
		if r.Mode == "crash-"+stage {
			os.Exit(77)
		}
		if r.Mode == "late-cancel" && stage == "committed" {
			cancel()
		}
		if stage == "applied" {
			switch r.Mode {
			case "change-card":
				fixtureFile(t, filepath.Join(r.Data, "jobs/normalized/survivor.yaml"), []byte("changed"))
			case "change-raw":
				fixtureFile(t, filepath.Join(r.Data, "jobs/incoming/survivor.txt"), []byte("changed"))
			case "add-card":
				fixtureFile(t, filepath.Join(r.Data, "jobs/normalized/late.yaml"), []byte("changed"))
			case "add-directory":
				if e := os.Mkdir(filepath.Join(r.Data, "jobs/normalized/late"), 0700); e != nil {
					t.Fatal(e)
				}
			case "add-material":
				fixtureFile(t, filepath.Join(r.Data, "outputs/application_materials/orphan/materials.yaml"), []byte("synthetic: evidence"))
			}
		}
		return nil
	}
	var out *Outcome
	var problem *Error
	switch r.Op {
	case "report":
		out, problem = s.IntegrityReport(ctx)
	case "sync":
		out, problem = s.SyncNormalizedJobs(ctx)
	case "repair":
		out, problem = s.RepairExactOrphan(ctx, r.Orphan, r.Survivor, !r.Unconfirmed)
	default:
		t.Fatal("unknown operation")
	}
	var tables map[string]table
	if _, e := os.Stat(filepath.Join(r.Data, databasePath)); e == nil && !r.OmitTables {
		tables = sessionTables(t, r.Data)
	}
	if json.NewEncoder(os.Stdout).Encode(nativeOutput{out, problem, tables}) != nil {
		t.Fatal("output")
	}
}
