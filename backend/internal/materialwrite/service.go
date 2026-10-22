// Package materialwrite owns material edits, review records and immutable exports
// with a coherent application/Resume Plan snapshot. It never commits SQL mutations.
package materialwrite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"time"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/materialintegrity"
	"careerhub.local/backend/internal/materials"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sqlitestore"
)

type Error struct{ Stage, Code, SourceClass string }

func (*Error) Error() string                { return "Application material operation could not be completed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func failed(stage, code string) *Error      { return &Error{stage, code, "ApplicationMaterialError"} }
func canceled() *Error                      { return failed("context", "MATERIAL_CANCELED") }

type Outcome struct {
	Result                   json.RawMessage
	Content                  []byte
	Committed, CleanupFailed bool
	// Partial identifies an observed/prepared export without a completion receipt.
	// It does not mean an existing export was removed or rolled back.
	Partial bool
}

func (Outcome) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "[redacted material outcome]") }

type Service struct {
	root       string
	now        func() time.Time
	checkpoint func(string) *Error
	read       func(*runtimepaths.Root, string, int64) ([]byte, bool, *runtimepaths.Error)
	create     func(*runtimepaths.Root, string, []byte) (bool, *runtimepaths.Error)
	replace    func(*runtimepaths.Root, string, []byte, []byte) (bool, *runtimepaths.Error)
	rollback   func(*sql.Tx) error
	closeDB    func(*sql.DB) error
	closeRoot  func(*runtimepaths.Root) error
}

func New(root string) *Service {
	return &Service{root: root, now: time.Now, checkpoint: func(string) *Error { return nil }, read: (*runtimepaths.Root).ReadSingleLinkFile, create: (*runtimepaths.Root).CreateFile, replace: (*runtimepaths.Root).ReplaceFile, rollback: (*sql.Tx).Rollback, closeDB: (*sql.DB).Close, closeRoot: (*runtimepaths.Root).Close}
}

type observation struct {
	raw     []byte
	present bool
	limit   int64
}
type reads struct {
	s     *Service
	root  *runtimepaths.Root
	ctx   context.Context
	files map[string]observation
	bytes int
}

func (r *reads) Check(path string) *runtimepaths.Error {
	if r.ctx.Err() != nil {
		return &runtimepaths.Error{Code: "READ_CANCELED"}
	}
	return r.root.Check(path)
}

func (r *reads) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	if r.ctx.Err() != nil {
		return nil, false, &runtimepaths.Error{Code: "READ_CANCELED"}
	}
	if limit < 1 || limit > 4<<20 {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	if o, ok := r.files[path]; ok {
		if int64(len(o.raw)) > limit {
			return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
		}
		return bytes.Clone(o.raw), o.present, nil
	}
	raw, present, e := r.s.read(r.root, path, limit)
	if e != nil {
		return nil, false, e
	}
	if len(r.files) >= 32 || len(raw) > (32<<20)-r.bytes {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	r.files[path] = observation{bytes.Clone(raw), present, limit}
	r.bytes += len(raw)
	return raw, present, nil
}
func (r *reads) unchanged() *Error {
	paths := make([]string, 0, len(r.files))
	for p := range r.files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		o := r.files[p]
		raw, present, e := r.s.read(r.root, p, o.limit)
		if e != nil || present != o.present || !bytes.Equal(raw, o.raw) {
			return failed("revision", "MATERIAL_SOURCE_CHANGED")
		}
	}
	return nil
}
func (s *Service) ready(root *runtimepaths.Root, expected *datahome.Info) (*datahome.Info, *Error) {
	if e := root.RequireProcessOwnership(); e != nil {
		return nil, failed("admission", "DATA_HOME_OWNERSHIP_REQUIRED")
	}
	home, e := datahome.Inspect(s.root, nil)
	if e != nil {
		return nil, failed("admission", e.Code)
	}
	if expected != nil && *home != *expected {
		return nil, failed("admission", "MATERIAL_HOME_CHANGED")
	}
	return home, nil
}
func current(ctx context.Context, tx *sql.Tx) *Error {
	var version int
	if e := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); e != nil {
		return failed("schema", "MATERIAL_DATABASE_QUERY_FAILED")
	}
	if version < applications.SchemaVersion {
		return failed("schema", "SCHEMA_PREPARATION_REQUIRED")
	}
	if version > applications.SchemaVersion {
		return failed("schema", "SCHEMA_TOO_NEW")
	}
	return nil
}

// Run borrows an initialized, claimed Data Home. The global gate and native path
// pins remain owned through synchronous rollback/close, including cancellation.
// Committed means the draft file was published, never that SQL was committed.
func (s *Service) Run(ctx context.Context, request materials.Request) (out *Outcome, problem *Error) {
	if e := materials.ValidateRequest(request); e != nil {
		return &Outcome{}, &Error{e.Stage, e.Code, e.SourceClass}
	}
	request.Payload = bytes.Clone(request.Payload)
	return s.run(ctx, "", func(ctx context.Context, tx *sql.Tx, data, _ *reads, clock, letterDate string) (*operationProposal, *Error) {
		proposal, e := materials.Prepare(ctx, tx, data, request, clock, letterDate)
		if e != nil {
			return nil, &Error{e.Stage, e.Code, e.SourceClass}
		}
		return &operationProposal{Proposal: proposal}, nil
	})
}

// Review uses the same single-file publication/cleanup owner as draft saving.
// Resources must be an explicit host-resolved root, not a request payload path.
func (s *Service) Review(ctx context.Context, resources string, request materialintegrity.ReviewRequest) (*Outcome, *Error) {
	if e := materialintegrity.ValidateReviewRequest(request); e != nil {
		return &Outcome{}, failed("request", e.Code)
	}
	if resources == "" {
		return &Outcome{}, failed("configuration", "MATERIAL_FACT_REGISTRY_REQUIRED")
	}
	request.TargetIDs = slices.Clone(request.TargetIDs)
	return s.run(ctx, resources, func(ctx context.Context, tx *sql.Tx, data, core *reads, clock, _ string) (*operationProposal, *Error) {
		proposal, e := materialintegrity.PrepareReview(ctx, tx, core, data, request, clock)
		if e != nil {
			return nil, failed("integrity", e.Code)
		}
		return &operationProposal{Proposal: proposal}, nil
	})
}

// A private operation can extend publication, not bypass the common admission,
// observation, SQL reservation, final readback or synchronous cleanup lifetime.
type operationProposal struct {
	*materials.Proposal
	content []byte
	publish func(*runtimepaths.Root, *reads, *Outcome, func() *Error) *Error
}

type prepareOperation func(context.Context, *sql.Tx, *reads, *reads, string, string) (*operationProposal, *Error)

func (s *Service) run(ctx context.Context, resourcesPath string, prepare prepareOperation) (out *Outcome, problem *Error) {
	out = &Outcome{}
	if s == nil || s.now == nil || s.checkpoint == nil || s.read == nil || s.create == nil || s.replace == nil || s.rollback == nil || s.closeDB == nil || s.closeRoot == nil {
		return out, failed("configuration", "MATERIAL_SERVICE_UNAVAILABLE")
	}
	if ctx == nil {
		return out, canceled()
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	release, e := operationgate.Acquire(ctx)
	if e != nil {
		return out, canceled()
	}
	defer release()
	root, pe := runtimepaths.Open(s.root)
	if pe != nil {
		return out, failed("path", pe.Code)
	}
	var db *sql.DB
	var conn *sql.Conn
	var tx *sql.Tx
	var pins *runtimepaths.SQLiteFiles
	var home *datahome.Info
	var resourceRoot *runtimepaths.Root
	var resources *reads
	cleanup := func(code string) {
		out.CleanupFailed = true
		if problem == nil {
			problem = failed("cleanup", code)
		}
	}
	defer func() {
		if tx != nil {
			if e := s.rollback(tx); e != nil {
				cleanup("MATERIAL_ROLLBACK_FAILED")
			}
		}
		if conn != nil {
			if e := conn.Close(); e != nil && !errors.Is(e, sql.ErrConnDone) {
				cleanup("MATERIAL_CONNECTION_CLOSE_FAILED")
			}
		}
		if db != nil && s.closeDB(db) != nil {
			cleanup("MATERIAL_DATABASE_CLOSE_FAILED")
		}
		if home != nil && problem == nil {
			_, problem = s.ready(root, home)
		}
		if pins != nil && pins.Close() != nil {
			cleanup("MATERIAL_FILES_CLOSE_FAILED")
		}
		if resourceRoot != nil && s.closeRoot(resourceRoot) != nil {
			cleanup("MATERIAL_RESOURCES_CLOSE_FAILED")
		}
		if s.closeRoot(root) != nil {
			cleanup("MATERIAL_ROOT_CLOSE_FAILED")
		}
		if problem != nil {
			out.Result = nil
			out.Content = nil
		}
	}()
	home, problem = s.ready(root, nil)
	if problem != nil {
		return out, problem
	}
	if ctx.Err() != nil {
		return out, canceled()
	}
	if resourcesPath != "" {
		resourceRoot, pe = runtimepaths.Open(resourcesPath)
		if pe != nil {
			return out, failed("resources", pe.Code)
		}
		resources = &reads{s, resourceRoot, ctx, map[string]observation{}, 0}
	}
	const database = "applications/career_hub.sqlite3"
	pins, pe = root.GuardSQLiteFiles(database)
	if pe != nil {
		return out, failed("path", pe.Code)
	}
	if pins == nil {
		return out, failed("schema", "SCHEMA_PREPARATION_REQUIRED")
	}
	db, e = sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(s.root, filepath.FromSlash(database)))
	if e != nil {
		return out, failed("open", "MATERIAL_DATABASE_OPEN_FAILED")
	}
	conn, e = db.Conn(ctx)
	if e != nil {
		return out, failed("open", "MATERIAL_DATABASE_OPEN_FAILED")
	}
	// IMMEDIATE reserves the existing DB against concurrent writers. Disabling
	// context auto-rollback keeps its lifetime under this synchronous owner.
	tx, e = conn.BeginTx(context.WithoutCancel(ctx), nil)
	if e != nil {
		return out, failed("transaction", "MATERIAL_DATABASE_TRANSACTION_FAILED")
	}
	if ctx.Err() != nil {
		return out, canceled()
	}
	if _, e = tx.ExecContext(ctx, "PRAGMA query_only=ON"); e != nil {
		return out, failed("transaction", "MATERIAL_DATABASE_READONLY_FAILED")
	}
	if problem = current(ctx, tx); problem != nil {
		return out, problem
	}
	reader := &reads{s, root, ctx, map[string]observation{}, 0}
	if problem = s.checkpoint("prepared"); problem != nil {
		return out, problem
	}
	now := s.now()
	proposal, problem := prepare(ctx, tx, reader, resources, now.UTC().Format("2006-01-02T15:04:05Z"), now.Format("2006-01-02"))
	if problem != nil {
		return out, problem
	}
	if problem = s.checkpoint("after_read"); problem != nil {
		return out, problem
	}
	if proposal == nil || proposal.Proposal == nil || len(proposal.Result) == 0 || !json.Valid(proposal.Result) || len(proposal.Result) > materials.MaxResponseBytes || len(proposal.content) > materialintegrity.MaxArtifactBytes {
		return out, failed("response", "MATERIAL_RESPONSE_INVALID")
	}
	if problem = s.checkpoint("before_publish"); problem != nil {
		return out, problem
	}
	guard := func() *Error {
		if ctx.Err() != nil {
			return canceled()
		}
		if e := reader.unchanged(); e != nil {
			return e
		}
		if resources != nil {
			if e := resources.unchanged(); e != nil {
				return e
			}
		}
		if _, e := s.ready(root, home); e != nil {
			return e
		}
		if e := pins.Check(); e != nil {
			return failed("path", e.Code)
		}
		return current(ctx, tx)
	}
	if problem = guard(); problem != nil {
		return out, problem
	}
	if proposal.publish != nil {
		if proposal.Path != "" {
			return out, failed("publication", "MATERIAL_PROPOSAL_INVALID")
		}
		if problem = proposal.publish(root, reader, out, guard); problem != nil {
			return out, problem
		}
	}
	if proposal.Path != "" {
		before, ok := reader.files[proposal.Path]
		if !ok || len(proposal.Bytes) == 0 {
			return out, failed("publication", "MATERIAL_PROPOSAL_INVALID")
		}
		if _, pe = root.EnsureDirectory(filepath.ToSlash(filepath.Dir(proposal.Path))); pe != nil {
			return out, failed("directory", pe.Code)
		}
		if ctx.Err() != nil {
			return out, canceled()
		}
		if before.present {
			out.Committed, pe = s.replace(root, proposal.Path, before.raw, proposal.Bytes)
		} else {
			out.Committed, pe = s.create(root, proposal.Path, proposal.Bytes)
		}
		if pe != nil {
			out.CleanupFailed = pe.Code == "FILE_CLOSE" || pe.Code == "FILE_CLEANUP"
			return out, failed("publication", pe.Code)
		}
		if !out.Committed {
			return out, failed("publication", "MATERIAL_SAVE_UNCONFIRMED")
		}
		reader.files[proposal.Path] = observation{bytes.Clone(proposal.Bytes), true, before.limit}
		if problem = s.checkpoint("after_publish"); problem != nil {
			return out, problem
		}
	}
	// A late cancellation or cleanup failure cannot retract the published file.
	// Readback is bounded and intentionally completes even after cancellation.
	if problem = reader.unchanged(); problem != nil {
		return out, problem
	}
	if resources != nil {
		if problem = resources.unchanged(); problem != nil {
			return out, problem
		}
	}
	if pe = pins.Check(); pe != nil {
		return out, failed("path", pe.Code)
	}
	if _, problem = s.ready(root, home); problem != nil {
		return out, problem
	}
	out.Result = proposal.Result
	out.Content = proposal.content
	return out, nil
}
