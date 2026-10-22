package discovery

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

// SourceChange is a proposed managed-file replacement, not a write or a commit.
// BeforePresent distinguishes absent from a valid empty previous file. The
// owning coordinator MUST recheck it and recoverably publish with the SQL tx.
type SourceChange struct {
	RelativePath  string `json:"path"`
	BeforePresent bool   `json:"before_present"`
	AfterPresent  bool   `json:"after_present"`
	Before        []byte `json:"before"`
	After         []byte `json:"after"`
}
type CaptureResult struct {
	EditResult
	Source *SourceChange `json:"-"`
}

func newCaptureID() (string, error) {
	var raw [16]byte
	if _, e := rand.Read(raw[:]); e != nil {
		return "", e
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	return "discovery_" + hex.EncodeToString(raw[:]), nil
}
func validCaptureID(id string) bool {
	if len(id) != 42 || !strings.HasPrefix(id, "discovery_") {
		return false
	}
	for _, c := range id[10:] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// CaptureInTransaction applies only transaction-local SQL and builds the full
// response plus a closed file-publication plan. It NEVER writes files/commits.
// newID is a trusted coordinator/test seam, not a request field; nil uses UUID4.
// Do not activate this primitive without the owned recovery coordinator.
func CaptureInTransaction(ctx context.Context, tx *sql.Tx, files CaptureFiles, input CaptureRequest, now string, newID func() (string, error)) (*CaptureResult, *WriteError) {
	return CaptureObservedInTransaction(ctx, tx, files, input, now, newID, nil)
}

// MutationObserver is a trusted owning-coordinator seam for recording exact SQL
// preimages before any selected record changes. It must not commit or publish.
type MutationObserver func(context.Context, *sql.Tx, []string) *WriteError

func CaptureObservedInTransaction(ctx context.Context, tx *sql.Tx, files CaptureFiles, input CaptureRequest, now string, newID func() (string, error), observe MutationObserver) (*CaptureResult, *WriteError) {
	plan, e := normalizeCapture(ctx, input, now)
	if e != nil {
		return nil, e
	}
	if tx == nil {
		return nil, writeFailure("transaction", "DATABASE_CONNECTION_INVALID")
	}
	r := &reader{query: tx, ctx: ctx, remaining: MaxReadBytes}
	retry, payload, e := captureObservation(r, "source_kind=? AND request_fingerprint=?", []any{plan.kind, plan.request})
	if e != nil {
		return nil, e
	}
	if retry != nil {
		if payload != plan.payloadSHA {
			return nil, sourceFailure("DiscoveryIdempotencyConflict", "request_fingerprint was already used for different discovery content")
		}
		return captureResponse(r, retry, "duplicate", []string{}, nil)
	}
	current, e := matchingCapture(r, plan)
	if e != nil {
		return nil, e
	}
	outcome := "updated"
	potential := []string{}
	var id string
	var change *SourceChange
	if current == nil {
		if newID == nil {
			newID = newCaptureID
		}
		var err error
		id, err = newID()
		if err != nil || !validCaptureID(id) {
			return nil, writeFailure("identity", "CAPTURE_ID_FAILED")
		}
		if observe != nil {
			if e := observe(ctx, tx, []string{id}); e != nil {
				return nil, e
			}
		}
		if plan.text != "" {
			change, e = captureReplacement(files, id, nil, plan.text)
			if e != nil {
				return nil, e
			}
		}
		var path any
		if change != nil {
			path = change.RelativePath
		}
		verified, from := "", ""
		if plan.scope == "confirmed_full" {
			verified, from = now, "confirmed_full"
		}
		e = editSQL(ctx, tx, `INSERT INTO job_discoveries (`+columns+`) VALUES (?, 'inbox', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, 1, ?, ?)`,
			id, plan.fields.Title, plan.fields.Company, plan.fields.Location, plan.url, plan.canonical, plan.identity, plan.scope, path, plan.contentSHA, utf8.RuneCountInString(plan.text), plan.observed, plan.observed, now, verified, from)
		if e != nil {
			return nil, e
		}
		potential, e = potentialCaptureDuplicates(r, plan.fields, id)
		if e != nil {
			return nil, e
		}
		outcome = "created"
	} else {
		id = current.DiscoveryID
		if observe != nil {
			if e := observe(ctx, tx, []string{id}); e != nil {
				return nil, e
			}
		}
		change, e = mergeCapture(ctx, tx, files, plan, *current, now)
		if e != nil {
			return nil, e
		}
	}
	inserted, err := tx.ExecContext(ctx, `INSERT INTO job_discovery_observations (discovery_id,source_kind,source_channel,request_fingerprint,request_payload_sha256,observed_url,observed_at,content_sha256,metadata_json,recorded_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		id, plan.kind, plan.channel, plan.request, plan.payloadSHA, plan.url, plan.observed, plan.contentSHA, plan.metadata, now)
	if err != nil {
		return nil, writeFailure("observation", "DATABASE_WRITE_FAILED")
	}
	observationID, err := inserted.LastInsertId()
	if err != nil {
		return nil, writeFailure("observation", "DATABASE_WRITE_FAILED")
	}
	observation, _, e := captureObservation(r, "observation_id=?", []any{observationID})
	if e != nil {
		return nil, e
	}
	if observation == nil {
		return nil, invalidWrite("Discovery observation was not stored")
	}
	return captureResponse(r, observation, outcome, potential, change)
}

func captureObservation(r *reader, where string, args []any) (*Observation, string, *WriteError) {
	// where is selected only by fixed internal call sites, never request SQL.
	var result *Observation
	var payload string
	e := r.rows("observation", `SELECT observation_id,discovery_id,source_kind,source_channel,request_fingerprint,observed_url,observed_at,content_sha256,metadata_json,recorded_at,request_payload_sha256 FROM job_discovery_observations WHERE `+where, args, 11, 1, func(raw []any) error {
		o := Observation{}
		var err error
		if o.ObservationID, err = integerCell(raw[0]); err != nil {
			return err
		}
		for i, target := range map[int]*string{1: &o.DiscoveryID, 2: &o.SourceKind, 3: &o.SourceChannel, 4: &o.RequestFingerprint, 5: &o.ObservedURL, 6: &o.ObservedAt, 7: &o.ContentSHA256, 9: &o.RecordedAt, 10: &payload} {
			if *target, err = textCell(raw[i], false); err != nil {
				return err
			}
		}
		metadata, err := textCell(raw[8], false)
		if err != nil {
			return err
		}
		if o.Metadata, err = parseObject(metadata); err != nil {
			return failure("observation", "METADATA_INVALID")
		}
		result = &o
		return nil
	})
	return result, payload, fromRead(e)
}
func captureResponse(r *reader, observation *Observation, outcome string, potential []string, change *SourceChange) (*CaptureResult, *WriteError) {
	current, e := editResponse(r, observation.DiscoveryID, outcome != "duplicate")
	if e != nil {
		return nil, e
	}
	response := struct {
		Outcome     string          `json:"outcome"`
		Discovery   json.RawMessage `json:"discovery"`
		Observation *Observation    `json:"observation"`
		Potential   []string        `json:"potential_duplicate_ids"`
	}{outcome, current.Result, observation, potential}
	result, e := serializedEdit(response, current.Changed)
	if e != nil {
		return nil, e
	}
	return &CaptureResult{EditResult: *result, Source: change}, nil
}

func matchingCapture(r *reader, p captureInput) (*Record, *WriteError) {
	if p.target != "" {
		record, e := writeRecord(r, p.target)
		if e != nil {
			return nil, e
		}
		if record.Revision != p.revision {
			return nil, sourceFailure("DiscoveryRevisionConflict", "Discovery changed while its public link was resolved")
		}
		if record.State != "inbox" && record.State != "shortlisted" {
			return nil, sourceFailure("DiscoveryPublicResolutionNotReady", "Only an active queued Discovery can resolve a public link")
		}
		if record.ContentScope != "link_only" {
			return nil, sourceFailure("DiscoveryPublicResolutionNotReady", "Public resolution requires a link-only Discovery")
		}
		return &record, nil
	}
	first := func(where string, args ...any) (*Record, *WriteError) {
		rows, e := selected(r, "SELECT "+columns+" FROM job_discoveries WHERE "+where+" ORDER BY first_seen_at,discovery_id LIMIT 1", args, 1)
		if e != nil {
			return nil, fromRead(e)
		}
		if len(rows) == 0 {
			return nil, nil
		}
		return &rows[0], nil
	}
	if p.identity != "" {
		row, e := first("posting_identity=?", p.identity)
		if e != nil || row != nil {
			return row, e
		}
	}
	if p.canonical != "" {
		return first("canonical_url=? AND (posting_identity='' OR ?='' OR posting_identity=?)", p.canonical, p.identity, p.identity)
	}
	if p.identity == "" && p.contentSHA != "" {
		return first("posting_identity='' AND canonical_url='' AND content_sha256=?", p.contentSHA)
	}
	return nil, nil
}
func potentialCaptureDuplicates(r *reader, fields Fields, excluding string) ([]string, *WriteError) {
	ids := []string{}
	if fields.Title == "" || fields.Company == "" || fields.Location == "" {
		return ids, nil
	}
	key := [3]string{textcompat.Fold(fields.Title), textcompat.Fold(fields.Company), textcompat.Fold(fields.Location)}
	e := r.rows("potential_duplicates", `SELECT discovery_id,title,company,location FROM job_discoveries WHERE discovery_id<>? ORDER BY first_seen_at,discovery_id`, []any{excluding}, 4, MaxObservations, func(raw []any) error {
		values := [4]string{}
		for i := range values {
			var err error
			values[i], err = textCell(raw[i], false)
			if err != nil {
				return err
			}
		}
		rowKey := [3]string{textcompat.Fold(normalize(values[1])), textcompat.Fold(normalize(values[2])), textcompat.Fold(normalize(values[3]))}
		if rowKey == key {
			ids = append(ids, values[0])
		}
		if len(ids) == 10 {
			return errStopRows
		}
		return nil
	})
	return ids, fromRead(e)
}

func capturePath(id string) (string, *WriteError) {
	// '..' is a source-compatible logical SQL identifier, not a safe file name.
	if id == ".." {
		return "", writeFailure("source", "SOURCE_PATH_REJECTED")
	}
	if _, e := writeID(id, "discovery_id"); e != nil {
		return "", writeFailure("source", "SOURCE_PATH_REJECTED")
	}
	return "jobs/discovery/captures/" + id + "/source.txt", nil
}
func rawCaptureSource(files CaptureFiles, record Record) ([]byte, *WriteError) {
	path := strings.ReplaceAll(record.SourceRelativePath, "\\", "/")
	if !strings.HasPrefix(path, "jobs/discovery/captures/") {
		return nil, writeFailure("source", "SOURCE_PATH_REJECTED")
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.ContainsRune(part, ':') {
			return nil, writeFailure("source", "SOURCE_PATH_REJECTED")
		}
	}
	if files == nil {
		return nil, writeFailure("source", "SOURCE_UNREADABLE")
	}
	raw, present, e := files.ReadFile(path, 4<<20)
	if e != nil || !present {
		return nil, writeFailure("source", "SOURCE_UNREADABLE")
	}
	if len(raw) > 4<<20 {
		return nil, writeFailure("source", "RESULT_LIMIT")
	}
	if !utf8.Valid(raw) {
		return nil, invalidWrite("Stored discovery source is unreadable")
	}
	// Capture merge uses raw decoded text, unlike detail's universal newlines.
	if int64(utf8.RuneCount(raw)) != record.ContentChars || sourceSHA(string(raw)) != record.ContentSHA256 {
		return nil, invalidWrite("Stored discovery source does not match its record")
	}
	return raw, nil
}
func captureReplacement(files CaptureFiles, id string, before []byte, text string) (*SourceChange, *WriteError) {
	path, e := capturePath(id)
	if e != nil {
		return nil, e
	}
	present := before != nil
	if !present {
		if files == nil {
			return nil, writeFailure("source", "SOURCE_UNREADABLE")
		}
		_, exists, err := files.ReadFile(path, 4<<20)
		if err != nil {
			return nil, writeFailure("source", "SOURCE_PATH_REJECTED")
		}
		if exists {
			return nil, writeFailure("source", "SOURCE_ALREADY_EXISTS")
		}
	}
	return &SourceChange{RelativePath: path, BeforePresent: present, AfterPresent: true, Before: append([]byte(nil), before...), After: []byte(text)}, nil
}

func mergeCapture(ctx context.Context, tx *sql.Tx, files CaptureFiles, p captureInput, current Record, now string) (*SourceChange, *WriteError) {
	if e := nextRevision(current.Revision); e != nil {
		return nil, e
	}
	var raw []byte
	if current.SourceRelativePath != "" {
		var e *WriteError
		raw, e = rawCaptureSource(files, current)
		if e != nil {
			return nil, e
		}
		// An existing zero-byte file is still present, never an absent precondition.
		if raw == nil {
			raw = []byte{}
		}
	}
	rank := map[string]int{"link_only": 0, "selection": 1, "page_visible": 2, "confirmed_full": 3}
	better := rank[p.scope] > rank[current.ContentScope]
	pageRefresh := p.text != "" && p.scope == "page_visible" && current.ContentScope == "page_visible" && p.contentSHA != current.ContentSHA256
	confirmedRefresh := p.text != "" && p.scope == "page_visible" && current.ContentScope == "confirmed_full" && p.contentSHA != current.ContentSHA256
	preferred := better || pageRefresh || confirmedRefresh
	replace := p.text != "" && (current.SourceRelativePath == "" || preferred)
	if better || confirmedRefresh {
		current.ContentScope = p.scope
	}
	if better && p.scope == "confirmed_full" {
		current.ContentVerification.VerifiedAt, current.ContentVerification.FromScope = now, "confirmed_full"
	} else if confirmedRefresh {
		current.ContentVerification.VerifiedAt, current.ContentVerification.FromScope = "", ""
	}
	var change *SourceChange
	if replace {
		path, e := capturePath(current.DiscoveryID)
		if e != nil {
			return nil, e
		}
		if current.SourceRelativePath != "" && current.SourceRelativePath != path {
			return nil, invalidWrite("Stored discovery source path is invalid")
		}
		change, e = captureReplacement(files, current.DiscoveryID, raw, p.text)
		if e != nil {
			return nil, e
		}
		current.SourceRelativePath, current.ContentSHA256, current.ContentChars = path, p.contentSHA, int64(utf8.RuneCountInString(p.text))
	}
	merge := func(old, incoming string) string {
		if incoming != "" && (old == "" || preferred) {
			return incoming
		}
		return old
	}
	current.Title, current.Company, current.Location = merge(current.Title, p.fields.Title), merge(current.Company, p.fields.Company), merge(current.Location, p.fields.Location)
	if current.ObservedURL == "" {
		current.ObservedURL = p.url
	}
	if current.CanonicalURL == "" {
		current.CanonicalURL = p.canonical
	}
	if p.target == "" && current.PostingIdentity == "" {
		current.PostingIdentity = p.identity
	}
	var path any
	if current.SourceRelativePath != "" {
		path = current.SourceRelativePath
	}
	e := editSQL(ctx, tx, `UPDATE job_discoveries SET title=?,company=?,location=?,observed_url=?,canonical_url=?,posting_identity=?,content_scope=?,source_relative_path=?,content_sha256=?,content_chars=?,first_seen_at=?,last_seen_at=?,updated_at=?,content_verified_at=?,content_verified_from_scope=?,revision=revision+1 WHERE discovery_id=? AND revision=?`,
		current.Title, current.Company, current.Location, current.ObservedURL, current.CanonicalURL, current.PostingIdentity, current.ContentScope, path, current.ContentSHA256, current.ContentChars, min(current.FirstSeenAt, p.observed), max(current.LastSeenAt, p.observed), now, current.ContentVerification.VerifiedAt, current.ContentVerification.FromScope, current.DiscoveryID, current.Revision)
	return change, e
}
