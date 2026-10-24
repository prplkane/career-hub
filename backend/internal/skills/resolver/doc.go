// Package resolver resolves complete competency names, aliases and
// historical IDs against an immutable in-memory catalog. A match is
// vocabulary, not evidence that the candidate has a skill. The package does
// not load or change catalogs and never creates a skill, evidence link, job,
// application or contact.
//
// New([]Term) snapshots each term's name, aliases, ID and archived state and
// fails with ErrDuplicateID on a repeated ID without returning a partial
// resolver. Resolve(source) compares the whole textcompat-normalized string:
// canonical and alias collisions are ambiguous, a canonical name does not
// outrank another term's alias, and repeated hits on one ID yield one
// candidate. Active and archived candidates are separate sorted ID lists;
// archived-only text is unmatched, and an archived candidate can neither
// make an active one ambiguous nor be selected. ResolveReference(id) uses
// the exact ID without folding or trimming; a known archived ID is a valid
// historical reference and unknown IDs are invalid.
//
// Every result keeps the original source string, all six result fields,
// empty candidate arrays rather than null, and null for an absent competency
// ID. Inputs and outputs do not alias caller memory; concurrent reads are
// safe after construction, and the zero-value resolver is an empty catalog.
//
// Model validation (ID patterns, nonblank names, alias uniqueness,
// relations, groups, merge rules) is upstream. Inputs must be valid UTF-8
// scalar strings; an input adapter must reject malformed UTF-8 before
// calling this package. There is no substring or fuzzy matching, no offsets
// and no size limit; the caller bounds requests.
package resolver
