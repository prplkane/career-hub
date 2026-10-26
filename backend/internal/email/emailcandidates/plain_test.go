package emailcandidates

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"careerhub.local/backend/internal/email/emailmime"
)

func TestPythonSourcePlainCases(t *testing.T) {
	t.Parallel()
	fixtures := filepath.Join("..", "..", "..", "..", "tests", "fixtures")
	raw, err := os.ReadFile(filepath.Join(fixtures, "migration_email_plain_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Cases []struct {
			Name       string      `json:"name"`
			EML        string      `json:"eml"`
			Text       string      `json:"text"`
			Ignored    int         `json:"ignored"`
			Candidates []Candidate `json:"candidates"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	for _, test := range suite.Cases {
		t.Run(test.Name, func(t *testing.T) {
			text := test.Text
			if test.EML != "" {
				content, err := os.ReadFile(filepath.Join(fixtures, "email_alerts", test.EML))
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := emailmime.Decode(content)
				if err != nil || len(decoded.Selected) != 1 || decoded.Selected[0].ContentType != "text/plain" {
					t.Fatalf("MIME plain body: %v, %d selected", err, len(decoded.Selected))
				}
				text = decoded.Selected[0].Text
			}
			candidates, ignored := ExtractPlain(text)
			if ignored != test.Ignored || len(candidates) != len(test.Candidates) {
				t.Fatalf("ignored=%d candidates=%d, want %d/%d", ignored, len(candidates), test.Ignored, len(test.Candidates))
			}
			for i, candidate := range candidates {
				if candidate != test.Candidates[i] {
					t.Errorf("candidate %d = %#v, want %#v", i, candidate, test.Candidates[i])
				}
			}
		})
	}
}

func TestRepeatedRecommendationsDoNotCauseQuadraticCleanup(t *testing.T) {
	t.Parallel()
	text := strings.Repeat("Great Match ", 10_000) + "https://jobs.example.test/job/77"
	candidates, ignored := ExtractPlain(text)
	if ignored != 0 || len(candidates) != 1 {
		t.Fatalf("ignored=%d candidates=%d", ignored, len(candidates))
	}
	if candidates[0].ContentScope != "link_only" || candidates[0].SourceRecommendation.Kind != "great_match" {
		t.Fatalf("scope=%q recommendation=%q", candidates[0].ContentScope, candidates[0].SourceRecommendation.Kind)
	}
}
