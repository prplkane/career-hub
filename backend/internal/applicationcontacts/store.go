package applicationcontacts

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/applications"
)

// Entropy belongs to the owning service, never request data. Production UUIDs
// are v4; tests can replay the exact reference allocation sequence.
type Entropy interface{ ID() (string, error) }
type SecureEntropy struct{}

func (SecureEntropy) ID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return hex.EncodeToString(b[:]), nil
}

type engine struct {
	ctx                   context.Context
	tx                    *sql.Tx
	clock                 string
	entropy               Entropy
	remaining, work, rows int
	changed, creating     bool
}

// ApplyInTransaction requires an admitted current-schema IMMEDIATE transaction.
// The caller must rollback on every error. The complete bounded projection is
// constructed here BEFORE commit, including no-op/replay read projections.
func ApplyInTransaction(ctx context.Context, tx *sql.Tx, r Request, clock string, entropy Entropy) (*applications.MutationResult, *Error) {
	if p := r.validate(); p != nil {
		return nil, p
	}
	if ctx == nil || tx == nil || entropy == nil {
		return nil, unsupported("CONFIGURATION_INVALID")
	}
	t, err := time.Parse("2006-01-02T15:04:05Z", clock)
	if err != nil || t.Year() < 1 || t.Year() > 9999 {
		return nil, unsupported("CLOCK_RANGE")
	}
	e := engine{ctx: ctx, tx: tx, clock: clock, entropy: entropy, remaining: maxScanBytes, work: 128 << 20}
	var result any
	var p *Error
	switch r.Operation {
	case "create":
		result, p = e.create(r.Fields)
	case "find":
		result, p = e.find(r.Fields)
	case "get":
		var key string
		key, p = id(r.Fields["contact_id"])
		if p == nil {
			result, p = e.record(key)
		}
	case "list":
		result, p = e.list(value(r.Fields, "include_archived", false))
	case "find_provenance":
		result, p = e.findProvenance(r.Fields)
	case "update":
		result, p = e.update(r.Fields, r.UpdateOrder)
	case "link", "unlink":
		result, p = e.link(r.Fields, r.Operation == "unlink")
	case "archive", "restore":
		result, p = e.archive(r.Fields, r.Operation == "archive")
	}
	if p != nil {
		return nil, p
	}
	if ctx.Err() != nil {
		return nil, unsupported("CANCELED")
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > maxScanBytes {
		return nil, unsupported("RESPONSE_LIMIT")
	}
	return &applications.MutationResult{Result: raw, Changed: e.changed}, nil
}

// Only static source-owned SQL identifiers reach this builder. Oversized or
// non-TEXT cells become a one-byte BLOB sentinel before driver materialization.
func textSQL(column string) string {
	return "CASE WHEN typeof(" + column + ") IN ('text','null') AND coalesce(length(CAST(" + column + " AS BLOB)),0) <= " + strconv.Itoa(maxCellBytes) + " THEN " + column + " ELSE X'00' END"
}
func columns(names []string) string {
	out := []string{}
	for _, name := range names {
		out = append(out, textSQL(name))
	}
	return strings.Join(out, ",")
}
func (e *engine) query(query string, limit int, args ...any) ([][]any, *Error) {
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
		e.rows++
		if len(out) >= limit || e.rows > 100000 {
			return nil, unsupported("ROW_LIMIT")
		}
		cells := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range cells {
			dest[i] = &cells[i]
		}
		if rows.Scan(dest...) != nil {
			return nil, unsupported("DATABASE_QUERY_FAILED")
		}
		for _, cell := range cells {
			switch v := cell.(type) {
			case nil, int64:
				e.remaining -= 8
			case string:
				if !utf8.ValidString(v) {
					return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
				}
				e.remaining -= len(v)
			default:
				return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
			}
		}
		if e.remaining < 0 {
			return nil, unsupported("SCAN_LIMIT")
		}
		out = append(out, cells)
	}
	if rows.Err() != nil || rows.Close() != nil {
		return nil, unsupported("DATABASE_QUERY_FAILED")
	}
	return out, nil
}
func (e *engine) exec(query string, args ...any) (int64, *Error) {
	result, err := e.tx.ExecContext(e.ctx, query, args...)
	if err != nil {
		var code interface{ Code() int }
		if errors.As(err, &code) && code.Code()&255 == 19 {
			if e.creating {
				return 0, invalid("An exact contact channel is already assigned")
			}
			return 0, &Error{Stage: "database", Code: "APPLICATION_CONTACT_CONSTRAINT", SourceClass: "IntegrityError"}
		}
		return 0, unsupported("DATABASE_WRITE_FAILED")
	}
	n, err := result.RowsAffected()
	if err != nil || n < 0 || n > maxRows {
		return 0, unsupported("ROW_LIMIT")
	}
	e.changed = e.changed || n > 0
	return n, nil
}
func (e *engine) uuid() (string, *Error) {
	s, err := e.entropy.ID()
	if err != nil {
		return "", unsupported("ENTROPY_UNAVAILABLE")
	}
	if !hexadecimal(s, 32) {
		return "", unsupported("ENTROPY_INVALID")
	}
	return s, nil
}
func (e *engine) collision(table, column, key string) *Error {
	rows, p := e.query("SELECT 1 FROM "+table+" WHERE "+column+"=? LIMIT 1", 1, key)
	if p != nil {
		return p
	}
	if len(rows) > 0 {
		return unsupported("IDENTITY_COLLISION")
	}
	return nil
}

var baseFields = []string{"contact_id", "name", "role", "company", "context", "identity_channel_type", "identity_channel_normalized", "archived_at", "created_at", "updated_at"}

func base(row []any) (map[string]any, *Error) {
	if len(row) != 11 {
		return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
	}
	out := map[string]any{}
	for i, key := range baseFields {
		v, ok := row[i].(string)
		if !ok && !(key == "archived_at" && row[i] == nil) {
			return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
		}
		out[key] = v
		if key == "archived_at" {
			out[key] = row[i]
		}
	}
	r, ok := row[10].(int64)
	if !ok || r <= 0 {
		return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
	}
	out["revision"] = r
	if _, p := id(out["contact_id"]); p != nil {
		return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
	}
	for _, key := range factNames {
		if utf8.RuneCountInString(out[key].(string)) > factLimit(key) {
			return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
		}
	}
	if !slices.Contains([]string{"", "email", "phone", "professional_profile"}, out["identity_channel_type"].(string)) {
		return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
	}
	return out, nil
}
func (e *engine) selectContact(key string) (map[string]any, *Error) {
	rows, p := e.query("SELECT "+columns(baseFields)+",revision FROM application_contacts WHERE contact_id=? LIMIT 2", 1, key)
	if p != nil {
		return nil, p
	}
	if len(rows) == 0 {
		return nil, problem("NOT_FOUND", "Contact was not found")
	}
	return base(rows[0])
}
func stringRow(row []any) ([]string, *Error) {
	out := make([]string, len(row))
	for i, v := range row {
		s, ok := v.(string)
		if !ok {
			return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
		}
		out[i] = s
	}
	return out, nil
}
func (e *engine) channels(key string) ([]map[string]any, []channel, *Error) {
	rows, p := e.query("SELECT "+columns([]string{"channel_id", "channel_type", "exact_value", "normalized_value"})+" FROM application_contact_channels WHERE contact_id=? ORDER BY channel_type,normalized_value,channel_id LIMIT 21", 20, key)
	if p != nil {
		return nil, nil, p
	}
	out := []map[string]any{}
	cs := []channel{}
	for _, row := range rows {
		s, p := stringRow(row)
		if p != nil {
			return nil, nil, p
		}
		c, p := normalizeChannel(map[string]any{"kind": s[1], "value": s[2]}, "channel")
		if p != nil || c.Value != s[2] || c.Normalized != s[3] {
			return nil, nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
		}
		out = append(out, map[string]any{"id": s[0], "kind": s[1], "value": s[2], "normalized_value": s[3]})
		cs = append(cs, c)
	}
	return out, cs, nil
}
func (e *engine) record(key string) (map[string]any, *Error) {
	row, p := e.selectContact(key)
	if p != nil {
		return nil, p
	}
	return e.project(row)
}
func (e *engine) project(row map[string]any) (map[string]any, *Error) {
	key := row["contact_id"].(string)
	out := map[string]any{"contract_version": ContractVersion, "id": key, "revision": row["revision"], "created_at": row["created_at"], "updated_at": row["updated_at"], "archived": row["archived_at"] != nil, "archived_at": ""}
	for _, f := range factNames {
		out[f] = row[f]
	}
	if row["archived_at"] != nil {
		out["archived_at"] = row["archived_at"]
	}
	cs, _, p := e.channels(key)
	if p != nil {
		return nil, p
	}
	out["channels"] = cs
	names := []string{"provenance_id", "origin", "source_reference", "discovery_id", "job_id", "source_content_sha256", "source_text", "stated_fields_json", "recorded_at"}
	rows, p := e.query("SELECT "+columns(names)+",line_start,line_end FROM application_contact_provenance WHERE contact_id=? ORDER BY recorded_at,rowid LIMIT 10001", maxRows, key)
	if p != nil {
		return nil, p
	}
	proofs := []map[string]any{}
	for _, r := range rows {
		s, p := stringRow(r[:9])
		if p != nil {
			return nil, p
		}
		start, a := r[9].(int64)
		end, b := r[10].(int64)
		if !a || !b || start < 0 || end < start {
			return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
		}
		st, p := readStated(s[7])
		if p != nil {
			return nil, p
		}
		proof := map[string]any{"id": s[0], "origin": s[1], "source_reference": s[2], "discovery_id": s[3], "job_id": s[4], "source_content_sha256": s[5], "source_text": s[6], "stated_fields": st, "recorded_at": s[8], "line_start": start, "line_end": end}
		proofs = append(proofs, proof)
	}
	out["provenance"] = proofs
	for _, kind := range []string{"job", "discovery"} {
		table, sourceTable := linkTables(kind)
		rows, p := e.query("SELECT "+columns([]string{"links." + kind + "_id", "sources.title", "sources.company"})+" FROM "+table+" AS links JOIN "+sourceTable+" AS sources ON sources."+kind+"_id=links."+kind+"_id WHERE links.contact_id=? ORDER BY links."+kind+"_id LIMIT 10001", maxRows, key)
		if p != nil {
			return nil, p
		}
		items := []map[string]any{}
		ids := []string{}
		for _, r := range rows {
			s, p := stringRow(r)
			if p != nil {
				return nil, p
			}
			items = append(items, map[string]any{"id": s[0], "title": s[1], "company": s[2]})
			ids = append(ids, s[0])
		}
		plural := "jobs"
		if kind == "discovery" {
			plural = "discoveries"
		}
		out[plural] = items
		out[kind+"_ids"] = ids
	}
	return out, nil
}
func linkTables(kind string) (string, string) {
	if kind == "job" {
		return "application_contact_job_links", "job_applications"
	}
	return "application_contact_discovery_links", "job_discoveries"
}
func (e *engine) requireSources(jobs, discoveries []string) *Error {
	for i, items := range [][]string{jobs, discoveries} {
		kind := "job"
		if i == 1 {
			kind = "discovery"
		}
		_, table := linkTables(kind)
		for _, key := range items {
			rows, p := e.query("SELECT 1 FROM "+table+" WHERE "+kind+"_id=? LIMIT 1", 1, key)
			if p != nil {
				return p
			}
			if len(rows) == 0 {
				return invalid("Unknown " + kind + "_id: " + key)
			}
		}
	}
	return nil
}
func proofSources(proof map[string]any) ([]string, []string) {
	jobs, discoveries := []string{}, []string{}
	if proof["job_id"] != "" {
		jobs = append(jobs, proof["job_id"].(string))
	}
	if proof["discovery_id"] != "" {
		discoveries = append(discoveries, proof["discovery_id"].(string))
	}
	return jobs, discoveries
}
func (e *engine) proofOwner(proof map[string]any) (string, *Error) {
	if proof["origin"] != "jd_candidate" {
		return "", nil
	}
	rows, p := e.query("SELECT "+textSQL("contact_id")+" FROM application_contact_provenance WHERE origin='jd_candidate' AND source_reference=? AND source_content_sha256=? LIMIT 2", 1, proof["source_reference"], proof["source_content_sha256"])
	if p != nil {
		return "", p
	}
	if len(rows) == 0 {
		return "", nil
	}
	s, p := stringRow(rows[0])
	if p != nil {
		return "", p
	}
	return s[0], nil
}
func (e *engine) insertProof(key string, proof map[string]any) *Error {
	owner, p := e.proofOwner(proof)
	if p != nil {
		return p
	}
	if owner != "" {
		if owner == key {
			return nil
		}
		return proofConflict()
	}
	u, p := e.uuid()
	if p != nil {
		return p
	}
	proofID := "contact_provenance_" + u
	if p = e.collision("application_contact_provenance", "provenance_id", proofID); p != nil {
		return p
	}
	_, p = e.exec("INSERT OR IGNORE INTO application_contact_provenance(provenance_id,contact_id,origin,source_reference,discovery_id,job_id,source_content_sha256,source_text,line_start,line_end,stated_fields_json,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", proofID, key, proof["origin"], proof["source_reference"], proof["discovery_id"], proof["job_id"], proof["source_content_sha256"], proof["source_text"], proof["line_start"], proof["line_end"], statedJSON(proof["stated_fields"].(map[string]any)), e.clock)
	return p
}
func stale() *Error { return problem("REVISION_CONFLICT", "Contact changed after it was loaded") }
func proofConflict() *Error {
	return problem("PROVENANCE_CONFLICT", "This JD contact candidate is already linked to another contact")
}
func (e *engine) bump(key string, revision int64, set string, args ...any) *Error {
	if revision == math.MaxInt64 {
		return unsupported("REVISION_LIMIT")
	}
	args = append(args, e.clock, key, revision)
	n, p := e.exec("UPDATE application_contacts SET "+set+"revision=revision+1,updated_at=? WHERE contact_id=? AND revision=?", args...)
	if p != nil {
		return p
	}
	if n != 1 {
		return stale()
	}
	return nil
}
