// Package catalog implements in-memory, already-validated skill catalog
// logic: typed validation, core/custom reconciliation and immutable snapshot
// payloads. Typed projections are not YAML or external API schemas, and the
// package never opens files, selects a Data Home or writes.
//
// Build(coreGroups, customGroups, coreSkills, customSkills, rawGroups,
// rawSkills) takes validated model projections; the raw bytes only feed the
// private-file revision. Reconciliation rules: empty core groups use the
// default groups; a private group replaces a same-ID core group, and a name
// collision disambiguates the core display name with a "built-in" suffix; a
// same-ID skill keeps the custom name and category, earlier names become
// deduplicated aliases, and only fields present in Term.FieldsSet override
// core values; a private name or alias matching a core canonical name
// shadows that core ID and redirects relations; duplicate skill IDs within
// one catalog fail before reconciliation; effective groups and skills are
// sorted by ID. Notices retain source order. ValidateTerms checks duplicate
// IDs, then relations (unknown target, self reference, mirrored pair, member
// cycle); Validate adds group identity and name rules and cascade state.
//
// Snapshot copies every input and returns independent copies from Groups,
// Skills, Notices, CoreGroupIDs and CoreSkillIDs. Payload returns the grouped
// registry shape with skills ordered stably by Unicode 15 casefold of the
// name. Lookup strips terms, rejects a blank term or empty batch, resolves
// exactly, and reports matches, candidates and archived candidates; a match
// is vocabulary, never candidate evidence.
//
// Two revisions exist. RawRevision hashes the exact custom-file bytes
// (groups, NUL, bytes, NUL, skills, NUL, bytes, NUL) and is the optimistic
// mutation token. The catalog revision hashes the ordered effective models
// as compact sorted-key UTF-8 JSON, omitting null created_via and
// created_at only there.
//
// Error keeps Class, Code and the exact Message, which can contain catalog
// text; diagnostic adapters must apply their own redaction.
package catalog
