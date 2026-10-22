package catalogwrite

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"

	"careerhub.local/backend/internal/agentvocabulary"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/runtimepaths"
)

type ExactVocabularyRequest struct {
	JobID, Name, PrimaryGroupID string
	SourceLine                  *int
	ConfirmedExact              bool
}

func (ExactVocabularyRequest) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted exact vocabulary request]")
}

// Registry-stage result, not the full agent receipt: successful creation must
// still attempt the separate JD reanalysis workflow. Record is a closed catalog
// lookup/create projection, never caller-provided raw metadata.
type ExactVocabularyResult struct {
	Operation, JobID, SourceRef, RegistryRevision string
	Record                                        any
	JobDraft                                      bool
	// Internal handoff witnesses, not fields of the public agent receipt.
	CardFileSHA256, RawFileSHA256 [32]byte
	CatalogRevision               string
}
type ExactVocabularyOutcome struct {
	Result      *ExactVocabularyResult
	Publication *ArchiveOutcome
}

type exactObservation struct {
	raw     []byte
	present bool
	limit   int64
}
type exactSourceReads struct {
	source jobcard.Source
	reads  map[string]exactObservation
}

func (r *exactSourceReads) Check(path string) *runtimepaths.Error { return r.source.Check(path) }
func (r *exactSourceReads) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	raw, present, e := r.source.ReadFile(path, limit)
	if e != nil {
		return nil, false, e
	}
	if old, seen := r.reads[path]; seen {
		if old.present != present || !bytes.Equal(old.raw, raw) {
			return nil, false, &runtimepaths.Error{Code: "FILE_CHANGED"}
		}
	} else {
		if len(r.reads) >= 2 {
			return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
		}
		r.reads[path] = exactObservation{bytes.Clone(raw), present, limit}
	}
	return raw, present, nil
}
func (r *exactSourceReads) unchanged() bool {
	for path, old := range r.reads {
		if r.source.Check(path) != nil {
			return false
		}
		raw, present, e := r.source.ReadFile(path, old.limit)
		if e != nil || old.present != present || !bytes.Equal(old.raw, raw) {
			return false
		}
	}
	return true
}
func exactString(v cataloginput.Value, key string) string {
	for _, entry := range v.Entries {
		if entry.Key.Kind == "string" && entry.Key.Text == key && entry.Value.Kind == "string" {
			return entry.Value.Text
		}
	}
	return ""
}
func exactFailure(code string) *Error {
	return &Error{Stage: "exact_vocabulary", Code: "AGENT_VOCABULARY_" + code}
}

// AddExactJobSkill never registers an application or changes candidate evidence.
// All reads and the existing private catalog transaction share one owner/gate.
// Reanalysis must run after this owner returns, not while holding its gate.
func (s *Service) AddExactJobSkill(ctx context.Context, request ExactVocabularyRequest) (*ExactVocabularyOutcome, *Error) {
	out := &ExactVocabularyOutcome{Publication: &ArchiveOutcome{}}
	if !request.ConfirmedExact {
		return out, exactFailure("CONFIRMATION_REQUIRED")
	}
	if !jobcard.ValidID(request.JobID) || len(request.Name) > 1<<20 {
		return out, exactFailure("REQUEST_INVALID")
	}
	if request.SourceLine != nil {
		line := *request.SourceLine
		request.SourceLine = &line
	}
	name, e := editName(request.Name, "skill name")
	if e != nil {
		return out, e
	}
	var result *ExactVocabularyResult
	out.Publication, e = s.runArchive(ctx, false, func(a *archiveSession, report *ArchiveOutcome) *Error {
		observed := &exactSourceReads{a.data, map[string]exactObservation{}}
		loaded, je := jobcard.Read(ctx, observed, "jobs/normalized/"+request.JobID+".yaml")
		if je != nil || !loaded.Report.SchemaValid() || !loaded.RawPresent || exactString(loaded.Card, "id") != request.JobID {
			return exactFailure("JOB_INVALID")
		}
		occurrence, se := agentvocabulary.FindExactSource(ctx, []byte(loaded.RawText), name, request.SourceLine, true)
		if se != nil {
			return &Error{Stage: "exact_vocabulary", Code: se.Code}
		}
		core := &archiveReadSet{a.resources, map[string]archiveObservation{}}
		private := &archiveReadSet{a.data, map[string]archiveObservation{}}
		state, ce := catalogstore.ReadState(core, private)
		if ce != nil {
			return &Error{Stage: "load", Code: "CATALOG_LOAD_FAILED", Load: ce}
		}
		lookup, le := state.Snapshot.Lookup([]string{name})
		if le != nil {
			return exactFailure("LOOKUP_FAILED")
		}
		rows, ok := lookup["results"].([]any)
		if !ok || len(rows) != 1 {
			return exactFailure("LOOKUP_FAILED")
		}
		row, ok := rows[0].(map[string]any)
		if !ok {
			return exactFailure("LOOKUP_FAILED")
		}
		result = &ExactVocabularyResult{JobID: request.JobID, SourceRef: "job:" + request.JobID + "#line:" + strconv.Itoa(occurrence.Line), RegistryRevision: state.Snapshot.Revision(), JobDraft: exactString(loaded.Card, "status") == "draft",
			CardFileSHA256: loaded.CardFileSHA256, RawFileSHA256: loaded.RawFileSHA256, CatalogRevision: state.Snapshot.CatalogRevision()}
		if row["status"] == "exact_match" || row["status"] == "alias_match" {
			if !observed.unchanged() || !core.unchanged() || !private.unchanged() || !a.ready() {
				return revisionConflict()
			}
			if e := cancelled(ctx); e != nil {
				return e
			}
			result.Operation, result.Record = "no_change_needed", row["match"]
			report.Revision = state.Snapshot.Revision()
			return nil
		}
		archived, ok := row["archived_candidates"].([]any)
		if !ok || row["status"] != "unmatched" || len(archived) != 0 {
			return exactFailure("VOCABULARY_COLLISION")
		}
		activeGroup := false
		for _, group := range state.Snapshot.Groups() {
			if group.ID == request.PrimaryGroupID && !group.Archived {
				activeGroup = true
				break
			}
		}
		if !activeGroup {
			return exactFailure("GROUP_UNAVAILABLE")
		}
		if s.now == nil {
			return exactFailure("CLOCK_UNAVAILABLE")
		}
		plan, pe := planEdit(ctx, state, editRequest{operation: createSkill, payload: map[string]any{"name": name, "aliases": []any{}, "primary_group_id": request.PrimaryGroupID}, revision: state.Snapshot.Revision(), now: s.now()})
		if pe != nil {
			return pe
		}
		if e := publishCatalogObserved(ctx, a, core, private, plan.replacements, plan.prospective, report, observed.unchanged); e != nil {
			return e
		}
		result.Operation, result.Record, result.RegistryRevision = "created", plan.skill.Record, report.Revision
		result.CatalogRevision = plan.prospective.CatalogRevision()
		return nil
	})
	if e == nil {
		out.Result = result
	}
	return out, e
}
