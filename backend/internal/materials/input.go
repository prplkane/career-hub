package materials

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

// Decode keeps the source JSON last-key-wins rule, but bounds lexical depth and
// rejects unpaired surrogates even if the corresponding field was overwritten.
func decode(raw []byte) (map[string]any, *Error) {
	if len(raw) > MaxInputBytes {
		return nil, failure("input", "MATERIAL_INPUT_LIMIT")
	}
	if !utf8.Valid(raw) {
		return nil, failure("input", "MATERIAL_UNICODE_UNSUPPORTED")
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
					return nil, failure("input", "MATERIAL_UNICODE_UNSUPPORTED")
				}
				if code < 0xd800 || code > 0xdbff {
					continue
				}
				if i+6 > len(raw) || string(raw[i:i+2]) != "\\u" {
					return nil, failure("input", "MATERIAL_UNICODE_UNSUPPORTED")
				}
				low, _ := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
				if low < 0xdc00 || low > 0xdfff {
					return nil, failure("input", "MATERIAL_UNICODE_UNSUPPORTED")
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
			return nil, failure("input", "MATERIAL_INPUT_LIMIT")
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var object map[string]any
	if d.Decode(&object) != nil || object == nil {
		return nil, invalid()
	}
	return object, nil
}

type model struct{ err *Error }

func (m *model) bad() {
	if m.err == nil {
		m.err = invalid()
	}
}
func (m *model) object(v any, fields ...string) map[string]any {
	x, ok := v.(map[string]any)
	if !ok {
		m.bad()
		return map[string]any{}
	}
	for key := range x {
		if !slices.Contains(fields, key) {
			m.bad()
		}
	}
	return x
}
func fallback(m map[string]any, k string, defaultValue any) any {
	if x, ok := m[k]; ok {
		return x
	}
	return defaultValue
}
func collapse(s string) string { return strings.Join(strings.FieldsFunc(s, textcompat.IsSpace), " ") }
func (m *model) text(v any, min, max int, normalize bool) string {
	s, ok := v.(string)
	length := utf8.RuneCountInString(s)
	if !ok || length < min || length > max {
		m.bad()
	}
	if normalize {
		s = collapse(s)
	}
	return s
}
func (m *model) enum(v any, values ...string) string {
	s, ok := v.(string)
	if !ok || !slices.Contains(values, s) {
		m.bad()
	}
	return s
}
func (m *model) list(v any, min, max int) []any {
	x, ok := v.([]any)
	if !ok || len(x) < min || len(x) > max {
		m.bad()
		return []any{}
	}
	return x
}
func (m *model) strings(v any, min, max int, normalization string, dedup, skipBlank bool) []string {
	list := m.list(v, min, max)
	out := []string{}
	seen := map[string]bool{}
	for _, x := range list {
		s := m.text(x, 0, MaxInputBytes, false)
		switch normalization {
		case "collapse":
			s = collapse(s)
		case "strip":
			s = textcompat.Strip(s)
		}
		if s == "" && normalization != "" {
			if skipBlank {
				continue
			}
			m.bad()
		}
		key := textcompat.Fold(s)
		if dedup && seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

var decimalInt = regexp.MustCompile(`^[+-]?[0-9]+(?:\.0+)?$`)

func (m *model) integer(v any, min, max int64, coerce bool) int64 {
	var s string
	fromString := false
	switch x := v.(type) {
	case json.Number:
		s = string(x)
	case string:
		fromString = true
		if !coerce {
			m.bad()
			return 0
		}
		s = textcompat.Strip(x)
		if !decimalInt.MatchString(s) {
			m.bad()
			return 0
		}
	case bool:
		if !coerce {
			m.bad()
			return 0
		}
		if x {
			s = "1"
		} else {
			s = "0"
		}
	default:
		m.bad()
		return 0
	}
	if len(s) > 64 {
		m.bad()
		return 0
	}
	if !strings.ContainsAny(s, ".eE") {
		if n, e := strconv.ParseInt(s, 10, 64); e == nil && n >= min && n <= max {
			return n
		}
		m.bad()
		return 0
	}
	// Coercive ints retain exact integer strings. JSON floating numbers follow
	// the source's binary64 conversion, within the safe integer range. Larger
	// revisions must use an integer token/string, not an imprecise float.
	if fromString && decimalInt.MatchString(s) && strings.Contains(s, ".") {
		p := strings.SplitN(s, ".", 2)
		if n, e := strconv.ParseInt(p[0], 10, 64); e == nil && n >= min && n <= max {
			return n
		}
	}
	n, e := strconv.ParseFloat(s, 64)
	if e != nil || math.IsInf(n, 0) || math.IsNaN(n) || math.Trunc(n) != n || n < 0 || n >= 1<<53 || n < float64(min) || n > float64(max) {
		m.bad()
		return 0
	}
	return int64(n)
}
func (m *model) hash(v any, prefix string) string {
	s := m.text(v, 0, MaxInputBytes, false)
	if len(s) != len(prefix)+64 || !strings.HasPrefix(s, prefix) {
		m.bad()
		return s
	}
	for _, r := range s[len(prefix):] {
		if !strings.ContainsRune("0123456789abcdef", r) {
			m.bad()
		}
	}
	return s
}

var coverID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,79}$`)
var bulletID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,99}$`)

func dateShape(s string) bool {
	r := []rune(s)
	if len(r) != 10 {
		return false
	}
	for i, c := range r {
		if i == 4 || i == 7 {
			if c != '-' {
				return false
			}
		} else {
			if _, ok := textcompat.DecimalDigit(c); !ok {
				return false
			}
		}
	}
	return true
}
func (m *model) id(v any, max int, trim bool) string {
	s := m.text(v, 1, max, false)
	if trim {
		s = textcompat.Strip(s)
	}
	p := coverID
	if max == 100 {
		p = bulletID
	}
	if !p.MatchString(s) {
		m.bad()
	}
	return s
}
func (m *model) job(v any) string {
	s := m.text(v, 1, 200, false)
	if s == "." || s == ".." || strings.ContainsAny(s, "/\\:\x00") {
		m.bad()
	}
	return s
}
func (m *model) candidate(v any) Candidate {
	x := m.object(v, "name", "email", "location_line", "phone", "linkedin", "github", "website")
	c := Candidate{m.text(x["name"], 1, 160, true), m.text(x["email"], 3, 320, true), m.text(fallback(x, "location_line", ""), 0, 240, true), m.text(fallback(x, "phone", ""), 0, 80, true), m.text(fallback(x, "linkedin", ""), 0, 500, true), m.text(fallback(x, "github", ""), 0, 500, true), m.text(fallback(x, "website", ""), 0, 500, true)}
	parts := strings.Split(c.Email, "@")
	valid := len(parts) == 2 && parts[0] != "" && len(parts[1]) >= 3 && strings.Contains(parts[1][1:len(parts[1])-1], ".")
	// The source regex permits internal repeated dots; this is a plausibility
	// check, not an email-delivery validator or automatic contact correction.
	for _, r := range c.Email {
		if textcompat.IsSpace(r) {
			valid = false
		}
	}
	if !valid {
		m.bad()
	}
	return c
}
func (m *model) origin(v any) string { return m.enum(v, "template", "user", "agent", "imported") }
func (m *model) coverEvidence(v any) CoverEvidence {
	x := m.object(v, "id", "label", "claim", "evidence_refs", "manual_checks", "origin")
	return CoverEvidence{m.id(x["id"], 80, true), m.text(x["label"], 1, 80, true), m.text(x["claim"], 1, 1200, true), m.strings(fallback(x, "evidence_refs", []any{}), 0, 20, "collapse", true, false), m.strings(fallback(x, "manual_checks", []any{}), 0, 10, "collapse", true, false), m.origin(fallback(x, "origin", "user"))}
}
func (m *model) coverContent(v any) CoverContent {
	x := m.object(v, "letter_date", "salutation", "opening", "evidence_lead_in", "evidence_items", "closing", "sign_off", "signature_name", "credential_line")
	c := CoverContent{Date: m.text(x["letter_date"], 0, MaxInputBytes, false), Salutation: m.text(x["salutation"], 1, 240, true), Opening: m.text(x["opening"], 1, 4000, true), EvidenceLeadIn: m.text(x["evidence_lead_in"], 1, 1000, true), EvidenceItems: []CoverEvidence{}, Closing: m.text(x["closing"], 1, 3000, true), SignOff: m.text(x["sign_off"], 1, 100, true), SignatureName: m.text(x["signature_name"], 1, 160, true), CredentialLine: m.text(fallback(x, "credential_line", ""), 0, 500, true)}
	if !dateShape(c.Date) {
		m.bad()
	}
	seen := map[string]bool{}
	for _, v := range m.list(fallback(x, "evidence_items", []any{}), 0, 4) {
		item := m.coverEvidence(v)
		if seen[item.ID] {
			m.bad()
		}
		seen[item.ID] = true
		c.EvidenceItems = append(c.EvidenceItems, item)
	}
	return c
}
func (m *model) cover(v any) CoverDraft {
	x := m.object(v, "schema_version", "id", "kind", "template_id", "job_id", "job_fingerprint", "profile_revision", "evidence_revision", "status", "company", "role_title", "candidate", "content")
	return CoverDraft{int(m.integer(fallback(x, "schema_version", json.Number("1")), 1, 1, false)), m.id(fallback(x, "id", "cover_letter_primary"), 80, false), m.enum(fallback(x, "kind", "cover_letter"), "cover_letter"), m.enum(fallback(x, "template_id", CoverTemplate), CoverTemplate), m.job(x["job_id"]), m.hash(x["job_fingerprint"], ""), m.hash(x["profile_revision"], "sha256:"), m.hash(x["evidence_revision"], "sha256:"), m.enum(fallback(x, "status", "draft"), "draft", "approved"), m.text(x["company"], 1, 240, true), m.text(x["role_title"], 1, 240, true), m.candidate(x["candidate"]), m.coverContent(x["content"])}
}
func ParseCover(raw []byte) (*CoverDraft, *Error) {
	x, e := decode(raw)
	if e != nil {
		return nil, e
	}
	m := model{}
	draft := m.cover(x)
	if m.err != nil {
		return nil, m.err
	}
	return &draft, nil
}

func (m *model) bullet(v any) ResumeBullet {
	x := m.object(v, "id", "text", "evidence_refs", "origin")
	return ResumeBullet{m.id(x["id"], 100, true), m.text(x["text"], 1, 1200, true), m.strings(x["evidence_refs"], 1, 10, "strip", true, false), m.origin(fallback(x, "origin", "user"))}
}
func (m *model) bullets(v any) []ResumeBullet {
	out := []ResumeBullet{}
	for _, v := range m.list(v, 0, 5) {
		out = append(out, m.bullet(v))
	}
	return out
}
func (m *model) experience(v any) ResumeExperience {
	x := m.object(v, "block_id", "company", "role", "dates", "location", "bullets")
	return ResumeExperience{m.text(x["block_id"], 1, 200, false), m.text(x["company"], 1, 300, true), m.text(x["role"], 1, 300, true), m.text(fallback(x, "dates", ""), 0, 200, true), m.text(fallback(x, "location", ""), 0, 200, true), m.bullets(fallback(x, "bullets", []any{}))}
}
func (m *model) project(v any) ResumeProject {
	x := m.object(v, "block_id", "name", "descriptor", "bullets")
	return ResumeProject{m.text(x["block_id"], 1, 200, false), m.text(x["name"], 1, 300, true), m.text(fallback(x, "descriptor", ""), 0, 500, true), m.bullets(fallback(x, "bullets", []any{}))}
}
func (m *model) skill(v any) ResumeSkill {
	x := m.object(v, "block_id", "label", "keywords")
	return ResumeSkill{m.text(x["block_id"], 1, 200, false), m.text(x["label"], 1, 200, true), m.strings(x["keywords"], 1, 100, "collapse", true, false)}
}
func (m *model) education(v any) ResumeEducation {
	x := m.object(v, "block_id", "record_type", "heading", "subheading", "location", "date_text", "details")
	return ResumeEducation{m.text(x["block_id"], 1, 200, false), m.enum(x["record_type"], "school", "certification"), m.text(x["heading"], 1, 500, true), m.text(fallback(x, "subheading", ""), 0, 500, true), m.text(fallback(x, "location", ""), 0, 300, true), m.text(fallback(x, "date_text", ""), 0, 200, true), m.strings(fallback(x, "details", []any{}), 0, 20, "collapse", false, true)}
}
func (m *model) resume(v any) ResumeDraft {
	x := m.object(v, "schema_version", "id", "kind", "template_id", "job_id", "job_fingerprint", "profile_revision", "evidence_revision", "plan_revision", "plan_fingerprint", "status", "company", "role_title", "target_pages", "candidate", "sections", "summary", "summary_evidence_refs", "experiences", "projects", "skills", "education")
	d := ResumeDraft{SchemaVersion: int(m.integer(fallback(x, "schema_version", json.Number("1")), 1, 1, false)), ID: m.enum(fallback(x, "id", "resume_primary"), "resume_primary"), Kind: m.enum(fallback(x, "kind", "resume"), "resume"), TemplateID: m.enum(fallback(x, "template_id", ResumeTemplate), ResumeTemplate), JobID: m.job(x["job_id"]), JobFingerprint: m.hash(x["job_fingerprint"], ""), ProfileRevision: m.hash(x["profile_revision"], "sha256:"), EvidenceRevision: m.hash(x["evidence_revision"], "sha256:"), PlanRevision: m.integer(x["plan_revision"], 0, math.MaxInt64, true), PlanFingerprint: m.hash(x["plan_fingerprint"], "sha256:"), Status: m.enum(fallback(x, "status", "draft"), "draft", "approved"), Company: m.text(x["company"], 1, 240, true), RoleTitle: m.text(x["role_title"], 1, 240, true), TargetPages: int(m.integer(fallback(x, "target_pages", json.Number("1")), 1, 2, false)), Candidate: m.candidate(x["candidate"]), Sections: []string{}, Summary: m.text(fallback(x, "summary", ""), 0, 2000, true), SummaryEvidenceRefs: m.strings(fallback(x, "summary_evidence_refs", []any{}), 0, 30, "", false, false), Experiences: []ResumeExperience{}, Projects: []ResumeProject{}, Skills: []ResumeSkill{}, Education: []ResumeEducation{}}
	seen := map[string]bool{}
	for _, v := range m.list(x["sections"], 0, 10000) {
		key := m.enum(v, "professional_summary", "technical_skills", "professional_experience", "projects", "education", "certifications")
		if seen[key] {
			m.bad()
		}
		seen[key] = true
		d.Sections = append(d.Sections, key)
	}
	seen = map[string]bool{}
	bulletIDs := map[string]bool{}
	checkBullets := func(bullets []ResumeBullet) {
		for _, b := range bullets {
			if bulletIDs[b.ID] {
				m.bad()
			}
			bulletIDs[b.ID] = true
		}
	}
	for _, v := range m.list(fallback(x, "experiences", []any{}), 0, 20) {
		i := m.experience(v)
		if seen[i.BlockID] {
			m.bad()
		}
		seen[i.BlockID] = true
		checkBullets(i.Bullets)
		d.Experiences = append(d.Experiences, i)
	}
	seen = map[string]bool{}
	for _, v := range m.list(fallback(x, "projects", []any{}), 0, 20) {
		i := m.project(v)
		if seen[i.BlockID] {
			m.bad()
		}
		seen[i.BlockID] = true
		checkBullets(i.Bullets)
		d.Projects = append(d.Projects, i)
	}
	seen = map[string]bool{}
	for _, v := range m.list(fallback(x, "skills", []any{}), 0, 30) {
		i := m.skill(v)
		if seen[i.BlockID] {
			m.bad()
		}
		seen[i.BlockID] = true
		d.Skills = append(d.Skills, i)
	}
	seen = map[string]bool{}
	for _, v := range m.list(fallback(x, "education", []any{}), 0, 30) {
		i := m.education(v)
		if seen[i.BlockID] {
			m.bad()
		}
		seen[i.BlockID] = true
		d.Education = append(d.Education, i)
	}
	return d
}
func ParseResume(raw []byte) (*ResumeDraft, *Error) {
	x, e := decode(raw)
	if e != nil {
		return nil, e
	}
	m := model{}
	draft := m.resume(x)
	if m.err != nil {
		return nil, m.err
	}
	return &draft, nil
}
func ParseCoverStore(raw []byte) (*CoverStore, *Error) {
	x, e := decode(raw)
	if e != nil {
		return nil, e
	}
	m := model{}
	x = m.object(x, "schema_version", "job_id", "revision", "updated_at", "materials")
	s := &CoverStore{SchemaVersion: int(m.integer(fallback(x, "schema_version", json.Number("1")), 1, 1, false)), JobID: m.text(x["job_id"], 0, MaxInputBytes, false), Revision: m.integer(x["revision"], 1, math.MaxInt64, true), UpdatedAt: m.text(x["updated_at"], 0, MaxInputBytes, false), Materials: []CoverDraft{}}
	for _, v := range m.list(fallback(x, "materials", []any{}), 0, 20) {
		s.Materials = append(s.Materials, m.cover(v))
	}
	if m.err != nil {
		return nil, m.err
	}
	return s, nil
}
func ParseResumeStore(raw []byte) (*ResumeStore, *Error) {
	x, e := decode(raw)
	if e != nil {
		return nil, e
	}
	m := model{}
	x = m.object(x, "schema_version", "job_id", "revision", "updated_at", "resume")
	s := &ResumeStore{int(m.integer(fallback(x, "schema_version", json.Number("1")), 1, 1, false)), m.text(x["job_id"], 0, MaxInputBytes, false), m.integer(x["revision"], 1, math.MaxInt64, true), m.text(x["updated_at"], 0, MaxInputBytes, false), m.resume(x["resume"])}
	if m.err != nil {
		return nil, m.err
	}
	return s, nil
}
