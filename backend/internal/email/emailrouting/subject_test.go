package emailrouting

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPythonSourceCases(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "tests", "fixtures", "migration_email_subject_routing_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Normalization []struct {
			Raw  string `json:"raw"`
			Want string `json:"want"`
		} `json:"normalization"`
		Policies []struct {
			Name      string `json:"name"`
			Raw       string `json:"raw"`
			Valid     bool   `json:"valid"`
			Canonical string `json:"canonical"`
		} `json:"policies"`
		Routes []struct {
			Policy  string `json:"policy"`
			Purpose string `json:"purpose"`
			Subject string `json:"subject"`
			Want    string `json:"want"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, test := range fixture.Normalization {
		if got := NormalizeSignal(test.Raw); got != test.Want {
			t.Errorf("NormalizeSignal(%q) = %q, want %q", test.Raw, got, test.Want)
		}
	}
	policies := make(map[string]Policy)
	for _, test := range fixture.Policies {
		t.Run(test.Name, func(t *testing.T) {
			policy, err := ParsePolicyJSON(test.Raw)
			if !test.Valid {
				if !errors.Is(err, ErrInvalidPolicy) {
					t.Fatalf("invalid policy error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := policy.CanonicalJSON(); got != test.Canonical {
				t.Errorf("canonical = %s, want %s", got, test.Canonical)
			}
			policies[test.Name] = policy
		})
	}
	for _, test := range fixture.Routes {
		policy, ok := policies[test.Policy]
		if !ok {
			t.Fatalf("unknown fixture policy %q", test.Policy)
		}
		if got := RoutedPurpose(test.Purpose, policy, test.Subject); got != test.Want {
			t.Errorf("route %q/%q/%q = %q, want %q", test.Policy, test.Purpose, test.Subject, got, test.Want)
		}
	}
}

func TestPhraseBoundaries(t *testing.T) {
	t.Parallel()
	parse := func(phrases []string) (Policy, error) {
		raw, err := json.Marshal(map[string]any{
			SenderPurposeJobAlerts: map[string]any{"include_any": phrases, "exclude_any": []string{}},
			SenderPurposeApplicationTracking: map[string]any{
				"include_any": []string{}, "exclude_any": []string{},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return ParsePolicyJSON(string(raw))
	}
	phrases := make([]string, 20)
	for i := range phrases {
		phrases[i] = "phrase " + string(rune('a'+i))
	}
	if _, err := parse(phrases); err != nil {
		t.Fatal(err)
	}
	if _, err := parse(append(phrases, "phrase z")); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("21 phrases: %v", err)
	}
	if _, err := parse([]string{strings.Repeat("ß", 60)}); err != nil {
		t.Fatal(err)
	}
	if _, err := parse([]string{strings.Repeat("ß", 61)}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("121+ normalized characters: %v", err)
	}
	policy, err := parse([]string{"New-Jobs", "new jobs"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(policy.CanonicalJSON(), `"include_any":["new jobs"]`) {
		t.Fatal("normalized duplicate was not removed")
	}
}
