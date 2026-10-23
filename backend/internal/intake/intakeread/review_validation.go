package intakeread

import (
	"fmt"
	"sort"
)

// BackfillReviewCategories returns a separate review value; explicit category
// choices always win. The count matches Python's in-place backfill operation.
func BackfillReviewCategories(extraction Extraction, review Review) (Review, int) {
	out := review
	out.BlockReviews = append([]ReviewBlock(nil), review.BlockReviews...)
	entities := map[string]ReviewEntity{}
	for _, entity := range review.Entities {
		entities[entity.ID] = entity
	}
	sections := map[string]string{}
	sectionMap := map[string]string{"contact": "profile", "summary": "summary", "experience": "experience", "education": "education", "projects": "project", "skills": "skill", "certifications": "certification"}
	for _, hint := range extraction.SectionHints {
		sections[hint.BlockID] = sectionMap[hint.SectionType]
	}
	count := 0
	for i := range out.BlockReviews {
		block := &out.BlockReviews[i]
		if block.Category != nil {
			continue
		}
		category := ""
		if block.OwnerID != nil {
			if owner, ok := entities[*block.OwnerID]; ok && validCategory(owner.EntityType) {
				category = owner.EntityType
			}
		}
		if category == "" && block.Disposition == "structural" {
			category = sections[block.BlockID]
		}
		if category != "" {
			value := category
			block.Category = &value
			count++
		}
	}
	return out, count
}

func validCategory(value string) bool {
	switch value {
	case "profile", "summary", "experience", "education", "project", "skill", "certification", "other":
		return true
	}
	return false
}

type ReviewIssue struct{ Severity, Code, Message, BlockID, EntityID string }
type ReviewValidation struct {
	Status                                                                          string
	ExpectedBlockCount, ReviewedBlockCount, UnassignedBlockCount, IgnoredBlockCount int
	ErrorCount, WarningCount                                                        int
	Issues                                                                          []ReviewIssue
}

// ValidateReview mirrors source cross-record issue order. It assumes both pure
// parsers have already accepted the complete strict models.
func ValidateReview(extraction Extraction, review Review) ReviewValidation {
	out := ReviewValidation{Status: "success", ReviewedBlockCount: len(review.BlockReviews)}
	add := func(code, message, blockID, entityID string) {
		out.Issues = append(out.Issues, ReviewIssue{Severity: "error", Code: code, Message: message, BlockID: blockID, EntityID: entityID})
		out.ErrorCount++
	}
	expected := map[string]bool{}
	for _, block := range extraction.Blocks {
		if block.Text != "" {
			expected[block.ID] = true
		}
	}
	out.ExpectedBlockCount = len(expected)
	reviewedCounts := map[string]int{}
	for _, item := range review.BlockReviews {
		reviewedCounts[item.BlockID]++
	}
	entityCounts := map[string]int{}
	entityOrder := []string{}
	entities := map[string]ReviewEntity{}
	for _, entity := range review.Entities {
		if entityCounts[entity.ID] == 0 {
			entityOrder = append(entityOrder, entity.ID)
		}
		entityCounts[entity.ID]++
		entities[entity.ID] = entity
	}
	if review.Source.ID != extraction.Source.ID {
		add("SOURCE_ID_MISMATCH", "Review and extraction source ids differ.", "", "")
	}
	if review.Source.SHA256 != extraction.Source.SHA256 {
		add("SOURCE_HASH_MISMATCH", "Review and extraction source hashes differ.", "", "")
	}
	if review.ExtractionSchemaVersion != extraction.SchemaVersion {
		add("EXTRACTION_SCHEMA_MISMATCH", "Review references a different extraction schema version.", "", "")
	}
	for _, id := range entityOrder {
		if entityCounts[id] > 1 {
			add("DUPLICATE_ENTITY_ID", fmt.Sprintf("Entity id appears %d times: %s", entityCounts[id], id), "", "")
		}
	}
	for _, item := range review.BlockReviews {
		id := item.BlockID
		if item.SourceID != extraction.Source.ID {
			add("INVALID_SOURCE_REF", "Block review points to a different source.", id, "")
		}
		if !expected[id] {
			add("UNKNOWN_OR_BLANK_BLOCK_REF", "Block review must point to a non-empty extracted block.", id, "")
		}
		if reviewedCounts[id] > 1 {
			add("DUPLICATE_BLOCK_REVIEW", "A non-empty extracted block must appear exactly once in review.", id, "")
		}
		if item.Disposition == "assigned" {
			if item.OwnerID == nil {
				add("ASSIGNED_BLOCK_WITHOUT_OWNER", "Assigned block requires an owner_id.", id, "")
			} else if owner, ok := entities[*item.OwnerID]; !ok {
				add("UNKNOWN_BLOCK_OWNER", "Assigned owner does not exist: "+*item.OwnerID, id, "")
			} else if item.Category != nil && (!validCategory(owner.EntityType) || owner.EntityType != *item.Category) {
				add("CATEGORY_OWNER_MISMATCH", "Block category does not match the assigned card type.", id, "")
			}
		} else if item.Disposition == "categorized" {
			if item.Category == nil {
				add("CATEGORIZED_BLOCK_WITHOUT_CATEGORY", "Category-only blocks require a category.", id, "")
			}
			if item.OwnerID != nil {
				add("CATEGORIZED_BLOCK_WITH_OWNER", "Category-only blocks cannot have an owner.", id, "")
			}
		} else if item.OwnerID != nil {
			add("OWNER_WITHOUT_ASSIGNMENT", "Only an assigned block may have an owner_id.", id, "")
		}
		seen := map[string]bool{}
		duplicate := false
		for _, suggested := range item.SuggestedOwnerIDs {
			if seen[suggested] {
				duplicate = true
			}
			seen[suggested] = true
		}
		if duplicate {
			add("DUPLICATE_SUGGESTED_OWNER", "Suggested owner ids must be unique.", id, "")
		}
		for _, suggested := range item.SuggestedOwnerIDs {
			if entityCounts[suggested] == 0 {
				add("UNKNOWN_SUGGESTED_OWNER", "Suggested owner does not exist: "+suggested, id, "")
			}
		}
	}
	missing := []string{}
	for id := range expected {
		if reviewedCounts[id] == 0 {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	for _, id := range missing {
		add("MISSING_BLOCK_REVIEW", "Non-empty extracted block is missing from review.", id, "")
	}
	type subject struct{ kind, id string }
	decisions := map[subject]int{}
	decisionOrder := []subject{}
	for _, d := range review.ApprovalDecisions {
		key := subject{d.SubjectKind, d.SubjectID}
		if decisions[key] == 0 {
			decisionOrder = append(decisionOrder, key)
		}
		decisions[key]++
	}
	categorized := map[string]bool{}
	for _, item := range review.BlockReviews {
		if item.Disposition == "categorized" {
			categorized[item.BlockID] = true
		}
	}
	for _, key := range decisionOrder {
		if decisions[key] > 1 {
			if key.kind == "block" {
				add("DUPLICATE_APPROVAL_DECISION", "Each approval subject may have only one decision.", key.id, "")
			} else {
				add("DUPLICATE_APPROVAL_DECISION", "Each approval subject may have only one decision.", "", key.id)
			}
		}
	}
	for _, d := range review.ApprovalDecisions {
		if d.SubjectKind == "entity" {
			if entityCounts[d.SubjectID] == 0 {
				add("UNKNOWN_APPROVAL_ENTITY", "Approval decision points to an unknown review card.", "", d.SubjectID)
			}
			continue
		}
		if !expected[d.SubjectID] {
			add("UNKNOWN_APPROVAL_BLOCK", "Approval decision points to an unknown source block.", d.SubjectID, "")
		} else if !categorized[d.SubjectID] {
			add("APPROVAL_BLOCK_NOT_CATEGORY_ONLY", "Block approval decisions are allowed only for category-only blocks.", d.SubjectID, "")
		}
		if d.Action != "source_only" && d.Action != "exclude" {
			add("INVALID_BLOCK_APPROVAL_ACTION", "Category-only blocks may be kept source-only or excluded.", d.SubjectID, "")
		}
	}
	for _, item := range review.BlockReviews {
		if item.Disposition == "unassigned" {
			out.UnassignedBlockCount++
		}
		if item.Disposition == "ignored" {
			out.IgnoredBlockCount++
		}
	}
	if out.UnassignedBlockCount > 0 {
		out.Issues = append(out.Issues, ReviewIssue{Severity: "warning", Code: "UNASSIGNED_BLOCKS", Message: fmt.Sprintf("%d source blocks still require a decision.", out.UnassignedBlockCount)})
		out.WarningCount++
	}
	if out.ErrorCount > 0 {
		out.Status = "error"
	} else if out.WarningCount > 0 {
		out.Status = "success_with_warnings"
	}
	return out
}
