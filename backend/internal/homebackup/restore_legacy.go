package homebackup

import (
	"bytes"
	"encoding/json"
)

// isLegacyRestoreJournal recognizes Python's canonical v1 record solely to
// provide an actionable refusal. It establishes no physical Home identity and
// must never authorize adoption, root movement, key access or journal removal.
func isLegacyRestoreJournal(raw []byte) bool {
	// Field order matches Python's sort_keys=True, including each root object.
	var legacy struct {
		MarkerSHA256 string `json:"marker_sha256"`
		RestoreID    string `json:"restore_id"`
		Roots        []struct {
			HadActive bool   `json:"had_active"`
			Name      string `json:"name"`
		} `json:"roots"`
		Version int `json:"version"`
	}
	if len(raw) == 0 || len(raw) > maxRestoreJournalBytes || json.Unmarshal(raw, &legacy) != nil ||
		legacy.Version != 1 || !lowerHex(legacy.RestoreID, 32) || !lowerHex(legacy.MarkerSHA256, 64) ||
		legacy.Roots == nil || len(legacy.Roots) > 8 {
		return false
	}
	previous := ""
	for _, root := range legacy.Roots {
		if !allowedRoot(root.Name) || root.Name <= previous {
			return false
		}
		previous = root.Name
	}
	canonical, err := json.Marshal(legacy)
	return err == nil && bytes.Equal(raw, append(canonical, '\n'))
}
