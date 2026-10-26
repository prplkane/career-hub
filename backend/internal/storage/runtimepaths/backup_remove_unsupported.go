//go:build !windows

package runtimepaths

import "context"

const backupRemovalSupported = false

func (*Root) removeBackupEntry(context.Context, string, *backupRemovalFile, backupRemovalOperations) (BackupRemovalOutcome, *Error) {
	return BackupNotRemoved, failure("BACKUP_REMOVE_UNSUPPORTED")
}
