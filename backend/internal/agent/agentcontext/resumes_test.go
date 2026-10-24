package agentcontext

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type resumeCase struct {
	Initialized    bool             `json:"initialized"`
	Library        []map[string]any `json:"library"`
	Application    map[string]any   `json:"application"`
	LibraryFailure *string          `json:"library_failure"`
	DigestFailure  *string          `json:"digest_failure"`
	IncludeLibrary bool             `json:"include_library"`
	IncludeDigest  bool             `json:"include_digest"`
	Target         *string          `json:"target"`
}

func stringPointer(value string) *string { return &value }
func baseResumeCase() resumeCase {
	rows := []map[string]any{}
	for i := 0; i < 12; i++ {
		rows = append(rows, map[string]any{"id": fmt.Sprintf("r%d", i), "display_name": fmt.Sprintf("Resume %d 😀", i)})
	}
	return resumeCase{Initialized: true, Library: rows, IncludeLibrary: true}
}
func applicationResume(id any) map[string]any {
	return map[string]any{"resume_selection": map[string]any{"resume": map[string]any{"id": id}}}
}

func TestResumeContextFailureGuards(t *testing.T) {
	c := baseResumeCase()
	input := ResumeContextInput{Initialized: true, Library: jsonFixture(t, c.Library), IncludeLibrary: true, IncludeDigest: true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if value, e := BuildResumeContext(ctx, input, nil); value != nil || e == nil {
		t.Fatal("canceled")
	}
	if value, e := BuildResumeContext(context.Background(), input, nil); value != nil || e == nil || e.Code != "AGENT_CONTEXT_RESUME_DIGEST_READER_REQUIRED" {
		t.Fatal("missing digest reader")
	}
	for _, bad := range []string{`[]`, `[{"id":"foreign"}]`, `null`, `{}`, `[{"id":null}]`} {
		callback := func(context.Context, json.RawMessage) (json.RawMessage, *string, *Error) {
			return json.RawMessage(bad), nil, nil
		}
		if value, e := BuildResumeContext(context.Background(), input, callback); value != nil || e == nil {
			t.Fatal("digest identity/framing")
		}
	}
	for _, mode := range []string{"reorder", "foreign"} {
		callback := func(_ context.Context, raw json.RawMessage) (json.RawMessage, *string, *Error) {
			var rows []map[string]any
			_ = decodeNumbers(raw, &rows)
			if mode == "reorder" {
				rows[0], rows[1] = rows[1], rows[0]
			} else {
				rows[0]["id"] = "foreign"
			}
			return jsonFixture(t, rows), nil, nil
		}
		if value, e := BuildResumeContext(context.Background(), input, callback); value != nil || e == nil || e.Code != "AGENT_CONTEXT_RESUME_DIGEST_IDENTITY" {
			t.Fatal("same-size digest identity drift")
		}
	}
	callback := func(context.Context, json.RawMessage) (json.RawMessage, *string, *Error) {
		return nil, nil, failure("SNAPSHOT_CHANGED")
	}
	if value, e := BuildResumeContext(context.Background(), input, callback); value != nil || e == nil || e.Code != "AGENT_CONTEXT_SNAPSHOT_CHANGED" {
		t.Fatal("fatal snapshot failure masked")
	}
	input.IncludeDigest = false
	input.Library = json.RawMessage(strings.Repeat(" ", 9<<20))
	if value, e := BuildResumeContext(context.Background(), input, nil); value != nil || e == nil {
		t.Fatal("library bound")
	}
	input.Library = json.RawMessage(`[{"id":{},"display_name":"invalid storage class"}]`)
	if value, e := BuildResumeContext(context.Background(), input, nil); value != nil || e == nil {
		t.Fatal("unsupported ID representation")
	}
	input.Library = json.RawMessage(`[null]`)
	if value, e := BuildResumeContext(context.Background(), input, nil); value != nil || e == nil {
		t.Fatal("null row")
	}
	input.Initialized = false
	if value, e := BuildResumeContext(context.Background(), input, nil); value == nil || e != nil || value.Warning == nil {
		t.Fatal("early missing-library branch")
	}
}

func TestResumeTargetEqualToSelectionKeepsFirstPriority(t *testing.T) {
	c := baseResumeCase()
	for _, includeLibrary := range []bool{false, true} {
		result, err := BuildResumeContext(context.Background(), ResumeContextInput{
			Initialized: true, Library: jsonFixture(t, c.Library), Application: jsonFixture(t, applicationResume("r11")),
			IncludeLibrary: includeLibrary, TargetID: stringPointer("r11"),
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		expected := []string{"r11"}
		if includeLibrary {
			for i := 0; i < 7; i++ {
				expected = append(expected, fmt.Sprintf("r%d", i))
			}
		}
		actual := []string{}
		for _, item := range result.Items {
			actual = append(actual, item["id"].(string))
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("library=%v target lost priority: got %v want %v", includeLibrary, actual, expected)
		}
	}
}
