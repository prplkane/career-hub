package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSubmissionCLIClosedFlags(t *testing.T) {
	base := []string{"record-submission", "--server", "http://127.0.0.1:1", "--job-id", "job-one", "--confirm-submitted"}
	for _, flag := range []string{"--kind=resume", "--name=skill", "--confirm-exact=false", "--stdin=false", "--payload-file=", "--expected-revision=0", "--approve=false", "--format=", "--confirm-submitted=false"} {
		var out, diagnostic bytes.Buffer
		if run(append(append([]string{}, base...), flag), strings.NewReader(""), &out, &diagnostic) != 1 || !strings.Contains(out.String(), "ARGUMENTS_INVALID") {
			t.Fatal("foreign flags accepted", flag)
		}
	}
	for _, command := range []string{"material-context", "save-draft", "save-material", "render-material", "registry-add-exact"} {
		for _, flag := range []string{"--confirm-submitted=false", "--note=", "--resume-id=", "--occurred-at="} {
			var out, diagnostic bytes.Buffer
			if run([]string{command, "--server", "http://127.0.0.1:1", "--job-id", "job-one", flag}, strings.NewReader(""), &out, &diagnostic) != 1 || !strings.Contains(out.String(), "ARGUMENTS_INVALID") {
				t.Fatal("submission flag ignored", command, flag)
			}
		}
	}
}
