//go:build windows

package startup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"careerhub.local/backend/internal/email/emailconnection"
	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/secretstore"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

// The process boundary is native; credentials are exclusively synthetic/in-memory.
type recoveryTestSecrets struct {
	t              *testing.T
	target         string
	present        bool
	reads, deletes int
}

func (s *recoveryTestSecrets) Read(target string) ([]byte, bool, error) {
	s.t.Helper()
	if target != s.target {
		s.t.Fatal("unexpected credential read")
	}
	s.reads++
	if !s.present {
		return nil, false, nil
	}
	return []byte("fictional-secret"), true, nil
}
func (s *recoveryTestSecrets) Write(string, []byte) error {
	s.t.Fatal("recovery must never create credentials")
	return errors.New("unexpected write")
}
func (s *recoveryTestSecrets) Delete(target string) (bool, error) {
	s.t.Helper()
	if target != s.target {
		s.t.Fatal("unexpected credential deletion")
	}
	s.deletes++
	was := s.present
	s.present = false
	return was, nil
}

func TestEmailMaintenanceNativeBoundary(t *testing.T) {
	for _, mode := range []string{"before", "after", "copied", "legacy", "invalid", "mixed", "other", "none", "channel", "home_changed"} {
		t.Run(mode, func(t *testing.T) {
			start := fixture(t)
			runEmailRecoveryChild(t, start, mode, "prepare")
			if mode == "copied" {
				copyPath := filepath.Join(filepath.Dir(start.DataRoot), "copy")
				if err := os.CopyFS(copyPath, os.DirFS(start.DataRoot)); err != nil {
					t.Fatal(err)
				}
				start.DataRoot = copyPath
			}
			runEmailRecoveryChild(t, start, mode, "inspect")
			runEmailRecoveryChild(t, start, mode, "recover")
			if mode == "before" || mode == "after" {
				runEmailRecoveryChild(t, start, mode, "restart")
			}
		})
	}
}

func runEmailRecoveryChild(t *testing.T, start protocol.StartRecord, mode, stage string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEmailMaintenanceNativeHelper$")
	cmd.Env = append(os.Environ(), "CH_EMAIL_MAINT_HOME="+start.DataRoot, "CH_EMAIL_MAINT_RES="+start.ResourceRoot,
		"CH_EMAIL_MAINT_MODE="+mode, "CH_EMAIL_MAINT_STAGE="+stage)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", stage, err, output)
	}
}

func TestEmailMaintenanceNativeHelper(t *testing.T) {
	stage := os.Getenv("CH_EMAIL_MAINT_STAGE")
	if stage == "" {
		t.Skip("subprocess only")
	}
	mode := os.Getenv("CH_EMAIL_MAINT_MODE")
	start := protocol.StartRecord{DataRoot: os.Getenv("CH_EMAIL_MAINT_HOME"), ResourceRoot: os.Getenv("CH_EMAIL_MAINT_RES"), Channel: "dev"}
	ctx := context.Background()
	if stage == "inspect" {
		before := snapshot(t, start.DataRoot)
		if mode == "channel" {
			start.Channel = "stable"
		}
		kind, err := InspectRecoveryForProcess(ctx, start)
		refused := mode == "channel" || mode == "mixed" || mode == "none"
		if refused != (err != nil) {
			t.Fatal("incorrect inspection admission", kind, err)
		}
		if !refused {
			expected := datahome.RecoveryEmailCredential
			if mode == "other" {
				expected = datahome.RecoveryNewJob
			}
			if kind != expected {
				t.Fatal("incorrect recovery kind", kind)
			}
		}
		if !reflect.DeepEqual(before, snapshot(t, start.DataRoot)) {
			t.Fatal("inspection changed Home")
		}
		return
	}
	if stage == "restart" {
		if _, err := OpenForProcess(ctx, start); err != nil {
			t.Fatal(err)
		}
		return
	}
	if stage == "prepare" {
		if _, err := OpenForProcess(ctx, start); err != nil {
			t.Fatal(err)
		}
		root, problem := runtimepaths.Open(start.DataRoot)
		if problem != nil {
			t.Fatal(problem)
		}
		defer root.Close()
		home, admission := datahome.InspectOwnedForEmailCredentialRecovery(root, &start.Channel)
		if admission != nil || home == nil {
			t.Fatal(admission)
		}
		scope, scopeErr := root.CredentialScope()
		if scopeErr != nil {
			t.Fatal(scopeErr)
		}
		name, err := emailcredentials.NewScopedGenerationName(*home, scope)
		if err != nil {
			t.Fatal(err)
		}
		witness := emailcredentials.RowWitness{Present: true, Revision: 1, Enabled: true, SecretName: name}
		intent := emailcredentials.ScopedIntent{Version: 2, Home: *home, RootFingerprint: scope.Fingerprint(), Operation: emailcredentials.InitialConfigure, After: witness}
		raw, err := emailcredentials.EncodeScopedIntent(intent, scope)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "legacy" {
			witness.SecretName, err = emailcredentials.NewGenerationName(*home)
			if err != nil {
				t.Fatal(err)
			}
			raw, err = emailcredentials.EncodeIntent(emailcredentials.Intent{Version: 1, Home: *home, Operation: emailcredentials.InitialConfigure, After: witness})
			if err != nil {
				t.Fatal(err)
			}
		}
		if mode == "invalid" {
			raw = []byte("invalid synthetic journal")
		}
		if mode != "none" && mode != "other" {
			writeFixture(t, start.DataRoot, datahome.EmailCredentialRecoveryPath, string(raw))
		}
		if mode == "mixed" || mode == "other" {
			writeFixture(t, start.DataRoot, datahome.NewJobRecoveryPath, "synthetic pending intent")
		}
		// Test-only target metadata outside Home is never production recovery authority.
		writeFixture(t, start.ResourceRoot, "synthetic-target", name)
		if mode == "after" {
			db, err := sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(start.DataRoot, "applications", "career_hub.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec(`INSERT INTO job_email_connections
				(connection_id,provider,account_address,folder,secret_name,enabled,lookback_days,max_messages_per_sync,revision,created_at,updated_at)
				VALUES ('primary','gmail_imap','alerts@example.test','INBOX',?,1,14,250,1,'t0','t0')`, name)
			closeErr := db.Close()
			if err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
		}
		return
	}
	if stage != "recover" {
		t.Fatal("unknown stage")
	}
	name, err := os.ReadFile(filepath.Join(start.ResourceRoot, "synthetic-target"))
	if err != nil {
		t.Fatal(err)
	}
	backend := &recoveryTestSecrets{t: t, target: "CareerHub/dev/provider/" + string(name), present: true}
	dbPath := filepath.Join(start.DataRoot, "applications", "career_hub.sqlite3")
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "channel" {
		start.Channel = "stable"
	}
	factoryCalls := 0
	result, err := recoverEmailForProcess(ctx, start, func() (secretstore.Backend, error) {
		factoryCalls++
		if mode == "home_changed" {
			raw, problem := datahome.MarkerBytes("dev", "dh_00000000000040008000000000000099")
			if problem != nil {
				t.Fatal(problem)
			}
			writeFixture(t, start.DataRoot, datahome.MarkerPath, string(raw))
		}
		return backend, nil
	})
	after, readErr := os.ReadFile(dbPath)
	if readErr != nil || !bytes.Equal(before, after) {
		t.Fatal("recovery modified database")
	}
	success := mode == "before" || mode == "after"
	if success {
		if err != nil || !result.IntentRemoved || !result.CleanupConfirmed || backend.reads == 0 || factoryCalls != 1 {
			t.Fatal("recovery not confirmed", result, err)
		}
		if mode == "before" && (backend.present || backend.deletes != 1 || result.DatabaseSide != emailcredentials.BeforeCommit) {
			t.Fatal("before-commit cleanup incorrect")
		}
		if mode == "after" && (!backend.present || backend.deletes != 0 || result.DatabaseSide != emailcredentials.AfterCommit) {
			t.Fatal("committed credential not retained")
		}
		return
	}
	if err == nil || result.IntentRemoved || backend.reads != 0 || backend.deletes != 0 {
		t.Fatal("unsafe recovery admitted", err)
	}
	if mode == "none" || mode == "other" || mode == "mixed" || mode == "channel" {
		if factoryCalls != 0 {
			t.Fatal("backend constructed before admission")
		}
	}
	if mode == "home_changed" {
		var problem *emailconnection.Error
		if !errors.As(err, &problem) || problem.Code != "EMAIL_CREDENTIAL_HOME_CHANGED" {
			t.Fatal("admitted identity not pinned", err)
		}
	}
	if mode != "none" && mode != "other" {
		if _, statErr := os.Stat(filepath.Join(start.DataRoot, datahome.EmailCredentialRecoveryPath)); statErr != nil {
			t.Fatal("failed recovery lost intent")
		}
	}
}
