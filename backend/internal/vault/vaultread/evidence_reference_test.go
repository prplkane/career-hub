package vaultread

import (
	"fmt"
	"strconv"
)

func extraExperience(id, company, role, category string) string {
	return fmt.Sprintf("- {id: %s, company: %s, role: %s, dates: '2025', category: %s, technologies: [], proof_points: []}\n", id, company, role, category)
}

func evidenceCases(view string) []validationCase {
	returned := func(views ...string) string {
		for _, candidate := range views {
			if view == candidate {
				return "VAULT_RESPONSE_UNSUPPORTED"
			}
		}
		return ""
	}
	cases := []validationCase{
		{"valid", func(*fixtureReader) {}, ""},
		{"profile_contact_null", appendYAML(profileIndex, "contact: null\n"), ""},
		{"profile_date", appendYAML(profileIndex, "custom: 2026-01-01\n"), returned("snapshot")},
		{"eligibility_date_not_returned", appendYAML(eligibilityIndex, "custom: 2026-01-01\n"), ""},
		{"common_validation", replaceYAML(profileIndex, "major: Computer Science", ""), "VAULT_RECORD_VALIDATION_FAILED"},
		{"source_exception", replaceYAML(proofIndex, "source_block: exp", "source_block: {}"), "VAULT_VALIDATION_UNSUPPORTED"},
		{"absent_certifications", func(f *fixtureReader) {
			f.files[schemas[educationIndex].path] = []byte("schools: []\ncustom_root: 2026-01-01\n")
			replaceYAML(skillIndex, "related_certifications: [cert]", "related_certifications: []")(f)
		}, ""},
		{"null_certifications", replaceYAML(educationIndex, "certifications: [{id: cert, name: Synthetic, credential: Test, completed: '2026'}]", "certifications: null"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"archived_options_retained", func(f *fixtureReader) {
			replaceYAML(experienceIndex, "category: technical", "category: technical, archived: true")(f)
			replaceYAML(projectIndex, "category: technical", "category: technical, archived: true")(f)
			replaceYAML(educationIndex, "credential: Test", "credential: Test, archived: true")(f)
		}, ""},
		{"invalid_archive_not_truthified", replaceYAML(experienceIndex, "category: technical", "category: technical, archived: 1"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"unknown_record_fields_preserved", func(f *fixtureReader) {
			for _, index := range []int{experienceIndex, projectIndex} {
				replaceYAML(index, "category: technical", "category: technical, unknown: {null: a, 'null': b, nested: [false, 0, ' Ω ']}")(f)
			}
			replaceYAML(skillIndex, "keywords: []", "keywords: [], unknown: {keep: false}")(f)
		}, ""},
		{"experience_unknown_date", replaceYAML(experienceIndex, "role: Engineer", "role: Engineer, custom: 2026-01-01"), returned("experiences", "snapshot")},
		{"project_unknown_date", replaceYAML(projectIndex, "summary: Test", "summary: Test, custom: 2026-01-01"), returned("projects", "snapshot")},
		{"skills_unknown_date", replaceYAML(skillIndex, "keywords: []", "keywords: [], custom: 2026-01-01"), returned("skills", "snapshot")},
		{"education_school_date", replaceYAML(educationIndex, "degree: BS", "degree: 2026-01-01"), returned("snapshot")},
		{"category_date", replaceYAML(experienceIndex, "category: technical", "category: 2026-01-01"), returned("experiences", "snapshot")},
		{"company_nested_dates_and_bytes", replaceYAML(experienceIndex, "company: Synthetic", "company: [2026-01-01, 2026-09-08T12:00:00Z, !!binary c2VjcmV0]"), returned("experiences", "projects", "snapshot")},
		{"role_nonfinite", replaceYAML(experienceIndex, "role: Engineer", "role: .nan"), returned("experiences", "projects", "snapshot")},
		{"name_date_label", replaceYAML(projectIndex, "name: Synthetic", "name: 2026-01-01"), returned("projects", "snapshot")},
		{"credential_bytes_label", replaceYAML(educationIndex, "credential: Test", "credential: !!binary c2VjcmV0"), returned("snapshot")},
		{"falsey_fields_and_raw_options", func(f *fixtureReader) {
			replaceYAML(experienceIndex, "company: Synthetic, role: Engineer", "company: false, role: 0")(f)
			replaceYAML(projectIndex, "name: Synthetic", "name: []")(f)
			replaceYAML(educationIndex, "name: Synthetic, credential: Test", "name: {}, credential: false")(f)
		}, ""},
		{"falsey_ids_excluded", func(f *fixtureReader) {
			for _, id := range []string{"false", "0", "-0.0", "[]", "{}"} {
				appendYAML(experienceIndex, extraExperience(id, "'Ignored option'", "'Ignored role'", "false"))(f)
			}
		}, ""},
		{"nonstring_and_blank_ids_preserved", func(f *fixtureReader) {
			for _, id := range []string{"true", "123456789012345678901234567890", "['ID']", "{key: value}", "'  '"} {
				appendYAML(experienceIndex, extraExperience(id, "false", "[]", "false"))(f)
			}
		}, ""},
		{"category_truthiness_and_display", func(f *fixtureReader) {
			values := []string{"false", "0", "-0.0", "[]", "{}", "' \\t '", "true", "12.5", "123", "[one, null, false]", "{a: b, false: 3}"}
			for index, category := range values {
				appendYAML(experienceIndex, extraExperience(fmt.Sprintf("extra%d", index), "Synthetic", "Engineer", category))(f)
			}
		}, ""},
		{"category_order_and_ties", func(f *fixtureReader) {
			values := []string{"Data", "data", "SS", "ss", "ß", "  data  ", "technical", "É", "e\u0301", "\u1c89", "\u1c8a", "Ω", "alpha"}
			for index, category := range values {
				text := strconv.QuoteToASCII(category)
				appendYAML(experienceIndex, extraExperience(fmt.Sprintf("cat%d", index), "Synthetic", "Engineer", text))(f)
				appendYAML(projectIndex, fmt.Sprintf("- {id: pcat%d, name: Synthetic, category: %s, summary: Test, technologies: [], proof_points: []}\n", index, text))(f)
			}
		}, ""},
		{"unicode_label_display", func(f *fixtureReader) {
			replaceYAML(experienceIndex, "company: Synthetic", "company: [\"a'\\u00a0\\u1c89\", {true: '\\n'}]")(f)
			replaceYAML(projectIndex, "name: Synthetic", "name: \"\\u001c  Ω\\u00a0\"")(f)
			replaceYAML(educationIndex, "credential: Test", "credential: {null: false, 1: true}")(f)
		}, ""},
		{"raw_revisions_only", func(f *fixtureReader) {
			for _, index := range []int{profileIndex, experienceIndex, projectIndex, skillIndex, educationIndex} {
				appendYAML(index, "# raw revision comment\r\n")(f)
			}
		}, ""},
	}
	return cases
}
