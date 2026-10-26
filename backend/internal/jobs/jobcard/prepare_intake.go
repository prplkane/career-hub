package jobcard

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/applications/applicationanalysis"
	"careerhub.local/backend/internal/jobs/jobdigest"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/textcompat"
)

var intakeRetryKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{7,127}$`)

// NormalizeIntakeKey applies the same bounded retry-key grammar to a new
// request and an existing card. Empty means no retry identity.
func NormalizeIntakeKey(value string) (string, *Error) {
	key := textcompat.Strip(value)
	if key != "" && !intakeRetryKey.MatchString(key) {
		return "", &Error{Code: "JOB_INTAKE_REQUEST_INVALID"}
	}
	return key, nil
}

// IntakePostingIdentity is the stable external posting selector used by both
// newly prepared cards and read-only duplicate inspection.
func IntakePostingIdentity(sourceURL, postingID string) string {
	return detailPostingIdentity(sourceURL, postingID)
}

// IntakeDraftInput describes one unpublished raw JD and its already-selected
// canonical destination. The future writer, not this pure builder, must prove
// that the selector is free and the source/catalog/profile have not changed.
type IntakeDraftInput struct {
	ID, RawPath, RawText, SourceURL, PostingID, IdempotencyKey string
}

func (IntakeDraftInput) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted intake draft input]")
}

// IntakeDigestSource matches subsequent owned reads of a retained UTF-8-sig
// text file with universal newlines. RawText itself remains byte-for-byte
// retained by the future writer; this normalization is analysis-only.
func IntakeDigestSource(raw string) (string, *Error) {
	if !utf8.ValidString(raw) || len(raw) > jobdigest.MaxSourceBytes || strings.ContainsRune(raw, 0) {
		return "", &Error{Code: "JOB_INTAKE_SOURCE_INVALID"}
	}
	source := strings.TrimPrefix(strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n"), "\ufeff")
	if textcompat.Strip(source) == "" || len(source) > jobdigest.MaxSourceBytes ||
		utf8.RuneCountInString(source) > jobdigest.MaxSourceRunes || strings.Count(source, "\n") >= jobdigest.MaxLines {
		return "", &Error{Code: "JOB_INTAKE_SOURCE_INVALID"}
	}
	return source, nil
}

// PrepareIntake constructs the one complete Go-v12 draft in memory. It does
// not create a raw file/card, deduplicate a posting, register a ledger row,
// compute an assessment or grant a file write. The writer must independently
// replay/verify the digest under pinned Home/resource ownership before commit.
func PrepareIntake(ctx context.Context, input IntakeDraftInput, fresh *jobdigest.Result) (cataloginput.Value, *Error) {
	if stoppedErr := stopped(ctx); stoppedErr != nil {
		return cataloginput.Value{}, stoppedErr
	}
	if !ValidID(input.ID) || (input.RawPath != "jobs/incoming/"+input.ID+".txt" && input.RawPath != "jobs/incoming/"+input.ID+".md") ||
		input.ID != reanalysisSourceID(input.RawPath) || fresh == nil {
		return cataloginput.Value{}, &Error{Code: "JOB_INTAKE_REQUEST_INVALID"}
	}
	key, keyErr := NormalizeIntakeKey(input.IdempotencyKey)
	if keyErr != nil {
		return cataloginput.Value{}, keyErr
	}
	source, sourceErr := IntakeDigestSource(input.RawText)
	if sourceErr != nil {
		return cataloginput.Value{}, sourceErr
	}
	fingerprint, fingerprintErr := applicationanalysis.SourceFingerprint(source)
	if fingerprintErr != nil || fresh.ContentFingerprint != fingerprint {
		return cataloginput.Value{}, &Error{Code: "JOB_INTAKE_DIGEST_INVALID"}
	}
	card := cataloginput.Value{Kind: "mapping"}
	for _, entry := range []struct{ name, value string }{
		{"id", input.ID}, {"status", "draft"}, {"company", ""}, {"title", ""},
		{"source_url", textcompat.Strip(input.SourceURL)}, {"location", ""},
		{"remote_policy", ""}, {"seniority", ""},
	} {
		setValue(&card, entry.name, stringValue(entry.value))
	}
	for _, name := range []string{
		"required_skills", "required_platforms", "required_capabilities", "preferred_skills",
		"technology_groups", "experience_requirements", "education_requirements", "responsibilities",
	} {
		setValue(&card, name, cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{}})
	}
	compensation := cataloginput.Value{Kind: "mapping"}
	setValue(&compensation, "status", stringValue("not_stated"))
	setValue(&compensation, "ranges", cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{}})
	setValue(&card, "compensation", compensation)
	visa := cataloginput.Value{Kind: "mapping"}
	for _, name := range []string{"citizenship_required", "clearance_required", "sponsorship_mentioned", "no_visa_or_opt"} {
		setValue(&visa, name, cataloginput.Value{Kind: "null"})
	}
	setValue(&card, "visa_signals", visa)
	setValue(&card, "raw_jd_path", stringValue(input.RawPath))
	setValue(&card, "notes", cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{stringValue("Draft created from the raw job description; manual review required.")}})
	setValue(&card, "manual_overrides", cataloginput.Value{Kind: "sequence", Items: []cataloginput.Value{}})
	setValue(&card, "posting_id", stringValue(textcompat.Strip(input.PostingID)))
	setValue(&card, "posting_identity", stringValue(IntakePostingIdentity(input.SourceURL, input.PostingID)))
	if key != "" {
		setValue(&card, "intake_idempotency_key", stringValue(key))
	}
	if digestErr := applyFreshDigest(&card, fresh, "JOB_INTAKE_DIGEST_INVALID"); digestErr != nil {
		return cataloginput.Value{}, digestErr
	}
	report, validationErr := Validate(ctx, card, func(_ context.Context, relative string) (bool, *Error) {
		return relative == input.RawPath, nil
	})
	if validationErr != nil || report == nil || !report.SchemaValid() {
		if canceled := stopped(ctx); canceled != nil {
			return cataloginput.Value{}, canceled
		}
		return cataloginput.Value{}, &Error{Code: "JOB_INTAKE_RESULT_INVALID"}
	}
	return card, nil
}
