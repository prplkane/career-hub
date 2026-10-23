package agentcontext

import (
	"context"
	"encoding/json"
	"sort"

	"careerhub.local/backend/internal/jobs/jobevaluation"
)

// CatalogEvidence is an ordered catalog projection. The guarded source-catalog
// builder must supply unique IDs in Python dictionary insertion order, after
// resolving last-wins source duplicates. Never derive this order from a Go map.
type CatalogEvidence struct {
	ID                string   `json:"id"`
	EvidenceType      string   `json:"evidence_type"`
	RelatedExperience []string `json:"related_experience"`
}

// SelectionExperience contains only already validated active experience data.
// The read owner, not this pure function, owns archived-record filtering.
type SelectionExperience struct {
	ID          string `json:"id"`
	Company     any    `json:"company"`
	Role        any    `json:"role"`
	Dates       any    `json:"dates"`
	Category    any    `json:"category"`
	ProofPoints any    `json:"proof_points"`
}
type EvidenceScore struct {
	EvidenceID   string   `json:"evidence_id"`
	EvidenceType string   `json:"evidence_type"`
	Score        int      `json:"score"`
	RequiredIDs  []string `json:"required_requirement_ids"`
	PreferredIDs []string `json:"preferred_requirement_ids"`
}
type ExperienceDecision struct {
	BlockID         string   `json:"block_id"`
	Company         any      `json:"company"`
	Role            any      `json:"role"`
	Dates           any      `json:"dates"`
	Category        any      `json:"category"`
	Selection       string   `json:"selection"`
	CoverageScore   int      `json:"coverage_score"`
	RequiredIDs     []string `json:"required_requirement_ids"`
	PreferredIDs    []string `json:"preferred_requirement_ids"`
	EvidencePreview any      `json:"evidence_preview"`
}
type SelectionWeights struct {
	Required  int `json:"required_requirement_match"`
	Preferred int `json:"preferred_requirement_match"`
	Maximum   int `json:"maximum_full_context_blocks"`
}
type Worksheet struct {
	SchemaVersion        int                  `json:"schema_version"`
	JobID                string               `json:"job_id"`
	DecisionDefault      string               `json:"decision_default"`
	Scoring              SelectionWeights     `json:"scoring"`
	Shortlist            []EvidenceScore      `json:"shortlist"`
	ExperienceCandidates []ExperienceDecision `json:"experience_candidates"`
}

const maxSelectionRecords = 10000

// BuildSelectionWorksheet preserves the source's 3/1 direct-evidence weights,
// stable ties and one-way related-project suppression. All active experiences
// remain undecided, including zero-score and non-shortlisted experiences.
// maxBlocks is normally 6; values outside 1..10000 are explicitly unsupported.
// Inputs must come from bounded validated snapshots; no disk reads occur here.
func BuildSelectionWorksheet(ctx context.Context, matrix Matrix, catalog []CatalogEvidence, experiences []SelectionExperience, maxBlocks int) (*Worksheet, []string, *Error) {
	if err := contextStopped(ctx); err != nil {
		return nil, nil, err
	}
	if len(catalog) > maxSelectionRecords || len(experiences) > maxSelectionRecords || len(matrix.Requirements) > jobevaluation.MaxRequirements || maxBlocks < 1 || maxBlocks > maxSelectionRecords {
		return nil, nil, failure("SELECTION_LIMIT")
	}
	ranked := make([]EvidenceScore, 0, len(catalog))
	byID := make(map[string]int, len(catalog))
	// Conservative encoded-byte accounting also bounds repeated references before
	// JSON encoding allocates them. Inputs remain owned by the validated caller.
	scoreBytes := 0
	for i, item := range catalog {
		scoreBytes += 256 + 6*(len(item.ID)+len(item.EvidenceType))
		if scoreBytes > jobevaluation.MaxResultBytes {
			return nil, nil, failure("SELECTION_LIMIT")
		}
		if _, duplicate := byID[item.ID]; duplicate || item.ID == "" {
			return nil, nil, failure("SELECTION_CATALOG")
		}
		byID[item.ID] = i
		ranked = append(ranked, EvidenceScore{item.ID, item.EvidenceType, 0, []string{}, []string{}})
	}
	// Bound fan-out before retaining per-evidence requirement lists.
	links := 0
	for _, row := range matrix.Requirements {
		if err := contextStopped(ctx); err != nil {
			return nil, nil, err
		}
		weight := 0
		switch row.Priority {
		case "required":
			weight = 3
		case "preferred":
			weight = 1
		default:
			continue
		}
		links += len(row.DirectEvidenceIDs)
		if links > 100000 {
			return nil, nil, failure("SELECTION_LIMIT")
		}
		for _, id := range row.DirectEvidenceIDs {
			i, exists := byID[id]
			if !exists {
				continue
			}
			scoreBytes += 6*len(row.ID) + 4
			if scoreBytes > jobevaluation.MaxResultBytes {
				return nil, nil, failure("SELECTION_LIMIT")
			}
			ranked[i].Score += weight
			if weight == 3 {
				ranked[i].RequiredIDs = append(ranked[i].RequiredIDs, row.ID)
			} else {
				ranked[i].PreferredIDs = append(ranked[i].PreferredIDs, row.ID)
			}
		}
	}
	typeOrder := func(kind string) int {
		switch kind {
		case "experience":
			return 0
		case "project":
			return 1
		case "certification":
			return 2
		}
		return 99
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if typeOrder(a.EvidenceType) != typeOrder(b.EvidenceType) {
			return typeOrder(a.EvidenceType) < typeOrder(b.EvidenceType)
		}
		return byID[a.EvidenceID] < byID[b.EvidenceID]
	})
	out := &Worksheet{1, matrix.JobID, "undecided", SelectionWeights{3, 1, maxBlocks}, []EvidenceScore{}, []ExperienceDecision{}}
	ids := []string{}
	selected := map[string]bool{}
	for _, score := range ranked {
		if err := contextStopped(ctx); err != nil {
			return nil, nil, err
		}
		if score.Score <= 0 {
			continue
		}
		if score.EvidenceType == "project" {
			skip := false
			for _, id := range catalog[byID[score.EvidenceID]].RelatedExperience {
				if selected[id] {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
		}
		out.Shortlist = append(out.Shortlist, score)
		ids = append(ids, score.EvidenceID)
		if score.EvidenceType == "experience" {
			selected[score.EvidenceID] = true
		}
		if len(ids) >= maxBlocks {
			break
		}
	}
	scores := make(map[string]EvidenceScore, len(ranked))
	for _, score := range ranked {
		scores[score.EvidenceID] = score
	}
	outputBudget := contextJSONBudget{ctx: ctx, bytes: scoreBytes + 256 + 6*len(matrix.JobID), maximum: jobevaluation.MaxResultBytes}
	for _, item := range experiences {
		if err := contextStopped(ctx); err != nil {
			return nil, nil, err
		}
		score, exists := scores[item.ID]
		if !exists {
			return nil, nil, failure("SELECTION_CATALOG")
		}
		outputBudget.bytes += 512 // fixed candidate keys and scalar fields
		var preview any
		switch proof := item.ProofPoints.(type) {
		case []string:
			preview = copiedStrings(proof[:min(2, len(proof))])
		case []any:
			preview = proof[:min(2, len(proof))]
		case string:
			preview = string([]rune(proof)[:min(2, len([]rune(proof)))])
		default:
			return nil, nil, failure("SELECTION_INPUT")
		}
		for _, value := range []any{item.ID, item.Company, item.Role, item.Dates, item.Category, score.RequiredIDs, score.PreferredIDs, preview} {
			if e := outputBudget.check(value, 0); e != nil {
				if e.Code == "AGENT_CONTEXT_CANCELED" {
					return nil, nil, e
				}
				if e.Code == "AGENT_CONTEXT_MODEL_LIMIT" {
					return nil, nil, failure("SELECTION_LIMIT")
				}
				return nil, nil, failure("SELECTION_INPUT")
			}
		}
		candidate := ExperienceDecision{
			item.ID, item.Company, item.Role, item.Dates, item.Category, "undecided", score.Score,
			copiedStrings(score.RequiredIDs), copiedStrings(score.PreferredIDs), preview,
		}
		// Legacy metadata/proof can be JSON containers, not just strings. Preserve
		// their values without sharing mutable caller objects in the returned DTO.
		encoded, e := json.Marshal(candidate)
		if e != nil {
			return nil, nil, failure("SELECTION_INPUT")
		}
		var detached ExperienceDecision
		if decodeNumbers(encoded, &detached) != nil {
			return nil, nil, failure("SELECTION_INPUT")
		}
		out.ExperienceCandidates = append(out.ExperienceCandidates, detached)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, nil, failure("SELECTION_INPUT")
	}
	if len(raw) > jobevaluation.MaxResultBytes {
		return nil, nil, failure("SELECTION_LIMIT")
	}
	if err := contextStopped(ctx); err != nil {
		return nil, nil, err
	}
	return out, ids, nil
}
