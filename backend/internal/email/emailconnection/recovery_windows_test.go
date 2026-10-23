//go:build windows

package emailconnection

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/email/emailcredentials"
	"careerhub.local/backend/internal/email/emailgate"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/secretstore"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

type fakeSecrets struct {
	values           map[string][]byte
	reads            []string
	readError        bool
	deleteAfterError bool
	onRead           func()
}

func (s *fakeSecrets) Write(target string, value []byte) error {
	if s.values == nil {
		s.values = map[string][]byte{}
	}
	s.values[target] = bytes.Clone(value)
	return nil
}
func (s *fakeSecrets) Read(target string) ([]byte, bool, error) {
	s.reads = append(s.reads, target)
	if s.onRead != nil {
		s.onRead()
	}
	if s.readError {
		return nil, false, errors.New("private fake secret-store details")
	}
	value, found := s.values[target]
	return bytes.Clone(value), found, nil
}
func (s *fakeSecrets) Delete(target string) (bool, error) {
	_, found := s.values[target]
	delete(s.values, target)
	if s.deleteAfterError {
		s.deleteAfterError = false
		return found, errors.New("private fake delete details")
	}
	return found, nil
}

func connectionRow(t *testing.T, home string, witness emailcredentials.RowWitness) {
	t.Helper()
	db, err := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(home, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if !witness.Present {
		return
	}
	enabled := 0
	if witness.Enabled {
		enabled = 1
	}
	_, err = db.Exec(`INSERT INTO job_email_connections
		(connection_id,provider,account_address,folder,secret_name,enabled,
		lookback_days,max_messages_per_sync,revision,created_at,updated_at)
		VALUES ('primary','gmail_imap','alerts@example.test','INBOX',?,?,14,250,?,'t0','t0')`,
		witness.SecretName, enabled, witness.Revision)
	if err != nil {
		t.Fatal(err)
	}
}

func assertWitness(t *testing.T, home string, expected emailcredentials.RowWitness) {
	t.Helper()
	db, err := sqlitestore.OpenReadOnly(context.Background(), filepath.Join(home, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	actual, problem := readWitness(context.Background(), db)
	if err := db.Close(); err != nil || problem != nil || actual != expected {
		t.Fatalf("database witness changed: %+v, want %+v; %v %v", actual, expected, problem, err)
	}
}

func assertSecret(t *testing.T, store *secretstore.Store, name string, want bool) {
	t.Helper()
	if name == "" {
		return
	}
	_, found, err := store.Get(name)
	if err != nil || found != want {
		t.Fatalf("secret presence %q: %t, want %t; %v", name, found, want, err)
	}
}

func runRecoveryScenario(t *testing.T, mode string) {
	t.Helper()
	ctx, home := context.Background(), os.Getenv("CAREERHUB_EMAIL_RECOVERY_HOME")
	info, startup := datahome.BootstrapForProcess(ctx, home, "dev")
	if startup != nil {
		t.Fatal(startup.Code)
	}
	if _, err := schema.PrepareOwned(ctx, home); err != nil {
		t.Fatal(err)
	}
	root, pathError := runtimepaths.Open(home)
	if pathError != nil {
		t.Fatal(pathError.Code)
	}
	defer root.Close()
	oldName, newName := "", ""
	var err error
	if oldName, err = testGenerationName(t, home, *info); err != nil {
		t.Fatal(err)
	}
	if newName, err = testGenerationName(t, home, *info); err != nil || newName == oldName {
		t.Fatal("new synthetic target unavailable")
	}
	unrelatedName, err := testGenerationName(t, home, *info)
	if err != nil || unrelatedName == oldName || unrelatedName == newName {
		t.Fatal("unrelated synthetic target unavailable")
	}
	intent := emailcredentials.ScopedIntent{
		Version: 2, Home: *info, RootFingerprint: testScope(t, home).Fingerprint(), Operation: emailcredentials.Rotate,
		Before: emailcredentials.RowWitness{Present: true, Revision: 4, Enabled: true, SecretName: oldName},
		After:  emailcredentials.RowWitness{Present: true, Revision: 5, Enabled: true, SecretName: newName},
	}
	current, oldFound, newFound := intent.After, true, true
	wantCode, wantOld, wantNew, wantRemoved := "", false, true, true
	wantCleanup := emailcredentials.CleanupOld
	switch mode {
	case "configure-before", "configure-after":
		intent.Operation = emailcredentials.InitialConfigure
		intent.Before = emailcredentials.RowWitness{}
		intent.After = emailcredentials.RowWitness{Present: true, Revision: 1, Enabled: true, SecretName: newName}
		current, oldFound = intent.Before, false
		wantCleanup, wantNew = emailcredentials.CleanupNew, false
		if mode == "configure-after" {
			current, wantCleanup, wantNew = intent.After, emailcredentials.CleanupNone, true
		}
	case "disconnect-after":
		intent.Operation = emailcredentials.Disconnect
		intent.After = emailcredentials.RowWitness{Present: true, Revision: 5, Enabled: false, SecretName: oldName}
		current, newFound, wantNew = intent.After, false, false
	case "forget-after":
		intent.Operation = emailcredentials.Forget
		intent.Before.Enabled = false
		intent.After = emailcredentials.RowWitness{}
		current, newFound, wantNew = intent.After, false, false
	case "rotate-v1-blocked":
		intent.Before.SecretName = emailcredentials.LegacyName
		newName, err = emailcredentials.NewGenerationName(*info)
		if err != nil {
			t.Fatal(err)
		}
		intent.After.SecretName = newName
		oldName, current = emailcredentials.LegacyName, intent.After
		wantCode, wantOld, wantRemoved = "EMAIL_CREDENTIAL_INTENT_INVALID", true, false
	case "promote-before", "promote-after", "promote-old-missing", "promote-new-missing":
		intent.Operation = emailcredentials.AdoptUnbound
		intent.Before.SecretName = emailcredentials.LegacyName
		oldName, wantOld = emailcredentials.LegacyName, true
		wantCleanup = emailcredentials.CleanupNone
		if mode == "promote-before" || mode == "promote-old-missing" {
			current, wantCleanup, wantNew = intent.Before, emailcredentials.CleanupNew, false
		}
		if mode == "promote-old-missing" {
			oldFound, wantOld = false, false
		}
		if mode == "promote-new-missing" {
			newFound, wantNew, wantRemoved = false, false, false
			wantCode = "EMAIL_CREDENTIAL_RECOVERY_REQUIRED"
		}
	case "third-state":
		current.Revision = 99
		wantCode, wantOld, wantRemoved = "EMAIL_CREDENTIAL_DATABASE_STATE_CHANGED", true, false
	case "missing-new":
		newFound = false
		wantCode, wantOld, wantNew, wantRemoved = "EMAIL_CREDENTIAL_RECOVERY_REQUIRED", true, false, false
	case "delete-reported-error":
		wantCode, wantRemoved = "EMAIL_SECRET_STORE_FAILED", false
	case "read-error":
		wantCode, wantOld, wantRemoved = "EMAIL_SECRET_STORE_FAILED", true, false
	case "marker-drift":
		wantCode, wantOld, wantRemoved = "EMAIL_CREDENTIAL_HOME_CHANGED", true, false
	case "corrupt-intent":
		wantCode, wantOld, wantRemoved = "EMAIL_CREDENTIAL_INTENT_INVALID", true, false
	case "foreign-home-intent":
		foreign := *info
		foreign.DataHomeID = "dh_ffffffffffffffffffffffffffffffff"
		if foreign.DataHomeID == info.DataHomeID {
			foreign.DataHomeID = "dh_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
		}
		intent.Home, intent.Operation = foreign, emailcredentials.InitialConfigure
		intent.Before = emailcredentials.RowWitness{}
		newName, err = testGenerationName(t, home, foreign)
		if err != nil {
			t.Fatal(err)
		}
		intent.After = emailcredentials.RowWitness{Present: true, Revision: 1, Enabled: true, SecretName: newName}
		current, oldFound = intent.Before, false
		wantCode, wantOld, wantRemoved = "EMAIL_CREDENTIAL_INTENT_HOME_CHANGED", false, false
	case "intent-drift":
		wantCode, wantOld, wantRemoved = "EMAIL_CREDENTIAL_INTENT_CHANGED", true, false
	case "gates-held":
	case "foreign-sentinel":
		wantCode, wantOld, wantRemoved = "DATA_HOME_RECOVERY_REQUIRED", true, false
	case "no-intent":
		wantCode, wantOld, wantRemoved = "", true, false
		wantCleanup = emailcredentials.CleanupNone
	case "rotate-after":
	default:
		t.Fatalf("unknown fixture mode %q", mode)
	}
	connectionRow(t, home, current)
	backend := &fakeSecrets{}
	store, err := secretstore.New("dev", backend)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(unrelatedName, "SYNTHETIC-UNRELATED-SECRET"); err != nil {
		t.Fatal(err)
	}
	if oldFound {
		if err := store.Set(oldName, "SYNTHETIC-OLD-SECRET"); err != nil {
			t.Fatal(err)
		}
	}
	if newFound {
		if err := store.Set(newName, "SYNTHETIC-NEW-SECRET"); err != nil {
			t.Fatal(err)
		}
	}
	var raw []byte
	if mode != "no-intent" {
		if mode == "corrupt-intent" {
			raw = []byte(`{"version":999}`)
		} else if mode == "rotate-v1-blocked" {
			raw, err = emailcredentials.EncodeIntent(emailcredentials.Intent{Version: 1, Home: *info, Operation: emailcredentials.Rotate, Before: intent.Before, After: intent.After})
			if err != nil {
				t.Fatal(err)
			}
		} else {
			raw, err = testEncodeIntent(t, home, intent)
			if err != nil {
				t.Fatal(err)
			}
		}
		if created, e := root.CreateFile(datahome.EmailCredentialRecoveryPath, raw); e != nil || !created {
			t.Fatal("synthetic intent not published")
		}
	}
	if mode == "foreign-sentinel" {
		if created, e := root.CreateFile(datahome.ResumeRecoveryPath, []byte("foreign synthetic intent")); e != nil || !created {
			t.Fatal("foreign sentinel unavailable")
		}
	}
	if mode == "delete-reported-error" {
		backend.deleteAfterError = true
	}
	if mode == "read-error" {
		backend.readError = true
	}
	if mode == "marker-drift" {
		markerPath := filepath.Join(home, datahome.MarkerPath)
		original, err := os.ReadFile(markerPath)
		if err != nil {
			t.Fatal(err)
		}
		other := "dh_ffffffffffffffffffffffffffffffff"
		if info.DataHomeID == other {
			other = "dh_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
		}
		changed := bytes.Replace(original, []byte(info.DataHomeID), []byte(other), 1)
		if bytes.Equal(changed, original) {
			t.Fatal("marker fixture did not change")
		}
		backend.onRead = func() {
			backend.onRead = nil
			if err := os.WriteFile(markerPath, changed, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if mode == "intent-drift" {
		intentPath := filepath.Join(home, datahome.EmailCredentialRecoveryPath)
		backend.onRead = func() {
			backend.onRead = nil
			if err := os.WriteFile(intentPath, []byte(`{"version":999}`), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if mode == "gates-held" {
		backend.onRead = func() {
			backend.onRead = nil
			if release, err := emailgate.TryAcquire(ctx); release != nil || !errors.Is(err, emailgate.ErrBusy) {
				t.Fatal("email gate was not held through secret read")
			}
			waitCtx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
			defer cancel()
			if release, err := operationgate.Acquire(waitCtx); release != nil || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("application gate was not held through secret read")
			}
		}
	}
	result, problem := Recover(ctx, home, backend)
	if mode == "rotate-v1-blocked" && len(backend.reads) != 0 {
		t.Fatal("old pending journal reached credential backend")
	}
	if strings.HasPrefix(mode, "promote-") {
		for _, target := range backend.reads {
			if strings.HasSuffix(target, "/"+emailcredentials.LegacyName) {
				t.Fatal("promotion recovery read the shared v1 target")
			}
		}
	}
	if wantCode == "" {
		if problem != nil || result.IntentRemoved != wantRemoved || result.Cleanup != wantCleanup {
			t.Fatalf("recovery result: %+v %v", result, problem)
		}
	} else if problem == nil || problem.Code != wantCode || strings.Contains(problem.Error(), "SYNTHETIC-") || result.IntentRemoved {
		t.Fatalf("recovery failure: %+v %v; want %s", result, problem, wantCode)
	}
	backend.readError = false
	assertWitness(t, home, current)
	assertSecret(t, store, oldName, wantOld)
	assertSecret(t, store, newName, wantNew)
	assertSecret(t, store, unrelatedName, true)
	_, present, e := root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
	wantIntentPresent := mode != "no-intent" && !wantRemoved
	if e != nil || present != wantIntentPresent {
		t.Fatalf("intent retained=%t removed=%t err=%v", present, wantRemoved, e)
	}
	if mode == "delete-reported-error" {
		retried, e := Recover(ctx, home, backend)
		if e != nil || !retried.IntentRemoved || retried.Cleanup != emailcredentials.CleanupNone {
			t.Fatalf("recovery did not converge after reported delete error: %+v %v", retried, e)
		}
		assertSecret(t, store, oldName, false)
	}
	if wantCode == "" && wantRemoved {
		again, e := Recover(ctx, home, backend)
		if e != nil || again != (Result{}) {
			t.Fatalf("repeat recovery was not idempotent: %+v %v", again, e)
		}
	}
}

func TestRecoveryOwnedSubprocess(t *testing.T) {
	if mode := os.Getenv("CAREERHUB_EMAIL_RECOVERY_MODE"); mode != "" {
		runRecoveryScenario(t, mode)
		return
	}
	for _, mode := range []string{
		"configure-before", "configure-after", "rotate-after", "disconnect-after", "forget-after",
		"promote-before", "promote-after", "promote-old-missing", "promote-new-missing",
		"rotate-v1-blocked", "third-state", "missing-new", "delete-reported-error",
		"read-error", "marker-drift", "intent-drift", "gates-held", "corrupt-intent",
		"foreign-home-intent", "foreign-sentinel", "no-intent",
	} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRecoveryOwnedSubprocess$")
			command.Env = append(os.Environ(), "CAREERHUB_EMAIL_RECOVERY_MODE="+mode,
				"CAREERHUB_EMAIL_RECOVERY_HOME="+filepath.Join(t.TempDir(), "home"))
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("synthetic recovery %s failed: %v %.900s", mode, err, output)
			}
		})
	}
}

func TestRecoveryPromotionPreservesSharedLegacy(t *testing.T) {
	if base := os.Getenv("CAREERHUB_EMAIL_SHARED_LEGACY_HOME"); base != "" {
		ctx := context.Background()
		firstPath, secondPath := filepath.Join(base, "first"), filepath.Join(base, "second")
		mode := os.Getenv("CAREERHUB_EMAIL_SHARED_LEGACY_MODE")
		path := firstPath
		if mode == "seed-second" {
			path = secondPath
		}
		owned, startup := datahome.BootstrapForProcess(ctx, path, "dev")
		if startup != nil {
			t.Fatal(startup.Code)
		}
		if _, err := schema.PrepareOwned(ctx, path); err != nil {
			t.Fatal(err)
		}
		before := emailcredentials.RowWitness{Present: true, Revision: 4, Enabled: true, SecretName: emailcredentials.LegacyName}
		if mode == "seed-second" {
			connectionRow(t, secondPath, before)
			return
		}
		if mode != "recover-first" {
			t.Fatal("unknown synthetic scenario")
		}
		first := owned
		second, admission := datahome.Inspect(secondPath, nil)
		if admission != nil || second == nil || second.Channel != first.Channel || second.DataHomeID == first.DataHomeID {
			t.Fatal("two distinct same-channel synthetic Homes unavailable")
		}
		newName, err := testGenerationName(t, firstPath, *first)
		if err != nil {
			t.Fatal(err)
		}
		after := emailcredentials.RowWitness{Present: true, Revision: 5, Enabled: true, SecretName: newName}
		connectionRow(t, firstPath, after)
		backend := &fakeSecrets{}
		store, err := secretstore.New(first.Channel, backend)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Set(emailcredentials.LegacyName, "SYNTHETIC-SHARED-SECRET"); err != nil {
			t.Fatal(err)
		}
		if err := store.Set(newName, "SYNTHETIC-NEW-SECRET"); err != nil {
			t.Fatal(err)
		}
		root, pathProblem := runtimepaths.Open(firstPath)
		if pathProblem != nil {
			t.Fatal(pathProblem.Code)
		}
		defer root.Close()
		raw, err := testEncodeIntent(t, firstPath, emailcredentials.ScopedIntent{
			Version: 2, Home: *first, RootFingerprint: testScope(t, firstPath).Fingerprint(), Operation: emailcredentials.AdoptUnbound,
			Before: before, After: after,
		})
		if err != nil {
			t.Fatal(err)
		}
		if created, problem := root.CreateFile(datahome.EmailCredentialRecoveryPath, raw); problem != nil || !created {
			t.Fatal("synthetic promotion intent unavailable")
		}
		result, problem := Recover(ctx, firstPath, backend)
		if problem != nil || !result.IntentRemoved || result.DatabaseSide != emailcredentials.AfterCommit || result.Cleanup != emailcredentials.CleanupNone {
			t.Fatalf("promotion recovery: %+v %v", result, problem)
		}
		for _, target := range backend.reads {
			if strings.HasSuffix(target, "/"+emailcredentials.LegacyName) {
				t.Fatal("promotion recovery read the shared v1 target")
			}
		}
		assertWitness(t, firstPath, after)
		assertWitness(t, secondPath, before)
		assertSecret(t, store, emailcredentials.LegacyName, true)
		assertSecret(t, store, newName, true)
		return
	}
	base := t.TempDir()
	for _, mode := range []string{"seed-second", "recover-first"} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRecoveryPromotionPreservesSharedLegacy$")
		command.Env = append(os.Environ(), "CAREERHUB_EMAIL_SHARED_LEGACY_HOME="+base,
			"CAREERHUB_EMAIL_SHARED_LEGACY_MODE="+mode)
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("shared legacy %s fixture failed: %v %.900s", mode, err, output)
		}
	}
}

func TestRecoveryRefusesUnownedAndInvalidCalls(t *testing.T) {
	backend := &fakeSecrets{}
	if result, problem := Recover(context.Background(), t.TempDir(), backend); result != (Result{}) || problem == nil || problem.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatalf("unowned recovery admitted: %+v %v", result, problem)
	}
	if result, problem := Recover(nil, t.TempDir(), backend); result != (Result{}) || problem == nil || problem.Code != "EMAIL_CREDENTIAL_RECOVERY_INVALID" {
		t.Fatalf("nil context admitted: %+v %v", result, problem)
	}
	if result, problem := Recover(context.Background(), t.TempDir(), nil); result != (Result{}) || problem == nil || problem.Code != "EMAIL_CREDENTIAL_RECOVERY_INVALID" {
		t.Fatalf("nil backend admitted: %+v %v", result, problem)
	}
}
