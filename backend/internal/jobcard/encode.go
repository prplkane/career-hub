package jobcard

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"unicode/utf8"

	"careerhub.local/backend/internal/cataloginput"
	"go.yaml.in/yaml/v4"
)

var errCardEncoding = errors.New("normalized Job Card YAML could not be encoded")

// EncodeCard emits conventional, Python-safe YAML for a validated card value.
// It is not a write or a byte-for-byte recreation of PyYAML safe_dump. The
// caller must validate the transformed value against the retained JD and use
// the actual serialized bytes as a revision-checked replacement payload.
func EncodeCard(ctx context.Context, card cataloginput.Value) ([]byte, *Error) {
	if ctx == nil || ctx.Err() != nil || card.Kind != "mapping" {
		return nil, &Error{Code: "JOB_CARD_ENCODE_FAILED"}
	}
	encoder := cardEncoder{ctx: ctx}
	node, ok := encoder.node(card, 0)
	if !ok {
		return nil, &Error{Code: "JOB_CARD_ENCODE_FAILED"}
	}
	output := &boundedCardOutput{}
	yamlEncoder := yaml.NewEncoder(output)
	yamlEncoder.SetIndent(2)
	if yamlEncoder.Encode(node) != nil {
		_ = yamlEncoder.Close()
		return nil, &Error{Code: "JOB_CARD_ENCODE_FAILED"}
	}
	if yamlEncoder.Close() != nil || ctx.Err() != nil {
		return nil, &Error{Code: "JOB_CARD_ENCODE_FAILED"}
	}
	raw := output.buffer.Bytes()
	if len(raw) > cataloginput.YAMLMaxInputBytes {
		return nil, &Error{Code: "JOB_CARD_ENCODE_FAILED"}
	}
	roundtrip, parseErr := cataloginput.ParseYAML(raw)
	if parseErr != nil || !sameCardValue(card, roundtrip) {
		return nil, &Error{Code: "JOB_CARD_ENCODE_FAILED"}
	}
	return bytes.Clone(raw), nil
}

type boundedCardOutput struct{ buffer bytes.Buffer }

func (out *boundedCardOutput) Write(raw []byte) (int, error) {
	if len(raw) > cataloginput.YAMLMaxInputBytes-out.buffer.Len() {
		return 0, errCardEncoding
	}
	return out.buffer.Write(raw)
}

type cardEncoder struct {
	ctx   context.Context
	nodes int
}

func (e *cardEncoder) node(value cataloginput.Value, depth int) (*yaml.Node, bool) {
	e.nodes++
	if e.ctx.Err() != nil || depth > cataloginput.YAMLMaxDepth || e.nodes > cataloginput.YAMLMaxNodes {
		return nil, false
	}
	node := &yaml.Node{}
	switch value.Kind {
	case "null":
		node.Kind, node.Tag, node.Value = yaml.ScalarNode, "!!null", "null"
	case "string":
		if !utf8.ValidString(value.Text) {
			return nil, false
		}
		node.Kind, node.Tag, node.Value, node.Style = yaml.ScalarNode, "!!str", value.Text, yaml.DoubleQuotedStyle
	case "bool":
		node.Kind, node.Tag, node.Value = yaml.ScalarNode, "!!bool", strconv.FormatBool(value.Boolean)
	case "int":
		node.Kind, node.Tag, node.Value = yaml.ScalarNode, "!!int", value.Text
	case "float":
		node.Kind, node.Tag, node.Value = yaml.ScalarNode, "!!float", value.Text
	case "date", "datetime":
		node.Kind, node.Tag, node.Value = yaml.ScalarNode, "!!timestamp", value.Text
	case "bytes":
		node.Kind, node.Tag, node.Value = yaml.ScalarNode, "!!binary", base64.StdEncoding.EncodeToString(value.Bytes)
	case "sequence":
		node.Kind, node.Tag = yaml.SequenceNode, "!!seq"
		for _, item := range value.Items {
			child, ok := e.node(item, depth+1)
			if !ok {
				return nil, false
			}
			node.Content = append(node.Content, child)
		}
	case "mapping":
		node.Kind, node.Tag = yaml.MappingNode, "!!map"
		for _, entry := range value.Entries {
			key, ok := e.node(entry.Key, depth+1)
			if !ok {
				return nil, false
			}
			child, ok := e.node(entry.Value, depth+1)
			if !ok {
				return nil, false
			}
			node.Content = append(node.Content, key, child)
		}
	default:
		return nil, false
	}
	return node, true
}

func sameCardValue(left, right cataloginput.Value) bool {
	if left.Kind != right.Kind || left.Text != right.Text || left.Boolean != right.Boolean ||
		!bytes.Equal(left.Bytes, right.Bytes) || len(left.Items) != len(right.Items) || len(left.Entries) != len(right.Entries) {
		return false
	}
	for i := range left.Items {
		if !sameCardValue(left.Items[i], right.Items[i]) {
			return false
		}
	}
	for i := range left.Entries {
		if !sameCardValue(left.Entries[i].Key, right.Entries[i].Key) || !sameCardValue(left.Entries[i].Value, right.Entries[i].Value) {
			return false
		}
	}
	return true
}
