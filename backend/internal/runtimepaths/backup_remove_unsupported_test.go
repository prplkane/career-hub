//go:build !windows

package runtimepaths

import (
	"context"
	"testing"
)

func TestBackupRemovalUnsupported(t *testing.T) {
	var root *Root
	for _, directory := range []bool{false, true} {
		var out BackupRemovalOutcome
		var p *Error
		if directory {
			out, p = root.RemoveEmptyBackupDirectory(context.Background(), "no-filesystem-access")
		} else {
			out, p = root.RemoveBackupFile(context.Background(), "no-filesystem-access", 0, "")
		}
		if out != BackupNotRemoved || p == nil || p.Code != "BACKUP_REMOVE_UNSUPPORTED" {
			t.Fatal(out, p)
		}
	}
}
