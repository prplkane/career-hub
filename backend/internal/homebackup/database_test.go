package homebackup

import (
	"context"
	"testing"

	"careerhub.local/backend/internal/runtimepaths"
)

func TestDatabaseSnapshotRejectsInvalidPreconditionsBeforeOpen(t *testing.T) {
	consume := func(*runtimepaths.Root, *databaseSnapshot) *Error {
		t.Fatal("invalid call reached consumer")
		return nil
	}
	for _, path := range []string{"", "jobs/snapshot", snapshotPrefix + "../escape", snapshotPrefix + "ABCDEF0123456789abcdef0123456789ab"} {
		if p := withDatabaseSnapshot(context.Background(), "not an absolute path", path, consume); p == nil || p.Code != "BACKUP_SNAPSHOT_INVALID" {
			t.Fatal(path, p)
		}
	}
	if p := withDatabaseSnapshot(context.Background(), "not an absolute path", snapshotPrefix+"0123456789abcdef0123456789abcdef", nil); p == nil || p.Code != "BACKUP_SNAPSHOT_INVALID" {
		t.Fatal(p)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		if p := withDatabaseSnapshot(ctx, "not an absolute path", "", consume); p == nil || p.Code != "BACKUP_CANCELED" {
			t.Fatal(p)
		}
	}
}
