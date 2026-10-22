# DESK-020: Make Offline State Explicit Without A Tracking Probe

- **Status:** Implemented in browser and Electron; native target acceptance pending
- **Decision date:** 2026-08-27
- **Evidence tickets:** `ELEC-330`, `ELEC-350`, `DIST-500`

## Context

Career Hub is local-first. Its renderer, Python API, data, catalogs, and export
paths can operate without internet access, while Gmail sync, public-JD
resolution, external postings, extension capture of live sites, and updates
cannot. A network cut must not resemble an application crash, erase work, or
leave a network action spinning indefinitely.

Chromium/Electron network booleans are asymmetric: `false` strongly indicates
that remote requests will fail, while `true` does not prove that DNS, a captive
portal, proxy, firewall, or particular provider will succeed. Calling a Career
Hub or third-party beacon only to label connectivity would add telemetry,
privacy leakage, and a new availability dependency.

## Decision

### State Model

The UI maintains presentation-only connectivity state; it is never persisted in
the Data Home and never changes canonical workflow state.

- **Local ready:** the sidecar and local API are healthy. This is independent
  of internet state.
- **Offline:** `navigator.onLine` is false (and, once Electron exists,
  `net.isOnline()` may provide the loading-page snapshot). This is a strong
  negative signal.
- **Connection available, unverified:** the browser/OS reports online. The UI
  does not display a global “internet verified” promise.
- **Network action unavailable:** a user-requested network operation returns a
  classified DNS/connect/timeout failure. The UI names that operation or
  provider rather than claiming every internet destination is down.

No periodic external reachability request is added. Browser `online` and
`offline` events update the global indicator. A successful user-requested
network operation clears its own failure. Reconnection never automatically
retries Gmail, follows a job URL, submits content, or changes a saved state.

### User Experience

When offline, every application surface shows one persistent, accessible
indicator:

> Offline — local features remain available. Internet actions will wait for a
> connection.

The indicator uses text and an icon, not color alone, and its transition is
announced through a polite live region. It does not use a blocking modal. Local
editing, navigation, search, matching, export, backup, document access, and
manual `.eml` import remain enabled.

Known network-only controls are disabled while offline and retain an adjacent
reason or tooltip. This includes Gmail sync, public-JD resolution, and update
checks. External posting links may remain available because the system browser
can provide its own cached/offline behavior, but Career Hub never interprets an
external-browser failure as a local-data failure.

When the network interface returns, show a bounded transient message:

> Connection available — retry internet actions when ready.

This wording is deliberately weaker than “Online.” Provider-specific errors
remain visible until that action succeeds or the user dismisses them.

### Error And Timeout Contract

Fetch failure between the renderer and `127.0.0.1` is a **local application
connection failure**, not an internet-offline state. It keeps the existing
sidecar recovery/error path. Python network adapters map DNS, connect, and read
timeouts to stable content-minimized codes. Authentication, rate-limit,
unsupported-page, and validation errors do not set offline state.

Every network action has an existing bounded timeout or gains one before being
enabled in desktop mode. A connection cut preserves the original local record,
request inputs needed for explicit retry, and any prior successful result. It
does not partially promote a job, mark email read, or infer application state.

### Offline Capability Contract

The installed app must start and provide the existing local workflows with all
non-loopback interfaces disabled. Tests cover disconnection before startup,
cutoff during each network action, reconnection without automatic retry, and a
second offline restart using the same synthetic Data Home. The packaged UI may
load no CDN font, script, style, image, or remote application page.

For macOS direct distribution, the notarization ticket is stapled to the app or
distribution container so Gatekeeper can validate it without contacting Apple.

## Alternatives

- Treat `navigator.onLine === true` as verified internet: rejected because
  Chromium documents false positives.
- Poll a public or Career Hub endpoint: rejected because it creates telemetry,
  captive-portal ambiguity, background work, and a hosted dependency.
- Disable the whole application offline: rejected because the product's core is
  deliberately local.
- Automatically replay failed operations: rejected because network actions may
  have external effects or stale inputs and require explicit user intent.

## Consequences

The app remains useful during outages and reports the failing layer accurately.
Network adapters need stable connectivity classifications. Rendered tests must
exercise both browser mode and the pinned Electron renderer. Online status is
conservative by design rather than visually optimistic.

## Reversal Conditions

A future first-party connectivity service may be considered only with an
explicit privacy/telemetry decision, bounded cadence, offline fallback, and no
effect on local readiness. Automatic replay requires per-operation idempotency
and a separate consent/state-machine review.

## Primary References

- Electron, online/offline detection and reliability limits:
  https://www.electronjs.org/docs/latest/tutorial/online-offline-events
- Electron `net.isOnline()`:
  https://www.electronjs.org/docs/latest/api/net/
- Apple, stapling for offline Gatekeeper validation:
  https://developer.apple.com/documentation/security/customizing-the-notarization-workflow
