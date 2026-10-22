package runtime

import (
	"errors"
	"io"
	"sync"
	"time"
)

const pipeCloseBudget = 250 * time.Millisecond

var errPipeCloseTimeout = errors.New("sidecar pipe close timed out")

// pipeCloser makes one bounded attempt, including when emit and deferred exit
// both close output. It does not cancel a native Close or promise quiescence:
// this runtime must return directly to process exit, which reaps pending I/O.
type pipeCloser struct {
	source io.Closer
	budget time.Duration
	once   sync.Once
	result error
}

func (c *pipeCloser) Close() error {
	c.once.Do(func() {
		closed := make(chan error, 1)
		go func() {
			outcome := errors.New("sidecar pipe close failed")
			defer func() {
				if recover() != nil {
					outcome = errors.New("sidecar pipe close failed")
				}
				closed <- outcome
			}()
			outcome = c.source.Close()
		}()
		timer := time.NewTimer(c.budget)
		defer timer.Stop()
		select {
		case c.result = <-closed:
		case <-timer.C:
			c.result = errPipeCloseTimeout
		}
	})
	return c.result
}

func ownInput(input io.ReadCloser) io.ReadCloser {
	return struct {
		io.Reader
		io.Closer
	}{input, &pipeCloser{source: input, budget: pipeCloseBudget}}
}

func ownOutput(output io.WriteCloser) io.WriteCloser {
	owned := struct {
		io.Writer
		io.Closer
	}{output, &pipeCloser{source: output, budget: pipeCloseBudget}}
	// Protocol.WriteRecord flushes only when the supplied writer supports it.
	// Preserve that optional capability rather than silently losing buffered I/O.
	if flusher, ok := output.(pipeFlusher); ok {
		return struct {
			io.WriteCloser
			pipeFlusher
		}{owned, flusher}
	}
	return owned
}

type pipeFlusher interface{ Flush() error }
