package intakeextract

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/intakeread"
	"careerhub.local/backend/internal/skills/resolver"
)

type corpusCase struct {
	Name     string          `json:"name"`
	Format   string          `json:"format"`
	Text     string          `json:"text"`
	Terms    []resolver.Term `json:"terms"`
	Expected map[string]any  `json:"expected"`
}

func loadCorpus(t *testing.T) []corpusCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "migration_resume_extractor_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SchemaVersion int          `json:"schema_version"`
		Cases         []corpusCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || fixture.SchemaVersion != 1 || len(fixture.Cases) != 9 {
		t.Fatal("invalid fictional source corpus")
	}
	return fixture.Cases
}

func TestBuildMatchesFictionalSourceCorpus(t *testing.T) {
	for _, sample := range loadCorpus(t) {
		t.Run(sample.Name, func(t *testing.T) {
			var blocks []Block
			if err := decodeJSON(sample.Expected["blocks"], &blocks); err != nil {
				t.Fatal(err)
			}
			if sample.Format == "txt" {
				got, err := ParseTXTBlocks([]byte(sample.Text))
				if err != nil || !reflect.DeepEqual(got, blocks) {
					t.Fatalf("TXT block mismatch: %v\nwant %#v\ngot %#v", err, blocks, got)
				}
				digest := sha256.Sum256([]byte(sample.Text))
				fromBytes, err := Extract(context.Background(), []byte(sample.Text), Source{"fixture", "fictional", "txt", fmt.Sprintf("%x", digest)}, "revision_fixture", sample.Terms)
				if err != nil || fromBytes.RegistryRevision != "revision_fixture" || !reflect.DeepEqual(fromBytes.Blocks, blocks) {
					t.Fatalf("pure byte-to-report TXT boundary failed: %v", err)
				}
			}
			report, err := Build(context.Background(), Source{"fixture", "fictional", sample.Format, strings.Repeat("a", 64)}, "", blocks, sample.Terms)
			if err != nil {
				t.Fatal(err)
			}
			serialized, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			if _, invalid := intakeread.ParseExtraction(serialized); invalid != nil {
				t.Fatal("generated report rejected by native strict extraction reader")
			}

			var actual map[string]any
			if err := decodeJSON(report, &actual); err != nil {
				t.Fatal(err)
			}
			actual["source"] = map[string]any{"file_type": sample.Format}
			want := sample.Expected
			// The native registered-literal matcher has reviewed corrections
			// versus spaCy. Its version and mention label differ explicitly;
			// recognition/quotes/offsets and every other field remain compared.
			if want["extractor_version"] != "vault-intake-0.2" {
				t.Fatal("source version fixture drift")
			}
			want["extractor_version"] = ExtractorVersion
			for _, raw := range want["section_hints"].([]any) {
				hint := raw.(map[string]any)
				if hint["extractor"] != "spacy_entity_ruler" {
					t.Fatal("source section extractor fixture drift")
				}
				hint["extractor"] = "registered_section_v1"
			}
			for _, raw := range want["technology_mentions"].([]any) {
				mention := raw.(map[string]any)
				if mention["extractor"] != "spacy_phrase_matcher" {
					t.Fatal("source extractor fixture drift")
				}
				mention["extractor"] = "registered_literal_v1"
			}
			if !reflect.DeepEqual(actual, want) {
				t.Fatalf("source report mismatch\nwant %#v\ngot %#v", want, actual)
			}
		})
	}
}

func TestNativeExtractorVersionDoesNotSilentlyBecomeCurrent(t *testing.T) {
	if got := intakeread.DigestStatus(true, "completed", "same", "same", ExtractorVersion, "vault-intake-0.2"); got != "outdated" {
		t.Fatalf("preview reader must not treat an unpublished native report as current: %s", got)
	}
}

func decodeJSON(value any, target any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func TestTXTBoundariesAndNoPartialReport(t *testing.T) {
	for input, expected := range map[string]int{
		"": 0, "a\n": 1, "a\r\nb": 2, "a\n\nb": 3, "\xef\xbb\xbf• one\n": 1,
	} {
		blocks, err := ParseTXTBlocks([]byte(input))
		if err != nil || len(blocks) != expected {
			t.Fatalf("splitlines boundary failed: %q: %v", input, err)
		}
	}
	if blocks, err := ParseTXTBlocks([]byte{0xff}); blocks != nil || !errors.Is(err, ErrInput) {
		t.Fatal("invalid UTF-8 accepted")
	}
	if blocks, err := ParseTXTBlocks([]byte{'a', 0, 'b'}); blocks != nil || !errors.Is(err, ErrInput) {
		t.Fatal("NUL source accepted")
	}
	if blocks, err := ParseTXTBlocks([]byte(strings.Repeat("x", 10<<20+1))); blocks != nil || !errors.Is(err, ErrLimit) {
		t.Fatal("oversized source accepted")
	}
	source := Source{"fixture", "fictional", "txt", strings.Repeat("a", 64)}
	if report, err := Build(context.Background(), source, "", []Block{{ID: "block_0000", Order: 0, Kind: "paragraph", Text: "Python"}, {ID: "block_0001", Order: 1, Kind: "paragraph", Text: "\xff"}}, []resolver.Term{{ID: "python", Name: "Python"}}); report != nil || !errors.Is(err, ErrInput) {
		t.Fatal("late invalid block published a partial report")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if report, err := Build(ctx, source, "", nil, nil); report != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled build published a report")
	}
}

func TestCandidateLimitsRefuseCompleteReport(t *testing.T) {
	source := Source{"fixture", "fictional", "txt", strings.Repeat("a", 64)}
	if report, err := Build(context.Background(), source, "", []Block{{ID: "block_0000", Order: 0, Kind: "paragraph", Text: strings.Repeat("Jan 2024 - Present ", maxCandidates+1)}}, nil); report != nil || !errors.Is(err, ErrLimit) {
		t.Fatal("excessive date candidates published a report", err)
	}
	blocks := make([]Block, maxCandidates+1)
	for i := range blocks {
		blocks[i] = Block{ID: blockID(i), Order: i, Kind: "paragraph", Text: "- Fictional bullet", IsBullet: true}
	}
	if report, err := Build(context.Background(), source, "", blocks, nil); report != nil || !errors.Is(err, ErrLimit) {
		t.Fatal("excessive bullet candidates published a report", err)
	}
}

func TestExtractRefusesMismatchedSourceBytes(t *testing.T) {
	source := Source{"fixture", "fictional", "txt", strings.Repeat("0", 64)}
	if report, err := Extract(context.Background(), []byte("Fictional Python"), source, "revision_fixture", nil); report != nil || !errors.Is(err, ErrInput) {
		t.Fatal("mismatched source bytes yielded a report")
	}
	if report, err := Extract(context.Background(), nil, source, "revision_fixture", nil); report != nil || !errors.Is(err, ErrInput) {
		t.Fatal("empty source bytes yielded a report")
	}
}
