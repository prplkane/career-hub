//go:build windows

package privacy

import (
	"context"
	"testing"

	"golang.org/x/sys/windows"
)

func TestVerifyPrivateRootAcceptsTheBootstrapPolicy(t *testing.T) {
	f := newNativeFixture(t) // protected owner+SYSTEM DACL, owned by this user
	if e := VerifyPrivateRoot(context.Background(), f.root); e != nil {
		t.Fatalf("private root refused: %s", e.Code)
	}
}

func TestVerifyPrivateRootRefusesLoosenedPermissions(t *testing.T) {
	cases := map[string]struct {
		sddl      string
		protected bool
	}{
		// Someone granted Everyone read access to the Home root.
		"extra reader": {"(A;OICI;FR;;;WD)", true},
		// Inheritance was re-enabled, so the parent folder's ACEs flow in.
		"unprotected": {"", false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newNativeFixture(t)
			f.setDACL(f.root, f.privateRootDACL()+c.sddl, c.protected)
			e := VerifyPrivateRoot(context.Background(), f.root)
			if e == nil || e.Code != "DATA_HOME_PERMISSIONS_UNSAFE" {
				t.Fatalf("loosened root accepted or misreported: %+v", e)
			}
		})
	}
	// Changing the owner to another account needs SeRestorePrivilege, which an
	// ordinary test process does not hold; the owner comparison is exercised by
	// the accepting case above.
}

func TestVerifyPrivateRootRefusesCanceledContext(t *testing.T) {
	f := newNativeFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := VerifyPrivateRoot(ctx, f.root); e == nil || e.Code != "ACL_CONTEXT_CANCELED" {
		t.Fatalf("canceled verification ran: %+v", e)
	}
	if e := VerifyPrivateRoot(nil, f.root); e == nil {
		t.Fatal("nil context accepted")
	}
}

func TestTokenDefaultOwnerIsReadable(t *testing.T) {
	owner, err := tokenDefaultOwner(windows.GetCurrentProcessToken())
	if err != nil || owner == nil || !owner.IsValid() {
		t.Fatal("the process token's default owner could not be read", err)
	}
}
