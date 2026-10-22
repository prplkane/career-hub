# Catalog model-input pilot

This internal, in-memory package mirrors the supported decoded-value boundary of
Python `TechnologyTerm`, `CompetencyRelation`, and `SkillGroup`. `ValidateTerms`
and `ValidateGroups` correspond to validating lists of those models. They do not
read files or activate a Go runtime in the product.

## Contract

`Value` retains raw Python types rather than conflating them through ordinary
JSON. The caller provides a finite, bounded, immutable-during-validation tree
with canonical scalar representations. Supported kinds are `null`, `string`,
`bytes`, `bool`, `int`, `float`, `date`, `datetime`, `sequence`, and `mapping`.
Integers use decimal text; floats use Python `repr` including `nan`, `inf`, and
`-inf`; dates and datetimes use Python `isoformat`. Strings are scalar UTF-8.
Byte values may contain invalid UTF-8; field validation reports that failure.

Mappings represent Python dictionaries *after* key equality/collision handling
and in insertion order. An external adapter must reject impossible tagged
representations, including duplicate/equal keys, or implement the preceding
dict-construction semantics explicitly. The validator additionally rejects
duplicate string keys instead of overwriting them. It is not a YAML parser.

Success creates independent arrays and optional-field pointers. Empty arrays
remain arrays; omitted nullable fields become null; `kind` defaults to
`technology`; `archived` defaults to false. `Term.FieldsSet` is sorted and records
exactly the recognized fields explicitly supplied, including explicit defaults
and nulls. Global duplicate IDs and graph validation are outside this layer.

Validation failures contain ordered `type`, `loc`, and `msg` issues only: known
fields in model declaration order, nested/list errors in input order, then extra
and invalid keys in mapping order. After-field validators run only after their
field succeeds; term name/alias uniqueness runs only after the whole model
succeeds. Failure returns no partial record list. Raw `input`, exception context,
documentation URLs, and Pydantic's rendered traceback are deliberately omitted.
Locations can still contain input field names or invalid-key representations;
this is a maintainer contract, not a promise that diagnostics contain no data.

## Characterized compatibility details

- Strings accept UTF-8 bytes but not numeric/date/bool coercion. Enums return
  enum errors even when their raw type is not a string. IDs use strict ASCII
  snake-case matching without trimming.
- Name/category/alias/timestamp stripping and name uniqueness use pinned
  Python3.12/UCD15 `textcompat`, not Go's Unicode version.
- Boolean coercion follows the source: recognized ASCII words ignore case but
  not surrounding whitespace; numeric zero/one are accepted. Other signed64
  integers/integral floats yield `bool_parsing`; nonintegral, nonfinite and
  out-of-signed64-range numbers yield `bool_type`.
- Timestamps retain every original fractional digit. CPython's datetime parser
  checks the first six fractional digits; the source regex checks Unicode15 Nd
  membership for the entire fraction. Consequently an Nd digit after six ASCII
  digits is accepted, while the same digit among the first six is rejected.
- Nonstring-key error locations support null (`None`), bool (integer0/1),
  signed64 integer or larger decimal text, Python float repr, and Python bytes
  repr. Date/datetime-key repr and non-scalar/unhashable keys are explicitly
  unsupported in this slice. Date/datetime *field values* are supported inputs
  and receive the source model's ordinary type errors.

Unknown raw kinds, nonscalar UTF-8 text, and unsupported key representations
return `ValidationError.Unsupported`, not a claimed Pydantic validation match.
The adapter should map that distinction to a sanitized unsupported-input error.

The maintainer adapter has a 10,000-issue and 4MiB encoded issue-array budget.
Each issue is measured before appending. Overflow discards the incomplete issue
list and returns `Unsupported`; it never reports truncated validation as parity.
These are operational bounds, not product rules or a claim about peak heap use.
The caller still owns input node/depth/byte bounds and final response budgeting.

## Validation and Unicode evidence

Focused Go tests cover defaults/presence, mutable-output isolation, ordered
errors, coercions, nested relations, enum choices, alias uniqueness, cascades,
timestamps, invalid-key locations and explicit unsupported/resource boundaries.
The parent migration harness compares synthetic and public core fixtures to the
actual installed Python models; private catalogs are not test inputs here.

`model_decimal.go` has 64 contiguous ranges representing all 680 Unicode15 Nd
scalars. This is a characterized category table, independent of newer Go Unicode
data. Its exhaustive SHA256 is
`93a69f3604c97cdaa6460b7acf761f7edd20725f281233cd2a52a9dcfa38a312`.
Regenerate/verify using the pinned Python3.12 runtime:

```python
import hashlib, unicodedata
assert unicodedata.unidata_version == "15.0.0"
points = [i for i in range(0x110000) if unicodedata.category(chr(i)) == "Nd"]
ranges = []
for point in points:
    if ranges and ranges[-1][1] + 1 == point:
        ranges[-1][1] = point
    else:
        ranges.append([point, point])
print(", ".join("{0x%X, 0x%X}" % tuple(span) for span in ranges))
print(len(points), hashlib.sha256(b"".join(i.to_bytes(4, "big") for i in points)).hexdigest())
```

Run `go test ./internal/cataloginput -count=1 -cover` and
`go vet -buildvcs=false ./internal/cataloginput` from `backend`, with the pinned
Go1.27.1 toolchain. Native Windows checks do not establish race-detector or
Linux/macOS runtime coverage. No YAML coercion/parser parity is claimed here.
