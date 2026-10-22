# Engineering Rules

## General

- Prefer small readable functions.
- Avoid unnecessary abstractions.
- Avoid hardcoded absolute paths.
- Do not silently swallow errors.
- Keep side effects at the edges.
- Do not introduce network calls unless the task requires them.
- Do not add heavy frameworks for small tasks.
- Prefer the standard library and existing project patterns before adding a
  dependency. A new dependency needs a stated reason in the PR.

## Go Backend

- Format with `gofmt`; run `go vet ./...` and
  `go test ./cmd/... ./internal/...` before a PR.
- Keep domain logic in `backend/internal/` packages that do not import HTTP
  types. `cmd/` packages configure and wire; they should not grow business
  rules.
- Access the Data Home only through `runtimepaths` and `sqlitestore` helpers,
  and take the `operationgate` lease for any use case that observes and then
  writes Data Home state.
- Return errors with fixed, documented codes. Never put file contents, paths
  inside the Home, candidate data, or credentials into error text or logs.
- Bound every read: use the existing record budgets and size limits instead of
  unbounded scans or reads.
- Windows behavior (ACLs, credentials, file replacement) needs a native
  `*_windows_test.go` test, not only a cross-compiled build.
- Generated files carry a `Code generated` header and keep their generator in
  the repository.

## Browser UI

- Use native ES modules; there is no build step. Put new behavior in a module
  under `src/intake_ui_static/features/` or `ui/`, not in `app.js`.
- Call the backend through the shared client in `ui/api.js`.
- Set text with `textContent`. When HTML is unavoidable, escape every
  interpolated value with `escapeHtml` from `ui/primitives.js`.
- Use design tokens from `styles/tokens.css`; the CSS budget test rejects new
  hard-coded values.
- A new static file must also be added to the sidecar allowlist in
  `backend/internal/sidecar/httpapi/static.go`.

## Data And YAML

- Validate required fields.
- Fail clearly on malformed data.
- Keep sample data clearly marked.
- Do not overwrite user data silently.

## SQL And SQLite

- Do not use `SELECT *` in application queries.
- Select only required columns.
- Use parameterized queries.
- Do not concatenate user input into SQL.
- Keep schema changes simple and documented.
- Add indexes only when there is a real query pattern.

## File Safety

- Do not delete or overwrite user-provided templates, resumes, vault data, or outputs unless explicitly requested.
- Generated files belong in the Data Home or an ignored, clearly versioned build
  location, never in tracked source.
