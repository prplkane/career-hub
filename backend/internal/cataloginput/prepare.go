package cataloginput

import (
	"strconv"

	"careerhub.local/backend/internal/textcompat"
)

// PrepareCatalog preserves safe_load(...) or [] and optional legacy alias
// cleanup. It consumes an already bounded, post-YAML decoded Value tree, not raw
// YAML or an HTTP request. It does not validate records or mutate input memory.
// Unchanged subtrees are borrowed; callers must not mutate either tree during use.
func PrepareCatalog(value Value, legacyAliases bool) Value {
	if catalogFalsey(value) {
		return Value{Kind: "sequence", Items: []Value{}}
	}
	if !legacyAliases || value.Kind != "sequence" {
		return value
	}
	items := make([]Value, len(value.Items))
	for index, item := range value.Items {
		items[index] = prepareLegacyRecord(item)
	}
	return Value{Kind: "sequence", Items: items}
}

func catalogFalsey(value Value) bool {
	switch value.Kind {
	case "null":
		return true
	case "bool":
		return !value.Boolean
	case "string":
		return value.Text == ""
	case "bytes":
		return len(value.Bytes) == 0
	case "int":
		// The decoded value contract requires canonical decimal integers.
		return value.Text == "0"
	case "float":
		parsed, err := strconv.ParseFloat(value.Text, 64)
		return err == nil && parsed == 0
	case "sequence":
		return len(value.Items) == 0
	case "mapping":
		return len(value.Entries) == 0
	default:
		return false
	}
}

func prepareLegacyRecord(value Value) Value {
	if value.Kind != "mapping" {
		return value
	}
	name := Value{}
	aliasesIndex := -1
	for index, entry := range value.Entries {
		if entry.Key.Kind != "string" {
			continue
		}
		switch entry.Key.Text {
		case "kind", "primary_domain", "primary_group_id", "relations", "archived":
			return value
		case "name":
			name = entry.Value
		case "aliases":
			aliasesIndex = index
		}
	}
	if name.Kind != "string" || aliasesIndex < 0 || value.Entries[aliasesIndex].Value.Kind != "sequence" {
		return value
	}
	aliases := value.Entries[aliasesIndex].Value.Items
	for _, alias := range aliases {
		if alias.Kind != "string" {
			return value
		}
	}
	seen := map[string]bool{textcompat.Normalize(name.Text): true}
	clean := make([]Value, 0, len(aliases))
	for _, alias := range aliases {
		key := textcompat.Normalize(alias.Text)
		if key != "" && !seen[key] {
			seen[key] = true
			clean = append(clean, alias)
		}
	}
	if len(clean) == len(aliases) {
		return value
	}
	entries := make([]Entry, len(value.Entries))
	copy(entries, value.Entries)
	entries[aliasesIndex].Value = Value{Kind: "sequence", Items: clean}
	return Value{Kind: "mapping", Entries: entries}
}
