package applicationwrite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIntakeReviewSuggestRejectsInputBeforeHomeAccess(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	owner := New(missing)
	validRevision := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, input := range [][2]string{{"", validRevision}, {"../escape", validRevision}, {"src_fictional_1234567890", "bad"}} {
		out, problem := owner.SuggestIntakeReview(context.Background(), input[0], input[1])
		if problem == nil || problem.Code != "REVIEW_SUGGEST_REQUEST_INVALID" || out.CommitAttempted || out.Committed {
			t.Fatal("invalid suggestion reached Home", input, out, problem)
		}
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("invalid suggestion created a Data Home")
	}
}
