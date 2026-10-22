package emailcandidates

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/emailmime"
)

func TestPythonSourceComposedEmailCases(t *testing.T) {
	t.Parallel()
	fixtures := filepath.Join("..", "..", "..", "tests", "fixtures")
	raw, err := os.ReadFile(filepath.Join(fixtures, "migration_email_message_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Cases []struct {
			EML             string   `json:"eml"`
			Sender          string   `json:"sender"`
			Subject         string   `json:"subject"`
			Ignored         int      `json:"ignored"`
			Titles          []string `json:"titles"`
			Companies       []string `json:"companies"`
			Locations       []string `json:"locations"`
			CandidateSHA256 string   `json:"candidate_sha256"`
			OutcomeSHA256   string   `json:"outcome_sha256"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	for _, test := range suite.Cases {
		t.Run(test.EML, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(fixtures, "email_alerts", test.EML))
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := emailmime.Decode(content)
			if err != nil {
				t.Fatal(err)
			}
			composed, err := ComposeSelected(decoded, test.Sender, test.Subject)
			if err != nil {
				t.Fatal(err)
			}
			if composed.IgnoredLinkCount != test.Ignored {
				t.Errorf("ignored=%d, want %d", composed.IgnoredLinkCount, test.Ignored)
			}
			var titles, companies, locations []string
			for _, candidate := range composed.Candidates {
				titles = append(titles, candidate.Title)
				companies = append(companies, candidate.Company)
				locations = append(locations, candidate.Location)
			}
			if len(composed.Candidates) == 0 {
				titles, companies, locations = []string{}, []string{}, []string{}
			}
			if !reflect.DeepEqual(titles, test.Titles) || !reflect.DeepEqual(companies, test.Companies) || !reflect.DeepEqual(locations, test.Locations) {
				t.Errorf("fields: titles=%q companies=%q locations=%q", titles, companies, locations)
			}
			if composed.Candidates == nil {
				composed.Candidates = []EnrichedCandidate{}
			}
			if composed.ApplicationOutcomes == nil {
				composed.ApplicationOutcomes = []ApplicationOutcome{}
			}
			candidateJSON := canonicalJSON(t, composed.Candidates)
			if actual := digest(candidateJSON); actual != test.CandidateSHA256 {
				t.Errorf("candidate sha256=%s, want %s; json=%s", actual, test.CandidateSHA256, candidateJSON)
			}
			outcomeJSON := canonicalJSON(t, composed.ApplicationOutcomes)
			if actual := digest(outcomeJSON); actual != test.OutcomeSHA256 {
				t.Errorf("outcome sha256=%s, want %s; json=%s", actual, test.OutcomeSHA256, outcomeJSON)
			}
		})
	}
}

func TestMessageWideHTMLBudgets(t *testing.T) {
	t.Parallel()
	part := func(text string) emailmime.BodyPart { return emailmime.BodyPart{ContentType: "text/html", Text: text} }
	links := strings.Repeat(`<a href="https://jobs.example.test/jobs/1">Engineer</a>`, 250)
	decoded := emailmime.Result{Selected: []emailmime.BodyPart{part(links), part(links + `<a href="https://jobs.example.test/jobs/2">Engineer</a>`)}}
	if _, err := ComposeSelected(decoded, "alerts@example.test", "Jobs"); !errors.Is(err, ErrHTMLLinkLimit) {
		t.Fatalf("cumulative links: %v", err)
	}
	complex := strings.Repeat("<div>", 100) + strings.Repeat("<br>", 220)
	decoded = emailmime.Result{Selected: []emailmime.BodyPart{part(complex), part(complex)}}
	if _, err := ComposeSelected(decoded, "alerts@example.test", "Jobs"); !errors.Is(err, ErrHTMLComplexity) {
		t.Fatalf("cumulative relations: %v", err)
	}
}

func TestMessageWideCandidateLimit(t *testing.T) {
	t.Parallel()
	makePart := func(start, count int) emailmime.BodyPart {
		var text strings.Builder
		for i := start; i < start+count; i++ {
			fmt.Fprintf(&text, "Title: Engineer %d Company: Example Systems Location: Remote Summary: Work on systems. https://jobs.example.test/jobs/%d\n\n", i, i)
		}
		return emailmime.BodyPart{ContentType: "text/plain", Text: text.String()}
	}
	exactParts := emailmime.Result{Selected: []emailmime.BodyPart{makePart(0, 60), makePart(60, 40)}}
	exact, err := ComposeSelected(exactParts, "alerts@example.test", "Jobs")
	if err != nil || len(exact.Candidates) != MaxCandidatesPerMessage || exact.Candidates[len(exact.Candidates)-1].Ordinal != MaxCandidatesPerMessage {
		t.Fatalf("exact candidate limit: %d, %v", len(exact.Candidates), err)
	}
	overParts := emailmime.Result{Selected: []emailmime.BodyPart{makePart(0, 60), makePart(60, 41)}}
	if _, err := ComposeSelected(overParts, "alerts@example.test", "Jobs"); !errors.Is(err, ErrCandidateLimit) {
		t.Fatalf("over candidate limit: %v", err)
	}
}
