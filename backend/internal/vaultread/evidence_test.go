package vaultread

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/cataloginput"
)

func fixtureValue(t *testing.T, raw string) cataloginput.Value {
	t.Helper()
	value, failure := cataloginput.ParseYAML([]byte(raw))
	if failure != nil {
		t.Fatal("synthetic YAML fixture was not admitted")
	}
	return value
}

func TestEvidenceCategoriesKeepExactNamesWithStableFoldTies(t *testing.T) {
	records := fixtureValue(t, `[
{category: data}, {category: ' Data '}, {category: technical},
{category: SS}, {category: ss}, {category: ß}, {category: data},
{category: false}, {category: 0}, {category: []}, {category: {}},
{category: '   '}, {category: true}, {category: [one, null]},
false, {}, {category: null}
]`)
	before, _ := json.Marshal(records)
	encoder := profileEncoder{ctx: context.Background(), source: "experiences"}
	encoder.categories(records)
	payload, failure := encoder.result()
	if failure != nil {
		t.Fatal("valid categories failed")
	}
	var got []string
	if json.Unmarshal(payload, &got) != nil || !reflect.DeepEqual(got, []string{
		"['one', None]", "Data", "data", "SS", "ss", "ß", "technical", "True",
	}) {
		t.Fatalf("synthetic category presence/order changed: %q", got)
	}
	after, _ := json.Marshal(records)
	if string(before) != string(after) {
		t.Fatal("category display mutated source records")
	}
}

func TestEvidenceOptionsKeepSourceTruthinessLabelsAndExactArchiveFlag(t *testing.T) {
	records := fixtureValue(t, `[
{id: exp, company: '  Synthetic  ', role: ' Engineer ', archived: true},
{id: false, company: Ignored}, {id: 0}, {id: []}, {id: {}}, false, {},
{id: '  ', company: false, role: []},
{id: 123456789012345678901234567890, company: 0, role: {}, archived: 1},
{id: ['nested'], company: [false, {true: ' a '}], role: 12.5}
]`)
	encoder := profileEncoder{ctx: context.Background(), source: "projects"}
	encoder.experienceOptions(records)
	payload, failure := encoder.result()
	if failure != nil || string(payload) != `[{"id":"exp","company":"  Synthetic  ","role":" Engineer ","archived":true},{"id":"  ","company":false,"role":[],"archived":false},{"id":123456789012345678901234567890,"company":0,"role":{},"archived":false},{"id":["nested"],"company":[false,{"true":" a "}],"role":12.5,"archived":false}]` {
		t.Fatal("raw reference values or exact archive flag changed")
	}
	encoder = profileEncoder{ctx: context.Background(), source: "legacy_skills"}
	encoder.referenceOptions(records, "company", "role")
	payload, failure = encoder.result()
	if failure != nil || string(payload) != `[{"id":"exp","label":"Synthetic / Engineer","archived":true},{"id":"  ","label":"  ","archived":false},{"id":123456789012345678901234567890,"label":"123456789012345678901234567890","archived":false},{"id":["nested"],"label":"[False, {True: ' a '}] / 12.5","archived":false}]` {
		t.Fatal("Python display labels, fallback or archived options changed")
	}
}

func TestEvidenceReadersCompareAllAdmittedFixturesOffline(t *testing.T) {
	for _, tc := range []struct {
		view string
		read func(context.Context, *vaultState) (json.RawMessage, *Error)
	}{
		{"experiences", experiencesPayload}, {"projects", projectsPayload},
		{"skills", skillsPayload}, {"snapshot", snapshotPayload},
	} {
		for _, sample := range evidenceCases(tc.view) {
			t.Run(tc.view+"/"+sample.name, func(t *testing.T) {
				f := validFixture()
				sample.edit(f)
				state := fixtureState(t, f)
				before, _ := json.Marshal(state.documents)
				revisions := state.revisions
				payload, failure := tc.read(context.Background(), state)
				code := ""
				if failure != nil {
					code = failure.Code
				}
				if code != sample.code || failure == nil && !json.Valid(payload) || failure != nil && payload != nil {
					t.Fatalf("synthetic projection classification: got %s want %s", code, sample.code)
				}
				if after, _ := json.Marshal(state.documents); string(before) != string(after) || revisions != state.revisions {
					t.Fatal("projection changed state")
				}
			})
		}
	}
}

func TestSnapshotUsesOneReadAndDoesNotApplyStandaloneProfileContactCheck(t *testing.T) {
	f := validFixture()
	appendYAML(profileIndex, "contact: null\n")(f)
	state := fixtureState(t, f)
	before, _ := json.Marshal(state.documents)
	payload, failure := snapshotPayload(context.Background(), state)
	if failure != nil || len(f.reads) != vaultFileCount {
		t.Fatal("snapshot reread files, failed or inherited standalone contact check")
	}
	var projections map[string]json.RawMessage
	if json.Unmarshal(payload, &projections) != nil || len(projections) != 5 {
		t.Fatal("snapshot projection membership changed")
	}
	for _, tc := range []struct {
		name  string
		index int
		read  func(context.Context, *vaultState) (json.RawMessage, *Error)
	}{
		{"experiences", experienceIndex, experiencesPayload},
		{"projects", projectIndex, projectsPayload},
		{"skills", skillIndex, skillsPayload},
		{"education", educationIndex, educationPayload},
	} {
		standalone, failure := tc.read(context.Background(), state)
		if failure != nil || string(standalone) != string(projections[tc.name]) ||
			!strings.Contains(string(standalone), state.revisions[tc.index]) {
			t.Fatal("snapshot diverged from standalone projection/revision")
		}
	}
	after, _ := json.Marshal(state.documents)
	if string(before) != string(after) || !strings.Contains(string(projections["profile"]), `"contact":null`) {
		t.Fatal("snapshot normalized records or discarded contact:null")
	}
}

func TestEvidenceProjectionLimitsAndCancellationAreAllOrNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*profileEncoder)
		code string
	}{
		{"category_budget", func(e *profileEncoder) {
			item := fixtureValue(t, "{category: synthetic}")
			item.Entries[0].Value.Text = strings.Repeat("a", MaxProfileBytes/2+1)
			e.categories(cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{item}})
		}, "VAULT_PROJECTION_LIMIT"},
		{"label_budget", func(e *profileEncoder) {
			large := cataloginput.Value{Kind: "string", Text: strings.Repeat("a", MaxProfileBytes/2)}
			item := cataloginput.Value{Kind: "mapping", Entries: []cataloginput.Entry{
				{Key: cataloginput.Value{Kind: "string", Text: "id"}, Value: cataloginput.Value{Kind: "string", Text: "id"}},
				{Key: cataloginput.Value{Kind: "string", Text: "company"}, Value: large},
				{Key: cataloginput.Value{Kind: "string", Text: "role"}, Value: large},
			}}
			e.referenceOptions(cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{item}}, "company", "role")
		}, "VAULT_PROJECTION_LIMIT"},
		{"failed_display", func(e *profileEncoder) {
			e.display(cataloginput.Value{Kind: "unsupported"})
		}, "VAULT_PROJECTION_UNSUPPORTED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoder := profileEncoder{ctx: context.Background(), source: "synthetic"}
			tc.run(&encoder)
			payload, failure := encoder.result()
			if payload != nil || failure == nil || failure.Code != tc.code || len(encoder.output) > MaxProfileBytes {
				t.Fatal("projection bound returned partial data or wrong error")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, read := range []func(context.Context, *vaultState) (json.RawMessage, *Error){
		experiencesPayload, projectsPayload, skillsPayload, snapshotPayload,
	} {
		payload, failure := read(ctx, fixtureState(t, validFixture()))
		if payload != nil || failure == nil || failure.Code != "VAULT_READ_CANCELED" {
			t.Fatal("canceled projection returned data")
		}
	}
}
