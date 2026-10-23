package catalogstore

import (
	"testing"

	"careerhub.local/backend/internal/skills/catalog"
)

func TestReadStateRetainsExactPrivateInputsNotEffectiveGroups(t *testing.T) {
	trace := []string{}
	r := &fakeReader{trace: &trace, files: map[string][]byte{
		CoreGroupsPath:   []byte("- {id: core, name: Shared}\n"),
		CustomGroupsPath: []byte("# raw private\r\n- {id: user_one, name: Shared}\r\n"),
		CustomSkillsPath: []byte("[]\r\n"),
		CoreSkillsPath:   []byte("- {id: core_skill, name: Core, category: tool, primary_group_id: core}\n"),
	}}
	state, err := loadState(r, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.CustomGroups) != 1 || state.CustomGroups[0].ID != "user_one" || len(state.Snapshot.Groups()) != 2 {
		t.Fatal("raw and effective models conflated")
	}
	if state.Snapshot.Revision() != catalog.RawRevision(state.RawGroups, state.RawSkills) {
		t.Fatal("revision uses different bytes")
	}
	state.RawGroups[0] = '!'
	state.CustomGroups[0].Name = "Changed caller-owned projection"
	if r.files[CustomGroupsPath][0] != '#' || state.Snapshot.Groups()[1].Name != "Shared" {
		t.Fatal("state does not own independent inputs")
	}
	state.CoreGroups[0].Name = "Mutated core projection"
	state.CoreSkills[0].Name = "Mutated core skill projection"
	*state.CoreSkills[0].PrimaryGroupID = "other"
	if state.Snapshot.Skills()[0].Name != "Core" || *state.Snapshot.Skills()[0].PrimaryGroupID != "core" {
		t.Fatal("new raw core projection aliases immutable snapshot")
	}
}
