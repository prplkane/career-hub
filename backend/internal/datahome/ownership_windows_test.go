//go:build windows

package datahome

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/runtimepaths"
)

func TestBootstrapOwnershipBeforeMarkerPermissionsAndSeed(t *testing.T) {
	for _, test := range []struct{ native, public string }{
		{"OWNERSHIP_IN_USE", "DATA_HOME_IN_USE"},
		{"OWNERSHIP_UNSUPPORTED", "DATA_HOME_OWNERSHIP_UNSUPPORTED"},
		{"OWNERSHIP_UNAVAILABLE", "DATA_HOME_OWNERSHIP_UNAVAILABLE"},
		{"private unknown cause", "DATA_HOME_OWNERSHIP_UNAVAILABLE"},
	} {
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		bootstrapFixtureWrite(t, root, MarkerPath, []byte("invalid fixture marker"))
		before := bootstrapDACL(t, root)
		// Missing downstream operations would panic if failure did not stop.
		ops := bootstrapOperations{claim: func(*runtimepaths.Root) *runtimepaths.Error {
			return &runtimepaths.Error{Code: test.native}
		}}
		info, e := bootstrap(context.Background(), root, "dev", ops)
		if info != nil || e == nil || e.Stage != "ownership" || e.Code != test.public || e.MayHaveChanged {
			t.Fatal("ownership failure did not precede private bootstrap operations")
		}
		raw, err := os.ReadFile(filepath.Join(root, MarkerPath))
		names, listErr := os.ReadDir(root)
		if err != nil || listErr != nil || string(raw) != "invalid fixture marker" || len(names) != 1 || before != bootstrapDACL(t, root) {
			t.Fatal("refused claim changed bootstrap fixture")
		}
	}
}

func TestBootstrapCancellationImmediatelyAfterClaim(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ops := bootstrapOperations{claim: func(*runtimepaths.Root) *runtimepaths.Error {
		cancel()
		return nil
	}}
	info, e := bootstrap(ctx, root, "dev", ops)
	if info != nil || e == nil || e.Code != "DATA_HOME_STARTUP_CANCELLED" {
		t.Fatal("cancellation after claim was ignored")
	}
	names, err := os.ReadDir(root)
	if err != nil || len(names) != 0 {
		t.Fatal("canceled claimed bootstrap seeded files")
	}
}
