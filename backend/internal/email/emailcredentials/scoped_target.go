package emailcredentials

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"io"
	"strings"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const (
	v3Prefix       = "email.v3."
	v3BindingWidth = 43
	v3NonceWidth   = 22
)

// ValidateScopedTarget admits only v3 names bound to the supplied Home and
// native root observation. Legacy/v2 names are never store targets here.
// A scope is not a live capability: callers must re-observe the held root at
// each external-effect boundary. This codec performs no I/O or ownership claim.
func ValidateScopedTarget(home datahome.Info, scope runtimepaths.CredentialScope, name string) error {
	if !scope.Valid() {
		return ErrInvalidTarget
	}
	return validateScopedTarget(home, scope.Fingerprint(), name)
}

// RequiresReauthorization distinguishes inert reconnect metadata from a
// current-root target. True never authorizes a credential read or retirement.
// Only exact legacy/same-Home v2 and canonical foreign v3 pointers are eligible.
func RequiresReauthorization(home datahome.Info, scope runtimepaths.CredentialScope, name string) (bool, error) {
	if !scope.Valid() || !validHome(home) {
		return false, ErrInvalidTarget
	}
	if validateScopedTarget(home, scope.Fingerprint(), name) == nil {
		return false, nil
	}
	if _, err := Classify(home, name); err == nil || canonicalScopedName(name) {
		return true, nil
	}
	return false, ErrInvalidTarget
}

// NewScopedGenerationName uses 128 bits of cryptographic entropy. Collision
// checks and durable publication/recovery remain the owning service's job.
func NewScopedGenerationName(home datahome.Info, scope runtimepaths.CredentialScope) (string, error) {
	if !scope.Valid() {
		return "", ErrInvalidTarget
	}
	return newScopedGenerationName(home, scope.Fingerprint(), rand.Reader)
}

func newScopedGenerationName(home datahome.Info, fingerprint string, entropy io.Reader) (string, error) {
	binding, ok := scopedBinding(home, fingerprint)
	if !ok {
		return "", ErrInvalidTarget
	}
	if entropy == nil {
		return "", ErrGenerationFailed
	}
	var nonce [16]byte
	if _, err := io.ReadFull(entropy, nonce[:]); err != nil {
		return "", ErrGenerationFailed
	}
	return v3Prefix + base64.RawURLEncoding.EncodeToString(binding[:]) + "." + base64.RawURLEncoding.EncodeToString(nonce[:]), nil
}

func validateScopedTarget(home datahome.Info, fingerprint, name string) error {
	binding, ok := scopedBinding(home, fingerprint)
	const separator = len(v3Prefix) + v3BindingWidth
	if !ok || !canonicalScopedName(name) {
		return ErrInvalidTarget
	}
	encodedBinding := name[len(v3Prefix):separator]
	if encodedBinding != base64.RawURLEncoding.EncodeToString(binding[:]) {
		return ErrInvalidTarget
	}
	return nil
}

func canonicalScopedName(name string) bool {
	const separator = len(v3Prefix) + v3BindingWidth
	return len(name) == separator+1+v3NonceWidth && strings.HasPrefix(name, v3Prefix) && name[separator] == '.' &&
		canonicalBase64URL(name[len(v3Prefix):separator], 32) && canonicalBase64URL(name[separator+1:], 16)
}

// Private pure codec seam; never accept a persisted fingerprint as authority
// at a public entry point. Public callers must supply a native CredentialScope.
func scopedBinding(home datahome.Info, fingerprint string) ([32]byte, bool) {
	if !validHome(home) || !canonicalFingerprint(fingerprint) {
		return [32]byte{}, false
	}
	homeID, _ := hex.DecodeString(home.DataHomeID[3:])
	rootDigest, _ := hex.DecodeString(fingerprint)
	// Exact frame: domain NUL, raw HomeID16, channel-byte-count LE32,
	// canonical ASCII channel, full root digest32. No truncated identities.
	frame := []byte("CareerHub.EmailCredentialBinding.v3\x00")
	frame = append(frame, homeID...)
	frame = binary.LittleEndian.AppendUint32(frame, uint32(len(home.Channel)))
	frame = append(frame, home.Channel...)
	frame = append(frame, rootDigest...)
	return sha256.Sum256(frame), true
}

func canonicalFingerprint(value string) bool {
	return len(value) == 64 && lowerHex(value[:32]) && lowerHex(value[32:])
}

func canonicalBase64URL(value string, size int) bool {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil && len(raw) == size && base64.RawURLEncoding.EncodeToString(raw) == value
}
