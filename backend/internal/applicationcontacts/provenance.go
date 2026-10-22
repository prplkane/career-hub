package applicationcontacts

import (
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

func stated(f map[string]string, channels []channel) map[string]any {
	out := map[string]any{}
	for _, key := range factNames {
		out[key] = f[key]
	}
	cs := []any{}
	for _, c := range channels {
		cs = append(cs, map[string]any{"kind": c.Kind, "value": c.Value})
	}
	out["channels"] = cs
	return out
}
func normalizedStated(v any, fallback map[string]any) (map[string]any, *Error) {
	if v == nil {
		return fallback, nil
	}
	m, ok := v.(map[string]any)
	if !ok || len(m) != 5 {
		return nil, invalid("provenance.stated_fields must contain exact contact facts and channels")
	}
	for k := range m {
		if k != "channels" && !slices.Contains(factNames, k) {
			return nil, invalid("provenance.stated_fields must contain exact contact facts and channels")
		}
	}
	f, p := facts(m)
	if p != nil {
		return nil, p
	}
	cs, p := normalizeChannels(m["channels"])
	if p != nil {
		return nil, p
	}
	if f["name"] == "" && len(cs) == 0 {
		return nil, invalid("provenance.stated_fields requires a name or exact channel")
	}
	return stated(f, cs), nil
}

var proofFields = []string{"origin", "source_reference", "discovery_id", "job_id", "source_content_sha256", "source_text", "line_start", "line_end", "stated_fields"}

func provenance(v any, reference string, fallback map[string]any) (map[string]any, *Error) {
	out := map[string]any{"origin": "manual", "source_reference": reference, "discovery_id": "", "job_id": "", "source_content_sha256": "", "source_text": "", "line_start": int64(0), "line_end": int64(0), "stated_fields": fallback}
	if v == nil {
		return out, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, invalid("provenance contains unsupported fields")
	}
	for k := range m {
		if !slices.Contains(proofFields, k) {
			return nil, invalid("provenance contains unsupported fields")
		}
	}
	origin, ok := m["origin"].(string)
	if !ok || origin != "manual" && origin != "jd_candidate" {
		return nil, invalid("provenance.origin is unsupported")
	}
	out["origin"] = origin
	r, p := text(value(m, "source_reference", reference), "provenance.source_reference", 256, false, false)
	if p != nil {
		return nil, p
	}
	out["source_reference"] = r
	for _, k := range []string{"discovery_id", "job_id"} {
		s, p := identifier(value(m, k, ""), "provenance."+k, true)
		if p != nil {
			return nil, p
		}
		out[k] = s
	}
	h, p := text(value(m, "source_content_sha256", ""), "provenance.source_content_sha256", 64, true, false)
	if p != nil {
		return nil, p
	}
	h = fold(h)
	out["source_content_sha256"] = h
	t, p := text(value(m, "source_text", ""), "provenance.source_text", 1200, true, true)
	if p != nil {
		return nil, p
	}
	out["source_text"] = t
	start, a := integer(value(m, "line_start", 0))
	end, b := integer(value(m, "line_end", 0))
	if !a || !b {
		return nil, invalid("provenance line values must be integers")
	}
	out["line_start"], out["line_end"] = start, end
	if origin == "jd_candidate" {
		if !hexadecimal(h, 64) {
			return nil, invalid("JD candidate provenance requires the exact source hash")
		}
		if t == "" || start <= 0 || end < start {
			return nil, invalid("JD candidate provenance requires exact source text and line range")
		}
		if out["job_id"] == "" && out["discovery_id"] == "" {
			return nil, invalid("JD candidate provenance requires a Discovery or Job reference")
		}
	} else if h != "" || t != "" || start != 0 || end != 0 {
		return nil, invalid("Manual provenance cannot claim JD source evidence")
	}
	s, p := normalizedStated(m["stated_fields"], fallback)
	if p != nil {
		return nil, p
	}
	out["stated_fields"] = s
	return out, nil
}

// Only the closed stated-fields shape uses Python's ensure_ascii=True compact
// sorted JSON. The stored bytes are part of compatibility, not a UI encoding.
func statedJSON(m map[string]any) string {
	var out strings.Builder
	quote := func(s string) {
		out.WriteByte('"')
		for _, r := range s {
			switch r {
			case '"', '\\':
				out.WriteByte('\\')
				out.WriteRune(r)
			case '\b':
				out.WriteString(`\b`)
			case '\f':
				out.WriteString(`\f`)
			case '\n':
				out.WriteString(`\n`)
			case '\r':
				out.WriteString(`\r`)
			case '\t':
				out.WriteString(`\t`)
			default:
				if r >= 0x20 && r < 0x7f {
					out.WriteRune(r)
				} else {
					for _, u := range utf16.Encode([]rune{r}) {
						s := strconv.FormatInt(int64(u), 16)
						out.WriteString(`\u` + strings.Repeat("0", 4-len(s)) + s)
					}
				}
			}
		}
		out.WriteByte('"')
	}
	out.WriteString(`{"channels":[`)
	for i, v := range m["channels"].([]any) {
		if i > 0 {
			out.WriteByte(',')
		}
		c := v.(map[string]any)
		out.WriteString(`{"kind":`)
		quote(c["kind"].(string))
		out.WriteString(`,"value":`)
		quote(c["value"].(string))
		out.WriteByte('}')
	}
	out.WriteByte(']')
	for _, key := range []string{"company", "context", "name", "role"} {
		out.WriteByte(',')
		quote(key)
		out.WriteByte(':')
		quote(m[key].(string))
	}
	out.WriteByte('}')
	return out.String()
}

func readStated(raw string) (map[string]any, *Error) {
	if len(raw) > maxCellBytes || !utf8.ValidString(raw) || !json.Valid([]byte(raw)) {
		return nil, unsupported("PROVENANCE_JSON_UNSUPPORTED")
	}
	// Grammar was checked first. Refuse unpaired surrogate escapes, rather than
	// allowing encoding/json to replace a stored name/address with U+FFFD.
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		if i+5 >= len(raw) || raw[i+1] != 'u' {
			i++
			continue
		}
		code, _ := strconv.ParseUint(raw[i+2:i+6], 16, 16)
		i += 5
		if code >= 0xdc00 && code <= 0xdfff {
			return nil, unsupported("PROVENANCE_JSON_UNSUPPORTED")
		}
		if code >= 0xd800 && code <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return nil, unsupported("PROVENANCE_JSON_UNSUPPORTED")
			}
			low, _ := strconv.ParseUint(raw[i+3:i+7], 16, 16)
			if low < 0xdc00 || low > 0xdfff {
				return nil, unsupported("PROVENANCE_JSON_UNSUPPORTED")
			}
			i += 6
		}
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	var read func(int) (any, bool)
	read = func(depth int) (any, bool) {
		nodes++
		if depth > 5 || nodes > 256 {
			return nil, false
		}
		t, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		d, ok := t.(json.Delim)
		if !ok {
			return t, true
		}
		switch d {
		case '{':
			m := map[string]any{}
			for decoder.More() {
				k, err := decoder.Token()
				key, ok := k.(string)
				if err != nil || !ok {
					return nil, false
				}
				if _, exists := m[key]; exists {
					return nil, false
				}
				v, ok := read(depth + 1)
				if !ok {
					return nil, false
				}
				m[key] = v
			}
			end, err := decoder.Token()
			return m, err == nil && end == json.Delim('}')
		case '[':
			a := []any{}
			for decoder.More() {
				v, ok := read(depth + 1)
				if !ok {
					return nil, false
				}
				a = append(a, v)
			}
			end, err := decoder.Token()
			return a, err == nil && end == json.Delim(']')
		default:
			return nil, false
		}
	}
	v, ok := read(0)
	if !ok {
		return nil, unsupported("PROVENANCE_JSON_UNSUPPORTED")
	}
	normalized, p := normalizedStated(v, nil)
	if p != nil || normalized == nil || !reflect.DeepEqual(v, normalized) {
		return nil, unsupported("PROVENANCE_JSON_UNSUPPORTED")
	}
	return normalized, nil
}
