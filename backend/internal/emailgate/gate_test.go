package emailgate

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWholeOperationSerialization(t *testing.T) {
	release, err := TryAcquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if another, err := TryAcquire(context.Background()); another != nil || !errors.Is(err, ErrBusy) {
		t.Fatalf("overlapping operation admitted: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if another, err := Acquire(ctx); another != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting operation outlived cancellation: %v", err)
	}
	release()
	next, err := Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer next()
	release() // The earlier releaser cannot free a later operation's slot.
	if another, err := TryAcquire(context.Background()); another != nil || !errors.Is(err, ErrBusy) {
		t.Fatalf("stale release freed another operation: %v", err)
	}
	next()
	last, err := TryAcquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	last()
}

func TestInvalidAndCanceledContext(t *testing.T) {
	if release, err := TryAcquire(nil); release != nil || !errors.Is(err, ErrContext) {
		t.Fatalf("nil try context: %v", err)
	}
	if release, err := Acquire(nil); release != nil || !errors.Is(err, ErrContext) {
		t.Fatalf("nil wait context: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if release, err := TryAcquire(ctx); release != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled try context: %v", err)
	}
	if release, err := Acquire(ctx); release != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled wait context: %v", err)
	}
}
