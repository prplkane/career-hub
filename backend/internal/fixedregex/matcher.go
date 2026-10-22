// Package fixedregex implements only the reviewed static pattern subset used
// by the migrated JD services. Never compile patterns from user data.
package fixedregex

// This adapter supports fixed, reviewed source patterns. It is not
// a general Python regex engine and must never compile user-provided patterns.
// RE2 retains linear-time matching; position markers supply Unicode15 boundary
// assertions without post-filtering greedy matches (which loses backtracking).
import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

type markers [4]rune // index = previous-word*2 + next-word
var defaultMarkers = markers{0xe000, 0xe001, 0xe002, 0xe003}

var spaceClass, decimalClass = regexpPropertyClasses()

type compiledRule struct{ search, start, full *regexp.Regexp }
type Matcher struct {
	marks markers
	rules map[string]compiledRule
}

const MaxSourceBytes = 4 << 20

var ErrInput = errors.New("unsupported fixed-pattern input")
var ErrLimit = errors.New("fixed-pattern match limit exceeded")

// Definition is reviewed source code, never user data.
type Definition struct {
	Pattern    string
	IgnoreCase bool
}
type Rules struct {
	definitions map[string]Definition
	baseline    *Matcher
}

func MustCompile(definitions map[string]Definition) *Rules {
	if len(definitions) == 0 || len(definitions) > 256 {
		panic("invalid fixed rule count")
	}
	copied := make(map[string]Definition, len(definitions))
	total := 0
	for key, value := range definitions {
		total += len(key) + len(value.Pattern)
		if key == "" || len(key) > 16386 || len(value.Pattern) > 16384 || !utf8.ValidString(value.Pattern) || total > 131072 {
			panic("invalid fixed rule definition")
		}
		copied[key] = value
	}
	return &Rules{copied, compileMatcher(defaultMarkers, copied)}
}
func (r *Rules) ForSource(source string) (*Matcher, error) {
	if r == nil || r.baseline == nil || len(source) > MaxSourceBytes || !utf8.ValidString(source) {
		return nil, ErrInput
	}
	if !strings.ContainsAny(source, string(defaultMarkers[:])) {
		return r.baseline, nil
	}
	used := make(map[rune]bool)
	for _, point := range source {
		used[point] = true
	}
	for _, definition := range r.definitions {
		for _, point := range definition.Pattern {
			used[point] = true
		}
	}
	var selected markers
	count := 0
	for point := rune(0xe000); count < len(selected) && point <= utf8.MaxRune; point++ {
		if !used[point] {
			selected[count] = point
			count++
		}
	}
	if count != len(selected) {
		return nil, ErrInput
	}
	return compileMatcher(selected, r.definitions), nil
}

// Markers returns a copy for invariant checks.
func (m *Matcher) Markers() [4]rune { return [4]rune(m.marks) }

func characterClass(ranges ...rune) *syntax.Regexp {
	return &syntax.Regexp{Op: syntax.OpCharClass, Rune: ranges}
}
func literal(point rune) *syntax.Regexp {
	return &syntax.Regexp{Op: syntax.OpLiteral, Rune: []rune{point}}
}
func concat(parts ...*syntax.Regexp) *syntax.Regexp {
	return &syntax.Regexp{Op: syntax.OpConcat, Sub: parts}
}
func (m markers) optional() *syntax.Regexp {
	return &syntax.Regexp{Op: syntax.OpQuest, Sub: []*syntax.Regexp{characterClass(m[0], m[0], m[1], m[1], m[2], m[2], m[3], m[3])}}
}

func excludeMarkers(ranges []rune, marks markers) []rune {
	result := make([]rune, 0, len(ranges)+8)
	for i := 0; i < len(ranges); i += 2 {
		first, last := ranges[i], ranges[i+1]
		for _, marker := range marks {
			if marker < first || marker > last {
				continue
			}
			if first < marker {
				result = append(result, first, marker-1)
			}
			first = marker + 1
		}
		if first <= last {
			result = append(result, first, last)
		}
	}
	return result
}

func rangeIncludes(ranges []rune, point rune) bool {
	for i := 0; i < len(ranges); i += 2 {
		if ranges[i] <= point && point <= ranges[i+1] {
			return true
		}
	}
	return false
}

func instrument(node *syntax.Regexp, marks markers, ignoreCase bool) *syntax.Regexp {
	switch node.Op {
	case syntax.OpWordBoundary:
		return characterClass(marks[1], marks[1], marks[2], marks[2])
	case syntax.OpNoWordBoundary:
		return characterClass(marks[0], marks[0], marks[3], marks[3])
	case syntax.OpCapture:
		if node.Name == "__not_prev_word" {
			return characterClass(marks[0], marks[0], marks[1], marks[1])
		}
		if node.Name == "__not_next_word" {
			return characterClass(marks[0], marks[0], marks[2], marks[2])
		}
	case syntax.OpLiteral:
		parts := make([]*syntax.Regexp, 0, len(node.Rune)*2)
		for _, point := range node.Rune {
			atom := literal(point)
			if node.Flags&syntax.FoldCase != 0 {
				values := []rune{point}
				for next := unicode.SimpleFold(point); next != point; next = unicode.SimpleFold(next) {
					values = append(values, next)
				}
				if point == 'i' || point == 'I' {
					values = append(values, 'İ', 'ı')
				}
				sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
				ranges := make([]rune, 0, len(values)*2)
				for _, r := range values {
					ranges = append(ranges, r, r)
				}
				atom = characterClass(ranges...)
			}
			parts = append(parts, marks.optional(), atom)
		}
		return concat(parts...)
	case syntax.OpCharClass, syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		ranges := node.Rune
		if node.Op == syntax.OpAnyChar {
			ranges = []rune{0, utf8.MaxRune}
		}
		if node.Op == syntax.OpAnyCharNotNL {
			ranges = []rune{0, '\n' - 1, '\n' + 1, utf8.MaxRune}
		}
		if ignoreCase && (rangeIncludes(ranges, 'I') || rangeIncludes(ranges, 'i')) {
			// These are Python IGNORECASE's two additional ASCII-I equivalents.
			// No reviewed source pattern has a negated letter class.
			for _, point := range []rune{'İ', 'ı'} {
				if !rangeIncludes(ranges, point) {
					ranges = append(append([]rune(nil), ranges...), point, point)
				}
			}
			pairs := make([][2]rune, 0, len(ranges)/2)
			for i := 0; i < len(ranges); i += 2 {
				pairs = append(pairs, [2]rune{ranges[i], ranges[i+1]})
			}
			sort.Slice(pairs, func(i, j int) bool { return pairs[i][0] < pairs[j][0] })
			ranges = nil
			for _, pair := range pairs {
				ranges = append(ranges, pair[:]...)
			}
		}
		return concat(marks.optional(), characterClass(excludeMarkers(ranges, marks)...))
	case syntax.OpEndText:
		return concat(marks.optional(), &syntax.Regexp{Op: syntax.OpEndText})
	}
	for i, child := range node.Sub {
		node.Sub[i] = instrument(child, marks, ignoreCase)
	}
	return node
}

var pythonUnicodeEscape = regexp.MustCompile(`\\u([0-9a-fA-F]{4})`)

func regexpPropertyClasses() (string, string) {
	var spaces, digits strings.Builder
	spaces.WriteByte('[')
	digits.WriteByte('[')
	for point := rune(0); point <= utf8.MaxRune; point++ {
		if textcompat.IsSpace(point) {
			fmt.Fprintf(&spaces, `\x{%x}`, point)
		}
		if _, ok := textcompat.DecimalDigit(point); ok {
			fmt.Fprintf(&digits, `\x{%x}`, point)
		}
	}
	spaces.WriteByte(']')
	digits.WriteByte(']')
	return spaces.String(), digits.String()
}

// Expand reviewed \s/\d atoms both outside and inside character classes. A
// nested bracket replacement would silently corrupt patterns such as [\d,].
// Preserve escaped backslashes; this is not a general Python regex parser.
func expandProperties(pattern string) string {
	var out strings.Builder
	inClass := false
	for index := 0; index < len(pattern); index++ {
		point := pattern[index]
		if point == '\\' && index+1 < len(pattern) {
			next := pattern[index+1]
			property := ""
			if next == 's' {
				property = spaceClass
			} else if next == 'd' {
				property = decimalClass
			}
			if property != "" {
				if inClass {
					property = property[1 : len(property)-1]
				}
				out.WriteString(property)
			} else {
				out.WriteByte(point)
				out.WriteByte(next)
			}
			index++
			continue
		}
		if point == '[' {
			inClass = true
		} else if point == ']' {
			inClass = false
		}
		out.WriteByte(point)
	}
	return out.String()
}

func compileMatcher(marks markers, definitions map[string]Definition) *Matcher {
	result := &Matcher{marks: marks, rules: make(map[string]compiledRule, len(definitions))}
	for key, definition := range definitions {
		pattern := pythonUnicodeEscape.ReplaceAllStringFunc(definition.Pattern, func(match string) string {
			point, _ := strconv.ParseUint(match[2:], 16, 32)
			return string(rune(point))
		})
		pattern = strings.NewReplacer(`(?<!\w)`, `(?P<__not_prev_word>)`, `(?!\w)`, `(?P<__not_next_word>)`).Replace(expandProperties(pattern))
		flags := syntax.Perl
		if definition.IgnoreCase {
			flags |= syntax.FoldCase
		}
		parsed, err := syntax.Parse(pattern, flags)
		if err != nil {
			panic("invalid fixed analysis rule: " + key)
		}
		pattern = instrument(parsed, marks, definition.IgnoreCase).String()
		result.rules[key] = compiledRule{
			search: regexp.MustCompile(pattern), start: regexp.MustCompile(`\A(?:` + pattern + `)`),
			full: regexp.MustCompile(`\A(?:` + pattern + `)(?:` + marks.optional().String() + `)\z`),
		}
	}
	return result
}

type Text struct {
	engine          *Matcher
	source, encoded string
	positions       []int32
}
type Match struct {
	text       string
	indices    []int
	expression *regexp.Regexp
}

func (match Match) Start() int    { return match.indices[0] }
func (match Match) End() int      { return match.indices[1] }
func (match Match) Value() string { return match.text[match.Start():match.End()] }
func (match Match) Group(name string) string {
	i := match.expression.SubexpIndex(name) * 2
	if i < 0 || i+1 >= len(match.indices) || match.indices[i] < 0 {
		return ""
	}
	return match.text[match.indices[i]:match.indices[i+1]]
}

func (engine *Matcher) Text(value string) (Text, error) {
	if engine == nil || engine.rules == nil || len(value) > MaxSourceBytes || !utf8.ValidString(value) || strings.ContainsAny(value, string(engine.marks[:])) {
		return Text{}, ErrInput
	}
	var encoded strings.Builder
	markerBytes := utf8.RuneLen(engine.marks[3])
	capacity := len(value) + (utf8.RuneCountInString(value)+1)*markerBytes
	encoded.Grow(capacity)
	// All inputs are subsets of the validated 4 MiB source. int32 offsets keep
	// this temporary byte-position map bounded on both 32- and 64-bit targets.
	positions := make([]int32, 0, capacity+1)
	previousWord := false
	write := func(point rune, offset int) {
		length, _ := encoded.WriteRune(point)
		for range length {
			positions = append(positions, int32(offset))
		}
	}
	for offset, point := range value {
		index := 0
		if previousWord {
			index += 2
		}
		nextWord := textcompat.IsWord(point)
		if nextWord {
			index++
		}
		write(engine.marks[index], offset)
		write(point, offset)
		previousWord = nextWord
	}
	index := 0
	if previousWord {
		index = 2
	}
	write(engine.marks[index], len(value))
	positions = append(positions, int32(len(value)))
	return Text{engine, value, encoded.String(), positions}, nil
}

func (text Text) Match(key, mode string) (Match, bool) {
	rule := text.engine.rules[key]
	expression := rule.search
	if mode == "start" {
		expression = rule.start
	}
	if mode == "full" {
		expression = rule.full
	}
	indices := expression.FindStringSubmatchIndex(text.encoded)
	if indices == nil {
		return Match{}, false
	}
	for i, index := range indices {
		if index >= 0 {
			indices[i] = int(text.positions[index])
		}
	}
	return Match{text.source, indices, expression}, true
}
func (text Text) Has(key string) bool { _, ok := text.Match(key, ""); return ok }

// All is source-compatible; new bounded composition should use AllLimit.
func (text Text) All(key string) []Match {
	result, err := text.AllLimit(key, MaxSourceBytes+1)
	if err != nil {
		panic("fixed source match invariant")
	}
	return result
}
func (text Text) AllLimit(key string, limit int) ([]Match, error) {
	if limit < 0 || limit > MaxSourceBytes+1 {
		return nil, ErrInput
	}
	expression := text.engine.rules[key].search
	indices := expression.FindAllStringSubmatchIndex(text.encoded, limit+1)
	if len(indices) > limit {
		return nil, ErrLimit
	}
	result := make([]Match, 0, len(indices))
	for _, set := range indices {
		for i, index := range set {
			if index >= 0 {
				set[i] = int(text.positions[index])
			}
		}
		result = append(result, Match{text.source, set, expression})
	}
	return result, nil
}
func (text Text) Split(key string, firstOnly bool) []string {
	parts := make([]string, 0)
	previous := 0
	for _, match := range text.All(key) {
		parts = append(parts, text.source[previous:match.Start()])
		previous = match.End()
		if firstOnly {
			break
		}
	}
	return append(parts, text.source[previous:])
}

func (match Match) GroupAt(index int) (string, bool) {
	if index < 0 || index >= len(match.indices)/2 {
		return "", false
	}
	index *= 2
	if match.indices[index] < 0 {
		return "", false
	}
	return match.text[match.indices[index]:match.indices[index+1]], true
}
func (match Match) Indices() []int { return append([]int(nil), match.indices...) }
