// Package operationgate serializes cooperating Data Home use cases in one
// sidecar process. Native ownership separately binds that process to one home.
// This is not an OS lock and does not exclude Python or external editors.
package operationgate

import (
	"context"
	"errors"
	"sync"
)

var shared = make(chan struct{}, 1)
var ErrContext = errors.New("workspace operation context is invalid")

// Acquire is cancellable while waiting and non-reentrant. Hold the lease from
// the first observation through native publication, rechecks and cleanup. A
// timeout must not release the lease while a native callback is still running.
func Acquire(ctx context.Context) (func(), error) {
	if ctx == nil {
		return nil, ErrContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case shared <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var once sync.Once
	release := func() { once.Do(func() { <-shared }) }
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}
