package homebackup

import (
	"context"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestCreateRequestRefusesBeforeFilesystem(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		request CreateRequest
		code    string
	}{
		{"nil-context", nil, CreateRequest{}, "BACKUP_CANCELED"},
		{"confirmation", context.Background(), CreateRequest{}, "BACKUP_WRITER_CONFIRMATION_REQUIRED"},
		{"channel", context.Background(), CreateRequest{ConfirmWritersStopped: true}, "BACKUP_CHANNEL_INVALID"},
		{"selection", context.Background(), CreateRequest{ConfirmWritersStopped: true, Channel: "stable", IncludeRoots: []string{}}, "BACKUP_SELECTION_INVALID"},
		{"destination", context.Background(), CreateRequest{ConfirmWritersStopped: true, Channel: "stable", ArchiveRelativePath: "backups/../outside.zip"}, "BACKUP_DESTINATION_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, p := CreateOwned(tc.ctx, tc.request, nil)
			if p == nil || p.Code != tc.code || got.SnapshotNamespace != runtimepaths.DirectoryNotCreated || got.ArchivePublication.Outcome != runtimepaths.FileNotPublished || got.FullSourceValidation != nil {
				t.Fatal(got, p)
			}
		})
	}
}

func TestSelectExportOrderAndIndependentMetadata(t *testing.T) {
	full := testManifest(testFile("applications/a", "a"), testFile("jobs/z", "z"), testFile("jobs/a", "a"), testFile("logs/a", "a"))
	schema := int64(1)
	full.SQLiteSchemaVersion = &schema
	got := selectExport(full, []string{"logs", "jobs"})
	if !reflect.DeepEqual(got.Files, []File{full.Files[3], full.Files[1], full.Files[2]}) || got.SQLiteSchemaVersion != nil {
		t.Fatal(got)
	}
	got.Files[0].RelativePath = "edited"
	got = selectExport(full, []string{"applications"})
	*got.SQLiteSchemaVersion = 99
	if schema != 1 || full.Files[3].RelativePath != "logs/a" {
		t.Fatal("selected export aliases full source metadata")
	}
}
