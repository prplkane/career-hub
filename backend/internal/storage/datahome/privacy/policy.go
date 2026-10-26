package privacy

import (
	"regexp"
	"strings"
)

const (
	MaxEntries       = 4096
	MaxSnapshotBytes = 1 << 20
	MaxPathBytes     = 4096
	MaxDepth         = 64
)

// Error never retains a path, account SID, OS error, or security descriptor.
type Error struct{ Code string }

func (e *Error) Error() string { return "data home privacy: " + e.Code }
func fail(code string) *Error  { return &Error{Code: code} }

var sidPattern = regexp.MustCompile(`^S-[0-9]+(?:-[0-9]+)+$`)

// privateDACL mirrors _windows_acl_snapshot_is_private. Production only passes
// canonical DACL-only SDDL returned by Windows, never a caller-supplied SID/ACL.
func privateDACL(sddl, principal string, inherited bool) bool {
	current := strings.TrimPrefix(principal, "*")
	if !sidPattern.MatchString(current) || !strings.HasPrefix(sddl, "D:") {
		return false
	}
	start := strings.IndexByte(sddl, '(')
	if start < 2 {
		return false
	}
	flags := sddl[2:start]
	protected := false
	for len(flags) != 0 {
		switch {
		case strings.HasPrefix(flags, "P"):
			protected, flags = true, flags[1:]
		case strings.HasPrefix(flags, "AI"), strings.HasPrefix(flags, "AR"):
			flags = flags[2:]
		default:
			return false
		}
	}
	if protected == inherited {
		return false
	}
	remaining, count := sddl[start:], 0
	trustees := map[string]bool{}
	for len(remaining) != 0 {
		end := strings.IndexByte(remaining, ')')
		if remaining[0] != '(' || end < 0 || count == 2 {
			return false
		}
		fields := strings.Split(remaining[1:end], ";")
		if len(fields) != 6 || fields[0] != "A" || fields[2] != "FA" || fields[3] != "" || fields[4] != "" {
			return false
		}
		aceFlags, seen := fields[1], map[string]bool{}
		for len(aceFlags) >= 2 {
			flag := aceFlags[:2]
			if seen[flag] || flag != "OI" && flag != "CI" && flag != "IO" && flag != "ID" {
				return false
			}
			seen[flag], aceFlags = true, aceFlags[2:]
		}
		if aceFlags != "" || seen["IO"] || (inherited && (!seen["ID"] || len(seen) != 1 && !(len(seen) == 3 && seen["OI"] && seen["CI"]))) || (!inherited && (len(seen) != 2 || !seen["OI"] || !seen["CI"])) {
			return false
		}
		trustee := fields[5]
		switch {
		case trustee == "SY" || trustee == "S-1-5-18":
			trustees["S-1-5-18"] = true
		case trustee == current || trustee == "LA" && strings.HasSuffix(current, "-500"):
			trustees[current] = true
		default:
			return false
		}
		count++
		remaining = remaining[end+1:]
	}
	// SYSTEM as the process user intentionally permits two SYSTEM ACEs, matching
	// source set semantics; for an ordinary user both distinct trustees are needed.
	return count == 2 && trustees[current] && trustees["S-1-5-18"]
}
