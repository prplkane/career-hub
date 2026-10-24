// Package vaultread owns fixed read-only Career Vault loading, manual,
// calendar and reference validation, and bounded JSON projection. Read(ctx,
// root, View) dispatches a closed view enum (Profile, Eligibility,
// Education, Experiences, Projects, legacy Skills, Snapshot); an unknown
// view is refused before any root is opened.
//
// The loader reads eight files from the supplied Data Home in a fixed
// order: profile, education, eligibility, experiences, projects, legacy
// skill blocks, proof blocks and style rules. Every file must exist and
// decode to its declared mapping or sequence; null or empty YAML is not a
// default document, and the first path, read, YAML or root-shape failure
// stops the load before manual validation. There is no resource fallback,
// default-file creation, profile discovery or migration. Each file uses the
// managed reader and the 2 MiB YAML bound, and its revision is hashed from
// the same bytes that were decoded, including CRLF and BOM; no file is
// reopened. Context cancellation is checked between reads and parses. Reads
// are not a cross-file transaction, and the caller rechecks Data Home
// identity around them.
//
// Projections retain unknown fields, original values and insertion order
// and insert no defaults. Education returns schools and certifications, with
// an absent certifications collection projected as an empty list. Dates,
// datetimes, bytes and nonfinite numbers are refused in a response. The
// response is bounded to 4 MiB rather than truncated.
//
// Error keeps a fixed stage, source and code, sanitized YAML stage and
// coordinates, and at most 256 ordered issues holding only fixed schema
// fields and integer positions; formatting never includes the root, native
// cause, private text or parser prose. Unsupported YAML, resource limits and
// cancellation are separate from validation failures, which the HTTP layer
// maps to a fixed 409 response.
package vaultread
