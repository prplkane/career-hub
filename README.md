# Career Hub

A local-first desktop app for running a job search from evidence: keep a
verified record of your experience, capture job descriptions, see where you
match and where you don't, and prepare resumes and cover letters that only
claim what you can back up.

[![Download for Windows](https://img.shields.io/github/v/release/prplkane/career-hub?label=Download%20for%20Windows&logo=windows&style=for-the-badge&color=2f6f9f)](https://github.com/prplkane/career-hub/releases/latest)

[![CI](https://github.com/prplkane/career-hub/actions/workflows/ci.yml/badge.svg)](https://github.com/prplkane/career-hub/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

Everything runs on your computer. There is no account, no cloud sync and no
telemetry, and the app itself makes no AI API calls. Your data stays in a
private folder that only your Windows account can read.

## What it does

- **Career Vault.** Import a resume, review each extracted fact, and approve
  only what is accurate. Approved roles, projects, skills and evidence become
  the single source for everything the app writes.
- **Job intake and assessment.** Paste or capture a job description. Career
  Hub normalizes the requirements, checks them against your Vault, and shows
  matched evidence, gaps and eligibility blockers. Matching is deterministic,
  so the same inputs give the same result.
- **Application materials.** Build a job-specific resume plan and cover letter
  from approved evidence, edit them in constrained editors that flag stale or
  unsupported claims, and export DOCX, PDF or plain text.
- **Application tracking.** A ledger records each application's status,
  materials and history, with explicit backup and restore.
- **Demand and gaps.** Across saved jobs, see which skills are asked for most
  and how well your evidence covers them.
- **Email job alerts.** A dedicated Gmail connection brings job-alert emails
  into the same deduplicated feed as manual captures.
- **Capture from the browser.** An extension for Chrome, Edge and Firefox,
  paired once with a one-time code, captures job pages and can fill empty
  application fields with answers you approved. It never submits a form.
- **Works with your own AI tools.** The `career-hub-agent` command line gives a
  coding agent you already use scoped access to prepare materials for one job.
  It can save drafts, but it never submits applications or adds candidate
  facts.

## Install

1. Download `CareerHub-…-Setup.exe` from the
   [latest release](https://github.com/prplkane/career-hub/releases/latest).
2. Run it. Builds are not code-signed yet, so Windows SmartScreen may warn on
   first run: choose **More info → Run anyway**.
3. Later versions install from **Settings → Check for updates**.

Windows 10 or later (x64) is required. Each release lists SHA-256 checksums in
`SHA256SUMS.txt`.

## How it is built

| Part | Technology | Role |
| --- | --- | --- |
| Desktop host | Electron 44 | Starts and supervises the backend, loads only its loopback origin, installs updates |
| Backend | Go 1.27 | Local HTTP API on `127.0.0.1`, domain logic, document rendering |
| Storage | YAML, Markdown, SQLite ([modernc.org/sqlite](https://modernc.org/sqlite), pure Go) | Human-readable records plus SQLite for high-churn data |
| Secrets | Windows DPAPI and Credential Manager | Protects connector credentials and sensitive documents at rest |
| UI | HTML, CSS, JavaScript ES modules | No framework and no build step |
| Browser extension | Manifest V3 | Job and form capture through a paired, revocable credential |

Design decisions are recorded as ADRs in [`docs/adr/`](docs/adr), for example
[local API admission](docs/adr/SEC-250_local_api_admission.md),
[secret and document protection](docs/adr/SEC-240_platform_secret_and_document_protection.md)
and [moving the backend from Python to Go](docs/adr/MIG-100_backend_language_and_migration.md).
The [architecture atlas](docs/reference/architecture_atlas.md) has the system
context, data model and workflow diagrams.

Engineering practices:

- CI on every change: Go formatting, `go vet` for Linux and Windows,
  `govulncheck`, the portable Go suite, desktop and UI tests, a Windows build
  with a startup smoke test, Gitleaks and Trivy.
- Dependencies are pinned, with hash-pinned Python tools and grouped Dependabot
  updates.
- Releases are built by CI from a tagged commit that has passed its checks.
- A repository gate rejects personal data and private paths before they can be
  committed.

## Product scope

What is implemented, and where its limits are, is defined by the
[Capability Acceptance Registry](docs/process/acceptance_registry.md). Planned
work and its safety boundaries are in the
[Product Backlog](docs/product/product_backlog.md). Both are deliberate about
what the app will not do: no scraping, no auto-apply, and no candidate facts
the user has not approved.

Adding a resume offers two paths. **Update Career Vault** runs extraction,
review and explicit approval. **Save for applications** stores the resume as
an immutable Resume Version for reuse without changing the Vault. Processed
imports are workflow artifacts, not application Resume Library items.

## Build from source

Windows x64 with Go 1.27.3 and Node 24.19.0:

```powershell
npm ci --ignore-scripts
npm --prefix desktop ci
npm --prefix desktop run build:go-sidecar
.\scripts\start_career_hub.ps1
```

Development data lives in `%LOCALAPPDATA%\CareerHub\dev`, never in the
checkout. Run the checks:

```powershell
Push-Location backend; go test ./cmd/... ./internal/...; Pop-Location
npm --prefix desktop test
npm run test:fast
python -m unittest discover -s tests -q
```

See [Start Here](START_HERE.md) for the full workflow,
[local development](docs/guides/local_development.md) and the
[documentation index](docs/README.md).

## Repository layout

```text
backend/     Go module: sidecar server, agent client, domain packages
desktop/     Electron host, packaging, desktop tests
src/         Browser UI served by the sidecar, shared read-only catalogs
extension/   Browser extension (Manifest V3)
scripts/     Developer, audit and launch scripts
tests/       Tooling tests and rendered smoke checks
docs/        ADRs, reference, guides, product scope
```

## Security and license

Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).
Career Hub is licensed under the [Apache License 2.0](LICENSE).
