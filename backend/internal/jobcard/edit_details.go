package jobcard

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/textcompat"
)

// EditDetails applies only the explicit normalized-card corrections supported
// by Python update_job_card_details. The result is an uncommitted value: its
// caller must validate it against the retained source, serialize it, and use a
// revision-checked owned replacement before exposing a write route.
func EditDetails(ctx context.Context, loaded *Loaded, updates map[string]any) (cataloginput.Value, *Error) {
	return editDetails(ctx, loaded, updates, 16384, 1000)
}

// A retained manual override can exceed the interactive request ceiling. The
// rescan caller uses the card/schema ceilings, while public edits retain their
// smaller body/list bounds. Both paths apply identical normalization rules.
func editDetails(ctx context.Context, loaded *Loaded, updates map[string]any, maxBytes, maxItems int) (cataloginput.Value, *Error) {
	if err := stopped(ctx); err != nil {
		return cataloginput.Value{}, err
	}
	raw, encodeErr := json.Marshal(updates)
	if loaded == nil || loaded.Report == nil || !loaded.Report.SchemaValid() || loaded.Card.Kind != "mapping" || len(updates) > 32 || encodeErr != nil || len(raw) > maxBytes || maxBytes < 0 || maxItems < 0 {
		return cataloginput.Value{}, &Error{Code: "JOB_DETAILS_INVALID"}
	}
	card := cloneValue(loaded.Card)
	keys := make([]string, 0, len(updates))
	for name := range updates {
		if !editableDetail(name) {
			return cataloginput.Value{}, &Error{Code: "JOB_DETAILS_INVALID"}
		}
		keys = append(keys, name)
	}
	slices.Sort(keys)
	for _, name := range keys {
		value := updates[name]
		switch {
		case stringDetail(name):
			text, ok := value.(string)
			if !ok {
				return cataloginput.Value{}, &Error{Code: "JOB_DETAILS_INVALID"}
			}
			setValue(&card, name, stringValue(textcompat.Strip(text)))
		case stringListDetail(name):
			items, ok := value.([]any)
			if !ok || len(items) > maxItems {
				return cataloginput.Value{}, &Error{Code: "JOB_DETAILS_INVALID"}
			}
			list := cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{}}
			seen := map[string]bool{}
			for _, item := range items {
				text, ok := item.(string)
				if !ok || textcompat.Strip(text) == "" {
					return cataloginput.Value{}, &Error{Code: "JOB_DETAILS_INVALID"}
				}
				text = textcompat.Strip(text)
				if !seen[text] {
					seen[text] = true
					list.Items = append(list.Items, stringValue(text))
				}
			}
			if name != "mentioned_technologies" {
				setValue(&card, name, list)
			}
		case structuredDetail(name):
			converted, ok := detailValueLimited(value, 0, maxItems)
			if !ok {
				return cataloginput.Value{}, &Error{Code: "JOB_DETAILS_INVALID"}
			}
			if name == "experience_requirements" || name == "visa_signals" {
				setValue(&card, name, converted)
			}
		}
	}
	if digest, found := lookup(card, "digest"); found && digest.Kind == "mapping" {
		fields := get(digest, "fields")
		if fields.Kind != "mapping" {
			fields = cataloginput.Value{Kind: "mapping"}
		}
		for _, name := range []string{"company", "title", "location", "remote_policy", "seniority"} {
			if _, touched := updates[name]; !touched {
				continue
			}
			value := get(card, name)
			field := get(fields, name)
			if field.Kind != "mapping" {
				field = cataloginput.Value{Kind: "mapping"}
			}
			state := "provided"
			if value.Text == "" {
				state = "not_stated"
			}
			setValue(&field, "value", value)
			setValue(&field, "state", stringValue(state))
			setValue(&field, "evidence", cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{}})
			setValue(&fields, name, field)
		}
		setValue(&digest, "fields", fields)
		for _, name := range []string{"mentioned_technologies", "eligibility_gates", "classified_requirements"} {
			value, touched := updates[name]
			if !touched {
				continue
			}
			if name == "mentioned_technologies" {
				items := value.([]any)
				list := cataloginput.Value{Kind: "sequence", Items: make([]cataloginput.Value, 0, len(items))}
				for _, item := range items {
					// Python retains the original values in this digest-only
					// field, even though its list validator normalizes elsewhere.
					list.Items = append(list.Items, stringValue(item.(string)))
				}
				setValue(&digest, name, list)
			} else {
				converted, _ := detailValueLimited(value, 0, maxItems)
				setValue(&digest, name, converted)
			}
		}
		unresolved := get(digest, "unresolved_fields")
		if unresolved.Kind == "sequence" {
			kept := make([]cataloginput.Value, 0, len(unresolved.Items))
			for _, item := range unresolved.Items {
				field := get(fields, item.Text)
				if item.Kind == "string" && (item.Text == "company" || item.Text == "title") && field.Kind == "mapping" && get(field, "state").Text != "not_stated" {
					continue
				}
				kept = append(kept, item)
			}
			unresolved.Items = kept
			setValue(&digest, "unresolved_fields", unresolved)
		}
		setValue(&card, "digest", digest)
	}
	overrides := map[string]bool{}
	if old := get(card, "manual_overrides"); old.Kind == "sequence" {
		for _, item := range old.Items {
			if item.Kind == "string" {
				overrides[item.Text] = true
			}
		}
	}
	for _, name := range keys {
		overrides[name] = true
	}
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	slices.Sort(names)
	list := cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{}}
	for _, name := range names {
		list.Items = append(list.Items, stringValue(name))
	}
	setValue(&card, "manual_overrides", list)
	if _, touched := updates["source_url"]; touched {
		setValue(&card, "posting_identity", stringValue(detailPostingIdentity(get(card, "source_url").Text, get(card, "posting_id").Text)))
	} else if _, touched := updates["posting_id"]; touched {
		setValue(&card, "posting_identity", stringValue(detailPostingIdentity(get(card, "source_url").Text, get(card, "posting_id").Text)))
	}
	if err := stopped(ctx); err != nil {
		return cataloginput.Value{}, err
	}
	return card, nil
}

func editableDetail(name string) bool {
	return stringDetail(name) || stringListDetail(name) || structuredDetail(name)
}
func stringDetail(name string) bool {
	return slices.Contains([]string{"company", "title", "location", "remote_policy", "seniority", "source_url", "posting_id"}, name)
}
func stringListDetail(name string) bool {
	return slices.Contains([]string{"required_skills", "required_platforms", "required_capabilities", "preferred_skills", "education_requirements", "mentioned_technologies"}, name)
}
func structuredDetail(name string) bool {
	return slices.Contains([]string{"experience_requirements", "visa_signals", "eligibility_gates", "classified_requirements"}, name)
}
func stringValue(text string) cataloginput.Value {
	return cataloginput.Value{Kind: "string", Text: text}
}
func setValue(mapping *cataloginput.Value, name string, value cataloginput.Value) {
	for index, entry := range mapping.Entries {
		if entry.Key.Kind == "string" && entry.Key.Text == name {
			mapping.Entries[index].Value = value
			return
		}
	}
	mapping.Entries = append(mapping.Entries, cataloginput.Entry{Key: stringValue(name), Value: value})
}
func cloneValue(value cataloginput.Value) cataloginput.Value {
	out := value
	out.Bytes = slices.Clone(value.Bytes)
	out.Items = make([]cataloginput.Value, len(value.Items))
	for i, item := range value.Items {
		out.Items[i] = cloneValue(item)
	}
	out.Entries = make([]cataloginput.Entry, len(value.Entries))
	for i, entry := range value.Entries {
		out.Entries[i] = cataloginput.Entry{Key: cloneValue(entry.Key), Value: cloneValue(entry.Value)}
	}
	return out
}
func detailValueLimited(value any, depth, maxItems int) (cataloginput.Value, bool) {
	if depth > 32 {
		return cataloginput.Value{}, false
	}
	switch v := value.(type) {
	case nil:
		return cataloginput.Value{Kind: "null"}, true
	case string:
		return stringValue(v), true
	case bool:
		return cataloginput.Value{Kind: "bool", Boolean: v}, true
	case json.Number:
		if strings.ContainsAny(string(v), ".eE") {
			return cataloginput.Value{Kind: "float", Text: string(v)}, true
		}
		return cataloginput.Value{Kind: "int", Text: string(v)}, true
	case []any:
		if len(v) > maxItems {
			return cataloginput.Value{}, false
		}
		out := cataloginput.Value{Kind: "sequence", Items: make([]cataloginput.Value, 0, len(v))}
		for _, item := range v {
			child, ok := detailValueLimited(item, depth+1, maxItems)
			if !ok {
				return cataloginput.Value{}, false
			}
			out.Items = append(out.Items, child)
		}
		return out, true
	case map[string]any:
		if len(v) > maxItems {
			return cataloginput.Value{}, false
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		out := cataloginput.Value{Kind: "mapping", Entries: make([]cataloginput.Entry, 0, len(v))}
		for _, key := range keys {
			child, ok := detailValueLimited(v[key], depth+1, maxItems)
			if !ok {
				return cataloginput.Value{}, false
			}
			out.Entries = append(out.Entries, cataloginput.Entry{Key: stringValue(key), Value: child})
		}
		return out, true
	}
	return cataloginput.Value{}, false
}
func detailPostingIdentity(sourceURL, postingID string) string {
	if id := textcompat.Normalize(postingID); id != "" {
		return "posting_id:" + id
	}
	parsed, err := url.Parse(textcompat.Strip(sourceURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if path == "" {
		path = "/"
	}
	return "url:" + textcompat.Fold(parsed.Scheme) + "://" + textcompat.Fold(parsed.Host) + path + func() string {
		if parsed.RawQuery == "" {
			return ""
		}
		return "?" + parsed.RawQuery
	}()
}
