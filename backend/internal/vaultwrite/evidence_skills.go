package vaultwrite

import (
	"context"
	"slices"
	"unicode/utf8"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/catalogwrite"
	"careerhub.local/backend/internal/skills/catalog"
)

type evidenceSelection struct {
	ids      []string
	pending  []catalogwrite.PendingSkill
	revision string
}

func parseEvidenceSelection(payload map[string]any) (*evidenceSelection, *Error) {
	present := false
	for _, key := range selectionFields {
		if _, ok := payload[key]; ok {
			present = true
		}
	}
	if !present {
		return nil, nil
	}
	rawIDs, ok := payload["competency_ids"].([]any)
	if !ok {
		return nil, invalid("competency_ids must be a list of skill ids")
	}
	if len(rawIDs) > cataloginput.YAMLMaxNodes {
		return nil, &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_LIMIT"}
	}
	ids := make([]string, 0, len(rawIDs))
	for _, item := range rawIDs {
		id, ok := item.(string)
		if !ok {
			return nil, invalid("competency_ids must be a list of skill ids")
		}
		ids = append(ids, id)
	}
	rawPending, ok := payload["pending_skills"].([]any)
	revision, revisionOK := payload["registry_revision"].(string)
	if !ok || !revisionOK {
		return nil, invalid("Skill selection is incomplete")
	}
	if len(rawIDs)+len(rawPending) > cataloginput.YAMLMaxNodes {
		return nil, &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_LIMIT"}
	}
	selection := &evidenceSelection{ids: ids, revision: revision, pending: make([]catalogwrite.PendingSkill, 0, len(rawPending))}
	for _, raw := range rawPending {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, invalid("pending_skills must contain named skill proposals")
		}
		name, ok := item["name"].(string)
		if !ok {
			return nil, invalid("pending_skills must contain named skill proposals")
		}
		var group *string
		if raw := item["primary_group_id"]; raw != nil {
			text, ok := raw.(string)
			if !ok {
				return nil, invalid("pending skill group id must be a string")
			}
			group = &text
		}
		selection.pending = append(selection.pending, catalogwrite.PendingSkill{Name: name, GroupID: group})
	}
	// The request adapter also caps the complete body. Retained archived IDs
	// bypass the registry planner, so bound every submitted string here too.
	size := 0
	check := func(text string) *Error {
		if !utf8.ValidString(text) {
			return &Error{Stage: "input", Code: "VAULT_EDITOR_UNICODE_UNSUPPORTED"}
		}
		size += len(text)
		if size > maxPayloadBytes {
			return &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_LIMIT"}
		}
		return nil
	}
	if e := check(revision); e != nil {
		return nil, e
	}
	for _, id := range ids {
		if e := check(id); e != nil {
			return nil, e
		}
	}
	for _, item := range selection.pending {
		if e := check(item.Name); e != nil {
			return nil, e
		}
		if item.GroupID != nil {
			if e := check(*item.GroupID); e != nil {
				return nil, e
			}
		}
	}
	return selection, nil
}

func catalogFailure(e *catalogwrite.Error) *Error {
	if e.Code == "CATALOG_OPERATION_CANCELLED" {
		return canceled()
	}
	result := &Error{Stage: e.Stage, Code: e.Code, Catalog: e}
	if e.Registry != nil {
		result.SourceClass = e.Registry.Class
		result.SourceMessage = e.Registry.Message
	}
	return result
}

func priorEvidenceIDs(record cataloginput.Value) (map[string]bool, *Error) {
	result := map[string]bool{}
	value, present := get(record, "competency_ids")
	if !present {
		return result, nil
	}
	switch value.Kind {
	case "sequence":
		for _, item := range value.Items {
			if item.Kind == "string" {
				result[item.Text] = true
			}
		}
	case "mapping":
		for _, entry := range value.Entries {
			if entry.Key.Kind == "string" {
				result[entry.Key.Text] = true
			}
		}
	case "string":
		for _, r := range value.Text {
			result[string(r)] = true
		}
	case "bytes":
		// Python bytes iterate integers, so none qualify as string IDs.
	default:
		return nil, &Error{Stage: "input", Code: "VAULT_EDITOR_PRIOR_SKILLS_UNSUPPORTED"}
	}
	return result, nil
}

func planEvidenceSkills(ctx context.Context, state *catalogstore.State, selection *evidenceSelection, id, stamp string, record cataloginput.Value) (cataloginput.Value, *catalogwrite.SkillReferencePlan, *Error) {
	if ctx == nil || state == nil || state.Snapshot == nil || selection == nil {
		return cataloginput.Value{}, nil, &Error{Stage: "configuration", Code: "VAULT_EDITOR_SKILLS_UNAVAILABLE"}
	}
	prior, e := priorEvidenceIDs(record)
	if e != nil {
		return cataloginput.Value{}, nil, e
	}
	byID := map[string]catalog.Term{}
	for _, skill := range state.Snapshot.Skills() {
		byID[skill.ID] = skill
	}
	retained := map[string]bool{}
	selected := []string{}
	for _, id := range selection.ids {
		if skill, ok := byID[id]; ok && skill.Archived && prior[id] {
			retained[id] = true
		} else {
			selected = append(selected, id)
		}
	}
	via := "vault_editor"
	plan, issue := catalogwrite.PlanSkillReferences(ctx, state, []catalogwrite.SkillReferenceDraft{{
		EntityID: id, CompetencyIDs: selected, PendingSkills: selection.pending, CreatedVia: &via, CreatedAt: &stamp,
	}}, selection.revision)
	if issue != nil {
		return cataloginput.Value{}, nil, catalogFailure(issue)
	}
	result := plan.Result()
	planned := map[string]string{}
	for i, skillID := range result.SkillIDsByEntity[id] {
		planned[skillID] = result.SkillNamesByEntity[id][i]
	}
	resolved, names := cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{}}, cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{}}
	seen := map[string]bool{}
	add := func(skillID string) {
		if seen[skillID] {
			return
		}
		name, exists := planned[skillID]
		if !exists {
			name = byID[skillID].Name
		}
		seen[skillID] = true
		resolved.Items = append(resolved.Items, textValue(skillID))
		names.Items = append(names.Items, textValue(name))
	}
	for _, skillID := range selection.ids {
		if _, planned := planned[skillID]; retained[skillID] || planned {
			add(skillID)
		}
	}
	for _, skillID := range result.SkillIDsByEntity[id] {
		add(skillID)
	}
	// Replace top-level fields in an owned entry slice. Nested extension values
	// are retained without modification, never edited as a side effect of a plan.
	record.Entries = slices.Clone(record.Entries)
	set(&record, "competency_ids", resolved)
	set(&record, "technologies", names)
	return record, plan, nil
}

func validateEvidenceSkillLinks(before, after cataloginput.Value, id string, state *catalogstore.State) *Error {
	fail := func() *Error { return &Error{Stage: "validation", Code: "VAULT_EDITOR_SKILL_LINKS_CHANGED"} }
	if state == nil || state.Snapshot == nil || before.Kind != "sequence" || after.Kind != "sequence" {
		return fail()
	}
	index := -1
	for i, record := range after.Items {
		key, _ := get(record, "id")
		if record.Kind == "mapping" && key.Kind == "string" && key.Text == id {
			if index >= 0 {
				return fail()
			}
			index = i
		}
	}
	if index < 0 || (len(after.Items) != len(before.Items) && len(after.Items) != len(before.Items)+1) {
		return fail()
	}
	var old cataloginput.Value
	if len(after.Items) == len(before.Items)+1 {
		if index != len(before.Items) {
			return fail()
		}
	} else {
		old = before.Items[index]
		oldID, _ := get(old, "id")
		if old.Kind != "mapping" || oldID.Kind != "string" || oldID.Text != id {
			return fail()
		}
	}
	for i, record := range before.Items {
		if i != index && !equalValue(record, after.Items[i]) {
			return fail()
		}
	}
	prior, e := priorEvidenceIDs(old)
	if e != nil {
		return e
	}
	record := after.Items[index]
	ids, _ := get(record, "competency_ids")
	names, _ := get(record, "technologies")
	if ids.Kind != "sequence" || names.Kind != "sequence" || len(ids.Items) != len(names.Items) {
		return fail()
	}
	skills := map[string]catalog.Term{}
	for _, skill := range state.Snapshot.Skills() {
		skills[skill.ID] = skill
	}
	seen := map[string]bool{}
	for i, key := range ids.Items {
		skill, exists := skills[key.Text]
		if key.Kind != "string" || !exists || seen[key.Text] || (skill.Archived && !prior[key.Text]) {
			return fail()
		}
		if names.Items[i].Kind != "string" || names.Items[i].Text != skill.Name {
			return fail()
		}
		seen[key.Text] = true
	}
	return nil
}
