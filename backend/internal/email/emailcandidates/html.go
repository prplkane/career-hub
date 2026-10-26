package emailcandidates

import (
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
	"golang.org/x/net/html"
)

// HTML extraction is deliberately offline and generic. Source-specific
// LinkedIn and Handshake cards need their own grouping rules before this can
// be connected to the mail synchronization path.
const (
	MaxHTMLLinks              = 500
	MaxHTMLRelationReferences = 50_000
	maxHTMLTextChars          = 500_000
)

var (
	ErrHTMLLinkLimit  = errors.New("EMAIL_LINK_LIMIT_EXCEEDED")
	ErrHTMLComplexity = errors.New("EMAIL_HTML_COMPLEXITY_LIMIT_EXCEEDED")
	ErrHTMLParse      = errors.New("EMAIL_PARSE_FAILED")
	ErrHTMLTextLimit  = errors.New("EMAIL_TEXT_LIMIT_EXCEEDED")
)

type HTMLResult struct {
	Candidates             []Candidate
	IgnoredLinks           int
	AnchorCount            int
	RelationReferenceCount int
	VisibleText            string
}

type htmlBlock struct {
	chunks    []string
	anchorIDs []int
}

type htmlAnchor struct {
	url                string
	blockIDs           []int
	labelChunks        []string
	descendantBlockIDs []int
}

type htmlContext struct {
	tag      string
	skipped  bool
	blockID  int
	anchorID int
}

type visibleHTML struct {
	blocks       []htmlBlock
	anchors      []htmlAnchor
	visible      strings.Builder
	stack        []htmlContext
	activeBlocks []int
	activeLinks  []int
	skipDepth    int
	linkBudget   int
	relationMax  int
	relations    int
}

var (
	ignoredContainerTags = set("canvas", "form", "head", "noscript", "object", "script", "select", "style", "svg", "template", "textarea")
	voidTags             = set("area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr")
	ignoredVoidTags      = set("area", "base", "embed", "img", "input", "link", "meta", "source")
	blockTags            = set("article", "blockquote", "dd", "div", "dl", "dt", "fieldset", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "li", "main", "p", "section", "table", "tbody", "td", "th", "tr")
	genericLinkLabels    = set("apply", "apply now", "careers", "click here", "continue", "jobs", "learn more", "open", "read more", "see details", "view", "view application", "view details", "view job", "view message", "view role", "website")
)

func (p *visibleHTML) reserve(count int) error {
	if count > p.relationMax-p.relations {
		return ErrHTMLComplexity
	}
	p.relations += count
	return nil
}

func (p *visibleHTML) appendToBlocks(value string) error {
	if err := p.reserve(len(p.activeBlocks)); err != nil {
		return err
	}
	for _, id := range p.activeBlocks {
		p.blocks[id].chunks = append(p.blocks[id].chunks, value)
	}
	return nil
}

func (p *visibleHTML) appendVisible(value string) error {
	p.visible.WriteString(value)
	if err := p.appendToBlocks(value); err != nil {
		return err
	}
	if err := p.reserve(len(p.activeLinks)); err != nil {
		return err
	}
	for _, id := range p.activeLinks {
		p.anchors[id].labelChunks = append(p.anchors[id].labelChunks, value)
	}
	return nil
}

func hiddenHTMLStyle(value string) bool {
	compact := strings.Join(strings.FieldsFunc(textcompat.Fold(value), textcompat.IsSpace), "")
	return strings.Contains(compact, "display:none") || strings.Contains(compact, "visibility:hidden")
}

func (p *visibleHTML) start(tag string, attrs []html.Attribute, forceVoid bool) error {
	attributes := make(map[string]string, len(attrs))
	for _, attribute := range attrs {
		attributes[textcompat.Fold(attribute.Key)] = attribute.Val
	}
	_, hiddenAttribute := attributes["hidden"]
	hidden := hiddenAttribute || textcompat.Fold(attributes["aria-hidden"]) == "true" || hiddenHTMLStyle(attributes["style"])
	skipped := p.skipDepth > 0 || hidden || ignoredContainerTags[tag] || ignoredVoidTags[tag]
	if tag == "br" && !skipped {
		if err := p.appendVisible("\n"); err != nil {
			return err
		}
	}
	context := htmlContext{tag: tag, skipped: skipped, blockID: -1, anchorID: -1}
	if !skipped && blockTags[tag] {
		if err := p.appendToBlocks("\n"); err != nil {
			return err
		}
		context.blockID = len(p.blocks)
		p.blocks = append(p.blocks, htmlBlock{})
		p.activeBlocks = append(p.activeBlocks, context.blockID)
		if err := p.reserve(len(p.activeLinks)); err != nil {
			return err
		}
		for _, id := range p.activeLinks {
			p.anchors[id].descendantBlockIDs = append(p.anchors[id].descendantBlockIDs, context.blockID)
		}
	}
	if !skipped && tag == "a" {
		if len(p.anchors) >= p.linkBudget {
			return ErrHTMLLinkLimit
		}
		context.anchorID = len(p.anchors)
		p.anchors = append(p.anchors, htmlAnchor{
			url:      textcompat.Strip(attributes["href"]),
			blockIDs: append([]int(nil), p.activeBlocks...),
		})
		p.activeLinks = append(p.activeLinks, context.anchorID)
		if err := p.reserve(len(p.activeBlocks)); err != nil {
			return err
		}
		for _, id := range p.activeBlocks {
			p.blocks[id].anchorIDs = append(p.blocks[id].anchorIDs, context.anchorID)
		}
	}
	if forceVoid || voidTags[tag] {
		if context.blockID >= 0 {
			p.activeBlocks = removeID(p.activeBlocks, context.blockID)
		}
		if context.anchorID >= 0 {
			p.activeLinks = removeID(p.activeLinks, context.anchorID)
		}
		return nil
	}
	if skipped {
		p.skipDepth++
	}
	p.stack = append(p.stack, context)
	return nil
}

func removeID(ids []int, target int) []int {
	for i, id := range ids {
		if id == target {
			return append(ids[:i], ids[i+1:]...)
		}
	}
	return ids
}

func (p *visibleHTML) end(tag string) error {
	match := -1
	for i := len(p.stack) - 1; i >= 0; i-- {
		if p.stack[i].tag == tag {
			match = i
			break
		}
	}
	if match < 0 {
		return nil
	}
	for i := len(p.stack) - 1; i >= match; i-- {
		context := p.stack[i]
		if context.anchorID >= 0 {
			p.activeLinks = removeID(p.activeLinks, context.anchorID)
		}
		if context.blockID >= 0 {
			p.activeBlocks = removeID(p.activeBlocks, context.blockID)
		}
		if context.skipped && p.skipDepth > 0 {
			p.skipDepth--
		}
	}
	p.stack = p.stack[:match]
	if p.skipDepth == 0 && blockTags[tag] {
		return p.appendToBlocks("\n")
	}
	return nil
}

func parseVisibleHTML(text string, linkBudget, relationBudget int) (*visibleHTML, error) {
	if utf8.RuneCountInString(text) > maxHTMLTextChars {
		return nil, ErrHTMLTextLimit
	}
	p := &visibleHTML{linkBudget: max(0, linkBudget), relationMax: max(0, relationBudget)}
	zer := html.NewTokenizer(strings.NewReader(text))
	zer.SetMaxBuf(len(text) + 1)
	for {
		typeOfToken := zer.Next()
		if typeOfToken == html.ErrorToken {
			if err := zer.Err(); err != nil && err != io.EOF {
				return nil, ErrHTMLParse
			}
			break
		}
		token := zer.Token()
		var err error
		switch typeOfToken {
		case html.StartTagToken, html.SelfClosingTagToken:
			err = p.start(token.Data, token.Attr, typeOfToken == html.SelfClosingTagToken)
		case html.EndTagToken:
			err = p.end(token.Data)
		case html.TextToken:
			if p.skipDepth == 0 {
				err = p.appendVisible(token.Data)
			}
		}
		if err != nil {
			return nil, err
		}
	}
	return p, nil
}

func plausibleTitleLabel(label string) bool {
	normalized := normalizeVisible(label)
	if genericLinkLabels[textcompat.Fold(normalized)] || ignoredLabels[textcompat.Fold(normalized)] {
		return false
	}
	if length := utf8.RuneCountInString(normalized); length < 2 || length > 200 {
		return false
	}
	for _, r := range normalized {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func blockHasContext(value, label string) bool {
	remaining := textcompat.Fold(value)
	if foldedLabel := textcompat.Fold(normalizeVisible(label)); foldedLabel != "" {
		remaining = strings.Replace(remaining, foldedLabel, " ", 1)
	}
	count := 0
	for _, r := range remaining {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' {
			count++
			if count >= 3 {
				return true
			}
		}
	}
	return false
}

func (p *visibleHTML) eligibleCounts(valid map[int]string) []int {
	counts := make([]int, len(p.blocks))
	for id, block := range p.blocks {
		for _, anchorID := range block.anchorIDs {
			if _, ok := valid[anchorID]; ok {
				counts[id]++
			}
		}
	}
	return counts
}

func (p *visibleHTML) selectBlock(anchor htmlAnchor, counts []int) string {
	label := normalizeVisible(strings.Join(anchor.labelChunks, ""))
	nearest := label
	for i := len(anchor.blockIDs) - 1; i >= 0; i-- {
		id := anchor.blockIDs[i]
		blockText := normalizeVisible(strings.Join(p.blocks[id].chunks, ""))
		if blockText == "" {
			continue
		}
		if nearest == "" {
			nearest = blockText
		}
		if counts[id] == 1 && blockHasContext(blockText, label) {
			return blockText
		}
	}
	return nearest
}

type parsedHTMLLinks struct {
	parser *visibleHTML
	result HTMLResult
	valid  map[int]string
	labels []string
	counts []int
}

func parseHTMLLinks(text string, linkBudget, relationBudget int) (parsedHTMLLinks, error) {
	p, err := parseVisibleHTML(text, linkBudget, relationBudget)
	if err != nil {
		return parsedHTMLLinks{}, err
	}
	parsed := parsedHTMLLinks{parser: p, result: HTMLResult{
		AnchorCount:            len(p.anchors),
		RelationReferenceCount: p.relations,
		VisibleText:            normalizeVisible(p.visible.String()),
	}, valid: make(map[int]string), labels: make([]string, len(p.anchors))}
	for id, anchor := range p.anchors {
		label := normalizeVisible(strings.Join(anchor.labelChunks, ""))
		parsed.labels[id] = label
		url, parsedURL := absoluteHTTPURL(anchor.url)
		if url == "" || shouldIgnoreLink(parsedURL, label) {
			parsed.result.IgnoredLinks++
			continue
		}
		parsed.valid[id] = url
	}
	parsed.counts = p.eligibleCounts(parsed.valid)
	return parsed, nil
}

// ExtractHTMLGeneric mirrors the generic branch of Python's HTML candidate
// extractor after MIME selection. Use ExtractHTMLSource to apply source-specific
// LinkedIn and Handshake grouping and suppression.
func ExtractHTMLGeneric(text string, linkBudget, relationBudget int) (HTMLResult, error) {
	parsed, err := parseHTMLLinks(text, linkBudget, relationBudget)
	if err != nil {
		return HTMLResult{}, err
	}
	extractGenericHTMLCandidates(&parsed)
	return parsed.result, nil
}

func extractGenericHTMLCandidates(parsed *parsedHTMLLinks) {
	p := parsed.parser
	valid := parsed.valid
	labels := parsed.labels
	counts := parsed.counts
	result := &parsed.result
	seen := make(map[string]bool)
	for id, anchor := range p.anchors {
		url, ok := valid[id]
		if !ok || seen[url] {
			continue
		}
		label := labels[id]
		sourceText := p.selectBlock(anchor, counts)
		labeledTitle := labeledField(sourceText, "title", "role")
		_, parsed := absoluteHTTPURL(url)
		if labeledTitle == "" && !plausibleTitleLabel(label) && !urlHasJobSignal(parsed) {
			result.IgnoredLinks++
			continue
		}
		seen[url] = true
		candidate := buildCandidate(url, sourceText)
		if candidate.Title == "" && plausibleTitleLabel(label) {
			candidate.Title = normalizeVisible(label)
			candidate.ContentScope = contentScope(sourceText, candidate.Title, candidate.Company, candidate.Location, url)
		}
		result.Candidates = append(result.Candidates, candidate)
	}
}
