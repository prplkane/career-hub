//go:build !windows

package startup

import (
	"context"
	"os"
	"testing"
)

func TestNativeStartupProviderUnavailableBeforeCreation(t *testing.T) {
	start := fixture(t)
	w, err := Open(context.Background(), start)
	requireCode(t, err, "DATA_HOME_BOOTSTRAP_UNSUPPORTED")
	if w != nil {
		t.Fatal("unsupported provider returned workspace")
	}
	if _, err := os.Lstat(start.DataRoot); !os.IsNotExist(err) {
		t.Fatal("unsupported provider created candidate")
	}
}
