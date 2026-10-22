# SQLite pilot dependency notices

These files are copied byte-for-byte from checksum-verified Go module sources
for the selected maintainer-only SQLite candidate. They do not constitute
production driver adoption or a complete packaged-artifact license audit.

The runtime dependency pins are recorded in backend/go.mod and go.sum. This
bundle includes cross-platform transitive dependencies even when one is unused
by the current Windows build. sqlite-vec and dependency development/test tools
are not imported by this adapter; their separate notices are not included here.
The existing YAML and x/text notices remain in their original directories.
`modernc-memory/LICENSE-MMAP-GO` retains its upstream extra blank line at EOF;
this is the sole intentional `git diff --check` whitespace exception, preserving
the source bytes and recorded hash rather than silently editing a legal notice.

SQLite is public-domain core code with a BSD-3-Clause Go driver. The libc
third-party notice file preserves the upstream Go/musl/go-netdb/NixOS notices.
Memory's embedded Go and mmap-go notices and x/sys's patent grant are retained.
Redistributions must carry the applicable notices and disclaimers. Review the
actual target binary's dependency list again before packaging.

## Source file hashes

Sources are relative to the verified module cache. All hashes are SHA256.
On 2026-10-14 the sqlite v1.60.1 and libc v1.77.1 files were re-hashed and are
byte-identical to the v1.58.0 and v1.75.6 copies retained here.

| Source | Retained file | SHA256 |
| --- | --- | --- |
| `modernc.org/sqlite@v1.60.1/LICENSE` | `modernc-sqlite/LICENSE` | `c6fe05491a60ae13bcd223088d2705e36dede24e5587226231d2459ada5c4822` |
| `modernc.org/sqlite@v1.60.1/LICENSE-SQLITE` | `modernc-sqlite/LICENSE-SQLITE` | `8438c9c89b849131ead81d5435cb97fcf052df5b0b286dda8a2d4c29e6cb3fd0` |
| `modernc.org/libc@v1.77.1/LICENSE` | `modernc-libc/LICENSE` | `95ff867eb55a56935fa7492406cfa953fb7c13ca73f4c0a86ae05756b4605600` |
| `modernc.org/libc@v1.77.1/LICENSE-3RD-PARTY.md` | `modernc-libc/LICENSE-3RD-PARTY.md` | `f597097efe3d97021f89170746bd3a0fb9a8b6fb26b82043ed68a4e0283bee6c` |
| `modernc.org/mathutil@v1.7.1/LICENSE` | `modernc-mathutil/LICENSE` | `bfa9bf72a72ca009fd62a8f84fca3dca67e51d93af96352723646599898b6cf5` |
| `modernc.org/memory@v1.12.1/LICENSE` | `modernc-memory/LICENSE` | `59895e669f48f168b6b858358f6005779cdf40a265f7828813061b56af67b496` |
| `modernc.org/memory@v1.12.1/LICENSE-GO` | `modernc-memory/LICENSE-GO` | `2d36597f7117c38b006835ae7f537487207d8ec407aa9d9980794b2030cbc067` |
| `modernc.org/memory@v1.12.1/LICENSE-MMAP-GO` | `modernc-memory/LICENSE-MMAP-GO` | `c2eba69f20d05414538c3a5df7694dde392e065ff70882e1625e90f5d6659fff` |
| `github.com/dustin/go-humanize@v1.0.1/LICENSE` | `go-humanize/LICENSE` | `a973b4498c13eb74baa2a8e5c351426a6826f2fcdd909916dbe53ee2e755fd71` |
| `github.com/google/uuid@v1.6.0/LICENSE` | `google-uuid/LICENSE` | `0a8d61ed3cbfd5312326e8126c31ce9c627a283adc99131b56896d29ada04b2d` |
| `github.com/mattn/go-isatty@v0.0.24/LICENSE` | `go-isatty/LICENSE` | `08eab1118c80885fa1fa6a6dd7303f65a379fcb3733e063d20d1bbc2c76e6fa1` |
| `github.com/ncruces/go-strftime@v1.0.0/LICENSE` | `go-strftime/LICENSE` | `38ae43959daf953a393a585b2988672cb65a5a541aca0d0be5e72595a0a16883` |
| `github.com/remyoudompheng/bigfft@v0.0.0-20230129092748-24d4a6f8daec/LICENSE` | `bigfft/LICENSE` | `dd26a7abddd02e2d0aba97805b31f248ef7835d9e10da289b22e3b8ab78b324d` |
| `golang.org/x/sys@v0.47.0/LICENSE` | `golang-x-sys/LICENSE` | `911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad` |
| `golang.org/x/sys@v0.47.0/PATENTS` | `golang-x-sys/PATENTS` | `96f408bfae65bf137fc2525d3ecb030271c50c1e90799f87abf8846d8dd505cc` |

Source provenance: [modernc SQLite v1.60.1](https://gitlab.com/cznic/sqlite/-/tree/v1.60.1),
[modernc libc v1.77.1](https://gitlab.com/cznic/libc/-/tree/v1.77.1), and the
module paths/versions above. A source hash records exact retained bytes, not
legal advice or a vulnerability review.
