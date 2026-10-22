package catalogwrite

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/catalogtransaction"
)

func checkEarlyEdit[T any](t *testing.T, out *EditOutcome[T], e *Error, code string) {
	t.Helper()
	if out == nil || out.Result != nil || e == nil || e.Code != code || out.Write.IntentRecorded || out.CleanupFailed {
		t.Fatalf("early refusal differs: %#v / %#v", out, e)
	}
}
func TestEditEntryConfigurationCancellationAndOutcome(t *testing.T) {
	for _, s := range []*Service{nil, {}, New("", "")} {
		group, e := s.CreateCustomGroup(nil, nil, "private token")
		checkEarlyEdit(t, group, e, "CATALOG_SERVICE_UNAVAILABLE")
		skill, e := s.CreateCustomSkill(nil, nil, "private token")
		checkEarlyEdit(t, skill, e, "CATALOG_SERVICE_UNAVAILABLE")
		skill, e = s.UpdateCustomSkill(nil, "private id", nil, "private token")
		checkEarlyEdit(t, skill, e, "CATALOG_SERVICE_UNAVAILABLE")
		skill, e = s.SetCustomSkillArchived(nil, "private id", true, "private token")
		checkEarlyEdit(t, skill, e, "CATALOG_SERVICE_UNAVAILABLE")
		group, e = s.CreateCustomGroup(nil, map[string]any{"z": 1, "a": 2}, "stale")
		checkEarlyEdit(t, group, e, "SKILL_REGISTRY_INVALID")
		if e.Registry.Message != "Unknown group fields: a, z" {
			t.Fatal("unknown fields not checked first/sorted")
		}
		skill, e = s.CreateCustomSkill(nil, map[string]any{"\xff": 1}, "stale")
		checkEarlyEdit(t, skill, e, "CATALOG_NAME_UNICODE_UNSUPPORTED")
	}
	s := New("", "")
	s.gate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, e := s.UpdateCustomSkill(ctx, "private id", nil, "private token")
	checkEarlyEdit(t, out, e, "CATALOG_OPERATION_CANCELLED")
	<-s.gate
	report := &ArchiveOutcome{Revision: "saved", Write: catalogtransaction.Report{IntentRecorded: true, FilesComplete: true}, CleanupFailed: true}
	e = &Error{Code: "CATALOG_CLOSE_FAILED"}
	result := editOutcome(&SkillResult{}, report, e)
	if result.Result != nil || result.Revision != "saved" || !reflect.DeepEqual(result.Write, report.Write) || !result.CleanupFailed {
		t.Fatal("late failure discarded publication facts or exposed success")
	}
	if strings.Contains(e.Error(), "saved") {
		t.Fatal("error string disclosed private detail")
	}
}
func TestEditInputHelpersAndOrder(t *testing.T) {
	if _, e := editName("\xff", "name"); e == nil || e.Code != "CATALOG_NAME_UNICODE_UNSUPPORTED" {
		t.Fatal("invalid Unicode accepted")
	}
	if got, e := editName("\x1c "+strings.Repeat("😀", 160)+"\u3000", "name"); e != nil || got != strings.Repeat("😀", 160) {
		t.Fatal("scalar/whitespace boundary differs")
	}
	aliases, e := editAliases([]string{" A ", "B"})
	if e != nil || !reflect.DeepEqual(aliases, []string{"A", "B"}) {
		t.Fatal("typed aliases differ")
	}
	if _, e := editAliases([]any{"a", "A", nil}); e == nil || e.Code != "SKILL_REGISTRY_INVALID" || e.Registry.Message != "alias must be a non-empty string" {
		t.Fatal("duplicate check preceded complete cleaning")
	}
	for _, value := range []any{make([]any, cataloginput.YAMLMaxNodes+1), make([]string, cataloginput.YAMLMaxNodes+1)} {
		if _, e := editAliases(value); e == nil || e.Code != "CATALOG_INPUT_LIMIT" {
			t.Fatal("alias limit not refused")
		}
	}
	for _, value := range []any{nil, "", "A", "_a", "1a", "a-b", "a b", "a\u00e9"} {
		if _, e := editGroupID(value); e == nil {
			t.Fatal("invalid group ID accepted")
		}
	}
	if got, e := editGroupID(" a_1 "); e != nil || got != "a_1" {
		t.Fatal("valid group ID refused")
	}
	if got, e := customID("!!!", []string{"user_custom_skill", "user_custom_skill_2"}, "custom_skill"); e != nil || got != "user_custom_skill_3" {
		t.Fatal("fallback suffix differs")
	}
	if got, e := customID("user_Résumé", nil, "unused"); e != nil || got != "user_resume" {
		t.Fatal("ID prefixed twice")
	}
	if _, e := customID("\xff", nil, "unused"); e == nil || e.Code != "CATALOG_ID_UNSUPPORTED" {
		t.Fatal("ID normalization failure hidden")
	}
	if e := editDomainError(errors.New("private detail")); e.Code != "CATALOG_VALIDATION_FAILED" || e.Registry != nil {
		t.Fatal("unknown validation error leaked")
	}
}
func TestEditPlanGuardsAndUTCTimestamp(t *testing.T) {
	files := archiveMemory{
		catalogstore.CoreGroupsPath: []byte("- {id: tools, name: Tools}\n"),
		catalogstore.CoreSkillsPath: []byte("[]\n"),
	}
	state, e := catalogstore.ReadState(files, files)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		ctx   context.Context
		state *catalogstore.State
	}{
		{nil, state}, {context.Background(), nil}, {context.Background(), &catalogstore.State{}},
	} {
		if plan, e := planEdit(tc.ctx, tc.state, editRequest{}); plan != nil || e == nil || e.Code != "CATALOG_INPUT_INVALID" {
			t.Fatal("invalid planner inputs accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if plan, e := planEdit(ctx, state, editRequest{}); plan != nil || e == nil || e.Code != "CATALOG_OPERATION_CANCELLED" {
		t.Fatal("cancelled planning accepted")
	}
	if plan, e := planEdit(context.Background(), state, editRequest{operation: "unknown", revision: state.Snapshot.Revision()}); plan != nil || e == nil || e.Code != "CATALOG_OPERATION_INVALID" {
		t.Fatal("unknown operation accepted")
	}
	request := editRequest{operation: createSkill, revision: state.Snapshot.Revision(), payload: map[string]any{"name": "New", "primary_group_id": "tools"}}
	for _, tc := range []struct {
		now  time.Time
		want string
	}{
		{time.Date(2026, 9, 9, 12, 34, 56, 987654321, time.FixedZone("offset", 3600)), "2026-09-09T11:34:56Z"},
		{time.Date(10000, 1, 1, 0, 0, 0, 0, time.FixedZone("offset", 3600)), "9999-12-31T23:00:00Z"},
		{time.Date(1, 1, 1, 0, 0, 0, 0, time.FixedZone("offset", 3600)), ""},
		{time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), ""},
	} {
		request.now = tc.now
		plan, e := planEdit(context.Background(), state, request)
		if tc.want == "" {
			if plan != nil || e == nil || e.Code != "CATALOG_CLOCK_INVALID" {
				t.Fatal("unsupported UTC date accepted")
			}
		} else if e != nil || plan == nil || plan.skill.Record.CreatedAt == nil || *plan.skill.Record.CreatedAt != tc.want {
			t.Fatalf("UTC timestamp differs: %#v", e)
		}
	}
	if len(files) != 2 {
		t.Fatal("pure planning created files")
	}
}
