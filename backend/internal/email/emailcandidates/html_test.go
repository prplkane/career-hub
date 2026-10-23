package emailcandidates

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/email/emailmime"
)

func TestPythonSourceGenericHTMLCases(t *testing.T) {
	t.Parallel()
	fixtures := filepath.Join("..", "..", "..", "..", "tests", "fixtures")
	raw, err := os.ReadFile(filepath.Join(fixtures, "migration_email_html_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Cases []struct {
			Name          string      `json:"name"`
			EML           string      `json:"eml"`
			HTML          string      `json:"html"`
			Ignored       int         `json:"ignored"`
			AnchorCount   int         `json:"anchor_count"`
			RelationCount int         `json:"relation_count"`
			VisibleText   string      `json:"visible_text"`
			Candidates    []Candidate `json:"candidates"`
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
			got, err := ExtractHTMLGeneric(htmlText, MaxHTMLLinks, MaxHTMLRelationReferences)
			if err != nil {
				t.Fatal(err)
			}
			if got.IgnoredLinks != test.Ignored || got.AnchorCount != test.AnchorCount ||
				got.RelationReferenceCount != test.RelationCount || got.VisibleText != test.VisibleText {
				t.Errorf("metadata = ignored=%d anchors=%d relations=%d visible=%q", got.IgnoredLinks,
					got.AnchorCount, got.RelationReferenceCount, got.VisibleText)
			}
			if !reflect.DeepEqual(got.Candidates, test.Candidates) {
				t.Errorf("candidates = %#v, want %#v", got.Candidates, test.Candidates)
			}
		})
	}
}

func TestHTMLBudgetsFailClosed(t *testing.T) {
	t.Parallel()
	links := strings.Repeat(`<a href="https://jobs.example.test/job/1">Engineer</a>`, MaxHTMLLinks+1)
	if _, err := ExtractHTMLGeneric(links, MaxHTMLLinks, MaxHTMLRelationReferences); !errors.Is(err, ErrHTMLLinkLimit) {
		t.Fatalf("link budget: %v", err)
	}
	deep := strings.Repeat("<div>", 225) + strings.Repeat("<br>", 300)
	if _, err := ExtractHTMLGeneric(deep, MaxHTMLLinks, MaxHTMLRelationReferences); !errors.Is(err, ErrHTMLComplexity) {
		t.Fatalf("relation budget: %v", err)
	}
	if _, err := ExtractHTMLGeneric(strings.Repeat("x", maxHTMLTextChars+1), MaxHTMLLinks, MaxHTMLRelationReferences); !errors.Is(err, ErrHTMLTextLimit) {
		t.Fatalf("text budget: %v", err)
	}
}

func TestHiddenLinksDoNotConsumeLinkBudget(t *testing.T) {
	t.Parallel()
	markup := `<div hidden><a href="https://jobs.example.test/job/1">Hidden</a></div><a href="https://jobs.example.test/job/2">Engineer</a>`
	got, err := ExtractHTMLGeneric(markup, 1, MaxHTMLRelationReferences)
	if err != nil || got.AnchorCount != 1 || len(got.Candidates) != 1 || got.Candidates[0].Title != "Engineer" {
		t.Fatalf("hidden link result: %+v, %v", got, err)
	}
}

func TestNonJobHTMLDoesNotProduceCandidates(t *testing.T) {
	t.Parallel()
	markup := `<a href="https://example.test/about">Apply</a><a href="https://example.test/account">Account</a>`
	got, err := ExtractHTMLGeneric(markup, MaxHTMLLinks, MaxHTMLRelationReferences)
	if err != nil || got.AnchorCount != 2 || got.IgnoredLinks != 2 || len(got.Candidates) != 0 {
		t.Fatalf("non-job result: %+v, %v", got, err)
	}
}
