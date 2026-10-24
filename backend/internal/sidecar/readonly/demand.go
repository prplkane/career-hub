package readonly

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/skills/demandgaps"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

// demandReader adds an aggregate budget and final byte recheck to the existing
// bounded Job Card reader. Only jobcard.Read chooses paths beneath the owned root.
type demandReader struct {
	ctx   context.Context
	root  jobcard.Source
	bytes int
	seen  map[string]demandObservation
}
type demandObservation struct {
	limit   int64
	present bool
	hash    [32]byte
}

func (r *demandReader) Check(path string) *runtimepaths.Error { return r.root.Check(path) }
func (r *demandReader) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	if r.ctx.Err() != nil {
		return nil, false, &runtimepaths.Error{Code: "DEMAND_CANCELED"}
	}
	raw, present, e := r.root.ReadFile(path, limit)
	if e != nil {
		return nil, false, e
	}
	r.bytes += len(raw)
	if r.bytes > 64<<20 {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	observation := demandObservation{limit, present, sha256.Sum256(raw)}
	if old, ok := r.seen[path]; ok && old != observation {
		return nil, false, &runtimepaths.Error{Code: "DEMAND_INPUT_CHANGED"}
	}
	r.seen[path] = observation
	return raw, present, nil
}
func (r *demandReader) verify() error {
	for path, old := range r.seen {
		if r.ctx.Err() != nil {
			return fail("DEMAND_CANCELED")
		}
		raw, present, e := r.root.ReadFile(path, old.limit)
		if e != nil || present != old.present || sha256.Sum256(raw) != old.hash {
			return fail("DEMAND_INPUT_CHANGED")
		}
	}
	return nil
}

func (w *Workspace) DemandGaps(ctx context.Context, request demandgaps.Request) (json.RawMessage, error) {
	if problem := request.Validate(); problem != nil {
		return nil, problem
	}
	release, e := operationgate.Acquire(ctx)
	if e != nil {
		return nil, fail("DEMAND_CANCELED")
	}
	defer release()
	month := request.AsOf
	if month == "" {
		month = time.Now().Format("2006-01")
	}
	return withJobSources(w, ctx, func(data, resources *runtimepaths.Root, entries []runtimepaths.FileMetadata) (json.RawMessage, error) {
		if len(entries) > demandgaps.MaxJobs {
			return nil, fail("DEMAND_INPUT_LIMIT")
		}
		evidence, e := prepareCompetencies(ctx, data, resources, month, false)
		if e != nil {
			return nil, e
		}
		private := &evidenceReader{ctx: ctx, source: data}
		shared := &evidenceReader{ctx: ctx, source: resources, resource: true}
		registry, problem := catalogstore.ReadState(shared, private)
		if problem != nil {
			return nil, fail("DEMAND_CATALOG_INVALID")
		}
		ids := make([]string, 0, len(entries))
		for _, entry := range entries {
			ids = append(ids, strings.TrimSuffix(entry.Name, filepath.Ext(entry.Name)))
		}
		summaries, e := w.ApplicationSummaries(ctx, ids)
		if e != nil {
			return nil, e
		}
		reader := &demandReader{ctx: ctx, root: data, seen: map[string]demandObservation{}}
		jobs := []demandgaps.Job{}
		for _, entry := range entries {
			id := strings.TrimSuffix(entry.Name, filepath.Ext(entry.Name))
			summary, registered := summaries[id]
			if !registered {
				continue
			}
			loaded, problem := jobcard.Read(ctx, reader, "jobs/normalized/"+entry.Name)
			if problem != nil || !loaded.Report.SchemaValid() {
				return nil, fail("DEMAND_JOB_INVALID")
			}
			var captured, fingerprint string
			raw, _ := json.Marshal(summary.CreatedAt)
			if json.Unmarshal(raw, &captured) != nil {
				return nil, fail("DEMAND_JOB_INVALID")
			}
			raw, _ = json.Marshal(summary.ContentFingerprint)
			if json.Unmarshal(raw, &fingerprint) != nil {
				return nil, fail("DEMAND_JOB_INVALID")
			}
			jobs = append(jobs, demandgaps.Job{ID: id, CapturedAt: captured, Fingerprint: fingerprint, Card: loaded.Card})
		}
		result, problemBuild := demandgaps.Build(ctx, registry.Snapshot, evidence, jobs, len(entries), len(summaries), request)
		if problemBuild != nil {
			return nil, problemBuild
		}
		if e := reader.verify(); e != nil {
			return nil, e
		}
		if e := private.verify(); e != nil {
			return nil, e
		}
		if e := shared.verify(); e != nil {
			return nil, e
		}
		after, e := prepareCompetencies(ctx, data, resources, month, false)
		if e != nil || !bytes.Equal(evidence, after) {
			return nil, fail("DEMAND_INPUT_CHANGED")
		}
		latest, e := w.ApplicationSummaries(ctx, ids)
		if e != nil {
			return nil, e
		}
		beforeJSON, _ := json.Marshal(summaries)
		afterJSON, _ := json.Marshal(latest)
		if !bytes.Equal(beforeJSON, afterJSON) {
			return nil, fail("DEMAND_INPUT_CHANGED")
		}
		return result, nil
	})
}
