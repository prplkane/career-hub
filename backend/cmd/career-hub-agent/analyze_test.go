package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnalyzeCLIRejectsMixedIntents(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid analysis contacted service") }))
	defer s.Close()
	for _, flags := range [][]string{{}, {"--job-id="}, {"--stdin=false"}, {"--stdin", "--source-file=missing"}, {"--job-id=synthetic", "--stdin=false"}, {"--job-id=synthetic", "--company="}, {"--stdin", "--purpose=assessment"}, {"--stdin", "--approve=false"}, {"--stdin", "--registry-mode=auto"}, {"--stdin", "--task-request-json={}"}, {"--source-file="}} {
		var out, diagnostic bytes.Buffer
		args := append([]string{"analyze", "--server", s.URL}, flags...)
		if run(args, strings.NewReader("PRIVATE"), &out, &diagnostic) != 1 || strings.Contains(out.String()+diagnostic.String(), "PRIVATE") {
			t.Fatal("invalid flags accepted", flags)
		}
	}
}
