package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContextCLIRejectsForeignFlagsBeforeNetwork(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid context contacted service") }))
	defer s.Close()
	base := []string{"context", "--server", s.URL, "--job-id", "synthetic", "--purpose", "assessment"}
	for _, extra := range [][]string{{"--kind=resume"}, {"--stdin=false"}, {"--approve=false"}, {"--format="}, {"--note="}, {"--resume-id=r1"}, {"--confirm-exact=false"}, {"--expected-revision=0"}, {"--purpose=unknown"}, {"--registry-mode=auto"}, {"--task-request-json="}, {"--task-request-json=null"}, {"--task-request-json={}", "--task-request-file="}} {
		var out, diagnostic bytes.Buffer
		if run(append(append([]string{}, base...), extra...), strings.NewReader("PRIVATE"), &out, &diagnostic) != 1 || strings.Contains(out.String()+diagnostic.String(), "PRIVATE") {
			t.Fatal("invalid flags accepted", extra)
		}
	}
	for _, command := range []string{"material-context", "save-draft", "save-material", "render-material", "registry-add-exact", "record-submission"} {
		var out, diagnostic bytes.Buffer
		if run([]string{command, "--server", s.URL, "--job-id", "synthetic", "--purpose=assessment"}, strings.NewReader(""), &out, &diagnostic) != 1 {
			t.Fatal("foreign context flag accepted")
		}
	}
}
