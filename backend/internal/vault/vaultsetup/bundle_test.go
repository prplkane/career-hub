package vaultsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applications/applicationhelper"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/vault/vaultbundle"
	"careerhub.local/backend/internal/vault/vaultread"
)

func TestPrepareBundleValidatesEverythingBeforeDisk(t *testing.T) {
	ctx := context.Background()
	bundle, problem := PrepareBundle(ctx, syntheticBundle(t, true))
	if problem != nil {
		t.Fatal(problem.Code)
	}
	if !bundle.bundled || len(bundle.files) != 11 || len(bundle.paths()) != 11 {
		t.Fatal("bundle scope", len(bundle.files))
	}
	paths := bundle.paths()
	if paths[0] != catalogstore.CustomGroupsPath || paths[1] != catalogstore.CustomSkillsPath || paths[2] != applicationhelper.AnswersPath || paths[3] != vaultread.SetupPaths()[0] {
		t.Fatal("effect order must be catalogs, answers, then Vault files", paths)
	}
	without, problem := PrepareBundle(ctx, syntheticBundle(t, false))
	if problem != nil || len(without.paths()) != 10 {
		t.Fatal("bundle without answers", problem)
	}
	mutate := func(path, text string) []byte {
		files := syntheticVault()
		files[path] = []byte(text)
		raw, problem := vaultbundle.Encode(files)
		if problem != nil {
			t.Fatal(problem.Code)
		}
		return raw
	}
	withAnswers := func(text string) []byte {
		files := syntheticVault()
		files[applicationhelper.AnswersPath] = []byte(text)
		raw, _ := vaultbundle.Encode(files)
		return raw
	}
	damaged := bytes.Replace(syntheticBundle(t, false), []byte(`"size":`), []byte(`"size":1`), 1)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, test := range []struct {
		name string
		ctx  context.Context
		raw  []byte
		code string
	}{
		{"not a bundle", ctx, []byte("name: Synthetic\n"), "VAULT_BUNDLE_INVALID"},
		{"damaged", ctx, damaged, "VAULT_BUNDLE_DAMAGED"},
		{"protected", ctx, []byte(`{"format":"career-hub-vault-bundle","format_version":1,"protection":"password-v1","payload":"x"}`), "VAULT_BUNDLE_PROTECTION_UNSUPPORTED"},
		{"newer", ctx, []byte(`{"format":"career-hub-vault-bundle","format_version":2}`), "VAULT_BUNDLE_VERSION_UNSUPPORTED"},
		{"invalid profile", ctx, mutate(vaultread.SetupPaths()[0], "name: [\n"), "VAULT_YAML_INVALID"},
		{"broken reference", ctx, mutate(vaultread.SetupPaths()[6], "- {id: proof, claim: Synthetic, evidence_type: experience, source_block: missing}\n"), "VAULT_RECORD_VALIDATION_FAILED"},
		{"invalid groups", ctx, mutate(catalogstore.CustomGroupsPath, "{not: a list}\n"), "VAULT_SETUP_CATALOG_INVALID"},
		{"invalid skills", ctx, mutate(catalogstore.CustomSkillsPath, "- {name: no id}\n"), "VAULT_SETUP_CATALOG_INVALID"},
		{"invalid answers", ctx, withAnswers("schema_version: 9\nanswers: []\n"), "VAULT_SETUP_ANSWERS_INVALID"},
		{"canceled", canceled, syntheticBundle(t, false), "VAULT_SETUP_CANCELED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, problem := PrepareBundle(test.ctx, test.raw)
			if problem == nil || problem.Code != test.code || got != nil {
				t.Fatalf("got %v, want %s", problem, test.code)
			}
		})
	}
}

func TestBundlePlanIsSeparateFromEightFilePlan(t *testing.T) {
	s := New(t.TempDir(), datahome.Info{FormatVersion: 2, Channel: "dev", Initialized: true, DataHomeID: "dh_0123456789abcdef0123456789abcdef"})
	groupsDefault, _ := datahome.BootstrapDefault(catalogstore.CustomGroupsPath)
	skillsDefault, _ := datahome.BootstrapDefault(catalogstore.CustomSkillsPath)
	defaults := func(bundle *Bundle) map[string]observed {
		state := map[string]observed{}
		for _, path := range bundle.paths() {
			state[path] = observed{}
		}
		if bundle.bundled {
			state[catalogstore.CustomGroupsPath] = observed{groupsDefault, true}
			state[catalogstore.CustomSkillsPath] = observed{skillsDefault, true}
		}
		return state
	}
	bundle, problem := PrepareBundle(context.Background(), syntheticBundle(t, true))
	if problem != nil {
		t.Fatal(problem.Code)
	}
	plan := s.plan(bundle, defaults(bundle))
	if len(plan.Create) != 9 || len(plan.Replace) != 2 || len(plan.Retain) != 0 {
		t.Fatal("bundle plan", plan)
	}
	eightFiles := map[string][]byte{}
	for _, path := range vaultread.SetupPaths() {
		eightFiles[path] = bundle.files[path]
	}
	eight, problem := Prepare(context.Background(), eightFiles)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	eightPlan := s.plan(eight, defaults(eight))
	if eightPlan.PlanID == plan.PlanID || eightPlan.Replace != nil {
		t.Fatal("bundle and eight-file plans must hash different operations")
	}
	raw, _ := json.Marshal(eightPlan)
	if strings.Contains(string(raw), "replace") || strings.Contains(string(raw), "Replace") {
		t.Fatal("eight-file plan JSON changed", string(raw))
	}
	// Any change to the bundled bytes or to the destination needs a new preview.
	files := syntheticVault()
	files[catalogstore.CustomSkillsPath] = []byte(syntheticSkills + "- {id: user_other_skill, name: Other Skill, category: tool, primary_group_id: user_synthetic_group}\n")
	files[applicationhelper.AnswersPath] = []byte(syntheticAnswers)
	changedRaw, _ := vaultbundle.Encode(files)
	changed, problem := PrepareBundle(context.Background(), changedRaw)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	if s.plan(changed, defaults(changed)).PlanID == plan.PlanID {
		t.Fatal("plan ignored a changed catalog")
	}
	retained := defaults(bundle)
	retained[catalogstore.CustomSkillsPath] = observed{bundle.files[catalogstore.CustomSkillsPath], true}
	again := s.plan(bundle, retained)
	if again.PlanID == plan.PlanID || len(again.Replace) != 1 || len(again.Retain) != 1 {
		t.Fatal("plan ignored destination state", again)
	}
}

func TestBundleActionRules(t *testing.T) {
	bundle, problem := PrepareBundle(context.Background(), syntheticBundle(t, true))
	if problem != nil {
		t.Fatal(problem.Code)
	}
	def, _ := datahome.BootstrapDefault(catalogstore.CustomGroupsPath)
	vaultPath := vaultread.SetupPaths()[0]
	for _, test := range []struct {
		name, path string
		entry      observed
		want       string
	}{
		{"catalog equal to bundle", catalogstore.CustomGroupsPath, observed{[]byte(syntheticGroups), true}, "retain"},
		{"catalog untouched default", catalogstore.CustomGroupsPath, observed{def, true}, "replace"},
		{"catalog customized", catalogstore.CustomGroupsPath, observed{[]byte("- {id: user_mine, name: Mine}\n"), true}, ""},
		{"catalog missing", catalogstore.CustomGroupsPath, observed{}, ""},
		{"answers absent", applicationhelper.AnswersPath, observed{}, "create"},
		{"answers identical", applicationhelper.AnswersPath, observed{[]byte(syntheticAnswers), true}, "retain"},
		{"answers differ", applicationhelper.AnswersPath, observed{[]byte("schema_version: 1\nanswers: [] # mine\n"), true}, ""},
		{"vault absent", vaultPath, observed{}, "create"},
		{"vault identical", vaultPath, observed{bundle.files[vaultPath], true}, "retain"},
		{"vault differs", vaultPath, observed{[]byte("name: Existing\n"), true}, ""},
	} {
		if got := action(bundle, test.path, test.entry); got != test.want {
			t.Fatalf("%s: got %q, want %q", test.name, got, test.want)
		}
	}
}
