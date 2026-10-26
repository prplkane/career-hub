//go:build windows

package readonly

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/documents/envelope"
	"careerhub.local/backend/internal/storage/documents/keys"
	"careerhub.local/backend/internal/storage/documents/ledger"
	"careerhub.local/backend/internal/storage/secretstore"
)

const activeDocumentKey = "key_11111111111111111111111111111111"
const retainedDocumentKey = "key_22222222222222222222222222222222"

type documentFixture struct {
	Start          protocol.StartRecord
	Mode           string
	Keys           map[string][]byte
	BackendFailure bool
	Unowned        bool
}
type documentOutcome struct {
	Code      string            `json:"code"`
	Documents []ledger.Document `json:"documents"`
	Revision  string            `json:"revision"`
	KeyReads  []string          `json:"key_reads"`
}

func documentTarget(key string) string { return "CareerHub/dev/document-key/" + fixtureID + "/" + key }

type documentFixtureBackend struct {
	t       *testing.T
	channel string
	values  map[string][]byte
	reads   []string
	failure bool
}

func (b *documentFixtureBackend) Read(target string) ([]byte, bool, error) {
	hash := sha256.Sum256([]byte(target))
	b.reads = append(b.reads, hex.EncodeToString(hash[:]))
	if !strings.HasPrefix(target, "CareerHub/"+b.channel+"/document-key/"+fixtureID+"/") {
		b.t.Fatal("reader escaped admitted key namespace")
	}
	if b.failure {
		return nil, false, &secretstore.BackendError{Code: secretstore.CodeWindowsFailure, Operation: "read", Win32Code: 5}
	}
	value, found := b.values[target]
	return bytes.Clone(value), found, nil
}
func (b *documentFixtureBackend) Write(string, []byte) error {
	b.t.Fatal("metadata read wrote a credential")
	return nil
}
func (b *documentFixtureBackend) Delete(string) (bool, error) {
	b.t.Fatal("metadata read deleted a credential")
	return false, nil
}

func preparedDocumentFixture(t *testing.T, mode string) documentFixture {
	t.Helper()
	_, start := fixture(t)
	request := documentFixture{Start: start, Mode: mode, Keys: map[string][]byte{documentTarget(activeDocumentKey): bytes.Repeat([]byte{1}, 32)}}
	if mode == "empty" || mode == "unowned_empty" {
		request.Unowned = mode == "unowned_empty"
		return request
	}
	if mode == "root_file" {
		writeFixture(t, start.DataRoot, documentRootPath, "synthetic")
		return request
	}
	if e := os.MkdirAll(filepath.Join(start.DataRoot, "documents", "files"), 0700); e != nil {
		t.Fatal(e)
	}
	if mode == "empty_directories" {
		return request
	}
	state := keys.State{Version: 1, Algorithm: keys.Algorithm, DataHomeID: fixtureID, ActiveKeyID: activeDocumentKey}
	if mode == "state_other_home" {
		state.DataHomeID = "dh_99999999999999999999999999999999"
	}
	stateBytes, e := state.CanonicalBytes()
	if e != nil {
		t.Fatal(e)
	}
	if mode != "state_missing" && mode != "orphan_without_state" {
		writeFixture(t, start.DataRoot, documentStatePath, string(stateBytes))
	}
	if mode == "active_without_ledger" {
		return request
	}
	documents := []ledger.Document{
		{ID: "doc_" + strings.Repeat("a", 32), Title: " Synthetic identity ", Category: "Identity", Filename: "original.pdf", ContentType: "application/pdf", SizeBytes: 12, SHA256: strings.Repeat("b", 64), CreatedAt: "2026-09-08T12Z", UpdatedAt: "2026-09-08T12Z"},
		{ID: "doc_" + strings.Repeat("c", 32), Title: "Archived title", Category: "Other", Filename: "archived.png", ContentType: "image/png", SizeBytes: 17, SHA256: strings.Repeat("d", 64), CreatedAt: "2026-09-08T12Z", UpdatedAt: "2026-09-08T12Z", Archived: true},
	}
	for _, document := range documents {
		writeFixture(t, start.DataRoot, documentFilesPath+"/"+document.ID+".chdoc", "Intentionally not a blob envelope; metadata never reads it.")
	}
	if mode == "orphan_without_state" || mode == "orphan_with_state" {
		return request
	}
	raw, e := json.Marshal(map[string]any{"version": 1, "documents": documents})
	if e != nil {
		t.Fatal(e)
	}
	if mode == "invalid_schema" {
		raw = []byte(`{"version":1,"documents":[{}]}`)
	}
	if mode == "large" {
		raw = append(raw, bytes.Repeat([]byte(" "), envelope.MaxObjectBytes-len(raw))...)
	}
	context := envelope.Context{DataHomeID: fixtureID, KeyID: activeDocumentKey, Purpose: "ledger", ObjectID: "ledger"}
	if mode == "rotated" || mode == "retained_missing" || mode == "rotated_active_missing" {
		context.KeyID = retainedDocumentKey
		request.Keys[documentTarget(retainedDocumentKey)] = bytes.Repeat([]byte{2}, 32)
	}
	if mode == "other_home_header" {
		context.DataHomeID = "dh_99999999999999999999999999999999"
	}
	if mode == "blob_purpose" {
		context.Purpose = "blob"
		context.ObjectID = documents[0].ID
	}
	encrypted, e := envelope.Encrypt(raw, request.Keys[documentTarget(context.KeyID)], context)
	if e != nil {
		t.Fatal(e)
	}
	if mode == "tampered" {
		encrypted[len(encrypted)-1] ^= 1
	}
	writeFixture(t, start.DataRoot, documentLedgerPath, string(encrypted))
	switch mode {
	case "missing_key", "rotated_active_missing":
		delete(request.Keys, documentTarget(activeDocumentKey))
	case "invalid_key":
		request.Keys[documentTarget(activeDocumentKey)] = []byte{}
	case "retained_missing":
		delete(request.Keys, documentTarget(retainedDocumentKey))
	case "backend_failure":
		request.BackendFailure = true
	case "legacy_ledger":
		writeFixture(t, start.DataRoot, documentLegacyPath, "synthetic legacy")
	case "legacy_blob":
		writeFixture(t, start.DataRoot, documentFilesPath+"/old.dpapi", "synthetic legacy")
	case "uppercase_legacy":
		writeFixture(t, start.DataRoot, documentFilesPath+"/old.DPAPI", "not a legacy suffix")
	case "extra_blob":
		writeFixture(t, start.DataRoot, documentFilesPath+"/extra.chdoc", "extra")
	case "missing_blob", "missing_archived_blob":
		id := documents[0].ID
		if mode == "missing_archived_blob" {
			id = documents[1].ID
		}
		if e := os.Remove(filepath.Join(start.DataRoot, "documents", "files", id+".chdoc")); e != nil {
			t.Fatal(e)
		}
	case "blob_directory":
		if e := os.Mkdir(filepath.Join(start.DataRoot, "documents", "files", "directory"), 0700); e != nil {
			t.Fatal(e)
		}
	case "state_invalid":
		writeFixture(t, start.DataRoot, documentStatePath, "{}")
	case "state_directory", "ledger_directory":
		path := documentStatePath
		if mode == "ledger_directory" {
			path = documentLedgerPath
		}
		absolute := filepath.Join(start.DataRoot, filepath.FromSlash(path))
		if e := os.Remove(absolute); e != nil {
			t.Fatal(e)
		}
		if e := os.Mkdir(absolute, 0700); e != nil {
			t.Fatal(e)
		}
	case "unowned":
		request.Unowned = true
	}
	return request
}

type documentBoundedOutput struct {
	data  bytes.Buffer
	limit int
}

func (b *documentBoundedOutput) Write(raw []byte) (int, error) {
	if len(raw) > b.limit-b.data.Len() {
		return 0, fmt.Errorf("synthetic document output bound")
	}
	return b.data.Write(raw)
}

func runDocumentFixture(t *testing.T, request documentFixture) documentOutcome {
	t.Helper()
	raw, e := json.Marshal(request)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDocumentReadProcessHelper$")
	cmd.Env = append(os.Environ(), "CAREERHUB_DOCUMENT_READ_HELPER=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(raw)
	out, diagnostics := &documentBoundedOutput{limit: 1 << 20}, &documentBoundedOutput{limit: 4096}
	cmd.Stdout, cmd.Stderr = out, diagnostics
	if cmd.Run() != nil || ctx.Err() != nil || diagnostics.data.Len() != 0 || cmd.ProcessState == nil || !cmd.ProcessState.Success() {
		stage := "unclassified helper failure"
		for _, label := range []string{"fixture did not actually deny content reads", "synthetic read-denial handle", "synthetic blob handle close", "synthetic blob directory", "synthetic blob path", "document read retained an operation handle", "fixture path restore failed", "fixture path unavailable", "synthetic workspace admission failed"} {
			if strings.Contains(out.data.String(), label) {
				stage = label
				break
			}
		}
		t.Fatalf("native document fixture %s failed: %s (raw output suppressed)", request.Mode, stage)
	}
	var result documentOutcome
	if json.Unmarshal(out.data.Bytes(), &result) != nil {
		t.Fatal("invalid document fixture response")
	}
	return result
}

func TestNativeDocumentReadsAndSourceParity(t *testing.T) {
	cases := []struct {
		mode, code     string
		records, reads int
	}{
		{"empty", "", 0, 0}, {"empty_directories", "", 0, 0}, {"active_without_ledger", "", 0, 1},
		{"complete", "", 2, 2}, {"rotated", "", 2, 2}, {"large", "", 2, 2}, {"content_locked", "", 2, 2},
		{"missing_key", "DOCUMENT_KEY_UNAVAILABLE", 0, 1}, {"invalid_key", "DOCUMENT_ENCRYPTION_INVALID", 0, 1},
		{"retained_missing", "DOCUMENT_KEY_UNAVAILABLE", 0, 2}, {"rotated_active_missing", "DOCUMENT_KEY_UNAVAILABLE", 0, 1},
		{"backend_failure", "DOCUMENT_ENCRYPTION_UNAVAILABLE", 0, 1}, {"state_missing", "DOCUMENT_KEY_STATE_MISSING", 0, 0},
		{"orphan_without_state", "DOCUMENT_KEY_STATE_MISSING", 0, 0}, {"orphan_with_state", "DOCUMENT_LEDGER_INVALID", 0, 1},
		{"missing_blob", "DOCUMENT_LEDGER_INVALID", 0, 2}, {"missing_archived_blob", "DOCUMENT_LEDGER_INVALID", 0, 2},
		{"extra_blob", "DOCUMENT_LEDGER_INVALID", 0, 2}, {"legacy_ledger", "DOCUMENT_MIGRATION_REQUIRED", 0, 0},
		{"legacy_blob", "DOCUMENT_MIGRATION_REQUIRED", 0, 0}, {"uppercase_legacy", "DOCUMENT_LEDGER_INVALID", 0, 2},
		{"root_file", "DOCUMENT_STORAGE_INVALID", 0, 0}, {"blob_directory", "DOCUMENT_FILE_INVALID", 0, 0},
		{"state_invalid", "DOCUMENT_ENCRYPTION_INVALID", 0, 0}, {"state_other_home", "DOCUMENT_ENCRYPTION_INVALID", 0, 0},
		{"state_directory", "DOCUMENT_ENCRYPTION_FAILED", 0, 0}, {"ledger_directory", "DOCUMENT_LEDGER_INVALID", 0, 1},
		{"tampered", "DOCUMENT_ENCRYPTION_INVALID", 0, 2}, {"other_home_header", "DOCUMENT_ENCRYPTION_INVALID", 0, 2},
		{"blob_purpose", "DOCUMENT_ENCRYPTION_INVALID", 0, 2}, {"invalid_schema", "DOCUMENT_LEDGER_INVALID", 0, 2},
		{"unowned", "DATA_HOME_OWNERSHIP_REQUIRED", 0, 0}, {"unowned_empty", "DATA_HOME_OWNERSHIP_REQUIRED", 0, 0},
	}
	requests := []documentFixture{}
	outcomes := []documentOutcome{}
	for _, tc := range cases {
		request := preparedDocumentFixture(t, tc.mode)
		base := filepath.Dir(request.Start.DataRoot)
		before := tree(t, base)
		result := runDocumentFixture(t, request)
		if result.Code != tc.code || len(result.Documents) != tc.records || len(result.KeyReads) != tc.reads {
			t.Fatalf("case %s: code=%s records=%d keyReads=%d", tc.mode, result.Code, len(result.Documents), len(result.KeyReads))
		}
		if result.Code != "" && (result.Documents != nil || result.Revision != "") {
			t.Fatal("failed native read leaked partial metadata")
		}
		if !reflect.DeepEqual(before, tree(t, base)) {
			t.Fatalf("case %s changed file bytes/mtime/mode/membership", tc.mode)
		}
		if !request.Unowned {
			requests = append(requests, request)
			outcomes = append(outcomes, result)
		}
	}
	t.Logf("native Windows document read cases: %d (two explicit process-ownership policy refusals)", len(cases))
	beforeSource := make([]map[string]string, len(requests))
	for i, request := range requests {
		beforeSource[i] = tree(t, filepath.Dir(request.Start.DataRoot))
	}
	compareDocumentSource(t, requests, outcomes)
	for i, request := range requests {
		if !reflect.DeepEqual(beforeSource[i], tree(t, filepath.Dir(request.Start.DataRoot))) {
			t.Fatalf("source read changed fixture %s", request.Mode)
		}
	}
}

func TestDocumentReadProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_DOCUMENT_READ_HELPER") != "1" {
		return
	}
	var request documentFixture
	if json.NewDecoder(io.LimitReader(os.Stdin, 64<<10)).Decode(&request) != nil {
		t.Fatal("invalid synthetic document request")
	}
	var workspace *Workspace
	var e error
	if request.Unowned {
		workspace, e = Open(request.Start)
	} else {
		workspace, e = OpenForProcess(request.Start)
	}
	if e != nil {
		t.Fatal("synthetic workspace admission failed")
	}
	backend := &documentFixtureBackend{t: t, channel: request.Start.Channel, values: request.Keys, reads: []string{}, failure: request.BackendFailure}
	var blobHandles []windows.Handle
	if request.Mode == "content_locked" {
		entries, e := os.ReadDir(filepath.Join(request.Start.DataRoot, "documents", "files"))
		if e != nil {
			t.Fatal("synthetic blob directory")
		}
		for _, entry := range entries {
			path := filepath.Join(request.Start.DataRoot, "documents", "files", entry.Name())
			wide, e := windows.UTF16PtrFromString(path)
			if e != nil {
				t.Fatal("synthetic blob path")
			}
			handle, e := windows.CreateFile(wide, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
			if e != nil {
				t.Fatal("synthetic read-denial handle")
			}
			blobHandles = append(blobHandles, handle)
			// A sharing-mode denial also blocks Go's native metadata opens. A
			// byte-range lock isolates ordinary byte reads without weakening
			// metadata/identity checks. This does not constrain memory mappings.
			var overlap windows.Overlapped
			if e := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, ^uint32(0), 0, &overlap); e != nil {
				t.Fatal("synthetic read-denial handle")
			}
			if _, e := os.ReadFile(path); e == nil {
				t.Fatal("fixture did not actually deny content reads")
			}
		}
	}
	result, e := workspace.readDocuments(context.Background(), func() (keys.Backend, error) { return backend, nil })
	for _, handle := range blobHandles {
		var overlap windows.Overlapped
		unlockError := windows.UnlockFileEx(handle, 0, ^uint32(0), 0, &overlap)
		if e := windows.CloseHandle(handle); e != nil || unlockError != nil {
			t.Fatal("synthetic blob handle close")
		}
	}
	out := documentOutcome{KeyReads: backend.reads}
	if e != nil {
		var ok bool
		typed, ok := e.(*Error)
		if !ok {
			t.Fatal("unsafe document service error")
		}
		out.Code = typed.Code
		if result != nil {
			t.Fatal("partial document result")
		}
	} else {
		listing := ledger.Metadata(result.records, result.encrypted, true)
		out.Documents, out.Revision = listing.Documents, listing.Revision
		if request.Mode == "empty" {
			// Exercise public entry points without creating or reading OS keys.
			metadata, e := workspace.Documents(context.Background(), true)
			if e != nil || metadata == nil || len(metadata.Documents) != 0 {
				t.Fatal("public empty metadata")
			}
			titles, e := workspace.DocumentTitles(context.Background())
			if e != nil || titles == nil || len(titles) != 0 {
				t.Fatal("public empty title view")
			}
		}
	}
	// A successful or failed read must release its per-read file/directory
	// handles before process exit, not rely on process death for cleanup.
	for _, relative := range []string{documentStatePath, documentLedgerPath, documentFilesPath} {
		path := filepath.Join(request.Start.DataRoot, filepath.FromSlash(relative))
		if _, e := os.Stat(path); os.IsNotExist(e) {
			continue
		} else if e != nil {
			t.Fatal("fixture path unavailable")
		}
		alias := path + "-cleanup-check"
		if e := os.Rename(path, alias); e != nil {
			t.Fatal("document read retained an operation handle")
		}
		if e := os.Rename(alias, path); e != nil {
			t.Fatal("fixture path restore failed")
		}
	}
	if json.NewEncoder(os.Stdout).Encode(out) != nil {
		t.Fatal("synthetic result write failed")
	}
	os.Exit(0)
}

func TestNativeDocumentCredentialChannelIsolation(t *testing.T) {
	requests := []documentFixture{}
	outcomes := []documentOutcome{}
	beforeSource := []map[string]string{}
	for _, scenario := range []struct{ channel, storedChannel, code string }{
		{"stable", "stable", ""}, {"dev", "stable", "DOCUMENT_KEY_UNAVAILABLE"},
	} {
		request := preparedDocumentFixture(t, "complete")
		request.Start.Channel = scenario.channel
		writeFixture(t, request.Start.DataRoot, datahome.MarkerPath, marker(scenario.channel, fixtureID))
		request.Keys = map[string][]byte{"CareerHub/" + scenario.storedChannel + "/document-key/" + fixtureID + "/" + activeDocumentKey: bytes.Repeat([]byte{1}, 32)}
		base := filepath.Dir(request.Start.DataRoot)
		before := tree(t, base)
		result := runDocumentFixture(t, request)
		if result.Code != scenario.code {
			t.Fatalf("channel isolation %s/%s: %s", scenario.channel, scenario.storedChannel, result.Code)
		}
		if !reflect.DeepEqual(before, tree(t, base)) {
			t.Fatal("channel read changed fixture")
		}
		requests = append(requests, request)
		outcomes = append(outcomes, result)
		beforeSource = append(beforeSource, before)
	}
	compareDocumentSource(t, requests, outcomes)
	for i, request := range requests {
		if !reflect.DeepEqual(beforeSource[i], tree(t, filepath.Dir(request.Start.DataRoot))) {
			t.Fatal("source channel read changed fixture")
		}
	}
}
