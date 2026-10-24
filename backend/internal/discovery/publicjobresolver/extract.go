package publicjobresolver

import (
	"encoding/json"
	stdhtml "html"
	"io"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
	"golang.org/x/net/html"
)

type Extraction struct {
	SourceText string `json:"source_text"`
	Title      string `json:"title"`
	Company    string `json:"company"`
	Location   string `json:"location"`
	PostingID  string `json:"posting_id"`
	Strategy   string `json:"strategy"`
}
type Expected struct{ Title, Company, Location string }

var excludedTags = wordSet("button form input noscript option script select style svg template textarea")
var blockTags = wordSet("address article aside blockquote br dd div dl dt fieldset figcaption figure footer h1 h2 h3 h4 h5 h6 header hr li main nav ol p pre section table tbody td th thead tr ul")
var jobSignals = []string{"about the role", "about this role", "job description", "qualifications", "requirements", "responsibilities", "what you will do", "what you'll do", "what we are looking for", "what we're looking for", "experience", "skills", "required experience", "preferred experience", "required skills", "preferred skills", "compensation", "salary", "benefits", "how to apply"}
var shellPhrases = []string{"search results", "jobs for you", "recommended jobs", "similar jobs", "people also viewed", "browse jobs", "filter results", "sort by", "job alerts"}
var semanticTokens = []string{"job-description", "jobdescription", "job-posting-description", "jobpostingdescription", "job-details", "jobdetails", "posting-description", "position-description"}
var jobLink = regexp.MustCompile(`(?i)(?:/jobs?(?:/|$)|/viewjob(?:/|$)|[?&](?:vjk|jk|currentjobid|jobpostingid|jobid)=)`)
var nonASCIIWord = regexp.MustCompile(`[^a-z0-9]+`)
var lineSpace = regexp.MustCompile(`[\t\f\v ]+`)
var rawTextTags = wordSet("script style xmp iframe noembed noframes plaintext title textarea")
var numericReference = regexp.MustCompile(`&#(?:[xX][0-9a-fA-F]+|[0-9]+);?`)

func withoutInvalidReferences(value string) string {
	return numericReference.ReplaceAllStringFunc(value, func(ref string) string {
		digits := strings.TrimSuffix(ref[2:], ";")
		base := 10
		if strings.HasPrefix(digits, "x") || strings.HasPrefix(digits, "X") {
			base = 16
			digits = digits[1:]
		}
		n, err := strconv.ParseUint(digits, base, 32)
		if err != nil {
			return ref
		}
		if n >= 1 && n <= 8 || n == 11 || n >= 14 && n <= 31 || n == 127 || n >= 0xfdd0 && n <= 0xfdef || n <= 0x10ffff && (n&0xffff == 0xfffe || n&0xffff == 0xffff) {
			return ""
		}
		return ref
	})
}
func unescapeText(value string) string {
	return stdhtml.UnescapeString(withoutInvalidReferences(value))
}

func wordSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

type semanticCandidate struct {
	score, depth, characters, links int
	pieces                          []string
	tooLarge                        bool
}

func (c *semanticCandidate) append(s string) {
	if s == "" || c.tooLarge {
		return
	}
	c.characters += utf8.RuneCountInString(s)
	if c.characters > MaxSourceChars+1 {
		c.tooLarge = true
		c.pieces = nil
		return
	}
	c.pieces = append(c.pieces, s)
}

type element struct {
	tag    string
	hidden bool
}
type pageParser struct {
	stack                            []element
	nodes, jsonCharacters, jsonDepth int
	active, completed                []*semanticCandidate
	scripts, jsonParts               []string
	fragment                         *semanticCandidate
}

func (p *pageParser) hidden() bool { return len(p.stack) > 0 && p.stack[len(p.stack)-1].hidden }
func (p *pageParser) append(s string) *Error {
	if p.hidden() {
		return nil
	}
	if p.fragment != nil {
		p.fragment.append(s)
		if p.fragment.tooLarge {
			return failure("CONTENT_UNSUPPORTED", "extract")
		}
		return nil
	}
	for _, c := range p.active {
		c.append(s)
	}
	return nil
}
func hiddenElement(tag string, a map[string]string, inherited bool) bool {
	_, hidden := a["hidden"]
	style := strings.Join(strings.FieldsFunc(textcompat.Fold(a["style"]), textcompat.IsSpace), "")
	return inherited || excludedTags[tag] || hidden || textcompat.Fold(a["aria-hidden"]) == "true" || strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden") || strings.Contains(style, "visibility:collapse")
}
func semanticSelector(a map[string]string) int {
	for _, v := range strings.FieldsFunc(a["itemprop"], textcompat.IsSpace) {
		if textcompat.Fold(v) == "description" {
			return 30
		}
	}
	var values []string
	for _, name := range []string{"id", "class", "data-testid", "data-test", "data-hook", "data-automation-id", "aria-label"} {
		values = append(values, a[name])
	}
	normalized := strings.Trim(nonASCIIWord.ReplaceAllString(textcompat.Fold(strings.Join(values, " ")), "-"), "-")
	compact := strings.ReplaceAll(normalized, "-", "")
	for _, v := range semanticTokens {
		if strings.Contains(normalized, v) {
			return 28
		}
	}
	for _, v := range semanticTokens {
		if strings.Contains(compact, strings.ReplaceAll(v, "-", "")) {
			return 26
		}
	}
	return 0
}
func (p *pageParser) start(tag string, attrs []html.Attribute) *Error {
	p.nodes++
	if p.nodes > 50000 {
		return failure("CONTENT_UNSUPPORTED", "extract")
	}
	a := map[string]string{}
	for _, v := range attrs {
		a[textcompat.Fold(v.Key)] = v.Val
	}
	inherited := p.hidden()
	hidden := hiddenElement(tag, a, inherited)
	p.stack = append(p.stack, element{tag, hidden})
	depth := len(p.stack)
	media, _, _ := strings.Cut(a["type"], ";")
	if p.fragment == nil && tag == "script" && !inherited && textcompat.Fold(textcompat.Strip(media)) == "application/ld+json" {
		if len(p.scripts) >= 20 {
			return failure("CONTENT_UNSUPPORTED", "extract")
		}
		p.jsonDepth = depth
		p.jsonParts = nil
		return nil
	}
	if hidden {
		return nil
	}
	if blockTags[tag] {
		if e := p.append("\n"); e != nil {
			return e
		}
	}
	if p.fragment != nil {
		return nil
	}
	if tag == "a" && jobLink.MatchString(strings.NewReplacer("İ", "i", "ı", "i", "ſ", "s", "K", "k").Replace(a["href"])) {
		for _, c := range p.active {
			c.links++
		}
	}
	if score := semanticSelector(a); score > 0 {
		if len(p.active)+len(p.completed) >= 180 {
			return failure("CONTENT_UNSUPPORTED", "extract")
		}
		p.active = append(p.active, &semanticCandidate{score: score, depth: depth})
	}
	return nil
}
func (p *pageParser) end(tag string) *Error {
	i := len(p.stack) - 1
	for i >= 0 && p.stack[i].tag != tag {
		i--
	}
	if i < 0 {
		return nil
	}
	depth := i + 1
	if !p.stack[i].hidden && blockTags[tag] {
		if e := p.append("\n"); e != nil {
			return e
		}
	}
	if p.jsonDepth >= depth && tag == "script" && p.jsonDepth == depth {
		p.finishScript()
		p.jsonDepth = 0
		p.jsonParts = nil
	}
	remaining := p.active[:0]
	for _, c := range p.active {
		if c.depth >= depth {
			p.completed = append(p.completed, c)
		} else {
			remaining = append(remaining, c)
		}
	}
	p.active = remaining
	p.stack = p.stack[:i]
	return nil
}
func (p *pageParser) finishScript() {
	if raw := textcompat.Strip(strings.Join(p.jsonParts, "")); raw != "" {
		p.scripts = append(p.scripts, raw)
	}
}
func (p *pageParser) data(s string) *Error {
	if p.jsonDepth > 0 && len(p.stack) >= p.jsonDepth {
		p.jsonCharacters += utf8.RuneCountInString(s)
		if p.jsonCharacters > 1000000 {
			return failure("CONTENT_UNSUPPORTED", "extract")
		}
		p.jsonParts = append(p.jsonParts, s)
		return nil
	}
	return p.append(s + " ")
}
func parsePage(value string, fragment bool) (*pageParser, *Error) {
	p := &pageParser{}
	if fragment {
		p.fragment = &semanticCandidate{}
	}
	z := html.NewTokenizer(strings.NewReader(value))
	z.SetMaxBuf(len(value) + 1)
	offset := 0
	for {
		kind := z.Next()
		if kind == html.ErrorToken {
			if z.Err() != io.EOF {
				return nil, failure("CONTENT_UNSUPPORTED", "extract")
			}
			break
		}
		raw := string(z.Raw())
		offset += len(raw)
		token := z.Token()
		if (kind == html.StartTagToken || kind == html.SelfClosingTagToken) && strings.Contains(raw, "&#") {
			attrs := html.NewTokenizer(strings.NewReader(withoutInvalidReferences(raw)))
			attrs.Next()
			token = attrs.Token()
		}
		var e *Error
		switch kind {
		case html.StartTagToken, html.SelfClosingTagToken:
			tag := textcompat.Fold(token.Data)
			e = p.start(tag, token.Attr)
			// Match the pinned HTMLParser raw/RCDATA boundary rather than the
			// HTML5 script tokenizer's nested escaped-script state machine.
			z.NextIsNotRawText()
			if e == nil && kind == html.SelfClosingTagToken {
				e = p.end(tag)
				z.NextIsNotRawText()
			}
			if e == nil && kind == html.StartTagToken && rawTextTags[tag] {
				end := len(value)
				if tag != "plaintext" {
					closing := regexp.MustCompile(`(?i)</` + regexp.QuoteMeta(tag) + `(?:[\t\n\f\r />])`)
					if match := closing.FindStringIndex(value[offset:]); match != nil {
						end = offset + match[0]
					}
				}
				data := value[offset:end]
				if tag == "title" || tag == "textarea" {
					data = unescapeText(data)
				}
				if data != "" {
					e = p.data(data)
				}
				offset = end
				z = html.NewTokenizer(strings.NewReader(value[offset:]))
				z.SetMaxBuf(len(value) + 1)
			}
		case html.EndTagToken:
			e = p.end(textcompat.Fold(token.Data))
		case html.TextToken:
			// HTMLParser emits an independent data event for a literal '<'
			// that did not start a tag. Entity-decoded '<' stays in its event.
			for raw != "" && e == nil {
				at := strings.IndexByte(raw, '<')
				if at < 0 {
					e = p.data(unescapeText(raw))
					break
				}
				if at > 0 {
					e = p.data(unescapeText(raw[:at]))
				}
				if e == nil {
					e = p.data("<")
				}
				raw = raw[at+1:]
			}
		}
		if e != nil {
			return nil, e
		}
	}
	if p.jsonDepth > 0 {
		p.finishScript()
	}
	p.completed = append(p.completed, p.active...)
	p.active = nil
	return p, nil
}
func normalizeSource(value string) string {
	if strings.ContainsRune(value, 0) {
		return ""
	}
	value = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(value)
	value = strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && r < 32 {
			return ' '
		}
		return r
	}, value)
	var lines []string
	for _, line := range strings.Split(value, "\n") {
		line = textcompat.Strip(lineSpace.ReplaceAllString(line, " "))
		if line != "" {
			lines = append(lines, line)
		}
	}
	return textcompat.Strip(strings.Join(lines, "\n"))
}
func phraseCount(text string, phrases []string) int {
	n := 0
	text = textcompat.Fold(text)
	for _, p := range phrases {
		if strings.Contains(text, p) {
			n++
		}
	}
	return n
}
func usable(text string) bool {
	n := utf8.RuneCountInString(text)
	signals := phraseCount(text, jobSignals)
	if n < MinSourceChars || n > MaxSourceChars || signals < 1 {
		return false
	}
	if signals >= 2 {
		return true
	}
	folded := textcompat.Fold(text)
	for _, phrase := range []string{"sign in", "log in", "forgot password", "create account", "verify you are human", "captcha"} {
		start := 0
		for start < len(folded) {
			i := strings.Index(folded[start:], phrase)
			if i < 0 {
				break
			}
			i += start
			end := i + len(phrase)
			before, after := false, false
			if i > 0 {
				r, _ := utf8.DecodeLastRuneInString(folded[:i])
				before = textcompat.IsWord(r)
			}
			if end < len(folded) {
				r, _ := utf8.DecodeRuneInString(folded[end:])
				after = textcompat.IsWord(r)
			}
			if !before && !after {
				return false
			}
			start = end
		}
	}
	return true
}
func normalizedTitle(value string) string {
	return strings.TrimSpace(nonASCIIWord.ReplaceAllString(textcompat.Fold(value), " "))
}
func compact(value any, limit int) string {
	var s string
	switch v := value.(type) {
	case string:
		s = v
	case json.Number:
		if string(v) == "NaN" {
			s = "nan"
			break
		}
		if !strings.ContainsAny(string(v), ".eE") {
			n, ok := new(big.Int).SetString(string(v), 10)
			if !ok {
				return ""
			}
			s = n.String()
		} else {
			f, err := strconv.ParseFloat(string(v), 64)
			if err != nil && !math.IsInf(f, 0) {
				return ""
			}
			s = strconv.FormatFloat(f, 'g', -1, 64)
			if math.IsInf(f, 1) {
				s = "inf"
			} else if math.IsInf(f, -1) {
				s = "-inf"
			} else if f == 0 || math.Abs(f) >= 1e-4 && math.Abs(f) < 1e16 {
				s = strconv.FormatFloat(f, 'f', -1, 64)
				if !strings.Contains(s, ".") {
					s += ".0"
				}
			}
		}
	default:
		return ""
	}
	s = strings.Join(strings.FieldsFunc(s, textcompat.IsSpace), " ")
	if strings.ContainsRune(s, 0) || utf8.RuneCountInString(s) > limit {
		return ""
	}
	return s
}
func organization(value any) string {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			if s := organization(item); s != "" {
				return s
			}
		}
		return ""
	case map[string]any:
		return compact(v["name"], 500)
	default:
		return compact(value, 500)
	}
}
func location(posting map[string]any) string {
	values, ok := posting["jobLocation"].([]any)
	if !ok {
		values = []any{posting["jobLocation"]}
	}
	for _, v := range values {
		loc, ok := v.(map[string]any)
		if !ok {
			continue
		}
		address := loc
		if a, found := loc["address"]; found {
			address, ok = a.(map[string]any)
			if !ok {
				continue
			}
		}
		country := address["addressCountry"]
		if m, ok := country.(map[string]any); ok {
			country = m["name"]
		}
		var parts []string
		for _, s := range []string{compact(address["addressLocality"], 100), compact(address["addressRegion"], 100), compact(address["postalCode"], 40), compact(country, 100)} {
			if s != "" {
				parts = append(parts, s)
			}
		}
		if s := compact(strings.Join(parts, ", "), 500); s != "" {
			return s
		}
	}
	if textcompat.Fold(compact(posting["jobLocationType"], 100)) == "telecommute" {
		return "Remote"
	}
	return ""
}

func jobpostingObjects(value any) ([]map[string]any, *Error) {
	type item struct {
		value any
		depth int
	}
	queue := []item{{value, 0}}
	var result []map[string]any
	for i := 0; i < len(queue); i++ {
		current := queue[i]
		if i >= 20000 || current.depth > 32 {
			return nil, failure("CONTENT_UNSUPPORTED", "extract")
		}
		switch v := current.value.(type) {
		case []any:
			for _, child := range v {
				queue = append(queue, item{child, current.depth + 1})
			}
		case map[string]any:
			types, ok := v["@type"].([]any)
			if !ok {
				types = []any{v["@type"]}
			}
			for _, typ := range types {
				if s, ok := typ.(string); ok && textcompat.Fold(s) == "jobposting" {
					result = append(result, v)
					break
				}
			}
			// Candidate order cannot select among competing JSON-LD objects:
			// only a unique exact title match can disambiguate them.
			for _, child := range v {
				switch child.(type) {
				case map[string]any, []any:
					queue = append(queue, item{child, current.depth + 1})
				}
			}
		}
	}
	return result, nil
}
func jsonldExtraction(scripts []string, expected string) (*Extraction, *Error) {
	var candidates []Extraction
	seen := map[Extraction]bool{}
	for _, raw := range scripts {
		var parsed any
		compatible, nanToken := pythonJSONConstants(raw)
		decoder := json.NewDecoder(strings.NewReader(compatible))
		decoder.UseNumber()
		if decoder.Decode(&parsed) != nil {
			continue
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			continue
		}
		parsed = restoreNaN(parsed, nanToken)
		objects, e := jobpostingObjects(parsed)
		if e != nil {
			return nil, e
		}
		for _, posting := range objects {
			description, ok := posting["description"].(string)
			if !ok {
				continue
			}
			p, e := parsePage(description, true)
			if e != nil {
				return nil, e
			}
			text := normalizeSource(strings.Join(p.fragment.pieces, ""))
			if !usable(text) {
				continue
			}
			title := compact(posting["title"], 500)
			if title == "" {
				continue
			}
			identifier := posting["identifier"]
			if m, ok := identifier.(map[string]any); ok {
				identifier = m["value"]
			}
			c := Extraction{SourceText: text, Title: title, Company: organization(posting["hiringOrganization"]), Location: location(posting), PostingID: compact(identifier, 300), Strategy: "jobposting_jsonld"}
			if !seen[c] {
				seen[c] = true
				candidates = append(candidates, c)
			}
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	if len(candidates) == 1 {
		return &candidates[0], nil
	}
	key := normalizedTitle(expected)
	index := -1
	for i, c := range candidates {
		if key != "" && normalizedTitle(c.Title) == key {
			if index >= 0 {
				return nil, failure("CONTENT_AMBIGUOUS", "extract")
			}
			index = i
		}
	}
	if index >= 0 {
		return &candidates[index], nil
	}
	return nil, failure("CONTENT_AMBIGUOUS", "extract")
}

// The pinned json.loads accepts nonfinite constants. Translate tokens outside
// strings to JSON numbers, preserving invalid-key/grammar rejection. A unique
// number spelling records NaN without colliding with any original JSON token.
func pythonJSONConstants(raw string) (string, string) {
	nan := "1e99999"
	for strings.Contains(raw, nan) {
		nan += "9"
	}
	var b strings.Builder
	quoted, escaped := false, false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if quoted {
			b.WriteByte(c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
			b.WriteByte(c)
			continue
		}
		replaced := false
		for _, pair := range [][2]string{{"-Infinity", "-1e9999"}, {"Infinity", "1e9999"}, {"NaN", nan}} {
			end := i + len(pair[0])
			if strings.HasPrefix(raw[i:], pair[0]) && (i == 0 || strings.ContainsRune("[{,: \t\r\n", rune(raw[i-1]))) && (end == len(raw) || end < len(raw) && strings.ContainsRune(",]} \t\r\n", rune(raw[end]))) {
				b.WriteString(pair[1])
				i += len(pair[0]) - 1
				replaced = true
				break
			}
		}
		if !replaced {
			b.WriteByte(c)
		}
	}
	return b.String(), nan
}
func restoreNaN(value any, token string) any {
	switch v := value.(type) {
	case json.Number:
		if string(v) == token {
			return json.Number("NaN")
		}
	case []any:
		for i, x := range v {
			v[i] = restoreNaN(x, token)
		}
	case map[string]any:
		for k, x := range v {
			v[k] = restoreNaN(x, token)
		}
	}
	return value
}
func semanticExtraction(candidates []*semanticCandidate, expected Expected) (*Extraction, *Error) {
	type scored struct {
		text          string
		score, length int
	}
	evaluated := map[string]scored{}
	for _, c := range candidates {
		if c.tooLarge {
			continue
		}
		text := normalizeSource(strings.Join(c.pieces, ""))
		if !usable(text) {
			continue
		}
		shells := phraseCount(text, shellPhrases)
		if c.links >= 2 || shells >= 2 || shells > 0 && c.links > 0 {
			continue
		}
		title := false
		key := normalizedTitle(expected.Title)
		lines := strings.Split(text, "\n")
		for _, line := range lines[:min(60, len(lines))] {
			if key != "" && normalizedTitle(line) == key {
				title = true
				break
			}
		}
		n := utf8.RuneCountInString(text)
		lengthScore := 2
		if n >= 600 {
			lengthScore = 4
		} else if n >= 300 {
			lengthScore = 3
		}
		score := c.score + min(4, phraseCount(text, jobSignals))*2 + lengthScore - c.links*2 - shells*3
		if title {
			score += 3
		}
		if prior, ok := evaluated[text]; !ok || score > prior.score {
			evaluated[text] = scored{text, score, n}
		}
	}
	var ranked []scored
	for _, v := range evaluated {
		ranked = append(ranked, v)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].length < ranked[j].length
	})
	if len(ranked) == 0 {
		return nil, nil
	}
	if len(ranked) > 1 && ranked[0].score-ranked[1].score < 3 {
		return nil, failure("CONTENT_AMBIGUOUS", "extract")
	}
	return &Extraction{SourceText: ranked[0].text, Title: compact(expected.Title, 500), Company: compact(expected.Company, 500), Location: compact(expected.Location, 500), Strategy: "semantic_container"}, nil
}

func Extract(response *HTMLResponse, expected Expected) (*Extraction, *Error) {
	if response == nil || len(response.Body) > MaxResponseBytes || response.BytesRead != len(response.Body) || response.RedirectCount < 0 || response.RedirectCount > MaxRedirects {
		return nil, failure("CONTENT_UNSUPPORTED", "extract")
	}
	text, err := decodeHTML(response.Body, response.Charset)
	if err != nil {
		return nil, failure("CONTENT_UNSUPPORTED", "extract")
	}
	replacements := strings.Count(text, "\ufffd")
	if replacements > 10 && float64(replacements)/float64(max(1, utf8.RuneCountInString(text))) > 0.01 {
		return nil, failure("CONTENT_UNSUPPORTED", "extract")
	}
	p, e := parsePage(text, false)
	if e != nil {
		return nil, e
	}
	if result, e := jsonldExtraction(p.scripts, expected.Title); result != nil || e != nil {
		return result, e
	}
	if result, e := semanticExtraction(p.completed, expected); result != nil || e != nil {
		return result, e
	}
	return nil, failure("CONTENT_NOT_FOUND", "extract")
}
