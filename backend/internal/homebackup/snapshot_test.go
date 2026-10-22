package homebackup

import (
	"context"
	"testing"

	"careerhub.local/backend/internal/runtimepaths"
)

func TestPreparedSnapshotPreconditions(t *testing.T) {
	consume := func(*runtimepaths.Root, []File, *int64) *Error { t.Fatal("invalid request consumed"); return nil }
	valid := snapshotPrefix + "0123456789abcdef0123456789abcdef"
	for _, test := range []struct {
		snapshot  string
		selection []string
		consumer  func(*runtimepaths.Root, []File, *int64) *Error
		code      string
	}{
		{"", nil, consume, "BACKUP_SNAPSHOT_INVALID"},
		{valid, nil, nil, "BACKUP_SNAPSHOT_INVALID"},
		{valid, []string{}, consume, "BACKUP_SELECTION_INVALID"},
		{valid, []string{"jobs", "jobs"}, consume, "BACKUP_SELECTION_INVALID"},
	} {
		r, p := withPreparedSnapshot(context.Background(), "not an absolute root", test.snapshot, test.selection, test.consumer)
		if p == nil || p.Code != test.code || r.Namespace != runtimepaths.DirectoryNotCreated || r.CopiedFiles != 0 {
			t.Fatal(r, p)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		r, p := withPreparedSnapshot(ctx, "not an absolute root", valid, nil, consume)
		if p == nil || p.Code != "BACKUP_CANCELED" || r.Namespace != runtimepaths.DirectoryNotCreated {
			t.Fatal(r, p)
		}
	}
}
