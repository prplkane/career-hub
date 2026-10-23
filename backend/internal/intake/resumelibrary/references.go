package resumelibrary

import (
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/isotime"
	"careerhub.local/backend/internal/textcompat"
)

func eventTime(value any, now string) (string, *Error) {
	if value == nil {
		return now, nil
	}
	s, ok := value.(string)
	if !ok || textcompat.Strip(s) == "" {
		return "", invalid("occurred_at must be a non-empty ISO date or timestamp")
	}
	if len(s) > 256 {
		return "", unsupported("TIMESTAMP_LIMIT")
	}
	s = textcompat.Strip(s)
	dateOnly := utf8.RuneCountInString(s) == 10
	if !dateOnly {
		s = strings.ReplaceAll(s, "Z", "+00:00")
	}
	t, _, err := isotime.Parse(s, dateOnly)
	if err == isotime.ErrRange {
		return "", unsupported("TIMESTAMP_RANGE")
	}
	if err != nil {
		return "", invalid("occurred_at must use ISO format, for example 2026-07-31")
	}
	return t.Format("2006-01-02T15:04:05Z"), nil
}

func (e *engine) reference(r Request) (any, *Error) {
	id, p := optionalResume(r.Fields["resume_id"])
	if p != nil {
		return nil, p
	}
	note, occurred := "", e.clock
	if r.Operation == "submitted" {
		var ok bool
		note, ok = value(r.Fields, "note", "").(string)
		if !ok {
			return nil, invalid("note must be a string")
		}
		occurred, p = eventTime(r.Fields["occurred_at"], e.clock)
		if p != nil {
			return nil, p
		}
	}
	job, p := jobID(r.Fields)
	if p != nil {
		return nil, p
	}
	j, p := e.job(job)
	if p != nil {
		return nil, p
	}
	if r.Operation == "submitted" && j[1] == int64(0) {
		return nil, invalid("Mark the application Applied before recording the submitted resume")
	}
	var v *version
	if id != nil {
		v, p = e.version(*id)
		if p != nil {
			return nil, p
		}
		if v == nil {
			return nil, invalid("selected resume does not exist")
		}
	}
	if r.Operation == "select" {
		if id == nil {
			p = e.exec("DELETE FROM application_resume_attachments WHERE job_id=?", job)
		} else {
			if v.archived != nil {
				old, p := e.one("SELECT "+textSQL("resume_id")+" FROM application_resume_attachments WHERE job_id=?", job)
				if p != nil {
					return nil, p
				}
				if old == nil || old[0] != *id {
					return nil, problem("ResumeVersionArchived", "selected Resume Version is archived; restore it before selecting")
				}
				return e.application(job)
			}
			p = e.exec("INSERT INTO application_resume_attachments (job_id,resume_id,relative_path,file_name,label,sha256,size_bytes,attached_at) VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(job_id) DO UPDATE SET resume_id=excluded.resume_id,relative_path=excluded.relative_path,file_name=excluded.file_name,label=excluded.label,sha256=excluded.sha256,size_bytes=excluded.size_bytes,attached_at=excluded.attached_at", job, v.id, v.path, v.fileName, v.name, v.hash, v.size, e.clock)
		}
	} else {
		prior, p := e.one("SELECT 1 FROM application_applied_resume_events WHERE job_id=? AND action IN ('recorded','corrected') LIMIT 1", job)
		if p != nil {
			return nil, p
		}
		action := "recorded"
		if prior != nil {
			action = "corrected"
		}
		var key any
		if id != nil {
			key = *id
		}
		p = e.exec("INSERT INTO application_applied_resume_events (job_id,resume_id,action,occurred_at,recorded_at,note) VALUES (?,?,?,?,?,?)", job, key, action, occurred, e.clock, textcompat.Strip(note))
		if p != nil {
			return nil, p
		}
	}
	if p != nil {
		return nil, p
	}
	if p = e.exec("UPDATE job_applications SET updated_at=? WHERE job_id=?", e.clock, job); p != nil {
		return nil, p
	}
	return e.application(job)
}

func (e *engine) referenceFile(r Request) (any, *Error) {
	job, p := jobID(r.Fields)
	if p != nil {
		return nil, p
	}
	if _, p = e.job(job); p != nil {
		return nil, p
	}
	var metadata map[string]any
	if r.Operation == "attachment_file" {
		row, p := e.one("SELECT "+textColumns("resume_id", "relative_path", "file_name", "label", "sha256", "attached_at")+","+intSQL("size_bytes")+" FROM application_resume_attachments WHERE job_id=?", job)
		if p != nil {
			return nil, p
		}
		if row == nil {
			return nil, problem("ApplicationNotFound", "No resume is attached to application: "+job)
		}
		metadata = map[string]any{"resume_id": row[0], "relative_path": row[1], "file_name": row[2], "label": row[3], "sha256": row[4], "attached_at": row[5], "size_bytes": row[6]}
	} else {
		row, p := e.one("SELECT "+textSQL("resume_id")+" FROM application_applied_resume_events WHERE job_id=? ORDER BY event_id DESC LIMIT 1", job)
		if p != nil {
			return nil, p
		}
		if row == nil || row[0] == nil {
			return nil, problem("ApplicationNotFound", "No submitted resume is recorded for application: "+job)
		}
		key, ok := row[0].(string)
		if !ok {
			return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
		}
		v, p := e.version(key)
		if p != nil {
			return nil, p
		}
		if v == nil {
			return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
		}
		metadata = map[string]any{"resume_id": v.id, "relative_path": v.path, "file_name": v.fileName, "label": v.name, "sha256": v.hash, "size_bytes": v.size, "attached_at": v.added}
	}
	path, ok := metadata["relative_path"].(string)
	if !ok {
		return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
	}
	path, p = storedPath(path)
	if p != nil {
		return nil, p
	}
	size, sizeOK := metadata["size_bytes"].(int64)
	hash, hashOK := metadata["sha256"].(string)
	if !sizeOK || !hashOK || size < 1 || size > MaxResumeBytes {
		return nil, unsupported("FILE_METADATA_UNSUPPORTED")
	}
	raw, present, pe := e.reader.ReadFile(path, MaxResumeBytes)
	if pe != nil || !present {
		if r.Operation == "attachment_file" && (pe == nil || pe.Code == "PATH_NOT_REGULAR") {
			return nil, invalid("Attached resume file is missing")
		}
		// Raw OS messages contain user paths. No false claim of portable message parity.
		return nil, unsupported("FILE_UNAVAILABLE")
	}
	if r.Operation == "attachment_file" {
		if int64(len(raw)) != size {
			return nil, invalid("Attached resume size no longer matches metadata")
		}
		if digest(raw) != hash {
			return nil, invalid("Attached resume hash no longer matches metadata")
		}
	} else if int64(len(raw)) != size || digest(raw) != hash {
		return nil, invalid("Submitted resume no longer matches metadata")
	}
	e.content = append([]byte{}, raw...)
	e.isFile = true
	return metadata, nil
}

func (e *engine) planRecord(job string) (map[string]any, *Error) {
	row, p := e.one("SELECT "+textColumns("plan_json", "status")+","+intSQL("revision")+","+textColumns("created_at", "updated_at")+" FROM application_resume_plans WHERE job_id=?", job)
	if p != nil || row == nil {
		return nil, p
	}
	revision, ok := row[2].(int64)
	if !ok || revision < 1 {
		return nil, unsupported("REVISION_UNSUPPORTED")
	}
	for _, i := range []int{0, 1, 3, 4} {
		if _, ok := row[i].(string); !ok {
			return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
		}
	}
	return map[string]any{"plan_json": row[0], "status": row[1], "revision": revision, "created_at": row[3], "updated_at": row[4]}, nil
}
func (e *engine) plan(r Request) (any, *Error) {
	f := r.Fields
	var plan, status string
	var expected int64
	if r.Operation == "save_plan" {
		var ok bool
		plan, ok = f["plan_json"].(string)
		if !ok || textcompat.Strip(plan) == "" {
			return nil, invalid("resume plan JSON must be a non-empty string")
		}
		if len(plan) > maxPlanBytes {
			return nil, invalid("resume plan JSON must not exceed 2 MB")
		}
		status, ok = f["status"].(string)
		if !ok || status != "draft" && status != "approved" {
			return nil, invalid("resume plan status must be draft or approved")
		}
		expected, ok = integer(f["expected_revision"])
		if !ok || expected < 0 {
			return nil, invalid("expected resume plan revision must be a non-negative integer")
		}
	}
	job, p := jobID(f)
	if p != nil {
		return nil, p
	}
	j, p := e.job(job)
	if p != nil {
		return nil, p
	}
	if r.Operation == "save_plan" && !slices.Contains([]string{"captured", "reviewed", "preparing", "applied", "interview", "offer", "rejected", "withdrawn", "closed"}, j[0].(string)) {
		return nil, invalid("a resume plan can be saved only for a persisted application record")
	}
	existing, p := e.planRecord(job)
	if p != nil {
		return nil, p
	}
	if r.Operation == "get_plan" {
		return existing, nil
	}
	actual := int64(0)
	created := e.clock
	if existing != nil {
		actual = existing["revision"].(int64)
		created = existing["created_at"].(string)
	}
	if expected != actual {
		return nil, problem("ApplicationRevisionConflict", "resume plan changed after this editor loaded; reload before saving")
	}
	if actual == math.MaxInt64 {
		return nil, unsupported("REVISION_OVERFLOW")
	}
	if p = e.exec("INSERT INTO application_resume_plans (job_id,plan_json,status,revision,created_at,updated_at) VALUES (?,?,?,?,?,?) ON CONFLICT(job_id) DO UPDATE SET plan_json=excluded.plan_json,status=excluded.status,revision=excluded.revision,updated_at=excluded.updated_at", job, plan, status, actual+1, created, e.clock); p != nil {
		return nil, p
	}
	if p = e.exec("UPDATE job_applications SET updated_at=? WHERE job_id=?", e.clock, job); p != nil {
		return nil, p
	}
	out, p := e.planRecord(job)
	if p != nil {
		return nil, p
	}
	if out == nil {
		return nil, invalid("saved resume plan could not be reloaded")
	}
	return out, nil
}
