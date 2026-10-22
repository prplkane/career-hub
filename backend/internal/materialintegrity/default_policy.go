package materialintegrity

import (
	"context"
	"encoding/json"
	"slices"
	"sort"

	"careerhub.local/backend/internal/applicationanalysis"
	"careerhub.local/backend/internal/materials"
	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/skills/matching"
	"careerhub.local/backend/internal/skills/resolver"
	"careerhub.local/backend/internal/textcompat"
)

const DefaultPolicyVersion = "material-fact-defaults-v1"

// PolicyInfo is safe to display/log: neither the private terms nor the JD are
// included. Ambiguous JD vocabulary remains a review aid, never credited evidence.
type PolicyInfo struct {
	Version          string `json:"version"`
	RegistryRevision string `json:"registry_revision"`
	NamedTerms       int    `json:"named_terms"`
	ActiveSkills     int    `json:"active_skills"`
	AmbiguousTerms   int    `json:"ambiguous_terms"`
	AmbiguousDemand  int    `json:"ambiguous_demand"`
}

// DefaultFactPolicy consumes a validated, merged registry and owner-built source
// snapshot. It never adds skills or interprets job requirements as candidate proof.
// Explicit terms can add checks, but cannot supply a fabricated JD demand ID.
func DefaultFactPolicy(ctx context.Context, registry *catalog.Snapshot, source materials.Sources, rawJD string, extra []NamedTerm) (*FactPolicy, PolicyInfo, *Error) {
	info := PolicyInfo{Version: DefaultPolicyVersion}
	if ctx == nil || ctx.Err() != nil {
		return nil, info, failure("MATERIAL_INTEGRITY_CANCELED")
	}
	if registry == nil {
		return nil, info, failure("MATERIAL_FACT_REGISTRY_REQUIRED")
	}
	if !factText(rawJD) || textcompat.Strip(rawJD) == "" || !identifier(source.JobID) ||
		!hash(source.ProfileRevision, "sha256:") || !hash(source.Catalog.VaultRevision, "sha256:") {
		return nil, info, failure("MATERIAL_FACT_SOURCE_INVALID")
	}
	fp, err := applicationanalysis.SourceFingerprint(rawJD)
	if err != nil || fp != source.JobFingerprint {
		return nil, info, failure("MATERIAL_FACT_JD_STALE")
	}
	info.RegistryRevision = registry.CatalogRevision()
	groups := map[string]bool{}
	for _, group := range registry.Groups() {
		groups[group.ID] = !group.Archived
	}
	literals := []resolver.Term{}
	for _, skill := range registry.Skills() {
		if !skill.Archived && groups[catalog.EffectiveGroupID(skill)] {
			literals = append(literals, resolver.Term{ID: skill.ID, Name: skill.Name, Aliases: skill.Aliases})
		}
	}
	info.ActiveSkills = len(literals)
	if info.ActiveSkills == 0 {
		return nil, info, failure("MATERIAL_FACT_REGISTRY_EMPTY")
	}
	matcher, err := matching.New(ctx, literals)
	if err != nil {
		return nil, info, failure("MATERIAL_FACT_POLICY_UNSUPPORTED")
	}
	found, err := matcher.FindBlocks(ctx, source.JobID, []matching.Block{{ID: "jd", Text: rawJD}})
	if err != nil {
		return nil, info, failure("MATERIAL_FACT_JD_UNSUPPORTED")
	}
	demands := map[string]string{}
	for _, m := range found[0].Mentions {
		demands[textcompat.Normalize(m.SourceText)] = m.TechnologyID
	}
	ambiguousDemand := map[string]bool{}
	for _, issue := range found[0].Issues {
		key := textcompat.Normalize(issue.SourceText)
		ambiguousDemand[key] = true
		delete(demands, key)
	}
	info.AmbiguousDemand = len(ambiguousDemand)
	names := map[string]NamedTerm{}
	ambiguous := map[string]bool{}
	bytes := 0
	add := func(term NamedTerm, optional bool) *Error {
		if ctx.Err() != nil {
			return failure("MATERIAL_INTEGRITY_CANCELED")
		}
		if !factText(term.Text) || len(term.Text) > matching.MaxValueBytes {
			return failure("MATERIAL_FACT_POLICY_INVALID")
		}
		bytes += len(term.Text) + len(term.Kind) + len(term.DemandID)
		if bytes > matching.MaxCatalogBytes {
			return failure("MATERIAL_FACT_POLICY_LIMIT")
		}
		term.Text = textcompat.Normalize(term.Text)
		if term.Text == "" && optional {
			return nil
		}
		if old, ok := names[term.Text]; ok && old != term {
			// A name can also be a technology/title. Still check the exact literal,
			// but do not guess its role or credit ambiguous vocabulary coverage.
			if old.Kind == term.Kind && (old.DemandID == "" || term.DemandID == "") {
				if old.DemandID != "" {
					term.DemandID = old.DemandID
				}
			} else {
				term.Kind, term.DemandID = "other", ""
				ambiguous[term.Text] = true
			}
		}
		names[term.Text] = term
		if len(names) > matching.MaxTerms {
			return failure("MATERIAL_FACT_POLICY_LIMIT")
		}
		return nil
	}
	for _, term := range literals {
		for i := -1; i < len(term.Aliases); i++ {
			text := term.Name
			if i >= 0 {
				text = term.Aliases[i]
			}
			if e := add(NamedTerm{"technology", text, demands[textcompat.Normalize(text)]}, false); e != nil {
				return nil, info, e
			}
		}
	}
	for _, term := range []NamedTerm{{"employer", source.Company, ""}, {"title", source.RoleTitle, ""}} {
		if e := add(term, false); e != nil {
			return nil, info, e
		}
	}
	if len(source.Catalog.Experiences) > matching.MaxTerms {
		return nil, info, failure("MATERIAL_FACT_POLICY_LIMIT")
	}
	for _, experience := range source.Catalog.Experiences {
		for _, field := range []struct {
			kind  string
			value any
		}{{"employer", experience.Company}, {"title", experience.Role}} {
			text, ok := field.value.(string)
			if !ok && field.value != nil {
				return nil, info, failure("MATERIAL_FACT_SOURCE_INVALID")
			}
			if e := add(NamedTerm{field.kind, text, ""}, true); e != nil {
				return nil, info, e
			}
		}
	}
	if len(extra) > matching.MaxTerms {
		return nil, info, failure("MATERIAL_FACT_POLICY_LIMIT")
	}
	for _, term := range extra {
		if term.DemandID != "" {
			return nil, info, failure("MATERIAL_FACT_POLICY_INVALID")
		}
		// Validate explicit configuration independently, even when a duplicate
		// source term would otherwise hide an invalid kind or blank value.
		if !slices.Contains([]string{"employer", "title", "technology", "date", "other"}, term.Kind) || textcompat.Strip(term.Text) == "" {
			return nil, info, failure("MATERIAL_FACT_POLICY_INVALID")
		}
		if e := add(term, false); e != nil {
			return nil, info, e
		}
	}
	items := make([]NamedTerm, 0, len(names))
	for _, term := range names {
		items = append(items, term)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Text < items[j].Text })
	p, e := NewFactPolicy(ctx, items)
	if e != nil {
		return nil, info, e
	}
	info.NamedTerms, info.AmbiguousTerms = len(items), len(ambiguous)
	identity, _ := json.Marshal(struct {
		Policy                                                             PolicyInfo
		Detector, JobID, JobFingerprint, ProfileRevision, EvidenceRevision string
	}{info, p.digest, source.JobID, fp, source.ProfileRevision, source.Catalog.VaultRevision})
	p.digest = digest(identity)
	return p, info, nil
}
