package catalog

import "testing"

func TestMutationMergeDoesNotReconcileNewConflicts(t *testing.T) {
	core := mergeFixture("core", "Shared")
	custom := mergeFixture("user_one", "Shared")
	merged, e := MergeForMutation([]Term{core}, []Term{custom})
	if e != nil || len(merged) != 2 {
		t.Fatal("id merge silently resolved new name collision")
	}
	if merged[0].ID != "core" || merged[1].ID != "user_one" {
		t.Fatal("source sorted order changed")
	}
	merged[0].Name = "Caller mutation"
	if core.Name != "Shared" {
		t.Fatal("merge aliases source memory")
	}
	if _, e := MergeForMutation([]Term{core, core}, nil); e == nil {
		t.Fatal("duplicate core ids admitted")
	}
	custom.ID = core.ID
	custom.Name = "Private"
	custom.Parent = nil
	custom.FieldsSet = []string{"parent"}
	merged, e = MergeForMutation([]Term{core}, []Term{custom})
	if e != nil || len(merged) != 1 || merged[0].Name != "Private" || len(merged[0].Aliases) != 1 || merged[0].Aliases[0] != "Shared" {
		t.Fatal("id overlay semantics changed")
	}
}
