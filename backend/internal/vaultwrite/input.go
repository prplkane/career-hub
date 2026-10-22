// Package vaultwrite implements explicit native-owned Career Vault edits. It is
// deliberately not connected to the shipped Python/Electron routes yet.
package vaultwrite

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogwrite"
	"careerhub.local/backend/internal/textcompat"
	"careerhub.local/backend/internal/vaultread"
)

// Error's printable form is content-free. SourceMessage can contain user input
// (unknown field names); only an explicit local validation response may use it.
// Committed remains true after a post-publication failure. Never blindly retry.
type Error struct {
	Stage, Code, SourceClass, SourceMessage string
	Committed                               bool
	Vault                                   *vaultread.Error
	Catalog                                 *catalogwrite.Error
}

func (*Error) Error() string                { return "Career Vault edit could not be completed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }

func invalid(message string) *Error {
	return &Error{Stage: "validation", Code: "VAULT_EDITOR_INVALID",
		SourceClass: "VaultEditorError", SourceMessage: message}
}

var contactFields = []string{
	"email", "phone", "address", "address_line1", "address_line2", "city",
	"region", "postal_code", "country", "linkedin", "github", "website",
}
var profileFields = []string{
	"name", "given_name", "family_name", "preferred_name", "current_location",
	"target_location", "location_flexibility", "work_authorization", "major", "role_targets",
}
var eligibilityFields = []string{
	"current_status", "work_authorization", "requires_sponsorship_now",
	"requires_future_sponsorship", "stem_opt_eligible",
	"citizenship_required_blocker", "clearance_required_blocker",
}

func textValue(text string) cataloginput.Value { return cataloginput.Value{Kind: "string", Text: text} }
func set(record *cataloginput.Value, key string, value cataloginput.Value) {
	for i, entry := range record.Entries {
		if entry.Key.Kind == "string" && entry.Key.Text == key {
			record.Entries[i].Value = value
			return
		}
	}
	record.Entries = append(record.Entries, cataloginput.Entry{Key: textValue(key), Value: value})
}
func remove(record *cataloginput.Value, key string) {
	for i, entry := range record.Entries {
		if entry.Key.Kind == "string" && entry.Key.Text == key {
			record.Entries = append(record.Entries[:i], record.Entries[i+1:]...)
			return
		}
	}
}
func get(record cataloginput.Value, key string) (cataloginput.Value, bool) {
	for _, entry := range record.Entries {
		if entry.Key.Kind == "string" && entry.Key.Text == key {
			return entry.Value, true
		}
	}
	return cataloginput.Value{}, false
}

// Payload comes from a bounded JSON adapter: string keys and []any arrays.
// Wrong JSON scalar types remain validation errors rather than coercions.
const maxPayloadBytes = 2 << 20

func cleanText(value any, field string, optional bool, maxChars int) (cataloginput.Value, *Error) {
	if optional && value == nil {
		return textValue(""), nil
	}
	text, ok := value.(string)
	if !ok {
		return cataloginput.Value{}, invalid(field + " must be a string")
	}
	if !utf8.ValidString(text) {
		return cataloginput.Value{}, &Error{Stage: "input", Code: "VAULT_EDITOR_UNICODE_UNSUPPORTED"}
	}
	if len(text) > maxPayloadBytes {
		return cataloginput.Value{}, &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_LIMIT"}
	}
	if optional && text == "" {
		return textValue(""), nil
	}
	text = strings.Join(strings.FieldsFunc(text, textcompat.IsSpace), " ")
	if text == "" {
		return cataloginput.Value{}, invalid(field + " is required")
	}
	if utf8.RuneCountInString(text) > maxChars {
		return cataloginput.Value{}, invalid(field + " must not exceed " + strconv.Itoa(maxChars) + " characters")
	}
	return textValue(text), nil
}

func cleanRoles(value any) (cataloginput.Value, *Error) {
	return cleanList(value, "role_targets")
}

func cleanList(value any, listField string) (cataloginput.Value, *Error) {
	items, ok := value.([]any)
	if !ok {
		return cataloginput.Value{}, invalid(listField + " must be a list")
	}
	if len(items) > 100 {
		return cataloginput.Value{}, invalid(listField + " must not contain more than 100 items")
	}
	out := cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{}}
	seen := map[string]bool{}
	size := 0
	for i, item := range items {
		field := fmt.Sprintf("%s[%d]", listField, i)
		text, ok := item.(string)
		if !ok {
			return cataloginput.Value{}, invalid(field + " must be a string")
		}
		if !utf8.ValidString(text) {
			return cataloginput.Value{}, &Error{Stage: "input", Code: "VAULT_EDITOR_UNICODE_UNSUPPORTED"}
		}
		size += len(text)
		if size > maxPayloadBytes {
			return cataloginput.Value{}, &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_LIMIT"}
		}
		text = strings.Join(strings.FieldsFunc(text, textcompat.IsSpace), " ")
		if text == "" {
			continue
		}
		if utf8.RuneCountInString(text) > 5000 {
			return cataloginput.Value{}, invalid(field + " must not exceed 5000 characters")
		}
		key := textcompat.Fold(text)
		if !seen[key] {
			out.Items = append(out.Items, textValue(text))
			seen[key] = true
		}
	}
	return out, nil
}

func normalize(target vaultread.EditTarget, payload map[string]any) (cataloginput.Value, *Error) {
	return normalizeVersioned(target, payload, false)
}

// Only the admitted, same-read profile format may enable unknown facts. The
// browser cannot change schema version through ordinary editor payloads.
func normalizeVersioned(target vaultread.EditTarget, payload map[string]any, unknownFacts bool) (cataloginput.Value, *Error) {
	fields, prefix := profileFields, "Unknown profile fields: "
	allowed := map[string]bool{}
	switch target {
	case vaultread.ProfileTarget:
		for _, field := range contactFields {
			allowed[field] = true
		}
	case vaultread.EligibilityTarget:
		fields, prefix = eligibilityFields, "Unknown eligibility profile fields: "
	default:
		return cataloginput.Value{}, &Error{Stage: "configuration", Code: "VAULT_EDITOR_TARGET_INVALID"}
	}
	for _, field := range fields {
		allowed[field] = true
	}
	// Bound unknown keys without traversing arbitrary unsupported values.
	if len(payload) > 1000 {
		return cataloginput.Value{}, &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_LIMIT"}
	}
	unknown, keyBytes := []string{}, 0
	for key := range payload {
		keyBytes += len(key)
		if keyBytes > maxPayloadBytes {
			return cataloginput.Value{}, &Error{Stage: "input", Code: "VAULT_EDITOR_INPUT_LIMIT"}
		}
		if !utf8.ValidString(key) {
			return cataloginput.Value{}, &Error{Stage: "input", Code: "VAULT_EDITOR_UNICODE_UNSUPPORTED"}
		}
		if !allowed[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) != 0 {
		sort.Strings(unknown)
		return cataloginput.Value{}, invalid(prefix + strings.Join(unknown, ", "))
	}
	out := cataloginput.Value{Kind: "mapping"}
	contact := cataloginput.Value{Kind: "mapping"}
	if target == vaultread.ProfileTarget {
		// The source validates contacts BEFORE even the required name.
		for _, field := range contactFields {
			value, e := cleanText(payload[field], field, true, 500)
			if e != nil {
				return cataloginput.Value{}, e
			}
			set(&contact, field, value)
		}
	}
	for i, field := range fields {
		var value cataloginput.Value
		var e *Error
		switch {
		case target == vaultread.EligibilityTarget && i >= 2:
			optional := unknownFacts || i != 3 && i != 4
			if payload[field] == nil && optional {
				value.Kind = "null"
			} else if b, ok := payload[field].(bool); ok {
				value = cataloginput.Value{Kind: "bool", Boolean: b}
			} else {
				suffix := "boolean"
				if optional {
					suffix = "boolean or null"
				}
				e = invalid(field + " must be a " + suffix)
			}
		case field == "role_targets":
			value, e = cleanRoles(payload[field])
		default:
			optional := target == vaultread.ProfileTarget && field != "name" &&
				field != "target_location" && field != "work_authorization" && field != "major"
			if unknownFacts {
				optional = target != vaultread.ProfileTarget || field != "name"
			}
			value, e = cleanText(payload[field], field, optional, 500)
		}
		if e != nil {
			return cataloginput.Value{}, e
		}
		set(&out, field, value)
	}
	if target == vaultread.ProfileTarget {
		set(&out, "contact", contact)
	}
	return out, nil
}

func merge(target vaultread.EditTarget, original, normalized cataloginput.Value) (cataloginput.Value, *Error) {
	if target == vaultread.ProfileTarget {
		contact, present := get(original, "contact")
		if !present {
			contact.Kind = "mapping"
		}
		if contact.Kind != "mapping" {
			return cataloginput.Value{}, invalid("career_vault/profile.yaml.contact: expected mapping")
		}
		submitted, _ := get(normalized, "contact")
		for _, entry := range submitted.Entries {
			if entry.Value.Text == "" {
				remove(&contact, entry.Key.Text)
			} else {
				set(&contact, entry.Key.Text, entry.Value)
			}
		}
		set(&normalized, "contact", contact)
	}
	for _, entry := range normalized.Entries {
		if target == vaultread.EligibilityTarget && entry.Value.Kind == "null" {
			remove(&original, entry.Key.Text)
		} else {
			set(&original, entry.Key.Text, entry.Value)
		}
	}
	return original, nil
}
