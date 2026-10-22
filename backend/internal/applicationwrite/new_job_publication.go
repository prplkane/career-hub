package applicationwrite

import (
	"bytes"
	"context"

	"careerhub.local/backend/internal/applicationanalysis"
	"careerhub.local/backend/internal/cataloginput"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/jobcard"
	"careerhub.local/backend/internal/runtimepaths"
)

// These are separate facts: a native call may report false+error after its
// effect, while readback can observe matching bytes without attributing who
// created them. An uncertain call always retains the pending intent.
type newJobPublicationFacts struct {
	// Guarded namespace creation can leave empty directories, even on error.
	// This is not a JD file/SQL commit or a pending publication intent.
	NamespaceMayHaveChanged                         bool
	IntentAttempted, IntentReported, IntentObserved bool
	RawAttempted, RawReported, RawObserved          bool
	CardAttempted, CardReported, CardObserved       bool
	Unconfirmed                                     bool
}

func (f newJobPublicationFacts) recoveryRequired() bool {
	return f.IntentAttempted || f.IntentObserved || f.Unconfirmed
}

type newJobPublicationChecks struct {
	// The future coordinator must call the owned duplicate/namespace/catalog
	// inspector under its retained operation gate before this function and
	// recheck that exact read set at each callback. Nil refuses publication.
	witnesses func(context.Context) *Error
	// A fixture-only mutation seam. Nil calls Root.CreateFile in production.
	create func(*runtimepaths.Root, string, []byte) (bool, *runtimepaths.Error)
	// A fixture-only fault seam at complete operation boundaries.
	checkpoint func(string) *Error
}

func (c newJobPublicationChecks) unchanged(ctx context.Context) *Error {
	if ctx == nil || ctx.Err() != nil {
		return canceled()
	}
	if c.witnesses == nil {
		return failed("configuration", "JOB_INTAKE_WITNESS_REQUIRED")
	}
	return c.witnesses(ctx)
}

func (c newJobPublicationChecks) step(stage string) *Error {
	if c.checkpoint != nil {
		return c.checkpoint(stage)
	}
	return nil
}

// publishFrozenNewJob is the private file publication primitive. Its caller
// must retain the process-owned Home, resource roots and global operation gate
// from duplicate inspection through return. After validation it prepares only
// the two fixed intake directories, reporting any possible directory effects
// separately. It writes one journal, then one
// raw JD, then one validated draft card, and leaves the journal in place for
// later ledger/assessment/status composition. It never removes or replays an
// uncertain file, nor claims the full job intake succeeded.
func publishFrozenNewJob(ctx context.Context, root *runtimepaths.Root, intent newJobIntent, raw, card []byte, checks newJobPublicationChecks) (facts newJobPublicationFacts, problem *Error) {
	if root == nil || !validNewJobIntent(intent) || len(raw) != intent.RawSize || digestBytes(raw) != intent.RawSHA256 ||
		len(card) != intent.CardSize || digestBytes(card) != intent.CardSHA256 {
		return facts, failed("preflight", "JOB_INTAKE_INPUT_INVALID")
	}
	if e := validateFrozenNewJob(ctx, intent, raw, card); e != nil {
		return facts, e
	}
	if e := checks.unchanged(ctx); e != nil {
		return facts, e
	}
	home, admissionErr := datahome.InspectOwnedForNewJobRecovery(root, nil)
	if admissionErr != nil {
		return facts, failed("admission", admissionErr.Code)
	}
	if *home != intent.Home {
		return facts, failed("admission", "JOB_INTAKE_HOME_CHANGED")
	}
	for _, directory := range []string{"jobs/incoming", "jobs/normalized"} {
		changed, err := root.EnsureDirectory(directory)
		facts.NamespaceMayHaveChanged = facts.NamespaceMayHaveChanged || changed
		if err != nil {
			return facts, failed("namespace", "JOB_INTAKE_STORAGE_UNAVAILABLE")
		}
		if changed {
			if e := checks.step("namespace:" + directory); e != nil {
				return facts, e
			}
			if e := checks.unchanged(ctx); e != nil {
				return facts, e
			}
			current, e := datahome.InspectOwnedForNewJobRecovery(root, nil)
			if e != nil || current == nil || *current != intent.Home {
				return facts, failed("admission", "JOB_INTAKE_HOME_CHANGED")
			}
		}
	}
	receiptPath, _, receiptErr := newJobTerminalReceiptFile(intent)
	if receiptErr != nil {
		return facts, receiptErr
	}
	for _, path := range []string{datahome.NewJobRecoveryPath, intent.RawPath, intent.CardPath, receiptPath} {
		present, err := root.Exists(path)
		if err != nil {
			return facts, failed("preflight", "JOB_INTAKE_PATH_UNAVAILABLE")
		}
		if present {
			return facts, failed("preflight", "JOB_INTAKE_PATH_CONFLICT")
		}
	}
	if e := checks.step("preflight"); e != nil {
		return facts, e
	}
	sealed, encodeErr := encodeNewJobIntent(intent)
	if encodeErr != nil {
		return facts, encodeErr
	}
	if e := newJobCreateAndObserve(root, datahome.NewJobRecoveryPath, sealed, "intent", checks.create,
		&facts.IntentAttempted, &facts.IntentReported, &facts.IntentObserved, &facts.Unconfirmed); e != nil {
		return facts, e
	}
	if e := checks.step("intent"); e != nil {
		return facts, e
	}
	if e := checks.unchanged(ctx); e != nil {
		return facts, e
	}
	if e := verifyNewJobPublicationState(root, intent, sealed, nil, nil); e != nil {
		facts.Unconfirmed = true
		return facts, e
	}
	if e := newJobCreateAndObserve(root, intent.RawPath, raw, "raw", checks.create,
		&facts.RawAttempted, &facts.RawReported, &facts.RawObserved, &facts.Unconfirmed); e != nil {
		return facts, e
	}
	if e := checks.step("raw"); e != nil {
		return facts, e
	}
	if e := checks.unchanged(ctx); e != nil {
		return facts, e
	}
	// A changed Home, journal or raw JD cannot authorize a card publication.
	if e := verifyNewJobPublicationState(root, intent, sealed, raw, nil); e != nil {
		facts.Unconfirmed = true
		return facts, e
	}
	if e := newJobCreateAndObserve(root, intent.CardPath, card, "card", checks.create,
		&facts.CardAttempted, &facts.CardReported, &facts.CardObserved, &facts.Unconfirmed); e != nil {
		return facts, e
	}
	if e := checks.step("card"); e != nil {
		return facts, e
	}
	if e := checks.unchanged(ctx); e != nil {
		return facts, e
	}
	if e := verifyNewJobPublicationState(root, intent, sealed, raw, card); e != nil {
		facts.Unconfirmed = true
		return facts, e
	}
	return facts, nil
}

func verifyNewJobPublicationState(root *runtimepaths.Root, intent newJobIntent, sealed, raw, card []byte) *Error {
	home, admissionErr := datahome.InspectOwnedForNewJobRecovery(root, nil)
	if admissionErr != nil {
		return failed("admission", admissionErr.Code)
	}
	if home == nil || *home != intent.Home {
		return failed("admission", "JOB_INTAKE_HOME_CHANGED")
	}
	current, present, readErr := root.ReadSingleLinkFile(datahome.NewJobRecoveryPath, newJobIntentLimit)
	if readErr != nil || !present || !bytes.Equal(current, sealed) {
		return failed("intent", "JOB_INTAKE_INTENT_CHANGED")
	}
	for _, item := range []struct {
		path     string
		expected []byte
	}{
		{intent.RawPath, raw}, {intent.CardPath, card},
	} {
		if item.expected == nil {
			continue
		}
		observed, present, err := root.ReadSingleLinkFile(item.path, int64(len(item.expected)))
		if err != nil || !present || !bytes.Equal(observed, item.expected) {
			return failed("source", "JOB_INTAKE_SOURCE_CHANGED")
		}
	}
	return nil
}

func validateFrozenNewJob(ctx context.Context, intent newJobIntent, raw, card []byte) *Error {
	if ctx == nil || ctx.Err() != nil {
		return canceled()
	}
	source, sourceErr := jobcard.IntakeDigestSource(string(raw))
	if sourceErr != nil {
		return failed("preflight", "JOB_INTAKE_INPUT_INVALID")
	}
	fingerprint, fingerprintErr := applicationanalysis.SourceFingerprint(source)
	if fingerprintErr != nil || fingerprint != intent.ContentFingerprint {
		return failed("preflight", "JOB_INTAKE_INPUT_INVALID")
	}
	value, parseErr := cataloginput.ParseYAML(card)
	if parseErr != nil || newJobCardField(value, "id") != intent.JobID ||
		newJobCardField(value, "raw_jd_path") != intent.RawPath || newJobCardField(value, "status") != "draft" ||
		newJobCardField(value, "content_fingerprint") != fingerprint {
		return failed("preflight", "JOB_INTAKE_CARD_INVALID")
	}
	report, validationErr := jobcard.Validate(ctx, value, func(_ context.Context, relative string) (bool, *jobcard.Error) {
		return relative == intent.RawPath, nil
	})
	if validationErr != nil || report == nil || !report.SchemaValid() {
		if ctx.Err() != nil {
			return canceled()
		}
		return failed("preflight", "JOB_INTAKE_CARD_INVALID")
	}
	return nil
}

func newJobCardField(card cataloginput.Value, name string) string {
	if card.Kind != "mapping" {
		return ""
	}
	for _, entry := range card.Entries {
		if entry.Key.Kind == "string" && entry.Key.Text == name && entry.Value.Kind == "string" {
			return entry.Value.Text
		}
	}
	return ""
}

func newJobCreateAndObserve(root *runtimepaths.Root, path string, expected []byte, stage string,
	create func(*runtimepaths.Root, string, []byte) (bool, *runtimepaths.Error), attempted, reported, observed, unconfirmed *bool) *Error {
	if create == nil {
		create = (*runtimepaths.Root).CreateFile
	}
	*attempted = true
	created, createErr := create(root, path, expected)
	*reported = created
	readback, present, readErr := root.ReadSingleLinkFile(path, int64(len(expected)))
	*observed = readErr == nil && present && bytes.Equal(readback, expected)
	if createErr != nil || !created || !*observed {
		*unconfirmed = true
		return failed(stage, "JOB_INTAKE_FILE_UNCONFIRMED")
	}
	return nil
}
