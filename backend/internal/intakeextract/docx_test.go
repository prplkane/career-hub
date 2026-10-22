package intakeextract

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
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
