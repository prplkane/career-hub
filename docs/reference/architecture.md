# Architecture

This document records current architecture decisions and runtime boundaries.
It does not authorize building future phases.

For diagrams (system context, ERDs, state machines, workflow sequences, and
the maintenance impact map), use the
[`Career Hub Architecture Atlas`](architecture_atlas.md). For known structural
debt and the improvement roadmap, see the
codebase assessment.

## Current Direction

- Career Hub is a local-first desktop application: an Electron host owns one
  native Go sidecar, which owns all reads and writes to the private Data Home.
- Keep deterministic domain logic in Go packages under `backend/internal/`,
  independent of HTTP transport and of the desktop host.
- Keep data human-reviewable with YAML and Markdown until a stronger storage
  need appears; use SQLite for high-churn records.
- Keep the dependency-light HTML/CSS/JavaScript UI (ES modules, no build step)
  until product complexity justifies a framework migration.
- Add cloud services, AI providers, scraping, or heavier editor infrastructure
  only when a task explicitly requires it.

## Runtime Components

| Component | Location | Responsibility |
| --- | --- | --- |
| Desktop host | `desktop/` | Electron main process: resolves platform paths, starts and supervises the sidecar, loads only its loopback origin, exposes a minimal preload API |
| Sidecar | `backend/cmd/career-hub-sidecar/` | Native Go executable: validates the Data Home, serves the UI and local API on `127.0.0.1` |
| Domain packages | `backend/internal/` | Vault, jobs, assessment, applications, materials, email, backup, and storage services |
| Browser UI | `src/intake_ui_static/` | Static HTML, ES modules, and CSS served by the sidecar |
| Shared catalogs | `src/data/` | Read-only technology, concept, and skill-group catalogs |
| Agent client | `backend/cmd/career-hub-agent/` | Local HTTP client for agent workflows; never a second Data Home writer |
| Browser extension | `extension/` | Attended job capture and application helpers, talking to the loopback service |
| Development tools | `scripts/`, `tests/test_*.py` | Python and Node checks; not part of the product runtime |

The UI and catalogs keep their historical `src/` paths because the sidecar,
packaging manifest, and tests reference them; see
[repository layout](repository_layout.md).

## Backend And Core Logic

Go owns all product logic:

- Career Vault loading, validation, and approved intake.
- Deterministic job parsing and advisory assessment.
- Resume Versions, Resume Plans, and application-only material drafts.
- Canonical DOCX/PDF/TXT rendering with structural validation.
- Application ledger, email-derived updates, backup, restore, and recovery.

Domain packages do not depend on HTTP. Use cases that touch the Data Home take
the process-wide operation lease (`backend/internal/operationgate`) so
observation, recheck, and publication cannot interleave. Filesystem access goes
through the checked, rooted helpers in `backend/internal/runtimepaths`.

## Local API

`backend/internal/sidecar/httpapi` is the HTTP boundary. It owns local
authorization, request decoding, fixed error codes, response deadlines, and the
exact static-asset allowlist. It delegates to an `ApplicationActions` table that
the sidecar command wires to domain packages. Route handlers do not own
document or evidence logic.

Errors cross the boundary as fixed codes without underlying causes, so private
data cannot leak through error text. The server listens only on loopback and
sends a strict Content Security Policy.

## Desktop Boundary

The selected desktop wrapper is Electron because a bundled Chromium
runtime gives the Windows and macOS applications one renderer baseline. Windows
x64 is the qualified target; packaging is described in
[native packaging](../../packaging/README.md).

The boundary remains intentionally narrow:

- The Go sidecar is the product and local-API owner.
- Electron starts it with `--sidecar-bootstrap` and sends ports, paths, and the
  session token over the versioned stdin contract documented in
  [`sidecar_protocol.md`](sidecar_protocol.md).
- The host verifies the native executable and resources, owns exactly one child
  process, and loads only the sidecar-owned loopback origin after readiness and
  health pass.
- The renderer runs sandboxed with context isolation, no Node integration, and
  denied permissions (`desktop/src/security.js`).

Electron does not provide Safari extension compatibility, signing or
notarization, or multi-device synchronization. Platform key storage
(Windows Credential Manager) is implemented in Go by
`backend/internal/secretstore`; distribution trust and synchronization remain
separate capabilities.

## Frontend Direction

The local UI uses ES modules and focused feature renderers without a build
step. The constrained material editor is typed form state, not a rich-text DOM
editor: this preserves ATS-safe output and avoids ProseMirror/TipTap-style
document dependencies.

The browser loads one import-only CSS manifest. Shared tokens and primitives,
workflow-owned styles, theme variants, and responsive rules are split under
`src/intake_ui_static/styles/`; their ownership and cascade contract are
documented in that directory. All values come from `styles/tokens.css` (the
signature design system's Ink & paper palette and its type and radius
scales). The Go static-asset test keeps the stylesheet list in sync with the
server allowlist, and the CSS budget ratchet (`tests/test_css_budget.py`) plus
the token contrast test keep hard-coded values and inaccessible colour pairs
from growing back.

New UI behavior belongs in a module under `features/` or `ui/`, not in
`app.js`, which is being reduced feature by feature.

If the UI later requires complex shared state, collaboration, or a component
ecosystem, prefer React, TypeScript, and Vite, with accessible component
primitives. Server-rendered frameworks (Next.js, SvelteKit) do not fit a
loopback desktop sidecar. See [technology selection](technology_selection.md).

## Storage Direction

- Keep low-churn candidate evidence and human-reviewed configuration in YAML and Markdown.
- Use SQLite (pure-Go `modernc.org/sqlite`) for queryable application history, lifecycle events, and other high-churn records.
- Store job-scoped material drafts as revisioned YAML under the selected Data Home; rendered DOCX/PDF/TXT files are derived artifacts, not additional sources of truth.
- Keep all private data in the Data Home (`%LOCALAPPDATA%\CareerHub\<channel>` on Windows), never in the code installation; see [private data](private_data.md).
- Do not add a cloud database until local storage is insufficient and hosting is explicitly approved.

## Boundaries

Architecture guidance alone does not authorize adding:

- Scrapers.
- AI API calls.
- Cloud services.
- Automatic application submission or implicit `Applied` status changes.
- An unrestricted rich-text/Word clone inside the material editor.
- Framework migrations or production dependencies without a concrete feature need.

Architecture docs should guide future work, not implement it.
