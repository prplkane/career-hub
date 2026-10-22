package readonly

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/vaultread"
)

func appendFixtureComment(t *testing.T, root, relative string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal("synthetic source unavailable")
	}
	writeFixture(t, root, relative, string(raw)+"# changed synthetic revision\n")
}

func TestVaultInvalidViewDoesNotAdmitWorkspace(t *testing.T) {
	for _, view := range []vaultread.View{0, vaultread.SetupView + 1, 255} {
		for _, w := range []*Workspace{nil, {dataHomeID: fixtureID}} {
			payload, err := w.Vault(context.Background(), view)
			requireVaultCode(t, err, "VAULT_VIEW_INVALID")
			if payload != nil {
				t.Fatal("invalid view returned data")
			}
		}
	}
	for view := vaultread.ProfileView; view <= vaultread.SetupView; view++ {
		for _, w := range []*Workspace{nil, {}} {
			payload, err := w.Vault(context.Background(), view)
			requireCode(t, err, "DATA_HOME_UNAVAILABLE")
			if payload != nil {
				t.Fatal("unavailable home returned data")
			}
		}
	}
}

func TestNativeVaultViewsAreFreshReadOnlyAndSnapshotPreservesNullContact(t *testing.T) {
	base, start := fixture(t)
	writeProfileFixture(t, start.DataRoot, "name: Synthetic\ntarget_location: US\nwork_authorization: synthetic\nmajor: CS\ncontact: null\n")
	w, err := Open(start)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		view     vaultread.View
		original func(context.Context, string) (json.RawMessage, *vaultread.Error)
		path     string
	}{
		{vaultread.EligibilityView, vaultread.ReadEligibility, "career_vault/visa_profile.yaml"},
		{vaultread.EducationView, vaultread.ReadEducation, "career_vault/education.yaml"},
		{vaultread.ExperiencesView, vaultread.ReadExperiences, "career_vault/experience_blocks.yaml"},
		{vaultread.ProjectsView, vaultread.ReadProjects, "career_vault/project_blocks.yaml"},
		{vaultread.SkillsView, vaultread.ReadSkills, "career_vault/skill_blocks.yaml"},
		{vaultread.SnapshotView, vaultread.ReadSnapshot, "career_vault/profile.yaml"},
	} {
		before := tree(t, base)
		want, failure := tc.original(context.Background(), start.DataRoot)
		first, err := w.Vault(context.Background(), tc.view)
		if failure != nil || err != nil || !bytes.Equal(first, want) || !reflect.DeepEqual(before, tree(t, base)) {
			t.Fatal("workspace view differs from service or mutates files")
		}
		first[0] = '!'
		second, err := w.Vault(context.Background(), tc.view)
		if err != nil || !bytes.Equal(second, want) {
			t.Fatal("caller-mutated payload was cached")
		}
		// Change only this fixture's source bytes and require a new revision.
		appendFixtureComment(t, start.DataRoot, tc.path)
		afterEdit := tree(t, base)
		third, err := w.Vault(context.Background(), tc.view)
		if err != nil || bytes.Equal(third, second) || !reflect.DeepEqual(afterEdit, tree(t, base)) {
			t.Fatal("workspace served stale revision or changed fixture")
		}
	}
	if payload, err := w.Profile(context.Background()); payload != nil {
		t.Fatal("standalone Profile lost its additional contact check")
	} else {
		requireVaultCode(t, err, "VAULT_RECORD_VALIDATION_FAILED")
	}
	writeFixture(t, start.DataRoot, datahome.MarkerPath, marker("dev", "dh_ffffffffffffffffffffffffffffffff"))
	before := tree(t, base)
	for view := vaultread.ProfileView; view <= vaultread.SetupView; view++ {
		payload, err := w.Vault(context.Background(), view)
		requireCode(t, err, "DATA_HOME_IDENTITY_CHANGED")
		if payload != nil || !reflect.DeepEqual(before, tree(t, base)) {
			t.Fatal("identity drift returned stale data or touched files")
		}
	}
}
