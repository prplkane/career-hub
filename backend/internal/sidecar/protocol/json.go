package protocol

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

func decodeObject(raw []byte) (map[string]any, error) {
	if !utf8.Valid(raw) {
		return nil, failure("SIDECAR_RECORD_INVALID_UTF8")
	}
	normalized, constants, unsupported := normalizeConstants(raw)
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.UseNumber() // Keep Python's integer-vs-float distinction, including -0.
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, failure("SIDECAR_RECORD_INVALID_JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, failure("SIDECAR_RECORD_INVALID_JSON")
	}
	if unsupported != "" {
		return nil, &CompatibilityError{Reason: unsupported}
	}
	value, err := restoreNumbers(value, constants)
	if err != nil {
		return nil, err
	}
	payload, ok := value.(map[string]any)
	if !ok {
		return nil, failure("SIDECAR_RECORD_INVALID_SHAPE")
	}
	return payload, nil
}

// Python accepts these non-JSON constants. Substitute collision-free numeric
// tokens outside strings, then let encoding/json validate the entire grammar.
// json.Number retains their identity; ordinary duplicate object keys stay last-wins.
func normalizeConstants(raw []byte) ([]byte, map[string]float64, string) {
	constants := map[string]float64{}
	markers := map[string]string{}
	for i, name := range []string{"NaN", "Infinity", "-Infinity"} {
		marker := "9e9999" + strconv.Itoa(i)
		for bytes.Contains(raw, []byte(marker)) {
			marker += "9"
		}
		markers[name] = marker
		constants[marker] = []float64{math.NaN(), math.Inf(1), math.Inf(-1)}[i]
	}
	var out bytes.Buffer
	unsupported := ""
	depth := 0
	for i := 0; i < len(raw); {
		start := i
		if raw[i] == '"' {
			i++
			for i < len(raw) {
				if raw[i] == '\\' {
					i += 2
					continue
				}
				if raw[i] == '"' {
					i++
					break
				}
				i++
			}
			if i > len(raw) {
				i = len(raw)
			}
			token := raw[start:i]
			if unsupported == "" && containsLoneSurrogate(token) {
				unsupported = "escaped lone surrogate"
			}
			out.Write(token)
		} else if strings.ContainsRune("{}[],: \t\r\n", rune(raw[i])) {
			if raw[i] == '{' || raw[i] == '[' {
				depth++
			}
			if raw[i] == '}' || raw[i] == ']' {
				depth--
			}
			if depth > 900 && unsupported == "" {
				unsupported = "deeply nested input"
			}
			out.WriteByte(raw[i])
			i++
		} else {
			for i < len(raw) && !strings.ContainsRune("{}[],: \t\r\n\"", rune(raw[i])) {
				i++
			}
			token := string(raw[start:i])
			// Check all lexical integers, including subsequently overwritten keys.
			if len(strings.TrimPrefix(token, "-")) > 4300 && !strings.ContainsAny(token, ".eE") && unsupported == "" {
				unsupported = "Python integer digit limit"
			}
			if marker, ok := markers[token]; ok {
				out.WriteString(marker)
			} else {
				out.WriteString(token)
			}
		}
	}
	return out.Bytes(), constants, unsupported
}

func containsLoneSurrogate(token []byte) bool {
	for i := 0; i < len(token); i++ {
		if token[i] != '\\' {
			continue
		}
		if i+5 >= len(token) || token[i+1] != 'u' {
			i++
			continue
		}
		code, err := strconv.ParseUint(string(token[i+2:i+6]), 16, 16)
		if err != nil {
			continue
		} // The JSON decoder reports malformed escapes.
		i += 5
		if code >= 0xdc00 && code <= 0xdfff {
			return true
		}
		if code < 0xd800 || code > 0xdbff {
			continue
		}
		if i+6 >= len(token) || token[i+1] != '\\' || token[i+2] != 'u' {
			return true
		}
		low, err := strconv.ParseUint(string(token[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return true
		}
		i += 6
	}
	return false
}

func restoreNumbers(value any, constants map[string]float64) (any, error) {
	switch v := value.(type) {
	case json.Number:
		if special, ok := constants[string(v)]; ok {
			return special, nil
		}
		if !strings.ContainsAny(string(v), ".eE") {
			return v, nil
		}
		// ParseFloat returns +/-Inf with ErrRange, as Python does for 1e400.
		f, _ := strconv.ParseFloat(string(v), 64)
		return f, nil
	case map[string]any:
		for key, item := range v {
			updated, err := restoreNumbers(item, constants)
			if err != nil {
				return nil, err
			}
			v[key] = updated
		}
	case []any:
		for i, item := range v {
			updated, err := restoreNumbers(item, constants)
			if err != nil {
				return nil, err
			}
			v[i] = updated
		}
	}
	return value, nil
}

func integerEquals(value any, expected int) bool {
	if number, ok := value.(json.Number); ok {
		if number == "-0" {
			return expected == 0
		}
		return string(number) == strconv.Itoa(expected)
	}
	if value == nil {
		return false
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == int64(expected)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return expected >= 0 && v.Uint() == uint64(expected)
	}
	return false
}

// EncodeRecord supports the JSON wire value types emitted by helpers or read by
// ReadRecord: string-keyed maps, []any, nil, bool, scalar Unicode strings, Go
// integers, json.Number and float64. It emits Python compact sorted ensure_ascii
// JSON, including Python's non-finite extension. It is not a general object codec.
func EncodeRecord(payload map[string]any) ([]byte, error) {
	var out bytes.Buffer
	if err := encodeValue(&out, payload, 0); err != nil {
		return nil, err
	}
	if out.Len() > MaxRecordBytes {
		return nil, failure("SIDECAR_OUTPUT_TOO_LARGE")
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

func encodeValue(out *bytes.Buffer, value any, depth int) error {
	if depth > 1000 {
		return &CompatibilityError{Reason: "output nesting or cycle"}
	}
	switch v := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		out.WriteString(strconv.FormatBool(v))
	case string:
		return encodeString(out, v)
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := encodeString(out, key); err != nil {
				return err
			}
			out.WriteByte(':')
			if err := encodeValue(out, v[key], depth+1); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	case []any:
		out.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := encodeValue(out, item, depth+1); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case json.Number:
		if v == "" || (v[0] != '-' && (v[0] < '0' || v[0] > '9')) || strings.TrimSpace(string(v)) != string(v) || !json.Valid([]byte(v)) {
			return failure("SIDECAR_OUTPUT_INVALID")
		}
		if strings.ContainsAny(string(v), ".eE") {
			f, _ := strconv.ParseFloat(string(v), 64)
			out.WriteString(pythonFloat(f))
		} else if v == "-0" {
			out.WriteByte('0')
		} else {
			out.WriteString(string(v))
		}
	case float64:
		out.WriteString(pythonFloat(v))
	default:
		r := reflect.ValueOf(value)
		switch r.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			out.WriteString(strconv.FormatInt(r.Int(), 10))
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			out.WriteString(strconv.FormatUint(r.Uint(), 10))
		default:
			return failure("SIDECAR_OUTPUT_INVALID")
		}
	}
	return nil
}

func pythonFloat(value float64) string {
	if math.IsNaN(value) {
		return "NaN"
	}
	if math.IsInf(value, 1) {
		return "Infinity"
	}
	if math.IsInf(value, -1) {
		return "-Infinity"
	}
	format := byte('f')
	if abs := math.Abs(value); abs != 0 && (abs < 1e-4 || abs >= 1e16) {
		format = 'e'
	}
	text := strconv.FormatFloat(value, format, -1, 64)
	if !strings.ContainsAny(text, ".e") {
		text += ".0"
	}
	return text
}

func encodeString(out *bytes.Buffer, value string) error {
	if !utf8.ValidString(value) {
		return &CompatibilityError{Reason: "non-scalar Unicode output"}
	}
	out.WriteByte('"')
	const hex = "0123456789abcdef"
	escape := func(code rune) {
		out.WriteString(`\u`)
		for shift := 12; shift >= 0; shift -= 4 {
			out.WriteByte(hex[(code>>shift)&15])
		}
	}
	for _, char := range value {
		switch char {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteRune(char)
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
			if char < 0x20 || char >= 0x7f {
				if char > 0xffff {
					high, low := utf16.EncodeRune(char)
					escape(high)
					escape(low)
				} else {
					escape(char)
				}
			} else {
				out.WriteRune(char)
			}
		}
	}
	out.WriteByte('"')
	return nil
}
