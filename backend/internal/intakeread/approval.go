package intakeread

import (
	"regexp"

	"careerhub.local/backend/internal/cataloginput"
)

// MaxApprovalBytes is the shared bounded YAML-adapter limit. Missing files are
// represented by the future owner, never by passing empty bytes here.
const MaxApprovalBytes = cataloginput.YAMLMaxInputBytes

// Approval is the content-minimized projection of one strict-valid persisted
// ApprovalApplyResult. The Python status reader treats any valid result file as
// saved_to_vault; it does not require Status == "applied".
type Approval struct {
	SchemaVersion            string
	Status                   string
	SourceID                 string
	AppliedAt                string
	ReviewRevision           string
	PreviousVaultRevision    string
	VaultRevision            string
	PreviousRegistryRevision *string
	RegistryRevision         *string
	ChangeCount              int
}

func nonemptyText(v cataloginput.Value) bool {
	value, ok := text(v)
	return ok && len(value) > 0
}

var (
	approvalSHARevision = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	approvalRegistrySHA = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func approvalRevision(v cataloginput.Value) bool {
	value, ok := text(v)
	return ok && approvalSHARevision.MatchString(value)
}

func registryRevision(v cataloginput.Value) bool {
	value, ok := text(v)
	return ok && approvalRegistrySHA.MatchString(value)
}

func approvalFieldResolutions(v cataloginput.Value) bool {
	if v.Kind != "mapping" {
		return false
	}
	for _, item := range v.Entries {
		if !validText(item.Key) || !validText(item.Value) {
			return false
		}
	}
	return true
}

var approvalChangeRules = map[string]fieldRule{
	"subject_kind":      {true, nonemptyText},
	"subject_id":        {true, nonemptyText},
	"action":            {true, nonemptyText},
	"entity_type":       {false, nullable(validText)},
	"target_id":         {false, nullable(validText)},
	"vault_path":        {false, nullable(validText)},
	"field_resolutions": {false, nullable(approvalFieldResolutions)},
}

func approvalChange(v cataloginput.Value) bool {
	_, ok := object(v, approvalChangeRules)
	return ok
}

var approvalRules = map[string]fieldRule{
	"schema_version":             {false, validInteger},
	"status":                     {false, validText},
	"source_id":                  {true, nonemptyText},
	"applied_at":                 {true, nonemptyText},
	"review_revision":            {true, approvalRevision},
	"previous_vault_revision":    {true, approvalRevision},
	"vault_revision":             {true, approvalRevision},
	"previous_registry_revision": {false, nullable(registryRevision)},
	"registry_revision":          {false, nullable(registryRevision)},
	"created_skill_ids":          {false, sequence(validText)},
	"changes":                    {false, sequence(approvalChange)},
	"changed_files":              {false, sequence(validText)},
}

// ParseApproval validates the entire persisted ApprovalApplyResult model and
// returns only fields needed by read-only workflow status. The future owner
// must separately guard the path, determine file absence and verify witnesses.
func ParseApproval(raw []byte) (Approval, *Error) {
	value, yamlError := cataloginput.ParseYAML(raw)
	if yamlError != nil {
		code := "APPROVAL_YAML_INVALID"
		if yamlError.Kind == "limit" {
			code = "APPROVAL_INPUT_LIMIT"
		} else if yamlError.Kind == "unsupported" {
			code = "APPROVAL_INPUT_UNSUPPORTED"
		}
		return Approval{}, &Error{Code: code}
	}
	root, valid := object(value, approvalRules)
	if !valid {
		return Approval{}, &Error{Code: "APPROVAL_MODEL_INVALID"}
	}
	result := Approval{
		SchemaVersion:         "1",
		Status:                "applied",
		SourceID:              stringField(root, "source_id"),
		AppliedAt:             stringField(root, "applied_at"),
		ReviewRevision:        stringField(root, "review_revision"),
		PreviousVaultRevision: stringField(root, "previous_vault_revision"),
		VaultRevision:         stringField(root, "vault_revision"),
	}
	if v, found := root["schema_version"]; found {
		result.SchemaVersion, _ = integer(v)
	}
	if v, found := root["status"]; found {
		result.Status, _ = text(v)
	}
	if v, found := root["previous_registry_revision"]; found && v.Kind != "null" {
		value, _ := text(v)
		result.PreviousRegistryRevision = &value
	}
	if v, found := root["registry_revision"]; found && v.Kind != "null" {
		value, _ := text(v)
		result.RegistryRevision = &value
	}
	if v, found := root["changes"]; found {
		result.ChangeCount = len(v.Items)
	}
	return result, nil
}
