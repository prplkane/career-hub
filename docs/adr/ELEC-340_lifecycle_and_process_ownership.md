# ELEC-340: One Desktop Owner With Native Close Semantics

- **Status:** Accepted and implemented in the desktop foundation; target-host
  lifecycle acceptance remains pending
- **Decision date:** 2026-08-28
- **Evidence tickets:** `ELEC-320`, `ELEC-330`, `ELEC-340`, `ELEC-350`

## Context

Career Hub's Python service owns SQLite and private local state. Starting two
sidecars for one channel/data identity could create competing writers, while
terminating an unrelated Python process by name would be unsafe. Electron also
has different native close conventions on Windows and macOS.

Electron's documented lifecycle makes `before-quit` cancellable, emits
`activate` when a macOS application is reopened from the Dock, and routes a
successful second-instance lock request to the original process. Those events
are the application boundary; renderer state never owns process lifecycle.

## Decision

1. The Electron main process acquires one channel-scoped single-instance lock
   before it creates a window or starts Python. A second launch's arguments and
   working directory are ignored; it can only focus or recreate the existing
   window.
2. Main retains the exact `ChildProcess` handle. It starts only the packaged
   executable resolved from the reviewed resource layout, sends paths through
   bounded stdin protocol records, and never uses a process-name kill.
3. On Windows, closing the last window requests app quit. On macOS, closing the
   last window follows native convention: the application and its one sidecar
   remain owned, and `activate` creates one fresh window against the existing
   origin. There is no tray, autostart, scheduled background work, or automatic
   network retry.
4. Explicit Quit enters a three-state controller: running, stopping, then
   finalizing. `before-quit` is prevented while the versioned sidecar shutdown
   runs. The sidecar has a three-second graceful bound plus a one-second exact-
   child termination bound. Electron finalizes Quit only after that exact child
   is confirmed exited. If termination times out, the supervisor retains the
   child handle in `stopping`, refuses restart, keeps Electron alive, and allows
   a later explicit Quit retry after the child exits. The host reports a
   sanitized error and restores/focuses its window so the retained owner does
   not become an invisible background process.
5. Windows shutdown/logout may bypass `before-quit`. The inherited stdin pipe
   is therefore also a required ownership signal: parent loss makes the sidecar
   stop itself.
6. An unexpected sidecar exit clears the retained child and origin before the
   UI receives `DESKTOP_SIDECAR_EXITED`. Restart is never automatic. The user
   may explicitly retry only from the trusted loading page, which starts one
   fresh child after ownership is confirmed clear.
7. Internet connectivity and sidecar health are separate states. Losing the
   internet keeps the loopback application and local workflows active; losing
   the sidecar shows a local-service error instead of an offline claim.

## Consequences

- A macOS red-window close is not the same as Quit. This preserves normal Dock
  reactivation and avoids a second sidecar; Cmd-Q or **Quit** leaves no retained
  Career Hub process after confirmed shutdown. A termination timeout leaves the
  owner visible instead of orphaning the still-running child.
- A crash requires an explicit retry, which avoids restart loops and makes data
  ownership visible.
- Offline support comes from bundled Chromium assets plus the local Python
  sidecar, not from Chromium alone. Gmail sync, public-link resolution, and
  external posting links still require a usable network path.
- System shutdown correctness depends on both the Electron quit path and the
  already-tested parent-pipe closure path.

## Verification Boundary

Pure lifecycle tests cover Windows/macOS close policy, single shutdown,
repeated quit events, activation suppression during quit, and fail-closed stop
failure with a later safe retry. Supervisor tests cover confirmed crash
ownership release, retained ownership after stop timeout, and a fresh retry
only after the exact child emits `exit`.
Linux development packaging exercises the integrated process path. Windows x64
and macOS arm64 native package, shutdown/logout, signing, and clean-install
evidence remain release gates and must not be inferred from Linux.

## References

- [Electron app lifecycle](https://www.electronjs.org/docs/latest/api/app)
- [Electron security checklist](https://www.electronjs.org/docs/latest/tutorial/security)
- [Python sidecar protocol](../reference/sidecar_protocol.md)
- [Desktop release gates](../reference/desktop_release_gates.md)
