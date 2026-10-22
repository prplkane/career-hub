package vaultread

import (
	"context"

	"careerhub.local/backend/internal/calendarinput"
	"careerhub.local/backend/internal/cataloginput"
)

// Issue identifies only fixed schema fields and integer positions. Unlike the
// prototype's error prose, it never contains a private ID, value or unknown key.
type Issue struct {
	Source string   `json:"source"`
	Code   string   `json:"code"`
	Loc    []any    `json:"loc"`
	Fields []string `json:"fields,omitempty"`
}

const maxValidationIssues = 256

type validator struct {
	ctx     context.Context
	issues  []Issue
	limited bool
	failure *Error
}

func lookup(value cataloginput.Value, key string) (cataloginput.Value, bool) {
	for _, entry := range value.Entries {
		if entry.Key.Kind == "string" && entry.Key.Text == key {
			return entry.Value, true
		}
	}
	return cataloginput.Value{Kind: "null"}, false
}

func field(value cataloginput.Value, key string) cataloginput.Value {
	result, _ := lookup(value, key)
	return result
}

func child(loc []any, suffix ...any) []any {
	out := make([]any, 0, len(loc)+len(suffix))
	out = append(out, loc...)
	return append(out, suffix...)
}

func (v *validator) stopped() bool {
	if v.failure == nil && (v.ctx == nil || v.ctx.Err() != nil) {
		v.failure = canceled()
	}
	return v.failure != nil
}

func (v *validator) issue(source int, loc []any, code string, fields ...string) {
	if len(v.issues) == maxValidationIssues {
		v.limited = true
		return
	}
	v.issues = append(v.issues, Issue{
		Source: schemas[source].source, Code: code, Loc: child(loc), Fields: fields,
	})
}

// Some prototype validators raise TypeError instead of VaultEditorError (for
// example a list used as a set member). Do not relabel these as ordinary domain
// validation failures or let them bypass validation in the port.
func (v *validator) unsupported(source int) {
	v.failure = &Error{Stage: "validation", Source: schemas[source].source, Code: "VAULT_VALIDATION_UNSUPPORTED"}
}

func (v *validator) required(source int, value cataloginput.Value, loc []any, keys []string) bool {
	var missing []string
	for _, key := range keys {
		value, present := lookup(value, key)
		if !present || value.Kind == "null" || value.Kind == "string" && value.Text == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) != 0 {
		v.issue(source, loc, "REQUIRED_FIELDS", missing...)
		return false
	}
	return true
}

func (v *validator) calendar(source int, item cataloginput.Value, loc []any) {
	if value, present := lookup(item, "date_range"); present {
		_, err := calendarinput.NormalizeRange(value)
		if err != nil {
			if err.Code == "CALENDAR_INPUT_UNSUPPORTED" {
				v.unsupported(source)
				return
			}
			path := child(loc, "date_range")
			if err.Field != "" {
				path = child(path, err.Field)
			}
			v.issue(source, path, err.Code)
		}
	}
	for _, key := range []string{"graduation_month", "completed_month"} {
		if value, present := lookup(item, key); present {
			_, err := calendarinput.NormalizeMonth(value)
			if err != nil {
				if err.Code == calendarinput.InputUnsupported {
					v.unsupported(source)
					return
				}
				v.issue(source, child(loc, key), err.Code)
			}
		}
	}
}

func (v *validator) archived(source int, item cataloginput.Value, loc []any) {
	if value, present := lookup(item, "archived"); present && value.Kind != "bool" {
		v.issue(source, child(loc, "archived"), "BOOLEAN_REQUIRED")
	}
}

func (v *validator) profile(item cataloginput.Value) {
	contact := field(item, "contact")
	if contact.Kind != "null" {
		if contact.Kind != "mapping" {
			v.issue(profileIndex, []any{"contact"}, "MAPPING_REQUIRED")
		} else {
			for _, entry := range contact.Entries {
				if entry.Key.Kind != "string" || entry.Value.Kind != "string" {
					v.issue(profileIndex, []any{"contact"}, "STRING_ENTRIES_REQUIRED")
					break
				}
			}
		}
	}
	v.stringList(profileIndex, field(item, "role_targets"), []any{"role_targets"})
}

func (v *validator) stringList(source int, value cataloginput.Value, loc []any) {
	if value.Kind == "null" {
		return
	}
	if value.Kind != "sequence" {
		v.issue(source, loc, "LIST_REQUIRED")
		return
	}
	for index, item := range value.Items {
		if v.stopped() {
			return
		}
		if item.Kind != "string" {
			v.issue(source, child(loc, index), "STRING_REQUIRED")
		}
	}
}

func (v *validator) education(value cataloginput.Value) {
	seen := map[string]bool{}
	for _, spec := range []struct {
		key      string
		required []string
		lists    []string
	}{
		{"schools", []string{"id", "institution", "degree"}, []string{"honors", "notes"}},
		{"certifications", []string{"id", "name", "credential", "completed"}, []string{"proof_points", "notes"}},
	} {
		records, present := lookup(value, spec.key)
		if !present {
			continue
		}
		if records.Kind != "sequence" {
			v.issue(educationIndex, []any{spec.key}, "LIST_REQUIRED")
			continue
		}
		for index, item := range records.Items {
			if v.stopped() {
				return
			}
			loc := []any{spec.key, index}
			if item.Kind != "mapping" {
				v.issue(educationIndex, loc, "MAPPING_REQUIRED")
				continue
			}
			v.required(educationIndex, item, loc, spec.required)
			id := field(item, "id")
			if id.Kind == "string" && id.Text != "" {
				if seen[id.Text] {
					v.issue(educationIndex, loc, "DUPLICATE_EDUCATION_ID")
				}
				seen[id.Text] = true
			}
			v.archived(educationIndex, item, loc)
			v.calendar(educationIndex, item, loc)
			for _, key := range spec.lists {
				v.stringList(educationIndex, field(item, key), child(loc, key))
			}
		}
	}
}

func (v *validator) style(value cataloginput.Value) {
	for _, key := range schemas[styleIndex].required[:7] {
		if field(value, key).Kind != "mapping" {
			v.issue(styleIndex, []any{key}, "MAPPING_REQUIRED")
		}
	}
	metadata := field(value, "standards_metadata")
	if metadata.Kind == "mapping" {
		for _, key := range []string{"reviewed_on", "review_due", "market", "authority_order"} {
			if _, present := lookup(metadata, key); !present {
				v.issue(styleIndex, []any{"standards_metadata"}, "REQUIRED_FIELD", key)
			}
		}
		for _, key := range []string{"reviewed_on", "review_due"} {
			date := field(metadata, key)
			if date.Kind != "string" || !calendarinput.ValidISODate(date.Text) {
				v.issue(styleIndex, []any{"standards_metadata", key}, "ISO_DATE_REQUIRED")
			}
		}
		v.nonemptyList(field(metadata, "authority_order"), []any{"standards_metadata", "authority_order"})
	}
	document := field(value, "document_rules")
	if document.Kind == "mapping" {
		layout := field(document, "layout")
		if layout.Kind != "string" || layout.Text != "single_column" {
			v.issue(styleIndex, []any{"document_rules", "layout"}, "SINGLE_COLUMN_REQUIRED")
		}
		v.nonemptyList(field(document, "standard_sections"), []any{"document_rules", "standard_sections"})
	}
	selection := field(value, "selection_rules")
	if selection.Kind == "mapping" {
		decisions := field(selection, "allowed_decisions")
		valid := decisions.Kind == "sequence"
		seen := map[string]bool{}
		for _, item := range decisions.Items {
			if !hashable(item) {
				v.unsupported(styleIndex)
				return
			}
			if item.Kind != "string" || (item.Text != "full" && item.Text != "compact" && item.Text != "excluded") {
				valid = false
			}
			seen[item.Text] = true
		}
		if !valid || len(seen) != 3 {
			v.issue(styleIndex, []any{"selection_rules", "allowed_decisions"}, "DECISIONS_REQUIRED")
		}
	}
}

func (v *validator) nonemptyList(value cataloginput.Value, loc []any) {
	if value.Kind != "sequence" || len(value.Items) == 0 {
		v.issue(styleIndex, loc, "NONEMPTY_LIST_REQUIRED")
	}
}

func (v *validator) list(source int, records cataloginput.Value, allowEmpty bool) {
	if len(records.Items) == 0 && !allowEmpty {
		v.issue(source, nil, "NONEMPTY_LIST_REQUIRED")
		return
	}
	for index, item := range records.Items {
		if v.stopped() {
			return
		}
		loc := []any{index}
		if item.Kind != "mapping" {
			v.issue(source, loc, "MAPPING_REQUIRED")
			continue
		}
		v.required(source, item, loc, schemas[source].required)
		v.archived(source, item, loc)
		v.calendar(source, item, loc)
	}
}

func hashable(value cataloginput.Value) bool {
	return value.Kind != "mapping" && value.Kind != "sequence"
}

func (v *validator) ids(source int, records cataloginput.Value, loc []any) map[string]bool {
	result := map[string]bool{}
	for index, item := range records.Items {
		if v.stopped() {
			return result
		}
		id := field(item, "id")
		if id.Kind == "string" && id.Text != "" {
			if result[id.Text] {
				v.issue(source, child(loc, index), "DUPLICATE_ID")
			}
			result[id.Text] = true
		}
	}
	return result
}

func (v *validator) references(source int, records cataloginput.Value, sets map[string]map[string]bool, keys []string) {
	for index, item := range records.Items {
		if v.stopped() {
			return
		}
		if item.Kind != "mapping" {
			continue
		}
		for _, key := range keys {
			refs := field(item, key)
			if refs.Kind == "null" {
				continue
			}
			loc := []any{index, key}
			if refs.Kind != "sequence" {
				v.issue(source, loc, "LIST_REQUIRED")
				continue
			}
			for _, ref := range refs.Items {
				if v.stopped() {
					return
				}
				if !hashable(ref) {
					v.unsupported(source)
					return
				}
				if ref.Kind != "string" || !sets[key][ref.Text] {
					v.issue(source, loc, "UNKNOWN_ID")
				}
			}
		}
	}
}

func (v *validator) allReferences(documents [vaultFileCount]cataloginput.Value) {
	experience := v.ids(experienceIndex, documents[experienceIndex], nil)
	project := v.ids(projectIndex, documents[projectIndex], nil)
	v.ids(proofIndex, documents[proofIndex], nil) // Duplicate proof IDs are validated, even though no foreign key consumes them.
	cert := v.ids(educationIndex, field(documents[educationIndex], "certifications"), []any{"certifications"})
	sets := map[string]map[string]bool{
		"related_experience": experience, "related_projects": project, "related_certifications": cert,
	}
	v.references(projectIndex, documents[projectIndex], sets, []string{"related_experience"})
	v.references(skillIndex, documents[skillIndex], sets, []string{"related_experience", "related_projects", "related_certifications"})
	proofSets := map[string]map[string]bool{"experience": experience, "project": project, "certification": cert}
	for index, item := range documents[proofIndex].Items {
		if v.stopped() {
			return
		}
		if item.Kind != "mapping" {
			continue
		}
		kind := field(item, "evidence_type")
		if !hashable(kind) {
			v.unsupported(proofIndex)
			return
		}
		ids := proofSets[kind.Text]
		if kind.Kind != "string" || ids == nil {
			v.issue(proofIndex, []any{index, "evidence_type"}, "UNKNOWN_EVIDENCE_TYPE")
			continue
		}
		ref := field(item, "source_block")
		if !hashable(ref) {
			v.unsupported(proofIndex)
			return
		}
		if ref.Kind != "string" || !ids[ref.Text] {
			v.issue(proofIndex, []any{index, "source_block"}, "UNKNOWN_ID")
		}
	}
}

func validateVault(ctx context.Context, state *vaultState) *Error {
	indexes := make([]int, vaultFileCount)
	for i := range indexes {
		indexes[i] = i
	}
	return validateVaultSubset(ctx, state, indexes)
}

func validateVaultSubset(ctx context.Context, state *vaultState, indexes []int) *Error {
	if ctx == nil || ctx.Err() != nil {
		return canceled()
	}
	version, formatErr := SchemaVersion(state.documents[profileIndex])
	if formatErr != nil {
		return formatErr
	}
	v := &validator{ctx: ctx}
	for _, index := range indexes {
		item := state.documents[index]
		if v.stopped() {
			return v.failure
		}
		if schemas[index].kind == "sequence" {
			v.list(index, item, version == 2)
			continue
		}
		if version == 2 && index == profileIndex {
			v.versionedProfile(item)
			continue
		}
		if version == 2 && index == eligibilityIndex {
			v.versionedEligibility(item)
			continue
		}
		// Required fields short-circuit this mapping validator only, not later
		// files or the final reference pass.
		if !v.required(index, item, nil, schemas[index].required) {
			continue
		}
		switch index {
		case profileIndex:
			v.profile(item)
		case educationIndex:
			v.education(item)
		case styleIndex:
			v.style(item)
		}
	}
	v.allReferences(state.documents)
	if v.stopped() {
		return v.failure
	}
	if v.limited {
		return &Error{Stage: "validation", Code: "VAULT_VALIDATION_LIMIT"}
	}
	if len(v.issues) != 0 {
		return &Error{Stage: "validation", Code: "VAULT_RECORD_VALIDATION_FAILED", Issues: v.issues}
	}
	return nil
}

func validateProfileState(ctx context.Context, state *vaultState) *Error {
	if failure := validateVault(ctx, state); failure != nil {
		return failure
	}
	// The profile editor adds this stricter check AFTER full-vault validation:
	// absent contact is allowed, explicit null is not. No default is inserted.
	// Other readers and the aggregate snapshot do not perform this check.
	if contact, present := lookup(state.documents[profileIndex], "contact"); present && contact.Kind != "mapping" {
		v := &validator{ctx: ctx}
		v.issue(profileIndex, []any{"contact"}, "MAPPING_REQUIRED")
		return &Error{Stage: "validation", Code: "VAULT_RECORD_VALIDATION_FAILED", Issues: v.issues}
	}
	return nil
}
