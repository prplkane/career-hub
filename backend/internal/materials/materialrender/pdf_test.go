package materialrender

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"

	pdfreader "github.com/ledongthuc/pdf"
)

func TestPDFGeneratedStreamProfile(t *testing.T) {
	raw, e := renderCoverPDF(context.Background(), exampleCover())
	if e != nil {
		t.Fatal(e.Code)
	}
	r, err := pdfreader.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	ops := map[string]int{}
	pdfreader.Interpret(r.Page(1).V.Key("Contents"), func(stk *pdfreader.Stack, op string) {
		ops[op]++
		for stk.Len() > 0 {
			stk.Pop()
		}
	})
	for op := range ops {
		if !slices.Contains([]string{"BT", "ET", "Q", "RG", "S", "TD", "TJ", "Tc", "Tf", "Ts", "l", "m", "q", "rg", "w"}, op) {
			t.Fatal("generated operator profile changed", op)
		}
	}
}

func TestPDFBytesAndIndependentFinalText(t *testing.T) {
	d := exampleCover()
	a, e := RenderCover(context.Background(), d, "pdf")
	if e != nil {
		t.Fatal(e.Code)
	}
	b, e := RenderCover(context.Background(), d, " PDF ")
	if e != nil {
		t.Fatal(e.Code)
	}
	if !bytes.Equal(a.Content, b.Content) || !bytes.HasPrefix(a.Content, []byte("%PDF-1.7")) {
		t.Fatal("PDF identity")
	}
	if a.PageCount != 1 || a.Pagination != "verified" || !strings.Contains(a.Text, d.Content.Opening) {
		t.Fatal("actual final text/pages")
	}
	if _, e := Extract(context.Background(), "pdf", a.Content); e == nil {
		t.Fatal("untrusted PDF parser exposed")
	}
	changed := d
	changed.Content.Opening = "This is a different claim."
	raw, e := renderCoverPDF(context.Background(), changed)
	if e != nil {
		t.Fatal(e.Code)
	}
	if _, e := finish(context.Background(), "cover_letter", d.TemplateID, "pdf", a.ExpectedText, raw); e == nil || e.Code != "MATERIAL_RENDER_TEXT_MISMATCH" {
		t.Fatal("final PDF text trusted the expected draft")
	}
	for _, data := range [][]byte{pdfRegular, pdfBold} {
		if len(data) < 50000 {
			t.Fatal("font resource missing")
		}
	}
	for i, data := range [][]byte{pdfRegular, pdfBold} {
		hash := sha256.Sum256(data)
		if hex.EncodeToString(hash[:]) != []string{"c4c45690b345435b2cba52ecabe275f05e49b389b39fe68ad03afbb551288d3d", "cc037385e4d55bfde89b13e03091ee93bf40c0c52ddd391ff031ab276f13b8e9"}[i] {
			t.Fatal("source font changed")
		}
	}
}

func TestPDFUnsupportedGlyphAndLayoutAreExplicit(t *testing.T) {
	for _, tc := range []struct{ name, value, code string }{{"glyph", "Name 🙂", "MATERIAL_PDF_GLYPH_UNSUPPORTED"}, {"overlap", strings.Repeat("Wide Name ", 14), "MATERIAL_PDF_LAYOUT_OVERFLOW"}} {
		t.Run(tc.name, func(t *testing.T) {
			d := exampleCover()
			d.Candidate.Name = tc.value
			_, e := RenderCover(context.Background(), d, "pdf")
			if e == nil || e.Code != tc.code {
				if e != nil {
					t.Fatal(e.Code)
				}
				t.Fatal("unsafe output accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := RenderCover(ctx, exampleCover(), "pdf"); e == nil || e.Code != "MATERIAL_RENDER_CANCELLED" {
		t.Fatal("cancel ignored")
	}
	w := &pdfSink{ctx: context.Background()}
	_, e := w.Write(make([]byte, MaxArtifactBytes+1))
	if e == nil || w.buf.Len() != 0 || w.err == nil {
		t.Fatal("size error not latched")
	}
	if _, e := w.Write([]byte("later")); e == nil || w.buf.Len() != 0 {
		t.Fatal("error reset")
	}
}

type pdfInspection struct {
	Title, Subject, Author string
	Pages                  []struct {
		Width, Height float64
		Chars         []struct {
			Text, Font string
			X, Y, Size float64
		}
		Links []struct {
			Target string
			Rect   []float64
		}
		Lines []struct{ X0, X1, Top, Width float64 }
		Fonts []struct {
			Name     string
			Embedded bool
		}
	}
}

func comparePDF(want, got pdfInspection) string {
	if want.Title != got.Title || want.Subject != got.Subject || got.Author != "" {
		return "metadata differs"
	}
	if len(want.Pages) != len(got.Pages) {
		return fmt.Sprintf("page count: %d / %d", len(want.Pages), len(got.Pages))
	}
	for pi, w := range want.Pages {
		g := got.Pages[pi]
		if w.Width != g.Width || w.Height != g.Height {
			return "page dimensions differ"
		}
		if len(w.Chars) != len(g.Chars) {
			return fmt.Sprintf("page %d glyph count: %d / %d", pi+1, len(w.Chars), len(g.Chars))
		}
		for i, c := range w.Chars {
			a := g.Chars[i]
			if c.Text != a.Text || c.Font != a.Font || math.Abs(c.Size-a.Size) > 0.001 || math.Abs(c.X-a.X) > 0.15 || math.Abs(c.Y-a.Y) > 0.05 {
				return fmt.Sprintf("page %d glyph %d: want %+v, got %+v", pi+1, i, c, a)
			}
		}
		if len(w.Links) != len(g.Links) {
			return "link count differs"
		}
		for i, l := range w.Links {
			if l.Target != g.Links[i].Target || len(l.Rect) != len(g.Links[i].Rect) {
				return "link target/rectangle differs"
			}
			for n, v := range l.Rect {
				if math.Abs(v-g.Links[i].Rect[n]) > 0.05 {
					return fmt.Sprintf("link rectangle: %v / %v", l.Rect, g.Links[i].Rect)
				}
			}
		}
		if len(w.Lines) != len(g.Lines) {
			return "divider count differs"
		}
		for i, l := range w.Lines {
			a := g.Lines[i]
			if math.Abs(l.X0-a.X0) > 0.05 || math.Abs(l.X1-a.X1) > 0.05 || math.Abs(l.Top-a.Top) > 0.05 || math.Abs(l.Width-a.Width) > 0.001 {
				return fmt.Sprintf("divider: %+v / %+v", l, a)
			}
		}
		if len(g.Fonts) != len(w.Fonts) || len(g.Fonts) < 2 {
			return "embedded font count differs"
		}
		for _, f := range g.Fonts {
			if !f.Embedded {
				return "font is not embedded"
			}
		}
	}
	return ""
}

func TestPDFConcurrentByteDeterminism(t *testing.T) {
	want, e := RenderCover(context.Background(), exampleCover(), "pdf")
	if e != nil {
		t.Fatal(e.Code)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			got, e := RenderCover(context.Background(), exampleCover(), "pdf")
			if e != nil {
				t.Error(e.Code)
				return
			}
			if !bytes.Equal(want.Content, got.Content) {
				t.Error("shared renderer state")
			}
		})
	}
	wg.Wait()
}

func TestPDFResourceBoundsAndCancellation(t *testing.T) {
	p := newPDF(context.Background(), "Example", "Example", 36)
	for range maxPDFPages {
		p.newPage()
	}
	if _, e := p.bytes(); e == nil || e.Code != "MATERIAL_PDF_PAGE_LIMIT" {
		t.Fatal("page limit ignored")
	}
	p = newPDF(context.Background(), "Example", "Example", 36)
	p.wrap(pdfPara(strings.Repeat("x", maxPDFGlyphs+1), 0, 10, 12, 0).p)
	if _, e := p.bytes(); e == nil || e.Code != "MATERIAL_PDF_TEXT_LIMIT" {
		t.Fatal("glyph budget ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	p = newPDF(ctx, "Example", "Example", 36)
	cancel()
	if _, e := p.bytes(); e == nil || e.Code != "MATERIAL_RENDER_CANCELLED" {
		t.Fatal("late cancellation ignored")
	}
}

func TestPDFContactCollisionWithoutPhone(t *testing.T) {
	d := exampleCover()
	d.Candidate.Location = strings.Repeat("Wide ", 14)
	d.Candidate.Website = "https://example.test/" + strings.Repeat("a", 50)
	if _, e := RenderCover(context.Background(), d, "pdf"); e == nil || e.Code != "MATERIAL_PDF_LAYOUT_OVERFLOW" {
		t.Fatal("location and links overlap")
	}
}

// Fuzz typed text through the real writer, never arbitrary PDFs through the
// trusted-generated-document reader. The general PDF importer is a separate gate.
func FuzzPDFTypedText(f *testing.F) {
	for _, s := range []string{"Plain application text.", "Literal <text> & café – data.", strings.Repeat("unbroken", 30), "Unsupported 🙂"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 2000 {
			t.Skip()
		}
		d := exampleCover()
		d.Content.Opening = text
		result, e := RenderCover(context.Background(), d, "pdf")
		if e != nil {
			if result != nil {
				t.Fatal("partial artifact returned on failure")
			}
			return
		}
		if result.PageCount < 1 || result.PageCount > maxPDFPages || len(result.Content) > MaxArtifactBytes {
			t.Fatal("invalid artifact returned")
		}
	})
}

func TestPDFComparisonDetectsMaterialChanges(t *testing.T) {
	base := []byte(`{"Title":"Example","Subject":"Example","Pages":[{"Width":612,"Height":792,"Chars":[{"Text":"x","Font":"Vera","X":42,"Y":700,"Size":10}],"Links":[{"Target":"https://example.test","Rect":[40,600,100,612]}],"Lines":[{"X0":42,"X1":570,"Top":120,"Width":0.75}],"Fonts":[{"Name":"Vera","Embedded":true},{"Name":"Vera-Bold","Embedded":true}]}]}`)
	var want pdfInspection
	if e := json.Unmarshal(base, &want); e != nil {
		t.Fatal(e)
	}
	for name, change := range map[string]func(*pdfInspection){
		"text":             func(x *pdfInspection) { x.Pages[0].Chars[0].Text = "y" },
		"font":             func(x *pdfInspection) { x.Pages[0].Chars[0].Font = "Other" },
		"size":             func(x *pdfInspection) { x.Pages[0].Chars[0].Size = 9 },
		"position":         func(x *pdfInspection) { x.Pages[0].Chars[0].Y = 699 },
		"page-count":       func(x *pdfInspection) { x.Pages = append(x.Pages, x.Pages[0]) },
		"page-width":       func(x *pdfInspection) { x.Pages[0].Width = 600 },
		"link":             func(x *pdfInspection) { x.Pages[0].Links[0].Target = "https://other.test" },
		"rectangle":        func(x *pdfInspection) { x.Pages[0].Links[0].Rect[0]++ },
		"divider":          func(x *pdfInspection) { x.Pages[0].Lines[0].Top++ },
		"embedding":        func(x *pdfInspection) { x.Pages[0].Fonts[0].Embedded = false },
		"missing-font":     func(x *pdfInspection) { x.Pages[0].Fonts = nil },
		"private-metadata": func(x *pdfInspection) { x.Author = "Private Name" },
	} {
		t.Run(name, func(t *testing.T) {
			var got pdfInspection
			_ = json.Unmarshal(base, &got)
			change(&got)
			if comparePDF(want, got) == "" {
				t.Fatal("changed material accepted")
			}
		})
	}
}
