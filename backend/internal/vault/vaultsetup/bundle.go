package vaultsetup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"careerhub.local/backend/internal/applications/applicationhelper"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/vault/vaultbundle"
	"careerhub.local/backend/internal/vault/vaultread"
)

// PrepareBundle decodes a Career Vault export file and validates everything
// that does not need disk: the closed bundle format, the eight Vault files
// (the same validator as eight-file setup), the saved short answers and both
// custom catalogs on their own. The prospective catalog and skill links are
// checked against the destination's core catalog by Preview/Apply.
func PrepareBundle(ctx context.Context, raw []byte) (*Bundle, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, &Error{"VAULT_SETUP_CANCELED"}
	}
	files, problem := vaultbundle.Decode(raw)
	if problem != nil {
		return nil, &Error{problem.Code}
	}
	vault := map[string][]byte{}
	for _, path := range vaultread.SetupPaths() {
		vault[path] = files[path]
	}
	if problem := vaultread.ValidateSetupFiles(ctx, vault); problem != nil {
		return nil, &Error{problem.Code}
	}
	if answers, present := files[applicationhelper.AnswersPath]; present && applicationhelper.ValidateAnswerFile(answers) != nil {
		return nil, &Error{"VAULT_SETUP_ANSWERS_INVALID"}
	}
	if _, problem := cataloginput.DecodeGroups(files[catalogstore.CustomGroupsPath], true); problem != nil {
		return nil, &Error{"VAULT_SETUP_CATALOG_INVALID"}
	}
	if _, problem := cataloginput.DecodeTerms(files[catalogstore.CustomSkillsPath], true); problem != nil {
		return nil, &Error{"VAULT_SETUP_CATALOG_INVALID"}
	}
	return &Bundle{files: files, bundled: true}, nil
}

// bundledCatalog serves only the two bundled custom catalog files to the
// catalog reader, in place of the destination's own. Any other path is refused.
type bundledCatalog map[string][]byte

func (b bundledCatalog) Check(path string) *runtimepaths.Error {
	if path != catalogstore.CustomGroupsPath && path != catalogstore.CustomSkillsPath {
		return &runtimepaths.Error{Code: "PATH_OUTSIDE_SCOPE"}
	}
	return nil
}
func (b bundledCatalog) ReadFile(path string, _ int64) ([]byte, bool, *runtimepaths.Error) {
	if problem := b.Check(path); problem != nil {
		return nil, false, problem
	}
	raw, present := b[path]
	return bytes.Clone(raw), present, nil
}

// checkBundleSkills builds the prospective catalog (this application's core
// catalog plus the bundled custom catalogs) and refuses any Vault record whose
// competency_ids name a skill that catalog lacks. Import and export share it,
// so export never writes a file that import would refuse.
func checkBundleSkills(ctx context.Context, resourceRoot string, files map[string][]byte) *Error {
	if ctx.Err() != nil {
		return &Error{"VAULT_SETUP_CANCELED"}
	}
	resources, pathErr := runtimepaths.Open(resourceRoot)
	if pathErr != nil {
		return &Error{"VAULT_SETUP_DESTINATION_UNAVAILABLE"}
	}
	defer resources.Close()
	state, catalogErr := catalogstore.ReadState(resources, bundledCatalog{
		catalogstore.CustomGroupsPath: files[catalogstore.CustomGroupsPath],
		catalogstore.CustomSkillsPath: files[catalogstore.CustomSkillsPath],
	})
	if catalogErr != nil {
		if catalogErr.Stage == "path" || catalogErr.Stage == "read" {
			return &Error{"VAULT_SETUP_DESTINATION_UNAVAILABLE"}
		}
		return &Error{"VAULT_SETUP_CATALOG_INVALID"}
	}
	known := map[string]bool{}
	for _, skill := range state.Snapshot.Skills() {
		known[skill.ID] = true
	}
	var docs [3]cataloginput.Value
	for index, path := range [3]string{"career_vault/experience_blocks.yaml", "career_vault/project_blocks.yaml", "career_vault/education.yaml"} {
		value, problem := cataloginput.ParseYAML(files[path])
		if problem != nil {
			return &Error{"VAULT_YAML_INVALID"}
		}
		docs[index] = value
	}
	if ctx.Err() != nil {
		return &Error{"VAULT_SETUP_CANCELED"}
	}
	if unresolvedSkillLinks(docs[0], docs[1], docs[2], known) > 0 {
		return &Error{"VAULT_SETUP_SKILL_UNKNOWN"}
	}
	return nil
}

// bundlePlan hashes a distinct operation and every path with its digest,
// presence and action, so a different file or destination needs a new preview.
func (s *Service) bundlePlan(bundle *Bundle, state map[string]observed) *Plan {
	plan := &Plan{ContractVersion: 1, Create: []string{}, Retain: []string{}, Replace: []string{}}
	hash := sha256.New()
	header, _ := json.Marshal(struct {
		Home      any
		Operation string
	}{s.home, "vault-bundle-import-v1"})
	hash.Write(header)
	for _, path := range bundle.paths() {
		entry := state[path]
		effect := action(bundle, path, entry)
		switch effect {
		case "create":
			plan.Create = append(plan.Create, path)
		case "retain":
			plan.Retain = append(plan.Retain, path)
		case "replace":
			plan.Replace = append(plan.Replace, path)
		}
		digest := sha256.Sum256(bundle.files[path])
		encoded, _ := json.Marshal(struct {
			Path, Digest, Action string
			Present              bool
		}{path, hex.EncodeToString(digest[:]), effect, entry.present})
		hash.Write(encoded)
	}
	plan.PlanID = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	return plan
}
