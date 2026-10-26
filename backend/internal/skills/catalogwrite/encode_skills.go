package catalogwrite

import (
	"errors"
	"slices"
	"strconv"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/skills/cataloginput"
	"go.yaml.in/yaml/v4"
)

var errSkillEncoding = errors.New("custom skill YAML could not be encoded")

// encodeSkills preserves the presence of optional overlay fields, including
// explicit nulls. Materializing absent defaults or dropping explicit nulls can
// change inheritance from core during an unrelated archive. This intentionally
// fixes the source model_dump(exclude_none=True) serializer's presence loss.
func encodeSkills(terms []catalog.Term) ([]byte, error) {
	nodes, scalarBytes := 2, 0
	var problem error
	node := func(kind yaml.Kind, tag, value string) *yaml.Node {
		nodes++
		if nodes > cataloginput.YAMLMaxNodes || len(value) > cataloginput.YAMLMaxInputBytes-scalarBytes || !utf8.ValidString(value) {
			problem = errSkillEncoding
			return nil
		}
		scalarBytes += len(value)
		n := &yaml.Node{Kind: kind, Tag: tag, Value: value}
		if tag == "!!str" {
			n.Style = yaml.DoubleQuotedStyle
		}
		return n
	}
	sequence := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, term := range terms {
		record := node(yaml.MappingNode, "!!map", "")
		if problem != nil {
			return nil, problem
		}
		pair := func(key string, value *yaml.Node) {
			record.Content = append(record.Content, node(yaml.ScalarNode, "!!str", key), value)
		}
		text := func(value string) *yaml.Node { return node(yaml.ScalarNode, "!!str", value) }
		pair("id", text(term.ID))
		pair("name", text(term.Name))
		aliases := node(yaml.SequenceNode, "!!seq", "")
		if problem != nil {
			return nil, problem
		}
		for _, alias := range term.Aliases {
			aliases.Content = append(aliases.Content, text(alias))
			if problem != nil {
				return nil, problem
			}
		}
		// An absent empty default is not an explicit user field. Keep its
		// absence across unrelated edits, just like the other overlay fields.
		if len(term.Aliases) != 0 || slices.Contains(term.FieldsSet, "aliases") {
			pair("aliases", aliases)
		}
		pair("category", text(term.Category))
		nullable := func(key string, value *string) {
			if !slices.Contains(term.FieldsSet, key) {
				return
			}
			if value == nil {
				pair(key, node(yaml.ScalarNode, "!!null", "null"))
			} else {
				pair(key, text(*value))
			}
		}
		nullable("primary_group_id", term.PrimaryGroupID)
		nullable("parent", term.Parent)
		if slices.Contains(term.FieldsSet, "kind") {
			pair("kind", text(term.Kind))
		}
		nullable("primary_domain", term.PrimaryDomain)
		if slices.Contains(term.FieldsSet, "relations") {
			relations := node(yaml.SequenceNode, "!!seq", "")
			if problem != nil {
				return nil, problem
			}
			for _, relation := range term.Relations {
				entry := node(yaml.MappingNode, "!!map", "")
				if problem != nil {
					return nil, problem
				}
				entry.Content = []*yaml.Node{text("type"), text(relation.Type), text("target_id"), text(relation.TargetID)}
				relations.Content = append(relations.Content, entry)
				if problem != nil {
					return nil, problem
				}
			}
			pair("relations", relations)
		}
		if slices.Contains(term.FieldsSet, "archived") {
			pair("archived", node(yaml.ScalarNode, "!!bool", strconv.FormatBool(term.Archived)))
		}
		nullable("created_via", term.CreatedVia)
		nullable("created_at", term.CreatedAt)
		if problem != nil {
			return nil, problem
		}
		sequence.Content = append(sequence.Content, record)
	}
	out := &groupYAMLOutput{}
	encoder := yaml.NewEncoder(out)
	encoder.SetIndent(2)
	if err := encoder.Encode(sequence); err != nil {
		_ = encoder.Close()
		return nil, errSkillEncoding
	}
	if err := encoder.Close(); err != nil {
		return nil, errSkillEncoding
	}
	return out.buffer.Bytes(), nil
}
