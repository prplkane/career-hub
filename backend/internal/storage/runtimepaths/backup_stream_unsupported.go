//go:build !windows

package runtimepaths

import (
	"io/fs"
	"os"
)

func (*Root) newBackupStage(string) (*backupStage, bool, *Error) {
	return nil, false, failure("BACKUP_STREAM_UNSUPPORTED")
}
func openBackupPublication(*os.Root, string, fs.FileInfo) (*os.File, *Error) {
	return nil, failure("BACKUP_STREAM_UNSUPPORTED")
}
func renameBackupFile(*os.File, *os.Root, string) error { return failure("BACKUP_STREAM_UNSUPPORTED") }
