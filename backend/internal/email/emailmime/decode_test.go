package emailmime

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPythonSourceMIMECases(t *testing.T) {
	t.Parallel()
	fixtureRoot := filepath.Join("..", "..", "..", "..", "tests", "fixtures")
	raw, err := os.ReadFile(filepath.Join(fixtureRoot, "migration_email_mime_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name         string `json:"name"`
			RawBase64    string `json:"raw_b64"`
			AllTextChars int    `json:"all_text_chars"`
			Selected     []struct {
				Type   string `json:"type"`
				Chars  int    `json:"chars"`
				SHA256 string `json:"sha256"`
			} `json:"selected"`
			Warnings []string `json:"warnings"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, test := range fixture.Cases {
		t.Run(test.Name, func(t *testing.T) {
			var content []byte
			var err error
			if test.RawBase64 != "" {
				content, err = base64.StdEncoding.DecodeString(test.RawBase64)
			} else {
				content, err = os.ReadFile(filepath.Join(fixtureRoot, "email_alerts", test.Name))
			}
			if err != nil {
				t.Fatal(err)
			}
			result, err := Decode(content)
			if err != nil {
				t.Fatal(err)
			}
			if result.AllTextChars != test.AllTextChars {
				t.Errorf("all text chars = %d, want %d", result.AllTextChars, test.AllTextChars)
			}
			if !slices.Equal(result.Warnings, test.Warnings) {
				t.Errorf("warnings = %v, want %v", result.Warnings, test.Warnings)
			}
			if len(result.Selected) != len(test.Selected) {
				t.Fatalf("selected parts = %d, want %d", len(result.Selected), len(test.Selected))
			}
			for i, part := range result.Selected {
				want := test.Selected[i]
				if part.ContentType != want.Type || utf8.RuneCountInString(part.Text) != want.Chars ||
					fmt.Sprintf("%x", sha256.Sum256([]byte(part.Text))) != want.SHA256 {
					t.Errorf("part %d: type=%q, chars=%d, sha256=%x", i, part.ContentType,
						utf8.RuneCountInString(part.Text), sha256.Sum256([]byte(part.Text)))
				}
			}
		})
	}
}

func TestByteAndDecodedTextBounds(t *testing.T) {
	t.Parallel()
	prefix := []byte("Content-Type: application/octet-stream\r\nContent-Disposition: attachment\r\n\r\n")
	exact := append(prefix, bytes.Repeat([]byte{'x'}, MaxEmailBytes-len(prefix))...)
	if _, err := Decode(exact); err != nil {
		t.Fatalf("exact byte bound: %v", err)
	}
	if _, err := Decode(append(exact, 'x')); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over byte bound: %v", err)
	}
	if _, err := Decode(nil); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty input: %v", err)
	}

	textPrefix := []byte("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	textExact := append(textPrefix, bytes.Repeat([]byte{'x'}, MaxDecodedTextChars)...)
	result, err := Decode(textExact)
	if err != nil || result.AllTextChars != MaxDecodedTextChars {
		t.Fatalf("exact text bound: %d, %v", result.AllTextChars, err)
	}
	if _, err := Decode(append(textExact, 'x')); !errors.Is(err, ErrTextLimit) {
		t.Fatalf("over text bound: %v", err)
	}
}

func multipartMessage(t *testing.T, partCount int) []byte {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for i := 0; i < partCount-1; i++ {
		if _, err := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/octet-stream"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return []byte(fmt.Sprintf("Content-Type: multipart/mixed; boundary=%q\r\n\r\n%s", writer.Boundary(), body.String()))
}

func nestedMessage(depth int) []byte {
	message := []byte("From: nested@example.test\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n")
	for i := 0; i < depth; i++ {
		message = append([]byte("From: nested@example.test\r\nContent-Type: message/rfc822\r\n\r\n"), message...)
	}
	return message
}

func TestStructureBoundsAndMalformedMultipart(t *testing.T) {
	t.Parallel()
	if _, err := Decode(multipartMessage(t, MaxMIMEParts)); err != nil {
		t.Fatalf("exact part bound: %v", err)
	}
	if _, err := Decode(multipartMessage(t, MaxMIMEParts+1)); !errors.Is(err, ErrPartLimit) {
		t.Fatalf("over part bound: %v", err)
	}
	if _, err := Decode(nestedMessage(MaxNestedDepth)); err != nil {
		t.Fatalf("exact nesting bound: %v", err)
	}
	if _, err := Decode(nestedMessage(MaxNestedDepth + 1)); !errors.Is(err, ErrNestingLimit) {
		t.Fatalf("over nesting bound: %v", err)
	}
	malformed := []byte("From: sender@example.test\r\nContent-Type: multipart/mixed; boundary=missing\r\n\r\nThis body has no declared boundary.")
	if _, err := Decode(malformed); !errors.Is(err, ErrParseFailed) {
		t.Fatalf("malformed multipart: %v", err)
	}
}

func TestAlternativeCountsUnselectedText(t *testing.T) {
	t.Parallel()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, test := range []struct{ kind, text string }{
		{"text/plain", strings.Repeat("x", 260_000)},
		{"text/html", strings.Repeat("y", 260_000)},
	} {
		part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {test.kind + "; charset=utf-8"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(part, test.text); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	content := []byte(fmt.Sprintf("Content-Type: multipart/alternative; boundary=%q\r\n\r\n%s", writer.Boundary(), body.String()))
	if _, err := Decode(content); !errors.Is(err, ErrTextLimit) {
		t.Fatalf("discarded alternative exceeded text limit: %v", err)
	}
}
