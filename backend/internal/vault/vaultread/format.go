package vaultread

import (
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/textcompat"
)

// SchemaVersion is an explicit Vault-document format, not the Data Home marker
// version. Unversioned files retain prototype validation; readers never infer
// v2 from missing facts or silently upgrade an existing profile.
func SchemaVersion(profile cataloginput.Value) (int, *Error) {
	version, present := lookup(profile, "vault_schema_version")
	if !present {
		return 1, nil
	}
	if version.Kind == "int" && (version.Text == "1" || version.Text == "2") {
		if version.Text == "2" {
			return 2, nil
		}
		return 1, nil
	}
	return 0, &Error{Stage: "validation", Source: "profile", Code: "VAULT_SCHEMA_VERSION_UNSUPPORTED"}
}

func (s *EditorState) AllowsUnknownFacts() bool {
	if s == nil || s.state == nil {
		return false
	}
	version, err := SchemaVersion(s.state.documents[profileIndex])
	return err == nil && version == 2
}

func (v *validator) versionedProfile(value cataloginput.Value) {
	name, present := lookup(value, "name")
	if !present || name.Kind != "string" || textcompat.Strip(name.Text) == "" {
		v.issue(profileIndex, []any{"name"}, "NONEMPTY_STRING_REQUIRED")
	}
	for _, key := range []string{"given_name", "family_name", "preferred_name", "current_location", "target_location", "location_flexibility", "work_authorization", "major"} {
		if item, present := lookup(value, key); present && item.Kind != "string" {
			v.issue(profileIndex, []any{key}, "STRING_REQUIRED")
		}
	}
	v.profile(value)
}

func (v *validator) versionedEligibility(value cataloginput.Value) {
	for _, key := range []string{"current_status", "work_authorization"} {
		if item, present := lookup(value, key); present && item.Kind != "string" {
			v.issue(eligibilityIndex, []any{key}, "STRING_REQUIRED")
		}
	}
	for _, key := range []string{"requires_sponsorship_now", "requires_future_sponsorship", "stem_opt_eligible", "citizenship_required_blocker", "clearance_required_blocker"} {
		if item, present := lookup(value, key); present && item.Kind != "bool" && item.Kind != "null" {
			v.issue(eligibilityIndex, []any{key}, "BOOLEAN_OR_NULL_REQUIRED")
		}
	}
}
