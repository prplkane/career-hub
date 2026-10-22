package agentcontext

import (
	"encoding/json"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

// WorkbenchContext accepts an already identity-validated workbench decoded as
// JSON (UseNumber), not an arbitrary filesystem path. It returns a detached,
// bounded projection. Unsupported stored text types fail explicitly rather than
// pretending to recover dictionary insertion order from a Go map. Use
// WorkbenchContextJSON for JSON-compatible legacy non-string text fields.
func WorkbenchContext(store map[string]any, task *TaskRequest) (json.RawMessage, *Error) {
	return workbenchContext(store, task, nil, nil)
}

// selected receives indexes in the filtered mapping items only after successful
// projection. Packet encoding uses these to retain nested source-field order;
// matching and privacy decisions remain owned by this single implementation.
func workbenchContext(store map[string]any, task *TaskRequest, selected *[]int, readText func(int, string) (string, *Error)) (json.RawMessage, *Error) {
	if task != nil && !ValidPurpose(task.fields.Purpose) {
		return nil, failure("TASK_INVALID")
	}
	result := struct {
		Exists    bool             `json:"exists"`
		Items     []map[string]any `json:"items"`
		Total     int              `json:"total"`
		Truncated bool             `json:"truncated"`
		Related   bool             `json:"related_item_found"`
	}{Items: []map[string]any{}}
	if store == nil {
		raw, _ := json.Marshal(result)
		return raw, nil
	}
	rawItems, ok := store["items"].([]any)
	if !ok || len(rawItems) > 1000 {
		return nil, failure("WORKBENCH_UNSUPPORTED")
	}
	items := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		if item, ok := raw.(map[string]any); ok {
			items = append(items, item)
		}
	}
	related := -1
	if readText == nil {
		readText = func(index int, key string) (string, *Error) {
			v := items[index][key]
			if v == nil || v == false || v == "" {
				return "", nil
			}
			s, ok := v.(string)
			if !ok || !utf8.ValidString(s) || len(s) > 2<<20 {
				return "", failure("WORKBENCH_UNSUPPORTED")
			}
			return s, nil
		}
	}
	if task != nil && task.fields.Purpose == "application_text" {
		f := task.fields
		if f.Kind == nil || f.ExactPrompt == nil {
			return nil, failure("TASK_INVALID")
		}
		prompt := textcompat.Fold(textcompat.Strip(*f.ExactPrompt))
		for i := len(items) - 1; i >= 0; i-- {
			item := items[i]
			if f.WorkbenchID != nil && item["id"] != *f.WorkbenchID {
				continue
			}
			if item["kind"] != *f.Kind {
				if f.WorkbenchID != nil {
					return nil, failure("WORKBENCH_TASK_MISMATCH")
				}
				continue
			}
			s, err := readText(i, "exact_prompt")
			if err != nil {
				return nil, err
			}
			match := item["kind"] == *f.Kind && textcompat.Fold(textcompat.Strip(s)) == prompt
			if f.WorkbenchID != nil && !match {
				return nil, failure("WORKBENCH_TASK_MISMATCH")
			}
			if match {
				related = i
				break
			}
		}
		if f.WorkbenchID != nil && related < 0 {
			return nil, failure("WORKBENCH_ITEM_NOT_FOUND")
		}
	}
	start, end := 0, len(items)
	if related >= 0 {
		start, end = related, related+1
	} else if start = end - 3; start < 0 {
		start = 0
	}
	clip := func(s string, limit int) (string, bool) {
		r := []rune(s)
		if len(r) > limit {
			return string(r[:limit]), true
		}
		return s, false
	}
	for i := start; i < end; i++ {
		item := items[i]
		prompt, err := readText(i, "exact_prompt")
		if err != nil {
			return nil, err
		}
		response, err := readText(i, "response")
		if err != nil {
			return nil, err
		}
		clipped, truncated := clip(prompt, 1000)
		defaultList := func(key string) any {
			v, ok := item[key]
			if !ok {
				return []any{}
			}
			return v
		}
		out := map[string]any{"id": item["id"], "kind": item["kind"], "exact_prompt": clipped, "exact_prompt_truncated": truncated, "limits": item["limits"], "evidence_refs": defaultList("evidence_refs"), "manual_checks": defaultList("manual_checks"), "state": item["state"], "response_available": response != ""}
		if i == related {
			out["response"], out["response_truncated"] = clip(response, 6000)
		}
		result.Items = append(result.Items, out)
	}
	result.Exists, result.Total, result.Related = true, len(items), related >= 0
	result.Truncated = related < 0 && len(items) > len(result.Items)
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > 2<<20 {
		return nil, failure("WORKBENCH_UNSUPPORTED")
	}
	if selected != nil {
		*selected = nil
		for i := start; i < end; i++ {
			*selected = append(*selected, i)
		}
	}
	return raw, nil
}
