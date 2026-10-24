# Browser Extension: Load And Pair

Career Hub Capture is an attended Manifest V3 extension for Chrome, Edge,
Firefox Desktop and Safari on macOS. It adds the current job page to the local
**Job Feed**, opens one verified application workspace, assists with an
attended application form, and observes one exact saved-job submission. It
makes no model or API call, never submits a form, and never persists a
capability token. This guide covers loading an unpacked build and pairing it
with Career Hub.

## Build And Load The Extension

Build browser-specific packages before testing anything except the Chrome/Edge
source manifest:

```powershell
python -m scripts.build_browser_extension
```

See [Browser Extension Compatibility](browser_extension_compatibility.md) for
the support matrix, exact install steps per browser and dynamic-origin pairing.

For a quick Chrome source check:

1. Open `chrome://extensions` in Chrome.
2. Turn on **Developer mode**.
3. Select **Load unpacked**.
4. Choose this repository's `extension` directory.
5. Confirm that the extension card shows the fixed developer ID
   `jhgaecfochimmafoopcpodncoeobecfg`.
6. Optionally pin **Career Hub Capture** from Chrome's Extensions menu.

The packaged public development key keeps the local Origin stable:

```text
chrome-extension://jhgaecfochimmafoopcpodncoeobecfg
```

The public key is an identifier, not a credential. A future Chrome Web Store
package may use the Store item's public key and ID instead. Do not add a
trailing path, wildcard, or popup filename to an Origin.

## Start Career Hub For This Extension

Extension access is off by default. The supported way to turn it on is the
app's own setting: open **Settings → Browser extension**, enable **Allow the
browser extension to connect**, and restart Career Hub. The desktop host stores
the choice and starts the sidecar on port 8766 from then on. For Firefox,
Safari, or another Chromium ID, copy the origin shown in the extension popup
into **Other browsers** before restarting.

For a one-off development launch the explicit switches still work and merge
with the saved setting:

```powershell
.\scripts\start_career_hub.ps1 -EnableExtension
```

The development app always uses the `%LOCALAPPDATA%\CareerHub\dev` Data Home;
the launcher has no profile switch, and the extension has none either. Use
disposable synthetic data rather than capturing into real data.

`-EnableExtension` is an opt-in convenience for the fixed Chrome developer ID
and requires port 8766. For Firefox, Safari, or an alternative build, copy the
origin shown in the popup and pass that one exact
`-ExtensionOrigin` instead; the two options are mutually exclusive.

If port 8766 is occupied, startup reports that the URL is already in use. Open
that server if it already has the required extension Origin; otherwise stop it
before starting the extension-enabled process. This development build targets
`http://127.0.0.1:8766` only.

## Pair The Extension

The configured `Origin` alone does not admit the extension, because a local
program can forge it. Pair once: in Career Hub open **Settings → Pair browser
extension → Create pairing code**, then type the 8-character code into the
extension side panel within five minutes. The code works once and is voided
after five wrong guesses. The extension keeps the resulting credential only in
its own extension storage and Career Hub stores only its SHA-256, so the
pairing survives restarts. **Unpair extension** in Settings revokes it at once;
the extension then forgets the credential and the side panel asks for a new
code.

## Troubleshooting

For a failure in the packaged browser-extension test, inspect the failed
workflow's `extension-startup-diagnostic` artifact. It records the last startup
stage and, when available, the desktop's error code and loading-screen image.
These diagnostics use the test's disposable Data Home and omit session tokens.
The startup deadline remains 30 seconds.

- **Career Hub isn't reachable**: turn on **Settings → Browser extension** and
  restart Career Hub so it listens on port 8766. Developers may instead start
  with `-EnableExtension` for the fixed Chrome build or the popup's exact
  `-ExtensionOrigin` for another installed package.
- **Pair this extension first**: open the side panel and type a fresh code
  from Career Hub **Settings → Pair browser extension**. Expected once after
  updating, after **Unpair**, and after reinstalling the extension.
- **Extension isn't allowed**: for the bundled Chrome build, confirm the ID is
  `jhgaecfochimmafoopcpodncoeobecfg`. For Firefox, Safari, a Store build, or an
  alternative Chromium ID, copy the exact origin shown in the current popup
  into **Settings → Browser extension → Other browsers**. Restart Career Hub,
  then reopen the popup.
- **Page text isn't available**: the page blocks temporary script access. Use
  **Save link only**.
- **Page is too large** or **One job description wasn't clear**: confirm that a
  single detail pane is open, then select the exact JD text and use **Save
  for later > Selection**. A search/list shell is intentionally not accepted
  as a full JD.
- **No job text found**: open the actual job detail rather than a login, search,
  or empty shell page.
- **Career Hub needs an update**: update the repository, reload the extension,
  and restart Career Hub.
- **Capture this page again to restore its exact local review link**: the tab
  navigated or the browser session ended since the capture, so its review
  target is gone.
- **Prepare this job first**: no single startable saved application matches the
  exact canonical posting URL. Use **Save and open workspace**, then retry from the
  posting. If the saved URL cannot be made exact, create a tracking link from
  the relevant Applications drawer.
- **More than one session matches**: two saved applications share the exact URL.
  Cancel the extra pending session instead of guessing which application owns
  the tab.
- **This tab couldn't be attended**: the observer could not be installed. The
  application stage was not changed; reload the posting and start a fresh
  session.
- **Changes do not appear**: reload the unpacked extension after changing the
  manifest, worker, extractor, popup, or CSS.

Never paste capability tokens, private URLs, or captured page content into bug
reports or screenshots.
