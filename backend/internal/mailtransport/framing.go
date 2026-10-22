package mailtransport

import (
	"strings"
)

const maxProtocolLine = 64 * 1024
const maxProtocolDepth = 32

// frameGuard is a resource guard, not an IMAP parser. The client still validates
// grammar. Unlike its recursive BODYSTRUCTURE reader, this state machine uses
// constant stack space. Literal octets are skipped only in a FETCH list, never
// after a literal-looking suffix in arbitrary OK/NO response text.
type frameGuard struct {
	line, depth                int
	quoted, escaped, cr, fetch bool
	opaque                     bool
	prefix                     [64]byte
	prefixLen                  int
	marker, digits             int
	literalSize, literalLeft   int64
	tooLarge                   bool
}

func (g *frameGuard) accept(data []byte) bool {
	for _, b := range data {
		if g.literalLeft > 0 {
			g.literalLeft--
			continue
		}
		g.line++
		if g.line > maxProtocolLine {
			g.tooLarge = true
			return false
		}
		if !g.fetch {
			// Status response text is arbitrary text: its unmatched quotes,
			// parentheses and literal-looking suffixes are not IMAP structure.
			if g.cr {
				if b != '\n' {
					return false
				}
				g.cr, g.opaque, g.line, g.prefixLen = false, false, 0, 0
				continue
			}
			if b == '\r' {
				g.cr = true
				continue
			}
			if b == '\n' {
				return false
			}
			if !g.opaque && b == '(' {
				if fetchPrefix(g.prefix[:g.prefixLen]) {
					g.fetch, g.depth = true, 1
				} else {
					parts := strings.Fields(string(g.prefix[:g.prefixLen]))
					if len(parts) == 2 && parts[0] == "*" && strings.EqualFold(parts[1], "FETCH") {
						return false
					}
					g.opaque = true
				}
				continue
			}
			if !g.opaque && b == ' ' && opaquePrefix(g.prefix[:g.prefixLen]) {
				g.opaque = true
			}
			if g.opaque {
				continue
			}
			if g.prefixLen < len(g.prefix) {
				g.prefix[g.prefixLen] = b
				g.prefixLen++
			} else {
				// A long numeric token with leading zeros is still accepted by
				// the client. Never pass an unresolved FETCH prefix as opaque.
				return false
			}
			continue
		}
		if g.marker != 0 {
			switch g.marker {
			case 1:
				if b >= '0' && b <= '9' {
					g.digits++
					if g.digits > 12 {
						return false
					}
					g.literalSize = g.literalSize*10 + int64(b-'0')
					if g.literalSize > MaxMessageBytes {
						g.tooLarge = true
						return false
					}
				} else if g.digits > 0 && b == '+' {
					g.marker = 2
				} else if g.digits > 0 && b == '}' {
					g.marker = 3
				} else {
					return false
				}
			case 2:
				if b != '}' {
					return false
				}
				g.marker = 3
			case 3:
				if b != '\r' {
					return false
				}
				g.marker = 4
			case 4:
				if b != '\n' {
					return false
				}
				g.literalLeft, g.marker, g.line = g.literalSize, 0, 0
			}
			continue
		}
		if g.quoted {
			if b == '\r' || b == '\n' {
				return false
			}
			if g.escaped {
				g.escaped = false
			} else if b == '\\' {
				g.escaped = true
			} else if b == '"' {
				g.quoted = false
			}
			continue
		}
		if g.cr {
			if b != '\n' {
				return false
			}
			g.cr, g.line = false, 0
			if g.depth == 0 {
				g.fetch, g.prefixLen = false, 0
			}
			continue
		}
		switch b {
		case '\r':
			g.cr = true
		case '\n':
			return false
		case '"':
			g.quoted = true
		case '(':
			g.depth++
			if g.depth > maxProtocolDepth {
				return false
			}
		case ')':
			if g.depth > 0 {
				g.depth--
			}
		case '{':
			if g.fetch && g.depth > 0 {
				g.marker, g.digits, g.literalSize = 1, 0, 0
			}
		}
	}
	return true
}

func fetchPrefix(prefix []byte) bool {
	parts := strings.Fields(string(prefix))
	if len(parts) != 3 || parts[0] != "*" || !strings.EqualFold(parts[2], "FETCH") || len(parts[1]) == 0 {
		return false
	}
	for _, b := range []byte(parts[1]) {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}

func opaquePrefix(prefix []byte) bool {
	parts := strings.Fields(string(prefix))
	if len(parts) == 1 && parts[0] == "+" {
		return true
	}
	if len(parts) < 2 {
		return false
	}
	if parts[0] != "*" {
		switch strings.ToUpper(parts[1]) {
		case "OK", "NO", "BAD":
			return true
		}
		return false
	}
	if parts[1][0] >= '0' && parts[1][0] <= '9' {
		return len(parts) >= 3 && !strings.EqualFold(parts[2], "FETCH")
	}
	// Other untagged response parsers do not enter recursive BODYSTRUCTURE.
	return !strings.EqualFold(parts[1], "FETCH")
}
