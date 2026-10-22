package vaultwrite

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/vaultread"
)

func TestVersionedUnknownInputsKeepSchemaAndLegacyRules(t *testing.T) {
	for _, target := range []vaultread.EditTarget{vaultread.ProfileTarget, vaultread.EligibilityTarget} {
		payload := map[string]any{}
		original := "{}"
		if target == vaultread.ProfileTarget {
			payload["name"] = "Synthetic"
			payload["role_targets"] = []any{}
			original = "vault_schema_version: 2\nname: Before\n"
		}
		if _, err := normalize(target, payload); err == nil {
			t.Fatal("legacy relaxed")
		}
		value, err := normalizeVersioned(target, payload, true)
		if err != nil {
			t.Fatal(err.Code)
		}
		before, parseErr := cataloginput.ParseYAML([]byte(original))
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		merged, err := merge(target, before, value)
		if err != nil {
			t.Fatal(err.Code)
		}
		if target == vaultread.ProfileTarget {
			version, err := vaultread.SchemaVersion(merged)
			if err != nil || version != 2 {
				t.Fatal("schema marker lost")
			}
		} else {
			for _, field := range eligibilityFields[2:] {
				if _, present := get(merged, field); present {
					t.Fatal("unknown became concrete")
				}
			}
			payload["stem_opt_eligible"] = "false"
			if _, err := normalizeVersioned(target, payload, true); err == nil {
				t.Fatal("string flag accepted")
			}
			delete(payload, "stem_opt_eligible")
		}
		payload["vault_schema_version"] = 2
		if _, err := normalizeVersioned(target, payload, true); err == nil {
			t.Fatal("client schema mutation accepted")
		}
	}
}

func TestProfileInputLimitsUnsupportedAndPrivacy(t *testing.T) {
	for _, item := range []struct {
		payload map[string]any
		code    string
	}{
		{map[string]any{"name": "\xff"}, "VAULT_EDITOR_UNICODE_UNSUPPORTED"},
		{map[string]any{"\xff": nil}, "VAULT_EDITOR_UNICODE_UNSUPPORTED"},
		{map[string]any{"name": strings.Repeat(" ", maxPayloadBytes+1)}, "VAULT_EDITOR_INPUT_LIMIT"},
		{map[string]any{strings.Repeat("k", maxPayloadBytes+1): nil}, "VAULT_EDITOR_INPUT_LIMIT"},
	} {
		if _, e := normalize(vaultread.ProfileTarget, item.payload); e == nil || e.Code != item.code {
			t.Fatal("input boundary", e)
		}
	}
	huge := map[string]any{}
	for i := 0; i < 1001; i++ {
		huge[fmt.Sprint(i)] = nil
	}
	if _, e := normalize(vaultread.ProfileTarget, huge); e == nil || e.Code != "VAULT_EDITOR_INPUT_LIMIT" {
		t.Fatal("keys limit")
	}
	if _, e := normalize(99, nil); e == nil || e.Code != "VAULT_EDITOR_TARGET_INVALID" {
		t.Fatal("target")
	}
	for _, list := range [][]any{{"\xff"}, {strings.Repeat(" ", maxPayloadBytes), "x"}} {
		if _, e := cleanRoles(list); e == nil || e.SourceClass != "" {
			t.Fatal("role transport limits")
		}
	}
	e := invalid("PRIVATE-UNKNOWN-FIELD")
	e.Vault = &vaultread.Error{Code: "SYNTHETIC"}
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		if fmt.Sprintf(format, e) != "Career Vault edit could not be completed." {
			t.Fatal("error leaked fields")
		}
	}
	for _, target := range []vaultread.EditTarget{vaultread.ProfileTarget, vaultread.EligibilityTarget} {
		for _, during := range []bool{false, true} {
			if conflict(target, during).SourceClass != "VaultEditorRevisionConflict" {
				t.Fatal("conflict classification")
			}
		}
	}
}

func TestProfileEncodingLimitsCancellationAndLosslessShapes(t *testing.T) {
	for _, raw := range []string{
		"x: {true: first, 1: second, null: a, 'null': b, 2.0: c, 3: d}\n",
		"x: [2026-01-01, 2026-01-01T10:01:02+01:00, !!binary c2VjcmV0, .nan, .inf, -.inf]\n",
		"x: [!!float '1e20', !!float '1e-20', -0.0, 0, 123456789012345678901234567890, 'on', 'off']\n",
		"x: 'line1\n\n  line2'\n", "x: {a: [], b: {}}\n",
	} {
		value, e := cataloginput.ParseYAML([]byte(raw))
		if e != nil {
			t.Fatal(e)
		}
		out, failure := encode(context.Background(), value)
		if failure != nil {
			t.Fatal(failure.Code)
		}
		again, e := cataloginput.ParseYAML(out)
		if e != nil || !equalValue(value, again) || bytes.Contains(out, []byte("\r\n")) {
			t.Fatal("lossy or CRLF writer")
		}
	}
	for _, value := range []cataloginput.Value{
		{Kind: "unsupported"}, {Kind: "string", Text: "\xff"}, {Kind: "int", Text: "bad"},
		{Kind: "string", Text: strings.Repeat("\x00", cataloginput.YAMLMaxInputBytes/2)},
		{Kind: "string", Text: strings.Repeat("x", cataloginput.YAMLMaxExpandedTextBytes+1)},
	} {
		if raw, e := encode(context.Background(), value); e == nil || raw != nil {
			t.Fatal("invalid/oversized output admitted")
		}
	}
	deep := cataloginput.Value{Kind: "null"}
	for i := 0; i < cataloginput.YAMLMaxDepth+1; i++ {
		deep = cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{deep}}
	}
	if _, e := encode(context.Background(), deep); e == nil || e.Code != "VAULT_EDITOR_ENCODING_LIMIT" {
		t.Fatal("depth bound")
	}
	e := valueEncoder{ctx: context.Background(), nodes: cataloginput.YAMLMaxNodes}
	e.node(cataloginput.Value{Kind: "null"}, 1)
	if e.problem == nil {
		t.Fatal("node limit")
	}
	for _, ctx := range []context.Context{nil, func() context.Context { c, cancel := context.WithCancel(context.Background()); cancel(); return c }()} {
		if raw, e := encode(ctx, cataloginput.Value{Kind: "mapping"}); e == nil || e.Code != "VAULT_EDITOR_CANCELED" || raw != nil {
			t.Fatal("cancel")
		}
	}
	if equalValue(textValue("one"), textValue("two")) ||
		equalValue(cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{textValue("one")}},
			cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{textValue("two")}}) {
		t.Fatal("value equality")
	}
}

func TestProfileServiceUnavailableAndIdentityBeforeIO(t *testing.T) {
	var nilService *Service
	for _, service := range []*Service{nilService, {}, New("not-an-absolute-root")} {
		if out, e := service.UpdateProfile(nil, "absent", nil, ""); e == nil || e.Code != "VAULT_EDITOR_RECORD_NOT_FOUND" || out.Committed {
			t.Fatal("identity precedence")
		}
		if out, e := service.UpdateEligibility(nil, "eligibility", nil, ""); e == nil || e.Code != "VAULT_EDITOR_UNAVAILABLE" || out.Committed {
			t.Fatal("configuration")
		}
	}
	if out, e := New("relative").UpdateProfile(context.Background(), "profile", nil, ""); e == nil || e.Stage != "path" || out.Committed {
		t.Fatal("invalid path")
	}
}
