package readonly

import (
	"context"
	"crypto/sha256"
	"sort"
	"strings"

	"careerhub.local/backend/internal/applications/applicationanalysis"
	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/textcompat"
	"golang.org/x/text/unicode/norm"
)

const maxIntakeIdentityCards = 4096
const maxIntakeIdentityBytes = 128 << 20

// NewJobDuplicate is one verified, observed match; it is not a persisted
// application, a freshness guarantee for a later write, or permission to apply.
type NewJobDuplicate struct {
	JobID, Reason, CardPath, RawPath string
	CardFileSHA256, RawFileSHA256    [32]byte
}

type intakeIdentity struct {
	NewJobDuplicate
	fingerprint, postingIdentity, retryKey string
}

type intakeFileObservation struct {
	path    string
	limit   int64
	present bool
	hash    [32]byte
}

// The Job Card reader can access only a direct normalized card and its direct
// incoming source. Each unique file consumes a bounded byte budget and is
// rechecked after the full scan; no card/cache field alone is trusted.
type intakeIdentitySource struct {
	ctx      context.Context
	source   evaluationSource
	observed map[string]intakeFileObservation
	bytes    int64
	limitHit bool
}

func intakeIdentityPath(path string, limit int64) bool {
	if strings.Count(path, "/") != 2 {
		return false
	}
	return strings.HasPrefix(path, "jobs/normalized/") && limit == cataloginput.YAMLMaxInputBytes ||
		strings.HasPrefix(path, "jobs/incoming/") && limit == applicationanalysis.MaxSourceBytes
}

func (r *intakeIdentitySource) Check(path string) *runtimepaths.Error {
	if !intakeIdentityPath(path, cataloginput.YAMLMaxInputBytes) && !intakeIdentityPath(path, applicationanalysis.MaxSourceBytes) {
		return &runtimepaths.Error{Code: "JOB_INTAKE_EXISTING_PATH_INVALID"}
	}
	if r.ctx == nil || r.ctx.Err() != nil {
		return &runtimepaths.Error{Code: "JOB_INTAKE_EXISTING_CANCELED"}
	}
	return r.source.Check(path)
}

func (r *intakeIdentitySource) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	if !intakeIdentityPath(path, limit) || r.ctx == nil || r.ctx.Err() != nil {
		return nil, false, &runtimepaths.Error{Code: "JOB_INTAKE_EXISTING_PATH_INVALID"}
	}
	raw, present, err := r.source.ReadFile(path, limit)
	if err != nil {
		return nil, false, err
	}
	if r.ctx.Err() != nil {
		return nil, false, &runtimepaths.Error{Code: "JOB_INTAKE_EXISTING_CANCELED"}
	}
	observed := intakeFileObservation{path: path, limit: limit, present: present, hash: sha256.Sum256(raw)}
	if before, found := r.observed[path]; found {
		if before != observed {
			return nil, false, &runtimepaths.Error{Code: "JOB_INTAKE_EXISTING_CHANGED"}
		}
		return raw, present, nil
	}
	r.bytes += int64(len(raw))
	if r.bytes > maxIntakeIdentityBytes {
		r.limitHit = true
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	r.observed[path] = observed
	return raw, present, nil
}

func (r *intakeIdentitySource) verify() error {
	paths := make([]string, 0, len(r.observed))
	for path := range r.observed {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := assessmentCanceled(r.ctx); err != nil {
			return err
		}
		before := r.observed[path]
		raw, present, err := r.source.ReadFile(path, before.limit)
		if err != nil || present != before.present || sha256.Sum256(raw) != before.hash {
			return fail("JOB_INTAKE_EXISTING_CHANGED")
		}
	}
	return assessmentCanceled(r.ctx)
}

func scanIntakeIdentities(ctx context.Context, source evaluationSource, names []string) ([]intakeIdentity, error) {
	if err := assessmentCanceled(ctx); err != nil {
		return nil, err
	}
	if source == nil || len(names) > runtimepaths.MaxDirectoryEntries {
		return nil, fail("JOB_INTAKE_EXISTING_UNAVAILABLE")
	}
	reader := &intakeIdentitySource{ctx: ctx, source: source, observed: map[string]intakeFileObservation{}}
	result := []intakeIdentity{}
	seenIDs := map[string]bool{}
	for _, name := range names {
		if !strings.HasSuffix(strings.ToLower(name), ".yaml") && !strings.HasSuffix(strings.ToLower(name), ".yml") {
			continue
		}
		if len(result) == maxIntakeIdentityCards {
			return nil, fail("JOB_INTAKE_INDEX_LIMIT")
		}
		path := "jobs/normalized/" + name
		// Reject a misbound card before its raw_jd_path is dereferenced by
		// jobcard.Read. The observed card bytes must remain identical there.
		cardRaw, present, readErr := reader.ReadFile(path, cataloginput.YAMLMaxInputBytes)
		if readErr != nil || !present {
			if reader.limitHit {
				return nil, fail("JOB_INTAKE_INDEX_LIMIT")
			}
			return nil, fail("JOB_INTAKE_EXISTING_INVALID")
		}
		cardValue, parseErr := cataloginput.ParseYAML(cardRaw)
		if parseErr != nil {
			return nil, fail("JOB_INTAKE_EXISTING_INVALID")
		}
		id := cardDigestField(cardValue, "id")
		rawPath := cardDigestField(cardValue, "raw_jd_path")
		identityKey := textcompat.Fold(norm.NFC.String(id))
		if !jobcard.ValidID(id) || (path != "jobs/normalized/"+id+".yaml" && path != "jobs/normalized/"+id+".yml") ||
			(rawPath != "jobs/incoming/"+id+".md" && rawPath != "jobs/incoming/"+id+".txt") || seenIDs[identityKey] {
			return nil, fail("JOB_INTAKE_EXISTING_INVALID")
		}
		loaded, err := jobcard.Read(ctx, reader, path)
		if err != nil || loaded == nil || loaded.Report == nil || !loaded.Report.SchemaValid() || !loaded.RawPresent {
			if canceled := assessmentCanceled(ctx); canceled != nil {
				return nil, canceled
			}
			if reader.limitHit {
				return nil, fail("JOB_INTAKE_INDEX_LIMIT")
			}
			return nil, fail("JOB_INTAKE_EXISTING_INVALID")
		}
		seenIDs[identityKey] = true
		sourceText, sourceErr := jobcard.IntakeDigestSource(loaded.RawText)
		if sourceErr != nil {
			return nil, fail("JOB_INTAKE_EXISTING_INVALID")
		}
		fingerprint, fpErr := applicationanalysis.SourceFingerprint(sourceText)
		if fpErr != nil {
			return nil, fail("JOB_INTAKE_EXISTING_INVALID")
		}
		storedFingerprint, _, valid := optionalIntakeString(loaded.Card, "content_fingerprint")
		if !valid || storedFingerprint != "" && storedFingerprint != fingerprint {
			return nil, fail("JOB_INTAKE_EXISTING_INVALID")
		}
		postingID, _, valid := optionalIntakeString(loaded.Card, "posting_id")
		if !valid {
			return nil, fail("JOB_INTAKE_EXISTING_INVALID")
		}
		posting := jobcard.IntakePostingIdentity(cardDigestField(loaded.Card, "source_url"), postingID)
		storedPosting, present, valid := optionalIntakeString(loaded.Card, "posting_identity")
		if !valid || present && storedPosting != posting {
			return nil, fail("JOB_INTAKE_EXISTING_INVALID")
		}
		storedKey, _, valid := optionalIntakeString(loaded.Card, "intake_idempotency_key")
		key, keyErr := jobcard.NormalizeIntakeKey(storedKey)
		if !valid || keyErr != nil || key != storedKey {
			return nil, fail("JOB_INTAKE_EXISTING_INVALID")
		}
		result = append(result, intakeIdentity{NewJobDuplicate: NewJobDuplicate{JobID: id, CardPath: path, RawPath: rawPath,
			CardFileSHA256: loaded.CardFileSHA256, RawFileSHA256: loaded.RawFileSHA256},
			fingerprint: fingerprint, postingIdentity: posting, retryKey: key})
	}
	if err := reader.verify(); err != nil {
		return nil, err
	}
	return result, nil
}

func optionalIntakeString(card cataloginput.Value, name string) (string, bool, bool) {
	if card.Kind != "mapping" {
		return "", false, false
	}
	for _, entry := range card.Entries {
		if entry.Key.Kind == "string" && entry.Key.Text == name {
			if entry.Value.Kind == "null" {
				return "", false, true
			}
			if entry.Value.Kind != "string" {
				return "", true, false
			}
			return entry.Value.Text, true, true
		}
	}
	return "", false, true
}

func decideIntakeDuplicate(fingerprint, postingIdentity, retryKey string, existing []intakeIdentity) (*NewJobDuplicate, error) {
	match := func(eligible func(intakeIdentity) bool) (*intakeIdentity, error) {
		var found *intakeIdentity
		for index := range existing {
			if !eligible(existing[index]) {
				continue
			}
			if found != nil {
				return nil, fail("JOB_INTAKE_DUPLICATE_AMBIGUOUS")
			}
			found = &existing[index]
		}
		return found, nil
	}
	result := func(found *intakeIdentity, reason string) *NewJobDuplicate {
		if found == nil {
			return nil
		}
		candidate := found.NewJobDuplicate
		candidate.Reason = reason
		return &candidate
	}
	if retryKey != "" {
		found, err := match(func(entry intakeIdentity) bool { return entry.retryKey == retryKey })
		if err != nil || found != nil {
			if found != nil && found.fingerprint != fingerprint {
				return nil, fail("JOB_INTAKE_RETRY_CONFLICT")
			}
			return result(found, "retry_key"), err
		}
	}
	if postingIdentity != "" {
		found, err := match(func(entry intakeIdentity) bool { return entry.postingIdentity == postingIdentity })
		return result(found, "posting"), err
	}
	found, err := match(func(entry intakeIdentity) bool { return entry.fingerprint == fingerprint })
	return result(found, "content"), err
}
