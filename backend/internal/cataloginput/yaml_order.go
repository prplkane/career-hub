package cataloginput

import "go.yaml.in/yaml/v4"

// Successful tree construction does not need Python's mutable collection
// objects. On a construction failure, however, its deferred generators affect
// which exception wins. This failure-only preflight uses the same FIFO queue:
// constructing a collection returns a cached placeholder immediately, then its
// children are constructed when its queued body is drained. It is independently
// budgeted and never exposes partial values or invokes custom constructors.
func yamlFirstConstructionFailure(root *yaml.Node) *YAMLError {
	p := yamlConstructionOrder{walker: yamlWalker{active: make(map[*yaml.Node]bool)}, cache: make(map[*yaml.Node]Value)}
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return nil
		}
		root = root.Content[0]
	}
	if _, e := p.construct(root); e != nil {
		return e
	}
	for i := 0; i < len(p.queue); i++ {
		n := p.queue[i]
		if n.Kind == yaml.SequenceNode {
			for _, child := range n.Content {
				if _, e := p.construct(child); e != nil {
					return e
				}
			}
		} else {
			pairs, e := p.walker.flatten(n, 0, make(map[*yaml.Node]bool))
			if e != nil {
				return e
			}
			for _, pair := range pairs {
				keyNode := pair.key
				if yamlPythonTag(keyNode) == "!!value" {
					copy := *keyNode
					copy.Tag = "!!str"
					copy.Style |= yaml.TaggedStyle
					keyNode = &copy
				}
				key, e := p.construct(keyNode)
				if e != nil {
					return e
				}
				if _, e = yamlKey(key, keyNode); e != nil {
					return e
				}
				if _, e = p.construct(pair.value); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

type yamlConstructionOrder struct {
	walker yamlWalker
	cache  map[*yaml.Node]Value
	queue  []*yaml.Node
}

func (p *yamlConstructionOrder) construct(n *yaml.Node) (Value, *YAMLError) {
	if e := p.walker.step(n, 0); e != nil {
		return Value{}, e
	}
	if n.Kind == yaml.AliasNode {
		p.walker.aliases++
		if p.walker.aliases > YAMLMaxAliasExpansions {
			return Value{}, yamlLimit(n, "YAML_ALIAS_EXPANSIONS")
		}
		n = n.Alias
	}
	if v, ok := p.cache[n]; ok {
		return v, nil
	}
	tag := yamlPythonTag(n)
	var v Value
	var err *YAMLError
	switch tag {
	case "!!seq":
		if n.Kind != yaml.SequenceNode {
			return Value{}, yamlConstruction(n, "YAML_SEQUENCE_SHAPE")
		}
		v = Value{Kind: "sequence"}
		p.queue = append(p.queue, n)
	case "!!map":
		if n.Kind != yaml.MappingNode {
			return Value{}, yamlConstruction(n, "YAML_MAPPING_SHAPE")
		}
		v = Value{Kind: "mapping"}
		p.queue = append(p.queue, n)
	case "!!str", "!!null", "!!bool", "!!int", "!!float", "!!timestamp", "!!binary":
		if n.Kind != yaml.ScalarNode {
			if yamlScalarMappingValue(n) {
				return Value{}, yamlFailure(n, "unsupported", "UnsupportedYAMLValue", "adapter", "YAML_SCALAR_MAPPING_VALUE")
			}
			return Value{}, yamlConstruction(n, "YAML_SCALAR_SHAPE")
		}
		p.walker.textBytes += len(n.Value)
		if p.walker.textBytes > YAMLMaxExpandedTextBytes {
			return Value{}, yamlLimit(n, "YAML_EXPANDED_TEXT")
		}
		v, err = yamlScalar(n, tag)
	case "!!set", "!!omap", "!!pairs":
		return Value{}, yamlFailure(n, "unsupported", "UnsupportedYAMLValue", "adapter", "YAML_SAFE_COLLECTION_TYPE")
	default:
		return Value{}, yamlConstruction(n, "YAML_UNSUPPORTED_TAG")
	}
	if err != nil {
		return Value{}, err
	}
	p.cache[n] = v
	return v, nil
}
