package intakeextract

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestUploadValidationTextAndNames(t *testing.T) {
	for _, name := range []string{"resume.txt", "r%C3%A9sum%C3%A9.TXT", "a+b.txt"} {
		content := []byte("\xef\xbb\xbfFictional résumé\n")
		before := bytes.Clone(content)
		got, err := ValidateUpload(name, content)
		if err != nil || got == "" || !bytes.Equal(content, before) {
			t.Fatalf("valid upload refused or normalized: %s", name)
		}
	}
	for _, name := range []string{"", "../x.txt", "..%2fx.txt", "x%5Cy.txt", "x%00.txt", "x:y.txt", " x.txt", "x.txt ", "%ZZ.txt", "%ff.txt", strings.Repeat("a", 252) + ".txt", "resume.pdf"} {
		if _, err := ValidateUpload(name, []byte("text")); err == nil {
			t.Errorf("accepted unsafe name %q", name)
		}
	}
	for _, content := range [][]byte{nil, {0xff}, []byte("a\x00b"), bytes.Repeat([]byte("a"), MaxUploadBytes+1)} {
		if _, err := ValidateUpload("resume.txt", content); err == nil {
			t.Error("accepted invalid content")
		}
	}
	if _, err := ValidateUpload("resume.txt", bytes.Repeat([]byte("a"), MaxUploadBytes)); err != nil {
		t.Fatal("maximum rejected", err)
	}
}

func uploadZIP(t *testing.T, names []string, expanded int) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, name := range names {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if expanded > 0 {
			if _, err := f.Write(bytes.Repeat([]byte("x"), expanded)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUploadValidationDOCXEnvelopeNotXMLExtraction(t *testing.T) {
	parts := []string{"[Content_Types].xml", "word/document.xml"}
	// Upload validates the container only; extraction owns XML validation.
	if _, err := ValidateUpload("resume.docx", uploadZIP(t, parts, 1)); err != nil {
		t.Fatal(err)
	}
	for _, content := range [][]byte{[]byte("not ZIP"), uploadZIP(t, parts[:1], 1), uploadZIP(t, parts, 26<<20)} {
		if _, err := ValidateUpload("resume.docx", content); err == nil {
			t.Error("accepted malformed/oversized archive")
		}
	}
	tooMany := append(parts, make([]string, 2047)...)
	for i := 2; i < len(tooMany); i++ {
		tooMany[i] = "extra"
	}
	if _, err := ValidateUpload("resume.docx", uploadZIP(t, tooMany, 0)); err == nil {
		t.Error("accepted excessive entries")
	}
}
