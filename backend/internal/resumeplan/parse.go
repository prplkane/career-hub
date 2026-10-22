package resumeplan

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

// Decode first checks lexical scalar/depth limits, including overwritten keys.
// Duplicate keys retain the source JSON loader's last-wins semantics. No input
// is treated as code, a regular expression or a path.
func decode(raw []byte) (map[string]any, *Error) {
	if len(raw) > MaxPlanBytes {
		return nil, failure("input", "RESUME_PLAN_INPUT_LIMIT")
	}
	if !utf8.Valid(raw) {
		return nil, failure("input", "RESUME_PLAN_UNICODE_UNSUPPORTED")
	}
	if !json.Valid(raw) {
		return nil, invalid()
	}
	depth, nodes := 0, 0
	for i := 0; i < len(raw); {
		switch raw[i] {
		case '"':
			i++
			for raw[i] != '"' {
				if raw[i] != '\\' {
					i++
					continue
				}
				if raw[i+1] != 'u' {
					i += 2
					continue
				}
				code, _ := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
				i += 6
				if code >= 0xdc00 && code <= 0xdfff {
					return nil, failure("input", "RESUME_PLAN_UNICODE_UNSUPPORTED")
				}
				if code < 0xd800 || code > 0xdbff {
					continue
				}
				if i+6 > len(raw) || string(raw[i:i+2]) != "\\u" {
					return nil, failure("input", "RESUME_PLAN_UNICODE_UNSUPPORTED")
				}
				low, _ := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
				if low < 0xdc00 || low > 0xdfff {
					return nil, failure("input", "RESUME_PLAN_UNICODE_UNSUPPORTED")
				}
				i += 6
			}
			i++
			nodes++
		case '{', '[':
			depth++
			nodes++
			i++
		case '}', ']':
			depth--
			i++
		default:
			i++
		}
		if depth > 64 || nodes > 100000 {
			return nil, failure("input", "RESUME_PLAN_INPUT_LIMIT")
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var result map[string]any
	if d.Decode(&result) != nil || result == nil {
		return nil, invalid()
	}
	return result, nil
}

type model struct{ err *Error }

func (m *model) bad() {
	if m.err == nil {
		m.err = invalid()
	}
}
func (m *model) object(v any, keys ...string) map[string]any {
	x, ok := v.(map[string]any)
	if !ok {
		m.bad()
		return map[string]any{}
	}
	for key := range x {
		if !slices.Contains(keys, key) {
			m.bad()
		}
	}
	return x
}
func fallback(x map[string]any, key string, value any) any {
	if v, ok := x[key]; ok {
		return v
	}
	return value
}
func (m *model) str(v any, min, max int) string {
	x, ok := v.(string)
	n := utf8.RuneCountInString(x)
	if !ok || n < min || n > max {
		m.bad()
	}
	return x
}
func (m *model) enum(v any, choices ...string) string {
	x, ok := v.(string)
	if !ok || !slices.Contains(choices, x) {
		m.bad()
	}
	return x
}

var integerString = regexp.MustCompile(`^[+-]?[0-9]+(?:\.0+)?$`)

func number(v any, stringsAllowed bool) (int, bool) {
	var s string
	switch x := v.(type) {
	case json.Number:
		s = string(x)
	case string:
		if !stringsAllowed {
			return 0, false
		}
		s = textcompat.Strip(x)
		if !integerString.MatchString(s) {
			return 0, false
		}
	default:
		return 0, false
	}
	if len(s) > 64 {
		return 0, false
	}
	// JSON numbers use the source JSON loader's binary64 conversion. Do not
	// construct arbitrary-precision powers from a tiny attacker-chosen exponent.
	n, e := strconv.ParseFloat(s, 64)
	if e != nil || math.IsInf(n, 0) || math.IsNaN(n) || math.Trunc(n) != n {
		return 0, false
	}
	if n < 0 || n > 5 {
		return 0, false
	}
	return int(n), true
}
func (m *model) integer(v any, min, max int, coercion bool) int {
	n, ok := number(v, coercion)
	if !ok || n < min || n > max {
		m.bad()
	}
	return n
}
func (m *model) boolean(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case json.Number:
		n, ok := number(x, false)
		if ok && n <= 1 {
			return n == 1
		}
	case string:
		switch strings.ToLower(x) {
		case "1", "true", "t", "on", "yes", "y":
			return true
		case "0", "false", "f", "off", "no", "n":
			return false
		}
	}
	m.bad()
	return false
}
func (m *model) list(v any, limit int) []any {
	x, ok := v.([]any)
	if !ok {
		m.bad()
		return nil
	}
	if len(x) > limit {
		m.err = failure("input", "RESUME_PLAN_INPUT_LIMIT")
		return nil
	}
	return x
}
func (m *model) strings(v any, limit int) []string {
	items := m.list(v, MaxItems)
	if len(items) > limit {
		m.bad()
		return []string{}
	}
	x := []string{}
	for _, item := range items {
		x = append(x, m.str(item, 0, MaxPlanBytes))
	}
	return x
}
func hash(s string, prefix string) bool {
	if len(s) != len(prefix)+64 || !strings.HasPrefix(s, prefix) {
		return false
	}
	for _, r := range s[len(prefix):] {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// Parse normalizes only after field validation: invalid content in an excluded
// experience/project is still rejected, exactly as with the nested source model.
func Parse(raw []byte) (*Plan, *Error) {
	x, e := decode(raw)
	if e != nil {
		return nil, e
	}
	m := model{}
	x = m.object(x, "schema_version", "job_id", "status", "vault_revision", "job_fingerprint", "target_pages", "sections", "experiences", "projects", "skills", "education")
	p := &Plan{SchemaVersion: m.integer(fallback(x, "schema_version", json.Number("1")), 1, 1, false),
		JobID: m.str(x["job_id"], 1, 200), Status: m.enum(fallback(x, "status", "draft"), "draft", "approved"),
		VaultRevision: m.str(x["vault_revision"], 0, MaxPlanBytes), JobFingerprint: m.str(x["job_fingerprint"], 0, MaxPlanBytes),
		TargetPages: m.integer(fallback(x, "target_pages", json.Number("1")), 1, 2, false),
		Sections:    []Section{}, Experiences: []Experience{}, Projects: []Project{}, Skills: []Skill{}, Education: []Education{}}
	if !hash(p.VaultRevision, "sha256:") || !hash(p.JobFingerprint, "") {
		m.bad()
	}
	seen := map[string]bool{}
	for _, v := range m.list(x["sections"], MaxItems) {
		o := m.object(v, "key", "included")
		key := m.enum(o["key"], sectionKeys[:]...)
		if seen[key] {
			m.bad()
		}
		seen[key] = true
		p.Sections = append(p.Sections, Section{key, m.boolean(fallback(o, "included", true))})
	}
	if len(p.Sections) != len(sectionKeys) {
		m.bad()
	}
	for _, v := range m.list(x["experiences"], MaxItems) {
		o := m.object(v, "block_id", "decision", "presentation", "target_bullets", "proof_ids", "reason")
		item := Experience{BlockID: m.str(o["block_id"], 1, 200), Decision: m.enum(fallback(o, "decision", "exclude"), "include", "exclude", "undecided"), ProofIDs: m.strings(fallback(o, "proof_ids", []any{}), 20), Reason: m.str(fallback(o, "reason", ""), 0, 1000)}
		if o["presentation"] != nil {
			v := m.enum(o["presentation"], "full", "compact")
			item.Presentation = &v
		}
		if o["target_bullets"] != nil {
			v := m.integer(o["target_bullets"], 2, 5, true)
			item.TargetBullets = &v
		}
		if item.Decision == "include" {
			n := 2
			if item.TargetBullets != nil {
				n = *item.TargetBullets
			}
			presentation := "full"
			if n == 2 {
				presentation = "compact"
			}
			item.TargetBullets, item.Presentation = &n, &presentation
		} else {
			item.Decision = "exclude"
			item.TargetBullets, item.Presentation, item.ProofIDs = nil, nil, []string{}
		}
		p.Experiences = append(p.Experiences, item)
	}
	for _, v := range m.list(fallback(x, "projects", []any{}), MaxItems) {
		o := m.object(v, "block_id", "proof_ids", "reason")
		p.Projects = append(p.Projects, Project{m.str(o["block_id"], 1, 200), m.strings(fallback(o, "proof_ids", []any{}), 10), m.str(fallback(o, "reason", ""), 0, 1000)})
	}
	for _, v := range m.list(fallback(x, "skills", []any{}), MaxItems) {
		o := m.object(v, "block_id", "keywords")
		p.Skills = append(p.Skills, Skill{m.str(o["block_id"], 1, 200), m.strings(fallback(o, "keywords", []any{}), 100)})
	}
	for _, v := range m.list(fallback(x, "education", []any{}), MaxItems) {
		o := m.object(v, "block_id", "record_type")
		p.Education = append(p.Education, Education{m.str(o["block_id"], 1, 200), m.enum(o["record_type"], "school", "certification")})
	}
	for _, s := range p.Sections {
		if s.Key == "projects" && !s.Included {
			p.Projects = []Project{}
		}
	}
	if m.err != nil {
		return nil, m.err
	}
	return p, nil
}
