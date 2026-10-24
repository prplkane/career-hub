package emailcandidates

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/email/emailmime"
)

func canonicalJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var mapped any
	if err := json.Unmarshal(raw, &mapped); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(mapped); err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSuffix(out.Bytes(), []byte{'\n'})
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func TestPythonSourceSpecificHTMLCases(t *testing.T) {
	t.Parallel()
	fixtures := filepath.Join("..", "..", "..", "..", "tests", "fixtures")
	raw, err := os.ReadFile(filepath.Join(fixtures, "migration_email_source_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Cases []struct {
			Name            string   `json:"name"`
			EML             string   `json:"eml"`
			HTML            string   `json:"html"`
			Sender          string   `json:"sender"`
			Subject         string   `json:"subject"`
			Ignored         int      `json:"ignored"`
			AnchorCount     int      `json:"anchor_count"`
			RelationCount   int      `json:"relation_count"`
			VisibleSHA256   string   `json:"visible_sha256"`
			Titles          []string `json:"titles"`
			Companies       []string `json:"companies"`
			Locations       []string `json:"locations"`
			Identities      []string `json:"identities"`
			CandidateSHA256 string   `json:"candidate_sha256"`
			OutcomeSHA256   string   `json:"outcome_sha256"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	for _, test := range suite.Cases {
		t.Run(test.Name, func(t *testing.T) {
			htmlText := test.HTML
			if test.EML != "" {
				content, err := os.ReadFile(filepath.Join(fixtures, "email_alerts", test.EML))
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := emailmime.Decode(content)
				if err != nil || len(decoded.Selected) != 1 || decoded.Selected[0].ContentType != "text/html" {
					t.Fatalf("MIME HTML body: %v, %d selected", err, len(decoded.Selected))
				}
				htmlText = decoded.Selected[0].Text
			}
			result, err := ExtractHTMLSource(htmlText, MaxHTMLLinks, MaxHTMLRelationReferences, test.Sender, test.Subject)
			if err != nil {
				t.Fatal(err)
			}
			candidates, outcomes := SeparateApplicationOutcomes(test.Subject, test.Sender, result.VisibleText, result.Candidates)
			if result.IgnoredLinks != test.Ignored || result.AnchorCount != test.AnchorCount || result.RelationReferenceCount != test.RelationCount {
				t.Errorf("metadata = ignored=%d anchors=%d relations=%d, want %d/%d/%d", result.IgnoredLinks,
					result.AnchorCount, result.RelationReferenceCount, test.Ignored, test.AnchorCount, test.RelationCount)
			}
			if actual := digest([]byte(result.VisibleText)); actual != test.VisibleSHA256 {
				t.Errorf("visible sha256 = %s, want %s (text=%q)", actual, test.VisibleSHA256, result.VisibleText)
			}
			var titles, companies, locations, identities []string
			for _, candidate := range candidates {
				titles = append(titles, candidate.Title)
				companies = append(companies, candidate.Company)
				locations = append(locations, candidate.Location)
				identities = append(identities, candidate.PostingIdentity)
			}
			if len(candidates) == 0 {
				titles, companies, locations, identities = []string{}, []string{}, []string{}, []string{}
			}
			if !reflect.DeepEqual(titles, test.Titles) || !reflect.DeepEqual(companies, test.Companies) ||
				!reflect.DeepEqual(locations, test.Locations) || !reflect.DeepEqual(identities, test.Identities) {
				t.Errorf("fields: titles=%q companies=%q locations=%q identities=%q", titles, companies, locations, identities)
			}
			if candidates == nil {
				candidates = []Candidate{}
			}
			if outcomes == nil {
				outcomes = []ApplicationOutcome{}
			}
			candidateJSON := canonicalJSON(t, candidates)
			if actual := digest(candidateJSON); actual != test.CandidateSHA256 {
				t.Errorf("candidate sha256 = %s, want %s; json=%s", actual, test.CandidateSHA256, candidateJSON)
			}
			outcomeJSON := canonicalJSON(t, outcomes)
			if actual := digest(outcomeJSON); actual != test.OutcomeSHA256 {
				t.Errorf("outcome sha256 = %s, want %s; json=%s", actual, test.OutcomeSHA256, outcomeJSON)
			}
		})
	}
}

func TestOutcomeEvidenceMustBeUniqueAndExplicit(t *testing.T) {
	t.Parallel()
	base := Candidate{
		Title: "Data Engineer", Company: "Synthetic Commerce", SourceProvider: "linkedin",
		PostingIdentity: "linkedin:4451000001", SourceText: "Data Engineer Synthetic Commerce · United States Applied on Aug 12",
	}
	subject := "Your application to Data Engineer at Synthetic Commerce"
	body := "Unfortunately, we will not be moving forward with your application."
	for _, test := range []struct {
		name       string
		sender     string
		body       string
		candidates []Candidate
	}{
		{"wrong sender", "notice@example.test", body, []Candidate{base}},
		{"no rejection", "jobs-noreply@linkedin.com", "Your application was received.", []Candidate{base}},
		{"ambiguous match", "jobs-noreply@linkedin.com", body, []Candidate{base, base}},
	} {
		t.Run(test.name, func(t *testing.T) {
			remaining, outcomes := SeparateApplicationOutcomes(subject, test.sender, test.body, test.candidates)
			if len(outcomes) != 0 || len(remaining) != len(test.candidates) {
				t.Fatalf("remaining=%d outcomes=%d", len(remaining), len(outcomes))
			}
		})
	}
}

func TestHandshakePlainCandidateIsSuppressedAfterPartComposition(t *testing.T) {
	t.Parallel()
	plain := Candidate{Title: "Generic Message", EmailSectionKind: "job_alert"}
	embedded := Candidate{Title: "Software Engineer", EmailSectionKind: "embedded_recommendation"}
	remaining, outcomes := SeparateApplicationOutcomes(
		"Application sent to Synthetic AI - here's what's next", handshakeTrackingSender,
		"Your application was sent to Synthetic AI. Apply to similar jobs.", []Candidate{plain, embedded},
	)
	if len(remaining) != 1 || remaining[0] != embedded || len(outcomes) != 1 || outcomes[0].ApplicationStatus != "applied" {
		t.Fatalf("remaining=%+v outcomes=%+v", remaining, outcomes)
	}
}
