// Package demandgaps joins saved-job vocabulary with explicit Vault evidence.
// It performs no I/O, infers no candidate skills, and never scores a person.
package demandgaps

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf16"

	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/skills/resolver"
	"careerhub.local/backend/internal/textcompat"
)

const MaxOutputBytes = 4 << 20
const MaxJobs = 10000

type Error struct{ Code string }

func (*Error) Error() string { return "Saved-job demand could not be projected." }
func invalid() *Error        { return &Error{Code: "DEMAND_INPUT_INVALID"} }

type Period struct {
	Start *string `json:"start"`
	End   *string `json:"end"`
}
type Request struct{ From, To, AsOf string }

// Validate accepts the same inclusive calendar dates and evidence month as the
// browser contract. The adapter trims query values before constructing it.
func (r Request) Validate() *Error {
	for _, v := range []string{r.From, r.To} {
		if v != "" {
			if _, e := time.Parse("2006-01-02", v); e != nil || len(v) != 10 || v[:4] == "0000" {
				return &Error{Code: "DEMAND_PERIOD_INVALID"}
			}
		}
	}
	if r.From != "" && r.To != "" && r.From > r.To {
		return &Error{Code: "DEMAND_PERIOD_INVALID"}
	}
	if r.AsOf != "" {
		if _, e := time.Parse("2006-01", r.AsOf); e != nil || len(r.AsOf) != 7 || r.AsOf[:4] == "0000" {
			return &Error{Code: "DEMAND_PERIOD_INVALID"}
		}
	}
	return nil
}

// Job contains a validated normalized card and its native ledger summary.
// Unregistered cards are counted separately and are not parsed by the owner.
type Job struct {
	ID, CapturedAt, Fingerprint string
	Card                        cataloginput.Value
}
type occurrence struct {
	JobID       string `json:"job_id"`
	ID          string `json:"competency_id"`
	Name        string `json:"canonical_name"`
	Text        string `json:"source_text"`
	Group       string `json:"primary_group_id"`
	Origin      string `json:"origin"`
	Requirement string `json:"requirement"`
	Alternative bool   `json:"in_alternative_group"`
	Source      string `json:"source_kind"`
	Line        any    `json:"line"`
	Start       any    `json:"start"`
	End         any    `json:"end"`
}

func field(v cataloginput.Value, key string) cataloginput.Value {
	for _, entry := range v.Entries {
		if entry.Key.Kind == "string" && entry.Key.Text == key {
			return entry.Value
		}
	}
	return cataloginput.Value{}
}
func str(v cataloginput.Value) string { return textcompat.Strip(v.Text) }
func number(v cataloginput.Value) any {
	if v.Kind == "int" {
		return json.Number(v.Text)
	}
	return nil
}
func position(v any) int {
	if n, ok := v.(json.Number); ok {
		i, _ := n.Int64()
		return int(i)
	}
	return -1
}
func keys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func occurrences(job Job, registry *catalog.Snapshot, terms map[string]catalog.Term, resolve *resolver.Resolver) ([]occurrence, *Error) {
	result := []occurrence{}
	add := func(skill catalog.Term, text, requirement, source string, alternative bool) occurrence {
		return occurrence{JobID: job.ID, ID: skill.ID, Name: skill.Name, Text: text, Group: catalog.EffectiveGroupID(skill), Origin: registry.SkillOrigin(skill.ID), Requirement: requirement, Source: source, Alternative: alternative}
	}
	current := field(field(job.Card, "digest"), "skill_occurrences")
	if current.Kind != "" {
		if current.Kind != "sequence" {
			return nil, invalid()
		}
		for _, raw := range current.Items {
			if raw.Kind != "mapping" {
				return nil, invalid()
			}
			req := str(field(raw, "requirement"))
			if req != "required" && req != "preferred" {
				continue
			}
			skill, ok := terms[str(field(raw, "skill_id"))]
			if !ok {
				return nil, invalid()
			}
			if skill.Archived {
				continue
			}
			o := add(skill, str(field(raw, "source_text")), req, "digest_occurrence", field(raw, "in_alternative_group").Boolean)
			if group := str(field(raw, "primary_group_id")); group != "" {
				o.Group = group
			}
			if origin := str(field(raw, "origin")); origin != "" {
				o.Origin = origin
			}
			o.Line, o.Start, o.End = number(field(raw, "line")), number(field(raw, "start")), number(field(raw, "end"))
			result = append(result, o)
		}
		return result, nil // An explicitly empty digest does not fall back to lists.
	}
	legacy := func(values cataloginput.Value, req string, alternative bool) {
		for _, raw := range values.Items {
			if raw.Kind != "string" || str(raw) == "" {
				continue
			}
			text := str(raw)
			resolution := resolve.Resolve(text)
			if resolution.CompetencyID == nil {
				continue
			}
			skill := terms[*resolution.CompetencyID]
			if skill.Archived {
				continue
			}
			source := "legacy_" + req + "_list"
			if alternative {
				source = "legacy_technology_group"
			}
			result = append(result, add(skill, text, req, source, alternative))
		}
	}
	legacy(field(job.Card, "required_skills"), "required", false)
	legacy(field(job.Card, "required_platforms"), "required", false)
	legacy(field(job.Card, "preferred_skills"), "preferred", false)
	for _, group := range field(job.Card, "technology_groups").Items {
		req := str(field(group, "requirement"))
		if req == "required" || req == "preferred" {
			legacy(field(group, "technologies"), req, true)
		}
	}
	return result, nil
}

// revision reproduces sorted compact ensure_ascii=True JSON used by the
// historical demand contract, including supplementary Unicode characters.
func revision(value any) string {
	raw, _ := json.Marshal(value)
	var normalized any
	_ = json.Unmarshal(raw, &normalized)
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(normalized)
	var ascii strings.Builder
	for _, r := range strings.TrimSuffix(encoded.String(), "\n") {
		if r < 128 {
			ascii.WriteRune(r)
		} else if r <= 0xffff {
			fmt.Fprintf(&ascii, "\\u%04x", r)
		} else {
			a, b := utf16.EncodeRune(r)
			fmt.Fprintf(&ascii, "\\u%04x\\u%04x", a, b)
		}
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(ascii.String())))
}

// Build consumes the existing competency projection from the SAME catalog
// snapshot. Revision checks prevent joining independently drifting catalogs.
func Build(ctx context.Context, registry *catalog.Snapshot, evidence json.RawMessage, input []Job, normalized, registered int, request Request) (json.RawMessage, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, &Error{Code: "DEMAND_CANCELED"}
	}
	if e := request.Validate(); e != nil {
		return nil, e
	}
	if registry == nil || len(evidence) > MaxOutputBytes || len(input) > MaxJobs || normalized < registered || registered != len(input) || normalized > MaxJobs {
		return nil, invalid()
	}
	var graph struct {
		Revision string           `json:"revision"`
		Vault    string           `json:"vault_revision"`
		Registry string           `json:"registry_revision"`
		Catalog  string           `json:"catalog_revision"`
		Month    string           `json:"as_of_month"`
		Archived bool             `json:"include_archived_competencies"`
		Items    []map[string]any `json:"competencies"`
	}
	if json.Unmarshal(evidence, &graph) != nil || graph.Catalog != registry.CatalogRevision() || graph.Archived || graph.Revision == "" || graph.Month == "" || request.AsOf != "" && graph.Month != request.AsOf {
		return nil, invalid()
	}
	terms := map[string]catalog.Term{}
	resolutionTerms := []resolver.Term{}
	for _, term := range registry.Skills() {
		terms[term.ID] = term
		resolutionTerms = append(resolutionTerms, resolver.Term{ID: term.ID, Name: term.Name, Aliases: term.Aliases, Archived: term.Archived})
	}
	resolve, e := resolver.New(resolutionTerms)
	if e != nil {
		return nil, invalid()
	}
	jobs := []Job{}
	jobIDs := map[string]bool{}
	for _, job := range input {
		if job.ID == "" || jobIDs[job.ID] || str(field(job.Card, "id")) != job.ID || len(job.CapturedAt) < 10 {
			return nil, invalid()
		}
		jobIDs[job.ID] = true
		date := job.CapturedAt[:10]
		if (Request{From: date}).Validate() != nil {
			return nil, invalid()
		}
		if request.From != "" && date < request.From || request.To != "" && date > request.To {
			continue
		}
		jobs = append(jobs, job)
	}
	slices.SortFunc(jobs, func(a, b Job) int {
		if a.CapturedAt != b.CapturedAt {
			return strings.Compare(a.CapturedAt, b.CapturedAt)
		}
		return strings.Compare(a.ID, b.ID)
	})
	saved := len(jobs)
	duplicates := 0
	seen := map[string]bool{}
	all := []occurrence{}
	jobRows := []map[string]any{}
	revisionJobs := []map[string]any{}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return nil, &Error{Code: "DEMAND_CANCELED"}
		}
		fingerprint := textcompat.Strip(job.Fingerprint)
		identity := "job:" + job.ID
		if fingerprint != "" {
			identity = "sha256:" + fingerprint
		}
		if seen[identity] {
			duplicates++
			continue
		}
		seen[identity] = true
		items, problem := occurrences(job, registry, terms, resolve)
		if problem != nil {
			return nil, problem
		}
		all = append(all, items...)
		if len(all) > 100000 {
			return nil, &Error{Code: "DEMAND_OUTPUT_LIMIT"}
		}
		required, preferred := map[string]bool{}, map[string]bool{}
		for _, item := range items {
			if item.Requirement == "required" {
				required[item.ID] = true
			} else {
				preferred[item.ID] = true
			}
		}
		row := map[string]any{"job_id": job.ID, "company": str(field(job.Card, "company")), "title": str(field(job.Card, "title")), "captured_at": job.CapturedAt}
		jobRows = append(jobRows, row)
		revisionJobs = append(revisionJobs, map[string]any{"job_id": job.ID, "company": row["company"], "title": row["title"], "captured_at": job.CapturedAt, "content_fingerprint": fingerprint, "required_competency_ids": keys(required), "preferred_competency_ids": keys(preferred)})
	}
	slices.SortStableFunc(all, func(a, b occurrence) int {
		for _, pair := range [][2]string{{a.JobID, b.JobID}, {a.ID, b.ID}, {a.Requirement, b.Requirement}} {
			if pair[0] != pair[1] {
				return strings.Compare(pair[0], pair[1])
			}
		}
		if position(a.Line) != position(b.Line) {
			return position(a.Line) - position(b.Line)
		}
		if position(a.Start) != position(b.Start) {
			return position(a.Start) - position(b.Start)
		}
		return strings.Compare(textcompat.Normalize(a.Text), textcompat.Normalize(b.Text))
	})
	period := Period{optional(request.From), optional(request.To)}
	demandRevision := revision(map[string]any{"schema_version": 1, "catalog_revision": registry.CatalogRevision(), "period": period, "jobs": revisionJobs, "occurrences": all})
	required, preferred := map[string]map[string]bool{}, map[string]map[string]bool{}
	ids := map[string]bool{}
	evidenceByID := map[string]map[string]any{}
	for _, item := range graph.Items {
		id, ok := item["id"].(string)
		if !ok {
			return nil, invalid()
		}
		ids[id] = true
		evidenceByID[id] = item
	}
	for _, item := range all {
		target := required
		if item.Requirement == "preferred" {
			target = preferred
		}
		if target[item.ID] == nil {
			target[item.ID] = map[string]bool{}
		}
		target[item.ID][item.JobID] = true
		ids[item.ID] = true
	}
	rows := []map[string]any{}
	domains := map[string]bool{}
	summary := map[string]int{"requested_competency_count": 0, "supported": 0, "thin_evidence": 0, "evidence_not_found": 0}
	for _, id := range keys(ids) {
		if ctx.Err() != nil {
			return nil, &Error{Code: "DEMAND_CANCELED"}
		}
		term, ok := terms[id]
		if !ok || term.Archived {
			return nil, invalid()
		}
		ev := evidenceByID[id]
		count := func(key string) int { n, _ := ev[key].(float64); return int(n) }
		domain := "unclassified"
		if term.PrimaryDomain != nil && *term.PrimaryDomain != "" {
			domain = *term.PrimaryDomain
		}
		union := map[string]bool{}
		for j := range required[id] {
			union[j] = true
		}
		for j := range preferred[id] {
			union[j] = true
		}
		status, explanation := "not_requested", "Not requested in saved jobs for the selected period."
		if len(union) > 0 {
			if count("evidence_count") == 0 {
				status, explanation = "evidence_not_found", "Evidence not found in Career Vault."
			} else if count("professional_context_count") > 0 {
				status = "supported"
				suffix := "context"
				if count("professional_context_count") != 1 {
					suffix = "contexts"
				}
				explanation = fmt.Sprintf("Career Vault contains evidence in %d professional %s.", count("professional_context_count"), suffix)
			} else {
				status, explanation = "thin_evidence", "Career Vault contains project or certification evidence, but no professional Experience context."
			}
			summary["requested_competency_count"]++
			summary[status]++
			domains[domain] = true
		}
		active, _ := ev["active_now"].(bool)
		row := map[string]any{"competency_id": id, "competency_name": term.Name, "kind": term.Kind, "primary_domain": domain, "primary_group_id": catalog.EffectiveGroupID(term), "origin": registry.SkillOrigin(id), "status": status, "explanation": explanation, "first_used": ev["first_used"], "last_used": ev["last_used"], "active_now": active, "required_job_count": len(required[id]), "preferred_job_count": len(preferred[id]), "distinct_job_count": len(union), "required_job_ids": keys(required[id]), "preferred_job_ids": keys(preferred[id])}
		for _, key := range []string{"evidence_count", "professional_context_count", "independent_project_count", "certification_count", "proof_count"} {
			row[key] = count(key)
		}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b map[string]any) int {
		for _, key := range []string{"required_job_count", "preferred_job_count"} {
			if a[key] != b[key] {
				return b[key].(int) - a[key].(int)
			}
		}
		x, y := textcompat.Fold(a["competency_name"].(string)), textcompat.Fold(b["competency_name"].(string))
		if x != y {
			return strings.Compare(x, y)
		}
		return strings.Compare(a["competency_id"].(string), b["competency_id"].(string))
	})
	result := map[string]any{"schema_version": 1, "read_only": true, "revision": revision(map[string]any{"schema_version": 1, "candidate_revision": graph.Revision, "demand_revision": demandRevision, "status_rule": "professional_context_supported_v1"}), "candidate_revision": graph.Revision, "demand_revision": demandRevision, "vault_revision": graph.Vault, "registry_revision": graph.Registry, "catalog_revision": graph.Catalog, "as_of_month": graph.Month, "period": period, "source": map[string]any{"kind": "application_ledger", "normalized_job_count": normalized, "saved_job_count": saved, "distinct_job_count": len(jobRows), "duplicate_job_count": duplicates, "unregistered_job_count": normalized - registered}, "jobs": jobRows, "summary": summary, "language_contract": map[string]any{"evidence_not_found": "Evidence not found in Career Vault.", "candidate_knowledge_inference": false}, "filters": map[string]any{"domains": keys(domains), "requirements": []string{"required", "preferred"}, "coverage_statuses": []string{"supported", "thin_evidence", "evidence_not_found"}}, "rows": rows}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, invalid()
	}
	if len(raw) > MaxOutputBytes {
		return nil, &Error{Code: "DEMAND_OUTPUT_LIMIT"}
	}
	return raw, nil
}
