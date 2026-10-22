# IMAP pilot dependency notices

The adapter pins `github.com/emersion/go-imap/v2 v2.0.0-beta.8`, with its exact
upstream `go-message v0.18.2` and `go-sasl v0.0.0-20241020182733-b788ff22d5a6`
dependencies. All three are MIT licensed. The existing pinned x/text dependency
and notices remain unchanged. This does not activate a mail service or constitute
a complete packaged-artifact license/security audit.

The upstream v2 API provides streamed literals and immediate socket ownership;
v1.2.1 is an older API, not assumed safer merely because its version is stable.
The v2 prerelease label remains a release-review risk. Source inspection and a
synthetic transport pilot, not the library name, determine feasibility. Never
switch silently to a floating `latest`, fork its parser without review, or expose
the library's writable mailbox methods through the application facade.

Exact retained LICENSE files were hash-compared with checksum-verified module
sources (SHA-256):

| File | SHA-256 |
| --- | --- |
| `go-imap/LICENSE` | `04c033c1515257f99b853e72b19ba7b0f589a9a8e690388120524a5190a03137` |
| `go-message/LICENSE` | `9e84a70e67cb6d9e79df2612bd111d5c505b40422ce146afe8477bb1af83155a` |
| `go-sasl/LICENSE` | `0e98a5e907c72bd63b5eaf64c662fa146f139931b2608a1427286577b9900cc4` |

Sources: [go-imap pinned tag](https://github.com/emersion/go-imap/tree/v2.0.0-beta.8),
[go-message pinned tag](https://github.com/emersion/go-message/tree/v0.18.2),
[go-sasl pinned commit](https://github.com/emersion/go-sasl/tree/b788ff22d5a6b3970cde181998f52658a475bffc).
Go module content and manifest checksums are in `backend/go.sum`.
