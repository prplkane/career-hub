package catalogwrite

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/textcompat"
)

type SkillRecord struct {
	catalog.Term
	Origin string `json:"origin"`
}
type SkillResult struct {
	Record   SkillRecord `json:"record"`
	Revision string      `json:"revision"`
}
type editOperation string

const (
	createGroup  editOperation = "create_group"
	createSkill  editOperation = "create_skill"
	updateSkill  editOperation = "update_skill"
	archiveSkill editOperation = "archive_skill"
)

type editRequest struct {
	operation editOperation
	id        string
	payload   map[string]any
	archived  bool
	revision  string
	now       time.Time
}
type editPlan struct {
	group        *Result
	skill        *SkillResult
	replacements map[string][]byte
	prospective  *catalog.Snapshot
}

func validateEditFields(operation editOperation, payload map[string]any) *Error {
	if operation == archiveSkill {
		return nil
	}
	allowed := []string{"name", "aliases", "primary_group_id"}
	label := "skill"
	if operation == createGroup {
		allowed = []string{"name"}
		label = "group"
	}
	unknown := []string{}
	for key := range payload {
		if !utf8.ValidString(key) {
			return &Error{Stage: "input", Code: "CATALOG_NAME_UNICODE_UNSUPPORTED"}
		}
		if !slices.Contains(allowed, key) {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return registryError("SKILL_REGISTRY_INVALID", "SkillRegistryError", "Unknown "+label+" fields: "+strings.Join(unknown, ", "))
	}
	return nil
}
func editName(value any, field string) (string, *Error) {
	text, ok := value.(string)
	if !ok || textcompat.Strip(text) == "" {
		return "", registryError("SKILL_REGISTRY_INVALID", "SkillRegistryError", field+" must be a non-empty string")
	}
	if !utf8.ValidString(text) {
		return "", &Error{Stage: "input", Code: "CATALOG_NAME_UNICODE_UNSUPPORTED"}
	}
	text = textcompat.Strip(text)
	if utf8.RuneCountInString(text) > 160 {
		return "", registryError("SKILL_REGISTRY_INVALID", "SkillRegistryError", field+" must be 160 characters or fewer")
	}
	return text, nil
}
func editAliases(value any) ([]string, *Error) {
	if value == nil {
		return []string{}, nil
	}
	raw, ok := value.([]any)
	if !ok {
		if typed, yes := value.([]string); yes {
			if len(typed) > cataloginput.YAMLMaxNodes {
				return nil, &Error{Stage: "input", Code: "CATALOG_INPUT_LIMIT"}
			}
			raw = make([]any, len(typed))
			for i, v := range typed {
				raw[i] = v
			}
		} else {
			return nil, registryError("SKILL_REGISTRY_INVALID", "SkillRegistryError", "aliases must be a list")
		}
	}
	if len(raw) > cataloginput.YAMLMaxNodes {
		return nil, &Error{Stage: "input", Code: "CATALOG_INPUT_LIMIT"}
	}
	aliases := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, value := range raw {
		alias, e := editName(value, "alias")
		if e != nil {
			return nil, e
		}
		aliases = append(aliases, alias)
	}
	// Source cleans all values before checking duplicate normalized aliases.
	for _, alias := range aliases {
		key := textcompat.Normalize(alias)
		if seen[key] {
			return nil, registryError("SKILL_NAME_CONFLICT", "SkillRegistryError", "aliases contain a duplicate after normalization")
		}
		seen[key] = true
	}
	return aliases, nil
}
func editGroupID(value any) (string, *Error) {
	id, e := editName(value, "primary_group_id")
	if e != nil {
		return "", e
	}
	valid := true
	for i, c := range []byte(id) {
		if !(c >= 'a' && c <= 'z' || i > 0 && (c >= '0' && c <= '9' || c == '_')) {
			valid = false
			break
		}
	}
	if !valid {
		return "", registryError("SKILL_GROUP_NOT_FOUND", "SkillRegistryError", "primary_group_id must be a lower-case snake_case id")
	}
	return id, nil
}
func customID(name string, existing []string, fallback string) (string, *Error) {
	ascii, e := textcompat.ASCIIFromNFKD15(name)
	if e != nil {
		return "", &Error{Stage: "input", Code: "CATALOG_ID_UNSUPPORTED"}
	}
	var slug strings.Builder
	separator := false
	for _, c := range []byte(ascii) {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			if separator && slug.Len() > 0 {
				slug.WriteByte('_')
			}
			separator = false
			slug.WriteByte(c)
		} else {
			separator = true
		}
	}
	base := slug.String()
	if base == "" {
		base = fallback
	}
	if !strings.HasPrefix(base, "user_") {
		base = "user_" + base
	}
	ids := make(map[string]bool, len(existing))
	for _, id := range existing {
		ids[id] = true
	}
	id := base
	for next := 2; ids[id]; next++ {
		id = base + "_" + strconv.Itoa(next)
	}
	return id, nil
}
func coreNameConflict(state *catalogstore.State, current *catalog.Term, name string, aliases []string) *Error {
	owners := map[string]catalog.Term{}
	for _, skill := range state.CoreSkills {
		for _, value := range append([]string{skill.Name}, skill.Aliases...) {
			owners[textcompat.Normalize(value)] = skill
		}
	}
	allowed := map[string]bool{}
	currentID := ""
	if current != nil {
		currentID = current.ID
		for _, value := range append([]string{current.Name}, current.Aliases...) {
			key := textcompat.Normalize(value)
			if owner, exists := owners[key]; exists && owner.ID != current.ID {
				allowed[key] = true
			}
		}
	}
	for _, value := range append([]string{name}, aliases...) {
		key := textcompat.Normalize(value)
		owner, exists := owners[key]
		if exists && owner.ID != currentID && !allowed[key] {
			return registryError("SKILL_NAME_CONFLICT", "SkillRegistryError", "Name or alias '"+value+"' conflicts with skill '"+owner.Name+"'.")
		}
	}
	return nil
}
func editDomainError(e error) *Error {
	if domain, ok := e.(*catalog.Error); ok {
		return &Error{Stage: "validation", Code: domain.Code, Registry: domain}
	}
	return &Error{Stage: "validation", Code: "CATALOG_VALIDATION_FAILED"}
}
func markTermFields(term *catalog.Term, fields ...string) {
	term.FieldsSet = slices.Clone(term.FieldsSet)
	for _, field := range fields {
		if !slices.Contains(term.FieldsSet, field) {
			term.FieldsSet = append(term.FieldsSet, field)
		}
	}
}
func editIDSet(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}
func planEdit(ctx context.Context, state *catalogstore.State, request editRequest) (*editPlan, *Error) {
	if e := validateEditFields(request.operation, request.payload); e != nil {
		return nil, e
	}
	if ctx == nil || state == nil || state.Snapshot == nil {
		return nil, &Error{Stage: "input", Code: "CATALOG_INPUT_INVALID"}
	}
	if e := cancelled(ctx); e != nil {
		return nil, e
	}
	if request.revision != state.Snapshot.Revision() {
		return nil, revisionConflict()
	}
	groups, terms := slices.Clone(state.CustomGroups), slices.Clone(state.CustomSkills)
	plan := &editPlan{replacements: map[string][]byte{}}
	if request.operation == createGroup {
		name, e := editName(request.payload["name"], "name")
		if e != nil {
			return nil, e
		}
		ids := []string{}
		for _, g := range state.Snapshot.Groups() {
			ids = append(ids, g.ID)
		}
		id, e := customID(name, ids, "custom_group")
		if e != nil {
			return nil, e
		}
		record := catalog.Group{ID: id, Name: name, CascadeArchivedSkillIDs: []string{}}
		groups = append(groups, record)
		effective := []catalog.Group{}
		coreIDs := editIDSet(state.Snapshot.CoreGroupIDs())
		for _, g := range state.Snapshot.Groups() {
			if coreIDs[g.ID] {
				effective = append(effective, g)
			}
		}
		if e := catalog.Validate(append(effective, groups...), state.Snapshot.Skills()); e != nil {
			return nil, editDomainError(e)
		}
		plan.group = &Result{Record: GroupRecord{record, "custom"}}
	} else {
		index := -1
		var record catalog.Term
		if request.operation == updateSkill || request.operation == archiveSkill {
			if slices.Contains(state.Snapshot.CoreSkillIDs(), request.id) {
				return nil, registryError("CORE_SKILL_READ_ONLY", "SkillRegistryError", "Core skills are read-only.")
			}
			index = slices.IndexFunc(terms, func(t catalog.Term) bool { return t.ID == request.id })
			if index < 0 {
				return nil, registryError("SKILL_NOT_FOUND", "SkillRegistryError", "Custom skill was not found.")
			}
			record = terms[index]
			for _, group := range state.Snapshot.Groups() {
				if group.ID == catalog.EffectiveGroupID(record) && group.Archived {
					if request.operation == updateSkill {
						return nil, registryError("SKILL_GROUP_ARCHIVED", "SkillRegistryError", "Restore the skill group before editing one of its skills.")
					}
					if !request.archived {
						return nil, registryError("SKILL_GROUP_ARCHIVED", "SkillRegistryError", "Restore the skill group before restoring one of its skills.")
					}
				}
			}
		}
		if request.operation == archiveSkill {
			record.Archived = request.archived
			markTermFields(&record, "archived")
		} else if request.operation == createSkill || request.operation == updateSkill {
			name, e := editName(request.payload["name"], "name")
			if e != nil {
				return nil, e
			}
			aliases, e := editAliases(request.payload["aliases"])
			if e != nil {
				return nil, e
			}
			groupID, e := editGroupID(request.payload["primary_group_id"])
			if e != nil {
				return nil, e
			}
			var current *catalog.Term
			if request.operation == updateSkill {
				current = &terms[index]
			}
			if e := coreNameConflict(state, current, name, aliases); e != nil {
				return nil, e
			}
			if request.operation == createSkill {
				for _, alias := range aliases {
					if textcompat.Normalize(alias) == textcompat.Normalize(name) {
						return nil, &Error{Stage: "model", Code: "CATALOG_MODEL_INVALID", Model: &cataloginput.ValidationError{Issues: []cataloginput.ValidationIssue{{Type: "value_error", Loc: []any{}, Msg: "Value error, name and aliases must be unique within one competency"}}}}
					}
				}
				ids := []string{}
				for _, term := range state.Snapshot.Skills() {
					ids = append(ids, term.ID)
				}
				id, e := customID(name, ids, "custom_skill")
				if e != nil {
					return nil, e
				}
				utc := request.now.UTC()
				if utc.Year() < 1 || utc.Year() > 9999 {
					return nil, &Error{Stage: "input", Code: "CATALOG_CLOCK_INVALID"}
				}
				stamp, origin := utc.Format("2006-01-02T15:04:05Z"), "manual_registry"
				record = catalog.Term{ID: id, Category: "custom_skill", Kind: "technology", Relations: []catalog.Relation{}, CreatedVia: &origin, CreatedAt: &stamp,
					FieldsSet: []string{"id", "name", "aliases", "category", "primary_group_id", "created_via", "created_at"}}
			}
			record.Name, record.Aliases, record.PrimaryGroupID = name, aliases, &groupID
			markTermFields(&record, "name", "aliases", "primary_group_id")
		} else {
			return nil, &Error{Stage: "input", Code: "CATALOG_OPERATION_INVALID"}
		}
		if index < 0 {
			terms = append(terms, record)
		} else {
			terms[index] = record
		}
		core := []catalog.Term{}
		coreIDs := editIDSet(state.Snapshot.CoreSkillIDs())
		for _, term := range state.Snapshot.Skills() {
			if coreIDs[term.ID] {
				core = append(core, term)
			}
		}
		merged, e := catalog.MergeForMutation(core, terms)
		if e != nil {
			return nil, editDomainError(e)
		}
		if e := catalog.Validate(state.Snapshot.Groups(), merged); e != nil {
			return nil, editDomainError(e)
		}
		plan.skill = &SkillResult{Record: SkillRecord{record, "custom"}}
	}
	rawGroups, rawSkills := state.RawGroups, state.RawSkills
	var e error
	if plan.group != nil {
		rawGroups, e = encodeGroups(groups)
		plan.replacements[catalogstore.CustomGroupsPath] = rawGroups
	} else {
		rawSkills, e = encodeSkills(terms)
		plan.replacements[catalogstore.CustomSkillsPath] = rawSkills
	}
	if e != nil {
		return nil, &Error{Stage: "encode", Code: "CATALOG_ENCODING_FAILED"}
	}
	// Re-decode actual output, not just the model intended by the caller.
	var inputErr *cataloginput.DecodeError
	if plan.group != nil {
		groups, inputErr = cataloginput.DecodeGroups(rawGroups, true)
	} else {
		terms, inputErr = cataloginput.DecodeTerms(rawSkills, true)
	}
	if inputErr != nil {
		return nil, &Error{Stage: "encode", Code: "CATALOG_ENCODING_FAILED"}
	}
	plan.prospective, e = catalog.Build(state.CoreGroups, groups, state.CoreSkills, terms, rawGroups, rawSkills)
	if e != nil {
		return nil, editDomainError(e)
	}
	if e := cancelled(ctx); e != nil {
		return nil, e
	}
	if plan.group != nil {
		plan.group.Revision = plan.prospective.Revision()
	} else {
		plan.skill.Revision = plan.prospective.Revision()
	}
	return plan, nil
}
