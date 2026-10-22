//go:build !windows

package runtimepaths

import "testing"

func TestOwnershipUnsupportedBeforeFilesystem(t *testing.T) {
	var root *Root
	code(t, root.ClaimForProcess(), "OWNERSHIP_UNSUPPORTED")
	code(t, root.RequireProcessOwnership(), "OWNERSHIP_UNSUPPORTED")
	_, e := acquireProcessClaim(nil)
	code(t, e, "OWNERSHIP_UNSUPPORTED")
}
