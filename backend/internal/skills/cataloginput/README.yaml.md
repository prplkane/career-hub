# Bounded raw YAML adapter (CH25, offline only)

`ParseYAML(raw []byte) (Value, *YAMLError)` is a decoding boundary, not a catalog
loader or model validator. It does not access files, normalize `safe_load(...) or
[]`, apply defaults or legacy alias cleanup, activate a product route, or touch
private data. The caller owns the TechnologyTerm / SkillGroup model and loader
steps. Presence survives as ordered mapping entries, including explicit null,
false, empty collections, and merged fields.

## Reference and dependency

The reference is Python 3.12.14 + PyYAML 6.0.3, with catalog bytes first decoded
using UTF-8, matching `path.open(encoding="utf-8")` in the Python product. This
is deliberately not `yaml.safe_load(raw_bytes)`, which also accepts UTF-16.
UTF-8 BOM works; invalid UTF-8 and UTF-16 bytes fail before YAML parsing.

The pinned module is `go.yaml.in/yaml/v4 v4.0.0-rc.6`, Go module directive 1.18,
no external module requirements and no cgo. Normal `go get`, `go mod tidy`,
and `go mod verify` were used with isolated workspace caches and the standard
Go proxy/checksum service. No global package install or replacement directive.

Provenance, checked 2026-09-07:

- [Upstream tag](https://github.com/yaml/go-yaml/tree/v4.0.0-rc.6), commit
  `1c17b9cee72bf81ae73a8be50fbd7dd3a9985125`, published 2026-06-17.
- Module sum `h1:1h7H1ohdUh93/FyE4YaDa1Zh64K6VVbjF4K6WUxMtH4=`.
- go.mod sum `h1:aZqd9kCMsGL7AuUv/m/PvWLdg5sjJsZ4oHDEnfPPfY0=`.
- [Checksum record](https://sum.golang.org/lookup/go.yaml.in/yaml/v4@v4.0.0-rc.6).
- [API documentation](https://pkg.go.dev/go.yaml.in/yaml/v4) and
  [limit plugin contract](https://pkg.go.dev/go.yaml.in/yaml/v4/plugin/limit).

v4 is a **release candidate**, not a stable-release claim. Upstream maintains
v4 development; v3 remains a security-fix fallback (`go.yaml.in/yaml/v3 v3.0.5`
was current at this review). v4 was selected for explicit parser-depth hooks,
structured stage errors, and AST handling; its readiness must be re-reviewed
before cutover. The official Go vulnerability index had no exact v4-module
entry on the check date, and GitHub's exact package/version advisory query was
empty. This is not a vulnerability-free guarantee. Repeat vulnerability and
dependency review at release; older YAML-path advisories are relevant history.

Apache-2.0 plus MIT-covered libyaml portions apply. Preserve the upstream
LICENSE/NOTICE and supplied MIT permission text under `backend/licenses/go-yaml-v4`.

## Supported semantics

- Empty stream becomes null. Exactly one document is required: the streaming
  loader performs two AST loads and requires EOF on the second. Native
  `WithSingleDocument(true)` is not used because it can stop after the first.
- YAML 1.1 PyYAML implicit scalar rules override the library's native resolver:
  yes/no/on/off, leading-zero octal, binary/hex integers, sexagesimal numbers,
  PyYAML exponent rules, null spellings, date/datetime, and quoted/folded text.
  `y`, `n`, `08`, `0o10`, and `1e3` are strings. Non-specific `!` tags preserve
  PyYAML's implicit resolution even on quoted scalars.
- Supported safe explicit scalar tags have separate constructors. Dates keep
  date type, datetimes preserve naive/aware distinction and microseconds;
  binary values remain bytes. Binary ASCII validation precedes permissive
  base64 decoding, including ignored characters after a padded quartet.
- Floats retain shortest Python-style repr, infinities, NaN, and negative zero;
  arbitrary-size integers use decimal text within the stated safety budget.
  Go-only hexadecimal float syntax is rejected for explicit float tags.
- Mapping duplicates are last-wins while preserving first insertion order and
  the first equal key's type. Bool/int/float numeric equality uses exact rational
  values, not Go's heterogeneous map equality. Aware datetime keys compare UTC
  instants; strings and bytes remain distinct.
- Merge mappings are flattened before local pairs; earlier maps in a merge
  sequence take precedence. Explicit local values always override merges.
- Non-recursive aliases expand to independent Value trees, with their own
  budgets; no mutable Value collection is shared between alias instances.
- Construction failures get a bounded failure-only FIFO preflight, preserving
  PyYAML's deferred collection-construction error precedence on the tested
  compound-invalid inputs. It does not reproduce arbitrary custom constructors.

## Explicit exclusions, not successful parity

`Kind="unsupported"` means the adapter cannot claim parity for this input.
Do not convert it to empty data or count it as a passing validation failure.

- `!!set`, `!!omap`, and `!!pairs`: Value has no set/tuple representation.
- Recursive values and recursive merges: Value is an acyclic tree, while
  PyYAML can construct recursive objects and mutate merge nodes.
- Alias references to special `=` / `<<` keys: Python constructor mutation and
  alias identity affect the result. Code `YAML_SPECIAL_KEY_ALIAS`.
- NaN mapping keys: Python object identity affects equality/deduplication.
- Escaped lone Unicode surrogates: Python can produce non-scalar Unicode
  strings; the Go Value contract cannot. The native failing escape coordinate
  is inspected; no replacement character or lossy UTF-8 is emitted.
- Non-ASCII explicit numeric forms not handled by Go's conversion routines.
- Scalar tags applied to special `{=: ...}` mappings; PyYAML has additional
  constructor-specific behavior here (`YAML_SCALAR_MAPPING_VALUE`).
- Merge source mappings/sequences with non-map/non-sequence tags. PyYAML may
  discard such tags during flattening; the adapter refuses to silently accept
  them, especially unsafe tags (`YAML_TAGGED_MERGE`).
- A failure-only preflight unable to reconcile depth-first and deferred
  construction reports `YAML_CONSTRUCTOR_ORDER`, not false class parity.

Unknown/unsafe constructors are never run. Unsupported tagged merge nodes are
rejected even where PyYAML would ignore their tags. Native lexical/parser error
prose and exact PyYAML exception classes are **not** claimed as equivalent.
`GoYAMLLoadError` retains the dependency's stage and available 1-based position.
Adapter-owned failures use fixed codes and the known constructor class. No raw
dependency prose, source text, key/anchor names, or paths enter YAMLError.

## Resource limits and where they apply

| Budget | Maximum | Enforcement |
| --- | ---: | --- |
| Raw UTF-8 bytes | 2 MiB | Before parser allocation |
| Nesting | 64 | Scanner hook, AST inspection, tree/merge walk |
| Syntactic nodes | 100,000 | After bounded AST parse, before conversion |
| Traversal/expanded nodes | 100,000 | During conversion and merge flattening |
| Alias dereferences | 10,000 | During tree expansion and merges |
| Expanded scalar source text | 8 MiB | Before scalar conversion for each visit |
| Integer digits | 4,300 | Before/after integer construction |

The failure-only ordering preflight has independent node/alias/text budgets.
Node budgets count traversal work, not only final deduplicated dictionary size.
Native constructor alias limits do not cover custom Node walking, hence the
adapter's own alias/cycle limits. Parsed AST size is first bounded by raw input
and scanner depth; node counting is not advertised as a pre-allocation parser
limit. Library allocation overhead is not an exact memory cap. There is no
in-process wall-clock cancellation API: a maintainer harness must still impose
a process timeout/memory limit. These limits intentionally narrow Python's
accepted input space; `Kind="limit"` is a hardening exclusion, not parity.

## Verification and continuation

`go test ./internal/skills/cataloginput` runs deterministic scalar, mapping, failure,
alias-isolation, UTF-8, and limit cases. The optional independent reference test
requires `CAREERHUB_YAML_PYTHON` to point to the existing pinned Python runtime:

```powershell
$env:CAREERHUB_YAML_PYTHON = 'C:\path\to\pinned\python.exe'
go test -count=1 ./internal/skills/cataloginput
go test ./internal/skills/cataloginput -run '^$' -fuzz FuzzParseYAML -fuzztime=15s -parallel=2
```

The reference subprocess is isolated (`-I -B`), receives synthetic JSON on
stdin decoded explicitly as UTF-8, checks PyYAML 6.0.3, and returns typed ordered
values or exception class only. It has a 30-second timeout and a two-second pipe
wait after termination; it neither reads nor writes user data. The
initial corpus contains 520 synthetic documents, including 400 deterministic
finite-float samples. A 15-second fuzz run completed 59,466 executions without
a crash; this is a bounded smoke check, not exhaustive parser proof.

The parent has integrated this boundary with model/loader characterization and
replayed 145 public/synthetic byte cases plus 276 model/preparation cases, all
passing their specified comparisons. See the durable evidence.
Keep every exclusion visible: byte-level YAML failures compare stage, not native
parser prose. Production error envelopes, managed filesystem loading/writes,
private-data migration, and product cutover are not declared complete by this adapter.
