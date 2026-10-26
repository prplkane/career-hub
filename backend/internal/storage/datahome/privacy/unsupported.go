//go:build !windows

package privacy

import "context"

// VerifyPrivateTree is Windows-only. POSIX mode/macOS extended ACL enforcement
// must be implemented and verified separately, never approximated with success.
func VerifyPrivateTree(context.Context, string) *Error {
	return fail("ACL_PLATFORM_UNSUPPORTED")
}

// VerifyPrivateRoot is Windows-only for the same reason as VerifyPrivateTree.
func VerifyPrivateRoot(context.Context, string) *Error {
	return fail("ACL_PLATFORM_UNSUPPORTED")
}
