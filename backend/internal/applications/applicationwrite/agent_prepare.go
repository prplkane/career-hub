package applicationwrite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/jobs/jobcard"
)

// PreparationWriteReceipt retains the source adapter's workflow meaning of
// changed: registration/status, not a promise that no metadata SQL changed.
// Actual commit/cleanup state remains in Outcome, not this projected receipt.
type PreparationWriteReceipt struct {
	Result          string  `json:"result"`
	Changed         bool    `json:"changed"`
	JobCreated      bool    `json:"job_created"`
	JobReused       bool    `json:"job_reused"`
	StatusBefore    *string `json:"status_before"`
	StatusAfter     string  `json:"status_after"`
	AppliedRecorded bool    `json:"applied_recorded"`
}

// SavedPreparationReceipt is an internal post-write composition fact, not an
// agent context packet or permission to use a different job/card. The host must
// require a successful Outcome before reading context and preserve stage facts
// if that later read fails. These fields never authorize a client-supplied write.
type SavedPreparationReceipt struct {
	JobID             string                  `json:"job_id"`
	CardSHA256        string                  `json:"card_sha256"`
	LedgerFingerprint string                  `json:"ledger_fingerprint"`
	ApplicationSHA256 string                  `json:"application_sha256"`
	WriteReceipt      PreparationWriteReceipt `json:"write_receipt"`
}

// PrepareSavedJob registers the exact saved card and starts preparation in one
// existing owner transaction. It never publishes a JD, assessment or material.
// In contrast with Python's two commits, a failed start rolls back registration.
func (s *Service) PrepareSavedJob(ctx context.Context, id string) (*Outcome, *Error) {
	return s.prepareSavedJob(ctx, id, nil)
}

// PreparationSourceBinding narrows a saved write to the card/raw bytes chosen
// by the completed intake and the status left by that phase. It does not grant
// Home access, bypass admission, or authorize new-file publication.
type PreparationSourceBinding struct {
	CardSHA256, RawSHA256 [32]byte
	Status                string
}

func (s *Service) PrepareSavedJobAfterIntake(ctx context.Context, id string, binding PreparationSourceBinding) (*Outcome, *Error) {
	if binding.CardSHA256 == ([32]byte{}) || binding.RawSHA256 == ([32]byte{}) || binding.Status == "" {
		return emptyFailure(failed("input", "AGENT_PREPARE_SOURCE_BINDING_REQUIRED"))
	}
	return s.prepareSavedJob(ctx, id, &binding)
}

func (s *Service) prepareSavedJob(ctx context.Context, id string, binding *PreparationSourceBinding) (*Outcome, *Error) {
	if !jobcard.ValidID(id) {
		return emptyFailure(failed("input", "AGENT_PREPARE_JOB_INVALID"))
	}
	return s.run(ctx, id, func(reads *sourceReads) (mutation, *Error) {
		loaded, e := jobcard.Read(ctx, reads, "jobs/normalized/"+id+".yaml")
		if e != nil {
			return nil, failed("source", e.Code)
		}
		if !loaded.Report.SchemaValid() || jobDetailsField(loaded, "id") != id {
			return nil, failed("source", "AGENT_PREPARE_JOB_INVALID")
		}
		if binding != nil && (!loaded.RawPresent || loaded.CardFileSHA256 != binding.CardSHA256 || loaded.RawFileSHA256 != binding.RawSHA256) {
			return nil, failed("source", "AGENT_PREPARE_SOURCE_CHANGED")
		}
		job := map[string]any{"id": id}
		for _, key := range []string{"company", "title", "source_url", "content_fingerprint", "raw_jd_path"} {
			for _, entry := range loaded.Card.Entries {
				if entry.Key.Kind == "string" && entry.Key.Text == key {
					if entry.Value.Kind != "string" {
						return nil, failed("source", "AGENT_PREPARE_JOB_INVALID")
					}
					job[key] = entry.Value.Text
				}
			}
		}
		snapshot, p := snapshots([]map[string]any{job}, reads)
		if p != nil {
			return nil, p
		}
		now := s.now()
		return func(ctx context.Context, tx *sql.Tx) (*applications.MutationResult, *Error) {
			before, p := applications.SummariesInTransaction(ctx, tx, []string{id})
			if p != nil {
				return nil, applicationProblem(p, id)
			}
			var old *string
			if summary, exists := before[id]; exists {
				raw, e := json.Marshal(summary.CurrentStatus)
				if e != nil || json.Unmarshal(raw, &old) != nil || old == nil {
					return nil, failed("read", "AGENT_PREPARE_STATUS_INVALID")
				}
			}
			if binding != nil && (old == nil || *old != binding.Status) {
				return nil, failed("source", "AGENT_PREPARE_SOURCE_CHANGED")
			}
			registered, p := applications.RegisterInTransaction(ctx, tx, snapshot, now)
			if p != nil {
				return nil, applicationProblem(p, id)
			}
			started, p := applications.StartInTransaction(ctx, tx, id, now, now)
			if p != nil {
				return nil, applicationProblem(p, id)
			}
			after, p := applications.SessionJobInTransaction(ctx, tx, id)
			if p != nil {
				return nil, applicationProblem(p, id)
			}
			summaries, p := applications.SummariesInTransaction(ctx, tx, []string{id})
			if p != nil {
				return nil, applicationProblem(p, id)
			}
			summary, present := summaries[id]
			if !present {
				return nil, failed("result", "AGENT_PREPARE_RESULT_INVALID")
			}
			summaryJSON, marshalErr := json.Marshal(summary)
			if marshalErr != nil {
				return nil, failed("result", "AGENT_PREPARE_RESULT_INVALID")
			}
			summaryHash := sha256.Sum256(summaryJSON)
			changed := old == nil || *old != after.Status
			result := "no_change_needed"
			if changed {
				result = "prepared"
			}
			receipt := SavedPreparationReceipt{JobID: id, CardSHA256: hex.EncodeToString(loaded.CardFileSHA256[:]), LedgerFingerprint: snapshot[0].ContentFingerprint, ApplicationSHA256: hex.EncodeToString(summaryHash[:]), WriteReceipt: PreparationWriteReceipt{Result: result, Changed: changed, JobReused: true, StatusBefore: old, StatusAfter: after.Status}}
			raw, e := json.Marshal(receipt)
			if e != nil {
				return nil, failed("result", "AGENT_PREPARE_RESULT_INVALID")
			}
			return &applications.MutationResult{Result: raw, Changed: registered.Changed || started.Changed}, nil
		}, nil
	})
}
