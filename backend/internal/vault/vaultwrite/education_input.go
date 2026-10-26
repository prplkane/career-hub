package vaultwrite

import (
	"slices"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/vault/calendarinput"
	"careerhub.local/backend/internal/vault/vaultread"
)

var schoolFields = []string{"institution", "degree", "major", "location", "dates", "date_range",
	"graduation_date", "graduation_month", "gpa", "status", "honors"}
var certificationFields = []string{"name", "credential", "completed", "completed_month", "proof_points"}

func educationKind(target vaultread.EditTarget) (string, bool) {
	switch target {
	case vaultread.SchoolTarget:
		return "school", true
	case vaultread.CertificationTarget:
		return "certification", true
	}
	return "", false
}

func educationMonth(payload map[string]any, field string, required bool) (cataloginput.Value, string, *Error) {
	display, err := cleanText(payload[field], field, true, 500)
	if err != nil {
		return cataloginput.Value{}, "", err
	}
	if display.Text == "" {
		if required {
			return cataloginput.Value{}, "", invalid(field + " is required")
		}
		return display, "", nil
	}
	month, issue := calendarinput.NormalizeMonth(display)
	if issue != nil {
		if issue.Code == calendarinput.InputUnsupported {
			return cataloginput.Value{}, "", &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_UNSUPPORTED"}
		}
		return cataloginput.Value{}, "", invalid(issue.Format(field))
	}
	return display, month, nil
}

func normalizeEducation(target vaultread.EditTarget, payload map[string]any) (cataloginput.Value, *Error) {
	kind, ok := educationKind(target)
	if !ok {
		return cataloginput.Value{}, &Error{Stage: "configuration", Code: "VAULT_EDITOR_TARGET_INVALID"}
	}
	fields, monthField := schoolFields, "graduation_date"
	if target == vaultread.CertificationTarget {
		fields, monthField = certificationFields, "completed"
	}
	if err := checkRecordKeys(payload, fields, kind); err != nil {
		return cataloginput.Value{}, err
	}
	var dates calendarinput.Fields
	if target == vaultread.SchoolTarget {
		var err *Error
		dates, err = evidenceDates(payload, false)
		if err != nil {
			return cataloginput.Value{}, err
		}
	}
	display, month, err := educationMonth(payload, monthField, target == vaultread.CertificationTarget)
	if err != nil {
		return cataloginput.Value{}, err
	}
	out := cataloginput.Value{Kind: "mapping"}
	for _, key := range fields {
		var value cataloginput.Value
		switch key {
		case "dates":
			value = textValue(dates.Dates)
		case "date_range":
			if dates.DateRange == nil {
				continue
			}
			value = dates.DateRange.Value()
		case "graduation_date", "completed":
			value = display
		case "graduation_month", "completed_month":
			// Source accepts these keys but derives their values from display.
			if month == "" {
				continue
			}
			value = textValue(month)
		case "honors", "proof_points":
			value, err = cleanList(payload[key], key)
		default:
			optional := key == "major" || key == "location" || key == "gpa" || key == "status"
			value, err = cleanText(payload[key], key, optional, 500)
		}
		if err != nil {
			return cataloginput.Value{}, err
		}
		set(&out, key, value)
	}
	return out, nil
}

func educationMissing(id *string) *Error {
	message := "Education record does not exist"
	if id != nil {
		message += ": " + *id
	}
	return &Error{Stage: "identity", Code: "VAULT_EDITOR_RECORD_NOT_FOUND", SourceClass: "VaultRecordNotFound", SourceMessage: message}
}

func educationItems(original cataloginput.Value, collection string) (cataloginput.Value, *Error) {
	records, present := get(original, collection)
	if !present {
		return cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{}}, nil
	}
	if records.Kind != "sequence" {
		return cataloginput.Value{}, invalid("career_vault/education.yaml." + collection + ": expected list")
	}
	return records, nil
}

// Inputs are owned clones from EditorState. Unknown root/record fields and both
// collection orders survive; update removes stale derived month/range metadata.
func planEducationRecord(target vaultread.EditTarget, original, normalized cataloginput.Value, op, id string, archived bool) (cataloginput.Value, *Error) {
	collection, ok := target.EducationCollection()
	if !ok || original.Kind != "mapping" {
		return cataloginput.Value{}, &Error{Stage: "configuration", Code: "VAULT_EDITOR_TARGET_INVALID"}
	}
	records, err := educationItems(original, collection)
	if err != nil {
		return cataloginput.Value{}, err
	}
	index := -1
	record := cataloginput.Value{Kind: "mapping"}
	if op == "create" {
		index = len(records.Items)
		set(&record, "id", textValue(id))
	} else if op == "update" || op == "archive" {
		for i, item := range records.Items {
			key, _ := get(item, "id")
			if item.Kind == "mapping" && key.Kind == "string" && key.Text == id {
				index, record = i, item
				break
			}
		}
		if index < 0 {
			return cataloginput.Value{}, educationMissing(&id)
		}
		record.Entries = slices.Clone(record.Entries)
	} else {
		return cataloginput.Value{}, &Error{Stage: "configuration", Code: "VAULT_EDITOR_OPERATION_INVALID"}
	}
	if op == "archive" {
		if archived {
			set(&record, "archived", cataloginput.Value{Kind: "bool", Boolean: true})
		} else {
			remove(&record, "archived")
		}
	} else {
		for _, key := range []string{"date_range", "graduation_month", "completed_month"} {
			if _, present := get(normalized, key); !present {
				// Only clear derived fields owned by this record's normalizer.
				if target == vaultread.SchoolTarget && key != "completed_month" || target == vaultread.CertificationTarget && key == "completed_month" {
					remove(&record, key)
				}
			}
		}
		for _, entry := range normalized.Entries {
			set(&record, entry.Key.Text, entry.Value)
		}
		set(&record, "id", textValue(id))
		if op == "create" {
			set(&record, "source", textValue("manual"))
		}
	}
	records.Items = slices.Clone(records.Items)
	if index == len(records.Items) {
		records.Items = append(records.Items, record)
	} else {
		records.Items[index] = record
	}
	original.Entries = slices.Clone(original.Entries)
	set(&original, collection, records)
	return original, nil
}
