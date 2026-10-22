package main

import (
	"context"
	"encoding/hex"
	"encoding/json"

	"careerhub.local/backend/internal/catalogwrite"
	"careerhub.local/backend/internal/sidecar/httpapi"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

// Keep facts from the two owners separate. In particular, a committed registry
// does not prove that a later uncertain JD write committed or rolled back.
// The authenticated application-mode route retains these separate stage facts.
type exactVocabularyCommandOutcome struct {
	Receipt         json.RawMessage
	Registry        *httpapi.ApplicationOutcome
	Reanalysis      *httpapi.ApplicationOutcome
	ReanalysisError *httpapi.ApplicationError
}

type exactVocabularyCoordinator struct {
	add       func(context.Context, catalogwrite.ExactVocabularyRequest) (*catalogwrite.ExactVocabularyOutcome, *catalogwrite.Error)
	reanalyze func(context.Context, string, string, *vocabularyReanalysisWitness) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError)
}

// The host must pass the same reanalysis coordinator used by the ordinary job
// actions so whole diagnostic runs cannot interleave across these entry points.
func newExactVocabularyCoordinator(w *readonly.Workspace, start protocol.StartRecord, reanalysis *reanalysisCoordinator) *exactVocabularyCoordinator {
	owner := catalogwrite.New(start.ResourceRoot, start.DataRoot)
	return &exactVocabularyCoordinator{
		add: func(ctx context.Context, request catalogwrite.ExactVocabularyRequest) (*catalogwrite.ExactVocabularyOutcome, *catalogwrite.Error) {
			if w.CheckAdmission() != nil {
				return &catalogwrite.ExactVocabularyOutcome{}, &catalogwrite.Error{Code: "AGENT_VOCABULARY_HOME_UNAVAILABLE"}
			}
			out, problem := owner.AddExactJobSkill(ctx, request)
			if w.CheckAdmission() != nil {
				committed := problem != nil && problem.Committed
				if out != nil {
					out.Result = nil
					if out.Publication != nil {
						committed = committed || out.Publication.Write.FilesComplete || len(out.Publication.Write.AppliedFiles) > 0
					}
				}
				return out, &catalogwrite.Error{Code: "AGENT_VOCABULARY_HOME_UNAVAILABLE", Committed: committed}
			}
			return out, problem
		},
		reanalyze: reanalysis.reanalyze,
	}
}

func (c *exactVocabularyCoordinator) Run(ctx context.Context, request catalogwrite.ExactVocabularyRequest) (*exactVocabularyCommandOutcome, *httpapi.ApplicationError) {
	out := &exactVocabularyCommandOutcome{Registry: &httpapi.ApplicationOutcome{}}
	fail := func(code string) (*exactVocabularyCommandOutcome, *httpapi.ApplicationError) {
		return out, &httpapi.ApplicationError{Code: code}
	}
	if c == nil || c.add == nil || c.reanalyze == nil || ctx == nil {
		return fail("AGENT_VOCABULARY_UNAVAILABLE")
	}
	if ctx.Err() != nil {
		return fail("AGENT_VOCABULARY_CANCELED")
	}
	value, problem := c.add(ctx, request)
	if value == nil {
		out.Registry.CommitUnknown, out.Registry.RecoveryRequired = true, true
	} else if value.Publication != nil {
		p := value.Publication
		applyWriteFacts(out.Registry, p.Write)
		out.Registry.Committed = out.Registry.Committed || p.Write.FilesComplete
		out.Registry.CleanupFailed = p.CleanupFailed
		out.Registry.RecoveryRequired = p.Write.IntentRecorded && !p.Write.JournalRemoved
	}
	if problem != nil {
		out.Registry.Committed = out.Registry.Committed || problem.Committed
		// Never serialize private catalog validation details or source text.
		return fail(exactVocabularyErrorCode(problem.Code))
	}
	if value == nil || value.Publication == nil || value.Result == nil || out.Registry.CleanupFailed || out.Registry.RecoveryRequired {
		return fail("AGENT_VOCABULARY_RESULT_INVALID")
	}
	r := value.Result
	if r.JobID != request.JobID || r.Record == nil || (r.Operation != "created" && r.Operation != "no_change_needed") {
		return fail("AGENT_VOCABULARY_RESULT_INVALID")
	}
	receipt := map[string]any{"schema_version": 1, "contract_version": 4, "result": r.Operation,
		"job_id": r.JobID, "source_ref": r.SourceRef, "record": r.Record, "candidate_evidence_changed": false}
	if r.Operation == "created" {
		if !value.Publication.Write.Completed || !out.Registry.Committed {
			return fail("AGENT_VOCABULARY_RESULT_INVALID")
		}
		receipt["registry_revision"], receipt["job_reanalyzed"] = r.RegistryRevision, false
		receipt["reanalysis_warning"] = nil
		warning := ""
		switch {
		case !r.JobDraft:
			warning = "skill was added, but only draft job cards can be reanalyzed automatically"
		case ctx.Err() != nil:
			warning = "skill was added but JD reanalysis was canceled"
		case r.CardFileSHA256 == ([32]byte{}) || r.RawFileSHA256 == ([32]byte{}) || r.CatalogRevision == "":
			warning = "skill was added but JD reanalysis source could not be verified"
		default:
			// The registry owner has returned; no global gate is nested here.
			out.Reanalysis, out.ReanalysisError = c.reanalyze(ctx, r.JobID, "sha256:"+hex.EncodeToString(r.CardFileSHA256[:]),
				&vocabularyReanalysisWitness{r.RawFileSHA256, r.CatalogRevision})
			a := out.Reanalysis
			if a == nil {
				out.Reanalysis = &httpapi.ApplicationOutcome{CommitUnknown: true, RecoveryRequired: true}
			}
			if out.ReanalysisError != nil || a == nil || len(a.Result) == 0 || a.Partial || a.CleanupFailed || a.CommitUnknown || a.RecoveryRequired {
				warning = "skill was added but JD reanalysis was not confirmed; inspect its diagnostic before retrying"
			} else {
				receipt["job_reanalyzed"] = true
			}
		}
		if warning != "" {
			receipt["reanalysis_warning"] = warning
		}
	} else if value.Publication.Write.IntentRecorded || out.Registry.Committed {
		return fail("AGENT_VOCABULARY_RESULT_INVALID")
	}
	raw, e := json.Marshal(receipt)
	if e != nil {
		return fail("AGENT_VOCABULARY_RESULT_INVALID")
	}
	out.Receipt = raw
	return out, nil
}

func exactVocabularyErrorCode(code string) string {
	switch code {
	case "AGENT_VOCABULARY_CONFIRMATION_REQUIRED", "AGENT_VOCABULARY_REQUEST_INVALID", "AGENT_VOCABULARY_NAME_INVALID",
		"AGENT_VOCABULARY_SOURCE_INVALID", "AGENT_VOCABULARY_SOURCE_LIMIT", "AGENT_VOCABULARY_LINE_INVALID", "AGENT_VOCABULARY_NOT_FOUND",
		"AGENT_VOCABULARY_JOB_INVALID", "AGENT_VOCABULARY_GROUP_UNAVAILABLE", "AGENT_VOCABULARY_VOCABULARY_COLLISION", "AGENT_VOCABULARY_HOME_UNAVAILABLE", "AGENT_VOCABULARY_CANCELED":
		return code
	case "CATALOG_OPERATION_CANCELLED":
		return "AGENT_VOCABULARY_CANCELED"
	default:
		return "AGENT_VOCABULARY_REGISTRY_FAILED"
	}
}

func (c *exactVocabularyCoordinator) Application(ctx context.Context, request httpapi.ExactVocabularyRequest) (*httpapi.ApplicationOutcome, *httpapi.ApplicationError) {
	command, problem := c.Run(ctx, request)
	out := &httpapi.ApplicationOutcome{Result: command.Receipt, VocabularyStages: &httpapi.VocabularyStages{Registry: command.Registry, Reanalysis: command.Reanalysis}}
	for _, stage := range []*httpapi.ApplicationOutcome{command.Registry, command.Reanalysis} {
		if stage == nil {
			continue
		}
		out.Committed = out.Committed || stage.Committed
		out.Partial = out.Partial || stage.Partial
		out.CleanupFailed = out.CleanupFailed || stage.CleanupFailed
		out.CommitUnknown = out.CommitUnknown || stage.CommitUnknown
		out.RecoveryRequired = out.RecoveryRequired || stage.RecoveryRequired
		out.RevisionRefreshRequired = out.RevisionRefreshRequired || stage.RevisionRefreshRequired
		if stage.RunID != "" {
			out.RunID = stage.RunID
		}
	}
	// No root Write: two owners' file counts/completion cannot be conflated.
	return out, problem
}
