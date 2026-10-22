package runtimepaths

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"
)

const scopeFixturePath = `\\?\Volume{12345678-1234-5678-90ab-123456789abc}\Synthetic`

func scopeFixtureID() (id [16]byte) {
	for i := range id {
		id[i] = byte(i + 1)
	}
	return
}

func TestCredentialScopeExactVersionedFrameAndIsolation(t *testing.T) {
	path := utf16.Encode([]rune(scopeFixturePath))
	id := scopeFixtureID()
	scope, err := credentialScopeDigest(0x0102030405060708, id, path)
	// Independently calculated with Python hashlib/struct and utf-16-le.
	if err != nil || !scope.Valid() || scope.Fingerprint() != "9c654a68771ca4fa8a63dfb0e092eee87006f3adca1b02281d5614b0a22f045f" {
		t.Fatal("credential root frame changed")
	}
	for _, change := range []int{0, 1, 2} {
		serial, otherID, otherPath := uint64(0x0102030405060708), id, append([]uint16(nil), path...)
		switch change {
		case 0:
			serial++
		case 1:
			otherID[15]++
		case 2:
			otherPath[len(otherPath)-1]++
		}
		other, err := credentialScopeDigest(serial, otherID, otherPath)
		if err != nil || scope.Equal(other) {
			t.Fatal("physical identity components collided")
		}
	}
	if !scope.Equal(scope) || scope.Equal(CredentialScope{}) || (CredentialScope{}).Valid() || (CredentialScope{}).Fingerprint() != "" {
		t.Fatal("zero/unbound scope admitted")
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		if fmt.Sprintf(format, scope) != "[credential root scope]" {
			t.Fatal("scope diagnostic disclosure")
		}
	}
}

func TestCredentialScopeRejectsUnqualifiedIdentityAndPath(t *testing.T) {
	id := scopeFixtureID()
	for _, path := range [][]uint16{
		nil, utf16.Encode([]rune(`C:\Synthetic`)),
		utf16.Encode([]rune(strings.Replace(scopeFixturePath, "12345678", "notaguid", 1))),
		append(utf16.Encode([]rune(scopeFixturePath)), 0),
		append(utf16.Encode([]rune(scopeFixturePath)), 0xd800),
		append(utf16.Encode([]rune(scopeFixturePath)), 0xdc00),
		make([]uint16, maxCredentialPathUnits),
	} {
		if scope, e := credentialScopeDigest(1, id, path); e == nil || scope.Valid() {
			t.Fatal("invalid root scope admitted")
		}
	}
	if scope, e := credentialScopeDigest(1, [16]byte{}, utf16.Encode([]rune(scopeFixturePath))); e == nil || scope.Valid() {
		t.Fatal("zero file ID admitted")
	}
	if scope, e := credentialScopeDigest(1, id, utf16.Encode([]rune(scopeFixturePath+"😀"))); e != nil || !scope.Valid() {
		t.Fatal("valid UTF-16 pair refused")
	}
}
