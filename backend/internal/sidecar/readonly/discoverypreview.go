package readonly

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"time"

	"careerhub.local/backend/internal/assessmentcontext"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/discovery"
	"careerhub.local/backend/internal/discoverypreview"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/jobevaluation"
	"careerhub.local/backend/internal/previewcache"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/sqlitestore"
)

// DiscoveryPreview is a complete fresh or revalidated cached preparation, never
// a job/application creation. The artifact and identity are absent for partial
// captures; the bounded receipt/instruction/contact projections remain honest.
type DiscoveryPreview struct {
	Prepared     *discoverypreview.Prepared
	Identity     *assessmentcontext.CacheIdentity
	Receipt      discoverypreview.Receipt
	Instructions discoverypreview.Instructions
	Contacts     discoverypreview.Contacts
	Cache        *previewcache.Entry
	CacheHit     bool
}

// PrepareDiscoveryPreview uses one admitted operation and the exact current
// stored capture, never client-selected source text, metadata or rule versions.
// This read-only checkpoint does not implement cache persistence or writer
// exclusion: full CH-46 publication/rollback qualification is still required.
func (w *Workspace) PrepareDiscoveryPreview(ctx context.Context, id string, month assessmentcontext.Month) (*DiscoveryPreview, error) {
	return w.discoveryPreview(ctx, id, month, false)
}

// ResolveDiscoveryPreview reads the separate Go cache, but never writes it.
// An identity/checksum match still runs current-context, full card/reference,
// evaluator and receipt checks. The owner coordinates publication separately.
func (w *Workspace) ResolveDiscoveryPreview(ctx context.Context, id string, month assessmentcontext.Month) (*DiscoveryPreview, error) {
	return w.discoveryPreview(ctx, id, month, true)
}
func (w *Workspace) discoveryPreview(ctx context.Context, id string, month assessmentcontext.Month, cached bool) (*DiscoveryPreview, error) {
	key, idErr := discovery.NormalizeID(id)
	if idErr != nil {
		return nil, discoveryReadError(idErr)
	}
	if !month.Valid() {
		return nil, fail("ASSESSMENT_MONTH_INVALID")
	}
	return readOwnedAssessment(w, ctx, func(data, resources *runtimepaths.Root) (result *DiscoveryPreview, problem error) {
		files, pathErr := data.GuardSQLiteFiles(applicationDatabasePath)
		if pathErr != nil {
			return nil, discoveryPathError(pathErr)
		}
		if files == nil {
			return nil, fail("DISCOVERY_SCHEMA_PREPARATION_REQUIRED")
		}
		defer func() {
			if files.Close() != nil {
				result, problem = nil, fail("DISCOVERY_CLEANUP_FAILED")
			}
		}()
		db, err := sqlitestore.OpenReadOnly(ctx, filepath.Join(w.start.DataRoot, filepath.FromSlash(applicationDatabasePath)))
		if err != nil {
			return nil, fail("DISCOVERY_DATABASE_UNAVAILABLE")
		}
		defer func() {
			if db.Close() != nil {
				result, problem = nil, fail("DISCOVERY_CLEANUP_FAILED")
			}
		}()
		return readPreviewWithCache(ctx, db, data, resources, key, month, cached)
	})
}
func readPreview(ctx context.Context, db *sql.DB, data, resources evaluationSource, id string, month assessmentcontext.Month) (*DiscoveryPreview, error) {
	return readPreviewWithCache(ctx, db, data, resources, id, month, false)
}
func readPreviewWithCache(ctx context.Context, db *sql.DB, data, resources evaluationSource, id string, month assessmentcontext.Month, cached bool) (*DiscoveryPreview, error) {
	capture, err := discovery.ReadCapture(ctx, db, data, id)
	if err != nil {
		return nil, discoveryReadError(err)
	}
	result, problem := preparePreviewWithCache(ctx, data, resources, capture, month, cached)
	if problem != nil {
		return nil, problem
	}
	again, err := discovery.ReadCapture(ctx, db, data, id)
	if err != nil {
		return nil, discoveryReadError(err)
	}
	// Compare the whole observed capture projection, including overrides,
	// revision, source path, verified scope, text and source identity.
	if !reflect.DeepEqual(capture, again) {
		return nil, fail("DISCOVERY_PREVIEW_INPUT_CHANGED")
	}
	if err := assessmentCanceled(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

type previewBytes struct {
	raw     []byte
	present bool
}

// A per-operation immutable byte snapshot avoids reparsing different physical
// observations into candidate/digest/identity stages. Inner rechecks see that
// snapshot; the outer reader rechecks every original physical observation.
type previewReadSet struct {
	reader *evidenceReader
	values map[string]previewBytes
}

func (s *previewReadSet) Check(path string) *runtimepaths.Error { return s.reader.Check(path) }
func (s *previewReadSet) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	if s.reader.ctx == nil || s.reader.ctx.Err() != nil {
		return nil, false, &runtimepaths.Error{Code: "EVIDENCE_CANCELED"}
	}
	if !s.reader.allowed(path) || limit != cataloginput.YAMLMaxInputBytes {
		return nil, false, &runtimepaths.Error{Code: "EVIDENCE_PATH_INVALID"}
	}
	if cached, ok := s.values[path]; ok {
		return bytes.Clone(cached.raw), cached.present, nil
	}
	raw, present, err := s.reader.ReadFile(path, limit)
	if err != nil {
		return nil, false, err
	}
	if !present && len(raw) != 0 {
		return nil, false, &runtimepaths.Error{Code: "EVIDENCE_INPUT_CHANGED"}
	}
	s.values[path] = previewBytes{raw: bytes.Clone(raw), present: present}
	return raw, present, nil
}
func newPreviewReadSet(ctx context.Context, source evaluationSource, resource bool) *previewReadSet {
	return &previewReadSet{reader: &evidenceReader{ctx: ctx, source: source, resource: resource, policy: !resource}, values: map[string]previewBytes{}}
}
func prepareDiscoveryPreview(ctx context.Context, dataSource, resourceSource evaluationSource, capture *discovery.Capture, month assessmentcontext.Month) (*DiscoveryPreview, error) {
	return preparePreviewWithCache(ctx, dataSource, resourceSource, capture, month, false)
}
func preparePreviewWithCache(ctx context.Context, dataSource, resourceSource evaluationSource, capture *discovery.Capture, month assessmentcontext.Month, cached bool) (*DiscoveryPreview, error) {
	if err := assessmentCanceled(ctx); err != nil {
		return nil, err
	}
	if capture == nil || dataSource == nil || resourceSource == nil || !month.Valid() {
		return nil, fail("DISCOVERY_PREVIEW_INPUT_INVALID")
	}
	// Even direct internal use must verify the retained physical capture;
	// callers cannot replace source access with a metadata/hash assertion.
	text, sourceErr := discovery.ReadCapturedSource(ctx, dataSource, capture.Record)
	if sourceErr != nil {
		return nil, discoveryReadError(sourceErr)
	}
	if text != capture.SourceText {
		return nil, fail("DISCOVERY_PREVIEW_INPUT_CHANGED")
	}
	if !discoverypreview.Eligible(capture.ContentScope) {
		summary, err := discoverypreview.Unavailable(capture.ContentScope, "partial_capture", 0, 0)
		if err != nil {
			return nil, fail("DISCOVERY_PREVIEW_INPUT_INVALID")
		}
		instructions, err := discoverypreview.PartialInstructions(capture.ContentScope)
		if err != nil {
			return nil, fail("DISCOVERY_PREVIEW_INPUT_INVALID")
		}
		contacts, err := discoverypreview.PartialContacts(capture.ContentScope)
		if err != nil {
			return nil, fail("DISCOVERY_PREVIEW_INPUT_INVALID")
		}
		return &DiscoveryPreview{Receipt: summary, Instructions: instructions, Contacts: contacts}, nil
	}
	data, resources := newPreviewReadSet(ctx, dataSource, false), newPreviewReadSet(ctx, resourceSource, true)
	today := time.Date(month.Year, time.Month(month.Number), 1, 0, 0, 0, 0, time.UTC)
	evidence, err := prepareJobEvidence(ctx, data, resources, today)
	if err != nil {
		return nil, err
	}
	raw, present, pathErr := data.ReadFile("career_vault/hard_blocker_policy.yaml", cataloginput.YAMLMaxInputBytes)
	if pathErr != nil {
		return nil, fail("DISCOVERY_PREVIEW_POLICY_READ_FAILED")
	}
	policy := cataloginput.Value{Kind: "null"}
	if present {
		var yamlErr *cataloginput.YAMLError
		policy, yamlErr = cataloginput.ParseYAML(raw)
		if yamlErr != nil {
			return nil, fail("DISCOVERY_PREVIEW_POLICY_INVALID")
		}
	}
	policy, err = discoverypreview.Policy(ctx, policy, present)
	if err != nil {
		return nil, previewFailure(err)
	}
	identityInput := assessmentcontext.CacheInputs{Month: month, Versions: discoverypreview.EngineVersions(),
		Capture: assessmentcontext.CacheCapture{DiscoveryID: capture.DiscoveryID, ContentSHA256: capture.ContentSHA256, Scope: capture.ContentScope, Title: capture.Title, Company: capture.Company, ObservedURL: capture.ObservedURL}}
	identity, identityErr := assessmentcontext.ReadCacheIdentity(ctx, data, resources, identityInput)
	if identityErr != nil {
		return nil, assessmentReadError(identityErr)
	}
	var sourceReadFailure *discovery.Error
	input := discoverypreview.Input{
		Capture:  discoverypreview.Capture{Identity: identityInput.Capture, Source: text, RelativePath: capture.SourceRelativePath},
		Registry: evidence.Registry, Evaluation: jobevaluation.Input{Profile: evidence.Candidate.Eligibility, Policy: policy, Index: evidence.Candidate.Index, DurationMonths: evidence.Candidate.DurationMonths, Concepts: evidence.Concepts, Expansions: evidence.Expansions},
		CheckSource: func(checkCtx context.Context, path string) (bool, *jobcard.Error) {
			if path != capture.SourceRelativePath {
				return false, nil
			}
			current, e := discovery.ReadCapturedSource(checkCtx, dataSource, capture.Record)
			if e != nil {
				sourceReadFailure = e
				return false, &jobcard.Error{Code: "JOB_CARD_SOURCE_UNAVAILABLE"}
			}
			return current == text, nil
		}}
	var entry *previewcache.Entry
	var prepared *discoverypreview.Prepared
	hit := false
	if cached {
		entry, err = previewcache.Load(ctx, dataSource, capture.DiscoveryID, identity)
		if err != nil {
			return nil, fail("DISCOVERY_PREVIEW_CACHE_READ_FAILED")
		}
		if entry.Disposition == previewcache.Match {
			prepared, err = discoverypreview.Restore(ctx, input, entry.Prepared)
			if sourceReadFailure != nil {
				return nil, discoveryReadError(sourceReadFailure)
			}
			if canceled := assessmentCanceled(ctx); canceled != nil {
				return nil, canceled
			}
			if err != nil {
				entry.Disposition, entry.Prepared = previewcache.Invalid, nil
			} else {
				hit = true
			}
		}
	}
	if !hit {
		prepared, err = discoverypreview.Build(ctx, input)
	}
	if err != nil {
		return nil, previewFailure(err)
	}
	if err := data.reader.verify(); err != nil {
		return nil, err
	}
	if err := resources.reader.verify(); err != nil {
		return nil, err
	}
	current, sourceErr := discovery.ReadCapturedSource(ctx, dataSource, capture.Record)
	if sourceErr != nil {
		return nil, discoveryReadError(sourceErr)
	}
	if current != text {
		return nil, fail("DISCOVERY_PREVIEW_INPUT_CHANGED")
	}
	if err := assessmentCanceled(ctx); err != nil {
		return nil, err
	}
	return &DiscoveryPreview{Prepared: prepared, Identity: &identity, Receipt: prepared.Artifact.Receipt, Instructions: prepared.Artifact.Instructions, Contacts: prepared.Artifact.Contacts, Cache: entry, CacheHit: hit}, nil
}
func previewFailure(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fail("ASSESSMENT_INPUT_CANCELED")
	}
	switch {
	case errors.Is(err, discoverypreview.ErrPolicy):
		return fail("DISCOVERY_PREVIEW_POLICY_INVALID")
	case errors.Is(err, discoverypreview.ErrLimit):
		return fail("DISCOVERY_PREVIEW_INPUT_LIMIT")
	case errors.Is(err, discoverypreview.ErrSource):
		return fail("DISCOVERY_PREVIEW_SOURCE_INVALID")
	case errors.Is(err, discoverypreview.ErrInput):
		return fail("DISCOVERY_PREVIEW_INPUT_INVALID")
	}
	var evaluationErr *jobevaluation.Error
	if errors.As(err, &evaluationErr) {
		if evaluationErr.Code == "EVALUATION_CANCELED" {
			return fail("ASSESSMENT_INPUT_CANCELED")
		}
		return fail("DISCOVERY_PREVIEW_EVALUATION_FAILED")
	}
	return digestFailure(err)
}
