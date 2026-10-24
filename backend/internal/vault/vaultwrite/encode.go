package vaultwrite

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/cataloginput"
	"go.yaml.in/yaml/v4"
)

// Serialize values, not JSON-decoded maps. Unknown fields, scalar mapping key
// types and Python dict order survive. Formatting/comments/anchors are not a
// byte contract of safe_dump; revisions always hash actual resulting bytes.
type valueEncoder struct {
	ctx                context.Context
	nodes, scalarBytes int
	problem            *Error
}

func (e *valueEncoder) node(v cataloginput.Value, depth int) *yaml.Node {
	if e.problem != nil {
		return nil
	}
	if e.ctx == nil || e.ctx.Err() != nil {
		e.problem = canceled()
		return nil
	}
	e.nodes++
	e.scalarBytes += len(v.Text) + len(v.Bytes)
	if depth > cataloginput.YAMLMaxDepth || e.nodes > cataloginput.YAMLMaxNodes ||
		e.scalarBytes > cataloginput.YAMLMaxExpandedTextBytes {
		e.problem = &Error{Stage: "encode", Code: "VAULT_EDITOR_ENCODING_LIMIT"}
		return nil
	}
	if !utf8.ValidString(v.Text) {
		e.problem = &Error{Stage: "encode", Code: "VAULT_EDITOR_UNICODE_UNSUPPORTED"}
		return nil
	}
	n := &yaml.Node{Kind: yaml.ScalarNode}
	switch v.Kind {
	case "string":
		n.Tag, n.Value, n.Style = "!!str", v.Text, yaml.DoubleQuotedStyle
	case "int", "float":
		scalar := v.Text
		if v.Kind == "float" {
			switch scalar {
			case "nan":
				scalar = ".nan"
			case "inf":
				scalar = ".inf"
			case "-inf":
				scalar = "-.inf"
			}
		}
		if scalar == v.Text && !json.Valid([]byte(v.Text)) {
			e.problem = &Error{Stage: "encode", Code: "VAULT_EDITOR_ENCODING_UNSUPPORTED"}
			return nil
		}
		// Explicit tags matter for PyYAML 1.1's exponent-only float resolver.
		n.Tag, n.Value, n.Style = "!!"+v.Kind, scalar, yaml.TaggedStyle
	case "date", "datetime":
		n.Tag, n.Value, n.Style = "!!timestamp", v.Text, yaml.TaggedStyle
	case "bytes":
		n.Tag, n.Value, n.Style = "!!binary", base64.StdEncoding.EncodeToString(v.Bytes), yaml.TaggedStyle
	case "bool":
		n.Tag, n.Value = "!!bool", strconv.FormatBool(v.Boolean)
	case "null":
		n.Tag, n.Value = "!!null", "null"
	case "sequence":
		n.Kind, n.Tag = yaml.SequenceNode, "!!seq"
		for _, item := range v.Items {
			child := e.node(item, depth+1)
			if e.problem != nil {
				return nil
			}
			n.Content = append(n.Content, child)
		}
	case "mapping":
		n.Kind, n.Tag = yaml.MappingNode, "!!map"
		for _, entry := range v.Entries {
			key, value := e.node(entry.Key, depth+1), e.node(entry.Value, depth+1)
			if e.problem != nil {
				return nil
			}
			n.Content = append(n.Content, key, value)
		}
	default:
		// Unknown internal value kinds must not be silently converted.
		e.problem = &Error{Stage: "encode", Code: "VAULT_EDITOR_ENCODING_UNSUPPORTED"}
		return nil
	}
	return n
}

type yamlOutput struct{ buffer bytes.Buffer }

var errYAMLLimit = errors.New("Vault YAML output limit")

func (out *yamlOutput) Write(p []byte) (int, error) {
	if len(p) > cataloginput.YAMLMaxInputBytes-out.buffer.Len() {
		return 0, errYAMLLimit
	}
	return out.buffer.Write(p)
}

func encode(ctx context.Context, value cataloginput.Value) ([]byte, *Error) {
	e := valueEncoder{ctx: ctx, nodes: 1} // include the document node
	node := e.node(value, 1)
	if e.problem != nil {
		return nil, e.problem
	}
	out := &yamlOutput{}
	writer := yaml.NewEncoder(out)
	writer.SetIndent(2)
	if err := writer.Encode(node); err != nil {
		_ = writer.Close()
		return nil, &Error{Stage: "encode", Code: "VAULT_EDITOR_ENCODING_LIMIT"}
	}
	if err := writer.Close(); err != nil {
		return nil, &Error{Stage: "encode", Code: "VAULT_EDITOR_ENCODING_FAILED"}
	}
	if ctx.Err() != nil {
		return nil, canceled()
	}
	raw := out.buffer.Bytes()
	parsed, parseErr := cataloginput.ParseYAML(raw)
	// Validation alone cannot detect lost unknown fields or scalar/key drift.
	if parseErr != nil || !equalValue(value, parsed) {
		return nil, &Error{Stage: "encode", Code: "VAULT_EDITOR_ENCODING_CHANGED_VALUE"}
	}
	return raw, nil
}

// Empty sequences/maps have identical source meaning whether their Go backing
// slice is nil or non-nil. All actual scalar types and ordering remain exact.
func equalValue(a, b cataloginput.Value) bool {
	if a.Kind != b.Kind || a.Text != b.Text || a.Boolean != b.Boolean ||
		!bytes.Equal(a.Bytes, b.Bytes) || len(a.Items) != len(b.Items) || len(a.Entries) != len(b.Entries) {
		return false
	}
	for i := range a.Items {
		if !equalValue(a.Items[i], b.Items[i]) {
			return false
		}
	}
	for i := range a.Entries {
		if !equalValue(a.Entries[i].Key, b.Entries[i].Key) ||
			!equalValue(a.Entries[i].Value, b.Entries[i].Value) {
			return false
		}
	}
	return true
}
