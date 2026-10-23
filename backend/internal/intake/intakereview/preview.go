package intakereview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"careerhub.local/backend/internal/intake/intakeread"
	"careerhub.local/backend/internal/skills/cataloginput"
)

// ApprovalPreviewPaths returns a fresh ordered list. These exact bytes and
// path separators participate in the Python-compatible Vault revision.
func ApprovalPreviewPaths() []string {
	return []string{"career_vault/profile.yaml", "career_vault/education.yaml", "career_vault/experience_blocks.yaml", "career_vault/project_blocks.yaml", "career_vault/skill_blocks.yaml"}
}

type ApprovalReason struct {
	Code   string   `json:"code"`
	Values []string `json:"values"`
}
type ApprovalBlock struct {
	BlockID  string `json:"block_id"`
	Category string `json:"category"`
	Kind     string `json:"kind"`
	Text     string `json:"text"`
}
type ApprovalCandidate struct {
	TargetID    string           `json:"target_id"`
	TargetType  string           `json:"target_type"`
	TargetLabel string           `json:"target_label"`
	VaultPath   string           `json:"vault_path"`
	Fields      map[string]any   `json:"fields"`
	Reasons     []ApprovalReason `json:"reasons"`
}
type ApprovalEntity struct {
	SourceEntityID string              `json:"source_entity_id"`
	EntityType     string              `json:"entity_type"`
	SourceLabel    string              `json:"source_label"`
	SourceFields   map[string]any      `json:"source_fields"`
	SourceBlockIDs []string            `json:"source_block_ids"`
	SourceBlocks   []ApprovalBlock     `json:"source_blocks"`
	Outcome        string              `json:"outcome"`
	Candidates     []ApprovalCandidate `json:"candidates"`
}
type ApprovalEngine struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version string `json:"version"`
}
type ApprovalPlan struct {
	SchemaVersion      int              `json:"schema_version"`
	Status             string           `json:"status"`
	SourceID           string           `json:"source_id"`
	ReviewRevision     string           `json:"review_revision"`
	VaultRevision      string           `json:"vault_revision"`
	RegistryRevision   string           `json:"registry_revision"`
	Engine             ApprovalEngine   `json:"engine"`
	Entities           []ApprovalEntity `json:"entities"`
	CategoryOnlyBlocks []ApprovalBlock  `json:"category_only_blocks"`
}

// BuildApprovalPreview is pure. The caller owns admitted roots, snapshots,
// catalog validation and freshness rechecks. Pending skills are display-only;
// no merge, catalog update or approval is performed. skills maps registry IDs
// to names, including archived entries, as in the source registry projection.
func BuildApprovalPreview(ctx context.Context, extractionRaw, reviewRaw []byte, vault map[string][]byte, registryRevision string, skills map[string]string) (*ApprovalPlan, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, fail("APPROVAL_PREVIEW_CANCELED")
	}
	decodedRevision, err := hex.DecodeString(registryRevision)
	if err != nil || len(decodedRevision) != 32 || hex.EncodeToString(decodedRevision) != registryRevision {
		return nil, fail("APPROVAL_PREVIEW_REGISTRY_INVALID")
	}
	extraction, extractionFailure := intakeread.ParseExtraction(extractionRaw)
	if extractionFailure != nil {
		return nil, fail("APPROVAL_PREVIEW_EXTRACTION_INVALID")
	}
	review, reviewFailure := intakeread.ParseReview(reviewRaw)
	if reviewFailure != nil || review.Source != extraction.Source {
		return nil, fail("APPROVAL_PREVIEW_REVIEW_INVALID")
	}
	document, failure := decodeReviewDocument(reviewRaw, review)
	if failure != nil {
		return nil, fail("APPROVAL_PREVIEW_REVIEW_INVALID")
	}
	review, _ = intakeread.BackfillReviewCategories(extraction, review)
	if !intakeread.BuildReviewCompletion(review, intakeread.ValidateReview(extraction, review)).ReadyForPreview {
		return nil, fail("APPROVAL_PREVIEW_INCOMPLETE")
	}
	// The source preview hashes the model after legacy category backfill.
	for i, value := range document["block_reviews"].([]any) {
		if review.BlockReviews[i].Category != nil {
			value.(map[string]any)["category"] = *review.BlockReviews[i].Category
		}
	}
	normalized, failure := encodeDocument(document)
	if failure != nil {
		return nil, fail("APPROVAL_PREVIEW_REVIEW_INVALID")
	}
	state, vaultRevision, failure := previewVault(vault)
	if failure != nil {
		return nil, failure
	}
	plan := &ApprovalPlan{SchemaVersion: 1, Status: "preview", SourceID: review.Source.ID, ReviewRevision: revision(normalized), VaultRevision: vaultRevision, RegistryRevision: registryRevision,
		Engine: ApprovalEngine{"deterministic", "career_vault_matcher", "1"}, Entities: []ApprovalEntity{}, CategoryOnlyBlocks: []ApprovalBlock{}}
	blocks := map[string]intakeread.Block{}
	for _, block := range extraction.Blocks {
		if block.Text != "" {
			blocks[block.ID] = block
		}
	}
	owned := map[string][]ApprovalBlock{}
	for _, item := range review.BlockReviews {
		block, present := blocks[item.BlockID]
		if !present {
			continue
		}
		category := ""
		if item.Category != nil {
			category = *item.Category
		}
		value := ApprovalBlock{block.ID, category, block.Kind, block.Text}
		if item.Disposition == "assigned" && item.OwnerID != nil && *item.OwnerID != "" {
			owned[*item.OwnerID] = append(owned[*item.OwnerID], value)
		}
		if item.Disposition == "categorized" && category != "" {
			plan.CategoryOnlyBlocks = append(plan.CategoryOnlyBlocks, value)
		}
	}
	sortBlocks := func(items []ApprovalBlock) {
		sort.SliceStable(items, func(i, j int) bool {
			return order(blocks[items[i].BlockID].Order).Cmp(order(blocks[items[j].BlockID].Order)) < 0
		})
	}
	sortBlocks(plan.CategoryOnlyBlocks)
	comparisons, responseBytes := 0, 0
	for _, value := range document["entities"].([]any) {
		if ctx.Err() != nil {
			return nil, fail("APPROVAL_PREVIEW_CANCELED")
		}
		entity := value.(map[string]any)
		if previewText(entity["review_state"]) != "active" {
			continue
		}
		typ, id := previewText(entity["entity_type"]), previewText(entity["id"])
		candidates, candidateFailure := previewCandidates(ctx, entity, state, &comparisons)
		if candidateFailure != nil {
			return nil, candidateFailure
		}
		outcome := "new"
		if len(candidates) > 0 {
			outcome = "possible_match"
		}
		if typ != "profile" && typ != "experience" && typ != "education" && typ != "certification" && typ != "project" && typ != "skill" {
			outcome = "unsupported"
		}
		sourceBlocks := append([]ApprovalBlock{}, owned[id]...)
		sortBlocks(sourceBlocks)
		ids := []string{}
		proof := append([]string{}, previewList(entity["items"])...)
		for i := range sourceBlocks {
			block := &sourceBlocks[i]
			if block.Category == "" {
				block.Category = typ
			}
			ids = append(ids, block.BlockID)
			if blocks[block.BlockID].IsBullet {
				proof = append(proof, block.Text)
			}
		}
		names := []string{}
		for _, skillID := range previewList(entity["technologies"]) {
			name, present := skills[skillID]
			if !present {
				name = skillID
			}
			names = append(names, name)
		}
		for _, pending := range entity["pending_skills"].([]any) {
			names = append(names, previewText(pending.(map[string]any)["name"]))
		}
		item := ApprovalEntity{id, typ, previewLabel(entity), previewSourceFields(entity, previewUnique(proof), names), ids, sourceBlocks, outcome, candidates}
		encoded, err := json.Marshal(item)
		responseBytes += len(encoded)
		if err != nil || responseBytes > 4<<20 {
			return nil, fail("APPROVAL_PREVIEW_RESULT_LIMIT")
		}
		plan.Entities = append(plan.Entities, item)
	}
	encoded, err := json.Marshal(plan)
	if err != nil || len(encoded) > 4<<20 {
		return nil, fail("APPROVAL_PREVIEW_RESULT_LIMIT")
	}
	if ctx.Err() != nil {
		return nil, fail("APPROVAL_PREVIEW_CANCELED")
	}
	return plan, nil
}

// previewVault accepts the supported scalar/list/mapping YAML subset. It
// refuses unrepresentable shapes instead of silently losing candidate fields.
func previewVault(raw map[string][]byte) (map[string]any, string, *Error) {
	state := map[string]any{}
	hash := sha256.New()
	total := 0
	for i, path := range ApprovalPreviewPaths() {
		data, present := raw[path]
		total += len(data)
		if !present {
			return nil, "", fail("APPROVAL_PREVIEW_VAULT_MISSING")
		}
		if total > 32<<20 {
			return nil, "", fail("APPROVAL_PREVIEW_INPUT_LIMIT")
		}
		value, err := cataloginput.ParseYAML(data)
		if err != nil {
			if err.Kind == "limit" {
				return nil, "", fail("APPROVAL_PREVIEW_INPUT_LIMIT")
			}
			return nil, "", fail("APPROVAL_PREVIEW_VAULT_INVALID")
		}
		if (i < 2 && value.Kind != "mapping") || (i >= 2 && value.Kind != "sequence") || !previewSupported(value) {
			return nil, "", fail("APPROVAL_PREVIEW_VAULT_UNSUPPORTED")
		}
		state[path] = valueDocument(value)
		hash.Write([]byte(path))
		hash.Write([]byte{0})
		hash.Write(data)
		hash.Write([]byte{0})
	}
	return state, "sha256:" + fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func previewSupported(v cataloginput.Value) bool {
	switch v.Kind {
	case "null", "string", "int", "bool", "date":
		return true
	case "float":
		return json.Valid([]byte(v.Text))
	case "sequence":
		for _, item := range v.Items {
			if !previewSupported(item) {
				return false
			}
		}
		return true
	case "mapping":
		for _, entry := range v.Entries {
			if entry.Key.Kind != "string" || !previewSupported(entry.Value) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
