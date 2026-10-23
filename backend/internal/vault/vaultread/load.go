// Package vaultread owns fixed read-only Career Vault profile loading,
// manual/calendar/reference validation and bounded JSON projection.
package vaultread

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const (
	profileIndex = iota
	educationIndex
	eligibilityIndex
	experienceIndex
	projectIndex
	skillIndex
	proofIndex
	styleIndex
	vaultFileCount
)

type schema struct {
	path, source, kind string
	required           []string
}

// Preserve CAREER_VAULT_SCHEMAS order, including the legacy skill file. These
// names are fixed application paths, not a user-configurable filesystem query.
var schemas = [vaultFileCount]schema{
	{"career_vault/profile.yaml", "profile", "mapping", []string{"name", "target_location", "work_authorization", "major"}},
	{"career_vault/education.yaml", "education", "mapping", []string{"schools"}},
	{"career_vault/visa_profile.yaml", "eligibility", "mapping", []string{"current_status", "work_authorization", "requires_future_sponsorship", "stem_opt_eligible"}},
	{"career_vault/experience_blocks.yaml", "experiences", "sequence", []string{"id", "company", "role", "dates", "category", "technologies", "proof_points"}},
	{"career_vault/project_blocks.yaml", "projects", "sequence", []string{"id", "name", "category", "summary", "technologies", "proof_points"}},
	{"career_vault/skill_blocks.yaml", "legacy_skills", "sequence", []string{"id", "skill_family", "related_experience", "keywords"}},
	{"career_vault/proof_blocks.yaml", "proof", "sequence", []string{"id", "claim", "evidence_type", "source_block"}},
	{"career_vault/style_rules.yaml", "style", "mapping", []string{"standards_metadata", "resume_voice", "bullet_rules", "document_rules", "ats_rules", "selection_rules", "quality_gates", "forbidden"}},
}

// Error contains fixed stage/source/code categories and sanitized YAML metadata.
// It must never retain a native cause, absolute path or private source prose.
// Source validation and unsupported/limit inputs remain distinct classifications.
type Error struct {
	Stage  string
	Source string
	Code   string
	Input  *cataloginput.YAMLError
	Issues []Issue
}

func (*Error) Error() string                { return "Career Vault read could not be completed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }

type reader interface {
	ReadFile(string, int64) ([]byte, bool, *runtimepaths.Error)
}

type vaultState struct {
	documents [vaultFileCount]cataloginput.Value
	revisions [vaultFileCount]string
}

// openVault does not create missing directories, unlike the prototype editor's
// create_parents=True path helper. Callers supply the explicit admitted root and
// own cross-file writer exclusion; this is not a filesystem snapshot or lease.
func openVault(ctx context.Context, root string) (state *vaultState, failure *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, canceled()
	}
	data, pathErr := runtimepaths.Open(root)
	if pathErr != nil {
		return nil, &Error{Stage: "path", Source: "data_root", Code: "VAULT_PATH_INVALID"}
	}
	defer func() {
		if err := data.Close(); err != nil && failure == nil {
			state, failure = nil, &Error{Stage: "read", Source: "data_root", Code: "VAULT_CLOSE_FAILED"}
		}
	}()
	return loadVault(ctx, data)
}

func canceled() *Error { return &Error{Stage: "context", Code: "VAULT_READ_CANCELED"} }

// Loading stops at the first read/YAML/root-shape failure. Manual validation
// intentionally follows all eight loads; no profile defaults are synthesized.
func loadVault(ctx context.Context, source reader) (*vaultState, *Error) {
	state := &vaultState{}
	for index, spec := range schemas {
		if ctx == nil || ctx.Err() != nil {
			return nil, canceled()
		}
		raw, present, pathErr := source.ReadFile(spec.path, cataloginput.YAMLMaxInputBytes)
		if pathErr != nil {
			if pathErr.Code == "FILE_SIZE_LIMIT" {
				return nil, &Error{Stage: "read", Source: spec.source, Code: "VAULT_INPUT_LIMIT"}
			}
			return nil, &Error{Stage: "read", Source: spec.source, Code: "VAULT_READ_FAILED"}
		}
		if !present {
			return nil, &Error{Stage: "read", Source: spec.source, Code: "VAULT_FILE_MISSING"}
		}
		if ctx.Err() != nil {
			return nil, canceled()
		}
		value, parseErr := parseVaultDocument(spec, raw)
		if parseErr != nil {
			return nil, parseErr
		}
		state.documents[index] = value
		// Hash the exact read immediately; do not retain eight extra raw-file
		// copies or reopen paths after validation to calculate revisions.
		state.revisions[index] = fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
	}
	if ctx.Err() != nil {
		return nil, canceled()
	}
	return state, nil
}

func parseVaultDocument(spec schema, raw []byte) (cataloginput.Value, *Error) {
	value, inputErr := cataloginput.ParseYAML(raw)
	if inputErr != nil {
		code := "VAULT_YAML_INVALID"
		switch inputErr.Class {
		case "ValueError", "KeyError", "AttributeError", "OverflowError", "TypeError":
			// These PyYAML constructor exceptions escape the prototype's
			// YAML/Unicode/OSError catch; never claim ordinary domain parity.
			code = "VAULT_INPUT_UNSUPPORTED"
		}
		if inputErr.Kind == "unsupported" {
			code = "VAULT_INPUT_UNSUPPORTED"
		}
		if inputErr.Kind == "limit" {
			code = "VAULT_INPUT_LIMIT"
		}
		return cataloginput.Value{}, &Error{Stage: "input", Source: spec.source, Code: code, Input: inputErr}
	}
	if value.Kind != spec.kind {
		return cataloginput.Value{}, &Error{Stage: "shape", Source: spec.source, Code: "VAULT_ROOT_TYPE"}
	}
	return value, nil
}
