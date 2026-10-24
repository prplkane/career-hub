package runtimepaths

import "sync"

// A dedicated sidecar owns at most one Data Home. Strong references intentionally
// survive Run returning: its timed-out native callbacks may still be running.
// Only OS process exit releases a published claim. There is no public unlock,
// finalizer, expiry, PID file or retry that can release another process's claim.
var processOwnership struct {
	sync.Mutex
	held *processClaim
}

// RequireProcessOwnership observes this process's existing claim for the exact
// opened directory. It never acquires/releases a claim or creates a lock file.
// Only cooperating backends are excluded; this is not a same-user sandbox.
func (r *Root) RequireProcessOwnership() *Error {
	if !ownershipSupported {
		return failure("OWNERSHIP_UNSUPPORTED")
	}
	if r == nil {
		return failure("ROOT_CLOSED")
	}
	processOwnership.Lock()
	claim := processOwnership.held
	processOwnership.Unlock()
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed || r.base == nil {
		return failure("ROOT_CLOSED")
	}
	if e := checkBindings(r.bindings); e != nil {
		return e
	}
	if !processRootOwned(claim, r) {
		return failure("OWNERSHIP_REQUIRED")
	}
	return nil
}

// ClaimForProcess excludes other cooperating Windows backends for this opened
// directory's native identity until this process exits. It retains independent
// root-chain handles, so closing the caller's Root does not release ownership.
// This is irreversible and intended only for a dedicated process, not embedding.
// It does not exclude unmodified Python tools or hostile same-user writers.
func (r *Root) ClaimForProcess() *Error {
	if !ownershipSupported {
		return failure("OWNERSHIP_UNSUPPORTED")
	}
	processOwnership.Lock()
	defer processOwnership.Unlock()
	if processOwnership.held != nil {
		return failure("OWNERSHIP_IN_USE")
	}
	if r == nil {
		return failure("ROOT_CLOSED")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed || r.base == nil || len(r.bindings) == 0 {
		return failure("ROOT_CLOSED")
	}
	claim, e := acquireProcessClaim(r)
	if e != nil {
		return e
	}
	processOwnership.held = claim
	return nil
}
