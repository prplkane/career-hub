// Package intakereview builds the editable review inventory from one already
// validated extraction. It is pure: file ownership, revision checks and
// publication belong to the later applicationwrite owner.
package intakereview

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/intakeread"
)

// Error carries only a fixed code. It never retains source text, identifiers,
// paths, parser prose or partially decoded review content.
type Error struct{ Code string }

func (*Error) Error() string { return "intake review could not be built" }

type Result struct {
	Bytes                  []byte
	Revision               string
	Review                 intakeread.Review
	Validation             intakeread.ReviewValidation
	NewDecisionCount       int
	PreservedDecisionCount int
}

// Build creates missing block decisions in extraction order and preserves all
// existing user-authored cards and decisions. JSON is intentionally used as a
// canonical YAML 1.2 document; both native and Python readers accept it.
func Build(extractionRaw, existingRaw []byte) (*Result, *Error) {
	extraction, failure := intakeread.ParseExtraction(extractionRaw)
	if failure != nil {
		return nil, fail("REVIEW_BUILD_EXTRACTION_INVALID")
	}

	entities := []any{}
	decisions := []any{}
	preserved := map[string]any{}
	if existingRaw != nil {
		existing, parseFailure := intakeread.ParseReview(existingRaw)
		if parseFailure != nil {
			return nil, fail("REVIEW_BUILD_EXISTING_INVALID")
		}
		if existing.Source.ID != extraction.Source.ID || existing.Source.SHA256 != extraction.Source.SHA256 {
			return nil, fail("REVIEW_BUILD_SOURCE_MISMATCH")
		}
		document, decodeFailure := decodeReviewDocument(existingRaw, existing)
		if decodeFailure != nil {
			return nil, decodeFailure
		}
		entities = document["entities"].([]any)
		decisions = document["approval_decisions"].([]any)
		blocks := document["block_reviews"].([]any)
		counts := map[string]int{}
		for index, item := range existing.BlockReviews {
			counts[item.BlockID]++
			preserved[item.BlockID] = blocks[index]
		}
		for _, count := range counts {
			if count > 1 {
				return nil, fail("REVIEW_BUILD_DUPLICATE_BLOCK")
			}
		}
	}

	valid := map[string]bool{}
	blockReviews := []any{}
	newCount := 0
	for _, block := range extraction.Blocks {
		if block.Text == "" {
			continue
		}
		valid[block.ID] = true
		if item, ok := preserved[block.ID]; ok {
			blockReviews = append(blockReviews, item)
			continue
		}
		blockReviews = append(blockReviews, map[string]any{
			"source_ref": map[string]any{
				"source_id": extraction.Source.ID,
				"block_id":  block.ID,
				"start":     nil,
				"end":       nil,
			},
			"category":            nil,
			"disposition":         "unassigned",
			"owner_id":            nil,
			"suggested_owner_ids": []any{},
			"user_note":           nil,
		})
		newCount++
	}
	for id := range preserved {
		if !valid[id] {
			return nil, fail("REVIEW_BUILD_STALE_BLOCK")
		}
	}

	document := map[string]any{
		"schema_version":            number(extractionSchemaOne),
		"status":                    "review",
		"extraction_schema_version": number(extraction.SchemaVersion),
		"source": map[string]any{
			"id":        extraction.Source.ID,
			"path":      extraction.Source.Path,
			"file_type": extraction.Source.FileType,
			"sha256":    extraction.Source.SHA256,
		},
		"entities":           entities,
		"block_reviews":      blockReviews,
		"approval_decisions": decisions,
	}
	encoded, encodeFailure := encodeDocument(document)
	if encodeFailure != nil {
		return nil, encodeFailure
	}
	if len(encoded) > intakeread.MaxReviewBytes {
		return nil, fail("REVIEW_BUILD_RESULT_LIMIT")
	}
	review, parseFailure := intakeread.ParseReview(encoded)
	if parseFailure != nil {
		return nil, fail("REVIEW_BUILD_RESULT_INVALID")
	}
	validation := intakeread.ValidateReview(extraction, review)
	if validation.Status == "error" {
		return nil, fail("REVIEW_BUILD_RESULT_INVALID")
	}
	return &Result{
		Bytes: encoded, Revision: revision(encoded), Review: review, Validation: validation,
		NewDecisionCount: newCount, PreservedDecisionCount: len(blockReviews) - newCount,
	}, nil
}

const extractionSchemaOne = "1"

func fail(code string) *Error { return &Error{Code: code} }

func number(value string) json.Number { return json.Number(value) }

func decodeReviewDocument(raw []byte, parsed intakeread.Review) (map[string]any, *Error) {
	value, yamlFailure := cataloginput.ParseYAML(raw)
	if yamlFailure != nil {
		return nil, fail("REVIEW_BUILD_EXISTING_INVALID")
	}
	converted, ok := valueDocument(value).(map[string]any)
	if !ok {
		return nil, fail("REVIEW_BUILD_EXISTING_INVALID")
	}
	entities, entitiesOK := converted["entities"].([]any)
	blocks, blocksOK := converted["block_reviews"].([]any)
	decisions, decisionsOK := converted["approval_decisions"].([]any)
	if !entitiesOK {
		entities = []any{}
	}
	if !decisionsOK {
		decisions = []any{}
	}
	if !blocksOK || len(entities) != len(parsed.Entities) || len(blocks) != len(parsed.BlockReviews) || len(decisions) != len(parsed.ApprovalDecisions) {
		return nil, fail("REVIEW_BUILD_EXISTING_INVALID")
	}
	for index := range entities {
		entity, ok := entities[index].(map[string]any)
		if !ok {
			return nil, fail("REVIEW_BUILD_EXISTING_INVALID")
		}
		normalizeEntity(entity, parsed.Entities[index])
	}
	for index := range blocks {
		block, ok := blocks[index].(map[string]any)
		if !ok {
			return nil, fail("REVIEW_BUILD_EXISTING_INVALID")
		}
		normalizeBlock(block, parsed.BlockReviews[index])
	}
	for index := range decisions {
		decision, ok := decisions[index].(map[string]any)
		if !ok {
			return nil, fail("REVIEW_BUILD_EXISTING_INVALID")
		}
		normalizeDecision(decision, parsed.ApprovalDecisions[index])
	}
	converted["entities"], converted["block_reviews"], converted["approval_decisions"] = entities, blocks, decisions
	normalizeDocumentRoot(converted, parsed)
	return converted, nil
}

func normalizeDocumentRoot(document map[string]any, parsed intakeread.Review) {
	document["schema_version"] = number(parsed.SchemaVersion)
	document["status"] = parsed.Status
	document["extraction_schema_version"] = number(parsed.ExtractionSchemaVersion)
	document["source"] = map[string]any{
		"id": parsed.Source.ID, "path": parsed.Source.Path,
		"file_type": parsed.Source.FileType, "sha256": parsed.Source.SHA256,
	}
}

func normalizeEntity(entity map[string]any, parsed intakeread.ReviewEntity) {
	entity["id"], entity["entity_type"], entity["review_state"] = parsed.ID, parsed.EntityType, parsed.ReviewState
	for _, field := range []string{"name", "headline", "organization", "title", "institution", "degree", "credential", "major", "gpa", "date_text", "location", "content"} {
		if _, ok := entity[field]; !ok {
			entity[field] = ""
		}
	}
	for _, field := range []string{"honors", "emails", "phones", "links", "technologies", "pending_skills", "items"} {
		if _, ok := entity[field]; !ok {
			entity[field] = []any{}
		}
	}
	for _, field := range []string{"suggestion_rule", "user_note"} {
		if _, ok := entity[field]; !ok {
			entity[field] = nil
		}
	}
	if skills, ok := entity["pending_skills"].([]any); ok {
		for _, item := range skills {
			if skill, ok := item.(map[string]any); ok {
				if _, present := skill["primary_group_id"]; !present {
					skill["primary_group_id"] = nil
				}
			}
		}
	}
}

func normalizeBlock(block map[string]any, parsed intakeread.ReviewBlock) {
	ref, ok := block["source_ref"].(map[string]any)
	if !ok {
		ref = map[string]any{}
		block["source_ref"] = ref
	}
	ref["source_id"], ref["block_id"] = parsed.SourceID, parsed.BlockID
	for _, field := range []string{"start", "end"} {
		if _, present := ref[field]; !present {
			ref[field] = nil
		}
	}
	block["disposition"] = parsed.Disposition
	if parsed.Category == nil {
		block["category"] = nil
	} else {
		block["category"] = *parsed.Category
	}
	if parsed.OwnerID == nil {
		block["owner_id"] = nil
	} else {
		block["owner_id"] = *parsed.OwnerID
	}
	suggested := make([]any, len(parsed.SuggestedOwnerIDs))
	for index, id := range parsed.SuggestedOwnerIDs {
		suggested[index] = id
	}
	block["suggested_owner_ids"] = suggested
	if _, present := block["user_note"]; !present {
		block["user_note"] = nil
	}
}

func normalizeDecision(decision map[string]any, parsed intakeread.ReviewDecision) {
	decision["subject_kind"], decision["subject_id"], decision["action"] = parsed.SubjectKind, parsed.SubjectID, parsed.Action
	if parsed.TargetID == nil {
		decision["target_id"] = nil
	} else {
		decision["target_id"] = *parsed.TargetID
	}
	if parsed.FieldResolutions == nil {
		decision["field_resolutions"] = nil
	} else {
		values := map[string]any{}
		for key, value := range parsed.FieldResolutions {
			values[key] = value
		}
		decision["field_resolutions"] = values
	}
}

func valueDocument(value cataloginput.Value) any {
	switch value.Kind {
	case "null":
		return nil
	case "string", "date", "datetime":
		return value.Text
	case "bytes":
		return string(value.Bytes)
	case "bool":
		return value.Boolean
	case "int", "float":
		return json.Number(canonicalNumber(value.Text))
	case "sequence":
		result := make([]any, len(value.Items))
		for index, item := range value.Items {
			result[index] = valueDocument(item)
		}
		return result
	case "mapping":
		result := map[string]any{}
		for _, entry := range value.Entries {
			result[entry.Key.Text] = valueDocument(entry.Value)
		}
		return result
	default:
		return nil
	}
}

func canonicalNumber(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "_", "")
	if strings.HasPrefix(value, "+") {
		value = value[1:]
	}
	if integer, ok := new(big.Int).SetString(value, 10); ok {
		return integer.String()
	}
	return value
}

func encodeDocument(document map[string]any) ([]byte, *Error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(document); err != nil {
		return nil, fail("REVIEW_BUILD_SERIALIZATION_FAILED")
	}
	encoded := bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
	// encoding/json intentionally escapes these two JavaScript separators even
	// with HTML escaping disabled; Python ensure_ascii=False does not.
	encoded = bytes.ReplaceAll(encoded, []byte(`\u2028`), []byte("\u2028"))
	encoded = bytes.ReplaceAll(encoded, []byte(`\u2029`), []byte("\u2029"))
	return encoded, nil
}

// Revision matches the source's sha256 over normalized model JSON. It is valid
// only for bytes already accepted by the strict parser.
func Revision(raw []byte) (string, *Error) {
	encoded, failure := Normalize(raw)
	if failure != nil {
		return "", failure
	}
	return revision(encoded), nil
}

// Normalize returns the complete strict Review model with source-compatible
// defaults as canonical JSON (and therefore valid YAML 1.2). Callers receive
// a fresh byte slice; this function performs no file access or mutation.
func Normalize(raw []byte) ([]byte, *Error) {
	parsed, failure := intakeread.ParseReview(raw)
	if failure != nil {
		return nil, fail("REVIEW_REVISION_INVALID")
	}
	document, decodeFailure := decodeReviewDocument(raw, parsed)
	if decodeFailure != nil {
		return nil, decodeFailure
	}
	return encodeDocument(document)
}

func revision(normalized []byte) string {
	digest := sha256.Sum256(normalized)
	return "sha256:" + hex.EncodeToString(digest[:])
}
