# Pinned Vault calendar-input validation

This pure package ports only `normalize_calendar_month`, `normalize_date_range`
from `src/date_normalization.py`, and `datetime.date.fromisoformat` as used by
`src/validate_vault.py` for style-rule date validation. It also supplies the
aware-timestamp predicate used by the encrypted document ledger. It does not write, read
files, select a Data Home, guess legacy ranges, format dates for a resume, or
provide a general datetime implementation.

## Contract

```go
NormalizeMonth(cataloginput.Value) (string, *Error)
NormalizeRange(cataloginput.Value) (Range, *Error)
ValidISODate(string) bool
ValidAwareISODateTime(string) bool
```

`Range` contains `Start string`, `End *string`, and `Ongoing bool`, with snake-case
JSON field names. Input Values are already-decoded and structurally bounded by
the existing YAML boundary: scalar UTF-8, unique post-Python-dict mapping entries,
bounded nodes/depth/text. Do not mutate an input concurrently. No scalar values
are coerced: a YAML date, number, bytes or boolean is not a calendar string.
Missing/null/empty-string end is absent; whitespace-only end is an error. Ongoing
must be a bool when present. Validation order is unknown keys, start, ongoing,
end, ongoing/end conflict, end-before-start. Returned End points to owned state.

Python whitespace is collapsed using `textcompat.IsSpace`; the characterized
Unicode15 decimal ranges match Python3.12. Regex shortcuts preserve their source
quirks: a mixed-Unicode year can survive the direct month/year/named-month branch,
but numeric full-date branches convert digits to ASCII. Range comparison uses
those exact normalized strings, not an invented date representation.

The ISO fallback implements the **pinned CPython3.12.14 C parser**, not RFC3339:
basic/extended calendar and week dates, year 1..9999, one arbitrary scalar
separator, partial clock components, comma/point fractions, and offset grammar.
The caller's global `Z` to `+00:00` replacement is retained. There is no timezone
conversion of the returned month. Offset minutes/seconds normalize before the
strict 24-hour bound. Existing permissive edges are retained, including a
terminal byte before an offset (`12x+01:00`), ignored basic-date suffixes accepted
by `date.fromisoformat`, and embedded-NUL terminal behavior. This compatibility
predicate must not be advertised as a strict ISO syntax or input-sanitizing API.

`ValidAwareISODateTime` reuses that parser without the month caller's whitespace
cleanup or global `Z` replacement. It checks timezone awareness in the time
portion, not from a date hyphen or an arbitrary separator. Date-only and naive
timestamps fail. The ledger separately enforces its 64-codepoint bound. Accepted
stored strings remain unchanged; no UTC conversion or editor cleanup occurs.

ISO parsing decisions are adapted from the official CPython3.12.14
[`Modules/_datetimemodule.c`](https://github.com/python/cpython/blob/v3.12.14/Modules/_datetimemodule.c)
and pinned local `Lib/_pydatetime.py`. `LICENSE.PSF` retains the applicable license,
copyright and modification notice. The Go implementation performs explicit
bounded string indexing, and uses standard-library calendar arithmetic instead
of copying Python object-construction machinery. No dependency is added.

## Safe errors and explicit boundaries

`Error` retains only `Code` and `Field` (empty, `start`, `end`, `ongoing`). `Error()`
is fixed. `Format(label)` expects a **trusted schema label**, such as
`career_vault/education.yaml.schools[0].date_range`, never user text. Supported
errors preserve source suffixes except unknown-field names, which are removed.

Codes: `CALENDAR_STRING_REQUIRED`, `CALENDAR_VALUE_REQUIRED`,
`CALENDAR_DATE_INVALID`, `CALENDAR_FORMAT_INVALID`,
`CALENDAR_RANGE_OBJECT_REQUIRED`, `CALENDAR_RANGE_UNKNOWN_FIELDS`,
`CALENDAR_RANGE_ONGOING_BOOLEAN_REQUIRED`, `CALENDAR_RANGE_END_WITH_ONGOING`,
`CALENDAR_RANGE_END_BEFORE_START`, `CALENDAR_INPUT_UNSUPPORTED`.

Non-string range keys trigger Python `TypeError` while sorting/joining unknown
keys. The typed Go boundary returns `CALENDAR_INPUT_UNSUPPORTED` instead of
inventing a supported domain failure or retaining the key. Impossible duplicate
post-dict entries and non-scalar UTF-8 inputs are likewise unsupported. Source
Python surrogate separators cannot be represented in this scalar-only boundary;
the YAML decoder already records that exclusion. Arbitrary Python subclasses,
custom Mapping implementations and their side effects are not represented.

Limits belong to the existing Value/YAML admission contract. This package adds
no narrower date-string limit; deterministic tests cover 100,000-character
whitespace/fraction inputs. All scans are bounded by input size. No all-input
parity, universal memory bound, installed runtime or other-platform acceptance
is claimed.

## Verification

From `backend`, with the pinned Go toolchain and existing offline module cache:

```powershell
go test -buildvcs=false -count=1 -v -cover ./internal/vault/calendarinput
go vet -buildvcs=false ./internal/vault/calendarinput
```

The optional actual-source comparison starts only the explicitly configured
Python executable, verifies version3.12.14, imports this checkout's source and
uses synthetic requests. UTF-8 input is decoded explicitly (Windows text stdin
otherwise depends on its code page). A 20-second context, two-second process
wait delay and fixed output caps bound the reference process; unexpected source
exceptions/infrastructure failures fail the check, without printing raw stderr.
It compares normalized values and fixed code/field/message projections, not only
acceptance. It does not inspect existing Vault contents.

The expanded native Windows run used Go1.27.1 and Python3.12.14: 21,261 actual
source comparisons passed, including direct awareness checks; package coverage
was 98.2%. The previous 13,108-case run covered only the earlier contracts.
This is a fixed synthetic corpus result, not a claim of exhaustive parser parity.

Focused tests also cover error ordering/redaction, caller ownership, optional
fields and the exhaustive pinned 680-decimal-codepoint membership fingerprint.
Fuzz seed execution is not a sustained fuzzing result; a fuzz target is available
with an explicit 4 KiB fuzz-only budget.
