package emailcredentials

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

func sampleScopedIntent(operation string) ScopedIntent {
	before := RowWitness{Present: true, Revision: 4, Enabled: true, SecretName: scopedGolden}
	name, err := newScopedGenerationName(testHome, scopedFingerprint, bytes.NewReader(make([]byte, 16)))
	if err != nil {
		panic(err)
	}
	after := RowWitness{Present: true, Revision: 5, Enabled: true, SecretName: name}
	switch operation {
	case InitialConfigure:
		before = RowWitness{}
		after.Revision = 1
	case AdoptUnbound:
		before.SecretName = intentV2
	case RebindCopied:
		before.SecretName, err = newScopedGenerationName(testHome, strings.Repeat("f", 64), bytes.NewReader(make([]byte, 16)))
		if err != nil {
			panic(err)
		}
	case Disconnect:
		after.SecretName = before.SecretName
		after.Enabled = false
	case Forget:
		after = RowWitness{}
	}
	return ScopedIntent{Version: 2, Home: testHome, RootFingerprint: scopedFingerprint, Operation: operation, Before: before, After: after}
}

func rawScopedIntent(t *testing.T, intent ScopedIntent) []byte {
	t.Helper()
	payload, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	return wrapScopedPayload(t, payload)
}
func wrapScopedPayload(t *testing.T, payload []byte) []byte {
	t.Helper()
	raw, err := json.Marshal(intentEnvelope{Payload: payload, SHA256: digest(payload)})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestScopedIntentRoundTripAndLegacySeparation(t *testing.T) {
	for _, operation := range []string{InitialConfigure, Rotate, AdoptUnbound, RebindCopied, Disconnect, Forget} {
		intent := sampleScopedIntent(operation)
		raw, err := encodeScopedIntent(intent, scopedFingerprint)
		if err != nil || len(raw) > MaxIntentBytes {
			t.Fatal("encode failed", operation, err)
		}
		decoded, err := decodeScopedIntent(raw, testHome, scopedFingerprint)
		if err != nil || decoded != intent {
			t.Fatal("roundtrip failed", operation, err)
		}
		if decoded, err := DecodeIntent(raw, testHome); decoded != (Intent{}) || !errors.Is(err, ErrIntentInvalid) {
			t.Fatal("legacy decoder accepted scoped intent")
		}
	}
	intent := sampleScopedIntent(AdoptUnbound)
	intent.Before.SecretName = LegacyName
	if _, err := encodeScopedIntent(intent, scopedFingerprint); err != nil {
		t.Fatal("legacy metadata adoption refused")
	}
	for _, operation := range []string{InitialConfigure, Rotate, PromoteLegacy, Disconnect, Forget} {
		raw, err := EncodeIntent(sampleIntent(operation))
		if err != nil {
			t.Fatal(err)
		}
		if decoded, err := decodeScopedIntent(raw, testHome, scopedFingerprint); decoded != (ScopedIntent{}) || !errors.Is(err, ErrIntentInvalid) {
			t.Fatal("old journal accepted")
		}
	}
	var zero runtimepaths.CredentialScope
	if raw, err := EncodeScopedIntent(intent, zero); raw != nil || !errors.Is(err, ErrIntentInvalid) {
		t.Fatal("zero scope encoded")
	}
	if decoded, err := DecodeScopedIntent(rawScopedIntent(t, intent), testHome, zero); decoded != (ScopedIntent{}) || !errors.Is(err, ErrIntentInvalid) {
		t.Fatal("zero scope decoded")
	}
}

func TestScopedIntentRejectsInvalidTransitionsAndEachWitness(t *testing.T) {
	for _, operation := range []string{InitialConfigure, Rotate, AdoptUnbound, RebindCopied, Disconnect, Forget} {
		for _, change := range []func(*ScopedIntent){
			func(p *ScopedIntent) { p.Version = 1 },
			func(p *ScopedIntent) { p.Operation = PromoteLegacy },
			func(p *ScopedIntent) { p.RootFingerprint = strings.Repeat("f", 64) },
			func(p *ScopedIntent) { p.Home.Channel = "beta" },
			func(p *ScopedIntent) { p.Before.Revision = -1 },
			func(p *ScopedIntent) { p.After.Revision = MaxRevision + 1 },
			func(p *ScopedIntent) { p.Before.SecretName = "arbitrary-target" },
			func(p *ScopedIntent) { p.After.SecretName = LegacyName },
			func(p *ScopedIntent) { p.After.SecretName = intentV2 },
			func(p *ScopedIntent) { p.After.Revision++ },
		} {
			intent := sampleScopedIntent(operation)
			change(&intent)
			if raw, err := encodeScopedIntent(intent, scopedFingerprint); raw != nil || !errors.Is(err, ErrIntentInvalid) {
				t.Fatal("invalid transition encoded", operation)
			}
			if decoded, err := decodeScopedIntent(rawScopedIntent(t, intent), testHome, scopedFingerprint); decoded != (ScopedIntent{}) || err == nil {
				t.Fatal("checksummed invalid transition decoded", operation)
			}
		}
	}
	for _, operation := range []string{Rotate, Disconnect, Forget} {
		for _, old := range []string{LegacyName, intentV2} {
			intent := sampleScopedIntent(operation)
			intent.Before.SecretName = old
			if validScopedIntent(intent, scopedFingerprint) {
				t.Fatal("unbound before target admitted outside adoption")
			}
		}
	}
	for _, change := range []func(*ScopedIntent){
		func(p *ScopedIntent) { p.After.SecretName = p.Before.SecretName },
		func(p *ScopedIntent) { p.After.Enabled = false },
		func(p *ScopedIntent) { p.Before.Present = false },
		func(p *ScopedIntent) { p.Before.Revision = MaxRevision },
	} {
		intent := sampleScopedIntent(Rotate)
		change(&intent)
		if validScopedIntent(intent, scopedFingerprint) {
			t.Fatal("invalid rotation admitted")
		}
	}
	intent := sampleScopedIntent(AdoptUnbound)
	intent.Before.SecretName = scopedGolden
	if validScopedIntent(intent, scopedFingerprint) {
		t.Fatal("adoption accepts a v3 before target")
	}
	intent = sampleScopedIntent(Disconnect)
	intent.Before.Enabled = false
	if validScopedIntent(intent, scopedFingerprint) {
		t.Fatal("disabled disconnect admitted")
	}
	// Replacing the declared scope and recomputing the checksum must not admit
	// either old-root witness under a new root's authority.
	other := strings.Repeat("f", 64)
	otherName, _ := newScopedGenerationName(testHome, other, bytes.NewReader(make([]byte, 16)))
	for _, beforeForeign := range []bool{true, false} {
		intent := sampleScopedIntent(Rotate)
		intent.RootFingerprint = other
		if beforeForeign {
			intent.After.SecretName = otherName
		} else {
			intent.Before.SecretName = otherName
		}
		if decoded, err := decodeScopedIntent(rawScopedIntent(t, intent), testHome, other); decoded != (ScopedIntent{}) || !errors.Is(err, ErrIntentInvalid) {
			t.Fatal("foreign v3 witness trusted")
		}
	}
}

func TestScopedIntentRejectsCorruptionAndCopiedJournal(t *testing.T) {
	intent := sampleScopedIntent(Rotate)
	raw := rawScopedIntent(t, intent)
	payload, _ := json.Marshal(intent)
	for _, bad := range [][]byte{
		nil, bytes.Repeat([]byte{' '}, MaxIntentBytes+1), append([]byte(" "), raw...), append(bytes.Clone(raw), []byte("{}")...),
		bytes.Replace(raw, []byte(`"sha256":"`), []byte(`"sha256":"0`), 1),
		bytes.Replace(raw, []byte(`{"payload":`), []byte(`{"payload":null,"payload":`), 1),
		wrapScopedPayload(t, bytes.Replace(payload, []byte(`{"version":2,`), []byte(`{"version":2,"version":2,`), 1)),
		wrapScopedPayload(t, bytes.Replace(payload, []byte(`{"version":2,`), []byte(`{"password":"forbidden","version":2,`), 1)),
		wrapScopedPayload(t, bytes.Replace(payload, []byte(`"root_fingerprint":`), []byte(`"root_fingerprint":"","root_fingerprint":`), 1)),
	} {
		if decoded, err := decodeScopedIntent(bad, testHome, scopedFingerprint); decoded != (ScopedIntent{}) || !errors.Is(err, ErrIntentInvalid) {
			t.Fatal("corrupt/noncanonical intent admitted")
		}
	}
	if decoded, err := decodeScopedIntent(raw, testHome, strings.Repeat("f", 64)); decoded != (ScopedIntent{}) || !errors.Is(err, ErrIntentScopeChanged) {
		t.Fatal("copied journal admitted")
	}
	other := testHome
	other.DataHomeID = "dh_ffffffffffffffffffffffffffffffff"
	if decoded, err := decodeScopedIntent(raw, other, scopedFingerprint); decoded != (ScopedIntent{}) || !errors.Is(err, ErrIntentHomeChanged) {
		t.Fatal("foreign Home admitted")
	}
}

func FuzzScopedIntentNeverReturnsInvalidRecord(f *testing.F) {
	raw, _ := encodeScopedIntent(sampleScopedIntent(Rotate), scopedFingerprint)
	f.Add(raw)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		intent, err := decodeScopedIntent(raw, testHome, scopedFingerprint)
		if err == nil && !validScopedIntent(intent, scopedFingerprint) {
			t.Fatal("invalid record returned")
		}
		if err != nil && intent != (ScopedIntent{}) {
			t.Fatal("error exposed witnesses")
		}
	})
}
