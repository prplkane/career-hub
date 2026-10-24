package readonly

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"

	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

// NewJobRequest has no preselected path. All fields are local user input, not
// permission to fetch the URL or publish an application.
type NewJobRequest struct {
	SourceName, RawText, SourceURL, PostingID, IdempotencyKey string
	// Empty uses the source-compatible Captured default. Applied is only an
	// explicit intake intent, never inferred from opening a posting.
	InitialStatus string
}

func (NewJobRequest) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted new job request]")
}

// NewJobInspection contains exactly one of Prepared or Duplicate. A duplicate
// is a verified read-only observation, not a persisted application/assessment.
type NewJobInspection struct {
	Prepared  *NewJobPreparation
	Duplicate *NewJobDuplicate
}

func (NewJobInspection) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted new job inspection]")
}

// InspectNewJob analyzes explicit local source and classifies verified saved
// cards before proposing a fresh destination. No result reserves, creates or
// replaces a path; the later writer must repeat this decision under its gate.
func (w *Workspace) InspectNewJob(ctx context.Context, request NewJobRequest, company, title string) (*NewJobInspection, error) {
	if _, err := jobcard.IntakeDigestSource(request.RawText); err != nil {
		return nil, fail(err.Code)
	}
	if !jobcard.ValidIntakeSourceName(request.SourceName) {
		return nil, fail("JOB_INTAKE_NAME_INVALID")
	}
	key, keyErr := jobcard.NormalizeIntakeKey(request.IdempotencyKey)
	if keyErr != nil {
		return nil, fail(keyErr.Code)
	}
	return readOwnedAssessment(w, ctx, func(data, resources *runtimepaths.Root) (result *NewJobInspection, failure error) {
		inputs, digest, err := analyzeNewJob(ctx, data, resources, request.RawText, request.SourceURL, company, title)
		if err != nil {
			return nil, err
		}
		rawDir, err := observeIntakeDirectory(data, w.start.DataRoot, "jobs/incoming")
		if err != nil {
			return nil, err
		}
		defer func() {
			if rawDir.close() != nil {
				result, failure = nil, fail("JOB_INTAKE_NAMESPACE_UNAVAILABLE")
			}
		}()
		cardDir, err := observeIntakeDirectory(data, w.start.DataRoot, "jobs/normalized")
		if err != nil {
			return nil, err
		}
		defer func() {
			if cardDir.close() != nil {
				result, failure = nil, fail("JOB_INTAKE_NAMESPACE_UNAVAILABLE")
			}
		}()
		identities, scanErr := scanIntakeIdentities(ctx, data, cardDir.names)
		if scanErr != nil {
			return nil, scanErr
		}
		duplicate, decisionErr := decideIntakeDuplicate(digest.ContentFingerprint,
			jobcard.IntakePostingIdentity(request.SourceURL, request.PostingID), key, identities)
		if decisionErr != nil {
			return nil, decisionErr
		}
		if duplicate != nil {
			if err := inputs.verify(); err != nil {
				return nil, err
			}
			if err := rawDir.verify(data); err != nil {
				return nil, err
			}
			if err := cardDir.verify(data); err != nil {
				return nil, err
			}
			return &NewJobInspection{Duplicate: duplicate}, nil
		}
		destination, selectErr := jobcard.SelectIntakeDestination(request.SourceName, digest, rawDir.names, cardDir.names)
		if selectErr != nil {
			return nil, fail(selectErr.Code)
		}
		input := jobcard.IntakeDraftInput{ID: destination.ID, RawPath: destination.RawPath,
			RawText: request.RawText, SourceURL: request.SourceURL, PostingID: request.PostingID, IdempotencyKey: request.IdempotencyKey}
		prepared, err := draftNewJob(ctx, inputs, digest, input)
		if err != nil {
			return nil, err
		}
		if err := inputs.verify(); err != nil {
			return nil, err
		}
		if err := rawDir.verify(data); err != nil {
			return nil, err
		}
		if err := cardDir.verify(data); err != nil {
			return nil, err
		}
		for _, path := range []string{destination.RawPath, destination.CardPath} {
			present, pathErr := data.Exists(path)
			if pathErr != nil || present {
				return nil, fail("JOB_INTAKE_NAMESPACE_CHANGED")
			}
		}
		if err := assessmentCanceled(ctx); err != nil {
			return nil, err
		}
		return &NewJobInspection{Prepared: prepared}, nil
	})
}

type intakeDirectory struct {
	relative string
	root     *runtimepaths.Root
	names    []string
}

func observeIntakeDirectory(data *runtimepaths.Root, dataRoot, relative string) (*intakeDirectory, error) {
	present, err := data.Exists(relative)
	if err != nil {
		return nil, fail("JOB_INTAKE_NAMESPACE_UNAVAILABLE")
	}
	result := &intakeDirectory{relative: relative, names: []string{}}
	if !present {
		return result, nil
	}
	result.root, err = runtimepaths.Open(filepath.Join(dataRoot, filepath.FromSlash(relative)))
	if err != nil {
		return nil, fail("JOB_INTAKE_NAMESPACE_UNAVAILABLE")
	}
	result.names, err = result.root.ListFileNames()
	if err != nil {
		_ = result.root.Close()
		return nil, fail("JOB_INTAKE_NAMESPACE_UNAVAILABLE")
	}
	return result, nil
}

func (d *intakeDirectory) verify(data *runtimepaths.Root) error {
	present, err := data.Exists(d.relative)
	if err != nil || present != (d.root != nil) {
		return fail("JOB_INTAKE_NAMESPACE_CHANGED")
	}
	if d.root != nil {
		names, listErr := d.root.ListFileNames()
		if listErr != nil || !slices.Equal(d.names, names) {
			return fail("JOB_INTAKE_NAMESPACE_CHANGED")
		}
	}
	return nil
}

func (d *intakeDirectory) close() error {
	if d.root != nil {
		return d.root.Close()
	}
	return nil
}
