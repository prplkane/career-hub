package homebackup

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidationCleanupLimitsDoNotExpandArchive(t *testing.T) {
	snapshot := snapshotPrefix + strings.Repeat("a", 32)
	files := make([]File, MaxFiles+4)
	for i := range files {
		files[i] = File{RelativePath: fmt.Sprintf("jobs/file_%05d.txt", i), SHA256: strings.Repeat("0", 64)}
	}
	if _, p := cleanupFiles(snapshot, files, validationCleanupLimits); p != nil {
		t.Fatal("bounded physical-copy allowance rejected", p)
	}
	if _, p := cleanupFiles(snapshot, files, backupCleanupLimits); p == nil {
		t.Fatal("validation allowance expanded backup file limit")
	}
	if _, p := cleanupFiles(snapshot, append(files, File{RelativePath: "jobs/overflow.txt", SHA256: files[0].SHA256}), validationCleanupLimits); p == nil {
		t.Fatal("unbounded copy count accepted")
	}
	large := make([]File, MaxTotalBytes/MaxEntryBytes+1)
	for i := range large {
		large[i] = File{RelativePath: fmt.Sprintf("jobs/file_%02d.bin", i), SizeBytes: MaxEntryBytes, SHA256: files[0].SHA256}
	}
	if _, p := cleanupFiles(snapshot, large, validationCleanupLimits); p != nil {
		t.Fatal("normalized plus physical bytes refused", p)
	}
	if _, p := cleanupFiles(snapshot, large, backupCleanupLimits); p == nil {
		t.Fatal("validation allowance expanded backup byte limit")
	}
	large[0].SizeBytes++
	if _, p := cleanupFiles(snapshot, large, validationCleanupLimits); p == nil {
		t.Fatal("validation allowance expanded individual file limit")
	}
}
