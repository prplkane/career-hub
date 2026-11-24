//go:build windows

package vaultsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/applicationhelper"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/skills/competencyexplorer"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/vault/vaultbundle"
	"careerhub.local/backend/internal/vault/vaultread"
)

// Each child process claims exactly one Home, as the sidecar does. A process
// can own only one Home, so export and import of two Homes use two children.
func runBundleChild(t *testing.T, scenario, mode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBundleChild$")
	cmd.Env = append(os.Environ(), "CAREERHUB_BUNDLE_CHILD="+mode, "CAREERHUB_BUNDLE_SCENARIO="+scenario)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v %.3000s", mode, err, out)
	}
}

func newScenario(t *testing.T) string {
	t.Helper()
	scenario := t.TempDir()
	syntheticResources(t, filepath.Join(scenario, "resources"))
	defaultHome(t, filepath.Join(scenario, "home"))
	return scenario
}

func TestBundleChild(t *testing.T) {
	mode := os.Getenv("CAREERHUB_BUNDLE_CHILD")
	if mode == "" {
		t.Skip("child process entry point")
	}
	scenario := os.Getenv("CAREERHUB_BUNDLE_SCENARIO")
	dir := filepath.Join(scenario, "home")
	held, pathErr := runtimepaths.Open(dir)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	defer held.Close()
	if err := held.ClaimForProcess(); err != nil {
		t.Fatal(err)
	}
	s := New(dir, bundleHome).WithResources(filepath.Join(scenario, "resources"))
	ctx := context.Background()
	if strings.HasPrefix(mode, "export") {
		runExportChild(t, s, scenario, mode)
		return
	}
	runImportChild(t, ctx, s, scenario, dir, mode)
}

func runExportChild(t *testing.T, s *Service, scenario, mode string) {
	out, problem := s.Export(context.Background())
	want := map[string]string{
		"export": "", "export_not_configured": "VAULT_EXPORT_NOT_CONFIGURED", "export_invalid": "VAULT_EXPORT_INVALID",
		"export_too_large": "VAULT_EXPORT_TOO_LARGE", "export_unknown_skill": "VAULT_EXPORT_SKILL_UNKNOWN",
	}[mode]
	if want != "" {
		if problem == nil || problem.Code != want || out != nil {
			t.Fatalf("export %s: got %v", mode, problem)
		}
		return
	}
	if problem != nil {
		t.Fatal("export", problem.Code)
	}
	again, problem := s.Export(context.Background())
	if problem != nil || !bytes.Equal(out, again) {
		t.Fatal("export is not deterministic", problem)
	}
	if err := os.WriteFile(filepath.Join(scenario, "bundle.chvault"), out, 0600); err != nil {
		t.Fatal(err)
	}
}

func readBundle(t *testing.T, scenario string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(scenario, "bundle.chvault"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func runImportChild(t *testing.T, ctx context.Context, s *Service, scenario, dir, mode string) {
	raw := readBundle(t, scenario)
	bundle, problem := PrepareBundle(ctx, raw)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	write := func(path string, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(path)), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	switch mode {
	case "customized":
		write(catalogstore.CustomSkillsPath, "- {id: user_mine, name: Mine, category: tool, primary_group_id: user_my_custom_skills}\n")
	case "catalog_missing":
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(catalogstore.CustomSkillsPath))); err != nil {
			t.Fatal(err)
		}
	case "already_equal":
		write(catalogstore.CustomGroupsPath, syntheticGroups)
		write(catalogstore.CustomSkillsPath, syntheticSkills)
	case "answers_conflict":
		write(applicationhelper.AnswersPath, "schema_version: 1\nanswers: [] # existing\n")
	case "answers_kept":
		write(applicationhelper.AnswersPath, "schema_version: 1\nanswers: [] # existing\n")
	}
	preview, problem := s.Preview(ctx, bundle)
	if mode == "customized" || mode == "catalog_missing" || mode == "answers_conflict" {
		if problem == nil || problem.Code != "VAULT_SETUP_FILE_CONFLICT" || preview.Plan != nil {
			t.Fatal("conflicting destination accepted", problem)
		}
		return
	}
	if problem != nil || preview.Plan == nil || preview.Committed {
		t.Fatal("preview", problem)
	}
	plan := preview.Plan
	_, withAnswers := bundle.files[applicationhelper.AnswersPath]
	switch mode {
	case "fresh", "answers_kept", "interrupted", "replace_failure", "stale":
		if len(plan.Replace) != 2 || len(plan.Retain) != 0 || len(plan.Create) != len(bundle.files)-2 {
			t.Fatal("fresh plan", plan)
		}
	case "already_equal":
		if len(plan.Replace) != 0 || len(plan.Retain) != 2 {
			t.Fatal("equal catalogs must be retained", plan)
		}
	case "resume":
		// The interrupted child wrote both catalogs and one Vault file.
		if len(plan.Replace) != 0 || len(plan.Retain) != 3 || len(plan.Create) != len(bundle.files)-3 {
			t.Fatal("resume plan", plan)
		}
	}
	effects := 0
	replace, create := s.replace, s.create
	s.replace = func(root *runtimepaths.Root, path string, expected, replacement []byte) (bool, *runtimepaths.Error) {
		effects++
		if mode == "replace_failure" && effects == 2 {
			return false, &runtimepaths.Error{Code: "private synthetic cause"}
		}
		return replace(root, path, expected, replacement)
	}
	s.create = func(root *runtimepaths.Root, path string, raw []byte) (bool, *runtimepaths.Error) {
		effects++
		done, err := create(root, path, raw)
		if mode == "interrupted" && effects == 3 && err == nil {
			os.Exit(0)
		}
		return done, err
	}
	if mode == "stale" {
		// The destination changes after the preview (here the groups file now
		// matches the bundle): the confirmed plan no longer describes it.
		write(catalogstore.CustomGroupsPath, syntheticGroups)
	}
	out, problem := s.Apply(ctx, bundle, plan.PlanID)
	if mode == "replace_failure" {
		if problem == nil || problem.Code != "VAULT_SETUP_PUBLICATION_UNVERIFIED" || out.Replaced != 1 || !out.Committed || !out.CommitUnknown || out.Created != 0 {
			t.Fatal("replace failure", out, problem)
		}
		encoded, _ := json.Marshal(out)
		if strings.Contains(string(encoded), "private") {
			t.Fatal("native error leaked")
		}
		return
	}
	if mode == "stale" {
		if problem == nil || problem.Code != "VAULT_SETUP_PLAN_CHANGED" || out.Committed || effects != 0 {
			t.Fatal("stale plan applied", out, problem)
		}
		return
	}
	if problem != nil || !out.Complete {
		t.Fatal("apply", out, problem)
	}
	if out.Created != len(plan.Create) || out.Replaced != len(plan.Replace) {
		t.Fatal("counts", out)
	}
	for path, want := range bundle.files {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("content was normalized or lost", path, err)
		}
	}
	if mode == "answers_kept" && !withAnswers {
		got, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(applicationhelper.AnswersPath)))
		if string(got) != "schema_version: 1\nanswers: [] # existing\n" {
			t.Fatal("answers that did not travel were changed")
		}
	}
	state, err := vaultread.ReadSetup(ctx, dir)
	if err != nil || !strings.Contains(string(state), `"state":"configured"`) {
		t.Fatal("not configured", err)
	}
	assertSkillsView(t, ctx, dir, filepath.Join(scenario, "resources"))
	again, problem := s.Preview(ctx, bundle)
	if problem != nil || len(again.Plan.Create) != 0 || len(again.Plan.Replace) != 0 {
		t.Fatal("repeat preview", problem)
	}
	replay, problem := s.Apply(ctx, bundle, again.Plan.PlanID)
	if problem != nil || !replay.Complete || replay.Committed || replay.Created != 0 || replay.Replaced != 0 {
		t.Fatal("repeat wrote", replay, problem)
	}
}

// assertSkillsView runs the Skills evidence projection on the destination in
// this same process, with no restart: every imported link must resolve.
func assertSkillsView(t *testing.T, ctx context.Context, dir, resourceRoot string) {
	t.Helper()
	data, pathErr := runtimepaths.Open(dir)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	defer data.Close()
	resources, pathErr := runtimepaths.Open(resourceRoot)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	defer resources.Close()
	docs, _, readErr := vaultread.ResumePlanSources(ctx, data)
	if readErr != nil {
		t.Fatal("vault sources", readErr.Code)
	}
	state, catalogErr := catalogstore.ReadState(resources, data)
	if catalogErr != nil {
		t.Fatal("catalog", catalogErr)
	}
	view, buildErr := competencyexplorer.Build(ctx, docs, state.Snapshot, "sha256:synthetic", "2026-11", false)
	if buildErr != nil {
		t.Fatal("Skills evidence view failed", buildErr.Code)
	}
	if !bytes.Contains(view, []byte("user_synthetic_skill")) {
		t.Fatal("custom skill evidence missing")
	}
}

func TestBundleImportNativeOwnedProcess(t *testing.T) {
	for _, mode := range []string{"fresh", "answers_kept", "customized", "catalog_missing", "already_equal", "answers_conflict", "stale", "replace_failure", "restart"} {
		for _, withAnswers := range []bool{true, false} {
			name := mode
			if withAnswers {
				name += "_with_answers"
			}
			t.Run(name, func(t *testing.T) {
				if mode == "answers_conflict" && !withAnswers {
					t.Skip("a bundle without answers never touches the destination answers file")
				}
				if mode == "answers_kept" && withAnswers {
					t.Skip("covered by answers_conflict: differing answers are never replaced")
				}
				scenario := newScenario(t)
				if err := os.WriteFile(filepath.Join(scenario, "bundle.chvault"), syntheticBundle(t, withAnswers), 0600); err != nil {
					t.Fatal(err)
				}
				if mode == "restart" {
					runBundleChild(t, scenario, "interrupted")
					runBundleChild(t, scenario, "resume")
					return
				}
				runBundleChild(t, scenario, mode)
			})
		}
	}
}

// Acceptance: export a synthetic Home with a custom skill group, a custom skill
// and linked experience, project and certification records; import it into a
// second Home that has only bootstrap defaults; every file is byte-equal and
// the Skills evidence view builds on the destination without a restart.
func TestBundleExportImportAcceptance(t *testing.T) {
	source := newScenario(t)
	files := syntheticVault()
	files[applicationhelper.AnswersPath] = []byte(syntheticAnswers)
	text := map[string]string{}
	for path, raw := range files {
		text[path] = string(raw)
	}
	writeTree(t, filepath.Join(source, "home"), text)
	runBundleChild(t, source, "export")
	exported := readBundle(t, source)
	if !bytes.HasPrefix(exported, []byte(`{"format":"career-hub-vault-bundle"`)) {
		t.Fatal("export is not a bundle")
	}
	destination := newScenario(t)
	if err := os.WriteFile(filepath.Join(destination, "bundle.chvault"), exported, 0600); err != nil {
		t.Fatal(err)
	}
	runBundleChild(t, destination, "fresh")
	for _, path := range vaultbundle.Paths() {
		want, err1 := os.ReadFile(filepath.Join(source, "home", filepath.FromSlash(path)))
		got, err2 := os.ReadFile(filepath.Join(destination, "home", filepath.FromSlash(path)))
		if err1 != nil || err2 != nil || !bytes.Equal(got, want) {
			t.Fatal("destination differs from source", path)
		}
	}
}

func TestBundleExportRefusals(t *testing.T) {
	for _, mode := range []string{"export_not_configured", "export_invalid", "export_too_large", "export_missing_catalog"} {
		t.Run(mode, func(t *testing.T) {
			scenario := newScenario(t)
			home := filepath.Join(scenario, "home")
			text := map[string]string{}
			for path, raw := range syntheticVault() {
				text[path] = string(raw)
			}
			profile := vaultread.SetupPaths()[0]
			switch mode {
			case "export_not_configured":
				delete(text, vaultread.SetupPaths()[3])
			case "export_invalid":
				text[profile] = "name: [\n"
			case "export_too_large":
				text[profile] = strings.Repeat("#", 2<<20+1)
			}
			writeTree(t, home, text)
			if mode == "export_missing_catalog" {
				if err := os.Remove(filepath.Join(home, filepath.FromSlash(catalogstore.CustomGroupsPath))); err != nil {
					t.Fatal(err)
				}
				mode = "export_not_configured"
			}
			runBundleChild(t, scenario, mode)
		})
	}
}

// An exported Vault that links to a skill
// the catalog lacks must not produce a file that import would refuse.
func TestBundleExportRefusesUnknownSkill(t *testing.T) {
	scenario := newScenario(t)
	text := map[string]string{}
	for path, raw := range syntheticVault() {
		text[path] = string(raw)
	}
	text[catalogstore.CustomSkillsPath] = "[]\n"
	text[catalogstore.CustomGroupsPath] = "- {id: user_synthetic_group, name: Synthetic group}\n"
	writeTree(t, filepath.Join(scenario, "home"), text)
	runBundleChild(t, scenario, "export_unknown_skill")
}

// The skill check reads only the core
// catalog and the bundle, before Home admission, so no process lease is used.
func TestBundleImportRefusesUnknownSkill(t *testing.T) {
	scenario := newScenario(t)
	files := syntheticVault()
	files[catalogstore.CustomSkillsPath] = []byte("[]\n")
	raw, encodeErr := vaultbundle.Encode(files)
	if encodeErr != nil {
		t.Fatal(encodeErr.Code)
	}
	bundle, problem := PrepareBundle(context.Background(), raw)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	s := New(filepath.Join(scenario, "home"), bundleHome).WithResources(filepath.Join(scenario, "resources"))
	created := false
	s.create = func(*runtimepaths.Root, string, []byte) (bool, *runtimepaths.Error) {
		created = true
		return false, nil
	}
	for _, apply := range []bool{false, true} {
		var out *Outcome
		if apply {
			out, problem = s.Apply(context.Background(), bundle, "sha256:"+strings.Repeat("0", 64))
		} else {
			out, problem = s.Preview(context.Background(), bundle)
		}
		if problem == nil || problem.Code != "VAULT_SETUP_SKILL_UNKNOWN" || out.Plan != nil || out.Committed || created {
			t.Fatalf("unknown skill link accepted (apply=%v): %v", apply, problem)
		}
	}
}

func TestBundleCatalogCheckedAgainstCore(t *testing.T) {
	scenario := newScenario(t)
	s := New(filepath.Join(scenario, "home"), bundleHome).WithResources(filepath.Join(scenario, "resources"))
	for name, skills := range map[string]string{
		"missing group":   "- {id: user_synthetic_skill, name: Synthetic Skill, category: tool, primary_group_id: user_absent_group}\n",
		"duplicate skill": syntheticSkills + syntheticSkills,
	} {
		files := syntheticVault()
		files[catalogstore.CustomSkillsPath] = []byte(skills)
		raw, _ := vaultbundle.Encode(files)
		bundle, problem := PrepareBundle(context.Background(), raw)
		if problem != nil {
			// A pure catalog decoder may already refuse it; that is also fine.
			if problem.Code != "VAULT_SETUP_CATALOG_INVALID" {
				t.Fatal(name, problem.Code)
			}
			continue
		}
		out, problem := s.Preview(context.Background(), bundle)
		if problem == nil || problem.Code != "VAULT_SETUP_CATALOG_INVALID" || out.Plan != nil {
			t.Fatal(name, problem)
		}
	}
	missing := New(filepath.Join(scenario, "home"), bundleHome)
	bundle, _ := PrepareBundle(context.Background(), syntheticBundle(t, false))
	if _, problem := missing.Preview(context.Background(), bundle); problem == nil || problem.Code != "VAULT_SETUP_INVALID" {
		t.Fatal("export-file import without the core catalog root", problem)
	}
	if _, problem := missing.Export(context.Background()); problem == nil || problem.Code != "VAULT_EXPORT_UNAVAILABLE" {
		t.Fatal("export without the core catalog root", problem)
	}
}
