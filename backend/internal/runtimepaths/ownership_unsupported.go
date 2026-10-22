//go:build !windows

package runtimepaths

const ownershipSupported = false

type processClaim struct{}

func processRootOwned(*processClaim, *Root) bool { return false }

func acquireProcessClaim(*Root) (*processClaim, *Error) {
	return nil, failure("OWNERSHIP_UNSUPPORTED")
}
