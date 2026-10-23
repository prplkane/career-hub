package intakereview

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
	"golang.org/x/text/unicode/norm"
)

func previewNormal(value string) string {
	value = textcompat.Fold(norm.NFKC.String(value))
	return strings.Join(strings.FieldsFunc(value, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }), " ")
}
func previewOverlap(a, b string) bool {
	a, b = previewNormal(a), previewNormal(b)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	if utf8.RuneCountInString(a) > utf8.RuneCountInString(b) {
		a, b = b, a
	}
	if utf8.RuneCountInString(a) >= 8 && strings.Contains(b, a) {
		return true
	}
	left, right := previewUniqueTokens(a), previewUniqueTokens(b)
	shared := 0
	for token := range left {
		if right[token] {
			shared++
		}
	}
	return shared >= 3 && float64(shared)/float64(min(len(left), len(right))) >= .6
}
func previewUniqueTokens(value string) map[string]bool {
	out := map[string]bool{}
	for _, token := range strings.Fields(previewNormal(value)) {
		out[token] = true
	}
	return out
}
func previewShared(a, b []string) []string {
	keys, seen := map[string]bool{}, map[string]bool{}
	out := []string{}
	for _, value := range a {
		key := previewNormal(value)
		if key != "" {
			keys[key] = true
		}
	}
	for _, value := range b {
		if keys[previewNormal(value)] && !seen[value] {
			out = append(out, value)
			seen[value] = true
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return textcompat.Fold(out[i]) < textcompat.Fold(out[j]) })
	return out
}
func previewUnique(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := previewNormal(value)
		if key != "" && !seen[key] {
			out = append(out, value)
			seen[key] = true
		}
	}
	return out
}
func previewYears(a, b string) []string {
	// Python regex \b is Unicode-aware; Go regexp's \b is ASCII-only.
	years := func(value string) []string {
		r := []rune(value)
		out := []string{}
		word := func(c rune) bool { return c == '_' || unicode.IsLetter(c) || unicode.IsNumber(c) }
		for i := 0; i+4 <= len(r); i++ {
			if i > 0 && word(r[i-1]) || i+4 < len(r) && word(r[i+4]) {
				continue
			}
			if (r[i] == '1' && r[i+1] == '9' || r[i] == '2' && r[i+1] == '0') && unicode.IsDigit(r[i+2]) && unicode.IsDigit(r[i+3]) {
				out = append(out, string(r[i:i+4]))
			}
		}
		return out
	}
	return previewShared(years(a), years(b))
}

// Scalar rendering follows str(value or "") for supported Vault values.
func previewText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		if f, err := strconv.ParseFloat(string(v), 64); err == nil && f == 0 {
			return ""
		}
		return string(v)
	case bool:
		if v {
			return "True"
		}
	}
	return ""
}
func previewList(value any) []string {
	out := []string{}
	values, _ := value.([]any)
	for _, value := range values {
		switch v := value.(type) {
		case nil:
			out = append(out, "None")
		case bool:
			if v {
				out = append(out, "True")
			} else {
				out = append(out, "False")
			}
		case json.Number:
			out = append(out, string(v))
		case string:
			out = append(out, v)
		}
	}
	return out
}
func previewClean(fields map[string]any) map[string]any {
	for key, value := range fields {
		if value == nil {
			delete(fields, key)
			continue
		}
		switch v := value.(type) {
		case string:
			if v == "" {
				delete(fields, key)
			}
		case []string:
			if len(v) == 0 {
				delete(fields, key)
			}
		}
	}
	return fields
}
func previewJoin(values ...string) string {
	nonempty := []string{}
	for _, v := range values {
		if v != "" {
			nonempty = append(nonempty, v)
		}
	}
	return strings.Join(nonempty, " / ")
}
func previewFirst(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
func previewLabel(e map[string]any) string {
	t := func(k string) string { return previewText(e[k]) }
	switch t("entity_type") {
	case "profile":
		return previewFirst(t("name"), "Profile & contact")
	case "experience":
		return previewFirst(previewJoin(t("organization"), t("title")), "Untitled experience")
	case "education":
		return previewFirst(t("institution"), t("degree"), "Untitled education")
	case "certification":
		return previewFirst(t("credential"), t("name"), "Untitled certification")
	case "project":
		return previewFirst(t("title"), "Untitled project")
	case "skill":
		return previewFirst(t("name"), "Untitled skill group")
	case "summary":
		return previewFirst(t("name"), "Professional Summary")
	}
	return previewFirst(t("name"), t("title"), t("id"))
}
func previewSourceFields(e map[string]any, proof, skills []string) map[string]any {
	t := func(k string) string { return previewText(e[k]) }
	l := func(k string) []string { return previewList(e[k]) }
	out := map[string]any{}
	first := func(values []string) string {
		if len(values) > 0 {
			return values[0]
		}
		return ""
	}
	switch t("entity_type") {
	case "profile":
		out = map[string]any{"name": t("name"), "location": t("location"), "email": first(l("emails")), "phone": first(l("phones"))}
		for _, link := range l("links") {
			key := "website"
			lower := textcompat.Fold(link)
			if strings.Contains(lower, "linkedin.com") {
				key = "linkedin"
			} else if strings.Contains(lower, "github.com") {
				key = "github"
			}
			if _, present := out[key]; !present {
				out[key] = link
			}
		}
	case "experience":
		out = map[string]any{"organization": t("organization"), "role": t("title"), "dates": t("date_text"), "location": t("location"), "technologies": skills, "proof_points": proof}
	case "education":
		out = map[string]any{"institution": t("institution"), "degree": t("degree"), "major": t("major"), "graduation": t("date_text"), "location": t("location"), "gpa": t("gpa"), "honors": l("honors")}
	case "certification":
		out = map[string]any{"name": t("name"), "credential": t("credential"), "completed": t("date_text"), "technologies": skills, "proof_points": proof}
	case "project":
		items := l("items")
		out = map[string]any{"name": t("title"), "summary": items[:min(1, len(items))], "dates": t("date_text"), "technologies": skills, "proof_points": proof}
	case "skill":
		out = map[string]any{"skill_family": t("name"), "keywords": previewUnique(append(l("items"), l("technologies")...))}
	case "summary":
		out = map[string]any{"content": t("content")}
	default:
		out["name"] = t("name")
	}
	return previewClean(out)
}

func previewRecordSupported(record map[string]any, scalar, list []string) bool {
	primitive := func(v any) bool {
		switch v.(type) {
		case nil, string, json.Number, bool:
			return true
		}
		return false
	}
	for _, key := range scalar {
		if !primitive(record[key]) {
			return false
		}
	}
	for _, key := range list {
		value, present := record[key]
		if !present {
			continue
		}
		items, ok := value.([]any)
		if !ok {
			return false
		}
		for _, item := range items {
			if !primitive(item) {
				return false
			}
		}
	}
	return true
}

func previewCandidates(ctx context.Context, e map[string]any, vault map[string]any, comparisons *int) ([]ApprovalCandidate, *Error) {
	t := func(k string) string { return previewText(e[k]) }
	typ := t("entity_type")
	paths := ApprovalPreviewPaths()
	out := []ApprovalCandidate{}
	if typ == "profile" {
		profile := vault[paths[0]].(map[string]any)
		contact, _ := profile["contact"].(map[string]any)
		if !previewRecordSupported(profile, []string{"name", "current_location"}, nil) {
			return nil, fail("APPROVAL_PREVIEW_VAULT_UNSUPPORTED")
		}
		keys := []string{}
		for key := range contact {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		contacts := []string{}
		for _, key := range keys {
			if !previewRecordSupported(contact, []string{key}, nil) {
				return nil, fail("APPROVAL_PREVIEW_VAULT_UNSUPPORTED")
			}
			if value := previewText(contact[key]); value != "" {
				contacts = append(contacts, value)
			}
		}
		reasons := []ApprovalReason{}
		if previewNormal(t("name")) == previewNormal(previewText(profile["name"])) {
			reasons = append(reasons, ApprovalReason{"name_match", []string{t("name")}})
		}
		shared := previewShared(append(append(previewList(e["emails"]), previewList(e["phones"])...), previewList(e["links"])...), contacts)
		if len(shared) > 0 {
			reasons = append(reasons, ApprovalReason{"shared_contact", shared})
		}
		if len(reasons) == 0 {
			reasons = append(reasons, ApprovalReason{"singleton_profile_destination", []string{paths[0]}})
		}
		fields := map[string]any{"name": previewText(profile["name"]), "location": previewText(profile["current_location"])}
		for _, key := range []string{"email", "phone", "linkedin", "github", "website"} {
			fields[key] = previewText(contact[key])
		}
		return []ApprovalCandidate{{"profile", "profile", previewFirst(previewText(profile["name"]), "Career Vault profile"), paths[0], previewClean(fields), reasons}}, nil
	}
	var records any
	var path string
	switch typ {
	case "experience":
		path = paths[2]
		records = vault[path]
	case "project":
		path = paths[3]
		records = vault[path]
	case "skill":
		path = paths[4]
		records = vault[path]
	case "education":
		path = paths[1]
		records = vault[path].(map[string]any)["schools"]
	case "certification":
		path = paths[1]
		records = vault[path].(map[string]any)["certifications"]
	default:
		return out, nil
	}
	if records == nil {
		records = []any{}
	}
	items, ok := records.([]any)
	if !ok {
		return nil, fail("APPROVAL_PREVIEW_VAULT_UNSUPPORTED")
	}
	type rankedCandidate struct {
		candidate ApprovalCandidate
		score     int
	}
	ranked := []rankedCandidate{}
	responseBytes := 0
	for _, value := range items {
		*comparisons++
		if *comparisons > 1000000 {
			return nil, fail("APPROVAL_PREVIEW_INPUT_LIMIT")
		}
		if ctx.Err() != nil {
			return nil, fail("APPROVAL_PREVIEW_CANCELED")
		}
		r, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if archived, _ := r["archived"].(bool); archived {
			continue
		}
		if !previewRecordSupported(r, []string{"id", "company", "role", "dates", "location", "category", "institution", "degree", "major", "graduation_date", "gpa", "name", "credential", "completed", "summary", "skill_family"}, []string{"technologies", "proof_points", "honors", "keywords"}) {
			return nil, fail("APPROVAL_PREVIEW_VAULT_UNSUPPORTED")
		}
		v := func(k string) string { return previewText(r[k]) }
		l := func(k string) []string { return previewList(r[k]) }
		reasons := []ApprovalReason{}
		fields := map[string]any{}
		label := ""
		score := 0
		add := func(code string, values []string) {
			if len(values) > 0 {
				reasons = append(reasons, ApprovalReason{code, values})
			}
		}
		switch typ {
		case "experience":
			if !previewOverlap(t("organization"), v("company")) {
				continue
			}
			add("organization_match", []string{previewFirst(v("company"), t("organization"))})
			add("shared_role_terms", previewShared(strings.Fields(previewNormal(t("title"))), strings.Fields(previewNormal(v("role")))))
			add("shared_dates", previewYears(t("date_text"), v("dates")))
			add("shared_technologies", previewShared(previewList(e["technologies"]), l("technologies")))
			label = previewJoin(v("company"), v("role"))
			fields = map[string]any{"organization": v("company"), "role": v("role"), "dates": v("dates"), "location": v("location"), "category": v("category"), "technologies": l("technologies"), "proof_points": l("proof_points")}
		case "education":
			if !previewOverlap(t("institution"), v("institution")) {
				continue
			}
			add("institution_match", []string{previewFirst(v("institution"), t("institution"))})
			for _, key := range []string{"degree", "major"} {
				if t(key) != "" && previewNormal(t(key)) == previewNormal(v(key)) {
					add(key+"_match", []string{t(key)})
				}
			}
			graduation := previewFirst(v("graduation_date"), v("dates"))
			add("shared_dates", previewYears(t("date_text"), graduation))
			label = previewFirst(v("institution"), "Education record")
			fields = map[string]any{"institution": v("institution"), "degree": v("degree"), "major": v("major"), "graduation": graduation, "location": v("location"), "gpa": v("gpa"), "honors": l("honors")}
		case "certification":
			nameMatch, credentialMatch := previewOverlap(t("name"), v("name")), previewOverlap(t("credential"), v("credential"))
			if !nameMatch && !credentialMatch {
				continue
			}
			if nameMatch {
				add("certification_name_match", []string{t("name")})
			}
			if credentialMatch {
				add("credential_match", []string{t("credential")})
			}
			add("shared_dates", previewYears(t("date_text"), v("completed")))
			label = previewJoin(v("name"), v("credential"))
			fields = map[string]any{"name": v("name"), "credential": v("credential"), "completed": v("completed"), "proof_points": l("proof_points")}
		case "project":
			if !previewOverlap(t("title"), v("name")) {
				continue
			}
			add("project_name_match", []string{previewFirst(v("name"), t("title"))})
			add("shared_technologies", previewShared(previewList(e["technologies"]), l("technologies")))
			label = previewFirst(v("name"), "Project")
			fields = map[string]any{"name": v("name"), "category": v("category"), "summary": v("summary"), "dates": v("dates"), "technologies": l("technologies"), "proof_points": l("proof_points")}
		case "skill":
			family := previewOverlap(t("name"), v("skill_family"))
			shared := previewShared(append(previewList(e["items"]), previewList(e["technologies"])...), l("keywords"))
			if !family && len(shared) < 2 {
				continue
			}
			if family {
				add("skill_family_match", []string{v("skill_family")})
				score = 3
			}
			add("shared_keywords", shared)
			score += len(shared)
			label = previewFirst(v("skill_family"), "Skill family")
			fields = map[string]any{"skill_family": v("skill_family"), "keywords": l("keywords")}
		}
		if v("id") == "" || label == "" {
			return nil, fail("APPROVAL_PREVIEW_VAULT_INVALID")
		}
		candidate := ApprovalCandidate{v("id"), typ, label, path, previewClean(fields), reasons}
		encoded, err := json.Marshal(candidate)
		responseBytes += len(encoded)
		if err != nil || responseBytes > 4<<20 {
			return nil, fail("APPROVAL_PREVIEW_RESULT_LIMIT")
		}
		out = append(out, candidate)
		ranked = append(ranked, rankedCandidate{candidate, score})
	}
	if typ == "skill" {
		sort.SliceStable(ranked, func(i, j int) bool {
			a, b := ranked[i], ranked[j]
			if a.score != b.score {
				return a.score > b.score
			}
			return textcompat.Fold(a.candidate.TargetLabel) < textcompat.Fold(b.candidate.TargetLabel)
		})
		out = []ApprovalCandidate{}
		for _, item := range ranked[:min(5, len(ranked))] {
			out = append(out, item.candidate)
		}
	}
	return out, nil
}
