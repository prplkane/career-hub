package applicationwrite

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"path/filepath"
	"strings"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/intakeextract"
	"careerhub.local/backend/internal/intakeread"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sqlitestore"
)

const intakeOperationTimeout = 60 * time.Second
const intakeCandidateLimit = 8

func secureIntakeID() (string, error) {
	var raw [16]byte
	if _, e := rand.Read(raw[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(raw[:]), nil
}

type intakeCandidate struct {
	operationID, rawPath, sourceID, reportPath, diagnosticPath string
}

func (s *Service) chooseIntakeCandidate(root *runtimepaths.Root, id, suffix, sha string) (intakeCandidate, *Error) {
	for attempt := 0; attempt < intakeCandidateLimit; attempt++ {
		operation, e := s.newIntakeID()
		if e != nil || !intakeID(operation) {
			return intakeCandidate{}, failed("identity", "INTAKE_ENTROPY_UNAVAILABLE")
		}
		stem := "resume_" + digestBytes([]byte(id))[:24] + "_digest_" + operation
		rawPath := "career_vault/raw/" + stem + suffix
		sourceID := "src_" + intakeSlug(stem) + "_" + sha[:10]
		candidate := intakeCandidate{operation, rawPath, sourceID,
			"career_vault/intake_runs/" + sourceID + "/extracted.yaml",
			"career_vault/logs/native-intake/" + operation + ".json"}
		collision := false
		for _, name := range []string{candidate.rawPath, path.Dir(candidate.reportPath), candidate.reportPath, candidate.diagnosticPath} {
			present, pe := root.Exists(name)
			if pe != nil {
				return intakeCandidate{}, failed("path", pe.Code)
			}
			collision = collision || present
		}
		if !collision {
			return candidate, nil
		}
	}
	return intakeCandidate{}, failed("identity", "INTAKE_IDENTITY_COLLISION")
}

// DigestResumeVersion is a host-only file writer. The renderer never supplies
// dataRoot/resourceRoot; no HTTP route or installed app calls it yet. Unlike
// runMode's SQL-mutation branch, this dedicated owner always rolls its IMMEDIATE
// transaction back after the report-last filesystem publication.
func (s *Service) DigestResumeVersion(ctx context.Context, resourceRoot, resumeID string) (out *Outcome, problem *Error) {
	out = &Outcome{}
	if s == nil || ctx == nil || s.now == nil || s.checkpoint == nil || s.rollback == nil ||
		s.closeDB == nil || s.closeRoot == nil || s.newIntakeID == nil || s.resumeEntropy == nil {
		return out, failed("configuration", "INTAKE_WRITER_UNAVAILABLE")
	}
	if !intakeResumeID(resumeID) {
		return out, failed("input", "INTAKE_RESUME_ID_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, intakeOperationTimeout)
	defer cancel()
	release, e := operationgate.Acquire(ctx)
	if e != nil {
		return out, failed("gate", "INTAKE_CANCELED")
	}
	defer release()
	root, pe := runtimepaths.Open(s.root)
	if pe != nil {
		return out, failed("path", pe.Code)
	}
	var resources *runtimepaths.Root
	var files *runtimepaths.SQLiteFiles
	var db *sql.DB
	var conn *sql.Conn
	var tx *sql.Tx
	var home *datahome.Info
	rolled := false
	cleanup := func(code string) {
		out.CleanupFailed = true
		if out.CleanupCode == "" {
			out.CleanupCode = code
		}
		if problem == nil {
			problem = failed("cleanup", code)
		}
	}
	defer func() {
		if tx != nil && !rolled {
			if s.rollback(tx) != nil {
				cleanup("INTAKE_ROLLBACK_FAILED")
			}
		}
		if conn != nil {
			if e := conn.Close(); e != nil && !errors.Is(e, sql.ErrConnDone) {
				cleanup("INTAKE_CONNECTION_CLOSE_FAILED")
			}
		}
		if db != nil && s.closeDB(db) != nil {
			cleanup("INTAKE_DATABASE_CLOSE_FAILED")
		}
		if home != nil && problem == nil {
			if _, e := s.ready(root, home); e != nil {
				problem = e
			}
		}
		if files != nil && files.Close() != nil {
			cleanup("INTAKE_SQLITE_FILES_CLOSE_FAILED")
		}
		if resources != nil && s.closeRoot(resources) != nil {
			cleanup("INTAKE_RESOURCES_CLOSE_FAILED")
		}
		if s.closeRoot(root) != nil {
			cleanup("INTAKE_ROOT_CLOSE_FAILED")
		}
		if problem != nil {
			out.Result = nil
		}
	}()
	home, problem = s.ready(root, nil)
	if problem != nil {
		return out, problem
	}
	resources, pe = runtimepaths.Open(resourceRoot)
	if pe != nil {
		return out, failed("resources", pe.Code)
	}
	files, pe = root.GuardSQLiteFiles(databasePath)
	if pe != nil {
		return out, failed("path", pe.Code)
	}
	if files == nil {
		return out, failed("schema", "SCHEMA_PREPARATION_REQUIRED")
	}
	now := s.now()
	source, problem := s.readIntakeSource(ctx, root, resumeID)
	if problem != nil {
		return out, problem
	}
	if problem = s.checkpoint("intake_source_snapshot"); problem != nil {
		return out, problem
	}
	catalog, problem := readIntakeCatalog(ctx, resources, root)
	if problem != nil {
		return out, problem
	}
	if problem = s.checkpoint("intake_catalog_snapshot"); problem != nil {
		return out, problem
	}
	if pe = files.Check(); pe != nil {
		return out, failed("path", pe.Code)
	}
	suffix := strings.ToLower(path.Ext(source.identity.FileName))
	candidate, problem := s.chooseIntakeCandidate(root, resumeID, suffix, source.identity.SHA256)
	if problem != nil {
		return out, problem
	}
	ref := intakeextract.Source{ID: candidate.sourceID, Path: candidate.rawPath,
		FileType: strings.TrimPrefix(suffix, "."), SHA256: source.identity.SHA256}
	report, extractionError := intakeextract.Extract(ctx, source.content, ref, catalog.revision, catalog.terms)
	if extractionError != nil {
		if errors.Is(extractionError, intakeextract.ErrLimit) {
			return out, failed("extraction", "INTAKE_EXTRACTION_LIMIT")
		}
		if ctx.Err() != nil {
			return out, failed("context", "INTAKE_CANCELED")
		}
		return out, failed("extraction", "INTAKE_EXTRACTION_UNAVAILABLE")
	}
	reportBytes, marshalError := json.Marshal(report)
	if marshalError != nil || len(reportBytes) == 0 || len(reportBytes) > intakeread.MaxExtractionBytes {
		return out, failed("report", "INTAKE_REPORT_LIMIT")
	}
	parsed, parseError := intakeread.ParseExtraction(reportBytes)
	if parseError != nil || parsed.Source.ID != ref.ID || parsed.Source.Path != ref.Path ||
		parsed.Source.SHA256 != ref.SHA256 || parsed.RegistryRevision != catalog.revision {
		return out, failed("report", "INTAKE_REPORT_INVALID")
	}
	provisional, _ := json.Marshal(intakeDiagnostic{SchemaVersion: 1, OperationID: candidate.operationID,
		SourceID: candidate.sourceID, State: "publishing", Stage: "publication"})
	terminal, _ := json.Marshal(intakeDiagnostic{SchemaVersion: 1, OperationID: candidate.operationID,
		SourceID: candidate.sourceID, State: "success", Stage: "completed"})
	p := intakeIntent{Version: 1, Home: *home, OperationID: candidate.operationID,
		Resume: source.identity, Catalog: catalog.witnesses, RegistryRevision: catalog.revision,
		RawPath: candidate.rawPath, SourceID: candidate.sourceID, ReportPath: candidate.reportPath,
		DiagnosticPath: candidate.diagnosticPath, Report: reportBytes, ReportSHA256: digestBytes(reportBytes),
		ProvisionalDiagnostic: provisional, TerminalDiagnostic: terminal}
	if _, problem = encodeIntakeIntent(p); problem != nil {
		return out, problem
	}
	result, marshalError := json.Marshal(struct {
		ResumeID        string `json:"resume_id"`
		SourceID        string `json:"source_id"`
		TechnologyCount int    `json:"technology_count"`
		BulletCount     int    `json:"bullet_count"`
	}{resumeID, candidate.sourceID, len(report.TechnologyIDs), len(report.BulletCandidates)})
	if marshalError != nil {
		return out, failed("result", "INTAKE_RESPONSE_INVALID")
	}
	out.IntakeSourceID = candidate.sourceID
	if problem = s.checkpoint("intake_report_prepared"); problem != nil {
		return out, problem
	}
	if _, problem = s.ready(root, home); problem != nil {
		return out, problem
	}
	if pe = files.Check(); pe != nil {
		return out, failed("path", pe.Code)
	}
	db, e = sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(s.root, filepath.FromSlash(databasePath)))
	if e != nil {
		return out, failed("open", "INTAKE_DATABASE_UNAVAILABLE")
	}
	conn, e = db.Conn(ctx)
	if e != nil {
		return out, failed("open", "INTAKE_DATABASE_UNAVAILABLE")
	}
	tx, e = conn.BeginTx(context.WithoutCancel(ctx), nil)
	if e != nil {
		return out, failed("transaction", "INTAKE_DATABASE_UNAVAILABLE")
	}
	if problem = schemaCurrent(ctx, tx); problem != nil {
		return out, problem
	}
	if problem = s.checkpoint("intake_reserved"); problem != nil {
		return out, problem
	}
	if pe = files.Check(); pe != nil {
		return out, failed("path", pe.Code)
	}
	if _, problem = s.ready(root, home); problem != nil {
		return out, problem
	}
	witnesses := func(ctx context.Context) *Error {
		if pe := files.Check(); pe != nil {
			return failed("path", pe.Code)
		}
		admitted, e := datahome.InspectOwnedForIntakeRecovery(root, nil)
		if e != nil || admitted == nil || *admitted != *home {
			return failed("admission", "INTAKE_HOME_CHANGED")
		}
		current, match, liveProblem := intakeLiveState(ctx, tx, root, resources, p, now, s.resumeEntropy)
		if liveProblem != nil {
			return liveProblem
		}
		if !match || !bytes.Equal(current, source.content) {
			return failed("witness", "INTAKE_WITNESS_CHANGED")
		}
		return nil
	}
	if problem = witnesses(ctx); problem != nil {
		return out, problem
	}
	facts, publicationError := publishFrozenIntake(ctx, root, p, source.content,
		intakePublicationChecks{witnesses: witnesses, checkpoint: s.checkpoint, mutations: s.intakeMutations})
	out.RecoveryRequired = facts.recoveryRequired()
	out.IntakeReportPublished = facts.ReportPublished && intakeFileEquals(root, p.ReportPath, p.Report)
	// A false+error create/remove result may still leave a fully inspectable
	// state. Absence of the exact journal under process ownership means there
	// is nothing the recovery owner can replay. Keep the response unsuccessful,
	// but do not falsely direct startup into recovery or invite a blind digest.
	if publicationError != nil && out.RecoveryRequired {
		if present, observed := root.Exists(datahome.IntakeRecoveryPath); observed == nil && !present {
			out.RecoveryRequired = false
		}
	}
	if out.RecoveryRequired {
		out.IntakeState = "recovery_required"
	} else if out.IntakeReportPublished {
		out.IntakeState = "published"
		if publicationError != nil {
			out.IntakeState = "published_unconfirmed"
		}
	}
	if publicationError != nil {
		return out, publicationError
	}
	if !out.IntakeReportPublished || out.RecoveryRequired {
		return out, failed("publication", "INTAKE_PUBLICATION_UNCONFIRMED")
	}
	rolled = true
	if s.rollback(tx) != nil {
		cleanup("INTAKE_ROLLBACK_FAILED")
		return out, problem
	}
	if pe = files.Check(); pe != nil {
		return out, failed("path", pe.Code)
	}
	if _, problem = s.ready(root, home); problem != nil {
		return out, problem
	}
	out.Result = result
	return out, nil
}
