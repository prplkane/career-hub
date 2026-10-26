package jobdiagnostics

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

const MaxPageEvents = 128

var cursorPattern = regexp.MustCompile(`^nj:([0-9]{6}):([0-9]{1,8})$`)

type Page struct {
	Cursor  string            `json:"cursor"`
	Events  []json.RawMessage `json:"events"`
	HasMore bool              `json:"has_more"`
}

func formatCursor(number int, offset int64) string { return fmt.Sprintf("nj:%06d:%d", number, offset) }

func parseCursor(value string) (number int, offset int64, initial bool, problem *StoreError) {
	if value == "" || value == "0" {
		return 1, 0, true, nil
	}
	match := cursorPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, 0, false, storeFailure("JOB_DIAGNOSTIC_CURSOR_INVALID")
	}
	number, err := strconv.Atoi(match[1])
	if err != nil || number < 1 || number > MaxDiagnosticSegments {
		return 0, 0, false, storeFailure("JOB_DIAGNOSTIC_CURSOR_INVALID")
	}
	offset, err = strconv.ParseInt(match[2], 10, 64)
	if err != nil || offset < 0 || offset > runtimepaths.MaxReadBytes {
		return 0, 0, false, storeFailure("JOB_DIAGNOSTIC_CURSOR_INVALID")
	}
	return number, offset, false, nil
}

// ReadPage is bounded to one 256 KiB single-link window and 128 complete
// events. Authentication belongs to its caller; Home identity and process
// ownership are checked here. A damaged tail refuses the entire page.
func ReadPage(ctx context.Context, dataRoot string, expected datahome.Info, after string) (page Page, problem *StoreError) {
	page.Events = []json.RawMessage{}
	if ctx == nil || ctx.Err() != nil {
		return Page{}, storeFailure("JOB_DIAGNOSTIC_CANCELED")
	}
	number, offset, initial, problem := parseCursor(after)
	if problem != nil {
		return Page{}, problem
	}
	release, err := operationgate.Acquire(ctx)
	if err != nil {
		return Page{}, storeFailure("JOB_DIAGNOSTIC_CANCELED")
	}
	defer release()
	defer func() {
		if problem != nil {
			return
		}
		if ctx.Err() != nil {
			page, problem = Page{}, storeFailure("JOB_DIAGNOSTIC_CANCELED")
		} else if !sameHome(dataRoot, expected) {
			page, problem = Page{}, storeFailure("JOB_DIAGNOSTIC_HOME_CHANGED")
		}
	}()
	if !sameHome(dataRoot, expected) {
		return Page{}, storeFailure("JOB_DIAGNOSTIC_HOME_CHANGED")
	}
	root, openErr := runtimepaths.Open(dataRoot)
	if openErr != nil {
		return Page{}, storeFailure("JOB_DIAGNOSTIC_STORE_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil {
			page, problem = Page{}, storeFailure("JOB_DIAGNOSTIC_CLOSE_FAILED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return Page{}, storeFailure("JOB_DIAGNOSTIC_OWNERSHIP_REQUIRED")
	}
	exists, existsErr := root.Exists(segmentDirectory)
	if existsErr != nil {
		return Page{}, storeFailure("JOB_DIAGNOSTIC_DIRECTORY_INVALID")
	}
	if !exists {
		if !initial && (number != 1 || offset != 0) {
			return Page{}, storeFailure("JOB_DIAGNOSTIC_CURSOR_EXPIRED")
		}
		page.Cursor = formatCursor(1, 0)
		return page, nil
	}
	segments, openErr := runtimepaths.Open(filepath.Join(dataRoot, filepath.FromSlash(segmentDirectory)))
	if openErr != nil {
		return Page{}, storeFailure("JOB_DIAGNOSTIC_DIRECTORY_INVALID")
	}
	defer func() {
		if segments.Close() != nil {
			page, problem = Page{}, storeFailure("JOB_DIAGNOSTIC_CLOSE_FAILED")
		}
	}()
	names, listErr := segments.ListFileNames()
	if listErr != nil || len(names) > MaxDiagnosticSegments {
		return Page{}, storeFailure("JOB_DIAGNOSTIC_DIRECTORY_INVALID")
	}
	last, nameErr := nextSegment(names)
	if nameErr != nil {
		return Page{}, nameErr
	}
	if last == 0 {
		if !initial && (number != 1 || offset != 0) {
			return Page{}, storeFailure("JOB_DIAGNOSTIC_CURSOR_EXPIRED")
		}
		page.Cursor = formatCursor(1, 0)
		return page, nil
	}
	if number > last+1 || number == last+1 && offset != 0 {
		return Page{}, storeFailure("JOB_DIAGNOSTIC_CURSOR_EXPIRED")
	}
	if number == last+1 {
		page.Cursor = formatCursor(number, 0)
		return page, nil
	}
	for number <= last {
		name := formatSegmentName(number)
		observedSize := int64(-1)
		if offset > 0 {
			previous, size, found, readErr := segments.ReadSingleLinkWindow(name, offset-1, 1)
			if readErr != nil || !found {
				if readErr != nil && readErr.Code == "READ_OFFSET_INVALID" {
					return Page{}, storeFailure("JOB_DIAGNOSTIC_CURSOR_EXPIRED")
				}
				return Page{}, storeFailure("JOB_DIAGNOSTIC_READ_UNAVAILABLE")
			}
			if len(previous) != 1 || previous[0] != '\n' {
				return Page{}, storeFailure("JOB_DIAGNOSTIC_CURSOR_INVALID")
			}
			observedSize = size
		}
		window, size, found, readErr := segments.ReadSingleLinkWindow(name, offset, runtimepaths.MaxWindowReadBytes)
		if readErr != nil {
			if readErr.Code == "READ_OFFSET_INVALID" {
				return Page{}, storeFailure("JOB_DIAGNOSTIC_CURSOR_EXPIRED")
			}
			return Page{}, storeFailure("JOB_DIAGNOSTIC_READ_UNAVAILABLE")
		}
		if !found || size == 0 {
			return Page{}, storeFailure("JOB_DIAGNOSTIC_RECOVERY_REQUIRED")
		}
		if observedSize >= 0 && observedSize != size {
			return Page{}, storeFailure("JOB_DIAGNOSTIC_READ_UNAVAILABLE")
		}
		tail, tailSize, tailFound, tailErr := segments.ReadSingleLinkWindow(name, size-1, 1)
		if tailErr != nil || !tailFound || tailSize != size {
			return Page{}, storeFailure("JOB_DIAGNOSTIC_READ_UNAVAILABLE")
		}
		if len(tail) != 1 || tail[0] != '\n' {
			return Page{}, storeFailure("JOB_DIAGNOSTIC_RECOVERY_REQUIRED")
		}
		if len(window) == 0 {
			if number == last {
				page.Cursor = formatCursor(number, offset)
				return page, nil
			}
			number++
			offset = 0
			continue
		}
		completeEnd := bytes.LastIndexByte(window, '\n') + 1
		if completeEnd == 0 {
			return Page{}, storeFailure("JOB_DIAGNOSTIC_RECOVERY_REQUIRED")
		}
		consumed := 0
		for consumed < completeEnd && len(page.Events) < MaxPageEvents {
			lineEnd := consumed + bytes.IndexByte(window[consumed:completeEnd], '\n') + 1
			line := window[consumed:lineEnd]
			if _, err := Decode(line); err != nil {
				return Page{}, storeFailure("JOB_DIAGNOSTIC_RECOVERY_REQUIRED")
			}
			page.Events = append(page.Events, append(json.RawMessage(nil), line[:len(line)-1]...))
			consumed = lineEnd
		}
		nextOffset := offset + int64(consumed)
		page.HasMore = nextOffset < size || number < last
		if nextOffset == size && number < last {
			page.Cursor = formatCursor(number+1, 0)
		} else {
			page.Cursor = formatCursor(number, nextOffset)
		}
		return page, nil
	}
	return Page{}, storeFailure("JOB_DIAGNOSTIC_CURSOR_EXPIRED")
}
