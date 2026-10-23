//go:build windows

package emailconnection

import (
	"bytes"
	"context"
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
)

type publicationSecrets struct {
	*fakeSecrets
	reads   []string
	deletes []string
}

func (backend *publicationSecrets) Read(target string) ([]byte, bool, error) {
	backend.reads = append(backend.reads, target)
	return backend.fakeSecrets.Read(target)
}

func (backend *publicationSecrets) Delete(target string) (bool, error) {
	backend.deletes = append(backend.deletes, target)
	return backend.fakeSecrets.Delete(target)
}

func publicationIntent(t *testing.T, path string, home datahome.Info) emailcredentials.ScopedIntent {
	t.Helper()
	name, err := testGenerationName(t, path, home)
	if err != nil {
		t.Fatal(err)
	}
	return emailcredentials.ScopedIntent{
		Version: 2, Home: home, RootFingerprint: testScope(t, path).Fingerprint(), Operation: emailcredentials.InitialConfigure,
		After: emailcredentials.RowWitness{Present: true, Revision: 1, Enabled: true, SecretName: name},
	}
}

func publicationScenario(t *testing.T, mode string) {
	t.Helper()
	ctx, homePath := context.Background(), os.Getenv("CAREERHUB_EMAIL_PUBLICATION_HOME")
	home, startup := datahome.BootstrapForProcess(ctx, homePath, "dev")
	if startup != nil {
		t.Fatal(startup.Code)
	}
	if _, err := schema.PrepareOwned(ctx, homePath); err != nil {
		t.Fatal(err)
	}
	intent := publicationIntent(t, homePath, *home)
	backend := &publicationSecrets{fakeSecrets: &fakeSecrets{}}
	store, err := secretstore.New(home.Channel, backend)
	if err != nil {
		t.Fatal(err)
	}
	unrelatedName, err := emailcredentials.NewGenerationName(*home)
	if err != nil || unrelatedName == intent.After.SecretName {
		t.Fatal("unrelated target unavailable")
	}
	if err := store.Set(unrelatedName, "SYNTHETIC-UNRELATED-SECRET"); err != nil {
		t.Fatal(err)
	}
	root, pathProblem := runtimepaths.Open(homePath)
	if pathProblem != nil {
		t.Fatal(pathProblem.Code)
	}
	defer root.Close()
	wantCode := ""
	wantExisting := []byte(nil)
	wantOld := false
	switch mode {
	case "configure", "postverify-failure":
	case "new-collision":
		if e := store.Set(intent.After.SecretName, "SYNTHETIC-COLLISION-SECRET"); e != nil {
			t.Fatal(e)
		}
		wantCode = "EMAIL_CREDENTIAL_TARGET_COLLISION"
	case "new-read-error":
		backend.readError = true
		wantCode = "EMAIL_SECRET_STORE_FAILED"
	case "rotate", "old-missing", "old-empty", "old-read-error", "v1-blocked", "v1-disconnect", "v1-forget", "disabled-old-missing":
		oldName, e := testGenerationName(t, homePath, *home)
		if e != nil || oldName == intent.After.SecretName {
			t.Fatal("old target unavailable")
		}
		if strings.HasPrefix(mode, "v1-") {
			oldName = emailcredentials.LegacyName
		}
		intent.Operation = emailcredentials.Rotate
		intent.Before = emailcredentials.RowWitness{Present: true, Revision: 4, Enabled: true, SecretName: oldName}
		intent.After.Revision = 5
		if mode == "v1-disconnect" {
			intent.Operation = emailcredentials.Disconnect
			intent.After.Enabled, intent.After.SecretName = false, oldName
		}
		if mode == "v1-forget" {
			intent.Operation = emailcredentials.Forget
			intent.After = emailcredentials.RowWitness{}
		}
		if mode == "disabled-old-missing" {
			intent.Before.Enabled = false
		}
		connectionRow(t, homePath, intent.Before)
		if mode == "old-empty" {
			backend.values["CareerHub/dev/provider/"+oldName] = []byte{}
		} else if mode != "old-missing" && mode != "disabled-old-missing" {
			if e := store.Set(oldName, "SYNTHETIC-OLD-SECRET"); e != nil {
				t.Fatal(e)
			}
			wantOld = true
		}
		switch mode {
		case "old-missing":
			wantCode = "EMAIL_CREDENTIAL_MISSING"
		case "old-empty":
			wantCode = "EMAIL_CREDENTIAL_RECOVERY_REQUIRED"
		case "old-read-error":
			backend.readError = true
			wantCode = "EMAIL_SECRET_STORE_FAILED"
		case "v1-blocked", "v1-disconnect", "v1-forget":
			wantCode = "EMAIL_CREDENTIAL_INTENT_INVALID"
		}
	case "promote-v1", "promote-v1-missing", "promote-v1-new-collision":
		intent.Operation = emailcredentials.AdoptUnbound
		intent.Before = emailcredentials.RowWitness{Present: true, Revision: 4, Enabled: true, SecretName: emailcredentials.LegacyName}
		intent.After.Revision = 5
		connectionRow(t, homePath, intent.Before)
		if mode != "promote-v1-missing" {
			if e := store.Set(emailcredentials.LegacyName, "SYNTHETIC-SHARED-SECRET"); e != nil {
				t.Fatal(e)
			}
			wantOld = true
		}
		if mode == "promote-v1-new-collision" {
			if e := store.Set(intent.After.SecretName, "SYNTHETIC-COLLISION-SECRET"); e != nil {
				t.Fatal(e)
			}
			wantCode = "EMAIL_CREDENTIAL_TARGET_COLLISION"
		}
	case "wrong-home":
		intent.Home.DataHomeID = "dh_ffffffffffffffffffffffffffffffff"
		if intent.Home.DataHomeID == home.DataHomeID {
			intent.Home.DataHomeID = "dh_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
		}
		intent = publicationIntent(t, homePath, intent.Home)
		wantCode = "EMAIL_CREDENTIAL_INTENT_HOME_CHANGED"
	case "wrong-before":
		intent.Operation = emailcredentials.Rotate
		missingName, e := testGenerationName(t, homePath, *home)
		if e != nil {
			t.Fatal(e)
		}
		intent.Before = emailcredentials.RowWitness{Present: true, Revision: 4, Enabled: true, SecretName: missingName}
		intent.After.Revision = 5
		wantCode = "EMAIL_CREDENTIAL_DATABASE_STATE_CHANGED"
	case "foreign-sentinel":
		if created, e := root.CreateFile(datahome.ResumeRecoveryPath, []byte("synthetic foreign intent")); e != nil || !created {
			t.Fatal("foreign sentinel fixture unavailable")
		}
		wantCode = "DATA_HOME_RECOVERY_REQUIRED"
	case "duplicate":
		wantExisting = []byte("synthetic prior intent")
		if created, e := root.CreateFile(datahome.EmailCredentialRecoveryPath, wantExisting); e != nil || !created {
			t.Fatal("existing intent fixture unavailable")
		}
		wantCode = "DATA_HOME_RECOVERY_REQUIRED"
	case "canceled":
		wantCode = "EMAIL_CREDENTIAL_OPERATION_CANCELED"
	default:
		t.Fatalf("unknown publication fixture %q", mode)
	}
	if mode == "postverify-failure" {
		raw, e := testEncodeIntent(t, homePath, intent)
		if e != nil {
			t.Fatal(e)
		}
		problem := publishIntent(root, raw, func() *Error { return failure("EMAIL_CREDENTIAL_PUBLICATION_UNCONFIRMED") })
		if problem == nil || problem.Code != "EMAIL_CREDENTIAL_PUBLICATION_UNCONFIRMED" {
			t.Fatalf("post-publication failure was hidden: %v", problem)
		}
		observed, present, pathErr := root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
		if pathErr != nil || !present || !bytes.Equal(observed, raw) {
			t.Fatal("uncertain publication removed or changed its intent")
		}
		return
	}
	operationCtx := ctx
	if mode == "canceled" {
		var cancel context.CancelFunc
		operationCtx, cancel = context.WithCancel(ctx)
		cancel()
	}
	session, problem := stageCredentialIntent(operationCtx, homePath, backend, intent)
	for _, target := range backend.reads {
		if strings.HasSuffix(target, "/"+unrelatedName) {
			t.Fatal("publication read an unrelated target")
		}
		if strings.HasPrefix(mode, "promote-v1") && strings.HasSuffix(target, "/"+emailcredentials.LegacyName) {
			t.Fatal("promotion publication read the shared v1 target")
		}
	}
	if strings.HasPrefix(mode, "v1-") && len(backend.reads) != 0 {
		t.Fatal("legacy retirement read a shared credential before ownership proof")
	}
	if len(backend.deletes) != 0 {
		t.Fatal("publication deleted a credential")
	}
	if wantCode != "" {
		if session != nil || problem == nil || problem.Code != wantCode {
			t.Fatalf("unsafe publication result: %v %v; want %s", session, problem, wantCode)
		}
		observed, present, e := root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
		if e != nil || (len(wantExisting) == 0 && present) || (len(wantExisting) > 0 && (!present || !bytes.Equal(observed, wantExisting))) {
			t.Fatal("failed publication changed the intent")
		}
		backend.readError = false
		if mode == "new-collision" {
			assertSecret(t, store, intent.After.SecretName, true)
		}
		assertSecret(t, store, unrelatedName, true)
		releaseEmail, gateErr := emailgate.TryAcquire(ctx)
		if gateErr != nil {
			t.Fatal("failed publication retained the email gate")
		}
		releaseEmail()
		availableCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		releaseGeneral, gateErr := operationgate.Acquire(availableCtx)
		if gateErr != nil {
			t.Fatal("failed publication retained the application gate")
		}
		releaseGeneral()
		return
	}
	if problem != nil || session == nil {
		t.Fatalf("publication failed: %v", problem)
	}
	if session.pins == nil || session.pins.Check() != nil {
		t.Fatal("publication session did not retain SQLite path pins")
	}
	raw, e := testEncodeIntent(t, homePath, intent)
	if e != nil {
		t.Fatal(e)
	}
	observed, present, pathErr := root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
	if pathErr != nil || !present || !bytes.Equal(observed, raw) {
		t.Fatal("published intent did not match exact bytes")
	}
	if info, e := datahome.Inspect(homePath, nil); info != nil || e == nil || e.Code != "DATA_HOME_RECOVERY_REQUIRED" {
		t.Fatal("ordinary admission did not block pending intent")
	}
	if release, e := emailgate.TryAcquire(ctx); release != nil || e != emailgate.ErrBusy {
		t.Fatal("publication session released email gate before close")
	}
	gateCtx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	if release, e := operationgate.Acquire(gateCtx); release != nil || e == nil {
		t.Fatal("publication session released application gate before close")
	}
	if e := session.Close(); e != nil {
		t.Fatal(e.Code)
	}
	if e := session.Close(); e != nil {
		t.Fatal("session close is not idempotent")
	}
	if _, e := Recover(ctx, homePath, backend); e != nil {
		t.Fatalf("published pre-commit intent could not recover: %s", e.Code)
	}
	_, present, pathErr = root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
	if pathErr != nil || present {
		t.Fatal("recovery did not remove the intent")
	}
	assertWitness(t, homePath, intent.Before)
	assertSecret(t, store, intent.After.SecretName, false)
	if intent.Before.Present {
		assertSecret(t, store, intent.Before.SecretName, wantOld)
	}
	assertSecret(t, store, unrelatedName, true)
}

func TestPublicationOwnedSubprocess(t *testing.T) {
	if mode := os.Getenv("CAREERHUB_EMAIL_PUBLICATION_MODE"); mode != "" {
		publicationScenario(t, mode)
		return
	}
	for _, mode := range []string{
		"configure", "rotate", "new-collision", "new-read-error", "old-missing", "old-empty",
		"old-read-error", "v1-blocked", "v1-disconnect", "v1-forget",
		"promote-v1", "promote-v1-missing", "promote-v1-new-collision",
		"disabled-old-missing", "wrong-home", "wrong-before",
		"foreign-sentinel", "duplicate", "canceled", "postverify-failure",
	} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPublicationOwnedSubprocess$")
			command.Env = append(os.Environ(), "CAREERHUB_EMAIL_PUBLICATION_MODE="+mode,
				"CAREERHUB_EMAIL_PUBLICATION_HOME="+filepath.Join(t.TempDir(), "home"))
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("synthetic publication %s failed: %v %.900s", mode, err, output)
			}
		})
	}
}

func TestPublicationRefusesUnownedAndMalformedCalls(t *testing.T) {
	intent := emailcredentials.ScopedIntent{Version: 2, Home: datahome.Info{FormatVersion: 2, Channel: "dev", Initialized: true, DataHomeID: "dh_0123456789abcdef0123456789abcdef"}}
	backend := &fakeSecrets{}
	if session, problem := stageCredentialIntent(context.Background(), t.TempDir(), backend, intent); session != nil || problem == nil || problem.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatalf("unowned publication admitted: %v %v", session, problem)
	}
	if session, problem := stageCredentialIntent(nil, t.TempDir(), backend, intent); session != nil || problem == nil || problem.Code != "EMAIL_CREDENTIAL_PUBLICATION_INVALID" {
		t.Fatalf("nil context admitted: %v %v", session, problem)
	}
	if session, problem := stageCredentialIntent(context.Background(), t.TempDir(), nil, intent); session != nil || problem == nil || problem.Code != "EMAIL_CREDENTIAL_PUBLICATION_INVALID" {
		t.Fatalf("nil backend admitted: %v %v", session, problem)
	}
	intent.After.SecretName = "arbitrary-target"
	if session, problem := stageCredentialIntent(context.Background(), t.TempDir(), backend, intent); session != nil || problem == nil || problem.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatalf("invalid intent admitted: %v %v", session, problem)
	}
}
