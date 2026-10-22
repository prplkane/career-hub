//go:build windows

package catalogwrite

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
)

type archiveNativeRequest struct {
	Data, Resources, Action, ID string
	Archived                    bool
	Revision                    string
}
type archiveNativeResponse struct {
	Outcome *ArchiveOutcome
	Code    string
}

func TestArchiveNativeProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_ARCHIVE_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var request archiveNativeRequest
	if json.NewDecoder(os.Stdin).Decode(&request) != nil {
		t.Fatal("request")
	}
	root, e := runtimepaths.Open(request.Data)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if request.Action != "unowned" {
		if e := root.ClaimForProcess(); e != nil {
			t.Fatal(e)
		}
	}
	service := New(request.Resources, request.Data)
	var outcome *ArchiveOutcome
	var failure *Error
	switch request.Action {
	case "recover":
		// Ordinary admission and writes cannot observe the mixed state.
		pending, e := root.Exists(datahome.RegistryRecoveryPath)
		if e != nil {
			t.Fatal(e)
		}
		if pending {
			if _, e := datahome.Inspect(request.Data, nil); e == nil || e.Code != "DATA_HOME_RECOVERY_REQUIRED" {
				t.Fatal("pending transaction admitted")
			}
			if _, e := service.SetCustomGroupArchived(context.Background(), request.ID, true, "ignored"); e == nil || e.Code != "CATALOG_RECOVERY_REQUIRED" {
				t.Fatal("archive bypassed recovery")
			}
			if _, e := service.RenameCustomGroup(context.Background(), request.ID, map[string]any{"name": "ignored"}, "ignored"); e == nil || e.Code != "CATALOG_RECOVERY_REQUIRED" {
				t.Fatal("rename bypassed recovery")
			}
		}
		outcome, failure = service.RecoverRegistry(context.Background())
	case "gate":
		release, e := operationgate.Acquire(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			outcome, failure = service.SetCustomGroupArchived(ctx, request.ID, request.Archived, request.Revision)
			close(done)
		}()
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("cancelled operation waited indefinitely")
		}
		release()
	default:
		outcome, failure = service.SetCustomGroupArchived(context.Background(), request.ID, request.Archived, request.Revision)
	}
	result := archiveNativeResponse{Outcome: outcome}
	if failure != nil {
		result.Code = failure.Code
	}
	if json.NewEncoder(os.Stdout).Encode(result) != nil {
		t.Fatal("report")
	}
	os.Exit(0)
}
