package materials

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"careerhub.local/backend/internal/fixedregex"
	"careerhub.local/backend/internal/textcompat"
)

type Evidence struct {
	ID               string   `json:"id"`
	SourceType       string   `json:"source_type"`
	SourceID         string   `json:"source_id"`
	SourceLabel      string   `json:"source_label"`
	Claim            string   `json:"claim"`
	MatchedJobSkills []string `json:"matched_job_skills"`
}
type CoverRevisions struct{ Job, Profile, Evidence string }

var placeholders = fixedregex.MustCompile(map[string]fixedregex.Definition{
	"cover":  {Pattern: `(?:\[(?:company|role|position|add|insert|todo)[^\]]*\]|<(?:company|role|position|add|insert|todo)[^>]*>|\bTODO\b)`, IgnoreCase: true},
	"resume": {Pattern: `(?:\[(?:add|insert|todo|metric|company|role)[^\]]*\]|<(?:add|insert|todo|metric|company|role)[^>]*>|\bTODO\b)`, IgnoreCase: true},
})

func placeholder(text, kind string) (bool, *Error) {
	m, e := placeholders.ForSource(text)
	if e != nil {
		return false, failure("input", "MATERIAL_TEXT_UNSUPPORTED")
	}
	t, e := m.Text(text)
	if e != nil {
		return false, failure("input", "MATERIAL_TEXT_UNSUPPORTED")
	}
	return t.Has(kind), nil
}

type validation struct {
	result Validation
	err    *Error
}

func newValidation() *validation { return &validation{result: Validation{Issues: []Issue{}}} }
func (v *validation) add(code, message, path string, save, export bool) {
	if len(v.result.Issues) >= 1000 {
		v.err = failure("validation", "MATERIAL_ISSUE_LIMIT")
		return
	}
	v.result.Issues = append(v.result.Issues, Issue{code, message, path, save, export})
	if save {
		v.result.BlockingSaveCount++
	}
	if export {
		v.result.BlockingExportCount++
	}
}
func (v *validation) finish(ctx context.Context) (*Validation, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("context", "MATERIAL_CANCELED")
	}
	if v.err != nil {
		return nil, v.err
	}
	v.result.IssueCount = len(v.result.Issues)
	v.result.Ready = v.result.BlockingExportCount == 0
	if _, e := JSON(v.result); e != nil {
		return nil, e
	}
	return &v.result, nil
}
func stringSet(list []string) map[string]bool {
	out := map[string]bool{}
	for _, x := range list {
		out[x] = true
	}
	return out
}
func absent(list []string, available map[string]bool) []string {
	out := []string{}
	for x := range stringSet(list) {
		if !available[x] {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}
func (v *validation) prose(text, path string) {
	for _, spec := range []struct{ char, code, label string }{{"–", "APPLICANT_PROSE_EN_DASH", "en dash"}, {"—", "APPLICANT_PROSE_EM_DASH", "em dash"}} {
		if strings.Contains(text, spec.char) {
			v.add(spec.code, "Applicant prose must not contain an "+spec.label+".", path, true, true)
		}
	}
	folded := textcompat.Fold(text)
	for _, phrase := range []string{"I am writing to apply", "I am writing to express my interest", "I am excited to apply", "I would welcome the opportunity"} {
		if strings.Contains(folded, textcompat.Fold(phrase)) {
			v.add("APPLICANT_PROSE_STOCK_PHRASE", `Applicant prose must not use the stock phrase "`+phrase+`".`, path, true, true)
		}
	}
}
func (v *validation) checkPlaceholder(text, path, kind string) {
	matched, e := placeholder(text, kind)
	if e != nil {
		v.err = e
		return
	}
	if matched {
		v.add("COVER_LETTER_PLACEHOLDER", "Remove placeholder text before export.", path, false, true)
	}
}

// ValidateCover preserves the existing structural/evidence-reference gate. Its
// ready_to_export field is not proof of final factual/format integrity: CH-8 must
// supply that separate gate before any migrated export handler is activated.
func ValidateCover(ctx context.Context, d CoverDraft, current CoverRevisions, evidence []Evidence) (*Validation, *Error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("context", "MATERIAL_CANCELED")
	}
	v := newValidation()
	for _, s := range []struct{ actual, expected, code, message, path string }{
		{d.JobFingerprint, current.Job, "MATERIAL_JOB_STALE", "The saved job description changed. Refresh job facts before saving or exporting.", "job_fingerprint"},
		{d.ProfileRevision, current.Profile, "MATERIAL_PROFILE_STALE", "Career Vault Profile changed. Review the contact block before saving or exporting.", "profile_revision"},
		{d.EvidenceRevision, current.Evidence, "MATERIAL_EVIDENCE_STALE", "Career Vault evidence changed. Review selected evidence before saving or exporting.", "evidence_revision"},
	} {
		if s.actual != s.expected {
			v.add(s.code, s.message, s.path, true, true)
		}
	}
	available := map[string]bool{}
	for _, e := range evidence {
		available[e.ID] = true
	}
	if len(d.Content.EvidenceItems) < 2 {
		v.add("COVER_LETTER_EVIDENCE_TOO_THIN", "Add at least two concise, job-relevant evidence points.", "content.evidence_items", false, true)
	}
	for i, item := range d.Content.EvidenceItems {
		path := fmt.Sprintf("content.evidence_items[%d]", i)
		if unknown := absent(item.EvidenceRefs, available); len(unknown) > 0 {
			v.add("COVER_LETTER_EVIDENCE_UNKNOWN", "Evidence references are missing or no longer active: "+strings.Join(unknown, ", "), path+".evidence_refs", true, true)
		}
		if len(item.EvidenceRefs) == 0 && len(item.ManualChecks) == 0 {
			v.add("COVER_LETTER_CLAIM_UNVERIFIED", "Link this claim to Career Vault evidence or record a manual check.", path, false, true)
		}
		a, e := placeholder(item.Label, "cover")
		if e != nil {
			return nil, e
		}
		b, e := placeholder(item.Claim, "cover")
		if e != nil {
			return nil, e
		}
		if a || b {
			v.add("COVER_LETTER_PLACEHOLDER", "Remove placeholder text before export.", path, false, true)
		}
	}
	for _, s := range []struct{ path, value string }{{"content.salutation", d.Content.Salutation}, {"content.opening", d.Content.Opening}, {"content.evidence_lead_in", d.Content.EvidenceLeadIn}, {"content.closing", d.Content.Closing}, {"content.credential_line", d.Content.CredentialLine}} {
		v.checkPlaceholder(s.value, s.path, "cover")
		v.prose(s.value, s.path)
	}
	for i, item := range d.Content.EvidenceItems {
		v.prose(item.Label, fmt.Sprintf("content.evidence_items[%d].label", i))
		v.prose(item.Claim, fmt.Sprintf("content.evidence_items[%d].claim", i))
	}
	parts := []string{d.Content.Opening, d.Content.EvidenceLeadIn}
	for _, item := range d.Content.EvidenceItems {
		parts = append(parts, item.Label+" "+item.Claim)
	}
	parts = append(parts, d.Content.Closing)
	words := len(strings.FieldsFunc(strings.Join(parts, " "), textcompat.IsSpace))
	if words < 150 {
		v.add("COVER_LETTER_TOO_SHORT", "The tailored body is under 150 words; add only evidence that improves the case.", "content", false, false)
	}
	if words > 650 {
		v.add("COVER_LETTER_TOO_LONG", "The tailored body exceeds the one-page 650-word safety limit.", "content", false, true)
	}
	return v.finish(ctx)
}
