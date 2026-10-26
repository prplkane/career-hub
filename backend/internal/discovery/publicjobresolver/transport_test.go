package publicjobresolver

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func publicLookup(context.Context, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
}
func fakeResponse(status int, headers http.Header, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body))}
}
func TestDNSRejectsEntireSetBeforeConnection(t *testing.T) {
	for _, answers := range [][]netip.Addr{nil, {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.1")}, {netip.Addr{}}, {netip.MustParseAddr("2606:4700:4700::1111%private-zone")}} {
		tx := transport{lookup: func(context.Context, string) ([]netip.Addr, error) { return answers, nil }, one: func(context.Context, target, netip.Addr) (*http.Response, func(), *Error) {
			t.Fatal("connection before DNS admission")
			return nil, nil, nil
		}}
		if _, e := tx.fetch(context.Background(), "https://example.com/a"); e == nil || e.Code != "PUBLIC_JOB_RESOLUTION_DNS_REJECTED" {
			t.Fatal(e)
		}
	}
}
func TestRedirectAdmissionAndBounds(t *testing.T) {
	for _, tc := range []struct{ name, location, code string }{
		{"downgrade", "http://other.example/a", "REDIRECT_REJECTED"}, {"protected", "https://www.linkedin.com/jobs/a", "REDIRECT_REJECTED"}, {"literal", "https://127.0.0.1/a", "REDIRECT_REJECTED"}, {"empty", "", "REDIRECT_REJECTED"}, {"repeat", "/next", "TOO_MANY_REDIRECTS"}, {"private DNS", "https://private.example/a", "DNS_REJECTED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connections, closes, lookups := 0, 0, 0
			tx := transport{lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
				lookups++
				if host == "private.example" {
					return []netip.Addr{netip.MustParseAddr("10.0.0.1")}, nil
				}
				return publicLookup(ctx, host)
			}, one: func(context.Context, target, netip.Addr) (*http.Response, func(), *Error) {
				connections++
				return fakeResponse(302, http.Header{"Location": {tc.location}}, ""), func() { closes++ }, nil
			}}
			_, e := tx.fetch(context.Background(), "https://example.com/a")
			if e == nil || e.Code != "PUBLIC_JOB_RESOLUTION_"+tc.code {
				t.Fatal(e)
			}
			if connections != closes || connections > 4 {
				t.Fatalf("connections=%d closes=%d", connections, closes)
			}
			if tc.name == "private DNS" && (lookups != 2 || connections != 1) {
				t.Fatal("redirect did not re-admit DNS")
			}
		})
	}
}
func TestResponseContract(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		headers    http.Header
		body, code string
	}{
		{"html", 200, http.Header{"Content-Type": {"text/html; charset=utf-8"}}, "<p>body</p>", ""},
		{"latin1", 200, http.Header{"Content-Type": {"text/html; charset=latin1"}}, "\xff", ""},
		{"rate", 429, nil, "", "RATE_LIMITED"}, {"missing", 404, nil, "", "HTTP_UNAVAILABLE"}, {"other", 201, nil, "", "HTTP_UNAVAILABLE"},
		{"encoding", 200, http.Header{"Content-Type": {"text/html"}, "Content-Encoding": {"gzip"}}, "", "CONTENT_UNSUPPORTED"},
		{"repeated encoding", 200, http.Header{"Content-Type": {"text/html"}, "Content-Encoding": {"identity", "gzip"}}, "", "CONTENT_UNSUPPORTED"},
		{"json", 200, http.Header{"Content-Type": {"application/json"}}, "{}", "CONTENT_UNSUPPORTED"},
		{"charset", 200, http.Header{"Content-Type": {"text/html; charset=unknown-secret"}}, "", "CONTENT_UNSUPPORTED"},
		{"length", 200, http.Header{"Content-Type": {"text/html"}, "Content-Length": {"-1"}}, "", "CONTENT_UNSUPPORTED"},
		{"declared", 200, http.Header{"Content-Type": {"text/html"}, "Content-Length": {"2097153"}}, "", "RESPONSE_TOO_LARGE"},
		{"stream", 200, http.Header{"Content-Type": {"text/html"}}, strings.Repeat("x", MaxResponseBytes+1), "RESPONSE_TOO_LARGE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			closed := false
			tx := transport{lookup: publicLookup, one: func(ctx context.Context, x target, a netip.Addr) (*http.Response, func(), *Error) {
				if x.host != "example.com" || x.request != "/a%2F?q=%zz" || x.port != "443" || a.String() != "93.184.216.34" {
					t.Fatalf("wrong pinned target: %+v %v", x, a)
				}
				return fakeResponse(tc.status, tc.headers, tc.body), func() { closed = true }, nil
			}}
			got, e := tx.fetch(context.Background(), "https://example.com/a%2F?q=%zz")
			if !closed {
				t.Fatal("connection leaked")
			}
			if tc.code != "" {
				if e == nil || e.Code != "PUBLIC_JOB_RESOLUTION_"+tc.code {
					t.Fatal(e)
				}
				return
			}
			if e != nil || string(got.Body) != tc.body || got.BytesRead != len(tc.body) || got.RedirectCount != 0 {
				t.Fatalf("%+v %v", got, e)
			}
		})
	}
}

func TestActualHTTPPinsAddressAndTimesOutStalledBody(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	client, server := net.Pipe()
	defer server.Close()
	lines := make(chan []string, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		r := bufio.NewReader(server)
		var request []string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if line == "\r\n" {
				break
			}
			request = append(request, strings.TrimSpace(line))
		}
		lines <- request
		io.WriteString(server, "HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nContent-Length: 10\r\n\r\n")
		var b [1]byte
		server.Read(b[:]) // Wait for the timed-out client to close.
	}()
	ctx, cancel := context.WithTimeout(context.Background(), totalTimeout)
	defer cancel()
	started := time.Now()
	r, close, e := pinnedResponseWithDial(ctx, target{scheme: "http", host: "jobs.example.com", port: "80", request: "/a%2F?q=%zz"}, netip.MustParseAddr("93.184.216.34"), func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != "93.184.216.34:80" {
			t.Errorf("dialed %s %s", network, address)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("missing connect deadline")
		}
		return client, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	_, e = readResponse(ctx, "http://jobs.example.com/a%2F?q=%zz", r, 0)
	close()
	if e == nil || e.Code != "PUBLIC_JOB_RESOLUTION_TIMEOUT" {
		t.Fatal(e)
	}
	if elapsed := time.Since(started); elapsed < 4*time.Second || elapsed > 9*time.Second {
		t.Fatalf("wrong read timeout: %v", elapsed)
	}
	request := strings.Join(<-lines, "\n")
	for _, line := range []string{"GET /a%2F?q=%zz HTTP/1.1", "Host: jobs.example.com", "User-Agent: " + userAgent, "Accept: text/html", "Accept-Encoding: identity", "Connection: close"} {
		if !strings.Contains(request, line) {
			t.Errorf("missing request header %q in %s", line, request)
		}
	}
	for _, forbidden := range []string{"Authorization:", "Cookie:", "Proxy-"} {
		if strings.Contains(request, forbidden) {
			t.Fatal("ambient credentials/proxy used")
		}
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("connection leaked")
	}
}

func TestActualTLSUsesHostnameAndRejectsUntrustedCertificate(t *testing.T) {
	sni := make(chan string, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("untrusted server reached HTTP") }))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) { sni <- hello.ServerName; return nil, nil }}
	server.StartTLS()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), totalTimeout)
	defer cancel()
	_, _, e := pinnedResponseWithDial(ctx, target{scheme: "https", host: "jobs.example.com", port: "443", request: "/"}, netip.MustParseAddr("93.184.216.34"), func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "93.184.216.34:443" {
			t.Errorf("not pinned: %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	})
	if e == nil || e.Code != "PUBLIC_JOB_RESOLUTION_TLS_FAILED" {
		t.Fatal(e)
	}
	select {
	case host := <-sni:
		if host != "jobs.example.com" {
			t.Fatal(host)
		}
	case <-time.After(time.Second):
		t.Fatal("missing SNI")
	}
}
func TestConnectionAttemptsAndDNSLimit(t *testing.T) {
	for _, count := range []int{4, 16, 17} {
		calls := 0
		tx := transport{lookup: func(context.Context, string) ([]netip.Addr, error) {
			var a []netip.Addr
			for i := 0; i < count; i++ {
				a = append(a, netip.AddrFrom4([4]byte{93, 184, 216, byte(i + 1)}))
			}
			return a, nil
		}, one: func(context.Context, target, netip.Addr) (*http.Response, func(), *Error) {
			calls++
			return nil, nil, failure("CONNECT_FAILED", "connect")
		}}
		_, e := tx.fetch(context.Background(), "https://example.com/a")
		if count > MaxDNSAddresses {
			if calls != 0 || e == nil || e.Code != "PUBLIC_JOB_RESOLUTION_DNS_REJECTED" {
				t.Fatalf("calls=%d %v", calls, e)
			}
		} else if calls != 4 || e == nil || e.Code != "PUBLIC_JOB_RESOLUTION_CONNECT_FAILED" {
			t.Fatalf("calls=%d %v", calls, e)
		}
	}
}
func TestCanceledAndSanitizedDNS(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tx := transport{lookup: publicLookup, one: func(context.Context, target, netip.Addr) (*http.Response, func(), *Error) {
		t.Fatal("canceled connection")
		return nil, nil, nil
	}}
	if _, e := tx.fetch(ctx, "https://example.com/a"); e == nil || e.Code != "PUBLIC_JOB_RESOLUTION_TIMEOUT" {
		t.Fatal(e)
	}
	tx.lookup = func(context.Context, string) ([]netip.Addr, error) {
		return nil, errors.New("secret private network details")
	}
	if _, e := tx.fetch(context.Background(), "https://example.com/a"); e == nil || strings.Contains(e.Error(), "secret") {
		t.Fatal(e)
	}
}
