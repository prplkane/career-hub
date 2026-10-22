package protocol

import (
	"runtime"
	"strings"
	"unicode/utf8"
)

type PathStyle string

const (
	Native  PathStyle = ""
	Windows PathStyle = "windows"
	POSIX   PathStyle = "posix"
)

// PathOptions makes platform and home expansion explicit for synthetic replay.
// ExpandUser receives only the leading ~ or ~user component, after lexical path
// parsing, and returns its home path. It must not resolve remaining components.
// Nil never consults the process environment or OS account database.
type PathOptions struct {
	Style      PathStyle
	ExpandUser func(string) (string, error)
}

type pathParts struct {
	drive, root string
	tail        []string
}

func splitPath(raw string, style PathStyle) pathParts {
	var p pathParts
	sep := "/"
	if style == Windows {
		sep = `\`
		raw = strings.ReplaceAll(raw, "/", sep)
		if strings.HasPrefix(raw, `\\`) {
			start := 2
			if strings.HasPrefix(strings.ToUpper(raw), `\\?\UNC\`) {
				start = 8
			}
			first := strings.Index(raw[start:], sep)
			end := -1
			if first >= 0 {
				first += start
				if next := strings.Index(raw[first+1:], sep); next >= 0 {
					end = first + 1 + next
				}
			}
			if end >= 0 {
				p.drive, p.root, raw = raw[:end], sep, raw[end+1:]
			} else {
				p.drive, raw = raw, ""
				parts := strings.Split(p.drive, sep)
				if !strings.HasSuffix(p.drive, sep) && ((len(parts) == 4 && !strings.Contains("?.", parts[2])) || len(parts) == 6) {
					p.root = sep
				}
			}
		} else {
			// Python's drive syntax uses a Unicode character followed by a colon,
			// not only ASCII A-Z; no filesystem existence is inferred.
			_, size := utf8.DecodeRuneInString(raw)
			if len(raw) > size && raw[size] == ':' {
				p.drive, raw = raw[:size+1], raw[size+1:]
			}
			if strings.HasPrefix(raw, sep) {
				p.root, raw = sep, raw[1:]
			}
		}
	} else if strings.HasPrefix(raw, "/") {
		p.root = "/"
		if strings.HasPrefix(raw, "//") && !strings.HasPrefix(raw, "///") {
			p.root = "//"
		}
		raw = strings.TrimLeft(raw, "/")
	}
	for _, part := range strings.Split(raw, sep) {
		if part != "" && part != "." {
			p.tail = append(p.tail, part)
		}
	}
	return p
}

func absolutePath(value any, options PathOptions) (string, error) {
	raw, ok := value.(string)
	if !ok || raw == "" || utf8.RuneCountInString(raw) > MaxPathChars || strings.ContainsRune(raw, 0) {
		return "", failure("SIDECAR_START_INVALID_PATH")
	}
	if !utf8.ValidString(raw) {
		return "", &CompatibilityError{Reason: "non-scalar Unicode path"}
	}
	style := options.Style
	if style == Native {
		style = POSIX
		if runtime.GOOS == "windows" {
			style = Windows
		}
	}
	if style != Windows && style != POSIX {
		return "", &CompatibilityError{Reason: "unknown path style"}
	}
	p := splitPath(raw, style)
	if p.drive == "" && p.root == "" && len(p.tail) > 0 && strings.HasPrefix(p.tail[0], "~") {
		if options.ExpandUser == nil {
			return "", &CompatibilityError{Reason: "home expansion requires injected resolver"}
		}
		home, err := options.ExpandUser(p.tail[0])
		if err != nil || strings.HasPrefix(home, "~") {
			return "", &CompatibilityError{Reason: "home expansion unavailable"}
		}
		if !utf8.ValidString(home) {
			return "", &CompatibilityError{Reason: "non-scalar Unicode home"}
		}
		expanded := splitPath(home, style)
		expanded.tail = append(expanded.tail, p.tail[1:]...)
		p = expanded
	}
	if p.root == "" || (style == Windows && p.drive == "") {
		return "", failure("SIDECAR_START_INVALID_PATH")
	}
	sep := "/"
	if style == Windows {
		sep = `\`
	}
	return p.drive + p.root + strings.Join(p.tail, sep), nil
}
