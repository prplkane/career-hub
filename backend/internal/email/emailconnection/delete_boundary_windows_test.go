//go:build windows

package emailconnection

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/storage/datahome"
)

func TestDeletionRefusesWitnessChangedDuringPresenceRead(t *testing.T) {
	mode := os.Getenv("CAREERHUB_DELETE_BOUNDARY_MODE")
	if mode == "" {
		for _, mode := range []string{"disconnect", "rotate", "recover", "recover-preremove", "settings"} {
			t.Run(mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDeletionRefusesWitnessChangedDuringPresenceRead$")
				cmd.Env = append(os.Environ(), "CAREERHUB_DELETE_BOUNDARY_MODE="+mode, "CAREERHUB_DELETE_BOUNDARY_HOME="+filepath.Join(t.TempDir(), "home"))
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("delete boundary %s: %v %.1500s", mode, err, out)
				}
			})
		}
		return
	}
	ctx, path := context.Background(), os.Getenv("CAREERHUB_DELETE_BOUNDARY_HOME")
	if _, p := datahome.BootstrapForProcess(ctx, path, "dev"); p != nil {
		t.Fatal(p)
	}
	if _, err := schema.PrepareOwned(ctx, path); err != nil {
		t.Fatal(err)
	}
	backend := &promotionSecrets{fakeSecrets: &fakeSecrets{}}
	if _, p := ConfigureNew(ctx, path, backend, validNewInput()); p != nil {
		t.Fatal(p)
	}
	before, _, _, _, _, _, _ := settingsState(t, path)
	oldTarget := "CareerHub/dev/provider/" + before.SecretName
	if strings.HasPrefix(mode, "recover") {
		// Retain a committed disconnect journal without retiring the key.
		// Preflight needs a successful read; fail only after the SQL commit.
		backend.onRead = func() {
			current, _, _, _, _, _, _ := settingsState(t, path)
			if current.Revision == 2 {
				backend.readError = true
			}
		}
		r, p := Disconnect(ctx, path, backend, 1)
		if p == nil || !r.Committed || !r.RecoveryRequired {
			t.Fatalf("recovery seed: %+v %v", r, p)
		}
		backend.readError = false
	}
	changed := false
	reads := 0
	backend.onRead = func() {
		current, _, _, _, _, _, _ := settingsState(t, path)
		if current.Revision != 2 {
			return
		}
		reads++
		// Recovery performs two presence observations before selecting deletion.
		if mode == "recover" && reads < 2 {
			return
		}
		if mode == "recover-preremove" && reads < 3 {
			return
		}
		// Rotation verifies the new value first. Inject specifically on the old key.
		if mode == "rotate" && backend.reads[len(backend.reads)-1] != oldTarget {
			return
		}
		backend.onRead = nil
		db := settingsDatabase(t, path, false)
		defer db.Close()
		if _, err := db.Exec(`UPDATE job_email_connections SET enabled=1,revision=3,secret_name=? WHERE connection_id='primary'`, before.SecretName); err != nil {
			t.Fatal(err)
		}
		changed = true
	}
	var p *Error
	switch mode {
	case "disconnect":
		_, p = Disconnect(ctx, path, backend, 1)
	case "rotate":
		_, p = Rotate(ctx, path, backend, RotateInput{AccountAddress: "alerts@example.test", Password: "qrstuvwxyzabcdef", JobSenders: []string{"jobs@example.test"}, ExpectedRevision: 1})
	case "recover", "recover-preremove":
		_, p = Recover(ctx, path, backend)
	case "settings":
		var r EditSettingsResult
		r, p = EditSettings(ctx, path, backend, EditSettingsInput{AccountAddress: "alerts@example.test", JobSenders: []string{"jobs@example.test"}, ExpectedRevision: 1})
		if !r.Committed || !r.RevisionRefreshRequired {
			t.Fatalf("stale settings success: %+v %v", r, p)
		}
	}
	if !changed {
		t.Fatal("drift hook was not exercised")
	}
	if p == nil || p.Code != "EMAIL_CREDENTIAL_DATABASE_STATE_CHANGED" {
		t.Fatalf("wrong boundary outcome: %v", p)
	}
	if mode != "recover-preremove" && (len(backend.deletes) != 0 || len(backend.values[oldTarget]) == 0) {
		t.Fatal("deleted a credential after its row witness changed")
	}
	if mode == "settings" {
		return
	} // Settings never publishes a credential journal.
	if _, err := os.Stat(filepath.Join(path, datahome.EmailCredentialRecoveryPath)); err != nil {
		t.Fatal("lost journal after witness drift")
	}
}
