package assessmentcontext

import (
	"context"
	"math"
	"math/big"
	"sort"
	"strconv"
	"unicode/utf16"

	"careerhub.local/backend/internal/skills/cataloginput"
)

const maxProjectionBytes = 8 << 20
const maxProjectionNodes = 500000

type canonicalEncoder struct {
	ctx                  context.Context
	out                  []byte
	nodes                int
	err                  *Error
	preserveMappingOrder bool
}

// Ordered projection material is additional v2 evidence: source flatten_text
// visits nested mapping values in insertion order. V1 sorted JSON loses that.
func orderedJSON(ctx context.Context, value cataloginput.Value) ([]byte, *Error) {
	encoder := canonicalEncoder{ctx: ctx, preserveMappingOrder: true}
	encoder.value(value, 0)
	if encoder.err != nil {
		return nil, encoder.err
	}
	return encoder.out, nil
}

func canonicalJSON(ctx context.Context, value cataloginput.Value) ([]byte, *Error) {
	encoder := canonicalEncoder{ctx: ctx}
	encoder.value(value, 0)
	if encoder.err != nil {
		return nil, encoder.err
	}
	return encoder.out, nil
}
func (encoder *canonicalEncoder) fail(code string) {
	if encoder.err == nil {
		encoder.err = &Error{Stage: "serialization", Code: code}
	}
}
func (encoder *canonicalEncoder) add(value string) {
	if encoder.err != nil {
		return
	}
	if len(value) > maxProjectionBytes-len(encoder.out) {
		encoder.fail("ASSESSMENT_PROJECTION_LIMIT")
		return
	}
	encoder.out = append(encoder.out, value...)
}
func (encoder *canonicalEncoder) unicodeEscape(point rune) {
	const hex = "0123456789abcdef"
	encoder.add(string([]byte{'\\', 'u', hex[(point>>12)&15], hex[(point>>8)&15], hex[(point>>4)&15], hex[point&15]}))
}
func (encoder *canonicalEncoder) quoted(value string) {
	encoder.add(`"`)
	for _, point := range value {
		switch point {
		case '"', '\\':
			encoder.add("\\" + string(point))
		case '\b':
			encoder.add(`\b`)
		case '\f':
			encoder.add(`\f`)
		case '\n':
			encoder.add(`\n`)
		case '\r':
			encoder.add(`\r`)
		case '\t':
			encoder.add(`\t`)
		default:
			if point >= 0x20 && point < 0x7f {
				encoder.add(string(point))
			} else if point <= 0xffff {
				encoder.unicodeEscape(point)
			} else {
				first, last := utf16.EncodeRune(point)
				encoder.unicodeEscape(first)
				encoder.unicodeEscape(last)
			}
		}
		if encoder.err != nil {
			return
		}
	}
	encoder.add(`"`)
}
func floatText(value string) string {
	switch value {
	case "nan":
		return "NaN"
	case "inf":
		return "Infinity"
	case "-inf":
		return "-Infinity"
	}
	return value
}
func jsonKey(value cataloginput.Value) (string, bool) {
	switch value.Kind {
	case "string", "int":
		return value.Text, true
	case "float":
		return floatText(value.Text), true
	case "bool":
		if value.Boolean {
			return "true", true
		}
		return "false", true
	case "null":
		return "null", true
	}
	return "", false
}
func keyClass(value cataloginput.Value) string {
	switch value.Kind {
	case "int", "float", "bool":
		return "number"
	}
	return value.Kind
}

type numberKey struct {
	value    *big.Rat
	infinity int
}

func sortableNumber(value cataloginput.Value) (numberKey, bool) {
	result := numberKey{value: new(big.Rat)}
	switch value.Kind {
	case "bool":
		if value.Boolean {
			result.value.SetInt64(1)
		}
	case "int":
		number, ok := new(big.Int).SetString(value.Text, 10)
		if !ok {
			return result, false
		}
		result.value.SetInt(number)
	case "float":
		if value.Text == "nan" {
			return result, false
		}
		if value.Text == "inf" {
			result.infinity = 1
		} else if value.Text == "-inf" {
			result.infinity = -1
		} else {
			number, err := strconv.ParseFloat(value.Text, 64)
			if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
				return result, false
			}
			result.value.SetFloat64(number)
		}
	default:
		return result, false
	}
	return result, true
}

// Source sort_keys sorts original Python keys before JSON name conversion. A
// string/numeric mix therefore fails; sorting their stringified names is wrong.
// Multi-key NaN ordering is deliberately unsupported (non-total Python ordering).
func sortedEntries(entries []cataloginput.Entry) ([]cataloginput.Entry, bool) {
	result := append([]cataloginput.Entry(nil), entries...)
	for _, entry := range result {
		if _, ok := jsonKey(entry.Key); !ok {
			return nil, false
		}
	}
	if len(result) <= 1 {
		return result, true
	}
	class := keyClass(result[0].Key)
	for _, entry := range result {
		if keyClass(entry.Key) != class {
			return nil, false
		}
	}
	if class == "string" {
		sort.SliceStable(result, func(i, j int) bool { return result[i].Key.Text < result[j].Key.Text })
		return result, true
	}
	if class != "number" {
		return nil, false
	}
	type sortableEntry struct {
		entry cataloginput.Entry
		key   numberKey
	}
	values := make([]sortableEntry, len(result))
	for i, entry := range result {
		key, ok := sortableNumber(entry.Key)
		if !ok {
			return nil, false
		}
		values[i] = sortableEntry{entry, key}
	}
	sort.SliceStable(values, func(i, j int) bool {
		a, b := values[i].key, values[j].key
		if a.infinity != b.infinity {
			return a.infinity < b.infinity
		}
		if a.infinity != 0 {
			return false
		}
		return a.value.Cmp(b.value) < 0
	})
	for i, value := range values {
		result[i] = value.entry
	}
	return result, true
}
func (encoder *canonicalEncoder) value(value cataloginput.Value, depth int) {
	if encoder.err != nil {
		return
	}
	if err := canceled(encoder.ctx); err != nil {
		encoder.err = err
		return
	}
	encoder.nodes++
	if encoder.nodes > maxProjectionNodes || depth > cataloginput.YAMLMaxDepth+5 {
		encoder.fail("ASSESSMENT_PROJECTION_LIMIT")
		return
	}
	switch value.Kind {
	case "string":
		encoder.quoted(value.Text)
	case "int":
		encoder.add(value.Text)
	case "float":
		encoder.add(floatText(value.Text))
	case "null":
		encoder.add("null")
	case "bool":
		if value.Boolean {
			encoder.add("true")
		} else {
			encoder.add("false")
		}
	case "sequence":
		encoder.add("[")
		for i, item := range value.Items {
			if i > 0 {
				encoder.add(",")
			}
			encoder.value(item, depth+1)
			if encoder.err != nil {
				return
			}
		}
		encoder.add("]")
	case "mapping":
		entries, ok := sortedEntries(value.Entries)
		if !ok {
			encoder.fail("ASSESSMENT_PROJECTION_UNSUPPORTED")
			return
		}
		if encoder.preserveMappingOrder {
			entries = value.Entries
		}
		encoder.add("{")
		for i, entry := range entries {
			if i > 0 {
				encoder.add(",")
			}
			key, _ := jsonKey(entry.Key)
			encoder.quoted(key)
			encoder.add(":")
			encoder.value(entry.Value, depth+1)
			if encoder.err != nil {
				return
			}
		}
		encoder.add("}")
	default:
		// Source json.dumps rejects dates, binary and sets, even if these values
		// passed its more permissive candidate projection or manual Vault checks.
		encoder.fail("ASSESSMENT_PROJECTION_UNSUPPORTED")
	}
}
