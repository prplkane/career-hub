package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"unicode/utf8"
)

// RawRevision fingerprints the exact custom-catalog bytes, not their decoded
// records. Missing files are represented by empty bytes by the external loader.
// Delimiters and labels are the Python _registry_revision contract.
func RawRevision(groups, skills []byte) string {
	digest := sha256.New()
	digest.Write([]byte("groups\x00"))
	digest.Write(groups)
	digest.Write([]byte("\x00skills\x00"))
	digest.Write(skills)
	digest.Write([]byte{0})
	return hex.EncodeToString(digest.Sum(nil))
}

func catalogRevision(groups []Group, skills []Term) (string, error) {
	groupValues := make([]any, 0, len(groups))
	for _, group := range groups {
		groupValues = append(groupValues, groupModel(group))
	}
	skillValues := make([]any, 0, len(skills))
	for _, skill := range skills {
		value := termModel(skill)
		// Legacy records predate these fields. Explicit non-null provenance is
		// hashed; null provenance is omitted independently of FieldsSet.
		if skill.CreatedVia == nil {
			delete(value, "created_via")
		}
		if skill.CreatedAt == nil {
			delete(value, "created_at")
		}
		skillValues = append(skillValues, value)
	}
	encoded, err := canonicalJSON(map[string]any{"groups": groupValues, "skills": skillValues})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// canonicalJSON covers only the model projection's JSON types. Unlike the
// sidecar wire encoder this is ensure_ascii=False. In particular U+2028/U+2029
// remain literal; replacing escaped strings after JSON encoding would corrupt
// literal backslash-u text. No files or runtime-dependent Unicode tables are used.
func canonicalJSON(value any) ([]byte, error) {
	var out bytes.Buffer
	if err := appendCanonical(&out, value); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func appendCanonical(out *bytes.Buffer, value any) error {
	switch item := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		if item {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case string:
		return appendCanonicalString(out, item)
	case []string:
		out.WriteByte('[')
		for i, text := range item {
			if i != 0 {
				out.WriteByte(',')
			}
			if err := appendCanonicalString(out, text); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case []any:
		out.WriteByte('[')
		for i, child := range item {
			if i != 0 {
				out.WriteByte(',')
			}
			if err := appendCanonical(out, child); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(item))
		for key := range item {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out.WriteByte('{')
		for i, key := range keys {
			if i != 0 {
				out.WriteByte(',')
			}
			if err := appendCanonicalString(out, key); err != nil {
				return err
			}
			out.WriteByte(':')
			if err := appendCanonical(out, item[key]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	default:
		return errors.New("catalog revision contains an unsupported projection type")
	}
	return nil
}

func appendCanonicalString(out *bytes.Buffer, value string) error {
	if !utf8.ValidString(value) {
		return errors.New("catalog revision requires valid UTF-8 scalar strings")
	}
	out.WriteByte('"')
	const hexDigits = "0123456789abcdef"
	for _, r := range value {
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
			if r < 0x20 {
				out.WriteString(`\u00`)
				out.WriteByte(hexDigits[(r>>4)&15])
				out.WriteByte(hexDigits[r&15])
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return nil
}
