package vaultsetup

import (
	"os"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/applications/applicationhelper"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/vault/vaultbundle"
	"careerhub.local/backend/internal/vault/vaultread"
)

// Synthetic people and skills only. Experience, project and certification
// records link to a custom skill the destination's default catalog lacks.
const (
	syntheticCoreGroups = "- {id: tools, name: Tools}\n"
	syntheticCoreSkills = "- {id: core_tool, name: Core Tool, category: tool, primary_group_id: tools}\n"
	syntheticGroups     = "- id: user_my_custom_skills\n  name: My custom skills\n  archived: false\n- {id: user_synthetic_group, name: Synthetic group}\n"
	syntheticSkills     = "- {id: user_synthetic_skill, name: Synthetic Skill, category: tool, primary_group_id: user_synthetic_group}\n"
	syntheticAnswers    = "schema_version: 1\nanswers: []\n"
	bundleHomeMarker    = `{"application":"CareerHub","channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef","format_version":2}`
)

var bundleHome = datahome.Info{FormatVersion: 2, Channel: "dev", Initialized: true, DataHomeID: "dh_0123456789abcdef0123456789abcdef"}

func syntheticVault() map[string][]byte {
	texts := []string{
		"name: Synthetic Candidate\ntarget_location: US\nwork_authorization: Synthetic only\nmajor: Computing\n",
		"schools: []\ncertifications: [{id: cert, name: Synthetic, credential: Test, completed: '2026', proof_points: [], competency_ids: [user_synthetic_skill]}]\n",
		"current_status: Synthetic\nwork_authorization: Synthetic only\nrequires_future_sponsorship: false\nstem_opt_eligible: false\n",
		"- {id: exp, company: Synthetic, role: Engineer, dates: '2025', category: technical, technologies: [Synthetic Skill], proof_points: [], competency_ids: [user_synthetic_skill, core_tool]}\n",
		"- {id: project, name: Synthetic, category: technical, summary: Test, technologies: [], proof_points: [], competency_ids: [user_synthetic_skill]}\n",
		"- {id: skill, skill_family: Technical, related_experience: [exp], keywords: []}\n",
		"- {id: proof, claim: Synthetic, evidence_type: experience, source_block: exp}\n",
		"standards_metadata: {reviewed_on: '2026-09-01', review_due: '2027-09-01', market: US, authority_order: [user]}\nresume_voice: {}\nbullet_rules: {}\ndocument_rules: {layout: single_column, standard_sections: [Experience]}\nats_rules: {}\nselection_rules: {allowed_decisions: [full, compact, excluded]}\nquality_gates: {}\nforbidden: []\n",
	}
	files := map[string][]byte{}
	for index, path := range vaultread.SetupPaths() {
		files[path] = []byte(texts[index])
	}
	files[catalogstore.CustomGroupsPath] = []byte(syntheticGroups)
	files[catalogstore.CustomSkillsPath] = []byte(syntheticSkills)
	return files
}

func syntheticBundle(t *testing.T, withAnswers bool) []byte {
	t.Helper()
	files := syntheticVault()
	if withAnswers {
		files[applicationhelper.AnswersPath] = []byte(syntheticAnswers)
	}
	raw, problem := vaultbundle.Encode(files)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	return raw
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for path, text := range files {
		target := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func syntheticResources(t *testing.T, root string) {
	t.Helper()
	writeTree(t, root, map[string]string{catalogstore.CoreGroupsPath: syntheticCoreGroups, catalogstore.CoreSkillsPath: syntheticCoreSkills})
}

// defaultHome is a Home as bootstrap leaves it: marker, empty Vault folder and
// the untouched default catalogs.
func defaultHome(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{datahome.MarkerPath: bundleHomeMarker}
	for _, path := range []string{catalogstore.CustomGroupsPath, catalogstore.CustomSkillsPath, "career_vault/application_helper.yaml"} {
		raw, ok := datahome.BootstrapDefault(path)
		if !ok {
			t.Fatal("missing bootstrap default", path)
		}
		files[path] = string(raw)
	}
	writeTree(t, root, files)
}
