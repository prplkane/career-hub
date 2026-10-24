// Package catalogstore composes read-only managed files with the catalog
// input and effective-registry boundaries. It never bootstraps a Data Home
// or writes.
//
// Load(resourceRoot, dataRoot) reads four fixed files: core groups from
// src/data/skill_groups.yaml and core skills from
// src/data/job_technologies.yaml under the resource root; custom groups from
// career_vault/taxonomy/skill_groups.yaml and custom skills from
// career_vault/taxonomy/technologies.yaml under the data root. Both roots
// must be existing absolute directories; an invalid root never falls back to
// a new profile, the working directory or another deployment's catalog. Both
// custom paths are checked before any decoding; reads follow core groups,
// custom groups, core skills, custom skills. Missing custom files are absent
// inputs and empty core groups use the default groups, but a corrupt or
// invalid file never becomes an empty successful result. After all inputs
// validate, catalog.Build performs reconciliation. ReadState serves callers
// that already hold open managed roots and returns the snapshot together
// with the owned raw custom bytes and models from those exact reads.
//
// A file's decoded models and raw revision always come from the same owned
// bytes, so an edit token is never computed from a different read than the
// models it protects. Reads are not a cross-file transaction; the host
// coordinates writers. The managed reader rejects non-regular files and
// link-like components, and each catalog read is bounded by the YAML
// adapter's 2 MiB input limit.
//
// Error carries a fixed stage and file-role label plus a sanitized path
// code, a structured input failure or a registry failure. Its default string
// contains no absolute path or content; nested model locations can contain
// skill labels and must not be logged wholesale.
package catalogstore
