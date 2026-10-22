package vaultwrite

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/intakeread"
	"careerhub.local/backend/internal/textcompat"
	"golang.org/x/text/unicode/norm"
)

type intakeEntity struct {
	ID           string   `json:"id"`
	Type         string   `json:"entity_type"`
	State        string   `json:"review_state"`
	Name         string   `json:"name"`
	Organization string   `json:"organization"`
	Title        string   `json:"title"`
	Institution  string   `json:"institution"`
	Degree       string   `json:"degree"`
	Credential   string   `json:"credential"`
	Major        string   `json:"major"`
	GPA          string   `json:"gpa"`
	Date         string   `json:"date_text"`
	Location     string   `json:"location"`
	Emails       []string `json:"emails"`
	Phones       []string `json:"phones"`
	Links        []string `json:"links"`
	Technologies []string `json:"technologies"`
	Items        []string `json:"items"`
	Honors       []string `json:"honors"`
	Pending      []struct {
		Name    string  `json:"name"`
		GroupID *string `json:"primary_group_id"`
	} `json:"pending_skills"`
}

func intakeFailure(code string) *Error { return &Error{Stage: "intake_approval", Code: code} }
func intakeNormal(value string) string {
	value = textcompat.Fold(norm.NFKC.String(value))
	return strings.Join(strings.FieldsFunc(value, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }), " ")
}
func intakeUnique(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = textcompat.Strip(value)
		key := intakeNormal(value)
		if key != "" && !seen[key] {
			out = append(out, value)
			seen[key] = true
		}
	}
	return out
}
func intakeList(values []string) cataloginput.Value {
	v := cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{}}
	for _, text := range values {
		v.Items = append(v.Items, textValue(text))
	}
	return v
}
func intakeField(record cataloginput.Value, key string) cataloginput.Value {
	v, _ := get(record, key)
	return v
}
func intakeTruthy(v cataloginput.Value) bool {
	switch v.Kind {
	case "", "null":
		return false
	case "string", "bytes":
		return v.Text != "" || len(v.Bytes) > 0
	case "bool":
		return v.Boolean
	case "int":
		return v.Text != "0"
	case "float":
		return v.Text != "0.0" && v.Text != "-0.0"
	case "sequence":
		return len(v.Items) > 0
	case "mapping":
		return len(v.Entries) > 0
	}
	return true
}
func intakeScalar(v cataloginput.Value) (string, *Error) {
	if !intakeTruthy(v) {
		return "", nil
	}
	switch v.Kind {
	case "string", "int", "float", "date":
		return v.Text, nil
	case "bool":
		return "True", nil
	}
	return "", intakeFailure("APPROVAL_VALUE_UNSUPPORTED")
}
func intakeStrings(v cataloginput.Value) ([]string, *Error) {
	if !intakeTruthy(v) {
		return []string{}, nil
	}
	if v.Kind != "sequence" {
		return nil, intakeFailure("APPROVAL_VALUE_UNSUPPORTED")
	}
	out := []string{}
	for _, item := range v.Items {
		text, err := intakeScalar(item)
		if err != nil {
			return nil, err
		}
		out = append(out, text)
	}
	return out, nil
}
func intakeAppend(record *cataloginput.Value, key string, values []string) *Error {
	old, err := intakeStrings(intakeField(*record, key))
	if err != nil {
		return err
	}
	set(record, key, intakeList(intakeUnique(append(old, values...))))
	return nil
}
func intakeNote(source string, blocks []string) string {
	return "Imported from intake review " + source + "; source blocks: " + strings.Join(blocks, ", ") + "."
}
func intakeLinks(links []string) map[string]string {
	out := map[string]string{}
	for _, link := range links {
		lower := textcompat.Fold(link)
		key := "website"
		if strings.Contains(lower, "linkedin.com") {
			key = "linkedin"
		} else if strings.Contains(lower, "github.com") {
			key = "github"
		}
		if _, ok := out[key]; !ok {
			out[key] = link
		}
	}
	return out
}

func intakeSourceValues(e intakeEntity, proof, names []string) map[string]cataloginput.Value {
	v := map[string]cataloginput.Value{}
	text := func(k, s string) {
		if s != "" {
			v[k] = textValue(s)
		}
	}
	list := func(k string, s []string) {
		if len(s) > 0 {
			v[k] = intakeList(s)
		}
	}
	switch e.Type {
	case "profile":
		text("name", e.Name)
		text("location", e.Location)
		if len(e.Emails) > 0 {
			text("email", e.Emails[0])
		}
		if len(e.Phones) > 0 {
			text("phone", e.Phones[0])
		}
		for k, s := range intakeLinks(e.Links) {
			text(k, s)
		}
	case "experience":
		text("organization", e.Organization)
		text("role", e.Title)
		text("dates", e.Date)
		text("location", e.Location)
		list("technologies", names)
		list("proof_points", proof)
	case "education":
		text("institution", e.Institution)
		text("degree", e.Degree)
		text("major", e.Major)
		text("graduation", e.Date)
		text("location", e.Location)
		text("gpa", e.GPA)
		list("honors", e.Honors)
	case "certification":
		text("name", e.Name)
		text("credential", e.Credential)
		text("completed", e.Date)
		list("technologies", names)
		list("proof_points", proof)
	case "project":
		text("name", e.Title)
		if len(e.Items) > 0 {
			text("summary", e.Items[0])
		}
		text("dates", e.Date)
		list("technologies", names)
		list("proof_points", proof)
	case "skill":
		text("skill_family", e.Name)
		list("keywords", intakeUnique(append(append([]string{}, e.Items...), names...)))
	}
	return v
}

var intakeAllowedFields = map[string][]string{
	"profile":       {"name", "location", "email", "phone", "linkedin", "github", "website"},
	"experience":    {"organization", "role", "dates", "location", "category", "technologies", "proof_points"},
	"education":     {"institution", "degree", "major", "graduation", "location", "gpa", "honors"},
	"certification": {"name", "credential", "completed", "technologies", "proof_points"},
	"project":       {"name", "category", "summary", "dates", "technologies", "proof_points"},
	"skill":         {"skill_family", "keywords"},
}

func intakeSelected(e intakeEntity, d intakeread.ReviewDecision, source map[string]cataloginput.Value) (map[string]cataloginput.Value, *Error) {
	allowed := map[string]bool{}
	for _, field := range intakeAllowedFields[e.Type] {
		allowed[field] = true
	}
	selected := map[string]cataloginput.Value{}
	for field, resolution := range d.FieldResolutions {
		if !allowed[field] {
			return nil, intakeFailure("INVALID_FIELD_RESOLUTION")
		}
		if resolution != "source" && resolution != "add_source" {
			continue
		}
		value, ok := source[field]
		if !ok || !intakeTruthy(value) || resolution == "add_source" && value.Kind != "sequence" {
			return nil, intakeFailure("INVALID_FIELD_RESOLUTION")
		}
		selected[field] = value
	}
	return selected, nil
}
func intakeID(e intakeEntity, records cataloginput.Value, sourceID string) (string, *Error) {
	label := e.Name
	switch e.Type {
	case "experience":
		label = e.Organization + " " + e.Title
	case "education":
		label = e.Institution
		if label == "" {
			label = e.Degree
		}
	case "certification":
		label = e.Credential
		if label == "" {
			label = e.Name
		}
	case "project":
		label = e.Title
	}
	slug := func(text string) (string, *Error) {
		ascii, err := textcompat.ASCIIFromNFKD15(text)
		if err != nil {
			return "", intakeFailure("APPROVAL_NAME_UNICODE_UNSUPPORTED")
		}
		text = textcompat.Fold(ascii)
		parts := strings.FieldsFunc(text, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') })
		result := strings.Join(parts, "_")
		if len(result) > 80 {
			result = result[:80]
		}
		return result, nil
	}
	base, err := slug(label)
	if err != nil {
		return "", err
	}
	if base == "" {
		base, err = slug(e.ID)
		if err != nil {
			return "", err
		}
	}
	if base == "" {
		base = "career_record"
	}
	ids := map[string]bool{}
	for _, item := range records.Items {
		id, err := intakeScalar(intakeField(item, "id"))
		if err != nil {
			return "", err
		}
		ids[id] = true
	}
	if !ids[base] {
		return base, nil
	}
	digest := sha256.Sum256([]byte(sourceID + ":" + e.ID))
	candidate := base + "_" + fmt.Sprintf("%x", digest[:4])
	if ids[candidate] {
		return "", intakeFailure("CREATE_ID_CONFLICT")
	}
	return candidate, nil
}

func intakeCreate(e intakeEntity, d intakeread.ReviewDecision, source map[string]cataloginput.Value, id, note string, skillIDs []string) (cataloginput.Value, *Error) {
	if e.Type == "profile" {
		return cataloginput.Value{}, intakeFailure("PROFILE_CREATE_NOT_ALLOWED")
	}
	selected := source
	legacy := d.FieldResolutions == nil
	if !legacy {
		var err *Error
		selected, err = intakeSelected(e, d, source)
		if err != nil {
			return cataloginput.Value{}, err
		}
		required := map[string][]string{"experience": {"organization", "role", "dates"}, "education": {"institution"}, "certification": {"name", "credential", "completed"}, "project": {"name", "summary"}, "skill": {"skill_family", "keywords"}}
		for _, k := range required[e.Type] {
			if !intakeTruthy(selected[k]) {
				return cataloginput.Value{}, intakeFailure("MISSING_APPROVAL_FIELD")
			}
		}
	}
	r := cataloginput.Value{Kind: "mapping"}
	set(&r, "id", textValue(id))
	copyField := func(from, to string, list bool, required bool) {
		value, ok := selected[from]
		if ok {
			set(&r, to, value)
		} else if required {
			if list {
				set(&r, to, intakeList(nil))
			} else {
				set(&r, to, textValue(""))
			}
		}
	}
	switch e.Type {
	case "experience":
		copyField("organization", "company", false, true)
		copyField("role", "role", false, true)
		copyField("dates", "dates", false, true)
		set(&r, "category", textValue("Work Experience"))
		copyField("technologies", "technologies", true, true)
		copyField("proof_points", "proof_points", true, true)
		copyField("location", "location", false, false)
	case "education":
		copyField("institution", "institution", false, true)
		for _, key := range []string{"degree", "major", "graduation", "location", "gpa", "honors"} {
			dest := key
			if key == "graduation" {
				dest = "graduation_date"
			}
			copyField(key, dest, key == "honors", legacy)
		}
	case "certification":
		copyField("name", "name", false, true)
		copyField("credential", "credential", false, true)
		copyField("completed", "completed", false, true)
		copyField("proof_points", "proof_points", true, true)
	case "project":
		copyField("name", "name", false, true)
		set(&r, "category", textValue("Project"))
		copyField("summary", "summary", false, true)
		if legacy && len(e.Items) == 0 {
			set(&r, "summary", textValue("Source-backed project record."))
		}
		copyField("technologies", "technologies", true, true)
		copyField("proof_points", "proof_points", true, true)
		copyField("dates", "dates", false, false)
	case "skill":
		copyField("skill_family", "skill_family", false, true)
		set(&r, "related_experience", intakeList(nil))
		copyField("keywords", "keywords", true, true)
	default:
		return cataloginput.Value{}, intakeFailure("UNSUPPORTED_APPROVAL_TYPE")
	}
	set(&r, "notes", intakeList([]string{note}))
	_, selectedSkills := selected["technologies"]
	if len(skillIDs) > 0 && (legacy || selectedSkills) && (e.Type == "experience" || e.Type == "project" || e.Type == "certification") {
		set(&r, "competency_ids", intakeList(skillIDs))
	}
	return r, nil
}

func intakeMergeProfile(e intakeEntity, d intakeread.ReviewDecision, source map[string]cataloginput.Value, r *cataloginput.Value) *Error {
	selected := source
	legacy := d.FieldResolutions == nil
	if !legacy {
		var err *Error
		selected, err = intakeSelected(e, d, source)
		if err != nil {
			return err
		}
	}
	for _, pair := range [][2]string{{"name", "name"}, {"location", "current_location"}} {
		if value, ok := selected[pair[0]]; ok && (!legacy || !intakeTruthy(intakeField(*r, pair[1]))) {
			set(r, pair[1], value)
		}
	}
	contact, exists := get(*r, "contact")
	needed := legacy
	for _, key := range []string{"email", "phone", "linkedin", "github", "website"} {
		if _, ok := selected[key]; ok {
			needed = true
		}
	}
	if !needed {
		return nil
	}
	if !exists {
		contact = cataloginput.Value{Kind: "mapping"}
	}
	if contact.Kind != "mapping" {
		return intakeFailure("INVALID_VAULT_SHAPE")
	}
	if legacy {
		// Legacy merge fills missing contacts in source order, including empty
		// values. Field-level selection intentionally uses first-link semantics.
		for _, item := range []struct {
			key    string
			values []string
		}{{"email", e.Emails}, {"phone", e.Phones}} {
			if len(item.values) > 0 && !intakeTruthy(intakeField(contact, item.key)) {
				set(&contact, item.key, textValue(item.values[0]))
			}
		}
		for _, link := range e.Links {
			for key := range intakeLinks([]string{link}) {
				if !intakeTruthy(intakeField(contact, key)) {
					set(&contact, key, textValue(link))
				}
			}
		}
		set(r, "contact", contact)
		return nil
	}
	for _, key := range []string{"email", "phone", "linkedin", "github", "website"} {
		if value, ok := selected[key]; ok && (!legacy || !intakeTruthy(intakeField(contact, key))) {
			set(&contact, key, value)
		}
	}
	set(r, "contact", contact)
	return nil
}

var intakeScalarFields = map[string][][2]string{
	"experience":    {{"organization", "company"}, {"role", "role"}, {"dates", "dates"}, {"location", "location"}},
	"education":     {{"institution", "institution"}, {"degree", "degree"}, {"major", "major"}, {"graduation", "graduation_date"}, {"location", "location"}, {"gpa", "gpa"}},
	"certification": {{"name", "name"}, {"credential", "credential"}, {"completed", "completed"}},
	"project":       {{"name", "name"}, {"summary", "summary"}, {"dates", "dates"}},
	"skill":         {{"skill_family", "skill_family"}},
}

func intakeMerge(e intakeEntity, d intakeread.ReviewDecision, source map[string]cataloginput.Value, r *cataloginput.Value, note string, skillIDs []string) *Error {
	if e.Type == "profile" {
		return intakeMergeProfile(e, d, source, r)
	}
	legacy := d.FieldResolutions == nil
	selected := source
	if !legacy {
		var err *Error
		selected, err = intakeSelected(e, d, source)
		if err != nil {
			return err
		}
	}
	for _, pair := range intakeScalarFields[e.Type] {
		if legacy && (e.Type == "experience" && pair[0] == "location" || e.Type == "project" && pair[0] != "summary" || e.Type == "skill") {
			continue
		}
		if value, ok := selected[pair[0]]; ok && (!legacy || !intakeTruthy(intakeField(*r, pair[1]))) {
			set(r, pair[1], value)
		}
	}
	if legacy && e.Type == "experience" && e.Title != "" {
		role, err := intakeScalar(intakeField(*r, "role"))
		if err != nil {
			return err
		}
		if intakeNormal(e.Title) != intakeNormal(role) {
			if err := intakeAppend(r, "resume_title_options", []string{e.Title}); err != nil {
				return err
			}
		}
	}
	lists := map[string][]string{"experience": {"technologies", "proof_points"}, "education": {"honors"}, "certification": {"proof_points"}, "project": {"technologies", "proof_points"}, "skill": {"keywords"}}
	for _, key := range lists[e.Type] {
		value, ok := selected[key]
		if !ok && !legacy {
			continue
		}
		items, err := intakeStrings(value)
		if err != nil {
			return err
		}
		if err := intakeAppend(r, key, items); err != nil {
			return err
		}
	}
	if legacy && e.Type == "skill" {
		if _, ok := get(*r, "related_experience"); !ok {
			set(r, "related_experience", intakeList(nil))
		}
	}
	_, selectedSkills := selected["technologies"]
	if len(skillIDs) > 0 && (legacy || selectedSkills) && (e.Type == "experience" || e.Type == "project" || e.Type == "certification") {
		if err := intakeAppend(r, "competency_ids", skillIDs); err != nil {
			return err
		}
	}
	if legacy || len(selected) > 0 {
		return intakeAppend(r, "notes", []string{note})
	}
	return nil
}
