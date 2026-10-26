package applicationanalysis

// Compatibility wrappers retain the source-qualified service and share one
// reviewed fixed-pattern engine with the complete deterministic JD digest.
import "careerhub.local/backend/internal/fixedregex"

type matcher struct {
	shared *fixedregex.Matcher
	marks  [4]rune
}
type matchingText struct {
	shared fixedregex.Text
	engine *matcher
	source string
}
type ruleMatch struct{ shared fixedregex.Match }

var fixedRules = func() *fixedregex.Rules {
	definitions := make(map[string]fixedregex.Definition, len(ruleDefinitions))
	for key, value := range ruleDefinitions {
		definitions[key] = fixedregex.Definition{Pattern: value.pattern, IgnoreCase: value.ignoreCase}
	}
	return fixedregex.MustCompile(definitions)
}()

func newMatcher(source string) *matcher {
	shared, err := fixedRules.ForSource(source)
	if err != nil {
		panic("validated analysis source invariant")
	}
	return &matcher{shared, shared.Markers()}
}
func (engine *matcher) text(value string) matchingText {
	shared, err := engine.shared.Text(value)
	if err != nil {
		panic("validated analysis text invariant")
	}
	return matchingText{shared, engine, value}
}
func (text matchingText) match(key, mode string) (ruleMatch, bool) {
	found, ok := text.shared.Match(key, mode)
	return ruleMatch{found}, ok
}
func (text matchingText) has(key string) bool { return text.shared.Has(key) }
func (text matchingText) all(key string) []ruleMatch {
	found := text.shared.All(key)
	result := make([]ruleMatch, 0, len(found))
	for _, item := range found {
		result = append(result, ruleMatch{item})
	}
	return result
}
func (text matchingText) split(key string, firstOnly bool) []string {
	return text.shared.Split(key, firstOnly)
}
func (match ruleMatch) start() int               { return match.shared.Start() }
func (match ruleMatch) end() int                 { return match.shared.End() }
func (match ruleMatch) value() string            { return match.shared.Value() }
func (match ruleMatch) group(name string) string { return match.shared.Group(name) }
