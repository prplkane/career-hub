package vaultread

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestOtherReadersDoNotInheritProfileOnlyContactCheck(t *testing.T) {
	f := validFixture()
	appendYAML(profileIndex, "contact: null\nunknown: 2026-01-01\n")(f)
	state := fixtureState(t, f)
	if payload, failure := profilePayload(context.Background(), state); payload != nil || failure == nil || failure.Code != "VAULT_RECORD_VALIDATION_FAILED" {
		t.Fatal("standalone profile contact check changed")
	}
	for _, read := range []func(context.Context, *vaultState) (json.RawMessage, *Error){eligibilityPayload, educationPayload} {
		payload, failure := read(context.Background(), state)
		if failure != nil || !json.Valid(payload) {
			t.Fatal("other projection inherited profile-only validation/JSON checks")
		}
	}
}

func TestReadModelRevisionsUseTheirExactSourceBytes(t *testing.T) {
	f := validFixture()
	appendYAML(eligibilityIndex, "# eligibility comment\r\n")(f)
	appendYAML(educationIndex, "# education comment\r\n")(f)
	state := fixtureState(t, f)
	for _, tc := range []struct {
		index int
		read  func(context.Context, *vaultState) (json.RawMessage, *Error)
	}{
		{eligibilityIndex, eligibilityPayload}, {educationIndex, educationPayload},
	} {
		payload, failure := tc.read(context.Background(), state)
		if failure != nil {
			t.Fatal("valid read model failed")
		}
		want := fmt.Sprintf("sha256:%x", sha256.Sum256(f.files[schemas[tc.index].path]))
		var value map[string]json.RawMessage
		if json.Unmarshal(payload, &value) != nil || string(value["revision"]) != fmt.Sprintf("%q", want) {
			t.Fatal("read model used the wrong source file revision")
		}
		f.files[schemas[tc.index].path][0] = 'X'
		if state.revisions[tc.index] != want {
			t.Fatal("read model revision aliases source memory")
		}
	}
}

func TestEducationDefaultsAndProjection(t *testing.T) {
	f := validFixture()
	f.files[schemas[educationIndex].path] = []byte("schools: []\nunknown_root: 2026-01-01\n")
	replaceYAML(skillIndex, "related_certifications: [cert]", "related_certifications: []")(f)
	state := fixtureState(t, f)
	payload, failure := educationPayload(context.Background(), state)
	if failure != nil || !strings.Contains(string(payload), `"schools":[],"certifications":[]`) ||
		strings.Contains(string(payload), "unknown_root") {
		t.Fatal("education absence/default/root projection changed")
	}
}

func TestOtherReadModelJSONFailureSourceAndValidation(t *testing.T) {
	for _, tc := range []struct {
		source string
		edit   func(*fixtureReader)
		read   func(context.Context, *vaultState) (json.RawMessage, *Error)
	}{
		{"eligibility", appendYAML(eligibilityIndex, "unknown: 2026-01-01\n"), eligibilityPayload},
		{"education", replaceYAML(educationIndex, "degree: BS", "degree: BS, unknown: 2026-01-01"), educationPayload},
	} {
		f := validFixture()
		tc.edit(f)
		state := fixtureState(t, f)
		payload, failure := tc.read(context.Background(), state)
		if payload != nil || failure == nil || failure.Code != "VAULT_RESPONSE_UNSUPPORTED" || failure.Source != tc.source {
			t.Fatal("response failure source changed or unknown field was dropped")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if payload, failure := tc.read(ctx, state); payload != nil || failure == nil || failure.Code != "VAULT_READ_CANCELED" {
			t.Fatal("canceled read model emitted payload")
		}
	}
}
