package jobcard

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/jobs/jobdigest"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/textcompat"
	"golang.org/x/text/unicode/norm"
)

const intakeNameBaseBytes = 180
const intakeNameAttempts = 128

// IntakeDestination is a name proposal, never a path reservation or proof of
// duplicate absence. The writer must recheck it under the publication gate.
type IntakeDestination struct {
	ID, RawPath, CardPath string
}

// SelectIntakeDestination preserves readable filenames and uses the exact JD
// fingerprint to resolve occupied names. Names must come from bounded owned
// directory observations; this pure function does not inspect the filesystem.
func SelectIntakeDestination(sourceName string, fresh *jobdigest.Result, rawNames, cardNames []string) (IntakeDestination, *Error) {
	if fresh == nil || !intakeFingerprint(fresh.ContentFingerprint) || len(rawNames) > runtimepaths.MaxDirectoryEntries || len(cardNames) > runtimepaths.MaxDirectoryEntries {
		return IntakeDestination{}, &Error{Code: "JOB_INTAKE_NAME_INVALID"}
	}
	stem, suffix, valid := intakeStem(sourceName, fresh)
	if !valid {
		return IntakeDestination{}, &Error{Code: "JOB_INTAKE_NAME_INVALID"}
	}
	base := boundedIntakeBase(slugID(stem))
	if intakeDeviceName(base) {
		base = "job_" + base
	}
	occupiedRaw, occupiedCard := intakeNameSet(rawNames), intakeNameSet(cardNames)
	for attempt := 0; attempt < intakeNameAttempts; attempt++ {
		id := base
		if attempt != 0 {
			id += "_" + fresh.ContentFingerprint[:12]
			if attempt > 1 {
				id += "_" + strconv.Itoa(attempt)
			}
		}
		rawName, cardName := id+suffix, id+".yaml"
		// The ID, not the requested extension, identifies one job. A retained
		// alternate source/card suffix must occupy the same ID.
		if !occupiedRaw[intakeNameKey(id+".md")] && !occupiedRaw[intakeNameKey(id+".txt")] &&
			!occupiedCard[intakeNameKey(cardName)] && !occupiedCard[intakeNameKey(id+".yml")] {
			return IntakeDestination{ID: id, RawPath: "jobs/incoming/" + rawName, CardPath: "jobs/normalized/" + cardName}, nil
		}
	}
	return IntakeDestination{}, &Error{Code: "JOB_INTAKE_NAME_EXHAUSTED"}
}

func intakeFingerprint(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func intakeStem(sourceName string, fresh *jobdigest.Result) (string, string, bool) {
	name := textcompat.Strip(sourceName)
	if !ValidIntakeSourceName(sourceName) {
		return "", "", false
	}
	if name == "" {
		company, title := fresh.Fields["company"].Value, fresh.Fields["title"].Value
		stem := strings.TrimSpace(strings.Join([]string{company, title}, "_"))
		if textcompat.Strip(stem) == "_" || textcompat.Strip(stem) == "" {
			stem = "job_" + fresh.ContentFingerprint[:12]
		}
		return stem, ".md", true
	}
	stem, suffix := name, ".md"
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		suffix = strings.ToLower(name[dot:])
		stem = name[:dot]
	}
	return stem, suffix, true
}

// ValidIntakeSourceName is request preflight. The writer still owns the final
// native path admission and no-replace check.
func ValidIntakeSourceName(sourceName string) bool {
	name := textcompat.Strip(sourceName)
	if len(name) > 512 || !utf8.ValidString(name) || strings.ContainsAny(name, "/\\") ||
		strings.ContainsFunc(name, func(r rune) bool { return r < 32 || r == 127 }) {
		return false
	}
	if name == "" {
		return true
	}
	if name == "." || name == ".." || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return false
	}
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		suffix := strings.ToLower(name[dot:])
		return suffix == ".md" || suffix == ".txt"
	}
	return true
}

func boundedIntakeBase(base string) string {
	if len(base) <= intakeNameBaseBytes {
		return base
	}
	end := 0
	for _, char := range base {
		width := utf8.RuneLen(char)
		if end+width > intakeNameBaseBytes {
			break
		}
		end += width
	}
	return strings.Trim(base[:end], "_")
}

func intakeDeviceName(base string) bool {
	upper := strings.ToUpper(base)
	switch upper {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) {
		return upper[3] >= '1' && upper[3] <= '9'
	}
	return false
}

func intakeNameSet(names []string) map[string]bool {
	result := make(map[string]bool, len(names))
	for _, name := range names {
		result[intakeNameKey(name)] = true
	}
	return result
}

func intakeNameKey(name string) string { return textcompat.Fold(norm.NFC.String(name)) }
