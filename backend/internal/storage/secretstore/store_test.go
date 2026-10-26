package secretstore

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type facadeMemory struct {
	values map[string][]byte
	trace  []string
	fail   string
	cause  error
}

func memory() *facadeMemory { return &facadeMemory{values: map[string][]byte{}} }
func (b *facadeMemory) Write(target string, value []byte) error {
	b.trace = append(b.trace, "write:"+target)
	if b.fail == "write" {
		return b.cause
	}
	b.values[target] = bytes.Clone(value)
	return nil
}
func (b *facadeMemory) Read(target string) ([]byte, bool, error) {
	b.trace = append(b.trace, "read:"+target)
	if b.fail == "read" {
		return nil, false, b.cause
	}
	value, found := b.values[target]
	return value, found, nil
}
func (b *facadeMemory) Delete(target string) (bool, error) {
	b.trace = append(b.trace, "delete:"+target)
	if b.fail == "delete" {
		return false, b.cause
	}
	_, found := b.values[target]
	delete(b.values, target)
	return found, nil
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	got, ok := err.(*ValidationError)
	if !ok || got.Code != code {
		t.Fatalf("expected validation category %s", code)
	}
}

func TestFacadeConstructorAndNameBeforeValue(t *testing.T) {
	var typedNil *facadeMemory
	for _, backend := range []Backend{nil, typedNil} {
		_, err := New("bad", backend)
		requireCode(t, err, CodeChannelInvalid)
		_, err = New("stable", backend)
		requireCode(t, err, CodeBackendRequired)
	}
	b := memory()
	s, err := New("dev", b)
	if err != nil {
		t.Fatal("constructor failed")
	}
	for _, name := range []string{"", ".start", "_start", "-start", " has space", "x\n", "a/b", "é", "a\x00", strings.Repeat("a", 81), string([]byte{0xff})} {
		requireCode(t, s.Set(name, ""), CodeNameInvalid)
		requireCode(t, s.Set(name, string([]byte{0xff})), CodeNameInvalid)
		_, _, err = s.Get(name)
		requireCode(t, err, CodeNameInvalid)
		_, err = s.Delete(name)
		requireCode(t, err, CodeNameInvalid)
	}
	if len(b.trace) != 0 {
		t.Fatal("invalid name called backend")
	}
	for _, name := range []string{"a", "0", "A.b_-9", strings.Repeat("Z", 80)} {
		if err := s.Set(name, "synthetic"); err != nil {
			t.Fatal("valid name rejected")
		}
		if _, ok := b.values["CareerHub/dev/provider/"+name]; !ok {
			t.Fatal("target changed")
		}
	}
}

func TestFacadeUTF8ByteBoundsAndUntrimmedValues(t *testing.T) {
	b := memory()
	s, _ := New("dev", b)
	for _, value := range []string{" ", "\x00", " \t\n\r ", strings.Repeat("a", 512), strings.Repeat("é", 256), strings.Repeat("🛠", 128)} {
		if err := s.Set("fixture", value); err != nil {
			t.Fatal("valid value rejected")
		}
		got, found, err := s.Get("fixture")
		if err != nil || !found || got != value {
			t.Fatal("value normalized or lost")
		}
	}
	requireCode(t, s.Set("fixture", ""), CodeValueEmpty)
	requireCode(t, s.Set("fixture", strings.Repeat("a", 513)), CodeValueTooLarge)
	requireCode(t, s.Set("fixture", strings.Repeat("é", 257)), CodeValueTooLarge)
	requireCode(t, s.Set("fixture", string([]byte{0xff})), CodeValueInvalidUTF8)
}

func TestFacadeGetDistinguishesMissingEmptyAndOversized(t *testing.T) {
	b := memory()
	s, _ := New("stable", b)
	if got, found, err := s.Get("fixture"); got != "" || found || err != nil {
		t.Fatal("missing changed")
	}
	for _, value := range [][]byte{nil, {}, bytes.Repeat([]byte("x"), 1024), []byte("\xef\xbb\xbf\x00")} {
		b.values["CareerHub/stable/provider/fixture"] = value
		got, found, err := s.Get("fixture")
		if !found || err != nil || got != string(value) {
			t.Fatal("stored value rejected or normalized")
		}
	}
	b.values["CareerHub/stable/provider/fixture"] = []byte("mutable")
	got, _, _ := s.Get("fixture")
	b.values["CareerHub/stable/provider/fixture"][0] = 'X'
	if got != "mutable" {
		t.Fatal("returned string aliases backend storage")
	}
	for _, value := range [][]byte{{0xff}, {0xc0, 0x80}, {0xed, 0xa0, 0x80}, {0xf4, 0x90, 0x80, 0x80}} {
		b.values["CareerHub/stable/provider/fixture"] = value
		got, found, err := s.Get("fixture")
		requireCode(t, err, CodeStoredValueInvalid)
		if got != "" || found {
			t.Fatal("invalid bytes escaped")
		}
	}
}

func TestFacadeDeleteAndChannelIsolation(t *testing.T) {
	b := memory()
	stable, _ := New("stable", b)
	dev, _ := New("dev", b)
	_ = stable.Set("fixture", "one")
	_ = dev.Set("fixture", "two")
	if deleted, err := stable.Delete("fixture"); !deleted || err != nil {
		t.Fatal("delete failed")
	}
	if deleted, err := stable.Delete("fixture"); deleted || err != nil {
		t.Fatal("missing delete changed")
	}
	if value, found, err := dev.Get("fixture"); value != "two" || !found || err != nil {
		t.Fatal("cross-channel deletion")
	}
}

func TestFacadeBackendErrorIdentityAndRedaction(t *testing.T) {
	cause := errors.New("private backend content")
	for _, operation := range []string{"write", "read", "delete"} {
		b := memory()
		b.fail, b.cause = operation, cause
		s, _ := New("stable", b)
		var err error
		switch operation {
		case "write":
			err = s.Set("private-name", "private-value")
		case "read":
			_, _, err = s.Get("private-name")
		case "delete":
			_, err = s.Delete("private-name")
		}
		wrapped, ok := err.(*BackendOperationError)
		if !ok || wrapped.Operation != operation || !errors.Is(err, cause) {
			t.Fatal("backend identity lost")
		}
		for _, format := range []string{"%s", "%v", "%+v", "%#v"} {
			if strings.Contains(fmt.Sprintf(format, err), "private") {
				t.Fatal("backend cause leaked")
			}
		}
	}
	validation := &ValidationError{Code: "private forged code"}
	if strings.Contains(fmt.Sprintf("%#v", validation), "private") {
		t.Fatal("validation internals leaked")
	}
}

func TestFacadeZeroAndNilStoreFailSafely(t *testing.T) {
	for _, s := range []*Store{nil, {}} {
		requireCode(t, s.Set("fixture", "value"), CodeBackendRequired)
		_, _, err := s.Get("fixture")
		requireCode(t, err, CodeBackendRequired)
		_, err = s.Delete("fixture")
		requireCode(t, err, CodeBackendRequired)
	}
}
