package mailtransport

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

const responseHeaderSection = "BODY[HEADER.FIELDS (MESSAGE-ID FROM DATE SUBJECT)]"

func TestReadOnlyTLSCommandSequenceAndExactBytes(t *testing.T) {
	headers := []byte("Message-ID: <synthetic@example.test>\r\nSubject: caf\xc3\xa9\r\n\r\n")
	body := []byte{'b', 'o', 'd', 'y', 0, 255, '\r', '\n'}
	f := newTLSFixture(t, false, func(p *imapPeer) error {
		p.automaticCapabilities = true
		if err := p.bootstrap(); err != nil {
			return err
		}
		tag, command, err := p.next("UID SEARCH ")
		if err != nil {
			return err
		}
		if !strings.Contains(command, "UID 5:*") || !strings.Contains(command, "SINCE \"2-Jan-2026\"") {
			return fmt.Errorf("search changed UID/date semantics: %s", command)
		}
		if err := p.write("* SEARCH 9 5 5 1 10\r\n" + tag + " OK search complete\r\n"); err != nil {
			return err
		}
		tag, command, err = p.next("UID FETCH 9 ")
		if err != nil {
			return err
		}
		if command != "UID FETCH 9 (UID RFC822.SIZE BODY.PEEK[HEADER.FIELDS (\"MESSAGE-ID\" \"FROM\" \"DATE\" \"SUBJECT\")])" {
			return errors.New("header fetch did not use exact PEEK fields")
		}
		if err := p.write(fmt.Sprintf("* 1 FETCH (UID 9 RFC822.SIZE 123 %s {%d}\r\n%s)\r\n%s OK headers complete\r\n", responseHeaderSection, len(headers), headers, tag)); err != nil {
			return err
		}
		tag, command, err = p.next("UID FETCH 9 ")
		if err != nil {
			return err
		}
		if command != "UID FETCH 9 (UID RFC822.SIZE BODY.PEEK[])" {
			return errors.New("message fetch did not use UID and PEEK")
		}
		if err := p.write(fmt.Sprintf("* 2 FETCH (UID 9 RFC822.SIZE 123 BODY[] {%d}\r\n%s)\r\n%s OK body complete\r\n", len(body), body, tag)); err != nil {
			return err
		}
		return p.waitClose()
	})
	m := openFixture(t, f)
	if m.UIDValidity() != "4242" {
		t.Fatal("UIDVALIDITY changed")
	}
	uids, err := m.SearchUIDs(context.Background(), 5, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	if err != nil || !reflect.DeepEqual(uids, []uint32{5, 9, 10}) {
		t.Fatalf("search = %v, %v", uids, err)
	}
	got, err := m.FetchHeaders(context.Background(), 9)
	if err != nil || got.UID != 9 || got.SizeBytes != 123 || !bytes.Equal(got.RawHeaders, headers) {
		t.Fatalf("headers = %#v, %v", got, err)
	}
	message, err := m.FetchMessage(context.Background(), 9)
	if err != nil || !bytes.Equal(message, body) {
		t.Fatalf("message = %v, %v", message, err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal("Close was not idempotent")
	}
	f.assertPeerClosed(t)
	f.assertScript(t)
	f.mu.Lock()
	defer f.mu.Unlock()
	capabilities := 0
	var domain []string
	for _, command := range f.commands {
		if command == "CAPABILITY" {
			capabilities++
		} else {
			domain = append(domain, strings.Fields(command)[0])
		}
	}
	if capabilities < 1 || !reflect.DeepEqual(domain, []string{"LOGIN", "EXAMINE", "UID", "UID", "UID"}) {
		t.Fatal("automatic CAPABILITY or readonly command sequence missing")
	}
}

func TestTLSCertificateAndHostnameRefusal(t *testing.T) {
	for _, hostname := range []bool{false, true} {
		t.Run(fmt.Sprint("hostname=", hostname), func(t *testing.T) {
			f := newTLSFixture(t, true, func(*imapPeer) error { return errors.New("untrusted TLS reached IMAP protocol") })
			if hostname {
				f.options.tlsConfig.ServerName = "wrong.synthetic.test"
			} else {
				f.options.tlsConfig.RootCAs = x509.NewCertPool()
			}
			m, err := open(context.Background(), fixtureAccount, fixturePassword, "INBOX", f.options)
			if m != nil {
				m.Close()
				t.Fatal("TLS refusal returned a mailbox")
			}
			assertCode(t, err, "EMAIL_SYNC_NETWORK_FAILED")
			f.assertPeerClosed(t)
			f.assertScript(t)
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.commands) != 0 {
				t.Fatal("credentials crossed failed TLS boundary")
			}
		})
	}
}

func TestOpenTaggedFailuresAndInvalidUIDValidity(t *testing.T) {
	cases := []struct{ stage, status, code string }{
		{"LOGIN", "NO", "EMAIL_SYNC_AUTH_FAILED"}, {"LOGIN", "BAD", "EMAIL_SYNC_AUTH_FAILED"},
		{"EXAMINE", "NO", "EMAIL_SYNC_PROTOCOL_FAILED"}, {"EXAMINE", "BAD", "EMAIL_SYNC_PROTOCOL_FAILED"},
		{"EXAMINE", "missing-uidvalidity", "EMAIL_SYNC_UIDVALIDITY_INVALID"},
	}
	for _, tc := range cases {
		t.Run(tc.stage+"/"+tc.status, func(t *testing.T) {
			f := newTLSFixture(t, false, func(p *imapPeer) error {
				if tc.stage == "LOGIN" {
					if err := p.write("* OK synthetic ready\r\n"); err != nil {
						return err
					}
				} else if err := p.login(); err != nil {
					return err
				}
				tag, _, err := p.next(tc.stage + " ")
				if err != nil {
					return err
				}
				status := tc.status
				if status == "missing-uidvalidity" {
					status = "OK [READ-ONLY]"
				}
				if err := p.write(tag + " " + status + " " + remotePrivateText + "\r\n"); err != nil {
					return err
				}
				return p.waitClose()
			})
			m, err := open(context.Background(), fixtureAccount, fixturePassword, "INBOX", f.options)
			if m != nil {
				m.Close()
				t.Fatal("failed Open returned mailbox")
			}
			assertCode(t, err, tc.code)
			f.assertPeerClosed(t)
			f.assertScript(t)
		})
	}
}

func TestFetchUIDBodyAndHeaderSizeRequirements(t *testing.T) {
	cases := []struct{ name, attributes, code string }{
		{"missing message", "", "EMAIL_SYNC_MESSAGE_MISSING"},
		{"wrong UID", "UID 8 RFC822.SIZE 3 " + responseHeaderSection + " {3}\r\nabc", "EMAIL_SYNC_MESSAGE_MISSING"},
		{"missing UID", "RFC822.SIZE 3 " + responseHeaderSection + " {3}\r\nabc", "EMAIL_SYNC_MESSAGE_MISSING"},
		{"body before UID", responseHeaderSection + " {3}\r\nabc UID 9 RFC822.SIZE 3", "EMAIL_SYNC_MESSAGE_MISSING"},
		{"missing body", "UID 9 RFC822.SIZE 3", "EMAIL_SYNC_MESSAGE_MISSING"},
		{"missing header size", "UID 9 " + responseHeaderSection + " {3}\r\nabc", "EMAIL_SYNC_PROTOCOL_FAILED"},
		{"wrong section", "UID 9 RFC822.SIZE 3 BODY[TEXT] {3}\r\nabc", "EMAIL_SYNC_PROTOCOL_FAILED"},
		{"duplicate UID", "UID 9 UID 9 RFC822.SIZE 3 " + responseHeaderSection + " {3}\r\nabc", "EMAIL_SYNC_PROTOCOL_FAILED"},
		{"duplicate size", "UID 9 RFC822.SIZE 3 RFC822.SIZE 3 " + responseHeaderSection + " {3}\r\nabc", "EMAIL_SYNC_PROTOCOL_FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newTLSFixture(t, false, func(p *imapPeer) error {
				if err := p.bootstrap(); err != nil {
					return err
				}
				tag, _, err := p.next("UID FETCH 9 ")
				if err != nil {
					return err
				}
				response := tag + " OK complete\r\n"
				if tc.attributes != "" {
					response = "* 1 FETCH (" + tc.attributes + ")\r\n" + response
				}
				if err := p.write(response); err != nil {
					return err
				}
				return p.waitClose()
			})
			m := openFixture(t, f)
			got, err := m.FetchHeaders(context.Background(), 9)
			assertCode(t, err, tc.code)
			if !reflect.DeepEqual(got, Headers{}) {
				t.Fatal("failed fetch exposed partial data")
			}
			m.Close()
			f.assertPeerClosed(t)
			f.assertScript(t)
		})
	}
}

func TestFetchTaggedFailuresByeAndDisconnect(t *testing.T) {
	for _, status := range []string{"NO", "BAD", "BYE", "disconnect"} {
		t.Run(status, func(t *testing.T) {
			f := newTLSFixture(t, false, func(p *imapPeer) error {
				if err := p.bootstrap(); err != nil {
					return err
				}
				tag, _, err := p.next("UID FETCH 9 ")
				if err != nil {
					return err
				}
				if status == "disconnect" {
					return nil
				}
				if status == "BYE" {
					return p.write("* BYE " + remotePrivateText + "\r\n")
				}
				if err := p.write(tag + " " + status + " " + remotePrivateText + "\r\n"); err != nil {
					return err
				}
				return p.waitClose()
			})
			m := openFixture(t, f)
			got, err := m.FetchMessage(context.Background(), 9)
			code := "EMAIL_SYNC_PROTOCOL_FAILED"
			if status == "disconnect" || status == "BYE" {
				code = "EMAIL_SYNC_NETWORK_FAILED"
			}
			assertCode(t, err, code)
			if got != nil {
				t.Fatal("failed fetch returned partial data")
			}
			m.Close()
			if status != "disconnect" && status != "BYE" {
				f.assertPeerClosed(t)
			}
			f.assertScript(t)
		})
	}
}

func TestStallsAndCancellationCloseConnection(t *testing.T) {
	for _, stage := range []string{"greeting", "login", "literal"} {
		for _, cancelOperation := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cancel=%v", stage, cancelOperation), func(t *testing.T) {
				reached := make(chan struct{})
				f := newTLSFixture(t, false, func(p *imapPeer) error {
					if stage == "login" {
						if err := p.write("* OK synthetic ready\r\n"); err != nil {
							return err
						}
						if _, _, err := p.next("LOGIN "); err != nil {
							return err
						}
					} else if stage == "literal" {
						if err := p.bootstrap(); err != nil {
							return err
						}
						if _, _, err := p.next("UID FETCH 9 "); err != nil {
							return err
						}
						if err := p.write("* 1 FETCH (UID 9 RFC822.SIZE 100 BODY[] {100}\r\npartial"); err != nil {
							return err
						}
					}
					close(reached)
					return p.waitClose()
				})
				f.options.operationTimeout = 250 * time.Millisecond
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				result := make(chan error, 1)
				var m *Mailbox
				if stage == "literal" {
					m = openFixture(t, f)
				}
				started := time.Now()
				go func() {
					if stage == "literal" {
						_, err := m.FetchMessage(ctx, 9)
						result <- err
					} else {
						value, err := open(ctx, fixtureAccount, fixturePassword, "INBOX", f.options)
						if value != nil {
							value.Close()
						}
						result <- err
					}
				}()
				select {
				case <-reached:
				case <-time.After(time.Second):
					t.Fatal("stall fixture did not reach selected stage")
				}
				if cancelOperation {
					cancel()
				}
				select {
				case err := <-result:
					code := "EMAIL_SYNC_NETWORK_FAILED"
					if cancelOperation {
						code = "EMAIL_SYNC_CANCELED"
					}
					assertCode(t, err, code)
				case <-time.After(time.Second):
					t.Fatal("transport cancellation did not interrupt blocking I/O")
				}
				if time.Since(started) >= time.Second {
					t.Fatal("operation exceeded injected deadline bound")
				}
				// Assert closure before calling Mailbox.Close or fixture cleanup.
				f.assertPeerClosed(t)
				f.assertScript(t)
			})
		}
	}
}

func TestOversizedLiteralClosesBeforeDrain(t *testing.T) {
	for _, headers := range []bool{false, true} {
		t.Run(fmt.Sprint("headers=", headers), func(t *testing.T) {
			limit, section := MaxMessageBytes, "BODY[]"
			if headers {
				limit, section = MaxHeaderBytes, responseHeaderSection
			}
			f := newTLSFixture(t, false, func(p *imapPeer) error {
				if err := p.bootstrap(); err != nil {
					return err
				}
				if _, _, err := p.next("UID FETCH 9 "); err != nil {
					return err
				}
				// Advertise an over-limit literal and intentionally send no body or
				// tagged completion. Refusal must close before deferred Fetch.Close.
				if err := p.write(fmt.Sprintf("* 1 FETCH (UID 9 RFC822.SIZE %d %s {%d}\r\n", limit+1, section, limit+1)); err != nil {
					return err
				}
				return p.waitClose()
			})
			m := openFixture(t, f)
			started := time.Now()
			var err error
			if headers {
				_, err = m.FetchHeaders(context.Background(), 9)
			} else {
				_, err = m.FetchMessage(context.Background(), 9)
			}
			assertCode(t, err, "EMAIL_SYNC_RESPONSE_TOO_LARGE")
			if time.Since(started) > 300*time.Millisecond {
				t.Fatal("literal refusal waited for operation timeout while draining")
			}
			f.assertPeerClosed(t)
			f.assertScript(t)
		})
	}
}

func TestMissingHeaderSizeClosesBeforeStalledTail(t *testing.T) {
	f := newTLSFixture(t, false, func(p *imapPeer) error {
		if err := p.bootstrap(); err != nil {
			return err
		}
		if _, _, err := p.next("UID FETCH 9 "); err != nil {
			return err
		}
		if err := p.write("* 1 FETCH (UID 9 " + responseHeaderSection + " {3}\r\nabc)\r\n"); err != nil {
			return err
		}
		return p.waitClose()
	})
	m := openFixture(t, f)
	_, err := m.FetchHeaders(context.Background(), 9)
	assertCode(t, err, "EMAIL_SYNC_PROTOCOL_FAILED")
	f.assertPeerClosed(t)
	f.assertScript(t)
}

func TestIdleSessionSurvivesIndividualOperationDeadline(t *testing.T) {
	f := newTLSFixture(t, false, func(p *imapPeer) error {
		if err := p.bootstrap(); err != nil {
			return err
		}
		tag, _, err := p.next("UID SEARCH ")
		if err != nil {
			return err
		}
		if err := p.write("* SEARCH 9\r\n" + tag + " OK complete\r\n"); err != nil {
			return err
		}
		return p.waitClose()
	})
	f.options.operationTimeout = 200 * time.Millisecond
	f.options.sessionTimeout = 2 * time.Second
	m := openFixture(t, f)
	time.Sleep(450 * time.Millisecond)
	got, err := m.SearchUIDs(context.Background(), 1, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	if err != nil || !reflect.DeepEqual(got, []uint32{9}) {
		t.Fatalf("idle session lost before whole-session deadline: %v, %v", got, err)
	}
	m.Close()
	f.assertPeerClosed(t)
	f.assertScript(t)
}

func TestSearchResponseBoundsAndInvalidTokens(t *testing.T) {
	for _, tc := range []struct{ name, response, code string }{
		{"empty", "* SEARCH\r\n", ""},
		{"bad token", "* SEARCH invalid\r\n", "EMAIL_SYNC_PROTOCOL_FAILED"},
		{"zero UID", "* SEARCH 0\r\n", "EMAIL_SYNC_PROTOCOL_FAILED"},
		{"oversized range", "* ESEARCH (TAG \"%s\") UID ALL 1:4294967295\r\n", "EMAIL_SYNC_RESPONSE_TOO_LARGE"},
		{"count zero without ALL", "* ESEARCH (TAG \"%s\") UID COUNT 0\r\n", "EMAIL_SYNC_PROTOCOL_FAILED"},
		{"count one without ALL", "* ESEARCH (TAG \"%s\") UID COUNT 1\r\n", "EMAIL_SYNC_PROTOCOL_FAILED"},
		{"non UID sequence set", "* ESEARCH (TAG \"%s\") ALL 9\r\n", "EMAIL_SYNC_PROTOCOL_FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTLSFixture(t, false, func(p *imapPeer) error {
				if err := p.bootstrap(); err != nil {
					return err
				}
				tag, _, err := p.next("UID SEARCH ")
				if err != nil {
					return err
				}
				response := tc.response
				if strings.Contains(response, "%s") {
					response = fmt.Sprintf(response, tag)
				}
				if err := p.write(response + tag + " OK complete\r\n"); err != nil {
					return err
				}
				return p.waitClose()
			})
			m := openFixture(t, f)
			got, err := m.SearchUIDs(context.Background(), 1, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
			if tc.code == "" {
				if err != nil || got == nil || len(got) != 0 {
					t.Fatalf("empty search = %v, %v", got, err)
				}
			} else {
				assertCode(t, err, tc.code)
				if got != nil {
					t.Fatal("failed search exposed partial UIDs")
				}
			}
			m.Close()
			f.assertPeerClosed(t)
			f.assertScript(t)
		})
	}
}

func TestInvalidOperationsEmitNoIMAPCommands(t *testing.T) {
	f := newTLSFixture(t, false, func(p *imapPeer) error {
		if err := p.bootstrap(); err != nil {
			return err
		}
		return p.waitClose()
	})
	m := openFixture(t, f)
	since := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		uid   uint32
		since time.Time
	}{{0, since}, {1, time.Time{}}, {1, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}} {
		_, err := m.SearchUIDs(context.Background(), item.uid, item.since)
		assertCode(t, err, "EMAIL_SYNC_PROTOCOL_FAILED")
	}
	_, err := m.FetchHeaders(context.Background(), 0)
	assertCode(t, err, "EMAIL_SYNC_PROTOCOL_FAILED")
	_, err = m.FetchMessage(context.Background(), 0)
	assertCode(t, err, "EMAIL_SYNC_PROTOCOL_FAILED")
	_, err = m.FetchMessage(nil, 1)
	assertCode(t, err, "EMAIL_CONNECTION_INVALID")
	m.Close()
	f.assertPeerClosed(t)
	f.assertScript(t)
}

func TestWholeSessionCancellationAndExpiryCloseIdleConnection(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint("cancel=", cancelled), func(t *testing.T) {
			f := newTLSFixture(t, false, func(p *imapPeer) error {
				if err := p.bootstrap(); err != nil {
					return err
				}
				return p.waitClose()
			})
			f.options.sessionTimeout = 500 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m, err := open(ctx, fixtureAccount, fixturePassword, "INBOX", f.options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { m.Close() })
			if cancelled {
				cancel()
			}
			f.assertPeerClosed(t)
			_, err = m.FetchMessage(context.Background(), 9)
			code := "EMAIL_SYNC_NETWORK_FAILED"
			if cancelled {
				code = "EMAIL_SYNC_CANCELED"
			}
			assertCode(t, err, code)
			f.assertScript(t)
		})
	}
}

func TestDeepFetchFramingIsRefusedOverActualTLS(t *testing.T) {
	structure := "(\"TEXT\" \"PLAIN\" NIL NIL NIL \"7BIT\" 1 1)"
	for i := 0; i < 40; i++ {
		structure = "(" + structure + " \"MIXED\")"
	}
	for _, prefix := range []string{"", "* OK harmless {100000}\r\n", "* OK unmatched \"( {100000}\r\n"} {
		t.Run(fmt.Sprint("prefix-bytes=", len(prefix)), func(t *testing.T) {
			f := newTLSFixture(t, false, func(p *imapPeer) error {
				if err := p.bootstrap(); err != nil {
					return err
				}
				if _, _, err := p.next("UID FETCH 9 "); err != nil {
					return err
				}
				if err := p.write(prefix + "* 1 FETCH (UID 9 BODYSTRUCTURE " + structure + ")\r\n"); err != nil {
					return err
				}
				return p.waitClose()
			})
			m := openFixture(t, f)
			got, err := m.FetchMessage(context.Background(), 9)
			assertCode(t, err, "EMAIL_SYNC_PROTOCOL_FAILED")
			if got != nil {
				t.Fatal("deep unrequested structure exposed message bytes")
			}
			f.assertPeerClosed(t)
			f.assertScript(t)
		})
	}
}

func TestInvalidInputsNeverDial(t *testing.T) {
	for _, tc := range []struct{ account, password, folder string }{
		{"", fixturePassword, "INBOX"}, {fixtureAccount, "", "INBOX"}, {fixtureAccount, fixturePassword, "Other"},
		{"bad\naccount", fixturePassword, "INBOX"}, {fixtureAccount, "bad\rpassword", "INBOX"},
		{string([]byte{255}), fixturePassword, "INBOX"}, {fixtureAccount, strings.Repeat("x", 4097), "INBOX"},
	} {
		m, err := open(context.Background(), tc.account, tc.password, tc.folder, options{})
		if m != nil {
			t.Fatal("invalid input returned mailbox")
		}
		assertCode(t, err, "EMAIL_CONNECTION_INVALID")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := open(ctx, fixtureAccount, fixturePassword, "INBOX", options{})
	assertCode(t, err, "EMAIL_SYNC_CANCELED")
	_, err = Open(nil, fixtureAccount, fixturePassword, "INBOX")
	assertCode(t, err, "EMAIL_CONNECTION_INVALID")
	var absent *Mailbox
	if err := absent.Close(); err != nil {
		t.Fatal(err)
	}
}
