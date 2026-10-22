package intakeread

import (
	"regexp"
	"strings"

	"careerhub.local/backend/internal/cataloginput"
)

const MaxReviewBytes = cataloginput.YAMLMaxInputBytes

// Review is a strict-valid, content-minimized IntakeReview. Callers own file
// admission and cross-record workflow validation.
type Review struct {
	SchemaVersion           string
	Status                  string
	ExtractionSchemaVersion string
	Source                  Source
	Entities                []ReviewEntity
	BlockReviews            []ReviewBlock
	ApprovalDecisions       []ReviewDecision
}

type ReviewEntity struct {
	ID, EntityType, ReviewState string
	// LegacyRequiredEmpty is evaluated from source fields, not inferred from type.
	LegacyRequiredEmpty map[string]bool
}

type ReviewBlock struct {
	SourceID, BlockID, Disposition string
	Category                       *string
	OwnerID                        *string
	SuggestedOwnerIDs              []string
}

type ReviewDecision struct {
	SubjectKind, SubjectID, Action string
	TargetID                       *string
	// Nil preserves legacy whole-card decisions; an empty map is distinct.
	FieldResolutions map[string]string
}

var groupIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func skillDraft(v cataloginput.Value) bool {
	_, ok := object(v, map[string]fieldRule{
		"name":             {true, textLength(1, 160)},
		"primary_group_id": {false, nullable(func(v cataloginput.Value) bool { s, ok := text(v); return ok && groupIDPattern.MatchString(s) })},
	})
	return ok
}

func textLength(min, max int) func(cataloginput.Value) bool {
	return func(v cataloginput.Value) bool {
		s, ok := text(v)
		return ok && len([]rune(s)) >= min && len([]rune(s)) <= max
	}
}

var reviewEntityRules = map[string]fieldRule{
	"id": {true, nonemptyText}, "entity_type": {true, nonemptyText},
	"review_state": {false, oneOf("active", "queued")},
	"name":         {false, validText}, "headline": {false, validText}, "organization": {false, validText},
	"title": {false, validText}, "institution": {false, validText}, "degree": {false, validText},
	"credential": {false, validText}, "major": {false, validText}, "gpa": {false, validText},
	"honors": {false, sequence(validText)}, "date_text": {false, validText}, "location": {false, validText},
	"emails": {false, sequence(validText)}, "phones": {false, sequence(validText)}, "links": {false, sequence(validText)},
	"technologies": {false, sequence(validText)}, "pending_skills": {false, sequence(skillDraft)},
	"content": {false, validText}, "items": {false, sequence(validText)},
	"suggestion_rule": {false, nullable(validText)}, "user_note": {false, nullable(validText)},
}

func reviewEntity(v cataloginput.Value) bool { _, ok := object(v, reviewEntityRules); return ok }

func legacyRequiredEmpty(value cataloginput.Value, found bool) bool {
	if !found || value.Kind == "null" {
		return true
	}
	if value.Kind == "sequence" {
		return len(value.Items) == 0
	}
	// Pydantic coerces UTF-8 YAML binary scalars to str before the source
	// completion check compares the field with an empty string.
	textValue, ok := text(value)
	return ok && textValue == ""
}

var reviewBlockRules = map[string]fieldRule{
	"source_ref": {true, sourceRef}, "category": {false, nullable(oneOf("profile", "summary", "experience", "education", "project", "skill", "certification", "other"))},
	"disposition": {false, oneOf("unassigned", "categorized", "assigned", "ignored", "structural")},
	"owner_id":    {false, nullable(validText)}, "suggested_owner_ids": {false, sequence(validText)}, "user_note": {false, nullable(validText)},
}

func reviewBlock(v cataloginput.Value) bool { _, ok := object(v, reviewBlockRules); return ok }

func reviewFieldResolutions(v cataloginput.Value) bool {
	if v.Kind != "mapping" {
		return false
	}
	for _, e := range v.Entries {
		key, ok := text(e.Key)
		if !ok || strings.TrimSpace(key) == "" || !oneOf("vault", "source", "add_source", "excluded", "same")(e.Value) {
			return false
		}
	}
	return true
}

var reviewDecisionRules = map[string]fieldRule{
	"subject_kind": {true, oneOf("entity", "block")}, "subject_id": {true, nonemptyText},
	"action":    {true, oneOf("merge", "create", "source_only", "exclude")},
	"target_id": {false, nullable(validText)}, "field_resolutions": {false, nullable(reviewFieldResolutions)},
}

func reviewDecision(v cataloginput.Value) bool {
	f, ok := object(v, reviewDecisionRules)
	if !ok {
		return false
	}
	action := stringField(f, "action")
	target, hasTarget := f["target_id"]
	if action == "merge" {
		if !hasTarget || target.Kind == "null" || stringField(f, "target_id") == "" {
			return false
		}
	} else if hasTarget && target.Kind != "null" {
		return false
	}
	if resolutions, found := f["field_resolutions"]; found && resolutions.Kind == "mapping" && len(resolutions.Entries) > 0 && (action == "source_only" || action == "exclude") {
		return false
	}
	return true
}

var reviewRules = map[string]fieldRule{
	"schema_version": {false, validInteger}, "status": {false, oneOf("review")},
	"extraction_schema_version": {true, validInteger}, "source": {true, source},
	"entities": {false, sequence(reviewEntity)}, "block_reviews": {true, sequence(reviewBlock)},
	"approval_decisions": {false, sequence(reviewDecision)},
}

func ParseReview(raw []byte) (Review, *Error) {
	v, yamlError := cataloginput.ParseYAML(raw)
	if yamlError != nil {
		code := "REVIEW_YAML_INVALID"
		if yamlError.Kind == "limit" {
			code = "REVIEW_INPUT_LIMIT"
		} else if yamlError.Kind == "unsupported" {
			code = "REVIEW_INPUT_UNSUPPORTED"
		}
		return Review{}, &Error{Code: code}
	}
	root, ok := object(v, reviewRules)
	if !ok {
		return Review{}, &Error{Code: "REVIEW_MODEL_INVALID"}
	}
	sf, _ := object(root["source"], sourceRules)
	out := Review{SchemaVersion: "1", Status: "review", Source: Source{ID: stringField(sf, "id"), Path: stringField(sf, "path"), FileType: stringField(sf, "file_type"), SHA256: stringField(sf, "sha256")}}
	if x, found := root["schema_version"]; found {
		out.SchemaVersion, _ = integer(x)
	}
	out.ExtractionSchemaVersion, _ = integer(root["extraction_schema_version"])
	for _, item := range root["entities"].Items {
		f, _ := object(item, reviewEntityRules)
		entity := ReviewEntity{ID: stringField(f, "id"), EntityType: stringField(f, "entity_type"), ReviewState: "active", LegacyRequiredEmpty: map[string]bool{}}
		if state, found := f["review_state"]; found {
			entity.ReviewState, _ = text(state)
		}
		for _, key := range []string{"name", "organization", "title", "institution", "credential", "date_text", "items"} {
			value, found := f[key]
			entity.LegacyRequiredEmpty[key] = legacyRequiredEmpty(value, found)
		}
		out.Entities = append(out.Entities, entity)
	}
	for _, item := range root["block_reviews"].Items {
		f, _ := object(item, reviewBlockRules)
		ref, _ := object(f["source_ref"], sourceRefRules)
		b := ReviewBlock{SourceID: stringField(ref, "source_id"), BlockID: stringField(ref, "block_id"), Disposition: "unassigned"}
		if x, found := f["disposition"]; found {
			b.Disposition, _ = text(x)
		}
		if x, found := f["category"]; found && x.Kind != "null" {
			s, _ := text(x)
			b.Category = &s
		}
		if x, found := f["owner_id"]; found && x.Kind != "null" {
			s, _ := text(x)
			b.OwnerID = &s
		}
		if x, found := f["suggested_owner_ids"]; found {
			for _, id := range x.Items {
				s, _ := text(id)
				b.SuggestedOwnerIDs = append(b.SuggestedOwnerIDs, s)
			}
		}
		out.BlockReviews = append(out.BlockReviews, b)
	}
	for _, item := range root["approval_decisions"].Items {
		f, _ := object(item, reviewDecisionRules)
		d := ReviewDecision{SubjectKind: stringField(f, "subject_kind"), SubjectID: stringField(f, "subject_id"), Action: stringField(f, "action")}
		if x, found := f["target_id"]; found && x.Kind != "null" {
			s, _ := text(x)
			d.TargetID = &s
		}
		if x, found := f["field_resolutions"]; found && x.Kind == "mapping" {
			d.FieldResolutions = map[string]string{}
			for _, e := range x.Entries {
				k, _ := text(e.Key)
				val, _ := text(e.Value)
				d.FieldResolutions[k] = val
			}
		}
		out.ApprovalDecisions = append(out.ApprovalDecisions, d)
	}
	return out, nil
}
