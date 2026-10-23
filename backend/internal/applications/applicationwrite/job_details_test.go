package applicationwrite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJobDetailsRejectsBadInputBeforeHomeAccess(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent-home")
	owner := New(missing)
	for _, tc := range []struct {
		id, revision string
		updates      map[string]any
	}{
		{"../escape", "sha256:" + strings.Repeat("0", 64), map[string]any{}},
		{"synthetic", "not-a-revision", map[string]any{}},
		{"synthetic", "sha256:" + strings.Repeat("0", 64), nil},
		{"synthetic", "sha256:" + strings.Repeat("0", 64), map[string]any{"title": strings.Repeat("x", 16385)}},
	} {
		out, err := owner.EditJobDetails(context.Background(), tc.id, tc.revision, tc.updates)
		if err == nil || err.Code != "JOB_DETAILS_REQUEST_INVALID" || out.CommitAttempted || out.Committed {
			t.Fatalf("invalid request reached Home: %#v %#v", out, err)
		}
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("invalid input created a Data Home")
	}
	if out, err := owner.EditJobDetails(context.Background(), "synthetic", "sha256:"+strings.Repeat("0", 64), map[string]any{}); err == nil || err.Code != "JOB_DETAILS_HOME_PIN_REQUIRED" || out.Committed {
		t.Fatalf("unpinned owner accepted: %#v %#v", out, err)
	}
}
