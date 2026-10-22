//go:build windows

package privacy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func broadBootstrapFixture(t *testing.T) *nativeFixture {
	t.Helper()
	f := newNativeFixture(t)
	f.mkdir("nested")
	f.write("nested/opaque.dat", []byte("synthetic private fixture\x00\xff"))
	f.setDACL(f.root, f.privateRootDACL()+"(A;OICI;FR;;;WD)", true)
	f.setDACL(filepath.Join(f.root, "nested"), f.privateRootDACL()+"(A;OICI;FR;;;WD)", true)
	f.setDACL(filepath.Join(f.root, "nested", "opaque.dat"), "D:P(A;;FA;;;"+f.sid+")(A;;FA;;;SY)(A;;FR;;;WD)", true)
	return f
}

func TestApplyPrivateTreeRepairsProtectedChildrenAndInherits(t *testing.T) {
	f := broadBootstrapFixture(t)
	parentACL := nativeDACL(t, f.container)
	file := filepath.Join(f.root, "nested", "opaque.dat")
	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if e := VerifyPrivateTree(context.Background(), f.root); e == nil {
		t.Fatal("broad fixture should not be private")
	}
	changed, e := ApplyPrivateTree(context.Background(), f.root)
	if e != nil || !changed {
		t.Fatalf("native ACL application failed: changed=%v error=%v", changed, e)
	}
	if e := VerifyPrivateTree(context.Background(), f.root); e != nil {
		t.Fatal(e)
	}
	if nativeDACL(t, f.container) != parentACL {
		t.Fatal("parent outside fixture changed")
	}
	after, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil || string(raw) != "synthetic private fixture\x00\xff" || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("ACL application changed content/mtime")
	}
	f.mkdir("later")
	f.write("later/new.dat", []byte("post-policy child"))
	if e := VerifyPrivateTree(context.Background(), f.root); e != nil {
		t.Fatalf("new children did not inherit: %v", e)
	}
}

func TestApplyPrivateTreePartialFailureIsRetryable(t *testing.T) {
	f := broadBootstrapFixture(t)
	calls := 0
	changed, e := applyPrivateTree(context.Background(), f.root, func(h windows.Handle, acl *windows.ACL, protected bool) error {
		calls++
		if calls == 2 {
			return errors.New("synthetic denied ACL mutation")
		}
		return setBootstrapDACL(h, acl, protected)
	})
	if !changed || e == nil || e.Code != "ACL_SETUP_FAILED" || calls != 2 {
		t.Fatal("partial ACL state not reported")
	}
	changed, e = ApplyPrivateTree(context.Background(), f.root)
	if e != nil || !changed {
		t.Fatal("ACL retry failed")
	}
	if e := VerifyPrivateTree(context.Background(), f.root); e != nil {
		t.Fatal(e)
	}
}

func TestApplyPrivateTreeRepairsContentReadDeniedFile(t *testing.T) {
	f := broadBootstrapFixture(t)
	file := filepath.Join(f.root, "nested", "opaque.dat")
	f.setDACL(file, "D:P(D;;0x1;;;"+f.sid+")(A;;FA;;;"+f.sid+")(A;;FA;;;SY)", true)
	if _, err := os.ReadFile(file); err == nil {
		t.Fatal("synthetic content-read denial did not take effect")
	}
	changed, e := ApplyPrivateTree(context.Background(), f.root)
	if e != nil || !changed {
		t.Fatalf("metadata/DACL-accessible file should be repairable: changed=%v error=%#v", changed, e)
	}
	raw, err := os.ReadFile(file)
	if err != nil || string(raw) != "synthetic private fixture\x00\xff" {
		t.Fatal("opaque file was not preserved and made readable")
	}
}

func TestApplyPrivateTreeRefusesBeforeAnyACLWrite(t *testing.T) {
	f := broadBootstrapFixture(t)
	before := nativeDACL(t, f.root)
	alias := filepath.Join(f.root, "hardlink")
	if err := os.Link(filepath.Join(f.root, "nested", "opaque.dat"), alias); err != nil {
		t.Fatal(err)
	}
	calls := 0
	changed, e := applyPrivateTree(context.Background(), f.root, func(windows.Handle, *windows.ACL, bool) error { calls++; return nil })
	if changed || e == nil || calls != 0 || nativeDACL(t, f.root) != before {
		t.Fatal("unsafe tree reached permission mutation")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []context.Context{nil, ctx} {
		changed, e := ApplyPrivateTree(c, f.root)
		if changed || e == nil || e.Code != "ACL_CONTEXT_CANCELED" {
			t.Fatal("cancelled permission setup reached mutation")
		}
	}
}
