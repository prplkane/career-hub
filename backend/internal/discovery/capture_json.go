package discovery

import (
	"context"
	"encoding/json"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

// Payload hashes are persisted replay identities. Go's default HTML escaping,
// U+2028 escaping and float thresholds would change Python's compact UTF-8 JSON.
// Keep this bounded encoder local to the closed Discovery capture contract.
type captureEncoder struct {
	ctx          context.Context
	out          []byte
	limit, nodes int
	metadata     bool
	err          *WriteError
}

func captureJSON(ctx context.Context, value any, limit int, metadata bool) (string, *WriteError) {
	if ctx == nil {
		return "", writeFailure("context", "CONTEXT_REQUIRED")
	}
	e := captureEncoder{ctx: ctx, limit: limit, metadata: metadata}
	e.value(value, 0)
	if e.err != nil {
		return "", e.err
	}
	return string(e.out), nil
}
func (e *captureEncoder) add(s string) {
	if e.err != nil {
		return
	}
	if len(s) > e.limit-len(e.out) {
		if e.metadata {
			e.err = invalidWrite("metadata exceeds the configured byte limit")
		} else {
			e.err = writeFailure("request", "INPUT_LIMIT")
		}
		return
	}
	e.out = append(e.out, s...)
}
func (e *captureEncoder) quoted(s string) {
	if !utf8.ValidString(s) {
		e.err = writeFailure("request", "INPUT_UNSUPPORTED")
		return
	}
	e.add(`"`)
	const hex = "0123456789abcdef"
	for _, c := range s {
		if e.err != nil {
			return
		}
		switch c {
		case '"', '\\':
			e.add("\\" + string(c))
		case '\b':
			e.add(`\b`)
		case '\f':
			e.add(`\f`)
		case '\n':
			e.add(`\n`)
		case '\r':
			e.add(`\r`)
		case '\t':
			e.add(`\t`)
		default:
			if c < 0x20 {
				e.add(string([]byte{'\\', 'u', '0', '0', hex[c>>4], hex[c&15]}))
			} else {
				e.add(string(c))
			}
		}
	}
	e.add(`"`)
}
func (e *captureEncoder) floating(n float64) {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		e.err = invalidWrite("metadata must contain only finite JSON values")
		return
	}
	e.add(pythonFloatJSON(n))
}
func (e *captureEncoder) value(value any, depth int) {
	if e.err != nil {
		return
	}
	if e.ctx.Err() != nil {
		e.err = writeFailure("context", "WRITE_CANCELED")
		return
	}
	e.nodes++
	if depth > 64 || e.nodes > 8192 {
		e.err = writeFailure("request", "INPUT_LIMIT")
		return
	}
	switch v := value.(type) {
	case nil:
		e.add("null")
	case bool:
		e.add(strconv.FormatBool(v))
	case string:
		e.quoted(v)
	case int:
		e.add(strconv.FormatInt(int64(v), 10))
	case int64:
		e.add(strconv.FormatInt(v, 10))
	case float64:
		e.floating(v)
	case json.Number:
		if len(v) > 4096 {
			e.err = writeFailure("request", "INPUT_LIMIT")
			return
		}
		if len(v) == 0 || (v[0] != '-' && (v[0] < '0' || v[0] > '9')) || !json.Valid([]byte(v)) {
			e.err = invalidWrite("metadata must contain only JSON values")
			return
		}
		if strings.ContainsAny(string(v), ".eE") {
			n, err := strconv.ParseFloat(string(v), 64)
			if err != nil {
				e.err = invalidWrite("metadata must contain only finite JSON values")
				return
			}
			e.floating(n)
		} else {
			n, ok := new(big.Int).SetString(string(v), 10)
			if !ok {
				e.err = invalidWrite("metadata must contain only JSON values")
				return
			}
			e.add(n.String())
		}
	case []any:
		if len(v) > 8192 {
			e.err = writeFailure("request", "INPUT_LIMIT")
			return
		}
		e.add("[")
		for i, child := range v {
			if e.err != nil {
				return
			}
			if i != 0 {
				e.add(",")
			}
			e.value(child, depth+1)
		}
		e.add("]")
	case map[string]any:
		if len(v) > 8192 {
			e.err = writeFailure("request", "INPUT_LIMIT")
			return
		}
		keys := make([]string, 0, len(v))
		for k := range v {
			if !utf8.ValidString(k) {
				e.err = writeFailure("request", "INPUT_UNSUPPORTED")
				return
			}
			if e.metadata {
				clean := textcompat.Strip(k)
				if clean == "" {
					e.err = invalidWrite("metadata keys must be non-empty strings")
					return
				}
				if slices.Contains([]string{"attachment", "attachments", "bcc", "cc", "cookie", "cookies", "credential", "credentials", "dom_html", "html", "password", "raw_header", "raw_headers", "recipient", "recipients", "secret", "to", "token"}, textcompat.Fold(clean)) {
					e.err = invalidWrite("metadata contains a prohibited private field")
					return
				}
			}
			keys = append(keys, k)
		}
		slices.Sort(keys)
		e.add("{")
		for i, k := range keys {
			if e.err != nil {
				return
			}
			if i != 0 {
				e.add(",")
			}
			e.quoted(k)
			e.add(":")
			e.value(v[k], depth+1)
		}
		e.add("}")
	default:
		e.err = invalidWrite("metadata must contain only JSON values")
	}
}
