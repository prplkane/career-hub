package emailcredentials

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/secretstore"
)

const scopedFingerprint = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

// Computed independently with Python hashlib/struct/base64, not this codec.
const scopedGolden = "email.v3.owP9PQ4uUz_2FPEFEEDLRmrNXuJz3wmxwJwjhMz2Pbk.q6urq6urq6urq6urq6urqw"

func TestScopedTargetGoldenAndStoreFacade(t *testing.T) {
	binding, ok := scopedBinding(testHome, scopedFingerprint)
	if !ok || hex.EncodeToString(binding[:]) != "a303fd3d0e2e533ff614f1051040cb466acd5ee273df09b1c09c2384ccf63db9" {
		t.Fatal("binding frame changed")
	}
	name, err := newScopedGenerationName(testHome, scopedFingerprint, bytes.NewReader(bytes.Repeat([]byte{0xab}, 16)))
	if err != nil || name != scopedGolden || len(name) != 75 || validateScopedTarget(testHome, scopedFingerprint, name) != nil {
		t.Fatal("v3 golden or width changed")
	}
	// Exercise the real facade with an inert backend, not Credential Manager.
	backend := &scopedRecordingBackend{}
	store, err := secretstore.New("dev", backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Get(name); err != nil || backend.target != "CareerHub/dev/provider/"+name {
		t.Fatal("v3 no longer fits the existing facade")
	}
	if _, err := Classify(testHome, name); !errors.Is(err, ErrInvalidTarget) {
		t.Fatal("v3 accidentally admitted by legacy codec")
	}
}

type scopedRecordingBackend struct{ target string }

func (b *scopedRecordingBackend) Read(target string) ([]byte, bool, error) {
	b.target = target
	return nil, false, nil
}
func (*scopedRecordingBackend) Write(string, []byte) error  { panic("unexpected write") }
func (*scopedRecordingBackend) Delete(string) (bool, error) { panic("unexpected delete") }

func TestScopedTargetRejectsAliasesAndForeignBindings(t *testing.T) {
	for _, name := range []string{
		"", LegacyName, intentV2, scopedGolden + "=", scopedGolden + ".x", scopedGolden[1:],
		strings.Replace(scopedGolden, "email.v3.", "Email.v3.", 1),
		strings.Replace(scopedGolden, "_", "/", 1),
		strings.Replace(scopedGolden, "Pbk.", "Pbl.", 1), // Nonzero unused binding bits.
		scopedGolden[:len(scopedGolden)-1] + "x",         // Nonzero unused nonce bits.
		scopedGolden[:len(scopedGolden)-1] + "\n",
		strings.Replace(scopedGolden, ".q6", ".=6", 1),
		strings.Replace(scopedGolden, ".q6", "_q6", 1),
		strings.Replace(scopedGolden, ".ow", ".pw", 1),
	} {
		if err := validateScopedTarget(testHome, scopedFingerprint, name); !errors.Is(err, ErrInvalidTarget) {
			t.Fatalf("malformed target admitted: %q", name)
		}
	}
	for _, change := range []func(*datahome.Info){
		func(h *datahome.Info) { h.Channel = "beta" },
		func(h *datahome.Info) { h.Channel = "stable" },
		func(h *datahome.Info) { h.DataHomeID = "dh_ffffffffffffffffffffffffffffffff" },
		func(h *datahome.Info) { h.Initialized = false },
		func(h *datahome.Info) { h.FormatVersion = 1 },
		func(h *datahome.Info) { h.Channel = "DEV" },
	} {
		home := testHome
		change(&home)
		if err := validateScopedTarget(home, scopedFingerprint, scopedGolden); !errors.Is(err, ErrInvalidTarget) {
			t.Fatal("foreign Home admitted")
		}
	}
	for _, fingerprint := range []string{"", strings.ToUpper(scopedFingerprint), scopedFingerprint + "0", strings.Repeat("g", 64), strings.Repeat("f", 64)} {
		if err := validateScopedTarget(testHome, fingerprint, scopedGolden); !errors.Is(err, ErrInvalidTarget) {
			t.Fatal("foreign/invalid root admitted")
		}
	}
}

func TestScopedGenerationRefusesInvalidScopeAndEntropy(t *testing.T) {
	var zero runtimepaths.CredentialScope
	if err := ValidateScopedTarget(testHome, zero, scopedGolden); !errors.Is(err, ErrInvalidTarget) {
		t.Fatal("zero scope admitted")
	}
	if name, err := NewScopedGenerationName(testHome, zero); name != "" || !errors.Is(err, ErrInvalidTarget) {
		t.Fatal("zero scope generated target")
	}
	for _, entropy := range []io.Reader{nil, bytes.NewReader(make([]byte, 15)), scopedErrorReader{}} {
		if name, err := newScopedGenerationName(testHome, scopedFingerprint, entropy); name != "" || !errors.Is(err, ErrGenerationFailed) {
			t.Fatal("missing entropy admitted")
		}
	}
	if name, err := newScopedGenerationName(testHome, "", scopedErrorReader{}); name != "" || !errors.Is(err, ErrInvalidTarget) {
		t.Fatal("invalid scope read entropy")
	}
	for _, channel := range []string{"dev", "stable"} {
		home := testHome
		home.Channel = channel
		name, err := newScopedGenerationName(home, scopedFingerprint, bytes.NewReader(make([]byte, 16)))
		if err != nil || validateScopedTarget(home, scopedFingerprint, name) != nil {
			t.Fatal("valid channel rejected")
		}
	}
}

type scopedErrorReader struct{}

func (scopedErrorReader) Read([]byte) (int, error) { return 0, errors.New("private entropy error") }

func FuzzScopedTargetNeverPanics(f *testing.F) {
	for _, seed := range []string{scopedGolden, LegacyName, "", scopedGolden + "="} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if validateScopedTarget(testHome, scopedFingerprint, name) == nil && len(name) != 75 {
			t.Fatal("accepted invalid width")
		}
	})
}
