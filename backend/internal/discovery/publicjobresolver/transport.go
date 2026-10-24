package publicjobresolver

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const userAgent = "CareerHub/0.1 (attended public job resolver)"
const operationTimeout = 5 * time.Second
const totalTimeout = 15 * time.Second

type HTMLResponse struct {
	FinalURL                 string
	Body                     []byte
	Charset                  string
	RedirectCount, BytesRead int
}
type target struct {
	url, scheme, host, request string
	port                       string
	addresses                  []netip.Addr
}
type transport struct {
	lookup func(context.Context, string) ([]netip.Addr, error)
	// Test seams are package-private: callers cannot supply an unvalidated URL,
	// proxy, credentials, TLS configuration or custom dialer through the API.
	one func(context.Context, target, netip.Addr) (*http.Response, func(), *Error)
}

func Fetch(ctx context.Context, value string) (*HTMLResponse, *Error) {
	t := transport{lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}, one: pinnedResponse}
	return t.fetch(ctx, value)
}
func (t transport) resolve(ctx context.Context, value, previous string) (target, *Error) {
	normalized, e := NormalizeURL(value, previous)
	if e != nil {
		return target{}, e
	}
	scheme, rest, _ := strings.Cut(normalized, "://")
	host, request, _ := strings.Cut(rest, "/")
	x := target{url: normalized, scheme: scheme, host: host, request: "/" + request, port: "80"}
	if scheme == "https" {
		x.port = "443"
	}
	answers, err := t.lookup(ctx, host)
	if err != nil {
		return target{}, failure("DNS_REJECTED", "dns")
	}
	seen := map[netip.Addr]bool{}
	for _, a := range answers {
		if !publicAddress(a) {
			return target{}, failure("DNS_REJECTED", "dns")
		}
		if !seen[a] {
			x.addresses = append(x.addresses, a)
			seen[a] = true
		}
		if len(x.addresses) > MaxDNSAddresses {
			return target{}, failure("DNS_REJECTED", "dns")
		}
	}
	if len(x.addresses) == 0 {
		return target{}, failure("DNS_REJECTED", "dns")
	}
	return x, nil
}

// deadlineConn bounds each read/write as well as the entire operation. net/http
// cancels the connection with the request; the context also bounds DNS and TLS.
type deadlineConn struct {
	net.Conn
	deadline time.Time
}

func (c *deadlineConn) until() time.Time {
	d := time.Now().Add(operationTimeout)
	if !c.deadline.IsZero() && c.deadline.Before(d) {
		return c.deadline
	}
	return d
}
func (c *deadlineConn) Read(p []byte) (int, error) {
	if err := c.Conn.SetReadDeadline(c.until()); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}
func (c *deadlineConn) Write(p []byte) (int, error) {
	if err := c.Conn.SetWriteDeadline(c.until()); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}

func pinnedResponse(ctx context.Context, x target, address netip.Addr) (*http.Response, func(), *Error) {
	return pinnedResponseWithDial(ctx, x, address, (&net.Dialer{}).DialContext)
}

func pinnedResponseWithDial(ctx context.Context, x target, address netip.Addr, dialAddress func(context.Context, string, string) (net.Conn, error)) (*http.Response, func(), *Error) {
	var tlsFailure bool
	// net/http detaches the dial context's cancellation/deadline. Keep the
	// request's absolute budget explicitly for the connected socket.
	deadline, _ := ctx.Deadline()
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		connectCtx, cancel := context.WithTimeout(ctx, operationTimeout)
		defer cancel()
		conn, err := dialAddress(connectCtx, "tcp", net.JoinHostPort(address.String(), x.port))
		if err != nil {
			return nil, err
		}
		wrapped := &deadlineConn{Conn: conn, deadline: deadline}
		if x.scheme != "https" {
			return wrapped, nil
		}
		secure := tls.Client(wrapped, &tls.Config{ServerName: x.host, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
		if err = secure.HandshakeContext(connectCtx); err != nil {
			tlsFailure = true
			conn.Close()
			return nil, err
		}
		return secure, nil
	}
	roundTrip := &http.Transport{Proxy: nil, DialContext: dial, DialTLSContext: dial, DisableCompression: true, DisableKeepAlives: true, ResponseHeaderTimeout: operationTimeout, MaxResponseHeaderBytes: 1 << 20, ForceAttemptHTTP2: false}
	req := &http.Request{Method: "GET", URL: &url.URL{Scheme: x.scheme, Host: x.host, Opaque: x.request}, Host: x.host, Header: http.Header{"User-Agent": {userAgent}, "Accept": {"text/html"}, "Accept-Encoding": {"identity"}, "Connection": {"close"}}, Close: true}
	response, err := roundTrip.RoundTrip(req.WithContext(ctx))
	close := func() {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		roundTrip.CloseIdleConnections()
	}
	if err != nil {
		close()
		var timeout net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &timeout) && timeout.Timeout() {
			return nil, nil, failure("TIMEOUT", "timeout")
		}
		if tlsFailure {
			return nil, nil, failure("TLS_FAILED", "tls")
		}
		return nil, nil, failure("CONNECT_FAILED", "connect")
	}
	return response, close, nil
}

func (t transport) fetch(parent context.Context, value string) (*HTMLResponse, *Error) {
	ctx, cancel := context.WithTimeout(parent, totalTimeout)
	defer cancel()
	current, previous, redirects := value, "", 0
	for {
		x, e := t.resolve(ctx, current, previous)
		if e != nil {
			return nil, e
		}
		var response *http.Response
		var close func()
		for _, address := range x.addresses[:min(MaxConnectionAttempts, len(x.addresses))] {
			if ctx.Err() != nil {
				return nil, failure("TIMEOUT", "timeout")
			}
			response, close, e = t.one(ctx, x, address)
			if e == nil {
				break
			}
		}
		if e != nil {
			return nil, e
		}
		if response == nil || close == nil {
			return nil, failure("FAILED", "http")
		}
		status := response.StatusCode
		if status == 301 || status == 302 || status == 303 || status == 307 || status == 308 {
			location := joinedHeader(response.Header, "Location")
			close()
			if redirects >= MaxRedirects {
				return nil, failure("TOO_MANY_REDIRECTS", "redirect")
			}
			if location == "" {
				return nil, failure("REDIRECT_REJECTED", "redirect")
			}
			redirected, err := joinRedirect(x.url, location)
			if err != nil {
				return nil, failure("REDIRECT_REJECTED", "redirect")
			}
			current, e = NormalizeURL(redirected, x.scheme)
			if e != nil {
				return nil, failure("REDIRECT_REJECTED", "redirect")
			}
			previous = x.scheme
			redirects++
			continue
		}
		result, e := readResponse(ctx, x.url, response, redirects)
		close()
		return result, e
	}
}

func readResponse(ctx context.Context, finalURL string, r *http.Response, redirects int) (*HTMLResponse, *Error) {
	if r.StatusCode == 429 {
		return nil, failure("RATE_LIMITED", "http")
	}
	if r.StatusCode != 200 {
		return nil, failure("HTTP_UNAVAILABLE", "http")
	}
	encoding := strings.ToLower(joinedHeader(r.Header, "Content-Encoding"))
	if encoding != "" && encoding != "identity" {
		return nil, failure("CONTENT_UNSUPPORTED", "response")
	}
	media, params, err := mime.ParseMediaType(joinedHeader(r.Header, "Content-Type"))
	if err != nil || !strings.EqualFold(media, "text/html") {
		return nil, failure("CONTENT_UNSUPPORTED", "response")
	}
	charset := params["charset"]
	if charset == "" {
		charset = "utf-8"
	}
	if _, err := decodeHTML(nil, charset); err != nil {
		return nil, failure("CONTENT_UNSUPPORTED", "response")
	}
	declared := joinedHeader(r.Header, "Content-Length")
	if declared != "" {
		for _, c := range declared {
			if c < '0' || c > '9' {
				return nil, failure("CONTENT_UNSUPPORTED", "response")
			}
		}
		n, err := strconv.ParseUint(declared, 10, 64)
		if err != nil || n > MaxResponseBytes {
			return nil, failure("RESPONSE_TOO_LARGE", "response")
		}
	}
	if r.Body == nil {
		return nil, failure("FAILED", "response")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxResponseBytes+1))
	if ctx.Err() != nil {
		return nil, failure("TIMEOUT", "timeout")
	}
	if err != nil {
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return nil, failure("TIMEOUT", "timeout")
		}
		return nil, failure("FAILED", "http")
	}
	if len(body) > MaxResponseBytes {
		return nil, failure("RESPONSE_TOO_LARGE", "response")
	}
	return &HTMLResponse{FinalURL: finalURL, Body: body, Charset: charset, RedirectCount: redirects, BytesRead: len(body)}, nil
}

func joinRedirect(base, location string) (string, error) {
	// Preserve urllib.parse.urljoin's same-scheme relative URLs, semicolon
	// parameters and relative empty-segment handling before URL admission.
	b := splitJoinURL(base, "")
	u := splitJoinURL(location, b.scheme)
	if u.scheme != b.scheme || u.scheme != "http" && u.scheme != "https" {
		return location, nil
	}
	if u.host != "" {
		return u.string(), nil
	}
	u.host = b.host
	if u.path == "" && u.params == "" {
		u.path = b.path
		u.params = b.params
		if u.query == "" {
			u.query = b.query
		}
		return u.string(), nil
	}
	var segments []string
	if strings.HasPrefix(u.path, "/") {
		segments = strings.Split(u.path, "/")
	} else {
		parts := strings.Split(b.path, "/")
		if parts[len(parts)-1] != "" {
			parts = parts[:len(parts)-1]
		}
		segments = append(parts, strings.Split(u.path, "/")...)
		filtered := []string{segments[0]}
		for _, s := range segments[1 : len(segments)-1] {
			if s != "" {
				filtered = append(filtered, s)
			}
		}
		segments = append(filtered, segments[len(segments)-1])
	}
	var resolved []string
	for _, s := range segments {
		if s == ".." {
			if len(resolved) > 0 {
				resolved = resolved[:len(resolved)-1]
			}
		} else if s != "." {
			resolved = append(resolved, s)
		}
	}
	if last := segments[len(segments)-1]; last == "." || last == ".." {
		resolved = append(resolved, "")
	}
	u.path = strings.Join(resolved, "/")
	if u.path == "" {
		u.path = "/"
	}
	return u.string(), nil
}

func joinedHeader(header http.Header, name string) string {
	return strings.TrimSpace(strings.Join(header.Values(name), ", "))
}

type joinURL struct{ scheme, host, path, params, query, fragment string }

func splitJoinURL(value, defaultScheme string) joinURL {
	value = strings.TrimLeftFunc(value, func(r rune) bool { return r <= 32 })
	value = strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(value)
	u := joinURL{scheme: defaultScheme}
	if i := strings.IndexByte(value, ':'); i > 0 {
		valid := value[0] >= 'a' && value[0] <= 'z' || value[0] >= 'A' && value[0] <= 'Z'
		for _, c := range value[:i] {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.') {
				valid = false
			}
		}
		if valid {
			u.scheme = strings.ToLower(value[:i])
			value = value[i+1:]
		}
	}
	if strings.HasPrefix(value, "//") {
		value = value[2:]
		u.host = value
		value = ""
		if i := strings.IndexAny(u.host, "/?#"); i >= 0 {
			u.host, value = u.host[:i], u.host[i:]
		}
	}
	value, u.fragment, _ = strings.Cut(value, "#")
	u.path, u.query, _ = strings.Cut(value, "?")
	last := strings.LastIndexByte(u.path, '/')
	if i := strings.IndexByte(u.path[last+1:], ';'); i >= 0 {
		i += last + 1
		u.path, u.params = u.path[:i], u.path[i+1:]
	}
	return u
}
func (u joinURL) string() string {
	path := u.path
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if u.params != "" {
		path += ";" + u.params
	}
	result := u.scheme + "://" + u.host + path
	if u.query != "" {
		result += "?" + u.query
	}
	if u.fragment != "" {
		result += "#" + u.fragment
	}
	return result
}
