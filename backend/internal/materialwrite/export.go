package materialwrite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"careerhub.local/backend/internal/materialintegrity"
	"careerhub.local/backend/internal/materialrender"
	"careerhub.local/backend/internal/materials"
	"careerhub.local/backend/internal/textcompat"
)

// ExportRequest selects a saved primary draft. No client draft, source revision,
// approval flag, output path or manual fact check can authorize an export.
type ExportRequest struct{ JobID, Kind, FileFormat string }
type ExportReadRequest struct{ JobID, Kind, ArtifactID string }

func (ExportRequest) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted material export request]")
}
func (ExportReadRequest) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted material download request]")
}

type ExportMetadata struct {
	JobID        string                           `json:"job_id"`
	Kind         string                           `json:"kind"`
	FileFormat   string                           `json:"format"`
	Revision     int64                            `json:"revision"`
	Filename     string                           `json:"filename"`
	ArtifactID   string                           `json:"artifact_id"`
	ArtifactPath string                           `json:"artifact_path"`
	ManifestPath string                           `json:"manifest_path"`
	CreatedAt    string                           `json:"created_at"`
	SizeBytes    int64                            `json:"size_bytes"`
	SHA256       string                           `json:"sha256"`
	Provenance   string                           `json:"provenance"`
	Validation   materialintegrity.Validation     `json:"validation"`
	Pagination   materialintegrity.FormatMetadata `json:"pagination"`
}

func (ExportMetadata) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted material export metadata]")
}

func exportDigest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func exportSelection(job, kind string) *Error {
	if e := materials.ValidateRequest(materials.Request{JobID: job, Kind: kind, Operation: "load"}); e != nil {
		return failed("request", "MATERIAL_EXPORT_REQUEST_INVALID")
	}
	return nil
}

func normalizeExportFormat(value string) (string, *Error) {
	f := strings.ToLower(textcompat.Strip(value))
	if f != "docx" && f != "pdf" && f != "txt" {
		return "", failed("request", "MATERIAL_RENDER_FORMAT_UNSUPPORTED")
	}
	return f, nil
}

// Export renders under the same source/SQL lifetime as review and draft editing.
// Content is exposed only after the last-written receipt and final readback.
// Committed is true only when this call publishes a completion receipt. Partial
// may remain true on an interrupted preparation; an exact retry resumes it.
func (s *Service) Export(ctx context.Context, resources string, r ExportRequest) (*Outcome, *Error) {
	if e := exportSelection(r.JobID, r.Kind); e != nil {
		return &Outcome{}, e
	}
	f, e := normalizeExportFormat(r.FileFormat)
	if e != nil {
		return &Outcome{}, e
	}
	if resources == "" {
		return &Outcome{}, failed("configuration", "MATERIAL_FACT_REGISTRY_REQUIRED")
	}
	return s.run(ctx, resources, func(ctx context.Context, tx *sql.Tx, data, core *reads, clock, _ string) (*operationProposal, *Error) {
		saved, e := materialintegrity.ReadAssessment(ctx, tx, core, data, r.JobID, r.Kind, clock)
		if e != nil {
			return nil, failed("integrity", e.Code)
		}
		if saved.Assessment.Facts.Validation.Status == "block" {
			return nil, failed("integrity", "MATERIAL_EXPORT_FACTS_BLOCKED")
		}
		rendered, target, filename, pe := renderSaved(ctx, saved, f)
		if pe != nil {
			return nil, pe
		}
		manifest, pe := exportManifest(ctx, saved.Assessment, rendered, target, clock)
		if pe != nil {
			return nil, pe
		}
		bundle, pe := prepareExport(data, manifest, rendered.Content)
		if pe != nil {
			return nil, pe
		}
		proposal := exportProposal(bundle.manifest, filename, bundle.content)
		proposal.publish = bundle.publish
		return proposal, nil
	})
}

// ReadExport never rerenders or repairs storage. A receipt is mandatory, and
// saved draft/proof/JD/registry/manual-review sources are assessed again. It does
// not parse persisted PDFs: those bytes were checked before receipt publication
// and are now bound to that separately retained manifest digest.
func (s *Service) ReadExport(ctx context.Context, resources string, r ExportReadRequest) (*Outcome, *Error) {
	if e := exportSelection(r.JobID, r.Kind); e != nil {
		return &Outcome{}, e
	}
	if !exportID.MatchString(r.ArtifactID) {
		return &Outcome{}, failed("request", "MATERIAL_EXPORT_REQUEST_INVALID")
	}
	if resources == "" {
		return &Outcome{}, failed("configuration", "MATERIAL_FACT_REGISTRY_REQUIRED")
	}
	return s.run(ctx, resources, func(ctx context.Context, tx *sql.Tx, data, core *reads, clock, _ string) (*operationProposal, *Error) {
		saved, e := materialintegrity.ReadAssessment(ctx, tx, core, data, r.JobID, r.Kind, clock)
		if e != nil {
			return nil, failed("integrity", e.Code)
		}
		if saved.Assessment.Facts.Validation.Status == "block" {
			return nil, failed("integrity", "MATERIAL_EXPORT_FACTS_BLOCKED")
		}
		bundle, pe := readExport(data, r, saved.Assessment.Sources)
		if pe != nil {
			return nil, pe
		}
		filename, pe := savedFilename(saved, bundle.manifest.Identity.Format)
		if pe != nil {
			return nil, pe
		}
		return exportProposal(bundle.manifest, filename, bundle.content), nil
	})
}

func exportProposal(m *materialintegrity.Manifest, filename string, content []byte) *operationProposal {
	id := m.Identity
	p := exportPaths(id.Sources.JobID, m.ArtifactID, id.Filename)
	meta := ExportMetadata{JobID: id.Sources.JobID, Kind: id.Sources.Kind, FileFormat: id.Format,
		Revision: id.Sources.DraftRevision, Filename: filename, ArtifactID: m.ArtifactID,
		ArtifactPath: p.artifact, ManifestPath: p.manifest, CreatedAt: m.CreatedAt,
		SizeBytes: id.SizeBytes, SHA256: id.SHA256, Provenance: "versioned",
		Validation: id.Validation, Pagination: id.Metadata}
	raw, _ := json.Marshal(meta) // closed, already validated scalar/slice model
	return &operationProposal{Proposal: &materials.Proposal{Result: raw}, content: content}
}

var filenameCharacters = regexp.MustCompile(`[^A-Za-z0-9]+`)

func filenameToken(value, fallback string) string {
	v := strings.Trim(filenameCharacters.ReplaceAllString(value, "_"), "_")
	if len(v) > 80 {
		v = v[:80]
	}
	if v == "" {
		return fallback
	}
	return v
}

// Preserve Python download names; private disk names are fixed kind.format.
func savedFilename(saved *materialintegrity.SavedAssessment, format string) (string, *Error) {
	if saved.Assessment.Sources.Kind == "resume" {
		d, e := materials.ParseResume(saved.Payload)
		if e != nil {
			return "", failed("draft", e.Code)
		}
		return filenameToken(d.Candidate.Name, "Candidate") + "_Resume_" + filenameToken(d.Company, "Company") + "_" + filenameToken(d.RoleTitle, "Role") + "." + format, nil
	}
	d, e := materials.ParseCover(saved.Payload)
	if e != nil {
		return "", failed("draft", e.Code)
	}
	return filenameToken(d.Candidate.Name, "Candidate") + "_Cover_Letter_" + filenameToken(d.Company, "Company") + "." + format, nil
}

func renderSaved(ctx context.Context, saved *materialintegrity.SavedAssessment, format string) (*materialrender.Result, int, string, *Error) {
	filename, pe := savedFilename(saved, format)
	if pe != nil {
		return nil, 0, "", pe
	}
	var rendered *materialrender.Result
	var e *materialrender.Error
	target := 1
	if saved.Assessment.Sources.Kind == "resume" {
		d, _ := materials.ParseResume(saved.Payload) // parsed by savedFilename
		target = d.TargetPages
		rendered, e = materialrender.RenderResume(ctx, *d, format)
	} else {
		d, _ := materials.ParseCover(saved.Payload)
		rendered, e = materialrender.RenderCover(ctx, *d, format)
	}
	if e != nil {
		return nil, 0, "", failed("render", e.Code)
	}
	return rendered, target, filename, nil
}

func exportManifest(ctx context.Context, assessment materialintegrity.DraftAssessment, rendered *materialrender.Result, target int, clock string) (*materialintegrity.Manifest, *Error) {
	if rendered == nil || rendered.Kind != assessment.Sources.Kind || rendered.TemplateID != assessment.Sources.TemplateID {
		return nil, failed("render", "MATERIAL_EXPORT_RENDER_IDENTITY")
	}
	validation := assessment.Facts.Validation
	validation.Findings = append([]materialintegrity.Finding{}, validation.Findings...)
	if validation.Status == "block" {
		return nil, failed("integrity", "MATERIAL_EXPORT_FACTS_BLOCKED")
	}
	check, e := materialintegrity.CompareText(ctx, rendered.ExpectedText, rendered.Text)
	if e != nil {
		return nil, failed("integrity", e.Code)
	}
	if !check.Match {
		return nil, failed("integrity", "MATERIAL_FINAL_TEXT_MISMATCH")
	}
	meta := materialintegrity.FormatMetadata{TargetPages: target, PageCountUnavailable: rendered.Pagination, PageBudget: "unavailable"}
	warn := func(code string) {
		validation.Status = "warning"
		validation.Findings = append(validation.Findings, materialintegrity.Finding{Severity: "warning", Code: code, DraftPath: "artifact"})
	}
	if rendered.FileFormat == "pdf" {
		if rendered.Pagination != "verified" || rendered.PageCount < 1 {
			return nil, failed("render", "MATERIAL_EXPORT_PAGINATION_INVALID")
		}
		if rendered.Kind == "resume" && rendered.PageCount > 2 {
			return nil, failed("integrity", "MATERIAL_EXPORT_HARD_PAGE_LIMIT")
		}
		pages := rendered.PageCount
		meta.PageCount, meta.PageCountUnavailable, meta.PageBudget = &pages, "", "pass"
		if pages > target {
			meta.PageBudget = "warning"
			warn("PAGE_BUDGET_EXCEEDED")
		}
	} else if rendered.FileFormat == "docx" {
		warn("PAGINATION_UNAVAILABLE")
	}
	created, err := time.Parse(time.RFC3339, clock)
	if err != nil {
		return nil, failed("configuration", "MATERIAL_EXPORT_CLOCK_INVALID")
	}
	id := materialintegrity.Identity{Sources: assessment.Sources,
		RendererVersion:  materialrender.RendererVersion + "." + materialrender.ExtractorVersion,
		ValidatorVersion: materialintegrity.ValidatorVersion, Format: rendered.FileFormat,
		Filename: rendered.Kind + "." + rendered.FileFormat, SizeBytes: int64(len(rendered.Content)),
		SHA256: exportDigest(rendered.Content), TextSHA256: check.ActualSHA256, Metadata: meta, Validation: validation}
	m, e := materialintegrity.NewManifest(id, created)
	if e != nil {
		return nil, failed("integrity", e.Code)
	}
	return m, nil
}
