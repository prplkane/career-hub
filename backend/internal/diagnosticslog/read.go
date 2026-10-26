package diagnosticslog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/discovery/promotiondiagnostics"
	"careerhub.local/backend/internal/discovery/publicjobresolver"
	"careerhub.local/backend/internal/email/emaildiagnostics"
	"careerhub.local/backend/internal/intake/sourceupload"
	"careerhub.local/backend/internal/jobs/jobdiagnostics"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const legacyPath = "career_vault/logs/intake.jsonl"

// One request returns at most 128 events from one stream. Empty streams may
// each inspect one guarded 256 KiB window before the next stream is tried.
// A complete legacy line cannot exceed that window.
const MaxPageEvents = 128

const (
	nativeIntakePath       = "career_vault/logs/native-intake"
	nativeIntakeFileLimit  = 64 << 10
	emptyIntakeFingerprint = "e3b0c44298fc1c149afbf4c8996fb924"
)

var (
	legacyMergedCursor = regexp.MustCompile(`^dl1:([0-9]{1,10}):ne:([0-9]{6}):([0-9]{1,8})$`)
	mergedCursor       = regexp.MustCompile(`^dl2:([0-9]{1,10}):ne:([0-9]{6}):([0-9]{1,8}):nj:([0-9]{6}):([0-9]{1,8})$`)
	completeCursor     = regexp.MustCompile(`^dl3:([0-9]{1,10}):ne:([0-9]{6}):([0-9]{1,8}):nj:([0-9]{6}):([0-9]{1,8}):ni:([0-9]{1,4}):([0-9a-f]{32}):([0-9]{1,4})$`)
	nativeCursor       = regexp.MustCompile(`^ni:([0-9]{1,4}):([0-9a-f]{32}):([0-9]{1,4})$`)
	nativeIntakeName   = regexp.MustCompile(`^([0-9a-f]{32})\.json$`)
	nativeSourceID     = regexp.MustCompile(`^src_[a-z0-9_]{1,508}$`)
)

type Error struct{ Code string }

func (e *Error) Error() string   { return e.Code }
func failure(code string) *Error { return &Error{Code: code} }

type Page struct {
	Cursor  string            `json:"cursor"`
	Events  []json.RawMessage `json:"events"`
	HasMore bool              `json:"has_more"`
}

func cursor(legacyOffset int64, email, job, intake string) string {
	return fmt.Sprintf("dl3:%d:%s:%s:%s", legacyOffset, email, job, intake)
}

func initialIntakeCursor() string { return "ni:0:" + emptyIntakeFingerprint + ":0" }

func parseCursor(raw string) (int64, string, string, string, *Error) {
	if raw == "" || raw == "0" || raw == "byte:0" {
		return 0, "ne:000001:0", "nj:000001:0", initialIntakeCursor(), nil
	}
	match := completeCursor.FindStringSubmatch(raw)
	intake := initialIntakeCursor()
	if match != nil {
		intake = fmt.Sprintf("ni:%s:%s:%s", match[6], match[7], match[8])
	} else {
		match = mergedCursor.FindStringSubmatch(raw)
	}
	if match == nil {
		match = legacyMergedCursor.FindStringSubmatch(raw)
		if match == nil {
			return 0, "", "", "", failure("DIAGNOSTIC_CURSOR_INVALID")
		}
	}
	legacy, e1 := strconv.ParseInt(match[1], 10, 64)
	segment, e2 := strconv.Atoi(match[2])
	offset, e3 := strconv.ParseInt(match[3], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || legacy > runtimepaths.MaxLegacyLogBytes ||
		segment < 1 || segment > emaildiagnostics.MaxDiagnosticSegments ||
		offset > runtimepaths.MaxReadBytes {
		return 0, "", "", "", failure("DIAGNOSTIC_CURSOR_INVALID")
	}
	job := "nj:000001:0"
	if len(match) >= 6 {
		jobSegment, e4 := strconv.Atoi(match[4])
		jobOffset, e5 := strconv.ParseInt(match[5], 10, 64)
		if e4 != nil || e5 != nil || jobSegment < 1 || jobSegment > jobdiagnostics.MaxDiagnosticSegments || jobOffset > runtimepaths.MaxReadBytes {
			return 0, "", "", "", failure("DIAGNOSTIC_CURSOR_INVALID")
		}
		job = fmt.Sprintf("nj:%06d:%d", jobSegment, jobOffset)
	}
	if _, _, _, problem := parseIntakeCursor(intake); problem != nil {
		return 0, "", "", "", failure("DIAGNOSTIC_CURSOR_INVALID")
	}
	return legacy, fmt.Sprintf("ne:%06d:%d", segment, offset), job, intake, nil
}

func parseIntakeCursor(value string) (count int, fingerprint string, offset int, problem *Error) {
	match := nativeCursor.FindStringSubmatch(value)
	if match == nil {
		return 0, "", 0, failure("INTAKE_DIAGNOSTIC_CURSOR_INVALID")
	}
	count, countErr := strconv.Atoi(match[1])
	offset, offsetErr := strconv.Atoi(match[3])
	if countErr != nil || offsetErr != nil || count < 0 || count > runtimepaths.MaxDirectoryEntries || offset < 0 || offset > count ||
		(count == 0 && (match[2] != emptyIntakeFingerprint || offset != 0)) {
		return 0, "", 0, failure("INTAKE_DIAGNOSTIC_CURSOR_INVALID")
	}
	return count, match[2], offset, nil
}

func sameHome(root string, expected datahome.Info) bool {
	if !expected.Initialized || expected.DataHomeID == "" || expected.Channel == "" {
		return false
	}
	current, problem := datahome.Inspect(root, &expected.Channel)
	return problem == nil && current != nil && current.Initialized &&
		current.DataHomeID == expected.DataHomeID && current.FormatVersion == expected.FormatVersion
}

type legacyPage struct {
	offset, size int64
	events       []json.RawMessage
	hasMore      bool
}

func readLegacy(ctx context.Context, dataRoot string, expected datahome.Info, offset int64) (page legacyPage, problem *Error) {
	page.events = []json.RawMessage{}
	if ctx == nil || ctx.Err() != nil {
		return legacyPage{}, failure("DIAGNOSTIC_CANCELED")
	}
	release, err := operationgate.Acquire(ctx)
	if err != nil {
		return legacyPage{}, failure("DIAGNOSTIC_CANCELED")
	}
	defer release()
	if !sameHome(dataRoot, expected) {
		return legacyPage{}, failure("DIAGNOSTIC_HOME_CHANGED")
	}
	root, e := runtimepaths.Open(dataRoot)
	if e != nil {
		return legacyPage{}, failure("DIAGNOSTIC_LEGACY_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil {
			page, problem = legacyPage{}, failure("DIAGNOSTIC_LEGACY_UNAVAILABLE")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return legacyPage{}, failure("DIAGNOSTIC_OWNERSHIP_REQUIRED")
	}
	if offset > 0 {
		previous, size, found, e := root.ReadSingleLinkLegacyLogWindow(legacyPath, offset-1, 1)
		if e != nil {
			return legacyPage{}, legacyReadError(e)
		}
		if !found || offset > size {
			return legacyPage{}, failure("DIAGNOSTIC_CURSOR_EXPIRED")
		}
		if len(previous) != 1 || previous[0] != '\n' {
			return legacyPage{}, failure("DIAGNOSTIC_CURSOR_INVALID")
		}
		page.size = size
	}
	window, size, found, e := root.ReadSingleLinkLegacyLogWindow(legacyPath, offset, runtimepaths.MaxWindowReadBytes)
	if e != nil {
		return legacyPage{}, legacyReadError(e)
	}
	if !found {
		if offset != 0 {
			return legacyPage{}, failure("DIAGNOSTIC_CURSOR_EXPIRED")
		}
		return page, nil
	}
	if page.size != 0 && page.size != size {
		return legacyPage{}, failure("DIAGNOSTIC_LEGACY_CHANGED")
	}
	page.size = size
	if len(window) == 0 {
		page.offset = offset
		return page, nil
	}
	completeEnd := bytes.LastIndexByte(window, '\n') + 1
	if completeEnd == 0 && int64(len(window)) == runtimepaths.MaxWindowReadBytes {
		return legacyPage{}, failure("DIAGNOSTIC_LEGACY_LINE_TOO_LARGE")
	}
	consumed := 0
	for consumed < completeEnd && len(page.events) < MaxPageEvents {
		lineEnd := consumed + bytes.IndexByte(window[consumed:completeEnd], '\n') + 1
		line := bytes.TrimSpace(window[consumed : lineEnd-1])
		if len(line) == 0 || !utf8.Valid(line) || !json.Valid(line) || line[0] != '{' {
			return legacyPage{}, failure("DIAGNOSTIC_LEGACY_CORRUPT")
		}
		page.events = append(page.events, append(json.RawMessage(nil), line...))
		consumed = lineEnd
	}
	// A second guarded observation rejects an append/replacement between the
	// cursor-boundary check and the window read; never expose a mixed page.
	_, observedSize, observed, e := root.ReadSingleLinkLegacyLogWindow(legacyPath, size, 1)
	if ctx.Err() != nil {
		return legacyPage{}, failure("DIAGNOSTIC_CANCELED")
	}
	if e != nil || !observed || observedSize != size || !sameHome(dataRoot, expected) {
		return legacyPage{}, failure("DIAGNOSTIC_LEGACY_CHANGED")
	}
	page.offset = offset + int64(consumed)
	// An incomplete final line does not hold the native stream hostage. It
	// remains unread until the legacy owner completes it.
	page.hasMore = page.offset < size && (len(page.events) == MaxPageEvents || int64(len(window)) == runtimepaths.MaxWindowReadBytes)
	return page, nil
}

func legacyReadError(e *runtimepaths.Error) *Error {
	switch e.Code {
	case "READ_OFFSET_INVALID":
		return failure("DIAGNOSTIC_CURSOR_EXPIRED")
	case "FILE_SIZE_LIMIT":
		return failure("DIAGNOSTIC_LEGACY_LIMIT")
	default:
		return failure("DIAGNOSTIC_LEGACY_UNAVAILABLE")
	}
}

type nativeIntakeRecord struct {
	SchemaVersion int    `json:"schema_version"`
	OperationID   string `json:"operation_id"`
	SourceID      string `json:"source_id"`
	State         string `json:"state"`
	Stage         string `json:"stage"`
	ErrorCode     string `json:"error_code"`
}

type nativeIntakeEvent struct {
	SchemaVersion int               `json:"schema_version"`
	Timestamp     string            `json:"timestamp"`
	RunID         string            `json:"run_id"`
	Sequence      int               `json:"sequence"`
	Component     string            `json:"component"`
	Source        string            `json:"source"`
	Stage         string            `json:"stage"`
	Event         string            `json:"event"`
	Status        string            `json:"status"`
	Details       map[string]string `json:"details"`
}

type nativeIntakePage struct {
	cursor  string
	events  []json.RawMessage
	hasMore bool
}

func sortNativeIntakeMetadata(entries []runtimepaths.FileMetadata) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Modified.Equal(entries[j].Modified) {
			return entries[i].Name < entries[j].Name
		}
		return entries[i].Modified.Before(entries[j].Modified)
	})
}

func nativeIntakeFingerprint(entries []runtimepaths.FileMetadata) string {
	hash := sha256.New()
	for _, entry := range entries {
		fmt.Fprintf(hash, "%s\x00%d\x00%d\x00", entry.Name, entry.Size, entry.Modified.UnixNano())
	}
	return fmt.Sprintf("%x", hash.Sum(nil))[:32]
}

func formatIntakeCursor(entries []runtimepaths.FileMetadata, offset int) string {
	return fmt.Sprintf("ni:%d:%s:%d", len(entries), nativeIntakeFingerprint(entries), offset)
}

func validNativeIntakeMetadata(entries []runtimepaths.FileMetadata) bool {
	for _, entry := range entries {
		if nativeIntakeName.FindStringSubmatch(entry.Name) == nil || entry.Size < 2 || entry.Size > nativeIntakeFileLimit || entry.Modified.IsZero() {
			return false
		}
	}
	return true
}

func projectNativeIntake(raw []byte, entry runtimepaths.FileMetadata) (json.RawMessage, bool) {
	if record, err := publicjobresolver.DecodeDiagnostic(raw); err == nil {
		if entry.Name != record.OperationID+".json" {
			return nil, false
		}
		event := nativeIntakeEvent{SchemaVersion: 1, Timestamp: entry.Modified.UTC().Format(time.RFC3339Nano), RunID: record.RunID, Component: record.Operation, Source: "Job Feed", Stage: record.Stage, Sequence: record.Sequence, Event: "started", Status: "running", Details: map[string]string{
			"operation": record.Operation, "committed": strconv.FormatBool(record.Committed), "commit_unknown": strconv.FormatBool(record.CommitUnknown), "partial": strconv.FormatBool(record.Partial), "recovery_required": strconv.FormatBool(record.RecoveryRequired), "cleanup_failed": strconv.FormatBool(record.CleanupFailed), "summary": "Public resolution is in progress or interrupted; no completion was recorded.",
		}}
		if record.State == "success" {
			event.Event = "completed"
			event.Status = "success"
			event.Details["summary"] = "Public posting content was retained for review."
			event.Details["outcome"] = record.Outcome
			event.Details["strategy"] = record.Strategy
			event.Details["redirect_count"] = strconv.Itoa(record.RedirectCount)
			event.Details["response_bytes"] = strconv.Itoa(record.ResponseBytes)
		} else if record.State == "failed" {
			event.Event = "failed"
			event.Status = "error"
			event.Details["error_code"] = record.ErrorCode
			event.Details["summary"] = "Public resolution did not complete; inspect the opportunity before retrying."
		}
		projected, err := json.Marshal(event)
		return projected, err == nil
	}
	if record, err := promotiondiagnostics.Decode(raw); err == nil {
		if entry.Name != record.OperationID+".json" {
			return nil, false
		}
		event := nativeIntakeEvent{SchemaVersion: 1, Timestamp: entry.Modified.UTC().Format(time.RFC3339Nano),
			RunID: record.RunID, Component: "discovery_promotion", Source: "Job Feed", Stage: record.Stage,
			Sequence: record.Sequence, Event: "started", Status: "running",
			Details: map[string]string{"operation": record.Operation, "action": record.Action,
				"child_run_id": record.ChildRunID, "committed": strconv.FormatBool(record.Committed),
				"commit_unknown": strconv.FormatBool(record.CommitUnknown), "partial": strconv.FormatBool(record.Partial),
				"recovery_required": strconv.FormatBool(record.RecoveryRequired), "cleanup_failed": strconv.FormatBool(record.CleanupFailed),
				"summary": "Promotion is in progress or interrupted; no completion was recorded. Check saved state before retrying."}}
		if record.State == "success" {
			event.Event, event.Status = "completed", "success"
			event.Details["summary"] = "Job promotion completed."
		} else if record.State == "failed" {
			event.Event, event.Status = "failed", "error"
			event.Details["error_code"] = record.ErrorCode
			event.Details["summary"] = "Job promotion did not complete; previously saved changes may remain."
		}
		projected, err := json.Marshal(event)
		return projected, err == nil
	}
	if record, err := sourceupload.Decode(raw); err == nil {
		if entry.Name != record.OperationID+".json" {
			return nil, false
		}
		event := nativeIntakeEvent{SchemaVersion: 1, Timestamp: entry.Modified.UTC().Format(time.RFC3339Nano),
			RunID: record.RunID, Component: "career_vault_intake", Source: "Resume upload", Stage: "source_upload",
			Sequence: 2, Event: "completed", Status: "success",
			Details: map[string]string{"operation": "source_upload", "publication": record.State,
				"commit_attempted": strconv.FormatBool(record.CommitAttempted), "committed": strconv.FormatBool(record.Committed)}}
		if record.Operation == "source_extract" {
			event.Source, event.Stage = "Resume extraction", "source_extraction"
			event.Details["operation"] = "source_extract"
		}
		if record.Operation == "review_save" {
			event.Source, event.Stage = "Resume review", "review_ui_save"
			event.Details["operation"] = "review_save"
		}
		if record.Operation == "approval_save" || record.Operation == "approval_recover" {
			event.Source, event.Stage = "Resume approval", record.Operation
			event.Details["operation"] = record.Operation
		}
		if record.Operation == "source_remove" {
			event.Source, event.Stage = "Resume source cleanup", "source_remove"
			event.Details["operation"] = "source_remove"
		}
		if record.State == "started" {
			event.Sequence, event.Event, event.Status = 1, "started", "running"
		}
		if record.State == "failed" || record.State == "unknown" {
			event.Event, event.Status = "failed", "error"
			event.Details["error_code"] = record.ErrorCode
		}
		projected, err := json.Marshal(event)
		return projected, err == nil
	}
	if len(raw) < 2 || len(raw) > nativeIntakeFileLimit || !utf8.Valid(raw) || bytes.Count(raw, []byte{'\n'}) != 0 {
		return nil, false
	}
	var record nativeIntakeRecord
	if json.Unmarshal(raw, &record) != nil || record.SchemaVersion != 1 || !nativeSourceID.MatchString(record.SourceID) {
		return nil, false
	}
	match := nativeIntakeName.FindStringSubmatch(entry.Name)
	if match == nil || record.OperationID != match[1] {
		return nil, false
	}
	canonical, err := json.Marshal(record)
	if err != nil || !bytes.Equal(canonical, raw) {
		return nil, false
	}
	event := nativeIntakeEvent{SchemaVersion: 1, Timestamp: entry.Modified.UTC().Format(time.RFC3339Nano),
		RunID: record.OperationID, Component: "career_vault_intake", Source: "Resume digest", Stage: "source_upload"}
	switch record.State {
	case "publishing":
		if record.Stage != "publication" || record.ErrorCode != "" {
			return nil, false
		}
		event.Sequence, event.Event, event.Status = 1, "started", "running"
		event.Details = map[string]string{"operation": "resume_digest", "publication": "publishing"}
	case "success":
		if record.Stage != "completed" || record.ErrorCode != "" {
			return nil, false
		}
		event.Sequence, event.Event, event.Status = 2, "completed", "success"
		event.Details = map[string]string{"operation": "resume_digest", "publication": "published"}
	case "aborted_before_raw":
		if record.Stage != "completed" || record.ErrorCode != "INTAKE_WITNESS_CHANGED" {
			return nil, false
		}
		event.Sequence, event.Event, event.Status = 2, "failed", "error"
		event.Details = map[string]string{"operation": "resume_digest", "publication": "not_published",
			"error_code": record.ErrorCode, "summary": "Resume digest stopped before publication."}
	default:
		return nil, false
	}
	projected, err := json.Marshal(event)
	return projected, err == nil
}

func sameNativeIntakeMetadata(left, right []runtimepaths.FileMetadata) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Name != right[index].Name || left[index].Size != right[index].Size || !left[index].Modified.Equal(right[index].Modified) {
			return false
		}
	}
	return true
}

func readNativeIntake(ctx context.Context, dataRoot string, expected datahome.Info, after string) (page nativeIntakePage, problem *Error) {
	page.events = []json.RawMessage{}
	previousCount, previousFingerprint, offset, problem := parseIntakeCursor(after)
	if problem != nil {
		return nativeIntakePage{}, problem
	}
	if ctx == nil || ctx.Err() != nil {
		return nativeIntakePage{}, failure("INTAKE_DIAGNOSTIC_CANCELED")
	}
	release, err := operationgate.Acquire(ctx)
	if err != nil {
		return nativeIntakePage{}, failure("INTAKE_DIAGNOSTIC_CANCELED")
	}
	defer release()
	if !sameHome(dataRoot, expected) {
		return nativeIntakePage{}, failure("INTAKE_DIAGNOSTIC_HOME_CHANGED")
	}
	root, openErr := runtimepaths.Open(dataRoot)
	if openErr != nil {
		return nativeIntakePage{}, failure("INTAKE_DIAGNOSTIC_READ_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil && problem == nil {
			page, problem = nativeIntakePage{}, failure("INTAKE_DIAGNOSTIC_CLOSE_FAILED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return nativeIntakePage{}, failure("INTAKE_DIAGNOSTIC_OWNERSHIP_REQUIRED")
	}
	entries, present, listErr := root.ListDirectoryFileMetadata(nativeIntakePath)
	if listErr != nil {
		return nativeIntakePage{}, failure("INTAKE_DIAGNOSTIC_DIRECTORY_INVALID")
	}
	if !present {
		entries = []runtimepaths.FileMetadata{}
	}
	sortNativeIntakeMetadata(entries)
	if !validNativeIntakeMetadata(entries) {
		return nativeIntakePage{}, failure("INTAKE_DIAGNOSTIC_DIRECTORY_INVALID")
	}
	if previousCount > len(entries) {
		return nativeIntakePage{}, failure("INTAKE_DIAGNOSTIC_CURSOR_EXPIRED")
	}
	if previousCount > 0 && nativeIntakeFingerprint(entries[:previousCount]) != previousFingerprint {
		offset = 0
	}
	end := offset + MaxPageEvents
	if end > len(entries) {
		end = len(entries)
	}
	for _, entry := range entries[offset:end] {
		raw, found, readErr := root.ReadSingleLinkFile(nativeIntakePath+"/"+entry.Name, nativeIntakeFileLimit)
		if readErr != nil || !found {
			return nativeIntakePage{}, failure("INTAKE_DIAGNOSTIC_READ_UNAVAILABLE")
		}
		event, valid := projectNativeIntake(raw, entry)
		if !valid {
			return nativeIntakePage{}, failure("INTAKE_DIAGNOSTIC_RECOVERY_REQUIRED")
		}
		page.events = append(page.events, event)
	}
	again, stillPresent, againErr := root.ListDirectoryFileMetadata(nativeIntakePath)
	if againErr != nil || stillPresent != present {
		return nativeIntakePage{}, failure("INTAKE_DIAGNOSTIC_READ_UNAVAILABLE")
	}
	if !stillPresent {
		again = []runtimepaths.FileMetadata{}
	}
	sortNativeIntakeMetadata(again)
	if ctx.Err() != nil {
		return nativeIntakePage{}, failure("INTAKE_DIAGNOSTIC_CANCELED")
	}
	if !sameNativeIntakeMetadata(entries, again) || !sameHome(dataRoot, expected) {
		return nativeIntakePage{}, failure("INTAKE_DIAGNOSTIC_READ_UNAVAILABLE")
	}
	page.cursor = formatIntakeCursor(entries, end)
	page.hasMore = end < len(entries)
	return page, nil
}

// ReadPage orders immutable legacy Python, native email and native saved-job
// history plus bounded per-run Resume Digest diagnostics without sharing a
// writable file. Older cursor versions are upgraded with missing streams at
// their beginning. HTTP owns authentication.
func ReadPage(ctx context.Context, dataRoot string, expected datahome.Info, after string) (Page, *Error) {
	legacyOffset, emailCursor, jobCursor, intakeCursor, problem := parseCursor(after)
	if problem != nil {
		return Page{}, problem
	}
	legacy, problem := readLegacy(ctx, dataRoot, expected, legacyOffset)
	if problem != nil {
		return Page{}, problem
	}
	if legacy.hasMore || len(legacy.events) != 0 {
		return Page{Cursor: cursor(legacy.offset, emailCursor, jobCursor, intakeCursor), Events: legacy.events, HasMore: true}, nil
	}
	email, emailProblem := emaildiagnostics.ReadPage(ctx, dataRoot, expected, emailCursor)
	if emailProblem != nil {
		return Page{}, failure(emailProblem.Code)
	}
	if email.HasMore || len(email.Events) != 0 {
		return Page{Cursor: cursor(legacy.offset, email.Cursor, jobCursor, intakeCursor), Events: email.Events, HasMore: true}, nil
	}
	job, jobProblem := jobdiagnostics.ReadPage(ctx, dataRoot, expected, jobCursor)
	if jobProblem != nil {
		return Page{}, failure(jobProblem.Code)
	}
	if job.HasMore || len(job.Events) != 0 {
		return Page{Cursor: cursor(legacy.offset, email.Cursor, job.Cursor, intakeCursor), Events: job.Events, HasMore: true}, nil
	}
	intake, intakeProblem := readNativeIntake(ctx, dataRoot, expected, intakeCursor)
	if intakeProblem != nil {
		return Page{}, intakeProblem
	}
	return Page{Cursor: cursor(legacy.offset, email.Cursor, job.Cursor, intake.cursor), Events: intake.events, HasMore: intake.hasMore}, nil
}
