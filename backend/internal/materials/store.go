package materials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"

	"careerhub.local/backend/internal/cataloginput"
	"go.yaml.in/yaml/v4"
)

func storeJSON(raw []byte) (json.RawMessage, *Error) {
	v, e := cataloginput.ParseYAML(raw)
	if e != nil {
		if e.Kind == "limit" {
			return nil, failure("store", "MATERIAL_STORE_LIMIT")
		}
		if e.Kind == "unsupported" {
			return nil, failure("store", "MATERIAL_STORE_UNSUPPORTED")
		}
		return nil, failure("store", "MATERIAL_STORE_INVALID")
	}
	var convert func(cataloginput.Value) (any, *Error)
	convert = func(v cataloginput.Value) (any, *Error) {
		switch v.Kind {
		case "null":
			return nil, nil
		case "bool":
			return v.Boolean, nil
		case "string":
			return v.Text, nil
		case "int", "float":
			if json.Valid([]byte(v.Text)) {
				return json.Number(v.Text), nil
			}
		case "sequence":
			out := []any{}
			for _, x := range v.Items {
				item, e := convert(x)
				if e != nil {
					return nil, e
				}
				out = append(out, item)
			}
			return out, nil
		case "mapping":
			out := map[string]any{}
			for _, x := range v.Entries {
				if x.Key.Kind != "string" {
					return nil, failure("store", "MATERIAL_STORE_INVALID")
				}
				item, e := convert(x.Value)
				if e != nil {
					return nil, e
				}
				out[x.Key.Text] = item
			}
			return out, nil
		}
		return nil, failure("store", "MATERIAL_STORE_UNSUPPORTED")
	}
	x, err := convert(v)
	if err != nil {
		return nil, err
	}
	return JSON(x)
}

// StoreYAML writes conventional YAML with quoted string values. It preserves
// typed records, not PyYAML's line wrapping or Windows newline spelling.
func StoreYAML(ctx context.Context, value any) ([]byte, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("context", "MATERIAL_CANCELED")
	}
	switch value.(type) {
	case CoverStore, *CoverStore, ResumeStore, *ResumeStore:
	default:
		return nil, failure("store", "MATERIAL_STORE_UNSUPPORTED")
	}
	raw, e := JSON(value)
	if e != nil {
		return nil, e
	}
	v, e := decode(raw)
	if e != nil {
		return nil, e
	}
	nodes := 0
	var node func(any) (*yaml.Node, *Error)
	node = func(v any) (*yaml.Node, *Error) {
		nodes++
		if nodes > cataloginput.YAMLMaxNodes || ctx.Err() != nil {
			return nil, failure("store", "MATERIAL_STORE_LIMIT")
		}
		n := &yaml.Node{Kind: yaml.ScalarNode}
		switch x := v.(type) {
		case nil:
			n.Tag, n.Value = "!!null", "null"
		case bool:
			n.Tag, n.Value = "!!bool", strconv.FormatBool(x)
		case string:
			n.Tag, n.Style, n.Value = "!!str", yaml.DoubleQuotedStyle, x
		case json.Number:
			n.Tag, n.Value = "!!int", x.String()
		case []any:
			n.Kind, n.Tag = yaml.SequenceNode, "!!seq"
			for _, item := range x {
				child, e := node(item)
				if e != nil {
					return nil, e
				}
				n.Content = append(n.Content, child)
			}
		case map[string]any:
			n.Kind, n.Tag = yaml.MappingNode, "!!map"
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				key, e := node(k)
				if e != nil {
					return nil, e
				}
				child, e := node(x[k])
				if e != nil {
					return nil, e
				}
				n.Content = append(n.Content, key, child)
			}
		default:
			return nil, failure("store", "MATERIAL_STORE_UNSUPPORTED")
		}
		return n, nil
	}
	n, e := node(v)
	if e != nil {
		return nil, e
	}
	out := &storeOutput{}
	encoder := yaml.NewEncoder(out)
	encoder.SetIndent(2)
	if err := encoder.Encode(n); err != nil {
		_ = encoder.Close()
		return nil, failure("store", "MATERIAL_STORE_LIMIT")
	}
	if err := encoder.Close(); err != nil {
		return nil, failure("store", "MATERIAL_STORE_LIMIT")
	}
	return out.buffer.Bytes(), nil
}

type storeOutput struct{ buffer bytes.Buffer }

func (o *storeOutput) Write(raw []byte) (int, error) {
	if len(raw) > MaxInputBytes-o.buffer.Len() {
		return 0, errors.New("material store limit")
	}
	return o.buffer.Write(raw)
}
