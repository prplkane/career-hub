// Package materialrender renders the two existing code-defined templates. It
// has no filesystem, process, network, approval, or publication authority.
package materialrender

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/materials"
	"careerhub.local/backend/internal/materials/materialintegrity"
	"careerhub.local/backend/internal/textcompat"
)

const (
	RendererVersion  = "material-docx-pdf-txt-v2"
	ExtractorVersion = "material-docx-pdf-txt-extractor-v2"
	MaxArtifactBytes = materialintegrity.MaxArtifactBytes
	MaxTextBytes     = materialintegrity.MaxTextBytes
)

type Error struct{ Code string }

func (*Error) Error() string                { return "Application document rendering could not be completed." }
func (e *Error) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, e.Error()) }
func fail(code string) *Error               { return &Error{Code: code} }

// Result is private application data, not a diagnostic/manifest. Text is
// extracted from the returned bytes; ExpectedText is independently projected
// from the typed draft, not from the XML writer's paragraph/run list.
type Result struct {
	Kind, FileFormat, TemplateID string
	Content                      []byte
	ExpectedText, Text           string
	Pagination                   string
	PageCount                    int // Actual PDF pages; zero means unavailable/not paginated.
}

func (Result) Format(s fmt.State, _ rune) { _, _ = io.WriteString(s, "[redacted rendered material]") }

func ready(ctx context.Context) *Error {
	if ctx == nil || ctx.Err() != nil {
		return fail("MATERIAL_RENDER_CANCELLED")
	}
	return nil
}

func outputFormat(value string) (string, *Error) {
	v := strings.ToLower(textcompat.Strip(value))
	if v != "docx" && v != "txt" && v != "pdf" {
		return "", fail("MATERIAL_RENDER_FORMAT_UNSUPPORTED")
	}
	return v, nil
}

// RenderResume validates the typed model again but does not decide whether it
// is approved, source-current, or factually supported. The export owner must do
// those checks and bind the final result to its immutable source snapshot.
func RenderResume(ctx context.Context, draft materials.ResumeDraft, format string) (*Result, *Error) {
	if e := ready(ctx); e != nil {
		return nil, e
	}
	f, e := outputFormat(format)
	if e != nil {
		return nil, e
	}
	raw, err := json.Marshal(draft)
	if err != nil || len(raw) > materials.MaxInputBytes {
		return nil, fail("MATERIAL_RENDER_INPUT_LIMIT")
	}
	d, problem := materials.ParseResume(raw)
	if problem != nil {
		return nil, fail(problem.Code)
	}
	expected, e := resumeProjection(*d, f)
	if e != nil {
		return nil, e
	}
	var content []byte
	if f == "txt" {
		content = []byte(expected)
	} else if f == "pdf" {
		content, e = renderResumePDF(ctx, *d)
		if e != nil {
			return nil, e
		}
	} else {
		content, e = renderResumeDOCX(ctx, *d)
		if e != nil {
			return nil, e
		}
	}
	return finish(ctx, "resume", d.TemplateID, f, expected, content)
}

func RenderCover(ctx context.Context, draft materials.CoverDraft, format string) (*Result, *Error) {
	if e := ready(ctx); e != nil {
		return nil, e
	}
	f, e := outputFormat(format)
	if e != nil {
		return nil, e
	}
	raw, err := json.Marshal(draft)
	if err != nil || len(raw) > materials.MaxInputBytes {
		return nil, fail("MATERIAL_RENDER_INPUT_LIMIT")
	}
	d, problem := materials.ParseCover(raw)
	if problem != nil {
		return nil, fail(problem.Code)
	}
	expected, e := coverProjection(*d, f)
	if e != nil {
		return nil, e
	}
	var content []byte
	if f == "txt" {
		content = []byte(expected)
	} else if f == "pdf" {
		content, e = renderCoverPDF(ctx, *d)
		if e != nil {
			return nil, e
		}
	} else {
		content, e = renderCoverDOCX(ctx, *d)
		if e != nil {
			return nil, e
		}
	}
	return finish(ctx, "cover_letter", d.TemplateID, f, expected, content)
}

func finish(ctx context.Context, kind, template, format, expected string, content []byte) (*Result, *Error) {
	if e := ready(ctx); e != nil {
		return nil, e
	}
	var text string
	var e *Error
	pages := 0
	if format == "pdf" {
		text, pages, e = extractGeneratedPDF(ctx, content)
	} else {
		text, e = Extract(ctx, format, content)
	}
	if e != nil {
		return nil, e
	}
	comparison, problem := materialintegrity.CompareText(ctx, expected, text)
	if problem != nil {
		return nil, fail(problem.Code)
	}
	if !comparison.Match {
		return nil, fail("MATERIAL_RENDER_TEXT_MISMATCH")
	}
	pagination := "not_paginated"
	if format == "docx" {
		pagination = "layout_engine_unavailable"
	} else if format == "pdf" {
		pagination = "verified"
	}
	return &Result{Kind: kind, FileFormat: format, TemplateID: template, Content: content, ExpectedText: expected, Text: text, Pagination: pagination, PageCount: pages}, nil
}

// Extract is intentionally not a general DOCX importer. It accepts only this
// renderer's restricted, bounded OOXML profile (no macros, fields, images,
// headers, comments, hidden text, or external resources except contact links).
// It never follows a relationship or decompresses to the filesystem.
func Extract(ctx context.Context, format string, content []byte) (string, *Error) {
	if e := ready(ctx); e != nil {
		return "", e
	}
	if len(content) == 0 || len(content) > MaxArtifactBytes {
		return "", fail("MATERIAL_RENDER_ARTIFACT_LIMIT")
	}
	switch format {
	case "txt":
		if len(content) > MaxTextBytes || !utf8.Valid(content) || strings.ContainsRune(string(content), 0) {
			return "", fail("MATERIAL_RENDER_TEXT_INVALID")
		}
		return string(content), nil
	case "docx":
		return extractDOCX(ctx, content)
	default:
		return "", fail("MATERIAL_RENDER_FORMAT_UNSUPPORTED")
	}
}

func joinPresent(sep string, values ...string) string {
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			kept = append(kept, value)
		}
	}
	return strings.Join(kept, sep)
}
