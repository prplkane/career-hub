package intakeread

import "testing"

// The fictional Python ResumeDigestSourceCharacterizationTests
// pin the same branch order, including unsupported filenames and archived rows.
func TestDigestStatusSourcePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, workflow, runRegistry, currentRegistry, runExtractor, currentExtractor, want string
		hasRun                                                                             bool
	}{
		{"no matched run", "saved_to_vault", "old", "new", "old", "new", "not_digested", false},
		{"review error beats stale catalog", "review_error", "old", "new", "v1", "v2", "error", true},
		{"stale catalog beats saved approval", "saved_to_vault", "old", "new", "v2", "v2", "outdated", true},
		{"stale extractor beats saved approval", "saved_to_vault", "new", "new", "v1", "v2", "outdated", true},
		{"current approval", "saved_to_vault", "new", "new", "v2", "v2", "digested", true},
		{"current review still open", "vault_decision", "new", "new", "v2", "v2", "in_progress", true},
		{"current extraction without review", "extracted", "new", "new", "v2", "v2", "in_progress", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DigestStatus(tc.hasRun, tc.workflow, tc.runRegistry, tc.currentRegistry, tc.runExtractor, tc.currentExtractor)
			if got != tc.want {
				t.Fatalf("status %q, want %q", got, tc.want)
			}
		})
	}
}
