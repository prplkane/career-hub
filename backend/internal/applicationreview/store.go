package applicationreview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/textcompat"
)

type engine struct {
	ctx         context.Context
	tx          *sql.Tx
	clock       string
	bytes, rows int
	changed     bool
}

// All operations borrow one transaction. No commit, independent connection,
// filesystem access or evidence promotion occurs here. The native owner must
// build the complete result before attempting commit and roll back any failure.
func ApplyInTransaction(ctx context.Context, tx *sql.Tx, r Request, clock string) (*applications.MutationResult, *Error) {
	if p := r.validate(); p != nil {
		return nil, p
	}
	if ctx == nil || tx == nil {
		return nil, unsupported("CONFIGURATION_INVALID")
	}
	t, er := time.Parse("2006-01-02T15:04:05Z", clock)
	if er != nil || t.Year() < 1 || t.Year() > 9999 {
		return nil, unsupported("CLOCK_RANGE")
	}
	e := engine{ctx: ctx, tx: tx, clock: clock}
	var out any
	var p *Error
	switch r.Operation {
	case "get_assessment":
		out, p = e.assessment(r.Fields["job_id"])
	case "save_assessment":
		out, p = e.saveAssessment(r.Fields)
	case "list_resolutions":
		out, p = e.resolutions(r.Fields)
	case "save_resolution":
		out, p = e.saveResolution(r.Fields)
	case "list_manual", "list_actions":
		out, p = e.instructions(r.Operation, r.Fields["job_id"])
	case "save_manual", "save_action":
		out, p = e.saveInstruction(r)
	}
	if p != nil {
		return nil, p
	}
	if ctx.Err() != nil {
		return nil, unsupported("CANCELED")
	}
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > applications.MaxResultBytes {
		return nil, unsupported("RESPONSE_LIMIT")
	}
	return &applications.MutationResult{Changed: e.changed, Result: raw}, nil
}

func text(name string) string {
	return "CASE WHEN typeof(" + name + ") IN ('text','null') AND coalesce(length(CAST(" + name + " AS BLOB)),0)<=2097152 THEN " + name + " ELSE X'00' END"
}
func number(name string) string {
	return "CASE WHEN typeof(" + name + ")='integer' THEN " + name + " ELSE X'00' END"
}
func (e *engine) query(q string, args ...any) ([]map[string]any, *Error) {
	rows, err := e.tx.QueryContext(e.ctx, q, args...)
	if err != nil {
		return nil, unsupported("DATABASE_QUERY_FAILED")
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil, unsupported("DATABASE_QUERY_FAILED")
	}
	// Computed column expressions do not expose their semantic name. Callers
	// supply stable aliases for projection queries; positional guards use c0/c1.
	out := []map[string]any{}
	for rows.Next() {
		e.rows++
		if e.rows > maxRows {
			return nil, unsupported("ROW_LIMIT")
		}
		v := make([]any, len(names))
		ptr := make([]any, len(v))
		for i := range v {
			ptr[i] = &v[i]
		}
		if rows.Scan(ptr...) != nil {
			return nil, unsupported("DATABASE_QUERY_FAILED")
		}
		r := map[string]any{}
		for i, x := range v {
			switch x := x.(type) {
			case nil:
				e.bytes++
			case int64:
				e.bytes += 8
			case string:
				if !utf8.ValidString(x) || len(x) > maxCellBytes {
					return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
				}
				e.bytes += len(x)
			default:
				return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
			}
			if e.bytes > maxScanBytes {
				return nil, unsupported("SCAN_LIMIT")
			}
			r[names[i]] = x
		}
		out = append(out, r)
	}
	if rows.Err() != nil {
		return nil, unsupported("DATABASE_QUERY_FAILED")
	}
	return out, nil
}
func (e *engine) one(q string, args ...any) (map[string]any, *Error) {
	rows, p := e.query(q, args...)
	if p != nil {
		return nil, p
	}
	if len(rows) > 1 {
		return nil, unsupported("ROW_CARDINALITY")
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}
func (e *engine) exec(q string, args ...any) (int64, *Error) {
	for i, value := range args {
		if n, ok := value.(*big.Int); ok {
			if !n.IsInt64() {
				return 0, problem("OverflowError", "Python int too large to convert to SQLite INTEGER")
			}
			args[i] = n.Int64()
		}
	}
	r, err := e.tx.ExecContext(e.ctx, q, args...)
	if err != nil {
		var coded interface{ Code() int }
		if errors.As(err, &coded) && coded.Code()&255 == 19 {
			return 0, problem("IntegrityError", "")
		}
		return 0, unsupported("DATABASE_WRITE_FAILED")
	}
	id, err := r.LastInsertId()
	if err != nil {
		return 0, unsupported("DATABASE_WRITE_FAILED")
	}
	e.changed = true
	return id, nil
}
func (e *engine) requireJob(id any) *Error {
	row, p := e.one("SELECT 1 AS present FROM job_applications WHERE job_id=?", id)
	if p != nil {
		return p
	}
	if row == nil {
		return problem("ApplicationNotFound", "Application job is not registered: "+sourceString(id))
	}
	return nil
}
func cols(names ...string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = text(n) + " AS " + n
	}
	return strings.Join(out, ",")
}

const identityText = "job_id,job_fingerprint,candidate_revision,assessment_context_revision,computed_at"

func assessmentColumns() string {
	return cols(strings.Split(identityText, ",")...) + "," + number("job_analyzer_version") + " AS job_analyzer_version," + number("assessment_version") + " AS assessment_version," + text("assessment_json") + " AS assessment_json"
}
func (e *engine) assessment(id any) (map[string]any, *Error) {
	r, p := e.one("SELECT "+assessmentColumns()+" FROM job_assessments WHERE job_id=?", id)
	if p != nil || r == nil {
		return r, p
	}
	raw, ok := r["assessment_json"].(string)
	if !ok {
		return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
	}
	if !json.Valid([]byte(raw)) {
		if unsupportedJSONConstants(raw) {
			return nil, unsupported("ASSESSMENT_JSON_UNSUPPORTED")
		}
		return nil, invalid("Stored assessment for " + sourceString(r["job_id"]) + " is invalid JSON")
	}
	if !storedJSONSafe(raw) {
		return nil, unsupported("ASSESSMENT_JSON_UNSUPPORTED")
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	var value any
	if d.Decode(&value) != nil {
		return nil, invalid("Stored assessment for " + sourceString(r["job_id"]) + " is invalid JSON")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, invalid("Stored assessment for " + sourceString(r["job_id"]) + " is invalid JSON")
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, invalid("Stored assessment for " + sourceString(r["job_id"]) + " must be an object")
	}
	if !boundedJSON(value) {
		return nil, unsupported("ASSESSMENT_JSON_UNSUPPORTED")
	}
	if p := normalizeStoredNumbers(value); p != nil {
		return nil, p
	}
	delete(r, "assessment_json")
	r["assessment"] = value
	return r, nil
}
func (e *engine) saveAssessment(f map[string]any) (any, *Error) {
	id, ok := f["job_id"].(string)
	chars := []rune(id)
	if !ok || textcompat.Strip(id) == "" || id == "." || strings.ContainsAny(id, "/\\") || len(chars) > 1 && chars[1] == ':' {
		return nil, invalid("job_id must be a plain identifier")
	}
	fingerprint, p := sha(f["job_fingerprint"], "job_fingerprint", false)
	if p != nil {
		return nil, p
	}
	analyzer, p := integer(f["job_analyzer_version"], 0, "job_analyzer_version must be a non-negative integer")
	if p != nil {
		return nil, p
	}
	candidate, p := sha(f["candidate_revision"], "candidate_revision", true)
	if p != nil {
		return nil, p
	}
	context, p := sha(f["assessment_context_revision"], "assessment_context_revision", true)
	if p != nil {
		return nil, p
	}
	version, p := integer(f["assessment_version"], 1, "assessment_version must be a positive integer")
	if p != nil {
		return nil, p
	}
	value, ok := f["assessment"].(map[string]any)
	if !ok {
		return nil, invalid("assessment must be an object")
	}
	raw, p := assessmentJSON(value)
	if p != nil {
		return nil, p
	}
	if p = e.requireJob(id); p != nil {
		return nil, p
	}
	_, p = e.exec(`INSERT INTO job_assessments(job_id,job_fingerprint,job_analyzer_version,candidate_revision,assessment_context_revision,assessment_version,assessment_json,computed_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(job_id) DO UPDATE SET job_fingerprint=excluded.job_fingerprint,job_analyzer_version=excluded.job_analyzer_version,candidate_revision=excluded.candidate_revision,assessment_context_revision=excluded.assessment_context_revision,assessment_version=excluded.assessment_version,assessment_json=excluded.assessment_json,computed_at=excluded.computed_at`, id, fingerprint, analyzer, candidate, context, version, string(raw), e.clock)
	if p != nil {
		return nil, p
	}
	r, p := e.assessment(id)
	if p == nil && r == nil {
		p = invalid("Saved assessment could not be reloaded")
	}
	return r, p
}

// Revision deliberately excludes assessment content and computed_at, matching
// the five source validity inputs. Never substitute the UI's current cache key.
func ResolutionRevision(record map[string]any) (string, *Error) {
	identity := map[string]any{}
	for _, k := range []string{"job_fingerprint", "job_analyzer_version", "candidate_revision", "assessment_context_revision", "assessment_version"} {
		identity[k] = record[k]
	}
	if !boundedJSON(identity) {
		return "", unsupported("REVISION_UNSUPPORTED")
	}
	raw, err := protocol.EncodeRecord(identity)
	if err != nil {
		return "", unsupported("REVISION_UNSUPPORTED")
	}
	h := sha256.Sum256(bytes.TrimSuffix(raw, []byte{'\n'}))
	return "sha256:" + hex.EncodeToString(h[:]), nil
}
func (e *engine) saveResolution(f map[string]any) (any, *Error) {
	key, ok := f["finding_key"].(string)
	if !ok {
		return nil, invalid("finding_key must be a string")
	}
	key = textcompat.Strip(key)
	if key == "" || utf8.RuneCountInString(key) > 160 {
		return nil, invalid("finding_key must be between 1 and 160 characters")
	}
	for _, c := range key {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._:-", c) {
			return nil, invalid("finding_key may contain only letters, numbers, dot, underscore, colon, and hyphen")
		}
	}
	resolution, ok := f["resolution"].(string)
	if !ok || !oneOf(resolution, "confirmed", "resolved", "not_applicable", "accepted_risk") {
		return nil, invalid("resolution must be confirmed, resolved, not_applicable, or accepted_risk")
	}
	note, p := normalizedNote(get(f, "note", ""))
	if p != nil {
		return nil, p
	}
	if p = e.requireJob(f["job_id"]); p != nil {
		return nil, p
	}
	r, p := e.one("SELECT "+cols("job_fingerprint", "candidate_revision", "assessment_context_revision")+","+number("job_analyzer_version")+" AS job_analyzer_version,"+number("assessment_version")+" AS assessment_version FROM job_assessments WHERE job_id=?", f["job_id"])
	if p != nil {
		return nil, p
	}
	if r == nil {
		return nil, invalid("calculate the current assessment before recording a resolution")
	}
	revision, p := ResolutionRevision(r)
	if p != nil {
		return nil, p
	}
	id, p := e.exec("INSERT INTO job_assessment_resolution_events(job_id,finding_key,assessment_revision,resolution,note,recorded_at) VALUES(?,?,?,?,?,?)", f["job_id"], key, revision, resolution, note, e.clock)
	if p != nil {
		return nil, p
	}
	return map[string]any{"event_id": id, "finding_key": key, "assessment_revision": revision, "resolution": resolution, "note": note, "recorded_at": e.clock, "stale": false}, nil
}
func oneOf(value string, allowed ...string) bool {
	for _, x := range allowed {
		if x == value {
			return true
		}
	}
	return false
}
func (e *engine) resolutions(f map[string]any) (any, *Error) {
	if p := e.requireJob(f["job_id"]); p != nil {
		return nil, p
	}
	rows, p := e.query("SELECT "+number("event_id")+" AS event_id,"+cols("finding_key", "assessment_revision", "resolution", "note", "recorded_at")+" FROM job_assessment_resolution_events WHERE job_id=? ORDER BY event_id DESC", f["job_id"])
	if p != nil {
		return nil, p
	}
	latest := map[string]map[string]any{}
	counts := map[string]int{}
	keys := []string{}
	for _, r := range rows {
		key, ok := r["finding_key"].(string)
		if !ok {
			return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
		}
		counts[key]++
		if _, exists := latest[key]; exists {
			continue
		}
		latest[key] = r
		keys = append(keys, key)
		revision, ok := r["assessment_revision"].(string)
		if !ok {
			return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
		}
		current := f["current_assessment_revision"]
		r["stale"] = truthy(current) && current != revision
	}
	sort.Strings(keys)
	out := []any{}
	for _, key := range keys {
		latest[key]["history_count"] = counts[key]
		out = append(out, latest[key])
	}
	return out, nil
}
