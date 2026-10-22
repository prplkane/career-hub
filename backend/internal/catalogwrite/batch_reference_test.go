package catalogwrite

import (
	"testing"
)

// Only the synthetic oracle uses positional JSON tuples. Runtime drafts are
// typed, approved inputs and have no generic JSON/file-writing adapter.
type referenceWire struct {
	EntityID string      `json:"entity_id"`
	IDs      []string    `json:"competency_ids"`
	Pending  [][]*string `json:"pending_skills"`
	Via      *string     `json:"created_via"`
	At       *string     `json:"created_at"`
}
type batchReferenceStep struct {
	editReferenceStep
	References   []referenceWire
	Replacements map[string]string
}

func referenceDrafts(t *testing.T, wire []referenceWire) []SkillReferenceDraft {
	t.Helper()
	result := make([]SkillReferenceDraft, len(wire))
	for i, r := range wire {
		draft := SkillReferenceDraft{EntityID: r.EntityID, CompetencyIDs: r.IDs, CreatedVia: r.Via, CreatedAt: r.At}
		for _, p := range r.Pending {
			if len(p) != 2 || p[0] == nil {
				t.Fatal("invalid oracle tuple")
			}
			draft.PendingSkills = append(draft.PendingSkills, PendingSkill{Name: *p[0], GroupID: p[1]})
		}
		result[i] = draft
	}
	return result
}
