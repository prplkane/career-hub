// Package intakeextract constructs a pure, source-backed resume extraction
// report. It owns no Data Home path, credential, HTTP route or publication.
//
// Extract accepts bytes, checks their SHA-256 against the caller's source
// identity, parses TXT or DOCX and builds a report. ParseTXTBlocks and
// ParseDOCXBlocks expose the pure adapters; Build accepts ordered blocks, a
// verified source identity, an effective registry revision and validated
// resolver terms. A vocabulary match is never approved candidate evidence.
// Stored runs are compared against ExtractorVersion; bump it only together
// with a reviewed change in extraction semantics, since every stored run is
// then labelled outdated.
//
// DOCX support is a declared subset, not general OPC parsing. The adapter
// requires the main document and style parts at their ordinary paths and
// refuses DTDs, duplicate, unsafe or symlink ZIP members, content controls,
// alternate chunks, tracked changes, field text, merged cells, nested tables,
// unknown paragraph styles and unknown body, paragraph or run wrappers
// instead of publishing silently omitted or false text. Ordinary body
// paragraphs, first-level table-cell paragraphs and simple hyperlinks are
// read. A closed list of empty Word markers (proofErr, bookmarks, comment and
// permission ranges, lastRenderedPageBreak) is tolerated; noBreakHyphen
// becomes U+2011 and softHyphen is dropped. Refusals return
// *UnsupportedConstructError, which matches both ErrUnsupportedConstruct and
// ErrInput. Adding a tolerated marker must never add a text source.
//
// Limits: 10 MiB input, 16 MiB document XML, 2 MiB style XML, 512 KiB per OPC
// part, 2,048 ZIP entries and 50 MiB declared uncompressed size; matching
// uses a 4 MiB text budget, and dates and bullets each cap at 4,096 results.
package intakeextract
