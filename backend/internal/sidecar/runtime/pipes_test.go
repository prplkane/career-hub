package runtime

import (
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

func TestPipeCloseSharesOneAttemptAndStickyTimeout(t *testing.T) {
	release, finished := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var calls atomic.Int32
	closer := &pipeCloser{budget: 20 * time.Millisecond, source: closerFunc(func() error {
		defer close(finished)
		calls.Add(1)
		<-release
		return nil
	})}
	var callers sync.WaitGroup
	t.Cleanup(func() {
		unblock()
		callers.Wait()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("synthetic close not reaped")
		}
	})
	results := make(chan error, 8)
	for i := 0; i < cap(results); i++ {
		callers.Add(1)
		go func() { defer callers.Done(); results <- closer.Close() }()
	}
	for i := 0; i < cap(results); i++ {
		select {
		case err := <-results:
			if !errors.Is(err, errPipeCloseTimeout) {
				t.Fatal("timeout result changed")
			}
		case <-time.After(time.Second):
			t.Fatal("concurrent closer exceeded bound")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("concurrent close attempted more than once")
	}
	// The already-spent attempt cannot restart its wait even while I/O remains.
	if !errors.Is(closer.Close(), errPipeCloseTimeout) {
		t.Fatal("timeout was not sticky")
	}
	unblock()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("late native close did not finish")
	}
	if !errors.Is(closer.Close(), errPipeCloseTimeout) {
		t.Fatal("late success changed the terminal timeout")
	}
}

func TestPipeClosePanicRemainsContentFree(t *testing.T) {
	closer := &pipeCloser{budget: time.Second, source: closerFunc(func() error { panic(privateCause) })}
	err := closer.Close()
	if err == nil || err.Error() != "sidecar pipe close failed" {
		t.Fatal("close panic leaked or escaped")
	}
}

func TestOwnedOutputPreservesOptionalFlush(t *testing.T) {
	plain := ownOutput(&memoryOutput{})
	if _, ok := plain.(pipeFlusher); ok {
		t.Fatal("wrapper invented a flush capability")
	}
	if err := plain.Close(); err != nil {
		t.Fatal(err)
	}
	buffered := &failingOutput{closed: make(chan struct{})}
	owned := ownOutput(buffered)
	flusher, ok := owned.(pipeFlusher)
	if !ok || flusher.Flush() == nil || buffered.flushes.Load() != 1 {
		t.Fatal("wrapper lost flush behavior")
	}
	if err := owned.Close(); err != nil {
		t.Fatal(err)
	}
}

// Models a native I/O cancellation that has not finished yet. Never leaks an
// actual blocked descriptor: test-owned release and completion channels reap it.
type stalledPipe struct {
	release   chan struct{}
	ioDone    chan struct{}
	closeDone chan struct{}
	closes    atomic.Int32
	closeOnce sync.Once
}

func (p *stalledPipe) Read([]byte) (int, error) {
	defer close(p.ioDone)
	<-p.release
	return 0, io.EOF
}
func (p *stalledPipe) Write([]byte) (int, error) {
	defer close(p.ioDone)
	<-p.release
	return 0, io.ErrClosedPipe
}
func (p *stalledPipe) Close() error {
	p.closes.Add(1)
	<-p.release
	p.closeOnce.Do(func() { close(p.closeDone) })
	return nil
}

func TestProcessBoundaryReturnsWhenNativePipeCloseStalls(t *testing.T) {
	for _, outputStalls := range []bool{false, true} {
		name := "silent_input_close"
		if outputStalls {
			name = "output_write_and_close"
		}
		t.Run(name, func(t *testing.T) {
			pipe := &stalledPipe{release: make(chan struct{}), ioDone: make(chan struct{}), closeDone: make(chan struct{})}
			var input io.ReadCloser = pipe
			var output io.WriteCloser = &memoryOutput{}
			if outputStalls {
				input, output = io.NopCloser(strings.NewReader("{}\n")), pipe
			}
			o := shortOptions()
			o.startup, o.output = 20*time.Millisecond, 20*time.Millisecond
			result := make(chan int, 1)
			runtimeDone := make(chan struct{})
			go func() { defer close(runtimeDone); result <- run(input, output, goodPrepare, o) }()
			t.Cleanup(func() {
				close(pipe.release)
				for _, done := range []<-chan struct{}{runtimeDone, pipe.ioDone, pipe.closeDone} {
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("synthetic pipe operation did not exit")
					}
				}
			})
			select {
			case status := <-result:
				if status != 3 {
					t.Fatalf("status = %d, want protocol failure", status)
				}
			case <-time.After(time.Second):
				t.Fatal("pipe Close prevented return to the dedicated process exit")
			}
			if pipe.closes.Load() != 1 {
				t.Fatal("pipe closure was not attempted exactly once")
			}
		})
	}
}
