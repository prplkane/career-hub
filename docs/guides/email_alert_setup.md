# Email Alerts Setup Guide

This guide prepares a dedicated job-alert mailbox and selective forwarding for
Career Hub without a GCP worker, webhook, public server, custom mail domain, or
access to a personal inbox.

Career Hub now supports bounded local Gmail IMAP sync for a dedicated alert
mailbox. Connection secrets stay in Windows Credential Manager; messages are
read without changing mailbox state, and the original email is not retained in
the Data Home.

## Recommended Setup

```text
Job board with separate alert address ------> dedicated Gmail
Existing personal Gmail alert --------------> exact forwarding filter
                                                   |
                                             dedicated Gmail
                                                   |
                                             local Career Hub sync
                                                   |
                                                Job Feed
```

Use a separate personal Gmail account only as an alert sink. Choose a neutral,
non-identifying local part such as `career-alerts`; the exact address is not
part of the Career Hub contract and must never be committed to the repository.

Do not use this account for:

- applications or recruiter correspondence;
- account recovery for important services;
- Drive documents, resumes, or personal mail;
- sending mail; or
- anything that would make broad mailbox access high impact.

Career Hub's connector behaves read-only, but a Gmail IMAP
App Password is not technically label-scoped or read-only. The dedicated
account is what contains that permission risk.

## Why Not The Other Options

### Existing personal inbox

Not recommended for the initial connector. A label and sender rules limit what
Career Hub intends to process, but a broad IMAP credential may still authorize
access to other mail. A future Google OAuth connector can revisit this with a
clear permission screen and review.

### Gmail `+jobs` alias

An address using Gmail's `+jobs` alias is useful for filtering but still lands
in the personal mailbox. It does not isolate Career Hub access.

### Custom domain

A custom address such as `jobs@example.com` can be pleasant and portable, but
it does not remove the need for a final mailbox. Use it only if a domain and
mail routing already exist; forward it to the same dedicated Gmail. Do not buy
or configure a domain solely for this MVP.

### GCP worker or webhook

Not needed. Gmail can be polled locally. Gmail push adds Cloud Pub/Sub, while a
public webhook adds hosting and another privacy boundary. Career Hub's first
live connector will make bounded local pulls instead.

## Part 1: Create The Dedicated Gmail

1. Create a new personal Gmail account with a non-sensitive job-alert name.
2. Give it a unique password not used by another account.
3. Add a recovery email/phone controlled by you.
4. Enable two-step verification.
5. Do not place resumes, personal correspondence, or Drive files in it.
6. Keep Spam filtering enabled; Career Hub will not poll Spam by default.
7. Record only the email address in your private setup notes. Never place the
   password, recovery codes, or future App Password in this repository.

For personal Gmail, IMAP access is always enabled as of January 2025; there is
normally no separate **Enable IMAP** toggle to change. See
[Google's current Gmail client guidance](https://support.google.com/mail/answer/7126229).

## Part 2: Route Alerts To It

Use the least disruptive option for each source.

### Option A: Separate alert destination

If a job board has a distinct notification/alert email setting, enter the
dedicated Gmail there.

Do not replace the primary account or recruiter/contact email merely to route
alerts. Some boards send recruiter messages, security notices, and recovery
mail to the primary address; those must remain visible in the normal mailbox.

### Option B: Selective forwarding from an existing Gmail

First authorize the forwarding destination:

1. Open the existing personal Gmail on a desktop browser.
2. Select **Settings > See all settings**.
3. Open **Forwarding and POP/IMAP** (or **Forwarding**).
4. Select **Add a forwarding address**.
5. Enter the dedicated Gmail address and confirm.
6. Open the verification message in the dedicated account and follow its link.
7. Return to the personal account and refresh Settings.
8. Leave global **Forward a copy of incoming mail** disabled. Career Hub needs
   only source-specific filters, not the whole inbox.

Then create one filter per alert source:

1. Open a representative alert message.
2. Use **More > Filter messages like these**, or open Gmail's advanced search.
3. Copy the exact `From` address from that alert. Add a stable subject phrase
   only when it helps distinguish job alerts from recruiter/account messages.
4. Run **Search** before saving and inspect every matching message.
5. Narrow the query if it includes recruiter replies, login/security mail, or
   unrelated notifications.
6. Select **Create filter**.
7. Enable **Forward it to** and select the dedicated Gmail.
8. Optionally apply a local label such as `Career Hub forwarded` for audit.
9. Do not enable Delete, Mark as read, or broad forwarding.
10. Repeat for each job-alert source.

Gmail documents destination verification and sender/filter-specific forwarding
in its [automatic forwarding guide](https://support.google.com/mail/answer/10957)
and [filter guide](https://support.google.com/mail/answer/6579). Filters affect
new messages; they are not a historical import mechanism. Gmail does not
automatically forward messages classified as spam.

### Source inventory

Keep a private setup table while configuring filters:

| Source | Exact alert sender | Subject discriminator | Route | Test received |
| --- | --- | --- | --- | --- |
| Example board | Copy from a real alert | Optional exact phrase | Direct or forwarded | Yes/No |

Do not commit a table containing a real personal address or private message
subject. Career Hub will later store approved sender policy in the private Data
Home.

## Part 3: Test Routing Before Career Hub

1. Trigger a new saved-search alert or wait for the next scheduled alert.
2. Confirm the alert still exists in the original mailbox when forwarding is
   used.
3. Confirm exactly one copy arrives in the dedicated Gmail.
4. Confirm a recruiter/account-security message from the same company does not
   match the filter.
5. Open the forwarded message and verify that its job links still work.
6. Record the actual alert `From` address and any stable subject discriminator.

Do this for at least two representative sources before enabling an automated
connector. Do not use an old message to validate the forwarding filter because
Gmail forwarding filters apply to new mail.

## Part 4: Career Hub Connection

The connection screen requests:

- provider: Gmail;
- dedicated account address;
- folder: normally `INBOX`;
- App Password entered directly into Windows Credential Manager;
- approved exact sender addresses/domains;
- initial schedule: **Manual only**; and
- a separate **Sync now** action that tests Gmail after the local settings save.

Without a Google OAuth project:

1. Confirm two-step verification is enabled on the dedicated account.
2. Open Google Account **App Passwords**.
3. Create one password named `Career Hub`.
4. Enter it once in the Career Hub connection screen.
5. Select **Save settings**. This stores the connection locally; it does not
   contact Gmail. Select **Sync now** afterward to validate the App Password
   and import approved alerts.
6. Do not save it in a text file, browser note, environment variable, YAML,
   command line, screenshot, or Git.
7. Use **Disconnect** in Career Hub to temporarily stop sync while keeping the
   local setup for the same mailbox. Use **Forget email setup** only when you
   want to remove that local setup entirely; it keeps existing Job Feed cards,
   but reconnecting requires completing the mailbox setup again. Revoke the App
   Password in Google when the connector is no longer needed.

Google states that App Passwords are not recommended when Google Sign-In is
available, require two-step verification, are revoked after a main-password
change, and may be unavailable for organization-managed, security-key-only, or
Advanced Protection accounts. See
[Google Account App Password guidance](https://support.google.com/accounts/answer/185833).

If **App Passwords** is unavailable, do not weaken account security. Continue
with explicit `.eml` import until a local OAuth connector is separately
approved.

## What Career Hub Will Read

The connector will:

1. select only the configured folder;
2. fetch UID, `Message-ID`, `From`, `Subject`, and `Date` for unseen messages;
3. compare the sender with the private allowlist and its explicit route:
   **Job opportunities**, **Application tracking**, or **Both streams**;
4. fetch the MIME body using a non-mutating operation only for an allowed
   sender;
5. ignore attachments, recipients, tracking pixels, unsubscribe/account links,
   and non-job links;
6. create or update opportunity discoveries only for a job-enabled sender
   route, and record explicit application outcomes only for a tracking-enabled
   route;
7. update an existing saved application only when a supported outcome carries
   the same exact provider posting id, otherwise retain an unlinked status
   update and keep it out of the active Feed; and
8. write only counts, timings, and stable error codes to diagnostics.

For recognized providers, Feed records also retain a normalized provider name.
LinkedIn digest links are grouped by numeric posting id, service/navigation
links are ignored, and the stored posting URL is reduced to the query-free
`https://www.linkedin.com/jobs/view/<id>/` form. Personal tracking and login
query parameters are not kept.

The parser rejects an email rather than performing unbounded work: one message
may contain at most 500 visible HTML links, 50,000 bounded HTML relationship
references, and 100 extracted job candidates. These budgets are enforced while
HTML is parsed, before multiplicative bookkeeping can accumulate.
It never silently truncates either collection.

It will not mark read, move, delete, reply, forward, send mail, inspect Drive,
or connect to SMTP.

### Mixed LinkedIn application updates

A LinkedIn application-result email can contain two different entity types:
the status of an application already submitted and separate jobs recommended
for future application. Career Hub keeps those boundaries explicit.

- A supported rejection requires the LinkedIn sender, the provider's
  application-result Subject form, the explicit `will not be moving forward
  with your application` wording, and one exact numeric LinkedIn posting id.
- The rejected posting does not become an active opportunity todo. If an
  unpromoted Feed item with the same posting id already exists, it is moved to
  the dismissed view.
- Other job cards in the same email remain normal **Needs JD** opportunities.
- An existing saved application with that exact posting id may move to
  **Rejected**. **Offer**, **Withdrawn**, and **Closed** are never overwritten;
  the update is shown as needing review instead.
- When no saved application has that posting id, Career Hub does not invent a
  job, JD, or application. The minimized update remains under **Applications >
  Email tracking > Application updates** as unlinked.
- Reimporting or resyncing the same message does not append another rejection
  event.

Only the provider, posting id, normalized title/company/location, status,
timestamp, resolution, and hashed message identity are retained. The original
email and rejection paragraph are not stored.

### Mixed Handshake application confirmations

The supported Handshake submission message is also treated as one transport
envelope with independent outputs:

- The exact `handshake@notifications.joinhandshake.com` sender, supported
  `Application sent to <company> - here's what's next` Subject form, and
  matching `Your application was sent to <company>` heading produce one
  **Applied** event.
- Handshake supplies only the company in this confirmation. Career Hub never
  guesses a role, creates an application, or links by company alone. The event
  remains under **Applications > Email tracking > Application updates** until
  the user optionally links it to an existing application.
- Explicit linking is revision-guarded and reuses the normal application
  transition rules. It cannot downgrade **Interview** or **Offer**, and a
  closed application is reported as a conflict instead of being overwritten.
- Cards beneath `Apply to similar jobs` are separate **Needs JD** opportunities
  only when the sender route also enables included job suggestions.
- `View application`, preferences, unsubscribe, generic notifications, and
  messages do not create jobs or contacts.

The application redirect token, original body, and service-link text are not
retained. Reimporting or resyncing the same message produces neither a second
event nor duplicate recommendations.

Unknown senders appear under **Job Feed > Email > New email senders** using
selected headers only. Their bodies are not automatically downloaded. The user
selects **Job opportunities only**, **Application updates only**, or **Updates
+ included job suggestions**, then chooses **Allow & sync** or **Ignore**. The
last label is stored as the `both` sender policy. A known application-
notification sender or deterministic application-status Subject may be
suggested for tracking, but the suggestion never approves it automatically.

During the version-20 local migration, an already-approved exact
`handshake@notifications.joinhandshake.com` rule is routed to **Application
updates only**. Other legacy sender rules use **Updates + included job
suggestions** so an update does not silently discard either kind of result;
review those routes in Email configuration after restarting Career Hub.

## How Messages Become Todos

Email is one producer feeding the shared **Job Feed**. The user-facing page has
`Feed · Email · Watchers` modes: Feed owns the unified job list, Email owns this
connection/sender/sync configuration, and Watchers remains a bounded
`Coming soon` setup placeholder. Email never owns a second list of jobs.

- An unambiguous job block always creates at most one deduplicated discovery.
- An explicit supported application outcome is not a job block and never
  appears as a new active todo.
- The Feed source column distinguishes the concrete provider, such as
  **LinkedIn**, from its transport, such as **Email**.
- `great match`, `top match`, or similar wording is preserved as
  **Recommended by source**. It is not a Career Hub assessment.
- A link/snippet todo enters **Needs JD**.
- The guarded local resolver route remains implemented and tested, but it is
  not offered as a Job Feed control. Open the retained posting and use attended
  extension capture; the result remains unverified until the user confirms the
  complete JD.
- A logged-in/protected link is completed by the explicit browser extension.
- Only a complete JD receives the existing deterministic assessment and can
  enter **Review first**, **Review**, or **Blocked**.

Initial hard blockers are limited to explicit, profile-incompatible:

- citizenship/work authorization/sponsorship requirements;
- required active clearance; and
- comparable minimum experience requirements such as `10+ years`.

`U.S. person` is retained exactly rather than rewritten as `U.S. citizen`.
Location, on-site, hybrid, and relocation are filterable planning facts, never
hard blockers for this user. Missing skills affect coverage but do not block.

## Scheduled Operation

Enable automation only after **Sync now** behaves correctly:

1. Run manual sync for the first few days.
2. Enable sync on Career Hub startup.
3. Optionally use the proposed 30-minute interval while the app is running.
4. Later install a one-shot Windows Task Scheduler job, for example at 06:00
   local, under the same Windows account that owns the stored credential.
5. A startup sync catches an alert missed because the computer was asleep or
   offline.

There is no tray daemon, hidden browser, GCP worker, or always-running process.

## Troubleshooting

### Nothing is forwarded

- Confirm the destination verification link was completed.
- Confirm global forwarding is off but the specific filter has **Forward it
  to** enabled.
- Test the filter search against a new representative message.
- Check whether the sender address changed.
- Check Spam; Gmail does not automatically forward spam.

### Too many unrelated messages arrive

- Replace a sender domain with an exact sender address.
- Add a stable subject discriminator.
- Split one broad filter into one filter per source.
- Never compensate by teaching Career Hub to scan and classify the whole
  personal inbox.

### App Passwords is missing

- Confirm two-step verification is enabled.
- Check whether the account is organization-managed, uses security-key-only
  verification, or has Advanced Protection.
- Do not disable security controls. Use `.eml` import and defer live sync.

### Connection times out, hangs, or incorrect credentials entered

- When **Sync now** is active, the button transitions to **Stop waiting**. This
  ends the browser wait; the already-started local mailbox check may still
  finish, so refresh its status before starting another sync.
- If saving settings while a sync is running, the server responds promptly with
  `EMAIL_SYNC_ALREADY_RUNNING` rather than hanging the thread or freezing the UI.
- Connection saving has a 10-second local-service safety limit and a separate
  15-second browser-response limit. Either limit restores form interactivity;
  neither result claims Gmail authentication failed because Gmail is tested by
  **Sync now**, not by **Save settings**.
- A server-side save or sync attempt writes a content-minimized Diagnostics run
  and returns its run ID with a stable error code. Credential checkpoints say
  only whether protected storage started, completed, or failed; they never log
  the mailbox address, sender list, or App Password.
- `EMAIL_CONNECTION_SAVE_TIMEOUT` means the local protected-store operation did
  not finish in ten seconds. Restart Career Hub before retrying so the previous
  native operation cannot overlap the next attempt, then use the displayed
  **Open Diagnostics** action. `EMAIL_CONNECTION_SAVE_RESPONSE_TIMEOUT` means
  only that the browser did not receive a response within fifteen seconds. The
  renderer adds a content-minimized fallback event under that run ID even when
  the request could not reach the local service. Restart Career Hub, inspect the
  saved connection state, and only then retry so an unconfirmed late save is
  not mistaken for a rejected save.
- Gmail authentication and network failures from **Sync now** are recorded as
  `EMAIL_SYNC_AUTH_FAILED` or `EMAIL_SYNC_NETWORK_FAILED` under the same run ID.
  Existing Feed cards and application history remain unchanged.
- A completed Gmail connection can still have message-level import issues. In
  that case the header says **Connected · import issues**, the result says
  **Sync incomplete**, and Diagnostics records `success_with_warnings` with
  aggregate counters and stable failure-code counts. It never stores the
  sender, Subject, body, or credential in that event. A retryable message stays
  in the dedup receipt queue, but it does not prevent newer message UIDs from
  being checked during the same bounded sync.
- `EMAIL_SYNC_MESSAGE_RETRY` means at least one message remains queued for a
  later import attempt. `EMAIL_SYNC_MESSAGE_SKIPPED` means a message reached its
  bounded retry limit (or a non-retryable size limit); other messages were
  still processed. Use the underlying `failure_code_counts` in Diagnostics to
  report parser or local-persistence defects without sharing the email itself.

### An alert says “great match” but has no JD

This is expected. The source recommendation creates a **Needs JD** todo. It is
not promoted to a high-confidence Career Hub match until the full posting is
captured and assessed.

### A rejection email also recommends jobs

The rejected application should appear under **Applications > Email tracking >
Application updates**, not in Email configuration and not as an active
opportunity. Recommended job cards from the same message stay in Feed when that
sender is routed to **Both streams**. If the update says **Not linked**, Career
Hub had no saved application with the exact provider posting id; it
intentionally did not create one from a snippet-only email.

### A Handshake submission email also recommends jobs

The submission appears under **Applications > Email tracking > Application
updates**. It remains unlinked when the email names only a company; use **Link
to an existing application** only when the correct saved application is clear.
With **Application updates only**, the recommendations are intentionally
ignored. Change the sender to **Updates + included job suggestions** when those
cards should also enter Feed. Generic Handshake messages remain quiet.

## Pre-Connection Checklist

- [ ] Dedicated account contains job alerts only.
- [ ] Unique password, recovery method, and two-step verification are enabled.
- [ ] Whole-inbox forwarding is disabled.
- [ ] Every forwarding filter was tested against actual search results.
- [ ] Recruiter/security messages are excluded.
- [ ] At least two new alerts reached the dedicated account successfully.
- [ ] Exact sender inventory exists privately and each sender has an explicit
      job, tracking, or both-streams route.
- [ ] No secret or personal message content was added to the repository.
- [ ] `.eml` parser and import acceptance tests pass before live connection.

## Current Implementation Status

- Guide and product contract: documented.
- `.eml` parser: implemented and covered by synthetic fixtures.
- Discovery storage/domain rules: implemented as a foundation.
- Explicit `.eml` import: implemented in **Job Feed > Email**.
- Job Feed: Feed, Email, and Watchers modes are available; explicit promotion
  is implemented for confirmed-complete discoveries, while email link/snippet
  items correctly remain **Needs JD**.
- Gmail connection: implemented.
- Startup/interval/one-shot scheduling: implemented and opt-in.
- Unknown-sender suggestions: implemented as advisory only.
- Email hardening: implemented with bounded HTML-link
  work, exact suppression of observed Handshake/SuccessFactors navigation
  labels on opaque tracking URLs, and transaction/source-preserving LinkedIn
  repair regressions.
- Mixed LinkedIn outcomes: explicit rejection is separated from
  same-email recommendations, exact saved applications update conservatively,
  and unlinked outcomes remain visible without creating a JD/application.
- Mixed Handshake confirmations: exact submission evidence creates
  an unlinked **Applied** event, optional linking targets an existing
  application with revision protection, and embedded recommendations follow
  the sender route independently.
- Live evidence: an allowed LinkedIn digest produced provider-attributed Feed
  cards, removed service-link noise, and merged duplicate anchors by posting id.
- Source watchers: **Coming soon** producer capability.
- Browser extension capture and attended application support: implemented; the
  extension remains a manual, user-triggered producer rather than an automatic
  watcher.
- Public link resolver: the guarded local resolver route is implemented and
  tested but not offered as a Job Feed control. It is not a Feed origin or
  configuration mode; the visible workflow is **Open posting** followed by
  attended extension capture.
