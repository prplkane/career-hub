package applications

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

type ResumeFilters struct {
	Search          string
	IncludeArchived bool
}

type ResumeUsage struct {
	Selections int64 `json:"current_application_selections"`
	Snapshots  int64 `json:"applied_resume_snapshots"`
	Events     int64 `json:"submitted_resume_events"`
}

// ResumeEntry never contains a path, filename or content hash. The optional
// management fields are absent (not null) from the source's small picker view.
type ResumeEntry struct {
	ResumeIdentity
	*ResumeManagement
}

type ResumeManagement struct {
	Archived  bool        `json:"archived"`
	AddedAt   Value       `json:"added_at"`
	FileType  string      `json:"file_type"`
	SizeBytes json.Number `json:"size_bytes"`
	Usage     ResumeUsage `json:"usage"`
	Deletable bool        `json:"deletable"`
}

// DigestIdentity is an internal projection, never a browser management payload.
// It does not itself access the referenced content or infer a digest status.
type DigestIdentity struct {
	FileName string `json:"file_name"`
	SHA256   string `json:"sha256"`
	Archived bool   `json:"archived"`
}

// ResumeManagementSnapshot keeps browser rows and only their internal digest
// identities together. The owner must not serialize Identities to HTTP.
type ResumeManagementSnapshot struct {
	Rows       []ResumeEntry             `json:"-"`
	Identities map[string]DigestIdentity `json:"-"`
}

func ValidateResumeFilters(filters ResumeFilters) *Error {
	if len(filters.Search) > MaxRequestBytes {
		return failure("request", "INPUT_LIMIT", nil)
	}
	if !utf8.ValidString(filters.Search) {
		return failure("request", "INPUT_UNSUPPORTED", nil)
	}
	return nil
}

func ReadResumeLibrary(ctx context.Context, db *sql.DB, filters ResumeFilters) ([]ResumeEntry, *Error) {
	if err := ValidateResumeFilters(filters); err != nil {
		return nil, err
	}
	var result []ResumeEntry
	e := readCurrent(ctx, db, func(q *queryState) *Error {
		var e *Error
		result, e = resumeLibrary(q, filters)
		return e
	})
	if e != nil {
		return nil, e
	}
	return result, nil
}

// ReadResumeManagementSnapshot performs the filtered management projection and
// its content-identity lookup in one read transaction. It does not own the DB,
// native path guard or request admission. No partial result escapes on failure.
func ReadResumeManagementSnapshot(ctx context.Context, db *sql.DB, search string) (*ResumeManagementSnapshot, *Error) {
	filters := ResumeFilters{Search: search, IncludeArchived: true}
	if err := ValidateResumeFilters(filters); err != nil {
		return nil, err
	}
	var result *ResumeManagementSnapshot
	err := readCurrent(ctx, db, func(q *queryState) *Error {
		rows, identities, problem := resumeLibraryProjection(q, filters, true)
		if problem != nil {
			return problem
		}
		result = &ResumeManagementSnapshot{Rows: rows, Identities: identities}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func ResumeLibraryInTransaction(ctx context.Context, tx *sql.Tx, filters ResumeFilters) ([]ResumeEntry, *Error) {
	if e := ValidateResumeFilters(filters); e != nil {
		return nil, e
	}
	q, e := mutationQuery(ctx, tx)
	if e != nil {
		return nil, e
	}
	return resumeLibrary(q, filters)
}

func resumeLibrary(q *queryState, filters ResumeFilters) ([]ResumeEntry, *Error) {
	rows, _, err := resumeLibraryProjection(q, filters, false)
	return rows, err
}

func resumeLibraryProjection(q *queryState, filters ResumeFilters, includeIdentities bool) ([]ResumeEntry, map[string]DigestIdentity, *Error) {
	result := []ResumeEntry{}
	identities := map[string]DigestIdentity{}
	err := func() *Error {
		where := "display_name LIKE ? COLLATE NOCASE"
		if !filters.IncludeArchived {
			where += " AND archived_at IS NULL"
		}
		rows, ids := [][]any{}, []string{}
		err := q.rows("resumes", "SELECT resume_id, display_name, relative_path, file_name, sha256, size_bytes, added_at, archived_at FROM resume_library WHERE "+where+" ORDER BY display_name COLLATE NOCASE, added_at DESC, resume_id", []any{"%" + textcompat.Strip(filters.Search) + "%"}, 8, func(raw []any) error {
			if len(rows) >= MaxRecords {
				return errBudget
			}
			id, ok := raw[0].(string)
			if !ok {
				return errors.New("unsupported identifier")
			}
			rows, ids = append(rows, raw), append(ids, id)
			return nil
		})
		if err != nil {
			return err
		}
		usage := map[string]ResumeUsage{}
		if filters.IncludeArchived {
			if err := resumeUsage(q, ids, usage); err != nil {
				return err
			}
		}
		for i, raw := range rows {
			v, err := q.cells(raw[:2])
			if err != nil {
				return failure("resumes", "DATABASE_VALUE_UNSUPPORTED", nil)
			}
			entry := ResumeEntry{ResumeIdentity: ResumeIdentity{v[0], v[1]}}
			if filters.IncludeArchived {
				fileName, ok := raw[3].(string)
				if !ok {
					return failure("resumes", "DATABASE_VALUE_UNSUPPORTED", nil)
				}
				added, addedErr := value(raw[6])
				size, sizeErr := resumeSize(raw[5])
				if addedErr != nil || sizeErr != nil {
					return failure("resumes", "DATABASE_VALUE_UNSUPPORTED", nil)
				}
				counts := usage[ids[i]]
				entry.ResumeManagement = &ResumeManagement{raw[7] != nil, added, resumeFileType(fileName), size, counts, counts == (ResumeUsage{})}
				if includeIdentities {
					hash, ok := raw[4].(string)
					if !ok {
						return failure("resume_digest", "DATABASE_VALUE_UNSUPPORTED", nil)
					}
					if _, duplicate := identities[ids[i]]; duplicate {
						return failure("resume_digest", "DATABASE_VALUE_UNSUPPORTED", nil)
					}
					identities[ids[i]] = DigestIdentity{fileName, hash, raw[7] != nil}
				}
			}
			result = append(result, entry)
		}
		return checkResult(result)
	}()
	if err != nil {
		return nil, nil, err
	}
	return result, identities, nil
}

// Three queries per bounded batch, not per resume. Historical initial_snapshot
// events are deliberately excluded; stale applied_resume_id references count
// even if their recorded flag is false. Both rules match the source exactly.
func resumeUsage(q *queryState, ids []string, usage map[string]ResumeUsage) *Error {
	queries := []struct {
		prefix, suffix string
	}{
		{"SELECT resume_id, COUNT(*) FROM application_resume_attachments WHERE resume_id IN (", ") GROUP BY resume_id"},
		{"SELECT applied_resume_id, COUNT(*) FROM job_applications WHERE applied_resume_id IN (", ") GROUP BY applied_resume_id"},
		{"SELECT resume_id, COUNT(*) FROM application_applied_resume_events WHERE resume_id IN (", ") AND action IN ('recorded', 'corrected') GROUP BY resume_id"},
	}
	for index, query := range queries {
		if err := q.batches(ids, "resume_usage", query.prefix, query.suffix, 2, func(raw []any) error {
			id, idOK := raw[0].(string)
			count, countOK := raw[1].(int64)
			if !idOK || !countOK || count < 0 {
				return errors.New("unsupported usage count")
			}
			counts := usage[id]
			switch index {
			case 0:
				counts.Selections = count
			case 1:
				counts.Snapshots = count
			case 2:
				counts.Events = count
			}
			usage[id] = counts
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func ReadResumeDigestIdentities(ctx context.Context, db *sql.DB) (map[string]DigestIdentity, *Error) {
	var result map[string]DigestIdentity
	e := readCurrent(ctx, db, func(q *queryState) *Error {
		var e *Error
		result, e = resumeDigestIdentities(q)
		return e
	})
	if e != nil {
		return nil, e
	}
	return result, nil
}

func ResumeDigestIdentitiesInTransaction(ctx context.Context, tx *sql.Tx) (map[string]DigestIdentity, *Error) {
	q, e := mutationQuery(ctx, tx)
	if e != nil {
		return nil, e
	}
	return resumeDigestIdentities(q)
}

func resumeDigestIdentities(q *queryState) (map[string]DigestIdentity, *Error) {
	result := map[string]DigestIdentity{}
	err := func() *Error {
		if err := q.rows("resume_digest", "SELECT resume_id, file_name, sha256, archived_at FROM resume_library", nil, 4, func(raw []any) error {
			if len(result) >= MaxRecords {
				return errBudget
			}
			id, idOK := raw[0].(string)
			name, nameOK := raw[1].(string)
			hash, hashOK := raw[2].(string)
			if !idOK || !nameOK || !hashOK {
				return errors.New("unsupported content identity")
			}
			result[id] = DigestIdentity{name, hash, raw[3] != nil}
			return nil
		}); err != nil {
			return err
		}
		return checkResult(result)
	}()
	if err != nil {
		return nil, err
	}
	return result, nil
}

func resumeFileType(name string) string {
	// Path(...).suffix in pinned Python3.12 ignores a sole leading or trailing
	// dot. Native path flavor matters only for malformed/legacy path-like names;
	// current upload writers require a plain filename on every platform.
	name = name[len(filepath.VolumeName(name)):]
	if runtime.GOOS == "windows" {
		name = strings.ReplaceAll(name, "\\", "/")
	}
	parts := strings.Split(name, "/")
	name = ""
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" && parts[i] != "." {
			name = parts[i]
			break
		}
	}
	if index := strings.LastIndexByte(name, '.'); index > 0 && index < len(name)-1 {
		return textcompat.Upper(name[index+1:])
	}
	return "FILE"
}

// Preserve int(size_bytes) for SQLite integers, finite REALs and Python's
// supported decimal text/ASCII byte forms. Keep large integers exact in JSON.
func resumeSize(raw any) (json.Number, error) {
	var input string
	bytesInput := false
	switch size := raw.(type) {
	case int64:
		return json.Number(strconv.FormatInt(size, 10)), nil
	case float64:
		if !math.IsInf(size, 0) && !math.IsNaN(size) {
			integer, _ := new(big.Float).SetFloat64(size).Int(nil)
			return json.Number(integer.String()), nil
		}
	case string:
		input = size
	case []byte:
		input, bytesInput = string(size), true
	default:
		return "", errors.New("unsupported resume size")
	}
	input = strings.TrimFunc(input, func(r rune) bool {
		if bytesInput {
			return r == ' ' || (r >= '\t' && r <= '\r')
		}
		return textcompat.IsSpace(r) && !(r >= 0x1c && r <= 0x1f)
	})
	var canonical strings.Builder
	if len(input) > 0 && (input[0] == '+' || input[0] == '-') {
		canonical.WriteByte(input[0])
		input = input[1:]
	}
	digits, previousDigit := 0, false
	for _, point := range input {
		if point == '_' && previousDigit {
			previousDigit = false
			continue
		}
		digit, valid := textcompat.DecimalDigit(point)
		if !valid || (bytesInput && point > 0x7f) {
			return "", errors.New("unsupported resume size")
		}
		canonical.WriteByte('0' + byte(digit))
		digits++
		previousDigit = true
	}
	if !previousDigit || digits > 4300 {
		return "", errors.New("unsupported resume size")
	}
	integer, ok := new(big.Int).SetString(canonical.String(), 10)
	if !ok {
		return "", errors.New("unsupported resume size")
	}
	return json.Number(integer.String()), nil
}
