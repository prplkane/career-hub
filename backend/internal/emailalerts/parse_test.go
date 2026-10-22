package emailalerts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/emailmime"
)

func canonicalResultJSON(t *testing.T, value any) []byte {
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

func resultDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func TestPythonSourceEnvelopeCases(t *testing.T) {
	t.Parallel()
	fixtures := filepath.Join("..", "..", "..", "tests", "fixtures")
	raw, err := os.ReadFile(filepath.Join(fixtures, "migration_email_envelope_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Cases []struct {
			Name         string   `json:"name"`
			EML          string   `json:"eml"`
			Raw          string   `json:"raw"`
			Sender       string   `json:"sender"`
			Subject      string   `json:"subject"`
			ReceivedAt   string   `json:"received_at"`
			WarningCodes []string `json:"warning_codes"`
			ResultSHA256 string   `json:"result_sha256"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	for _, test := range suite.Cases {
		t.Run(test.Name, func(t *testing.T) {
			content := []byte(test.Raw)
			if test.EML != "" {
				var err error
				content, err = os.ReadFile(filepath.Join(fixtures, "email_alerts", test.EML))
				if err != nil {
					t.Fatal(err)
				}
			}
			result, err := Parse(content)
			if err != nil {
				t.Fatal(err)
			}
			if result.Sender.Address != test.Sender || result.Subject != test.Subject || result.ReceivedAt != test.ReceivedAt {
				t.Errorf("metadata: sender=%q subject=%q received=%q", result.Sender.Address, result.Subject, result.ReceivedAt)
			}
			var codes []string
			for _, warning := range result.Warnings {
				codes = append(codes, warning.Code)
			}
			if codes == nil {
				codes = []string{}
			}
			if !reflect.DeepEqual(codes, test.WarningCodes) {
				t.Errorf("warnings=%q, want %q", codes, test.WarningCodes)
			}
			serialized := canonicalResultJSON(t, result)
			if actual := resultDigest(serialized); actual != test.ResultSHA256 {
				t.Errorf("result sha256=%s, want %s; json=%s", actual, test.ResultSHA256, serialized)
			}
			if bytes.Contains(serialized, []byte("<encoded-1@example.test>")) || bytes.Contains(serialized, []byte("<single-123@example.test>")) {
				t.Fatal("raw message ID leaked")
			}
		})
	}
}

func TestEnvelopeInputBounds(t *testing.T) {
	t.Parallel()
	if _, err := Parse(nil); !errors.Is(err, emailmime.ErrEmpty) {
		t.Fatalf("empty input: %v", err)
	}
	tooLarge := []byte(strings.Repeat("x", emailmime.MaxEmailBytes+1))
	if _, err := Parse(tooLarge); !errors.Is(err, emailmime.ErrTooLarge) {
		t.Fatalf("large input: %v", err)
	}
}
