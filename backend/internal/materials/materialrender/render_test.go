package materialrender

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"careerhub.local/backend/internal/materials"
	"careerhub.local/backend/internal/materials/materialintegrity"
)

func exampleCover() materials.CoverDraft {
	return materials.CoverDraft{SchemaVersion: 1, ID: "cover_letter_primary", Kind: "cover_letter", TemplateID: materials.CoverTemplate, JobID: "example", JobFingerprint: strings.Repeat("a", 64), ProfileRevision: "sha256:" + strings.Repeat("b", 64), EvidenceRevision: "sha256:" + strings.Repeat("c", 64), Status: "draft", Company: "Example Company", RoleTitle: "Engineer", Candidate: materials.Candidate{Name: "Example Person", Email: "example@example.test"}, Content: materials.CoverContent{Date: "2026-09-12", Salutation: "Dear Hiring Team:", Opening: "I build reliable services.", EvidenceLeadIn: "My work:", EvidenceItems: []materials.CoverEvidence{{ID: "proof_1", Label: "Delivery", Claim: "Built services.", EvidenceRefs: []string{}, ManualChecks: []string{}, Origin: "user"}}, Closing: "Thank you.", SignOff: "Sincerely,", SignatureName: "Example Person"}}
}

func TestDeterministicCoverAndActualExtraction(t *testing.T) {
	d := exampleCover()
	for _, format := range []string{"docx", "txt"} {
		t.Run(format, func(t *testing.T) {
			a, e := RenderCover(context.Background(), d, format)
			if e != nil {
				t.Fatal(e.Code)
			}
			b, e := RenderCover(context.Background(), d, " \t"+strings.ToUpper(format)+"\n")
			if e != nil {
				t.Fatal(e.Code)
			}
			if !bytes.Equal(a.Content, b.Content) {
				t.Fatal("render is not byte deterministic")
			}
			if a.Text == "" || !strings.Contains(a.Text, d.Content.Opening) {
				t.Fatal("missing actual text")
			}
			if format == "docx" && a.Pagination != "layout_engine_unavailable" {
				t.Fatal("invented pagination")
			}
			if format == "txt" && a.Pagination != "not_paginated" {
				t.Fatal("TXT pagination")
			}
			if strings.Contains(fmt.Sprintf("%+v", a), d.Candidate.Email) || strings.Contains(fmt.Sprintf("%#v", a), d.Content.Opening) {
				t.Fatal("result formatting leaks prose")
			}
		})
	}
}

func rewritePart(t *testing.T, original []byte, target string, change func([]byte) []byte, extraName string) []byte {
	t.Helper()
	z, e := zip.NewReader(bytes.NewReader(original), int64(len(original)))
	if e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, f := range z.File {
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		raw, e := io.ReadAll(r)
		_ = r.Close()
		if e != nil {
			t.Fatal(e)
		}
		if f.Name == target {
			raw = change(raw)
		}
		part, e := w.Create(f.Name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = part.Write(raw); e != nil {
			t.Fatal(e)
		}
	}
	if extraName != "" {
		part, e := w.Create(extraName)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = part.Write([]byte("extra"))
	}
	if w.Close() != nil {
		t.Fatal("close zip")
	}
	return out.Bytes()
}

func TestDOCXFinalTextDoesNotTrustProjection(t *testing.T) {
	d := exampleCover()
	rendered, e := RenderCover(context.Background(), d, "docx")
	if e != nil {
		t.Fatal(e.Code)
	}
	changed := rewritePart(t, rendered.Content, "word/document.xml", func(b []byte) []byte {
		return bytes.ReplaceAll(b, []byte(d.Content.Opening), []byte("Different final text."))
	}, "")
	text, e := Extract(context.Background(), "docx", changed)
	if e != nil {
		t.Fatal(e.Code)
	}
	check, problem := materialintegrity.CompareText(context.Background(), rendered.ExpectedText, text)
	if problem != nil || check.Match {
		t.Fatal("extractor reused the expected draft instead of reading the final file")
	}
}

func TestDOCXRejectsUnsupportedOrHostilePackage(t *testing.T) {
	d := exampleCover()
	d.Candidate.Website = "https://example.test/person"
	r, e := RenderCover(context.Background(), d, "docx")
	if e != nil {
		t.Fatal(e.Code)
	}
	cases := []struct{ name, part, old, new, extra string }{
		{"hidden-run", "word/document.xml", "<w:rPr>", "<w:rPr><w:vanish/>", ""},
		{"field", "word/document.xml", "<w:t ", "<w:instrText ", ""},
		{"white-text", "word/document.xml", "<w:sz ", `<w:color w:val="FFFFFF"/><w:sz `, ""},
		{"tiny-text", "word/document.xml", `w:val="21"`, `w:val="1"`, ""},
		{"remote-template", "word/_rels/document.xml.rels", "/hyperlink", "/attachedTemplate", ""},
		{"unsafe-contact", "word/_rels/document.xml.rels", "https://example.test/person", "file:///private.txt", ""},
		{"field-style", "word/styles.xml", "Calibri", "Wingdings", ""},
		{"DTD", "word/document.xml", xmlHead, xmlHead + `<!DOCTYPE document [<!ENTITY x "private">]>`, ""},
		{"duplicate-attribute", "word/document.xml", `w:ascii="Calibri"`, `w:ascii="Calibri" w:ascii="Calibri"`, ""},
		{"unexpected-attribute", "word/document.xml", "<w:r>", `<w:r hidden="yes">`, ""},
		{"outside-body-text", "word/document.xml", "<w:body>", "<w:body>unexpected prose", ""},
		{"header", "", "", "", "word/header1.xml"},
		{"macro", "", "", "", "word/vbaProject.bin"},
		{"path-traversal", "", "", "", "../escaped.xml"},
		{"duplicate-part", "", "", "", "word/document.xml"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := rewritePart(t, r.Content, c.part, func(b []byte) []byte { return bytes.ReplaceAll(b, []byte(c.old), []byte(c.new)) }, c.extra)
			if _, e := Extract(context.Background(), "docx", b); e == nil {
				t.Fatal("unsupported content accepted")
			}
		})
	}
	truncated := r.Content[:len(r.Content)-20]
	if _, e := Extract(context.Background(), "docx", truncated); e == nil {
		t.Fatal("truncated ZIP accepted")
	}
	expanded := rewritePart(t, r.Content, "word/document.xml", func([]byte) []byte { return bytes.Repeat([]byte("x"), MaxTextBytes+1) }, "")
	if _, e := Extract(context.Background(), "docx", expanded); e == nil {
		t.Fatal("decompression bound missing")
	}
}

func TestRenderLimitsCancellationAndNoImplicitFallback(t *testing.T) {
	d := exampleCover()
	for _, f := range []string{"html", "docm", "", "../docx"} {
		if _, e := RenderCover(context.Background(), d, f); e == nil || e.Code != "MATERIAL_RENDER_FORMAT_UNSUPPORTED" {
			t.Fatal("unqualified format fallback", f)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := RenderCover(ctx, d, "docx"); e == nil || e.Code != "MATERIAL_RENDER_CANCELLED" {
		t.Fatal("cancelled render")
	}
	if _, e := RenderCover(nil, d, "txt"); e == nil {
		t.Fatal("nil context")
	}
	d.TemplateID = "different-template"
	if _, e := RenderCover(context.Background(), d, "docx"); e == nil {
		t.Fatal("unknown template")
	}
	d = exampleCover()
	d.Content.Opening = "a\x01b"
	if _, e := RenderCover(context.Background(), d, "docx"); e == nil || e.Code != "MATERIAL_RENDER_XML_TEXT_INVALID" {
		t.Fatal("XML-invalid control silently replaced")
	}
	d = exampleCover()
	d.Candidate.Website = "javascript:alert(1)"
	if _, e := RenderCover(context.Background(), d, "docx"); e == nil {
		t.Fatal("unsafe link")
	}
	for _, b := range [][]byte{{0xff}, {0}, bytes.Repeat([]byte("x"), MaxArtifactBytes+1), nil} {
		if _, e := Extract(context.Background(), "txt", b); e == nil {
			t.Fatal("invalid TXT accepted")
		}
	}
}

func FuzzExtractRejectsWithoutPanic(f *testing.F) {
	f.Add([]byte("not a ZIP"), true)
	f.Add([]byte("bounded text"), false)
	r, e := RenderCover(context.Background(), exampleCover(), "docx")
	if e != nil {
		f.Fatal(e.Code)
	}
	f.Add(r.Content, true)
	f.Fuzz(func(t *testing.T, raw []byte, docx bool) {
		format := "txt"
		if docx {
			format = "docx"
		}
		if len(raw) > MaxArtifactBytes+1 {
			t.Skip()
		}
		_, _ = Extract(context.Background(), format, raw)
	})
}

func TestJSONRoundTripDoesNotAddAuthority(t *testing.T) {
	d := exampleCover()
	raw, _ := json.Marshal(d)
	parsed, e := materials.ParseCover(raw)
	if e != nil {
		t.Fatal(e.Code)
	}
	if parsed.Status != "draft" {
		t.Fatal("fixture status")
	}
	if _, e := RenderCover(context.Background(), *parsed, "txt"); e != nil {
		t.Fatal(e.Code)
	}
	if parsed.Status != "draft" {
		t.Fatal("renderer changed approval")
	}
}
