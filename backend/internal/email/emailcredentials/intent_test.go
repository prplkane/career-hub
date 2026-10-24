package emailcredentials

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const intentV2 = "email.v2.0123456789abcdef0123456789abcdef.abababababababababababababababab"

func sampleIntent(operation string) Intent {
	before := RowWitness{Present: true, Revision: 4, Enabled: true, SecretName: LegacyName}
	after := RowWitness{Present: true, Revision: 5, Enabled: false, SecretName: LegacyName}
	switch operation {
	case InitialConfigure:
		before = RowWitness{}
		after = RowWitness{Present: true, Revision: 1, Enabled: true, SecretName: intentV2}
	case Rotate:
		before.Enabled = false
		after = RowWitness{Present: true, Revision: 5, Enabled: true, SecretName: intentV2}
	case PromoteLegacy:
		after = RowWitness{Present: true, Revision: 5, Enabled: true, SecretName: intentV2}
	case Forget:
		before.Enabled = false
		after = RowWitness{}
	}
	return Intent{Version: 1, Home: testHome, Operation: operation, Before: before, After: after}
}

func TestIntentRoundTripsExactOperations(t *testing.T) {
	for _, operation := range []string{InitialConfigure, Rotate, PromoteLegacy, Disconnect, Forget} {
		intent := sampleIntent(operation)
		raw, err := EncodeIntent(intent)
		if err != nil || len(raw) == 0 || len(raw) > MaxIntentBytes {
			t.Fatalf("encode %s: %v", operation, err)
		}
		decoded, err := DecodeIntent(raw, testHome)
		if err != nil || decoded != intent {
			t.Fatalf("decode %s: %#v %v", operation, decoded, err)
		}
		if bytes.Contains(raw, []byte("account_address")) || bytes.Contains(raw, []byte("password")) {
			t.Fatal("intent contains an account or credential field")
		}
	}
}

func TestIntentRejectsImpossibleWitnesses(t *testing.T) {
	for _, test := range []struct {
		name   string
		intent Intent
	}{
		{"version", func() Intent { p := sampleIntent(Rotate); p.Version = 2; return p }()},
		{"unknown-operation", func() Intent { p := sampleIntent(Rotate); p.Operation = "save"; return p }()},
		{"foreign-home", func() Intent {
			p := sampleIntent(Rotate)
			p.Home.DataHomeID = "dh_ffffffffffffffffffffffffffffffff"
			return p
		}()},
		{"configure-before-present", func() Intent { p := sampleIntent(InitialConfigure); p.Before.Present = true; return p }()},
		{"configure-v1", func() Intent { p := sampleIntent(InitialConfigure); p.After.SecretName = LegacyName; return p }()},
		{"configure-disabled", func() Intent { p := sampleIntent(InitialConfigure); p.After.Enabled = false; return p }()},
		{"rotate-same-target", func() Intent { p := sampleIntent(Rotate); p.After.SecretName = LegacyName; return p }()},
		{"rotate-no-revision", func() Intent { p := sampleIntent(Rotate); p.After.Revision = 4; return p }()},
		{"rotate-v1-target", func() Intent {
			p := sampleIntent(Rotate)
			p.Before.SecretName = intentV2
			p.After.SecretName = LegacyName
			return p
		}()},
		{"promote-no-row", func() Intent { p := sampleIntent(PromoteLegacy); p.Before = RowWitness{}; return p }()},
		{"promote-v2-before", func() Intent { p := sampleIntent(PromoteLegacy); p.Before.SecretName = intentV2; return p }()},
		{"promote-v1-after", func() Intent { p := sampleIntent(PromoteLegacy); p.After.SecretName = LegacyName; return p }()},
		{"promote-disabled-after", func() Intent { p := sampleIntent(PromoteLegacy); p.After.Enabled = false; return p }()},
		{"promote-no-revision", func() Intent { p := sampleIntent(PromoteLegacy); p.After.Revision = 4; return p }()},
		{"disconnect-changed-target", func() Intent { p := sampleIntent(Disconnect); p.After.SecretName = intentV2; return p }()},
		{"disconnect-already-disabled", func() Intent { p := sampleIntent(Disconnect); p.Before.Enabled = false; return p }()},
		{"forget-still-present", func() Intent { p := sampleIntent(Forget); p.After = p.Before; return p }()},
		{"absent-retained-name", func() Intent { p := sampleIntent(Forget); p.After.SecretName = LegacyName; return p }()},
		{"revision-limit", func() Intent {
			p := sampleIntent(Rotate)
			p.Before.Revision = MaxRevision
			p.After.Revision = MaxRevision + 1
			return p
		}()},
	} {
		t.Run(test.name, func(t *testing.T) {
			if raw, err := EncodeIntent(test.intent); raw != nil || !errors.Is(err, ErrIntentInvalid) {
				t.Fatalf("admitted impossible intent: %q %v", raw, err)
			}
		})
	}
}

func TestIntentDecodeRejectsCorruptionAndForeignHome(t *testing.T) {
	raw, err := EncodeIntent(sampleIntent(Rotate))
	if err != nil {
		t.Fatal(err)
	}
	var envelope intentEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	brokenHash := envelope
	brokenHash.SHA256 = strings.Repeat("0", 64)
	brokenHashRaw, _ := json.Marshal(brokenHash)
	unknownField := envelope
	unknownField.Payload = bytes.Replace(unknownField.Payload, []byte(`{"version":1,`), []byte(`{"password":"forbidden","version":1,`), 1)
	unknownField.SHA256 = digest(unknownField.Payload)
	unknownFieldRaw, _ := json.Marshal(unknownField)
	duplicatePayload := envelope
	duplicatePayload.Payload = bytes.Replace(duplicatePayload.Payload, []byte(`{"version":1,`), []byte(`{"version":1,"version":1,`), 1)
	duplicatePayload.SHA256 = digest(duplicatePayload.Payload)
	duplicatePayloadRaw, _ := json.Marshal(duplicatePayload)
	for _, candidate := range [][]byte{
		nil, bytes.Repeat([]byte{' '}, MaxIntentBytes+1), append([]byte(" "), raw...),
		append(bytes.Clone(raw), []byte("{}")...),
		bytes.Replace(raw, []byte(`{"payload":`), []byte(`{"payload":null,"payload":`), 1),
		brokenHashRaw, unknownFieldRaw, duplicatePayloadRaw,
	} {
		if decoded, err := DecodeIntent(candidate, testHome); decoded != (Intent{}) || !errors.Is(err, ErrIntentInvalid) {
			t.Fatalf("admitted corrupt intent: %#v %v", decoded, err)
		}
	}
	other := testHome
	other.DataHomeID = "dh_ffffffffffffffffffffffffffffffff"
	if decoded, err := DecodeIntent(raw, other); decoded != (Intent{}) || !errors.Is(err, ErrIntentHomeChanged) {
		t.Fatalf("admitted foreign Home: %#v %v", decoded, err)
	}
}
