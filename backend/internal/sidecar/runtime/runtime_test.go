package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"careerhub.local/backend/internal/sidecar/protocol"
)

const privateCause = "PRIVATE-CAUSE-C:/synthetic/private/token-secret"

func TestFixedLoopbackPortBoundsFailBeforePipeOrWorkspaceAccess(t *testing.T) {
	for _, port := range []int{-1, 0, 65536} {
		if RunWithLoopbackPort(nil, nil, nil, port) != 2 {
			t.Fatal("invalid fixed listener reached runtime")
		}
	}
}

func TestPrematureServeResultCannotBecomeSuccessfulEOF(t *testing.T) {
	for _, err := range []error{nil, http.ErrServerClosed, errors.New(privateCause)} {
		if !(serveResult{err: err, premature: true}).failed() {
			t.Fatal("premature server exit masked by concurrent parent EOF")
		}
	}
	if (serveResult{err: http.ErrServerClosed}).failed() || (serveResult{}).failed() {
		t.Fatal("owned shutdown classified as premature failure")
	}
}

func shortOptions() options {
	o := defaults()
	o.startup, o.initialize = 100*time.Millisecond, 100*time.Millisecond
	o.output, o.shutdown = 100*time.Millisecond, 120*time.Millisecond
	return o
}

func startPayload(t *testing.T) map[string]any {
	t.Helper()
	root, err := filepath.Abs("synthetic-unused-runtime-root")
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"command": "start", "protocol_version": 1, "port": 0, "channel": "dev",
		"resource_root": filepath.Join(root, "resources"), "data_root": filepath.Join(root, "data"),
	}
}

func goodPrepare(context.Context, protocol.StartRecord) (Prepared, error) {
	return Prepared{AppVersion: "0.1.2", Handler: func(int) (http.Handler, error) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/health" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(protocol.HealthRecord("0.1.2"))
		}), nil
	}}, nil
}

type runtimeHarness struct {
	input   *os.File
	output  *os.File
	result  <-chan int
	records <-chan readResult
}

func launchRuntime(t *testing.T, prepare Prepare, o options) *runtimeHarness {
	t.Helper()
	in, hostInput, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	out, childOutput, err := os.Pipe()
	if err != nil {
		in.Close()
		hostInput.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close(); hostInput.Close(); out.Close(); childOutput.Close() })
	result := make(chan int, 1)
	go func() { result <- run(in, childOutput, prepare, o) }()
	records := make(chan readResult, 8)
	go func() {
		defer close(records)
		for {
			record, err := protocol.ReadRecord(out)
			records <- readResult{record: record, err: err}
			if err != nil {
				return
			}
		}
	}()
	return &runtimeHarness{input: hostInput, output: out, result: result, records: records}
}

func (h *runtimeHarness) send(t *testing.T, record map[string]any) {
	t.Helper()
	if err := protocol.WriteRecord(h.input, record); err != nil {
		t.Fatal(err)
	}
}

func (h *runtimeHarness) next(t *testing.T) readResult {
	t.Helper()
	select {
	case value, ok := <-h.records:
		if !ok {
			t.Fatal("protocol output already ended")
		}
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("runtime protocol read exceeded test bound")
		return readResult{}
	}
}

func (h *runtimeHarness) expectError(t *testing.T, code string) {
	t.Helper()
	got := h.next(t)
	want := protocol.ErrorRecord(code)
	// Decode the helper through the same wire-number representation.
	encoded, err := protocol.EncodeRecord(want)
	if err != nil {
		t.Fatal(err)
	}
	wireWant, err := protocol.ReadRecord(bytes.NewReader(encoded))
	if err != nil || got.err != nil || !reflect.DeepEqual(got.record, wireWant) {
		t.Fatalf("error record = %#v, %v; want fixed %s", got.record, got.err, code)
	}
	if strings.Contains(fmt.Sprint(got.record), privateCause) {
		t.Fatal("protocol disclosed underlying cause")
	}
}

func (h *runtimeHarness) ready(t *testing.T) int {
	t.Helper()
	got := h.next(t)
	if got.err != nil || len(got.record) != 5 || got.record["event"] != "ready" || got.record["channel"] != "dev" || got.record["app_version"] != "0.1.2" || got.record["protocol_version"] != json.Number("1") {
		t.Fatalf("invalid ready record: %#v, %v", got.record, got.err)
	}
	value, ok := got.record["port"].(json.Number)
	port, err := value.Int64()
	if !ok || err != nil || port < 1 || port > 65535 {
		t.Fatal("invalid ready port")
	}
	return int(port)
}

func (h *runtimeHarness) done(t *testing.T, status int) {
	t.Helper()
	select {
	case got := <-h.result:
		if got != status {
			t.Fatalf("exit status = %d, want %d", got, status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runtime exceeded test exit bound")
	}
	last := h.next(t)
	var closed *protocol.Error
	if !errors.As(last.err, &closed) || closed.Code != "SIDECAR_PIPE_CLOSED" || last.record != nil {
		t.Fatalf("unexpected output after terminal record: %#v, %v", last.record, last.err)
	}
}

func TestReadyHealthShutdownAndParentEOF(t *testing.T) {
	for _, eof := range []bool{false, true} {
		t.Run(fmt.Sprint("eof=", eof), func(t *testing.T) {
			h := launchRuntime(t, goodPrepare, shortOptions())
			h.send(t, startPayload(t))
			port := h.ready(t)
			transport := &http.Transport{DisableKeepAlives: true}
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: transport, Timeout: time.Second}
			response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/health", port))
			if err != nil {
				t.Fatal(err)
			}
			var health map[string]any
			err = json.NewDecoder(response.Body).Decode(&health)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 || !reflect.DeepEqual(health, map[string]any{"status": "ok", "protocol_version": float64(1), "app_version": "0.1.2"}) {
				t.Fatalf("health = %#v, status %d, %v", health, response.StatusCode, err)
			}
			if eof {
				h.input.Close()
			} else {
				h.send(t, map[string]any{"command": "shutdown", "protocol_version": 1})
			}
			h.done(t, 0)
			assertPortReleased(t, port)
		})
	}
}

func TestStartupInputFailuresAreBoundedAndSanitized(t *testing.T) {
	cases := []struct {
		name, raw, code string
		close           bool
	}{
		{"silent", "", "SIDECAR_START_TIMEOUT", false},
		{"partial", "{", "SIDECAR_START_TIMEOUT", false},
		{"eof", "", "SIDECAR_PIPE_CLOSED", true},
		{"truncated", "{", "SIDECAR_RECORD_TRUNCATED", true},
		{"json", "{broken\n", "SIDECAR_RECORD_INVALID_JSON", false},
		{"utf8", "\xff\n", "SIDECAR_RECORD_INVALID_UTF8", false},
		{"shape", "[]\n", "SIDECAR_RECORD_INVALID_SHAPE", false},
		{"fields", "{}\n", "SIDECAR_RECORD_INVALID_FIELDS", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var prepared atomic.Bool
			h := launchRuntime(t, func(ctx context.Context, start protocol.StartRecord) (Prepared, error) {
				prepared.Store(true)
				return goodPrepare(ctx, start)
			}, shortOptions())
			if tc.raw != "" {
				if _, err := io.WriteString(h.input, tc.raw); err != nil {
					t.Fatal(err)
				}
			}
			if tc.close {
				h.input.Close()
			}
			h.expectError(t, tc.code)
			h.done(t, 3)
			if prepared.Load() {
				t.Fatal("invalid startup reached initialization")
			}
		})
	}
}

func TestDuplicateOrMalformedRuntimeRecordStopsServer(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprint("duplicate=", duplicate), func(t *testing.T) {
			h := launchRuntime(t, goodPrepare, shortOptions())
			h.send(t, startPayload(t))
			port := h.ready(t)
			code := "SIDECAR_RECORD_INVALID_JSON"
			if duplicate {
				h.send(t, startPayload(t))
				code = "SIDECAR_RECORD_INVALID_FIELDS"
			} else if _, err := io.WriteString(h.input, "{broken\n"); err != nil {
				t.Fatal(err)
			}
			h.expectError(t, code)
			h.done(t, 3)
			assertPortReleased(t, port)
		})
	}
}

func TestPreparationAndHandlerFailures(t *testing.T) {
	cases := []struct {
		name    string
		prepare Prepare
		code    string
	}{
		{"nil prepare", nil, "SIDECAR_STARTUP_FAILED"},
		{"error", func(context.Context, protocol.StartRecord) (Prepared, error) {
			return Prepared{}, errors.New(privateCause)
		}, "SIDECAR_STARTUP_FAILED"},
		{"wrapped allowed", func(context.Context, protocol.StartRecord) (Prepared, error) {
			return Prepared{}, fmt.Errorf("%s: %w", privateCause, &StartupError{Code: "DATA_HOME_CHANNEL_MISMATCH"})
		}, "DATA_HOME_CHANNEL_MISMATCH"},
		{"unknown code", func(context.Context, protocol.StartRecord) (Prepared, error) {
			return Prepared{}, &StartupError{Code: privateCause}
		}, "SIDECAR_STARTUP_FAILED"},
		{"panic", func(context.Context, protocol.StartRecord) (Prepared, error) { panic(privateCause) }, "SIDECAR_STARTUP_FAILED"},
		{"nil factory", func(context.Context, protocol.StartRecord) (Prepared, error) {
			return Prepared{AppVersion: "0.1.2"}, nil
		}, "SIDECAR_STARTUP_FAILED"},
		{"nil handler", preparedHandler(func(int) (http.Handler, error) { return nil, nil }), "SIDECAR_STARTUP_FAILED"},
		{"handler error", preparedHandler(func(int) (http.Handler, error) { return nil, errors.New(privateCause) }), "SIDECAR_STARTUP_FAILED"},
		{"handler panic", preparedHandler(func(int) (http.Handler, error) { panic(privateCause) }), "SIDECAR_STARTUP_FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := launchRuntime(t, tc.prepare, shortOptions())
			h.send(t, startPayload(t))
			h.expectError(t, tc.code)
			h.done(t, 4)
		})
	}
}

func preparedHandler(handler func(int) (http.Handler, error)) Prepare {
	return func(context.Context, protocol.StartRecord) (Prepared, error) {
		return Prepared{AppVersion: "0.1.2", Handler: handler}, nil
	}
}

func TestPreparationTimeoutCannotPublishLateReadiness(t *testing.T) {
	release, finished := make(chan struct{}), make(chan struct{})
	var listenCalled atomic.Bool
	o := shortOptions()
	listen := o.listen
	o.listen = func(ctx context.Context) (net.Listener, error) { listenCalled.Store(true); return listen(ctx) }
	h := launchRuntime(t, func(ctx context.Context, start protocol.StartRecord) (Prepared, error) {
		defer close(finished)
		<-ctx.Done()
		<-release
		return goodPrepare(ctx, start)
	}, o)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	h.send(t, startPayload(t))
	h.expectError(t, "SIDECAR_INITIALIZATION_TIMEOUT")
	h.done(t, 4)
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("late initialization fixture did not exit")
	}
	if listenCalled.Load() {
		t.Fatal("expired initialization reached listener creation")
	}
}

func TestPreparationDeadlineWinsCooperativeCompletion(t *testing.T) {
	// Both the callback result and ctx.Done can become ready together. A callback
	// returning its cancellation error must not change the public timeout code.
	for i := 0; i < 25; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			finished := make(chan struct{})
			var listened atomic.Bool
			o := shortOptions()
			o.initialize = 5 * time.Millisecond
			listen := o.listen
			o.listen = func(ctx context.Context) (net.Listener, error) {
				listened.Store(true)
				return listen(ctx)
			}
			h := launchRuntime(t, func(ctx context.Context, _ protocol.StartRecord) (Prepared, error) {
				defer close(finished)
				<-ctx.Done()
				return Prepared{}, ctx.Err()
			}, o)
			h.send(t, startPayload(t))
			h.expectError(t, "SIDECAR_INITIALIZATION_TIMEOUT")
			h.done(t, 4)
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("cooperative initializer survived cancellation")
			}
			if listened.Load() {
				t.Fatal("expired preparation reached listener creation")
			}
		})
	}
}

func TestHandlerConstructionTimeoutClosesListener(t *testing.T) {
	release, finished := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	var port atomic.Int32
	h := launchRuntime(t, preparedHandler(func(value int) (http.Handler, error) {
		defer close(finished)
		port.Store(int32(value))
		<-release
		return http.NotFoundHandler(), nil
	}), shortOptions())
	h.send(t, startPayload(t))
	h.expectError(t, "SIDECAR_STARTUP_FAILED")
	h.done(t, 4)
	if port.Load() == 0 {
		t.Fatal("handler fixture never started")
	}
	assertPortReleased(t, int(port.Load()))
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("handler fixture did not exit")
	}
}

type addressListener struct {
	address net.Addr
	closed  atomic.Bool
}

func (l *addressListener) Accept() (net.Conn, error) { return nil, errors.New(privateCause) }
func (l *addressListener) Close() error              { l.closed.Store(true); return nil }
func (l *addressListener) Addr() net.Addr            { return l.address }

func TestBindAndAddressFailures(t *testing.T) {
	for _, address := range []net.Addr{nil, &net.UnixAddr{Name: "synthetic", Net: "unix"}, &net.TCPAddr{IP: net.IPv4zero, Port: 1234}, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 65536}} {
		t.Run(fmt.Sprint(address), func(t *testing.T) {
			o := shortOptions()
			listener := &addressListener{address: address}
			o.listen = func(context.Context) (net.Listener, error) {
				if address == nil {
					return nil, errors.New(privateCause)
				}
				return listener, nil
			}
			h := launchRuntime(t, goodPrepare, o)
			h.send(t, startPayload(t))
			h.expectError(t, "SIDECAR_LISTENER_FAILED")
			h.done(t, 5)
			if address != nil && !listener.closed.Load() {
				t.Fatal("invalid listener not closed")
			}
		})
	}
}

func TestServeFailureInterruptsBlockedInput(t *testing.T) {
	for _, panicServe := range []bool{false, true} {
		t.Run(fmt.Sprint("panic=", panicServe), func(t *testing.T) {
			release := make(chan struct{})
			o := shortOptions()
			o.serve = func(*http.Server, net.Listener) error {
				<-release
				if panicServe {
					panic(privateCause)
				}
				return errors.New(privateCause)
			}
			h := launchRuntime(t, goodPrepare, o)
			h.send(t, startPayload(t))
			port := h.ready(t)
			close(release)
			h.expectError(t, "SIDECAR_SERVER_FAILED")
			h.done(t, 7) // host input stays open, exercising cancellation of the owned read.
			assertPortReleased(t, port)
		})
	}
}

type failingOutput struct {
	mode                    string
	writes, flushes, closes atomic.Int32
	closed                  chan struct{}
	once                    sync.Once
}

func (w *failingOutput) Write(p []byte) (int, error) {
	w.writes.Add(1)
	switch w.mode {
	case "blocked":
		<-w.closed
		return 0, errors.New(privateCause)
	case "short":
		return len(p) - 1, nil
	case "write":
		return 0, errors.New(privateCause)
	default:
		return len(p), nil
	}
}
func (w *failingOutput) Flush() error { w.flushes.Add(1); return errors.New(privateCause) }
func (w *failingOutput) Close() error {
	w.closes.Add(1)
	w.once.Do(func() { close(w.closed) })
	return nil
}

func TestReadinessOutputFailureDoesNotWriteAnotherRecord(t *testing.T) {
	for _, mode := range []string{"blocked", "short", "write", "flush"} {
		t.Run(mode, func(t *testing.T) {
			in, host, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { in.Close(); host.Close() })
			out := &failingOutput{mode: mode, closed: make(chan struct{})}
			t.Cleanup(func() { out.Close() })
			result := make(chan int, 1)
			go func() { result <- run(in, out, goodPrepare, shortOptions()) }()
			if err := protocol.WriteRecord(host, startPayload(t)); err != nil {
				t.Fatal(err)
			}
			select {
			case status := <-result:
				if status != 6 {
					t.Fatalf("status=%d, want6", status)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("output failure prevented bounded exit")
			}
			if out.writes.Load() != 1 || out.closes.Load() < 1 {
				t.Fatal("failed output was reused or not closed")
			}
			wantFlush := int32(0)
			if mode == "flush" {
				wantFlush = 1
			}
			if out.flushes.Load() != wantFlush {
				t.Fatal("unexpected flush after output failure")
			}
		})
	}
}

func TestEmitClosesBlockedPipe(t *testing.T) {
	reader, writer := io.Pipe()
	t.Cleanup(func() { reader.Close(); writer.Close() })
	started := time.Now()
	if emit(writer, protocol.ErrorRecord("SIDECAR_STARTUP_FAILED"), 30*time.Millisecond) {
		t.Fatal("unread synchronous pipe accepted output")
	}
	if time.Since(started) > time.Second {
		t.Fatal("blocked pipe exceeded output deadline")
	}
	if _, err := writer.Write([]byte("must not write")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("timed-out output pipe remained writable")
	}
}

type errorAfterReader struct{ *bytes.Reader }

func (r errorAfterReader) Read(p []byte) (int, error) {
	if r.Len() == 0 {
		return 0, errors.New(privateCause)
	}
	return r.Reader.Read(p)
}

type memoryOutput struct {
	bytes.Buffer
	closed bool
}

func (w *memoryOutput) Close() error { w.closed = true; return nil }

func TestUnderlyingInputErrorsAreContentFree(t *testing.T) {
	for _, afterReady := range []bool{false, true} {
		t.Run(fmt.Sprint("after-ready=", afterReady), func(t *testing.T) {
			var raw []byte
			if afterReady {
				var err error
				raw, err = protocol.EncodeRecord(startPayload(t))
				if err != nil {
					t.Fatal(err)
				}
			}
			input := io.NopCloser(errorAfterReader{bytes.NewReader(raw)})
			output := &memoryOutput{}
			if status := run(input, output, goodPrepare, shortOptions()); status != 3 {
				t.Fatalf("status = %d, want 3", status)
			}
			if !output.closed || bytes.Contains(output.Bytes(), []byte(privateCause)) {
				t.Fatal("input failure disclosed cause or did not close output")
			}
			reader := bytes.NewReader(output.Bytes())
			if afterReady {
				record, err := protocol.ReadRecord(reader)
				if err != nil || record["event"] != "ready" {
					// The next input read now runs during preparation. A ready
					// record is optional for this deliberately racing memory
					// reader; the terminal sanitized error is still mandatory.
					reader = bytes.NewReader(output.Bytes())
				}
			}
			record, err := protocol.ReadRecord(reader)
			code := "SIDECAR_STARTUP_READ_FAILED"
			if afterReady {
				code = "SIDECAR_RUNTIME_READ_FAILED"
			}
			if err != nil || len(record) != 3 || record["error_code"] != code || record["event"] != "error" || record["protocol_version"] != json.Number("1") {
				t.Fatalf("unexpected error record: %#v, %v", record, err)
			}
			if reader.Len() != 0 {
				t.Fatal("extra output after failure")
			}
		})
	}
}

func TestParentControlCancelsPreparationAndHandlerBeforeReadiness(t *testing.T) {
	for _, phase := range []string{"prepare", "handler"} {
		for _, control := range []string{"eof", "shutdown", "malformed", "duplicate"} {
			t.Run(phase+"/"+control, func(t *testing.T) {
				entered, finished := make(chan struct{}), make(chan struct{})
				var listened atomic.Bool
				var port atomic.Int32
				o := shortOptions()
				o.initialize = time.Second
				listen := o.listen
				o.listen = func(ctx context.Context) (net.Listener, error) {
					listened.Store(true)
					return listen(ctx)
				}
				h := launchRuntime(t, func(ctx context.Context, start protocol.StartRecord) (Prepared, error) {
					wait := func() {
						close(entered)
						<-ctx.Done()
						close(finished)
					}
					if phase == "prepare" {
						wait()
						return Prepared{}, ctx.Err()
					}
					return Prepared{AppVersion: "0.1.2", Handler: func(value int) (http.Handler, error) {
						port.Store(int32(value))
						wait()
						return nil, ctx.Err()
					}}, nil
				}, o)
				h.send(t, startPayload(t))
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("preparation fixture not entered")
				}
				switch control {
				case "eof":
					h.input.Close()
				case "shutdown":
					h.send(t, map[string]any{"command": "shutdown", "protocol_version": 1})
				case "malformed":
					if _, err := io.WriteString(h.input, "{broken\n"); err != nil {
						t.Fatal(err)
					}
				case "duplicate":
					h.send(t, startPayload(t))
				}
				status := 0
				if control == "malformed" {
					h.expectError(t, "SIDECAR_RECORD_INVALID_JSON")
					status = 3
				}
				if control == "duplicate" {
					h.expectError(t, "SIDECAR_RECORD_INVALID_FIELDS")
					status = 3
				}
				h.done(t, status) // No readiness before the terminal pipe close.
				select {
				case <-finished:
				case <-time.After(time.Second):
					t.Fatal("cooperative preparation was not cancelled")
				}
				if phase == "prepare" && listened.Load() {
					t.Fatal("cancelled preparation opened a listener")
				}
				if phase == "handler" {
					if port.Load() == 0 {
						t.Fatal("handler had no listener")
					}
					assertPortReleased(t, int(port.Load()))
				}
			})
		}
	}
}

func TestShutdownWithActiveAndHeaderlessConnectionsFitsHostBudget(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprint("active=", active), func(t *testing.T) {
			entered, cancelled := make(chan struct{}), make(chan struct{})
			accepted := make(chan struct{})
			var acceptedOnce sync.Once
			o := shortOptions()
			o.serve = func(server *http.Server, listener net.Listener) error {
				server.ConnState = func(_ net.Conn, state http.ConnState) {
					if state == http.StateNew {
						acceptedOnce.Do(func() { close(accepted) })
					}
				}
				return server.Serve(listener)
			}
			h := launchRuntime(t, preparedHandler(func(int) (http.Handler, error) {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					close(entered)
					<-r.Context().Done()
					close(cancelled)
				}), nil
			}), o)
			h.send(t, startPayload(t))
			port := h.ready(t)
			connection, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { connection.Close() })
			select {
			case <-accepted:
			case <-time.After(time.Second):
				t.Fatal("connection not accepted")
			}
			if active {
				if _, err := fmt.Fprintf(connection, "GET / HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\n", port); err != nil {
					t.Fatal(err)
				}
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("handler did not start")
				}
			}
			started := time.Now()
			h.send(t, map[string]any{"command": "shutdown", "protocol_version": 1})
			h.done(t, 0)
			if time.Since(started) >= 2*time.Second {
				t.Fatal("shutdown exhausted unchanged 3-second host budget")
			}
			if active {
				select {
				case <-cancelled:
				case <-time.After(time.Second):
					t.Fatal("handler context not cancelled")
				}
			}
			connection.SetReadDeadline(time.Now().Add(time.Second))
			_, err = io.Copy(io.Discard, connection)
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("connection survived shutdown")
			}
			assertPortReleased(t, port)
		})
	}
}

func TestShutdownForceClosesConnectionWithUncooperativeHandler(t *testing.T) {
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	h := launchRuntime(t, preparedHandler(func(int) (http.Handler, error) {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			defer close(finished)
			close(entered)
			<-release
		}), nil
	}), shortOptions())
	h.send(t, startPayload(t))
	port := h.ready(t)
	connection, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	if _, err := fmt.Fprintf(connection, "GET / HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n\r\n", port); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	started := time.Now()
	h.send(t, map[string]any{"command": "shutdown", "protocol_version": 1})
	h.done(t, 0)
	if time.Since(started) >= 2*time.Second {
		t.Fatal("force-close exhausted host budget")
	}
	connection.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := io.Copy(io.Discard, connection); err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			t.Fatal("uncooperative handler retained connection")
		}
	}
	assertPortReleased(t, port)
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("handler fixture did not exit")
	}
}

func TestRunDefaultEntryClosesOutputOnInvalidStartup(t *testing.T) {
	output := &memoryOutput{}
	if status := Run(io.NopCloser(strings.NewReader("{}\n")), output, goodPrepare); status != 3 {
		t.Fatalf("status = %d, want 3", status)
	}
	if !output.closed {
		t.Fatal("Run left owned output open")
	}
	record, err := protocol.ReadRecord(bytes.NewReader(output.Bytes()))
	if err != nil || record["error_code"] != "SIDECAR_RECORD_INVALID_FIELDS" {
		t.Fatal("default entry changed protocol failure")
	}
}

func assertPortReleased(t *testing.T, port int) {
	t.Helper()
	connection, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err == nil {
		connection.Close()
		t.Fatal("listener survived runtime exit")
	}
}

func TestErrorCodeAllowlistsNeverExposeUnknownCause(t *testing.T) {
	for _, code := range []string{privateCause, "SIDECAR_STARTUP_FAILED\nsecret", ""} {
		if got := protocolCode(fmt.Errorf("%s: %w", privateCause, &protocol.Error{Code: code}), "fixed-fallback"); got != "fixed-fallback" {
			t.Fatal("unrecognized protocol code escaped")
		}
		if got := initializationCode(fmt.Errorf("%s: %w", privateCause, &StartupError{Code: code})); got != "SIDECAR_STARTUP_FAILED" {
			t.Fatal("unrecognized initialization code escaped")
		}
	}
	if got := protocolCode(errors.New(privateCause), "fixed-fallback"); got != "fixed-fallback" {
		t.Fatal("raw error escaped")
	}
}

func TestPreparationCodesSurviveInitializationBoundary(t *testing.T) {
	for _, code := range []string{
		"APPLICATION_PATH_REJECTED", "DATA_HOME_OWNERSHIP_REQUIRED", "APPLICATION_ORPHANED_SQLITE_FILES",
		"APPLICATION_DATABASE_CREATE_FAILED", "APPLICATION_DATABASE_UNAVAILABLE", "APPLICATION_CLEANUP_FAILED",
		"APPLICATION_UPGRADE_LOCK_FAILED", "APPLICATION_ROLLBACK_FAILED", "APPLICATION_SCHEMA_READ_FAILED",
		"APPLICATION_SCHEMA_TOO_NEW", "APPLICATION_SCHEMA_UPGRADE_FAILED", "APPLICATION_COMMIT_UNCONFIRMED",
		"APPLICATION_BACKUP_PATH_FAILED", "APPLICATION_BACKUP_NAME_FAILED", "APPLICATION_BACKUP_CREATE_FAILED",
		"APPLICATION_BACKUP_CLEANUP_FAILED", "APPLICATION_BACKUP_VERIFY_FAILED", "APPLICATION_BACKUP_METADATA_FAILED",
		"APPLICATION_BACKUP_SOURCE_FAILED", "APPLICATION_BACKUP_COPY_FAILED",
	} {
		if got := initializationCode(&StartupError{Code: code}); got != code {
			t.Fatalf("lost preparation code %s", code)
		}
	}
}
