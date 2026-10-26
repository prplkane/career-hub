package jobcard

import (
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/jobs/joburls"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/textcompat"
)

// ValidID is the bounded exact saved-job selector, not an arbitrary path.
func ValidID(id string) bool {
	return id != "" && id != "." && id != ".." && len(id) <= 256 && utf8.ValidString(id) && textcompat.Strip(id) == id &&
		!strings.ContainsAny(id, "/\\:") && !strings.ContainsFunc(id, func(c rune) bool { return c < 32 || c == 127 })
}

func (v *validator) sourceURL(data cataloginput.Value) {
	value := get(data, "source_url")
	if value.Kind != "string" || value.Text == "" || !v.active() {
		return
	}
	valid, err := joburls.ValidSource(value.Text)
	if err != nil {
		v.failure = &Error{Code: "JOB_CARD_URL_LIMIT"}
		return
	}
	if !valid {
		v.add("source_url", "expected absolute HTTP or HTTPS URL")
	}
}

// managedPath enforces a lexical fixed subtree, never Path.Clean/resolve on an
// untrusted name. Native component/Unicode alias, link and regular-file checks
// remain the responsibility of the retained runtimepaths.Root handle.
func managedPath(raw, folder string) (string, bool) {
	if raw == "" || len(raw) > runtimepaths.MaxPathBytes || !utf8.ValidString(raw) {
		return "", false
	}
	path := strings.ReplaceAll(raw, "\\", "/")
	if !strings.HasPrefix(path, "jobs/"+folder+"/") {
		return "", false
	}
	parts := strings.Split(path, "/")
	if len(parts) > runtimepaths.MaxComponents {
		return "", false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > runtimepaths.MaxComponentBytes || strings.ContainsAny(part, `<>:"|?*`) || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return "", false
		}
		for _, r := range part {
			if r < 32 || r == 127 {
				return "", false
			}
		}
	}
	return path, true
}

func (v *validator) rawPath(data cataloginput.Value, check RawFileCheck, folder string) {
	value := get(data, "raw_jd_path")
	if value.Kind != "string" || !v.active() {
		return
	}
	if !nonempty(value) {
		v.add("raw_jd_path", "must not be empty")
		return
	}
	path, valid := managedPath(value.Text, folder)
	if !valid {
		v.add("raw_jd_path", "expected canonical relative file inside jobs/"+folder)
		return
	}
	if check == nil {
		v.failure = &Error{Code: "JOB_CARD_SOURCE_CHECK_REQUIRED"}
		return
	}
	exists, err := check(v.ctx, path)
	if !v.active() {
		return
	}
	if err != nil {
		// Do not propagate arbitrary provider error text or codes into diagnostics.
		v.failure = &Error{Code: "JOB_CARD_SOURCE_UNAVAILABLE"}
		switch err.Code {
		case "JOB_CARD_INPUT_LIMIT", "JOB_CARD_SOURCE_ENCODING":
			v.failure.Code = err.Code
		}
		return
	}
	if !exists {
		v.add("raw_jd_path", "file does not exist")
	}
}
