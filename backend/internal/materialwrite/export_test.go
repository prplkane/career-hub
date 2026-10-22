package materialwrite

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"careerhub.local/backend/internal/materialintegrity"
	"careerhub.local/backend/internal/materialrender"
	"careerhub.local/backend/internal/materials"
)

func exportAssessment() materialintegrity.DraftAssessment {
	h := exportDigest([]byte("fixture"))
	revision := int64(1)
	return materialintegrity.DraftAssessment{
		Sources: materialintegrity.Sources{JobID: "job", Kind: "resume", MaterialID: "resume_primary", DraftRevision: 1, DraftSHA256: h, JobFingerprint: h,
			ProfileRevision: "sha256:" + h, EvidenceRevision: "sha256:" + h, PlanRevision: &revision, PlanFingerprint: "sha256:" + h, TemplateID: materials.ResumeTemplate,
			ProofIDs: []string{}, ManualCheckIDs: []string{}, PolicySHA256: h},
		Facts: materialintegrity.FactResult{Validation: materialintegrity.Validation{Status: "pass", Findings: []materialintegrity.Finding{}}},
	}
}

func TestExportManifestOwnerFormatGates(t *testing.T) {
	for _, test := range []struct {
		format, pagination string
		pages, target      int
		want               string
	}{
		{"txt", "not_paginated", 0, 1, "pass"},
		{"docx", "layout_engine_unavailable", 0, 1, "warning"},
		{"pdf", "verified", 1, 1, "pass"},
		{"pdf", "verified", 2, 1, "warning"},
		{"pdf", "verified", 2, 2, "pass"},
		{"pdf", "verified", 3, 2, "MATERIAL_EXPORT_HARD_PAGE_LIMIT"},
		{"pdf", "layout_engine_unavailable", 0, 1, "MATERIAL_EXPORT_PAGINATION_INVALID"},
	} {
		t.Run(fmt.Sprintf("%s-%d-%d", test.format, test.pages, test.target), func(t *testing.T) {
			// This tests owner policy, not PDF rendering/extraction qualification.
			r := &materialrender.Result{Kind: "resume", TemplateID: materials.ResumeTemplate, FileFormat: test.format, Content: []byte("fixture bytes"), ExpectedText: "Exact text", Text: "Exact text", PageCount: test.pages, Pagination: test.pagination}
			m, e := exportManifest(context.Background(), exportAssessment(), r, test.target, "2026-09-12T12:00:00Z")
			if strings.HasPrefix(test.want, "MATERIAL_") {
				if e == nil || e.Code != test.want || m != nil {
					t.Fatalf("wanted %s", test.want)
				}
				return
			}
			if e != nil || m == nil || m.Identity.Validation.Status != test.want {
				t.Fatalf("owner policy: %v", e)
			}
			if m.Identity.SHA256 != exportDigest(r.Content) || !strings.Contains(m.Identity.RendererVersion, materialrender.ExtractorVersion) {
				t.Fatal("bytes/extractor not pinned")
			}
		})
	}
	r := &materialrender.Result{Kind: "resume", TemplateID: materials.ResumeTemplate, FileFormat: "txt", Content: []byte("changed text"), ExpectedText: "Exact text", Text: "changed text", Pagination: "not_paginated"}
	if _, e := exportManifest(context.Background(), exportAssessment(), r, 1, "2026-09-12T12:00:00Z"); e == nil || e.Code != "MATERIAL_FINAL_TEXT_MISMATCH" {
		t.Fatal("final text mismatch accepted")
	}
	a := exportAssessment()
	a.Facts.Validation.Status = "block"
	if _, e := exportManifest(context.Background(), a, r, 1, "2026-09-12T12:00:00Z"); e == nil || e.Code != "MATERIAL_EXPORT_FACTS_BLOCKED" {
		t.Fatal("factual block bypassed")
	}
}

func TestExportNamesAndRedaction(t *testing.T) {
	for _, test := range []struct{ input, fallback, want string }{
		{"  Zoë O'Neil ", "Candidate", "Zo_O_Neil"},
		{"会社", "Company", "Company"},
		{strings.Repeat("Z", 81), "Role", strings.Repeat("Z", 80)},
		{"../C:\\Example", "Candidate", "C_Example"},
	} {
		if got := filenameToken(test.input, test.fallback); got != test.want {
			t.Fatalf("name token %q", got)
		}
	}
	for _, value := range []any{Outcome{Content: []byte("private-marker")}, ExportRequest{JobID: "private-marker"}, ExportReadRequest{JobID: "private-marker"}, LegacyExportRequest{Filename: "private-marker"}, ExportMetadata{Filename: "private-marker"}, LegacyExportMetadata{Filename: "private-marker"}} {
		if strings.Contains(fmt.Sprintf("%+v", value), "private-marker") {
			t.Fatal("private export diagnostic leak")
		}
	}
}

func FuzzExportReceipt(f *testing.F) {
	id := "material_" + strings.Repeat("a", 64)
	f.Add(receiptBytes(id, []byte("fixture")))
	f.Add([]byte(`{"schema_version":1}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		r, e := decodeReceipt(raw, id)
		if e == nil && (r.ArtifactID != id || !exportHash.MatchString(r.ManifestSHA256)) {
			t.Fatal("invalid receipt identity")
		}
	})
}
