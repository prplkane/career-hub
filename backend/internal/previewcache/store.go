package previewcache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"time"

	"careerhub.local/backend/internal/assessmentcontext"
	"careerhub.local/backend/internal/discoverypreview"
	"careerhub.local/backend/internal/runtimepaths"
)

type Reader interface {
	ReadFile(string, int64) ([]byte, bool, *runtimepaths.Error)
}

func Load(ctx context.Context, files Reader, id string, current assessmentcontext.CacheIdentity) (*Entry, error) {
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	path, err := Path(id)
	if err != nil || files == nil || !validIdentity(current) {
		return nil, ErrInvalid
	}
	raw, present, e := files.ReadFile(path, MaxBytes)
	if e != nil || len(raw) > MaxBytes || !present && len(raw) != 0 {
		return nil, ErrRead
	}
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	result := &Entry{Observation: Observation{Path: path, Present: present, SHA256: sha256.Sum256(raw)}, Disposition: Missing}
	if present {
		result.Prepared, result.Disposition, err = Decode(ctx, raw, id, current)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// WriteReport preserves publication facts on every return, including cleanup
// errors after replacement. This disposable cache makes no power-loss durability
// claim and never rolls back/deletes user history to recover from a cache error.
type WriteReport struct {
	DirectoriesCreated bool
	Attempted          bool
	Committed          bool
}
type writer interface {
	Reader
	RequireProcessOwnership() *runtimepaths.Error
	EnsureDirectory(string) (bool, *runtimepaths.Error)
	CreateFile(string, []byte) (bool, *runtimepaths.Error)
	ReplaceFile(string, []byte, []byte) (bool, *runtimepaths.Error)
}

// SaveOwned only admits native roots. The service must hold the shared operation
// gate through this call and cleanup; CAS is not a noncooperating-writer lock.
func SaveOwned(ctx context.Context, root *runtimepaths.Root, observed Observation, id string, current assessmentcontext.CacheIdentity, prepared *discoverypreview.Prepared, computed time.Time) (WriteReport, error) {
	return save(ctx, root, observed, id, current, prepared, computed)
}
func save(ctx context.Context, root writer, observed Observation, id string, current assessmentcontext.CacheIdentity, prepared *discoverypreview.Prepared, computed time.Time) (report WriteReport, problem error) {
	raw, err := Encode(ctx, id, current, prepared, computed)
	if err != nil {
		return report, err
	}
	path, err := Path(id)
	if err != nil || root == nil || observed.Path != path {
		return report, ErrInvalid
	}
	if root.RequireProcessOwnership() != nil {
		return report, ErrWrite
	}
	before, present, e := root.ReadFile(path, MaxBytes)
	if e != nil || len(before) > MaxBytes || !present && len(before) != 0 {
		return report, ErrRead
	}
	if present != observed.Present || sha256.Sum256(before) != observed.SHA256 {
		return report, ErrConflict
	}
	if err := stopped(ctx); err != nil {
		return report, err
	}
	if !present {
		for _, directory := range []string{"cache", "cache/go", Directory} {
			changed, e := root.EnsureDirectory(directory)
			report.DirectoriesCreated = report.DirectoriesCreated || changed
			if e != nil {
				return report, ErrWrite
			}
			if err := stopped(ctx); err != nil {
				return report, err
			}
		}
	}
	report.Attempted = true
	if present {
		report.Committed, e = root.ReplaceFile(path, before, raw)
	} else {
		report.Committed, e = root.CreateFile(path, raw)
	}
	if e != nil || !report.Committed {
		return report, ErrWrite
	}
	// Native cleanup/readback runs synchronously after publication, even when
	// the original request was canceled. Never label this uncommitted or retry.
	retained, exists, e := root.ReadFile(path, MaxBytes)
	if e != nil || !exists || !bytes.Equal(raw, retained) {
		return report, ErrWrite
	}
	return report, nil
}
