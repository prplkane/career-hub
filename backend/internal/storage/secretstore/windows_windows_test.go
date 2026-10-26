//go:build windows

package secretstore

import (
	"bytes"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

type fakeAPI struct {
	write  func(*credential, uint32) (bool, uint32)
	read   func(*uint16, uint32, uint32) (*credential, bool, uint32)
	delete func(*uint16, uint32, uint32) (bool, uint32)
	free   func(*credential)
}

func (f *fakeAPI) Write(c *credential, flags uint32) (bool, uint32) { return f.write(c, flags) }
func (f *fakeAPI) Read(target *uint16, kind, flags uint32) (*credential, bool, uint32) {
	return f.read(target, kind, flags)
}
func (f *fakeAPI) Delete(target *uint16, kind, flags uint32) (bool, uint32) {
	return f.delete(target, kind, flags)
}
func (f *fakeAPI) Free(c *credential) { f.free(c) }

func backendError(t *testing.T, err error, code, operation string, win32 uint32) {
	t.Helper()
	value, ok := err.(*BackendError)
	if !ok || value.Code != code || value.Operation != operation || value.Win32Code != win32 {
		t.Fatalf("unexpected fixed backend error: %v", err)
	}
}

func TestCredentialABI(t *testing.T) {
	var c credential
	var a credentialAttribute
	want := []uintptr{0, 4, 8, 16, 24, 32, 40, 48, 52, 56, 64, 72, 80, 24}
	if unsafe.Sizeof(uintptr(0)) == 4 {
		want = []uintptr{0, 4, 8, 12, 16, 24, 28, 32, 36, 40, 44, 48, 52, 16}
	}
	got := []uintptr{unsafe.Offsetof(c.Flags), unsafe.Offsetof(c.Type), unsafe.Offsetof(c.TargetName), unsafe.Offsetof(c.Comment), unsafe.Offsetof(c.LastWritten), unsafe.Offsetof(c.CredentialBlobSize), unsafe.Offsetof(c.CredentialBlob), unsafe.Offsetof(c.Persist), unsafe.Offsetof(c.AttributeCount), unsafe.Offsetof(c.Attributes), unsafe.Offsetof(c.TargetAlias), unsafe.Offsetof(c.UserName), unsafe.Sizeof(c), unsafe.Sizeof(a)}
	if !reflect.DeepEqual(got, want) || unsafe.Sizeof(windows.Filetime{}) != 8 {
		t.Fatal("CREDENTIALW native ABI mismatch")
	}
	if unsafe.Offsetof(a.ValueSize) != unsafe.Sizeof(uintptr(0))+4 || unsafe.Offsetof(a.Value) != unsafe.Sizeof(uintptr(0))+8 {
		t.Fatal("CREDENTIAL_ATTRIBUTEW ABI mismatch")
	}
}

func TestWriteBinaryIdentityFieldsOwnershipAndBounds(t *testing.T) {
	for _, length := range []int{0, 1, 32, 513, MaxWindowsBlobBytes} {
		t.Run(string(rune('a'+length%26)), func(t *testing.T) {
			secret := make([]byte, length)
			for i := range secret {
				secret[i] = byte(i)
			}
			before := bytes.Clone(secret)
			called := false
			backend := &windowsBackend{api: &fakeAPI{write: func(c *credential, flags uint32) (bool, uint32) {
				called = true
				if flags != 0 || c.Flags != 0 || c.Type != 1 || c.Persist != 2 || c.AttributeCount != 0 || c.Attributes != nil || c.Comment != nil || c.TargetAlias != nil || c.LastWritten != (windows.Filetime{}) {
					t.Fatal("write policy/unused fields changed")
				}
				if windows.UTF16PtrToString(c.TargetName) != "CareerHub/dev/provider/synthetic-é-😀" || windows.UTF16PtrToString(c.UserName) != "CareerHub" {
					t.Fatal("write target/username mismatch")
				}
				if int(c.CredentialBlobSize) != length || c.CredentialBlob == nil {
					t.Fatal("write blob boundary mismatch")
				}
				data := unsafe.Slice(c.CredentialBlob, length+1)
				if !bytes.Equal(data[:length], before) || data[length] != 0 {
					t.Fatal("write changed binary bytes or terminator")
				}
				data[0] = 0xff
				runtime.GC()   // Fake native consumer cannot mutate caller input.
				return true, 5 // Stale LastError is not failure when BOOL succeeds.
			}}}
			if err := backend.Write("CareerHub/dev/provider/synthetic-é-😀", secret); err != nil || !called || !bytes.Equal(secret, before) {
				t.Fatal("write failed or aliased caller bytes")
			}
		})
	}
	backend := &windowsBackend{api: &fakeAPI{}}
	backendError(t, backend.Write("target", make([]byte, MaxWindowsBlobBytes+1)), CodeBlobTooLarge, "", 0)
	backendError(t, backend.Write("bad\x00target", make([]byte, MaxWindowsBlobBytes+1)), CodeTargetInvalid, "", 0)
}

func TestTargetUTF16ValidationWithoutNativeCalls(t *testing.T) {
	for _, target := range []string{"", strings.Repeat("a", MaxWindowsTargetUnits), strings.Repeat("😀", 16383) + "a", "é"} {
		encoded, err := windowsTarget(target)
		if err != nil || len(encoded) != len(utf16.Encode([]rune(target)))+1 || encoded[len(encoded)-1] != 0 {
			t.Fatal("valid UTF16 target rejected")
		}
	}
	backend := &windowsBackend{api: &fakeAPI{}}
	for _, target := range []string{strings.Repeat("a", MaxWindowsTargetUnits+1), strings.Repeat("😀", 16384), "bad\x00target", string([]byte{0xff}), string([]byte{0xed, 0xa0, 0x80})} {
		backendError(t, backend.Write(target, nil), CodeTargetInvalid, "", 0)
		data, found, err := backend.Read(target)
		backendError(t, err, CodeTargetInvalid, "", 0)
		if data != nil || found {
			t.Fatal("invalid read returned data")
		}
		deleted, err := backend.Delete(target)
		backendError(t, err, CodeTargetInvalid, "", 0)
		if deleted {
			t.Fatal("invalid delete returned true")
		}
	}
}

func TestReadCopiesBinaryValueAndFreesEveryNonnilSuccess(t *testing.T) {
	for _, length := range []int{0, 32, 513, MaxWindowsBlobBytes} {
		data := make([]byte, length)
		for i := range data {
			data[i] = byte(i)
		}
		before := bytes.Clone(data)
		c := &credential{Type: 1, CredentialBlobSize: uint32(length)}
		if length > 0 {
			c.CredentialBlob = &data[0]
		}
		freed := 0
		backend := &windowsBackend{api: &fakeAPI{read: func(name *uint16, kind, flags uint32) (*credential, bool, uint32) {
			if windows.UTF16PtrToString(name) != "synthetic" || kind != 1 || flags != 0 {
				t.Fatal("read arguments mismatch")
			}
			return c, true, 1168
		}, free: func(got *credential) {
			if got != c {
				t.Fatal("freed wrong pointer")
			}
			freed++
			for i := range data {
				data[i] = 0xff
			}
		}}}
		got, found, err := backend.Read("synthetic")
		if err != nil || !found || freed != 1 || got == nil || !bytes.Equal(got, before) {
			t.Fatal("read ownership/free/value mismatch")
		}
	}
	for _, c := range []*credential{{Type: 2}, {Type: 1, CredentialBlobSize: MaxWindowsBlobBytes + 1}, {Type: 1, CredentialBlobSize: 1}} {
		freed := 0
		backend := &windowsBackend{api: &fakeAPI{read: func(*uint16, uint32, uint32) (*credential, bool, uint32) { return c, true, 0 }, free: func(got *credential) {
			if got != c {
				t.Fatal("freed wrong malformed pointer")
			}
			freed++
		}}}
		got, found, err := backend.Read("synthetic")
		backendError(t, err, CodeInvalidResult, "", 0)
		if got != nil || found || freed != 1 {
			t.Fatal("malformed read exposed bytes or leaked native result")
		}
	}
	backend := &windowsBackend{api: &fakeAPI{read: func(*uint16, uint32, uint32) (*credential, bool, uint32) { return nil, true, 0 }, free: func(*credential) { t.Fatal("attempted to free absent allocation") }}}
	got, found, err := backend.Read("synthetic")
	backendError(t, err, CodeInvalidResult, "", 0)
	if got != nil || found {
		t.Fatal("nil native result accepted")
	}
}

func TestOnly1168MeansMissing(t *testing.T) {
	for _, code := range []uint32{0, 5, 1168, 1312} {
		backend := &windowsBackend{api: &fakeAPI{
			write:  func(*credential, uint32) (bool, uint32) { return false, code },
			read:   func(*uint16, uint32, uint32) (*credential, bool, uint32) { return nil, false, code },
			delete: func(*uint16, uint32, uint32) (bool, uint32) { return false, code },
			free:   func(*credential) { t.Fatal("failed read reached CredFree") },
		}}
		backendError(t, backend.Write("synthetic", nil), CodeWindowsFailure, "write", code)
		data, found, readErr := backend.Read("synthetic")
		deleted, deleteErr := backend.Delete("synthetic")
		if data != nil || found || deleted {
			t.Fatal("failed native operation returned success")
		}
		if code == 1168 {
			if readErr != nil || deleteErr != nil {
				t.Fatal("missing was failure")
			}
		} else {
			backendError(t, readErr, CodeWindowsFailure, "read", code)
			backendError(t, deleteErr, CodeWindowsFailure, "delete", code)
		}
	}
	backend := &windowsBackend{api: &fakeAPI{delete: func(name *uint16, kind, flags uint32) (bool, uint32) {
		if windows.UTF16PtrToString(name) != "synthetic" || kind != 1 || flags != 0 {
			t.Fatal("delete arguments mismatch")
		}
		return true, 1312
	}}}
	if deleted, err := backend.Delete("synthetic"); err != nil || !deleted {
		t.Fatal("stale error overrode successful delete")
	}
}

type fakeProcedure struct {
	findErr      error
	finds, calls int
	result       uintptr
	errno        error
	args         []uintptr
}

func (f *fakeProcedure) Find() error { f.finds++; return f.findErr }
func (f *fakeProcedure) Call(args ...uintptr) (uintptr, uintptr, error) {
	f.calls++
	f.args = append([]uintptr{}, args...)
	return f.result, 0, f.errno
}

func TestProcedureResolutionFailureIsReturnedNotPanicked(t *testing.T) {
	for _, failed := range []string{"CredWriteW", "CredReadW", "CredDeleteW", "CredFree", ""} {
		var resolved []string
		procs := map[string]*fakeProcedure{}
		api, err := bindWindowsAPI(func(name string) procedure {
			resolved = append(resolved, name)
			proc := &fakeProcedure{}
			if name == failed {
				proc.findErr = errors.New("private DLL path marker")
			}
			procs[name] = proc
			return proc
		})
		if failed == "" {
			if err != nil || api == nil || len(resolved) != 4 {
				t.Fatal("procedure resolution failed")
			}
		} else {
			backendError(t, err, CodeBackendUnavailable, "initialize", 0)
			if api != nil || resolved[len(resolved)-1] != failed {
				t.Fatal("failed resolution returned API or continued")
			}
		}
		for _, proc := range procs {
			if proc.finds != 1 || proc.calls != 0 {
				t.Fatal("resolution made a native credential call")
			}
		}
	}
	if api, err := bindWindowsAPI(func(string) procedure { return nil }); api != nil || err == nil {
		t.Fatal("nil procedure accepted")
	}
}

func TestNativeCallShimsCaptureImmediateErrno(t *testing.T) {
	write := &fakeProcedure{result: 1, errno: syscall.Errno(5)}
	read := &fakeProcedure{errno: syscall.Errno(1312)}
	del := &fakeProcedure{errno: syscall.Errno(1168)}
	free := &fakeProcedure{errno: errors.New("unused private error")}
	api := &windowsAPI{write: write, read: read, delete: del, free: free}
	c := &credential{}
	name := []uint16{'x', 0}
	if ok, code := api.Write(c, 0); !ok || code != 5 || len(write.args) != 2 || write.args[0] == 0 {
		t.Fatal("write shim changed results")
	}
	if value, ok, code := api.Read(&name[0], 1, 0); value != nil || ok || code != 1312 || len(read.args) != 4 || read.args[1] != 1 || read.args[2] != 0 || read.args[3] == 0 {
		t.Fatal("read shim changed results")
	}
	if ok, code := api.Delete(&name[0], 1, 0); ok || code != 1168 || len(del.args) != 3 || del.args[1] != 1 || del.args[2] != 0 {
		t.Fatal("delete shim changed results")
	}
	api.Free(c)
	if free.calls != 1 || len(free.args) != 1 || free.args[0] == 0 {
		t.Fatal("free shim changed arguments")
	}
	if errnoCode(errors.New("private non-errno marker")) != 0 || errnoCode(nil) != 0 {
		t.Fatal("unknown syscall error disclosed data")
	}
}
