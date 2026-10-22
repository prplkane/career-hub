# x/net HTML tokenizer notice

`golang.org/x/net v0.60.0` supplies only the HTML tokenizer used by the
offline MIG-170 email extraction seam. Its upstream BSD-style LICENSE and
PATENTS files are retained here for the future packaged binary notice set.
This does not complete the package-wide license or security review.

Upgraded from v0.58.0 on 2026-10-13 for CVE-2026-78669 (HTTP/2 SETTINGS
denial of service, fixed in v0.60.0). The tokenizer is not affected, but the
module-level scanner gate is. `LICENSE` and `PATENTS` are byte-identical
between the two tags.

The retained files were compared against the checksum-verified module source:

| File | Upstream SHA-256 |
| --- | --- |
| `LICENSE` | `911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad` |
| `PATENTS` | `96f408bfae65bf137fc2525d3ecb030271c50c1e90799f87abf8846d8dd505cc` |

Source: [pinned Go x/net module](https://github.com/golang/net/tree/v0.60.0).
