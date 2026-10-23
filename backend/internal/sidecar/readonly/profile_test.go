package readonly

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/vault/vaultread"
)

func profileInfo() *datahome.Info {
	return &datahome.Info{Initialized: true, FormatVersion: 2, Channel: "dev", DataHomeID: fixtureID}
}

func requireVaultCode(t *testing.T, err error, code string) *vaultread.Error {
	t.Helper()
	failure, ok := err.(*vaultread.Error)
	if !ok || failure.Code != code {
		t.Fatalf("expected fixed Vault code %s, got %v", code, err)
	}
	for _, format := range []string{"%s", "%v", "%+v", "%#v"} {
		if fmt.Sprintf(format, failure) != "Career Vault read could not be completed." {
			t.Fatal("Vault error rendering exposed nonfixed content")
		}
	}
	return failure
}

func TestProfileAdmissionRefusesBeforeLoading(t *testing.T) {
	for _, tc := range []struct {
		name string
		info *datahome.Info
		err  *datahome.Error
		code string
	}{
		{"missing", nil, nil, "DATA_HOME_UNAVAILABLE"},
		{"uninitialized", &datahome.Info{}, nil, "DATA_HOME_UNAVAILABLE"},
		{"changed", &datahome.Info{Initialized: true, DataHomeID: "different"}, nil, "DATA_HOME_IDENTITY_CHANGED"},
		{"newer", nil, &datahome.Error{Code: "DATA_HOME_NEWER_THAN_APP"}, "DATA_HOME_NEWER_THAN_APP"},
		{"recovery", nil, &datahome.Error{Code: "DATA_HOME_RECOVERY_REQUIRED"}, "DATA_HOME_RECOVERY_REQUIRED"},
		{"channel", nil, &datahome.Error{Code: "DATA_HOME_CHANNEL_MISMATCH"}, "DATA_HOME_CHANNEL_MISMATCH"},
		{"unknown", nil, &datahome.Error{Code: "PRIVATE-CAUSE"}, "DATA_HOME_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			payload, err := admittedVault(context.Background(), fixtureID, func() (*datahome.Info, *datahome.Error) {
				reads++
				return tc.info, tc.err
			}, func() (json.RawMessage, *vaultread.Error) {
				t.Fatal("rejected admission reached profile loader")
				return nil, nil
			})

			requireCode(t, err, tc.code)
			if payload != nil || reads != 1 {
				t.Fatal("admission rejection returned content or repeated reads")
			}
		})
	}
}

func TestProfilePostReadAdmissionDiscardsPayloadAndStaleDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name string
		info *datahome.Info
		err  *datahome.Error
		code string
	}{
		{"missing", nil, nil, "DATA_HOME_UNAVAILABLE"},
		{"uninitialized", &datahome.Info{}, nil, "DATA_HOME_UNAVAILABLE"},
		{"identity", &datahome.Info{Initialized: true, FormatVersion: 2, Channel: "dev", DataHomeID: "different"}, nil, "DATA_HOME_IDENTITY_CHANGED"},
		{"channel", &datahome.Info{Initialized: true, FormatVersion: 2, Channel: "stable", DataHomeID: fixtureID}, nil, "DATA_HOME_IDENTITY_CHANGED"},
		{"format", &datahome.Info{Initialized: true, FormatVersion: 3, Channel: "dev", DataHomeID: fixtureID}, nil, "DATA_HOME_IDENTITY_CHANGED"},
		{"newer", nil, &datahome.Error{Code: "DATA_HOME_NEWER_THAN_APP"}, "DATA_HOME_NEWER_THAN_APP"},
		{"recovery", nil, &datahome.Error{Code: "DATA_HOME_RECOVERY_REQUIRED"}, "DATA_HOME_RECOVERY_REQUIRED"},
		{"unknown", nil, &datahome.Error{Code: "PRIVATE-CAUSE"}, "DATA_HOME_UNAVAILABLE"},
	} {
		for _, loadErr := range []*vaultread.Error{nil, {Stage: "validation", Code: "VAULT_RECORD_VALIDATION_FAILED"}} {
			t.Run(fmt.Sprintf("%s/load_failed=%t", tc.name, loadErr != nil), func(t *testing.T) {
				reads, loads := 0, 0
				payload, err := admittedVault(context.Background(), fixtureID, func() (*datahome.Info, *datahome.Error) {
					reads++
					if reads == 1 {
						return profileInfo(), nil
					}
					return tc.info, tc.err
				}, func() (json.RawMessage, *vaultread.Error) {
					loads++
					return json.RawMessage(`{"record":{"name":"Synthetic"}}`), loadErr
				})

				requireCode(t, err, tc.code)
				if payload != nil || reads != 2 || loads != 1 {
					t.Fatal("post-read admission returned stale data or changed sequence")
				}
			})
		}
	}
}

func TestProfilePreservesSafeErrorAndRejectsMissingResult(t *testing.T) {
	loadErr := &vaultread.Error{Stage: "validation", Source: "profile", Code: "VAULT_RECORD_VALIDATION_FAILED", Issues: []vaultread.Issue{{Source: "profile", Code: "STRING_REQUIRED", Loc: []any{"role_targets", 0}}}}
	for _, failure := range []*vaultread.Error{loadErr, nil} {
		reads := 0
		payload, err := admittedVault(context.Background(), fixtureID, func() (*datahome.Info, *datahome.Error) {
			reads++
			return profileInfo(), nil
		}, func() (json.RawMessage, *vaultread.Error) { return nil, failure })

		if payload != nil || reads != 2 {
			t.Fatal("failed read did not recheck admission or returned data")
		}
		if failure == nil {
			requireCode(t, err, "SIDECAR_SCHEMA_INITIALIZATION_FAILED")
		} else if requireVaultCode(t, err, failure.Code) != failure {
			t.Fatal("safe structured loader error was discarded")
		}
	}
	if payload, err := admittedVault(context.Background(), fixtureID, func() (*datahome.Info, *datahome.Error) {
		return profileInfo(), nil
	}, func() (json.RawMessage, *vaultread.Error) { return json.RawMessage{}, nil }); len(payload) != 0 {
		t.Fatal("empty successful loader result returned content")
	} else {
		requireCode(t, err, "SIDECAR_SCHEMA_INITIALIZATION_FAILED")
	}
}

func TestProfileCancellationBetweenOperations(t *testing.T) {
	for _, stage := range []string{"before", "inspect_before", "load", "inspect_after"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if stage == "before" {
				cancel()
			}
			reads, loads := 0, 0
			payload, err := admittedVault(ctx, fixtureID, func() (*datahome.Info, *datahome.Error) {
				reads++
				if stage == "inspect_before" && reads == 1 || stage == "inspect_after" && reads == 2 {
					cancel()
				}
				return profileInfo(), nil
			}, func() (json.RawMessage, *vaultread.Error) {
				loads++
				if stage == "load" {
					cancel()
				}
				return json.RawMessage(`{}`), nil
			})

			requireVaultCode(t, err, "VAULT_READ_CANCELED")
			want := map[string][2]int{"before": {0, 0}, "inspect_before": {1, 0}, "load": {1, 1}, "inspect_after": {2, 1}}[stage]
			if payload != nil || reads != want[0] || loads != want[1] {
				t.Fatal("cancellation did not stop at the operation boundary")
			}
		})
	}
	for _, w := range []*Workspace{nil, {}} {
		if payload, err := w.Profile(context.Background()); payload != nil {
			t.Fatal("unavailable workspace returned profile")
		} else {
			requireCode(t, err, "DATA_HOME_UNAVAILABLE")
		}
		if payload, err := w.Profile(nil); payload != nil {
			t.Fatal("nil context returned profile")
		} else {
			requireVaultCode(t, err, "VAULT_READ_CANCELED")
		}
	}
	payload, err := admittedVault(context.Background(), "", func() (*datahome.Info, *datahome.Error) {
		t.Fatal("empty admission ID inspected a home")
		return nil, nil
	}, nil)

	requireCode(t, err, "DATA_HOME_UNAVAILABLE")
	if payload != nil {
		t.Fatal("empty admission ID returned a profile")
	}
}

// Minimal valid synthetic evidence is local to this test. No actual Vault or
// default resource profile is consulted; every required file is explicit.
func writeProfileFixture(t *testing.T, root, profile string) {
	t.Helper()
	files := map[string]string{
		"profile.yaml":           profile,
		"education.yaml":         "schools: []\n",
		"visa_profile.yaml":      "current_status: synthetic\nwork_authorization: synthetic\nrequires_future_sponsorship: false\nstem_opt_eligible: false\n",
		"experience_blocks.yaml": "- {id: exp, company: Synthetic, role: Engineer, dates: '2026', category: technical, technologies: [], proof_points: []}\n",
		"project_blocks.yaml":    "- {id: project, name: Synthetic, category: technical, summary: Test, technologies: [], proof_points: []}\n",
		"skill_blocks.yaml":      "- {id: skill, skill_family: Technical, related_experience: [exp], keywords: []}\n",
		"proof_blocks.yaml":      "- {id: proof, claim: Synthetic, evidence_type: experience, source_block: exp}\n",
		"style_rules.yaml":       "standards_metadata: {reviewed_on: '2026-09-01', review_due: '2027-09-01', market: US, authority_order: [test]}\nresume_voice: {}\nbullet_rules: {}\ndocument_rules: {layout: single_column, standard_sections: [test]}\nats_rules: {}\nselection_rules: {allowed_decisions: [full, compact, excluded]}\nquality_gates: {}\nforbidden: []\n",
	}
	for name, raw := range files {
		writeFixture(t, root, "career_vault/"+name, raw)
	}
}

func TestNativeProfileFreshOwnedPayloadAndUnchangedFiles(t *testing.T) {
	base, start := fixture(t)
	raw := "name: '  Synthetic Ω  '\r\ntarget_location: US\r\nwork_authorization: synthetic\r\nmajor: Computer Science\r\ncustom: {null: first, 'null': second}\r\n"
	writeProfileFixture(t, start.DataRoot, raw)
	w, err := Open(start)
	if err != nil {
		t.Fatal(err)
	}
	before := tree(t, base)
	first, err := w.Profile(context.Background())
	if err != nil || !json.Valid(first) || !bytes.Contains(first, []byte(fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(raw))))) || !bytes.Contains(first, []byte(`"null":"first","null":"second"`)) {
		t.Fatal("profile did not preserve raw revision or ordered unknown fields")
	}
	saved := bytes.Clone(first)
	first[0] = '!'
	second, err := w.Profile(context.Background())
	if err != nil || !bytes.Equal(second, saved) || !reflect.DeepEqual(before, tree(t, base)) {
		t.Fatal("payload ownership or read-only fixture state changed")
	}
	updated := raw + "updated: true\r\n"
	writeFixture(t, start.DataRoot, "career_vault/profile.yaml", updated)
	before = tree(t, base)
	third, err := w.Profile(context.Background())
	if err != nil || bytes.Equal(third, second) || !bytes.Contains(third, []byte(`"updated":true`)) || !bytes.Contains(third, []byte(fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(updated))))) || !reflect.DeepEqual(before, tree(t, base)) {
		t.Fatal("profile served stale data/revision or changed updated fixture")
	}
}

func TestNativeProfileMissingVaultDoesNotCreateDirectory(t *testing.T) {
	base, start := fixture(t)
	w, err := Open(start)
	if err != nil {
		t.Fatal(err)
	}
	before := tree(t, base)
	payload, err := w.Profile(context.Background())
	failure := requireVaultCode(t, err, "VAULT_FILE_MISSING")
	if payload != nil || failure.Source != "profile" || !reflect.DeepEqual(before, tree(t, base)) {
		t.Fatal("missing profile was synthesized or another source consulted")
	}
	if _, statErr := os.Lstat(filepath.Join(start.DataRoot, "career_vault")); !os.IsNotExist(statErr) {
		t.Fatal("profile read created Vault directory")
	}
}

func TestNativeProfileRevalidatesResourcesAndHomeBeforeVault(t *testing.T) {
	for _, tc := range []struct {
		name, path, value, code string
		resource, remove        bool
	}{
		{"resource", "VERSION", "invalid", "SIDECAR_RESOURCE_INVALID", true, false},
		{"missing_marker", datahome.MarkerPath, "", "DATA_HOME_UNAVAILABLE", false, true},
		{"identity", datahome.MarkerPath, marker("dev", "dh_ffffffffffffffffffffffffffffffff"), "DATA_HOME_IDENTITY_CHANGED", false, false},
		{"newer", datahome.MarkerPath, `{"application":"CareerHub","format_version":3,"channel":"dev"}`, "DATA_HOME_NEWER_THAN_APP", false, false},
		{"channel", datahome.MarkerPath, marker("stable", fixtureID), "DATA_HOME_CHANNEL_MISMATCH", false, false},
		{"recovery", datahome.DocumentRecoveryPath, "synthetic", "DATA_HOME_RECOVERY_REQUIRED", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, start := fixture(t)
			w, err := Open(start)
			if err != nil {
				t.Fatal(err)
			}
			root := start.DataRoot
			if tc.resource {
				root = start.ResourceRoot
			}
			if tc.remove {
				removeFixture(t, root, tc.path)
			} else {
				writeFixture(t, root, tc.path, tc.value)
			}
			before := tree(t, base)
			payload, err := w.Profile(context.Background())
			requireCode(t, err, tc.code)
			if payload != nil || !reflect.DeepEqual(before, tree(t, base)) {
				t.Fatal("rejected profile request read stale data or changed files")
			}
		})
	}
}
