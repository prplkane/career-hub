package applicationtexts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"

	"careerhub.local/backend/internal/skills/cataloginput"
	"go.yaml.in/yaml/v4"
)

const MaxStoreBytes = 2 << 20
const MaxItems = 1000

func problem(code string) *Error { return &Error{Code: "APPLICATION_TEXT_" + code} }
func field(v cataloginput.Value, name string) cataloginput.Value {
	for _, entry := range v.Entries {
		if entry.Key.Kind == "string" && entry.Key.Text == name {
			return entry.Value
		}
	}
	return cataloginput.Value{}
}
func stringIs(v cataloginput.Value, text string) bool { return v.Kind == "string" && v.Text == text }

// Equality follows Python's collection semantics, not YAML key ordering or
// integer/boolean spelling. The values are bounded, parsed, acyclic trees.
func valueEqual(a, b cataloginput.Value) bool {
	number := func(v cataloginput.Value) (*big.Rat, bool) {
		if v.Kind == "bool" {
			if v.Boolean {
				return big.NewRat(1, 1), true
			}
			return big.NewRat(0, 1), true
		}
		if v.Kind == "float" {
			f, err := strconv.ParseFloat(v.Text, 64)
			if err != nil {
				return nil, false
			}
			r := new(big.Rat).SetFloat64(f)
			return r, r != nil
		}
		if v.Kind != "int" {
			return nil, false
		}
		return new(big.Rat).SetString(v.Text)
	}
	if x, ok := number(a); ok {
		y, yes := number(b)
		return yes && x.Cmp(y) == 0
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case "mapping":
		if len(a.Entries) != len(b.Entries) {
			return false
		}
		for _, x := range a.Entries {
			found := false
			for _, y := range b.Entries {
				if valueEqual(x.Key, y.Key) {
					found = valueEqual(x.Value, y.Value)
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	case "sequence":
		if len(a.Items) != len(b.Items) {
			return false
		}
		for i := range a.Items {
			if !valueEqual(a.Items[i], b.Items[i]) {
				return false
			}
		}
		return true
	case "bytes":
		return bytes.Equal(a.Bytes, b.Bytes)
	case "float":
		return a.Text != "nan" && a.Text == b.Text
	case "null":
		return true
	default:
		return a.Text == b.Text && a.Boolean == b.Boolean
	}
}

func validateWorkbench(v cataloginput.Value, job, fingerprint string) *Error {
	return validateWorkbenchIdentity(v, job, cataloginput.Value{Kind: "string", Text: fingerprint})
}

func validateWorkbenchIdentity(v cataloginput.Value, job string, fingerprint cataloginput.Value) *Error {
	stored := field(v, "jd_fingerprint")
	if stored.Kind == "" {
		stored.Kind = "null"
	} // Python dict.get on a legacy missing key.
	if v.Kind != "mapping" || !valueEqual(field(v, "schema_version"), cataloginput.Value{Kind: "int", Text: "1"}) ||
		!stringIs(field(v, "job_id"), job) || !valueEqual(stored, fingerprint) {
		return problem("STORE_IDENTITY_INVALID")
	}
	items, observations := field(v, "items"), field(v, "observations")
	if items.Kind != "sequence" || observations.Kind != "sequence" || len(items.Items) > MaxItems {
		return problem("STORE_INVALID")
	}
	seen := map[string]bool{}
	for _, item := range items.Items {
		id, kind, state := field(item, "id"), field(item, "kind"), field(item, "state")
		if item.Kind != "mapping" || id.Kind != "string" || seen[id.Text] || kind.Kind != "string" || !kindAllowed(kind.Text) ||
			state.Kind != "string" || state.Text != "draft" && state.Text != "user_approved" && state.Text != "submitted" {
			return problem("STORE_INVALID")
		}
		seen[id.Text] = true
	}
	return nil
}

// ParseWorkbench validates an existing bounded store against an already
// validated Job Card. Nil fingerprint means the card's missing/null legacy
// fingerprint, not permission to ignore identity. It preserves YAML types/order
// and performs no read, normalization or write. The owner handles absence.
func ParseWorkbench(ctx context.Context, raw []byte, job string, fingerprint *string) (cataloginput.Value, *Error) {
	empty := cataloginput.Value{}
	if ctx == nil || ctx.Err() != nil {
		return empty, problem("CANCELED")
	}
	if len(raw) > MaxStoreBytes {
		return empty, problem("STORE_LIMIT")
	}
	store, e := cataloginput.ParseYAML(raw)
	if e != nil {
		return empty, problem("STORE_INVALID")
	}
	expected := cataloginput.Value{Kind: "null"}
	if fingerprint != nil {
		expected = cataloginput.Value{Kind: "string", Text: *fingerprint}
	}
	if e := validateWorkbenchIdentity(store, job, expected); e != nil {
		return empty, e
	}
	if ctx.Err() != nil {
		return empty, problem("CANCELED")
	}
	return store, nil
}

// updateWorkbench preserves every untouched typed value. Reformatting YAML is
// permitted only for a real update; a no-op returns no replacement bytes.
func updateWorkbench(ctx context.Context, raw []byte, present bool, job, fingerprint string, draft Draft) ([]byte, string, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, "", problem("CANCELED")
	}
	var store cataloginput.Value
	if present {
		var e *cataloginput.YAMLError
		store, e = cataloginput.ParseYAML(raw)
		if e != nil {
			return nil, "", problem("STORE_INVALID")
		}
	} else {
		initial, _ := json.Marshal(map[string]any{"schema_version": 1, "job_id": job, "jd_fingerprint": fingerprint, "items": []any{}, "observations": []any{}})
		store, _ = cataloginput.ParseYAML(initial)
	}
	if e := validateWorkbench(store, job, fingerprint); e != nil {
		return nil, "", e
	}
	encoded, e := json.Marshal(draft)
	if e != nil || len(encoded) > MaxStoreBytes {
		return nil, "", invalid()
	}
	item, pe := cataloginput.ParseYAML(encoded)
	if pe != nil {
		return nil, "", invalid()
	}
	result := "created"
	for i := range store.Entries {
		entry := &store.Entries[i]
		if !stringIs(entry.Key, "items") {
			continue
		}
		found := false
		for n, old := range entry.Value.Items {
			if !stringIs(field(old, "id"), draft.ID) {
				continue
			}
			if valueEqual(old, item) {
				return nil, "no_change_needed", nil
			}
			if !stringIs(field(old, "state"), "draft") {
				return nil, "", problem("HISTORY_IMMUTABLE")
			}
			entry.Value.Items[n] = item
			result, found = "updated", true
			break
		}
		if !found {
			if len(entry.Value.Items) >= MaxItems {
				return nil, "", problem("STORE_LIMIT")
			}
			entry.Value.Items = append(entry.Value.Items, item)
		}
	}
	output, err := encodeStore(ctx, store)
	return output, result, err
}

type storeBuffer struct{ bytes.Buffer }

func (b *storeBuffer) Write(p []byte) (int, error) {
	if len(p) > MaxStoreBytes-b.Len() {
		return 0, errors.New("application text store limit")
	}
	return b.Buffer.Write(p)
}
func encodeStore(ctx context.Context, value cataloginput.Value) ([]byte, *Error) {
	nodes := 0
	var node func(cataloginput.Value, int) (*yaml.Node, *Error)
	node = func(v cataloginput.Value, depth int) (*yaml.Node, *Error) {
		nodes++
		if ctx.Err() != nil || depth > cataloginput.YAMLMaxDepth || nodes > cataloginput.YAMLMaxNodes {
			return nil, problem("STORE_LIMIT")
		}
		n := &yaml.Node{Kind: yaml.ScalarNode, Value: v.Text}
		switch v.Kind {
		case "null":
			n.Tag, n.Value = "!!null", "null"
		case "bool":
			n.Tag, n.Value = "!!bool", strconv.FormatBool(v.Boolean)
		case "string":
			n.Tag, n.Style = "!!str", yaml.DoubleQuotedStyle
		case "int":
			n.Tag = "!!int"
		case "float":
			n.Tag = "!!float"
			switch v.Text {
			case "inf":
				n.Value = ".inf"
			case "-inf":
				n.Value = "-.inf"
			case "nan":
				n.Value = ".nan"
			}
		case "date", "datetime":
			n.Tag = "!!timestamp"
		case "bytes":
			n.Tag, n.Value = "!!binary", base64.StdEncoding.EncodeToString(v.Bytes)
		case "sequence":
			n.Kind, n.Tag = yaml.SequenceNode, "!!seq"
			for _, child := range v.Items {
				c, e := node(child, depth+1)
				if e != nil {
					return nil, e
				}
				n.Content = append(n.Content, c)
			}
		case "mapping":
			n.Kind, n.Tag = yaml.MappingNode, "!!map"
			for _, entry := range v.Entries {
				k, e := node(entry.Key, depth+1)
				if e != nil {
					return nil, e
				}
				v, e := node(entry.Value, depth+1)
				if e != nil {
					return nil, e
				}
				n.Content = append(n.Content, k, v)
			}
		default:
			return nil, problem("STORE_UNSUPPORTED")
		}
		return n, nil
	}
	n, e := node(value, 0)
	if e != nil {
		return nil, e
	}
	var out storeBuffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(n); err != nil {
		_ = encoder.Close()
		return nil, problem("STORE_LIMIT")
	}
	if encoder.Close() != nil {
		return nil, problem("STORE_LIMIT")
	}
	return out.Bytes(), nil
}
