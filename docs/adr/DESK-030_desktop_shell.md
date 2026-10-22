# DESK-030: Keep Electron As The Desktop Shell And Keep It Current

- **Status:** Accepted
- **Decision date:** 2026-10-13
- **Evidence tickets:** `CH-139` (Phase 1 quality gates), `CH-134`
- **Supersedes:** the deferred "evaluate Electron/Wails after backend parity"
  item in [technology selection](../reference/technology_selection.md)

## Context

Career Hub's renderer is a browser UI served by the native Go sidecar on
`127.0.0.1` and shown by an Electron host. [MIG-100](MIG-100_backend_language_and_migration.md)
deferred the shell question until the Go backend reached parity, which it did
with PR #40 (2026-10-03). The product direction is a private, encrypted, local
career record for non-technical users on Windows first and macOS later, so
renderer hardening and a trustworthy update path weigh more than install size.

What a replacement must preserve today:

- About 5,000 lines in `desktop/src/` with 29 test files. Most of it is product
  logic, not shell glue: sidecar supervision and executable verification
  (`sidecar.js`, `package_manifest.js`), paths and channels (`app_paths.js`),
  installer and update flows, and recovery, backup and restore orchestration.
- Renderer hardening: sandbox, context isolation, no Node integration, denied
  permissions (`security.js`), and nine fuses including ASAR integrity
  (`forge.config.js`).
- A loopback endpoint for the browser extension (`extension/manifest.json`
  grants `http://127.0.0.1/*`) and for the `career-hub-agent` client. No shell
  choice removes it.

## Decision

1. **Keep Electron.** No alternative currently improves security or the update
   path enough to justify a 4 to 7 engineer-week port (estimate below).
2. **Keep Electron current.** The exact pin stays, because it makes the
   installer reproducible and qualified, but it is now upgraded deliberately:
   - move to the latest patch of the pinned major at least monthly, since each
     patch carries Chromium security fixes (Dependabot ignores the pin; see
     `.github/dependabot.yml`);
   - move to a newer supported major before the pinned major's end of support.
     Electron supports three majors at a time, so plan a major upgrade about
     every eight months. Electron 44 support ends **2027-03-02**.
3. **Review the Windows updater.** Squirrel.Windows has had no release since
   2.0.1 (2020-09-27). Evaluate MSIX (supported by Electron's `autoUpdater`)
   or Velopack before the first public release.

The first application of this decision moved Electron from 44.0.0 (Chromium
152.0.7977.54) to 44.7.0 (Chromium 152.0.7977.130, Node 24.21.0).

## Alternatives

| Option | Status on 2026-10-13 | Why not now |
| --- | --- | --- |
| Wails v3 (Go, system WebView) | Pre-release only: `v3.0.0-beta.29` is an automated nightly; latest stable Wails is v2.14.0 | Not released. Its built-in updater checks SHA-256, with signatures optional. Its Windows WebView2 permission handling needs an explicit deny policy to match `security.js`. |
| Wails v2 | Stable | No built-in updater; no equivalent of ASAR integrity fuses. |
| Thin Go WebView (`webview_go`, `go-webview2`) | `webview_go` inactive since 2024; `go-webview2` is Windows-only | Every hardening and update feature would be built by hand. |
| Tauri v2 with the Go sidecar | Stable, active | Adds a Rust layer while Go stays a sidecar; its required updater signatures are attractive, but they do not offset a second shell language. |
| Fyne or Gio (native Go UI) | Fyne stable; Gio pre-1.0 | Requires rewriting the whole UI. |

System WebViews also split the renderer: WebView2 (Chromium) on Windows but
WKWebView (Safari engine) on macOS, so each platform would need its own UI
qualification. Electron keeps one Chromium baseline.

Running Go in-process (Wails) would take UI traffic off loopback, but the
extension and agent endpoint would remain. The local API lockdown is required
either way and is cheaper to do once, on the current shell.

## Consequences

- Install size stays large (Electron bundles Chromium). This is accepted for
  renderer consistency and hardening.
- Electron upgrades become a recurring maintenance task with a qualification
  step: desktop tests, the foundation check, the native rendered smoke in CI,
  and a packaged install on Windows.
- The opt-in native integration tests in `desktop/tests/go-*.integration.test.js`
  run the unchanged Electron supervisor against a real Go child. Since
  2026-10-15 none needs Python: 11 sidecar, 2 maintenance and 1 backup test pass
  with `CAREERHUB_GO_SIDECAR`, `CAREERHUB_GO_SIDECAR_SHA256`,
  `CAREERHUB_TEST_GO_SIDECAR=1` and, for maintenance, `CAREERHUB_GO_STARTUP_FIXTURE`
  (a test binary of `internal/sidecar/startup`). They do not run in CI yet;
  wiring them in is tracked so shell upgrades are verified against a real Go
  child automatically.

## Reversal Conditions

Revisit this decision, starting with Wails v3, when all of these hold:

1. Wails v3 has a stable 3.0.0 release.
2. Its updater can require signatures (built in, or through a custom provider).
3. macOS work begins. WKWebView qualification is then needed anyway, so the
   switch costs less.

Rough cost to switch to Wails v3 at that point (judgment, not measured):
2 to 3 weeks to port lifecycle, navigation, recovery and path logic to Go;
1 to 2 weeks for a signed updater, installer and code signing; about 1 week
for security parity (permission deny policy, navigation lock, an integrity
manifest); about 1 week for Windows and macOS qualification.

A separate question, moving UI traffic from loopback HTTP to a private desktop
channel, is compatible with this decision and belongs to the local API
lockdown work.

## Primary References

- Electron release schedule and support dates: <https://releases.electronjs.org/schedule>
- Electron 44.7.0 release: <https://releases.electronjs.org/release/v44.7.0>
- ASAR integrity: <https://www.electronjs.org/docs/latest/tutorial/asar-integrity>
- Electron `autoUpdater` (Squirrel and MSIX): <https://www.electronjs.org/docs/latest/api/auto-updater>
- Wails releases: <https://github.com/wailsapp/wails/releases>
- Wails v3 self-update guide: <https://docs-preview.wails.io/tutorials/04-self-update-a-wails-app/>
- Tauri updater (required signatures): <https://v2.tauri.app/plugin/updater/>
- Squirrel.Windows releases: <https://github.com/Squirrel/Squirrel.Windows/releases>
- Velopack: <https://github.com/velopack/velopack>
- WebView2 distribution: <https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution>
