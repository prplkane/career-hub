package applicationwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"path"

	"careerhub.local/backend/internal/intake/resumelibrary"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

type intakeRecoveryResult struct {
	State string
	Facts intakePublicationFacts
}

// intakeLiveWitness returns verified original bytes only when the active
// Resume Version and the exact effective catalog still match the sealed
// intent. A definite mismatch is distinct from an uninspectable source.
type intakeLiveWitness func(context.Context) (source []byte, matches bool, problem *Error)

// recoverFrozenIntake settles only one previously sealed, owned intake intent.
// Its caller retains the native gate, both roots, guarded SQLite files and an
// IMMEDIATE transaction through this call. It never re-extracts or overwrites
// the stored report; report presence is the reader-visible commit boundary.
func recoverFrozenIntake(ctx context.Context, root *runtimepaths.Root, p intakeIntent, sealed []byte, live intakeLiveWitness, checkpoint func(string) *Error) (result intakeRecoveryResult, problem *Error) {
	result.Facts.IntentRecorded = true
	if root == nil || live == nil || ctx == nil {
		return result, failed("configuration", "INTAKE_RECOVERY_UNAVAILABLE")
	}
	want, e := encodeIntakeIntent(p)
	if e != nil || !bytes.Equal(sealed, want) {
		return result, failed("intent", "INTAKE_INTENT_INVALID")
	}
	home, admitted := datahome.InspectOwnedForIntakeRecovery(root, nil)
	if admitted != nil {
		return result, failed("admission", admitted.Code)
	}
	if *home != p.Home || !intakeFileEquals(root, datahome.IntakeRecoveryPath, sealed) {
		return result, failed("intent", "INTAKE_INTENT_CHANGED")
	}
	step := func(stage string) *Error {
		if ctx.Err() != nil {
			return failed("context", "INTAKE_CANCELED")
		}
		if checkpoint != nil {
			return checkpoint(stage)
		}
		return nil
	}
	rawPresent, e2 := intakeRecoveryFile(root, p.RawPath, p.Resume.Size, nil, p.Resume.SHA256)
	if e2 != nil {
		return result, e2
	}
	if rawPresent {
		result.Facts.RawPublished = true
	}
	reportPresent, e2 := intakeRecoveryFile(root, p.ReportPath, len(p.Report), p.Report, "")
	if e2 != nil {
		return result, e2
	}
	if reportPresent {
		result.Facts.ReportPublished = true
	}
	diagnostic, e2 := intakeRecoveryDiagnostic(root, p)
	if e2 != nil {
		return result, e2
	}
	result.Facts.DiagnosticPublished = diagnostic != "absent"
	if diagnostic == "aborted" {
		if rawPresent || reportPresent {
			return result, failed("recovery", "INTAKE_ARTIFACT_CONFLICT")
		}
		result.State = "aborted_before_raw"
		if e := intakeRemoveIntent(root, sealed, &result.Facts); e != nil {
			return result, e
		}
		return result, nil
	}
	if reportPresent && !rawPresent || !reportPresent && diagnostic == "success" || rawPresent && !reportPresent && diagnostic != "publishing" {
		return result, failed("recovery", "INTAKE_ARTIFACT_CONFLICT")
	}
	if e := step("classified"); e != nil {
		return result, e
	}
	source, matches, e2 := live(ctx)
	if e2 != nil {
		return result, e2
	}
	if matches && (len(source) != p.Resume.Size || digestBytes(source) != p.Resume.SHA256) {
		return result, failed("witness", "INTAKE_WITNESS_INVALID")
	}
	if !rawPresent && !reportPresent && !matches {
		return intakeAbortBeforeRaw(ctx, root, p, sealed, diagnostic, result, step)
	}
	if rawPresent && !reportPresent && !matches {
		return result, failed("witness", "INTAKE_WITNESS_CHANGED")
	}
	stale := reportPresent && !matches
	if !reportPresent {
		for _, directory := range []string{path.Dir(p.RawPath), path.Dir(p.ReportPath), path.Dir(p.DiagnosticPath)} {
			if _, e := root.EnsureDirectory(directory); e != nil {
				return result, failed("directory", e.Code)
			}
		}
		if diagnostic == "absent" {
			if e := intakeCreateAndVerify(root, p.DiagnosticPath, p.ProvisionalDiagnostic, &result.Facts.DiagnosticPublished, &result.Facts.Unconfirmed); e != nil {
				return result, failed("diagnostic", e.Code)
			}
			diagnostic = "publishing"
		}
		if e := step("before_raw"); e != nil {
			return result, e
		}
		if !rawPresent {
			if e := intakeCreateAndVerify(root, p.RawPath, source, &result.Facts.RawPublished, &result.Facts.Unconfirmed); e != nil {
				return result, failed("raw", e.Code)
			}
		}
		if e := step("before_report"); e != nil {
			return result, e
		}
		source, matches, e2 = live(ctx)
		if e2 != nil {
			return result, e2
		}
		if !matches || len(source) != p.Resume.Size || digestBytes(source) != p.Resume.SHA256 {
			return result, failed("witness", "INTAKE_WITNESS_CHANGED")
		}
		if e := intakeCreateAndVerify(root, p.ReportPath, p.Report, &result.Facts.ReportPublished, &result.Facts.Unconfirmed); e != nil {
			return result, failed("report", e.Code)
		}
		if e := step("report"); e != nil {
			return result, e
		}
		_, matches, e2 = live(ctx)
		if e2 != nil {
			return result, e2
		}
		stale = !matches
	}
	result.State = "recovered_published"
	if stale {
		result.State = "historical_stale"
	}
	if e := intakeFinishRecovered(root, p, sealed, diagnostic, &result.Facts, step); e != nil {
		return result, e
	}
	return result, nil
}

func intakeRecoveryFile(root *runtimepaths.Root, name string, limit int, expected []byte, expectedSHA string) (bool, *Error) {
	if limit < 0 || limit > resumelibrary.MaxResumeBytes {
		return false, failed("recovery", "INTAKE_ARTIFACT_CONFLICT")
	}
	raw, present, e := root.ReadSingleLinkFile(name, int64(limit))
	if e != nil {
		return false, failed("recovery", "INTAKE_ARTIFACT_CONFLICT")
	}
	if !present {
		return false, nil
	}
	if len(raw) != limit || expected != nil && !bytes.Equal(raw, expected) || expectedSHA != "" && digestBytes(raw) != expectedSHA {
		return false, failed("recovery", "INTAKE_ARTIFACT_CONFLICT")
	}
	return true, nil
}

func intakeRecoveryDiagnostic(root *runtimepaths.Root, p intakeIntent) (string, *Error) {
	raw, present, e := root.ReadSingleLinkFile(p.DiagnosticPath, intakeDiagnosticLimit)
	if e != nil {
		return "", failed("recovery", "INTAKE_ARTIFACT_CONFLICT")
	}
	if !present {
		return "absent", nil
	}
	if bytes.Equal(raw, p.ProvisionalDiagnostic) {
		return "publishing", nil
	}
	if bytes.Equal(raw, p.TerminalDiagnostic) {
		return "success", nil
	}
	if bytes.Equal(raw, intakeAbortedDiagnostic(p)) {
		return "aborted", nil
	}
	return "", failed("recovery", "INTAKE_ARTIFACT_CONFLICT")
}

func intakeAbortedDiagnostic(p intakeIntent) []byte {
	raw, _ := json.Marshal(intakeDiagnostic{SchemaVersion: 1, OperationID: p.OperationID, SourceID: p.SourceID,
		State: "aborted_before_raw", Stage: "completed", ErrorCode: "INTAKE_WITNESS_CHANGED"})
	return raw
}

func intakeAbortBeforeRaw(ctx context.Context, root *runtimepaths.Root, p intakeIntent, sealed []byte, diagnostic string, result intakeRecoveryResult, step func(string) *Error) (intakeRecoveryResult, *Error) {
	if ctx.Err() != nil {
		return result, failed("context", "INTAKE_CANCELED")
	}
	aborted := intakeAbortedDiagnostic(p)
	if _, e := root.EnsureDirectory(path.Dir(p.DiagnosticPath)); e != nil {
		return result, failed("directory", e.Code)
	}
	if diagnostic == "absent" {
		if e := intakeCreateAndVerify(root, p.DiagnosticPath, aborted, &result.Facts.DiagnosticPublished, &result.Facts.Unconfirmed); e != nil {
			return result, failed("diagnostic", e.Code)
		}
	} else {
		committed, e := root.ReplaceFile(p.DiagnosticPath, p.ProvisionalDiagnostic, aborted)
		if e != nil || !committed || !intakeFileEquals(root, p.DiagnosticPath, aborted) {
			result.Facts.Unconfirmed = true
			if e != nil {
				return result, failed("diagnostic", e.Code)
			}
			return result, failed("diagnostic", "INTAKE_DIAGNOSTIC_UNCONFIRMED")
		}
	}
	if e := step("aborted_diagnostic"); e != nil {
		return result, e
	}
	result.State = "aborted_before_raw"
	if e := intakeRemoveIntent(root, sealed, &result.Facts); e != nil {
		return result, e
	}
	return result, nil
}

func intakeFinishRecovered(root *runtimepaths.Root, p intakeIntent, sealed []byte, diagnostic string, facts *intakePublicationFacts, step func(string) *Error) *Error {
	if diagnostic == "absent" {
		if _, e := root.EnsureDirectory(path.Dir(p.DiagnosticPath)); e != nil {
			return failed("directory", e.Code)
		}
		if e := intakeCreateAndVerify(root, p.DiagnosticPath, p.TerminalDiagnostic, &facts.DiagnosticPublished, &facts.Unconfirmed); e != nil {
			return failed("diagnostic", e.Code)
		}
	} else if diagnostic == "publishing" {
		committed, e := root.ReplaceFile(p.DiagnosticPath, p.ProvisionalDiagnostic, p.TerminalDiagnostic)
		if e != nil || !committed || !intakeFileEquals(root, p.DiagnosticPath, p.TerminalDiagnostic) {
			facts.Unconfirmed = true
			if e != nil {
				return failed("diagnostic", e.Code)
			}
			return failed("diagnostic", "INTAKE_DIAGNOSTIC_UNCONFIRMED")
		}
	}
	if e := step("terminal_diagnostic"); e != nil {
		return e
	}
	return intakeRemoveIntent(root, sealed, facts)
}

func intakeRemoveIntent(root *runtimepaths.Root, sealed []byte, facts *intakePublicationFacts) *Error {
	removed, e := root.RemoveFile(datahome.IntakeRecoveryPath, sealed)
	if removed {
		facts.IntentRemoved = true
	}
	if e != nil {
		facts.Unconfirmed = !removed
		return failed("cleanup", e.Code)
	}
	if !removed {
		facts.Unconfirmed = true
		return failed("cleanup", "INTAKE_INTENT_UNCONFIRMED")
	}
	return nil
}
