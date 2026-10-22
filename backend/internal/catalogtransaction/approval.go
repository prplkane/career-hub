package catalogtransaction

import (
	"bytes"
	"context"
	"encoding/hex"
	"strings"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
)

const ApprovalJournalPath = datahome.ApprovalRecoveryPath

// Reuse the existing native mutation ceiling for the complete encoded journal.
// Seven before/after pairs plus one result must fit together; per-leaf 2 MiB
// bounds alone do not imply aggregate admissibility. Refuse before any writes.
const MaxApprovalJournalBytes = int(runtimepaths.MaxReadBytes)

var approvalVaultPaths = [...]string{
	"career_vault/profile.yaml", "career_vault/education.yaml",
	"career_vault/experience_blocks.yaml", "career_vault/project_blocks.yaml",
	"career_vault/skill_blocks.yaml",
}

func approvalSourceID(id string) bool {
	if len(id) < 5 || len(id) > 512 || !strings.HasPrefix(id, "src_") {
		return false
	}
	for _, c := range id[4:] {
		if c != '_' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func validApprovalIntent(data intent) bool {
	if data.Version != 3 || len(data.Changes) != 8 || len(data.Guards) != 2 {
		return false
	}
	result := data.Changes[7]
	if !approvalSourceID(result.RecordID) || result.BeforePresent || len(result.Before) != 0 || result.CheckOnly || len(result.After) == 0 || len(result.After) > cataloginput.YAMLMaxInputBytes {
		return false
	}
	base := "career_vault/intake_runs/" + result.RecordID + "/"
	if result.Path != base+"approval_result.yaml" {
		return false
	}
	for i, path := range approvalVaultPaths {
		c := data.Changes[i]
		if c.Path != path || c.RecordID != "" || !c.BeforePresent || len(c.Before) > cataloginput.YAMLMaxInputBytes || len(c.After) > cataloginput.YAMLMaxInputBytes || c.CheckOnly && !bytes.Equal(c.Before, c.After) {
			return false
		}
	}
	if data.Changes[5].Path != catalogstore.CustomGroupsPath || data.Changes[6].Path != catalogstore.CustomSkillsPath {
		return false
	}
	catalogs := append([]Change(nil), data.Changes[5:7]...)
	// Result creation is a mutation even if all Vault/catalog leaves are checks.
	if catalogs[0].CheckOnly && catalogs[1].CheckOnly {
		if !bytes.Equal(catalogs[0].Before, catalogs[0].After) {
			return false
		}
		catalogs[0].CheckOnly = false
	}
	if !valid(data.HomeID, catalogs) {
		return false
	}
	for i, leaf := range []string{"extracted.yaml", "review.yaml"} {
		guard := data.Guards[i]
		if guard.Path != base+leaf || len(guard.Revision) != 71 || !strings.HasPrefix(guard.Revision, "sha256:") {
			return false
		}
		digest, err := hex.DecodeString(guard.Revision[7:])
		if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != guard.Revision[7:] {
			return false
		}
	}
	return true
}

// BeginApprovalOwned publishes only the closed approval policy. The caller
// owns the shared operation gate and must validate the full proposal/result
// against guarded source evidence, before-images and current shipped catalogs.
func BeginApprovalOwned(ctx context.Context, root *runtimepaths.Root, homeID string, changes []Change, guards []Guard, validate Validate) (Report, error) {
	if err := stopped(ctx); err != nil {
		return Report{}, err
	}
	data := intent{Version: 3, HomeID: homeID, Changes: changes, Guards: guards}
	if !validApprovalIntent(data) || validate == nil {
		return Report{}, ErrInput
	}
	if info, err := datahome.InspectOwnedForApprovalRecovery(root, nil); err != nil || info.DataHomeID != homeID {
		return Report{}, ErrInput
	}
	return beginIntent(ctx, root, data, validate)
}

// RecoverApprovalOwned refuses third-state edits and every other recovery
// sentinel. Revalidation must reconstruct the original proposal, not merely
// accept any syntactically valid YAML found in an untrusted journal.
func RecoverApprovalOwned(ctx context.Context, root *runtimepaths.Root, homeID string, validate Validate) (Report, error) {
	if err := stopped(ctx); err != nil {
		return Report{}, err
	}
	if validate == nil {
		return Report{}, ErrInput
	}
	if info, err := datahome.InspectOwnedForApprovalRecovery(root, nil); err != nil || info.DataHomeID != homeID {
		return Report{}, ErrInput
	}
	return resumePolicy(ctx, root, homeID, 3, validate)
}
