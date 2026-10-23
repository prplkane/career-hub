# Browser Extension Development

Career Hub Capture is an attended cross-browser Manifest V3 tool for adding the current job
page to the local **Job Feed**, opening one verified application workspace,
assisting with an attended application form, and observing one exact saved-job
submission. Version `0.10.0` offers browser-specific packages for Chrome, Edge,
Firefox Desktop, and Safari on macOS, plus three explicit save modes, one compact
prepare flow, a local application assistant, and exact-URL submission tracking:

- **Prepare application** extracts the same bounded visible-page capture and
  opens its server-issued Discovery review with a prepare intent. After the
  user verifies one complete JD, Career Hub can save it as **Preparing** and
  open the existing Resume/Cover Letter workspace.
- **Page** under **Save for later** extracts bounded visible-page text and a small allowlist of
  JobPosting fields, then saves a `page_visible` item for review.
- **Selection** sends only the user's selected text and marks it as a
  partial `selection` capture.
- **Link only** sends URL and title without page text and creates a
  `link_only` **Needs JD** item.
- **Review in Career Hub** opens only the Discovery id returned by the server
  for the current tab's successful capture. The popup cannot supply a route,
  Discovery id, or replacement JD.
- **Track submission** claims an existing pending session or creates one only
  when exactly one startable saved job has the current canonical posting URL.
  No match creates nothing; an ambiguous exact URL fails closed. The popup
  cannot send a Job id, company, title, or replacement URL.
- **Application assistant** opens a separate browser helper surface for reviewed Profile
  autofill, one-field copy, an active local Resume shelf, and explicitly saved
  reusable answers. Copy and resume opening do not inspect the application
  page.
- **Review profile fields** inspects bounded visible supported controls in the
  active main frame, shows proposed mappings and empty state, and writes
  nothing until the user chooses **Fill selected fields**.
- **Find completed answers** reads only supported short answers after its own
  click. The user reviews and saves one exact safe question at a time; saved
  answers are filled only after a separate exact-question review.

Capture actions do not create a normalized JD, application, or Applied status.
Only the separate, confirmed **Prepare application** promotion creates the
saved job and moves it to Preparing. The extension makes no model/API call;
Resume and Cover Letter generation continues through the bounded local agent
commands exposed by Job Workspace.
A submission signal also leaves the application in its current stage until the
user answers **Mark this application as applied?** in Applications.
A usable page capture may store a versioned advisory preview on its Discovery;
selection and link-only captures remain explicitly unassessed. Page or selected
text must be reviewed and confirmed complete in the local browser UI before
intake controls appear. The extension never persists a capability token. The
capture review target, the one-shot helper reviews, and an armed application
binding use only WebExtensions `storage.session`, which the browser keeps in
memory and never writes to disk, so they survive the browser's idle termination
of the service worker within the same browser session without becoming durable
extension data. The review target is dropped when its tab navigates or closes.

## Build And Load The Extension

Build browser-specific packages before testing anything except the Chrome/Edge
source manifest:

```powershell
python -m scripts.build_browser_extension
```

See [Browser Extension Compatibility](browser_extension_compatibility.md) for
the support matrix, exact install steps, dynamic-origin pairing, Firefox AMO
requirements, and Safari/Xcode release gate.

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
the launcher has no profile switch, and the extension has none either. Use the
synthetic qualification harness (`npm run qualify:extension`) for disposable
fixtures rather than capturing into real data.

`-EnableExtension` is an opt-in convenience for the fixed Chrome developer ID
and requires port 8766. For Firefox, Safari, or an alternative build, copy the
origin shown in the popup and pass that one exact
`-ExtensionOrigin` instead; the two options are mutually exclusive.

If port 8766 is occupied, startup reports that the URL is already in use. Open
that server if it already has the required extension Origin; otherwise stop it
before starting the extension-enabled process. This development build targets
`http://127.0.0.1:8766` only.

**Pairing (ADR SEC-250 item 4).** The configured `Origin` alone no longer
admits the extension, because a local program can forge it. Pair once: in
Career Hub open **Settings → Pair browser extension → Create pairing code**,
then type the 8-character code into the extension side panel within five
minutes. The code works once and is voided after five wrong guesses. The worker
receives a 256-bit credential, keeps it only in `chrome.storage.local`, and
sends it as `X-Career-Hub-Extension-Credential` on each session bootstrap.
Career Hub stores only its SHA-256 in the Data Home's `session/` folder, so the
pairing survives restarts. **Unpair extension** in Settings revokes it at once;
the worker then forgets the credential and the side panel asks for a new code.

The service worker obtains a new capture token with `POST /api/session`
for every capture or application-session operation and keeps it only for that
request. The POST is intentional:
Chrome can omit `Origin` on a host-permitted extension GET, while POST preserves
the configured extension-Origin boundary. The server accepts the resulting
token only from that Origin and only for the fixed capture, application-session
claim, minimal submission-signal, and interruption routes.

Profile also exposes an empty-by-default **Application helper** allowlist. Its
reserved `POST /api/application-helper/session` bootstrap issues a different
token limited to the purpose-specific assistant projection and explicit
reusable-answer save/delete routes. Capture tokens cannot read assistant data,
and assistant tokens cannot capture a page, inspect applications, download a
resume, or call browser configuration APIs. The projection contains only
enabled-and-present canonical Profile values, active Resume Version ids and
display labels, and minimized explicitly saved answers.

The helper surface renders preview-safe Profile masking, rechecks the Profile
revision before writing one value to the clipboard, keeps no assistant value in
extension storage, and clears its in-memory projection when the panel closes.
There is no Copy all action. Opening a Resume Version re-fetches the shelf and
constructs one fixed local deep link; the browser-only Career Hub route owns the
verified file download.

Reviewed Profile fill stores only an expiring one-shot token plus target
metadata (page labels and form-target hashes, never a Profile value) in
memory-backed `storage.session`. One shared Review/Fill matcher scans at most
240 eligible visible controls after a separate 2,000-node raw bound, groups
matches by form, and uses only one uniquely strongest form. Fill re-fetches the
current projection, revalidates the same tab and exact URL, and injects only the
selected values after every form-scoped target remains supported, unambiguous,
and empty. Supported controls are short text inputs, textarea, select, and
yes/no radio groups. Explicitly enabled voluntary US demographic answers remain
one-field Copy only because a generic page-controlled combobox cannot be
selected without invoking page-owned behavior. Credentials, uploads,
demographic controls, consent, open-ended screening, and non-empty controls
remain outside reviewed fill.

Reusable answers use exact normalized questions only. **Find completed
answers** returns a bounded reviewed candidate list and keeps only a SHA-256
digest of each page value in its one-shot review; **Save** re-reads that one
page value, confirms it still matches the reviewed digest, then writes or
updates one local answer using an independent optimistic revision. **Review saved answers** exposes no answer value to the
page, and **Fill selected answers** revalidates the tab, URL, answer revision,
question, type, target, and empty state before a one-shot fill. Sensitive,
financial, demographic, eligibility, compensation, upload, job-specific, and
open-ended questions are rejected in both the page helper and local server.
The lazy answer bank lives only in the private Data Home and is not created on a
fresh checkout or data profile until the user explicitly saves an answer.

Every review is consumed after one attempt. No value, selector, URL, resume id
outside the current shelf, or free-form field can be supplied as replacement
data by the side-panel message.

## Use And Verify

1. Open a normal HTTP or HTTPS job page.
2. Select the **Career Hub Capture** toolbar action.
3. Confirm that the popup shows the current title and hostname.
4. Choose one action:
   - use **Prepare application** to capture the visible job and continue into
     its exact local verification/workspace flow;
   - use **Page** under **Save for later** when one complete job is visibly open;
   - select the exact JD text first, then use **Selection**, when the page
     contains several jobs or extraction is ambiguous;
   - use **Link only** as the safe fallback.
5. Confirm the success state identifies the saved mode and shows one bounded
   receipt: **No clear conflict found**, **Review needed**, **Clear conflict
   found**, or **Assessment unavailable**.
   - A clear conflict includes exact JD wording and the compared Career profile
     fact.
   - A partial selection or link says that no gaps were inferred.
   - Required and then preferred evidence gaps are shown before unknown source
     phrases in the bounded receipt. Unknown phrases never become vocabulary.
   - When a complete JD contains a narrowly detected extra application step,
     the popup adds only **N extra application steps flagged**. It never renders
     the instruction wording.
   - When a complete JD contains a narrowly detected contact candidate, the
     popup adds only **N contact candidates flagged**. Exact identity fields,
     channels, context, and evidence remain in Career Hub review, and the count
     creates no contact.
   - The receipt has no fit score and never prevents the user from continuing.
6. Choose **Review in Career Hub**. Confirm a new local tab opens the exact
   `#job-feed/<discovery_id>` route for that capture and shows retained text,
   scope, identity, provenance, and current assessment context before any
   intake controls.
7. Choose **Review later** to make no write, or explicitly confirm that the
   retained page/selection is one complete JD. Confirmation alone creates no
   Job or Application.
8. After confirmation, choose **Save job** or **Record as applied** only after
   the separate confirmation prompt. Both hand off to that exact Job Workspace;
   **Record as applied** is for an application already submitted by the user.
9. Repeat the same capture and confirm the Feed still contains one semantic
   discovery with another observation, not another Job or Application.

### Use the application assistant

1. In **Career Vault > Profile**, open **Application helper**, enable only the
   synthetic fields needed for the test, and save the allowlist. **Manage
   allowed fields** in the extension opens this dialog directly.
2. Open the extension popup and choose **Application helper**. Confirm Chrome
   or Edge opens a side panel, Firefox opens its sidebar, and Safari opens a
   dedicated extension tab. The capture popup remains compact.
3. Confirm each enabled, present Profile field has one canonical label, a
   masked or preview-safe value, and its own **Copy** button. Empty and disabled
   fields must be absent.
4. Choose one **Copy** action and confirm its accessible success message names
   only that field. The extension re-fetches the projection first; if Profile
   changed while the panel was open, it refreshes the list and copies nothing.
5. Close the panel and confirm helper values are absent from local and session
   extension storage. With Career Hub stopped, reopen it and confirm the panel
   shows an actionable offline state without rendering a prior value.
6. Confirm **Resume shelf** lists active Resume Version display labels only.
   Open one and verify Career Hub focuses that exact local record for a
   browser-only **Download**; no file bytes or private source filename are
   exposed to the extension.
7. On a disposable application form, choose **Review profile fields on page**.
   Confirm the preview identifies the hostname, canonical field, page label,
   and whether the control is empty; it must not display or send the Profile
   value.
8. Confirm non-empty and ambiguous matches are disabled. Select only desired
   empty mappings and choose **Fill selected fields**. The extension inserts
   those values, emits input/change events, and clicks nothing.
9. Change the Profile, active URL, or reviewed form before Fill. Confirm the
   one-shot review fails closed and asks for a new review; a stale form produces
   no partial writes. A same-shape dynamic rerender may be revalidated and
   filled.
10. Complete one safe short synthetic question, choose **Find completed
    answers**, review its displayed value, and explicitly save that one row.
    Clear the page control, choose **Review saved answers**, and fill the exact
    match. Confirm sensitive, eligibility, compensation, job-specific, and
    open-ended prompts never appear as candidates. Copy and delete the saved
    row explicitly, and confirm no extension storage contains the answer.

Chrome/Edge `sidePanel`, Firefox `sidebarAction`, and Safari's extension-tab
fallback host this separate attended surface. The
`clipboardWrite` permission supports only a user-clicked Profile-field or
saved-answer write; `clipboardRead` is not requested. The popup opens the helper
directly from the click handler because Firefox requires the sidebar call to
retain that user gesture.

### Track one submission

1. Open one saved job's exact posting, open the extension, and choose **Track
   submission**. If exactly one Saved/Preparing application has that canonical
   URL, Career Hub moves it to Preparing and binds the four-hour session to the
   current tab. If direct matching is unavailable, **Create tracking link** in
   the Applications drawer remains an explicit fallback.
2. A different URL, ambiguous duplicate URL, different tab, or different
   Origin must fail closed; no application is guessed or created.
3. Apply normally. The attended-session observer never fills fields, uploads a
   file, clicks Continue/Next/Submit, or reads field values. Same-Origin DOM
   replacement and navigation re-install the bounded observer in the already
   bound tab. Optional reviewed prefill remains a separate two-action helper.
4. A trusted native form submit or a trusted click whose complete visible label
   is exactly **Submit application**, **Submit my application**, **Send
   application**, or **Complete application** is only a candidate. It must be
   followed within 15 minutes by one high-precision visible success phrase.
   Validation failure, a button label alone, login, CAPTCHA, Continue/Next
   without success, Save draft, Withdraw, and tab reuse do not change the
   application stage.
5. Return to **Applications** when the popup says a confirmation is waiting.
   Choose **Mark Applied** only if the external submission really succeeded, or
   **Not submitted** to dismiss the signal. Closing the tab or losing
   instrumentation leaves the stage unchanged and shows the recoverable session
   outcome in the application drawer.

Use only synthetic fixtures for these checks; the packaged qualification
harness runs them against a disposable Data Home.

## Extraction Boundary

The `scripting` permission is used only after the user opens the toolbar popup
and chooses a capture action. `activeTab` provides temporary access to that one
HTTP(S) tab; there is no persistent site access, broad host wildcard, always-on
content script, background crawl, navigation, scrolling, clicking, or iframe
collection. Profile/answer Copy and Resume opening do not invoke `scripting` or
inspect the active tab. Profile Review/Fill and reusable-answer Find/Review,
save recheck, and Fill invoke it only after the corresponding explicit action,
always with `allFrames: false`. Each scan is bounded, chooses one uniquely
strongest form, and never accepts an arbitrary selector or caller-supplied
answer value from the panel.

**Save page** uses this bounded order:

1. one unambiguous `application/ld+json` `JobPosting`;
2. one provider-hinted description container that passes the shared validator;
3. one ranked explicit, title-anchored, or semantic description container;
4. bounded visible body text only when the page has one title and no job-list,
   search, recommendation, or multi-role contamination signals;
5. otherwise an instruction to use an exact selection.

Indeed, LinkedIn, Handshake, MeeBoss, and 12twenty adapters provide candidate
selectors and title hints only. They never bypass the shared validator. The
validator rejects search/list shells, multiple linked job titles, recommendation
regions, and whole-page candidates that substantially overlap a contaminated
body. A selector change therefore fails closed to **Save selection** instead of
saving the surrounding job board. Matching a selected-card title to a detail
heading is supporting evidence, not sufficient proof by itself.

Only title, company, location, and posting ID may come from structured data.
The worker sends normalized text, never raw HTML. Scripts, styles, forms,
inputs, hidden content, cookies, request headers, page local/session storage,
credentials, and screenshots are excluded. Source text is capped at 500,000
characters and the server body is capped at 1 MiB.

The application observer locally checks only bounded visible status/headline
text for four enumerated success phrases and the exact final-action labels
listed above. It transmits no page text or DOM: only the opaque session id,
bound tab/origin, timestamps, one random event key, and one enumerated rule id.
Each tab uses its own WebExtensions `storage.session` key so concurrent tabs cannot
overwrite one another. No form value, resume content, cookie, credential,
screenshot, or raw HTML is stored there. After a detected signal, the stored
per-session token is cleared.

The popup sends fixed commands only. The service worker independently chooses
the active tab and fixed loopback API routes. It stores the server-issued review
id only in memory-backed `storage.session`, keyed by the capturing tab, and
constructs the fixed local Job Feed hash itself, including the fixed `/prepare` intent; it never accepts a URL, route,
Discovery id, Job id, or JD text from popup messages.

## Preview And Receipt Boundary

Career Hub, not the extension, runs the existing deterministic JD analyzer and
evaluator for `page_visible` or explicitly `confirmed_full` source text. The
stored preview is reused only while the content hash, analyzer/evaluator
versions, Career Vault candidate revision, taxonomy context revision, and
capture scope still match. A fresh page capture or relevant Career Vault change
therefore refreshes it.

The capture response contains only outcome/readiness metadata and the compact
receipt required by the popup. It does not expose a general Discovery record,
observation history, full assessment, Career Vault, or a read endpoint to the
extension. Its optional `extra_step_count` comes from the same bounded local JD
analysis; partial and link-only capture returns zero and the local review says
**Full JD required**. Exact instruction wording and checklist history stay in
Career Hub. Missing skills and unrecognized wording remain **Review**, not hard
blockers. **Clear conflict** is limited to source-backed citizenship,
sponsorship/work-authorization, active-clearance, or verified relevant-years
conflicts. The result is advisory and never changes Saved, Applied, Skipped, or
Dismissed state.

## Troubleshooting

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
  **Link only**.
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
  exact canonical posting URL. Use **Prepare application**, then retry from the
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

Inspect background errors from Chrome/Edge's extension card, Firefox's
`about:debugging` **Inspect** action, or Safari's developer tools. Never paste
capability tokens, private URLs, or captured page content into bug reports or
screenshots.
