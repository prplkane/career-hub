package runtime

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"careerhub.local/backend/internal/sidecar/protocol"
)

// Prepared is supplied only by the trusted composition root, never by HTTP.
type Prepared struct {
	AppVersion string
	Handler    func(port int) (http.Handler, error)
	// Release, when set, runs once after the server has stopped serving, to
	// withdraw anything Handler published for this process (for example the
	// owner-only agent connection file). It must be safe to call after a
	// partially failed Handler.
	Release func()
}

type Prepare func(context.Context, protocol.StartRecord) (Prepared, error)

// StartupError carries a fixed category. Unknown codes are not emitted.
type StartupError struct{ Code string }

func (*StartupError) Error() string { return "sidecar initialization failed" }

type options struct {
	startup, initialize, output, shutdown time.Duration
	listen                                func(context.Context) (net.Listener, error)
	serve                                 func(*http.Server, net.Listener) error
}

func defaults() options {
	return options{
		startup: 10 * time.Second, initialize: 10 * time.Second,
		output: 2 * time.Second, shutdown: 2 * time.Second,
		listen: listenLoopback,
		serve:  func(s *http.Server, l net.Listener) error { return s.Serve(l) },
	}
}

// Run owns and closes both pipes. It is a dedicated process entry point: the
// caller must exit afterwards, not reuse it as an in-process background service.
// Pipe closure is attempted once within a bounded wait; native I/O cancellation
// may still be pending. The desktop supervisor remains the final reap bound.
func Run(input io.ReadCloser, output io.WriteCloser, prepare Prepare) int {
	return run(input, output, prepare, defaults())
}

// RunWithLoopbackPort is selected only by trusted process arguments, not stdin
// or HTTP. Protocol v1 continues to require port=0. The listener remains IPv4
// loopback-only and retains the platform's exclusive-address policy.
func RunWithLoopbackPort(input io.ReadCloser, output io.WriteCloser, prepare Prepare, port int) int {
	if port < 1 || port > 65535 {
		return 2
	}
	o := defaults()
	o.listen = func(ctx context.Context) (net.Listener, error) { return listenLoopbackPort(ctx, port) }
	return run(input, output, prepare, o)
}

type readResult struct {
	record map[string]any
	err    error
}

type serveResult struct {
	err       error
	premature bool
}

func (r serveResult) failed() bool {
	return r.premature || r.err != nil && !errors.Is(r.err, http.ErrServerClosed)
}

func readOne(input io.Reader) <-chan readResult {
	result := make(chan readResult, 1)
	go func() {
		record, err := protocol.ReadRecord(input)
		result <- readResult{record, err}
	}()
	return result
}

func emit(output io.WriteCloser, payload map[string]any, timeout time.Duration) bool {
	result := make(chan error, 1)
	go func() { result <- protocol.WriteRecord(output, payload) }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-result:
		return err == nil
	case <-timer.C:
		_ = output.Close()
		return false
	}
}

func run(input io.ReadCloser, output io.WriteCloser, prepare Prepare, o options) int {
	if input == nil || output == nil {
		return 3
	}
	input, output = ownInput(input), ownOutput(output)
	defer input.Close()
	defer output.Close()
	fail := func(code string, status int) int {
		emit(output, protocol.ErrorRecord(code), o.output)
		return status
	}
	startTimer := time.NewTimer(o.startup)
	defer startTimer.Stop()
	var first readResult
	select {
	case first = <-readOne(input):
	case <-startTimer.C:
		return fail("SIDECAR_START_TIMEOUT", 3)
	}
	if first.err != nil {
		return fail(protocolCode(first.err, "SIDECAR_STARTUP_READ_FAILED"), 3)
	}
	start, err := protocol.ParseStart(first.record, protocol.PathOptions{})
	if err != nil {
		return fail(protocolCode(err, "SIDECAR_INPUT_UNSUPPORTED"), 3)
	}
	if prepare == nil {
		return fail("SIDECAR_STARTUP_FAILED", 4)
	}
	// Keep exactly one reader for the next control record throughout startup and
	// serving. Unlike the source's post-ready reader, parent loss cancels a slow
	// initializer too. A completed read is queued before cancellation so every
	// cancellation branch can preserve its exact fixed outcome.
	parentContext, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()
	control := make(chan readResult, 1)
	go func() {
		record, err := protocol.ReadRecord(input)
		control <- readResult{record: record, err: err}
		parentCancel()
	}()
	stopBeforeReady := func(next readResult) int {
		parentCancel()
		if code := controlCode(next); code != "" {
			return fail(code, 3)
		}
		return 0
	}
	pendingStop := func() (int, bool) {
		select {
		case next := <-control:
			return stopBeforeReady(next), true
		default:
			return 0, false
		}
	}
	if status, stopped := pendingStop(); stopped {
		return status
	}
	initContext, initCancel := context.WithTimeout(parentContext, o.initialize)
	defer initCancel()
	type initialization struct {
		prepared Prepared
		err      error
	}
	initialized := make(chan initialization, 1)
	go func() {
		result := initialization{err: errors.New("sidecar initialization failed")}
		defer func() {
			if recover() != nil {
				result = initialization{err: errors.New("sidecar initialization failed")}
			}
			initialized <- result
		}()
		result.prepared, result.err = prepare(initContext, start)
	}()
	var prepared Prepared
	select {
	case result := <-initialized:
		if status, stopped := pendingStop(); stopped {
			return status
		}
		if initContext.Err() != nil {
			if status, stopped := pendingStop(); stopped {
				return status
			}
			return fail("SIDECAR_INITIALIZATION_TIMEOUT", 4)
		}
		if result.err != nil {
			return fail(initializationCode(result.err), 4)
		}
		prepared = result.prepared
	case next := <-control:
		return stopBeforeReady(next)
	case <-initContext.Done():
		if status, stopped := pendingStop(); stopped {
			return status
		}
		return fail("SIDECAR_INITIALIZATION_TIMEOUT", 4)
	}
	if prepared.Handler == nil || prepared.AppVersion == "" {
		return fail("SIDECAR_STARTUP_FAILED", 4)
	}
	if initContext.Err() != nil {
		if status, stopped := pendingStop(); stopped {
			return status
		}
		return fail("SIDECAR_INITIALIZATION_TIMEOUT", 4)
	}
	listener, err := o.listen(initContext)
	if status, stopped := pendingStop(); stopped {
		if listener != nil {
			_ = listener.Close()
		}
		return status
	}
	if err != nil || listener == nil {
		return fail("SIDECAR_LISTENER_FAILED", 5)
	}
	defer listener.Close()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !address.IP.Equal(net.IPv4(127, 0, 0, 1)) || address.Port < 1 || address.Port > 65535 {
		return fail("SIDECAR_LISTENER_FAILED", 5)
	}
	handler, err := buildHandler(initContext, prepared.Handler, address.Port)
	if prepared.Release != nil {
		// Registered before any early return so a stop requested during
		// startup still withdraws what Handler published.
		defer prepared.Release()
	}
	if status, stopped := pendingStop(); stopped {
		return status
	}
	if err != nil || handler == nil {
		return fail("SIDECAR_STARTUP_FAILED", 4)
	}
	serverContext, serverCancel := context.WithCancel(parentContext)
	defer serverCancel()
	server := &http.Server{
		Handler: handler, MaxHeaderBytes: 16 * 1024,
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 15 * time.Second,
		ErrorLog:    log.New(io.Discard, "", 0),
		BaseContext: func(net.Listener) context.Context { return serverContext },
	}
	if status, stopped := pendingStop(); stopped {
		return status
	}
	var stopping atomic.Bool
	served := make(chan serveResult, 1)
	go func() {
		var serveError error
		defer func() {
			if recover() != nil {
				serveError = errors.New("sidecar server failed")
			}
			served <- serveResult{serveError, !stopping.Load()}
		}()
		serveError = o.serve(server, listener)
	}()
	status, serverStopped, outputHealthy := 0, false, true
	if !emit(output, protocol.ReadyRecord(prepared.AppVersion, address.Port, start.Channel), o.output) {
		status, outputHealthy = 6, false
	} else {
		select {
		case <-served:
			status, serverStopped = 7, true
		case next := <-control:
			if code := controlCode(next); code != "" {
				outputHealthy = emit(output, protocol.ErrorRecord(code), o.output)
				status = 3
			}
		}
	}
	stopping.Store(true)
	serverCancel()
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), o.shutdown)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		// Close stalled connections before the host's termination deadline. The
		// maintainer material owner may still be cleaning up an interrupted write;
		// its claim remains process-owned until exit, and recovery uses receipts.
		if server.Close() != nil {
			status = 7
		}
	}
	if !serverStopped {
		// Shutdown may exhaust its grace while Serve has already returned after
		// the listener closed. Inspect that result before selecting a spent timer.
		select {
		case result := <-served:
			if result.failed() {
				status = 7
			}
		default:
			select {
			case result := <-served:
				if result.failed() {
					status = 7
				}
			case <-time.After(100 * time.Millisecond):
				status = 7
			}
		}
	}
	if status == 7 && outputHealthy {
		emit(output, protocol.ErrorRecord("SIDECAR_SERVER_FAILED"), o.output)
	}
	return status
}

func controlCode(next readResult) string {
	if next.err != nil {
		code := protocolCode(next.err, "SIDECAR_RUNTIME_READ_FAILED")
		if code != "SIDECAR_PIPE_CLOSED" {
			return code
		}
	} else if err := protocol.ParseShutdown(next.record); err != nil {
		return protocolCode(err, "SIDECAR_INPUT_UNSUPPORTED")
	}
	return ""
}

func buildHandler(ctx context.Context, factory func(int) (http.Handler, error), port int) (http.Handler, error) {
	type result struct {
		handler http.Handler
		err     error
	}
	completed := make(chan result, 1)
	go func() {
		value := result{err: errors.New("sidecar handler failed")}
		defer func() {
			if recover() != nil {
				value = result{err: errors.New("sidecar handler failed")}
			}
			completed <- value
		}()
		value.handler, value.err = factory(port)
	}()
	select {
	case value := <-completed:
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return value.handler, value.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func protocolCode(err error, fallback string) string {
	var p *protocol.Error
	if errors.As(err, &p) {
		switch p.Code {
		case "SIDECAR_PIPE_CLOSED", "SIDECAR_RECORD_TOO_LARGE", "SIDECAR_RECORD_TRUNCATED",
			"SIDECAR_RECORD_INVALID_JSON", "SIDECAR_RECORD_INVALID_UTF8", "SIDECAR_RECORD_INVALID_SHAPE",
			"SIDECAR_RECORD_INVALID_FIELDS", "SIDECAR_PROTOCOL_UNSUPPORTED",
			"SIDECAR_COMMAND_UNSUPPORTED", "SIDECAR_START_INVALID_PORT", "SIDECAR_START_INVALID_CHANNEL", "SIDECAR_START_INVALID_TOKEN",
			"SIDECAR_START_INVALID_PATH":
			return p.Code
		}
	}
	return fallback
}

func initializationCode(err error) string {
	var e *StartupError
	if errors.As(err, &e) {
		switch e.Code {
		case "SIDECAR_START_INVALID_PORT", "SIDECAR_START_INVALID_CHANNEL", "SIDECAR_START_INVALID_TOKEN", "SIDECAR_START_INVALID_PATH",
			"SIDECAR_PATH_BOUNDARY_INVALID", "SIDECAR_RESOURCE_INVALID", "SIDECAR_SCHEMA_INITIALIZATION_FAILED",
			"DATA_HOME_UNAVAILABLE", "INVALID_DATA_HOME", "DATA_HOME_NEWER_THAN_APP", "DATA_HOME_MIGRATION_REQUIRED",
			"DATA_HOME_RECOVERY_REQUIRED", "DATA_HOME_CHANNEL_MISMATCH", "DATA_HOME_INPUT_UNSUPPORTED", "DATA_HOME_IDENTITY_CHANGED",
			"DATA_HOME_UNWRITABLE", "DATA_HOME_UNREADABLE", "PARTIAL_DATA_HOME", "DEFAULT_CONFLICT",
			"DATA_HOME_ID_UNAVAILABLE", "DATA_HOME_STARTUP_CANCELLED", "DATA_HOME_BOOTSTRAP_UNSUPPORTED",
			"DATA_HOME_IN_USE", "DATA_HOME_OWNERSHIP_UNAVAILABLE", "DATA_HOME_OWNERSHIP_UNSUPPORTED",
			"ACL_SETUP_FAILED", "ACL_TREE_LIMIT", "ACL_CONTEXT_CANCELED", "ACL_PLATFORM_UNSUPPORTED", "DATA_HOME_PERMISSIONS_UNSAFE",
			"APPLICATION_PATH_REJECTED", "DATA_HOME_OWNERSHIP_REQUIRED", "APPLICATION_ORPHANED_SQLITE_FILES",
			"APPLICATION_DATABASE_CREATE_FAILED", "APPLICATION_DATABASE_UNAVAILABLE", "APPLICATION_CLEANUP_FAILED",
			"APPLICATION_UPGRADE_LOCK_FAILED", "APPLICATION_ROLLBACK_FAILED", "APPLICATION_SCHEMA_READ_FAILED",
			"APPLICATION_SCHEMA_TOO_NEW", "APPLICATION_SCHEMA_UPGRADE_FAILED", "APPLICATION_COMMIT_UNCONFIRMED",
			"APPLICATION_BACKUP_PATH_FAILED", "APPLICATION_BACKUP_NAME_FAILED", "APPLICATION_BACKUP_CREATE_FAILED",
			"APPLICATION_BACKUP_CLEANUP_FAILED", "APPLICATION_BACKUP_VERIFY_FAILED", "APPLICATION_BACKUP_METADATA_FAILED",
			"APPLICATION_BACKUP_SOURCE_FAILED", "APPLICATION_BACKUP_COPY_FAILED":
			return e.Code
		}
	}
	return "SIDECAR_STARTUP_FAILED"
}
