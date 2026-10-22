package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrepareCLIRejectsMixedIntent(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid preparation contacted service") }))
	defer s.Close()
	for _, flags := range [][]string{{}, {"--job-id="}, {"--stdin=false"}, {"--stdin", "--source-file=missing"}, {"--job-id=synthetic", "--stdin=false"}, {"--job-id=synthetic", "--source-name="}, {"--job-id=synthetic", "--posting-id="}, {"--stdin", "--purpose="}, {"--stdin", "--approve=false"}, {"--stdin", "--kind=resume"}, {"--stdin", "--registry-mode=auto"}, {"--stdin", "--task-request-json=null"}, {"--stdin", "--source-name=../PRIVATE"}, {"--stdin", "--task-request-json={}", "--task-request-file="}, {"--source-file="}, {"--stdin", "--purpose=application_text", "--task-request-json={\"purpose\":\"resume_delta\"}"}} {
		var out, diagnostic bytes.Buffer
		args := append([]string{"prepare", "--server", s.URL}, flags...)
		if run(args, strings.NewReader("PRIVATE"), &out, &diagnostic) != 1 || strings.Contains(out.String()+diagnostic.String(), "PRIVATE") {
			t.Fatal("invalid flags accepted", flags)
		}
	}
	for _, command := range []string{"analyze", "context", "record-submission", "material-context", "registry-add-exact"} {
		for _, flag := range []string{"--source-name=", "--posting-id="} {
			var out, diagnostic bytes.Buffer
			if run([]string{command, "--server", s.URL, "--job-id=synthetic", flag}, strings.NewReader(""), &out, &diagnostic) != 1 {
				t.Fatal("foreign source metadata accepted")
			}
		}
	}
}
