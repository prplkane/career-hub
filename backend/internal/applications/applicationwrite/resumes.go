package applicationwrite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/intake/resumelibrary"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

// The owning result releases verified bytes only after all read guards and
// cleanup succeed. Metadata stays in Outcome.Result; bytes are not JSON/base64.
type ResumeOutcome struct {
	Outcome *Outcome
	Bytes   []byte
	IsFile  bool
}

func (ResumeOutcome) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted resume outcome]")
}
func resumeProblem(p *resumelibrary.Error) *Error {
	if p == nil {
		return nil
	}
	return &Error{Stage: p.Stage, Code: p.Code, SourceClass: p.SourceClass, SourceMessage: p.SourceMessage}
}

func (s *Service) Resume(ctx context.Context, r resumelibrary.Request) (*ResumeOutcome, *Error) {
	return s.resumePrepared(ctx, func(*sourceReads) (resumelibrary.Request, *Error) { return r, nil })
}

func (s *Service) resumePrepared(ctx context.Context, prepare func(*sourceReads) (resumelibrary.Request, *Error)) (*ResumeOutcome, *Error) {
	var content []byte
	isFile := false
	out, p := s.run(ctx, "", func(reads *sourceReads) (mutation, *Error) {
		reads.resumeReads = &resumeReads{owner: reads}
		r, problem := prepare(reads)
		if problem != nil {
			return nil, problem
		}
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			var before resumelibrary.Witness
			observe := func(id string) *resumelibrary.Error {
				var p *resumelibrary.Error
				before, p = resumelibrary.MutationWitness(ctx, tx, id)
				return p
			}
			result, p := resumelibrary.ApplyObservedInTransaction(ctx, tx, r, s.now(), s.resumeEntropy, reads.resumeReads, observe)
			if p != nil {
				return nil, resumeProblem(p)
			}
			if result.File != nil {
				if e := reads.planResumePublication(ctx, tx, r.Operation, *result.File, before); e != nil {
					return nil, e
				}
			}
			content, isFile = result.Bytes, result.IsFile
			return &result.Mutation, nil
		}, nil
	})
	result := &ResumeOutcome{Outcome: out}
	if p == nil {
		result.Bytes = content
		result.IsFile = isFile
	}
	return result, p
}

type resumeReads struct {
	owner *sourceReads
	files []captureObservation
	total int
}

func (r *resumeReads) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	if r.owner.ctx.Err() != nil {
		return nil, false, &runtimepaths.Error{Code: "READ_CANCELED"}
	}
	if limit < 1 || limit > resumelibrary.MaxResumeBytes {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	for _, seen := range r.files {
		if seen.path == path {
			if int64(len(seen.raw)) > limit {
				return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
			}
			return seen.raw, seen.present, nil
		}
	}
	raw, present, e := r.owner.root.ReadSingleLinkFile(path, limit)
	if e != nil {
		return nil, false, e
	}
	if len(r.files) >= 2 || len(raw) > 2*resumelibrary.MaxResumeBytes-r.total {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	r.total += len(raw)
	r.files = append(r.files, captureObservation{path, raw, present})
	return raw, present, nil
}
func (r *resumeReads) unchanged() *Error {
	for _, seen := range r.files {
		if r.owner.ctx.Err() != nil {
			return canceled()
		}
		raw, present, p := r.owner.root.ReadSingleLinkFile(seen.path, resumelibrary.MaxResumeBytes)
		if p != nil || present != seen.present || !bytes.Equal(raw, seen.raw) {
			return failed("source", "RESUME_LIBRARY_FILE_CHANGED")
		}
	}
	return nil
}

// RecoverResumeWrites has no arbitrary path or request replay. It settles only
// the fixed intent in this already-owned current-schema home.
func (s *Service) RecoverResumeWrites(ctx context.Context) (*Outcome, *Error) {
	return s.runMode(ctx, func(reads *sourceReads) (mutation, *Error) {
		raw, present, e := reads.root.ReadSingleLinkFile(datahome.ResumeRecoveryPath, resumeIntentLimit)
		if e != nil {
			reads.resumeRecovery = &resumePublication{recorded: true}
			return nil, failed("intent", "RESUME_INTENT_UNAVAILABLE")
		}
		if present {
			p := &resumePublication{recorded: true, raw: raw}
			reads.resumeRecovery = p
			var e *Error
			p.intent, e = decodeResumeIntent(raw, reads.home)
			if e != nil {
				return nil, e
			}
		}
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			p := reads.resumeRecovery
			state := "none"
			published := false
			if p != nil {
				if e := p.settle(ctx, reads.root, tx, reads.pins, reads.home, "", s.checkpoint); e != nil {
					return nil, e
				}
				state, published = p.state, p.published
			} else if found, e := reads.root.Exists(datahome.ResumeRecoveryPath); e != nil || found {
				reads.resumeRecovery = &resumePublication{recorded: true}
				return nil, failed("intent", "RESUME_INTENT_CHANGED")
			}
			raw, _ := json.Marshal(map[string]any{"recovered": p != nil, "database_state": state, "resume_file_published": published})
			return &applications.MutationResult{Result: raw, Changed: false}, nil
		}, nil
	}, "resume")
}
