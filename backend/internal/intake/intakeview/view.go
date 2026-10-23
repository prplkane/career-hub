// Package intakeview projects strict persisted intake artifacts into the
// browser's existing read-only response shapes. It owns no paths or files.
package intakeview

import (
	"encoding/json"
	"math"
	"math/big"
	"path"
	"strconv"
	"strings"
	"time"

	"careerhub.local/backend/internal/intake/intakeread"
	"careerhub.local/backend/internal/intake/intakereview"
	"careerhub.local/backend/internal/skills/cataloginput"
)

type Error struct{ Code string }

func (*Error) Error() string { return "intake browser view could not be projected" }

type ExtractionCounts struct {
	Blocks             int `json:"blocks"`
	Sections           int `json:"sections"`
	Technologies       int `json:"technologies"`
	TechnologyMentions int `json:"technology_mentions"`
	Dates              int `json:"dates"`
	Bullets            int `json:"bullets"`
}

type ExtractionDetail struct {
	ID                 string           `json:"id"`
	SourceName         string           `json:"source_name"`
	ModifiedAt         string           `json:"modified_at"`
	Status             string           `json:"status"`
	Counts             ExtractionCounts `json:"counts"`
	Blocks             []any            `json:"blocks"`
	SectionHints       []any            `json:"section_hints"`
	TechnologyIDs      []any            `json:"technology_ids"`
	TechnologyMentions []any            `json:"technology_mentions"`
	DateCandidates     []any            `json:"date_candidates"`
	BulletCandidates   []any            `json:"bullet_candidates"`
	UnresolvedFields   []any            `json:"unresolved_fields"`
	ReviewExists       bool             `json:"review_exists"`
}

type ReviewValidationIssue struct {
	Severity string  `json:"severity"`
	Code     string  `json:"code"`
	Message  string  `json:"message"`
	BlockID  *string `json:"block_id"`
	EntityID *string `json:"entity_id"`
}

type ReviewValidation struct {
	Status               string                  `json:"status"`
	ExpectedBlockCount   int                     `json:"expected_block_count"`
	ReviewedBlockCount   int                     `json:"reviewed_block_count"`
	UnassignedBlockCount int                     `json:"unassigned_block_count"`
	IgnoredBlockCount    int                     `json:"ignored_block_count"`
	ErrorCount           int                     `json:"error_count"`
	WarningCount         int                     `json:"warning_count"`
	Issues               []ReviewValidationIssue `json:"issues"`
}

type CompletionBlock struct {
	BlockID           string   `json:"block_id"`
	Category          *string  `json:"category"`
	SuggestedOwnerIDs []string `json:"suggested_owner_ids"`
}

type CompletionIssue struct {
	Code     string  `json:"code"`
	Message  string  `json:"message"`
	EntityID *string `json:"entity_id"`
	BlockID  *string `json:"block_id"`
}

type ReviewCompletion struct {
	Status                 string            `json:"status"`
	ReadyForPreview        bool              `json:"ready_for_preview"`
	ReadyToApprove         bool              `json:"ready_to_approve"`
	UnresolvedBlockCount   int               `json:"unresolved_block_count"`
	SuggestedBlockCount    int               `json:"suggested_block_count"`
	CategorizedBlockCount  int               `json:"categorized_block_count"`
	UnclassifiedBlockCount int               `json:"unclassified_block_count"`
	QueuedEntityCount      int               `json:"queued_entity_count"`
	QueuedEntityIDs        []string          `json:"queued_entity_ids"`
	UnresolvedBlocks       []CompletionBlock `json:"unresolved_blocks"`
	ValidationErrorCodes   []string          `json:"validation_error_codes"`
	ApprovalIssueCount     int               `json:"approval_issue_count"`
	ApprovalIssues         []CompletionIssue `json:"approval_issues"`
}

type ReviewDetail struct {
	SourceID            string           `json:"source_id"`
	SourceName          string           `json:"source_name"`
	ModifiedAt          string           `json:"modified_at"`
	Review              map[string]any   `json:"review"`
	ReviewRevision      string           `json:"review_revision"`
	Validation          ReviewValidation `json:"validation"`
	Completion          ReviewCompletion `json:"completion"`
	ApprovalApplication map[string]any   `json:"approval_application"`
	Blocks              []any            `json:"blocks"`
}

func fail(code string) *Error { return &Error{Code: code} }

// Extraction projects a complete strict ExtractionReport. modified is the
// guarded extraction-file timestamp observed by the caller.
func Extraction(raw []byte, modified time.Time, reviewExists bool) (*ExtractionDetail, *Error) {
	parsed, parseFailure := intakeread.ParseExtraction(raw)
	if parseFailure != nil {
		return nil, fail("INTAKE_VIEW_EXTRACTION_INVALID")
	}
	document, decodeFailure := decodeDocument(raw)
	if decodeFailure != nil {
		return nil, decodeFailure
	}
	blocks := list(document, "blocks")
	for _, value := range blocks {
		normalizeBlock(mapping(value))
	}
	sections := list(document, "section_hints")
	for _, value := range sections {
		normalizeSection(mapping(value))
	}
	mentions := list(document, "technology_mentions")
	for _, value := range mentions {
		normalizeMention(mapping(value))
	}
	dates := list(document, "date_candidates")
	for _, value := range dates {
		normalizeDate(mapping(value))
	}
	bullets := list(document, "bullet_candidates")
	for _, value := range bullets {
		normalizeBullet(mapping(value))
	}
	technologies := list(document, "technology_ids")
	unresolved := list(document, "unresolved_fields")
	return &ExtractionDetail{
		ID: parsed.Source.ID, SourceName: sourceBase(parsed.Source.Path), ModifiedAt: pythonUTC(modified), Status: "extracted",
		Counts: ExtractionCounts{len(blocks), len(sections), len(technologies), len(mentions), len(dates), len(bullets)},
		Blocks: blocks, SectionHints: sections, TechnologyIDs: technologies, TechnologyMentions: mentions,
		DateCandidates: dates, BulletCandidates: bullets, UnresolvedFields: unresolved, ReviewExists: reviewExists,
	}, nil
}

// Review projects one complete Review detail response. approvalRaw is ignored
// only when approvalPresent is false; an invalid present approval fails closed.
func Review(extractionRaw, reviewRaw, approvalRaw []byte, approvalPresent bool, modified time.Time) (*ReviewDetail, *Error) {
	extraction, extractionFailure := intakeread.ParseExtraction(extractionRaw)
	if extractionFailure != nil {
		return nil, fail("INTAKE_VIEW_EXTRACTION_INVALID")
	}
	review, reviewFailure := intakeread.ParseReview(reviewRaw)
	if reviewFailure != nil {
		return nil, fail("INTAKE_VIEW_REVIEW_INVALID")
	}
	normalized, normalizeFailure := intakereview.Normalize(reviewRaw)
	if normalizeFailure != nil {
		return nil, fail("INTAKE_VIEW_REVIEW_INVALID")
	}
	var reviewDocument map[string]any
	if json.Unmarshal(normalized, &reviewDocument) != nil {
		return nil, fail("INTAKE_VIEW_REVIEW_INVALID")
	}
	revision, revisionFailure := intakereview.Revision(reviewRaw)
	if revisionFailure != nil {
		return nil, fail("INTAKE_VIEW_REVIEW_INVALID")
	}
	backfilled, _ := intakeread.BackfillReviewCategories(extraction, review)
	blocks := list(reviewDocument, "block_reviews")
	if len(blocks) != len(backfilled.BlockReviews) {
		return nil, fail("INTAKE_VIEW_REVIEW_INVALID")
	}
	for index, value := range blocks {
		item := mapping(value)
		if backfilled.BlockReviews[index].Category == nil {
			item["category"] = nil
		} else {
			item["category"] = *backfilled.BlockReviews[index].Category
		}
	}
	validation := intakeread.ValidateReview(extraction, backfilled)
	completion := intakeread.BuildReviewCompletion(backfilled, validation)
	approval, approvalFailure := approvalDocument(approvalRaw, approvalPresent)
	if approvalFailure != nil {
		return nil, approvalFailure
	}
	extractionDocument, decodeFailure := decodeDocument(extractionRaw)
	if decodeFailure != nil {
		return nil, decodeFailure
	}
	extractionBlocks := list(extractionDocument, "blocks")
	visibleBlocks := make([]any, 0, len(extractionBlocks))
	for _, value := range extractionBlocks {
		item := mapping(value)
		normalizeBlock(item)
		if textValue, _ := item["text"].(string); textValue != "" {
			visibleBlocks = append(visibleBlocks, item)
		}
	}
	return &ReviewDetail{
		SourceID: extraction.Source.ID, SourceName: sourceBase(extraction.Source.Path), ModifiedAt: pythonUTC(modified),
		Review: reviewDocument, ReviewRevision: revision, Validation: validationView(validation),
		Completion: completionView(completion), ApprovalApplication: approval, Blocks: visibleBlocks,
	}, nil
}

// ReviewValidationView projects validated issues for an explicit review save.
func ReviewValidationView(value intakeread.ReviewValidation) ReviewValidation {
	return validationView(value)
}

func validationView(value intakeread.ReviewValidation) ReviewValidation {
	issues := make([]ReviewValidationIssue, 0, len(value.Issues))
	for _, issue := range value.Issues {
		issues = append(issues, ReviewValidationIssue{issue.Severity, issue.Code, issue.Message, optional(issue.BlockID), optional(issue.EntityID)})
	}
	return ReviewValidation{value.Status, value.ExpectedBlockCount, value.ReviewedBlockCount, value.UnassignedBlockCount, value.IgnoredBlockCount, value.ErrorCount, value.WarningCount, issues}
}

func completionView(value intakeread.ReviewCompletion) ReviewCompletion {
	blocks := make([]CompletionBlock, 0, len(value.UnresolvedBlocks))
	for _, block := range value.UnresolvedBlocks {
		blocks = append(blocks, CompletionBlock{block.BlockID, block.Category, nonnilStrings(block.SuggestedOwnerIDs)})
	}
	issues := make([]CompletionIssue, 0, len(value.ApprovalIssues))
	for _, issue := range value.ApprovalIssues {
		issues = append(issues, CompletionIssue{issue.Code, issue.Message, optional(issue.EntityID), optional(issue.BlockID)})
	}
	return ReviewCompletion{
		value.Status, value.ReadyForPreview, value.ReadyToApprove, value.UnresolvedBlockCount,
		value.SuggestedBlockCount, value.CategorizedBlockCount, value.UnclassifiedBlockCount,
		value.QueuedEntityCount, nonnilStrings(value.QueuedEntityIDs), blocks,
		nonnilStrings(value.ValidationErrorCodes), value.ApprovalIssueCount, issues,
	}
}

func approvalDocument(raw []byte, present bool) (map[string]any, *Error) {
	if !present {
		return nil, nil
	}
	parsed, failure := intakeread.ParseApproval(raw)
	if failure != nil {
		return nil, fail("INTAKE_VIEW_APPROVAL_INVALID")
	}
	document, decodeFailure := decodeDocument(raw)
	if decodeFailure != nil {
		return nil, fail("INTAKE_VIEW_APPROVAL_INVALID")
	}
	document["schema_version"] = integerNumber(parsed.SchemaVersion)
	document["status"] = parsed.Status
	for _, field := range []string{"previous_registry_revision", "registry_revision"} {
		if _, ok := document[field]; !ok {
			document[field] = nil
		}
	}
	for _, field := range []string{"created_skill_ids", "changes", "changed_files"} {
		if _, ok := document[field]; !ok {
			document[field] = []any{}
		}
	}
	for _, value := range list(document, "changes") {
		item := mapping(value)
		for _, field := range []string{"entity_type", "target_id", "vault_path", "field_resolutions"} {
			if _, ok := item[field]; !ok {
				item[field] = nil
			}
		}
	}
	return document, nil
}

func normalizeBlock(item map[string]any) {
	item["order"] = integerNumberValue(item["order"])
	if _, ok := item["style"]; !ok {
		item["style"] = nil
	}
	if value, ok := item["is_bullet"]; ok {
		item["is_bullet"] = booleanValue(value)
	} else {
		item["is_bullet"] = false
	}
}

func normalizeSection(item map[string]any) {
	normalizeSourceRef(mapping(item["source_ref"]))
	if _, ok := item["extractor"]; !ok {
		item["extractor"] = "spacy_entity_ruler"
	}
}

func normalizeMention(item map[string]any) {
	normalizeSourceRef(mapping(item["source_ref"]))
	if _, ok := item["extractor"]; !ok {
		item["extractor"] = "spacy_phrase_matcher"
	}
}

func normalizeDate(item map[string]any) {
	normalizeSourceRef(mapping(item["source_ref"]))
	if _, ok := item["end_text"]; !ok {
		item["end_text"] = nil
	}
	if value, ok := item["current"]; ok {
		item["current"] = booleanValue(value)
	} else {
		item["current"] = false
	}
}

func normalizeBullet(item map[string]any) {
	normalizeSourceRef(mapping(item["source_ref"]))
	if _, ok := item["extractor"]; !ok {
		item["extractor"] = "document_bullet"
	}
}

func normalizeSourceRef(item map[string]any) {
	for _, field := range []string{"start", "end"} {
		value, ok := item[field]
		if !ok || value == nil {
			item[field] = nil
		} else {
			item[field] = integerNumberValue(value)
		}
	}
}

func decodeDocument(raw []byte) (map[string]any, *Error) {
	value, failure := cataloginput.ParseYAML(raw)
	if failure != nil {
		return nil, fail("INTAKE_VIEW_INPUT_INVALID")
	}
	document, ok := projectValue(value).(map[string]any)
	if !ok {
		return nil, fail("INTAKE_VIEW_INPUT_INVALID")
	}
	return document, nil
}

func projectValue(value cataloginput.Value) any {
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
		return json.Number(strings.ReplaceAll(strings.TrimPrefix(strings.TrimSpace(value.Text), "+"), "_", ""))
	case "sequence":
		result := make([]any, len(value.Items))
		for index, item := range value.Items {
			result[index] = projectValue(item)
		}
		return result
	case "mapping":
		result := map[string]any{}
		for _, entry := range value.Entries {
			key, ok := projectValue(entry.Key).(string)
			if !ok {
				return nil
			}
			result[key] = projectValue(entry.Value)
		}
		return result
	}
	return nil
}

func integerNumberValue(value any) json.Number {
	switch typed := value.(type) {
	case json.Number:
		return integerNumber(typed.String())
	case string:
		return integerNumber(typed)
	case bool:
		if typed {
			return "1"
		}
		return "0"
	case float64:
		return integerNumber(strconv.FormatFloat(typed, 'g', -1, 64))
	}
	return "0"
}

func integerNumber(value string) json.Number {
	value = strings.ReplaceAll(strings.TrimPrefix(strings.TrimSpace(value), "+"), "_", "")
	if before, after, found := strings.Cut(value, "."); found && strings.Trim(after, "0") == "" {
		value = before
	}
	if integer, ok := new(big.Int).SetString(value, 10); ok {
		return json.Number(integer.String())
	}
	if number, err := strconv.ParseFloat(value, 64); err == nil && !math.IsInf(number, 0) && !math.IsNaN(number) {
		return json.Number(strconv.FormatInt(int64(number), 10))
	}
	return "0"
}

func booleanValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case json.Number:
		return typed.String() != "0"
	case string:
		switch strings.ToLower(typed) {
		case "1", "true", "t", "yes", "y", "on":
			return true
		}
	}
	return false
}

func list(document map[string]any, field string) []any {
	values, _ := document[field].([]any)
	if values == nil {
		return []any{}
	}
	return values
}

func mapping(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}

func nonnilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string(nil), values...)
}

func sourceBase(value string) string {
	if value == "" {
		return ""
	}
	return path.Base(strings.ReplaceAll(value, `\`, "/"))
}

func pythonUTC(value time.Time) string {
	seconds := float64(value.Unix()) + float64(value.Nanosecond())/1e9
	normalized := time.UnixMicro(int64(math.RoundToEven(seconds * 1e6))).UTC()
	if normalized.Nanosecond()/1000 == 0 {
		return normalized.Format("2006-01-02T15:04:05+00:00")
	}
	return normalized.Format("2006-01-02T15:04:05.000000+00:00")
}
