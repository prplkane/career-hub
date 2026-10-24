//go:build !windows

package privacy

import (
	"context"
	"testing"
)

func TestUnsupportedPlatform(t *testing.T) {
	if e := VerifyPrivateTree(context.Background(), "/nonexistent/synthetic"); e == nil || e.Code != "ACL_PLATFORM_UNSUPPORTED" {
		t.Fatal("unsupported platform was not explicit")
	}
}
