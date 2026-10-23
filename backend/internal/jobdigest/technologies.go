package jobdigest

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/skills/matching"
	"careerhub.local/backend/internal/skills/resolver"
	"careerhub.local/backend/internal/textcompat"
)

// maxUnrecognizedHeadings bounds the quoted heading evidence in a digest.
const maxUnrecognizedHeadings = 24

func (a *analysis) sectionLines() {
	current := ""
	for _, l := range a.lines {
		if !a.check() {
			return
		}
		heading := a.heading(l.text)
		matched := ""
		for _, section := range []string{"responsibilities", "required", "preferred", "technology_stack"} {
			if in("SECTION_"+section, heading) {
				matched = section
				break
			}
		}
		if matched != "" {
			current = matched
			if matched != "responsibilities" {
				a.requirementSection = true
			}
			continue
		}
		if current == "responsibilities" && ((strings.HasPrefix(heading, "as a ") && strings.HasSuffix(heading, "you will")) || (strings.HasPrefix(heading, "by the end of the internship") && strings.HasSuffix(heading, "contributed to"))) {
			continue
		}
		boundary := in("SECTION_BOUNDARIES", heading)
		for _, prefix := range sets["SECTION_BOUNDARY_PREFIXES"] {
			boundary = boundary || strings.HasPrefix(heading, prefix)
		}
		if boundary {
			current = ""
			continue
		}
		if utf8.RuneCountInString(l.clean) <= 80 && strings.HasSuffix(textcompat.Strip(analysisText(l.text)), ":") {
			// A short colon-terminated line that no rule names is most likely a
			// heading this analyzer does not know. Keep it as coverage evidence.
			current = ""
			if len(a.headingCandidates) < maxUnrecognizedHeadings {
				a.headingCandidates = append(a.headingCandidates, l)
			}
			continue
		}
		l.section = current
		a.sections = append(a.sections, l)
	}
}

type mentionRef struct {
	id         string
	start, end int
}

func ref(value matching.Mention) mentionRef {
	return mentionRef{value.TechnologyID, value.SourceRef.Start, value.SourceRef.End}
}
func uniqueInSpan(source string, mentions []matching.Mention, start, end int) []matching.Mention {
	first, last := utf8.RuneCountInString(source[:start]), utf8.RuneCountInString(source[:end])
	result := []matching.Mention{}
	seen := map[string]bool{}
	for _, m := range mentions {
		if m.SourceRef.Start >= first && m.SourceRef.End <= last && !seen[m.TechnologyID] {
			seen[m.TechnologyID] = true
			result = append(result, m)
		}
	}
	if len(result) < 2 {
		return nil
	}
	return result
}

// Map the normalized search boundary back before using it in original text.
// Python's casefold().rfind index is not an original offset after expansions.
func lastIncludingEnd(value string) int {
	var folded strings.Builder
	positions := []int{}
	for offset, point := range value {
		part := textcompat.Fold(string(point))
		folded.WriteString(part)
		for range len(part) {
			positions = append(positions, offset)
		}
	}
	index := strings.LastIndex(folded.String(), "including")
	if index < 0 {
		return 0
	}
	last := positions[index+len("including")-1]
	_, width := utf8.DecodeRuneInString(value[last:])
	return last + width
}

func (a *analysis) alternatives(value string, mentions []matching.Mention) [][]matching.Mention {
	groups := [][]matching.Mention{}
	add := func(start, end int) {
		if group := uniqueInSpan(value, mentions, start, end); len(group) > 0 {
			groups = append(groups, group)
		}
	}
	for _, match := range a.allKey(value, ruleAliases["PREFERENCE_ALTERNATIVE_PATTERN"]) {
		add(match.Start(), match.End())
	}
	if match, ok := a.matchKey(value, ruleAliases["EXPLICIT_ALTERNATIVE_PATTERN"], ""); ok {
		add(match.Start(), len(value))
	}
	for _, kind := range []string{"AND_OR_PATTERN", "SERIAL_OR_PATTERN"} {
		local := kind == "AND_OR_PATTERN"
		for _, match := range a.allKey(value, ruleAliases[kind]) {
			before := value[:match.Start()]
			start := 0
			for _, token := range []string{"(", ":", ";"} {
				start = max(start, strings.LastIndex(before, token)+1)
			}
			start = max(start, lastIncludingEnd(before))
			if local {
				start = max(start, strings.LastIndex(before, ",")+1)
			}
			end := len(value)
			tokens := []string{")", ";", "."}
			if local {
				tokens = append(tokens, ",")
			}
			for _, token := range tokens {
				if index := strings.Index(value[match.End():], token); index >= 0 {
					end = min(end, match.End()+index)
				}
			}
			add(start, end)
		}
	}
	result := [][]matching.Mention{}
	seen := map[string]bool{}
	for _, group := range groups {
		var identity strings.Builder
		for _, m := range group {
			fmt.Fprintf(&identity, "%d:%s", len(m.TechnologyID), m.TechnologyID)
		}
		if !seen[identity.String()] {
			seen[identity.String()] = true
			result = append(result, group)
		}
	}
	return result
}
func (a *analysis) preferred(value string, m matching.Mention) bool {
	points := []rune(value)
	start, end := m.SourceRef.Start, m.SourceRef.End
	suffix := string(points[end:min(len(points), end+48)])
	before := string(points[max(0, start-24):start])
	qualifier := `(?:preferred|valuable|nice[- ]to[- ]have)`
	if _, ok := a.match(suffix, `\s*(?:\([^)]*\b`+qualifier+`\b[^)]*\)|(?:is|are)?\s*(?:also\s+)?`+qualifier+`\b)`, true, "start"); ok {
		return true
	}
	return a.has(before, `\b`+qualifier+`\s*$`, true)
}

func (a *analysis) technologies() error {
	skills := a.input.Registry.Skills()
	terms := make([]resolver.Term, 0, len(skills))
	byID := map[string]catalog.Term{}
	for _, term := range skills {
		if !a.check() {
			return a.err
		}
		byID[term.ID] = term
		terms = append(terms, resolver.Term{ID: term.ID, Name: term.Name, Aliases: term.Aliases, Archived: term.Archived})
	}
	matcher, err := matching.New(a.ctx, terms)
	if err != nil {
		return err
	}
	blocks := make([]matching.Block, len(a.sections))
	for i, l := range a.sections {
		blocks[i] = matching.Block{ID: fmt.Sprintf("line_%04d", l.number), Text: l.text}
	}
	scans, err := matcher.FindBlocks(a.ctx, "job_description", blocks)
	if err != nil {
		return err
	}
	found := map[[2]string]bool{}
	for i, l := range a.sections {
		if !a.check() {
			return a.err
		}
		mentions := scans[i].Mentions
		level := l.section
		if level != "required" && level != "preferred" {
			level = "mentioned"
		}
		inline := map[mentionRef]bool{}
		groupLevel := level
		if l.section == "required" {
			for _, m := range mentions {
				inline[ref(m)] = a.preferred(l.text, m)
				if inline[ref(m)] {
					groupLevel = "preferred"
				}
			}
		}
		alternativeRefs := map[mentionRef]bool{}
		for _, group := range a.alternatives(l.text, mentions) {
			if !a.item() {
				return a.err
			}
			names := make([]string, 0, len(group))
			for _, m := range group {
				names = append(names, m.CanonicalName)
				alternativeRefs[ref(m)] = true
			}
			a.result.TechnologyGroups = append(a.result.TechnologyGroups, TechnologyGroup{groupLevel, "any_of", names, []Evidence{evidence(l.number, l.text, "technology_alternatives")}})
		}
		for _, m := range mentions {
			if !a.item() {
				return a.err
			}
			individual := level
			if inline[ref(m)] {
				individual = "preferred"
			}
			alternative := alternativeRefs[ref(m)]
			if alternative && (individual == "required" || individual == "preferred") {
				individual = "mentioned"
			}
			requirement := individual
			if alternative {
				requirement = groupLevel
			}
			term := byID[m.TechnologyID]
			a.result.Occurrences = append(a.result.Occurrences, Occurrence{m.TechnologyID, m.CanonicalName, m.SourceText, catalog.EffectiveGroupID(term), a.input.Registry.SkillOrigin(term.ID), requirement, alternative, l.number, m.SourceRef.Start, m.SourceRef.End})
			identity := [2]string{m.TechnologyID, individual}
			if !found[identity] {
				found[identity] = true
				a.result.Provenance.Technologies = append(a.result.Provenance.Technologies, Technology{m.TechnologyID, m.CanonicalName, individual, term.Category, []Evidence{evidence(l.number, l.text, "technology_taxonomy")}})
			}
		}
	}
	for _, technology := range a.result.Provenance.Technologies {
		target := &a.result.MentionedTechnologies
		if technology.Requirement == "preferred" {
			target = &a.result.PreferredSkills
		} else if technology.Requirement == "required" {
			target = &a.result.RequiredPlatforms
			if in("SKILL_CATEGORIES", technology.Category) {
				target = &a.result.RequiredSkills
			}
		}
		if !slicesOf((*target)...)(technology.Name) {
			*target = append(*target, technology.Name)
		}
	}
	for _, values := range [][]string{a.result.RequiredSkills, a.result.RequiredPlatforms, a.result.PreferredSkills, a.result.MentionedTechnologies} {
		sort.SliceStable(values, func(i, j int) bool { return textcompat.Fold(values[i]) < textcompat.Fold(values[j]) })
	}
	return a.err
}
