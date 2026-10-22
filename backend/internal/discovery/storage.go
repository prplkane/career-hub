package discovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"unicode/utf8"
)

const (
	SchemaVersion   = 27
	MaxCellBytes    = 2 << 20
	MaxReadBytes    = 8 << 20
	MaxObservations = 10000
)

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
type reader struct {
	query     queryer
	ctx       context.Context
	remaining int
}

var errLimit = errors.New("discovery read limit")

// A closed internal query may stop after enough ordered matches, without
// scanning an unbounded tail. Defers still close/check the native rows handle.
var errStopRows = errors.New("discovery ordered query complete")

// One current-schema snapshot per operation. Statement cancellation remains
// active, but rollback is synchronous before the caller releases native guards.
// There is deliberately no initialize_database fallback on a read path.
func snapshot(ctx context.Context, db *sql.DB, load func(*reader) *Error) (problem *Error) {
	if ctx == nil || db == nil {
		return failure("open", "DATABASE_CONNECTION_INVALID")
	}
	if ctx.Err() != nil {
		return failure("read", "READ_CANCELED")
	}
	defer func() {
		if ctx.Err() != nil {
			problem = failure("read", "READ_CANCELED")
		}
	}()
	conn, err := db.Conn(ctx)
	if err != nil {
		return failure("open", "DATABASE_OPEN_FAILED")
	}
	defer func() {
		if e := conn.Close(); e != nil && !errors.Is(e, sql.ErrConnDone) {
			problem = failure("cleanup", "DATABASE_CLEANUP_FAILED")
		}
	}()
	tx, err := conn.BeginTx(context.WithoutCancel(ctx), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return failure("open", "DATABASE_TRANSACTION_FAILED")
	}
	defer func() {
		if e := tx.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
			problem = failure("cleanup", "DATABASE_CLEANUP_FAILED")
		}
	}()
	var version int64
	if tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version) != nil {
		return failure("schema", "DATABASE_QUERY_FAILED")
	}
	if version < SchemaVersion {
		return failure("schema", "SCHEMA_PREPARATION_REQUIRED")
	}
	if version > SchemaVersion {
		return failure("schema", "SCHEMA_TOO_NEW")
	}
	return load(&reader{tx, ctx, MaxReadBytes})
}

// Charge every fetched cell, even metadata not selected by the projection.
// This bounds accepted values, not allocations internal to the SQLite driver.
func (r *reader) rows(stage, query string, args []any, columns, maximum int, consume func([]any) error) (problem *Error) {
	rows, err := r.query.QueryContext(r.ctx, query, args...)
	if err != nil {
		return failure(stage, "DATABASE_QUERY_FAILED")
	}
	defer func() {
		if rows.Close() != nil {
			problem = failure("cleanup", "DATABASE_CLEANUP_FAILED")
		}
	}()
	count := 0
	for rows.Next() {
		count++
		if count > maximum {
			return failure(stage, "RESULT_LIMIT")
		}
		values, targets := make([]any, columns), make([]any, columns)
		for i := range values {
			targets[i] = &values[i]
		}
		if rows.Scan(targets...) != nil {
			return failure(stage, "DATABASE_SCAN_FAILED")
		}
		for _, v := range values {
			size := 8
			switch x := v.(type) {
			case string:
				if !utf8.ValidString(x) {
					return failure(stage, "DATABASE_VALUE_UNSUPPORTED")
				}
				size = len(x)
			case nil, int64:
			case float64:
				if math.IsInf(x, 0) || math.IsNaN(x) {
					return failure(stage, "DATABASE_VALUE_UNSUPPORTED")
				}
			default:
				return failure(stage, "DATABASE_VALUE_UNSUPPORTED")
			}
			r.remaining -= size
			if size > MaxCellBytes || r.remaining < 0 {
				return failure(stage, "RESULT_LIMIT")
			}
		}
		if err := consume(values); err != nil {
			if errors.Is(err, errStopRows) {
				return nil
			}
			if errors.Is(err, errLimit) {
				return failure(stage, "RESULT_LIMIT")
			}
			var typed *Error
			if errors.As(err, &typed) {
				return typed
			}
			return failure(stage, "DATABASE_VALUE_UNSUPPORTED")
		}
	}
	if rows.Err() != nil {
		return failure(stage, "DATABASE_QUERY_FAILED")
	}
	return nil
}

func checkResult(value any) *Error {
	raw, err := json.Marshal(value)
	if err != nil {
		return failure("result", "DATABASE_VALUE_UNSUPPORTED")
	}
	if len(raw) > MaxReadBytes {
		return failure("result", "RESULT_LIMIT")
	}
	return nil
}

// The supported database shape is the writer's TEXT/INTEGER/null schema, not
// arbitrary Python str(BLOB), str(None), or int(fraction) coercions after tampering.
func textCell(raw any, nullable bool) (string, error) {
	if raw == nil && nullable {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", ErrUnsupported
	}
	return value, nil
}
func integerCell(raw any) (int64, error) {
	value, ok := raw.(int64)
	if !ok {
		return 0, ErrUnsupported
	}
	return value, nil
}
