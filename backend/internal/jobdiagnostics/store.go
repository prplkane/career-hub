package jobdiagnostics

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
)

const (
	segmentDirectory      = "career_vault/logs/native-jobs"
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
func formatSegmentName(number int) string  { return fmt.Sprintf("segment-%06d.jsonl", number) }

func sameHome(root string, expected datahome.Info) bool {
	if !expected.Initialized || expected.DataHomeID == "" || expected.Channel == "" {
		return false
	}
	current, problem := datahome.Inspect(root, &expected.Channel)
	return problem == nil && current != nil && current.Initialized &&
		current.DataHomeID == expected.DataHomeID && current.FormatVersion == expected.FormatVersion
}

func nextSegment(names []string) (int, *StoreError) {
	last := 0
	seen := make(map[int]bool, len(names))
	for _, name := range names {
		match := segmentPattern.FindStringSubmatch(name)
		if match == nil {
			return 0, storeFailure("JOB_DIAGNOSTIC_DIRECTORY_INVALID")
		}
		number, err := strconv.Atoi(match[1])
		if err != nil || number < 1 || number > MaxDiagnosticSegments || seen[number] {
			return 0, storeFailure("JOB_DIAGNOSTIC_DIRECTORY_INVALID")
		}
		seen[number] = true
		if number > last {
			last = number
		}
	}
	if last != len(names) {
		return 0, storeFailure("JOB_DIAGNOSTIC_DIRECTORY_INVALID")
	}
	return last, nil
}

func tailEvent(previous []byte) (Decoded, *StoreError) {
	if len(previous) == 0 || previous[len(previous)-1] != '\n' {
		return Decoded{}, storeFailure("JOB_DIAGNOSTIC_RECOVERY_REQUIRED")
	}
	start := bytes.LastIndexByte(previous[:len(previous)-1], '\n') + 1
	last, err := Decode(previous[start:])
	if err != nil {
		return Decoded{}, storeFailure("JOB_DIAGNOSTIC_RECOVERY_REQUIRED")
	}
	return last, nil
}

func contiguous(previous *Decoded, current Decoded) bool {
	if previous == nil {
		return current.Sequence == 1 && current.Event == "started"
	}
	if previous.Event == "started" {
		if current.RunID != previous.RunID || current.Operation != previous.Operation || current.Sequence != 2 {
			return false
		}
		switch previous.Operation {
		case "reanalyze":
			return current.JobKeySHA256 == previous.JobKeySHA256 &&
				(current.Event == "planned" || current.Event == "failed" || current.Event == "completed" && current.Status == "success" && !current.CommitAttempted && !current.Committed)
		case "intake":
			return current.SourceKeySHA256 == previous.SourceKeySHA256 &&
				(current.Event == "dispatched" || current.Event == "failed" && current.ErrorCode == "JOB_INTAKE_INTERRUPTED")
		default:
			return false
		}
	}
	if previous.Event == "planned" {
		return previous.Operation == "reanalyze" && current.Operation == "reanalyze" &&
			current.JobKeySHA256 == previous.JobKeySHA256 && current.RunID == previous.RunID && current.Sequence == 3 &&
			(current.Event == "completed" || current.Event == "failed")
	}
	if previous.Event == "dispatched" {
		return previous.Operation == "intake" && current.Operation == "intake" &&
			current.SourceKeySHA256 == previous.SourceKeySHA256 && current.RunID == previous.RunID && current.Sequence == 3 &&
			(current.Event == "completed" || current.Event == "failed")
	}
	return current.RunID != previous.RunID && current.Sequence == 1 && current.Event == "started"
}

// Append publishes one validated, fixed event into a journal separate from
// Python intake.jsonl and native-email. The caller supplies a startup Home pin
// and must serialize one entire run; the store refuses interleaved sequences.
// No segment is deleted or truncated. A failed or uncertain append before a
// planned event is not permission to replace a job card.
func Append(ctx context.Context, dataRoot string, expected datahome.Info, event Event) (out AppendOutcome, problem *StoreError) {
	if ctx == nil || ctx.Err() != nil {
		return out, storeFailure("JOB_DIAGNOSTIC_CANCELED")
	}
	decoded, err := Decode(event.raw)
	if err != nil {
		return out, storeFailure("JOB_DIAGNOSTIC_EVENT_INVALID")
	}
	required := int64(len(event.raw))
	if decoded.Event == "started" {
		required += 2 * MaxEventBytes
	} else if decoded.Event == "planned" || decoded.Event == "dispatched" {
		required += MaxEventBytes
	}
	release, gateErr := operationgate.Acquire(ctx)
	if gateErr != nil {
		return out, storeFailure("JOB_DIAGNOSTIC_CANCELED")
	}
	defer release()
	if !sameHome(dataRoot, expected) {
		return out, storeFailure("JOB_DIAGNOSTIC_HOME_CHANGED")
	}
	root, openErr := runtimepaths.Open(dataRoot)
	if openErr != nil {
		return out, storeFailure("JOB_DIAGNOSTIC_STORE_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil && problem == nil {
			problem = storeFailure("JOB_DIAGNOSTIC_CLOSE_FAILED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return out, storeFailure("JOB_DIAGNOSTIC_OWNERSHIP_REQUIRED")
	}
	if _, e := root.EnsureDirectory(segmentDirectory); e != nil {
		return out, storeFailure("JOB_DIAGNOSTIC_STORE_UNAVAILABLE")
	}
	segments, openErr := runtimepaths.Open(filepath.Join(dataRoot, filepath.FromSlash(segmentDirectory)))
	if openErr != nil {
		return out, storeFailure("JOB_DIAGNOSTIC_STORE_UNAVAILABLE")
	}
	defer func() {
		if segments.Close() != nil && problem == nil {
			problem = storeFailure("JOB_DIAGNOSTIC_CLOSE_FAILED")
		}
	}()
	names, listErr := segments.ListFileNames()
	if listErr != nil || len(names) > MaxDiagnosticSegments {
		return out, storeFailure("JOB_DIAGNOSTIC_DIRECTORY_INVALID")
	}
	last, nameErr := nextSegment(names)
	if nameErr != nil {
		return out, nameErr
	}
	var previous []byte
	if last != 0 {
		var found bool
		var readErr *runtimepaths.Error
		previous, found, readErr = segments.ReadSingleLinkFile(formatSegmentName(last), runtimepaths.MaxReadBytes)
		if readErr != nil || !found {
			return out, storeFailure("JOB_DIAGNOSTIC_STORE_UNAVAILABLE")
		}
		lastEvent, tailErr := tailEvent(previous)
		if tailErr != nil {
			return out, tailErr
		}
		if !contiguous(&lastEvent, decoded) {
			return out, storeFailure("JOB_DIAGNOSTIC_SEQUENCE_INVALID")
		}
	} else if !contiguous(nil, decoded) {
		return out, storeFailure("JOB_DIAGNOSTIC_SEQUENCE_INVALID")
	}
	var writeErr *runtimepaths.Error
	if last == 0 {
		out.Committed, writeErr = segments.CreateFile(formatSegmentName(1), event.raw)
	} else if int64(len(previous))+required <= runtimepaths.MaxReadBytes {
		combined := make([]byte, 0, len(previous)+len(event.raw))
		combined = append(combined, previous...)
		combined = append(combined, event.raw...)
		out.Committed, writeErr = segments.ReplaceFile(formatSegmentName(last), previous, combined)
	} else if last < MaxDiagnosticSegments {
		out.Committed, writeErr = segments.CreateFile(formatSegmentName(last+1), event.raw)
	} else {
		return out, storeFailure("JOB_DIAGNOSTIC_LIMIT")
	}
	if writeErr != nil {
		if out.Committed {
			return out, storeFailure("JOB_DIAGNOSTIC_POSTCOMMIT_FAILED")
		}
		out.Uncertain = true
		return out, storeFailure("JOB_DIAGNOSTIC_COMMIT_UNKNOWN")
	}
	if !out.Committed {
		out.Uncertain = true
		return out, storeFailure("JOB_DIAGNOSTIC_COMMIT_UNKNOWN")
	}
	if !sameHome(dataRoot, expected) {
		return out, storeFailure("JOB_DIAGNOSTIC_HOME_CHANGED")
	}
	return out, nil
}
