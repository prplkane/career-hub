# SEC-240: Use Platform Key Stores With A Versioned Document Envelope

- **Status:** Implemented in source; target-host review pending
- **Decision date:** 2026-08-27
- **Evidence tickets:** `SEC-240`, `SIDE-210`, `ELEC-350`

## Context

Career Hub currently stores provider secrets through a Python-owned Windows
Credential Manager boundary and encrypts every private-document ledger/blob
directly with current-user Windows DPAPI. That is safe for the existing Windows
dogfood boundary but cannot support macOS, key rotation, or a common ciphertext
format. Browser mode and the future packaged sidecar must retain the same secret
access; making Electron the key owner would split the product into incompatible
security paths.

The migration cannot silently replace existing `.dpapi` files. Windows DPAPI is
normally bound to the same account and machine, so copied ciphertext and a
normal recovery archive are not portable by themselves. A missing OS-stored key
must never cause Career Hub to generate a replacement and overwrite data.

## Decision

### Ownership And Platform Backends

Python remains the only product secret/key boundary. The existing byte-oriented
`CredentialBackend` protocol gains a platform factory:

- Windows continues to use Generic Credentials in Windows Credential Manager.
- macOS uses a Generic Password item through Security.framework
  `SecItemAdd`, `SecItemCopyMatching`, `SecItemUpdate`, and `SecItemDelete`.
- Other platforms fail with a stable unavailable error and never fall back to a
  plaintext file, environment variable, shell command, or third-party keyring.

Every macOS query sets `kSecUseDataProtectionKeychain=true`,
`kSecAttrSynchronizable=false`, and
`kSecAttrAccessibleWhenUnlockedThisDeviceOnly`. Career Hub currently performs no
locked-screen background work, so the less restrictive after-first-unlock class
is unnecessary. Service/account identifiers are fixed, content-minimized, and
channel-scoped. Secrets never enter process arguments, logs, diagnostics,
backups, or the Data Home.

### Data-Home And Key Identity

The next Data Home format adds one random `data_home_id`. It is stable across a
same-machine move and is not derived from an absolute path, username, or private
content. Document master-key targets contain the application id, release
channel, `data_home_id`, purpose, and an opaque key id.

Each Data Home has one active random 256-bit document master key per channel.
The OS key store holds the key. A plaintext, non-secret document key-state file
contains only its format version, algorithm, `data_home_id`, and opaque active
key id. Envelopes also carry the key id so later rotation can retain old keys
until every object is verified under the new key.

If encrypted data or key state exists but its OS key is absent, startup fails
closed with a recovery-focused error. It does not create a new key, delete the
old state, or expose ciphertext as corrupt user content.

### Authenticated-Encryption Envelope

New document metadata and blobs use a bounded binary envelope with:

```text
magic | envelope version | canonical header length | canonical JSON header
      | 12-byte nonce | AES-256-GCM ciphertext and 128-bit tag
```

The canonical header is authenticated additional data and contains only the
algorithm, envelope version, `data_home_id`, key id, object purpose, and stable
object id. It contains no filename, title, note, account, path, or other private
metadata. Every encryption uses a fresh cryptographically random 12-byte nonce;
nonce reuse with the same key is forbidden. Parsing rejects unknown versions,
algorithms, purposes, duplicate/unknown header fields, oversized headers,
truncation, key/home mismatches, and authentication failure before returning
plaintext.

Use the pinned Python `cryptography` AESGCM implementation. The standard
library has no suitable authenticated-encryption primitive; implementing one
locally is rejected.

### Migration, Backup, Rollback, And Uninstall

Activating the new envelope increments the Data Home format so an older binary
refuses the newer state before writing. Migration is an explicit Windows-only
operation for a Data Home containing `.dpapi` documents:

1. inspect the legacy marker and every legacy document without changing it;
2. create and verify a named recovery copy containing the original marker,
   ledger, and blobs;
3. generate/store the new OS key and write all new envelopes into private
   staging;
4. decrypt every staged envelope and compare metadata, byte length, and SHA-256
   with the legacy plaintext;
5. atomically activate the staged document directory and new marker;
6. retain the recovery copy and old key material needed for documented
   rollback.

Any interruption before activation leaves the legacy Data Home authoritative.
Any failure after activation restores the named recovery copy or reports a
stable manual-recovery state; it never mixes `.dpapi` and new active records.
A macOS process cannot migrate Windows DPAPI ciphertext. It instructs the user
to perform the migration/export under the original Windows account.

Normal recovery archives contain document ciphertext and key-state metadata,
not the OS master key. They remain account/device-bound. Portable encrypted
export, password/key recovery, and cross-device synchronization are separate
features and must not weaken this boundary. Ordinary uninstall preserves the
Data Home and OS keys; removing both requires an explicit destructive action
that explains recovery loss.

## Verification Gates

- Unit tests use an in-memory credential backend and deterministic envelope
  fixtures; they cover tampering, nonce uniqueness, wrong key/home/purpose,
  missing keys, bounds, rotation lookup, and secret-free errors.
- Windows tests exercise real Credential Manager, legacy DPAPI migration,
  interruption before activation, rollback, and same-account restore.
- macOS arm64 tests exercise real signed-sidecar Keychain add/read/update/delete,
  locked/unlocked behavior, non-synchronization, relaunch persistence, and a
  copied-home missing-key failure. macOS x64 is tested only if that target is
  approved.
- Packaging manifests include `cryptography` native resources but no key,
  credential, Data Home, developer path, or private fixture.
- An independent security review inspects the envelope, native bindings,
  migration, redaction, and target-host evidence before existing ciphertext is
  changed outside synthetic fixtures.

## Alternatives

- Electron `safeStorage`: rejected because Python browser mode and email sync
  would need an Electron-only proxy.
- Direct DPAPI/Keychain encryption of every document: rejected because it
  preserves two ciphertext formats and blocks common validation/key rotation.
- The macOS `security` command: rejected because spawning a shell/tool creates
  argument, prompt, diagnostic, and packaging exposure.
- A generic Python keyring package: rejected because backend selection and
  fallback behavior are broader than this two-platform reviewed boundary.
- Plaintext key files protected by directory permissions: rejected because
  account-scoped OS secret storage is available on both supported platforms.

## Consequences

`cryptography` becomes a reviewed runtime/build dependency. The sidecar must
package its native components for each target. Normal backups remain local
recovery artifacts rather than portable vaults. Key loss is reported honestly
and cannot be repaired by guessing or silently regenerating a key.

## Reversal Conditions

Changing the algorithm, accessibility class, key ownership, synchronization,
or recovery model requires a new envelope version, target-host evidence, and a
recoverable migration. Existing keys/ciphertext remain readable until every
affected object and recovery path has been verified under the replacement.

## Primary References

- Apple, `kSecUseDataProtectionKeychain`:
  https://developer.apple.com/documentation/security/ksecusedataprotectionkeychain
- Apple, Generic Password items:
  https://developer.apple.com/documentation/security/ksecclassgenericpassword
- Apple, Keychain accessibility:
  https://developer.apple.com/documentation/security/ksecattraccessible
- Microsoft, DPAPI scope and limitations:
  https://learn.microsoft.com/en-us/windows/win32/seccrypto/example-c-program-using-cryptprotectdata
- `cryptography`, AESGCM:
  https://cryptography.io/en/stable/hazmat/primitives/aead/
