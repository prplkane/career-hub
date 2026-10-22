package runtimepaths

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

// CredentialScope is an immutable observation of a held physical root, not a
// credential capability or proof of hostile-clone authenticity. Only the native
// provider creates valid values. Callers must re-observe it at effect boundaries.
type CredentialScope struct {
	digest [32]byte
	valid  bool
}

func (s CredentialScope) Valid() bool { return s.valid }
func (s CredentialScope) Fingerprint() string {
	if !s.valid {
		return ""
	}
	return hex.EncodeToString(s.digest[:])
}
func (s CredentialScope) Equal(other CredentialScope) bool {
	return s.valid && other.valid && s.digest == other.digest
}
func (CredentialScope) String() string { return "[credential root scope]" }
func (CredentialScope) Format(w fmt.State, _ rune) {
	_, _ = io.WriteString(w, "[credential root scope]")
}

// CredentialScope never acquires ownership or opens a path to derive identity.
// It checks the existing process claim and pinned path chain before and after
// querying the claim's terminal handle. Closing this Root invalidates new reads.
func (r *Root) CredentialScope() (CredentialScope, *Error) {
	if !ownershipSupported {
		return CredentialScope{}, failure("CREDENTIAL_SCOPE_UNSUPPORTED")
	}
	if r == nil {
		return CredentialScope{}, failure("ROOT_CLOSED")
	}
	processOwnership.Lock()
	claim := processOwnership.held
	processOwnership.Unlock()
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed || r.base == nil {
		return CredentialScope{}, failure("ROOT_CLOSED")
	}
	if e := checkBindings(r.bindings); e != nil {
		return CredentialScope{}, e
	}
	if !processRootOwned(claim, r) {
		return CredentialScope{}, failure("OWNERSHIP_REQUIRED")
	}
	scope, e := credentialScopeForClaim(claim)
	if e != nil {
		return CredentialScope{}, e
	}
	if e := checkBindings(r.bindings); e != nil {
		return CredentialScope{}, e
	}
	if !processRootOwned(claim, r) {
		return CredentialScope{}, failure("OWNERSHIP_REQUIRED")
	}
	return scope, nil
}

const maxCredentialPathUnits = 32768

func credentialScopeDigest(serial uint64, fileID [16]byte, finalPath []uint16) (CredentialScope, *Error) {
	if fileID == [16]byte{} || len(finalPath) == 0 || len(finalPath) >= maxCredentialPathUnits {
		return CredentialScope{}, failure("CREDENTIAL_SCOPE_UNAVAILABLE")
	}
	for i := 0; i < len(finalPath); i++ {
		unit := finalPath[i]
		if unit == 0 || unit >= 0xdc00 && unit <= 0xdfff {
			return CredentialScope{}, failure("CREDENTIAL_SCOPE_UNAVAILABLE")
		}
		if unit >= 0xd800 && unit <= 0xdbff {
			i++
			if i == len(finalPath) || finalPath[i] < 0xdc00 || finalPath[i] > 0xdfff {
				return CredentialScope{}, failure("CREDENTIAL_SCOPE_UNAVAILABLE")
			}
		}
	}
	path := string(utf16.Decode(finalPath))
	const prefix = `\\?\Volume{`
	if !strings.HasPrefix(path, prefix) || len(path) < len(prefix)+38 || path[len(prefix)+36:len(prefix)+38] != `}\` {
		return CredentialScope{}, failure("CREDENTIAL_SCOPE_UNAVAILABLE")
	}
	for i, c := range path[len(prefix) : len(prefix)+36] {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c == '-' {
				continue
			}
		} else if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' {
			continue
		}
		return CredentialScope{}, failure("CREDENTIAL_SCOPE_UNAVAILABLE")
	}
	// Versioned, unambiguous byte frame: domain NUL, serial LE64, FILE_ID_128
	// bytes, path code-unit count LE32, then exact normalized UTF-16LE units.
	// No Unicode/case/path rewriting: uncertain aliases reconnect conservatively.
	frame := []byte("CareerHub.CredentialRoot.v1\x00")
	frame = binary.LittleEndian.AppendUint64(frame, serial)
	frame = append(frame, fileID[:]...)
	frame = binary.LittleEndian.AppendUint32(frame, uint32(len(finalPath)))
	for _, unit := range finalPath {
		frame = binary.LittleEndian.AppendUint16(frame, unit)
	}
	return CredentialScope{digest: sha256.Sum256(frame), valid: true}, nil
}
