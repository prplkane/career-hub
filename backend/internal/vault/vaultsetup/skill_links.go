package vaultsetup

import (
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/textcompat"
)

// unresolvedSkillLinks counts competency_ids entries in experience, project and
// certification records that do not name a known skill id.
//
// Inputs are parsed YAML documents from a Career Vault export file:
//   - experiences is the experience_blocks.yaml root: a sequence of records.
//   - projects is the project_blocks.yaml root: a sequence of records.
//   - education is the education.yaml root: a mapping whose "certifications"
//     key holds a sequence of records. Schools are not checked.
//   - known holds every skill id in the prospective catalog (this
//     application's core catalog plus the bundled custom skills), archived
//     skills included.
//
// Archived records count too: unarchiving one later must not break the Skills
// evidence view. Anything that is not a record, or a root of the wrong kind,
// counts 0; shape errors belong to Vault validation, which runs first.
//
// A count above zero refuses the import (VAULT_SETUP_SKILL_UNKNOWN) and the
// export (VAULT_EXPORT_SKILL_UNKNOWN).
func unresolvedSkillLinks(experiences, projects, education cataloginput.Value, known map[string]bool) int {
	total := 0
	for _, records := range []cataloginput.Value{experiences, projects, recordField(education, "certifications")} {
		if records.Kind != "sequence" {
			continue
		}
		for _, record := range records.Items {
			if record.Kind != "mapping" {
				continue
			}
			total += unresolvedInRecord(recordField(record, "competency_ids"), known)
		}
	}
	return total
}

// recordField returns one named field of a mapping. A missing field, or a
// value that is not a mapping, yields the zero Value (Kind "").
func recordField(record cataloginput.Value, name string) cataloginput.Value {
	if record.Kind != "mapping" {
		return cataloginput.Value{}
	}
	for _, entry := range record.Entries {
		if entry.Key.Kind == "string" && entry.Key.Text == name {
			return entry.Value
		}
	}
	return cataloginput.Value{}
}

// unresolvedInRecord counts the entries of ONE record's competency_ids value
// that do not name a known skill id. ids is that value as parsed YAML:
//   - Kind "" means the record has no competency_ids field; Kind "null" means
//     it is written as null. Both count 0.
//   - Kind "sequence" holds the entries in ids.Items. A string entry has Kind
//     "string" and its text in Text.
//
// Rules (skill_links_test.go pins each one):
//   - Compare each entry after textcompat.Strip; blank entries are ignored.
//   - Every unknown entry counts, duplicates included. Ids are case sensitive.
//   - A value that is neither absent, null nor a sequence counts 1, and so
//     does each item that is not a string: neither can name a skill.
func unresolvedInRecord(ids cataloginput.Value, known map[string]bool) int {
	if ids.Kind == "" || ids.Kind == "null" {
		return 0
	}
	if ids.Kind != "sequence" {
		return 1
	}
	count := 0
	for _, item := range ids.Items {
		if item.Kind != "string" {
			count++
			continue
		}
		if id := textcompat.Strip(item.Text); id != "" && !known[id] {
			count++
		}
	}
	return count
}
