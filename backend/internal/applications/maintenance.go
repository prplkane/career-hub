package applications

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"slices"
)

// IntegrityReport compares source identities, not candidate suitability. It
// never infers that an orphan can be deleted or that an absent JD was withdrawn.
type IntegrityReport struct {
	NormalizedJobCount         int        `json:"normalized_job_count"`
	LedgerJobCount             int        `json:"ledger_job_count"`
	InvalidCardIDs             []string   `json:"invalid_card_ids"`
	LedgerWithoutNormalized    []string   `json:"ledger_without_normalized"`
	FingerprintMismatches      []string   `json:"fingerprint_mismatches"`
	DuplicateFingerprintGroups [][]string `json:"duplicate_fingerprint_groups"`
}

func integrity(q *queryState, cards map[string]string, invalid []string) (*IntegrityReport, *Error) {
	r := &IntegrityReport{NormalizedJobCount: len(cards), InvalidCardIDs: append([]string{}, invalid...),
		LedgerWithoutNormalized: []string{}, FingerprintMismatches: []string{}, DuplicateFingerprintGroups: [][]string{}}
	groups := map[string][]string{}
	if q != nil {
		e := q.rows("integrity", "SELECT job_id, content_fingerprint FROM job_applications ORDER BY job_id", nil, 2, func(row []any) error {
			if r.LedgerJobCount >= MaxRecords {
				return errBudget
			}
			id, idOK := row[0].(string)
			fp, fpOK := row[1].(string)
			if !idOK || !fpOK {
				return errors.New("unsupported identity")
			}
			r.LedgerJobCount++
			groups[fp] = append(groups[fp], id)
			if want, exists := cards[id]; !exists {
				r.LedgerWithoutNormalized = append(r.LedgerWithoutNormalized, id)
			} else if want != fp {
				r.FingerprintMismatches = append(r.FingerprintMismatches, id)
			}
			return nil
		})
		if e != nil {
			return nil, e
		}
	}
	keys := make([]string, 0, len(groups))
	for fp := range groups {
		keys = append(keys, fp)
	}
	slices.Sort(keys)
	for _, fp := range keys {
		if len(groups[fp]) > 1 {
			r.DuplicateFingerprintGroups = append(r.DuplicateFingerprintGroups, groups[fp])
		}
	}
	if _, e := mutationResult(r, false); e != nil {
		return nil, e
	}
	return r, nil
}

// ReadIntegrity uses a coherent read-only transaction, and permits a missing
// database without creating it. The owning adapter must hold path/admission
// guards and revalidate its complete normalized-source inventory.
func ReadIntegrity(ctx context.Context, db *sql.DB, cards map[string]string, invalid []string) (r *IntegrityReport, problem *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("read", "READ_CANCELED", nil)
	}
	if db == nil {
		return integrity(nil, cards, invalid)
	}
	problem = readCurrent(ctx, db, func(q *queryState) *Error {
		r, problem = integrity(q, cards, invalid)
		return problem
	})
	if problem != nil {
		r = nil
	}
	return
}

// RepairExactOrphanInTransaction only removes one fully redundant state. The
// caller must validate the absent orphan and current survivor against retained
// normalized sources first, and must roll back on every error. No auto-merge.
func RepairExactOrphanInTransaction(ctx context.Context, tx *sql.Tx, orphanID, survivorID, fingerprint string) (*MutationResult, *Error) {
	q, e := mutationQuery(ctx, tx)
	if e != nil {
		return nil, e
	}
	orphan, e := mutationExisting(q, orphanID)
	if e != nil {
		return nil, e
	}
	survivor, e := mutationExisting(q, survivorID)
	if e != nil {
		return nil, e
	}
	// IDs and creation/update metadata intentionally differ; all user state must
	// be identical. Reject unsupported cells even when both BLOBs are equal.
	if _, e := q.cells(orphan); e != nil {
		return nil, failure("repair", "DATABASE_VALUE_UNSUPPORTED", nil)
	}
	if _, e := q.cells(survivor); e != nil {
		return nil, failure("repair", "DATABASE_VALUE_UNSUPPORTED", nil)
	}
	for _, i := range []int{1, 2, 3, 4, 5, 6, 7, 10, 11} {
		if !reflect.DeepEqual(orphan[i], survivor[i]) {
			return nil, failure("repair", "REPAIR_STATE_DIFFERS", nil)
		}
	}
	if fingerprint == "" || orphan[4] != fingerprint {
		return nil, failure("repair", "REPAIR_SOURCE_MISMATCH", nil)
	}
	history := func(id string) ([][]any, *Error) {
		rows := [][]any{}
		e := q.rows("repair", "SELECT status, occurred_at, recorded_at, note FROM application_status_events WHERE job_id = ? ORDER BY event_id", []any{id}, 4, func(row []any) error {
			if len(rows) >= MaxHistoryRows {
				return errBudget
			}
			if _, err := q.cells(row); err != nil {
				return err
			}
			rows = append(rows, row)
			return nil
		})
		return rows, e
	}
	a, e := history(orphanID)
	if e != nil {
		return nil, e
	}
	b, e := history(survivorID)
	if e != nil {
		return nil, e
	}
	if !reflect.DeepEqual(a, b) {
		return nil, failure("repair", "REPAIR_HISTORY_DIFFERS", nil)
	}
	// Fixed schema-28 references, including non-FK provenance. These are not
	// permissions to discard SET NULL/CASCADE relationships. Future schemas must
	// update this closed query; a schema >28 is rejected by the owning service.
	var referenced int
	eSQL := tx.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM application_resume_attachments WHERE job_id = ?1) OR
		EXISTS(SELECT 1 FROM application_applied_resume_events WHERE job_id = ?1) OR
		EXISTS(SELECT 1 FROM application_resume_plans WHERE job_id = ?1) OR
		EXISTS(SELECT 1 FROM job_assessments WHERE job_id = ?1) OR
		EXISTS(SELECT 1 FROM job_assessment_resolution_events WHERE job_id = ?1) OR
		EXISTS(SELECT 1 FROM application_sessions WHERE job_id = ?1) OR
		EXISTS(SELECT 1 FROM application_instruction_manual_items WHERE job_id = ?1) OR
		EXISTS(SELECT 1 FROM application_instruction_action_events WHERE job_id = ?1) OR
		EXISTS(SELECT 1 FROM application_contact_job_links WHERE job_id = ?1) OR
		EXISTS(SELECT 1 FROM application_contact_provenance WHERE job_id = ?1) OR
		EXISTS(SELECT 1 FROM job_discoveries WHERE promoted_job_id = ?1) OR
		EXISTS(SELECT 1 FROM job_email_application_outcomes WHERE matched_job_id = ?1) OR
		EXISTS(SELECT 1 FROM reusable_answer_use_events WHERE job_id = ?1)`, orphanID).Scan(&referenced)
	if eSQL != nil {
		return nil, failure("repair", "DATABASE_QUERY_FAILED", eSQL)
	}
	if referenced != 0 {
		return nil, failure("repair", "REPAIR_DURABLE_DATA", nil)
	}
	deleted, sqlError := tx.ExecContext(ctx, "DELETE FROM job_applications WHERE job_id = ?", orphanID)
	if sqlError != nil {
		return nil, failure("repair", "DATABASE_WRITE_FAILED", sqlError)
	}
	count, sqlError := deleted.RowsAffected()
	if sqlError != nil || count != 1 {
		return nil, failure("repair", "REPAIR_NOT_APPLIED", sqlError)
	}
	var remains int
	if e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM job_applications WHERE job_id = ?)", orphanID).Scan(&remains); e != nil {
		return nil, failure("repair", "DATABASE_QUERY_FAILED", e)
	}
	if remains != 0 {
		return nil, failure("repair", "REPAIR_NOT_APPLIED", nil)
	}
	return mutationResult(map[string]string{"removed_job_id": orphanID, "surviving_job_id": survivorID}, true)
}
