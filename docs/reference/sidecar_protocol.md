# Native sidecar protocol

Electron owns the Go sidecar, its loopback server, private session token and
lifecycle. The normal launch mode is `--sidecar-bootstrap`; the native owner
validates or initializes the selected Home before readiness. The read-only
`--sidecar` mode remains for bounded diagnostics. Python and PyInstaller are no
longer runtime entrypoints.

Resource and Data Home paths, ports and tokens are sent through the owned stdin
protocol, not renderer-selected executable arguments. Explicit extension-origin
arguments are host-authorized. Installed resources are bound to the verified
native executable directory; no `_MEIPASS` or Python resource resolution exists.
See cutover and
backup/restore for current maintenance
capabilities and qualification scope. The underlying wire events below retain
their versioned contract.

## Startup

The host must write one startup record within 10 seconds:

```json
{"command":"start","protocol_version":1,"port":0,"resource_root":"<absolute-resource-root>","data_root":"<absolute-data-root>","channel":"stable"}
```

Version 1 requires exactly these fields:

- `command` is `start`;
- `protocol_version` is `1`;
- `port` is `0`, requiring an operating-system-assigned port;
- `resource_root` and `data_root` are absolute, non-overlapping paths;
- `channel` is `dev` or `stable`; the retired `beta` channel is refused.

The desktop host also sends one optional field, `session_token`: the window's
capability token (ADR SEC-250), 32 random bytes as canonical unpadded URL-safe
base64. The sidecar refuses any other value with `SIDECAR_START_INVALID_TOKEN`,
installs it as the browser token, and never echoes it in any record. The
renderer receives it from the host through the preload
(`careerHubDesktop.session.token()`), not over HTTP. When the field is absent
the sidecar generates its own token.

Before readiness, the sidecar validates the resource boundary, initializes or
validates the selected data home, initializes the current schemas/services, and
binds only to `127.0.0.1`.

Success emits exactly one readiness record:

```json
{"app_version":"0.1.2","channel":"stable","event":"ready","port":49152,"protocol_version":1}
```

The port is the only listener value accepted from readiness. The Electron host
must construct `http://127.0.0.1:<validated-port>` itself. It must not accept a
URL, hostname, token, or navigation target from sidecar output.

`VERSION` is the sole editable application-version source and currently contains
`0.1.2`. It is embedded and validated by the packaged sidecar; changing the
application version does not change protocol version 1.

## One-shot desktop maintenance

The same packaged executable accepts the separate internal
`--desktop-maintenance` mode. It reads exactly one bounded record, emits exactly
one path-free result, and exits; it never starts an HTTP listener. The Electron
main process, not the renderer, supplies the already-resolved Data Home path.
The current commands are:

- `document_migration_plan`, with `protocol_version` and `data_root`;
- `document_migration_apply`, adding the exact reviewed `confirm_plan`;
- `document_migration_recover`, reserved for explicit interrupted-migration
  recovery.

Planning returns only the plan hash, source/target format versions, document
and ciphertext-file counts, and total encrypted bytes. Apply returns only the
confirmed plan hash and document count. Migration failures use stable,
path-free `DOCUMENT_MIGRATION_*` codes. The loading-page preload exposes
parameterless plan/apply actions; it cannot inject a path or replace the plan
held by Electron main.

## Health And Session Boundaries

After readiness, `GET /api/health` returns only:

```json
{"status":"ok","protocol_version":1,"app_version":"0.1.2"}
```

Health requires an exact loopback Host, accepts no foreign Origin, and contains
no path, private content, or capability token. It does not replace the existing
`/api/session` boundary. The UI obtains its memory-only browser token only from
the host (see Startup). `/api/session` requires that token, like every other
`/api/` route, and returns capabilities only; no route returns a token.

## Shutdown And Parent Loss

For a normal close, the host writes:

```json
{"command":"shutdown","protocol_version":1}
```

The sidecar stops the HTTP server, closes its listener, and exits successfully.
Closing the stdin pipe has the same cleanup behavior because it means the owning
host exited. Any other post-readiness record is a protocol failure and stops the
sidecar with a non-zero exit.

## Errors And Exit Status

Startup and protocol failures emit one path-free record such as:

```json
{"error_code":"SIDECAR_RECORD_INVALID_JSON","event":"error","protocol_version":1}
```

Exit status categories are:

| Status | Meaning |
| --- | --- |
| `0` | Graceful shutdown or parent-pipe closure |
| `3` | Input framing, version, command, or startup-timeout failure |
| `4` | Resource, data-home, or schema initialization failure |
| `5` | Loopback listener startup failure |
| `6` | Readiness could not be written to the owning pipe |
| `7` | HTTP server lifecycle failure |

Error records and logs never echo a startup record, filesystem path, capability
token, request body, or environment dump.

## Current Limitations

- A locked Electron host, measured PyInstaller `onedir` sidecar, and unsigned
  Windows per-user development installer exist. Signed Windows and native
  macOS release artifacts remain release work.
- The installed app/extension discovery and pairing contract is intentionally
  unresolved. The current unpacked extension still uses its browser-development
  origin and fixed port.
- Platform data permissions, Keychain/Credential Manager secrets, and document
  encryption are implemented in source with target-host acceptance still due;
  signing, notarization, and release updates remain later platform work.
- Multi-device synchronization is a separate future capability; this local
  process protocol does not provide it.

## Verification

Run the focused contract tests with:

```powershell
Push-Location backend
go test ./internal/sidecar/... ./internal/documentmigration/...
Pop-Location
npm --prefix desktop test
```
