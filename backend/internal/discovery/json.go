package discovery

import (
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// JSONDocument preserves arbitrary stored object fields. It does not interpret
// instructions, invoke tools, expose a mailbox, or synthesize absent evidence.
type JSONDocument map[string]any
type realNumber float64

func (n realNumber) MarshalJSON() ([]byte, error) {
	if math.IsNaN(float64(n)) || math.IsInf(float64(n), 0) {
		return nil, ErrUnsupported
	}
	return []byte(pythonFloatJSON(float64(n))), nil
}

// Python JSON retains REAL/float type and uses fixed notation in [1e-4,1e16).
// The same spelling is required when capture payloads are hashed for replay.
func pythonFloatJSON(n float64) string {
	s := strconv.FormatFloat(n, 'g', -1, 64)
	if n == 0 || math.Abs(n) >= 1e-4 && math.Abs(n) < 1e16 {
		s = strconv.FormatFloat(n, 'f', -1, 64)
	}
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}
func jsonNumbers(value any) any {
	switch v := value.(type) {
	case float64:
		return realNumber(v)
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			out[k] = jsonNumbers(x)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = jsonNumbers(x)
		}
		return out
	default:
		return value
	}
}
func (d JSONDocument) MarshalJSON() ([]byte, error) {
	return json.Marshal(jsonNumbers(map[string]any(d)))
}

func parseObject(raw string) (JSONDocument, error) {
	if len(raw) > MaxCellBytes || !utf8.ValidString(raw) || !json.Valid([]byte(raw)) {
		return nil, ErrUnsupported
	}
	// Inspect lexical tokens too: duplicate keys stay Python's last-wins, but
	// overwritten non-scalar escapes/oversized integers are not silently hidden.
	depth := 0
	for i := 0; i < len(raw); {
		switch raw[i] {
		case '"':
			i++
			for i < len(raw) && raw[i] != '"' {
				if raw[i] != '\\' {
					i++
					continue
				}
				if raw[i+1] != 'u' {
					i += 2
					continue
				}
				code, _ := strconv.ParseUint(raw[i+2:i+6], 16, 16)
				i += 6
				if code >= 0xdc00 && code <= 0xdfff {
					return nil, ErrUnsupported
				}
				if code < 0xd800 || code > 0xdbff {
					continue
				}
				if i+6 > len(raw) || raw[i:i+2] != "\\u" {
					return nil, ErrUnsupported
				}
				low, e := strconv.ParseUint(raw[i+2:i+6], 16, 16)
				if e != nil || low < 0xdc00 || low > 0xdfff {
					return nil, ErrUnsupported
				}
				i += 6
			}
			i++
		case '{', '[':
			depth++
			if depth > 128 {
				return nil, ErrUnsupported
			}
			i++
		case '}', ']':
			depth--
			i++
		case ',', ':', ' ', '\t', '\r', '\n':
			i++
		default:
			start := i
			for i < len(raw) && !strings.ContainsRune("{}[],: \t\r\n", rune(raw[i])) {
				i++
			}
			token := raw[start:i]
			if len(strings.TrimPrefix(token, "-")) > 4300 && !strings.ContainsAny(token, ".eE") {
				return nil, ErrUnsupported
			}
		}
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if decoder.Decode(&object) != nil || object == nil {
		return nil, ErrUnsupported
	}
	var convert func(any) (any, error)
	convert = func(value any) (any, error) {
		switch v := value.(type) {
		case json.Number:
			if strings.ContainsAny(string(v), ".eE") {
				number, e := v.Float64()
				if e != nil || math.IsInf(number, 0) || math.IsNaN(number) {
					return nil, ErrUnsupported
				}
				return number, nil
			}
			number, ok := new(big.Int).SetString(string(v), 10)
			if !ok {
				return nil, ErrUnsupported
			}
			return json.Number(number.String()), nil
		case map[string]any:
			for k, x := range v {
				y, e := convert(x)
				if e != nil {
					return nil, e
				}
				v[k] = y
			}
		case []any:
			for i, x := range v {
				y, e := convert(x)
				if e != nil {
					return nil, e
				}
				v[i] = y
			}
		}
		return value, nil
	}
	if _, err := convert(object); err != nil {
		return nil, err
	}
	return JSONDocument(object), nil
}
