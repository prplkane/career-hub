package vaultwrite

import (
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/calendarinput"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/textcompat"
	"careerhub.local/backend/internal/vaultread"
)

var experienceFields = []string{"company", "role", "dates", "date_range", "category", "technologies", "proof_points"}
var projectFields = []string{"name", "category", "summary", "dates", "date_range", "technologies", "related_experience", "proof_points"}
var selectionFields = []string{"competency_ids", "pending_skills", "registry_revision"}

func evidenceLabel(target vaultread.EditTarget) (string, bool) {
	switch target {
	case vaultread.ExperienceTarget:
		return "Experience", true
	case vaultread.ProjectTarget:
		return "Project", true
	}
	return "", false
}

func evidenceMissing(target vaultread.EditTarget, id *string) *Error {
	label, ok := evidenceLabel(target)
	if !ok {
		return &Error{Stage: "configuration", Code: "VAULT_EDITOR_TARGET_INVALID"}
	}
	message := label + " record does not exist"
	if id != nil {
		message += ": " + *id
	}
	return &Error{Stage: "identity", Code: "VAULT_EDITOR_RECORD_NOT_FOUND",
		SourceClass: "VaultRecordNotFound", SourceMessage: message}
}

// Windows Path.name compatibility is pinned independently of the future host
// OS. IDs are record keys, never filesystem components passed to an I/O API.
func validEvidenceID(id string) bool {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return false
	}
	_, firstSize := utf8.DecodeRuneInString(id)
	return !(len(id) > firstSize && id[firstSize] == ':')
}

func checkRecordKeys(payload map[string]any, fields []string, label string) *Error {
	if len(payload) > 1000 {
		return &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_LIMIT"}
	}
	allowed := map[string]bool{}
	for _, key := range fields {
		allowed[key] = true
	}
	unknown, size := []string{}, 0
	for key := range payload {
		size += len(key)
		if size > maxPayloadBytes {
			return &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_LIMIT"}
		}
		if !utf8.ValidString(key) {
			return &Error{Stage: "input", Code: "VAULT_EDITOR_UNICODE_UNSUPPORTED"}
		}
		if !allowed[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return invalid("Unknown " + strings.ToLower(label) + " fields: " + strings.Join(unknown, ", "))
	}
	return nil
}

// Date validators inspect scalar type, not arbitrary nested objects. Preserve
// the wrong-type shape without recursively coercing untrusted JSON content.
func dateScalar(value any) (cataloginput.Value, *Error) {
	switch v := value.(type) {
	case nil:
		return cataloginput.Value{Kind: "null"}, nil
	case string:
		if len(v) > maxPayloadBytes {
			return cataloginput.Value{}, &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_LIMIT"}
		}
		if !utf8.ValidString(v) {
			return cataloginput.Value{}, &Error{Stage: "input", Code: "VAULT_EDITOR_UNICODE_UNSUPPORTED"}
		}
		return textValue(v), nil
	case bool:
		return cataloginput.Value{Kind: "bool", Boolean: v}, nil
	case float64, int:
		return cataloginput.Value{Kind: "float"}, nil
	case []any:
		return cataloginput.Value{Kind: "sequence"}, nil
	case map[string]any:
		return cataloginput.Value{Kind: "mapping"}, nil
	default:
		return cataloginput.Value{}, &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_UNSUPPORTED"}
	}
}

func evidenceDates(payload map[string]any, required bool) (calendarinput.Fields, *Error) {
	dates, e := dateScalar(payload["dates"])
	if e != nil {
		return calendarinput.Fields{}, e
	}
	// Preserve dates-before-range validation even if the range itself is malformed.
	if dates.Kind != "null" && dates.Kind != "string" {
		return calendarinput.Fields{}, invalid("dates must be a string")
	}
	raw, e := dateScalar(payload["date_range"])
	if e != nil {
		return calendarinput.Fields{}, e
	}
	if values, ok := payload["date_range"].(map[string]any); ok {
		if len(values) > 1000 {
			return calendarinput.Fields{}, &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_LIMIT"}
		}
		unknown := false
		for key := range values {
			if len(key) > maxPayloadBytes {
				return calendarinput.Fields{}, &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_LIMIT"}
			}
			if !utf8.ValidString(key) {
				return calendarinput.Fields{}, &Error{Stage: "input", Code: "VAULT_EDITOR_UNICODE_UNSUPPORTED"}
			}
			if key != "start" && key != "end" && key != "ongoing" {
				unknown = true
			}
		}
		if unknown {
			return calendarinput.Fields{}, invalid("date_range has unknown fields")
		}
		for _, key := range []string{"start", "end", "ongoing"} {
			if value, present := values[key]; present {
				child, e := dateScalar(value)
				if e != nil {
					return calendarinput.Fields{}, e
				}
				set(&raw, key, child)
			}
		}
	}
	result, calendarError := calendarinput.NormalizeRangeFields(dates, raw, required)
	if calendarError != nil {
		if calendarError.Cause.Code == calendarinput.InputUnsupported {
			return calendarinput.Fields{}, &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_UNSUPPORTED"}
		}
		return calendarinput.Fields{}, invalid(calendarError.Message())
	}
	return result, nil
}

func normalizeEvidence(target vaultread.EditTarget, payload map[string]any) (cataloginput.Value, *Error) {
	label, ok := evidenceLabel(target)
	if !ok {
		return cataloginput.Value{}, &Error{Stage: "configuration", Code: "VAULT_EDITOR_TARGET_INVALID"}
	}
	fields := experienceFields
	if target == vaultread.ProjectTarget {
		fields = projectFields
	}
	if e := checkRecordKeys(payload, append(append([]string(nil), fields...), selectionFields...), label); e != nil {
		return cataloginput.Value{}, e
	}
	dates, e := evidenceDates(payload, target == vaultread.ExperienceTarget)
	if e != nil {
		return cataloginput.Value{}, e
	}
	out := cataloginput.Value{Kind: "mapping"}
	for _, key := range fields {
		var value cataloginput.Value
		switch key {
		case "date_range":
			if dates.DateRange == nil {
				continue
			}
			value = dates.DateRange.Value()
		case "dates":
			value = textValue(dates.Dates)
		case "technologies", "proof_points", "related_experience":
			value, e = cleanList(payload[key], key)
		default:
			limit := 500
			if key == "summary" {
				limit = 5000
			}
			value, e = cleanText(payload[key], key, false, limit)
		}
		if e != nil {
			return cataloginput.Value{}, e
		}
		set(&out, key, value)
	}
	return out, nil
}

func allocateEvidenceID(target vaultread.EditTarget, record cataloginput.Value, existing map[string]bool) (string, *Error) {
	label, ok := evidenceLabel(target)
	if !ok {
		return "", &Error{Stage: "configuration", Code: "VAULT_EDITOR_TARGET_INVALID"}
	}
	name, _ := get(record, "name")
	if target == vaultread.ExperienceTarget {
		company, _ := get(record, "company")
		role, _ := get(record, "role")
		name = textValue(company.Text + " " + role.Text)
	}
	return allocateNamedID(name.Text, "manual_"+strings.ToLower(label), existing)
}

func allocateNamedID(name, fallback string, existing map[string]bool) (string, *Error) {
	ascii, e := textcompat.ASCIIFromNFKD15(name)
	if e != nil {
		return "", &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_UNSUPPORTED"}
	}
	var slug strings.Builder
	separator := false
	for _, char := range strings.ToLower(ascii) {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			if separator && slug.Len() > 0 {
				slug.WriteByte('_')
			}
			slug.WriteRune(char)
			separator = false
		} else {
			separator = true
		}
	}
	base := slug.String()
	if len(base) > 72 {
		base = base[:72]
	}
	if base == "" {
		base = fallback
	}
	if !existing[base] {
		return base, nil
	}
	for index := 2; ; index++ {
		id := base + "_" + strconv.Itoa(index)
		if !existing[id] {
			return id, nil
		}
	}
}

type evidenceRecordPlan struct {
	Candidate cataloginput.Value
	Index     int
}

// planEvidenceRecord accepts an owned clone from EditorState.Record. Archive
// never normalizes input or changes skills. Update clears stale date metadata
// when normalization cannot derive a range from the newly submitted dates.
func planEvidenceRecord(target vaultread.EditTarget, original, normalized cataloginput.Value, operation, id string, archived bool) (evidenceRecordPlan, *Error) {
	if _, ok := evidenceLabel(target); !ok || original.Kind != "sequence" {
		return evidenceRecordPlan{}, &Error{Stage: "configuration", Code: "VAULT_EDITOR_TARGET_INVALID"}
	}
	index := -1
	var record cataloginput.Value
	if operation == "create" {
		record = cataloginput.Value{Kind: "mapping"}
		set(&record, "id", textValue(id))
		index = len(original.Items)
	} else if operation == "update" || operation == "archive" {
		for i, item := range original.Items {
			value, _ := get(item, "id")
			if item.Kind == "mapping" && value.Kind == "string" && value.Text == id {
				index, record = i, item
				break
			}
		}
		if index < 0 {
			return evidenceRecordPlan{}, evidenceMissing(target, &id)
		}
	} else {
		return evidenceRecordPlan{}, &Error{Stage: "configuration", Code: "VAULT_EDITOR_OPERATION_INVALID"}
	}
	if operation == "archive" {
		if archived {
			set(&record, "archived", cataloginput.Value{Kind: "bool", Boolean: true})
		} else {
			remove(&record, "archived")
		}
	} else {
		if _, present := get(normalized, "date_range"); !present {
			remove(&record, "date_range")
		}
		for _, entry := range normalized.Entries {
			set(&record, entry.Key.Text, entry.Value)
		}
		set(&record, "id", textValue(id))
		if operation == "create" {
			set(&record, "source", textValue("manual"))
		}
	}
	if index == len(original.Items) {
		original.Items = append(original.Items, record)
	} else {
		original.Items[index] = record
	}
	return evidenceRecordPlan{original, index}, nil
}
