package cataloginput

import (
	"encoding/hex"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v4"
)

type yamlPair struct{ key, value *yaml.Node }

func (w *yamlWalker) mapping(n *yaml.Node, depth int) (Value, *YAMLError) {
	pairs, e := w.flatten(n, depth, make(map[*yaml.Node]bool))
	if e != nil {
		return Value{}, e
	}
	out := Value{Kind: "mapping", Entries: make([]Entry, 0, len(pairs))}
	positions := make(map[string]int, len(pairs))
	for _, pair := range pairs {
		var key Value
		if yamlPythonTag(pair.key) == "!!value" {
			// SafeConstructor.flatten_mapping turns the special '=' key into str.
			copy := *pair.key
			copy.Tag = "!!str"
			copy.Style |= yaml.TaggedStyle
			key, e = w.value(&copy, depth+1)
		} else {
			key, e = w.value(pair.key, depth+1)
		}
		if e != nil {
			return Value{}, e
		}
		fingerprint, keyErr := yamlKey(key, pair.key)
		if keyErr != nil {
			return Value{}, keyErr
		}
		value, e := w.value(pair.value, depth+1)
		if e != nil {
			return Value{}, e
		}
		if i, ok := positions[fingerprint]; ok {
			out.Entries[i].Value = value
		} else {
			positions[fingerprint] = len(out.Entries)
			out.Entries = append(out.Entries, Entry{Key: key, Value: value})
		}
	}
	return out, nil
}

// Flatten before constructing/deduplicating. All merge pairs precede local
// pairs, merge-sequence order is reversed, and last assignment wins while the
// first equal key retains its insertion position and type (Python dict).
func (w *yamlWalker) flatten(n *yaml.Node, depth int, active map[*yaml.Node]bool) ([]yamlPair, *YAMLError) {
	if e := w.step(n, depth); e != nil {
		return nil, e
	}
	if active[n] {
		return nil, yamlFailure(n, "unsupported", "UnsupportedYAMLValue", "adapter", "YAML_RECURSIVE_MERGE")
	}
	active[n] = true
	defer delete(active, n)
	if n.Kind == yaml.AliasNode {
		w.aliases++
		if w.aliases > YAMLMaxAliasExpansions {
			return nil, yamlLimit(n, "YAML_ALIAS_EXPANSIONS")
		}
		return w.flatten(n.Alias, depth+1, active)
	}
	if n.Kind != yaml.MappingNode {
		return nil, yamlConstruction(n, "YAML_MERGE_SHAPE")
	}
	if yamlPythonTag(n) != "!!map" {
		// PyYAML flattening can discard a source mapping's own constructor
		// tag. Do not silently accept such tags, including unsafe Python tags.
		return nil, yamlFailure(n, "unsupported", "UnsupportedYAMLValue", "adapter", "YAML_TAGGED_MERGE")
	}
	var merged, local []yamlPair
	appendPairs := func(p []yamlPair) *YAMLError {
		if len(merged)+len(local)+len(p) > YAMLMaxNodes {
			return yamlLimit(n, "YAML_NODES")
		}
		merged = append(merged, p...)
		return nil
	}
	for i := 0; i < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if yamlPythonTag(k) != "!!merge" {
			local = append(local, yamlPair{k, v})
			continue
		}
		resolved := v
		// A merge alias can refer to a mapping or a sequence of mappings.
		if resolved.Kind == yaml.AliasNode {
			w.aliases++
			if w.aliases > YAMLMaxAliasExpansions {
				return nil, yamlLimit(v, "YAML_ALIAS_EXPANSIONS")
			}
			resolved = resolved.Alias
		}
		if resolved.Kind == yaml.SequenceNode {
			if yamlPythonTag(resolved) != "!!seq" {
				return nil, yamlFailure(resolved, "unsupported", "UnsupportedYAMLValue", "adapter", "YAML_TAGGED_MERGE")
			}
			for j := len(resolved.Content) - 1; j >= 0; j-- {
				p, e := w.flatten(resolved.Content[j], depth+1, active)
				if e != nil {
					return nil, e
				}
				if e = appendPairs(p); e != nil {
					return nil, e
				}
			}
		} else {
			p, e := w.flatten(resolved, depth+1, active)
			if e != nil {
				return nil, e
			}
			if e = appendPairs(p); e != nil {
				return nil, e
			}
		}
	}
	return append(merged, local...), nil
}

func yamlKey(v Value, n *yaml.Node) (string, *YAMLError) {
	switch v.Kind {
	case "null":
		return "null", nil
	case "string":
		return "str:" + v.Text, nil
	case "bytes":
		return "bytes:" + hex.EncodeToString(v.Bytes), nil
	case "bool":
		if v.Boolean {
			return "number:1", nil
		}
		return "number:0", nil
	case "int":
		return "number:" + v.Text, nil
	case "float":
		f, _ := strconv.ParseFloat(v.Text, 64)
		if math.IsNaN(f) {
			// Python key equality depends on object identity for NaN. Value has
			// no object identity; don't silently collapse or duplicate these keys.
			return "", yamlFailure(n, "unsupported", "UnsupportedYAMLValue", "adapter", "YAML_NAN_MAPPING_KEY")
		}
		if math.IsInf(f, 1) {
			return "number:+inf", nil
		}
		if math.IsInf(f, -1) {
			return "number:-inf", nil
		}
		return "number:" + new(big.Rat).SetFloat64(f).RatString(), nil
	case "date":
		return "date:" + v.Text, nil
	case "datetime":
		if strings.HasSuffix(v.Text, "Z") || strings.ContainsAny(v.Text[10:], "+-") {
			t, err := time.Parse(time.RFC3339Nano, v.Text)
			if err != nil {
				return "", yamlFailure(n, "unsupported", "UnsupportedYAMLValue", "adapter", "YAML_DATETIME_KEY")
			}
			return "aware:" + t.UTC().Format(time.RFC3339Nano), nil
		}
		return "naive:" + v.Text, nil
	default:
		return "", yamlConstruction(n, "YAML_UNHASHABLE_KEY")
	}
}
