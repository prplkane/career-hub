package jobdigest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/applicationanalysis"
	"careerhub.local/backend/internal/fixedregex"
	"careerhub.local/backend/internal/textcompat"
)

var ErrInput = errors.New("invalid deterministic JD digest input")
var ErrLimit = errors.New("deterministic JD digest limit exceeded")
var rules = fixedregex.MustCompile(definitions)

type line struct {
	number                    int
	raw, text, clean, section string
}
type analysis struct {
	ctx                     context.Context
	input                   Input
	engine                  *fixedregex.Matcher
	err                     error
	work, cacheBytes, items int
	cache                   map[string]fixedregex.Text
	lines, sections         []line
	result                  *Result
}

func (a *analysis) check() bool {
	if a.err == nil {
		a.err = a.ctx.Err()
	}
	return a.err == nil
}
func key(pattern string, ignore bool) string {
	if ignore {
		return "i:" + pattern
	}
	return "c:" + pattern
}
func (a *analysis) encoded(value string) (fixedregex.Text, bool) {
	if !a.check() {
		return fixedregex.Text{}, false
	}
	a.work += len(value)
	if a.work > 128<<20 {
		a.err = ErrLimit
		return fixedregex.Text{}, false
	}
	if text, ok := a.cache[value]; ok {
		return text, true
	}
	a.cacheBytes += len(value)
	if a.cacheBytes > 8<<20 {
		a.err = ErrLimit
		return fixedregex.Text{}, false
	}
	text, err := a.engine.Text(value)
	if err != nil {
		a.err = ErrInput
		return fixedregex.Text{}, false
	}
	a.cache[value] = text
	return text, true
}
func (a *analysis) matchKey(value, rule, mode string) (fixedregex.Match, bool) {
	text, ok := a.encoded(value)
	if !ok {
		return fixedregex.Match{}, false
	}
	return text.Match(rule, mode)
}
func (a *analysis) match(value, pattern string, ignore bool, mode string) (fixedregex.Match, bool) {
	return a.matchKey(value, key(pattern, ignore), mode)
}
func (a *analysis) has(value, pattern string, ignore bool) bool {
	_, ok := a.match(value, pattern, ignore, "")
	return ok
}
func (a *analysis) named(value, name string) bool {
	_, ok := a.matchKey(value, ruleAliases[name], "")
	return ok
}
func (a *analysis) allKey(value, rule string) []fixedregex.Match {
	text, ok := a.encoded(value)
	if !ok {
		return nil
	}
	matches, err := text.AllLimit(rule, MaxItems-a.items)
	if err != nil {
		a.err = ErrLimit
		return nil
	}
	a.items += len(matches)
	return matches
}
func (a *analysis) item() bool {
	if !a.check() {
		return false
	}
	a.items++
	if a.items > MaxItems {
		a.err = ErrLimit
		return false
	}
	return true
}
func prefix(value string, count int) string {
	for offset := range value {
		if count == 0 {
			return value[:offset]
		}
		count--
	}
	return value
}
func evidence(number int, text, rule string) Evidence {
	return Evidence{number, prefix(text, 500), rule}
}
func field(value, state string, proof ...Evidence) Field {
	result := Field{value, state, []Evidence{}}
	result.Evidence = append(result.Evidence, proof...)
	return result
}
func in(set, value string) bool                { return slices.Contains(sets[set], value) }
func firstGroup(match fixedregex.Match) string { value, _ := match.GroupAt(1); return value }

var replacementRepair = strings.NewReplacer(" \ufffd ", " - ", "\ufffd", "'")
var punctuationRepair = strings.NewReplacer("\u2013", "-", "\u2014", "-", "\u2019", "'")
var mojibakeRepair = strings.NewReplacer("\u00e2\u20ac\u201c", "-", "\u00e2\u20ac\u201d", "-", "\u00e2\u20ac\u2122", "'", "\u00e2\u20ac\u00a2", "-", "\u00e2\u02c6\u2122", " ")

func analysisText(value string) string {
	return mojibakeRepair.Replace(punctuationRepair.Replace(replacementRepair.Replace(value)))
}
func (a *analysis) clean(value string) string {
	value = textcompat.Strip(analysisText(value))
	if match, ok := a.matchKey(value, ruleAliases["BULLET_PREFIX"], "start"); ok {
		value = value[match.End():]
	}
	return strings.Trim(value, "#* _\t")
}
func (a *analysis) heading(value string) string {
	return textcompat.Fold(strings.TrimRight(a.clean(value), ":"))
}

// prepareAnalysis validates input and prepares the shared source/registry view.
func prepareAnalysis(ctx context.Context, input Input) (*analysis, error) {
	if input.Registry == nil || input.Registry.Revision() == "" {
		return nil, ErrInput
	}
	return prepareSource(ctx, input)
}

func prepareSource(ctx context.Context, input Input) (*analysis, error) {
	if ctx == nil {
		return nil, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(input.Source) > MaxSourceBytes {
		return nil, ErrLimit
	}
	if !utf8.ValidString(input.Source) || !utf8.ValidString(input.Company) || !utf8.ValidString(input.Title) || !utf8.ValidString(input.SourceURL) || !utf8.ValidString(input.Profile.CurrentStatus) {
		return nil, ErrInput
	}
	if utf8.RuneCountInString(input.Source) > MaxSourceRunes || len(input.Company) > 4096 || len(input.Title) > 4096 || len(input.SourceURL) > 65536 || len(input.Profile.CurrentStatus) > 4096 {
		return nil, ErrLimit
	}
	for _, value := range []OptionalBool{input.Profile.CitizenshipBlocker, input.Profile.ClearanceBlocker, input.Profile.SponsorshipNow, input.Profile.SponsorshipFuture} {
		if value.Value != nil && !value.Present {
			return nil, ErrInput
		}
	}
	engine, err := rules.ForSource(input.Source + input.Company + input.Title + input.SourceURL + input.Profile.CurrentStatus)
	if err != nil {
		return nil, ErrInput
	}
	a := &analysis{ctx: ctx, input: input, engine: engine, cache: map[string]fixedregex.Text{}}
	rawLines := textcompat.SplitLines(input.Source)
	if len(rawLines) > MaxLines {
		return nil, ErrLimit
	}
	for index, raw := range rawLines {
		if !a.check() {
			return nil, a.err
		}
		text := textcompat.Strip(raw)
		if text == "" {
			continue
		}
		a.lines = append(a.lines, line{number: index + 1, raw: raw, text: text, clean: a.clean(text)})
	}
	return a, nil
}

// DigestCompensation is the existing exact-source compensation projection for
// older Job Cards without a stored compensation field. It does not analyze
// skills or require candidate facts/a registry just to display a salary range.
func DigestCompensation(ctx context.Context, source string) (*Compensation, error) {
	a, err := prepareSource(ctx, Input{Source: source})
	if err != nil {
		return nil, err
	}
	result := a.compensation()
	if !a.check() {
		return nil, a.err
	}
	if err := outputBound(ctx, reflect.ValueOf(result)); err != nil {
		return nil, err
	}
	return &result, nil
}

// Analyze builds every digest family without files, network or persistent state.
// No result is published until all stages, limits and final cancellation pass.
func Analyze(ctx context.Context, input Input) (*Result, error) {
	a, err := prepareAnalysis(ctx, input)
	if err != nil {
		return nil, err
	}
	result := &Result{Fields: map[string]Field{}, RequiredSkills: []string{}, RequiredPlatforms: []string{}, PreferredSkills: []string{}, MentionedTechnologies: []string{}, TechnologyGroups: []TechnologyGroup{}, Occurrences: []Occurrence{}, Responsibilities: []string{}, Experience: []Experience{}, Education: []string{}, Classified: []Classified{}, UnresolvedFields: []string{}, Provenance: Provenance{[]Technology{}, []Evidence{}, []Evidence{}, []Evidence{}}}
	a.result = result
	a.fields()
	a.sectionLines()
	if !a.check() {
		return nil, a.err
	}
	if err := a.technologies(); err != nil {
		return nil, err
	}
	a.requirements()
	a.visa()
	a.classifyAndCover()
	if !a.check() {
		return nil, a.err
	}
	for _, name := range []string{"company", "title"} {
		if result.Fields[name].State == "not_stated" {
			result.UnresolvedFields = append(result.UnresolvedFields, name)
		}
	}
	result.ContentFingerprint, err = applicationanalysis.SourceFingerprint(input.Source)
	if err != nil {
		return nil, err
	}
	application, err := applicationanalysis.Analyze(input.Source)
	if err != nil {
		return nil, err
	}
	result.Instructions, result.Contacts = application.Instructions, application.Contacts
	if !a.check() {
		return nil, a.err
	}
	if err := outputBound(ctx, reflect.ValueOf(result)); err != nil {
		return nil, err
	}
	if err := a.verifyReferences(); err != nil {
		return nil, err
	}
	return result, nil
}

func outputBound(ctx context.Context, value reflect.Value) error {
	bytes, nodes := 0, 0
	var visit func(reflect.Value, int) error
	visit = func(v reflect.Value, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		nodes++
		if nodes > 100000 || depth > 32 {
			return ErrLimit
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if !v.IsNil() {
				return visit(v.Elem(), depth+1)
			}
		case reflect.String:
			bytes += v.Len()
			if bytes > MaxOutputBytes {
				return ErrLimit
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if err := visit(v.Field(i), depth+1); err != nil {
					return err
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				if err := visit(v.Index(i), depth+1); err != nil {
					return err
				}
			}
		case reflect.Map:
			iterator := v.MapRange()
			for iterator.Next() {
				if err := visit(iterator.Key(), depth+1); err != nil {
					return err
				}
				if err := visit(iterator.Value(), depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(value, 0)
}
