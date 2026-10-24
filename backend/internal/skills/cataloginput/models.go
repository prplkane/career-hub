package cataloginput

import (
	"encoding/json"
	"sort"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/textcompat"
)

// These are maintainer-adapter resource bounds, not product validation rules.
// Exhausting either returns Unsupported, never a truncated ValidationError.
const MaxValidationIssues = 10000
const MaxValidationIssueBytes = 4 * 1024 * 1024

var termFields = []string{"id", "name", "aliases", "category", "primary_group_id", "parent", "kind", "primary_domain", "relations", "archived", "created_via", "created_at"}
var groupFields = []string{"id", "name", "archived", "cascade_archived_skill_ids"}
var relationFields = []string{"type", "target_id"}

type validation struct {
	issues      []ValidationIssue
	issueBytes  int
	unsupported string
}

func newValidation() *validation { return &validation{issues: []ValidationIssue{}, issueBytes: 2} }

func (v *validation) unsupportedInput(reason string) {
	if v.unsupported == "" {
		v.unsupported = reason
		v.issues = []ValidationIssue{}
	}
}

func (v *validation) add(kind string, loc []any, message string) {
	if v.unsupported != "" {
		return
	}
	issue := ValidationIssue{Type: kind, Loc: append([]any{}, loc...), Msg: message}
	encoded, err := json.Marshal(issue)
	if err != nil {
		v.unsupportedInput("validation issue cannot be encoded")
		return
	}
	extra := len(encoded)
	if len(v.issues) != 0 {
		extra++ // the comma between two issues
	}
	if len(v.issues) >= MaxValidationIssues || extra > MaxValidationIssueBytes-v.issueBytes {
		v.unsupportedInput("validation issue budget exceeded")
		return
	}
	v.issues = append(v.issues, issue)
	v.issueBytes += extra
}

func (v *validation) failure() *ValidationError {
	if v.unsupported == "" && len(v.issues) == 0 {
		return nil
	}
	return &ValidationError{Issues: v.issues, Unsupported: v.unsupported}
}

func at(loc []any, component any) []any {
	result := make([]any, len(loc)+1)
	copy(result, loc)
	result[len(loc)] = component
	return result
}

// checkKinds rejects representations outside the declared decoded-value domain.
// The adapter, not these model validators, owns canonical numeric/date encodings
// and tree resource bounds. In particular, no arbitrary Python objects are
// treated as strings, and no invalid scalar UTF-8 is silently replaced.
func (v *validation) checkKinds(input Value) {
	if v.unsupported != "" {
		return
	}
	switch input.Kind {
	case "string", "int", "float", "date", "datetime":
		if !utf8.ValidString(input.Text) {
			v.unsupportedInput("non-scalar UTF-8 text")
		}
	case "null", "bytes", "bool":
	case "sequence":
		for _, child := range input.Items {
			v.checkKinds(child)
			if v.unsupported != "" {
				break
			}
		}
	case "mapping":
		for _, entry := range input.Entries {
			v.checkKinds(entry.Key)
			v.checkKinds(entry.Value)
			if v.unsupported != "" {
				break
			}
		}
	default:
		v.unsupportedInput("unknown decoded value kind")
	}
}

// ValidateTerms mirrors TypeAdapter(list[TechnologyTerm]).validate_python for
// the supported raw Value domain. It does not load YAML, normalize legacy
// aliases, validate a graph, or reject duplicate IDs across separate records.
// Failure returns no partially validated records.
func ValidateTerms(input Value) ([]catalog.Term, *ValidationError) {
	v := newValidation()
	v.checkKinds(input)
	if v.unsupported == "" && input.Kind != "sequence" {
		v.add("list_type", nil, "Input should be a valid list")
	}
	result := []catalog.Term{}
	if input.Kind == "sequence" {
		for i, item := range input.Items {
			if v.unsupported != "" {
				break
			}
			result = append(result, v.term(item, []any{i}))
		}
	}
	if err := v.failure(); err != nil {
		return nil, err
	}
	return result, nil
}

// ValidateGroups is the corresponding list[SkillGroup] model boundary.
func ValidateGroups(input Value) ([]catalog.Group, *ValidationError) {
	v := newValidation()
	v.checkKinds(input)
	if v.unsupported == "" && input.Kind != "sequence" {
		v.add("list_type", nil, "Input should be a valid list")
	}
	result := []catalog.Group{}
	if input.Kind == "sequence" {
		for i, item := range input.Items {
			if v.unsupported != "" {
				break
			}
			result = append(result, v.group(item, []any{i}))
		}
	}
	if err := v.failure(); err != nil {
		return nil, err
	}
	return result, nil
}

func (v *validation) record(input Value, loc []any, model string) (map[string]Value, bool) {
	if input.Kind != "mapping" {
		v.add("model_type", loc, "Input should be a valid dictionary or instance of "+model)
		return nil, false
	}
	fields := make(map[string]Value, len(input.Entries))
	for _, entry := range input.Entries {
		if entry.Key.Kind == "string" {
			if _, exists := fields[entry.Key.Text]; exists {
				v.unsupportedInput("mapping must represent a post-dict unique-key sequence")
				return nil, false
			}
			fields[entry.Key.Text] = entry.Value
		}
	}
	return fields, true
}

func (v *validation) extras(input Value, loc []any, known []string) {
	for _, entry := range input.Entries {
		if v.unsupported != "" {
			return
		}
		if entry.Key.Kind != "string" {
			key, supported := keyLocation(entry.Key)
			if !supported {
				v.unsupportedInput("mapping key representation is unsupported")
				return
			}
			v.add("invalid_key", at(loc, key), "Keys should be strings")
			continue
		}
		found := false
		for _, name := range known {
			if entry.Key.Text == name {
				found = true
				break
			}
		}
		if !found {
			v.add("extra_forbidden", at(loc, entry.Key.Text), "Extra inputs are not permitted")
		}
	}
}

func (v *validation) required(fields map[string]Value, key string, loc []any, identifier bool) string {
	value, present := fields[key]
	if !present {
		v.add("missing", at(loc, key), "Field required")
		return ""
	}
	if identifier {
		return v.identifier(value, at(loc, key))
	}
	return v.nonblank(value, at(loc, key))
}

func (v *validation) term(input Value, loc []any) catalog.Term {
	term := catalog.Term{Aliases: []string{}, Relations: []catalog.Relation{}, Kind: "technology", FieldsSet: []string{}}
	fields, ok := v.record(input, loc, "TechnologyTerm")
	if !ok {
		return term
	}
	before := len(v.issues)
	for _, name := range termFields {
		if _, present := fields[name]; present {
			term.FieldsSet = append(term.FieldsSet, name)
		}
	}
	sort.Strings(term.FieldsSet)
	term.ID = v.required(fields, "id", loc, true)
	term.Name = v.required(fields, "name", loc, false)
	if value, present := fields["aliases"]; present {
		term.Aliases = v.aliases(value, at(loc, "aliases"))
	}
	term.Category = v.required(fields, "category", loc, false)
	if value, present := fields["primary_group_id"]; present {
		term.PrimaryGroupID = v.optionalIdentifier(value, at(loc, "primary_group_id"))
	}
	if value, present := fields["parent"]; present {
		term.Parent = v.optionalIdentifier(value, at(loc, "parent"))
	}
	if value, present := fields["kind"]; present {
		term.Kind = v.enum(value, at(loc, "kind"), kinds)
	}
	if value, present := fields["primary_domain"]; present {
		term.PrimaryDomain = v.optionalEnum(value, at(loc, "primary_domain"), domains)
	}
	if value, present := fields["relations"]; present {
		term.Relations = v.relations(value, at(loc, "relations"))
	}
	if value, present := fields["archived"]; present {
		term.Archived = v.boolean(value, at(loc, "archived"))
	}
	if value, present := fields["created_via"]; present {
		term.CreatedVia = v.optionalEnum(value, at(loc, "created_via"), creationSources)
	}
	if value, present := fields["created_at"]; present {
		term.CreatedAt = v.timestamp(value, at(loc, "created_at"))
	}
	v.extras(input, loc, termFields)
	if v.unsupported == "" && len(v.issues) == before {
		seen := map[string]bool{textcompat.Normalize(term.Name): true}
		for _, alias := range term.Aliases {
			normalized := textcompat.Normalize(alias)
			if seen[normalized] {
				v.add("value_error", loc, "Value error, name and aliases must be unique within one competency")
				break
			}
			seen[normalized] = true
		}
	}
	return term
}

func (v *validation) relations(input Value, loc []any) []catalog.Relation {
	result := []catalog.Relation{}
	if input.Kind != "sequence" {
		v.add("list_type", loc, "Input should be a valid list")
		return result
	}
	for i, item := range input.Items {
		if v.unsupported != "" {
			break
		}
		itemLoc := at(loc, i)
		fields, ok := v.record(item, itemLoc, "CompetencyRelation")
		if !ok {
			continue
		}
		relation := catalog.Relation{}
		if value, present := fields["type"]; present {
			relation.Type = v.enum(value, at(itemLoc, "type"), relationTypes)
		} else {
			v.add("missing", at(itemLoc, "type"), "Field required")
		}
		relation.TargetID = v.required(fields, "target_id", itemLoc, true)
		v.extras(item, itemLoc, relationFields)
		result = append(result, relation)
	}
	return result
}

func (v *validation) group(input Value, loc []any) catalog.Group {
	group := catalog.Group{CascadeArchivedSkillIDs: []string{}}
	fields, ok := v.record(input, loc, "SkillGroup")
	if !ok {
		return group
	}
	group.ID = v.required(fields, "id", loc, true)
	group.Name = v.required(fields, "name", loc, false)
	if value, present := fields["archived"]; present {
		group.Archived = v.boolean(value, at(loc, "archived"))
	}
	if value, present := fields["cascade_archived_skill_ids"]; present {
		group.CascadeArchivedSkillIDs = v.cascade(value, at(loc, "cascade_archived_skill_ids"))
	}
	v.extras(input, loc, groupFields)
	return group
}
