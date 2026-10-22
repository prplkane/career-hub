package homebackup

import (
	"context"
	"strings"
	"testing"
)

func TestRestoreExtractionPreconditions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	consume := func(Manifest) *Error { t.Fatal("consumer reached without admission"); return nil }
	stage := restorePrefix + strings.Repeat("a", 32)
	for _, ctx := range []context.Context{nil, ctx} {
		result, p := withExtractedRestore(ctx, nil, "backups/a.zip", stage, consume)
		if p == nil || p.Code != "BACKUP_CANCELED" || result.CopiedFiles != 0 {
			t.Fatal(result, p)
		}
	}
	for _, source := range []string{"../outside.zip", "jobs/a.zip", "backups/a.zip:ads", "backups/../a.zip", "backups/a"} {
		if _, p := withExtractedRestore(context.Background(), nil, source, stage, consume); p == nil || p.Code != "BACKUP_SOURCE_UNAVAILABLE" {
			t.Fatal("source admission", source, p)
		}
	}
	if _, p := withExtractedRestore(context.Background(), nil, "backups/a.zip", stage, nil); p == nil || p.Code != "BACKUP_SNAPSHOT_INVALID" {
		t.Fatal("missing consumer", p)
	}
	if _, p := withExtractedRestore(context.Background(), nil, "backups/a.zip", stage, consume); p == nil || p.Code != "BACKUP_OWNERSHIP_REQUIRED" {
		t.Fatal("missing owner", p)
	}
}
