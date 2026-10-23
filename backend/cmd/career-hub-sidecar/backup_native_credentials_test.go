//go:build windows

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/documents/envelope"
	"careerhub.local/backend/internal/storage/documents/keys"
	"careerhub.local/backend/internal/storage/homebackup"
	"careerhub.local/backend/internal/storage/secretstore"
)

// Opt-in packaged-binary qualification, not a mock or an installed-UI test.
// Only one freshly generated, absence-checked Windows credential is touched.
// Test fixtures never use an existing Home, mailbox or document. An interrupted
// parent process requires inspection of the logged fixture ID before cleanup.
func TestPackagedBackupRestoreNativeDocumentKey(t *testing.T) {
	if os.Getenv("CAREERHUB_TEST_NATIVE_CREDENTIALS") != "1" {
		t.Skip("opt-in synthetic native credentials not enabled")
	}
	executable, expected := os.Getenv("CAREERHUB_GO_SIDECAR"), os.Getenv("CAREERHUB_GO_SIDECAR_SHA256")
	resources := os.Getenv("CAREERHUB_GO_RESOURCES")
	if !filepath.IsAbs(executable) || !filepath.IsAbs(resources) || len(expected) != 64 {
		t.Fatal("absolute packaged executable/resources and exact SHA-256 required")
	}
	read := func(name string) []byte {
		t.Helper()
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal("fixture read failed", err)
		}
		return raw
	}
	digest := func(raw []byte) string { value := sha256.Sum256(raw); return hex.EncodeToString(value[:]) }
	if digest(read(executable)) != expected {
		t.Fatal("packaged executable digest mismatch; no credential access")
	}
	defer func() {
		if digest(read(executable)) != expected {
			t.Error("packaged executable changed during qualification")
		}
	}()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	write := func(name string, value []byte) {
		t.Helper()
		file := filepath.Join(home, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, value, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal("fixture identity unavailable")
	}
	nonce := hex.EncodeToString(random[:])
	homeID, keyID, docID := "dh_"+nonce, "key_"+nonce, "doc_"+nonce
	target := "CareerHub/dev/document-key/" + homeID + "/" + keyID
	backend, err := secretstore.NewWindowsBackend()
	if err != nil {
		t.Fatal("native key adapter unavailable; enabled check failed")
	}
	if _, present, err := backend.Read(target); err != nil || present {
		t.Fatal("synthetic credential is not available; no writes started")
	}
	t.Logf("synthetic backup credential fixture ID: %s", nonce)
	// Register before the first write: a failed native write may have effects.
	t.Cleanup(func() {
		if _, err := backend.Delete(target); err != nil {
			t.Errorf("native fixture key cleanup failed; fixture=%s", nonce)
			return
		}
		if _, present, err := backend.Read(target); err != nil || present {
			t.Errorf("native fixture key cleanup unconfirmed; fixture=%s", nonce)
			return
		}
		t.Log("exact synthetic credential removal verified")
	})
	key := make([]byte, keys.KeyBytes)
	if _, err := rand.Read(key); err != nil {
		t.Fatal("synthetic key generation failed")
	}
	setKey := func(value []byte) {
		t.Helper()
		if err := backend.Write(target, value); err != nil {
			t.Fatal("synthetic native key write failed")
		}
	}
	setKey(key)
	marker, p := datahome.MarkerBytes("dev", homeID)
	if p != nil {
		t.Fatal(p)
	}
	write(datahome.MarkerPath, marker)
	state := keys.State{Version: 1, Algorithm: keys.Algorithm, DataHomeID: homeID, ActiveKeyID: keyID}
	stateBytes, err := state.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	blobPath := "documents/files/" + docID + ".chdoc"
	seed := func(content string) map[string][]byte {
		t.Helper()
		plain := []byte(content)
		blob, err := envelope.Encrypt(plain, key, envelope.Context{DataHomeID: homeID, KeyID: keyID, Purpose: "blob", ObjectID: docID})
		if err != nil {
			t.Fatal(err)
		}
		record := map[string]any{"id": docID, "title": "Synthetic backup document", "category": "Identity", "issued_on": "", "expires_on": "", "notes": "", "filename": "fixture.pdf", "content_type": "application/pdf", "size_bytes": len(plain), "sha256": digest(plain), "created_at": "2026-09-23T12:00:00+00:00", "updated_at": "2026-09-23T12:00:00+00:00", "archived": false}
		ledger, err := json.Marshal(map[string]any{"version": 1, "documents": []any{record}})
		if err != nil {
			t.Fatal(err)
		}
		sealed, err := envelope.Encrypt(ledger, key, envelope.Context{DataHomeID: homeID, KeyID: keyID, Purpose: "ledger", ObjectID: "ledger"})
		if err != nil {
			t.Fatal(err)
		}
		files := map[string][]byte{"documents/key_state.json": stateBytes, "documents/ledger.chdoc": sealed, blobPath: blob, "jobs/fixture.txt": []byte("synthetic job marker " + digest(plain))}
		for name, raw := range files {
			write(name, raw)
		}
		return files
	}
	request := map[string]any{"command": "backup_create", "protocol_version": 1, "data_root": home, "resource_root": resources, "channel": "dev", "confirm_writers_stopped": true, "archive_relative_path": "backups/encrypted.zip"}
	run := func(wantExit int) map[string]any {
		t.Helper()
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, executable, "--desktop-maintenance")
		cmd.Dir = resources
		cmd.Stdin = bytes.NewReader(append(raw, '\n'))
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != wantExit {
			// Never dump output from a native credential-bearing process.
			t.Fatalf("maintenance exit mismatch: expected=%d process=%v error=%v", wantExit, cmd.ProcessState, err)
		}
		if len(stderr.Bytes()) != 0 || bytes.Contains(out, key) || bytes.Contains(out, []byte(target)) {
			t.Fatal("maintenance emitted unexpected or secret-bearing output")
		}
		var result map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(out), &result); err != nil {
			t.Fatal("expected one JSON maintenance result")
		}
		return result
	}
	assertFiles := func(prefix string, files map[string][]byte) {
		t.Helper()
		for name, raw := range files {
			if !bytes.Equal(read(filepath.Join(home, prefix, filepath.FromSlash(name))), raw) {
				t.Fatal("encrypted fixture bytes changed", name)
			}
		}
	}
	original := seed("synthetic original encrypted document")
	created := run(0)
	cleanup, ok := created["snapshot_cleanup"].(map[string]any)
	if created["success"] != true || created["archive_publication"] != "published" || !ok || cleanup["status"] != "complete" {
		t.Fatal("encrypted backup or snapshot cleanup was not confirmed")
	}
	snapshot, ok := created["snapshot_relative_path"].(string)
	if !ok || !protocol.ValidBackupSnapshotPath(snapshot) {
		t.Fatal("invalid cleanup snapshot path")
	}
	if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(snapshot))); !os.IsNotExist(err) {
		t.Fatal("confirmed backup cleanup retained its snapshot")
	}
	archive := filepath.Join(home, "backups/encrypted.zip")
	archiveBytes := read(archive)
	reader, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != len(original)+1 {
		t.Fatal("archive contains unexpected entries")
	}
	seen := map[string]bool{}
	for _, file := range reader.File {
		if seen[file.Name] {
			t.Fatal("duplicate archive member")
		}
		seen[file.Name] = true
		handle, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(io.LimitReader(handle, 1<<20))
		closeErr := handle.Close()
		if err != nil || closeErr != nil || bytes.Contains(raw, key) || bytes.Contains(raw, []byte("synthetic original encrypted document")) {
			t.Fatal("archive read failed or contained unprotected key/document content")
		}
		if file.Name == homebackup.ManifestName {
			if _, p := homebackup.ParseManifest(raw); p != nil {
				t.Fatal("archive manifest invalid")
			}
			continue
		}
		if want, exists := original[file.Name]; !exists || !bytes.Equal(raw, want) {
			t.Fatal("archive does not preserve the exact encrypted file", file.Name)
		}
	}
	newer := seed("synthetic newer encrypted document")
	request["command"] = "backup_restore"
	restored := run(0)
	if restored["success"] != true || restored["complete"] != true || restored["journal_removal"] != "removed" {
		t.Fatal("encrypted restore did not commit")
	}
	retained, ok := restored["recovery_relative_path"].(string)
	restoreID, idOK := restored["restore_id"].(string)
	if !ok || !idOK || !protocol.ValidRestoreID(restoreID) || !protocol.ValidRestoreRecoveryPath(retained, restoreID) {
		t.Fatal("invalid retained-original path")
	}
	assertFiles("", original)
	assertFiles(filepath.FromSlash(retained), newer)
	assertUnchanged := func() {
		t.Helper()
		if !bytes.Equal(read(archive), archiveBytes) || !bytes.Equal(read(filepath.Join(home, datahome.MarkerPath)), marker) {
			t.Fatal("archive or Home identity changed")
		}
		if _, err := os.Stat(filepath.Join(home, datahome.RestoreRecoveryPath)); !os.IsNotExist(err) {
			t.Fatal("unexpected restore recovery intent")
		}
		if !bytes.Equal(read(filepath.Join(home, "jobs/fixture.txt")), original["jobs/fixture.txt"]) {
			t.Fatal("unrelated active job changed")
		}
	}
	assertUnchanged()
	if deleted, err := backend.Delete(target); err != nil || !deleted {
		t.Fatal("could not remove exact fixture key for missing-key case")
	}
	refuse := func(code string) {
		t.Helper()
		result := run(4)
		if result["success"] != false || result["error_code"] != code {
			t.Fatalf("expected bounded refusal %s, got code=%v", code, result["error_code"])
		}
		assertUnchanged()
	}
	// Existing encrypted active data must refuse before any activation or key creation.
	refuse("BACKUP_DOCUMENT_KEY_UNAVAILABLE")
	assertFiles("", original)
	request["command"], request["archive_relative_path"] = "backup_create", "backups/missing-key.zip"
	refuse("BACKUP_DOCUMENT_KEY_UNAVAILABLE")
	if _, err := os.Stat(filepath.Join(home, "backups/missing-key.zip")); !os.IsNotExist(err) {
		t.Fatal("failed key validation published an archive")
	}
	// With no active documents, refusal must come from validating the archived
	// candidate. Retain the original synthetic directory outside the active Home.
	if err := os.Rename(filepath.Join(home, "documents"), filepath.Join(base, "retained-documents")); err != nil {
		t.Fatal(err)
	}
	request["command"], request["archive_relative_path"] = "backup_restore", "backups/encrypted.zip"
	refuse("BACKUP_DOCUMENT_KEY_UNAVAILABLE")
	if _, present, err := backend.Read(target); err != nil || present {
		t.Fatal("missing key was recreated or cannot be checked")
	}
	wrongKey := bytes.Clone(key)
	wrongKey[0] ^= 0xff
	setKey(wrongKey)
	refuse("BACKUP_DOCUMENT_AUTHENTICATION_FAILED")
	if observed, present, err := backend.Read(target); err != nil || !present || !bytes.Equal(observed, wrongKey) {
		t.Fatal("authentication refusal replaced the existing key")
	}
	if _, err := os.Stat(filepath.Join(home, "documents")); !os.IsNotExist(err) {
		t.Fatal("unvalidated archive activated documents")
	}
	assertFiles(filepath.FromSlash(retained), newer)
	setKey(key) // Only the known fixture slot; never a product fallback.
	if result := run(0); result["success"] != true || result["complete"] != true {
		t.Fatal("explicit restore with the original key failed")
	}
	assertFiles("", original)
	assertUnchanged()
	storedKey, present, err := backend.Read(target)
	if err != nil || !present || !bytes.Equal(storedKey, key) {
		t.Fatal("restore replaced the native key")
	}
	opened, err := envelope.Decrypt(read(filepath.Join(home, filepath.FromSlash(blobPath))), storedKey,
		envelope.Context{DataHomeID: homeID, KeyID: keyID, Purpose: "blob", ObjectID: docID})
	if err != nil || !bytes.Equal(opened.Plaintext, []byte("synthetic original encrypted document")) {
		t.Fatal("restored document does not authenticate with original native key")
	}
}
