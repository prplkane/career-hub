package applicationwrite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIntakeReviewBuildRejectsInputBeforeHomeAccess(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	owner := New(missing)
	for _, sourceID := range []string{"", "../escape", "src_UPPER", "src_has-hyphen"} {
		out, problem := owner.BuildIntakeReview(context.Background(), sourceID, nil)
		if problem == nil || problem.Code != "REVIEW_BUILD_REQUEST_INVALID" || out.CommitAttempted || out.Committed {
			t.Fatal("invalid source reached Home", sourceID, out, problem)
		}
	}
	badRevision := "not-a-revision"
	if out, problem := owner.BuildIntakeReview(context.Background(), "src_fictional_1234567890", &badRevision); problem == nil || problem.Code != "REVIEW_BUILD_REQUEST_INVALID" || out.CommitAttempted {
		t.Fatal("invalid revision reached Home", out, problem)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("invalid request created a Data Home")
	}
}
