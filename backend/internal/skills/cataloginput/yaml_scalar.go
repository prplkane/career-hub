package cataloginput

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v4"
)

// Pinned to the resolver rules of PyYAML 6.0.3, not Go's YAML 1.2 resolver.
var (
	yamlBoolPattern      = regexp.MustCompile(`^(?:yes|Yes|YES|no|No|NO|true|True|TRUE|false|False|FALSE|on|On|ON|off|Off|OFF)$`)
	yamlIntPattern       = regexp.MustCompile(`^[-+]?(?:0b[0-1_]+|0[0-7_]+|(?:0|[1-9][0-9_]*)|0x[0-9a-fA-F_]+|[1-9][0-9_]*(?::[0-5]?[0-9])+)$`)
	yamlFloatPattern     = regexp.MustCompile(`^(?:[-+]?(?:[0-9][0-9_]*)\.[0-9_]*(?:[eE][-+][0-9]+)?|\.[0-9][0-9_]*(?:[eE][-+][0-9]+)?|[-+]?[0-9][0-9_]*(?::[0-5]?[0-9])+\.[0-9_]*|[-+]?\.(?:inf|Inf|INF)|\.(?:nan|NaN|NAN))$`)
	yamlTimestampPattern = regexp.MustCompile(`^(?:[0-9]{4}-[0-9]{2}-[0-9]{2}|[0-9]{4}-[0-9]{1,2}-[0-9]{1,2}(?:[Tt]|[ \t]+)[0-9]{1,2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]*)?(?:[ \t]*(?:Z|[-+][0-9]{1,2}(?::[0-9]{2})?))?)$`)
	yamlTimestampParts   = regexp.MustCompile(`^([0-9]{4})-([0-9]{1,2})-([0-9]{1,2})(?:(?:[Tt]|[ \t]+)([0-9]{1,2}):([0-9]{2}):([0-9]{2})(?:\.([0-9]*))?(?:[ \t]*(Z|([-+])([0-9]{1,2})(?::([0-9]{2}))?))?)?$`)
)

func yamlPythonTag(n *yaml.Node) string {
	if n.Style&yaml.TaggedStyle != 0 {
		return n.Tag
	}
	if n.Kind == yaml.SequenceNode {
		return "!!seq"
	}
	if n.Kind == yaml.MappingNode {
		return "!!map"
	}
	if n.Tag != "!" && n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return "!!str"
	}
	v := strings.TrimSuffix(n.Value, "\n") // Python regex '$' also matches before a final newline.
	if yamlBoolPattern.MatchString(v) {
		return "!!bool"
	}
	if yamlFloatPattern.MatchString(v) {
		return "!!float"
	}
	if yamlIntPattern.MatchString(v) {
		return "!!int"
	}
	if v == "<<" {
		return "!!merge"
	}
	if (v == "" && n.Value == "") || v == "~" || v == "null" || v == "Null" || v == "NULL" {
		return "!!null"
	}
	if yamlTimestampPattern.MatchString(v) {
		return "!!timestamp"
	}
	if v == "=" {
		return "!!value"
	}
	return "!!str"
}

func yamlScalar(n *yaml.Node, tag string) (Value, *YAMLError) {
	v := n.Value
	switch tag {
	case "!!str":
		return Value{Kind: "string", Text: v}, nil
	case "!!null":
		return Value{Kind: "null"}, nil
	case "!!bool":
		switch strings.ToLower(v) {
		case "yes", "true", "on":
			return Value{Kind: "bool", Boolean: true}, nil
		case "no", "false", "off":
			return Value{Kind: "bool"}, nil
		default:
			return Value{}, yamlFailure(n, "invalid", "KeyError", "constructor", "YAML_BOOL_VALUE")
		}
	case "!!int":
		return yamlInteger(n)
	case "!!float":
		return yamlFloat(n)
	case "!!timestamp":
		return yamlTimestamp(n)
	case "!!binary":
		return yamlBinary(n)
	}
	return Value{}, yamlConstruction(n, "YAML_UNSUPPORTED_TAG")
}

func yamlNumberFailure(n *yaml.Node, code string) *YAMLError {
	for _, r := range n.Value {
		if r > 127 {
			return yamlFailure(n, "unsupported", "UnsupportedYAMLValue", "adapter", "YAML_UNICODE_NUMBER")
		}
	}
	return yamlFailure(n, "invalid", "ValueError", "constructor", code)
}

func yamlInteger(n *yaml.Node) (Value, *YAMLError) {
	s := strings.ReplaceAll(n.Value, "_", "")
	negative := strings.HasPrefix(s, "-")
	if strings.HasPrefix(s, "+") || negative {
		s = s[1:]
	}
	if len(s) > YAMLMaxIntegerDigits {
		return Value{}, yamlLimit(n, "YAML_INTEGER_DIGITS")
	}
	result := new(big.Int)
	if strings.Contains(s, ":") && !strings.HasPrefix(s, "0") {
		parts := strings.Split(s, ":")
		for _, p := range parts {
			x, ok := new(big.Int).SetString(strings.TrimSpace(p), 10)
			if !ok {
				return Value{}, yamlNumberFailure(n, "YAML_INT_VALUE")
			}
			result.Mul(result, big.NewInt(60))
			result.Add(result, x)
		}
	} else {
		base := 10
		switch {
		case strings.HasPrefix(s, "0b"):
			base = 2
			s = s[2:]
		case strings.HasPrefix(s, "0x"):
			base = 16
			s = s[2:]
		case strings.HasPrefix(s, "0"):
			base = 8
			if strings.HasPrefix(s, "0o") {
				s = s[2:]
			}
		}
		var ok bool
		result, ok = result.SetString(strings.TrimSpace(s), base)
		if !ok {
			return Value{}, yamlNumberFailure(n, "YAML_INT_VALUE")
		}
	}
	if negative {
		result.Neg(result)
	}
	text := result.String()
	if len(strings.TrimPrefix(text, "-")) > YAMLMaxIntegerDigits {
		return Value{}, yamlLimit(n, "YAML_INTEGER_DIGITS")
	}
	return Value{Kind: "int", Text: text}, nil
}

func yamlFloat(n *yaml.Node) (Value, *YAMLError) {
	s := strings.ToLower(strings.ReplaceAll(n.Value, "_", ""))
	negative := strings.HasPrefix(s, "-")
	if strings.HasPrefix(s, "+") || negative {
		s = s[1:]
	}
	var f float64
	var err error
	switch s {
	case ".inf", "inf", "infinity":
		f = math.Inf(1)
	case ".nan", "nan":
		f = math.NaN()
	default:
		if strings.Contains(s, "0x") {
			return Value{}, yamlNumberFailure(n, "YAML_FLOAT_VALUE")
		}
		if strings.Contains(s, ":") {
			// Match Python's reversed accumulation order, including rounding.
			parts := strings.Split(s, ":")
			base := 1.0
			for i := len(parts) - 1; i >= 0; i-- {
				var p float64
				p, err = strconv.ParseFloat(strings.TrimSpace(parts[i]), 64)
				if err != nil && !errorsFloatRange(err) {
					break
				}
				f += p * base
				base *= 60
			}
		} else {
			f, err = strconv.ParseFloat(strings.TrimSpace(s), 64)
		}
	}
	if err != nil && !errorsFloatRange(err) {
		return Value{}, yamlNumberFailure(n, "YAML_FLOAT_VALUE")
	}
	if negative {
		f = -f
	}
	return Value{Kind: "float", Text: yamlFloatRepr(f)}, nil
}

func errorsFloatRange(err error) bool {
	e, ok := err.(*strconv.NumError)
	return ok && e.Err == strconv.ErrRange
}

// CPython repr uses shortest round-trippable digits, fixed notation at exponents
// -4..15, .0 on integral fixed values, and at least two exponent digits.
func yamlFloatRepr(f float64) string {
	if math.IsNaN(f) {
		return "nan"
	}
	if math.IsInf(f, 1) {
		return "inf"
	}
	if math.IsInf(f, -1) {
		return "-inf"
	}
	e := strconv.FormatFloat(f, 'e', -1, 64)
	idx := strings.IndexByte(e, 'e')
	exp, _ := strconv.Atoi(e[idx+1:])
	if exp >= -4 && exp < 16 {
		s := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	return e
}

func yamlTimestamp(n *yaml.Node) (Value, *YAMLError) {
	m := yamlTimestampParts.FindStringSubmatch(strings.TrimSuffix(n.Value, "\n"))
	if m == nil {
		return Value{}, yamlFailure(n, "invalid", "AttributeError", "constructor", "YAML_TIMESTAMP_SYNTAX")
	}
	num := func(i int) int { n, _ := strconv.Atoi(m[i]); return n }
	y, mo, d, h, mi, s := num(1), num(2), num(3), num(4), num(5), num(6)
	fail := func() (Value, *YAMLError) {
		return Value{}, yamlFailure(n, "invalid", "ValueError", "constructor", "YAML_TIMESTAMP_VALUE")
	}
	if y < 1 || y > 9999 || mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 || s > 59 {
		return fail()
	}
	t := time.Date(y, time.Month(mo), d, h, mi, s, 0, time.UTC)
	if t.Day() != d || int(t.Month()) != mo {
		return fail()
	}
	if m[4] == "" {
		return Value{Kind: "date", Text: fmt.Sprintf("%04d-%02d-%02d", y, mo, d)}, nil
	}
	text := fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d", y, mo, d, h, mi, s)
	fraction := m[7]
	if len(fraction) > 6 {
		fraction = fraction[:6]
	}
	fraction += strings.Repeat("0", 6-len(fraction))
	if fraction != "000000" {
		text += "." + fraction
	}
	if m[8] != "" {
		offset := num(10)*60 + num(11)
		if offset >= 24*60 {
			return fail()
		}
		sign := "+"
		if m[9] == "-" && offset != 0 {
			sign = "-"
		}
		text += fmt.Sprintf("%s%02d:%02d", sign, offset/60, offset%60)
	}
	return Value{Kind: "datetime", Text: text}, nil
}

// PyYAML uses base64.decodebytes (not validate=True): ignore ASCII noise and
// excess padding, stop at a completed padded quartet, reject incomplete data.
func yamlBinary(n *yaml.Node) (Value, *YAMLError) {
	// ASCII encoding happens before decoding in PyYAML, even for characters
	// after padding that the permissive decoder would otherwise ignore.
	for _, r := range n.Value {
		if r > 127 {
			return Value{}, yamlConstruction(n, "YAML_BINARY_ASCII")
		}
	}
	out := make([]byte, 0, len(n.Value)/4*3)
	quartet := make([]byte, 0, 4)
	pads := 0
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	for _, r := range n.Value {
		if r == '=' {
			if len(quartet) < 2 {
				continue
			}
			pads++
			if len(quartet)+pads >= 4 {
				out = append(out, quartet[0]<<2|quartet[1]>>4)
				if len(quartet) == 3 {
					out = append(out, quartet[1]<<4|quartet[2]>>2)
				}
				return Value{Kind: "bytes", Bytes: out}, nil
			}
			continue
		}
		idx := strings.IndexRune(alphabet, r)
		if idx < 0 {
			continue
		}
		pads = 0
		quartet = append(quartet, byte(idx))
		if len(quartet) == 4 {
			out = append(out, quartet[0]<<2|quartet[1]>>4, quartet[1]<<4|quartet[2]>>2, quartet[2]<<6|quartet[3])
			quartet = quartet[:0]
		}
	}
	if len(quartet) != 0 {
		return Value{}, yamlConstruction(n, "YAML_BINARY_PADDING")
	}
	return Value{Kind: "bytes", Bytes: out}, nil
}
