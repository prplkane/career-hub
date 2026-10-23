package jobcard

import "careerhub.local/backend/internal/cataloginput"

func (v *validator) digest(data cataloginput.Value) {
	fingerprint := get(data, "content_fingerprint")
	if fingerprint.Kind != "null" && !hexDigest(fingerprint) {
		v.add("content_fingerprint", "expected lowercase SHA-256")
	}
	info := get(data, "digest")
	if info.Kind == "null" || !v.mapping(info, "digest") {
		return
	}
	if !equalInteger(get(info, "schema_version"), 1) {
		v.add("digest.schema_version", "expected 1")
	}
	analyzer := get(info, "analyzer_version")
	if analyzer.Kind != "null" && !atLeast(analyzer, 1) {
		v.add("digest.analyzer_version", "expected positive integer")
	}
	fields := get(info, "fields")
	if v.mapping(fields, "digest.fields") {
		for _, key := range []string{"company", "title", "location", "remote_policy", "seniority"} {
			field, path := get(fields, key), "digest.fields."+key
			if !v.mapping(field, path) {
				continue
			}
			if !member(get(field, "state"), "provided", "explicit", "derived", "needs_review", "not_stated") {
				v.add(path+".state", "unexpected state")
			}
			v.strings(field, path, false, "value")
			v.list(get(field, "evidence"), path+".evidence", false)
		}
	}
	v.classified(get(info, "classified_requirements"))
	v.instructions(get(info, "application_instructions"), fingerprint)
	v.contacts(get(info, "contact_candidates"), fingerprint)
	v.occurrences(get(info, "skill_occurrences"))
	v.coverage(get(info, "analysis_coverage"))
	v.list(get(info, "unresolved_fields"), "digest.unresolved_fields", false)
	v.mapping(get(info, "eligibility_gates"), "digest.eligibility_gates")
}

func (v *validator) classified(value cataloginput.Value) {
	const path = "digest.classified_requirements"
	if value.Kind == "null" || !v.list(value, path, false) {
		return
	}
	for i, item := range value.Items {
		current := itemPath(path, i)
		if !v.mapping(item, current) {
			continue
		}
		if !member(get(item, "category"), "technical", "capability", "constraint", "education") {
			v.add(current+".category", "unexpected category")
		}
		if !member(get(item, "requirement"), "required", "preferred") {
			v.add(current+".requirement", "unexpected level")
		}
		v.strings(item, current, true, "name", "text")
		v.evidence(get(item, "evidence"), current+".evidence")
	}
}

func (v *validator) sourceEnvelope(value, fingerprint cataloginput.Value, path string, contact bool) {
	for _, key := range []string{"schema_version", "extractor_version"} {
		if !equalInteger(get(value, key), 1) {
			v.add(path+"."+key, "expected 1")
		}
	}
	source := get(value, "source_fingerprint")
	if !hexDigest(source) {
		v.add(path+".source_fingerprint", "expected lowercase SHA-256")
	} else if fingerprint.Kind == "string" && source.Text != fingerprint.Text {
		v.add(path+".source_fingerprint", "must match content_fingerprint")
	}
	if contact && !hexDigest(get(value, "content_sha256")) {
		v.add(path+".content_sha256", "expected lowercase SHA-256")
	}
	if !member(get(value, "status"), "analyzed") {
		v.add(path+".status", "expected analyzed")
	}
}

func (v *validator) sourceCounts(value cataloginput.Value, path, count, items string, n int) {
	if !equalInteger(get(value, count), n) {
		v.add(path+"."+count, "must match "+items)
	}
	for _, key := range []string{"source_chars_scanned", "source_lines_scanned"} {
		if !atLeast(get(value, key), 0) {
			v.add(path+"."+key, "expected non-negative integer")
		}
	}
	if !boolean(get(value, "truncated")) {
		v.add(path+".truncated", "expected boolean")
	}
}

func (v *validator) sourceLines(value cataloginput.Value, path string) {
	v.strings(value, path, true, "source_text", "rule")
	start := get(value, "line_start")
	if !atLeast(start, 1) {
		v.add(path+".line_start", "expected positive integer")
	}
	if !compareIntegers(get(value, "line_end"), start, false) {
		v.add(path+".line_end", "expected integer at or after line_start")
	}
	if !equalInteger(get(value, "extractor_version"), 1) {
		v.add(path+".extractor_version", "expected 1")
	}
	if !boolean(get(value, "evidence_truncated")) {
		v.add(path+".evidence_truncated", "expected boolean")
	}
}

func (v *validator) instructions(value, fingerprint cataloginput.Value) {
	const path = "digest.application_instructions"
	if value.Kind == "null" || !v.mapping(value, path) {
		return
	}
	v.sourceEnvelope(value, fingerprint, path, false)
	items := get(value, "items")
	if !v.list(items, path+".items", false) {
		items = cataloginput.Value{Kind: "sequence"}
	} else if len(items.Items) > 24 {
		v.add(path+".items", "exceeds bounded maximum")
	}
	v.sourceCounts(value, path, "item_count", "items", len(items.Items))
	for i, item := range items.Items {
		current := itemPath(path+".items", i)
		if !v.mapping(item, current) {
			continue
		}
		if !hexDigest(get(item, "fingerprint")) {
			v.add(current+".fingerprint", "expected lowercase SHA-256")
		}
		if !member(get(item, "category"), "email_destination", "exact_phrase", "required_artifact", "cover_letter_format", "external_step", "deadline") {
			v.add(current+".category", "unexpected category")
		}
		if !member(get(item, "confidence"), "explicit", "review") {
			v.add(current+".confidence", "unexpected confidence")
		}
		v.sourceLines(item, current)
	}
}

func (v *validator) occurrences(value cataloginput.Value) {
	const path = "digest.skill_occurrences"
	if value.Kind == "null" || !v.list(value, path, false) {
		return
	}
	for i, item := range value.Items {
		current := itemPath(path, i)
		if !v.mapping(item, current) {
			continue
		}
		for _, key := range []string{"skill_id", "primary_group_id"} {
			if !stableID(get(item, key)) {
				v.add(current+"."+key, "expected stable id")
			}
		}
		v.strings(item, current, true, "canonical_name", "source_text")
		if !member(get(item, "origin"), "core", "custom") {
			v.add(current+".origin", "unexpected origin")
		}
		if !member(get(item, "requirement"), "required", "preferred", "mentioned") {
			v.add(current+".requirement", "unexpected level")
		}
		if !boolean(get(item, "in_alternative_group")) {
			v.add(current+".in_alternative_group", "expected boolean")
		}
		if !atLeast(get(item, "line"), 1) {
			v.add(current+".line", "expected positive integer")
		}
		start := get(item, "start")
		if !atLeast(start, 0) {
			v.add(current+".start", "expected non-negative integer")
		}
		if !compareIntegers(get(item, "end"), start, true) {
			v.add(current+".end", "expected integer after start")
		}
	}
}

func (v *validator) coverage(value cataloginput.Value) {
	const path = "digest.analysis_coverage"
	if value.Kind == "null" || !v.mapping(value, path) {
		return
	}
	valid := true
	for _, key := range []string{"source_lines", "relevant_lines", "structured_lines", "needs_review_lines", "context_lines"} {
		if !atLeast(get(value, key), 0) {
			valid = false
			v.add(path+"."+key, "expected non-negative integer")
		}
	}
	if valid {
		source, _ := integer(get(value, "source_lines"))
		relevant, _ := integer(get(value, "relevant_lines"))
		structured, _ := integer(get(value, "structured_lines"))
		review, _ := integer(get(value, "needs_review_lines"))
		context, _ := integer(get(value, "context_lines"))
		if relevant.Cmp(structured.Add(structured, review)) != 0 {
			v.add(path, "relevant_lines must equal structured_lines + needs_review_lines")
		}
		if source.Cmp(context.Add(context, relevant)) != 0 {
			v.add(path, "source_lines must equal relevant_lines + context_lines")
		}
	}
	if flag, present := lookup(value, "no_requirement_sections"); present && !boolean(flag) {
		v.add(path+".no_requirement_sections", "expected boolean")
	}
	if headings, present := lookup(value, "unrecognized_headings"); present && v.list(headings, path+".unrecognized_headings", false) {
		for i, item := range headings.Items {
			current := itemPath(path+".unrecognized_headings", i)
			if !v.mapping(item, current) {
				continue
			}
			if !atLeast(get(item, "line"), 1) {
				v.add(current+".line", "expected positive integer")
			}
			v.strings(item, current, true, "text", "rule")
		}
	}
	items := get(value, "review_items")
	if !v.list(items, path+".review_items", false) {
		return
	}
	if valid && !equalInteger(get(value, "needs_review_lines"), len(items.Items)) {
		v.add(path, "needs_review_lines must equal review_items count")
	}
	for i, item := range items.Items {
		current := itemPath(path+".review_items", i)
		if !v.mapping(item, current) {
			continue
		}
		if !atLeast(get(item, "line"), 1) {
			v.add(current+".line", "expected positive integer")
		}
		if !member(get(item, "section"), "responsibilities", "required", "preferred") {
			v.add(current+".section", "unexpected section")
		}
		v.strings(item, current, true, "text", "rule")
	}
}
