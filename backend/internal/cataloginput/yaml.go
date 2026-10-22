package cataloginput

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"

	"go.yaml.in/yaml/v4"
)

// These are migration-adapter safety bounds, not limits imposed by PyYAML.
// A limit result is never a successful semantic comparison or a model error.
const (
	YAMLMaxInputBytes        = 2 << 20
	YAMLMaxDepth             = 64
	YAMLMaxNodes             = 100000
	YAMLMaxAliasExpansions   = 10000
	YAMLMaxExpandedTextBytes = 8 << 20
	YAMLMaxIntegerDigits     = 4300
)

// YAMLError contains only fixed codes and source coordinates; never source text,
// paths, field names, anchor names, or the dependency's raw error prose.
// Kind is invalid, limit, or unsupported. Class is a Python exception family
// only for adapter-owned construction failures. GoYAMLLoadError deliberately
// does not assert exception-class parity for native scanner/parser failures.
type YAMLError struct {
	Kind   string `json:"kind"`
	Class  string `json:"class"`
	Stage  string `json:"stage"`
	Code   string `json:"code"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

func (e *YAMLError) Error() string { return "catalog YAML input " + e.Kind + ": " + e.Code }

func yamlFailure(n *yaml.Node, kind, class, stage, code string) *YAMLError {
	e := &YAMLError{Kind: kind, Class: class, Stage: stage, Code: code}
	if n != nil {
		e.Line, e.Column = n.Line, n.Column
	}
	return e
}

func yamlLimit(n *yaml.Node, code string) *YAMLError {
	return yamlFailure(n, "limit", "YAMLResourceLimit", "adapter", code)
}

// ParseYAML decodes one UTF-8 document to the raw, presence-preserving Value
// boundary. It does not normalize a falsey root, validate catalog models, open
// files, resolve external resources, or run custom YAML constructors.
func ParseYAML(raw []byte) (Value, *YAMLError) {
	if len(raw) > YAMLMaxInputBytes {
		return Value{}, yamlLimit(nil, "YAML_INPUT_BYTES")
	}
	if !utf8.Valid(raw) {
		return Value{}, yamlFailure(nil, "invalid", "UnicodeDecodeError", "reader", "YAML_UTF8")
	}
	limits := &yamlParserLimits{}
	loader, err := yaml.NewLoader(bytes.NewReader(raw), yaml.WithSingleDocument(false), yaml.WithUniqueKeys(false), yaml.WithPlugin(limits))
	if err != nil {
		return Value{}, yamlLoadFailure(err, limits, raw)
	}
	var root yaml.Node
	if err = loader.Load(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return Value{Kind: "null"}, nil
		}
		return Value{}, yamlLoadFailure(err, limits, raw)
	}
	// Native single-document mode silently stops after the first document. A
	// second AST load (not an interface{} decode) must instead reach EOF.
	var extra yaml.Node
	if err = loader.Load(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return Value{}, yamlLoadFailure(err, limits, raw)
		}
		return Value{}, yamlFailure(&extra, "invalid", "ComposerError", "composer", "YAML_MULTIPLE_DOCUMENTS")
	}
	w := yamlWalker{active: make(map[*yaml.Node]bool)}
	if e := w.inspect(&root, 0, make(map[string]*yaml.Node)); e != nil {
		return Value{}, e
	}
	w.nodes = 0
	value, failure := w.value(&root, 0)
	if failure != nil && failure.Kind == "invalid" && failure.Stage == "constructor" {
		// PyYAML queues collection construction. A depth-first tree conversion
		// can find a different invalid scalar first; use a bounded failure-only
		// preflight to retain that deferred-construction ordering.
		if first := yamlFirstConstructionFailure(&root); first != nil {
			return Value{}, first
		}
		return Value{}, yamlFailure(nil, "unsupported", "UnsupportedYAMLValue", "adapter", "YAML_CONSTRUCTOR_ORDER")
	}
	return value, failure
}

type yamlParserLimits struct{ depthExceeded, aliasExceeded bool }

func (b *yamlParserLimits) CheckDepth(depth int, _ *yaml.DepthContext) error {
	if depth > YAMLMaxDepth {
		b.depthExceeded = true
		return errors.New("catalog YAML depth limit")
	}
	return nil
}
func (b *yamlParserLimits) CheckAlias(aliases, _ int) error {
	if aliases > YAMLMaxAliasExpansions {
		b.aliasExceeded = true
		return errors.New("catalog YAML alias limit")
	}
	return nil
}

func yamlLoadFailure(err error, b *yamlParserLimits, raw []byte) *YAMLError {
	if b.depthExceeded {
		return yamlLimit(nil, "YAML_DEPTH")
	}
	if b.aliasExceeded {
		return yamlLimit(nil, "YAML_ALIAS_EXPANSIONS")
	}
	e := yamlFailure(nil, "invalid", "GoYAMLLoadError", "loader", "YAML_PARSE")
	var le *yaml.LoadError
	if errors.As(err, &le) {
		// PyYAML can construct escaped lone surrogates as Python strings; Go
		// Unicode-scalar strings cannot represent them. Inspect only the exact
		// failing escape coordinate, never a regex over unrelated source text.
		if le.Stage == yaml.ScannerStage && le.Message == "found invalid Unicode character escape code" && yamlSurrogateAt(raw, le.Mark.Index) {
			e.Kind, e.Class, e.Code = "unsupported", "UnsupportedYAMLValue", "YAML_NONSCALAR_UNICODE"
		}
		switch string(le.Stage) {
		case "reader", "scanner", "parser", "composer", "resolver", "constructor":
			e.Stage = string(le.Stage)
		}
		e.Line, e.Column = le.Mark.Line, le.Mark.Column
	}
	return e
}

func yamlSurrogateAt(raw []byte, index int) bool {
	runes := []rune(string(raw))
	if index < 2 || index >= len(runes) || runes[index-2] != '\\' {
		return false
	}
	length := 4
	if runes[index-1] == 'U' {
		length = 8
	} else if runes[index-1] != 'u' {
		return false
	}
	if index+length > len(runes) {
		return false
	}
	v, err := strconv.ParseUint(string(runes[index:index+length]), 16, 32)
	return err == nil && v >= 0xD800 && v <= 0xDFFF
}

type yamlWalker struct {
	nodes, aliases, textBytes int
	active                    map[*yaml.Node]bool
}

func (w *yamlWalker) step(n *yaml.Node, depth int) *YAMLError {
	if depth > YAMLMaxDepth {
		return yamlLimit(n, "YAML_DEPTH")
	}
	w.nodes++
	if w.nodes > YAMLMaxNodes {
		return yamlLimit(n, "YAML_NODES")
	}
	return nil
}

// inspect follows only syntactic Content, never aliases. The input-byte and
// scanner-depth limits already bound parsing; this bounds subsequent traversal.
func (w *yamlWalker) inspect(n *yaml.Node, depth int, anchors map[string]*yaml.Node) *YAMLError {
	if e := w.step(n, depth); e != nil {
		return e
	}
	if n.Anchor != "" {
		if _, ok := anchors[n.Anchor]; ok {
			return yamlFailure(n, "invalid", "ComposerError", "composer", "YAML_DUPLICATE_ANCHOR")
		}
		anchors[n.Anchor] = n
	}
	if !utf8.ValidString(n.Value) {
		return yamlFailure(n, "unsupported", "UnsupportedYAMLValue", "adapter", "YAML_NONSCALAR_UNICODE")
	}
	if n.Kind == yaml.AliasNode {
		tag := yamlPythonTag(n.Alias)
		if tag == "!!value" || tag == "!!merge" {
			return yamlFailure(n, "unsupported", "UnsupportedYAMLValue", "adapter", "YAML_SPECIAL_KEY_ALIAS")
		}
	}
	for _, child := range n.Content {
		if e := w.inspect(child, depth+1, anchors); e != nil {
			return e
		}
	}
	return nil
}

func (w *yamlWalker) value(n *yaml.Node, depth int) (Value, *YAMLError) {
	if e := w.step(n, depth); e != nil {
		return Value{}, e
	}
	if w.active[n] {
		return Value{}, yamlFailure(n, "unsupported", "UnsupportedYAMLValue", "adapter", "YAML_RECURSIVE_VALUE")
	}
	w.active[n] = true
	defer delete(w.active, n)
	if n.Kind == yaml.AliasNode {
		w.aliases++
		if w.aliases > YAMLMaxAliasExpansions {
			return Value{}, yamlLimit(n, "YAML_ALIAS_EXPANSIONS")
		}
		return w.value(n.Alias, depth+1)
	}
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return Value{Kind: "null"}, nil
		}
		return w.value(n.Content[0], depth+1)
	}
	tag := yamlPythonTag(n)
	switch tag {
	case "!!set", "!!omap", "!!pairs":
		return Value{}, yamlFailure(n, "unsupported", "UnsupportedYAMLValue", "adapter", "YAML_SAFE_COLLECTION_TYPE")
	case "!!seq":
		if n.Kind != yaml.SequenceNode {
			return Value{}, yamlConstruction(n, "YAML_SEQUENCE_SHAPE")
		}
		out := Value{Kind: "sequence", Items: make([]Value, 0, len(n.Content))}
		for _, child := range n.Content {
			v, e := w.value(child, depth+1)
			if e != nil {
				return Value{}, e
			}
			out.Items = append(out.Items, v)
		}
		return out, nil
	case "!!map":
		if n.Kind != yaml.MappingNode {
			return Value{}, yamlConstruction(n, "YAML_MAPPING_SHAPE")
		}
		return w.mapping(n, depth)
	case "!!str", "!!null", "!!bool", "!!int", "!!float", "!!timestamp", "!!binary":
		if n.Kind != yaml.ScalarNode {
			if yamlScalarMappingValue(n) {
				return Value{}, yamlFailure(n, "unsupported", "UnsupportedYAMLValue", "adapter", "YAML_SCALAR_MAPPING_VALUE")
			}
			return Value{}, yamlConstruction(n, "YAML_SCALAR_SHAPE")
		}
		w.textBytes += len(n.Value)
		if w.textBytes > YAMLMaxExpandedTextBytes {
			return Value{}, yamlLimit(n, "YAML_EXPANDED_TEXT")
		}
		return yamlScalar(n, tag)
	default:
		return Value{}, yamlConstruction(n, "YAML_UNSUPPORTED_TAG")
	}
}

func yamlScalarMappingValue(n *yaml.Node) bool {
	if n.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i < len(n.Content); i += 2 {
		if yamlPythonTag(n.Content[i]) == "!!value" {
			return true
		}
	}
	return false
}

func yamlConstruction(n *yaml.Node, code string) *YAMLError {
	return yamlFailure(n, "invalid", "ConstructorError", "constructor", code)
}
