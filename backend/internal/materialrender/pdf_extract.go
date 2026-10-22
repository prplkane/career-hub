package materialrender

import (
	"bytes"
	"context"
	"io"
	"strings"

	pdfreader "github.com/ledongthuc/pdf"
)

type pdfReadAt struct {
	ctx context.Context
	*bytes.Reader
	remaining int64
}

func (r *pdfReadAt) ReadAt(b []byte, off int64) (int, error) {
	if r.ctx.Err() != nil {
		return 0, r.ctx.Err()
	}
	r.remaining -= int64(len(b))
	if r.remaining < 0 {
		return 0, io.ErrUnexpectedEOF
	}
	return r.Reader.ReadAt(b, off)
}

// Only call with bytes just produced in this package. This library is NOT an
// untrusted-PDF importer: its decompressor and object graph are not a sandbox.
// Public Extract deliberately does not accept PDF. Persisted/legacy artifacts
// require their own authenticated-byte admission before any parser is invoked.
// The independent reader resolves actual PDF streams and embedded ToUnicode
// maps. Nothing is reconstructed from the draft, writer cache or a text receipt.
func extractGeneratedPDF(ctx context.Context, raw []byte) (text string, pages int, problem *Error) {
	defer func() {
		if recover() != nil {
			text = ""
			pages = 0
			problem = fail("MATERIAL_PDF_EXTRACT_INVALID")
			if e := ready(ctx); e != nil {
				problem = e
			}
		}
	}()
	if e := ready(ctx); e != nil {
		return "", 0, e
	}
	if len(raw) == 0 || len(raw) > MaxArtifactBytes {
		return "", 0, fail("MATERIAL_RENDER_ARTIFACT_LIMIT")
	}
	r, e := pdfreader.NewReader(&pdfReadAt{ctx, bytes.NewReader(raw), 64 << 20}, int64(len(raw)))
	if e != nil {
		return "", 0, fail("MATERIAL_PDF_EXTRACT_INVALID")
	}
	pages = r.NumPage()
	if pages < 1 || pages > maxPDFPages {
		return "", 0, fail("MATERIAL_PDF_PAGE_LIMIT")
	}
	var out strings.Builder
	ops := 0
	for i := 1; i <= pages; i++ {
		if e := ready(ctx); e != nil {
			return "", 0, e
		}
		page := r.Page(i)
		fonts := map[string]pdfreader.TextEncoding{}
		for _, name := range page.Fonts() {
			f := page.Font(name)
			fonts[name] = f.Encoder()
		}
		var encoding pdfreader.TextEncoding
		show := func(v pdfreader.Value) {
			if encoding == nil || v.Kind() != pdfreader.String {
				panic("unsupported text")
			}
			for _, ch := range encoding.Decode(v.RawString()) {
				if ch == 0 || ch == '\ufffd' || out.Len() > MaxTextBytes-4 {
					panic("invalid text")
				}
				out.WriteRune(ch)
			}
		}
		pdfreader.Interpret(page.V.Key("Contents"), func(stack *pdfreader.Stack, op string) {
			ops++
			if ops > maxPDFGlyphs*8 || ctx.Err() != nil || stack.Len() > 16 {
				panic("operation limit")
			}
			args := make([]pdfreader.Value, stack.Len())
			for i := len(args) - 1; i >= 0; i-- {
				args[i] = stack.Pop()
			}
			switch op {
			case "Tf":
				if len(args) != 2 {
					panic("invalid font")
				}
				encoding = fonts[args[0].Name()]
			case "Tj":
				if len(args) != 1 {
					panic("invalid text")
				}
				show(args[0])
			case "TJ":
				if len(args) != 1 || args[0].Len() > maxPDFGlyphs {
					panic("invalid text")
				}
				for j := 0; j < args[0].Len(); j++ {
					v := args[0].Index(j)
					if v.Kind() == pdfreader.String {
						show(v)
					}
				}
			case "Ts":
				if len(args) != 1 || args[0].Float64() != 0 {
					panic("unexpected text rise")
				}
			case "BT", "ET", "Td", "TD", "Tm", "Tc", "rg", "RG", "w", "m", "l", "S", "q", "Q":
				// Static template positions/colors/lines, not text.
			default:
				panic("unexpected generated PDF operation")
			}
		})
	}
	if e := ready(ctx); e != nil {
		return "", 0, e
	}
	return out.String(), pages, nil
}
