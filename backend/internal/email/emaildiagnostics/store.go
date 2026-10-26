package emaildiagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const (
	segmentDirectory      = "career_vault/logs/native-email"
	MaxDiagnosticSegments = 8
)

var segmentPattern = regexp.MustCompile(`^segment-([0-9]{6})\.jsonl$`)

type StoreError struct{ Code string }

func (e *StoreError) Error() string { return e.Code }

type AppendOutcome struct {
	Committed bool
	Uncertain bool
}

func storeFailure(code string) *StoreError { return &StoreError{Code: code} }

func formatSegmentName(number int) string { return fmt.Sprintf("segment-%06d.jsonl", number) }

func sameHome(root string, expected datahome.Info) bool {
	if !expected.Initialized || expected.DataHomeID == "" || expected.Channel == "" {
		return false
	}
	current, problem := datahome.Inspect(root, &expected.Channel)
	return problem == nil && current != nil && current.Initialized &&
		current.DataHomeID == expected.DataHomeID && current.FormatVersion == expected.FormatVersion
}

func decodeEvent(raw []byte) (record, bool) {
	if len(raw) == 0 || len(raw) > MaxEventBytes || raw[len(raw)-1] != '\n' || bytes.Count(raw, []byte{'\n'}) != 1 {
		return record{}, false
	}
	var event record
	if json.Unmarshal(raw[:len(raw)-1], &event) != nil || event.SchemaVersion != 1 ||
		!ValidRunID(event.RunID) || event.Sequence < 1 || event.Sequence > 2 ||
		event.Component != "career_vault_intake" || !validRecordDetails(event) {
		return record{}, false
	}
	canonical, err := json.Marshal(event)
	if err != nil || !bytes.Equal(canonical, raw[:len(raw)-1]) {
		return record{}, false
	}
	if event.Sequence == 1 {
		return event, event.Event == "started" && event.Status == "running"
	}
	return event, (event.Event == "completed" && (event.Status == "success" || event.Status == "success_with_warnings")) ||
		(event.Event == "failed" && event.Status == "error")
}

func validEvent(raw []byte) bool { _, valid := decodeEvent(raw); return valid }

func nextSegment(names []string) (string, error) {
	last := 0
	seen := make(map[int]bool, len(names))
	for _, name := range names {
		match := segmentPattern.FindStringSubmatch(name)
		if match == nil {
			return "", storeFailure("EMAIL_DIAGNOSTIC_DIRECTORY_INVALID")
		}
		number, err := strconv.Atoi(match[1])
		if err != nil || number < 1 || seen[number] {
			return "", storeFailure("EMAIL_DIAGNOSTIC_DIRECTORY_INVALID")
		}
		seen[number] = true
		if number > last {
			last = number
		}
	}
	// Rotation never deletes a segment. A gap indicates removal/tampering,
	// not permission to silently continue from the highest surviving file.
	if last != len(names) {
		return "", storeFailure("EMAIL_DIAGNOSTIC_DIRECTORY_INVALID")
	}
	if last == 0 {
		return formatSegmentName(1), nil
	}
	return formatSegmentName(last), nil
}

// Append writes one immutable JSONL event into a separate native journal.
// Existing Python intake.jsonl bytes are never opened for mutation. A full
// segment rolls to the next fixed name, up to eight 16 MiB segments (128 MiB
// maximum). No segment is truncated or deleted; capacity exhaustion refuses
// a new run rather than silently erasing diagnostic history.
// The process-owning caller supplies the startup Home marker as an immutable
// identity pin. All publication facts survive later cleanup/admission errors.
func Append(ctx context.Context, dataRoot string, expected datahome.Info, event Event) (out AppendOutcome, problem *StoreError) {
	if ctx == nil || ctx.Err() != nil {
		return out, storeFailure("EMAIL_DIAGNOSTIC_CANCELED")
	}
	decoded, valid := decodeEvent(event.raw)
	if !valid {
		return out, storeFailure("EMAIL_DIAGNOSTIC_EVENT_INVALID")
	}
	required := int64(len(event.raw))
	if decoded.Sequence == 1 {
		// Reserve the largest possible terminal record before touching the
		// mailbox. The email gate admits only one sync run at a time.
		required += MaxEventBytes
	}
	release, err := operationgate.Acquire(ctx)
	if err != nil {
		return out, storeFailure("EMAIL_DIAGNOSTIC_CANCELED")
	}
	defer release()
	if !sameHome(dataRoot, expected) {
		return out, storeFailure("EMAIL_DIAGNOSTIC_HOME_CHANGED")
	}
	root, e := runtimepaths.Open(dataRoot)
	if e != nil {
		return out, storeFailure("EMAIL_DIAGNOSTIC_STORE_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil && problem == nil {
			problem = storeFailure("EMAIL_DIAGNOSTIC_CLOSE_FAILED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return out, storeFailure("EMAIL_DIAGNOSTIC_OWNERSHIP_REQUIRED")
	}
	if _, e = root.EnsureDirectory(segmentDirectory); e != nil {
		return out, storeFailure("EMAIL_DIAGNOSTIC_STORE_UNAVAILABLE")
	}
	segments, e := runtimepaths.Open(filepath.Join(dataRoot, filepath.FromSlash(segmentDirectory)))
	if e != nil {
		return out, storeFailure("EMAIL_DIAGNOSTIC_STORE_UNAVAILABLE")
	}
	defer func() {
		if segments.Close() != nil && problem == nil {
			problem = storeFailure("EMAIL_DIAGNOSTIC_CLOSE_FAILED")
		}
	}()
	names, e := segments.ListFileNames()
	if e != nil {
		return out, storeFailure("EMAIL_DIAGNOSTIC_DIRECTORY_INVALID")
	}
	if len(names) > MaxDiagnosticSegments {
		return out, storeFailure("EMAIL_DIAGNOSTIC_LIMIT")
	}
	name, err := nextSegment(names)
	if err != nil {
		return out, storeFailure("EMAIL_DIAGNOSTIC_DIRECTORY_INVALID")
	}
	if len(names) == 0 {
		out.Committed, e = segments.CreateFile(name, event.raw)
	} else {
		previous, found, readError := segments.ReadSingleLinkFile(name, runtimepaths.MaxReadBytes)
		if readError != nil || !found {
			return out, storeFailure("EMAIL_DIAGNOSTIC_STORE_UNAVAILABLE")
		}
		if len(previous) == 0 || previous[len(previous)-1] != '\n' {
			return out, storeFailure("EMAIL_DIAGNOSTIC_RECOVERY_REQUIRED")
		}
		if int64(len(previous))+required <= runtimepaths.MaxReadBytes {
			combined := make([]byte, 0, len(previous)+len(event.raw))
			combined = append(combined, previous...)
			combined = append(combined, event.raw...)
			out.Committed, e = segments.ReplaceFile(name, previous, combined)
		} else {
			match := segmentPattern.FindStringSubmatch(name)
			number, numberError := strconv.Atoi(match[1])
			if numberError != nil || number >= MaxDiagnosticSegments {
				return out, storeFailure("EMAIL_DIAGNOSTIC_LIMIT")
			}
			out.Committed, e = segments.CreateFile(formatSegmentName(number+1), event.raw)
		}
	}
	if e != nil {
		if out.Committed {
			return out, storeFailure("EMAIL_DIAGNOSTIC_POSTCOMMIT_FAILED")
		}
		return out, storeFailure("EMAIL_DIAGNOSTIC_STORE_UNAVAILABLE")
	}
	if !out.Committed {
		out.Uncertain = true
		return out, storeFailure("EMAIL_DIAGNOSTIC_COMMIT_UNKNOWN")
	}
	if !sameHome(dataRoot, expected) {
		return out, storeFailure("EMAIL_DIAGNOSTIC_HOME_CHANGED")
	}
	return out, nil
}
