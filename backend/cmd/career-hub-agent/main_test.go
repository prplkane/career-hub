package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHelpAndInvalidCLIArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"--help"}, {"material-context", "--help"}} {
		var out, err bytes.Buffer
		if run(args, strings.NewReader(""), &out, &err) != 0 || !strings.Contains(out.String(), "--server") || err.Len() != 0 {
			t.Fatal("help contract")
		}
	}
	base := []string{"--server", "http://127.0.0.1:1", "--job-id", "plan_job"}
	for _, args := range [][]string{
		{"material-context", "--private-secret"},
		{"material-context", "--server", "http://private-secret.test"},
		append(append([]string{"save-material"}, base...), "--stdin"),
		append(append([]string{"save-material"}, base...), "--expected-revision", "-1", "--stdin"),
		append(append([]string{"save-material"}, base...), "--expected-revision", "true", "--stdin"),
		append(append([]string{"save-material"}, base...), "--expected-revision", "0"),
		append(append([]string{"save-material"}, base...), "--expected-revision", "0", "--stdin", "--payload-file", "private-secret.json"),
		append(append([]string{"material-context"}, base...), "--approve"),
		append(append([]string{"material-context"}, base...), "--format", "pdf"),
		append(append([]string{"material-context"}, base...), "--stdin"),
		append(append([]string{"material-context"}, base...), "private-secret"),
		append([]string{"record-submission"}, base...),
	} {
		var out, err bytes.Buffer
		if run(args, strings.NewReader("private-secret"), &out, &err) != 1 {
			t.Fatal("invalid args accepted")
		}
		var result map[string]any
		if json.Unmarshal(out.Bytes(), &result) != nil || result["result"] != "failed" || result["retryable"] != false || strings.Contains(out.String()+err.String(), "private-secret") {
			t.Fatal("argument failure not structured/private")
		}
	}
}

func TestVocabularyFlagsArePurposeSpecific(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid flags reached network") }))
	defer s.Close()
	base := []string{"registry-add-exact", "--server", s.URL, "--job-id", "plan_job", "--name", "NebulaDB", "--primary-group-id", "databases_storage", "--confirm-exact"}
	for _, extra := range []string{"--kind=cover_letter", "--approve=false", "--stdin=false", "--format=", "--payload-file=", "--expected-revision=0", "--source-line=0", "--source-line=", "--source-line=3.0", "--source-line=200001", "--confirm-exact=false"} {
		args := append(append([]string{}, base...), extra)
		var out, diagnostic bytes.Buffer
		if run(args, strings.NewReader(""), &out, &diagnostic) != 1 || !strings.Contains(out.String(), "ARGUMENTS_INVALID") || diagnostic.Len() != 0 {
			t.Fatal("incompatible vocabulary flag accepted", extra)
		}
	}
	for _, command := range []string{"material-context", "save-draft", "save-material", "render-material"} {
		for _, flag := range []string{"--name=ignored", "--primary-group-id=ignored", "--source-line=1", "--confirm-exact=false"} {
			var out, diagnostic bytes.Buffer
			args := []string{command, "--server", s.URL, "--job-id", "plan_job", flag}
			if run(args, strings.NewReader(""), &out, &diagnostic) != 1 || !strings.Contains(out.String(), "ARGUMENTS_INVALID") {
				t.Fatal("foreign vocabulary flag ignored")
			}
		}
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("private-secret") }

func TestCLIInputBoundsAndPrivateFailures(t *testing.T) {
	base := []string{"save-material", "--server", "http://127.0.0.1:1", "--job-id", "plan_job", "--expected-revision", "0"}
	for _, reader := range []io.Reader{brokenReader{}, strings.NewReader(strings.Repeat("x", (1<<20)+1))} {
		var out, diagnostic bytes.Buffer
		if run(append(append([]string{}, base...), "--stdin"), reader, &out, &diagnostic) != 1 || strings.Contains(out.String(), "private-secret") {
			t.Fatal("bounded input failure")
		}
	}
	for _, path := range []string{filepath.Join(t.TempDir(), "private-secret-missing.json"), t.TempDir()} {
		var out, diagnostic bytes.Buffer
		if run(append(append([]string{}, base...), "--payload-file", path), strings.NewReader(""), &out, &diagnostic) != 1 || strings.Contains(out.String(), path) {
			t.Fatal("file failure disclosed path")
		}
	}
}

func TestCLIFileAndStdinHaveSameIntent(t *testing.T) {
	var calls atomic.Int32
	var received []byte
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/session" {
			_, _ = io.WriteString(w, `{"client":"browser","capabilities":{"material_integrity":1}}`)
			return
		}
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if received != nil && !bytes.Equal(received, body) {
			t.Error("stdin/file intent differs")
		}
		received = body
		w.WriteHeader(409)
		_, _ = io.WriteString(w, `{"error_code":"MATERIAL_REVISION_CONFLICT","details":"private-secret","operation_state":{"known":true,"committed":false,"partial":false,"cleanup_failed":false}}`)
	}))
	defer s.Close()
	path := filepath.Join(t.TempDir(), "draft.json")
	payload := `{"draft":{"job_id":"plan_job","private":"private-secret"}}`
	if err := os.WriteFile(path, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	for _, flags := range [][]string{{"--stdin"}, {"--payload-file", path}} {
		args := append([]string{"save-material", "--connection", connectionFile(t, s.URL, token), "--job-id", "plan_job", "--expected-revision", "7", "--timings"}, flags...)
		var out, diagnostic bytes.Buffer
		if run(args, strings.NewReader(payload), &out, &diagnostic) != 1 {
			t.Fatal("conflict accepted")
		}
		var receipt struct {
			Code  string `json:"error_code"`
			State struct {
				Known     bool
				Committed bool
			} `json:"operation_state"`
		}
		if json.Unmarshal(out.Bytes(), &receipt) != nil || receipt.Code != "MATERIAL_REVISION_CONFLICT" || !receipt.State.Known || receipt.State.Committed {
			t.Fatal("CLI lost owner failure state")
		}
		var timing map[string]any
		if json.Unmarshal(diagnostic.Bytes(), &timing) != nil || len(timing) != 1 || timing["elapsed_ms"] == nil || strings.Contains(out.String()+diagnostic.String(), "private-secret") {
			t.Fatal("timing/body privacy")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("CLI retried mutation")
	}
	var wire map[string]any
	if json.Unmarshal(received, &wire) != nil || wire["action"] != "save_draft" || wire["expected_revision"] != float64(7) || wire["draft"] == nil {
		t.Fatal("CLI changed intent")
	}
}
