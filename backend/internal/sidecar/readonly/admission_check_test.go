package readonly

import (
	"reflect"
	"testing"

	"careerhub.local/backend/internal/datahome"
)

func TestCheckAdmissionRetainsWorkspaceIdentityWithoutWrites(t *testing.T) {
	for _, test := range []struct{ name, path, content, code string }{
		{name: "unchanged"},
		{"identity", datahome.MarkerPath, marker("dev", "dh_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), "DATA_HOME_IDENTITY_CHANGED"},
		{"channel", datahome.MarkerPath, marker("stable", fixtureID), "DATA_HOME_CHANNEL_MISMATCH"},
		{"recovery", datahome.RestoreRecoveryPath, "synthetic recovery", "DATA_HOME_RECOVERY_REQUIRED"},
		{"resource", "VERSION", "invalid", "SIDECAR_RESOURCE_INVALID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			base, start := fixture(t)
			w, err := Open(start)
			if err != nil {
				t.Fatal(err)
			}
			if test.path != "" {
				root := start.DataRoot
				if test.name == "resource" {
					root = start.ResourceRoot
				}
				writeFixture(t, root, test.path, test.content)
			}
			before := tree(t, base)
			err = w.CheckAdmission()
			if test.code == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				requireCode(t, err, test.code)
			}
			if !reflect.DeepEqual(before, tree(t, base)) {
				t.Fatal("admission check changed files")
			}
		})
	}
	for _, w := range []*Workspace{nil, {}} {
		requireCode(t, w.CheckAdmission(), "DATA_HOME_UNAVAILABLE")
	}
}
