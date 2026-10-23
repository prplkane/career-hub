package jobcard

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"careerhub.local/backend/internal/skills/cataloginput"
)

func TestEncodeCardPreservesEditedSourceSemantics(t *testing.T) {
	loaded := editableFixture(t)
	card, err := EditDetails(context.Background(), loaded, map[string]any{
		"company": "YAML: [safe]", "title": "true", "required_skills": []any{"Go", "Python"},
		"source_url": "https://example.test/jobs/7?src=mail",
	})
	if err != nil {
		t.Fatal(err)
	}
	report, failure := Validate(context.Background(), card, syntheticCheck)
	if failure != nil || !report.SchemaValid() {
		t.Fatalf("edited card invalid: %#v %#v", report, failure)
	}
	raw, failure := EncodeCard(context.Background(), card)
	if failure != nil || len(raw) == 0 || !bytes.Contains(raw, []byte(`"YAML: [safe]"`)) || !bytes.Contains(raw, []byte(`"true"`)) {
		t.Fatalf("unsafe or empty YAML: %#v", failure)
	}
	parsed, parseErr := cataloginput.ParseYAML(raw)
	if parseErr != nil || !sameCardValue(card, parsed) {
		t.Fatalf("YAML roundtrip changed facts: %#v", parseErr)
	}
	report, failure = Validate(context.Background(), parsed, syntheticCheck)
	if failure != nil || !report.SchemaValid() {
		t.Fatalf("serialized card invalid: %#v %#v", report, failure)
	}
}

func TestEncodeCardBoundsAndUnsupportedValues(t *testing.T) {
	base := editableFixture(t).Card
	for _, tc := range []cataloginput.Value{
		{Kind: "mapping", Entries: []cataloginput.Entry{{Key: stringValue("opaque"), Value: cataloginput.Value{Kind: "unknown"}}}},
		{Kind: "mapping", Entries: []cataloginput.Entry{{Key: stringValue("too_long"), Value: stringValue(strings.Repeat("x", cataloginput.YAMLMaxInputBytes))}}},
	} {
		if raw, err := EncodeCard(context.Background(), tc); err == nil || len(raw) != 0 {
			t.Fatal("invalid YAML emitted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if raw, err := EncodeCard(ctx, base); err == nil || len(raw) != 0 {
		t.Fatal("canceled encode produced output")
	}
	if raw, err := EncodeCard(context.Background(), cataloginput.Value{Kind: "string", Text: "not a card"}); err == nil || len(raw) != 0 {
		t.Fatal("non-mapping card encoded")
	}
}

func TestEncodeCardRetainsUnknownSafeScalars(t *testing.T) {
	card := editableFixture(t).Card
	put(&card, "opaque_date", cataloginput.Value{Kind: "date", Text: "2026-09-14"})
	put(&card, "opaque_bytes", cataloginput.Value{Kind: "bytes", Bytes: []byte{0, 1, 2}})
	raw, err := EncodeCard(context.Background(), card)
	if err != nil {
		t.Fatalf("safe opaque scalars lost: %#v", err)
	}
	parsed, parseErr := cataloginput.ParseYAML(raw)
	if parseErr != nil || !sameCardValue(card, parsed) {
		t.Fatalf("opaque scalars changed: %#v", parseErr)
	}
}
