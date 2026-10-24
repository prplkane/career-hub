package catalogwrite

import (
	"bytes"
	"errors"
	"strconv"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/catalog"
	"careerhub.local/backend/internal/skills/cataloginput"
	"go.yaml.in/yaml/v4"
)

var errGroupEncoding = errors.New("custom group YAML could not be encoded")

// encodeGroups serializes already-validated custom models, never an effective
// catalog Snapshot. It preserves scalar values and input record order, writes
// id/name/archived in that order, and omits empty cascade_archived_skill_ids.
//
// The output is conventional LF YAML, not byte-for-byte PyYAML safe_dump output.
// In particular, Python's single-file write_text also translates LF to CRLF on
// Windows. Callers must hash the actual stored bytes for the mutation revision;
// neither normalized YAML nor the effective catalog revision is that token.
// All string values are quoted to preserve YAML 1.1-looking strings in PyYAML.
// Domain validation, path safety, persistence and coordination belong to callers.
func encodeGroups(groups []catalog.Group) ([]byte, error) {
	// Bound the AST before constructing it. The two reserved nodes are the
	// document and sequence nodes counted by the existing reader's node budget.
	nodes, scalarBytes := 2, 0
	addString := func(value string) bool {
		if !utf8.ValidString(value) || len(value) > cataloginput.YAMLMaxInputBytes-scalarBytes {
			return false
		}
		scalarBytes += len(value)
		return true
	}
	for _, group := range groups {
		if nodes > cataloginput.YAMLMaxNodes-7 || !addString(group.ID) || !addString(group.Name) {
			return nil, errGroupEncoding
		}
		nodes += 7 // mapping plus three key/value pairs
		if len(group.CascadeArchivedSkillIDs) != 0 {
			if len(group.CascadeArchivedSkillIDs) > cataloginput.YAMLMaxNodes-nodes-2 {
				return nil, errGroupEncoding
			}
			nodes += 2 + len(group.CascadeArchivedSkillIDs) // key, sequence, items
			for _, id := range group.CascadeArchivedSkillIDs {
				if !addString(id) {
					return nil, errGroupEncoding
				}
			}
		}
	}
	sequence := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, group := range groups {
		record := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
			groupYAMLKey("id"), groupYAMLString(group.ID),
			groupYAMLKey("name"), groupYAMLString(group.Name),
			groupYAMLKey("archived"), {Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(group.Archived)},
		}}
		if len(group.CascadeArchivedSkillIDs) != 0 {
			ids := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for _, id := range group.CascadeArchivedSkillIDs {
				ids.Content = append(ids.Content, groupYAMLString(id))
			}
			record.Content = append(record.Content, groupYAMLKey("cascade_archived_skill_ids"), ids)
		}
		sequence.Content = append(sequence.Content, record)
	}
	output := &groupYAMLOutput{}
	encoder := yaml.NewEncoder(output)
	encoder.SetIndent(2)
	if err := encoder.Encode(sequence); err != nil {
		_ = encoder.Close()
		return nil, errGroupEncoding
	}
	if err := encoder.Close(); err != nil {
		return nil, errGroupEncoding
	}
	return output.buffer.Bytes(), nil
}

func groupYAMLKey(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func groupYAMLString(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Style: yaml.DoubleQuotedStyle, Value: value}
}

// A named buffer prevents io.Copy from discovering bytes.Buffer.ReadFrom and
// bypassing Write's cap. No partial output is returned by encodeGroups on error.
type groupYAMLOutput struct{ buffer bytes.Buffer }

func (output *groupYAMLOutput) Write(p []byte) (int, error) {
	if len(p) > cataloginput.YAMLMaxInputBytes-output.buffer.Len() {
		return 0, errGroupEncoding
	}
	return output.buffer.Write(p)
}
