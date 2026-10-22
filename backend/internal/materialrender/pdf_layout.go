package materialrender

import (
	"bytes"
	"context"
	_ "embed"
	"math"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
	"github.com/signintech/gopdf"
)

// Unmodified source-template font files. The redistribution notice accompanies
// them in assets; no host font, user font path, or remote asset is consulted.
//
//go:embed assets/Vera.ttf
var pdfRegular []byte

//go:embed assets/VeraBd.ttf
var pdfBold []byte

const maxPDFPages = 32
const maxPDFGlyphs = 100_000

var pdfFontNames = [2]string{"BitstreamVeraSans-Roman", "BitstreamVeraSans-Bold"}

type pdfSpan struct {
	text string
	face int
}
type pdfGlyph struct {
	r     rune
	face  int
	width float64
}
type pdfLine struct {
	glyphs []pdfGlyph
	width  float64
	shrink bool
}
type pdfParagraph struct {
	spans                        []pdfSpan
	size, leading, before, after float64
	center, bullet               bool
}
type pdfBlock struct {
	p        pdfParagraph
	rule     bool
	group    int
	keepNext bool
}
type measuredPDFBlock struct {
	pdfBlock
	lines []pdfLine
}
type pdfDocument struct {
	ctx                                context.Context
	writer                             gopdf.GoPdf
	metrics                            [2]gopdf.SubsetFontObj
	widths                             [2]map[rune]float64
	err                                *Error
	left, top, width, bottom, y, after float64
	pages, glyphs                      int
}

func newPDF(ctx context.Context, title, subject string, margin float64) *pdfDocument {
	p := &pdfDocument{ctx: ctx, left: margin, top: margin, width: 612 - 2*margin, bottom: 792 - margin}
	p.writer.Start(gopdf.Config{Unit: gopdf.UnitPT, PageSize: gopdf.Rect{W: 612, H: 792}})
	p.writer.SetInfo(gopdf.PdfInfo{Title: title, Subject: subject, Producer: RendererVersion})
	for i, data := range [][]byte{pdfRegular, pdfBold} {
		option := gopdf.TtfOption{OnGlyphNotFound: func(r rune) { p.err = fail("MATERIAL_PDF_GLYPH_UNSUPPORTED") }}
		p.metrics[i].SetTtfFontOption(option)
		if p.metrics[i].SetTTFData(data) != nil || p.writer.AddTTFFontDataWithOption(pdfFontNames[i], data, option) != nil {
			p.err = fail("MATERIAL_PDF_FONT_INVALID")
		}
		p.widths[i] = map[rune]float64{}
	}
	p.newPage()
	return p
}

func (p *pdfDocument) check() bool {
	if e := ready(p.ctx); e != nil {
		p.err = e
	}
	return p.err == nil
}
func (p *pdfDocument) newPage() {
	if !p.check() {
		return
	}
	if p.pages >= maxPDFPages {
		p.err = fail("MATERIAL_PDF_PAGE_LIMIT")
		return
	}
	p.writer.AddPage()
	p.pages++
	p.y, p.after = p.top, 0
}

// ReportLab measures the original font units, not the integer /1000 widths in
// a PDF subset. Keep source wrapping and alignment independent of that rounding.
func (p *pdfDocument) glyphWidth(r rune, face int, size float64) float64 {
	if v, ok := p.widths[face][r]; ok {
		return v * size
	}
	index, e := p.metrics[face].CharCodeToGlyphIndex(r)
	if e != nil || index == 0 {
		p.err = fail("MATERIAL_PDF_GLYPH_UNSUPPORTED")
		return 0
	}
	ttf := p.metrics[face].GetTTFParser()
	if index >= ttf.NumberOfHMetrics() {
		index = ttf.NumberOfHMetrics() - 1
	}
	v := float64(ttf.Widths()[index]) / float64(ttf.UnitsPerEm())
	p.widths[face][r] = v
	return v * size
}
func (p *pdfDocument) widthOf(text string, face int, size float64) float64 {
	w := 0.0
	for _, r := range text {
		w += p.glyphWidth(r, face, size)
	}
	return w
}

func (p *pdfDocument) draw(text string, face int, size, x, y float64, boundary bool) {
	if !p.check() || text == "" {
		return
	}
	w := p.widthOf(strings.TrimRight(text, " "), face, size)
	if x < 36-0.05 || x+w > 576+0.05 || y-size < p.top-1 || y+size*0.24 > 756+0.05 {
		p.err = fail("MATERIAL_PDF_LAYOUT_OVERFLOW")
		return
	}
	// Encode semantic separation as actual spaces, without moving the visible
	// glyphs. Extraction must not invent a space inside a visually wrapped word.
	if boundary {
		text = " " + text
		x -= p.widthOf(" ", face, size)
	}
	if p.writer.SetFont(pdfFontNames[face], "", size) != nil {
		p.err = fail("MATERIAL_PDF_FONT_INVALID")
		return
	}
	_ = p.writer.SetCharSpacing(0)
	actual, e := p.writer.MeasureTextWidth(text)
	if e != nil || p.err != nil {
		if p.err == nil {
			p.err = fail("MATERIAL_PDF_FONT_INVALID")
		}
		return
	}
	n := utf8.RuneCountInString(text)
	if n > 1 {
		// Retain exact source run width despite the library's integer subset
		// advances. The per-character distribution is checked independently.
		_ = p.writer.SetCharSpacing((p.widthOf(text, face, size) - actual) / float64(n-1))
	}
	p.writer.SetXY(x, y)
	if p.writer.Text(text) != nil {
		p.err = fail("MATERIAL_PDF_WRITE_FAILED")
	}
	_ = p.writer.SetCharSpacing(0)
}

func (p *pdfDocument) wrap(par pdfParagraph) []pdfLine {
	if !p.check() {
		return nil
	}
	var glyphs []pdfGlyph
	pendingSpace := false
	for _, span := range par.spans {
		for _, r := range span.text {
			if textcompat.IsSpace(r) {
				pendingSpace = len(glyphs) > 0
				continue
			}
			if pendingSpace {
				glyphs = append(glyphs, pdfGlyph{' ', span.face, p.glyphWidth(' ', span.face, par.size)})
				pendingSpace = false
			}
			glyphs = append(glyphs, pdfGlyph{r, span.face, p.glyphWidth(r, span.face, par.size)})
		}
	}
	p.glyphs += len(glyphs)
	if p.glyphs > maxPDFGlyphs {
		p.err = fail("MATERIAL_PDF_TEXT_LIMIT")
		return nil
	}
	limit := p.width
	if par.bullet {
		limit -= 12
	}
	lines := []pdfLine{}
	for start := 0; start < len(glyphs); {
		if !p.check() {
			return nil
		}
		width, lastSpace, beforeSpace := 0.0, -1, 0.0
		end := start
		for end < len(glyphs) && width+glyphs[end].width <= limit+1e-7 {
			if glyphs[end].r == ' ' {
				lastSpace = end
				beforeSpace = width
			}
			width += glyphs[end].width
			end++
		}
		if end == start {
			p.err = fail("MATERIAL_PDF_LAYOUT_OVERFLOW")
			return nil
		}
		if end < len(glyphs) {
			// Source paragraphs may shrink inter-word spaces by at most 5%
			// to keep one more complete word on a line. Do not shrink letters.
			wordEnd, fullWidth, spaces := end, width, 0
			for wordEnd < len(glyphs) && glyphs[wordEnd].r != ' ' {
				fullWidth += glyphs[wordEnd].width
				wordEnd++
			}
			for _, g := range glyphs[start:wordEnd] {
				if g.r == ' ' {
					spaces++
				}
			}
			if spaces > 0 && fullWidth > limit && fullWidth <= limit+float64(spaces)*p.glyphWidth(' ', 0, par.size)*0.05 {
				stop := wordEnd
				if stop < len(glyphs) {
					stop++
				}
				lineGlyphs := append([]pdfGlyph(nil), glyphs[start:stop]...)
				for i := 0; i < wordEnd-start; i++ {
					if lineGlyphs[i].r == ' ' {
						lineGlyphs[i].width -= (fullWidth - limit) / float64(spaces)
					}
				}
				lines = append(lines, pdfLine{lineGlyphs, limit, true})
				start = stop
				continue
			}
			if glyphs[end].r == ' ' {
				lines = append(lines, pdfLine{glyphs[start : end+1], width, false})
				start = end + 1
				continue
			}
			if lastSpace >= start {
				// An oversized word can fill the remaining first line, as in
				// ReportLab's splitLongWords, instead of creating a blank gap.
				wordEnd := end
				for wordEnd < len(glyphs) && glyphs[wordEnd].r != ' ' {
					wordEnd++
				}
				wordWidth := 0.0
				for _, g := range glyphs[lastSpace+1 : wordEnd] {
					wordWidth += g.width
				}
				if wordWidth <= limit {
					end = lastSpace + 1
					width = beforeSpace
				}
			}
		}
		lines = append(lines, pdfLine{glyphs[start:end], width, false})
		start = end
	}
	return lines
}

func (p *pdfDocument) line(line pdfLine, par pdfParagraph, first, boundary bool) {
	x := p.left
	if par.bullet {
		x += 12
	}
	if par.center {
		x += (p.width - line.width) / 2
	}
	baseline := p.y + par.size
	if par.bullet && first {
		p.draw("•", 0, 7, p.left-1, p.y+6, true)
		boundary = true
	}
	for start := 0; start < len(line.glyphs); {
		end := start + 1
		face := line.glyphs[start].face
		for end < len(line.glyphs) && line.glyphs[end].face == face && (!line.shrink || line.glyphs[end-1].r != ' ') {
			end++
		}
		var text strings.Builder
		width := 0.0
		for _, g := range line.glyphs[start:end] {
			text.WriteRune(g.r)
			width += g.width
		}
		// Trailing spaces are real extraction separators, not visible overflow.
		value := text.String()
		p.draw(value, face, par.size, x, baseline, boundary && start == 0)
		x += width
		start = end
	}
	p.y += par.leading
}

func (p *pdfDocument) layout(blocks []pdfBlock) {
	measured := make([]measuredPDFBlock, len(blocks))
	for i, b := range blocks {
		measured[i] = measuredPDFBlock{pdfBlock: b}
		if !b.rule {
			measured[i].lines = p.wrap(b.p)
		}
	}
	for i, b := range measured {
		if !p.check() {
			return
		}
		if b.group > 0 && (i == 0 || measured[i-1].group != b.group) {
			height := 0.0
			for j := i; j < len(measured) && measured[j].group == b.group; j++ {
				height += float64(len(measured[j].lines))*measured[j].p.leading + measured[j].p.after
			}
			// Source KeepTogether advances even an oversized group when it
			// starts below the frame top, then permits splitting at the top.
			if p.y > p.top && p.y+height > p.bottom {
				p.newPage()
			}
		}
		if b.rule {
			if p.y+0.75 > p.bottom {
				p.newPage()
			}
			p.writer.SetStrokeColor(0, 0, 0)
			p.writer.SetLineWidth(0.75)
			p.writer.Line(p.left, p.y+0.75, p.left+p.width, p.y+0.75)
			p.y += 2.75
			p.after = 2
			continue
		}
		if len(b.lines) == 0 {
			// A zero-height source Paragraph still updates trailing space.
			p.y += math.Max(0, b.p.before-p.after) + b.p.after
			p.after = b.p.after
			continue
		}
		before := 0.0
		if p.y > p.top {
			before = math.Max(0, b.p.before-p.after)
		}
		height := float64(len(b.lines)) * b.p.leading
		if b.keepNext {
			height += 2.75
		}
		if b.keepNext && p.y+before+height > p.bottom {
			p.newPage()
			before = 0
		}
		p.y += before
		for start := 0; start < len(b.lines); {
			if !p.check() {
				return
			}
			fit := int(math.Floor((p.bottom - p.y + 1e-6) / b.p.leading))
			remaining := len(b.lines) - start
			if fit >= remaining {
				fit = remaining
			} else {
				if fit < 2 && remaining > 1 {
					p.newPage()
					continue
				}
				if remaining-fit == 1 && fit > 2 {
					fit--
				}
			}
			if fit <= 0 {
				p.newPage()
				continue
			}
			for j := 0; j < fit; j++ {
				p.line(b.lines[start+j], b.p, start+j == 0, start+j == 0)
			}
			start += fit
			if start < len(b.lines) {
				p.newPage()
			}
		}
		p.y += b.p.after
		p.after = b.p.after
	}
}

// gopdf's compiler currently ignores several nested io.Writer errors. Latch our
// cancellation/size failure and check it explicitly even if WriteTo returns nil.
type pdfSink struct {
	ctx context.Context
	buf bytes.Buffer
	err *Error
}

func (w *pdfSink) Write(b []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if e := ready(w.ctx); e != nil {
		w.err = e
		return 0, e
	}
	if len(b) > MaxArtifactBytes-w.buf.Len() {
		w.err = fail("MATERIAL_RENDER_ARTIFACT_LIMIT")
		return 0, w.err
	}
	return w.buf.Write(b)
}
func (p *pdfDocument) bytes() ([]byte, *Error) {
	if !p.check() {
		return nil, p.err
	}
	w := &pdfSink{ctx: p.ctx}
	_, e := p.writer.WriteTo(w)
	if w.err != nil {
		return nil, w.err
	}
	if e != nil {
		return nil, fail("MATERIAL_PDF_WRITE_FAILED")
	}
	if !p.check() {
		return nil, p.err
	}
	return w.buf.Bytes(), nil
}
