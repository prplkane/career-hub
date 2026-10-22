//go:build windows

package datahome

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/datahome/privacy"
	"golang.org/x/sys/windows"
)

// ADR SEC-250 item 5: an existing Home whose root permissions were loosened is
// refused at startup before any write, and starts again once repaired.
func TestBootstrapRefusesAnExistingHomeWithLoosenedRootPermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	if _, e := Bootstrap(context.Background(), root, "stable"); e != nil {
		t.Fatalf("fresh bootstrap: %#v", e)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	loosened, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := loosened.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	before := bootstrapTree(t, root)
	_, e := Bootstrap(context.Background(), root, "stable")
	if e == nil || e.Code != "DATA_HOME_PERMISSIONS_UNSAFE" || e.Stage != "permissions" {
		t.Fatalf("loosened Home was not refused: %#v", e)
	}
	if !reflect.DeepEqual(before, bootstrapTree(t, root)) {
		t.Fatal("a refused start changed the Home")
	}
	if _, e := privacy.ApplyPrivateTree(context.Background(), root); e != nil {
		t.Fatalf("repair: %s", e.Code)
	}
	if _, e := Bootstrap(context.Background(), root, "stable"); e != nil {
		t.Fatalf("repaired Home still refused: %#v", e)
	}
}
