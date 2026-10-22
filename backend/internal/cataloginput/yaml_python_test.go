package cataloginput

import (
	"encoding/base64"
)

func yamlTestProjection(v Value) map[string]any {
	out := map[string]any{"kind": v.Kind}
	switch v.Kind {
	case "bool":
		out["value"] = v.Boolean
	case "string", "int", "float", "date", "datetime":
		out["value"] = v.Text
	case "bytes":
		out["value"] = base64.StdEncoding.EncodeToString(v.Bytes)
	case "sequence":
		items := make([]any, 0, len(v.Items))
		for _, item := range v.Items {
			items = append(items, yamlTestProjection(item))
		}
		out["value"] = items
	case "mapping":
		entries := make([]any, 0, len(v.Entries))
		for _, entry := range v.Entries {
			entries = append(entries, []any{yamlTestProjection(entry.Key), yamlTestProjection(entry.Value)})
		}
		out["value"] = entries
	}
	return out
}
