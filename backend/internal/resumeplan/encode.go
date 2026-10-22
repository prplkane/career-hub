package resumeplan

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// PlanJSON preserves Pydantic's compact UTF-8 JSON spelling in the existing SQL
// field, including literal HTML characters and U+2028/U+2029. No fingerprint or
// schema change is needed. Only strings are rewritten; all keys/order/numbers
// come from the fixed Plan struct, not a generic caller-controlled encoder.
func PlanJSON(p Plan) (json.RawMessage, *Error) {
	raw, e := json.Marshal(p)
	if e != nil || len(raw) > MaxResponseBytes {
		return nil, failure("response", "RESUME_PLAN_RESPONSE_LIMIT")
	}
	var out bytes.Buffer
	for i := 0; i < len(raw); {
		if raw[i] != '"' {
			out.WriteByte(raw[i])
			i++
			continue
		}
		start := i
		i++
		for raw[i] != '"' {
			if raw[i] == '\\' {
				i++
			}
			i++
		}
		i++
		var value string
		if json.Unmarshal(raw[start:i], &value) != nil {
			return nil, invalid()
		}
		out.WriteByte('"')
		for _, r := range value {
			switch r {
			case '"':
				out.WriteString(`\"`)
			case '\\':
				out.WriteString(`\\`)
			case '\b':
				out.WriteString(`\b`)
			case '\t':
				out.WriteString(`\t`)
			case '\n':
				out.WriteString(`\n`)
			case '\f':
				out.WriteString(`\f`)
			case '\r':
				out.WriteString(`\r`)
			default:
				if r < 0x20 {
					fmt.Fprintf(&out, `\u%04x`, r)
				} else {
					out.WriteRune(r)
				}
			}
		}
		out.WriteByte('"')
	}
	if out.Len() > MaxPlanBytes {
		return nil, failure("input", "RESUME_PLAN_INPUT_LIMIT")
	}
	return out.Bytes(), nil
}
