package readonly

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/skills/competencyexplorer"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/vault/vaultread"
)

func (w *Workspace) CompetencyExplorer(ctx context.Context, archived bool) (json.RawMessage, error) {
	release, e := operationgate.Acquire(ctx)
	if e != nil {
		return nil, fail("COMPETENCY_CANCELED")
	}
	defer release()
	return readOwnedAssessment(w, ctx, func(data, resources *runtimepaths.Root) (json.RawMessage, error) {
		return prepareCompetencies(ctx, data, resources, time.Now().Format("2006-01"), archived)
	})
}

func prepareCompetencies(ctx context.Context, dataSource, resourceSource evaluationSource, month string, archived bool) (json.RawMessage, error) {
	data := &evidenceReader{ctx: ctx, source: dataSource}
	resources := &evidenceReader{ctx: ctx, source: resourceSource, resource: true}
	docs, _, e := vaultread.ResumePlanSources(ctx, data)
	if e != nil {
		return nil, e
	}
	registry, loadErr := catalogstore.ReadState(resources, data)
	if loadErr != nil {
		return nil, fail("COMPETENCY_CATALOG_INVALID")
	}
	h := sha256.New()
	for _, path := range competencyexplorer.SourcePaths() {
		raw, present, e := data.ReadFile(path, cataloginput.YAMLMaxInputBytes)
		if e != nil || !present {
			return nil, fail("COMPETENCY_INPUT_UNAVAILABLE")
		}
		h.Write([]byte(path))
		h.Write([]byte{0})
		h.Write(raw)
		h.Write([]byte{0})
	}
	raw, problem := competencyexplorer.Build(ctx, docs, registry.Snapshot, fmt.Sprintf("sha256:%x", h.Sum(nil)), month, archived)
	if problem != nil {
		return nil, problem
	}
	if e := data.verify(); e != nil {
		return nil, e
	}
	if e := resources.verify(); e != nil {
		return nil, e
	}
	return raw, nil
}
