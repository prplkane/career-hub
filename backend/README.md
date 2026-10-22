# Career Hub Go backend

This module is the Career Hub product backend. It builds two executables:

- `cmd/career-hub-sidecar` — the local application server. The Electron desktop
  starts it, and it owns every read and write to the private Data Home.
- `cmd/career-hub-agent` — a local HTTP client for agent workflows. It talks to
  a running sidecar and is never a second Data Home writer. See
  [agent workflows](../docs/guides/agent_workflows.md).

Go has been the only product backend since PR #40 (2026-10-03). For how the
pieces fit together, read [architecture](../docs/reference/architecture.md) and the
[sidecar protocol](../docs/reference/sidecar_protocol.md).

## Toolchain and checks

Use Go 1.27.3 with `GOTOOLCHAIN=local` for controlled offline builds. The build
is CGO-free (`CGO_ENABLED=0`); SQLite is the pure-Go `modernc.org/sqlite`.
Windows x64 is the qualified target, and native builds and tests are required
even when cross-compilation succeeds.

```text
gofmt -l .
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
go test ./cmd/... ./internal/...
```

CI runs all four on Windows and fails on any unformatted file, vet finding, or
known vulnerability that the code can actually reach. `govulncheck` covers the
Go standard library too, so a Go security release usually means bumping the
`go` line in `go.mod` together with `desktop/scripts/build-go-sidecar.cjs` and
`desktop/src/package_manifest.js`, which enforce the same version.

Use a workspace-local `GOCACHE`/`GOTMPDIR` when the execution sandbox cannot
write to the normal cache. Do not weaken file or credential protections to make
a test pass. Race tests need CGO and a native compiler; they are not part of the
standard CGO-free run.

To build the sidecar the desktop uses:

```powershell
npm --prefix desktop run build:go-sidecar
```

## Package map

Packages live flat under `internal/`. Several have their own README with
contracts and limits.

| Area | Packages |
| --- | --- |
| Sidecar runtime and HTTP | `sidecar/startup`, `sidecar/runtime`, `sidecar/protocol`, `sidecar/httpapi`, `sidecar/readonly`, `sidecar/assessment`, `sidecar/maintenance` |
| Data Home, files, storage | `datahome`, `runtimepaths`, `sqlitestore`, `operationgate`, `homebackup`, `documentmigration`, `documents` |
| Secrets | `secretstore`, `emailcredentials` |
| Career Vault | `vaultread`, `vaultwrite`, `vaultsetup`, `calendarinput` |
| Skills and catalogs | `skills`, `cataloginput`, `catalogstore`, `catalogtransaction`, `catalogwrite`, `competencyexplorer`, `demandgaps` |
| Resume intake | `intakeextract`, `intakeread`, `intakereview`, `intakeview`, `sourceupload`, `resumelibrary`, `resumeplan` |
| Jobs and assessment | `jobcard`, `jobdigest`, `jobevaluation`, `jobevidence`, `jobdiagnostics`, `joburls`, `evaluationinput`, `eligibilityfacts`, `assessmentcontext`, `assessmentpolicy`, `savedassessment`, `previewcache` |
| Discovery | `discovery`, `discoverypreview`, `publicjobresolver`, `promotiondiagnostics` |
| Applications | `applications`, `applicationwrite`, `applicationanalysis`, `applicationchecklist`, `applicationcontacts`, `applicationhelper`, `applicationreview`, `applicationsessions`, `applicationtexts`, `contactcandidate` |
| Materials | `materials`, `materialwrite`, `materialrender`, `materialintegrity` |
| Email | `emailalerts`, `emailcandidates`, `emailconnection`, `emaildiagnostics`, `emailgate`, `emailheaders`, `emailhost`, `emailimport`, `emailmime`, `emailoutcomes`, `emailquality`, `emailreceipts`, `emailrouting`, `emailworker`, `mailtransport` |
| Agent client support | `agentcontext`, `agentmaterials`, `agentvocabulary` |
| Shared utilities | `textcompat`, `fixedregex`, `isotime`, `diagnosticslog` |

Notes on a few packages whose names undersell them:

- `runtimepaths` is the filesystem-safety layer: rooted walks, Windows ACL and
  ownership checks, atomic replace, and single-owner Data Home claims. See
  [its README](internal/runtimepaths/README.md) and
  [ownership limits](internal/runtimepaths/README.ownership.md).
- `textcompat` reproduces Python 3.12 / Unicode 15 case folding and whitespace
  rules from pinned Unicode data. Stored skill keys depend on it, so it cannot
  be swapped for `golang.org/x/text/cases` without a data migration. See
  [its README](internal/textcompat/README.md).
- `operationgate` serializes Data Home use cases in one process. It is a
  deliberate correctness choice, not a throughput limit to tune away.

## Dependencies

Direct dependencies are listed in `go.mod`. Three are pre-release or untagged
and are isolated or reviewed:

- `go.yaml.in/yaml/v4 v4.0.0-rc.6` — catalog and document YAML. Its limits and
  distribution notices are recorded in
  [the adapter contract](internal/cataloginput/README.yaml.md).
- `github.com/emersion/go-imap/v2 v2.0.0-beta.8` — used only by
  `mailtransport`.
- `github.com/ledongthuc/pdf` (pseudo-version) — used only by
  `materialrender` for text extraction, with panics converted to errors.

Third-party license texts are kept under `licenses/`.

The Python-to-Go comparison probes that once lived under `cmd/` were removed
after the migration; the record is in
[docs/migration](../docs/README.md#migration-records).
