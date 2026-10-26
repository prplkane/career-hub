package homebackup

import (
	"context"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

// VerifyLocal verifies one archive inside a caller-admitted, process-owned
// managed Root. The native source lease stays pinned through ZIP inspection,
// hashing and final cleanup. No paths/handles escape and nothing is extracted.
// The maintenance caller must still admit the Home/channel/recovery state;
// ownership of a directory is not authorization to restore its contents.
func VerifyLocal(ctx context.Context, root *runtimepaths.Root, relative string) (manifest Manifest, problem *Error) {
	if ctx == nil || ctx.Err() != nil {
		return Manifest{}, failure("BACKUP_CANCELED")
	}
	lease, sourceProblem := root.OpenBackupRead(ctx, relative, MaxArchiveBytes)
	if sourceProblem != nil {
		if ctx.Err() != nil {
			return Manifest{}, failure("BACKUP_CANCELED")
		}
		return Manifest{}, failure("BACKUP_SOURCE_UNAVAILABLE")
	}
	defer func() {
		if lease.Close() != nil {
			manifest, problem = Manifest{}, failure("BACKUP_SOURCE_CLOSE_FAILED")
		} else if ctx.Err() != nil {
			manifest, problem = Manifest{}, failure("BACKUP_CANCELED")
		}
	}()
	archive, problem := Inspect(ctx, lease, lease.Size())
	if problem != nil {
		return Manifest{}, problem
	}
	if problem = archive.Verify(ctx); problem != nil {
		return Manifest{}, problem
	}
	return archive.Manifest(), nil
}
