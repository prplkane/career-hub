package applicationwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/applicationanalysis"
	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/textcompat"
)

const maxRawBytes = applicationanalysis.MaxSourceBytes
const maxRawTotal = 8 << 20

func boundedText(value string, max int) *Error {
	if len(value) > max {
		return &Error{Stage: "input", Code: "APPLICATION_LEDGER_INPUT_LIMIT"}
	}
	if !utf8.ValidString(value) {
		return &Error{Stage: "input", Code: "APPLICATION_LEDGER_UNICODE_UNSUPPORTED"}
	}
	return nil
}
func trim(value string) string { return strings.TrimFunc(value, textcompat.IsSpace) }
func requestSize(size int) *Error {
	if size > applications.MaxRequestBytes {
		return failed("input", "APPLICATION_LEDGER_INPUT_LIMIT")
	}
	return nil
}
func rawInvalid() *Error {
	return invalid("job.raw_jd_path must reference a readable managed JD source")
}

type rawObservation struct {
	path        string
	raw         []byte
	fingerprint string
}
type sourceReads struct {
	ctx                    context.Context
	root                   *runtimepaths.Root
	files                  []rawObservation
	index                  map[string]int
	total                  int
	home                   datahome.Info
	captures               []captureObservation
	publication            *discoveryPublication
	recovery               *discoveryPublication
	resumePublication      *resumePublication
	resumeRecovery         *resumePublication
	resumeReads            *resumeReads
	approvedImport         *sourceReads // Closed approval/extraction witness; no SQL/publication.
	assessmentReads        *assessmentReads
	intakeResources        *runtimepaths.Root
	normalizedReads        *normalizedReads
	newJobWitness          *newJobLedgerWitness
	duplicateNewJobWitness *newJobDuplicateWitness
	pins                   *runtimepaths.SQLiteFiles
}

type captureObservation struct {
	path    string
	raw     []byte
	present bool
}

// Check lets closed domain readers validate an already bounded relative path
// against this operation's retained root. The reader still owns path scope.
func (r *sourceReads) Check(path string) *runtimepaths.Error {
	if r == nil || r.ctx == nil || r.ctx.Err() != nil || r.root == nil {
		return &runtimepaths.Error{Code: "READ_CANCELED"}
	}
	return r.root.Check(path)
}

// ReadFile implements Discovery's read-only managed source port and retains
// exact present/absent observations for the precommit recheck. No fetch/OCR.
func (r *sourceReads) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	if r.ctx != nil && r.ctx.Err() != nil {
		return nil, false, &runtimepaths.Error{Code: "READ_CANCELED"}
	}
	if limit < 1 || limit > 4<<20 {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	for _, seen := range r.captures {
		if seen.path == path {
			if int64(len(seen.raw)) > limit {
				return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
			}
			return seen.raw, seen.present, nil
		}
	}
	raw, present, e := r.root.ReadSingleLinkFile(path, limit)
	if e != nil {
		return nil, false, e
	}
	if len(raw) > maxRawTotal-r.total || len(r.captures) >= 10000 {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	r.total += len(raw)
	r.captures = append(r.captures, captureObservation{path, raw, present})
	return raw, present, nil
}

func (r *sourceReads) fingerprint(path string) (string, *Error) {
	// Explicit fingerprints do not reach this function. Raw sources never cause
	// a network request and must pass the existing managed-relative-path guard.
	if e := boundedText(path, applications.MaxIDBytes); e != nil {
		return "", e
	}
	if index, found := r.index[path]; found {
		return r.files[index].fingerprint, nil
	}
	raw, present, err := r.root.ReadFile(path, maxRawBytes)
	if err != nil {
		if err.Code == "FILE_SIZE_LIMIT" {
			return "", &Error{Stage: "input", Code: "APPLICATION_LEDGER_INPUT_LIMIT"}
		}
		return "", rawInvalid()
	}
	if !present {
		return "", rawInvalid()
	}
	if len(raw) > maxRawTotal-r.total {
		return "", &Error{Stage: "input", Code: "APPLICATION_LEDGER_INPUT_LIMIT"}
	}
	fp, problem := fingerprintBytes(raw)
	if problem != nil {
		return "", problem
	}
	if r.index == nil {
		r.index = make(map[string]int)
	}
	r.index[path] = len(r.files)
	r.total += len(raw)
	r.files = append(r.files, rawObservation{path, raw, fp})
	return fp, nil
}
func fingerprintBytes(raw []byte) (string, *Error) {
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	if !utf8.Valid(raw) {
		return "", rawInvalid()
	}
	// read_text(encoding="utf-8-sig") also uses universal newline translation.
	text := strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\r", "\n")
	fp, err := applicationanalysis.SourceFingerprint(text)
	if errors.Is(err, applicationanalysis.ErrLimit) {
		return "", &Error{Stage: "input", Code: "APPLICATION_LEDGER_INPUT_LIMIT"}
	}
	if err != nil {
		return "", &Error{Stage: "input", Code: "APPLICATION_LEDGER_UNICODE_UNSUPPORTED"}
	}
	return fp, nil
}
func (r *sourceReads) unchanged() *Error {
	if r.approvedImport != nil {
		if e := r.approvedImport.unchanged(); e != nil {
			return e
		}
	}
	if r.duplicateNewJobWitness != nil {
		if e := r.duplicateNewJobWitness.unchanged(r); e != nil {
			return e
		}
	}
	if r.newJobWitness != nil {
		if e := r.newJobWitness.unchanged(r.root); e != nil {
			return e
		}
	}
	if r.normalizedReads != nil {
		if e := r.normalizedReads.unchanged(); e != nil {
			return e
		}
	}
	if r.assessmentReads != nil {
		if e := r.assessmentReads.unchanged(); e != nil {
			return e
		}
	}
	if r.resumeReads != nil {
		if e := r.resumeReads.unchanged(); e != nil {
			return e
		}
	}
	for _, seen := range r.files {
		if r.ctx != nil && r.ctx.Err() != nil {
			return canceled()
		}
		raw, present, err := r.root.ReadFile(seen.path, maxRawBytes)
		if err != nil || !present || !bytes.Equal(raw, seen.raw) {
			return &Error{Stage: "source", Code: "APPLICATION_LEDGER_SOURCE_CHANGED"}
		}
	}
	for _, seen := range r.captures {
		if r.ctx != nil && r.ctx.Err() != nil {
			return canceled()
		}
		raw, present, e := r.root.ReadSingleLinkFile(seen.path, 4<<20)
		if e != nil || present != seen.present || !bytes.Equal(raw, seen.raw) {
			return failed("source", "APPLICATION_LEDGER_SOURCE_CHANGED")
		}
	}
	return nil
}

func snapshots(inputs []map[string]any, reads *sourceReads) ([]applications.JobSnapshot, *Error) {
	if len(inputs) > applications.MaxRequestedIDs {
		return nil, &Error{Stage: "input", Code: "APPLICATION_LEDGER_INPUT_LIMIT"}
	}
	jobs := make([]applications.JobSnapshot, 0, len(inputs))
	total := 0
	for _, job := range inputs {
		if reads.ctx != nil && reads.ctx.Err() != nil {
			return nil, canceled()
		}
		id, ok := job["id"].(string)
		if !ok {
			return nil, invalid("job.id must be a non-empty string")
		}
		if e := boundedText(id, applications.MaxIDBytes); e != nil {
			return nil, e
		}
		total += len(id) // Charge duplicates and whitespace before normalization.
		id = trim(id)
		if id == "" {
			return nil, invalid("job.id must be a non-empty string")
		}
		runes := []rune(id)
		// PureWindowsPath.name semantics, including non-ASCII drive letters.
		// This is a DB identifier, NOT a filename: '..', CON and inner ':' are
		// valid source IDs and must not be passed through filename sanitization.
		if id == "." || strings.ContainsAny(id, "/\\\x00") || len(runes) > 1 && runes[1] == ':' {
			return nil, invalid("job.id must be a plain identifier")
		}
		values := [4]string{}
		for i, key := range []string{"company", "title", "source_url", "content_fingerprint"} {
			value, exists := job[key]
			if !exists {
				value = ""
			}
			text, ok := value.(string)
			if !ok {
				return nil, invalid("job." + key + " must be a string")
			}
			if e := boundedText(text, applications.MaxCellBytes); e != nil {
				return nil, e
			}
			total += len(text)
			values[i] = trim(text)
		}
		raw, _ := job["raw_jd_path"].(string)
		if values[3] == "" {
			if e := boundedText(raw, applications.MaxIDBytes); e != nil {
				return nil, e
			}
		}
		if values[3] == "" && trim(raw) != "" {
			total += len(raw)
			if total > applications.MaxRequestBytes {
				return nil, &Error{Stage: "input", Code: "APPLICATION_LEDGER_INPUT_LIMIT"}
			}
			var e *Error
			values[3], e = reads.fingerprint(raw)
			if e != nil {
				return nil, e
			}
		}
		if total > applications.MaxRequestBytes {
			return nil, &Error{Stage: "input", Code: "APPLICATION_LEDGER_INPUT_LIMIT"}
		}
		jobs = append(jobs, applications.JobSnapshot{JobID: id, Company: values[0], Title: values[1],
			SourceURL: values[2], ContentFingerprint: values[3]})
	}
	return jobs, nil
}

// revisionInput admits the optional expected_revision guard: absent (nil)
// means unguarded; otherwise it must be a positive integer, given as a Go
// integer or an integral json.Number. Strings, fractions and zero are invalid.
func revisionInput(value any) (*int64, *Error) {
	if value == nil {
		return nil, nil
	}
	var revision int64
	switch n := value.(type) {
	case int:
		revision = int64(n)
	case int64:
		revision = n
	case json.Number:
		parsed, err := strconv.ParseInt(string(n), 10, 64)
		if err != nil {
			return nil, invalid("expected_revision must be a positive integer")
		}
		revision = parsed
	default:
		return nil, invalid("expected_revision must be a positive integer")
	}
	if revision < 1 {
		return nil, invalid("expected_revision must be a positive integer")
	}
	return &revision, nil
}

func notesInput(value any, nullable bool) (*string, *Error) {
	if value == nil && nullable {
		return nil, nil
	}
	text, ok := value.(string)
	if !ok {
		if nullable {
			return nil, invalid("notes must be a string")
		}
		return nil, invalid("note must be a string")
	}
	if e := boundedText(text, applications.MaxRequestBytes); e != nil {
		return nil, e
	}
	return &text, nil
}
