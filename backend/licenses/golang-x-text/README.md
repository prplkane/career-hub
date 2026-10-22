# golang.org/x/text notices and pin

Dependency: `golang.org/x/text v0.42.0`, BSD-3-Clause with the upstream additional
patent grant. `LICENSE` and `PATENTS` are copied from the checksum-verified module
(Git may normalize line endings). Preserve both in any future binary packaging.

Upstream: <https://go.googlesource.com/text/+/refs/tags/v0.42.0>.
Commit: `fafe4a06967e06550e69ee42787d9902845d2a3f`.
Published: 2026-09-08T16:29:55Z. Module directive: Go 1.26.0.
Module sum: `h1:JbOZXgfeCPU9gacVtYliJqOhD+zhrEqK4LfdpmlUZqI=`.
go.mod sum: `h1:ojzP1Z+2QtioaF8DTtO8K5q7JWVVYwZKenzujK0Zd0E=`.

The managed-path adapter imports only `unicode/norm` and its Go-standard-library
/ same-module dependencies. The upstream module also declares development
dependencies on x/tools, x/mod and x/sync; these are not imports of the adapter.
No source or code generator from the module is executed by the application.

The initially considered Unicode15-only v0.33.0 was rejected before import/use:
[GO-2026-5970 / CVE-2026-56852](https://vuln.go.dev/ID/GO-2026-5970.json)
affects normalization with invalid UTF-8 before v0.39.0. v0.41.0 contains the
fix.

v0.42.0 (2026-10-13) is the minimum version required by `golang.org/x/net
v0.60.0`, which fixes CVE-2026-78669. It keeps the same Unicode 15.0.0 and
17.0.0 tables and fixes six NFC composition edge cases in `unicode/norm`
(Hangul followed by other runes, starter blocking, supplementary-plane
recomposition keys, and transform buffer handling). These move NFC toward the
Unicode specification that Python implements; the Unicode15 refusal guard is
unchanged. `LICENSE` and `PATENTS` are byte-identical to v0.41.0.

The official module vulnerability index reviewed on 2026-09-08 lists older
issues fixed in v0.3.3, v0.3.7, v0.3.8 and v0.39.0; it lists no later applicable
advisory at this checkpoint. This is not a vulnerability-free guarantee; repeat
the release vulnerability gate when actually packaging or upgrading.

Do not downgrade to retain old tables. On Go1.27 this version uses Unicode17;
the adapter retains a small, independently reproduced Unicode15 compatibility
refusal guard instead. See `internal/runtimepaths/README.md`.
