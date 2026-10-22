package readonly

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sort"
	"time"

	"careerhub.local/backend/internal/agentcontext"
	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sqlitestore"
)

// AgentContext owns an admitted saved-job read. It does not implement analyze's
// incoming-JD normalization or prepare's ledger mutations. Host supplies profile
// as a public label; paths and channel are bound to this Workspace, never a DTO.
// No HTTP/client capability is enabled merely by adding this internal method.
func (w *Workspace) AgentContext(ctx context.Context, request agentcontext.SavedPacketRequest) (json.RawMessage, error) {
	return w.agentContext(ctx, request, nil)
}

// AgentPreparedContext reads after the host's successful preparation write. It
// never performs or replays that write. The host retains stage/commit facts if
// this separate, bound read fails. Public context routes cannot supply binding.
func (w *Workspace) AgentPreparedContext(ctx context.Context, request agentcontext.SavedPacketRequest) (json.RawMessage, error) {
	return w.agentPreparedContext(ctx, request, nil)
}

func (w *Workspace) agentPreparedContext(ctx context.Context, request agentcontext.SavedPacketRequest, faults *agentContextFaults) (json.RawMessage, error) {
	if e := agentcontext.ValidateSavedPacketRequest(request); e != nil {
		return nil, fail(e.Code)
	}
	if request.Operation != "prepare" {
		return nil, fail("AGENT_CONTEXT_PREPARATION_BINDING_REQUIRED")
	}
	return w.readAgentPacket(ctx, request.Channel, func(ctx context.Context, tx *sql.Tx, data, resources *agentPacketSource, digest *agentDigestObservation) ([]byte, *agentcontext.Error) {
		return agentcontext.PrepareSavedPacket(ctx, tx, data, resources, request, time.Now().UTC(), digest.Read)
	}, faults)
}

type agentContextFaults struct {
	checkpoint func(string)
	rollback   func(*sql.Tx) error
	closeDB    func(*sql.DB) error
	closeRuns  func(*agentDigestObservation) error
}

func (w *Workspace) agentContext(ctx context.Context, request agentcontext.SavedPacketRequest, faults *agentContextFaults) (json.RawMessage, error) {
	if e := agentcontext.ValidateSavedPacketRequest(request); e != nil {
		return nil, fail(e.Code)
	}
	if request.Operation != "context" || len(request.WriteReceipt) != 0 {
		return nil, fail("AGENT_CONTEXT_READ_INTENT_REQUIRED")
	}
	// Python's saved context command labels the assessment purpose analyze.
	// exact-auto exposes vocabulary context only; this owner still never writes.
	if request.Purpose == "assessment" {
		request.Operation = "analyze"
	}
	return w.readAgentPacket(ctx, request.Channel, func(ctx context.Context, tx *sql.Tx, data, resources *agentPacketSource, digest *agentDigestObservation) ([]byte, *agentcontext.Error) {
		return agentcontext.PrepareSavedPacket(ctx, tx, data, resources, request, time.Now().UTC(), digest.Read)
	}, faults)
}

func (w *Workspace) AgentAnalyzeSource(ctx context.Context, request agentcontext.SourcePacketRequest) (json.RawMessage, error) {
	return w.agentAnalyzeSource(ctx, request, nil)
}

func (w *Workspace) agentAnalyzeSource(ctx context.Context, request agentcontext.SourcePacketRequest, faults *agentContextFaults) (json.RawMessage, error) {
	if e := agentcontext.ValidateSourcePacketRequest(request); e != nil {
		return nil, fail(e.Code)
	}
	return w.readAgentPacket(ctx, request.Channel, func(ctx context.Context, tx *sql.Tx, data, resources *agentPacketSource, _ *agentDigestObservation) ([]byte, *agentcontext.Error) {
		return agentcontext.PrepareSourcePacket(ctx, tx, data, resources, request, time.Now().UTC())
	}, faults)
}

// All packet read phases share admission, SQL reservation, observation and
// cleanup. A source packet never obtains a digest callback or a write method.
func (w *Workspace) readAgentPacket(ctx context.Context, channel string, prepare func(context.Context, *sql.Tx, *agentPacketSource, *agentPacketSource, *agentDigestObservation) ([]byte, *agentcontext.Error), faults *agentContextFaults) (json.RawMessage, error) {
	if w == nil || channel != w.start.Channel {
		return nil, fail("AGENT_CONTEXT_CHANNEL_MISMATCH")
	}
	if ctx == nil {
		return nil, fail("AGENT_CONTEXT_CANCELED")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	release, e := operationgate.Acquire(ctx)
	if e != nil {
		return nil, fail("AGENT_CONTEXT_CANCELED")
	}
	defer release() // Includes SQL rollback, digest/root closes and final admission.
	return readOwnedAssessment(w, ctx, func(data, resources *runtimepaths.Root) (out json.RawMessage, problem error) {
		step := func(name string) {
			if faults != nil && faults.checkpoint != nil {
				faults.checkpoint(name)
			}
		}
		pins, pe := data.GuardSQLiteFiles(applicationDatabasePath)
		if pe != nil || pins == nil {
			return nil, fail("AGENT_CONTEXT_DATABASE_UNAVAILABLE")
		}
		var db *sql.DB
		var tx *sql.Tx
		var digest *agentDigestObservation
		d := &agentPacketSource{root: data, ctx: ctx, files: map[string]agentPacketFile{}}
		r := &agentPacketSource{root: resources, ctx: ctx, files: map[string]agentPacketFile{}}
		cleanupFailure := func() { out, problem = nil, fail("AGENT_CONTEXT_CLEANUP_FAILED") }
		defer func() {
			// Keep file/directory witnesses live through database cleanup. No
			// canceled-context auto-rollback races with the explicit owner.
			if tx != nil {
				rollback := (*sql.Tx).Rollback
				if faults != nil && faults.rollback != nil {
					rollback = faults.rollback
				}
				if rollback(tx) != nil {
					cleanupFailure()
				}
			}
			if db != nil {
				closeDB := (*sql.DB).Close
				if faults != nil && faults.closeDB != nil {
					closeDB = faults.closeDB
				}
				if closeDB(db) != nil {
					cleanupFailure()
				}
			}
			step("after_database_close")
			if problem == nil && (ctx.Err() != nil || !d.recheck() || !r.recheck() || digest.Check(ctx) != nil || pins.Check() != nil) {
				out, problem = nil, fail("AGENT_CONTEXT_SOURCE_CHANGED")
			}
			if digest != nil {
				if digest.Close() != nil {
					cleanupFailure()
				}
				if faults != nil && faults.closeRuns != nil && faults.closeRuns(digest) != nil {
					cleanupFailure()
				}
			}
			if pins.Close() != nil {
				cleanupFailure()
			}
			if problem != nil {
				out = nil
			}
		}()
		var err error
		db, err = sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(w.start.DataRoot, filepath.FromSlash(applicationDatabasePath)))
		if err != nil {
			return nil, fail("AGENT_CONTEXT_DATABASE_UNAVAILABLE")
		}
		tx, err = db.BeginTx(context.WithoutCancel(ctx), nil)
		if err != nil {
			return nil, fail("AGENT_CONTEXT_TRANSACTION_FAILED")
		}
		// Existing open mode reserves an IMMEDIATE transaction; query_only
		// prevents this read owner from mutating even with a writable handle.
		if _, err = tx.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
			return nil, fail("AGENT_CONTEXT_TRANSACTION_FAILED")
		}
		var version int
		if tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version) != nil {
			return nil, fail("AGENT_CONTEXT_DATABASE_UNAVAILABLE")
		}
		if version != applications.SchemaVersion {
			return nil, fail("AGENT_CONTEXT_SCHEMA_UNSUPPORTED")
		}
		digest = &agentDigestObservation{dataRoot: w.start.DataRoot, data: data, resources: resources, tx: tx}
		step("before_read")
		packet, ce := prepare(ctx, tx, d, r, digest)
		if ce != nil {
			return nil, fail(ce.Code)
		}
		step("after_read")
		if !d.recheck() || !r.recheck() || digest.Check(ctx) != nil || pins.Check() != nil {
			return nil, fail("AGENT_CONTEXT_SOURCE_CHANGED")
		}
		return packet, nil
	})
}

// Retain first observations until the native owner finishes cleanup. The
// packet's own read-port checks are earlier checks, not a publication permit.
type agentPacketFile struct {
	raw     []byte
	present bool
	limit   int64
}
type agentPacketSource struct {
	root  *runtimepaths.Root
	ctx   context.Context
	files map[string]agentPacketFile
	bytes int
}

func (s *agentPacketSource) Check(path string) *runtimepaths.Error { return s.root.Check(path) }
func (s *agentPacketSource) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	if s.ctx.Err() != nil {
		return nil, false, &runtimepaths.Error{Code: "READ_CANCELED"}
	}
	if limit < 1 || limit > 4<<20 {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	raw, present, e := s.root.ReadSingleLinkFile(path, limit)
	if e != nil {
		return nil, false, e
	}
	if old, ok := s.files[path]; ok {
		if present != old.present || !bytes.Equal(raw, old.raw) {
			return nil, false, &runtimepaths.Error{Code: "FILE_CHANGED"}
		}
	} else {
		if len(s.files) >= 32 || len(raw) > (32<<20)-s.bytes {
			return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
		}
		s.files[path] = agentPacketFile{bytes.Clone(raw), present, limit}
		s.bytes += len(raw)
	}
	return raw, present, nil
}
func (s *agentPacketSource) recheck() bool {
	paths := make([]string, 0, len(s.files))
	for path := range s.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		old := s.files[path]
		raw, present, e := s.root.ReadSingleLinkFile(path, old.limit)
		if e != nil || present != old.present || !bytes.Equal(raw, old.raw) {
			return false
		}
	}
	return true
}
