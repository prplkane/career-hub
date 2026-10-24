package materialrender

import (
	"archive/zip"
	"bytes"
	"context"
	_ "embed"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"careerhub.local/backend/internal/materials"
)

const (
	wNS     = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	rNS     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	pkgNS   = "http://schemas.openxmlformats.org/package/2006/relationships"
	xmlNS   = "http://www.w3.org/XML/1998/namespace"
	xmlHead = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`
)

// This is a small, code-owned OOXML template, not an imported user document.
// Every writer has explicit layout properties matching the original Python
// templates. Integer values are Word twips / half-points (9.75 pt is stored as
// 19 half-points by the source python-docx renderer, not rounded up to 20).
type run struct {
	text         string
	size         int
	bold, italic int // 0 = inherit, 1 = true, -1 = explicit false
	link         *materials.ContactLink
}
type tab struct {
	pos   int
	align string
}
type paragraph struct {
	runs                []run
	before, after, line int
	resume, keep        bool
	align, style        string
	tabs                []tab
	border              int
	bullet              bool
}
type document struct {
	paragraphs                  []paragraph
	title, subject, description string
	normalSize                  int
}

func validXMLText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if !(r == '\t' || r == '\n' || r == '\r' || r >= 0x20 && r <= 0xd7ff || r >= 0xe000 && r <= 0xfffd || r >= 0x10000 && r <= 0x10ffff) {
			return false
		}
	}
	return true
}
func escaped(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func onOff(name string, state int) string {
	if state > 0 {
		return "<w:" + name + "/>"
	}
	if state < 0 {
		return "<w:" + name + " w:val=\"0\"/>"
	}
	return ""
}
func (d *document) add(p paragraph) { d.paragraphs = append(d.paragraphs, p) }

func (p paragraph) properties() string {
	var b strings.Builder
	b.WriteString("<w:pPr>")
	if p.style != "" {
		fmt.Fprintf(&b, `<w:pStyle w:val="%s"/>`, p.style)
	}
	if p.resume {
		if p.keep {
			b.WriteString("<w:keepNext/>")
		} else {
			b.WriteString(`<w:keepNext w:val="0"/>`)
		}
		b.WriteString("<w:widowControl/>")
	}
	if p.border != 0 {
		fmt.Fprintf(&b, `<w:pBdr><w:bottom w:val="single" w:sz="%d" w:space="1" w:color="000000"/></w:pBdr>`, p.border)
	}
	if len(p.tabs) > 0 {
		b.WriteString("<w:tabs>")
		for _, t := range p.tabs {
			fmt.Fprintf(&b, `<w:tab w:val="%s" w:pos="%d"/>`, t.align, t.pos)
		}
		b.WriteString("</w:tabs>")
	}
	fmt.Fprintf(&b, `<w:spacing w:before="%d" w:after="%d" w:line="%d" w:lineRule="auto"/>`, p.before, p.after, p.line)
	if p.bullet {
		b.WriteString(`<w:ind w:left="288" w:hanging="288"/>`)
	}
	if p.align != "" {
		fmt.Fprintf(&b, `<w:jc w:val="%s"/>`, p.align)
	}
	b.WriteString("</w:pPr>")
	return b.String()
}

func writeRun(b *strings.Builder, r run) {
	b.WriteString(`<w:r><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/>`)
	b.WriteString(onOff("b", r.bold))
	b.WriteString(onOff("i", r.italic))
	if r.link != nil {
		fmt.Fprintf(b, `<w:color w:val="%s"/>`, r.link.Color)
	}
	fmt.Fprintf(b, `<w:sz w:val="%d"/>`, r.size)
	if r.link != nil {
		fmt.Fprintf(b, `<w:szCs w:val="%d"/><w:u w:val="single"/>`, r.size)
	}
	b.WriteString("</w:rPr>")
	// Match python-docx: both CR and LF are breaks; a CRLF pair is two breaks.
	start := 0
	flush := func(end int) {
		if start == end {
			return
		}
		b.WriteString(`<w:t xml:space="preserve">`)
		b.WriteString(escaped(r.text[start:end]))
		b.WriteString("</w:t>")
	}
	for i, c := range r.text {
		if c != '\t' && c != '\r' && c != '\n' {
			continue
		}
		flush(i)
		if c == '\t' {
			b.WriteString("<w:tab/>")
		} else {
			b.WriteString("<w:br/>")
		}
		start = i + 1
	}
	flush(len(r.text))
	b.WriteString("</w:r>")
}

const sectionXML = `<w:sectPr><w:pgSz w:w="12240" w:h="15840"/><w:pgMar w:top="720" w:right="720" w:bottom="720" w:left="720" w:header="216" w:footer="216" w:gutter="0"/><w:cols w:space="720"/><w:docGrid w:linePitch="360"/></w:sectPr>`

// Preserve the stock source template's script-specific font fallbacks. Fonts
// are named, not redistributed; native Word/LibreOffice must supply them.
//
//go:embed assets/theme1.xml
var themeXML string

const packageRelationships = xmlHead + `<Relationships xmlns="` + pkgNS + `"><Relationship Id="rId1" Type="` + rNS + `/officeDocument" Target="word/document.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/></Relationships>`
const contentTypes = xmlHead + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/><Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/><Override PartName="/word/settings.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"/><Override PartName="/word/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/></Types>`
const settingsXML = xmlHead + `<w:settings xmlns:w="` + wNS + `"><w:defaultTabStop w:val="720"/><w:characterSpacingControl w:val="doNotCompress"/><w:compat><w:useFELayout/><w:compatSetting w:name="compatibilityMode" w:uri="http://schemas.microsoft.com/office/word" w:val="14"/><w:compatSetting w:name="overrideTableStyleFontSizeAndJustification" w:uri="http://schemas.microsoft.com/office/word" w:val="1"/><w:compatSetting w:name="enableOpenTypeFeatures" w:uri="http://schemas.microsoft.com/office/word" w:val="1"/><w:compatSetting w:name="doNotFlipMirrorIndents" w:uri="http://schemas.microsoft.com/office/word" w:val="1"/></w:compat><w:themeFontLang w:val="en-US" w:eastAsia="ja-JP"/><w:clrSchemeMapping w:bg1="light1" w:t1="dark1" w:bg2="light2" w:t2="dark2" w:accent1="accent1" w:accent2="accent2" w:accent3="accent3" w:accent4="accent4" w:accent5="accent5" w:accent6="accent6" w:hyperlink="hyperlink" w:followedHyperlink="followedHyperlink"/><w:decimalSymbol w:val="."/><w:listSeparator w:val=","/></w:settings>`

// The source's List Bullet is numId 1 / abstractNumId 8, using the Symbol
// private-use bullet. Retain that glyph/font instead of inserting body text.
const numberingXML = xmlHead + `<w:numbering xmlns:w="` + wNS + `"><w:abstractNum w:abstractNumId="8"><w:multiLevelType w:val="singleLevel"/><w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:pStyle w:val="ListBullet"/><w:lvlText w:val=""/><w:lvlJc w:val="left"/><w:pPr><w:tabs><w:tab w:val="num" w:pos="360"/></w:tabs><w:ind w:left="360" w:hanging="360"/></w:pPr><w:rPr><w:rFonts w:ascii="Symbol" w:hAnsi="Symbol" w:hint="default"/></w:rPr></w:lvl></w:abstractNum><w:num w:numId="1"><w:abstractNumId w:val="8"/></w:num></w:numbering>`

func stylesXML(size int) string {
	return xmlHead + `<w:styles xmlns:w="` + wNS + `"><w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:asciiTheme="minorHAnsi" w:eastAsiaTheme="minorEastAsia" w:hAnsiTheme="minorHAnsi" w:cstheme="minorBidi"/><w:sz w:val="22"/><w:szCs w:val="22"/><w:lang w:val="en-US" w:eastAsia="en-US" w:bidi="ar-SA"/></w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="200" w:line="276" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults><w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/><w:sz w:val="` + strconv.Itoa(size) + `"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="ListBullet"><w:name w:val="List Bullet"/><w:basedOn w:val="Normal"/><w:uiPriority w:val="99"/><w:unhideWhenUsed/><w:pPr><w:numPr><w:numId w:val="1"/></w:numPr><w:contextualSpacing/></w:pPr></w:style><w:style w:type="paragraph" w:customStyle="1" w:styleId="CareerHubResumeBullet"><w:name w:val="Career Hub Resume Bullet"/><w:basedOn w:val="ListBullet"/><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri"/><w:sz w:val="19"/></w:rPr></w:style></w:styles>`
}

func (d document) bytes(ctx context.Context) ([]byte, *Error) {
	if !validXMLText(d.title) || !validXMLText(d.subject) || !validXMLText(d.description) {
		return nil, fail("MATERIAL_RENDER_XML_TEXT_INVALID")
	}
	var body, rels strings.Builder
	body.WriteString(xmlHead + `<w:document xmlns:w="` + wNS + `" xmlns:r="` + rNS + `"><w:body>`)
	rels.WriteString(xmlHead + `<Relationships xmlns="` + pkgNS + `"><Relationship Id="rId1" Type="` + rNS + `/styles" Target="styles.xml"/><Relationship Id="rId2" Type="` + rNS + `/numbering" Target="numbering.xml"/><Relationship Id="rId3" Type="` + rNS + `/settings" Target="settings.xml"/><Relationship Id="rId4" Type="` + rNS + `/theme" Target="theme/theme1.xml"/>`)
	linkIDs := map[string]string{}
	for _, p := range d.paragraphs {
		if e := ready(ctx); e != nil {
			return nil, e
		}
		body.WriteString("<w:p>" + p.properties())
		for _, r := range p.runs {
			if !validXMLText(r.text) {
				return nil, fail("MATERIAL_RENDER_XML_TEXT_INVALID")
			}
			if r.link == nil {
				writeRun(&body, r)
				continue
			}
			if !validXMLText(r.link.Target) {
				return nil, fail("MATERIAL_RENDER_XML_TEXT_INVALID")
			}
			id, found := linkIDs[r.link.Target]
			if !found {
				id = "rId" + strconv.Itoa(len(linkIDs)+5)
				linkIDs[r.link.Target] = id
				fmt.Fprintf(&rels, `<Relationship Id="%s" Type="%s/hyperlink" Target="%s" TargetMode="External"/>`, id, rNS, escaped(r.link.Target))
			}
			fmt.Fprintf(&body, `<w:hyperlink r:id="%s">`, id)
			writeRun(&body, r)
			body.WriteString("</w:hyperlink>")
		}
		body.WriteString("</w:p>")
		if body.Len() > MaxTextBytes {
			return nil, fail("MATERIAL_RENDER_ARTIFACT_LIMIT")
		}
	}
	body.WriteString(sectionXML + "</w:body></w:document>")
	rels.WriteString("</Relationships>")
	core := xmlHead + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>` + escaped(d.title) + `</dc:title><dc:subject>` + escaped(d.subject) + `</dc:subject><dc:creator></dc:creator><cp:lastModifiedBy></cp:lastModifiedBy><dc:description>` + escaped(d.description) + `</dc:description></cp:coreProperties>`
	parts := []struct{ name, text string }{
		{"[Content_Types].xml", contentTypes}, {"_rels/.rels", packageRelationships}, {"docProps/core.xml", core},
		{"word/document.xml", body.String()}, {"word/_rels/document.xml.rels", rels.String()},
		{"word/styles.xml", stylesXML(d.normalSize)}, {"word/numbering.xml", numberingXML}, {"word/settings.xml", settingsXML},
		{"word/theme/theme1.xml", themeXML},
	}
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, part := range parts {
		if e := ready(ctx); e != nil {
			return nil, e
		}
		h := &zip.FileHeader{Name: part.name, Method: zip.Deflate}
		// No clocks, machine identity, paths, prose hashes or randomness enter ZIP
		// metadata. The immutable manifest supplies the actual creation time.
		h.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		h.SetMode(0600)
		w, err := z.CreateHeader(h)
		if err != nil {
			return nil, fail("MATERIAL_RENDER_ZIP_INVALID")
		}
		if _, err = w.Write([]byte(part.text)); err != nil {
			return nil, fail("MATERIAL_RENDER_ZIP_INVALID")
		}
	}
	if z.Close() != nil {
		return nil, fail("MATERIAL_RENDER_ZIP_INVALID")
	}
	if out.Len() > MaxArtifactBytes {
		return nil, fail("MATERIAL_RENDER_ARTIFACT_LIMIT")
	}
	return out.Bytes(), nil
}
