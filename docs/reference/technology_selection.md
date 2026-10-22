# Technology Selection

- Prefer free and open-source solutions whenever practical.
- When cost is unavoidable, prefer affordable options.
- Do not choose a tool only because it is cheapest; choose the best fit for reliability, maintainability, user feedback, and total cost.
- Before adding a paid service, explain why local, free, or open-source options are insufficient.
- Prefer local-first solutions for early stages unless a hosted option clearly improves the project.

## Desktop Wrapper

- Use Electron for the authorized Windows/macOS wrapper.
- Keep the dependency-light browser UI; do not make a
  React migration a prerequisite for the desktop shell.
- Use Electron as a narrow lifecycle/process/navigation boundary, not as a
  second product backend.
- Accept the larger bundled runtime in exchange for one controlled Chromium
  renderer across supported desktop platforms. Re-measure startup, memory, and
  artifact size when the host is introduced.
- Follow Electron's current security checklist: no renderer Node integration,
  context isolation and sandboxing enabled, navigation/window creation
  restricted, IPC senders validated, and a supported Electron release pinned.
- Use Electron Forge for the development package foundation. The host and
  packaging dependencies are isolated in `desktop/`; installer makers remain a
  release-phase decision.

Windows desktop packaging now uses the verified Go `go-onedir` artifact and
native agent client. The manifest binds every bundled file to the clean source
commit; Electron verifies it before launch. Python/PyInstaller packaging has
been retired. The historical onefile/onedir measurements explain the reference
choice but are not the native packaging contract. See
`migration/default_runtime_cutover.md`.

This choice does not port the Chrome extension to Safari and does not add cloud
or multi-device synchronization.

## Approved Backend Migration

The user approved a staged Python-to-Go backend migration on 2026-09-07 under
[`MIG-100`](../adr/MIG-100_backend_language_and_migration.md). Go replaces application
logic and backend adapters, not the interface or data formats by implication.
Keep Electron integrated and tested throughout; evaluate Electron/Wails after
backend parity instead of coupling two migrations. That evaluation is recorded
in [`DESK-030`](../adr/DESK-030_desktop_shell.md): Electron stays, kept on the latest
patch of a supported major.

Go is the default Windows candidate backend after the documented compatibility,
data-safety and packaged Windows gates. Final retirement/artifact qualification
is recorded separately in
`migration/python_runtime_retirement.md`.
Build in an isolated worktree and qualify with synthetic Homes.
Preserve versioned HTTP/extension/agent contracts, local data ownership, encryption,
and source/evidence truth. Replace Python-specific packaging and dependency rules
with equivalent verified Go rules as each applicable slice reaches cutover.

The Python library selections below describe the reference implementation. They
are not requirements to recreate Python dependencies in Go; replacements must
prove equivalent supported behavior before product adoption. In particular,
native secret policy and the encrypted envelope remain invariant while their
implementations change. Use reviewed standard cryptographic primitives, never a
new cryptographic algorithm or plaintext fallback.

### Current native library decisions

The exact pins are in `backend/go.mod` and `backend/go.sum`. Selection follows
the supported contract rather than matching Python package names:

| Capability | Native implementation | Verification and limits |
| --- | --- | --- |
| HTTP, cancellation and operation lifetime | `net/http`, `context`, bounded owner operations | Explicit deadlines, loopback capabilities and lifecycle tests; no second backend in Electron. |
| SQLite | `database/sql` with `modernc.org/sqlite` | CGO-free Windows build; native locking/WAL and crash-recovery qualification. See `migration/sqlite_driver_decision.md`. |
| YAML | `go.yaml.in/yaml/v4` plus bounded domain validation | Retained Python semantic corpus; Go writes semantic YAML with documented formatting differences. |
| Document protection and Windows secrets | Standard `crypto/aes`, `crypto/cipher`, `crypto/rand`; `x/sys/windows` adapters | Existing envelope and credential identities remain binding; DPAPI/account-bound recovery tests. |
| DOCX/PDF/TXT exports | Standard ZIP/XML/text writers, `gopdf` and pinned embedded Vera fonts | Structural/source comparisons and bounded renderer errors; see `migration/material_render_contract.md`. |
| Imported PDFs | `ledongthuc/pdf` with bounded input/output | Source-intake fixtures and limits; no automatic semantic equivalence assumed from a parser swap. |
| Email | `emersion/go-imap/v2`, `go-message`, `go-sasl`, native credential/run owners | Synthetic protocol/credential/restart tests; real-mailbox acceptance remains a release limitation. |
| Public-link transport and extraction | `net/http`, `x/net/html`, `x/text`; explicit URL/DNS policy | Pinned compatibility vectors, deadline/DNS/TLS tests; fetched text remains unverified. |

Unicode/nameprep and JSON/YAML compatibility helpers retain measured reference
semantics where a bare Go standard-library call would change the product
contract. Go resource lifetimes, cancellation, error facts and SQL ownership
remain explicit. Python survives only in independent developer tooling and the
optional pinned historical comparison environment; `PyYAML` is its sole active
requirement. No supported product call launches Python.

## Platform Secret And Document Encryption

The following bullets record the historical Python selection. The current
Windows implementation uses the native adapters and standard cryptography
listed above; macOS native qualification remains a separate platform gate.

- Keep OS-secret access in the Python sidecar so browser mode and Electron use
  one security boundary.
- Retain Windows Credential Manager and add macOS Security.framework Generic
  Password items using the data-protection Keychain with synchronization
  disabled.
- Use a pinned `cryptography` AES-256-GCM implementation for a versioned common
  document envelope. Never implement encryption locally or fall back to a
  plaintext key file.
- Keep normal backups account/device-bound. Portable key recovery and
  cross-device synchronization require a separate reviewed design.
- The full decision, migration rules, and primary platform references are in
  `docs/adr/SEC-240_platform_secret_and_document_protection.md`.

## Offline Connectivity

- Core UI, loopback API, data, catalogs, and exports remain fully local.
- Use Chromium's negative offline signal and classified network-operation
  failures. Do not call a remote beacon merely to display connectivity.
- Keep local features enabled during an outage and require explicit retry when
  connectivity returns.
- Treat `online` as connection-available but unverified; it is not proof that a
  provider, proxy, or DNS path works.

References:

- [Electron security](https://www.electronjs.org/docs/latest/tutorial/security)
- [Electron Forge configuration](https://www.electronforge.io/config/configuration)

## Deterministic Document Font

- Use the unmodified Bitstream Vera Sans regular and bold faces already shipped
  by the pinned ReportLab dependency for generated PDFs.
- Resolve the faces as package resources, never from an operating-system font
  directory. Missing font or license resources fail PDF generation instead of
  silently substituting a platform font.
- Retain ReportLab's accompanying
  `fonts/bitstream-vera-license.txt` in every packaged sidecar. The license
  permits bundling the unmodified fonts with the application; `SIDE-210` must
  list both faces and the license in its auditable build manifest.
- This selection makes direct PDF rendering deterministic across supported
  hosts. It does not claim identical DOCX appearance because an external Word
  processor still controls DOCX font availability and pagination.

## Career Vault Intake

The following choices describe the pinned Python reference. Current Go uses
bounded domain structs/validators, standard ZIP/XML DOCX reading, the PDF parser
listed above and the characterized deterministic matcher/extractor. Historical
Pydantic/spaCy/python-docx behavior is retained through measured compatibility
fixtures; these packages are no longer product dependencies.

- Use Pydantic models as the validation and JSON Schema contract for future YAML, API, and UI layers.
- Use `python-docx` to preserve Word paragraph and list structure during source intake.
- Use a blank English spaCy pipeline with `PhraseMatcher` and `EntityRuler` for local terminology and section matching.
- Keep small regular expressions for narrow date and bullet-marker formats.
- Do not require a trained spaCy pipeline, GLiNER, fuzzy matching, or a generative model until benchmark evidence justifies it.
- User-approved taxonomy aliases extend later deterministic extraction without model training.

### Resume Parser Libraries

- `pyresparser` is an opinionated resume parser that itself depends on spaCy and NLTK; it is not a replacement NLP engine for spaCy.

- Do not add `pyresparser` to the production environment now.
- Its released dependency set targets an older spaCy/NLTK stack and adds many packages that overlap the current adapters and matcher.
- Its generic fields do not preserve the source spans, review states, or reusable-card ownership required by Career Vault.
- Evaluate it only in an isolated benchmark environment if current extraction misses company, title, or education candidates often enough to justify the cost.
- Compare candidate precision, source traceability, startup time, install compatibility, and license implications before adoption.
