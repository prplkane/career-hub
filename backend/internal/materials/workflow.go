package materials

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"slices"
	"time"

	"careerhub.local/backend/internal/intake/resumeplan"
)

type Request struct {
	Kind, Operation, JobID string
	Payload                json.RawMessage `json:"Payload,omitempty"`
	ExpectedRevision       int64
	Approve                bool
}

func (Request) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "[redacted material request]") }

type Proposal struct {
	Result json.RawMessage
	Path   string
	Bytes  []byte
}

func (Proposal) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "[redacted material proposal]") }
func ValidateRequest(r Request) *Error {
	if !slices.Contains([]string{"cover_letter", "resume"}, r.Kind) || !slices.Contains([]string{"default", "load", "save"}, r.Operation) {
		return failure("request", "MATERIAL_OPERATION_INVALID")
	}
	m := model{}
	m.job(r.JobID)
	if m.err != nil {
		return failure("request", "MATERIAL_JOB_ID_INVALID")
	}
	if len(r.Payload) > MaxInputBytes || r.ExpectedRevision < 0 || r.Operation != "save" && (len(r.Payload) > 0 || r.ExpectedRevision != 0 || r.Approve) {
		return failure("request", "MATERIAL_REQUEST_INVALID")
	}
	return nil
}
func revisionConflict() *Error {
	return &Error{Stage: "revision", Code: "MATERIAL_REVISION_CONFLICT", SourceClass: "ApplicationMaterialRevisionConflict"}
}
func storePath(r Request) string {
	name := "materials.yaml"
	if r.Kind == "resume" {
		name = "resume.yaml"
	}
	return "outputs/application_materials/" + r.JobID + "/" + name
}
func storedCover(raw []byte, job string) (*CoverStore, *Error) {
	json, e := storeJSON(raw)
	if e != nil {
		return nil, e
	}
	s, e := ParseCoverStore(json)
	if e != nil {
		return nil, failure("store", "MATERIAL_STORE_INVALID")
	}
	if s.JobID != job {
		return nil, failure("store", "MATERIAL_STORE_JOB_MISMATCH")
	}
	seen := map[string]bool{}
	for _, d := range s.Materials {
		if d.JobID != job {
			return nil, failure("store", "MATERIAL_STORE_JOB_MISMATCH")
		}
		if seen[d.ID] {
			return nil, failure("store", "MATERIAL_STORE_DUPLICATE_ID")
		}
		seen[d.ID] = true
	}
	return s, nil
}
func storedResume(raw []byte, job string) (*ResumeStore, *Error) {
	json, e := storeJSON(raw)
	if e != nil {
		return nil, e
	}
	s, e := ParseResumeStore(json)
	if e != nil {
		return nil, failure("store", "MATERIAL_STORE_INVALID")
	}
	if s.JobID != job || s.Resume.JobID != job {
		return nil, failure("store", "MATERIAL_STORE_JOB_MISMATCH")
	}
	return s, nil
}

// ReadSavedDraft exposes only a persisted primary draft and its actual store
// revision. Export/review owners must separately revalidate current sources.
// Missing drafts never turn into a generated default with invented provenance.
func ReadSavedDraft(ctx context.Context, source resumeplan.Source, kind, job string) (json.RawMessage, int64, *Error) {
	r := Request{Kind: kind, JobID: job, Operation: "load"}
	if e := ValidateRequest(r); e != nil {
		return nil, 0, e
	}
	if ctx == nil || ctx.Err() != nil {
		return nil, 0, failure("context", "MATERIAL_CANCELED")
	}
	if source == nil {
		return nil, 0, failure("configuration", "MATERIAL_SOURCE_REQUIRED")
	}
	raw, present, pe := source.ReadFile(storePath(r), MaxInputBytes)
	if pe != nil {
		return nil, 0, failure("store", pe.Code)
	}
	if !present {
		return nil, 0, failure("store", "MATERIAL_DRAFT_NOT_SAVED")
	}
	if kind == "resume" {
		s, e := storedResume(raw, job)
		if e != nil {
			return nil, 0, e
		}
		payload, e := JSON(s.Resume)
		return payload, s.Revision, e
	}
	s, e := storedCover(raw, job)
	if e != nil {
		return nil, 0, e
	}
	for _, d := range s.Materials {
		if d.ID == "cover_letter_primary" {
			payload, e := JSON(d)
			return payload, s.Revision, e
		}
	}
	return nil, 0, failure("store", "MATERIAL_DRAFT_NOT_SAVED")
}

// Prepare produces the complete response and optional single-file replacement
// while the owning service retains its SQL reservation and source observations.
// SQL is read-only here: publication must not be modeled as a ledger mutation.
func Prepare(ctx context.Context, tx *sql.Tx, source resumeplan.Source, r Request, clock, letterDate string) (*Proposal, *Error) {
	if e := ValidateRequest(r); e != nil {
		return nil, e
	}
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("context", "MATERIAL_CANCELED")
	}
	if source == nil || tx == nil {
		return nil, failure("configuration", "MATERIAL_SOURCE_REQUIRED")
	}
	s, e := ReadSources(ctx, tx, source, r.JobID, r.Kind == "resume", clock)
	if e != nil {
		return nil, e
	}
	if r.Operation == "default" {
		var d any
		if r.Kind == "cover_letter" {
			d, e = DefaultCover(*s, letterDate)
		} else {
			d, e = DefaultResume(*s)
		}
		if e != nil {
			return nil, e
		}
		raw, e := JSON(d)
		return &Proposal{Result: raw}, e
	}
	path := storePath(r)
	raw, present, pe := source.ReadFile(path, MaxInputBytes)
	if pe != nil {
		return nil, failure("store", pe.Code)
	}
	var cover *CoverStore
	var resume *ResumeStore
	revision := int64(0)
	if present {
		if r.Kind == "cover_letter" {
			cover, e = storedCover(raw, r.JobID)
			if e == nil {
				revision = cover.Revision
			}
		} else {
			resume, e = storedResume(raw, r.JobID)
			if e == nil {
				revision = resume.Revision
			}
		}
		if e != nil {
			return nil, e
		}
	}
	if r.Operation == "save" && revision != r.ExpectedRevision {
		return nil, revisionConflict()
	}
	var editor *Editor
	var store any
	changed := false
	if r.Kind == "cover_letter" {
		var d *CoverDraft
		if r.Operation == "save" {
			d, e = ParseCover(r.Payload)
		} else if cover != nil {
			for _, candidate := range cover.Materials {
				if candidate.ID == "cover_letter_primary" {
					d = &candidate
					break
				}
			}
		}
		persisted := d != nil && present
		if e != nil {
			return nil, e
		}
		if d == nil {
			d, e = DefaultCover(*s, letterDate)
			if e != nil {
				return nil, e
			}
		}
		if d.JobID != r.JobID {
			return nil, failure("validation", "MATERIAL_JOB_MISMATCH")
		}
		if r.Operation == "save" {
			d.Status = "draft"
			if r.Approve {
				d.Status = "approved"
			}
		}
		editor, e = CoverEditor(ctx, *d, *s, revision, persisted, letterDate)
		if e != nil {
			return nil, e
		}
		if r.Operation == "save" {
			if e = canSave(editor.Validation, r.Approve); e != nil {
				return nil, e
			}
			items := []CoverDraft{}
			if cover != nil {
				items = append(items, cover.Materials...)
			}
			index := -1
			for i, x := range items {
				if x.ID == d.ID {
					index = i
					break
				}
			}
			changed = index < 0 || !reflect.DeepEqual(items[index], *d)
			if changed {
				if revision == math.MaxInt64 {
					return nil, failure("revision", "MATERIAL_REVISION_LIMIT")
				}
				if index < 0 {
					items = append(items, *d)
				} else {
					items[index] = *d
				}
				revision++
				store = CoverStore{1, r.JobID, revision, clock, items}
			}
			editor.Revision, editor.Persisted = revision, true
		}
	} else {
		var d *ResumeDraft
		if r.Operation == "save" {
			d, e = ParseResume(r.Payload)
		} else if resume != nil {
			d = &resume.Resume
		} else {
			d, e = DefaultResume(*s)
		}
		if e != nil {
			return nil, e
		}
		if d.JobID != r.JobID {
			return nil, failure("validation", "MATERIAL_JOB_MISMATCH")
		}
		if r.Operation == "save" {
			d.Status = "draft"
			if r.Approve {
				d.Status = "approved"
			}
		}
		editor, e = ResumeEditor(ctx, *d, *s, revision, present)
		if e != nil {
			return nil, e
		}
		if r.Operation == "save" {
			if e = canSave(editor.Validation, r.Approve); e != nil {
				return nil, e
			}
			changed = resume == nil || !reflect.DeepEqual(resume.Resume, *d)
			if changed {
				if revision == math.MaxInt64 {
					return nil, failure("revision", "MATERIAL_REVISION_LIMIT")
				}
				revision++
				store = ResumeStore{1, r.JobID, revision, clock, *d}
			}
			editor.Revision, editor.Persisted = revision, true
		}
	}
	result, e := JSON(editor)
	if e != nil {
		return nil, e
	}
	out := &Proposal{Result: result}
	if changed {
		stamp, err := time.Parse("2006-01-02T15:04:05Z", clock)
		if err != nil || stamp.Year() < 1 {
			return nil, failure("clock", "MATERIAL_CLOCK_INVALID")
		}
		raw, e := StoreYAML(ctx, store)
		if e != nil {
			return nil, e
		}
		// Re-read proposed bytes through the actual store boundary before any
		// publication, including aggregate counts and quote-sensitive scalars.
		var parsed any
		if r.Kind == "cover_letter" {
			parsed, e = storedCover(raw, r.JobID)
		} else {
			parsed, e = storedResume(raw, r.JobID)
		}
		if e != nil {
			return nil, e
		}
		got, e := JSON(parsed)
		if e != nil {
			return nil, e
		}
		want, e := JSON(store)
		if e != nil {
			return nil, e
		}
		if string(got) != string(want) {
			return nil, failure("store", "MATERIAL_STORE_ROUNDTRIP_FAILED")
		}
		out.Path, out.Bytes = path, raw
	}
	return out, nil
}
func canSave(v Validation, approve bool) *Error {
	if v.BlockingSaveCount > 0 {
		return failure("validation", "MATERIAL_SAVE_INVALID")
	}
	if approve && !v.Ready {
		return failure("validation", "MATERIAL_NOT_READY")
	}
	return nil
}
