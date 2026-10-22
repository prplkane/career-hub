package agentcontext

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/cataloginput"
)

const contextJSONLimit = 32 << 20

// Packet identity uses Python dict insertion order; snapshot identity sorts
// keys recursively. An ordinary Go map cannot preserve the former. Reuse the
// catalog's ordered value representation, restricted here to JSON scalar keys
// and finite JSON values. This does not admit YAML dates, bytes or arbitrary
// marshalers. Callers must preserve source order before reaching this boundary.
func decodeOrderedContextJSON(ctx context.Context, raw []byte) (cataloginput.Value, *Error) {
	if err := contextStopped(ctx); err != nil {
		return cataloginput.Value{}, err
	}
	if len(raw) > contextJSONLimit {
		return cataloginput.Value{}, failure("JSON_LIMIT")
	}
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return cataloginput.Value{}, failure("JSON_INPUT")
	}
	// encoding/json otherwise silently replaces lone UTF-16 escapes with U+FFFD,
	// changing the identity of input the Python UTF-8 hash cannot encode.
	for i := 0; i < len(raw); i++ {
		if i%4096 == 0 {
			if err := contextStopped(ctx); err != nil {
				return cataloginput.Value{}, err
			}
		}
		if raw[i] != '\\' {
			continue
		}
		i++ // json.Valid has already checked escape spelling and bounds.
		if raw[i] != 'u' {
			continue
		}
		n, _ := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		i += 4
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return cataloginput.Value{}, failure("JSON_INPUT")
			}
			low, _ := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if low < 0xdc00 || low > 0xdfff {
				return cataloginput.Value{}, failure("JSON_INPUT")
			}
			i += 6
		} else if n >= 0xdc00 && n <= 0xdfff {
			return cataloginput.Value{}, failure("JSON_INPUT")
		}
	}
	d := orderedContextDecoder{ctx: ctx, decoder: json.NewDecoder(bytes.NewReader(raw))}
	d.decoder.UseNumber()
	return d.value(0)
}

type orderedContextDecoder struct {
	ctx     context.Context
	decoder *json.Decoder
	nodes   int
}

func (d *orderedContextDecoder) value(depth int) (cataloginput.Value, *Error) {
	empty := cataloginput.Value{}
	if err := contextStopped(d.ctx); err != nil {
		return empty, err
	}
	d.nodes++
	if depth > 64 || d.nodes > 200000 {
		return empty, failure("JSON_LIMIT")
	}
	token, err := d.decoder.Token()
	if err != nil {
		return empty, failure("JSON_INPUT")
	}
	switch v := token.(type) {
	case nil:
		return cataloginput.Value{Kind: "null"}, nil
	case bool:
		return cataloginput.Value{Kind: "bool", Boolean: v}, nil
	case string:
		return cataloginput.Value{Kind: "string", Text: v}, nil
	case json.Number:
		kind := "int"
		if strings.ContainsAny(string(v), ".eE") {
			kind = "float"
		}
		text, problem := contextNumberText(kind, string(v))
		if problem != nil {
			return empty, problem
		}
		return cataloginput.Value{Kind: kind, Text: text}, nil
	case json.Delim:
		out := cataloginput.Value{Kind: "sequence"}
		if v == '{' {
			out.Kind = "mapping"
		}
		positions := map[string]int{}
		for d.decoder.More() {
			key := ""
			if v == '{' {
				k, e := d.decoder.Token()
				var ok bool
				key, ok = k.(string)
				if e != nil || !ok {
					return empty, failure("JSON_INPUT")
				}
				d.nodes++
			}
			item, problem := d.value(depth + 1)
			if problem != nil {
				return empty, problem
			}
			if v == '[' {
				out.Items = append(out.Items, item)
			} else if index, exists := positions[key]; exists {
				// Python json.loads keeps the last value at the first key slot.
				out.Entries[index].Value = item
			} else {
				positions[key] = len(out.Entries)
				out.Entries = append(out.Entries, cataloginput.Entry{Key: cataloginput.Value{Kind: "string", Text: key}, Value: item})
			}
		}
		if _, err := d.decoder.Token(); err != nil {
			return empty, failure("JSON_INPUT")
		}
		return out, nil
	default:
		return empty, failure("JSON_INPUT")
	}
}

func contextNumberText(kind, text string) (string, *Error) {
	if len(text) == 0 || len(text) > 4301 || strings.TrimSpace(text) != text || (text[0] != '-' && (text[0] < '0' || text[0] > '9')) || !json.Valid([]byte(text)) {
		return "", failure("JSON_NUMBER")
	}
	if kind == "int" {
		if strings.ContainsAny(text, ".eE") || len(strings.TrimPrefix(text, "-")) > 4300 {
			return "", failure("JSON_NUMBER")
		}
		if text == "-0" {
			text = "0"
		}
		return text, nil
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return "", failure("JSON_NUMBER")
	}
	// Same finite Python repr convention as the SQLite Resume Version scalar
	// projection: preserve REAL .0 and negative zero, fixed at exponents -4..15.
	e := strconv.FormatFloat(f, 'e', -1, 64)
	i := strings.IndexByte(e, 'e')
	exponent, _ := strconv.Atoi(e[i+1:])
	if exponent >= -4 && exponent < 16 {
		e = strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(e, ".") {
			e += ".0"
		}
	}
	return e, nil
}

func encodeOrderedContextJSON(ctx context.Context, value cataloginput.Value, sortKeys bool) ([]byte, *Error) {
	e := orderedContextEncoder{ctx: ctx, sortKeys: sortKeys, maximum: contextJSONLimit}
	if err := e.value(value, 0); err != nil {
		return nil, err
	}
	if err := contextStopped(ctx); err != nil {
		return nil, err
	}
	return e.output, nil
}

type orderedContextEncoder struct {
	ctx            context.Context
	sortKeys       bool
	nodes, maximum int
	output         []byte
}

func (e *orderedContextEncoder) append(text string) *Error {
	if len(text) > e.maximum-len(e.output) {
		return failure("JSON_LIMIT")
	}
	e.output = append(e.output, text...)
	return nil
}

func (e *orderedContextEncoder) quoted(text string) *Error {
	if len(text) > e.maximum-len(e.output)-2 {
		return failure("JSON_LIMIT")
	}
	if !utf8.ValidString(text) {
		return failure("JSON_INPUT")
	}
	if err := e.append("\""); err != nil {
		return err
	}
	for i, r := range text {
		if i%4096 == 0 {
			if err := contextStopped(e.ctx); err != nil {
				return err
			}
		}
		piece := string(r)
		switch r {
		case '"', '\\':
			piece = "\\" + piece
		case '\b':
			piece = `\b`
		case '\f':
			piece = `\f`
		case '\n':
			piece = `\n`
		case '\r':
			piece = `\r`
		case '\t':
			piece = `\t`
		default:
			if r < 0x20 {
				const hex = "0123456789abcdef"
				piece = string([]byte{'\\', 'u', '0', '0', hex[r>>4], hex[r&15]})
			}
		}
		if err := e.append(piece); err != nil {
			return err
		}
	}
	return e.append("\"")
}

func (e *orderedContextEncoder) value(v cataloginput.Value, depth int) *Error {
	if err := contextStopped(e.ctx); err != nil {
		return err
	}
	e.nodes++
	if depth > 64 || e.nodes > 200000 || len(v.Entries) > (200000-e.nodes)/2 || len(v.Items) > 200000-e.nodes {
		return failure("JSON_LIMIT")
	}
	switch v.Kind {
	case "null":
		return e.append("null")
	case "bool":
		return e.append(strconv.FormatBool(v.Boolean))
	case "string":
		return e.quoted(v.Text)
	case "int", "float":
		text, err := contextNumberText(v.Kind, v.Text)
		if err != nil {
			return err
		}
		return e.append(text)
	case "sequence":
		if err := e.append("["); err != nil {
			return err
		}
		for i, item := range v.Items {
			if i > 0 {
				if err := e.append(","); err != nil {
					return err
				}
			}
			if err := e.value(item, depth+1); err != nil {
				return err
			}
		}
		return e.append("]")
	case "mapping":
		// The decoded representation already has dict semantics; duplicate keys
		// in programmatically assembled packets indicate a builder defect.
		seen := map[string]bool{}
		for _, entry := range v.Entries {
			if entry.Key.Kind != "string" || seen[entry.Key.Text] {
				return failure("JSON_INPUT")
			}
			seen[entry.Key.Text] = true
		}
		entries := v.Entries
		if e.sortKeys {
			entries = slices.Clone(entries)
			slices.SortFunc(entries, func(a, b cataloginput.Entry) int { return strings.Compare(a.Key.Text, b.Key.Text) })
		}
		if err := e.append("{"); err != nil {
			return err
		}
		for i, entry := range entries {
			if i > 0 {
				if err := e.append(","); err != nil {
					return err
				}
			}
			if err := e.value(entry.Key, depth+1); err != nil {
				return err
			}
			if err := e.append(":"); err != nil {
				return err
			}
			if err := e.value(entry.Value, depth+1); err != nil {
				return err
			}
		}
		return e.append("}")
	default:
		return failure("JSON_INPUT")
	}
}
