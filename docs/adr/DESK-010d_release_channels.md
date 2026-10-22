# DESK-010d: Keep Dev, Beta, And Stable Release Channels Explicit

- **Status:** Accepted
- **Decision date:** 2026-08-27
- **Evidence tickets:** `ELEC-330`, `ELEC-350`

## Context

Desktop dogfood and public releases have different trust and support needs, but
the channel must not become an excuse for hidden data sharing, silent updates,
or weaker runtime security.

## Decision

Career Hub has exactly three channels:

- `dev`: local developer builds; unsigned unless a developer explicitly tests
  signing; never offered as an update to another channel.
- `beta`: opt-in, signed/notarized prereleases for bounded testing; separate
  data and secrets; no automatic promotion to stable.
- `stable`: signed/notarized reviewed releases for normal use.

Every process handshake and diagnostic record identifies the channel. Channel
selection is build metadata, not a renderer or command-line user preference in
an installed release. There is no updater in the foundation phase. Any future
update check, download, or channel switch requires a separate reviewed design,
explicit consent, signature verification, and recovery behavior.

## Alternatives

- Stable only: rejected because it makes dogfood indistinguishable from a
  supported release.
- Arbitrary named channels: rejected because they multiply data and support
  contracts.
- Infer channel from a version suffix: rejected because version and storage
  identity have different compatibility roles.

## Consequences

Build and installer jobs must be channel-aware, while product logic remains the
same. Beta findings cannot silently modify stable state, and stable promotion is
a new build from reviewed source rather than relabeling an installed beta.

## Reversal Conditions

Adding, merging, or renaming a channel requires signed-release evidence plus a
tested data/secret migration that never aliases two writable homes.

## Amendment 2026-10-20: Unsigned Windows Updates

The owner decided not to buy a code-signing certificate for this unpaid open
source product. Windows builds of any channel may be published unsigned through
`.github/workflows/release.yml` and installed builds update from that feed. This
replaces "signed/notarized" for Windows `beta` and `stable` and the
"no updater" statement above; macOS still requires signing and notarization.

- **Consent:** the existing Settings updater checks and downloads, but installs
  only on **Restart and install** or a later normal restart.
- **Integrity without a signature:** packages come over HTTPS from a GitHub
  Release created only by a tag on `main`; Squirrel checks each package hash
  against `RELEASES` from the same release. A compromised publisher account could
  ship a malicious update, so publishing uses only the workflow token on a tag,
  `main` is protected and the owner account uses 2FA.
- **Channels stay separate:** one feed serves one channel; changing it never
  moves a Home, and restore still refuses another channel's backup.
- **Reversal:** add Authenticode when free signing is available, and require it
  before recommending the app to people who cannot judge the SmartScreen
  warning.

## Amendment 2026-10-21: Two Channels

The owner retired `beta`. Career Hub has exactly two channels: `dev` for
development builds and `stable` for releases. Git branches and pull requests
already separate work in progress, so a third installed identity, Data Home and
validation path added nothing. Packaging, installer identities, launch and
build metadata, the sidecar start protocol and every backend channel check now
accept only `dev` and `stable`, and refuse `beta`. No `beta` Home was in use.

## Amendment 2026-10-22: Public Feed Repository

Installed 0.3.0 builds could not check for updates: this source repository is
private, so its release feed answers 404 to anonymous requests. The owner chose
one public repository, `prplkane/career-hub` (created as `career-hub-public`
and recreated clean on 2026-10-22, where releases are also built), for both the curated
source export and the releases, instead of a binaries-only repository or making
this repository public, so users get the installer and updates and readers get
the source without the development history.

- **Publishing credential:** the release job only reads this repository and
  publishes with a fine-grained token limited to Contents on the public
  repository, held as a secret and used only by the tag-gated publish step.
  A leaked token could still ship a malicious update, so the mitigations above
  stay: tag-only publishing from CI, protected `main` and 2FA.
- **Guard:** a release stops before building when the public repository is
  missing or private or the secret is unset.
- **Migration:** builds up to 0.3.0 embed the private feed and need one manual
  install of the next release.
