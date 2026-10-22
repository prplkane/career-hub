package vaultsetup

import (
	"context"
	"encoding/json"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

// PrepareEmpty creates a deterministic candidate-free bundle from the user's
// explicit name. It does not write: callers still obtain and confirm the same
// guarded eight-file preview plan, preserving existing and partial Homes.
func PrepareEmpty(ctx context.Context, name string) (*Bundle, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, &Error{"VAULT_SETUP_CANCELED"}
	}
	if len(name) > 4096 {
		return nil, &Error{"VAULT_SETUP_NAME_INVALID"}
	}
	name = textcompat.Strip(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 500 {
		return nil, &Error{"VAULT_SETUP_NAME_INVALID"}
	}
	// JSON is admitted by the bounded YAML decoder and quotes user text exactly;
	// no user value is interpolated into YAML syntax or a filesystem path.
	profile, err := json.Marshal(map[string]any{
		"vault_schema_version": 2, "name": name, "major": "", "target_location": "",
		"work_authorization": "", "contact": map[string]string{}, "role_targets": []string{},
	})
	if err != nil {
		return nil, &Error{"VAULT_SETUP_NAME_INVALID"}
	}
	return Prepare(ctx, map[string][]byte{
		"career_vault/profile.yaml":           append(profile, '\n'),
		"career_vault/education.yaml":         []byte("schools: []\ncertifications: []\n"),
		"career_vault/visa_profile.yaml":      []byte("current_status: ''\nwork_authorization: ''\nrequires_sponsorship_now: null\nrequires_future_sponsorship: null\nstem_opt_eligible: null\ncitizenship_required_blocker: null\nclearance_required_blocker: null\n"),
		"career_vault/experience_blocks.yaml": []byte("[]\n"),
		"career_vault/project_blocks.yaml":    []byte("[]\n"),
		"career_vault/skill_blocks.yaml":      []byte("[]\n"),
		"career_vault/proof_blocks.yaml":      []byte("[]\n"),
		"career_vault/style_rules.yaml":       []byte(emptyStyleDefaults),
	})
}

// Application writing defaults, not candidate facts. Keep the review dates and
// authority aligned with docs/reference/resume_standards.md; no setup-time claim of a new
// standards review and no user-specific location or eligibility is inferred.
const emptyStyleDefaults = `standards_metadata:
  reviewed_on: '2026-07-19'
  review_due: '2027-01-19'
  market: US private-sector technical roles
  authority_order: [explicit_employer_instructions, verified_career_vault, resume_standards]
resume_voice:
  factual: true
  avoid_unsupported_claims: true
bullet_rules:
  require_source_evidence: true
  invent_metrics: false
document_rules:
  layout: single_column
  standard_sections: [Professional Summary, Technical Skills, Professional Experience, Projects, Education]
ats_rules:
  selectable_text: true
selection_rules:
  allowed_decisions: [full, compact, excluded]
quality_gates:
  require_user_review: true
forbidden: [fabricated_experience, inferred_work_authorization, unsupported_metrics]
`
