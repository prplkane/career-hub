package vaultread

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/jobdigest"
	"careerhub.local/backend/internal/jobevidence"
)

func emptyV2Fixture() *fixtureReader {
	f := validFixture()
	f.files[schemas[profileIndex].path] = []byte("vault_schema_version: 2\nname: Synthetic Candidate\ncontact: {}\nrole_targets: []\n")
	f.files[schemas[educationIndex].path] = []byte("schools: []\ncertifications: []\n")
	f.files[schemas[eligibilityIndex].path] = []byte("current_status: ''\nrequires_future_sponsorship: null\nstem_opt_eligible: null\n")
	for _, index := range []int{experienceIndex, projectIndex, skillIndex, proofIndex} {
		f.files[schemas[index].path] = []byte("[]\n")
	}
	return f
}

func TestExplicitVaultSchemaVersion(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{"{}", 1}, {"vault_schema_version: 1", 1}, {"vault_schema_version: 2", 2},
		{"vault_schema_version: '2'", 0}, {"vault_schema_version: null", 0},
		{"vault_schema_version: false", 0}, {"vault_schema_version: 3", 0},
	} {
		value, err := cataloginput.ParseYAML([]byte(tc.raw))
		if err != nil {
			t.Fatal(err)
		}
		version, problem := SchemaVersion(value)
		if version != tc.want || (problem != nil) != (tc.want == 0) {
			t.Fatal(tc.raw, version, problem)
		}
		if problem != nil && problem.Code != "VAULT_SCHEMA_VERSION_UNSUPPORTED" {
			t.Fatal(problem.Code)
		}
	}
}

func TestEmptyV2KeepsValidationAndDoesNotInventEvidence(t *testing.T) {
	ctx := context.Background()
	f := emptyV2Fixture()
	if _, err := profilePayload(ctx, fixtureState(t, f)); err != nil {
		t.Fatal(err.Code)
	}
	for _, projection := range []func(context.Context, *vaultState) ([]byte, *Error){
		func(ctx context.Context, state *vaultState) ([]byte, *Error) { return profilePayload(ctx, state) },
		func(ctx context.Context, state *vaultState) ([]byte, *Error) { return eligibilityPayload(ctx, state) },
	} {
		raw, err := projection(ctx, fixtureState(t, emptyV2Fixture()))
		if err != nil || !bytes.Contains(raw, []byte(`"vault_schema_version":2`)) {
			t.Fatal("editor format metadata missing", err)
		}
	}
	intake := map[string]cataloginput.Value{}
	emptyState := fixtureState(t, emptyV2Fixture())
	for _, index := range []int{profileIndex, educationIndex, experienceIndex, projectIndex, skillIndex} {
		intake[schemas[index].path] = emptyState.documents[index]
	}
	if err := ValidateIntakeVault(ctx, intake); err != nil {
		t.Fatal("empty resume approval input rejected", err.Code)
	}
	evidence, err := PrepareEvaluation(ctx, emptyV2Fixture(), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err.Code)
	}
	matches, failure := jobevidence.Find(ctx, "Engineer", evidence.Index)
	if failure != nil || len(matches.Direct) != 0 || len(evidence.DurationMonths) != 0 {
		t.Fatal("invented evidence", failure)
	}
	eligibility, projectionErr := jobdigest.ProfileProjection(ctx, evidence.Eligibility)
	if projectionErr != nil || eligibility.CurrentStatus != "" || eligibility.SponsorshipNow.Value != nil || eligibility.SponsorshipFuture.Value != nil || eligibility.CitizenshipBlocker.Value != nil || eligibility.ClearanceBlocker.Value != nil {
		t.Fatal("invented eligibility", projectionErr)
	}
	records, _, err := HelperSources(ctx, emptyV2Fixture(), true, true)
	if err != nil {
		t.Fatal(err.Code)
	}
	if flag, _ := lookup(records["eligibility"], "stem_opt_eligible"); flag.Kind != "null" {
		t.Fatal("unknown became false")
	}
	for _, tc := range []struct {
		name string
		edit func(*fixtureReader)
	}{
		{"legacy_empty", replaceYAML(profileIndex, "vault_schema_version: 2", "vault_schema_version: 1")},
		{"unversioned_empty", replaceYAML(profileIndex, "vault_schema_version: 2", "")},
		{"invalid_name", replaceYAML(profileIndex, "name: Synthetic Candidate", "name: '   '")},
		{"invalid_optional_text", appendYAML(profileIndex, "major: false\n")},
		{"invalid_flag", replaceYAML(eligibilityIndex, "stem_opt_eligible: null", "stem_opt_eligible: 'false'")},
		{"bad_record", func(f *fixtureReader) { f.files[schemas[experienceIndex].path] = []byte("- {id: exp}\n") }},
		{"dangling_reference", func(f *fixtureReader) {
			f.files[schemas[proofIndex].path] = []byte("- {id: proof, claim: Test, evidence_type: experience, source_block: absent}\n")
		}},
		{"bad_style", replaceYAML(styleIndex, "layout: single_column", "layout: columns")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := emptyV2Fixture()
			tc.edit(f)
			if _, err := profilePayload(ctx, fixtureState(t, f)); err == nil {
				t.Fatal("invalid v2 accepted")
			}
		})
	}
	// Merely adding the explicit v1 marker cannot relax inherited validation.
	f = validFixture()
	appendYAML(profileIndex, "vault_schema_version: 1\n")(f)
	if _, err := profilePayload(ctx, fixtureState(t, f)); err != nil {
		t.Fatal(err.Code)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	f.files[schemas[profileIndex].path] = []byte(strings.Replace(string(f.files[schemas[profileIndex].path]), "version: 1", "version: 99", 1))
	if err := validateVault(ctx, fixtureState(t, f)); err == nil || err.Code != "VAULT_READ_CANCELED" {
		t.Fatal("cancellation precedence", err)
	}
}
