// Package schema contains fixed application schema upgrades and explicit owned
// native preparation with verified recovery. It is not connected to startup or
// request reads. The transaction engine remains private to its coordinator.
package schema

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"

	"careerhub.local/backend/internal/joburls"
)

const Version = 27

const (
	maxPostingRows  = 100_000
	maxPostingBytes = 32 << 20
)

// Errors are fixed diagnostics; neither SQL values nor driver text are public.
type Error struct {
	Step  int
	Stage string
	cause error
}

func (e *Error) Error() string { return "application schema upgrade failed at " + e.Stage }
func (e *Error) Unwrap() error { return e.cause }

type actionKind uint8

const (
	sqlAction actionKind = iota
	columnAction
	noRowsAction
	postingsAction
)

type action struct {
	kind               actionKind
	table, column, sql string
}

// applySteps requires an already-owned transaction and never commits it.
// Its caller must roll back on any error, including cancellation. No backup or
// filesystem claim is implied by this pure connection-level engine.
func applySteps(ctx context.Context, tx *sql.Tx, previous int) error {
	if ctx == nil || tx == nil || previous < 0 || previous > Version {
		return &Error{Stage: "precondition"}
	}
	for i := previous; i < Version; i++ {
		for _, op := range migrations[i] {
			if err := ctx.Err(); err != nil {
				return &Error{Step: i + 1, Stage: "canceled", cause: err}
			}
			stage, err := applyAction(ctx, tx, op)
			if err != nil {
				return &Error{Step: i + 1, Stage: stage, cause: err}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return &Error{Step: Version, Stage: "canceled", cause: err}
	}
	return nil
}

func applyAction(ctx context.Context, tx *sql.Tx, op action) (string, error) {
	switch op.kind {
	case columnAction:
		// The name is a fixed generated identifier, never caller/SQLite text.
		rows, err := tx.QueryContext(ctx, "PRAGMA table_info("+op.table+")")
		if err != nil {
			return "columns", err
		}
		found := false
		for rows.Next() {
			var id, required, primaryKey int
			var name, typ string
			var defaultValue any
			if err = rows.Scan(&id, &name, &typ, &required, &defaultValue, &primaryKey); err != nil {
				break
			}
			found = found || name == op.column
		}
		err = errors.Join(err, rows.Err(), rows.Close())
		if err != nil || found {
			return "columns", err
		}
		_, err = tx.ExecContext(ctx, op.sql)
		return "column-add", err
	case noRowsAction:
		var found int
		err := tx.QueryRowContext(ctx, op.sql).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return "resume-references", nil
		}
		if err == nil {
			err = errors.New("dangling resume reference")
		}
		return "resume-references", err
	case postingsAction:
		return "posting-identities", repairPostingIdentities(ctx, tx)
	case sqlAction:
		_, err := tx.ExecContext(ctx, op.sql)
		return "statement", err
	default:
		return "operation", errors.New("unsupported fixed schema operation")
	}
}

type postingCandidate struct{ id, url string }

func repairPostingIdentities(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT discovery_id, observed_url, canonical_url, posting_identity FROM job_discoveries`)
	if err != nil {
		return err
	}
	candidates := make(map[string][]postingCandidate)
	owners := make(map[string]map[string]bool)
	var order []string
	count, size := 0, 0
	for rows.Next() {
		var values [4]any
		if err = rows.Scan(&values[0], &values[1], &values[2], &values[3]); err != nil {
			break
		}
		var text [4]string
		for i, value := range values {
			var ok bool
			text[i], ok = value.(string)
			if !ok || !utf8.ValidString(text[i]) || len(text[i]) > joburls.MaxURLBytes {
				err = errors.New("unsupported posting identity storage")
				break
			}
			size += len(text[i])
		}
		count++
		if err != nil || count > maxPostingRows || size > maxPostingBytes {
			if err == nil {
				err = errors.New("posting backfill resource limit")
			}
			break
		}
		id, observed, canonical, identity := text[0], text[1], text[2], text[3]
		if identity != "" {
			if owners[identity] == nil {
				owners[identity] = make(map[string]bool)
			}
			owners[identity][id] = true
		}
		posting, resolveErr := joburls.Resolve(observed)
		if resolveErr != nil {
			err = resolveErr
			break
		}
		if posting.Identity == "" {
			posting, resolveErr = joburls.Resolve(canonical)
			if resolveErr != nil {
				err = resolveErr
				break
			}
		}
		if posting.Identity != "" && posting.URL != "" {
			if _, exists := candidates[posting.Identity]; !exists {
				order = append(order, posting.Identity)
			}
			candidates[posting.Identity] = append(candidates[posting.Identity], postingCandidate{id, posting.URL})
		}
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, identity := range order {
		items := candidates[identity]
		if len(items) != 1 {
			continue
		}
		candidate := items[0]
		otherOwner := false
		for owner := range owners[identity] {
			otherOwner = otherOwner || owner != candidate.id
		}
		if otherOwner {
			continue
		}
		if _, err = tx.ExecContext(ctx, `UPDATE job_discoveries SET posting_identity = ?, canonical_url = ? WHERE discovery_id = ?`, identity, candidate.url, candidate.id); err != nil {
			return err
		}
	}
	return nil
}
