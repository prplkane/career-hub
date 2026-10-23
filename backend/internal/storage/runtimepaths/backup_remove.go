package runtimepaths

import (
	"context"
	"encoding/hex"
	"os"
)

type BackupRemovalOutcome string

const (
	BackupNotRemoved     BackupRemovalOutcome = "not_removed"
	BackupRemoved        BackupRemovalOutcome = "removed"
	BackupRemovalUnknown BackupRemovalOutcome = "unknown"
)

type backupRemovalFile struct {
	size   int64
	digest string
}

type backupRemovalOperations struct {
	beforeDelete func() *Error
	remove       func(*os.File) error
	close        func(*os.File) error
}

// RemoveBackupFile removes exactly one owned regular single-link file after a
// bounded streaming size/SHA-256 comparison. It is not a retention policy: the
// maintenance caller must authorize this exact path and stop independent writers.
// Unknown or removed-with-error outcomes must never trigger an automatic retry.
func (r *Root) RemoveBackupFile(ctx context.Context, relative string, size int64, sha256 string) (BackupRemovalOutcome, *Error) {
	if !backupRemovalSupported {
		return BackupNotRemoved, failure("BACKUP_REMOVE_UNSUPPORTED")
	}
	digest, err := hex.DecodeString(sha256)
	if size < 0 || size > MaxBackupReadBytes || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != sha256 {
		return BackupNotRemoved, failure("BACKUP_REMOVE_EXPECTATION_INVALID")
	}
	return r.removeBackupEntry(ctx, relative, &backupRemovalFile{size, sha256}, backupRemovalOperations{})
}

// RemoveEmptyBackupDirectory never traverses or deletes children. The native
// empty-directory requirement is checked again by the delete operation itself.
// This cannot remove the Root or any ancestor through an empty/dot path.
func (r *Root) RemoveEmptyBackupDirectory(ctx context.Context, relative string) (BackupRemovalOutcome, *Error) {
	return r.removeBackupEntry(ctx, relative, nil, backupRemovalOperations{})
}
