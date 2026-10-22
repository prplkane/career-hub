package assessmentcontext

import (
	"encoding/base64"

	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/runtimepaths"
)

type memoryReader struct {
	files  map[string][]byte
	reads  []string
	onRead func(string, int) ([]byte, bool, *runtimepaths.Error, bool)
}

func (reader *memoryReader) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	reader.reads = append(reader.reads, path)
	if reader.onRead != nil {
		raw, present, err, handled := reader.onRead(path, len(reader.reads))
		if handled {
			return raw, present, err
		}
	}
	raw, present := reader.files[path]
	if int64(len(raw)) > limit {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	return append([]byte(nil), raw...), present, nil
}

func typed(value cataloginput.Value) any {
	result := map[string]any{"kind": value.Kind}
	switch value.Kind {
	case "string", "int", "float", "date", "datetime":
		result["text"] = value.Text
	case "bytes":
		result["text"] = base64.StdEncoding.EncodeToString(value.Bytes)
	case "bool":
		result["value"] = value.Boolean
	case "sequence":
		items := make([]any, 0, len(value.Items))
		for _, item := range value.Items {
			items = append(items, typed(item))
		}
		result["items"] = items
	case "mapping":
		entries := make([]any, 0, len(value.Entries))
		for _, entry := range value.Entries {
			entries = append(entries, []any{typed(entry.Key), typed(entry.Value)})
		}
		result["entries"] = entries
	}
	return result
}
