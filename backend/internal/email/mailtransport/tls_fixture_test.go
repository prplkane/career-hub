package mailtransport

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

const fixtureAccount = "synthetic@example.test"
const fixturePassword = "synthetic-test-password"
const remotePrivateText = "PRIVATE-REMOTE-CONTENT-DO-NOT-DISCLOSE"
const maxFixtureCommandBytes = 16 * 1024
const maxFixtureCommands = 64

type tlsFixture struct {
	listener   net.Listener
	options    options
	done       chan error
	peerClosed chan struct{}
	peerOnce   sync.Once
	mu         sync.Mutex
	connection net.Conn
	commands   []string
}

type imapPeer struct {
	fixture               *tlsFixture
	conn                  *tls.Conn
	reader                *bufio.Reader
	automaticCapabilities bool
}

// Both CA and server keys/certificates exist only in memory. The sole listener
// uses an OS-assigned IPv4 loopback port; no DNS lookup or mailbox is involved.
func newTLSFixture(t *testing.T, allowTLSFailure bool, script func(*imapPeer) error) *tlsFixture {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic IMAP test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"mail.synthetic.test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	serverDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &tlsFixture{listener: listener, done: make(chan error, 1), peerClosed: make(chan struct{})}
	f.options = options{address: listener.Addr().String(), tlsConfig: &tls.Config{RootCAs: pool, ServerName: "mail.synthetic.test", MinVersion: tls.VersionTLS12}, operationTimeout: 400 * time.Millisecond, sessionTimeout: 3 * time.Second}
	serverTLS := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{serverDER, caDER}, PrivateKey: serverKey}}, MinVersion: tls.VersionTLS12}
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			f.done <- err
			return
		}
		f.mu.Lock()
		f.connection = conn
		f.mu.Unlock()
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
		secure := tls.Server(conn, serverTLS)
		if err := secure.HandshakeContext(context.Background()); err != nil {
			if allowTLSFailure {
				f.markPeerClosed()
				f.done <- nil
			} else {
				f.done <- fmt.Errorf("synthetic TLS handshake failed: %w", err)
			}
			return
		}
		f.done <- script(&imapPeer{fixture: f, conn: secure, reader: bufio.NewReaderSize(secure, maxFixtureCommandBytes+1)})
	}()
	t.Cleanup(func() {
		listener.Close()
		f.mu.Lock()
		conn := f.connection
		f.mu.Unlock()
		if conn != nil {
			conn.Close()
		}
		select {
		case err := <-f.done:
			if err != nil {
				t.Errorf("synthetic server: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("synthetic server did not stop after fixture close")
		}
	})
	return f
}

func (f *tlsFixture) markPeerClosed() { f.peerOnce.Do(func() { close(f.peerClosed) }) }

func (f *tlsFixture) assertPeerClosed(t *testing.T) {
	t.Helper()
	select {
	case <-f.peerClosed:
	case <-time.After(time.Second):
		t.Fatal("client returned without closing the owned TLS connection")
	}
}

func (f *tlsFixture) assertScript(t *testing.T) {
	t.Helper()
	select {
	case err := <-f.done:
		f.done <- err // Cleanup also verifies/reaps the already completed worker.
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("synthetic IMAP script did not finish")
	}
}

func (p *imapPeer) write(value string) error { _, err := io.WriteString(p.conn, value); return err }

func (p *imapPeer) read() (string, string, error) {
	raw, err := p.reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) || len(raw) > maxFixtureCommandBytes {
		return "", "", errors.New("synthetic client command exceeded fixture byte limit")
	}
	if err != nil {
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return "", "", errors.New("synthetic peer read timed out before client close")
		}
		p.fixture.markPeerClosed()
		return "", "", io.EOF
	}
	line := string(raw)
	if !strings.HasSuffix(line, "\r\n") {
		return "", "", errors.New("client command missing CRLF")
	}
	fields := strings.SplitN(strings.TrimSuffix(line, "\r\n"), " ", 2)
	if len(fields) != 2 {
		return "", "", errors.New("client command missing tag")
	}
	p.fixture.mu.Lock()
	if len(p.fixture.commands) >= maxFixtureCommands {
		p.fixture.mu.Unlock()
		return "", "", errors.New("synthetic client exceeded fixture command-count limit")
	}
	p.fixture.commands = append(p.fixture.commands, fields[1])
	p.fixture.mu.Unlock()
	return fields[0], fields[1], nil
}

// The selected library may refresh CAPABILITY after greeting and LOGIN. Serve
// those requests explicitly while preserving all domain-command assertions.
func (p *imapPeer) next(prefix string) (string, string, error) {
	for {
		tag, command, err := p.read()
		if err != nil {
			return "", "", err
		}
		if command == "CAPABILITY" {
			if err := p.write("* CAPABILITY IMAP4rev1\r\n" + tag + " OK capability complete\r\n"); err != nil {
				return "", "", err
			}
			continue
		}
		if !strings.HasPrefix(command, prefix) {
			return "", "", fmt.Errorf("unexpected synthetic command kind; expected %s", prefix)
		}
		return tag, command, nil
	}
}

func (p *imapPeer) login() error {
	capability := "[CAPABILITY IMAP4rev1] "
	if p.automaticCapabilities {
		capability = ""
	}
	if err := p.write("* OK " + capability + "synthetic IMAP ready\r\n"); err != nil {
		return err
	}
	tag, command, err := p.next("LOGIN ")
	if err != nil {
		return err
	}
	if command != "LOGIN \""+fixtureAccount+"\" \""+fixturePassword+"\"" {
		return errors.New("LOGIN changed synthetic account/password bytes")
	}
	return p.write(tag + " OK " + capability + "login complete\r\n")
}

func (p *imapPeer) bootstrap() error {
	if err := p.login(); err != nil {
		return err
	}
	tag, command, err := p.next("EXAMINE ")
	if err != nil {
		return err
	}
	if command != "EXAMINE INBOX" && command != "EXAMINE \"INBOX\"" {
		return errors.New("mailbox selection is not exact readonly INBOX")
	}
	return p.write("* FLAGS (\\Seen)\r\n* 3 EXISTS\r\n* OK [UIDVALIDITY 4242] valid\r\n" + tag + " OK [READ-ONLY] examine complete\r\n")
}

func (p *imapPeer) waitClose() error {
	_, _, err := p.next("no-domain-command-after-operation")
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("Close emitted an unexpected IMAP command")
}

func openFixture(t *testing.T, f *tlsFixture) *Mailbox {
	t.Helper()
	m, err := open(context.Background(), fixtureAccount, fixturePassword, "INBOX", f.options)
	if err != nil {
		t.Fatalf("synthetic mailbox open failed: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var safe *Error
	if !errors.As(err, &safe) || safe.Code != code || err.Error() != code {
		t.Fatalf("error = %v, want fixed %s", err, code)
	}
	if strings.Contains(fmt.Sprintf("%+v", err), remotePrivateText) || strings.Contains(err.Error(), fixturePassword) {
		t.Fatal("mail failure exposed remote text or credential")
	}
}
