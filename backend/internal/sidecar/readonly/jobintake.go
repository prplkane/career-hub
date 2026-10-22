package readonly

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"

	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/jobdigest"
)

// NewJobPreparation is an unpublished card and the exact inputs used to
// prepare it. None of these witnesses grants authority to create a path; the
// future owner must repeat selection and recheck under its gate.
type NewJobPreparation struct {
	Destination                      jobcard.IntakeDestination
	Digest                           *jobdigest.Result
	CardBytes                        []byte
	RawFileSHA256, PlannedCardSHA256 [32]byte
	ProfileFileSHA256                [32]byte
	ProfilePresent                   bool
	CatalogRevision                  string
	readSet                          newJobReadSet
}

func (NewJobPreparation) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted new job preparation]")
}

// The explicit-ID helper is for deterministic transformation tests only. The
// owned entrypoint is InspectNewJob, which checks duplicates before a name.
func prepareNewJob(ctx context.Context, dataSource, resourceSource evaluationSource, input jobcard.IntakeDraftInput, company, title string) (*NewJobPreparation, error) {
	if err := assessmentCanceled(ctx); err != nil {
		return nil, err
	}
	if dataSource == nil || resourceSource == nil {
		return nil, fail("JOB_INTAKE_PREPARATION_FAILED")
	}
	inputs, digest, inputErr := analyzeNewJob(ctx, dataSource, resourceSource, input.RawText, input.SourceURL, company, title)
	if inputErr != nil {
		return nil, inputErr
	}
	prepared, draftErr := draftNewJob(ctx, inputs, digest, input)
	if draftErr != nil {
		return nil, draftErr
	}
	if err := inputs.verify(); err != nil {
		return nil, err
	}
	if err := assessmentCanceled(ctx); err != nil {
		return nil, err
	}
	return prepared, nil
}

func analyzeNewJob(ctx context.Context, dataSource, resourceSource evaluationSource, rawText, sourceURL, company, title string) (*jobDigestInputs, *jobdigest.Result, error) {
	source, sourceErr := jobcard.IntakeDigestSource(rawText)
	if sourceErr != nil {
		return nil, nil, fail(sourceErr.Code)
	}
	inputs, inputErr := loadJobDigestInputs(ctx, dataSource, resourceSource)
	if inputErr != nil {
		return nil, nil, inputErr
	}
	digest, digestErr := jobdigest.Analyze(ctx, jobdigest.Input{Source: source, Company: company, Title: title,
		SourceURL: sourceURL, Registry: inputs.registry.Snapshot, Profile: inputs.profile})
	if digestErr != nil {
		return nil, nil, digestFailure(digestErr)
	}
	return inputs, digest, nil
}

func draftNewJob(ctx context.Context, inputs *jobDigestInputs, digest *jobdigest.Result, input jobcard.IntakeDraftInput) (*NewJobPreparation, error) {
	readSet, readSetErr := captureNewJobReadSet(inputs)
	if readSetErr != nil {
		return nil, readSetErr
	}
	card, cardErr := jobcard.PrepareIntake(ctx, input, digest)
	if cardErr != nil {
		return nil, fail(cardErr.Code)
	}
	encoded, encodeErr := jobcard.EncodeCard(ctx, card)
	if encodeErr != nil {
		return nil, fail(encodeErr.Code)
	}
	return &NewJobPreparation{Destination: jobcard.IntakeDestination{ID: input.ID, RawPath: input.RawPath, CardPath: "jobs/normalized/" + input.ID + ".yaml"}, Digest: digest, CardBytes: encoded,
		RawFileSHA256: sha256.Sum256([]byte(input.RawText)), PlannedCardSHA256: sha256.Sum256(encoded),
		ProfileFileSHA256: sha256.Sum256(inputs.profileRaw), ProfilePresent: inputs.profilePresent,
		CatalogRevision: inputs.registry.Snapshot.CatalogRevision(), readSet: readSet}, nil
}
