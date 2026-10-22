package intakeread

import (
	"strings"
	"unicode"
)

// CompletionBlock and CompletionIssue retain the source's ordered blockers
// without carrying user-authored text or source message prose.
type CompletionBlock struct {
	BlockID           string
	Category          *string
	SuggestedOwnerIDs []string
}
type CompletionIssue struct{ Code, Message, EntityID, BlockID string }
type ReviewCompletion struct {
	Status                                                                                   string
	ReadyForPreview, ReadyToApprove                                                          bool
	UnresolvedBlockCount, SuggestedBlockCount, CategorizedBlockCount, UnclassifiedBlockCount int
	QueuedEntityCount                                                                        int
	QueuedEntityIDs                                                                          []string
	UnresolvedBlocks                                                                         []CompletionBlock
	ValidationErrorCodes                                                                     []string
	ApprovalIssueCount                                                                       int
	ApprovalIssues                                                                           []CompletionIssue
}

var legacyRequired = map[string][]string{
	"profile": {"name"}, "experience": {"organization", "title"}, "education": {"institution"},
	"certification": {"name", "credential", "date_text"}, "project": {"title"}, "skill": {"name", "items"},
}
var granularRequired = map[string][]string{
	"experience": {"organization", "role", "dates"}, "education": {"institution"},
	"certification": {"name", "credential", "completed"}, "project": {"name", "summary"}, "skill": {"skill_family", "keywords"},
}

// BuildReviewCompletion mirrors the source readiness and approval blockers.
// It consumes the explicitly backfilled review and its cross-record validation.
func BuildReviewCompletion(review Review, validation ReviewValidation) ReviewCompletion {
	out := ReviewCompletion{}
	seenCodes := map[string]bool{}
	for _, issue := range validation.Issues {
		if issue.Severity == "error" && !seenCodes[issue.Code] {
			out.ValidationErrorCodes = append(out.ValidationErrorCodes, issue.Code)
			seenCodes[issue.Code] = true
		}
	}
	for _, block := range review.BlockReviews {
		if block.Disposition != "unassigned" {
			continue
		}
		out.UnresolvedBlocks = append(out.UnresolvedBlocks, CompletionBlock{BlockID: block.BlockID, Category: block.Category, SuggestedOwnerIDs: append([]string(nil), block.SuggestedOwnerIDs...)})
		if len(block.SuggestedOwnerIDs) > 0 {
			out.SuggestedBlockCount++
		}
		if block.Category != nil {
			out.CategorizedBlockCount++
		}
	}
	out.UnresolvedBlockCount = len(out.UnresolvedBlocks)
	out.UnclassifiedBlockCount = out.UnresolvedBlockCount - out.CategorizedBlockCount
	for _, entity := range review.Entities {
		if entity.ReviewState == "queued" {
			out.QueuedEntityIDs = append(out.QueuedEntityIDs, entity.ID)
		}
	}
	out.QueuedEntityCount = len(out.QueuedEntityIDs)
	if len(out.ValidationErrorCodes) > 0 {
		out.Status = "invalid"
	} else if out.UnresolvedBlockCount > 0 || out.QueuedEntityCount > 0 {
		out.Status = "incomplete"
	} else {
		out.Status = "ready"
	}
	out.ReadyForPreview = out.Status == "ready"
	assigned := map[string]int{}
	for _, block := range review.BlockReviews {
		if block.Disposition == "assigned" && block.OwnerID != nil && *block.OwnerID != "" {
			assigned[*block.OwnerID]++
		}
	}
	type subject struct{ kind, id string }
	decisions := map[subject]ReviewDecision{}
	for _, decision := range review.ApprovalDecisions {
		decisions[subject{decision.SubjectKind, decision.SubjectID}] = decision
	}
	add := func(code, message, entityID, blockID string) {
		out.ApprovalIssues = append(out.ApprovalIssues, CompletionIssue{Code: code, Message: message, EntityID: entityID, BlockID: blockID})
	}
	for _, block := range review.BlockReviews {
		if block.Disposition == "categorized" {
			if _, ok := decisions[subject{"block", block.BlockID}]; !ok {
				add("MISSING_APPROVAL_DECISION", "Choose Do not add, or return to card review and attach this block.", "", block.BlockID)
			}
		}
	}
	for _, entity := range review.Entities {
		if entity.ReviewState != "active" {
			continue
		}
		if assigned[entity.ID] == 0 {
			add("ENTITY_WITHOUT_SOURCE_BLOCKS", "Active card has no assigned source blocks.", entity.ID, "")
			continue
		}
		decision, ok := decisions[subject{"entity", entity.ID}]
		if !ok {
			add("MISSING_APPROVAL_DECISION", "Choose how this reviewed card should be handled.", entity.ID, "")
			continue
		}
		if decision.Action == "source_only" || decision.Action == "exclude" {
			continue
		}
		legacy, supported := legacyRequired[entity.EntityType]
		if !supported {
			add("UNSUPPORTED_APPROVAL_DESTINATION", title(entity.EntityType)+" cannot be added to the Career Vault in this phase.", entity.ID, "")
			continue
		}
		if decision.Action != "create" {
			continue
		}
		if decision.FieldResolutions == nil {
			for _, field := range legacy {
				if entity.LegacyRequiredEmpty[field] {
					add("MISSING_APPROVAL_FIELD", title(entity.EntityType)+" card requires source "+legacyFieldLabel(field)+" before creating a Career Vault record.", entity.ID, "")
				}
			}
		} else {
			for _, field := range granularRequired[entity.EntityType] {
				resolution := decision.FieldResolutions[field]
				if resolution != "source" && resolution != "add_source" {
					add("MISSING_APPROVAL_FIELD", title(entity.EntityType)+" card requires source "+granularFieldLabel(field)+" before creating a Career Vault record.", entity.ID, "")
				}
			}
		}
	}
	out.ApprovalIssueCount = len(out.ApprovalIssues)
	out.ReadyToApprove = out.ReadyForPreview && out.ApprovalIssueCount == 0
	return out
}

func title(value string) string {
	var result strings.Builder
	previousCased := false
	for _, point := range value {
		if unicode.IsLetter(point) {
			if previousCased {
				point = unicode.ToLower(point)
			} else {
				point = unicode.ToTitle(point)
			}
			previousCased = true
		} else {
			previousCased = false
		}
		result.WriteRune(point)
	}
	return result.String()
}

func legacyFieldLabel(field string) string {
	labels := map[string]string{"date_text": "completion date", "items": "skill items"}
	if value := labels[field]; value != "" {
		return value
	}
	return field
}

func granularFieldLabel(field string) string {
	labels := map[string]string{"completed": "completion date", "summary": "summary", "skill_family": "skill family", "keywords": "skill items"}
	if value := labels[field]; value != "" {
		return value
	}
	return field
}

// WorkflowInput distinguishes absent optional artifacts from present malformed
// bytes. The file owner must supply guarded bytes; this function performs no I/O.
type WorkflowInput struct {
	ApprovalPresent bool
	ApprovalRaw     []byte
	ReviewPresent   bool
	ReviewRaw       []byte
	ExtractionRaw   []byte
}
type WorkflowResult struct {
	Status, SavedAt, ErrorCode               string
	ReviewEntityCount                        int
	UnresolvedBlockCount, ApprovalIssueCount *int
	Completion                               *ReviewCompletion
}

func intPointer(value int) *int { return &value }

// ReviewWorkflow follows resume_library_status approval-first precedence.
func ReviewWorkflow(input WorkflowInput) WorkflowResult {
	if input.ApprovalPresent {
		approval, failure := ParseApproval(input.ApprovalRaw)
		if failure != nil {
			return WorkflowResult{Status: "review_error", ErrorCode: failure.Code}
		}
		return WorkflowResult{Status: "saved_to_vault", SavedAt: approval.AppliedAt, ReviewEntityCount: approval.ChangeCount, UnresolvedBlockCount: intPointer(0), ApprovalIssueCount: intPointer(0)}
	}
	if !input.ReviewPresent {
		return WorkflowResult{Status: "extracted"}
	}
	extraction, failure := ParseExtraction(input.ExtractionRaw)
	if failure != nil {
		return WorkflowResult{Status: "review_error", ErrorCode: failure.Code}
	}
	review, failure := ParseReview(input.ReviewRaw)
	if failure != nil {
		return WorkflowResult{Status: "review_error", ErrorCode: failure.Code}
	}
	backfilled, _ := BackfillReviewCategories(extraction, review)
	validation := ValidateReview(extraction, backfilled)
	completion := BuildReviewCompletion(backfilled, validation)
	status := "in_review"
	if completion.Status == "invalid" {
		status = "review_error"
	} else if completion.ReadyToApprove {
		status = "ready_to_save"
	} else if completion.ReadyForPreview {
		status = "vault_decision"
	}
	return WorkflowResult{Status: status, ReviewEntityCount: len(review.Entities), UnresolvedBlockCount: intPointer(completion.UnresolvedBlockCount), ApprovalIssueCount: intPointer(completion.ApprovalIssueCount), Completion: &completion}
}
