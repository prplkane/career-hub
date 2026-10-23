# Pure resume extraction (CH-76, complete within the declared subset)

This package has no filesystem, Data Home, SQL, credential, HTTP or publication
authority. `Extract` accepts bytes, checks their SHA-256 against the caller's
source identity, parses TXT/DOCX and builds a report. `ParseTXTBlocks` and
`ParseDOCXBlocks` expose the pure adapters; `Build` accepts ordered blocks,
an already verified source identity, an effective registry revision and
validated resolver terms. Callers must not
interpret a match as approved candidate evidence. The native preview
Resume-Version digest and general `/api/extract` routes invoke this package
through owned publication services; the Python extractor is retired, and the
read-only management and agent-digest owners compare each stored run against
`intakeextract.ExtractorVersion` (`vault-intake-0.3`), so a run left behind by
Python `vault-intake-0.2` reads as `outdated`.

The fictional source corpus is
`tests/fixtures/migration_resume_extractor_cases.json`, generated for review
through `tests.test_migration_resume_extractor --print-cases` and pinned by
Python/Go tests. It covers TXT line endings/blank/Unicode bullets and date
word boundaries, DOCX
paragraph/table order, headings, custom styles, hyperlink text, list
style/numbering, tabs/breaks, sections,
dates, literal skills, ambiguity, archive and codepoint spans. Generated Go
reports pass both `intakeread.ParseExtraction` and the actual Python
`ExtractionReport` model. The full byte-to-report entry point is tested for
exact source SHA and effective revision propagation. The existing registered-literal matcher supplies
bounded token/source-span semantics; its reviewed corrections are identified
by `registered_literal_v1`, and the report uses `vault-intake-0.3` instead of
Python's `vault-intake-0.2`. Section hints identify the native
`registered_section_v1` recognizer rather than claiming a spaCy pass.

This is not full arbitrary-DOCX parity or a complete OPC package validator.
The supported subset requires the declared main document/style content types
and internal root/document style relationships at their ordinary paths.
The standard-library adapter rejects DTD, duplicate/unsafe or symlink ZIP
members, content controls, alternate chunks, unknown body/paragraph/run
wrappers, tracked changes, field text, merged cells, nested tables, unknown
paragraph styles and out-of-bound packages instead of publishing silently
omitted or false text. It permits ordinary body paragraphs, first-level
table-cell paragraphs and simple hyperlinks.

Word writes several zero-text markers into ordinary documents, so the adapter
tolerates a closed list of them without changing the extracted text:
`proofErr` (spelling/grammar marks), `bookmarkStart`/`bookmarkEnd`
(including `_GoBack`), `commentRangeStart`/`commentRangeEnd` and
`permStart`/`permEnd` as children of `body`, `p`, `hyperlink`, `tbl`, `tr`
and `tc`, and `lastRenderedPageBreak` inside a run. Each must be empty: a
marker wrapping a run is refused rather than read. Inside runs
`noBreakHyphen` becomes U+2011 (a visible non-breaking hyphen, kept instead
of python-docx's plain `-`) and `softHyphen` is deliberately dropped, as
python-docx did, because it is a discretionary line-break hint rather than
prose and a registered literal split at one must still match. Text-bearing
wrappers stay refused: `ins`, `del`, `sdt`, `altChunk`, `txbxContent`,
`fldSimple`, `fldChar`/`instrText`, `smartTag`, `drawing`/`pict`, `sym`,
`footnoteReference` and anything else outside the whitelist. Such refusals
return an `*UnsupportedConstructError` naming the element; it matches both
`ErrUnsupportedConstruct` and `ErrInput`, so owners that map `ErrInput` to a
fixed code are unchanged while diagnostics can name the construct. Adding a
marker to the tolerated list must never add a text source. The document XML ceiling is
16 MiB, style XML 2 MiB, OPC XML 512 KiB per part, 2,048 ZIP entries,
10 MiB input and 50 MiB total declared uncompressed size. Matching uses its
existing 4 MiB text and bounded result budgets; dates and bullets each have
a 4,096-result cap. These are first-pass native refusal bounds, not source
equivalence claims.

Broader OPC variants, inherited styles and embedded content still deserve
review against actual documents; retain explicit refusal for unqualified
semantics. External hyperlink text and a renamed default paragraph style have
positive source cases, and `docx_test.go` carries a Word-shaped fixture with
the tolerated markers. Python source tests showed merged-cell duplication and
ignored field/revision wrappers; Go refuses them. Test writer recovery
separately. The `vault-intake-0.3` cutover is complete: `report_test.go`
pins that the current readers label a Python `vault-intake-0.2` run
`outdated`, so bump `ExtractorVersion` only together with a reviewed change
in extraction semantics, since every stored run is then re-labelled.
