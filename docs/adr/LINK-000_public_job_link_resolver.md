# LINK-000: Attended Public Job-Link Resolver

- **Status:** Backend implemented and accepted (`LINK-130`, 2026-08-23); Feed
  action withdrawn on 2026-09-01
- **Decision date:** 2026-08-23
- **Scope:** one explicit enrichment attempt for one existing local Discovery
- **Capability:** `CAP-JOB-LINK-RESOLUTION`
- **Supersedes:** the undetailed resolver placeholder in
  `docs/product/job_discovery_capture_plan.md`

## Outcome

The guarded local route may retrieve one active link-only Discovery's already
retained public corporate/ATS URL once, extract one bounded job description,
and attach the result to the same Discovery as unverified `page_visible`
content. The current Job Feed deliberately does not expose that route: its
primary action is **Open posting**, followed by attended extension capture.

Resolution is an enricher, not a producer. It never creates a second Feed,
silently creates a Job/Application, confirms completeness, writes Skills or
Career Vault facts, searches the web, or becomes a recurring watcher.

The safe fallback is a normal product result:

```text
guarded local resolver route
  -> complete-looking single-job source -> Review capture
  -> protected / ambiguous / unsupported / unavailable -> no change

Job Feed
  -> Open posting -> attended browser-extension capture
```

## Context

Email alerts often provide a title, company, and URL but not the complete JD.
Career Hub already stores those records as `link_only`, supports attended
extension capture, and requires explicit complete-JD confirmation before
promotion. A narrow public resolver removes repetitive opening and copying for
ordinary corporate and ATS pages while preserving those gates.

A generic `urlopen()` implementation would create a serious SSRF boundary.
The URL and every redirect are remote-controlled; DNS may resolve to local or
private addresses or change between validation and connection. The resolver
therefore requires address validation and connection pinning, not only URL
syntax checks.

## Decision

Implement a synchronous, single-attempt standard-library resolver behind one
browser-only mutation route. The `ThreadingHTTPServer` keeps the fetch off other
request threads, while one resolver lock prevents concurrent outbound jobs in
this single-user release.

The production transport will use `socket`, `ssl`, and `http.client` with a
small pinned-connection adapter. It must connect to an already validated socket
address and, for HTTPS, retain the original DNS hostname for SNI and certificate
hostname verification. It must not call `urllib.request.urlopen()`, inherit
system proxy settings, or perform a second hostname lookup during connect.

No new dependency is accepted. The installed project already targets Python
3.13, whose `ipaddress.is_global` behavior covers IPv4-mapped IPv6 semantics.

## User And API Contract

The action is available only when all are true:

- the Discovery is `inbox` or `shortlisted`;
- its content scope is `link_only`;
- it has one retained absolute HTTP/HTTPS URL;
- it is not already enriched by `public_resolver`; and
- no resolution is currently running.

The browser calls:

```text
POST /api/discoveries/<discovery-id>/resolve-public-link
```

with exactly:

```json
{
  "contract_version": 1,
  "expected_revision": 3
}
```

There is deliberately no request-body URL, method, header, selector, cookie,
credential, or parser rule. The server reloads the retained URL by Discovery
id. The route uses the normal browser capability and same-origin boundary; it
is not added to either extension capability allowlist.

The server verifies the revision before network access and again before
persisting. A stale result never replaces a newer email/extension capture.
Expected failures return one stable public code and a short recovery message;
the UI keeps **Open posting** available and does not mutate the Discovery.

## URL Eligibility

Each initial URL and redirect target must satisfy all rules independently:

1. Maximum serialized length: 4,096 characters; no control characters,
   whitespace, backslashes, or fragments in the request target.
2. Scheme is exactly `http` or `https`.
3. No username, password, or empty hostname.
4. DNS hostname only. Literal IPv4/IPv6 targets are rejected for this product
   capability even when globally routable.
5. Hostname is normalized with IDNA, is at most 253 ASCII characters, and has
   valid non-empty labels.
6. Port is the scheme default: 80 for HTTP or 443 for HTTPS.
7. Known protected/login-first job-board domains remain ineligible. This is a
   product-scope guard, not the SSRF defense. The attended extension handles
   LinkedIn, Handshake, and similar authenticated surfaces.
8. HTTPS may redirect to HTTPS. An initial HTTP link may upgrade to HTTPS, but
   HTTPS-to-HTTP downgrade is rejected.

Unknown query parameters are retained because they may carry the posting id.
Fragments and known tracking parameters follow the existing conservative
Discovery canonicalization contract. `file:`, `data:`, `ftp:`, `gopher:`,
UNC paths, browser-internal schemes, and custom protocols are never supported.

## DNS And Connection Boundary

For every hop:

1. Resolve A and AAAA candidates with `socket.getaddrinfo()` for the exact
   normalized hostname and default port.
2. Reject no-answer results and more than 16 unique socket addresses.
3. Parse every returned address with `ipaddress.ip_address()`.
4. Require every address to be globally reachable. Explicitly reject
   loopback, private, link-local, multicast, unspecified, reserved,
   non-global/shared space, and non-global IPv4-mapped IPv6 addresses.
5. Retain the validated address family and socket address. Try at most four
   validated addresses; do not resolve the hostname again during connection.
6. For HTTPS, wrap that connected socket with `ssl.create_default_context()`
   and `server_hostname=<original normalized hostname>`. Certificate and
   hostname verification must remain enabled.

If any DNS answer is non-global, the whole hop fails closed. Selecting a public
answer while ignoring a private sibling would leave an avoidable ambiguity.

## HTTP Boundary

- Method: `GET` only.
- Redirects: 301, 302, 303, 307, and 308 only; at most three; every target is
  resolved with `urljoin()` and reruns the full URL/DNS policy.
- Headers sent: fixed transparent User-Agent, `Accept: text/html`,
  `Accept-Encoding: identity`, `Connection: close`, and the exact Host header.
- Never send Authorization, Cookie, Referer, Origin, forwarded client headers,
  email headers, local paths, or Career Hub identifiers.
- Connect timeout: 5 seconds; read timeout: 5 seconds; total transport/streaming
  budget: 15 seconds across redirects and address attempts. Python's blocking
  operating-system DNS lookup cannot be cancelled independently; if it returns
  after the deadline, the resolver fails before connecting. The threaded local
  server keeps that lookup from blocking unrelated UI requests.
- Response: status 200 and `text/html` only. Charset may be UTF-8 or another
  declared Python codec; invalid bytes use bounded replacement decoding.
- Encoded response limit: 2 MiB. Reject an oversized `Content-Length` before
  reading and stop streaming at limit + 1.
- Reject non-identity `Content-Encoding`; there is no transparent gzip/brotli
  decompression in this slice.
- Do not process Refresh headers, meta refresh, JavaScript navigation,
  subresources, forms, scripts other than bounded JSON-LD data, images, CSS,
  attachments, robots crawling, or additional links.
- There is no automatic retry after a completed HTTP response and no search
  fallback.

`401`, `403`, login/interstitial content, and protected known providers become
**Needs JD** with an attended-capture suggestion. `404`/`410`, `429`, 5xx,
timeouts, TLS failure, unsafe redirects, and size/type violations are similarly
bounded failures rather than reasons to broaden the fetcher.

## Extraction Boundary

The parser receives at most the accepted 2 MiB HTML response and emits plain
text only. It never stores raw DOM/HTML.

Supported strategies, in order:

1. One bounded `JobPosting` JSON-LD object. At most 20 JSON-LD scripts and
   1 MiB total JSON-LD text are examined. Traversal is bounded to 32 levels and
   20,000 values. If several postings exist, exactly one exact normalized title
   match to the Discovery may disambiguate them; otherwise the page is
   ambiguous.
2. One explicit semantic job-description container identified by a closed set
   of `itemprop`, `id`, `class`, or ATS `data-*` tokens. There is no whole-body
   fallback. Candidate text must pass the same single-job and shell/noise
   principles used by attended extension capture.

Accepted source text must be between 160 and 500,000 normalized characters,
contain bounded job-description signals, and represent one job. Navigation,
search results, recommendations, login shells, cookie/privacy boilerplate, and
multiple-job pages are rejected. Links and markup become text; scripts, style,
form values, comments, hidden metadata outside supported JSON-LD, and event
attributes are ignored.

Extracted title/company/location are source suggestions. Existing explicit
field overrides always remain effective. The resolver result has
`content_scope=page_visible`, never `confirmed_full`; the user must inspect the
retained text and choose **Confirm complete JD** before the existing promotion
path becomes available.

## Persistence, Identity, And Idempotency

A successful resolution attaches one normal Discovery observation:

- `source_kind=public_resolver`;
- `source_channel=public_job_link`;
- deterministic request fingerprint from contract version, Discovery id, and
  retained source URL;
- content fingerprint and bounded extractor metadata;
- final public URL, redirect count, strategy, and source provider; and
- the plain-text snapshot under the existing private
  `jobs/discovery/captures/<discovery-id>/source.txt` boundary.

The original alert/capture URL remains provenance. The final validated public
URL is exposed as `resolved_url` and becomes the preferred **Open posting** and
saved-JD source URL after the user promotes the item. It is not used as an
arbitrary redirect endpoint.

Persistence reuses the Discovery transaction, atomic source replacement,
field-override separation, and assessment preview. Successful retry returns
the same observation/result. A failed attempt stores no response body and does
not advance the Discovery revision. An optimistic revision mismatch after the
fetch discards the result.

No source is marked complete automatically. No resolver output enters Demand &
coverage, Applications, Contacts, Skills, Career Vault, or application
materials before the existing explicit gates.

## Diagnostics And Privacy

Public responses may contain tracking tokens, private candidate links, or
unexpected internal content. Diagnostics therefore contain only:

- generated run id;
- stable stage and error code;
- duration, redirect count, byte count, and extraction strategy when safe; and
- success/failure outcome.

They never contain the URL, hostname, DNS addresses, response headers/body,
title, company, JD text, query string, local Data Home path, capability token,
email metadata, or exception traceback returned to the browser. The retained
source and final URL remain private Data Home content and participate in normal
recovery.

## Stable Failure Codes

The implementation may group lower-level exceptions only into this public
vocabulary:

- `PUBLIC_JOB_RESOLUTION_NOT_ELIGIBLE`
- `PUBLIC_JOB_RESOLUTION_STALE`
- `PUBLIC_JOB_RESOLUTION_URL_REJECTED`
- `PUBLIC_JOB_RESOLUTION_PROTECTED_SOURCE`
- `PUBLIC_JOB_RESOLUTION_DNS_REJECTED`
- `PUBLIC_JOB_RESOLUTION_CONNECT_FAILED`
- `PUBLIC_JOB_RESOLUTION_TLS_FAILED`
- `PUBLIC_JOB_RESOLUTION_TIMEOUT`
- `PUBLIC_JOB_RESOLUTION_REDIRECT_REJECTED`
- `PUBLIC_JOB_RESOLUTION_TOO_MANY_REDIRECTS`
- `PUBLIC_JOB_RESOLUTION_HTTP_UNAVAILABLE`
- `PUBLIC_JOB_RESOLUTION_RATE_LIMITED`
- `PUBLIC_JOB_RESOLUTION_RESPONSE_TOO_LARGE`
- `PUBLIC_JOB_RESOLUTION_CONTENT_UNSUPPORTED`
- `PUBLIC_JOB_RESOLUTION_CONTENT_NOT_FOUND`
- `PUBLIC_JOB_RESOLUTION_CONTENT_AMBIGUOUS`
- `PUBLIC_JOB_RESOLUTION_FAILED`

Messages state what the user can do next; they do not expose transport details.

## Ordered Implementation Tickets

Only one ticket belongs in one implementation goal.

### `LINK-000` Fix The Contract And Threat Boundary

**Goal:** this ADR, the current repository-scoped threat model, and the roadmap
identify the resolver as one explicit safe enrichment action.

**Files allowed to modify:** this ADR,
`docs/product/job_discovery_capture_plan.md`, and `docs/process/acceptance_registry.md`.

**Acceptance:** no runtime behavior, dependency, schema, or capability status
changes; the public resolver remains Planned.

### `LINK-100` Add Public URL Policy And Pinned Transport

**Status:** Complete on 2026-08-23.

**Goal:** one pure resolver module either returns a bounded accepted HTML
response or one stable failure without performing an unsafe connection.

**Files allowed to modify:** `src/public_job_resolver.py` and
`tests/test_public_job_resolver.py` only.

**Required synthetic checks:** URL parser edge cases; literal/encoded host
rejection; all IPv4/IPv6 non-global classes; mixed DNS answers; address count;
no second DNS connect; Host/SNI preservation; default certificate verification;
redirect hop validation and downgrade rejection; timeout; status/type/encoding/
size limits; header allowlist; no proxy/auth/cookie; stable sanitized failures.

**Authorization:** dependencies=no; downloads/network=no; real-private-data=no;
provider-settings=no; secrets=no; browser-install=no; git actions=no.

### `LINK-110` Extract And Attach One Unverified JD

**Status:** Complete on 2026-08-23.

**Goal:** bounded synthetic JSON-LD and semantic HTML fixtures enrich exactly
one eligible Discovery as `page_visible` while preserving identity, overrides,
revision safety, and the existing preview path.

**Files allowed to modify:** `src/public_job_resolver.py`,
`src/job_discovery.py`, `tests/test_public_job_resolver.py`, and
`tests/test_job_discovery.py`.

**Required synthetic checks:** one/multiple/malformed/deep JSON-LD; exact-title
disambiguation; semantic container; search/login/multi-job/noise rejection;
plain-text normalization; target-only enrichment; stale revision before and
after fetch; retry idempotency; atomic-write recovery; final URL provenance;
field override preservation; no automatic confirmation/promotion/skill write.

**Authorization:** dependencies=no; downloads/network=no; real-private-data=no;
provider-settings=no; secrets=no; browser-install=no; git actions=no.

### `LINK-120` Add The Explicit Feed Action

**Status:** Complete on 2026-08-23.

**Goal:** one browser-only route and compact Feed action run the resolver and
reuse the current Discovery review/confirmation/promotion surfaces.

**Files allowed to modify:** `src/run_intake_ui.py`,
`src/intake_ui_routes/mutations.py`, `src/intake_ui_static/ui/api.js`,
`src/intake_ui_static/features/discoveries.js`,
`src/intake_ui_static/styles/discovery.css`, route/API tests, and one synthetic
browser smoke fixture.

**Required checks:** exact body contract; browser capability only; extension
tokens rejected; button eligibility and busy state; success refreshes the same
card; failure preserves Needs JD and Open posting; double click/idempotency;
stale response; mobile/desktop layout; no new top-level mode or settings.

**Authorization:** dependencies=no; downloads/network=no; real-private-data=no;
provider-settings=no; secrets=no; browser-install=no; git actions=no.

### `LINK-130` Security And Acceptance Gate

**Status:** Complete on 2026-08-23.

**Goal:** complete focused, full regression, public-tree, and security-diff
checks; mark the capability Implemented only if every synthetic exit condition
passes.

**Files allowed to modify:** test fixtures, this ADR,
`docs/product/job_discovery_capture_plan.md`, `docs/process/acceptance_registry.md`, and
`START_HERE.md`.

**Exit evidence:** resolver/API/domain/browser suites; full unit discovery;
JavaScript syntax checks; public-tree check; no private fixtures; security diff
review covering SSRF, DNS rebinding, TLS, redirect, parser limits, stale writes,
XSS, and diagnostics. A live public-site smoke is optional dogfood evidence,
not permission to weaken a failed synthetic control.

**Completed evidence:** 85 focused resolver/domain/route/API tests; the
713-test repository suite with three expected skips; JavaScript syntax and
Python compile checks; the public-tree check; agent-workflow changed-file
audit; a desktop/mobile synthetic rendered smoke; and Codex Security diff scan
`23000d75-0192-42f0-9624-ff96ffe24f7e` over snapshot
`f8687e23c803a99d776355fe58e5a5c211e5317bf2488e6285b32904b228e362`
with complete coverage and no findings. Live provider behavior remains optional
dogfood evidence and cannot change these boundaries.

## Explicit Non-goals

- recurring watchers, crawling, URL lists, cron, sitemaps, or RSS;
- web search by title/company or following related-job links;
- browser automation, JavaScript execution, scrolling, clicking, CAPTCHA, or
  anti-bot bypass;
- LinkedIn/Handshake/other authenticated retrieval;
- cookies, credentials, browser profiles, local proxy inheritance, or custom
  request headers;
- user-defined CSS/XPath/extraction rules;
- PDF/attachment parsing, image OCR, or non-HTML sources;
- AI parsing, semantic fit scoring, automatic vocabulary/evidence changes;
- automatic complete-JD confirmation, promotion, application creation, status
  change, or submission.

## Reversal Conditions

Keep the resolver disabled or remove it if validated-address connection pinning
cannot preserve standard TLS hostname verification, response limits cannot be
enforced before persistence, or provider behavior would require authentication,
browser state, JavaScript execution, or anti-bot workarounds. The attended
extension remains the supported fallback.

## Authoritative References

- [OWASP SSRF Prevention Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Server_Side_Request_Forgery_Prevention_Cheat_Sheet.html)
- [Python `ipaddress`](https://docs.python.org/3.13/library/ipaddress.html)
- [Python `http.client`](https://docs.python.org/3.13/library/http.client.html)
- [Python `ssl`](https://docs.python.org/3.13/library/ssl.html)
- [Python `socket`](https://docs.python.org/3.13/library/socket.html)
