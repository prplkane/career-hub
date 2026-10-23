package resumelibrary

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type Entropy interface{ ID() (string, error) }
type SecureEntropy struct{}

func (SecureEntropy) ID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return hex.EncodeToString(b[:]), nil
}

// FileReader must confine paths to the owned Data Home, refuse links/hardlinks,
// and retain exact observations for an owning precommit recheck. It never writes.
type FileReader interface {
	ReadFile(string, int64) ([]byte, bool, *runtimepaths.Error)
}
type FileChange struct {
	ResumeID      string `json:"resume_id"`
	RelativePath  string `json:"relative_path"`
	BeforePresent bool   `json:"before_present"`
	AfterPresent  bool   `json:"after_present"`
	Before        []byte `json:"before"`
	After         []byte `json:"after"`
}

func (FileChange) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted resume file change]")
}

type Result struct {
	Mutation applications.MutationResult
	File     *FileChange
	// File bytes are separate from the bounded JSON metadata, not base64 payloads.
	Bytes  []byte
	IsFile bool
}

func (Result) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "[redacted resume result]") }

type engine struct {
	ctx             context.Context
	tx              *sql.Tx
	reader          FileReader
	entropy         Entropy
	clock           string
	remaining, rows int
	changed         bool
	file            *FileChange
	content         []byte
	isFile          bool
	observe         func(string) *Error
}

// ApplyInTransaction never commits or publishes files. The owner MUST roll back
// on errors and durably record/reconcile File before exposing success. Calling
// this primitive followed by a bare Commit is NOT a complete upload/delete API.
func ApplyInTransaction(ctx context.Context, tx *sql.Tx, r Request, clock string, entropy Entropy, reader FileReader) (*Result, *Error) {
	return ApplyObservedInTransaction(ctx, tx, r, clock, entropy, reader, nil)
}

// The observer freezes the owning recovery witness before the single version
// row is changed. It cannot independently commit or publish a file.
func ApplyObservedInTransaction(ctx context.Context, tx *sql.Tx, r Request, clock string, entropy Entropy, reader FileReader, observe func(string) *Error) (*Result, *Error) {
	if p := r.validate(); p != nil {
		return nil, p
	}
	if ctx == nil || tx == nil || entropy == nil || reader == nil {
		return nil, unsupported("CONFIGURATION_INVALID")
	}
	t, err := time.Parse("2006-01-02T15:04:05Z", clock)
	if err != nil || t.Year() < 1 || t.Year() > 9999 {
		return nil, unsupported("CLOCK_RANGE")
	}
	e := engine{ctx: ctx, tx: tx, clock: clock, reader: reader, entropy: entropy, remaining: maxScanBytes, observe: observe}
	var out any
	var p *Error
	switch r.Operation {
	case "upload", "approved", "attach":
		out, p = e.upload(r)
	case "archive":
		out, p = e.archive(r.Fields)
	case "delete":
		out, p = e.delete(r.Fields["resume_id"])
	case "version_file":
		out, p = e.versionFile(r.Fields)
	case "select", "submitted":
		out, p = e.reference(r)
	case "attachment_file", "submitted_file":
		out, p = e.referenceFile(r)
	case "get_plan", "save_plan":
		out, p = e.plan(r)
	case "get_application":
		var id string
		id, p = jobID(r.Fields)
		if p == nil {
			out, p = e.application(id)
		}
	case "list":
		search, ok := value(r.Fields, "search", "").(string)
		if !ok {
			p = invalid("resume search must be a string")
			break
		}
		archived, ok := value(r.Fields, "include_archived", false).(bool)
		if !ok {
			p = invalid("include_archived must be a boolean")
			break
		}
		var ae *applications.Error
		out, ae = applications.ResumeLibraryInTransaction(ctx, tx, applications.ResumeFilters{Search: search, IncludeArchived: archived})
		p = projectionError(ae, "")
	case "identities":
		var ae *applications.Error
		out, ae = applications.ResumeDigestIdentitiesInTransaction(ctx, tx)
		p = projectionError(ae, "")
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
	return &Result{Mutation: applications.MutationResult{Result: raw, Changed: e.changed}, File: e.file, Bytes: e.content, IsFile: e.isFile}, nil
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
func (e *engine) uuid() (string, *Error) {
	id, err := e.entropy.ID()
	if err != nil || len(id) != 32 {
		return "", unsupported("ENTROPY_UNAVAILABLE")
	}
	for _, c := range id {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return "", unsupported("ENTROPY_INVALID")
		}
	}
	return id, nil
}
func textSQL(name string) string {
	return "CASE WHEN typeof(" + name + ") IN ('text','null') AND coalesce(length(CAST(" + name + " AS BLOB)),0)<=2097152 THEN " + name + " ELSE X'00' END"
}
func intSQL(name string) string {
	return "CASE WHEN typeof(" + name + ")='integer' THEN " + name + " ELSE X'00' END"
}
func textColumns(names ...string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = textSQL(n)
	}
	return strings.Join(out, ",")
}
func (e *engine) query(query string, limit int, args ...any) ([][]any, *Error) {
	rows, err := e.tx.QueryContext(e.ctx, query, args...)
	if err != nil {
		return nil, unsupported("DATABASE_QUERY_FAILED")
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil, unsupported("DATABASE_QUERY_FAILED")
	}
	out := [][]any{}
	for rows.Next() {
		e.rows++
		if len(out) >= limit || e.rows > 10000 {
			return nil, unsupported("ROW_LIMIT")
		}
		row := make([]any, len(names))
		dest := make([]any, len(names))
		for i := range row {
			dest[i] = &row[i]
		}
		if rows.Scan(dest...) != nil {
			return nil, unsupported("DATABASE_QUERY_FAILED")
		}
		for _, cell := range row {
			switch v := cell.(type) {
			case nil, int64:
				e.remaining -= 8
			case string:
				if len(v) > maxCellBytes || !utf8.ValidString(v) {
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
		out = append(out, row)
	}
	if rows.Err() != nil || rows.Close() != nil {
		return nil, unsupported("DATABASE_QUERY_FAILED")
	}
	return out, nil
}
func (e *engine) one(query string, args ...any) ([]any, *Error) {
	rows, p := e.query(query, 1, args...)
	if p != nil || len(rows) == 0 {
		return nil, p
	}
	return rows[0], nil
}
func (e *engine) exec(query string, args ...any) *Error {
	if _, err := e.tx.ExecContext(e.ctx, query, args...); err != nil {
		var code interface{ Code() int }
		if errors.As(err, &code) && code.Code()&255 == 19 {
			return &Error{Stage: "database", Code: "RESUME_LIBRARY_CONSTRAINT", SourceClass: "IntegrityError"}
		}
		return unsupported("DATABASE_WRITE_FAILED")
	}
	e.changed = true
	return nil
}
func jobID(f map[string]any) (string, *Error) {
	id, ok := f["job_id"].(string)
	if !ok || len(id) > applications.MaxIDBytes {
		return "", unsupported("JOB_ID_UNSUPPORTED")
	}
	return id, nil
}
func (e *engine) job(id string) ([]any, *Error) {
	r, p := e.one("SELECT "+textSQL("current_status")+","+intSQL("applied_resume_recorded")+" FROM job_applications WHERE job_id=?", id)
	if p != nil {
		return nil, p
	}
	if r == nil {
		return nil, problem("ApplicationNotFound", "Application job is not registered: "+id)
	}
	_, ok := r[0].(string)
	flag, flagOK := r[1].(int64)
	if !ok || !flagOK || flag < 0 || flag > 1 {
		return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
	}
	return r, nil
}
func projectionError(p *applications.Error, id string) *Error {
	if p == nil {
		return nil
	}
	if p.Code == "NOT_FOUND" {
		return problem("ApplicationNotFound", "Application job is not registered: "+id)
	}
	return unsupported(p.Code)
}
func (e *engine) application(id string) (any, *Error) {
	r, p := applications.ApplicationInTransaction(e.ctx, e.tx, id)
	return r, projectionError(p, id)
}

type version struct {
	id, name, path, fileName, hash, added string
	size                                  int64
	archived                              any
}

func (e *engine) version(id string) (*version, *Error) {
	r, p := e.one("SELECT "+textColumns("resume_id", "display_name", "relative_path", "file_name", "sha256", "added_at", "archived_at")+","+intSQL("size_bytes")+" FROM resume_library WHERE resume_id=?", id)
	if p != nil || r == nil {
		return nil, p
	}
	str := [6]string{}
	for i := range str {
		v, ok := r[i].(string)
		if !ok {
			return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
		}
		str[i] = v
	}
	size, ok := r[7].(int64)
	if !ok {
		return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
	}
	return &version{str[0], str[1], str[2], str[3], str[4], str[5], size, r[6]}, nil
}
func (v *version) summary() map[string]any { return map[string]any{"id": v.id, "display_name": v.name} }
