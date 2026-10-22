//go:build windows

package emailconnection

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/emailcredentials"
	"careerhub.local/backend/internal/emailgate"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/secretstore"
	"careerhub.local/backend/internal/sqlitestore"
)

func TestOwnedEmailConnectionSnapshot(t *testing.T) {
	if os.Getenv("CAREERHUB_OWNED_SNAPSHOT_CHILD") == "1" {
		checkOwnedSnapshot(t, os.Getenv("CAREERHUB_OWNED_SNAPSHOT_HOME"))
		return
	}
	homePath := filepath.Join(t.TempDir(), "home")
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnedEmailConnectionSnapshot$", "-test.v")
	child.Env = append(os.Environ(), "CAREERHUB_OWNED_SNAPSHOT_CHILD=1", "CAREERHUB_OWNED_SNAPSHOT_HOME="+homePath)
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("synthetic owned snapshot child failed: %v %.2000s", err, output)
	}
	// The child released its process claim on exit; the parent must not read
	// the exact same synthetic Home without a new owner claim.
	if _, problem := ReadOwnedSnapshot(context.Background(), homePath, datahome.Info{}, &fakeSecrets{}); problem == nil || problem.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatalf("unclaimed parent read was not denied: %v", problem)
	}
}

func checkOwnedSnapshot(t *testing.T, homePath string) {
	t.Helper()
	ctx := context.Background()
	home, startup := datahome.BootstrapForProcess(ctx, homePath, "dev")
	if startup != nil {
		t.Fatal(startup.Code)
	}
	if _, err := schema.PrepareOwned(ctx, homePath); err != nil {
		t.Fatal(err)
	}
	backend := &fakeSecrets{}
	release, gateErr := emailgate.TryAcquire(ctx)
	if gateErr != nil {
		t.Fatal(gateErr)
	}
	blockedCtx, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
	_, blocked := ReadOwnedSnapshot(blockedCtx, homePath, *home, backend)
	cancel()
	release()
	if blocked == nil || blocked.Code != "EMAIL_CONNECTION_CANCELED" {
		t.Fatalf("credential snapshot bypassed occupied email gate: %v", blocked)
	}
	store, err := secretstore.New(home.Channel, backend)
	if err != nil {
		t.Fatal(err)
	}
	read := func() (ConnectionSnapshot, string) {
		t.Helper()
		snapshot, problem := ReadOwnedSnapshot(ctx, homePath, *home, backend)
		if problem != nil {
			return snapshot, problem.Code
		}
		return snapshot, ""
	}
	if snapshot, code := read(); code != "" || snapshot.Configured || snapshot.CredentialPresent || len(backend.reads) != 0 {
		t.Fatalf("fresh Home snapshot invalid: configured=%v present=%v code=%s", snapshot.Configured, snapshot.CredentialPresent, code)
	}
	backend.readError = true
	wrong := *home
	wrong.DataHomeID = "dh_00000000000000000000000000000000"
	if wrong.DataHomeID == home.DataHomeID {
		wrong.DataHomeID = "dh_11111111111111111111111111111111"
	}
	if _, problem := ReadOwnedSnapshot(ctx, homePath, wrong, backend); problem == nil || problem.Code != "DATA_HOME_CHANGED" {
		t.Fatalf("wrong startup Home identity reached backend: %v", problem)
	}
	wrong = *home
	wrong.Channel = "stable"
	if _, problem := ReadOwnedSnapshot(ctx, homePath, wrong, backend); problem == nil || problem.Code != "DATA_HOME_CHANNEL_MISMATCH" {
		t.Fatalf("wrong startup channel reached backend: %v", problem)
	}
	backend.readError = false
	if err := store.Set(emailcredentials.LegacyName, "SYNTHETIC-LEGACY"); err != nil {
		t.Fatal(err)
	}
	if snapshot, code := read(); code != "" || snapshot.Configured || snapshot.CredentialPresent || len(backend.reads) != 0 {
		t.Fatalf("missing row probed legacy credential: configured=%v present=%v code=%s", snapshot.Configured, snapshot.CredentialPresent, code)
	}
	root, pathError := runtimepaths.Open(homePath)
	if pathError != nil {
		t.Fatal(pathError)
	}
	defer root.Close()
	scope, pathError := root.CredentialScope()
	if pathError != nil {
		t.Fatal(pathError)
	}
	target, err := emailcredentials.NewScopedGenerationName(*home, scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(target, "SYNTHETIC-V3"); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(homePath, "applications", "career_hub.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `INSERT INTO job_email_connections (
		connection_id, provider, account_address, folder, secret_name,
		enabled, lookback_days, max_messages_per_sync, revision,
		created_at, updated_at
	) VALUES ('primary', 'gmail_imap', 'jobs@example.test', 'INBOX', ?,
		1, 14, 250, 1, 't0', 't0')`, target); err != nil {
		t.Fatal(err)
	}
	snapshot, code := read()
	encoded, encodeErr := json.Marshal(snapshot)
	if code != "" || !snapshot.Configured || !snapshot.CredentialPresent ||
		snapshot.AccountAddress != "jobs@example.test" || encodeErr != nil ||
		strings.Contains(string(encoded), target) || strings.Contains(string(encoded), "SYNTHETIC-V3") {
		t.Fatalf("v3 snapshot invalid: configured=%v present=%v code=%s", snapshot.Configured, snapshot.CredentialPresent, code)
	}
	if _, err := store.Delete(target); err != nil {
		t.Fatal(err)
	}
	if snapshot, code := read(); code != "" || !snapshot.Configured || snapshot.CredentialPresent {
		t.Fatalf("missing v3 snapshot invalid: configured=%v present=%v code=%s", snapshot.Configured, snapshot.CredentialPresent, code)
	}
	if _, err := db.ExecContext(ctx, `UPDATE job_email_connections SET secret_name=?`, emailcredentials.LegacyName); err != nil {
		t.Fatal(err)
	}
	legacyReads := len(backend.reads)
	if snapshot, code := read(); code != "" || !snapshot.Configured || snapshot.CredentialPresent ||
		snapshot.LastErrorCode != "EMAIL_CREDENTIAL_RECONNECT_REQUIRED" || len(backend.reads) != legacyReads {
		t.Fatalf("legacy reconnect snapshot invalid: configured=%v present=%v code=%s", snapshot.Configured, snapshot.CredentialPresent, code)
	}
	oldTarget, err := emailcredentials.NewGenerationName(*home)
	if err != nil {
		t.Fatal(err)
	}
	foreignParts := strings.Split(target, ".")
	foreignParts[2] = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	for _, unbound := range []string{oldTarget, strings.Join(foreignParts, ".")} {
		if _, err := db.ExecContext(ctx, `UPDATE job_email_connections SET secret_name=?`, unbound); err != nil {
			t.Fatal(err)
		}
		backend.readError = true // No read is permitted, even for mere presence.
		if snapshot, code := read(); code != "" || !snapshot.Configured || snapshot.CredentialPresent ||
			snapshot.LastErrorCode != "EMAIL_CREDENTIAL_RECONNECT_REQUIRED" || len(backend.reads) != legacyReads {
			t.Fatal("unbound/copied pointer reached secret store or lost metadata")
		}
	}
	backend.readError = false
	if _, err := db.ExecContext(ctx, `UPDATE job_email_connections SET secret_name=?`, target); err != nil {
		t.Fatal(err)
	}
	backend.readError = true
	if _, code := read(); code != "EMAIL_SECRET_STORE_FAILED" {
		t.Fatalf("backend error not fail-closed: %s", code)
	}
	if _, err := db.ExecContext(ctx, `UPDATE job_email_connections SET secret_name='foreign-target'`); err != nil {
		t.Fatal(err)
	}
	if _, code := read(); code != "EMAIL_CONNECTION_INVALID" {
		t.Fatalf("foreign target reached secret backend: %s", code)
	}
	parts := strings.Split(target, ".")
	if len(parts) != 4 {
		t.Fatal("unexpected synthetic generation shape")
	}
	foreignID := strings.Repeat("0", 32)
	if parts[2] == foreignID {
		foreignID = strings.Repeat("1", 32)
	}
	parts[2] = foreignID
	if _, err := db.ExecContext(ctx, `UPDATE job_email_connections SET secret_name=?`, strings.Join(parts, ".")); err != nil {
		t.Fatal(err)
	}
	if _, code := read(); code != "EMAIL_CONNECTION_INVALID" {
		t.Fatalf("foreign Home generation reached secret backend: %s", code)
	}
	if _, err := db.ExecContext(ctx, `UPDATE job_email_connections SET secret_name=?`, target); err != nil {
		t.Fatal(err)
	}
	backend.readError = false
	if _, err := db.ExecContext(ctx, `PRAGMA user_version=26`); err != nil {
		t.Fatal(err)
	}
	if _, code := read(); code != "EMAIL_CONNECTION_SCHEMA_PREPARATION_REQUIRED" {
		t.Fatalf("old schema admitted: %s", code)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA user_version=28`); err != nil {
		t.Fatal(err)
	}
	if _, code := read(); code != "EMAIL_CONNECTION_SCHEMA_TOO_NEW" {
		t.Fatalf("future schema admitted: %s", code)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA user_version=27`); err != nil {
		t.Fatal(err)
	}
	var injectedErr error
	backend.onRead = func() {
		injectedErr = os.WriteFile(filepath.Join(homePath, datahome.EmailCredentialRecoveryPath), []byte("synthetic-pending-intent"), 0600)
	}
	if _, code := read(); injectedErr != nil || code != "DATA_HOME_CHANGED" {
		t.Fatalf("new recovery intent was not detected: code=%s error=%v", code, injectedErr)
	}
	backend.onRead = nil
	backend.readError = true
	if _, code := read(); code != "DATA_HOME_RECOVERY_REQUIRED" {
		t.Fatalf("pre-existing recovery intent reached secret backend: %s", code)
	}
}
