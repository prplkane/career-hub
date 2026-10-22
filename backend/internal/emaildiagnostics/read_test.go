package emaildiagnostics

import "testing"

func TestNativeEmailCursorAndSegmentValidation(t *testing.T) {
	for _, cursor := range []string{"", "0", "byte:0", "ne:000001:0", "ne:000008:16777216"} {
		if _, _, _, problem := parseCursor(cursor); problem != nil {
			t.Fatalf("valid cursor %q refused: %v", cursor, problem)
		}
	}
	for _, cursor := range []string{"byte:1", "ne:000000:0", "ne:000001:-1", "ne:1:0", "ne:000001:16777217", "ne:000009:0", "ne:000001:0:extra", "private@example.test"} {
		if _, _, _, problem := parseCursor(cursor); problem == nil || problem.Code != "EMAIL_DIAGNOSTIC_CURSOR_INVALID" {
			t.Fatalf("invalid cursor %q accepted: %v", cursor, problem)
		}
	}
	for _, names := range [][]string{{"segment-000002.jsonl"},
		{"segment-000001.jsonl", "segment-000003.jsonl"},
		{"segment-000001.jsonl", "unexpected.txt"},
		{"segment-000000.jsonl"},
	} {
		if _, err := nextSegment(names); err == nil {
			t.Fatalf("invalid native journal directory accepted: %q", names)
		}
	}
	if name, err := nextSegment([]string{"segment-000002.jsonl", "segment-000001.jsonl"}); err != nil || name != "segment-000002.jsonl" {
		t.Fatalf("valid rotated journal refused: %q %v", name, err)
	}
}
