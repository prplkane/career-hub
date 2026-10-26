package resumelibrary

import (
	"strings"

	"careerhub.local/backend/internal/textcompat"
)

func (e *engine) upload(r Request) (any, *Error) {
	f := r.Fields
	job := ""
	if r.Operation == "attach" {
		var p *Error
		job, p = jobID(f)
		if p != nil {
			return nil, p
		}
		if _, p = e.job(job); p != nil {
			return nil, p
		}
	}
	sourceID, sourceHash := "", ""
	if r.Operation == "approved" {
		var ok bool
		sourceID, ok = f["source_id"].(string)
		if !ok || !strings.HasPrefix(sourceID, "src_") || strings.ContainsAny(sourceID, "/\\") {
			return nil, invalid("approved import source id is invalid")
		}
		sourceHash, ok = f["source_sha256"].(string)
		if !ok || !validHash(sourceHash) {
			return nil, invalid("approved import source hash is invalid")
		}
		if digest(r.Content) != sourceHash {
			return nil, invalid("approved import bytes do not match its source hash")
		}
	}
	display := value(f, "display_name", "")
	if r.Operation == "attach" {
		display = value(f, "label", "")
	}
	u, p := NormalizeUpload(f["file_name"], r.Content, display)
	if p != nil {
		return nil, p
	}
	random, p := e.uuid()
	if p != nil {
		return nil, p
	}
	id := "resume_" + random
	if r.Operation == "approved" {
		row, p := e.one("SELECT "+textColumns("source_sha256", "resume_id")+" FROM approved_import_resume_versions WHERE source_id=?", sourceID)
		if p != nil {
			return nil, p
		}
		if row != nil {
			if row[0] != sourceHash {
				return nil, invalid("approved import source changed after it was saved")
			}
			key, ok := row[1].(string)
			if !ok {
				return nil, unsupported("DATABASE_VALUE_UNSUPPORTED")
			}
			v, p := e.version(key)
			if p != nil {
				return nil, p
			}
			if v == nil {
				return nil, invalid("approved import is missing its stored Resume Version")
			}
			if v.archived != nil {
				return nil, problem("ResumeVersionArchived", "approved import is already saved but archived; restore it before using it")
			}
			path, p := v.canonicalPath()
			if p != nil {
				return nil, p
			}
			raw, p := e.readVersion(path, v, "approved")
			if p != nil {
				return nil, p
			}
			if p = verifyVersion(raw, v); p != nil {
				return nil, p
			}
			return map[string]any{"resume": v.summary(), "reused": true}, nil
		}
	}
	path := LibraryPath + "/" + id + u.Suffix
	// No overwrite and no speculative cleanup: even a faulty entropy source or
	// old metadata collision must leave the existing immutable version untouched.
	old, p := e.one("SELECT 1 FROM resume_library WHERE resume_id=? OR relative_path=? LIMIT 1", id, path)
	if p != nil {
		return nil, p
	}
	if old != nil {
		return nil, unsupported("IDENTITY_COLLISION")
	}
	_, present, pe := e.reader.ReadFile(path, MaxResumeBytes)
	if pe != nil {
		return nil, unsupported("FILE_UNAVAILABLE")
	}
	if present {
		return nil, unsupported("IDENTITY_COLLISION")
	}
	hash := digest(r.Content)
	if e.observe != nil {
		if p = e.observe(id); p != nil {
			return nil, p
		}
	}
	if p = e.exec("INSERT INTO resume_library (resume_id,display_name,relative_path,file_name,sha256,size_bytes,added_at,origin) VALUES (?,?,?,?,?,?,?,'uploaded')", id, u.DisplayName, path, u.FileName, hash, len(r.Content), e.clock); p != nil {
		return nil, p
	}
	if r.Operation == "approved" {
		if p = e.exec("INSERT INTO approved_import_resume_versions (source_id,source_sha256,resume_id,created_at) VALUES (?,?,?,?)", sourceID, sourceHash, id, e.clock); p != nil {
			return nil, p
		}
	}
	v, p := e.version(id)
	if p != nil {
		return nil, p
	}
	if v == nil {
		return nil, unsupported("RESPONSE_UNSUPPORTED")
	}
	e.file = &FileChange{ResumeID: id, RelativePath: path, AfterPresent: true, After: append([]byte{}, r.Content...)}
	if r.Operation == "attach" {
		// One transaction, unlike the source's separately committed upload/select.
		return e.reference(Request{Operation: "select", Fields: map[string]any{"job_id": job, "resume_id": id}})
	}
	if r.Operation == "approved" {
		return map[string]any{"resume": v.summary(), "reused": false}, nil
	}
	return v.summary(), nil
}

func (e *engine) archive(f map[string]any) (any, *Error) {
	id, p := resumeID(f["resume_id"], false)
	if p != nil {
		return nil, p
	}
	flag, ok := f["archived"].(bool)
	if !ok {
		return nil, invalid("archived must be a boolean")
	}
	v, p := e.version(id)
	if p != nil {
		return nil, p
	}
	if v == nil {
		return nil, problem("ResumeNotFound", "Resume version is not stored: "+id)
	}
	if flag != (v.archived != nil) {
		var stamp any
		if flag {
			stamp = e.clock
		}
		if p = e.exec("UPDATE resume_library SET archived_at=? WHERE resume_id=?", stamp, id); p != nil {
			return nil, p
		}
		v, p = e.version(id)
		if p != nil {
			return nil, p
		}
		if v == nil {
			return nil, unsupported("RESPONSE_UNSUPPORTED")
		}
	}
	out := v.summary()
	out["archived"] = v.archived != nil
	return out, nil
}

func (e *engine) delete(input any) (any, *Error) {
	id, p := resumeID(input, true)
	if p != nil {
		return nil, p
	}
	v, p := e.version(id)
	if p != nil {
		return nil, p
	}
	if v == nil {
		return nil, problem("ResumeNotFound", "Resume Version is not stored")
	}
	for _, query := range []string{
		"SELECT 1 FROM application_resume_attachments WHERE resume_id=? LIMIT 1",
		"SELECT 1 FROM job_applications WHERE applied_resume_id=? LIMIT 1",
		"SELECT 1 FROM application_applied_resume_events WHERE resume_id=? LIMIT 1",
	} {
		row, p := e.one(query, id)
		if p != nil {
			return nil, p
		}
		if row != nil {
			return nil, problem("ResumeVersionInUse", "Resume Version is used by application history and cannot be deleted")
		}
	}
	path, p := v.canonicalPath()
	if p != nil {
		return nil, p
	}
	raw, p := e.readVersion(path, v, "delete")
	if p != nil {
		return nil, p
	}
	// Source allocates a quarantine name here. Preserve allocation order without
	// making its unsafe rename/unlink-before-commit filesystem changes.
	if _, p = e.uuid(); p != nil {
		return nil, p
	}
	if p = verifyVersion(raw, v); p != nil {
		return nil, p
	}
	if e.observe != nil {
		if p = e.observe(id); p != nil {
			return nil, p
		}
	}
	if p = e.exec("DELETE FROM resume_library WHERE resume_id=?", id); p != nil {
		return nil, p
	}
	remaining, p := e.version(id)
	if p != nil {
		return nil, p
	}
	if remaining != nil {
		return nil, invalid("Resume Version row was not deleted")
	}
	e.file = &FileChange{ResumeID: id, RelativePath: path, BeforePresent: true, Before: append([]byte{}, raw...)}
	out := v.summary()
	out["deleted"] = true
	return out, nil
}

// Metadata filenames do not become output paths. Only the canonical immutable
// ID and a validated suffix address bytes; native confinement is still required.
func (v *version) canonicalPath() (string, *Error) {
	if !plainName(v.id) || v.id == ".." || !supportedSuffix(suffix(v.fileName)) {
		return "", problem("ResumeVersionFileInvalid", "Resume Version file metadata is invalid")
	}
	want := LibraryPath + "/" + v.id + suffix(v.fileName)
	normalized, p := storedPath(v.path)
	if p != nil || normalized != want {
		return "", problem("ResumeVersionFileInvalid", "Resume Version file path is invalid")
	}
	return normalized, nil
}
func storedPath(raw string) (string, *Error) {
	if raw == "" {
		return "", invalid("Stored resume path is invalid")
	}
	parts := strings.Split(strings.ReplaceAll(raw, "\\", "/"), "/")
	if parts[0] == "" || strings.ContainsRune(parts[0], ':') {
		return "", invalid("Stored resume path must remain inside applications/resumes")
	}
	normalized := []string{}
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." || strings.ContainsRune(part, '\x00') {
			return "", invalid("Stored resume path must remain inside applications/resumes")
		}
		normalized = append(normalized, part)
	}
	path := strings.Join(normalized, "/")
	if !strings.HasPrefix(path, LibraryPath+"/") {
		return "", invalid("Stored resume path must remain inside applications/resumes")
	}
	return path, nil
}
func verifyVersion(raw []byte, v *version) *Error {
	hash := textcompat.Fold(v.hash)
	if v.size < 1 || v.size > MaxResumeBytes || !validHash(hash) {
		return problem("ResumeVersionFileInvalid", "Resume Version file metadata is invalid")
	}
	if int64(len(raw)) != v.size || digest(raw) != hash {
		return problem("ResumeVersionFileInvalid", "Resume Version file no longer matches its stored metadata")
	}
	return nil
}
func (e *engine) readVersion(path string, v *version, mode string) ([]byte, *Error) {
	raw, present, pe := e.reader.ReadFile(path, MaxResumeBytes)
	if pe != nil {
		if pe.Code == "PATH_NOT_REGULAR" {
			if mode == "approved" {
				return nil, problem("ResumeVersionFileInvalid", "approved import stored Resume Version is invalid")
			}
			if mode == "delete" {
				return nil, problem("ResumeVersionFileInvalid", "Resume Version file is not a regular file")
			}
			return nil, problem("ResumeVersionFileMissing", "Resume Version file is missing")
		}
		return nil, unsupported("FILE_UNAVAILABLE")
	}
	if !present {
		message := "Resume Version file is missing"
		if mode == "approved" {
			message = "approved import is missing its stored Resume Version file"
		}
		return nil, problem("ResumeVersionFileMissing", message)
	}
	return raw, nil
}

func (e *engine) versionFile(f map[string]any) (any, *Error) {
	id, p := resumeID(f["resume_id"], false)
	if p != nil {
		return nil, p
	}
	v, p := e.version(id)
	if p != nil {
		return nil, p
	}
	if v == nil {
		return nil, problem("ResumeNotFound", "Resume version is not stored: "+id)
	}
	flag, ok := value(f, "require_active", true).(bool)
	// Do not silently reinterpret arbitrary truthy JSON as an access policy.
	if !ok {
		return nil, unsupported("ACTIVE_POLICY_UNSUPPORTED")
	}
	if flag && v.archived != nil {
		return nil, problem("ResumeVersionArchived", "Archived Resume Version must be restored before it can be digested")
	}
	path, p := v.canonicalPath()
	if p != nil {
		return nil, p
	}
	raw, p := e.readVersion(path, v, "version_file")
	if p != nil {
		return nil, p
	}
	if p = verifyVersion(raw, v); p != nil {
		return nil, p
	}
	e.content = append([]byte{}, raw...)
	e.isFile = true
	return map[string]any{"id": v.id, "display_name": v.name, "file_name": v.fileName, "sha256": v.hash, "size_bytes": v.size, "added_at": v.added, "archived": v.archived != nil}, nil
}
