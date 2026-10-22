package readonly

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"

	"careerhub.local/backend/internal/applicationanalysis"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/jobdigest"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/skills/matching"
	"careerhub.local/backend/internal/textcompat"
)

// DigestOptions are explicit hints. Reanalysis instead derives only saved
// company/title fields previously marked provided, within the same card read
// that supplies the returned revision. A blank SourceURL uses the saved URL.
type DigestOptions struct {
	Company, Title, SourceURL string
	Reanalysis                bool
}

// RetainedJobDigest is fresh read-only preparation, not a persisted cache or
// an evaluated job. File hashes identify exact observations, including BOM and
// newline bytes; ProfilePresent distinguishes absent from an empty profile.
// CatalogRevision identifies the complete effective registry.
type RetainedJobDigest struct {
	Digest                                           *jobdigest.Result
	CardFileSHA256, RawFileSHA256, ProfileFileSHA256 [32]byte
	PlannedCardSHA256                                [32]byte
	ProfilePresent                                   bool
	CatalogRevision                                  string
}

type jobDigestInputs struct {
	data, resources *evidenceReader
	registry        *catalogstore.State
	profile         jobdigest.EligibilityProfile
	profileRaw      []byte
	profilePresent  bool
}

// Both retained and new-JD analysis consume the same fixed catalog/profile
// set. The caller rechecks these observed files after analysis and encoding.
func loadJobDigestInputs(ctx context.Context, dataSource, resourceSource evaluationSource) (*jobDigestInputs, error) {
	data := &evidenceReader{ctx: ctx, source: dataSource}
	resources := &evidenceReader{ctx: ctx, source: resourceSource, resource: true}
	registry, catalogErr := catalogstore.ReadState(resources, data)
	if err := assessmentCanceled(ctx); err != nil {
		return nil, err
	}
	if catalogErr != nil {
		return nil, fail("JOB_DIGEST_CATALOG_INVALID")
	}
	raw, present, readErr := data.ReadFile("career_vault/visa_profile.yaml", cataloginput.YAMLMaxInputBytes)
	if err := assessmentCanceled(ctx); err != nil {
		return nil, err
	}
	if readErr != nil || (!present && len(raw) != 0) {
		return nil, fail("JOB_DIGEST_PROFILE_READ_FAILED")
	}
	value := cataloginput.Value{Kind: "null"}
	if present {
		var err *cataloginput.YAMLError
		value, err = cataloginput.ParseYAML(raw)
		if err != nil {
			return nil, fail("JOB_DIGEST_PROFILE_INVALID")
		}
	}
	profile, err := jobdigest.ProfileProjection(ctx, value)
	if err != nil {
		if errors.Is(err, jobdigest.ErrInput) {
			return nil, fail("JOB_DIGEST_PROFILE_INVALID")
		}
		return nil, digestFailure(err)
	}
	return &jobDigestInputs{data: data, resources: resources, registry: registry,
		profile: profile, profileRaw: raw, profilePresent: present}, nil
}

func (inputs *jobDigestInputs) verify() error {
	if err := inputs.data.verify(); err != nil {
		return err
	}
	return inputs.resources.verify()
}

// DigestJobSource requires the same native ownership/admission as other full
// assessment reads. It reads one valid Job Card and only its managed incoming
// JD, the four fixed catalog files and the five-field eligibility projection.
// No private document contents, browser/network access, HTTP activation, writes,
// skill insertion, candidate evidence inference or application status changes.
func (w *Workspace) DigestJobSource(ctx context.Context, relative string, options DigestOptions) (*RetainedJobDigest, error) {
	return readOwnedAssessment(w, ctx, func(data, resources *runtimepaths.Root) (*RetainedJobDigest, error) {
		return digestJobSource(ctx, data, resources, relative, options)
	})
}

func digestJobSource(ctx context.Context, dataSource, resourceSource evaluationSource, relative string, options DigestOptions) (*RetainedJobDigest, error) {
	if err := assessmentCanceled(ctx); err != nil {
		return nil, err
	}
	if dataSource == nil || resourceSource == nil {
		return nil, fail("JOB_DIGEST_READ_FAILED")
	}
	loaded, cardErr := jobcard.Read(ctx, dataSource, relative)
	if cardErr != nil {
		return nil, fail(cardErr.Code)
	}
	if !loaded.Report.SchemaValid() {
		return nil, fail("JOB_DIGEST_CARD_INVALID")
	}
	if options.Reanalysis {
		if options.Company != "" || options.Title != "" || options.SourceURL != "" {
			return nil, fail("JOB_DIGEST_OPTIONS_INVALID")
		}
		if cardDigestField(loaded.Card, "status") != "draft" {
			return nil, fail("JOB_REANALYSIS_NOT_DRAFT")
		}
		for _, field := range []struct {
			name   string
			target *string
		}{{"company", &options.Company}, {"title", &options.Title}} {
			if cardDigestField(loaded.Card, "digest", "fields", field.name, "state") == "provided" {
				*field.target = cardDigestField(loaded.Card, field.name)
			}
		}
	}
	// UTF-8-sig and universal newlines match source intake's retained-file read.
	// Exact raw byte hashes remain separate for read-set change detection.
	source := strings.TrimPrefix(loaded.RawText, "\ufeff")
	if !loaded.RawPresent || textcompat.Strip(source) == "" {
		return nil, fail("JOB_DIGEST_SOURCE_MISSING")
	}
	inputs, inputErr := loadJobDigestInputs(ctx, dataSource, resourceSource)
	if inputErr != nil {
		return nil, inputErr
	}
	if options.SourceURL == "" {
		for _, entry := range loaded.Card.Entries {
			if entry.Key.Kind == "string" && entry.Key.Text == "source_url" {
				options.SourceURL = entry.Value.Text
				break
			}
		}
	}
	result, err := jobdigest.Analyze(ctx, jobdigest.Input{Source: source, Company: options.Company, Title: options.Title, SourceURL: options.SourceURL, Registry: inputs.registry.Snapshot, Profile: inputs.profile})
	if err != nil {
		return nil, digestFailure(err)
	}
	// Reuse the validated managed-path reader to recheck both card and raw file.
	// These observations detect changes, not atomic filesystem snapshot isolation.
	again, cardErr := jobcard.Read(ctx, dataSource, relative)
	if cardErr != nil {
		return nil, fail(cardErr.Code)
	}
	if loaded.CardFileSHA256 != again.CardFileSHA256 || loaded.RawFileSHA256 != again.RawFileSHA256 || loaded.RawPresent != again.RawPresent {
		return nil, fail("JOB_DIGEST_INPUT_CHANGED")
	}
	if err := inputs.verify(); err != nil {
		return nil, err
	}
	if err := assessmentCanceled(ctx); err != nil {
		return nil, err
	}
	prepared := &RetainedJobDigest{Digest: result, CardFileSHA256: loaded.CardFileSHA256, RawFileSHA256: loaded.RawFileSHA256, ProfileFileSHA256: sha256.Sum256(inputs.profileRaw), ProfilePresent: inputs.profilePresent, CatalogRevision: inputs.registry.Snapshot.CatalogRevision()}
	if options.Reanalysis {
		card, transformErr := jobcard.PrepareReanalysis(ctx, loaded, result)
		if transformErr != nil {
			return nil, fail(transformErr.Code)
		}
		encoded, encodeErr := jobcard.EncodeCard(ctx, card)
		if encodeErr != nil {
			return nil, fail(encodeErr.Code)
		}
		prepared.PlannedCardSHA256 = sha256.Sum256(encoded)
	}
	return prepared, nil
}
func cardDigestField(card cataloginput.Value, path ...string) string {
	for _, name := range path {
		if card.Kind != "mapping" {
			return ""
		}
		found := false
		for _, entry := range card.Entries {
			if entry.Key.Kind == "string" && entry.Key.Text == name {
				card, found = entry.Value, true
				break
			}
		}
		if !found {
			return ""
		}
	}
	if card.Kind == "string" {
		return card.Text
	}
	return ""
}
func digestFailure(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fail("ASSESSMENT_INPUT_CANCELED")
	}
	if errors.Is(err, jobdigest.ErrLimit) || errors.Is(err, matching.ErrLimit) || errors.Is(err, applicationanalysis.ErrLimit) {
		return fail("JOB_DIGEST_INPUT_LIMIT")
	}
	if errors.Is(err, jobdigest.ErrSourceMismatch) {
		return fail("JOB_DIGEST_SOURCE_MISMATCH")
	}
	if errors.Is(err, jobdigest.ErrInput) || errors.Is(err, matching.ErrInput) || errors.Is(err, applicationanalysis.ErrInput) {
		return fail("JOB_DIGEST_INPUT_INVALID")
	}
	return fail("JOB_DIGEST_ANALYSIS_FAILED")
}
