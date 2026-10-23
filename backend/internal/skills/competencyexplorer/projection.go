// Package competencyexplorer projects explicit Career Vault skill links. It
// never infers vocabulary from prose, reads a JD, writes data or scores a person.
package competencyexplorer

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/textcompat"
	"careerhub.local/backend/internal/vault/calendarinput"
)

const MaxOutputBytes = 4 << 20

var inputPaths = [3]string{"career_vault/experience_blocks.yaml", "career_vault/project_blocks.yaml", "career_vault/education.yaml"}

// SourcePaths returns a copy of the fixed source inventory, not a mutable path
// allowlist. The owning reader independently rejects out-of-scope access.
func SourcePaths() [3]string { return inputPaths }

type Error struct{ Code string }

func (*Error) Error() string { return "Skill evidence could not be projected." }
func invalid() *Error        { return &Error{Code: "COMPETENCY_INPUT_INVALID"} }
func field(record cataloginput.Value, key string) cataloginput.Value {
	for _, entry := range record.Entries {
		if entry.Key.Kind == "string" && entry.Key.Text == key {
			return entry.Value
		}
	}
	return cataloginput.Value{}
}
func text(record cataloginput.Value, key string) (string, *Error) {
	v := field(record, key)
	if v.Kind != "string" || textcompat.Strip(v.Text) == "" {
		return "", invalid()
	}
	return textcompat.Strip(v.Text), nil
}
func stringsField(record cataloginput.Value, key string, allowNull bool) ([]string, *Error) {
	v := field(record, key)
	out := []string{}
	if v.Kind == "" || allowNull && v.Kind == "null" {
		return out, nil
	}
	if v.Kind != "sequence" {
		return nil, invalid()
	}
	seen := map[string]bool{}
	for _, item := range v.Items {
		if item.Kind != "string" {
			return nil, invalid()
		}
		value := textcompat.Strip(item.Text)
		if value != "" && !seen[value] {
			out = append(out, value)
			seen[value] = true
		}
	}
	return out, nil
}
func active(v cataloginput.Value) []cataloginput.Value {
	result := []cataloginput.Value{}
	for _, r := range v.Items {
		a := field(r, "archived")
		if r.Kind == "mapping" && !(a.Kind == "bool" && a.Boolean) {
			result = append(result, r)
		}
	}
	return result
}
func absent(v cataloginput.Value) bool {
	return v.Kind == "" || v.Kind == "null" || v.Kind == "string" && v.Text == ""
}
func interval(record cataloginput.Value, kind string) (*calendarinput.Range, *Error) {
	if kind == "certification" {
		value := field(record, "completed_month")
		if absent(value) || value.Kind == "bool" && !value.Boolean {
			value = field(record, "completed")
		}
		month, e := calendarinput.NormalizeMonth(value)
		if e != nil {
			return nil, nil
		}
		return &calendarinput.Range{Start: month, End: &month}, nil
	}
	raw := field(record, "date_range")
	if !absent(raw) {
		v, e := calendarinput.NormalizeRange(raw)
		if e != nil {
			return nil, invalid()
		}
		return &v, nil
	}
	dates := field(record, "dates")
	if dates.Kind != "string" {
		return nil, nil
	}
	v, e := calendarinput.NormalizeRangeFields(dates, cataloginput.Value{}, false)
	if e != nil {
		return nil, invalid()
	}
	return v.DateRange, nil
}

type node struct {
	id, kind, title string
	context         any
	interval        *calendarinput.Range
	proofs          int
	skills, related []string
	independent     bool
	size            int
}

func (n node) payload(direct bool, via []string) map[string]any {
	view := map[string]string{"experience": "vault", "project": "projects", "certification": "education"}[n.kind]
	value := map[string]any{"id": n.id, "source_type": n.kind, "title": n.title, "context": n.context, "date_interval": n.interval, "proof_count": n.proofs, "competency_ids": n.skills, "related_experience_ids": n.related, "is_independent_project": n.independent, "view": view, "record_id": n.id, "direct": direct}
	if !direct {
		value["via_project_ids"] = via
	}
	return value
}

func buildNodes(ctx context.Context, docs map[string]cataloginput.Value, skills map[string]catalog.Term, archived bool) (map[string]node, map[string][]string, *Error) {
	nodes := map[string]node{}
	edges := map[string][]string{}
	experiences := active(docs[inputPaths[0]])
	activeIDs := map[string]bool{}
	for _, r := range experiences {
		id, e := text(r, "id")
		if e != nil {
			return nil, nil, e
		}
		activeIDs[id] = true
	}
	collections := []struct {
		kind    string
		records []cataloginput.Value
	}{{"experience", experiences}, {"project", active(docs[inputPaths[1]])}, {"certification", active(field(docs[inputPaths[2]], "certifications"))}}
	links := 0
	for _, c := range collections {
		for _, r := range c.records {
			if ctx.Err() != nil {
				return nil, nil, &Error{Code: "COMPETENCY_CANCELED"}
			}
			id, e := text(r, "id")
			if e != nil {
				return nil, nil, e
			}
			name, contextKey := "name", ""
			if c.kind == "experience" {
				name, contextKey = "role", "company"
			}
			if c.kind == "certification" {
				contextKey = "credential"
			}
			title, e := text(r, name)
			if e != nil {
				return nil, nil, e
			}
			n := node{id: id, kind: c.kind, title: title, skills: []string{}, related: []string{}}
			if contextKey != "" {
				value, e := text(r, contextKey)
				if e != nil {
					return nil, nil, e
				}
				n.context = value
			}
			n.interval, e = interval(r, c.kind)
			if e != nil {
				return nil, nil, e
			}
			proof := field(r, "proof_points")
			if proof.Kind != "" && proof.Kind != "sequence" {
				return nil, nil, invalid()
			}
			for _, p := range proof.Items {
				if p.Kind != "string" {
					return nil, nil, invalid()
				}
				if textcompat.Strip(p.Text) != "" {
					n.proofs++
				}
			}
			ids, e := stringsField(r, "competency_ids", false)
			if e != nil {
				return nil, nil, e
			}
			for _, id := range ids {
				s, ok := skills[id]
				if !ok {
					return nil, nil, invalid()
				}
				if archived || !s.Archived {
					n.skills = append(n.skills, id)
				}
			}
			if c.kind == "project" {
				ids, e := stringsField(r, "related_experience", true)
				if e != nil {
					return nil, nil, e
				}
				n.independent = len(ids) == 0
				for _, id := range ids {
					if activeIDs[id] {
						n.related = append(n.related, id)
					}
				}
			}
			key := c.kind + ":" + id
			if _, exists := nodes[key]; exists {
				return nil, nil, invalid()
			}
			raw, eJSON := json.Marshal(n.payload(true, nil))
			if eJSON != nil {
				return nil, nil, invalid()
			}
			n.size = len(raw)
			nodes[key] = n
			for _, skill := range n.skills {
				edges[skill] = append(edges[skill], key)
				links++
				if links > 100000 {
					return nil, nil, &Error{Code: "COMPETENCY_OUTPUT_LIMIT"}
				}
			}
		}
	}
	return nodes, edges, nil
}

type span struct {
	start, end int
	active     bool
}

func monthIndex(value string) (int, bool) {
	if len(value) != 7 || value[4] != '-' {
		return 0, false
	}
	y, e1 := strconv.Atoi(value[:4])
	m, e2 := strconv.Atoi(value[5:])
	return y*12 + m - 1, e1 == nil && e2 == nil && y >= 1 && m >= 1 && m <= 12
}
func monthText(value int) string { return fmt.Sprintf("%04d-%02d", value/12, value%12+1) }
func union(values []*calendarinput.Range, asOf string) ([]span, *Error) {
	month, ok := monthIndex(asOf)
	if !ok {
		return nil, invalid()
	}
	spans := []span{}
	for _, v := range values {
		if v == nil {
			continue
		}
		start, ok := monthIndex(v.Start)
		if !ok {
			return nil, invalid()
		}
		if start > month {
			continue
		}
		end := start
		if v.Ongoing {
			end = month
		} else if v.End != nil {
			end, ok = monthIndex(*v.End)
			if !ok {
				return nil, invalid()
			}
		}
		if end < start {
			return nil, invalid()
		}
		spans = append(spans, span{start, min(end, month), month <= end})
	}
	slices.SortFunc(spans, func(a, b span) int {
		if a.start != b.start {
			return a.start - b.start
		}
		return a.end - b.end
	})
	merged := []span{}
	for _, v := range spans {
		if len(merged) == 0 || v.start > merged[len(merged)-1].end+1 {
			merged = append(merged, v)
		} else {
			last := &merged[len(merged)-1]
			last.end = max(last.end, v.end)
			last.active = last.active || v.active
		}
	}
	return merged, nil
}
func digest(value any) string {
	raw, _ := json.Marshal(value)
	return fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
}
func sortedKeys[T any](values map[string]T) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

// Build consumes already validated, byte-bound Vault/catalog inputs from the
// owning reader. Only explicit IDs produce evidence; legacy names stay inert.
func Build(ctx context.Context, docs map[string]cataloginput.Value, registry *catalog.Snapshot, vaultRevision, asOf string, archived bool) (json.RawMessage, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, &Error{Code: "COMPETENCY_CANCELED"}
	}
	if registry == nil || vaultRevision == "" {
		return nil, invalid()
	}
	if _, ok := monthIndex(asOf); !ok {
		return nil, invalid()
	}
	all := map[string]catalog.Term{}
	for _, skill := range registry.Skills() {
		all[skill.ID] = skill
	}
	nodes, edges, e := buildNodes(ctx, docs, all, archived)
	if e != nil {
		return nil, e
	}
	items := []map[string]any{}
	contexts := map[string]bool{}
	domains, kinds := map[string]bool{}, map[string]bool{}
	evidenceCount, activeCount, bytesUsed := 0, 0, 0
	for _, id := range sortedKeys(edges) {
		if ctx.Err() != nil {
			return nil, &Error{Code: "COMPETENCY_CANCELED"}
		}
		skill := all[id]
		groups := map[string][]map[string]any{"experiences": {}, "projects": {}, "certifications": {}}
		professional, sourceTypes, directExperiences := map[string]bool{}, map[string]bool{}, map[string]bool{}
		via := map[string][]string{}
		intervals := []*calendarinput.Range{}
		proofs, independent, certs := 0, 0, 0
		appendNode := func(n node, direct bool, via []string) bool {
			bytesUsed += n.size
			if bytesUsed > MaxOutputBytes {
				return false
			}
			groups[n.kind+"s"] = append(groups[n.kind+"s"], n.payload(direct, via))
			return true
		}
		for _, key := range edges[id] {
			n := nodes[key]
			if !appendNode(n, true, nil) {
				return nil, &Error{Code: "COMPETENCY_OUTPUT_LIMIT"}
			}
			proofs += n.proofs
			sourceTypes[n.kind] = true
			switch n.kind {
			case "experience":
				professional[n.id] = true
				directExperiences[n.id] = true
			case "project":
				for _, exp := range n.related {
					professional[exp] = true
					via[exp] = append(via[exp], n.id)
				}
				if n.independent {
					independent++
				}
				intervals = append(intervals, n.interval)
			case "certification":
				certs++
			}
		}
		for _, exp := range sortedKeys(professional) {
			n, exists := nodes["experience:"+exp]
			if !exists {
				return nil, invalid()
			}
			contexts[exp] = true
			intervals = append(intervals, n.interval)
			if !directExperiences[exp] {
				slices.Sort(via[exp])
				if !appendNode(n, false, via[exp]) {
					return nil, &Error{Code: "COMPETENCY_OUTPUT_LIMIT"}
				}
			}
		}
		merged, e := union(intervals, asOf)
		if e != nil {
			return nil, e
		}
		months := 0
		isActive := false
		var first, last any
		for _, v := range merged {
			months += v.end - v.start + 1
			isActive = isActive || v.active
		}
		if len(merged) > 0 {
			first, last = monthText(merged[0].start), monthText(merged[len(merged)-1].end)
		}
		for _, values := range groups {
			slices.SortFunc(values, func(a, b map[string]any) int {
				x, y := textcompat.Fold(a["title"].(string)), textcompat.Fold(b["title"].(string))
				if x != y {
					return compare(x, y)
				}
				return compare(a["id"].(string), b["id"].(string))
			})
		}
		domain := "unclassified"
		if skill.PrimaryDomain != nil && *skill.PrimaryDomain != "" {
			domain = *skill.PrimaryDomain
		}
		domains[domain] = true
		kinds[skill.Kind] = true
		types := []string{}
		for _, kind := range []string{"experience", "project", "certification"} {
			if sourceTypes[kind] {
				types = append(types, kind)
			}
		}
		item := map[string]any{"id": id, "name": skill.Name, "aliases": append([]string{}, skill.Aliases...), "kind": skill.Kind, "primary_domain": domain, "primary_group_id": catalog.EffectiveGroupID(skill), "origin": registry.SkillOrigin(id), "archived": skill.Archived, "evidence_count": len(edges[id]), "professional_context_count": len(professional), "independent_project_count": independent, "project_count": len(groups["projects"]), "certification_count": certs, "proof_count": proofs, "first_used": first, "last_used": last, "active_now": isActive, "experience_months": months, "source_types": types, "evidence": groups}
		items = append(items, item)
		evidenceCount += len(edges[id])
		if isActive {
			activeCount++
		}
	}
	slices.SortFunc(items, func(a, b map[string]any) int {
		x, y := textcompat.Fold(a["name"].(string)), textcompat.Fold(b["name"].(string))
		if x != y {
			return compare(x, y)
		}
		return compare(a["id"].(string), b["id"].(string))
	})
	graph := digest(map[string]any{"schema_version": 1, "vault_revision": vaultRevision, "catalog_revision": registry.CatalogRevision(), "include_archived_competencies": archived})
	result := map[string]any{"schema_version": 1, "read_only": true, "revision": digest(map[string]any{"schema_version": 1, "graph_revision": graph, "as_of_month": asOf}), "graph_revision": graph, "vault_revision": vaultRevision, "registry_revision": registry.Revision(), "catalog_revision": registry.CatalogRevision(), "include_archived_competencies": archived, "as_of_month": asOf, "summary": map[string]any{"competency_count": len(items), "evidence_count": evidenceCount, "professional_context_count": len(contexts), "active_now_count": activeCount}, "filters": map[string]any{"domains": sortedKeys(domains), "kinds": sortedKeys(kinds)}, "competencies": items}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, invalid()
	}
	if len(raw) > MaxOutputBytes {
		return nil, &Error{Code: "COMPETENCY_OUTPUT_LIMIT"}
	}
	return raw, nil
}
func compare(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
