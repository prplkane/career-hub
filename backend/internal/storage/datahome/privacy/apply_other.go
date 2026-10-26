//go:build !windows

package privacy

import "context"

// Native permission application must be implemented and verified per platform;
// an unsupported result never falls back to a permissive/no-op provider.
func ApplyPrivateTree(context.Context, string) (bool, *Error) {
	return false, fail("ACL_PLATFORM_UNSUPPORTED")
}
