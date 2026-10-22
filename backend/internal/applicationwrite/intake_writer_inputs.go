package applicationwrite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"

	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/resumelibrary"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/skills/resolver"
	"careerhub.local/backend/internal/sqlitestore"
)

type intakeSourceSnapshot struct {
	identity intakeResumeIdentity
	content  []byte
}

type intakeCatalogSnapshot struct {
	witnesses []intakeCatalogWitness
	revision  string
	terms     []resolver.Term
}

// The initial source snapshot uses a read-only SQLite transaction and the
// existing Resume Version reader, never the public Service.Resume endpoint.
// It releases that transaction before potentially expensive TXT/DOCX parsing.
func (s *Service) readIntakeSource(ctx context.Context, root *runtimepaths.Root, resumeID string) (snapshot intakeSourceSnapshot, problem *Error) {
	if !intakeResumeID(resumeID) || root == nil {
		return snapshot, failed("input", "INTAKE_RESUME_ID_INVALID")
	}
	db, e := sqlitestore.OpenReadOnly(ctx, filepath.Join(s.root, filepath.FromSlash(databasePath)))
	if e != nil {
		return snapshot, failed("open", "INTAKE_DATABASE_UNAVAILABLE")
	}
	var conn *sql.Conn
	var tx *sql.Tx
	defer func() {
		if tx != nil && tx.Rollback() != nil && problem == nil {
			problem = failed("cleanup", "INTAKE_READ_ROLLBACK_FAILED")
		}
		if conn != nil && conn.Close() != nil && problem == nil {
			problem = failed("cleanup", "INTAKE_READ_CONNECTION_CLOSE_FAILED")
		}
		if s.closeDB(db) != nil && problem == nil {
			problem = failed("cleanup", "INTAKE_READ_DATABASE_CLOSE_FAILED")
		}
		if problem != nil {
			snapshot = intakeSourceSnapshot{}
		}
	}()
	conn, e = db.Conn(ctx)
	if e != nil {
		return snapshot, failed("open", "INTAKE_DATABASE_UNAVAILABLE")
	}
	tx, e = conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return snapshot, failed("transaction", "INTAKE_DATABASE_UNAVAILABLE")
	}
	if problem = schemaCurrent(ctx, tx); problem != nil {
		return snapshot, problem
	}
	witness, re := resumelibrary.MutationWitness(ctx, tx, resumeID)
	if re != nil {
		return snapshot, failed("database", "INTAKE_SOURCE_UNAVAILABLE")
	}
	if !witness.Present {
		return snapshot, failed("identity", "INTAKE_RESUME_NOT_FOUND")
	}
	reader := &intakeVersionReader{root: root}
	version, re := resumelibrary.ApplyObservedInTransaction(ctx, tx, resumelibrary.Request{
		Operation: "version_file", Fields: map[string]any{"resume_id": resumeID, "require_active": true},
	}, s.now(), s.resumeEntropy, reader, nil)
	if re != nil {
		if reader.readProblem != "" {
			return snapshot, failed("source", "INTAKE_SOURCE_UNAVAILABLE")
		}
		switch re.SourceClass {
		case "ResumeNotFound":
			return snapshot, failed("identity", "INTAKE_RESUME_NOT_FOUND")
		case "ResumeVersionArchived":
			return snapshot, failed("identity", "INTAKE_RESUME_ARCHIVED")
		case "ResumeVersionFileMissing", "ResumeVersionFileInvalid":
			return snapshot, failed("source", "INTAKE_SOURCE_UNAVAILABLE")
		}
		return snapshot, failed("source", "INTAKE_SOURCE_UNAVAILABLE")
	}
	var metadata struct {
		ID       string `json:"id"`
		FileName string `json:"file_name"`
		SHA256   string `json:"sha256"`
		Size     int    `json:"size_bytes"`
		Archived bool   `json:"archived"`
	}
	if json.Unmarshal(version.Mutation.Result, &metadata) != nil || !version.IsFile || metadata.ID != resumeID ||
		metadata.Archived || !intakeFileName(metadata.FileName) || metadata.Size < 1 || metadata.Size > resumelibrary.MaxResumeBytes ||
		len(version.Bytes) != metadata.Size || !strings.EqualFold(metadata.SHA256, digestBytes(version.Bytes)) {
		return snapshot, failed("source", "INTAKE_SOURCE_UNAVAILABLE")
	}
	if ext := strings.ToLower(filepath.Ext(metadata.FileName)); ext != ".txt" && ext != ".docx" {
		return snapshot, failed("format", "INTAKE_FORMAT_UNSUPPORTED")
	}
	second, re := resumelibrary.MutationWitness(ctx, tx, resumeID)
	if re != nil || second != witness {
		return snapshot, failed("source", "INTAKE_SOURCE_CHANGED")
	}
	snapshot.identity = intakeResumeIdentity{ID: resumeID, FileName: metadata.FileName, SHA256: digestBytes(version.Bytes),
		Size: metadata.Size, Row: witness}
	snapshot.content = bytes.Clone(version.Bytes)
	return snapshot, nil
}

// Both core and custom raw inputs are observed before and after the effective
// catalog is built. The later publication reservation checks them again.
func readIntakeCatalog(ctx context.Context, resources, data *runtimepaths.Root) (snapshot intakeCatalogSnapshot, problem *Error) {
	if ctx == nil || ctx.Err() != nil || resources == nil || data == nil {
		return snapshot, failed("catalog", "INTAKE_CATALOG_UNAVAILABLE")
	}
	paths := []struct {
		name string
		root *runtimepaths.Root
	}{
		{catalogstore.CoreGroupsPath, resources},
		{catalogstore.CustomGroupsPath, data},
		{catalogstore.CoreSkillsPath, resources},
		{catalogstore.CustomSkillsPath, data},
	}
	snapshot.witnesses = make([]intakeCatalogWitness, 0, len(paths))
	for _, item := range paths {
		raw, present, e := item.root.ReadSingleLinkFile(item.name, 2<<20)
		if e != nil {
			return intakeCatalogSnapshot{}, failed("catalog", "INTAKE_CATALOG_UNAVAILABLE")
		}
		w := intakeCatalogWitness{Path: item.name, Present: present}
		if present {
			w.Size, w.SHA256 = len(raw), digestBytes(raw)
		}
		snapshot.witnesses = append(snapshot.witnesses, w)
	}
	state, e := catalogstore.ReadState(intakeCatalogReader{resources}, intakeCatalogReader{data})
	if e != nil {
		return intakeCatalogSnapshot{}, failed("catalog", "INTAKE_CATALOG_UNAVAILABLE")
	}
	snapshot.revision = state.Snapshot.CatalogRevision()
	for _, term := range state.Snapshot.Skills() {
		snapshot.terms = append(snapshot.terms, resolver.Term{ID: term.ID, Name: term.Name, Aliases: term.Aliases, Archived: term.Archived})
	}
	for i, item := range paths {
		if ctx.Err() != nil {
			return intakeCatalogSnapshot{}, failed("context", "INTAKE_CANCELED")
		}
		raw, present, e := item.root.ReadSingleLinkFile(item.name, 2<<20)
		w := snapshot.witnesses[i]
		if e != nil || present != w.Present || present && (len(raw) != w.Size || digestBytes(raw) != w.SHA256) {
			return intakeCatalogSnapshot{}, failed("catalog", "INTAKE_CATALOG_CHANGED")
		}
	}
	return snapshot, nil
}
