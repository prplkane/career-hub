# Contributing to Career Hub

Career Hub is a local-first desktop app: an Electron host runs a native Go
sidecar that owns the private Data Home. Start with the
[README](README.md) for what it does and the
[documentation index](docs/README.md) for everything else.

## Prerequisites

Windows x64 is the qualified platform.

| Tool | Version | Pinned in |
| --- | --- | --- |
| Go | 1.27.3 | `backend/go.mod` (CI uses `GOTOOLCHAIN=local`) |
| Node | 24.19.0 | `.nvmrc`, `desktop/package.json` |
| Python | 3.13 (developer tools only) | `.python-version` |

## Set up and run

```powershell
npm ci --ignore-scripts
npm --prefix desktop ci
npm --prefix desktop run build:go-sidecar
.\scripts\start_career_hub.ps1
```

Development data lives in `%LOCALAPPDATA%\CareerHub\dev`, never in the
checkout. Use synthetic data for tests and examples; never commit real resumes,
personal data, or credentials. The `check_public_tree.py` gate in CI rejects
known private paths, but it is a backstop, not a substitute for care. See
[private data](docs/reference/private_data.md) and [local development](docs/guides/local_development.md).

## Before you open a pull request

Run the checks for what you changed. CI runs them on Linux and Windows.

```powershell
# Go backend
Push-Location backend
gofmt -l .
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
go test ./cmd/... ./internal/...
Pop-Location

# Desktop host and browser UI
npm --prefix desktop test
npm --prefix desktop run check
node --test tests/*.test.mjs tests/*.test.js

# Developer tools, documentation guards, and the public-tree gate
python -m unittest discover -s tests -q
python scripts/check_public_tree.py
```

Follow [engineering rules](docs/process/engineering_rules.md). In short: put domain
logic in `backend/internal/`, keep error text free of private data, put new UI
behavior in a module under `src/intake_ui_static/features/` rather than
`app.js`, and prefer the standard library over new dependencies.

## Pull requests

- Keep each PR to one reviewable change, with tests for behavior changes and
  docs updated in the same PR.
- Work on a branch, never directly on `main`.
- Write commit subjects in the Conventional Commits style used in the history:
  `feat(ui): …`, `fix(runtimepaths): …`, `docs: …`, `chore(deps): …`, `ci: …`.
- Describe what changed, why, and how it was verified, including anything you
  could not run.
- Add a line under **Unreleased** in [CHANGELOG.md](CHANGELOG.md) for any
  user-visible or security-relevant change.

## Dependencies

Dependabot proposes monthly grouped updates (`.github/dependabot.yml`).
Versions that repository checks pin exactly — Electron, Forge, the installer
packages and `modernc.org/libc` — are upgraded by hand together
with their qualification steps. Explain any new dependency in the PR.

## Security

Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md),
not in issues or pull requests.

## License

Contributions are accepted under the [Apache License 2.0](LICENSE).
