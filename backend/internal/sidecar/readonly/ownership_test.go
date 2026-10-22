package readonly

import (
	"reflect"
	"testing"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
)

func TestOwnershipPrecedesPrivateAdmissionAndSanitizesFailures(t *testing.T) {
	for _, test := range []struct{ native, public string }{
		{"OWNERSHIP_IN_USE", "DATA_HOME_IN_USE"},
		{"OWNERSHIP_UNSUPPORTED", "DATA_HOME_OWNERSHIP_UNSUPPORTED"},
		{"OWNERSHIP_UNAVAILABLE", "DATA_HOME_OWNERSHIP_UNAVAILABLE"},
		{"private native cause", "DATA_HOME_OWNERSHIP_UNAVAILABLE"},
	} {
		base, start := fixture(t)
		writeFixture(t, start.DataRoot, datahome.MarkerPath, "invalid private marker")
		before := tree(t, base)
		called := false
		w, err := open(start, func(root *runtimepaths.Root) *runtimepaths.Error {
			called = true
			if root == nil {
				t.Fatal("claim did not receive checked root")
			}
			return &runtimepaths.Error{Code: test.native}
		})
		requireCode(t, err, test.public)
		if w != nil || !called || !reflect.DeepEqual(before, tree(t, base)) {
			t.Fatal("failed ownership admitted a workspace or changed files")
		}
	}
}

func TestOwnershipOnlyFollowsValidResourceBoundary(t *testing.T) {
	_, start := fixture(t)
	writeFixture(t, start.ResourceRoot, "VERSION", "invalid")
	w, err := open(start, func(*runtimepaths.Root) *runtimepaths.Error {
		t.Fatal("invalid resources acquired process ownership")
		return nil
	})
	if w != nil || err == nil {
		t.Fatal("invalid resource version admitted")
	}
}
