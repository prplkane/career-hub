// Package emailgate serializes complete native email operations. It is
// separate from the per-transaction application gate because a worker calls
// that gate repeatedly while its outer email run remains active.
package emailgate

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrContext = errors.New("email operation context is invalid")
	ErrBusy    = errors.New("email operation already running")
	held       = make(chan struct{}, 1)
)

// Acquire waits for the one email operation slot, respecting cancellation.
// The caller must hold the returned release until all synchronous cleanup is
// finished. No timeout or finalizer can release an unfinished native call.
func Acquire(ctx context.Context) (func(), error) {
	if ctx == nil {
		return nil, ErrContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case held <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return ownedRelease(ctx)
}

// TryAcquire refuses an overlapping sync immediately; a save or recovery may
// instead choose Acquire and wait. Both use the same process-wide slot.
func TryAcquire(ctx context.Context) (func(), error) {
	if ctx == nil {
		return nil, ErrContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case held <- struct{}{}:
		return ownedRelease(ctx)
	default:
		return nil, ErrBusy
	}
}

func ownedRelease(ctx context.Context) (func(), error) {
	var once sync.Once
	release := func() { once.Do(func() { <-held }) }
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}
