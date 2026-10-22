package vaultread

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"careerhub.local/backend/internal/cataloginput"
)

func TestProfileKeepsUnknownFieldsKeyOrderAndExactRevision(t *testing.T) {
	f := validFixture()
	appendYAML(profileIndex, "custom: {null: first, 'null': second, true: yes, 1: same-python-key, 2.0: float-key, 3: integer-key}\nunknown: [false, 1.25, 123456789012345678901234567890, null, '', '  spaced  ']\n")(f)
	state := fixtureState(t, f)
	payload, err := profilePayload(context.Background(), state)
	if err != nil || !json.Valid(payload) {
		t.Fatal("valid profile projection failed")
	}
	expectedRevision := fmt.Sprintf("sha256:%x", sha256.Sum256(f.files[schemas[profileIndex].path]))
	if !strings.Contains(string(payload), expectedRevision) ||
		!strings.Contains(string(payload), `"null":"first","null":"second","true":"same-python-key","2.0":"float-key","3":"integer-key"`) ||
		!strings.Contains(string(payload), `[false,1.25,123456789012345678901234567890,null,"","  spaced  "]`) {
		t.Fatal("ordered scalar keys, unknown values or raw revision changed")
	}
	if strings.Contains(string(payload), `"contact"`) {
		t.Fatal("absent contact default was inserted")
	}
}

func TestProfileRejectsUnserializableValuesWithoutDroppingFields(t *testing.T) {
	for _, raw := range []string{
		"unknown: 2026-01-01\n", "unknown: 2026-01-01T10:00:00Z\n",
		"unknown: !!binary c2VjcmV0\n", "unknown: .nan\n", "unknown: .inf\n",
		"unknown: {2026-01-01: value}\n",
	} {
		f := validFixture()
		appendYAML(profileIndex, raw)(f)
		payload, err := profilePayload(context.Background(), fixtureState(t, f))
		if payload != nil || err == nil || err.Code != "VAULT_RESPONSE_UNSUPPORTED" {
			t.Fatal("unsupported profile field was emitted or dropped")
		}
	}
}

func TestProfileOutputBoundsAndCancellation(t *testing.T) {
	for _, value := range []cataloginput.Value{
		{Kind: "string", Text: strings.Repeat("x", MaxProfileBytes+1)},
		{Kind: "string", Text: strings.Repeat("\x00", MaxProfileBytes/6+1)},
		{Kind: "sequence", Items: []cataloginput.Value{{Kind: "string", Text: strings.Repeat("x", MaxProfileBytes-2)}, {Kind: "null"}}},
	} {
		encoder := profileEncoder{ctx: context.Background()}
		encoder.value(value)
		if encoder.failure == nil || encoder.failure.Code != "VAULT_RESPONSE_LIMIT" || len(encoder.output) > MaxProfileBytes {
			t.Fatal("response output exceeded bound or silently truncated")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	encoder := profileEncoder{ctx: ctx}
	encoder.value(cataloginput.Value{Kind: "null"})
	if encoder.failure == nil || encoder.failure.Code != "VAULT_READ_CANCELED" || len(encoder.output) != 0 {
		t.Fatal("canceled projection emitted response")
	}
}
