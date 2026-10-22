package emailheaders

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPythonSourceHeaderMetadata(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", "..", "tests", "fixtures", "migration_email_header_metadata_cases.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Cases []struct {
			Name string `json:"name"`
			Raw  string `json:"raw"`
		} `json:"cases"`
		Expected map[string]string `json:"expected_sha256"`
	}
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	if len(suite.Cases) != len(suite.Expected) {
		t.Fatal("missing or duplicate source cases")
	}
	for _, test := range suite.Cases {
		t.Run(test.Name, func(t *testing.T) {
			result, err := Parse([]byte(test.Raw))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var mapped map[string]any
			if err := json.Unmarshal(encoded, &mapped); err != nil {
				t.Fatal(err)
			}
			canonical, err := json.Marshal(mapped)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(canonical)
			if got, want := hex.EncodeToString(digest[:]), suite.Expected[test.Name]; got != want {
				t.Errorf("metadata hash = %s, want %s; result = %s", got, want, canonical)
			}
			if strings.Contains(string(canonical), "<job-1@example.test>") {
				t.Fatal("raw Message-ID leaked")
			}
		})
	}
}

func TestHeaderInputBoundAndSafeError(t *testing.T) {
	t.Parallel()
	for _, raw := range [][]byte{
		[]byte("Private mailbox text"),
		[]byte("X: " + strings.Repeat("x", MaxHeaderBytes)),
	} {
		_, err := Parse(raw)
		if !errors.Is(err, ErrProtocol) || err.Error() != "EMAIL_SYNC_PROTOCOL_FAILED" {
			t.Fatalf("unsafe or unexpected error: %v", err)
		}
	}
}
