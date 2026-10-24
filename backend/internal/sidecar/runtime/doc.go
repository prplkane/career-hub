// Package runtime owns one sidecar process and its loopback HTTP server. It
// does not discover a profile; the host supplies the Data Home.
//
// The executable reads one v1 startup record on stdin (explicit absolute
// resource and data roots, port 0 and a valid channel), emits a readiness
// record on stdout and stops on a shutdown record or stdin EOF. Stdout
// carries bounded protocol records only, never logs, paths or capabilities.
// Invalid arguments exit 2 without reading input. The control record is read
// throughout preparation and serving: EOF or a valid shutdown cancels
// preparation and exits 0, invalid control exits 3. --sidecar serves the
// read-only routes against an initialized Home; --sidecar-bootstrap first
// initializes a Windows Home, prepares the application schema with a verified
// recovery copy, and then also serves the owned material routes. Both modes
// take the Windows process ownership claim, which lasts until process exit.
//
// Lifecycle bounds: first startup record 10 s; initialization 10 s; each
// protocol write 2 s; each pipe close at most 250 ms; graceful HTTP shutdown
// 2 s with a 100 ms Serve join. HTTP header/read/write/idle limits are
// 2/5/10/15 s with a 16 KiB header limit; material operations use a
// 20-second context and a 25-second write deadline. The listener binds only
// IPv4 127.0.0.1:0, and Windows sets SO_EXCLUSIVEADDRUSE before bind.
//
// Cancellation is cooperative: contexts and deadlines do not preempt
// synchronous filesystem or native calls, and process exit is the final
// stop. Run must be followed by process exit; it is not a reusable service,
// and a publication interrupted by shutdown requires an exact retry after
// the process has exited.
package runtime
