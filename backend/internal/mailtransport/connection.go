package mailtransport

import (
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"
)

var errWireLimit = errors.New("mail transport wire limit exceeded")

// guardedConn constrains the library's own deadline updates and bounds plaintext
// ingress before the IMAP decoder. Limits count wire bytes, not total heap usage.
// It wraps the verified TLS connection, never the unencrypted socket.
type guardedConn struct {
	net.Conn
	mu               sync.Mutex
	deadline         time.Time
	remaining, total int64
	limited          bool
	invalidFrame     bool
	networkFailure   bool
	once             sync.Once
	frame            frameGuard // one library decoder reader owns framing state
}

func (c *guardedConn) budget(deadline time.Time, size int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline, c.remaining = deadline, size
	return c.Conn.SetDeadline(deadline)
}

func (c *guardedConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	allowed := min(c.remaining, c.total)
	if allowed <= 0 {
		c.limited = true
		c.mu.Unlock()
		_ = c.Close()
		return 0, errWireLimit
	}
	if int64(len(p)) > allowed {
		p = p[:allowed]
	}
	c.mu.Unlock()
	n, err := c.Conn.Read(p)
	if n > 0 && !c.frame.accept(p[:n]) {
		c.mu.Lock()
		c.limited, c.invalidFrame = c.frame.tooLarge, !c.frame.tooLarge
		c.mu.Unlock()
		_ = c.Close()
		return 0, errWireLimit
	}
	c.mu.Lock()
	if err != nil {
		c.networkFailure = true
	}
	c.remaining -= int64(n)
	c.total -= int64(n)
	if c.remaining < 0 || c.total < 0 {
		c.limited = true
		err, n = errWireLimit, 0
	}
	c.mu.Unlock()
	if errors.Is(err, errWireLimit) {
		_ = c.Close()
	}
	return n, err
}

func (c *guardedConn) hitLimit() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limited
}

func (c *guardedConn) badFrame() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.invalidFrame
}

func (c *guardedConn) networkFailed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.networkFailure
}

func (c *guardedConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.SetDeadline(earlier(t, c.deadline))
}
func (c *guardedConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.SetReadDeadline(earlier(t, c.deadline))
}
func (c *guardedConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.SetWriteDeadline(earlier(t, c.deadline))
}
func (c *guardedConn) Close() error {
	var err error
	c.once.Do(func() {
		// TLS.Close may send close_notify with its own five-second deadline.
		// Cancellation owns the socket, not a best-effort graceful TLS exchange.
		if secure, ok := c.Conn.(*tls.Conn); ok {
			err = secure.NetConn().Close()
		} else {
			err = c.Conn.Close()
		}
	})
	return err
}
func earlier(a, b time.Time) time.Time {
	if a.IsZero() || (!b.IsZero() && b.Before(a)) {
		return b
	}
	return a
}
