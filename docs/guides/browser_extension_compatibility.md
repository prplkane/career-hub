# Browser Extension Compatibility

Career Hub Capture version `0.10.0` shares one reviewed WebExtensions codebase
and generates browser-specific Manifest V3 packages. The supported scope is
major desktop browsers; mobile browsers and Firefox for Android are not release
targets.

## Support Matrix

| Browser | Minimum | Package | Application helper | Release status |
| --- | ---: | --- | --- | --- |
| Google Chrome | 116 | `chrome` | Chrome side panel | Supported |
| Microsoft Edge | 116 | `edge` | Edge sidebar/side panel | Supported; Chromium-compatible package |
| Mozilla Firefox Desktop | 140 | `firefox` | Firefox sidebar | Supported; Mozilla lint and Firefox runtime load are automated/repeatable |
| Apple Safari on macOS | 17 | `safari` | Dedicated extension tab | Source-compatible package; live macOS/Xcode validation and signing are required before release |

Brave, Vivaldi, and Opera may load the Chrome package because they are based on
Chromium, but they are best-effort compatibility targets until their own release
matrix is exercised. Mobile Safari and Firefox for Android are out of scope.

The helper location differs because Chrome/Edge `sidePanel` and Firefox
`sidebarAction` are incompatible APIs, while Safari has neither compatible
surface. Capture, Job Feed review, attended form review/fill, reusable answers,
and submission tracking use the same bounded logic in every package.
Safari's dedicated helper tab, and the tab fallback used if a native side-panel
surface cannot open, remain bound to the originating application tab. Moving
that application tab to another window or closing it fails closed instead of
retargeting another page.

## Build All Packages

From the repository root, run:

```powershell
python -m scripts.build_browser_extension
```

The command creates ignored, reproducible build artifacts under
`outputs/browser_extensions/`:

- one unpacked directory for each browser;
- `.zip` archives for Chrome, Edge, and Safari;
- an `.xpi` archive for Firefox.

Existing output is never replaced implicitly. Add `--overwrite` to rebuild an
existing target, or select one or more targets with repeated `--target` flags:

```powershell
python -m scripts.build_browser_extension --target firefox --overwrite
python -m scripts.build_browser_extension --target safari --overwrite
```

The target manifests retain only the existing loopback host permission,
`http://127.0.0.1/*`. They do not add broad site access or persistent content
scripts.

## Install For Development

### Chrome

1. Open `chrome://extensions`, enable **Developer mode**, and choose **Load
   unpacked**.
2. Select `outputs/browser_extensions/chrome`.
3. Confirm the developer ID is `jhgaecfochimmafoopcpodncoeobecfg`.
4. Start Career Hub with `-EnableExtension` on port 8766.

The fixed public development origin is:

```text
chrome-extension://jhgaecfochimmafoopcpodncoeobecfg
```

### Edge

1. Open `edge://extensions`, enable **Developer mode**, and choose **Load
   unpacked**.
2. Select `outputs/browser_extensions/edge`.
3. If Edge shows the fixed ID above, use `-EnableExtension`. If the installed ID
   differs, copy the exact `chrome-extension://<id>` origin and use
   `-ExtensionOrigin`.

### Firefox Desktop

1. Open `about:debugging#/runtime/this-firefox`.
2. Choose **Load Temporary Add-on** and select
   `outputs/browser_extensions/firefox/manifest.json`.
3. Open the Career Hub popup and copy the displayed exact
   `moz-extension://<uuid>` origin.
4. Start Career Hub with that value passed to `-ExtensionOrigin`.

Firefox assigns the runtime UUID. Never substitute the Gecko add-on ID or use a
wildcard. The temporary installation is removed when Firefox exits; a release
build must be signed through Mozilla Add-ons.

### Safari On macOS

For a local Safari 17+ check, build the Safari target, then use Safari's
**Settings > Developer > Add Temporary Extension** command and select the
generated Safari directory or ZIP. Safari removes temporary extensions after
24 hours or when Safari quits.

For an Xcode package, run this on a Mac with current Xcode command-line tools:

```bash
xcrun safari-web-extension-packager \
  /path/to/career-hub/outputs/browser_extensions/safari \
  --copy-resources \
  --macos-only
```

Review every compatibility warning, build the generated container app, enable
the extension in Safari, and run the Safari manual matrix before signing or App
Store submission. A JavaScript-only Safari web extension still needs the
container project for normal distribution.

Safari's extension host is intentionally not pinned and may change between
launches. Open the popup, copy its current exact
`safari-web-extension://<host>` origin, and pass it to `-ExtensionOrigin` for
that server start. Do not weaken the server boundary to a wildcard. The
application helper opens in a dedicated extension tab on Safari.
Keep the originating application tab in the same Safari window while using the
helper; the helper remains explicitly bound to that tab even though the helper
tab becomes active.

## Start With An Exact Dynamic Origin

Firefox, Safari, and any Chromium installation whose ID differs from the fixed
development ID use the explicit launcher option:

```powershell
.\scripts\start_career_hub.ps1 `
  -DataProfile sandbox `
  -ExtensionOrigin "moz-extension://the-exact-runtime-uuid"
```

Use `safari-web-extension://...` or `chrome-extension://...` as appropriate.
Only a scheme plus host is accepted; paths, credentials, ports, query strings,
fragments, wildcards, and ordinary web origins are rejected. The allowed origin
is memory-only and must be supplied at every server start.

## Release Gates

Run these checks before publishing any target:

```powershell
python -m unittest tests.test_browser_extension_build tests.test_browser_extension tests.test_launcher_security
node tests/browser_extension_compatibility.cjs
node tests/browser_extension_worker_harness.cjs
```

For Firefox, also run Mozilla's current `web-ext lint` against the generated
directory and load it in the minimum/current supported Firefox versions. The
manifest truthfully declares the data categories needed for the extension's
primary functions: personally identifying information, location information,
browsing activity, website content, and website activity. Those categories
cover job-page capture to the local Career Hub app, reviewed application-field
assistance, reusable-answer saving, and submission-state tracking. The extension
does not send telemetry or data to third-party analytics; its only declared host
is the local Career Hub service at `127.0.0.1`, while reviewed field values are
inserted into the application page the user is attending. Firefox private-window
access is disabled so these workflows cannot run in private browsing. A current
Mozilla linter may emit an Android-version warning even though this manifest does
not opt into Firefox for Android; desktop errors and warnings remain release
blockers.

For Safari, the release gate must happen on macOS with the current Safari and
Xcode: package, review converter warnings, temporarily load the extension,
exercise capture and the helper-tab fallback, restart Safari and re-pair its
exact origin, then build/sign the container. Linux test results are not evidence
of Safari runtime readiness.

For Chrome and Edge, load the generated unpacked target and run the attended
sandbox flow in each browser. Store signing, privacy disclosures, screenshots,
and store review are distribution work; generated archives alone are not a
published release.
