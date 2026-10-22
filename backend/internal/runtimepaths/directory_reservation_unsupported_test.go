//go:build !windows

package runtimepaths

import "testing"

func TestDirectoryReservationUnsupported(t *testing.T) {
	var root *Root
	g, o, p := root.ReserveDirectory("no-filesystem-access")
	if g != nil || o != DirectoryNotCreated || p == nil || p.Code != "DIRECTORY_RESERVATION_UNSUPPORTED" {
		t.Fatal(o, p)
	}
}
