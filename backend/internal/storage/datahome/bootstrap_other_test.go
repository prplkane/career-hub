//go:build !windows

package datahome

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapUnsupportedPlatformNeverCreatesRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-created")
	info, e := Bootstrap(context.Background(), root, "stable")
	if info != nil || e == nil || e.Stage != "platform" || e.Code != "DATA_HOME_BOOTSTRAP_UNSUPPORTED" || e.MayHaveChanged {
		t.Fatal("unsupported bootstrap did not fail before mutation")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("unsupported bootstrap created a root")
	}
}
