// Package cataloginput validates decoded catalog values without loading
// files. A Value keeps the decoded input kinds (null, string, bytes, bool,
// int, float, date, datetime, sequence, mapping) that ordinary JSON would
// erase; integers are decimal text, floats repr-style text, and dates and
// datetimes ISO text.
//
// ParseYAML(raw) is the bounded decoding boundary for one UTF-8 YAML
// document (a BOM is accepted; invalid UTF-8 and UTF-16 fail before
// parsing). It applies YAML 1.1 implicit scalar rules (yes/no/on/off,
// leading-zero octal, sexagesimal numbers, null spellings, dates), keeps
// mapping duplicates last-wins in first insertion order, flattens merge keys
// before local pairs, and expands non-recursive aliases into independent
// trees. Limits: 2 MiB of raw bytes, nesting 64, 100,000 syntactic and
// 100,000 expanded nodes, 10,000 alias dereferences, 8 MiB expanded scalar
// text and 4,300 integer digits. A Kind="limit" result is a hardening
// refusal; Kind="unsupported" (sets, omaps, pairs, recursive values, aliases
// to special keys, NaN keys, escaped lone surrogates, tagged merge sources)
// must never be read as empty data. YAMLError carries fixed codes and the
// parser stage and position, never source text, key or anchor names, or
// paths.
//
// ValidateTerms and ValidateGroups validate term and group mappings. Results
// are independent values: empty arrays stay arrays, omitted nullable fields
// become null, kind defaults to technology, archived to false, and
// Term.FieldsSet records exactly the fields explicitly supplied. IDs are
// strict ASCII snake case; strings are not coerced from numbers, dates or
// booleans; name, alias and timestamp handling uses textcompat. Failures are
// ordered type/loc/msg issues (declared field order, nested errors in input
// order, then extra keys) with no partial record list, no raw input and no
// traceback; an unrepresentable input returns ValidationError.Unsupported.
// The issue list is bounded to 10,000 entries and 4 MiB encoded.
//
// model_decimal.go pins the Unicode 15 Nd ranges used by the ID and
// timestamp rules; regenerate it from the Unicode 15 character database
// (unicodedata.category == "Nd" over every code point), not from Go's newer
// tables, and keep the exhaustive membership hash test passing.
package cataloginput
