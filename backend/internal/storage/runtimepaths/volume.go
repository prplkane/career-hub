package runtimepaths

import "os"

// CheckLocalVolume observes whether this root is on the initially supported
// Windows database volume: fixed NTFS, persistent ACL support, not read-only.
// It neither writes nor checks effective ACLs, free space, cloud-sync software,
// writer ownership, or the volume of a later descendant opened by SQLite.
// Non-Windows qualification remains unsupported, not implicitly successful.
func (r *Root) CheckLocalVolume() *Error {
	return r.checkLocalVolumeWith(checkLocalVolume)
}

// The per-call seam tests lifecycle/error handling without mutable global hooks.
func (r *Root) checkLocalVolumeWith(inspect func(*os.File) *Error) (problem *Error) {
	if !volumeCheckSupported {
		return failure("VOLUME_PLATFORM_UNSUPPORTED")
	}
	if r == nil {
		return failure("ROOT_CLOSED")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed || r.base == nil || len(r.bindings) == 0 {
		return failure("ROOT_CLOSED")
	}
	if e := checkBindings(r.bindings); e != nil {
		return e
	}
	directory, err := r.base.Open(".")
	if err != nil {
		return failure("PATH_DIRECTORY_UNAVAILABLE")
	}
	defer func() {
		if err := directory.Close(); err != nil && problem == nil {
			problem = failure("FILE_CLOSE")
		}
	}()
	before, err := directory.Stat()
	if err != nil || isLinkLike(before) || !before.IsDir() ||
		!os.SameFile(r.bindings[len(r.bindings)-1].identity, before) {
		return failure("PATH_CHANGED")
	}
	problem = inspect(directory)
	// Recheck even after a failed native query; a stale namespace takes priority.
	after, err := directory.Stat()
	if err != nil || isLinkLike(after) || !after.IsDir() || !os.SameFile(before, after) {
		return failure("PATH_CHANGED")
	}
	if e := checkBindings(r.bindings); e != nil {
		return e
	}
	return problem
}
