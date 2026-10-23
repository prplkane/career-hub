package intakeextract

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func fictionalDOCX(t *testing.T, parts [][2]string) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, part := range parts {
		file, err := writer.Create(part[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(part[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

const fictionalDocument = `<?xml version="1.0"?><w:document xmlns:w="` + wordNS + `"><w:body><w:p><w:r><w:t>Fictional Python</w:t></w:r></w:p></w:body></w:document>`
const fictionalContentTypes = `<Types xmlns="` + contentTypesNS + `"><Override PartName="/word/document.xml" ContentType="` + documentContentType + `"/><Override PartName="/word/styles.xml" ContentType="` + stylesContentType + `"/></Types>`
const fictionalRootRelationships = `<Relationships xmlns="` + packageRelationshipsNS + `"><Relationship Id="rId1" Type="` + officeRelationshipsNS + `officeDocument" Target="word/document.xml"/></Relationships>`
const fictionalDocumentRelationships = `<Relationships xmlns="` + packageRelationshipsNS + `"><Relationship Id="rId1" Type="` + officeRelationshipsNS + `styles" Target="styles.xml"/></Relationships>`
const fictionalStyles = `<w:styles xmlns:w="` + wordNS + `"><w:style w:type="paragraph" w:styleId="Normal" w:default="1"><w:name w:val="Normal"/></w:style></w:styles>`

func fictionalParts(document string) [][2]string {
	return [][2]string{
		{"[Content_Types].xml", fictionalContentTypes},
		{"_rels/.rels", fictionalRootRelationships},
		{"word/_rels/document.xml.rels", fictionalDocumentRelationships},
		{"word/styles.xml", fictionalStyles},
		{"word/document.xml", document},
	}
}

func replaceFictionalPart(parts [][2]string, name, content string) [][2]string {
	result := append([][2]string(nil), parts...)
	for i := range result {
		if result[i][0] == name {
			result[i][1] = content
		}
	}
	return result
}

func removeFictionalPart(parts [][2]string, name string) [][2]string {
	result := make([][2]string, 0, len(parts))
	for _, part := range parts {
		if part[0] != name {
			result = append(result, part)
		}
	}
	return result
}

func TestDOCXPackageAndXMLRefusalsDoNotPublishBlocks(t *testing.T) {
	base := fictionalParts(fictionalDocument)
	valid := fictionalDOCX(t, base)
	blocks, err := ParseDOCXBlocks(context.Background(), valid)
	if err != nil || len(blocks) != 1 || blocks[0].Text != "Fictional Python" {
		t.Fatalf("minimal fictional DOCX rejected: %v", err)
	}
	for name, parts := range map[string][][2]string{
		"missing-document":         removeFictionalPart(base, "word/document.xml"),
		"missing-root-relations":   removeFictionalPart(base, "_rels/.rels"),
		"duplicate-document":       append(append([][2]string(nil), base...), base[4]),
		"unsafe-member":            append(append([][2]string(nil), base...), [2]string{"../outside", "fictional"}),
		"bad-content-types":        replaceFictionalPart(base, "[Content_Types].xml", "<Types/>"),
		"wrong-document-type":      replaceFictionalPart(base, "[Content_Types].xml", strings.Replace(fictionalContentTypes, documentContentType, "application/xml", 1)),
		"wrong-root-relationship":  replaceFictionalPart(base, "_rels/.rels", strings.Replace(fictionalRootRelationships, `Target="word/document.xml"`, `Target="elsewhere.xml"`, 1)),
		"wrong-style-relationship": replaceFictionalPart(base, "word/_rels/document.xml.rels", strings.Replace(fictionalDocumentRelationships, `Target="styles.xml"`, `Target="elsewhere.xml"`, 1)),
		"bad-xml-after-paragraph":  replaceFictionalPart(base, "word/document.xml", strings.Replace(fictionalDocument, "</w:body>", "<w:p>", 1)),
		"entity-directive":         replaceFictionalPart(base, "word/document.xml", strings.Replace(fictionalDocument, "<w:document", `<!DOCTYPE document [<!ENTITY x "private">]><w:document`, 1)),
		"external-chunk":           replaceFictionalPart(base, "word/document.xml", strings.Replace(fictionalDocument, "</w:body>", "<w:altChunk/></w:body>", 1)),
		"content-control":          replaceFictionalPart(base, "word/document.xml", strings.Replace(fictionalDocument, "</w:body>", "<w:sdt/></w:body>", 1)),
		"unknown-body-wrapper":     replaceFictionalPart(base, "word/document.xml", strings.Replace(fictionalDocument, "</w:body>", "<w:customXml><w:p><w:r><w:t>Hidden role</w:t></w:r></w:p></w:customXml></w:body>", 1)),
		"deleted-run":              replaceFictionalPart(base, "word/document.xml", strings.Replace(fictionalDocument, "</w:p>", "<w:del><w:r><w:t>False skill</w:t></w:r></w:del></w:p>", 1)),
		"field-run":                replaceFictionalPart(base, "word/document.xml", strings.Replace(fictionalDocument, "</w:p>", "<w:fldSimple><w:r><w:t>False skill</w:t></w:r></w:fldSimple></w:p>", 1)),
		"merged-cell":              replaceFictionalPart(base, "word/document.xml", strings.Replace(fictionalDocument, "</w:body>", `<w:tbl><w:tr><w:tc><w:tcPr><w:gridSpan w:val="2"/></w:tcPr><w:p/></w:tc></w:tr></w:tbl></w:body>`, 1)),
		"oversized-document":       replaceFictionalPart(base, "word/document.xml", strings.Repeat("x", maxDocumentXML+1)),
	} {
		t.Run(name, func(t *testing.T) {
			got, problem := ParseDOCXBlocks(context.Background(), fictionalDOCX(t, parts))
			want := ErrInput
			if name == "oversized-document" {
				want = ErrLimit
			}
			if got != nil || !errors.Is(problem, want) {
				t.Fatalf("unsafe package yielded blocks: %v", problem)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, problem := ParseDOCXBlocks(ctx, valid); got != nil || !errors.Is(problem, context.Canceled) {
		t.Fatal("canceled DOCX adapter yielded blocks")
	}
}

func TestDOCXSymlinkMemberRefused(t *testing.T) {
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, part := range fictionalParts(fictionalDocument) {
		header := &zip.FileHeader{Name: part[0], Method: zip.Deflate}
		if part[0] == "word/document.xml" {
			header.SetMode(os.ModeSymlink | 0o777)
		}
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(part[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if blocks, err := ParseDOCXBlocks(context.Background(), output.Bytes()); blocks != nil || !errors.Is(err, ErrInput) {
		t.Fatal("ZIP symlink yielded source blocks")
	}
}

func FuzzDOCXAdapterNeverReturnsPartialOrPanics(f *testing.F) {
	f.Add([]byte("not a document"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 128<<10 {
			return
		}
		blocks, err := ParseDOCXBlocks(context.Background(), raw)
		if err != nil && blocks != nil {
			t.Fatal("failed DOCX parse returned partial blocks")
		}
	})
}

// wordSavedDocument mirrors what Word writes for a two-page resume: spell
// and grammar marks around words, the _GoBack bookmark at body and paragraph
// level, a rendered page-break hint inside a run, comment and permission
// ranges, range markers between table rows and cells, a non-breaking hyphen
// and a soft hyphen. None of these carries prose.
const wordSavedDocument = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<w:document xmlns:w="` + wordNS + `" xmlns:r="` + officeRelationshipsNS + `">` +
	`<w:body>` +
	`<w:bookmarkStart w:id="0" w:name="_GoBack"/><w:bookmarkEnd w:id="0"/>` +
	`<w:p><w:pPr><w:pStyle w:val="Normal"/><w:rPr><w:b/></w:rPr></w:pPr>` +
	`<w:proofErr w:type="spellStart"/><w:r><w:rPr><w:b/></w:rPr><w:t>Jane</w:t></w:r><w:proofErr w:type="spellEnd"/>` +
	`<w:r><w:t xml:space="preserve"> Example</w:t></w:r>` +
	`<w:bookmarkStart w:id="1" w:name="_GoBack"/><w:bookmarkEnd w:id="1"/></w:p>` +
	`<w:p><w:proofErr w:type="gramStart"/><w:r><w:lastRenderedPageBreak/><w:t>Built a C</w:t><w:noBreakHyphen/><w:t>suite report</w:t></w:r><w:proofErr w:type="gramEnd"/></w:p>` +
	`<w:p><w:commentRangeStart w:id="2"/><w:r><w:t>Comm</w:t><w:softHyphen/><w:t>ented Python</w:t></w:r><w:commentRangeEnd w:id="2"/></w:p>` +
	`<w:p><w:permStart w:id="3" w:edGrp="everyone"/><w:hyperlink r:id="rId9"><w:proofErr w:type="spellStart"/><w:r><w:t>example.test</w:t></w:r><w:proofErr w:type="spellEnd"/><w:bookmarkStart w:id="4" w:name="_Hlk1"/><w:bookmarkEnd w:id="4"/></w:hyperlink><w:permEnd w:id="3"/></w:p>` +
	`<w:tbl><w:bookmarkStart w:id="5" w:name="_Hlk2"/><w:tblPr/><w:tblGrid><w:gridCol/></w:tblGrid>` +
	`<w:tr><w:trPr/><w:commentRangeStart w:id="6"/><w:tc><w:tcPr/><w:proofErr w:type="spellStart"/><w:p><w:r><w:t>Cell</w:t></w:r></w:p><w:bookmarkStart w:id="7" w:name="_GoBack"/><w:bookmarkEnd w:id="7"/></w:tc><w:commentRangeEnd w:id="6"/></w:tr>` +
	`<w:bookmarkEnd w:id="5"/></w:tbl>` +
	`<w:sectPr/></w:body></w:document>`

// stripWordMarkers removes every tolerated marker so the comparison document
// is the same resume in the hand-written subset the fixtures already use.
func stripWordMarkers(document string) string {
	marker := regexp.MustCompile(`<w:(proofErr|bookmarkStart|bookmarkEnd|lastRenderedPageBreak|commentRangeStart|commentRangeEnd|permStart|permEnd|softHyphen)\b[^>]*/>`)
	plain := marker.ReplaceAllString(document, "")
	return strings.ReplaceAll(plain, "<w:noBreakHyphen/>", "<w:t>‑</w:t>")
}

func TestDOCXWordMarkersDoNotChangeExtractedText(t *testing.T) {
	plain := stripWordMarkers(wordSavedDocument)
	for _, name := range []string{"proofErr", "bookmarkStart", "lastRenderedPageBreak", "noBreakHyphen", "softHyphen", "commentRangeStart", "permStart"} {
		if !strings.Contains(wordSavedDocument, "<w:"+name) || strings.Contains(plain, "<w:"+name) {
			t.Fatalf("fixture must contain and the stripped copy must omit %s", name)
		}
	}
	want, err := ParseDOCXBlocks(context.Background(), fictionalDOCX(t, fictionalParts(plain)))
	if err != nil {
		t.Fatalf("marker-free fixture rejected: %v", err)
	}
	got, err := ParseDOCXBlocks(context.Background(), fictionalDOCX(t, fictionalParts(wordSavedDocument)))
	if err != nil {
		t.Fatalf("Word-saved fixture rejected: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("markers changed extraction\nwant %#v\ngot %#v", want, got)
	}
	texts := make([]string, 0, len(got))
	kinds := make([]string, 0, len(got))
	for _, block := range got {
		texts = append(texts, block.Text)
		kinds = append(kinds, block.Kind)
	}
	if !reflect.DeepEqual(texts, []string{"Jane Example", "Built a C‑suite report", "Commented Python", "example.test", "Cell"}) ||
		!reflect.DeepEqual(kinds, []string{"paragraph", "paragraph", "paragraph", "paragraph", "table_cell"}) {
		t.Fatalf("unexpected blocks: %q %q", texts, kinds)
	}
}

func TestDOCXTextBearingConstructsStillRefusedWithTypedError(t *testing.T) {
	inParagraph := func(inner string) string {
		return strings.Replace(fictionalDocument, "</w:p>", inner+"</w:p>", 1)
	}
	inBody := func(inner string) string {
		return strings.Replace(fictionalDocument, "</w:body>", inner+"</w:body>", 1)
	}
	inRun := func(inner string) string {
		return strings.Replace(fictionalDocument, "</w:r>", inner+"</w:r>", 1)
	}
	for name, sample := range map[string]struct{ document, element string }{
		"content-control":        {inBody(`<w:sdt><w:sdtContent><w:p><w:r><w:t>Hidden role</w:t></w:r></w:p></w:sdtContent></w:sdt>`), "sdt"},
		"simple-field":           {inParagraph(`<w:fldSimple w:instr="AUTHOR"><w:r><w:t>False name</w:t></w:r></w:fldSimple>`), "fldSimple"},
		"field-char":             {inRun(`<w:fldChar w:fldCharType="begin"/>`), "fldChar"},
		"field-instruction":      {inRun(`<w:instrText>PAGE</w:instrText>`), "instrText"},
		"inserted-run":           {inParagraph(`<w:ins w:id="1"><w:r><w:t>False skill</w:t></w:r></w:ins>`), "ins"},
		"deleted-run":            {inParagraph(`<w:del w:id="1"><w:r><w:delText>False skill</w:delText></w:r></w:del>`), "del"},
		"smart-tag":              {inParagraph(`<w:smartTag><w:r><w:t>Place</w:t></w:r></w:smartTag>`), "smartTag"},
		"drawing":                {inRun(`<w:drawing/>`), "drawing"},
		"picture":                {inRun(`<w:pict/>`), "pict"},
		"symbol":                 {inRun(`<w:sym w:font="Wingdings" w:char="F0FC"/>`), "sym"},
		"footnote-reference":     {inRun(`<w:footnoteReference w:id="1"/>`), "footnoteReference"},
		"text-box":               {inBody(`<w:p><w:r><w:pict><w:txbxContent><w:p><w:r><w:t>Boxed</w:t></w:r></w:p></w:txbxContent></w:pict></w:r></w:p>`), "pict"},
		"alternate-chunk":        {inBody(`<w:altChunk r:id="rId5"/>`), "altChunk"},
		"marker-wrapping-a-run":  {inParagraph(`<w:bookmarkStart w:id="1" w:name="x"><w:r><w:t>Smuggled</w:t></w:r></w:bookmarkStart>`), "r"},
		"marker-inside-run":      {inRun(`<w:proofErr w:type="spellStart"/>`), "proofErr"},
		"page-break-in-body":     {inBody(`<w:lastRenderedPageBreak/>`), "lastRenderedPageBreak"},
		"hyphen-outside-run":     {inParagraph(`<w:noBreakHyphen/>`), "noBreakHyphen"},
		"nested-table":           {inBody(`<w:tbl><w:tr><w:tc><w:tbl><w:tr><w:tc><w:p/></w:tc></w:tr></w:tbl></w:tc></w:tr></w:tbl>`), "tbl"},
		"vertically-merged-cell": {inBody(`<w:tbl><w:tr><w:tc><w:tcPr><w:vMerge/></w:tcPr><w:p/></w:tc></w:tr></w:tbl>`), "vMerge"},
	} {
		t.Run(name, func(t *testing.T) {
			blocks, err := ParseDOCXBlocks(context.Background(), fictionalDOCX(t, fictionalParts(sample.document)))
			if blocks != nil || !errors.Is(err, ErrUnsupportedConstruct) || !errors.Is(err, ErrInput) {
				t.Fatalf("construct not refused with the typed error: %v", err)
			}
			var typed *UnsupportedConstructError
			if !errors.As(err, &typed) || typed.Element != sample.element {
				t.Fatalf("typed error does not name %q: %v", sample.element, err)
			}
			if !strings.Contains(err.Error(), sample.element) {
				t.Fatalf("error text omits the element: %v", err)
			}
		})
	}
	if errors.Is(ErrInput, ErrUnsupportedConstruct) || errors.Is(ErrLimit, ErrUnsupportedConstruct) {
		t.Fatal("generic errors must not read as unsupported constructs")
	}
}
