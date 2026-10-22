package mailtransport

import (
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestGuardedTLSCloseOwnsUnderlyingSocket(t *testing.T) {
	raw := &recordingConn{input: bytes.NewReader(nil)}
	secure := tls.Client(raw, &tls.Config{ServerName: "synthetic.test", MinVersion: tls.VersionTLS12})
	c := &guardedConn{Conn: secure}
	_ = c.Close()
	_ = c.Close()
	if raw.closes != 1 {
		t.Fatal("TLS socket not closed exactly once")
	}
}

type recordingConn struct {
	input                 *bytes.Reader
	deadline, read, write time.Time
	closes                int
	setError              error
}

func (c *recordingConn) Read(p []byte) (int, error)         { return c.input.Read(p) }
func (c *recordingConn) Write(p []byte) (int, error)        { return len(p), nil }
func (c *recordingConn) Close() error                       { c.closes++; return nil }
func (c *recordingConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (c *recordingConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (c *recordingConn) SetDeadline(v time.Time) error      { c.deadline = v; return c.setError }
func (c *recordingConn) SetReadDeadline(v time.Time) error  { c.read = v; return c.setError }
func (c *recordingConn) SetWriteDeadline(v time.Time) error { c.write = v; return c.setError }

func TestGuardedDeadlineClamping(t *testing.T) {
	raw := &recordingConn{input: bytes.NewReader(nil)}
	c := &guardedConn{Conn: raw, total: 1024}
	bound := time.Now().Add(time.Second)
	if err := c.budget(bound, 100); err != nil {
		t.Fatal(err)
	}
	for _, v := range []time.Time{{}, bound.Add(time.Hour), bound.Add(-time.Millisecond)} {
		want := earlier(v, bound)
		if err := c.SetDeadline(v); err != nil || raw.deadline != want {
			t.Fatal("SetDeadline escaped bound")
		}
		if err := c.SetReadDeadline(v); err != nil || raw.read != want {
			t.Fatal("SetReadDeadline escaped bound")
		}
		if err := c.SetWriteDeadline(v); err != nil || raw.write != want {
			t.Fatal("SetWriteDeadline escaped bound")
		}
	}
	raw.setError = errors.New("synthetic deadline refusal")
	if c.budget(bound, 20) == nil {
		t.Fatal("deadline failure hidden")
	}
	if earlier(bound, time.Time{}) != bound {
		t.Fatal("zero bound changed nonzero deadline")
	}
}

func TestGuardedWireBudgetsAndFraming(t *testing.T) {
	for _, tc := range []struct {
		name, wire     string
		per, total     int64
		limit, invalid bool
	}{
		{"wireLimit", "* OK data\r\n", 4, 100, true, false},
		{"totalLimit", "* OK data\r\n", 100, 4, true, false},
		{"framingRefusal", "* FETCH (BODYSTRUCTURE ((", 100, 100, false, true},
		{"normalEOF", "* OK data\r\n", 100, 100, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := &recordingConn{input: bytes.NewReader([]byte(tc.wire))}
			c := &guardedConn{Conn: raw, total: tc.total}
			if c.budget(time.Now().Add(time.Second), tc.per) != nil {
				t.Fatal("budget")
			}
			_, err := io.ReadAll(c)
			if tc.limit || tc.invalid {
				if !errors.Is(err, errWireLimit) || raw.closes != 1 {
					t.Fatal("bad input did not close socket")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if c.hitLimit() != tc.limit || c.badFrame() != tc.invalid {
				t.Fatal("error classification differs")
			}
			_ = c.Close()
			_ = c.Close()
			if raw.closes != 1 {
				t.Fatal("socket close not idempotent")
			}
		})
	}
}

func TestGuardedBudgetResetPreservesTotalAndFraming(t *testing.T) {
	raw := &recordingConn{input: bytes.NewReader([]byte("* OK one\r\n* OK two\r\n"))}
	c := &guardedConn{Conn: raw, total: 12}
	_ = c.budget(time.Now().Add(time.Second), 10)
	buf := make([]byte, 10)
	if n, err := c.Read(buf); n != 10 || err != nil {
		t.Fatal("first read")
	}
	_ = c.budget(time.Now().Add(time.Second), 100)
	if n, err := c.Read(buf); n != 2 || err != nil {
		t.Fatal("reset lost cumulative limit")
	}
	if _, err := c.Read(buf); !errors.Is(err, errWireLimit) {
		t.Fatal("total bound bypassed by reset")
	}
}
