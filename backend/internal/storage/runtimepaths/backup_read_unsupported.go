//go:build !windows

package runtimepaths

import "context"

const backupReadSupported = false

func openBackupRead(*Root, context.Context, string, int64) (*BackupRead, *Error) {
	return nil, failure("BACKUP_READ_UNSUPPORTED")
}
