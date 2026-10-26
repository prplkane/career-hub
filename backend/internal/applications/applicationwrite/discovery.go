package applicationwrite

import (
	"context"
	"database/sql"
	"encoding/json"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/storage/datahome"
)

// RecoverDiscoveryWrites settles only this home's fixed Discovery intent. It
// requires existing native ownership and schema; there is no migration, normal
// capture retry, arbitrary journal path or bypass of another recovery gate.
func (s *Service) RecoverDiscoveryWrites(ctx context.Context) (*Outcome, *Error) {
	return s.runMode(ctx, func(reads *sourceReads) (mutation, *Error) {
		raw, present, pe := reads.root.ReadSingleLinkFile(datahome.DiscoveryRecoveryPath, discoveryIntentLimit)
		if pe != nil {
			// An unreadable pending intent is not evidence that recovery is done.
			reads.recovery = &discoveryPublication{recorded: true}
			return nil, failed("intent", "DISCOVERY_INTENT_UNAVAILABLE")
		}
		if present {
			p := &discoveryPublication{raw: raw, recorded: true}
			reads.recovery = p
			var e *Error
			p.intent, e = decodeDiscoveryIntent(raw, reads.home)
			if e != nil {
				return nil, e
			}
		}
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			p := reads.recovery
			state, applied := "none", 0
			if p != nil {
				if e := p.settle(ctx, reads.root, tx, reads.pins, reads.home, "", s.checkpoint); e != nil {
					return nil, e
				}
				state, applied = p.state, p.applied
			} else {
				if found, e := reads.root.Exists(datahome.DiscoveryRecoveryPath); e != nil || found {
					reads.recovery = &discoveryPublication{recorded: true}
					return nil, failed("intent", "DISCOVERY_INTENT_CHANGED")
				}
			}
			raw, _ := json.Marshal(struct {
				Recovered      bool   `json:"recovered"`
				DatabaseState  string `json:"database_state"`
				FilesPublished int    `json:"source_files_published"`
			}{p != nil, state, applied})
			return &applications.MutationResult{Result: raw, Changed: false}, nil
		}, nil
	}, "discovery")
}

func discoveryProblem(e *discovery.WriteError) *Error {
	if e == nil {
		return nil
	}
	return &Error{Stage: e.Stage, Code: e.Code, SourceClass: e.SourceClass, SourceMessage: e.SourceMessage}
}

// Discovery uses this same owned SQLite ledger; there is no nested transaction,
// independent process lock or implicitly committing file helper.
func (s *Service) CaptureDiscovery(ctx context.Context, input discovery.CaptureRequest) (*Outcome, *Error) {
	return s.run(ctx, "", func(reads *sourceReads) (mutation, *Error) {
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			before := []discovery.RowWitness{}
			observe := func(ctx context.Context, tx *sql.Tx, ids []string) *discovery.WriteError {
				var e *discovery.WriteError
				before, e = discovery.MutationWitnesses(ctx, tx, ids)
				return e
			}
			result, e := discovery.CaptureObservedInTransaction(ctx, tx, reads, input, s.now(), s.newDiscoveryID, observe)
			if e != nil {
				return nil, discoveryProblem(e)
			}
			if result.Source != nil {
				if e := reads.planPublication(ctx, tx, []discovery.SourceChange{*result.Source}, before); e != nil {
					return nil, e
				}
			}
			return &applications.MutationResult{Result: result.Result, Changed: result.Changed}, nil
		}, nil
	})
}
func (s *Service) EditDiscovery(ctx context.Context, input discovery.EditRequest) (*Outcome, *Error) {
	return s.run(ctx, "", func(reads *sourceReads) (mutation, *Error) {
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			result, e := discovery.EditInTransaction(ctx, tx, reads, input, s.now())
			if e != nil {
				return nil, discoveryProblem(e)
			}
			return &applications.MutationResult{Result: result.Result, Changed: result.Changed}, nil
		}, nil
	})
}
func (s *Service) DeleteDiscoveries(ctx context.Context, input discovery.DeleteRequest) (*Outcome, *Error) {
	return s.run(ctx, "", func(reads *sourceReads) (mutation, *Error) {
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			before := []discovery.RowWitness{}
			observe := func(ctx context.Context, tx *sql.Tx, ids []string) *discovery.WriteError {
				var e *discovery.WriteError
				before, e = discovery.MutationWitnesses(ctx, tx, ids)
				return e
			}
			result, e := discovery.DeleteObservedInTransaction(ctx, tx, reads, input, observe)
			if e != nil {
				return nil, discoveryProblem(e)
			}
			if len(result.Sources) != 0 {
				if e := reads.planPublication(ctx, tx, result.Sources, before); e != nil {
					return nil, e
				}
			}
			return &applications.MutationResult{Result: result.Result, Changed: result.Changed}, nil
		}, nil
	})
}
