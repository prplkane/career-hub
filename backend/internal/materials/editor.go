package materials

import (
	"context"
	"strings"

	"careerhub.local/backend/internal/textcompat"
)

type Preview struct {
	PlainText string `json:"plain_text"`
	WordCount int    `json:"word_count"`
}
type ExportOptions struct {
	SupportedFormats []string `json:"supported_formats"`
	VisualDOCXQA     string   `json:"visual_docx_qa"`
}
type CurrentSources struct {
	JobFingerprint   string `json:"job_fingerprint"`
	ProfileRevision  string `json:"profile_revision"`
	EvidenceRevision string `json:"evidence_revision"`
	PlanRevision     *int64 `json:"plan_revision,omitempty"`
	PlanFingerprint  string `json:"plan_fingerprint,omitempty"`
}
type SelectedEvidence struct {
	Evidence
	SelectedInPlan bool `json:"selected_in_plan"`
}
type Editor struct {
	Draft          any            `json:"draft"`
	Revision       int64          `json:"revision"`
	Persisted      bool           `json:"persisted"`
	CurrentSources CurrentSources `json:"current_sources"`
	SourceDefaults any            `json:"source_defaults"`
	Catalog        any            `json:"catalog"`
	Plan           any            `json:"plan,omitempty"`
	Validation     Validation     `json:"validation"`
	Preview        Preview        `json:"preview"`
	Exports        ExportOptions  `json:"exports"`
}

// ExportOptions describes the legacy formats, not activation of a Go exporter.
// No adapter may enable migrated exports until CH-8's integrity gate is complete.
func CoverEditor(ctx context.Context, d CoverDraft, s Sources, revision int64, persisted bool, letterDate string) (*Editor, *Error) {
	defaults, e := DefaultCover(s, letterDate)
	if e != nil {
		return nil, e
	}
	evidence, e := EvidenceCatalog(s.Catalog)
	if e != nil {
		return nil, e
	}
	v, e := ValidateCover(ctx, d, CoverRevisions{s.JobFingerprint, s.ProfileRevision, s.Catalog.VaultRevision}, evidence)
	if e != nil {
		return nil, e
	}
	text, e := CoverPlainText(d)
	if e != nil {
		return nil, e
	}
	out := &Editor{Draft: d, Revision: revision, Persisted: persisted, CurrentSources: CurrentSources{JobFingerprint: s.JobFingerprint, ProfileRevision: s.ProfileRevision, EvidenceRevision: s.Catalog.VaultRevision}, SourceDefaults: defaults, Catalog: map[string]any{"evidence": evidence}, Validation: *v, Preview: Preview{text, len(strings.FieldsFunc(text, textcompat.IsSpace))}, Exports: ExportOptions{[]string{"docx", "pdf", "txt"}, "unavailable_without_libreoffice"}}
	if _, e = JSON(out); e != nil {
		return nil, e
	}
	return out, nil
}
func ResumeEditor(ctx context.Context, d ResumeDraft, s Sources, revision int64, persisted bool) (*Editor, *Error) {
	defaults, e := DefaultResume(s)
	if e != nil {
		return nil, e
	}
	v, e := ValidateResume(ctx, d, s)
	if e != nil {
		return nil, e
	}
	text, e := ResumePlainText(d)
	if e != nil {
		return nil, e
	}
	evidence, e := EvidenceCatalog(s.Catalog)
	if e != nil {
		return nil, e
	}
	selected := allSelected(s.Plan.Plan)
	items := []SelectedEvidence{}
	for _, x := range evidence {
		if selected[x.ID] {
			items = append(items, SelectedEvidence{x, true})
		}
	}
	planRevision := s.Plan.Revision
	out := &Editor{Draft: d, Revision: revision, Persisted: persisted, CurrentSources: CurrentSources{JobFingerprint: s.Plan.Fingerprint, ProfileRevision: s.ProfileRevision, EvidenceRevision: s.Catalog.VaultRevision, PlanRevision: &planRevision, PlanFingerprint: defaults.PlanFingerprint}, SourceDefaults: defaults, Catalog: map[string]any{"evidence": items}, Plan: map[string]any{"status": s.Plan.Plan.Status, "persisted": s.Plan.Persisted, "revision": s.Plan.Revision, "summary": s.Plan.Summary}, Validation: *v, Preview: Preview{text, len(strings.FieldsFunc(text, textcompat.IsSpace))}, Exports: ExportOptions{[]string{"docx", "pdf", "txt"}, "unavailable_without_libreoffice"}}
	if _, e = JSON(out); e != nil {
		return nil, e
	}
	return out, nil
}
