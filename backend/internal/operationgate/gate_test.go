package operationgate

import (
	"context"
	"testing"
	"time"
)

func TestSharedLeaseAndCancellation(t *testing.T) {
	first, err := Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		release, err := Acquire(ctx)
		if release != nil {
			release()
		}
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("waiter bypassed lease")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation hung")
	}
	first()
	first()
	next, err := Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	next()
	if release, err := Acquire(nil); err == nil || release != nil {
		t.Fatal("nil context accepted")
	}
	if release, err := Acquire(ctx); err == nil || release != nil {
		t.Fatal("canceled context accepted")
	}
}
