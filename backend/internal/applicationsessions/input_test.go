package applicationsessions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 8<<20 {
		return 0, os.ErrInvalid
	}
	return b.Buffer.Write(p)
}

func TestRequestAndSecretBoundaries(t *testing.T) {
	for _, r := range []Request{
		{Operation: "unknown"},
		{Operation: "reviews", Fields: map[string]any{"job_id": "injected"}},
		{Operation: "claim", Fields: map[string]any{"request_id": "x", "current_url": "x", "tab_id": 1, "job_id": "injected"}},
		{Operation: "create", Fields: map[string]any{"job_id": strings.Repeat("x", maxRequestBytes+1)}},
		{Operation: "create", Fields: map[string]any{"job_id": "\xff"}},
		{Operation: "create", Fields: map[string]any{"job_id": map[string]any{}}},
		{Operation: "cancel", Fields: map[string]any{"session_id": "application_session_" + strings.Repeat("a", 32), "expected_revision": json.Number(strings.Repeat("1", 257))}},
	} {
		if r.validate() == nil {
			t.Fatal("unbounded command accepted")
		}
	}
	p := &Error{Stage: "secret-stage", Code: "secret-code", SourceClass: "secret-class", SourceMessage: "secret-message"}
	for _, v := range []string{fmt.Sprint(p), fmt.Sprintf("%v", p), fmt.Sprintf("%+v", p), fmt.Sprintf("%#v", p)} {
		if strings.Contains(v, "secret") {
			t.Fatal("private metadata formatted")
		}
	}
	r := Request{Operation: "signal", Fields: map[string]any{"session_token": "private-token"}}
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		if strings.Contains(fmt.Sprintf(format, r), "private-token") {
			t.Fatal("request capability leaked")
		}
	}
	seen := map[string]bool{}
	for range 20 {
		id, e := (SecureEntropy{}).ID()
		if e != nil {
			t.Fatal(e)
		}
		if _, p := sessionID("application_session_" + id); p != nil {
			t.Fatal("invalid secure ID")
		}
		capability, e := (SecureEntropy{}).Token()
		if e != nil {
			t.Fatal(e)
		}
		if seen[id] || seen[capability] || len(capability) != 43 {
			t.Fatal("entropy output invalid or reused")
		}
		seen[id], seen[capability] = true, true
	}
}
