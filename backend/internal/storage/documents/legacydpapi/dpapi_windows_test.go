//go:build windows

package legacydpapi

import (
	"bytes"
	"errors"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

type fakeAPI struct {
	unprotect func(*windows.DataBlob, *windows.DataBlob) bool
	free      func(*byte) bool
}

func (f *fakeAPI) Unprotect(source, output *windows.DataBlob) bool {
	return f.unprotect(source, output)
}
func (f *fakeAPI) Free(pointer *byte) bool { return f.free(pointer) }
func resolver(api nativeAPI) func() (nativeAPI, error) {
	return func() (nativeAPI, error) { return api, nil }
}

func TestDataBlobABI(t *testing.T) {
	var value windows.DataBlob
	wantSize, wantOffset := uintptr(16), uintptr(8)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		wantSize, wantOffset = 8, 4
	}
	if unsafe.Sizeof(value) != wantSize || unsafe.Offsetof(value.Size) != 0 || unsafe.Sizeof(value.Size) != 4 || unsafe.Offsetof(value.Data) != wantOffset {
		t.Fatal("DATA_BLOB ABI mismatch")
	}
}

func TestInputRejectedBeforeNativeResolution(t *testing.T) {
	resolve := func() (nativeAPI, error) { t.Fatal("invalid input reached native resolution"); return nil, nil }
	for _, content := range [][]byte{nil, {}} {
		data, err := unprotectWith(content, resolve)
		requireError(t, err, CodeInputInvalid)
		if data != nil {
			t.Fatal("invalid input returned plaintext")
		}
	}
	data, err := unprotectWith(make([]byte, MaxCiphertextBytes+1), resolve)
	requireError(t, err, CodeInputTooLarge)
	if data != nil {
		t.Fatal("oversized input returned plaintext")
	}
}

func TestResolverFailureHasNoPrivateCauseOrNativeCall(t *testing.T) {
	for _, resolve := range []func() (nativeAPI, error){
		func() (nativeAPI, error) { return nil, errors.New("private DLL path marker") },
		func() (nativeAPI, error) { return &fakeAPI{}, errors.New("private DLL marker") },
		func() (nativeAPI, error) { return nil, nil },
	} {
		data, err := unprotectWith([]byte("synthetic"), resolve)
		requireError(t, err, CodeInitializationFailed)
		if data != nil {
			t.Fatal("resolution failure returned plaintext")
		}
	}
}

func TestInputSnapshotAndCopyBeforeFree(t *testing.T) {
	content := []byte{0, 0xff, 0x80, 1, 0, 7}
	before := bytes.Clone(content)
	nativeOutput := []byte{0, 0xfe, 0x80, 3, 0}
	want := bytes.Clone(nativeOutput)
	called, freed := 0, 0
	api := &fakeAPI{unprotect: func(source, output *windows.DataBlob) bool {
		called++
		if int(source.Size) != len(before) || source.Data == nil || output.Size != 0 || output.Data != nil {
			t.Fatal("input framing/output initialization mismatch")
		}
		buffer := unsafe.Slice(source.Data, len(before)+1)
		if !bytes.Equal(buffer[:len(before)], before) || buffer[len(before)] != 0 {
			t.Fatal("input snapshot/terminator changed")
		}
		buffer[0] = 9 // Native consumer must not mutate caller input.
		output.Size = uint32(len(nativeOutput))
		output.Data = &nativeOutput[0]
		runtime.GC()
		return true
	}, free: func(pointer *byte) bool {
		freed++
		if pointer != &nativeOutput[0] {
			t.Fatal("freed wrong native pointer")
		}
		clear(nativeOutput)
		return true
	}}
	resolve := func() (nativeAPI, error) {
		for i := range content {
			content[i] = 0x33
		}
		return api, nil
	}
	got, err := unprotectWith(content, resolve)
	if err != nil || called != 1 || freed != 1 || !bytes.Equal(got, want) {
		t.Fatal("copy/free/ownership mismatch")
	}
	if content[0] != 0x33 {
		t.Fatal("native input aliases caller buffer")
	}
	got[0] = 0x44
	if nativeOutput[0] != 0 {
		t.Fatal("returned plaintext aliases native allocation")
	}
}

func TestExactCiphertextLimitReachesFakeAPI(t *testing.T) {
	content := make([]byte, MaxCiphertextBytes)
	content[len(content)-1] = 0x7f
	freed := 0
	api := &fakeAPI{unprotect: func(source, output *windows.DataBlob) bool {
		if source.Size != MaxCiphertextBytes || unsafe.Slice(source.Data, int(source.Size))[len(content)-1] != 0x7f {
			t.Fatal("exact ciphertext bound changed")
		}
		return true
	}, free: func(pointer *byte) bool {
		if pointer != nil {
			t.Fatal("unexpected empty output allocation")
		}
		freed++
		return true
	}}
	got, err := unprotectWith(content, resolver(api))
	if err != nil || got == nil || len(got) != 0 || freed != 1 {
		t.Fatal("exact maximum/empty output mismatch")
	}
}

func TestSuccessfulEmptyOutputWithOrWithoutAllocation(t *testing.T) {
	var allocation byte
	for _, pointer := range []*byte{nil, &allocation} {
		freed := 0
		api := &fakeAPI{unprotect: func(source, output *windows.DataBlob) bool { output.Data = pointer; return true }, free: func(value *byte) bool {
			if value != pointer {
				t.Fatal("wrong zero-size free pointer")
			}
			freed++
			return true
		}}
		got, err := unprotectWith([]byte{1}, resolver(api))
		if err != nil || got == nil || len(got) != 0 || freed != 1 {
			t.Fatal("source empty-output behavior changed")
		}
	}
}

func TestPlaintextBoundAndMalformedResultsAreFreed(t *testing.T) {
	large := make([]byte, MaxPlaintextBytes)
	large[len(large)-1] = 0x7f
	freed := 0
	api := &fakeAPI{unprotect: func(_, output *windows.DataBlob) bool {
		output.Size = uint32(len(large))
		output.Data = &large[0]
		return true
	}, free: func(value *byte) bool {
		if value != &large[0] {
			t.Fatal("wrong max-output pointer")
		}
		freed++
		return true
	}}
	got, err := unprotectWith([]byte{1}, resolver(api))
	if err != nil || len(got) != MaxPlaintextBytes || got[len(got)-1] != 0x7f || freed != 1 {
		t.Fatal("exact plaintext limit rejected")
	}
	got[0] = 0xff
	if large[0] != 0 {
		t.Fatal("max output aliases native memory")
	}
	var allocation byte
	for _, out := range []windows.DataBlob{
		{Size: MaxPlaintextBytes + 1, Data: &allocation},
		{Size: ^uint32(0), Data: &allocation},
		{Size: 1, Data: nil},
	} {
		for _, freeOK := range []bool{true, false} {
			freed := 0
			api := &fakeAPI{unprotect: func(_, output *windows.DataBlob) bool { *output = out; return true }, free: func(pointer *byte) bool {
				if pointer != out.Data {
					t.Fatal("wrong malformed-output pointer")
				}
				freed++
				return freeOK
			}}
			got, err := unprotectWith([]byte{1}, resolver(api))
			code := CodeOutputInvalid
			if !freeOK {
				code = CodeCleanupFailed
			}
			requireError(t, err, code)
			if got != nil || freed != 1 {
				t.Fatal("malformed result exposed data or skipped cleanup")
			}
		}
	}
}

func TestNativeFailureNeitherReadsNorFreesUncertainOutput(t *testing.T) {
	var uncertain byte
	for _, out := range []windows.DataBlob{{}, {Size: ^uint32(0), Data: &uncertain}, {Size: 1, Data: nil}} {
		api := &fakeAPI{unprotect: func(_, output *windows.DataBlob) bool { *output = out; return false }, free: func(*byte) bool { t.Fatal("failed native call incorrectly established ownership"); return true }}
		got, err := unprotectWith([]byte{0, 0xff}, resolver(api))
		requireError(t, err, CodeDecryptFailed)
		if got != nil {
			t.Fatal("failed native call returned plaintext")
		}
	}
}

func TestCleanupFailureSuppressesEvenValidPlaintext(t *testing.T) {
	nativeOutput := []byte("synthetic output")
	for _, length := range []uint32{0, uint32(len(nativeOutput))} {
		freed := 0
		api := &fakeAPI{unprotect: func(_, output *windows.DataBlob) bool {
			output.Size = length
			output.Data = &nativeOutput[0]
			return true
		}, free: func(pointer *byte) bool { freed++; return false }}
		got, err := unprotectWith([]byte{1}, resolver(api))
		requireError(t, err, CodeCleanupFailed)
		if got != nil || freed != 1 {
			t.Fatal("cleanup failure returned plaintext or retried free")
		}
	}
}

type fakeProcedure struct {
	findErr      error
	finds, calls int
	result       uintptr
	lastErr      error
	args         []uintptr
}

func (f *fakeProcedure) Find() error { f.finds++; return f.findErr }
func (f *fakeProcedure) Call(args ...uintptr) (uintptr, uintptr, error) {
	f.calls++
	f.args = append([]uintptr{}, args...)
	return f.result, 0, f.lastErr
}

func TestResolveAllProceduresBeforeNativeUse(t *testing.T) {
	for _, failure := range []string{"unprotect", "free", ""} {
		unprotect, free := &fakeProcedure{}, &fakeProcedure{}
		if failure == "unprotect" {
			unprotect.findErr = errors.New("private crypt32 path")
		}
		if failure == "free" {
			free.findErr = errors.New("private kernel32 path")
		}
		api, err := bindWindowsAPI(unprotect, free)
		if failure == "" {
			if err != nil || api == nil {
				t.Fatal("resolved API rejected")
			}
		} else {
			requireError(t, err, CodeInitializationFailed)
			if api != nil {
				t.Fatal("partial API exposed")
			}
		}
		wantFreeFinds := 1
		if failure == "unprotect" {
			wantFreeFinds = 0
		}
		if unprotect.finds != 1 || free.finds != wantFreeFinds || unprotect.calls != 0 || free.calls != 0 {
			t.Fatal("resolution order/native invocation changed")
		}
	}
	for _, pair := range [][2]procedure{{nil, &fakeProcedure{}}, {&fakeProcedure{}, nil}} {
		api, err := bindWindowsAPI(pair[0], pair[1])
		requireError(t, err, CodeInitializationFailed)
		if api != nil {
			t.Fatal("nil procedure accepted")
		}
	}
}

func TestNativeShimsUseExactFlagsAndResultSemantics(t *testing.T) {
	unprotect := &fakeProcedure{result: 1, lastErr: syscall.Errno(5)}
	free := &fakeProcedure{result: 0, lastErr: syscall.Errno(5)}
	api := &windowsAPI{unprotect: unprotect, free: free}
	source, output := &windows.DataBlob{}, &windows.DataBlob{}
	if !api.Unprotect(source, output) {
		t.Fatal("stale last-error overrode successful BOOL")
	}
	args := unprotect.args
	if len(args) != 7 || args[0] == 0 || args[1] != 0 || args[2] != 0 || args[3] != 0 || args[4] != 0 || args[5] != 1 || args[6] == 0 {
		t.Fatal("DPAPI options/arity changed")
	}
	unprotect.result = 0
	unprotect.lastErr = syscall.Errno(0)
	if api.Unprotect(source, output) {
		t.Fatal("zero last-error overrode failed BOOL")
	}
	var allocation byte
	if !api.Free(&allocation) || len(free.args) != 1 || free.args[0] == 0 {
		t.Fatal("LocalFree(NULL return) did not succeed")
	}
	if !api.Free(nil) || free.args[0] != 0 {
		t.Fatal("LocalFree nil input changed")
	}
	free.result = 1
	free.lastErr = syscall.Errno(0)
	if api.Free(&allocation) {
		t.Fatal("returned handle did not fail cleanup")
	}
}
