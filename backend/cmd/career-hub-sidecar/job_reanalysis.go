package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"careerhub.local/backend/internal/applications/applicationwrite"
	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/jobs/jobdiagnostics"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/storage/datahome"
)

// The private ports permit bounded transition faults without replacing the
// production path, owner, catalog or card readers. A coordinator serializes
// one entire journal run; individual owners still take operationgate in turn.
type reanalysisPorts struct {
	home    func() (datahome.Info, error)
	digest  func(context.Context, string, readonly.DigestOptions) (*readonly.RetainedJobDigest, error)
	write   func(context.Context, datahome.Info, string, string, applicationwrite.ReanalysisPreparation) (*applicationwrite.Outcome, *applicationwrite.Error)
	saved   func(context.Context, string) (*readonly.SavedJob, error)
	last    func(context.Context, string, datahome.Info) (*jobdiagnostics.Decoded, *jobdiagnostics.StoreError)
	append  func(context.Context, string, datahome.Info, jobdiagnostics.Event) (jobdiagnostics.AppendOutcome, *jobdiagnostics.StoreError)
	observe func(context.Context, string, datahome.Info, jobdiagnostics.Decoded) (jobdiagnostics.PublicationState, *jobdiagnostics.StoreError)
}

type reanalysisCoordinator struct {
	mu       sync.Mutex
	dataRoot string
	ports    reanalysisPorts
}

func newReanalysisCoordinator(w *readonly.Workspace, start protocol.StartRecord) *reanalysisCoordinator {
	owner := applicationwrite.New(start.DataRoot)
	return &reanalysisCoordinator{dataRoot: start.DataRoot, ports: reanalysisPorts{
		home: w.StartupHome, digest: w.DigestJobSource, saved: w.SavedJob,
		write: func(ctx context.Context, home datahome.Info, id, revision string, prepared applicationwrite.ReanalysisPreparation) (*applicationwrite.Outcome, *applicationwrite.Error) {
			return owner.PinnedToHome(home).PinnedToResources(start.ResourceRoot).ReanalyzeJob(ctx, id, revision, prepared)
		},
		last: jobdiagnostics.ReadLastEvent, append: jobdiagnostics.Append, observe: jobdiagnostics.ObservePlannedPublication,
	}}
}

func reanalysisRevision(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	raw, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil && hex.EncodeToString(raw) == value[len("sha256:"):]
}

func reanalysisFailure(code string) *httpapi.ApplicationError {
	return &httpapi.ApplicationError{Code: code}
}

func (c *reanalysisCoordinator) appendExact(ctx context.Context, home datahome.Info, event jobdiagnostics.Event) bool {
	result, problem := c.ports.append(ctx, c.dataRoot, home, event)
	return problem == nil && result.Committed
}

// reconcile never replays a card write. A terminal unknown state blocks new
// runs globally, preserving its last-journal visibility until explicit review.
func (c *reanalysisCoordinator) reconcile(ctx context.Context, home datahome.Info) bool {
	last, problem := c.ports.last(ctx, c.dataRoot, home)
	if problem != nil {
		return false
	}
	if last == nil {
		return true
	}
	if last.Operation == "intake" {
		if last.Event == "started" && last.Sequence == 1 {
			terminal, err := jobdiagnostics.IntakeInterrupted(*last, "none", false)
			return err == nil && c.appendExact(ctx, home, terminal)
		}
		return last.Event == "completed" || last.Event == "failed" && !last.RecoveryRequired
	}
	if last.Operation != "reanalyze" {
		return false
	}
	if last.PublicationState == jobdiagnostics.PublicationUnknown {
		return false
	}
	var state jobdiagnostics.PublicationState
	switch last.Event {
	case "started":
		state = jobdiagnostics.PublicationNotPublished
	case "planned":
		state, problem = c.ports.observe(ctx, c.dataRoot, home, *last)
		if problem != nil {
			return false
		}
	default:
		return last.Event == "completed" || last.Event == "failed"
	}
	event, err := jobdiagnostics.Interrupted(*last, state)
	if err != nil || !c.appendExact(ctx, home, event) {
		return false
	}
	return state != jobdiagnostics.PublicationUnknown
}

func reanalysisPreparationCode(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "JOB_REANALYSIS_CANCELED"
	}
	var source *readonly.Error
	if errors.As(err, &source) {
		switch source.Code {
		case "JOB_REANALYSIS_NOT_DRAFT":
			return "JOB_REANALYSIS_NOT_DRAFT"
		case "JOB_DIGEST_SOURCE_MISSING", "JOB_REANALYSIS_SOURCE_INVALID":
			return "JOB_REANALYSIS_SOURCE_INVALID"
		}
	}
	return "JOB_REANALYSIS_PREPARATION_FAILED"
}

func reanalysisWriteCode(problem *applicationwrite.Error) string {
	if problem == nil {
		return "JOB_REANALYSIS_RESULT_INVALID"
	}
	switch problem.Code {
	case "JOB_REANALYSIS_REVISION_CONFLICT", "JOB_REANALYSIS_CARD_CHANGED":
		return "JOB_REANALYSIS_REVISION_CONFLICT"
	case "JOB_REANALYSIS_INPUT_CHANGED":
		return "JOB_REANALYSIS_INPUT_CHANGED"
	case "JOB_REANALYSIS_NOT_DRAFT":
		return "JOB_REANALYSIS_NOT_DRAFT"
	case "JOB_REANALYSIS_SOURCE_INVALID", "JOB_REANALYSIS_SOURCE_MISMATCH":
		return "JOB_REANALYSIS_SOURCE_INVALID"
	case "APPLICATION_LEDGER_CANCELED":
		return "JOB_REANALYSIS_CANCELED"
	}
	return "JOB_REANALYSIS_RESULT_INVALID"
}

func reanalysisPreparation(value *readonly.RetainedJobDigest) applicationwrite.ReanalysisPreparation {
	return applicationwrite.ReanalysisPreparation{Digest: value.Digest,
		CardFileSHA256: value.CardFileSHA256, RawFileSHA256: value.RawFileSHA256,
		ProfileFileSHA256: value.ProfileFileSHA256, ProfilePresent: value.ProfilePresent,
		CatalogRevision: value.CatalogRevision, PlannedCardSHA256: value.PlannedCardSHA256}
}

func (c *reanalysisCoordinator) Reanalyze(ctx context.Context, id, revision string) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	return c.reanalyze(ctx, id, revision, nil)
}

// A vocabulary command must not silently digest a different source or catalog
// after its first owner releases the global gate. Normal UI reanalysis retains
// its existing contract; this additional witness is internal, never caller JSON.
type vocabularyReanalysisWitness struct {
	RawFileSHA256   [32]byte
	CatalogRevision string
}

func (c *reanalysisCoordinator) reanalyze(ctx context.Context, id, revision string, observed *vocabularyReanalysisWitness) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	out := &httpapi.ApplicationOutcome{}
	if c == nil || ctx == nil || !jobcard.ValidID(id) || !reanalysisRevision(revision) || c.dataRoot == "" ||
		c.ports.home == nil || c.ports.digest == nil || c.ports.write == nil || c.ports.saved == nil ||
		c.ports.last == nil || c.ports.append == nil || c.ports.observe == nil {
		return out, reanalysisFailure("JOB_REANALYSIS_REQUEST_INVALID")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil {
		return out, reanalysisFailure("JOB_REANALYSIS_CANCELED")
	}
	home, homeErr := c.ports.home()
	if homeErr != nil {
		return out, reanalysisFailure("JOB_REANALYSIS_HOME_UNAVAILABLE")
	}
	if !c.reconcile(ctx, home) {
		out.RecoveryRequired = true
		return out, reanalysisFailure("JOB_REANALYSIS_RECOVERY_REQUIRED")
	}
	run, runErr := jobdiagnostics.New(id)
	if runErr != nil {
		return out, reanalysisFailure("JOB_REANALYSIS_DIAGNOSTIC_FAILED")
	}
	out.RunID = run.ID()
	started, eventErr := run.Started()
	if eventErr != nil || !c.appendExact(ctx, home, started) {
		out.RecoveryRequired = true
		return out, reanalysisFailure("JOB_REANALYSIS_DIAGNOSTIC_FAILED")
	}
	// No journal or writer lock is held across the fresh read. The owner later
	// checks the exact witnesses again while holding its own publication gate.
	prepared, digestErr := c.ports.digest(ctx, "jobs/normalized/"+id+".yaml", readonly.DigestOptions{Reanalysis: true})
	if digestErr != nil || prepared == nil || prepared.Digest == nil || prepared.PlannedCardSHA256 == ([32]byte{}) {
		code := reanalysisPreparationCode(digestErr)
		if !c.failRun(ctx, home, run, code, false, false) {
			out.RecoveryRequired = true
			return out, reanalysisFailure("JOB_REANALYSIS_DIAGNOSTIC_FAILED")
		}
		return out, reanalysisFailure(code)
	}
	prior := "sha256:" + hex.EncodeToString(prepared.CardFileSHA256[:])
	if prior != revision {
		out.RevisionRefreshRequired = true
		if !c.failRun(ctx, home, run, "JOB_REANALYSIS_REVISION_CONFLICT", false, false) {
			out.RecoveryRequired = true
			return out, reanalysisFailure("JOB_REANALYSIS_DIAGNOSTIC_FAILED")
		}
		return out, reanalysisFailure("JOB_REANALYSIS_REVISION_CONFLICT")
	}
	if observed != nil && (observed.RawFileSHA256 != prepared.RawFileSHA256 || observed.CatalogRevision != prepared.CatalogRevision) {
		if !c.failRun(ctx, home, run, "JOB_REANALYSIS_INPUT_CHANGED", false, false) {
			out.RecoveryRequired = true
			return out, reanalysisFailure("JOB_REANALYSIS_DIAGNOSTIC_FAILED")
		}
		return out, reanalysisFailure("JOB_REANALYSIS_INPUT_CHANGED")
	}
	noChange := prepared.CardFileSHA256 == prepared.PlannedCardSHA256
	if !noChange {
		plan, err := run.Planned(prepared.CardFileSHA256, prepared.PlannedCardSHA256)
		if err != nil || !c.appendExact(ctx, home, plan) {
			out.RecoveryRequired = true
			return out, reanalysisFailure("JOB_REANALYSIS_DIAGNOSTIC_FAILED")
		}
	}
	written, writeErr := c.ports.write(ctx, home, id, revision, reanalysisPreparation(prepared))
	if written == nil {
		out.CommitUnknown, out.RecoveryRequired = true, true
		return out, reanalysisFailure("JOB_REANALYSIS_WRITE_UNCERTAIN")
	}
	out.Committed, out.CleanupFailed = written.Committed, written.CleanupFailed
	out.CommitUnknown = written.CommitAttempted && !written.Committed
	if out.CommitUnknown {
		out.RecoveryRequired = true
	}
	var terminal jobdiagnostics.Event
	if noChange {
		if written.CommitAttempted || written.Committed {
			out.CommitUnknown, out.RecoveryRequired = !written.Committed, true
			return out, reanalysisFailure("JOB_REANALYSIS_RECOVERY_REQUIRED")
		}
		if writeErr == nil {
			terminal, eventErr = run.Unchanged()
		} else {
			terminal, eventErr = run.Failed(reanalysisWriteCode(writeErr), false, false)
		}
	} else if writeErr != nil {
		code := reanalysisWriteCode(writeErr)
		if written.Committed {
			code = "JOB_REANALYSIS_COMMITTED_UNVERIFIED"
		} else if written.CommitAttempted {
			code = "JOB_REANALYSIS_WRITE_UNCERTAIN"
		}
		terminal, eventErr = run.Failed(code, written.CommitAttempted, written.Committed)
	} else if written.CommitAttempted {
		if written.Committed {
			terminal, eventErr = run.Completed()
		} else {
			terminal, eventErr = run.Failed("JOB_REANALYSIS_WRITE_UNCERTAIN", true, false)
		}
	} else {
		terminal, eventErr = run.Failed("JOB_REANALYSIS_RESULT_INVALID", false, false)
	}
	if eventErr != nil || !c.appendExact(ctx, home, terminal) {
		out.RecoveryRequired = true
		out.Partial = out.Committed
		return out, reanalysisFailure("JOB_REANALYSIS_DIAGNOSTIC_FAILED")
	}
	if writeErr != nil || !noChange && !written.Committed {
		code := reanalysisWriteCode(writeErr)
		if written.Committed {
			code = "JOB_REANALYSIS_COMMITTED_UNVERIFIED"
		} else if written.CommitAttempted {
			code = "JOB_REANALYSIS_WRITE_UNCERTAIN"
		}
		out.Partial = out.Committed
		return out, reanalysisFailure(code)
	}
	saved, readErr := c.ports.saved(ctx, id)
	expected := prepared.PlannedCardSHA256
	if noChange {
		expected = prepared.CardFileSHA256
	}
	if readErr != nil || saved == nil || saved.Card == nil || saved.Card["card_revision"] != "sha256:"+hex.EncodeToString(expected[:]) {
		out.Partial = out.Committed
		if out.Committed {
			return out, reanalysisFailure("JOB_REANALYSIS_COMMITTED_UNVERIFIED")
		}
		return out, reanalysisFailure("JOB_REANALYSIS_RESULT_INVALID")
	}
	result, err := json.Marshal(map[string]any{"job": saved.Card, "run_id": run.ID(), "unchanged": noChange})
	if err != nil {
		out.Partial = out.Committed
		return out, reanalysisFailure("JOB_REANALYSIS_RESULT_INVALID")
	}
	out.Result = result
	return out, nil
}

func (c *reanalysisCoordinator) failRun(ctx context.Context, home datahome.Info, run *jobdiagnostics.Run, code string, attempted, committed bool) bool {
	event, err := run.Failed(code, attempted, committed)
	return err == nil && c.appendExact(ctx, home, event)
}
