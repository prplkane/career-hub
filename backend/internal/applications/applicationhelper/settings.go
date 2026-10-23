package applicationhelper

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/cataloginput"
)

func lookup(v cataloginput.Value, name string) cataloginput.Value {
	for _, entry := range v.Entries {
		if entry.Key.Kind == "string" && entry.Key.Text == name {
			return entry.Value
		}
	}
	return cataloginput.Value{}
}
func mapping(v cataloginput.Value, keys ...string) bool {
	if v.Kind != "mapping" || len(v.Entries) != len(keys) {
		return false
	}
	for _, e := range v.Entries {
		if e.Key.Kind != "string" || !slices.Contains(keys, e.Key.Text) {
			return false
		}
	}
	return true
}
func yamlDocument(raw []byte, class string) (cataloginput.Value, *Error) {
	if len(raw) > MaxInputBytes {
		return cataloginput.Value{}, failure("read", "APPLICATION_HELPER_INPUT_LIMIT")
	}
	v, e := cataloginput.ParseYAML(raw)
	if e != nil {
		return cataloginput.Value{}, invalid(class)
	}
	return v, nil
}
func normalizeFields(value any) ([]string, *Error) {
	list, ok := value.([]any)
	if !ok || len(list) > len(fields) {
		return nil, invalid("ApplicationHelperError")
	}
	selected := map[string]bool{}
	for _, item := range list {
		id, ok := item.(string)
		if !ok || len(id) > 128 || selected[id] {
			return nil, invalid("ApplicationHelperError")
		}
		selected[id] = true
	}
	result := []string{}
	for _, field := range fields {
		if selected[field.id] {
			result = append(result, field.id)
		}
	}
	if len(result) != len(list) {
		return nil, invalid("ApplicationHelperError")
	}
	return result, nil
}

// All emitted values here come from the closed field catalog. This deliberately
// preserves the legacy v1-to-v2 in-memory revision, without writing on a read.
func configurationBytes(enabled []string) []byte {
	out := "schema_version: 2\nenabled_fields:"
	if len(enabled) == 0 {
		return []byte(out + " []\n")
	}
	for _, id := range enabled {
		out += "\n- " + id
	}
	return []byte(out + "\n")
}
func configuration(raw []byte, present bool) ([]string, []byte, *Error) {
	if !present {
		return []string{}, configurationBytes(nil), nil
	}
	v, e := yamlDocument(raw, "ApplicationHelperError")
	if e != nil {
		return nil, nil, e
	}
	if !mapping(v, "schema_version", "enabled_fields") {
		return nil, nil, invalid("ApplicationHelperError")
	}
	version := lookup(v, "schema_version")
	if version.Kind != "int" || (version.Text != "1" && version.Text != "2") {
		return nil, nil, invalid("ApplicationHelperError")
	}
	list := lookup(v, "enabled_fields")
	if list.Kind != "sequence" {
		return nil, nil, invalid("ApplicationHelperError")
	}
	items := []any{}
	for _, v := range list.Items {
		if v.Kind != "string" {
			return nil, nil, invalid("ApplicationHelperError")
		}
		items = append(items, v.Text)
	}
	enabled, e := normalizeFields(items)
	if e != nil {
		return nil, nil, e
	}
	if version.Text == "1" {
		raw = configurationBytes(enabled)
	}
	return enabled, raw, nil
}
func configurationPayload(enabled []string, raw []byte) map[string]any {
	items := []any{}
	for _, field := range fields {
		items = append(items, map[string]any{"id": field.id, "label": field.label, "source": field.source, "value_kind": field.kind, "enabled": slices.Contains(enabled, field.id)})
	}
	return map[string]any{"contract_version": 2, "revision": revision(raw), "enabled_count": len(enabled), "fields": items}
}
func demographicAnswers(value map[string]any) (map[string]string, *Error) {
	if len(value) != len(demographics) {
		return nil, invalid("ApplicationDemographicsError")
	}
	result := map[string]string{}
	for _, f := range demographics {
		answer, ok := value[f.id].(string)
		if !ok || (answer != "" && !slices.Contains(f.options, answer)) {
			return nil, invalid("ApplicationDemographicsError")
		}
		result[f.id] = answer
	}
	return result, nil
}
func demographicsBytes(answers map[string]string) []byte {
	out := "schema_version: 1\ncountries:\n  US:\n"
	for _, f := range demographics {
		value := answers[f.id]
		if value == "" {
			value = "''"
		} else if value == "Yes" || value == "No" {
			value = "'" + value + "'"
		}
		out += "    " + f.id + ": " + value + "\n"
	}
	return []byte(out)
}
func demographicSettings(raw []byte, present bool) (map[string]string, []byte, *Error) {
	if !present {
		values := map[string]string{}
		for _, f := range demographics {
			values[f.id] = ""
		}
		return values, demographicsBytes(values), nil
	}
	v, e := yamlDocument(raw, "ApplicationDemographicsError")
	if e != nil {
		return nil, nil, e
	}
	if !mapping(v, "schema_version", "countries") || lookup(v, "schema_version").Kind != "int" || lookup(v, "schema_version").Text != "1" {
		return nil, nil, invalid("ApplicationDemographicsError")
	}
	countries := lookup(v, "countries")
	if !mapping(countries, "US") {
		return nil, nil, invalid("ApplicationDemographicsError")
	}
	us := lookup(countries, "US")
	if !mapping(us, "gender", "hispanic_latino", "veteran_status", "disability_status") {
		return nil, nil, invalid("ApplicationDemographicsError")
	}
	input := map[string]any{}
	for _, f := range demographics {
		v := lookup(us, f.id)
		if v.Kind != "string" {
			return nil, nil, invalid("ApplicationDemographicsError")
		}
		input[f.id] = v.Text
	}
	values, e := demographicAnswers(input)
	return values, raw, e
}
func demographicsPayload(values map[string]string, raw []byte) map[string]any {
	items := []any{}
	for _, f := range demographics {
		items = append(items, map[string]any{"id": f.id, "label": f.label, "value": values[f.id], "options": slices.Clone(f.options)})
	}
	return map[string]any{"contract_version": 1, "revision": revision(raw), "sections": []any{map[string]any{"id": "US", "label": "US", "fields": items}}}
}
func response(ctx context.Context, v any) (json.RawMessage, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, canceled()
	}
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > MaxResponseBytes {
		return nil, failure("response", "APPLICATION_HELPER_RESPONSE_LIMIT")
	}
	if ctx.Err() != nil {
		return nil, canceled()
	}
	return raw, nil
}
func plain(value any, limit int) (string, bool) {
	s, ok := value.(string)
	return s, ok && len(s) <= limit && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
