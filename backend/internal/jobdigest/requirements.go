package jobdigest

import (
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

const educationPattern = `\b(?:bachelor|master|doctorate|ph\.?d|degree)\b`

func decimalInt(value string) int {
	n := 0
	for _, point := range value {
		digit, ok := textcompat.DecimalDigit(point)
		if !ok {
			return -1
		}
		n = n*10 + digit
	}
	return n
}

func (a *analysis) requirements() {
	type experienceKey struct {
		minimum, maximum int
		hasMaximum       bool
		area             string
	}
	seenExperience, seenEducation := map[experienceKey]bool{}, map[string]bool{}
	for _, l := range a.sections {
		if !a.check() {
			return
		}
		if l.section == "responsibilities" && utf8.RuneCountInString(l.clean) >= 12 {
			if !a.item() {
				return
			}
			a.result.Responsibilities = append(a.result.Responsibilities, l.clean)
			a.result.Provenance.Responsibilities = append(a.result.Provenance.Responsibilities, evidence(l.number, l.text, "responsibility_section"))
		}
		if l.section != "required" {
			continue
		}
		if !a.named(l.clean, "AGE_PATTERN") {
			match, isRange := a.matchKey(l.clean, ruleAliases["EXPERIENCE_RANGE_PATTERN"], "")
			ok := isRange
			if !ok {
				match, ok = a.matchKey(l.clean, ruleAliases["EXPERIENCE_MINIMUM_PATTERN"], "")
			}
			if ok {
				item := Experience{MinimumYears: decimalInt(match.Group("minimum")), Area: strings.Trim(match.Group("area"), " ,")}
				rule := "minimum_experience"
				identity := experienceKey{minimum: item.MinimumYears, area: item.Area}
				if isRange {
					maximum := decimalInt(match.Group("maximum"))
					item.MaximumYears = &maximum
					rule = "experience_range"
					identity.maximum, identity.hasMaximum = maximum, true
				}
				if !seenExperience[identity] {
					if !a.item() {
						return
					}
					seenExperience[identity] = true
					a.result.Experience = append(a.result.Experience, item)
					a.result.Provenance.Experience = append(a.result.Provenance.Experience, evidence(l.number, l.text, rule))
				}
			}
		}
		if a.has(l.clean, educationPattern, true) && !seenEducation[l.clean] {
			if !a.item() {
				return
			}
			seenEducation[l.clean] = true
			a.result.Education = append(a.result.Education, l.clean)
			a.result.Provenance.Education = append(a.result.Provenance.Education, evidence(l.number, l.text, "education_requirement"))
		}
	}
}

func (a *analysis) structured() map[int]bool {
	result := map[int]bool{}
	add := func(values []Evidence) {
		for _, item := range values {
			result[item.Line] = true
		}
	}
	for _, field := range a.result.Fields {
		add(field.Evidence)
	}
	for _, item := range a.result.Provenance.Technologies {
		add(item.Evidence)
	}
	for _, item := range a.result.TechnologyGroups {
		add(item.Evidence)
	}
	add(a.result.Provenance.Experience)
	add(a.result.Provenance.Education)
	add(a.result.Provenance.Responsibilities)
	for _, gate := range a.result.Gates {
		add(gate.Evidence)
	}
	for _, item := range a.result.Classified {
		add(item.Evidence)
	}
	return result
}
func (a *analysis) classifyAndCover() {
	structured := a.structured()
	for _, l := range a.sections {
		if !a.check() {
			return
		}
		if (l.section != "required" && l.section != "preferred") || structured[l.number] || utf8.RuneCountInString(l.clean) < 12 {
			continue
		}
		level := l.section
		if l.section == "required" && a.has(l.clean, `\bpreferred\b`, true) {
			level = "preferred"
		}
		category, name, rule := "", "", ""
		if a.has(l.clean, educationPattern, true) {
			category, name, rule = "education", "Education", "education_preference"
		} else if a.named(l.clean, "AGE_PATTERN") {
			category, name, rule = "constraint", "Minimum age", "constraint_minimum_age"
		} else if a.named(l.clean, "TRAVEL_REQUIREMENT_PATTERN") {
			category, name, rule = "constraint", "Travel", "constraint_travel"
		} else {
			for _, capability := range capabilities {
				if _, ok := a.matchKey(l.clean, capability[2], ""); ok {
					category, name, rule = "capability", capability[0], capability[1]
					break
				}
			}
		}
		if category == "" && a.named(l.clean, "TECHNICAL_REQUIREMENT_PATTERN") {
			category, name, rule = "technical", "Technical experience", "technical_requirement_terms"
		}
		if category == "" {
			continue
		}
		if !a.item() {
			return
		}
		a.result.Classified = append(a.result.Classified, Classified{category, name, level, prefix(l.clean, 500), []Evidence{evidence(l.number, l.text, rule)}})
	}
	structured = a.structured()
	coverage := Coverage{SourceLines: len(a.lines), ReviewItems: []ReviewItem{}}
	for _, l := range a.sections {
		if !a.check() {
			return
		}
		if (l.section != "responsibilities" && l.section != "required" && l.section != "preferred") || utf8.RuneCountInString(l.clean) < 12 || (strings.Contains(l.clean, "<<") && strings.Contains(l.clean, ">>")) {
			continue
		}
		coverage.RelevantLines++
		if structured[l.number] {
			coverage.StructuredLines++
		} else {
			if !a.item() {
				return
			}
			coverage.ReviewItems = append(coverage.ReviewItems, ReviewItem{l.number, l.section, prefix(l.clean, 500), "unstructured_relevant_line"})
		}
	}
	coverage.NeedsReviewLines = len(coverage.ReviewItems)
	coverage.ContextLines = coverage.SourceLines - coverage.RelevantLines
	coverage.NoRequirementSections = !a.requirementSection
	coverage.UnrecognizedHeadings = []Evidence{}
	if coverage.NoRequirementSections {
		for _, l := range a.headingCandidates {
			if !a.item() {
				return
			}
			coverage.UnrecognizedHeadings = append(coverage.UnrecognizedHeadings, evidence(l.number, l.text, "unrecognized_heading"))
		}
	}
	a.result.Coverage = coverage
}
