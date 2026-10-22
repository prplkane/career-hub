# Pinned Python text semantics

This internal adapter preserves Python 3.12 / Unicode 15.0 identity rules
while the backend is migrated. It is not a fuzzy matcher, tokenizer, general
Unicode security filter, or a replacement for input validation.

`Fold` uses default full C+F mappings; `Strip` trims Python's 29 whitespace
characters; `Normalize` folds, splits on those characters and joins with ASCII
space. Those helpers do not apply NFC/NFKC, accent removal, locale casing or punctuation changes.
Composed and decomposed accents remain distinct. Go/Python source offsets are
not transformed or interpreted by this package. Inputs are valid UTF-8 scalar
strings; unpaired surrogates and malformed UTF-8 require an explicit decoding
boundary policy, not silent replacement by the caller.

## Why generated data, not a frozen library dependency

Current [`golang.org/x/text/cases` v0.41.0](https://pkg.go.dev/golang.org/x/text@v0.41.0/cases)
uses Unicode 17 and has no version selector. Adopting it directly changes existing
skill keys: U+1C89 and U+A7CB now fold but Python15 leaves them unchanged. Keeping
an older broad dependency or adding an upgrade-sensitive override layer is not
needed for one fixed identity contract. The case/whitespace helpers use the Unicode
Consortium's data and standard-library code, without data-file reads. The separate
CH-44 source-fingerprint normalizer reuses the existing pinned x/text dependency
as described below; it does not alter skill keys or the other helpers.
Changing the Unicode contract later requires a reviewed identity/revision migration.

## Reproduction and license

Downloaded 2026-09-07 from the official HTTPS source:

- [Unicode15 CaseFolding.txt](https://www.unicode.org/Public/15.0.0/ucd/CaseFolding.txt),
  retained unmodified under `ucd/CaseFolding-15.0.0.txt`; SHA-256
  `cdd49e55eae3bbf1f0a3f6580c974a0263cb86a6a08daa10fbf705b4808a56f7`.
- [Unicode License v3](https://www.unicode.org/license.txt), retained unmodified
  as `ucd/LICENSE.txt`; SHA-256
  `e7a93b009565cfce55919a381437ac4db883e9da2126fa28b91d12732bc53d96`.
  Preserve this notice in source and associated packaged documentation before
  redistributing a binary containing the generated data. Packaging is not yet wired.

From `backend`, run `go generate ./internal/textcompat`. The offline generator
checks both pinned hashes, C+F uniqueness, scalar ranges and 1530 mappings before
writing only the named `tables_generated.go` in the package. Tests reproduce the
exact source bytes; editing the generated table alone fails that check.

The table contains 1530 nonidentity mappings, 104 expansions, and identity fallback
for unlisted scalars. It is a sorted private array; lookup and public helpers have
no mutable per-call/shared caches. Native race execution is still a separate gate.

## Exhaustive scalar comparison

The Go test computes a SHA-256 stream over all **1,112,064 Unicode scalars** in
ascending order, excluding D800-DFFF. For each scalar: its four-byte big-endian
value; then each of `Fold`, `Strip`, `Normalize` as four-byte UTF-8 byte length plus
UTF-8 bytes; then one byte for `IsSpace`. The independently computed Python
3.12.14/UCD15.0 stream uses `casefold`, `strip`, `" ".join(casefold().split())`
and `isspace`. Its fingerprint is
`15a49065d359eba87a56cb769dd7a0ed6ba79d2f4c67012fa54ccdc74c5e348f`.

This covers every scalar's output, including unassigned identity characters and
all whitespace. Contextual fixtures additionally cover Turkish I, Greek sigma,
Cherokee, ligatures, combining marks, C++/.NET, emoji and retained zero-width
characters. It does not claim exhaustive equality over every string, malformed
encoding, spaCy tokenization, YAML coercion or the complete Skills Registry.

## Printable property for Vault display labels

`IsPrintable` implements Python3.12.14/UCD15's scalar printable property,
including ASCII space but excluding other separator/control/unassigned scalars.
This is needed only when preserving Python's nested string `repr` in legacy
Vault evidence labels; it is not a UI sanitization or permission decision.
Go1.27.1's `unicode` tables use Unicode17 and would display newly assigned
characters differently from the source. Raw user strings remain unchanged.

`internal/generate_printable.py` queries the pinned interpreter's `isprintable`
and emits the Go table to stdout; it does not read/write user files and is not
a runtime dependency. Run the script with Python3.12.14 `-I -B`, review its
output and regenerate only `printable_generated.go`, then format with `gofmt`.
The existing `go generate` command regenerates only case-fold data.

The generated table has 711 disjoint ranges and 148,998 printable code points.
The SHA-256 of one 0/1 byte for each code point U+0000..U+10FFFF is
`a24a109bc8e347f9490466c59e4160d0dc9dd1006a5be9a4c91af71cd2e7fa99`.
Tests recompute the exhaustive fingerprint, validate canonical ranges and
reject out-of-range, surrogate and post-Unicode15 examples. Unicode data carries
the retained `ucd/LICENSE.txt` notice above.

## Uppercase and decimal conversion for resume metadata

`Upper` preserves Python3.12.14/UCD15 locale-independent full uppercase, including
expansions such as `ß` to `SS`. `DecimalDigit` recognizes only the source Unicode
decimal digits, not superscripts or other numeric lookalikes. Resume management
uses these for filename type labels and legacy `int(size_bytes)` conversion;
they do not change skill identities or add a filename-access permission.

`internal/generate_upper.py` emits both private sorted tables to stdout with the
pinned interpreter (`-X utf8 -I -B`); it performs no filesystem I/O. Review the
output into `upper_generated.go` and format it with `gofmt`. The existing
`go generate` command still regenerates only case folds. The generated data
retains the Unicode license above, with no new runtime dependency.

Tests fingerprint all changed uppercase scalars as `hex:UTF8-uppercase\n`:
`b648162bdf8db291c9414eec55ad0b05450c25a41dbffbd5cf28c72681ec79ab`.
The decimal fingerprint stores one byte per code point U+0000..U+10FFFF,
zero for nondigits and digit+1 otherwise:
`63786f7ca550846ae22618aeff8eb7c9dcd25b8aae895b6bf8276ce90abec8b5`.
Post-Unicode15 characters remain unchanged/unrecognized. These are exhaustive
scalar checks, not claims about malformed UTF-8, every string or native race safety.

## JD analysis properties and source normalization

`IsWord`, `IsUpperString` and `SplitLines` preserve Python's regex word property,
uppercase-string predicate and line separators. `internal/generate_analysis_properties.py`
emits 2,503 private property ranges from Python3.12.14/UCD15 to stdout. The
all-code-point property-byte SHA-256 is
`d6017d99ed01c04e23484b6180a093a5c1bd9b9c997832967d2275c3921d8702`.
It retains the Unicode license above; `go generate` still only regenerates folds.

`NFKC15` is a distinct bounded operation for existing complete-JD fingerprints,
not skill-key normalization or evidence rewriting. It reuses stable per-scalar
data from the pinned x/text Unicode17 implementation for assigned-Unicode15
characters, orders combining classes and composes without stream-safe insertion.
Later assignments stay opaque. It rejects invalid UTF-8, changed normalization
data version and explicit memory/output budgets atomically. All valid scalars
plus contextual sequences are compared with actual Python.

See the analysis contract
for source provenance, budgets, command lines, fixture counts and limitations.

## Combining marks for registered literal boundaries

`IsMark` freezes the Mn/Mc/Me categories to Unicode15, keeping a combining mark
attached to its neighboring literal token without inheriting Go's Unicode17
assignments. `internal/generate_mark.py` queries pinned Python3.12.14 and prints
`mark_generated.go` to stdout; review/apply it, then run `gofmt`. It does not read
private files or run in production; `go generate` still only regenerates folds.
The table retains `ucd/LICENSE.txt`. Tests reconstruct the byte stream for all
U+0000..U+10FFFF (one 0/1 byte each), with SHA-256
`e6fbe2b96cfd2ab74a4042a43d342474a2ec2b93b3c95c8c07e26614eef2bae7`.
This boundary property changes no existing fold, identity, word or NFKC contract.

## Full lower/title casing for JD hostname display

`Lower` and `Title` preserve Python3.12.14/UCD15 full casing, including dotted-I
and ligature expansions and contextual final sigma. `Title` follows `str.title`,
not editorial title casing: a combining mark or apostrophe starts a new word.
Sigma context skips Case_Ignorable characters; a dot can therefore join the
casing context across hostname labels. These helpers only serve the review-only
company-name fallback. They do not rewrite evidence or registry identities.

`internal/generate_case.py` emits private compact mappings and Case_Ignorable
ranges to stdout using pinned Python3.12.14 (`-X utf8 -I -B`). The property is
observed from two independent minimal final-sigma contexts, including characters
that are both cased and ignorable. Review/apply the output into
`case_generated.go`, then format with `gofmt`; `go generate` still only
regenerates folds. Preserve the existing `ucd/LICENSE.txt` notice.

Tests recompute all changed-scalar `hex:UTF8-mapping\n` fingerprints:
lower `41cbc053e29ec86177d97c065af93df94e1180f3f6823b5bf9f4c25d9c77ab64`;
title `5c5313a924cb7c81d8cd80a030aed468af69dc7a9aff432df03d371dbd1ad9f6`.
The one-byte-per-code-point Case_Ignorable fingerprint is
`cfafce96d60bf7a7d32406fc211aaf08a204804a8db4f456f16759e0c417bf0f`.
Twenty-three contextual source fixtures supplement these scalar/property checks;
they do not claim exhaustive equivalence over all strings or malformed UTF-8.
