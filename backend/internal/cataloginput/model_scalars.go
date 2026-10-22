package cataloginput

import (
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

var kinds = []string{"technology", "language", "framework", "library", "platform", "service", "tool", "protocol", "standard", "database", "methodology", "practice", "concept", "domain"}
var domains = []string{"ai_systems", "software_engineering", "data_and_analytics", "cloud_and_infrastructure", "security_and_identity", "web_and_api", "business_systems", "product_and_delivery", "other"}
var creationSources = []string{"manual_registry", "vault_editor", "resume_digest", "legacy_migration", "unspecified"}
var relationTypes = []string{"member_of", "uses", "implements", "replaces", "related_to"}

func (v *validation) stringValue(input Value, loc []any) (string, bool) {
	switch input.Kind {
	case "string":
		return input.Text, true
	case "bytes":
		if utf8.Valid(input.Bytes) {
			return string(input.Bytes), true
		}
		v.add("string_unicode", loc, "Input should be a valid string, unable to parse raw data as a unicode string")
	default:
		v.add("string_type", loc, "Input should be a valid string")
	}
	return "", false
}

func validIdentifier(text string) bool {
	if len(text) == 0 || text[0] < 'a' || text[0] > 'z' {
		return false
	}
	for i := 1; i < len(text); i++ {
		c := text[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

func (v *validation) identifier(input Value, loc []any) string {
	text, ok := v.stringValue(input, loc)
	if ok && !validIdentifier(text) {
		v.add("string_pattern_mismatch", loc, "String should match pattern '^[a-z][a-z0-9_]*$'")
	}
	return text
}

func (v *validation) optionalIdentifier(input Value, loc []any) *string {
	if input.Kind == "null" {
		return nil
	}
	text := v.identifier(input, loc)
	return &text
}

func (v *validation) nonblank(input Value, loc []any) string {
	text, ok := v.stringValue(input, loc)
	if ok {
		text = textcompat.Strip(text)
		if text == "" {
			v.add("value_error", loc, "Value error, must not be blank")
		}
	}
	return text
}

func (v *validation) strings(input Value, loc []any) ([]string, bool) {
	result := []string{}
	if input.Kind != "sequence" {
		v.add("list_type", loc, "Input should be a valid list")
		return result, false
	}
	ok := true
	for i, item := range input.Items {
		if v.unsupported != "" {
			return result, false
		}
		text, valid := v.stringValue(item, at(loc, i))
		ok = ok && valid
		result = append(result, text)
	}
	return result, ok
}

func (v *validation) aliases(input Value, loc []any) []string {
	result, ok := v.strings(input, loc)
	if ok {
		blank := false
		for i, text := range result {
			result[i] = textcompat.Strip(text)
			blank = blank || result[i] == ""
		}
		if blank {
			v.add("value_error", loc, "Value error, aliases must not contain blank values")
		}
	}
	return result
}

func (v *validation) cascade(input Value, loc []any) []string {
	result, ok := v.strings(input, loc)
	if !ok {
		return result
	}
	seen := make(map[string]bool, len(result))
	for _, text := range result {
		if seen[text] {
			v.add("value_error", loc, "Value error, must not contain duplicate skill ids")
			return result
		}
		seen[text] = true
	}
	for _, text := range result {
		if !validIdentifier(text) {
			v.add("value_error", loc, "Value error, must contain lower-case snake_case skill ids")
			break
		}
	}
	return result
}

func enumMessage(choices []string) string {
	quoted := make([]string, len(choices))
	for i, choice := range choices {
		quoted[i] = "'" + choice + "'"
	}
	return "Input should be " + strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
}

func (v *validation) enum(input Value, loc []any, choices []string) string {
	text := ""
	if input.Kind == "string" {
		text = input.Text
	} else if input.Kind == "bytes" && utf8.Valid(input.Bytes) {
		text = string(input.Bytes)
	}
	for _, choice := range choices {
		if text == choice {
			return text
		}
	}
	v.add("enum", loc, enumMessage(choices))
	return ""
}

func (v *validation) optionalEnum(input Value, loc []any, choices []string) *string {
	if input.Kind == "null" {
		return nil
	}
	text := v.enum(input, loc, choices)
	return &text
}

func asciiLower(text string) string {
	buf := []byte(text)
	for i, c := range buf {
		if c >= 'A' && c <= 'Z' {
			buf[i] = c + ('a' - 'A')
		}
	}
	return string(buf)
}

func (v *validation) boolean(input Value, loc []any) bool {
	parsing := false
	switch input.Kind {
	case "bool":
		return input.Boolean
	case "string", "bytes":
		text := input.Text
		if input.Kind == "bytes" {
			text = string(input.Bytes)
		}
		switch asciiLower(text) {
		case "1", "true", "t", "yes", "y", "on":
			return true
		case "0", "false", "f", "no", "n", "off":
			return false
		}
		parsing = true
	case "int":
		integer, err := strconv.ParseInt(input.Text, 10, 64)
		if err == nil {
			if integer == 0 || integer == 1 {
				return integer == 1
			}
			parsing = true
		}
	case "float":
		number, err := strconv.ParseFloat(input.Text, 64)
		if err == nil && !math.IsNaN(number) && !math.IsInf(number, 0) && math.Trunc(number) == number && number >= -9223372036854775808.0 && number < 9223372036854775808.0 {
			if number == 0 || number == 1 {
				return number == 1
			}
			parsing = true
		}
	}
	if parsing {
		v.add("bool_parsing", loc, "Input should be a valid boolean, unable to interpret input")
	} else {
		v.add("bool_type", loc, "Input should be a valid boolean")
	}
	return false
}

func validTimestamp(text string) bool {
	if len(text) < 20 || text[4] != '-' || text[7] != '-' || text[10] != 'T' || text[13] != ':' || text[16] != ':' || text[len(text)-1] != 'Z' {
		return false
	}
	for _, span := range [][2]int{{0, 4}, {5, 7}, {8, 10}, {11, 13}, {14, 16}, {17, 19}} {
		for i := span[0]; i < span[1]; i++ {
			if text[i] < '0' || text[i] > '9' {
				return false
			}
		}
	}
	if len(text) != 20 {
		if len(text) < 22 || text[19] != '.' {
			return false
		}
		fractionDigits := 0
		for _, digit := range text[20 : len(text)-1] {
			// CPython datetime parses the first six fractional digits only;
			// the model's preceding Unicode-aware regex still checks every
			// remaining digit. Non-ASCII Nd suffixes therefore remain valid.
			if fractionDigits < 6 && (digit < '0' || digit > '9') || !unicode15Decimal(digit) {
				return false
			}
			fractionDigits++
		}
	}
	// Parse only the seconds portion: Python accepts arbitrarily many fractional
	// digits and the model retains every original digit rather than rounding it.
	if text[:4] == "0000" {
		return false
	}
	_, err := time.Parse("2006-01-02T15:04:05", text[:19])
	return err == nil
}

func (v *validation) timestamp(input Value, loc []any) *string {
	if input.Kind == "null" {
		return nil
	}
	text, ok := v.stringValue(input, loc)
	if ok {
		text = textcompat.Strip(text)
		if !validTimestamp(text) {
			v.add("value_error", loc, "Value error, created_at must be an ISO 8601 UTC timestamp")
		}
	}
	return &text
}

func keyLocation(key Value) (any, bool) {
	switch key.Kind {
	case "null":
		return "None", true
	case "bool":
		if key.Boolean {
			return int64(1), true
		}
		return int64(0), true
	case "int":
		integer, err := strconv.ParseInt(key.Text, 10, 64)
		if err == nil {
			return integer, true
		}
		return key.Text, true
	case "float":
		return key.Text, true
	case "bytes":
		return bytesRepr(key.Bytes), true
	default:
		// Date/datetime repr locations and unhashable keys are explicitly outside
		// this slice; treating ISO strings as Python repr would be false parity.
		return nil, false
	}
}

func bytesRepr(raw []byte) string {
	quote := byte('\'')
	if strings.Contains(string(raw), "'") && !strings.Contains(string(raw), "\"") {
		quote = '"'
	}
	var result strings.Builder
	result.WriteByte('b')
	result.WriteByte(quote)
	const hex = "0123456789abcdef"
	for _, c := range raw {
		switch c {
		case '\\':
			result.WriteString("\\\\")
		case '\t':
			result.WriteString("\\t")
		case '\n':
			result.WriteString("\\n")
		case '\r':
			result.WriteString("\\r")
		default:
			if c == quote {
				result.WriteByte('\\')
				result.WriteByte(c)
			} else if c >= 0x20 && c <= 0x7e {
				result.WriteByte(c)
			} else {
				result.WriteString("\\x")
				result.WriteByte(hex[c>>4])
				result.WriteByte(hex[c&15])
			}
		}
	}
	result.WriteByte(quote)
	return result.String()
}
