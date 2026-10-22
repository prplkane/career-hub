package jobcard

import (
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/textcompat"
)

func technologyGroups(data cataloginput.Value) (cataloginput.Value, bool) {
	if value, present := lookup(data, "technology_groups"); present {
		return value, true
	}
	return lookup(get(data, "digest"), "technology_groups")
}
func (v *validator) evidence(value cataloginput.Value, path string) {
	if !v.list(value, path, true) {
		return
	}
	for i, item := range value.Items {
		current := itemPath(path, i)
		if !v.mapping(item, current) {
			continue
		}
		if !atLeast(get(item, "line"), 1) {
			v.add(current+".line", "expected positive integer")
		}
		v.strings(item, current, true, "text", "rule")
	}
}
func (v *validator) experience(data cataloginput.Value) {
	value, present := lookup(data, "experience_requirements")
	if !present || !v.list(value, "experience_requirements", false) {
		return
	}
	for i, item := range value.Items {
		path := itemPath("experience_requirements", i)
		if !v.mapping(item, path) {
			continue
		}
		missing := ""
		if !has(item, "minimum_years") {
			missing = "minimum_years"
		}
		if !has(item, "area") {
			if missing != "" {
				missing += ", "
			}
			missing += "area"
		}
		if missing != "" {
			v.add(path, "missing required fields: "+missing)
			continue
		}
		minimum := get(item, "minimum_years")
		validMinimum := nonnegative(minimum)
		if !validMinimum {
			v.add(path+".minimum_years", "expected non-negative number")
		}
		maximum := get(item, "maximum_years")
		if maximum.Kind != "null" {
			if !nonnegative(maximum) {
				v.add(path+".maximum_years", "expected non-negative number")
			} else if validMinimum && numericBelow(maximum, minimum) {
				v.add(path+".maximum_years", "must be at least minimum_years")
			}
		}
		v.strings(item, path, true, "area")
	}
}
func (v *validator) groups(data cataloginput.Value) {
	value, present := technologyGroups(data)
	if !present || !v.list(value, "technology_groups", false) {
		return
	}
	for i, item := range value.Items {
		path := itemPath("technology_groups", i)
		if !v.mapping(item, path) {
			continue
		}
		if !member(get(item, "requirement"), "required", "preferred", "mentioned") {
			v.add(path+".requirement", "unexpected requirement level")
		}
		if !member(get(item, "operator"), "any_of") {
			v.add(path+".operator", "expected any_of")
		}
		technologies := get(item, "technologies")
		if technologies.Kind != "sequence" || len(technologies.Items) < 2 {
			v.add(path+".technologies", "expected at least two alternatives")
		} else {
			valid, unique := true, true
			seen := map[string]bool{}
			for _, value := range technologies.Items {
				if !v.active() {
					return
				}
				if !nonempty(value) {
					valid = false
					break
				}
				key := textcompat.Fold(value.Text)
				if seen[key] {
					unique = false
				}
				seen[key] = true
			}
			if !valid {
				v.add(path+".technologies", "expected non-empty strings")
			} else if !unique {
				v.add(path+".technologies", "alternatives must be unique")
			}
		}
		v.evidence(get(item, "evidence"), path+".evidence")
	}
}
func (v *validator) compensation(data cataloginput.Value) {
	value := get(data, "compensation")
	if value.Kind == "null" || !v.mapping(value, "compensation") {
		return
	}
	status := get(value, "status")
	if !member(status, "explicit", "not_stated") {
		v.add("compensation.status", "unexpected status")
	}
	ranges := get(value, "ranges")
	if !v.list(ranges, "compensation.ranges", false) {
		return
	}
	if member(status, "explicit") && len(ranges.Items) == 0 {
		v.add("compensation.ranges", "explicit compensation requires a range")
	}
	if member(status, "not_stated") && len(ranges.Items) > 0 {
		v.add("compensation.ranges", "not_stated compensation must be empty")
	}
	for i, item := range ranges.Items {
		path := itemPath("compensation.ranges", i)
		if !v.mapping(item, path) {
			continue
		}
		for _, key := range []string{"minimum", "maximum"} {
			if !nonnegative(get(item, key)) {
				v.add(path+"."+key, "expected non-negative number")
			}
		}
		if numericBelow(get(item, "maximum"), get(item, "minimum")) {
			v.add(path+".maximum", "must be at least minimum")
		}
		v.strings(item, path, false, "currency", "location")
		if !member(get(item, "period"), "year", "month", "week", "hour", "unspecified") {
			v.add(path+".period", "unexpected period")
		}
		v.strings(item, path, true, "display_text")
		v.evidence(get(item, "evidence"), path+".evidence")
	}
}
func (v *validator) visa(data cataloginput.Value) {
	value, present := lookup(data, "visa_signals")
	if !present || !v.mapping(value, "visa_signals") {
		return
	}
	for _, key := range visaFields {
		item, present := lookup(value, key)
		if !present {
			v.add("visa_signals", "missing required field: "+key)
		} else if item.Kind != "null" && !boolean(item) {
			v.add("visa_signals."+key, "expected boolean or null")
		}
	}
}
