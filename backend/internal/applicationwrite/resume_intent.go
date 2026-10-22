package applicationwrite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/resumelibrary"
	"careerhub.local/backend/internal/runtimepaths"
)

// A single immutable upload OR deletion: at most one 10 MiB pre/postimage,
// base64 plus metadata fits the existing native 16 MiB file-write ceiling.
const resumeIntentLimit = 16 << 20

type resumeIntent struct {
	Version   int                      `json:"version"`
	Home      datahome.Info            `json:"home"`
	Operation string                   `json:"operation"`
	File      resumelibrary.FileChange `json:"file"`
	Before    resumelibrary.Witness    `json:"before_row"`
	After     resumelibrary.Witness    `json:"after_row"`
}
type resumePublication struct {
	intent                       resumeIntent
	raw                          []byte
	recorded, removed, published bool
	cleanupCode, state           string
}

func (p *resumePublication) facts() publicationFacts {
	return publicationFacts{p.recorded, p.removed, p.cleanupCode, p.state}
}
func (*resumePublication) intentStage() string { return "resume_intent" }
func validResumeIntent(p resumeIntent) bool {
	h := p.Home
	if p.Version != 1 || h.FormatVersion != 2 || !h.Initialized || !slices.Contains([]string{"dev", "stable"}, h.Channel) || len(h.DataHomeID) != 35 || !strings.HasPrefix(h.DataHomeID, "dh_") {
		return false
	}
	for _, c := range h.DataHomeID[3:] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	if !slices.Contains([]string{"upload", "approved", "attach", "delete"}, p.Operation) || !resumelibrary.ValidChange(p.File) {
		return false
	}
	if (p.Operation == "delete") != p.File.BeforePresent {
		return false
	}
	for _, w := range []resumelibrary.Witness{p.Before, p.After} {
		if w.ResumeID != p.File.ResumeID || w.Present && !validDigest(w.SHA256) || !w.Present && w.SHA256 != "" {
			return false
		}
	}
	return p.Before.Present == p.File.BeforePresent && p.After.Present == p.File.AfterPresent
}
func decodeResumeIntent(raw []byte, home datahome.Info) (resumeIntent, *Error) {
	var p resumeIntent
	var envelope discoveryEnvelope
	if len(raw) > resumeIntentLimit || !canonicalIntentJSON(raw, &envelope) || envelope.SHA256 != digestBytes(envelope.Payload) || !canonicalIntentJSON(envelope.Payload, &p) || !validResumeIntent(p) {
		return p, failed("intent", "RESUME_INTENT_INVALID")
	}
	if p.Home != home {
		return p, failed("intent", "RESUME_INTENT_HOME_CHANGED")
	}
	return p, nil
}
func (r *sourceReads) planResumePublication(ctx context.Context, tx *sql.Tx, operation string, file resumelibrary.FileChange, before resumelibrary.Witness) *Error {
	if r.pendingPublication() != nil || len(r.files) != 0 || len(r.captures) != 0 {
		return failed("intent", "RESUME_INTENT_INVALID")
	}
	after, p := resumelibrary.MutationWitness(ctx, tx, file.ResumeID)
	if p != nil {
		return resumeProblem(p)
	}
	intent := resumeIntent{1, r.home, operation, file, before, after}
	if !validResumeIntent(intent) {
		return failed("intent", "RESUME_INTENT_INVALID")
	}
	payload, err := json.Marshal(intent)
	if err != nil {
		return failed("intent", "RESUME_INTENT_INVALID")
	}
	raw, err := json.Marshal(discoveryEnvelope{Payload: payload, SHA256: digestBytes(payload)})
	if err != nil || len(raw) > resumeIntentLimit {
		return failed("intent", "RESUME_INTENT_LIMIT")
	}
	frozen, e := decodeResumeIntent(raw, r.home)
	if e != nil {
		return e
	}
	r.resumePublication = &resumePublication{intent: frozen, raw: raw}
	return nil
}
func (p *resumePublication) databaseSide(ctx context.Context, tx *sql.Tx) (string, *Error) {
	w, e := resumelibrary.MutationWitness(ctx, tx, p.intent.File.ResumeID)
	if e != nil {
		return "", resumeProblem(e)
	}
	if w == p.intent.After {
		return "committed", nil
	}
	if w == p.intent.Before {
		return "rolled_back", nil
	}
	return "", failed("witness", "RESUME_DATABASE_STATE_CHANGED")
}
func (p *resumePublication) checkHome(root *runtimepaths.Root, home datahome.Info) *Error {
	current, e := datahome.InspectOwnedForResumeRecovery(root, nil)
	if e != nil {
		return failed("admission", e.Code)
	}
	if *current != home || home != p.intent.Home {
		return failed("admission", "RESUME_INTENT_HOME_CHANGED")
	}
	return nil
}
func (p *resumePublication) checkJournal(root *runtimepaths.Root) *Error {
	raw, present, e := root.ReadSingleLinkFile(datahome.ResumeRecoveryPath, resumeIntentLimit)
	if e != nil || !present || !bytes.Equal(raw, p.raw) {
		return failed("intent", "RESUME_INTENT_CHANGED")
	}
	return nil
}
func (p *resumePublication) observe(root *runtimepaths.Root) (bool, bool, *Error) {
	c := p.intent.File
	raw, present, e := root.ReadSingleLinkFile(c.RelativePath, resumelibrary.MaxResumeBytes)
	if e != nil {
		return false, false, failed("source", "RESUME_FILE_UNAVAILABLE")
	}
	return present == c.BeforePresent && bytes.Equal(raw, c.Before), present == c.AfterPresent && bytes.Equal(raw, c.After), nil
}
func (p *resumePublication) record(ctx context.Context, root *runtimepaths.Root, tx *sql.Tx, home datahome.Info) *Error {
	if ctx.Err() != nil {
		return canceled()
	}
	if e := p.checkHome(root, home); e != nil {
		return e
	}
	if present, e := root.Exists(datahome.ResumeRecoveryPath); e != nil || present {
		return failed("intent", "RESUME_INTENT_UNAVAILABLE")
	}
	before, _, e := p.observe(root)
	if e != nil {
		return e
	}
	if !before {
		return failed("source", "RESUME_FILE_CHANGED")
	}
	side, e := p.databaseSide(ctx, tx)
	if e != nil {
		return e
	}
	if side != "committed" {
		return failed("witness", "RESUME_DATABASE_STATE_CHANGED")
	}
	if e := resumelibrary.CheckPublication(ctx, tx, p.intent.File, true); e != nil {
		return resumeProblem(e)
	}
	if ctx.Err() != nil {
		return canceled()
	}
	var pe *runtimepaths.Error
	p.recorded, pe = root.CreateFile(datahome.ResumeRecoveryPath, p.raw)
	if pe != nil || !p.recorded {
		return failed("intent", "RESUME_INTENT_WRITE_FAILED")
	}
	return nil
}
func (p *resumePublication) boundary(ctx context.Context, root *runtimepaths.Root, tx *sql.Tx, pins *runtimepaths.SQLiteFiles, home datahome.Info) *Error {
	if ctx.Err() != nil {
		return canceled()
	}
	if e := p.checkHome(root, home); e != nil {
		return e
	}
	if e := p.checkJournal(root); e != nil {
		return e
	}
	if pins == nil || pins.Check() != nil {
		return failed("path", "RESUME_DATABASE_PATH_CHANGED")
	}
	return schemaCurrent(ctx, tx)
}
func (p *resumePublication) checkReady(ctx context.Context, root *runtimepaths.Root, tx *sql.Tx, pins *runtimepaths.SQLiteFiles, home datahome.Info) *Error {
	if e := p.boundary(ctx, root, tx, pins, home); e != nil {
		return e
	}
	before, _, e := p.observe(root)
	if e != nil {
		return e
	}
	if !before {
		return failed("source", "RESUME_FILE_CHANGED")
	}
	side, e := p.databaseSide(ctx, tx)
	if e != nil {
		return e
	}
	if side != "committed" {
		return failed("witness", "RESUME_DATABASE_STATE_CHANGED")
	}
	return resumeProblem(resumelibrary.CheckPublication(ctx, tx, p.intent.File, true))
}
func (p *resumePublication) finishCommitted(ctx context.Context, root *runtimepaths.Root, conn *sql.Conn, pins *runtimepaths.SQLiteFiles, home datahome.Info, checkpoint func(string) *Error, rollback func(*sql.Tx) error) (problem *Error) {
	tx, e := conn.BeginTx(context.WithoutCancel(ctx), nil)
	if e != nil {
		return failed("verify", "RESUME_VERIFY_TRANSACTION_FAILED")
	}
	defer func() {
		if rollback(tx) != nil {
			p.cleanupCode = "RESUME_VERIFY_ROLLBACK_FAILED"
			if problem == nil {
				problem = failed("cleanup", p.cleanupCode)
			}
		}
	}()
	return p.settle(ctx, root, tx, pins, home, "committed", checkpoint)
}

func (p *resumePublication) settle(ctx context.Context, root *runtimepaths.Root, tx *sql.Tx, pins *runtimepaths.SQLiteFiles, home datahome.Info, expected string, checkpoint func(string) *Error) *Error {
	if e := p.boundary(ctx, root, tx, pins, home); e != nil {
		return e
	}
	side, e := p.databaseSide(ctx, tx)
	if e != nil {
		return e
	}
	if expected != "" && side != expected {
		return failed("witness", "RESUME_DATABASE_STATE_CHANGED")
	}
	p.state = side
	before, after, e := p.observe(root)
	if e != nil {
		return e
	}
	if !before && !after || side == "rolled_back" && !before {
		return failed("source", "RESUME_FILE_CHANGED")
	}
	if e := resumelibrary.CheckPublication(ctx, tx, p.intent.File, side == "committed"); e != nil {
		return resumeProblem(e)
	}
	if side == "committed" {
		if !after {
			if e := p.boundary(ctx, root, tx, pins, home); e != nil {
				return e
			}
			var pe *runtimepaths.Error
			changed := false
			c := p.intent.File
			if c.AfterPresent {
				_, pe = root.EnsureDirectory(resumelibrary.LibraryPath)
				if pe == nil {
					changed, pe = root.CreateFile(c.RelativePath, c.After)
				}
			} else {
				changed, pe = root.RemoveFile(c.RelativePath, c.Before)
			}
			p.published = p.published || changed
			if pe != nil || !changed {
				return failed("publish", "RESUME_FILE_PUBLICATION_FAILED")
			}
		}
		if e := checkpoint("resume_file"); e != nil {
			return e
		}
	}
	if e := checkpoint("resume_files_complete"); e != nil {
		return e
	}
	if e := p.boundary(ctx, root, tx, pins, home); e != nil {
		return e
	}
	before, after, e = p.observe(root)
	if e != nil {
		return e
	}
	if side == "committed" && !after || side == "rolled_back" && !before {
		return failed("source", "RESUME_FILE_CHANGED")
	}
	current, e := p.databaseSide(ctx, tx)
	if e != nil {
		return e
	}
	if current != side {
		return failed("witness", "RESUME_DATABASE_STATE_CHANGED")
	}
	if e := resumelibrary.CheckPublication(ctx, tx, p.intent.File, side == "committed"); e != nil {
		return resumeProblem(e)
	}
	var pe *runtimepaths.Error
	p.removed, pe = root.RemoveFile(datahome.ResumeRecoveryPath, p.raw)
	if pe != nil || !p.removed {
		return failed("cleanup", "RESUME_INTENT_REMOVE_FAILED")
	}
	return checkpoint("resume_journal_removed")
}
