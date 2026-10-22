package main

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSaveDraftCLI(t *testing.T) {
	payload := `{"id":"answer","kind":"short_answer","source":"Employer form","exact_prompt":"Why?","response":"Supported answer."}`
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/session" {
			io.WriteString(w, `{"client":"browser","capabilities":{"application_texts":1}}`)
			return
		}
		calls++
		raw, _ := io.ReadAll(r.Body)
		if string(raw) != payload || r.Method != "POST" || r.Header.Get("X-Career-Hub-Token") != token {
			t.Error("incorrect owner request")
		}
		io.WriteString(w, `{"schema_version":1,"result":"created","job_id":"plan_job","item_id":"answer","kind":"short_answer","state":"draft","artifact_path":"outputs/application_texts/plan_job/application_texts.yaml"}`)
	}))
	defer s.Close()
	base := []string{"save-draft", "--connection", connectionFile(t, s.URL, token), "--job-id", "plan_job", "--stdin"}
	var out, diagnostics bytes.Buffer
	if run(base, strings.NewReader(payload), &out, &diagnostics) != 0 || calls != 1 || strings.Contains(out.String(), "contract_version") {
		t.Fatal("text command failed or receipt changed")
	}
	for _, flags := range [][]string{{"--approve"}, {"--approve=false"}, {"--kind", "cover_letter"}, {"--expected-revision", "0"}, {"--format", "txt"}, {"--payload-file", "private-marker"}} {
		out.Reset()
		if run(append(append([]string{}, base...), flags...), strings.NewReader(payload), &out, &diagnostics) != 1 || calls != 1 {
			t.Fatal("invalid mixed command reached owner")
		}
	}
}
