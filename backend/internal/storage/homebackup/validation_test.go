package homebackup

import (
	"context"
	"testing"
)

func TestSnapshotValidationPreconditionsAndOwnedManifest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		if summary, p := ValidateSnapshot(ctx, "not an absolute root", "", testManifest(), nil); summary != nil || p == nil || p.Code != "BACKUP_CANCELED" {
			t.Fatal(summary, p)
		}
	}
	if summary, p := ValidateSnapshot(context.Background(), "not an absolute root", "../outside", testManifest(), nil); summary != nil || p == nil || p.Code != "BACKUP_SNAPSHOT_INVALID" {
		t.Fatal(summary, p)
	}
	manifest := testManifest(testFile("jobs/a", "synthetic"))
	version := int64(1)
	manifest.SQLiteSchemaVersion = &version
	owned, p := validationManifest(manifest)
	if p != nil {
		t.Fatal(p)
	}
	manifest.Files[0].RelativePath = "changed"
	version = 2
	if owned.Files[0].RelativePath != "jobs/a" || *owned.SQLiteSchemaVersion != 1 {
		t.Fatal("aliased caller manifest")
	}
}

func TestSnapshotSchemaAbsenceAndOrphanRefusal(t *testing.T) {
	s := payloadSource{ctx: context.Background(), files: map[string]File{}}
	version := int64(1)
	if v, n, p := validatePayloadDatabase(s, "unused", &version); v != nil || n != 0 || p == nil || p.Code != "BACKUP_SCHEMA_UNSUPPORTED" {
		t.Fatal(v, n, p)
	}
	if v, n, p := validatePayloadDatabase(s, "unused", nil); v != nil || n != 0 || p != nil {
		t.Fatal(v, n, p)
	}
	s.files[databasePath+"-wal"] = testFile(databasePath+"-wal", "orphan")
	if v, n, p := validatePayloadDatabase(s, "unused", nil); v != nil || n != 0 || p == nil || p.Code != "BACKUP_DATABASE_NOT_STANDALONE" {
		t.Fatal(v, n, p)
	}
}
