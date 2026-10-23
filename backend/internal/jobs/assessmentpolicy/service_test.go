package assessmentpolicy

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestPolicyRejectsMalformedWithoutDefaulting(t *testing.T) {
	valid := string(encode(map[string]bool{classes[0]: true, classes[1]: true, classes[2]: false}))
	for _, raw := range []string{"", "[]", "[", "null", valid + "extra: true\n", strings.Replace(valid, "active_clearance: true", "active_clearance: 1", 1), strings.Replace(valid, "schema_version: 1", "schema_version: true", 1), strings.Replace(valid, "schema_version: 1", "schema_version: 1.0", 1), strings.Repeat("#", MaxBytes+1)} {
		if _, _, e := decode(context.Background(), []byte(raw), true); e == nil || e.Code != "HARD_BLOCKER_POLICY_INVALID" {
			t.Fatal("invalid persisted policy accepted")
		}
	}
	for _, value := range []any{nil, []any{}, map[string]any{}, map[string]any{classes[0]: true, classes[1]: false, classes[2]: 1}} {
		if _, e := normalized(value); e == nil {
			t.Fatal("invalid flags accepted")
		}
	}
	// Raw CRLF/comments are part of the editor revision, not normalized on read.
	raw := []byte("# retained\r\n" + strings.ReplaceAll(valid, "\n", "\r\n"))
	_, retained, e := decode(context.Background(), raw, true)
	if e != nil || !bytes.Equal(raw, retained) {
		t.Fatal("read rewrote source bytes")
	}
	var service *Service
	if out, e := service.Run(context.Background(), false, nil, ""); e == nil || out.Committed {
		t.Fatal("unconfigured service")
	}
}
