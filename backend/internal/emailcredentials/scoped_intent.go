package emailcredentials

import (
	"encoding/json"
	"errors"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
)

// AdoptUnbound records an explicit fresh-password replacement of an exact
// legacy/v2 row. Before is CAS metadata only: never read/delete its credential.
const AdoptUnbound = "adopt_unbound"

// RebindCopied switches an exact foreign-root v3 row to a fresh local target.
// Like adoption, the old pointer is CAS metadata only, never store authority.
const RebindCopied = "rebind_copied"

var ErrIntentScopeChanged = errors.New("EMAIL_CREDENTIAL_INTENT_SCOPE_CHANGED")

// ScopedIntent is deliberately not Intent: the legacy recovery planner cannot
// consume it accidentally. Version 2 is canonical and scoped to the current
// held root; the checksum detects corruption, not hostile journal forgery.
type ScopedIntent struct {
	Version         int           `json:"version"`
	Home            datahome.Info `json:"home"`
	RootFingerprint string        `json:"root_fingerprint"`
	Operation       string        `json:"operation"`
	Before          RowWitness    `json:"before"`
	After           RowWitness    `json:"after"`
}

// EncodeScopedIntent validates the explicit journal fingerprint against an
// independent native observation before returning bytes. It performs no writes.
func EncodeScopedIntent(intent ScopedIntent, scope runtimepaths.CredentialScope) ([]byte, error) {
	if !scope.Valid() {
		return nil, ErrIntentInvalid
	}
	return encodeScopedIntent(intent, scope.Fingerprint())
}

// DecodeScopedIntent never falls back to v1 journals or the journal's own root
// fingerprint as authority. Local v3 witnesses must match the expected Home and
// scope. Only rebind_copied permits a foreign v3 before-pointer as inert metadata.
func DecodeScopedIntent(raw []byte, home datahome.Info, scope runtimepaths.CredentialScope) (ScopedIntent, error) {
	if !scope.Valid() {
		return ScopedIntent{}, ErrIntentInvalid
	}
	return decodeScopedIntent(raw, home, scope.Fingerprint())
}

func encodeScopedIntent(intent ScopedIntent, fingerprint string) ([]byte, error) {
	if !validScopedIntent(intent, fingerprint) {
		return nil, ErrIntentInvalid
	}
	payload, err := json.Marshal(intent)
	if err != nil {
		return nil, ErrIntentInvalid
	}
	raw, err := json.Marshal(intentEnvelope{Payload: payload, SHA256: digest(payload)})
	if err != nil || len(raw) > MaxIntentBytes {
		return nil, ErrIntentInvalid
	}
	return raw, nil
}

func decodeScopedIntent(raw []byte, home datahome.Info, fingerprint string) (ScopedIntent, error) {
	var envelope intentEnvelope
	var intent ScopedIntent
	if !validHome(home) || !canonicalFingerprint(fingerprint) || len(raw) > MaxIntentBytes ||
		!canonicalJSON(raw, &envelope) || envelope.SHA256 != digest(envelope.Payload) ||
		!canonicalJSON(envelope.Payload, &intent) || intent.Version != 2 ||
		!validHome(intent.Home) || !canonicalFingerprint(intent.RootFingerprint) {
		return ScopedIntent{}, ErrIntentInvalid
	}
	if intent.Home != home {
		return ScopedIntent{}, ErrIntentHomeChanged
	}
	if intent.RootFingerprint != fingerprint {
		return ScopedIntent{}, ErrIntentScopeChanged
	}
	if !validScopedIntent(intent, fingerprint) {
		return ScopedIntent{}, ErrIntentInvalid
	}
	return intent, nil
}

func validScopedIntent(intent ScopedIntent, fingerprint string) bool {
	if intent.Version != 2 || !validHome(intent.Home) || !canonicalFingerprint(fingerprint) ||
		intent.RootFingerprint != fingerprint || !validScopedWitness(intent.Home, fingerprint, intent.After) {
		return false
	}
	before, after := intent.Before, intent.After
	if intent.Operation == AdoptUnbound || intent.Operation == RebindCopied {
		// Old codecs are used only to validate inert CAS metadata. No old target
		// is exposed as a scoped credential. Rebinding requires a foreign, not
		// current, canonical v3 pointer and cannot be confused with rotation.
		validBefore := validWitness(intent.Home, before)
		if intent.Operation == RebindCopied {
			validBefore = before.Revision >= 1 && before.Revision <= MaxRevision &&
				canonicalScopedName(before.SecretName) && validateScopedTarget(intent.Home, fingerprint, before.SecretName) != nil
		}
		return before.Present && validBefore && after.Present && after.Enabled &&
			before.Revision < MaxRevision && after.Revision == before.Revision+1
	}
	if !validScopedWitness(intent.Home, fingerprint, before) {
		return false
	}
	switch intent.Operation {
	case InitialConfigure:
		return !before.Present && after.Present && after.Enabled && after.Revision == 1
	case Rotate:
		return before.Present && after.Present && after.Enabled && before.Revision < MaxRevision &&
			after.Revision == before.Revision+1 && before.SecretName != after.SecretName
	case Disconnect:
		return before.Present && before.Enabled && after.Present && !after.Enabled &&
			before.Revision < MaxRevision && after.Revision == before.Revision+1 && before.SecretName == after.SecretName
	case Forget:
		return before.Present && !after.Present
	default:
		return false
	}
}

func validScopedWitness(home datahome.Info, fingerprint string, witness RowWitness) bool {
	if !witness.Present {
		return witness == (RowWitness{})
	}
	return witness.Revision >= 1 && witness.Revision <= MaxRevision &&
		validateScopedTarget(home, fingerprint, witness.SecretName) == nil
}
