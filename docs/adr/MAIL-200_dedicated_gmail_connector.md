# MAIL-200: Dedicated Gmail Connector For Private Dogfood

- **Status:** Accepted and live-activated for private dogfood
- **Decision date:** 2026-08-14
- **Scope:** one local, single-user, alert-only Gmail mailbox
- **Supersedes:** nothing
- **Implementation evidence:** `MAIL-210`, `MAIL-220`, and `MAIL-230` pass
  against synthetic mailboxes. On 2026-08-15, a user-approved live smoke verified Gmail
  authentication, header-only quarantine of two unknown service senders,
  repeat-run deduplication, and startup sync at the saved interval. No allowed
  real job-alert message was present in the bounded lookback window.

## Context

Career Hub already imports bounded `.eml` files into the shared Discovery
Inbox. The requested next step is a local pull worker that runs only while the
application is open, with an explicit **Sync now** action. It must not require a
GCP worker, public webhook, cloud queue, continuously running daemon, or access
to a personal mailbox.

Google's current provider constraints rule out a deceptively lightweight OAuth
option:

- personal Gmail keeps IMAP enabled, but Google recommends Sign in with Google
  rather than sharing the main account password;
- Gmail IMAP OAuth requires the full `https://mail.google.com/` scope;
- the narrower Gmail API `gmail.readonly` scope is still classified as
  restricted; and
- an installed-app OAuth flow requires a Google Cloud project, registered
  client, system-browser consent, loopback redirect, refresh-token lifecycle,
  and public-app verification work when the personal/testing exception no
  longer applies.

An App Password is also broad: it is neither read-only nor folder-scoped. The
only acceptable reason to use it here is that the mailbox is a dedicated,
low-impact alert sink containing no personal mail, recruiter correspondence,
resumes, Drive documents, or account-recovery material.

## Decision

For private dogfood, use Python's standard-library `imaplib.IMAP4_SSL` with
`ssl.create_default_context()`, Gmail host `imap.gmail.com`, port `993`, and an
App Password stored only through `src.secret_store.SecretStore`.

This is an implementation decision, not evidence that the selected account is
eligible. Live activation remains disabled until the user, while signed into
the dedicated personal Gmail account, confirms that the official **App
Passwords** page is available and completes a successful **Sync now**.
The attempted read-only eligibility check during `MAIL-200` reached an
organization-managed sign-in boundary, so account-specific availability was
not inferred or recorded.

If the App Password page is unavailable, the account is organization-managed,
or the user rejects the broad credential, Career Hub keeps `.eml` import as the
supported workflow. OAuth requires a separate ADR and explicit provider
registration approval.

No new Python dependency and no Google Cloud/provider registration are accepted
by this ADR. `MAIL-210` may implement the standard-library adapter and test it
against fakes. A real network connection occurs only after the user enters an
App Password into the local connection screen and invokes **Sync now**, or
explicitly enables the local schedule.

## Eligibility Gate

All conditions must be true before live sync can be enabled:

1. The account is a separate personal Gmail account used only for job alerts.
2. Unique password, recovery method, and two-step verification are enabled.
3. The user can open Google's App Passwords page for that exact account.
4. The generated credential is named `Career Hub` and is entered once into the
   local connection screen; the main Google password is never accepted.
5. At least one exact sender rule is enabled.
6. A read-only `INBOX` test succeeds without changing flags or mailbox state.

The UI reports this gate as `Not configured`, `Needs attention`, or `Ready`.
It must never report `Ready` merely because an address and sender rules exist.

## Alternatives

| Option | Authentication and access | Dependencies / packaging | Refresh and revocation | Failure behavior | Decision |
| --- | --- | --- | --- | --- | --- |
| Gmail IMAP + dedicated-account App Password | App Password authorizes broad mailbox access; Career Hub enforces read-only behavior and selects only `INBOX` | Python standard library only; no Cloud project | No refresh token; Google revokes the App Password explicitly and may revoke it after a main-password change | Authentication failure becomes `Needs attention`; `.eml` import remains available | Selected for private dogfood only |
| Gmail API + local OAuth | `gmail.readonly` is narrower than full IMAP OAuth but is still a restricted scope | Cloud project, OAuth client/consent setup, token handling, and maintained auth/API packages | Offline refresh token plus provider revocation and re-consent paths | Testing warnings, token expiry/revocation, consent or admin-policy errors | Deferred until public-distribution UX and verification justify it |
| IMAP + OAuth XOAUTH2 | Requires full `https://mail.google.com/` scope despite read-only product intent | Cloud project, OAuth client, token handling; IMAP remains | Same OAuth refresh/revocation lifecycle | Broad-scope consent and verification burden without a narrower permission | Rejected |
| Explicit `.eml` import | No mailbox credential or provider registration | Already implemented | None | User exports selected messages manually | Permanent fallback |

The selection optimizes for the current private, local-first boundary rather
than technical novelty. It must not become the default authorization model of a
publicly distributed Career Hub without a new decision.

## Secret Contract

The sole credential is stored under this logical key:

```text
job-discovery.gmail-imap.primary.v1
```

`SecretStore` therefore creates this channel-scoped Windows Credential Manager
target:

```text
CareerHub/<channel>/provider/job-discovery.gmail-imap.primary.v1
```

The credential value contains only the App Password. The account address,
folder, sender rules, cursor, and status never share that credential. The
secret is never written to SQLite, YAML, environment variables, command-line
arguments, browser storage, logs, diagnostics, backups, screenshots, or Git.
There is no API that lists stored provider secrets.

## Non-secret Private State

`MAIL-210` and `MAIL-220` store the following in the existing private
`applications/career_hub.sqlite3`.

### `job_email_connections`

- `connection_id`: fixed MVP value `primary`;
- `provider`: fixed value `gmail_imap`;
- `account_address`: normalized dedicated-mailbox address;
- `folder`: default and initial allowlisted value `INBOX`;
- `enabled`: explicit local switch;
- `schedule_enabled`: explicit opt-in, default `false`;
- `interval_minutes`: user controlled and bounded to `15..1440`, default `30`;
- `lookback_days`: current fixed value `14`;
- `max_messages_per_sync`: current fixed value `250`;
- `revision`, `created_at`, and `updated_at`.

Host and port are provider constants, not user-editable configuration. Account
addresses are private data: they may appear in the connection UI and private
database, but never in diagnostics or repository files.

### `job_email_sender_rules`

- composite key `(connection_id, sender_address)`;
- `sender_address`: normalized exact address only;
- `disposition`: `allowed` or `disabled`;
- `purpose`: `job_alerts`, `application_tracking`, or `both`;
- `created_at` and `updated_at`.

Subject text is optional parser input, not a required sender-rule field. Domain
rules are deferred because one compromised or multi-purpose domain would make
the initial allowlist too broad.

### `job_email_sync_state`

- `connection_id` and `folder`;
- `uidvalidity`;
- `last_committed_uid`;
- `last_started_at` and `last_completed_at`;
- `consecutive_failures` and `next_retry_at`;
- stable `last_result` and `last_error_code` values;
- short-lived `lease_owner` and `lease_expires_at` for cross-process exclusion.

### `job_email_message_receipts`

- `connection_id`, `folder`, `uidvalidity`, and `uid` as the transport identity;
- normalized sender address, local first-seen timestamp, and normalized
  header-sent date when valid;
- hashed `Message-ID` and whole-message SHA-256 fallback;
- boolean job-alert, application-status, and service/security Subject signals;
  and
- processing state, candidate count, stable error code, and processed timestamp.

No raw Subject, MIME body, attachment, raw URL, or original email is retained
in this receipt table. Subject text exists only while parsing the bounded
header response. Receipt metadata remains across disconnects because it is the
first deduplication boundary.

## Read-only Protocol Boundary

The live adapter has an explicit command allowlist:

1. establish `IMAP4_SSL` with a default validated TLS context and bounded
   connect/read timeout;
2. authenticate with the dedicated address and App Password;
3. select `INBOX` with `readonly=True` so the server uses `EXAMINE` semantics;
4. use `UID SEARCH`, never mutable message sequence numbers;
5. fetch `UID`, `RFC822.SIZE`, and bounded headers with
   `BODY.PEEK[HEADER.FIELDS (MESSAGE-ID FROM DATE SUBJECT)]`;
6. fetch `BODY.PEEK[]` only for an allowed sender and only after the declared
   message-size limit passes; and
7. use only capability/no-op/logout operations around that flow.

`STORE`, `COPY`, `MOVE`, `EXPUNGE`, `APPEND`, mailbox create/delete/rename,
flag mutation, reply, forward, and SMTP are forbidden. `BODY.PEEK` is required
because ordinary body retrieval may set `\Seen`.

The first implementation keeps the existing `.eml` structural limits: at most
10 MiB per message, 200 MIME parts, 10 nested message levels, 500,000 decoded
text characters, 500 HTML links, 50,000 parser relationship references, and
100 candidates. Initial sync is also bounded to 14 days
and 250 messages. Exceeding a bound creates a stable receipt outcome, not a
partial hidden import.

## Sender Policy And Filtering

Headers are fetched before the body. The normalized `From` address is compared
with enabled exact-address rules:

- **Allowed / Job opportunities:** fetch the bounded body, pass the bytes to
  the existing email parser, and merge candidates into Discovery storage while
  ignoring application-status entities.
- **Allowed / Application tracking:** fetch the bounded body, retain only
  supported minimized status entities, and never create a Discovery.
- **Allowed / Both streams:** preserve both independently when one message
  contains recommendations and a supported status update.
- **Unknown:** store an `unknown_sender` receipt with sender plus bounded,
  derived header signals, advance only after that receipt commits, and do not
  fetch the body or create discoveries. The Email UI can approve or disable
  that sender; approval queues the retained UID for a later bounded fetch.
- **Disabled:** retain a header-only `unknown_sender` receipt but omit that
  address from the review queue; do not fetch the body.

Mailbox sync does not perform role relevance inference, numeric fit scoring,
skill writes, or title-based rejection. Allowed alert candidates enter the one
Job Feed. Link-only candidates remain **Needs JD**; blocker assessment waits for
a confirmed complete JD.

Unknown senders receive advisory ordering only:

- **Suggested job alert** appears when a bounded sender token/provider or a
  deterministic Subject phrase signals job alerts.
- **Suggested application tracking** appears for a bounded known status sender
  or deterministic application-status Subject phrase.
- **Recurring — review** appears only after at least four messages on three
  distinct valid sent dates across at least 14 days.
- A service/security Subject signal suppresses both recommendations.

The UI exposes fixed reason codes translated into short explanations, never
the Subject itself. A recommendation never creates a sender rule, fetches a
body, imports a discovery, or replaces the explicit **Allow & sync** action.

## Cursor And Deduplication

Cursor progression is durable and retry-safe per message:

1. Read the mailbox's `UIDVALIDITY` and compare it with local state.
2. Search UIDs greater than `last_committed_uid`, within the bounded initial or
   recovery window.
3. Persist discoveries through the existing idempotent Discovery transaction.
4. Persist the receipt and advance `last_uid` together only after the Discovery
   write succeeds. If this second transaction fails, the retry reuses the same
   Discovery identity rather than duplicating the job.

Deduplication is layered and never fuzzy:

1. `(connection_id, folder, UIDVALIDITY, UID)` prevents transport retries;
2. normalized hashed `Message-ID` prevents exact message reprocessing;
3. whole-message SHA-256 is the fallback when `Message-ID` is absent; and
4. existing Discovery posting identity, canonical URL, and content fingerprint
   merge the same job repeated by forwarded or later alert messages.

If `UIDVALIDITY` changes, the previous UID cursor is invalid. The worker records
the new value, performs a 14-day/250-message bounded rescan, and relies on the
message hashes and Discovery identities. It never trusts an old UID under a new
`UIDVALIDITY`.

A parsing or storage error must first become a durable retryable receipt; it
cannot silently move the cursor past an unrecorded message. An in-process lock
plus an expiring SQLite lease prevents app tabs, Task Scheduler, interval, and
**Sync now** overlap.

## Local Scheduling

Scheduling is opt-in and remains disabled after install, update, migration, or
initial connection setup unless the user checks **Sync while Career Hub is
open**. When enabled, the browser requests one scheduled sync at application
startup and repeats at the saved 15-to-1440-minute interval. The default choice
is 30 minutes. Scheduled failures use exponential 5-to-60-minute backoff;
manual **Sync now** bypasses that backoff but never bypasses a live lease.

The same configured connector has a non-interactive one-shot entry point:

```powershell
python -m src.email_sync sync-once --data-root C:\path\to\CareerHubData\main --channel stable
```

The command accepts no credential argument, opens no browser, performs one
bounded scheduled run, emits sanitized JSON, and exits. It is suitable for one
user-created Windows Task Scheduler action running under the same Windows
account. It exits successfully without mailbox access when scheduling is off,
backoff is active, or another valid lease owns the run.

## Disconnect, Rescan, Forget, And Retention

- **Disconnect** disables sync and deletes the App Password from Credential
  Manager. It retains account configuration, sender rules, cursor, receipts,
  discoveries, saved jobs, and applications so reconnect can resume safely.
- **Rescan recent email** requires an explicit confirmation. It clears only
  the stored mailbox position (`UIDVALIDITY` and `last_committed_uid`) so the
  next sync rechecks the bounded 14-day/250-message window. Existing receipts
  keep the scan idempotent.
- **Forget email setup** removes the App Password plus the local mailbox
  connection, sender rules, sync state, and receipts after destructive
  confirmation. It retains discoveries, jobs, applications, and provenance;
  using email sync again requires the full mailbox setup.
- Original email bytes and attachments are never retained. Extracted discovery
  text follows the existing Discovery retention rules. Non-content receipts are
  retained while the connection exists and are included in normal private Data
  Home backup; the Credential Manager secret is not.

Revoking the App Password in Google is the authoritative remote disconnect.
Career Hub must explain that local deletion alone cannot prove remote
revocation.

## Failure Contract

- Bad or revoked credential: `EMAIL_SYNC_AUTH_FAILED`; stop the run without changing the
  cursor and ask for reconnect.
- Offline, DNS, TLS, timeout, or temporary server failure: stable transient
  code, bounded retry/backoff, no cursor advance beyond committed receipts.
- Missing or non-read-only folder: `EMAIL_SYNC_PROTOCOL_FAILED`; no fallback to another
  folder or whole-mailbox scan.
- UIDVALIDITY change: bounded recovery rescan, not a full historical scan.
- Oversized/structurally invalid message: durable per-message result; valid
  siblings continue.
- Missing, invalid, or inaccessible Data Home in the one-shot worker:
  `EMAIL_SYNC_DATA_HOME_UNAVAILABLE`; emit sanitized JSON without a traceback
  or local path.
- Local database/storage failure after a run starts: abort before cursor
  commit; retry later.

Diagnostics contain only result codes, counts, duration, retry time, and a
generated run id. They never contain address, sender, subject, body, URL, UID,
Message-ID, token, or App Password.

## Consequences

### Positive

- No hosted service, Google Cloud project, OAuth client, or new dependency.
- The worker can run manually, at startup and bounded intervals while Career
  Hub is open, or through the same one-shot CLI in Windows Task Scheduler.
- Existing email parser and Discovery deduplication remain the only ingestion
  path.
- Credential storage, channel separation, disconnect, scheduling, and recovery
  behavior are explicit and synthetic-verified before live activation.

### Negative

- The App Password is broader than Career Hub's read-only behavior.
- Availability depends on Google account security and organization policy.
- The design is intentionally unsuitable as the default for a public multi-user
  release.
- Unknown senders require one manual approval before body retrieval.

## Reversal Conditions

Replace this decision with `.eml`-only or a separately approved OAuth connector
when any of these becomes true:

- App Passwords are unavailable or deprecated;
- the dedicated mailbox contains personal or application correspondence;
- Career Hub is distributed to users who are not personally known;
- Google policy or scopes materially change;
- cross-platform secret storage becomes a release requirement; or
- a measured OAuth implementation produces materially better permission and
  onboarding UX without disproportionate verification/packaging cost.

## Authoritative References

- [Google: Add Gmail to another email client](https://support.google.com/mail/answer/7126229?hl=en)
- [Google: Sign in with App Passwords](https://support.google.com/accounts/answer/185833?hl=en)
- [Google: OAuth 2.0 mechanism for Gmail IMAP](https://developers.google.com/workspace/gmail/imap/xoauth2-protocol)
- [Google: Choose Gmail API scopes](https://developers.google.com/workspace/gmail/api/auth/scopes)
- [Google: OAuth for desktop apps](https://developers.google.com/identity/protocols/oauth2/native-app)
- [Google: Restricted-scope verification](https://developers.google.com/identity/protocols/oauth2/production-readiness/restricted-scope-verification)
- [Python: `imaplib`](https://docs.python.org/3/library/imaplib.html)
- [RFC 9051: IMAP4rev2](https://www.rfc-editor.org/rfc/rfc9051.html)
