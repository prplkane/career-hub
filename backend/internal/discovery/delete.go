package discovery

import (
	"context"
	"database/sql"
	"strings"

	"careerhub.local/backend/internal/textcompat"
)

type DeleteRequest struct {
	Operation                     string // delete or flush; flush selects only dismissed rows
	DiscoveryID, ExpectedRevision any
}
type DeleteResult struct {
	EditResult
	Sources []SourceChange `json:"-"`
}
type deletionTarget struct{ id, path string }

// DeleteInTransaction plans file removals but NEVER unlinks a file or commits.
// The owning service must journal/recheck/publish them with the SQL transaction.
// Unsafe/aliased paths refuse the entire operation; explicit deletion does not
// authorize deleting another discovery's retained file.
func DeleteInTransaction(ctx context.Context, tx *sql.Tx, files CaptureFiles, input DeleteRequest) (*DeleteResult, *WriteError) {
	return DeleteObservedInTransaction(ctx, tx, files, input, nil)
}
func DeleteObservedInTransaction(ctx context.Context, tx *sql.Tx, files CaptureFiles, input DeleteRequest, observe MutationObserver) (*DeleteResult, *WriteError) {
	if input.Operation != "delete" && input.Operation != "flush" {
		return nil, writeFailure("request", "OPERATION_UNSUPPORTED")
	}
	var id string
	var revision int64
	var e *WriteError
	if input.Operation == "delete" {
		id, e = writeText(input.DiscoveryID, "discovery_id", 300)
		if e != nil {
			return nil, e
		}
		id, e = writeID(id, "discovery_id")
		if e != nil {
			return nil, e
		}
		if input.ExpectedRevision != nil {
			revision, e = revisionInput(input.ExpectedRevision, false)
			if e != nil {
				return nil, e
			}
		}
	} else if input.DiscoveryID != nil || input.ExpectedRevision != nil {
		return nil, writeFailure("request", "DELETE_FIELDS_UNSUPPORTED")
	}
	if ctx == nil || tx == nil {
		return nil, writeFailure("transaction", "DATABASE_CONNECTION_INVALID")
	}
	if ctx.Err() != nil {
		return nil, writeFailure("context", "WRITE_CANCELED")
	}
	r := &reader{query: tx, ctx: ctx, remaining: MaxReadBytes}
	targets := []deletionTarget{}
	var response any
	if input.Operation == "delete" {
		current, e := writeRecord(r, id)
		if e != nil {
			return nil, e
		}
		if revision != 0 && revision != current.Revision {
			return nil, revisionConflict()
		}
		if current.State == "promoted" {
			return nil, invalidWrite("Promoted opportunities cannot be deleted from the Job Feed. Manage or delete the saved job in Applications instead.")
		}
		targets = append(targets, deletionTarget{id, current.SourceRelativePath})
		response = map[string]any{"deleted": true, "discovery_id": id, "state": current.State}
	} else {
		err := r.rows("flush", `SELECT discovery_id,source_relative_path FROM job_discoveries WHERE state='dismissed'`, nil, 2, MaxObservations, func(raw []any) error {
			id, err := textCell(raw[0], false)
			if err != nil {
				return err
			}
			path, err := textCell(raw[1], true)
			if err != nil {
				return err
			}
			targets = append(targets, deletionTarget{id, path})
			return nil
		})
		if err != nil {
			return nil, fromRead(err)
		}
		ids := make([]string, len(targets))
		for i, target := range targets {
			ids[i] = target.id
		}
		response = map[string]any{"deleted": true, "count": len(ids), "deleted_ids": ids}
	}
	changes, e := deletionSources(r, files, targets)
	if e != nil {
		return nil, e
	}
	if len(targets) != 0 {
		if observe != nil {
			ids := make([]string, len(targets))
			for i, target := range targets {
				ids[i] = target.id
			}
			if e := observe(ctx, tx, ids); e != nil {
				return nil, e
			}
		}
		query, args := "DELETE FROM job_discoveries WHERE discovery_id=?", []any{id}
		if input.Operation == "flush" {
			query, args = "DELETE FROM job_discoveries WHERE state='dismissed'", nil
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return nil, writeFailure("delete", "DATABASE_WRITE_FAILED")
		}
		count, err := result.RowsAffected()
		if err != nil || count != int64(len(targets)) {
			return nil, writeFailure("delete", "DELETE_RESULT_CHANGED")
		}
	}
	result, e := serializedEdit(response, len(targets) != 0)
	if e != nil {
		return nil, e
	}
	return &DeleteResult{EditResult: *result, Sources: changes}, nil
}

func deletionSources(r *reader, files CaptureFiles, targets []deletionTarget) ([]SourceChange, *WriteError) {
	changes := []SourceChange{}
	paths := map[string]string{}
	remaining := MaxReadBytes
	for _, target := range targets {
		if _, e := writeID(target.id, "discovery_id"); e != nil {
			return nil, e
		}
		if target.path == "" {
			continue
		}
		canonical, e := capturePath(target.id)
		if e != nil {
			return nil, e
		}
		if target.path != canonical {
			return nil, writeFailure("source", "SOURCE_PATH_REJECTED")
		}
		folded := textcompat.Fold(canonical)
		if _, ok := paths[folded]; ok {
			return nil, writeFailure("source", "SOURCE_REFERENCE_CONFLICT")
		}
		paths[folded] = target.id
		if files == nil {
			return nil, writeFailure("source", "SOURCE_UNREADABLE")
		}
		raw, present, err := files.ReadFile(canonical, 4<<20)
		if err != nil {
			return nil, writeFailure("source", "SOURCE_PATH_REJECTED")
		}
		if !present {
			continue
		}
		remaining -= len(raw)
		if len(raw) > 4<<20 || remaining < 0 {
			return nil, writeFailure("source", "RESULT_LIMIT")
		}
		// Deletion may remove corrupt bytes, but the exact preimage must be
		// recoverable. Do not decode, repair or truncate the retained file here.
		changes = append(changes, SourceChange{RelativePath: canonical, BeforePresent: true, Before: append([]byte(nil), raw...)})
	}
	if len(paths) != 0 {
		e := r.rows("source_references", `SELECT discovery_id,source_relative_path FROM job_discoveries WHERE source_relative_path IS NOT NULL AND source_relative_path<>''`, nil, 2, MaxObservations, func(raw []any) error {
			id, e := textCell(raw[0], false)
			if e != nil {
				return e
			}
			path, e := textCell(raw[1], false)
			if e != nil {
				return e
			}
			owner, ok := paths[textcompat.Fold(strings.ReplaceAll(path, "\\", "/"))]
			if ok && owner != id {
				return failure("source", "SOURCE_REFERENCE_CONFLICT")
			}
			return nil
		})
		if e != nil {
			return nil, fromRead(e)
		}
	}
	return changes, nil
}
