package emailcredentials

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"careerhub.local/backend/internal/storage/datahome"
)

var testHome = datahome.Info{
	FormatVersion: 2,
	Channel:       "dev",
	Initialized:   true,
	DataHomeID:    "dh_0123456789abcdef0123456789abcdef",
}

func TestClassifyExactTargets(t *testing.T) {
	const v2 = "email.v2.0123456789abcdef0123456789abcdef.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, test := range []struct {
		name string
		kind Kind
	}{
		{LegacyName, Legacy},
		{v2, HomeBound},
	} {
		kind, err := Classify(testHome, test.name)
		if err != nil || kind != test.kind {
			t.Fatalf("classify %q: kind=%d err=%v", test.name, kind, err)
		}
	}
	for _, name := range []string{
		"", "another-target", "CareerHub/dev/provider/" + LegacyName,
		"email.v2.ffffffffffffffffffffffffffffffff.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"email.v2.0123456789abcdef0123456789abcdef.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"email.v2.0123456789abcdef0123456789abcdef.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		v2 + "a", v2 + "/other", strings.Replace(v2, ".", "_", 1),
	} {
		if kind, err := Classify(testHome, name); kind != 0 || !errors.Is(err, ErrInvalidTarget) {
			t.Fatalf("admitted malformed target %q: kind=%d err=%v", name, kind, err)
		}
	}
}

func TestClassifyRejectsInvalidHome(t *testing.T) {
	for _, change := range []func(*datahome.Info){
		func(h *datahome.Info) { h.FormatVersion = 1 },
		func(h *datahome.Info) { h.Initialized = false },
		func(h *datahome.Info) { h.Channel = "test" },
		func(h *datahome.Info) { h.DataHomeID = "dh_ABCDEF0123456789abcdef0123456789" },
		func(h *datahome.Info) { h.DataHomeID = "dh_0123456789abcdef0123456789abcde" },
	} {
		home := testHome
		change(&home)
		if kind, err := Classify(home, LegacyName); kind != 0 || !errors.Is(err, ErrInvalidTarget) {
			t.Fatalf("admitted invalid Home: kind=%d err=%v", kind, err)
		}
	}
}

func TestNewGenerationName(t *testing.T) {
	name, err := newGenerationName(testHome, bytes.NewReader(bytes.Repeat([]byte{0xab}, 16)))
	if err != nil || name != "email.v2.0123456789abcdef0123456789abcdef.abababababababababababababababab" || len(name) != 74 {
		t.Fatalf("generated target: %q %v", name, err)
	}
	if kind, err := Classify(testHome, name); err != nil || kind != HomeBound {
		t.Fatalf("generated target not admitted: kind=%d err=%v", kind, err)
	}
	for _, entropy := range []interface{ Read([]byte) (int, error) }{nil, bytes.NewReader([]byte{1})} {
		if got, err := newGenerationName(testHome, entropy); got != "" || !errors.Is(err, ErrGenerationFailed) {
			t.Fatalf("accepted missing entropy: %q %v", got, err)
		}
	}
	badHome := testHome
	badHome.Channel = "other"
	if got, err := newGenerationName(badHome, bytes.NewReader(bytes.Repeat([]byte{1}, 16))); got != "" || !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("generated target for invalid Home: %q %v", got, err)
	}
}
