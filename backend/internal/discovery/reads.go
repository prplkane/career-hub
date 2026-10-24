package discovery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"slices"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/jobs/joburls"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/textcompat"
)

const joins = ` FROM job_discoveries AS discoveries
LEFT JOIN job_discovery_field_overrides AS overrides ON overrides.discovery_id = discoveries.discovery_id
LEFT JOIN job_discovery_assessments AS assessments ON assessments.discovery_id = discoveries.discovery_id
AND assessments.content_sha256 = discoveries.content_sha256 AND assessments.scope = discoveries.content_scope`

func selected(r *reader, query string, args []any, maximum int) ([]Record, *Error) {
	result := []Record{}
	err := r.rows("discoveries", query, args, 20, maximum, func(raw []any) error {
		record, e := baseRecord(raw)
		if e != nil {
			return e
		}
		result = append(result, record)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func enrich(r *reader, records []Record) *Error {
	return enrichCapture(r, records, true)
}

func enrichCapture(r *reader, records []Record, includeAssessment bool) *Error {
	if len(records) == 0 {
		return nil
	}
	args := make([]any, len(records))
	positions := map[string]int{}
	for i, record := range records {
		if _, ok := positions[record.DiscoveryID]; ok {
			return failure("discoveries", "DATABASE_VALUE_UNSUPPORTED")
		}
		args[i] = record.DiscoveryID
		positions[record.DiscoveryID] = i
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",") + ")"
	lookup := func(value any) (*Record, error) {
		id, e := textCell(value, false)
		if e != nil {
			return nil, e
		}
		index, ok := positions[id]
		if !ok {
			return nil, ErrUnsupported
		}
		return &records[index], nil
	}
	if err := r.rows("overrides", "SELECT discovery_id,title,company,location,revision,updated_at FROM job_discovery_field_overrides WHERE discovery_id IN ("+marks, args, 6, len(records), func(raw []any) error {
		record, e := lookup(raw[0])
		if e != nil {
			return e
		}
		for i, target := range []**string{&record.FieldOverrides.Title, &record.FieldOverrides.Company, &record.FieldOverrides.Location} {
			if raw[i+1] != nil {
				v, e := textCell(raw[i+1], false)
				if e != nil {
					return e
				}
				*target = &v
			}
		}
		if record.FieldOverrides.Revision, e = integerCell(raw[4]); e != nil {
			return e
		}
		record.FieldOverrides.UpdatedAt, e = textCell(raw[5], false)
		return e
	}); err != nil {
		return err
	}
	stored := map[string]map[string]any{}
	if err := r.rows("observations", "SELECT discovery_id,source_kind,metadata_json,observed_url FROM job_discovery_observations WHERE discovery_id IN ("+marks+" ORDER BY observation_id", args, 4, MaxObservations, func(raw []any) error {
		record, e := lookup(raw[0])
		if e != nil {
			return e
		}
		kind, e := textCell(raw[1], false)
		if e != nil {
			return e
		}
		metadataRaw, e := textCell(raw[2], false)
		if e != nil {
			return e
		}
		url, e := textCell(raw[3], false)
		if e != nil {
			return e
		}
		metadata, e := parseObject(metadataRaw)
		if e != nil {
			return failure("observations", "METADATA_INVALID")
		}
		if !slices.Contains(record.SourceKinds, kind) {
			record.SourceKinds = append(record.SourceKinds, kind)
		}
		if kind == "public_resolver" && url != "" {
			record.ResolvedURL = url
		}
		provider, e := metadataText(metadata["source_provider"])
		if e != nil {
			return e
		}
		provider = textcompat.Fold(textcompat.Strip(provider))
		if provider == "" || provider == "generic" {
			provider, e = joburls.ProviderName(url)
			if e != nil {
				return e
			}
		}
		if provider != "" && provider != "generic" && !slices.Contains(record.SourceProviders, provider) {
			record.SourceProviders = append(record.SourceProviders, provider)
		}
		if facts, ok := metadata["capture_facts"].(map[string]any); kind == "email_file" && ok {
			stored[record.DiscoveryID] = facts
		}
		return nil
	}); err != nil {
		return err
	}
	assessments := map[string]*assessmentSummary{}
	if includeAssessment {
		if err := r.rows("assessments", "SELECT discovery_id,content_sha256,scope,assessment_json,computed_at FROM job_discovery_assessments WHERE discovery_id IN ("+marks, args, 5, len(records), func(raw []any) error {
			record, e := lookup(raw[0])
			if e != nil {
				return e
			}
			a := &assessmentSummary{}
			for i, target := range []*string{&a.sha, &a.scope, &a.raw, &a.computed} {
				v, e := textCell(raw[i+1], false)
				if e != nil {
					return e
				}
				*target = v
			}
			assessments[record.DiscoveryID] = a
			return nil
		}); err != nil {
			return err
		}
	}
	for i := range records {
		record := &records[i]
		slices.Sort(record.SourceKinds)
		slices.Sort(record.SourceProviders)
		if err := finishRecord(record, assessments[record.DiscoveryID], stored[record.DiscoveryID]); err != nil {
			if typed, ok := err.(*Error); ok {
				return typed
			}
			return failure("projection", "DATABASE_VALUE_UNSUPPORTED")
		}
	}
	return nil
}

func ReadList(ctx context.Context, db *sql.DB, options PageOptions) ([]Record, *Error) {
	return readList(ctx, db, options, true)
}

// ReadFeedInputs excludes legacy derived assessments. A native caller must
// project current decisions before exposing this page; SQL queue selection is
// deliberately disallowed because it would consult source-era summaries.
func ReadFeedInputs(ctx context.Context, db *sql.DB, options PageOptions) ([]Record, *Error) {
	if len(options.Queues) != 0 || options.Sort == "queue_priority" {
		return nil, failure("request", "CURRENT_DECISION_PROJECTION_REQUIRED")
	}
	return readList(ctx, db, options, false)
}

func readList(ctx context.Context, db *sql.DB, options PageOptions, includeAssessment bool) ([]Record, *Error) {
	if err := ValidatePage(options); err != nil {
		return nil, err
	}
	where, args, _ := queryParts(options.Filters)
	order := map[string]string{
		"last_desc":      "discoveries.last_seen_at DESC, discoveries.discovery_id",
		"first_desc":     "discoveries.first_seen_at DESC, discoveries.discovery_id",
		"title_asc":      "LOWER(COALESCE(overrides.title, discoveries.title)), discoveries.discovery_id",
		"queue_priority": "CASE (" + queueSQL + ") WHEN 'clear_conflict' THEN 0 WHEN 'review_first' THEN 1 WHEN 'review' THEN 2 ELSE 3 END, discoveries.last_seen_at DESC, discoveries.discovery_id",
	}[options.Sort]
	query := "SELECT discoveries." + strings.ReplaceAll(columns, ",", ",discoveries.") + joins + where + " ORDER BY " + order + " LIMIT ? OFFSET ?"
	args = append(args, options.Limit, options.Offset)
	var result []Record
	err := snapshot(ctx, db, func(r *reader) *Error {
		var err *Error
		result, err = selected(r, query, args, options.Limit)
		if err != nil {
			return err
		}
		if err = enrichCapture(r, result, includeAssessment); err != nil {
			return err
		}
		return checkResult(result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func ReadCount(ctx context.Context, db *sql.DB, filters Filters) (int64, *Error) {
	where, args, e := queryParts(filters)
	if e != nil {
		return 0, e
	}
	var count int64
	err := snapshot(ctx, db, func(r *reader) *Error {
		return r.rows("count", "SELECT COUNT(*)"+joins+where, args, 1, 1, func(raw []any) error { var err error; count, err = integerCell(raw[0]); return err })
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// CaptureFiles admits only a native managed-root read in production. The narrow
// seam permits synthetic integrity faults without opening the user's Data Home.
type CaptureFiles interface {
	ReadFile(string, int64) ([]byte, bool, *runtimepaths.Error)
}

func capturedText(files CaptureFiles, record Record) (string, *Error) {
	if record.SourceRelativePath == "" {
		return "", nil
	}
	path := strings.ReplaceAll(record.SourceRelativePath, "\\", "/")
	if !strings.HasPrefix(path, "jobs/discovery/captures/") {
		return "", failure("source", "SOURCE_PATH_REJECTED")
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.ContainsRune(part, ':') {
			return "", failure("source", "SOURCE_PATH_REJECTED")
		}
	}
	if files == nil {
		return "", failure("source", "SOURCE_UNREADABLE")
	}
	// 500k Unicode characters can use 2 MB; universal-newline source files may
	// use two bytes per logical newline. The raw file limit is explicit, not a
	// truncation. File contents are still checked against the stored exact count.
	raw, present, e := files.ReadFile(path, 4<<20)
	if e != nil {
		if e.Code == "FILE_SIZE_LIMIT" {
			return "", failure("source", "RESULT_LIMIT")
		}
		if strings.HasPrefix(e.Code, "PATH_") {
			return "", failure("source", "SOURCE_PATH_REJECTED")
		}
		return "", failure("source", "SOURCE_UNREADABLE")
	}
	if !present {
		return "", failure("source", "SOURCE_PATH_REJECTED")
	}
	if len(raw) > 4<<20 {
		return "", failure("source", "RESULT_LIMIT")
	}
	if !utf8.Valid(raw) {
		return "", failure("source", "SOURCE_UNREADABLE")
	}
	text := strings.ReplaceAll(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\r", "\n")
	if int64(utf8.RuneCountInString(text)) != record.ContentChars {
		return "", failure("source", "SOURCE_LENGTH_MISMATCH")
	}
	hash := ""
	if text != "" {
		digest := sha256.Sum256([]byte(text))
		hash = hex.EncodeToString(digest[:])
	}
	if hash != record.ContentSHA256 {
		return "", failure("source", "SOURCE_HASH_MISMATCH")
	}
	return text, nil
}

func detailObservations(r *reader, id string) ([]Observation, *Error) {
	observations := []Observation{}
	err := r.rows("observations", `SELECT observation_id,discovery_id,source_kind,source_channel,request_fingerprint,observed_url,observed_at,content_sha256,metadata_json,recorded_at,request_payload_sha256 FROM job_discovery_observations WHERE discovery_id=? ORDER BY recorded_at,observation_id`, []any{id}, 11, MaxObservations, func(raw []any) error {
		o := Observation{}
		var e error
		if o.ObservationID, e = integerCell(raw[0]); e != nil {
			return e
		}
		for i, target := range map[int]*string{1: &o.DiscoveryID, 2: &o.SourceKind, 3: &o.SourceChannel, 4: &o.RequestFingerprint, 5: &o.ObservedURL, 6: &o.ObservedAt, 7: &o.ContentSHA256, 9: &o.RecordedAt} {
			v, e := textCell(raw[i], false)
			if e != nil {
				return e
			}
			*target = v
		}
		metadata, e := textCell(raw[8], false)
		if e != nil {
			return e
		}
		if o.Metadata, e = parseObject(metadata); e != nil {
			return failure("observations", "METADATA_INVALID")
		}
		observations = append(observations, o)
		return nil
	})
	return observations, err
}

func emailSource(r *reader, observations []Observation) (*EmailSource, *Error) {
	var metadata JSONDocument
	for _, o := range observations {
		if o.SourceKind == "email_file" {
			metadata = o.Metadata
		}
	}
	if metadata == nil {
		return nil, nil
	}
	source := &EmailSource{}
	message, e := metadataText(metadata["message_id_hash"])
	if e != nil {
		return nil, failure("email_source", "DATABASE_VALUE_UNSUPPORTED")
	}
	if source.SenderDomain, e = metadataText(metadata["sender_domain"]); e != nil {
		return nil, failure("email_source", "DATABASE_VALUE_UNSUPPORTED")
	}
	if message != "" {
		if err := r.rows("email_source", `SELECT sender_address FROM job_email_message_receipts WHERE connection_id='primary' AND message_id_hash=? AND sender_address<>'' ORDER BY updated_at DESC,receipt_id DESC LIMIT 1`, []any{message}, 1, 1, func(raw []any) error { var e error; source.SenderAddress, e = textCell(raw[0], false); return e }); err != nil {
			return nil, err
		}
	}
	if source.SenderAddress != "" {
		if err := r.rows("email_source", `SELECT disposition,purpose FROM job_email_sender_rules WHERE connection_id='primary' AND sender_address=?`, []any{source.SenderAddress}, 2, 1, func(raw []any) error {
			var e error
			if source.Disposition, e = textCell(raw[0], false); e != nil {
				return e
			}
			source.Purpose, e = textCell(raw[1], false)
			return e
		}); err != nil {
			return nil, err
		}
	}
	source.Configurable = source.SenderAddress != ""
	return source, nil
}

// Capture is the effective retained source used for fresh advisory analysis.
// It deliberately excludes old assessment JSON and email-message details, so
// a corrupt derived cache cannot prevent recomputation from a valid capture.
type Capture struct {
	Record
	SourceText string
}

func ReadCapture(ctx context.Context, db *sql.DB, files CaptureFiles, id string) (*Capture, *Error) {
	key, e := NormalizeID(id)
	if e != nil {
		return nil, e
	}
	var result *Capture
	err := snapshot(ctx, db, func(r *reader) *Error {
		result, e = readCapture(r, files, key)
		return e
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ReadCaptureInTransaction borrows an already admitted current-schema owner
// transaction. It never opens a database or commits, and its managed capture
// file is still observed through the caller's read/recheck port.
func ReadCaptureInTransaction(ctx context.Context, tx *sql.Tx, files CaptureFiles, id string) (*Capture, *Error) {
	key, e := NormalizeID(id)
	if e != nil {
		return nil, e
	}
	if ctx == nil || tx == nil {
		return nil, failure("open", "DATABASE_CONNECTION_INVALID")
	}
	if ctx.Err() != nil {
		return nil, failure("read", "READ_CANCELED")
	}
	var version int64
	if tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version) != nil {
		return nil, failure("schema", "DATABASE_QUERY_FAILED")
	}
	if version < SchemaVersion {
		return nil, failure("schema", "SCHEMA_PREPARATION_REQUIRED")
	}
	if version > SchemaVersion {
		return nil, failure("schema", "SCHEMA_TOO_NEW")
	}
	return readCapture(&reader{query: tx, ctx: ctx, remaining: MaxReadBytes}, files, key)
}

func readCapture(r *reader, files CaptureFiles, key string) (*Capture, *Error) {
	records, err := selected(r, "SELECT "+columns+" FROM job_discoveries WHERE discovery_id=?", []any{key}, 1)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, failure("discoveries", "NOT_FOUND")
	}
	if err = enrichCapture(r, records, false); err != nil {
		return nil, err
	}
	result := &Capture{Record: records[0]}
	if result.SourceText, err = ReadCapturedSource(r.ctx, files, result.Record); err != nil {
		return nil, err
	}
	if err = checkResult(result); err != nil {
		return nil, err
	}
	return result, nil
}

// ReadCapturedSource repeats the same confined path, decoding, scalar-count and
// SHA-256 checks used by detail reads. Record metadata is never proof by itself.
func ReadCapturedSource(ctx context.Context, files CaptureFiles, record Record) (string, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return "", failure("read", "READ_CANCELED")
	}
	text, err := capturedText(files, record)
	if ctx.Err() != nil {
		return "", failure("read", "READ_CANCELED")
	}
	return text, err
}

func ReadDetail(ctx context.Context, db *sql.DB, files CaptureFiles, id string) (*Detail, *Error) {
	return readDetail(ctx, db, files, id, true)
}

// ReadFeedDetail retains capture and email provenance without interpreting a
// legacy derived assessment as a native current-context decision.
func ReadFeedDetail(ctx context.Context, db *sql.DB, files CaptureFiles, id string) (*Detail, *Error) {
	return readDetail(ctx, db, files, id, false)
}

func readDetail(ctx context.Context, db *sql.DB, files CaptureFiles, id string, includeAssessment bool) (*Detail, *Error) {
	key, e := NormalizeID(id)
	if e != nil {
		return nil, e
	}
	var result *Detail
	err := snapshot(ctx, db, func(r *reader) *Error {
		records, err := selected(r, "SELECT "+columns+" FROM job_discoveries WHERE discovery_id=?", []any{key}, 1)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return failure("discoveries", "NOT_FOUND")
		}
		if err = enrichCapture(r, records, includeAssessment); err != nil {
			return err
		}
		result = &Detail{Record: records[0]}
		if result.SourceText, err = capturedText(files, result.Record); err != nil {
			return err
		}
		if r.ctx.Err() != nil {
			return failure("read", "READ_CANCELED")
		}
		if result.Observations, err = detailObservations(r, key); err != nil {
			return err
		}
		if result.EmailSource, err = emailSource(r, result.Observations); err != nil {
			return err
		}
		return checkResult(result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Internal artifact only. This is not the HTTP review_assessment payload and
// does not assert freshness against the current candidate/context revisions.
func ReadAssessment(ctx context.Context, db *sql.DB, id string) (*Assessment, *Error) {
	key, e := NormalizeID(id)
	if e != nil {
		return nil, e
	}
	var result *Assessment
	err := snapshot(ctx, db, func(r *reader) *Error {
		if err := r.rows("assessment", `SELECT discovery_id,content_sha256,analyzer_version,evaluator_version,candidate_revision,assessment_context_revision,assessment_version,scope,assessment_json,computed_at FROM job_discovery_assessments WHERE discovery_id=?`, []any{key}, 10, 1, func(raw []any) error {
			a := &Assessment{}
			for i, target := range map[int]*string{0: &a.DiscoveryID, 1: &a.ContentSHA256, 4: &a.CandidateRevision, 5: &a.AssessmentContextRevision, 7: &a.Scope, 9: &a.ComputedAt} {
				v, e := textCell(raw[i], false)
				if e != nil {
					return e
				}
				*target = v
			}
			for i, target := range map[int]*int64{2: &a.AnalyzerVersion, 3: &a.EvaluatorVersion, 6: &a.AssessmentVersion} {
				v, e := integerCell(raw[i])
				if e != nil {
					return e
				}
				*target = v
			}
			metadata, e := textCell(raw[8], false)
			if e != nil {
				return e
			}
			if a.Assessment, e = parseObject(metadata); e != nil {
				return failure("assessment", "ASSESSMENT_INVALID")
			}
			result = a
			return nil
		}); err != nil {
			return err
		}
		return checkResult(result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
