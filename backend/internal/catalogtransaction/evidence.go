package catalogtransaction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
)

const EvidenceJournalPath = datahome.EvidenceRecoveryPath

// This is also the existing native writer's ceiling. Oversized combined
// before/after states fail before intent creation; never silently truncate.
const MaxEvidenceJournalBytes = 16 << 20

// Guard binds an unchanged required Vault companion without duplicating its
// private contents in the journal. All seven fixed companions must be present
// with the same raw SHA-256 before publication and during explicit recovery.
type Guard struct {
	Path     string `json:"path"`
	Revision string `json:"revision"`
}

var vaultPaths = [...]string{
	"career_vault/profile.yaml", "career_vault/education.yaml",
	"career_vault/visa_profile.yaml", "career_vault/experience_blocks.yaml",
	"career_vault/project_blocks.yaml", "career_vault/skill_blocks.yaml",
	"career_vault/proof_blocks.yaml", "career_vault/style_rules.yaml",
}

func policy(version int) (path string, limit, nodes int) {
	switch version {
	case 1:
		return JournalPath, MaxJournalBytes, 128
	case 2:
		return EvidenceJournalPath, MaxEvidenceJournalBytes, 384
	case 3:
		return ApprovalJournalPath, MaxApprovalJournalBytes, 512
	default:
		return "", 0, 0
	}
}

func validIntent(data intent) bool {
	if data.Version == 3 {
		return validApprovalIntent(data)
	}
	if data.Version == 1 {
		return len(data.Guards) == 0 && valid(data.HomeID, data.Changes)
	}
	if data.Version != 2 || len(data.Changes) != 3 || len(data.Guards) != 7 {
		return false
	}
	record := data.Changes[0]
	if record.RecordID == "" || len(record.RecordID) > cataloginput.YAMLMaxInputBytes || !utf8.ValidString(record.RecordID) {
		return false
	}
	if record.Path != vaultPaths[3] && record.Path != vaultPaths[4] {
		return false
	}
	if !record.BeforePresent || record.CheckOnly ||
		len(record.Before) > cataloginput.YAMLMaxInputBytes || len(record.After) > cataloginput.YAMLMaxInputBytes {
		return false
	}
	if data.Changes[1].Path != catalogstore.CustomGroupsPath || data.Changes[2].Path != catalogstore.CustomSkillsPath {
		return false
	}
	// Reuse the existing catalog whitelist/type/size checks. A canonical record
	// is still a mutation when both companion catalogs are check-only.
	catalogs := append([]Change(nil), data.Changes[1:]...)
	allChecks := catalogs[0].CheckOnly && catalogs[1].CheckOnly
	if allChecks {
		for _, item := range catalogs {
			if !bytes.Equal(item.Before, item.After) {
				return false
			}
		}
		catalogs[0].CheckOnly = false
	}
	if !valid(data.HomeID, catalogs) {
		return false
	}
	index := 0
	for _, path := range vaultPaths {
		if path == record.Path {
			continue
		}
		guard := data.Guards[index]
		if guard.Path != path || !strings.HasPrefix(guard.Revision, "sha256:") || len(guard.Revision) != 71 {
			return false
		}
		raw, e := hex.DecodeString(guard.Revision[7:])
		if e != nil || len(raw) != 32 || hex.EncodeToString(raw) != guard.Revision[7:] {
			return false
		}
		index++
	}
	return true
}

func checkGuards(ctx context.Context, root storage, guards []Guard) error {
	for _, guard := range guards {
		if err := stopped(ctx); err != nil {
			return err
		}
		raw, present, e := root.ReadFile(guard.Path, cataloginput.YAMLMaxInputBytes)
		if e != nil || len(raw) > cataloginput.YAMLMaxInputBytes {
			return ErrIO
		}
		if !present || fmt.Sprintf("sha256:%x", sha256.Sum256(raw)) != guard.Revision {
			return ErrConflict
		}
	}
	return nil
}

// BeginEvidenceOwned/RecoverEvidenceOwned are not a generic filesystem
// transaction API: exactly one existing Experience/Project file, both private
// catalogs, and all seven immutable Vault companions form the closed policy.
// The caller must hold the shared operation gate and validate the complete
// prospective Vault/catalog and response using current shipped resources.
func BeginEvidenceOwned(ctx context.Context, root *runtimepaths.Root, homeID string,
	changes []Change, guards []Guard, validate Validate) (Report, error) {
	if e := stopped(ctx); e != nil {
		return Report{}, e
	}
	data := intent{Version: 2, HomeID: homeID, Changes: changes, Guards: guards}
	if !validIntent(data) || validate == nil {
		return Report{}, ErrInput
	}
	if info, e := datahome.InspectOwnedForEvidenceRecovery(root, nil); e != nil || info.DataHomeID != homeID {
		return Report{}, ErrInput
	}
	return beginIntent(ctx, root, data, validate)
}
func RecoverEvidenceOwned(ctx context.Context, root *runtimepaths.Root, homeID string, validate Validate) (Report, error) {
	if e := stopped(ctx); e != nil {
		return Report{}, e
	}
	if validate == nil {
		return Report{}, ErrInput
	}
	if info, e := datahome.InspectOwnedForEvidenceRecovery(root, nil); e != nil || info.DataHomeID != homeID {
		return Report{}, ErrInput
	}
	return resumePolicy(ctx, root, homeID, 2, validate)
}
