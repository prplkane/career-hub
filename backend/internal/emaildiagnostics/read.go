package emaildiagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/runtimepaths"
)

// ReadPage never returns a partial/corrupt native journal page. These bounds
// are below the sidecar's 4 MiB JSON response ceiling and its UI's 1000-event
// in-memory cap. One page reads at most one 256 KiB window of one segment.
const MaxPageEvents = 128

var cursorPattern = regexp.MustCompile(`^ne:([0-9]{6}):([0-9]{1,8})$`)

type Page struct {
	Cursor  string            `json:"cursor"`
	Events  []json.RawMessage `json:"events"`
	HasMore bool              `json:"has_more"`
}

func formatCursor(number int, offset int64) string {
	return fmt.Sprintf("ne:%06d:%d", number, offset)
}

func parseCursor(value string) (number int, offset int64, initial bool, problem *StoreError) {
	if value == "" || value == "0" || value == "byte:0" {
		return 1, 0, true, nil
	}
	match := cursorPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, 0, false, storeFailure("EMAIL_DIAGNOSTIC_CURSOR_INVALID")
	}
	number, err := strconv.Atoi(match[1])
	if err != nil || number < 1 || number > MaxDiagnosticSegments {
		return 0, 0, false, storeFailure("EMAIL_DIAGNOSTIC_CURSOR_INVALID")
	}
	offset, err = strconv.ParseInt(match[2], 10, 64)
	if err != nil || offset < 0 || offset > runtimepaths.MaxReadBytes {
		return 0, 0, false, storeFailure("EMAIL_DIAGNOSTIC_CURSOR_INVALID")
	}
	return number, offset, false, nil
}

// ReadPage reads only the native email journal. It does not claim to replace
// the Python /api/logs route or merge legacy events. The owner must have
// admitted and pinned this Home; caller authentication remains an HTTP concern.
// A cursor at current EOF can be polled again after a later append or rotation.
func ReadPage(ctx context.Context, dataRoot string, expected datahome.Info, cursor string) (page Page, problem *StoreError) {
	page.Events = []json.RawMessage{}
	if ctx == nil || ctx.Err() != nil {
		return Page{}, storeFailure("EMAIL_DIAGNOSTIC_CANCELED")
	}
	number, offset, initial, problem := parseCursor(cursor)
	if problem != nil {
		return Page{}, problem
	}
	release, err := operationgate.Acquire(ctx)
	if err != nil {
		return Page{}, storeFailure("EMAIL_DIAGNOSTIC_CANCELED")
	}
	defer release()
	defer func() {
		if problem != nil {
			return
		}
		if ctx.Err() != nil {
			page, problem = Page{}, storeFailure("EMAIL_DIAGNOSTIC_CANCELED")
		} else if !sameHome(dataRoot, expected) {
			page, problem = Page{}, storeFailure("EMAIL_DIAGNOSTIC_HOME_CHANGED")
		}
	}()
	if !sameHome(dataRoot, expected) {
		return Page{}, storeFailure("EMAIL_DIAGNOSTIC_HOME_CHANGED")
	}
	root, e := runtimepaths.Open(dataRoot)
	if e != nil {
		return Page{}, storeFailure("EMAIL_DIAGNOSTIC_STORE_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil {
			page, problem = Page{}, storeFailure("EMAIL_DIAGNOSTIC_CLOSE_FAILED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return Page{}, storeFailure("EMAIL_DIAGNOSTIC_OWNERSHIP_REQUIRED")
	}
	exists, e := root.Exists(segmentDirectory)
	if e != nil {
		return Page{}, storeFailure("EMAIL_DIAGNOSTIC_DIRECTORY_INVALID")
	}
	if !exists {
		if !initial && (number != 1 || offset != 0) {
			return Page{}, storeFailure("EMAIL_DIAGNOSTIC_CURSOR_EXPIRED")
		}
		page.Cursor = formatCursor(1, 0)
		return page, nil
	}
	segments, e := runtimepaths.Open(filepath.Join(dataRoot, filepath.FromSlash(segmentDirectory)))
	if e != nil {
		return Page{}, storeFailure("EMAIL_DIAGNOSTIC_DIRECTORY_INVALID")
	}
	defer func() {
		if segments.Close() != nil {
			page, problem = Page{}, storeFailure("EMAIL_DIAGNOSTIC_CLOSE_FAILED")
		}
	}()
	names, e := segments.ListFileNames()
	if e != nil {
		return Page{}, storeFailure("EMAIL_DIAGNOSTIC_DIRECTORY_INVALID")
	}
	if len(names) > MaxDiagnosticSegments {
		return Page{}, storeFailure("EMAIL_DIAGNOSTIC_LIMIT")
	}
	if _, err := nextSegment(names); err != nil {
		return Page{}, storeFailure("EMAIL_DIAGNOSTIC_DIRECTORY_INVALID")
	}
	last := len(names)
	if number > last+1 || number == last+1 && offset != 0 {
		return Page{}, storeFailure("EMAIL_DIAGNOSTIC_CURSOR_EXPIRED")
	}
	if number == last+1 {
		page.Cursor = formatCursor(number, 0)
		return page, nil
	}
	for number <= last {
		name := formatSegmentName(number)
		var observedSize int64 = -1
		if offset > 0 {
			previous, size, found, e := segments.ReadSingleLinkWindow(name, offset-1, 1)
			if e != nil || !found {
				if e != nil && e.Code == "READ_OFFSET_INVALID" {
					return Page{}, storeFailure("EMAIL_DIAGNOSTIC_CURSOR_EXPIRED")
				}
				return Page{}, storeFailure("EMAIL_DIAGNOSTIC_READ_UNAVAILABLE")
			}
			if len(previous) != 1 || previous[0] != '\n' {
				return Page{}, storeFailure("EMAIL_DIAGNOSTIC_CURSOR_INVALID")
			}
			observedSize = size
		}
		window, size, found, e := segments.ReadSingleLinkWindow(name, offset, runtimepaths.MaxWindowReadBytes)
		if e != nil {
			if e.Code == "READ_OFFSET_INVALID" {
				return Page{}, storeFailure("EMAIL_DIAGNOSTIC_CURSOR_EXPIRED")
			}
			return Page{}, storeFailure("EMAIL_DIAGNOSTIC_READ_UNAVAILABLE")
		}
		if !found || size == 0 {
			return Page{}, storeFailure("EMAIL_DIAGNOSTIC_RECOVERY_REQUIRED")
		}
		if observedSize >= 0 && observedSize != size {
			return Page{}, storeFailure("EMAIL_DIAGNOSTIC_READ_UNAVAILABLE")
		}
		// Writer publication is whole-record. Refuse an incomplete tail before
		// returning any earlier events from a damaged segment.
		tail, tailSize, tailFound, tailError := segments.ReadSingleLinkWindow(name, size-1, 1)
		if tailError != nil || !tailFound || tailSize != size {
			return Page{}, storeFailure("EMAIL_DIAGNOSTIC_READ_UNAVAILABLE")
		}
		if len(tail) != 1 || tail[0] != '\n' {
			return Page{}, storeFailure("EMAIL_DIAGNOSTIC_RECOVERY_REQUIRED")
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
			return Page{}, storeFailure("EMAIL_DIAGNOSTIC_RECOVERY_REQUIRED")
		}
		consumed := 0
		for consumed < completeEnd && len(page.Events) < MaxPageEvents {
			lineEnd := consumed + bytes.IndexByte(window[consumed:completeEnd], '\n') + 1
			line := window[consumed:lineEnd]
			if !validEvent(line) {
				return Page{}, storeFailure("EMAIL_DIAGNOSTIC_RECOVERY_REQUIRED")
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
	return Page{}, storeFailure("EMAIL_DIAGNOSTIC_CURSOR_EXPIRED")
}
