// Package mailtransport implements only the existing read-only Gmail transport.
// It has no credential store, persistence, MIME parser, or production activation.
package mailtransport

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

const (
	MaxMessageBytes = 10 * 1024 * 1024
	MaxHeaderBytes  = 256 * 1024
	MaxSearchUIDs   = 100_000
	maxControlBytes = 1024 * 1024
	maxSessionBytes = 256 * 1024 * 1024
)

// Error exposes a stable category, never a credential or remote response text.
type Error struct{ Code string }

func (e *Error) Error() string  { return e.Code }
func failure(code string) error { return &Error{Code: code} }

type Headers struct {
	UID        uint32
	SizeBytes  int64
	RawHeaders []byte
}

type options struct {
	address                          string
	tlsConfig                        *tls.Config
	operationTimeout, sessionTimeout time.Duration
}

func defaults() options {
	return options{
		address:          "imap.gmail.com:993",
		tlsConfig:        &tls.Config{ServerName: "imap.gmail.com", MinVersion: tls.VersionTLS12},
		operationTimeout: 20 * time.Second, sessionTimeout: 120 * time.Second,
	}
}

type Mailbox struct {
	client      *imapclient.Client
	conn        *guardedConn
	life        context.Context
	cancel      context.CancelFunc
	stopLife    func() bool
	gate        chan struct{}
	opTimeout   time.Duration
	uidValidity string
}

// Open uses verified implicit TLS to the fixed Gmail endpoint and only INBOX.
// ctx owns the whole session, which additionally expires after two minutes.
// No endpoint override, plaintext fallback, debug writer or system proxy exists.
func Open(ctx context.Context, account, password, folder string) (*Mailbox, error) {
	return open(ctx, account, password, folder, defaults())
}

func open(ctx context.Context, account, password, folder string, o options) (*Mailbox, error) {
	if ctx == nil || folder != "INBOX" || !validCredential(account, 320) || !validCredential(password, 4096) {
		return nil, failure("EMAIL_CONNECTION_INVALID")
	}
	if err := ctx.Err(); err != nil {
		return nil, contextFailure(err)
	}
	life, cancel := context.WithTimeout(ctx, o.sessionTimeout)
	dialContext, finishDial := context.WithTimeout(life, o.operationTimeout)
	dialer := &tls.Dialer{NetDialer: &net.Dialer{}, Config: o.tlsConfig.Clone()}
	raw, err := dialer.DialContext(dialContext, "tcp", o.address)
	finishDial()
	if err != nil {
		cancel()
		if ctx.Err() != nil {
			return nil, contextFailure(ctx.Err())
		}
		return nil, failure("EMAIL_SYNC_NETWORK_FAILED")
	}
	conn := &guardedConn{Conn: raw, total: maxSessionBytes}
	m := &Mailbox{conn: conn, life: life, cancel: cancel, gate: make(chan struct{}, 1), opTimeout: o.operationTimeout}
	m.stopLife = context.AfterFunc(life, func() { _ = conn.Close() })
	// Initialize the guard before starting the library's background decoder.
	deadline, _ := life.Deadline()
	if err = conn.budget(earlier(deadline, time.Now().Add(o.operationTimeout)), maxControlBytes); err != nil {
		_ = m.Close()
		return nil, failure("EMAIL_SYNC_NETWORK_FAILED")
	}
	m.client = imapclient.New(conn, &imapclient.Options{})
	if err = m.run(ctx, maxControlBytes, "EMAIL_SYNC_NETWORK_FAILED", func() error { return m.client.WaitGreeting() }); err == nil {
		if m.client.State() != imap.ConnStateNotAuthenticated {
			err = failure("EMAIL_SYNC_PROTOCOL_FAILED")
		} else {
			err = m.run(ctx, maxControlBytes, "EMAIL_SYNC_AUTH_FAILED", func() error { return m.client.Login(account, password).Wait() })
		}
	}
	if err == nil {
		err = m.run(ctx, maxControlBytes, "EMAIL_SYNC_PROTOCOL_FAILED", func() error {
			data, selectErr := m.client.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait()
			if selectErr != nil {
				return selectErr
			}
			if data.UIDValidity == 0 {
				return failure("EMAIL_SYNC_UIDVALIDITY_INVALID")
			}
			m.uidValidity = strconv.FormatUint(uint64(data.UIDValidity), 10)
			return nil
		})
	}
	if err != nil {
		_ = m.Close()
		return nil, err
	}
	return m, nil
}

func validCredential(s string, maxBytes int) bool {
	return len(s) > 0 && len(s) <= maxBytes && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}

func (m *Mailbox) UIDValidity() string { return m.uidValidity }

// run serializes commands. Cancellation closes the entire session: an abandoned
// command cannot be reused as if its tagged response had been consumed.
func (m *Mailbox) run(ctx context.Context, limit int64, taggedFailure string, fn func() error) error {
	if ctx == nil {
		return failure("EMAIL_CONNECTION_INVALID")
	}
	op, cancel := context.WithTimeout(ctx, m.opTimeout)
	defer cancel()
	select {
	case m.gate <- struct{}{}:
	case <-op.Done():
		return contextFailure(op.Err())
	case <-m.life.Done():
		return contextFailure(m.life.Err())
	}
	defer func() { <-m.gate }()
	if op.Err() != nil {
		return contextFailure(op.Err())
	}
	if m.life.Err() != nil {
		return contextFailure(m.life.Err())
	}
	deadline, _ := op.Deadline()
	lifeDeadline, _ := m.life.Deadline()
	if err := m.conn.budget(earlier(deadline, lifeDeadline), limit); err != nil {
		_ = m.conn.Close()
		return failure("EMAIL_SYNC_NETWORK_FAILED")
	}
	stopped := make(chan struct{})
	stop := context.AfterFunc(op, func() { _ = m.conn.Close(); close(stopped) })
	defer stop()
	err := fn()
	if !stop() {
		<-stopped
	}
	if op.Err() != nil {
		_ = m.conn.Close()
		return contextFailure(op.Err())
	}
	if m.life.Err() != nil {
		return contextFailure(m.life.Err())
	}
	if m.conn.hitLimit() {
		return failure("EMAIL_SYNC_RESPONSE_TOO_LARGE")
	}
	if m.conn.badFrame() {
		return failure("EMAIL_SYNC_PROTOCOL_FAILED")
	}
	// The client reads asynchronously between calls. Do not let the completed
	// command's deadline kill an otherwise healthy idle session.
	if idleErr := m.conn.budget(lifeDeadline, maxControlBytes); idleErr != nil && err == nil {
		_ = m.conn.Close()
		return failure("EMAIL_SYNC_NETWORK_FAILED")
	}
	if err == nil {
		return nil
	}
	var safe *Error
	if errors.As(err, &safe) {
		return safe
	}
	var status *imap.Error
	if errors.As(err, &status) {
		if status.Type == imap.StatusResponseTypeNo || status.Type == imap.StatusResponseTypeBad {
			return failure(taggedFailure)
		}
	}
	_ = m.conn.Close()
	// Remote/parser messages can contain private mail text; never wrap them.
	var network net.Error
	if m.conn.networkFailed() || errors.As(err, &network) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return failure("EMAIL_SYNC_NETWORK_FAILED")
	}
	return failure("EMAIL_SYNC_PROTOCOL_FAILED")
}

func contextFailure(err error) error {
	if errors.Is(err, context.Canceled) {
		return failure("EMAIL_SYNC_CANCELED")
	}
	return failure("EMAIL_SYNC_NETWORK_FAILED")
}

func (m *Mailbox) SearchUIDs(ctx context.Context, start uint32, since time.Time) ([]uint32, error) {
	if start == 0 || since.IsZero() || since.Year() < 1 || since.Year() > 9999 {
		return nil, failure("EMAIL_SYNC_PROTOCOL_FAILED")
	}
	values := make([]uint32, 0)
	err := m.run(ctx, maxControlBytes, "EMAIL_SYNC_PROTOCOL_FAILED", func() error {
		set := imap.UIDSet{{Start: imap.UID(start), Stop: 0}}
		data, err := m.client.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{set}, Since: since}, nil).Wait()
		if err != nil {
			return err
		}
		all, ok := data.All.(imap.UIDSet)
		if !ok {
			return failure("EMAIL_SYNC_PROTOCOL_FAILED")
		}
		// Do not expand an attacker-controlled ESEARCH range before bounding it.
		var count uint64
		for _, r := range all {
			if r.Start == 0 || r.Stop == 0 || r.Start > r.Stop {
				return failure("EMAIL_SYNC_PROTOCOL_FAILED")
			}
			count += uint64(r.Stop) - uint64(r.Start) + 1
			if count > MaxSearchUIDs {
				return failure("EMAIL_SYNC_RESPONSE_TOO_LARGE")
			}
		}
		for _, r := range all {
			for uid := uint64(r.Start); uid <= uint64(r.Stop); uid++ {
				if uid >= uint64(start) {
					values = append(values, uint32(uid))
				}
			}
		}
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		// UIDSet normally merges overlaps; keep the public invariant explicit.
		unique := values[:0]
		for _, uid := range values {
			if len(unique) == 0 || unique[len(unique)-1] != uid {
				unique = append(unique, uid)
			}
		}
		values = unique
		return nil
	})
	if err != nil {
		return nil, err
	}
	return values, nil
}

func (m *Mailbox) FetchHeaders(ctx context.Context, uid uint32) (Headers, error) {
	section := &imap.FetchItemBodySection{Peek: true, Specifier: imap.PartSpecifierHeader, HeaderFields: []string{"MESSAGE-ID", "FROM", "DATE", "SUBJECT"}}
	return m.fetch(ctx, uid, section, MaxHeaderBytes, true)
}

func (m *Mailbox) FetchMessage(ctx context.Context, uid uint32) ([]byte, error) {
	result, err := m.fetch(ctx, uid, &imap.FetchItemBodySection{Peek: true}, MaxMessageBytes, false)
	if err != nil {
		return nil, err
	}
	return result.RawHeaders, nil
}

func (m *Mailbox) fetch(ctx context.Context, uid uint32, section *imap.FetchItemBodySection, limit int64, requireSize bool) (Headers, error) {
	if uid == 0 {
		return Headers{}, failure("EMAIL_SYNC_PROTOCOL_FAILED")
	}
	var result Headers
	err := m.run(ctx, limit+maxControlBytes, "EMAIL_SYNC_PROTOCOL_FAILED", func() error {
		cmd := m.client.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{UID: true, RFC822Size: true, BodySection: []*imap.FetchItemBodySection{section}})
		// Always drain after closing the socket on refusal. Client.Close alone
		// waits for the decoder, which may itself be waiting on this consumer.
		defer cmd.Close()
		found := false
		for msg := cmd.Next(); msg != nil; msg = cmd.Next() {
			var got Headers
			hasUID, hasSize, hasBody := false, false, false
			for item := msg.Next(); item != nil; item = msg.Next() {
				switch item := item.(type) {
				case imapclient.FetchItemDataUID:
					if hasUID {
						_ = m.conn.Close()
						return failure("EMAIL_SYNC_PROTOCOL_FAILED")
					}
					got.UID, hasUID = uint32(item.UID), true
				case imapclient.FetchItemDataRFC822Size:
					if hasSize || item.Size < 0 {
						_ = m.conn.Close()
						return failure("EMAIL_SYNC_PROTOCOL_FAILED")
					}
					got.SizeBytes, hasSize = item.Size, true
				case imapclient.FetchItemDataBodySection:
					if hasBody || !item.MatchCommand(section) || item.Literal == nil {
						_ = m.conn.Close()
						return failure("EMAIL_SYNC_PROTOCOL_FAILED")
					}
					if item.Literal.Size() > limit {
						_ = m.conn.Close()
						return failure("EMAIL_SYNC_RESPONSE_TOO_LARGE")
					}
					body, readErr := io.ReadAll(io.LimitReader(item.Literal, limit+1))
					if readErr != nil {
						_ = m.conn.Close()
						return readErr
					}
					if int64(len(body)) > limit {
						_ = m.conn.Close()
						return failure("EMAIL_SYNC_RESPONSE_TOO_LARGE")
					}
					got.RawHeaders, hasBody = body, true
				default:
					// FLAGS and unsolicited metadata are not candidate evidence.
				}
			}
			if !hasUID || got.UID != uid || !hasBody {
				continue
			}
			if requireSize && !hasSize {
				_ = m.conn.Close()
				return failure("EMAIL_SYNC_PROTOCOL_FAILED")
			}
			if found {
				_ = m.conn.Close()
				return failure("EMAIL_SYNC_PROTOCOL_FAILED")
			}
			result, found = got, true
		}
		if err := cmd.Close(); err != nil {
			return err
		}
		if !found {
			return failure("EMAIL_SYNC_MESSAGE_MISSING")
		}
		return nil
	})
	if err != nil {
		return Headers{}, err
	}
	return result, nil
}

// Close terminates the socket, never the IMAP CLOSE/EXPUNGE mailbox command.
// It is idempotent and does not wait on a consumer that may be closing it.
func (m *Mailbox) Close() error {
	if m == nil {
		return nil
	}
	if m.stopLife != nil {
		m.stopLife()
	}
	if m.cancel != nil {
		m.cancel()
	}
	if m.conn != nil {
		_ = m.conn.Close()
	}
	return nil
}
