package materialwrite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"careerhub.local/backend/internal/materialintegrity"
	"careerhub.local/backend/internal/materials"
)

// LegacyExportRequest selects only a previous Python export basename, never a
// general Data Home path. It cannot read a v1 artifact without its receipt.
type LegacyExportRequest struct{ JobID, Kind, Filename string }

func (LegacyExportRequest) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted legacy export request]")
}

type LegacyExportMetadata struct {
	JobID        string `json:"job_id"`
	Kind         string `json:"kind"`
	FileFormat   string `json:"format"`
	Filename     string `json:"filename"`
	ArtifactPath string `json:"artifact_path"`
	SizeBytes    int    `json:"size_bytes"`
	SHA256       string `json:"sha256"`
	Provenance   string `json:"provenance"`
}

func (LegacyExportMetadata) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "[redacted legacy export metadata]")
}

var legacyResumeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_]*_Resume_[A-Za-z0-9_]+\.(docx|pdf|txt)$`)
var legacyCoverName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_]*_Cover_Letter_[A-Za-z0-9_]+\.(docx|pdf|txt)$`)

// ReadLegacyExport downloads the unchanged bounded bytes, labeled unverified.
// No draft/source revision, claim validation, manifest, extraction or approval is
// invented. Old PDFs/DOCX are opaque downloads, not parser input or inline HTML.
// Callers must display legacy_unverified and retain human review.
func (s *Service) ReadLegacyExport(ctx context.Context, r LegacyExportRequest) (*Outcome, *Error) {
	if e := exportSelection(r.JobID, r.Kind); e != nil {
		return &Outcome{}, e
	}
	pattern := legacyResumeName
	if r.Kind == "cover_letter" {
		pattern = legacyCoverName
	}
	if len(r.Filename) > 254 || !pattern.MatchString(r.Filename) {
		return &Outcome{}, failed("request", "MATERIAL_LEGACY_EXPORT_NAME_INVALID")
	}
	return s.run(ctx, "", func(_ context.Context, _ *sql.Tx, data, _ *reads, _, _ string) (*operationProposal, *Error) {
		path := "outputs/application_materials/" + r.JobID + "/exports/" + r.Filename
		content, present, e := readExportFile(data, path, materialintegrity.MaxArtifactBytes)
		if e != nil {
			return nil, e
		}
		if !present || len(content) == 0 {
			return nil, failed("storage", "MATERIAL_LEGACY_EXPORT_UNAVAILABLE")
		}
		format := r.Filename[strings.LastIndexByte(r.Filename, '.')+1:]
		raw, _ := json.Marshal(LegacyExportMetadata{r.JobID, r.Kind, format, r.Filename, path, len(content), exportDigest(content), "legacy_unverified"})
		return &operationProposal{Proposal: &materials.Proposal{Result: raw}, content: content}, nil
	})
}
