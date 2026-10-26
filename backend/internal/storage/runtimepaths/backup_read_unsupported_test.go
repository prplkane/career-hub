//go:build !windows

package runtimepaths

import (
	"context"
	"testing"
)

func TestBackupReadUnsupportedBeforeFilesystem(t *testing.T) {
	var root *Root
	lease, problem := root.OpenBackupRead(context.Background(), "../never-open", MaxBackupReadBytes)
	if lease != nil {
		t.Fatal("unsupported lease admitted")
	}
	code(t, problem, "BACKUP_READ_UNSUPPORTED")
}
