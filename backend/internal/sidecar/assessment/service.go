// Package assessment composes fresh/validated cached previews and explicit
// disposable-cache publication. It is not wired to the installed HTTP runtime.
package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"careerhub.local/backend/internal/assessmentcontext"
	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/discoverypreview"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/previewcache"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

type Error struct{ Code string }

func (*Error) Error() string                    { return "Discovery assessment could not be completed." }
func (e *Error) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, e.Error()) }
func fail(code string) error                    { return &Error{Code: code} }

type Service struct {
	start     protocol.StartRecord
	workspace *readonly.Workspace
	now       func() time.Time
	save      func(context.Context, *runtimepaths.Root, previewcache.Observation, string, assessmentcontext.CacheIdentity, *discoverypreview.Prepared, time.Time) (previewcache.WriteReport, error)
}
type Result struct {
	Preview       *readonly.DiscoveryPreview
	CacheStatus   string
	Write         previewcache.WriteReport
	Review        json.RawMessage
	ComputedAt    string
	CleanupFailed bool
}

// Open requires the existing native owner before reading private admission
// data. It never bootstraps or claims a second home. The host supplies the same
// explicit StartRecord used for its owned workspace, never renderer arguments.
func Open(start protocol.StartRecord) (service *Service, problem error) {
	root, e := runtimepaths.Open(start.DataRoot)
	if e != nil {
		return nil, fail("ASSESSMENT_ROOT_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil {
			service, problem = nil, fail("ASSESSMENT_CLEANUP_FAILED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return nil, fail("ASSESSMENT_OWNERSHIP_REQUIRED")
	}
	w, err := readonly.Open(start)
	if err != nil {
		return nil, fail("ASSESSMENT_ADMISSION_FAILED")
	}
	return &Service{start: start, workspace: w, now: time.Now, save: previewcache.SaveOwned}, nil
}

// Assess holds the same non-reentrant process gate used by catalog mutations.
// Native operations finish synchronously; neither timeout nor a late cleanup
// error erases publication facts. A failure never exposes a partial preview.
func (s *Service) Assess(ctx context.Context, id string, month assessmentcontext.Month) (result *Result, problem error) {
	return s.assess(ctx, id, month, false)
}

func (s *Service) assess(ctx context.Context, id string, month assessmentcontext.Month, review bool) (result *Result, problem error) {
	result = &Result{}
	if s == nil || s.workspace == nil || s.now == nil || s.save == nil || ctx == nil {
		return result, fail("ASSESSMENT_SERVICE_INVALID")
	}
	key, idErr := discovery.NormalizeID(id)
	if idErr != nil || !month.Valid() {
		return result, fail("ASSESSMENT_INPUT_INVALID")
	}
	id = key
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	release, err := operationgate.Acquire(ctx)
	if err != nil {
		return result, fail("ASSESSMENT_INPUT_CANCELED")
	}
	defer release()
	return s.assessOwned(ctx, id, month, review)
}

// assessOwned requires the caller's process gate. Page composition reuses the
// same owner without recursively acquiring its non-reentrant gate.
func (s *Service) assessOwned(ctx context.Context, id string, month assessmentcontext.Month, review bool) (result *Result, problem error) {
	result = &Result{}
	root, e := runtimepaths.Open(s.start.DataRoot)
	if e != nil {
		return result, fail("ASSESSMENT_ROOT_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil {
			result.Preview = nil
			result.Review = nil
			result.CleanupFailed = true
			problem = fail("ASSESSMENT_CLEANUP_FAILED")
		}
	}()
	if root.RequireProcessOwnership() != nil {
		return result, fail("ASSESSMENT_OWNERSHIP_REQUIRED")
	}
	var first *discovery.Detail
	if review {
		var err error
		first, err = s.workspace.DiscoveryFeedDetail(ctx, id)
		if err != nil {
			return result, err
		}
		// Keep the detail, its preview, cache publication and the final witness
		// under one writer gate. Never call the public Assess entry recursively.
		defer func() {
			if problem == nil {
				result.Review, problem = s.finishReview(ctx, id, first, result)
			}
			if problem != nil {
				result.Preview, result.Review = nil, nil
			}
		}()
	}
	preview, err := s.workspace.ResolveDiscoveryPreview(ctx, id, month)
	if err != nil && review && feedContextUnavailable(err) {
		preview, err = unavailableFeedPreview(first)
	}
	if err != nil {
		return result, fail("ASSESSMENT_PREVIEW_UNAVAILABLE")
	}
	if preview.Prepared == nil {
		result.Preview, result.CacheStatus = preview, "ineligible"
		return result, nil
	}
	if preview.CacheHit {
		result.Preview, result.CacheStatus = preview, "hit"
		return result, nil
	}
	if preview.Cache == nil || preview.Identity == nil {
		return result, fail("ASSESSMENT_PREVIEW_INVALID")
	}
	if s.workspace.CheckAdmission() != nil {
		return result, fail("ASSESSMENT_ADMISSION_CHANGED")
	}
	result.CacheStatus = string(preview.Cache.Disposition)
	computed := s.now()
	result.Write, err = s.save(ctx, root, preview.Cache.Observation, id, *preview.Identity, preview.Prepared, computed)
	if err != nil {
		if errors.Is(err, previewcache.ErrConflict) {
			return result, fail("ASSESSMENT_CACHE_CHANGED")
		}
		if errors.Is(err, previewcache.ErrRead) {
			return result, fail("ASSESSMENT_CACHE_READ_FAILED")
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return result, fail("ASSESSMENT_INPUT_CANCELED")
		}
		return result, fail("ASSESSMENT_CACHE_SAVE_FAILED")
	}
	if s.workspace.CheckAdmission() != nil {
		return result, fail("ASSESSMENT_ADMISSION_CHANGED")
	}
	result.Preview = preview
	result.ComputedAt = computed.UTC().Format("2006-01-02T15:04:05Z")
	return result, nil
}
