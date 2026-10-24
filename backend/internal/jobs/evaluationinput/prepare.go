// Package evaluationinput shares full, current Vault/registry/concept preparation.
// Callers retain admitted, immutable source observations through publication.
// This does not reuse validation tokens, read documents, or own a transaction.
package evaluationinput

import (
	"context"
	"fmt"
	"io"
	"time"

	"careerhub.local/backend/internal/jobs/jobevidence"
	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/vault/vaultread"
)

const conceptsPath = "src/data/requirement_concepts.yaml"

type Source interface {
	Check(string) *runtimepaths.Error
	ReadFile(string, int64) ([]byte, bool, *runtimepaths.Error)
}
type Inputs struct {
	Candidate  *vaultread.EvaluationEvidence
	Concepts   *jobevidence.Concepts
	Expansions map[string][]string
	Registry   *catalog.Snapshot
}
type Error struct{ Code string }

func (*Error) Error() string                { return "Evaluation preparation could not be completed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func fail(code string) *Error               { return &Error{Code: code} }
func active(ctx context.Context) *Error {
	if ctx == nil || ctx.Err() != nil {
		return fail("ASSESSMENT_INPUT_CANCELED")
	}
	return nil
}

func Prepare(ctx context.Context, data, resources Source, today time.Time) (*Inputs, *Error) {
	if err := active(ctx); err != nil {
		return nil, err
	}
	if today.Year() < 1 || today.Year() > 9999 {
		return nil, fail("EVIDENCE_MONTH_INVALID")
	}
	if data == nil || resources == nil {
		return nil, fail("EVIDENCE_READ_FAILED")
	}
	candidate, vaultErr := vaultread.PrepareEvaluation(ctx, data, today)
	if err := active(ctx); err != nil {
		return nil, err
	}
	if vaultErr != nil {
		switch vaultErr.Code {
		case "VAULT_RECORD_VALIDATION_FAILED", "VAULT_REFERENCE_VALIDATION_FAILED", "VAULT_VALIDATION_UNSUPPORTED":
			return nil, fail("EVIDENCE_VAULT_INVALID")
		case "VAULT_INPUT_LIMIT", "VAULT_VALIDATION_LIMIT", "EVIDENCE_INPUT_LIMIT", "EVIDENCE_TEXT_LIMIT", "EVIDENCE_INDEX_LIMIT", "EVIDENCE_ID_LIMIT":
			return nil, fail("EVIDENCE_INPUT_LIMIT")
		case "EVIDENCE_ID_UNSUPPORTED", "EVIDENCE_DATES_UNSUPPORTED", "VAULT_INPUT_UNSUPPORTED":
			return nil, fail("EVIDENCE_INPUT_UNSUPPORTED")
		default:
			return nil, fail("EVIDENCE_VAULT_READ_FAILED")
		}
	}
	registry, catalogErr := catalogstore.ReadState(resources, data)
	if err := active(ctx); err != nil {
		return nil, err
	}
	if catalogErr != nil {
		return nil, fail("EVIDENCE_CATALOG_INVALID")
	}
	raw, present, pathErr := resources.ReadFile(conceptsPath, cataloginput.YAMLMaxInputBytes)
	if err := active(ctx); err != nil {
		return nil, err
	}
	if pathErr != nil {
		return nil, fail("EVIDENCE_READ_FAILED")
	}
	value := cataloginput.Value{Kind: "null"}
	if present {
		var inputErr *cataloginput.YAMLError
		value, inputErr = cataloginput.ParseYAML(raw)
		if inputErr != nil {
			return nil, fail("EVIDENCE_CONCEPT_INVALID")
		}
	}
	concepts, err := jobevidence.LoadConcepts(ctx, value)
	if err != nil {
		return nil, evidencePreparationError(err)
	}
	// Deliberate source correction: archived registry edges must not provide
	// inferred parent coverage. Direct text evidence is still independently valid.
	// Removing an archived intermediate also breaks traversal through that edge.
	active := []catalog.Term{}
	for _, term := range registry.Snapshot.Skills() {
		if !term.Archived {
			active = append(active, term)
		}
	}
	expansions, err := jobevidence.BuildExpansions(ctx, active)
	if err != nil {
		return nil, evidencePreparationError(err)
	}
	return &Inputs{Candidate: candidate, Concepts: concepts, Expansions: expansions, Registry: registry.Snapshot}, nil
}
func evidencePreparationError(err *jobevidence.Error) *Error {
	switch err.Code {
	case "EVIDENCE_CANCELED", "EVIDENCE_CONCEPT_INVALID", "EVIDENCE_CONCEPT_LIMIT", "EVIDENCE_EXPANSION_LIMIT", "EVIDENCE_TEXT_INVALID",
		"EVIDENCE_TEXT_LIMIT", "EVIDENCE_INPUT_LIMIT", "EVIDENCE_MONTH_INVALID":
		return fail(err.Code)
	default:
		return fail("EVIDENCE_PREPARATION_FAILED")
	}
}
