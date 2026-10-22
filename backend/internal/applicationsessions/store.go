package applicationsessions

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/applications"
)

// Entropy is supplied by the owning service, never request fields. Production
// uses SecureEntropy; deterministic implementations are only synthetic tests.
type Entropy interface {
	ID() (string, error)
	Token() (string, error)
}
type SecureEntropy struct{}

func (SecureEntropy) ID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(b[:]), nil
}
func (SecureEntropy) Token() (string, error) {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

type engine struct {
	ctx       context.Context
	tx        *sql.Tx
	now       time.Time
	clock     string
	entropy   Entropy
	remaining int
	changed   bool
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

// ApplyInTransaction must be enclosed in an admitted current-schema IMMEDIATE
// write transaction. A returned error requires full rollback, even for a read
// which expired old sessions. No partial status changes or secret responses
// may escape an unsuccessful operation.
func ApplyInTransaction(ctx context.Context, tx *sql.Tx, r Request, now time.Time, entropy Entropy) (*applications.MutationResult, *Error) {
	if e := r.validate(); e != nil {
		return nil, e
	}
	if ctx == nil || tx == nil || entropy == nil {
		return nil, unsupported("CONFIGURATION_INVALID")
	}
	// Python datetime has microsecond resolution. Preserve that clock precision
	// before boundary comparisons, not merely when formatting stored seconds.
	now = now.UTC().Truncate(time.Microsecond)
	if now.Year() < 1 || now.Year() > 9999 {
		return nil, unsupported("CLOCK_RANGE")
	}
	e := &engine{ctx: ctx, tx: tx, now: now, clock: stamp(now), entropy: entropy, remaining: maxScanBytes}
	var value any
	var err *Error
	switch r.Operation {
	case "create", "latest":
		id, ok := scalar(r.Fields["job_id"], applications.MaxIDBytes)
		if !ok {
			return nil, unsupported("JOB_ID_UNSUPPORTED")
		}
		if r.Operation == "create" {
			value, err = e.create(id)
		} else {
			value, err = e.latest(id)
		}
	case "claim", "claim_create":
		value, err = e.claim(r.Fields, r.Operation == "claim_create")
	case "signal":
		value, err = e.signal(r.Fields)
	case "interrupt":
		value, err = e.interrupt(r.Fields)
	case "reviews":
		value, err = e.reviews()
	case "confirm", "dismiss", "cancel":
		value, err = e.resolve(r.Operation, r.Fields)
	}
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, unsupported("CANCELED")
	}
	raw, encodeErr := json.Marshal(value)
	if encodeErr != nil || len(raw) > maxScanBytes {
		return nil, unsupported("RESPONSE_UNSUPPORTED")
	}
	return &applications.MutationResult{Result: raw, Changed: e.changed}, nil
}

// Driver cells are checked without database/sql implicit coercion. Large TEXT
// and non-text storage never get materialized as strings; boundedTextSQL uses
// a tiny BLOB sentinel that this reader explicitly rejects.
func boundedTextSQL(column string) string {
	return "CASE WHEN typeof(" + column + ") IN ('text','null') AND coalesce(length(CAST(" + column + " AS BLOB)),0) <= " + strconv.Itoa(applications.MaxRecordCellBytes) + " THEN " + column + " ELSE X'00' END"
}
func (e *engine) query(query string, args []any, limit int) ([][]any, *Error) {
	rows, err := e.tx.QueryContext(e.ctx, query, args...)
	if err != nil {
		return nil, unsupported("DATABASE_QUERY_FAILED")
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, unsupported("DATABASE_QUERY_FAILED")
	}
	out := [][]any{}
	for rows.Next() {
		if len(out) >= limit {
			return nil, unsupported("ROW_LIMIT")
		}
		values := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range dest {
			dest[i] = &values[i]
		}
		if rows.Scan(dest...) != nil {
			return nil, unsupported("DATABASE_QUERY_FAILED")
		}
		for _, v := range values {
			switch value := v.(type) {
			case nil, int64:
				e.remaining -= 8
			case string:
				if !utf8.ValidString(value) {
					return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
				}
				e.remaining -= len(value)
			default:
				return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
			}
		}
		if e.remaining < 0 {
			return nil, unsupported("SCAN_LIMIT")
		}
		out = append(out, values)
	}
	if rows.Err() != nil || rows.Close() != nil {
		return nil, unsupported("DATABASE_QUERY_FAILED")
	}
	return out, nil
}
func (e *engine) exec(query string, args ...any) *Error {
	result, err := e.tx.ExecContext(e.ctx, query, args...)
	if err != nil {
		return unsupported("DATABASE_WRITE_FAILED")
	}
	n, err := result.RowsAffected()
	if err != nil || n < 0 || n > maxRows {
		return unsupported("ROW_LIMIT")
	}
	e.changed = e.changed || n > 0
	return nil
}

// Before any revision increment, bound the affected set and reject storage
// coercion/overflow. A failed operation is rolled back as one atomic unit.
func (e *engine) update(set, where string, values, keys []any) *Error {
	rows, p := e.query("SELECT "+boundedTextSQL("session_id")+",revision FROM application_sessions WHERE "+where+" LIMIT 10001", keys, maxRows)
	if p != nil {
		return p
	}
	for _, row := range rows {
		if _, ok := row[0].(string); !ok {
			return unsupported("DATABASE_VALUE_UNSUPPORTED")
		}
		rev, ok := row[1].(int64)
		if !ok || rev < 1 || rev == math.MaxInt64 {
			return unsupported("REVISION_UNSUPPORTED")
		}
	}
	args := append(append([]any{}, values...), keys...)
	return e.exec("UPDATE application_sessions SET "+set+",revision=revision+1 WHERE "+where, args...)
}
func (e *engine) expire() *Error {
	return e.update("state='expired',interruption_reason='timeout',session_token_sha256='',resolved_at=?,updated_at=?", "state IN ('pending_claim','armed') AND expires_at<=?", []any{e.clock, e.clock}, []any{e.clock})
}

const sessionColumns = "session_id job_id state expected_url expected_origin bound_tab_id bound_origin session_token_sha256 claim_request_id detected_event_key detection_rule interruption_reason created_at expires_at claimed_at detected_at resolved_at updated_at revision"

var sessionSelect = func() string {
	cols := []string{}
	for _, column := range strings.Fields(sessionColumns) {
		if column == "revision" || column == "bound_tab_id" {
			cols = append(cols, "s."+column)
		} else {
			cols = append(cols, boundedTextSQL("s."+column))
		}
	}
	for _, column := range []string{"company", "title", "source_url", "current_status"} {
		cols = append(cols, boundedTextSQL("a."+column))
	}
	return "SELECT " + strings.Join(cols, ",") + " FROM application_sessions s JOIN job_applications a ON a.job_id=s.job_id"
}()

type session struct {
	values   map[string]any
	revision int64
	tab      *int64
}

func (s *session) text(key string) string { v, _ := s.values[key].(string); return v }
func (s *session) projection() map[string]any {
	v := map[string]any{"contract_version": ContractVersion, "revision": s.revision, "review_required": s.text("state") == "submission_detected", "application_status": s.text("current_status")}
	for _, key := range strings.Fields("session_id job_id state company title source_url expected_origin detection_rule interruption_reason created_at expires_at claimed_at detected_at resolved_at") {
		v[key] = s.values[key]
	}
	return v
}
func (e *engine) sessions(suffix string, args []any, limit int) ([]*session, *Error) {
	rows, p := e.query(sessionSelect+suffix, args, limit)
	if p != nil {
		return nil, p
	}
	cols := append(strings.Fields(sessionColumns), "company", "title", "source_url", "current_status")
	out := make([]*session, 0, len(rows))
	for _, row := range rows {
		s := &session{values: map[string]any{}}
		for i, col := range cols {
			v := row[i]
			s.values[col] = v
			switch col {
			case "revision":
				n, ok := v.(int64)
				if !ok || n < 1 {
					return nil, unsupported("REVISION_UNSUPPORTED")
				}
				s.revision = n
			case "bound_tab_id":
				if v != nil {
					n, ok := v.(int64)
					if !ok || n < 0 || n > 2147483647 {
						return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
					}
					s.tab = &n
				}
			default:
				_, text := v.(string)
				nullable := col == "claimed_at" || col == "detected_at" || col == "resolved_at"
				if !text && !(nullable && v == nil) {
					return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
				}
			}
		}
		if _, p := sessionID(s.text("session_id")); p != nil {
			return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
		}
		if !slices.Contains([]string{"pending_claim", "armed", "submission_detected", "confirmed_applied", "dismissed", "interrupted", "expired", "cancelled"}, s.text("state")) {
			return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
		}
		out = append(out, s)
	}
	return out, nil
}
func (e *engine) session(id string) (*session, *Error) {
	rows, p := e.sessions(" WHERE s.session_id=?", []any{id}, 1)
	if p != nil {
		return nil, p
	}
	if len(rows) == 0 {
		return nil, problem("NOT_FOUND", "Application session was not found")
	}
	return rows[0], nil
}
func (e *engine) application(id string) (*applications.SessionJob, *Error) {
	job, p := applications.SessionJobInTransaction(e.ctx, e.tx, id)
	return job, applicationProblem(p, id)
}
func applicationProblem(p *applications.Error, id string) *Error {
	if p == nil {
		return nil
	}
	if p.Code == "NOT_FOUND" {
		return &Error{"identity", "APPLICATION_LEDGER_NOT_FOUND", "ApplicationNotFound", "Application job is not registered: " + id}
	}
	return &Error{Stage: p.Stage, Code: p.Code}
}
func (e *engine) project(id string) (any, *Error) {
	s, p := e.session(id)
	if p != nil {
		return nil, p
	}
	return s.projection(), nil
}
func startable(status string) bool {
	return slices.Contains([]string{"captured", "reviewed", "preparing"}, status)
}

func (e *engine) latest(job string) (any, *Error) {
	if p := e.expire(); p != nil {
		return nil, p
	}
	rows, p := e.sessions(" WHERE s.job_id=? ORDER BY s.created_at DESC,s.session_id DESC LIMIT 1", []any{job}, 1)
	if p != nil {
		return nil, p
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0].projection(), nil
}
func (e *engine) reviews() (any, *Error) {
	if p := e.expire(); p != nil {
		return nil, p
	}
	rows, p := e.sessions(" WHERE s.state='submission_detected' ORDER BY s.detected_at DESC,s.session_id LIMIT 10001", nil, maxRows)
	if p != nil {
		return nil, p
	}
	items := make([]map[string]any, 0, len(rows))
	for _, s := range rows {
		items = append(items, s.projection())
	}
	return map[string]any{"contract_version": ContractVersion, "items": items, "total": len(items)}, nil
}
