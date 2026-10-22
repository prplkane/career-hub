package resumeplan

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/textcompat"
	"careerhub.local/backend/internal/vaultread"
)

type Source interface {
	ReadFile(string, int64) ([]byte, bool, *runtimepaths.Error)
}

func field(v cataloginput.Value, key string) cataloginput.Value {
	for _, e := range v.Entries {
		if e.Key.Kind == "string" && e.Key.Text == key {
			return e.Value
		}
	}
	return cataloginput.Value{Kind: "null"}
}
func active(v cataloginput.Value) bool {
	a := field(v, "archived")
	return v.Kind == "mapping" && !(a.Kind == "bool" && a.Boolean)
}
func stringValue(v cataloginput.Value) (string, bool) { return v.Text, v.Kind == "string" }
func sourceError() *Error                             { return failure("source", "RESUME_PLAN_SOURCE_UNSUPPORTED") }

// Only fields in the catalog projection are encoded. Unrelated YAML date/bytes
// extensions stay private and cannot break a plan by being JSON-encoded.
func projected(v cataloginput.Value) (any, *Error) {
	switch v.Kind {
	case "null":
		return nil, nil
	case "string":
		return v.Text, nil
	case "bool":
		return v.Boolean, nil
	case "int", "float":
		if json.Valid([]byte(v.Text)) {
			return json.Number(v.Text), nil
		}
	case "sequence":
		a := []any{}
		for _, i := range v.Items {
			x, e := projected(i)
			if e != nil {
				return nil, e
			}
			a = append(a, x)
		}
		return a, nil
	case "mapping":
		m := map[string]any{}
		for _, i := range v.Entries {
			if i.Key.Kind != "string" {
				return nil, sourceError()
			}
			x, e := projected(i.Value)
			if e != nil {
				return nil, e
			}
			m[i.Key.Text] = x
		}
		return m, nil
	}
	return nil, sourceError()
}

type projection struct{ err *Error }

func (p *projection) value(v cataloginput.Value, key string, defaultValue any) any {
	for _, entry := range v.Entries {
		if entry.Key.Kind == "string" && entry.Key.Text == key {
			x, e := projected(entry.Value)
			if e != nil {
				p.err = e
			}
			return x
		}
	}
	return defaultValue
}
func (p *projection) id(v cataloginput.Value) string {
	id, ok := stringValue(field(v, "id"))
	if !ok || id == "" {
		p.err = sourceError()
	}
	return id
}
func (p *projection) strings(v cataloginput.Value) []string {
	out := []string{}
	if v.Kind != "sequence" {
		p.err = sourceError()
		return out
	}
	for _, x := range v.Items {
		s, ok := stringValue(x)
		if !ok {
			p.err = sourceError()
		}
		out = append(out, s)
	}
	return out
}

func jobTerms(ctx context.Context, source Source, id string) ([]string, *Error) {
	terms := []string{}
	if id == "" {
		return terms, nil
	}
	if strings.ContainsAny(id, "/\\\x00") || id == "." || id == ".." || len(id) > 800 {
		return nil, failure("input", "RESUME_PLAN_JOB_ID_INVALID")
	}
	raw, present, e := source.ReadFile("jobs/normalized/"+id+".yaml", cataloginput.YAMLMaxInputBytes)
	if e != nil {
		return nil, failure("source", "RESUME_PLAN_JOB_SOURCE_UNAVAILABLE")
	}
	if !present {
		return terms, nil
	}
	v, pe := cataloginput.ParseYAML(raw)
	if pe != nil {
		if pe.Kind == "invalid" {
			return terms, nil
		}
		return nil, failure("input", "RESUME_PLAN_JOB_INPUT_UNSUPPORTED")
	}
	if v.Kind != "mapping" {
		return terms, nil
	}
	seen := map[string]bool{}
	total := 0
	appendTerms := func(list cataloginput.Value) {
		if list.Kind != "sequence" {
			return
		}
		for _, item := range list.Items {
			if item.Kind != "string" {
				continue
			}
			s := textcompat.Strip(item.Text)
			key := textcompat.Fold(s)
			if s != "" && !seen[key] {
				seen[key] = true
				terms = append(terms, s)
				total += len(s)
			}
		}
	}
	for _, key := range []string{"required_skills", "required_platforms", "preferred_skills"} {
		appendTerms(field(v, key))
	}
	groups := field(v, "technology_groups")
	if groups.Kind == "sequence" {
		for _, g := range groups.Items {
			if g.Kind == "mapping" {
				appendTerms(field(g, "technologies"))
			}
		}
	}
	if ctx.Err() != nil {
		return nil, failure("context", "RESUME_PLAN_CANCELED")
	}
	if len(terms) > 2000 || total > 1<<20 {
		return nil, failure("input", "RESUME_PLAN_MATCH_LIMIT")
	}
	return terms, nil
}

// Python's literal re.IGNORECASE uses single-codepoint equivalence, not full
// casefold (which would match sharp-S to 'ss'). No user regex is compiled.
func literalFold(s string) string {
	return strings.Map(func(r rune) rune {
		if r == 'İ' || r == 'ı' || r == 'I' || r == 'i' {
			return 'i'
		}
		best := r
		for n := unicode.SimpleFold(r); n != r; n = unicode.SimpleFold(n) {
			if n < best {
				best = n
			}
		}
		return best
	}, s)
}
func asciiLetterOrDigit(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}
func literalMatch(claim, term string) bool {
	for offset := 0; offset <= len(claim); {
		i := strings.Index(claim[offset:], term)
		if i < 0 {
			return false
		}
		i += offset
		j := i + len(term)
		before, after := rune(0), rune(0)
		if i > 0 {
			before, _ = utf8.DecodeLastRuneInString(claim[:i])
		}
		if j < len(claim) {
			after, _ = utf8.DecodeRuneInString(claim[j:])
		}
		if !asciiLetterOrDigit(before) && !asciiLetterOrDigit(after) {
			return true
		}
		_, n := utf8.DecodeRuneInString(claim[i:])
		offset = i + n
	}
	return false
}

func ReadCatalog(ctx context.Context, source Source, id string) (*Catalog, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("context", "RESUME_PLAN_CANCELED")
	}
	docs, revision, e := vaultread.ResumePlanSources(ctx, source)
	if e != nil {
		return nil, &Error{Stage: e.Stage, Code: e.Code, SourceClass: "ResumePlanError"}
	}
	terms, problem := jobTerms(ctx, source, id)
	if problem != nil {
		return nil, problem
	}
	folded := make([]string, len(terms))
	for i, s := range terms {
		folded[i] = literalFold(s)
	}
	proofs := map[string][]Proof{}
	budget := 128 << 20
	for _, proof := range docs["career_vault/proof_blocks.yaml"].Items {
		owner, ok1 := stringValue(field(proof, "source_block"))
		pid, ok2 := stringValue(field(proof, "id"))
		claim, ok3 := stringValue(field(proof, "claim"))
		if !ok1 || !ok2 || !ok3 || owner == "" || pid == "" || claim == "" {
			continue
		}
		matches := []string{}
		text := literalFold(claim)
		for i, term := range folded {
			if ctx.Err() != nil {
				return nil, failure("context", "RESUME_PLAN_CANCELED")
			}
			cost := len(text) + len(term)
			if cost > budget {
				return nil, failure("input", "RESUME_PLAN_MATCH_LIMIT")
			}
			budget -= cost
			if literalMatch(text, term) {
				matches = append(matches, terms[i])
			}
		}
		proofs[owner] = append(proofs[owner], Proof{pid, claim, &matches})
	}
	c := &Catalog{VaultRevision: revision, Experiences: []ExperienceSource{}, Projects: []ProjectSource{}, Skills: []SkillSource{}, Education: []EducationSource{}}
	p := projection{}
	for _, v := range docs["career_vault/experience_blocks.yaml"].Items {
		if !active(v) {
			continue
		}
		id := p.id(v)
		pp := proofs[id]
		if pp == nil {
			pp = []Proof{}
		}
		c.Experiences = append(c.Experiences, ExperienceSource{id, p.value(v, "company", ""), p.value(v, "role", ""), p.value(v, "dates", ""), p.value(v, "location", ""), p.value(v, "category", ""), p.value(v, "technologies", []any{}), pp})
	}
	for _, v := range docs["career_vault/project_blocks.yaml"].Items {
		if !active(v) {
			continue
		}
		id := p.id(v)
		pp := []Proof{}
		points := field(v, "proof_points")
		if points.Kind != "sequence" {
			return nil, sourceError()
		}
		for i, claim := range points.Items {
			if claim.Kind != "string" || textcompat.Strip(claim.Text) == "" {
				continue
			}
			h := sha256.Sum256([]byte(id + "\x00" + claim.Text))
			pid := fmt.Sprintf("%s_proof_%02d_%x", id, i+1, h[:6])
			pp = append(pp, Proof{ID: pid, Claim: claim.Text})
		}
		c.Projects = append(c.Projects, ProjectSource{id, p.value(v, "name", ""), p.value(v, "category", ""), p.value(v, "summary", ""), p.value(v, "technologies", []any{}), pp})
	}
	for _, v := range docs["career_vault/skill_blocks.yaml"].Items {
		if active(v) {
			c.Skills = append(c.Skills, SkillSource{p.id(v), p.value(v, "skill_family", ""), p.strings(field(v, "keywords"))})
		}
	}
	education := docs["career_vault/education.yaml"]
	for _, spec := range []struct {
		key, kind, name string
		details         []string
	}{{"schools", "school", "institution", []string{"degree", "major", "graduation_date"}}, {"certifications", "certification", "name", []string{"credential", "completed"}}} {
		for _, v := range field(education, spec.key).Items {
			if !active(v) {
				continue
			}
			parts := []string{}
			for _, k := range spec.details {
				x := field(v, k)
				if x.Kind == "null" || x.Kind == "string" && x.Text == "" || x.Kind == "bool" && !x.Boolean || x.Kind == "int" && x.Text == "0" {
					continue
				}
				if x.Kind != "string" {
					return nil, sourceError()
				}
				parts = append(parts, x.Text)
			}
			c.Education = append(c.Education, EducationSource{p.id(v), spec.kind, p.value(v, spec.name, ""), strings.Join(parts, " / ")})
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	if len(c.Experiences)+len(c.Projects)+len(c.Skills)+len(c.Education) > MaxItems {
		return nil, failure("input", "RESUME_PLAN_CATALOG_LIMIT")
	}
	if ctx.Err() != nil {
		return nil, failure("context", "RESUME_PLAN_CANCELED")
	}
	if _, problem = JSON(c); problem != nil {
		return nil, problem
	}
	return c, nil
}
