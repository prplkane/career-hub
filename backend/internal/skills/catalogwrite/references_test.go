package catalogwrite

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/skills/catalogstore"
)

func referenceTestState(t *testing.T) *catalogstore.State {
	t.Helper()
	files := archiveMemory{catalogstore.CoreGroupsPath: []byte("- {id: tools, name: Tools}\n"), catalogstore.CoreSkillsPath: []byte("[]\n")}
	state, e := catalogstore.ReadState(files, files)
	if e != nil {
		t.Fatal(e)
	}
	return state
}
func TestReferencePlanOwnsItsProposal(t *testing.T) {
	state := referenceTestState(t)
	via, stamp := "resume_digest", "2026-09-09T12:34:56Z"
	drafts := []SkillReferenceDraft{{EntityID: "entity", CreatedVia: &via, CreatedAt: &stamp,
		PendingSkills: []PendingSkill{{Name: "First"}, {Name: "Second"}}}}
	p, e := PlanSkillReferences(context.Background(), state, drafts, state.Snapshot.Revision())
	if e != nil {
		t.Fatal(e)
	}
	result, replacements := p.Result(), p.CatalogReplacements()
	wantResult, wantFiles := normalizedJSON(t, result), normalizedJSON(t, replacements)
	drafts[0].EntityID, drafts[0].PendingSkills[0].Name, via, stamp = "changed", "changed", "changed", "changed"
	result.SkillIDsByEntity["entity"][0] = "changed"
	result.SkillNamesByEntity["entity"][0] = "changed"
	*result.CreatedSkills[0].CreatedVia = "changed"
	*result.CreatedSkills[0].CreatedAt = "changed"
	*result.CreatedSkills[0].PrimaryGroupID = "changed"
	result.CreatedSkills[0].FieldsSet[0] = "changed"
	for key, raw := range replacements {
		raw[0] = '!'
		delete(replacements, key)
	}
	if !reflect.DeepEqual(wantResult, normalizedJSON(t, p.Result())) || !reflect.DeepEqual(wantFiles, normalizedJSON(t, p.CatalogReplacements())) {
		t.Fatal("returned mutable proposal aliases the private plan or inputs")
	}
	if len(state.CustomGroups) != 0 || len(state.CustomSkills) != 0 || len(state.RawGroups) != 0 || len(state.RawSkills) != 0 {
		t.Fatal("planning edited captured source state")
	}
}
func TestReferenceInputBoundsAndGuards(t *testing.T) {
	state := referenceTestState(t)
	for _, refs := range [][]SkillReferenceDraft{
		make([]SkillReferenceDraft, cataloginput.YAMLMaxNodes+1),
		{{EntityID: strings.Repeat("x", cataloginput.YAMLMaxInputBytes+1)}},
		{{CompetencyIDs: make([]string, cataloginput.YAMLMaxNodes)}},
		{{PendingSkills: make([]PendingSkill, cataloginput.YAMLMaxNodes)}},
	} {
		p, e := PlanSkillReferences(context.Background(), state, refs, state.Snapshot.Revision())
		if p != nil || e == nil || e.Code != "CATALOG_REFERENCE_INPUT_LIMIT" {
			t.Fatal("reference adapter bound not refused atomically")
		}
	}
	for _, refs := range [][]SkillReferenceDraft{
		{{CompetencyIDs: []string{"\xff"}}},
		{{PendingSkills: []PendingSkill{{Name: "\xff"}}}},
	} {
		if p, e := PlanSkillReferences(context.Background(), state, refs, state.Snapshot.Revision()); p != nil || e == nil || e.Code != "CATALOG_NAME_UNICODE_UNSUPPORTED" {
			t.Fatal("unsupported Unicode misreported as a size limit")
		}
	}
	for _, empty := range []*catalogstore.State{nil, {}} {
		if p, e := PlanSkillReferences(context.Background(), empty, nil, ""); p != nil || e == nil || e.Code != "CATALOG_INPUT_INVALID" {
			t.Fatal("invalid reference state accepted")
		}
		if p, e := planDefaultGroup(context.Background(), empty); p != nil || e == nil || e.Code != "CATALOG_INPUT_INVALID" {
			t.Fatal("invalid default state accepted")
		}
	}
	if p, e := PlanSkillReferences(nil, state, nil, ""); p != nil || e == nil || e.Code != "CATALOG_INPUT_INVALID" {
		t.Fatal("nil context accepted")
	}
	if p, e := planDefaultGroup(nil, state); p != nil || e == nil || e.Code != "CATALOG_INPUT_INVALID" {
		t.Fatal("nil default context accepted")
	}
	if p, e := PlanSkillReferences(context.Background(), state, []SkillReferenceDraft{{CompetencyIDs: []string{"UPPER"}}}, "stale"); p != nil || e == nil || e.Code != "SKILL_REGISTRY_REVISION_CONFLICT" {
		t.Fatal("validation preceded stale revision")
	}
	for _, s := range []*Service{nil, {}, New("", "")} {
		out, e := s.CreateCustomSkills(nil, map[string]any{"names": []string{"First"}, "primary_group_id": "tools"}, "revision")
		checkEarlyEdit(t, out, e, "CATALOG_SERVICE_UNAVAILABLE")
		refs, e := s.CommitRegistrySkillReferences(nil, nil, "revision")
		checkEarlyEdit(t, refs, e, "CATALOG_SERVICE_UNAVAILABLE")
		group, e := s.EnsureDefaultCustomGroup(nil)
		checkEarlyEdit(t, group, e, "CATALOG_SERVICE_UNAVAILABLE")
		out, e = s.CreateCustomSkills(nil, map[string]any{"unknown": 1}, "revision")
		checkEarlyEdit(t, out, e, "SKILL_REGISTRY_INVALID")
	}
}

type referenceCancelAt struct {
	context.Context
	at, calls int
}

func (c *referenceCancelAt) Err() error {
	c.calls++
	if c.at > 0 && c.calls >= c.at {
		return context.Canceled
	}
	return nil
}
func TestReferenceCancellationAtEveryCheckpoint(t *testing.T) {
	state := referenceTestState(t)
	group, via := "tools", "unspecified"
	refs := []SkillReferenceDraft{{EntityID: "first", CreatedVia: &via, PendingSkills: []PendingSkill{{Name: "Alpha", GroupID: &group}, {Name: "Beta"}}},
		{EntityID: "second", CompetencyIDs: []string{"user_alpha"}, PendingSkills: []PendingSkill{{Name: "ALPHA", GroupID: &group}}}}
	count := &referenceCancelAt{Context: context.Background()}
	if p, e := PlanSkillReferences(count, state, refs, state.Snapshot.Revision()); e != nil || p == nil {
		t.Fatal("baseline")
	}
	if count.calls < 8 {
		t.Fatal("checkpoint inventory unexpectedly empty")
	}
	for i := 1; i <= count.calls; i++ {
		ctx := &referenceCancelAt{Context: context.Background(), at: i}
		if p, e := PlanSkillReferences(ctx, state, refs, state.Snapshot.Revision()); p != nil || e == nil || e.Code != "CATALOG_OPERATION_CANCELLED" {
			t.Fatalf("checkpoint %d failed", i)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, e := planDefaultGroup(ctx, state); p != nil || e == nil || e.Code != "CATALOG_OPERATION_CANCELLED" {
		t.Fatal("cancelled default plan accepted")
	}
}
func TestBatchInputAndLinearIDReuse(t *testing.T) {
	for _, payload := range []map[string]any{
		{"names": []string{}}, {"names": []string{" "}, "primary_group_id": "tools"},
		{"names": make([]string, 101)}, {"\xff": true},
	} {
		if _, _, e := cleanBatch(payload); e == nil {
			t.Fatal("invalid batch accepted")
		}
	}
	names, group, e := cleanBatch(map[string]any{"names": []string{" First ", "FIRST"}, "primary_group_id": " tools "})
	if e != nil || !reflect.DeepEqual(names, []string{"First"}) || group != "tools" {
		t.Fatal("typed batch cleaning differs")
	}
	if _, e := batchDraft(names, group, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)); e == nil || e.Code != "CATALOG_CLOCK_INVALID" {
		t.Fatal("invalid batch clock accepted")
	}
	draft, e := batchDraft(names, group, time.Date(2026, 9, 9, 12, 13, 14, 15, time.FixedZone("offset", 3600)))
	if e != nil || *draft.CreatedAt != "2026-09-09T11:13:14Z" || *draft.CreatedVia != "manual_registry" {
		t.Fatal("batch provenance differs")
	}
	used, next := map[string]bool{"user_custom_skill": true, "user_custom_skill_2": true, "user_custom_skill_4": true}, map[string]int{}
	for _, want := range []string{"user_custom_skill_3", "user_custom_skill_5", "user_custom_skill_6"} {
		got, e := referenceID("名字", used, next)
		if e != nil || got != want {
			t.Fatal("incremental suffix allocation skipped a free source ID")
		}
	}
	if _, e := referenceID("\xff", used, next); e == nil {
		t.Fatal("invalid ID text accepted")
	}
}
