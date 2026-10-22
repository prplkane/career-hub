package applicationwrite

import (
	"bytes"
	"context"
	"path"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/resumelibrary"
	"careerhub.local/backend/internal/runtimepaths"
)

// intakePublicationFacts describe only observed filesystem facts. In
// particular, a failed CreateFile is not evidence that its destination is
// absent, and a published report is never rolled back by this component.
type intakePublicationFacts struct {
	IntentRecorded      bool
	DiagnosticPublished bool
	RawPublished        bool
	ReportPublished     bool
	IntentRemoved       bool
	Unconfirmed         bool
}

func (f intakePublicationFacts) recoveryRequired() bool {
	return (f.IntentRecorded || f.Unconfirmed) && !f.IntentRemoved
}

type intakePublicationChecks struct {
	// The future owner must recheck the active Resume Version and all four
	// catalog bytes under its retained SQLite reservation at every call.
	witnesses  func(context.Context) *Error
	checkpoint func(string) *Error // fixture-only fault seam; nil means no fault
	mutations  intakeFileMutations // fixture-only overrides; zero value uses Root
}

// intakeFileMutations is deliberately private and operation-scoped. Tests can
// make a native call take effect and still return an ambiguous result without
// replacing path admission, readback, ownership, or witness verification.
// Production always uses the zero value and therefore the unchanged Root APIs.
type intakeFileMutations struct {
	create  func(*runtimepaths.Root, string, []byte) (bool, *runtimepaths.Error)
	replace func(*runtimepaths.Root, string, []byte, []byte) (bool, *runtimepaths.Error)
	remove  func(*runtimepaths.Root, string, []byte) (bool, *runtimepaths.Error)
}

func (m intakeFileMutations) createFile(root *runtimepaths.Root, name string, content []byte) (bool, *runtimepaths.Error) {
	if m.create != nil {
		return m.create(root, name, content)
	}
	return root.CreateFile(name, content)
}

func (m intakeFileMutations) replaceFile(root *runtimepaths.Root, name string, expected, replacement []byte) (bool, *runtimepaths.Error) {
	if m.replace != nil {
		return m.replace(root, name, expected, replacement)
	}
	return root.ReplaceFile(name, expected, replacement)
}

func (m intakeFileMutations) removeFile(root *runtimepaths.Root, name string, expected []byte) (bool, *runtimepaths.Error) {
	if m.remove != nil {
		return m.remove(root, name, expected)
	}
	return root.RemoveFile(name, expected)
}

func (c intakePublicationChecks) step(stage string) *Error {
	if c.checkpoint != nil {
		return c.checkpoint(stage)
	}
	return nil
}

func (c intakePublicationChecks) unchanged(ctx context.Context) *Error {
	if ctx == nil || ctx.Err() != nil {
		return failed("context", "INTAKE_CANCELED")
	}
	if c.witnesses == nil {
		return failed("configuration", "INTAKE_WITNESS_REQUIRED")
	}
	return c.witnesses(ctx)
}

// publishFrozenIntake is deliberately private and unconnected. Its caller must
// hold the application gate, resource/Data Home roots, guarded SQLite files,
// and a BEGIN IMMEDIATE reservation through return. The source and report have
// already been extracted and validated in memory. This function only publishes
// their fixed files, retaining the journal on every uncertain/error path.
func publishFrozenIntake(ctx context.Context, root *runtimepaths.Root, intent intakeIntent, source []byte, checks intakePublicationChecks) (facts intakePublicationFacts, problem *Error) {
	if root == nil || !validIntakeIntent(intent) || len(source) == 0 || len(source) > resumelibrary.MaxResumeBytes ||
		len(source) != intent.Resume.Size || digestBytes(source) != intent.Resume.SHA256 {
		return facts, failed("preflight", "INTAKE_INPUT_INVALID")
	}
	if e := checks.unchanged(ctx); e != nil {
		return facts, e
	}
	home, e := datahome.InspectOwnedForIntakeRecovery(root, nil)
	if e != nil {
		return facts, failed("admission", e.Code)
	}
	if *home != intent.Home {
		return facts, failed("admission", "INTAKE_HOME_CHANGED")
	}
	for _, target := range []string{datahome.IntakeRecoveryPath, intent.RawPath, path.Dir(intent.ReportPath), intent.ReportPath, intent.DiagnosticPath} {
		present, pe := root.Exists(target)
		if pe != nil {
			return facts, failed("preflight", pe.Code)
		}
		if present {
			return facts, failed("preflight", "INTAKE_PATH_CONFLICT")
		}
	}
	if e := checks.step("preflight"); e != nil {
		return facts, e
	}
	sealed, e2 := encodeIntakeIntent(intent)
	if e2 != nil {
		return facts, e2
	}
	created, pe := checks.mutations.createFile(root, datahome.IntakeRecoveryPath, sealed)
	if created {
		facts.IntentRecorded = true
	}
	if pe != nil {
		facts.Unconfirmed = true
		return facts, failed("intent", pe.Code)
	}
	if !created || !intakeFileEquals(root, datahome.IntakeRecoveryPath, sealed) {
		facts.Unconfirmed = true
		return facts, failed("intent", "INTAKE_INTENT_UNCONFIRMED")
	}
	if e := checks.step("intent"); e != nil {
		return facts, e
	}
	for _, directory := range []string{path.Dir(intent.RawPath), path.Dir(intent.ReportPath), path.Dir(intent.DiagnosticPath)} {
		if _, pe := root.EnsureDirectory(directory); pe != nil {
			return facts, failed("directory", pe.Code)
		}
	}
	if e := checks.step("directory"); e != nil {
		return facts, e
	}
	if e := intakeCreateAndVerifyWith(root, intent.DiagnosticPath, intent.ProvisionalDiagnostic, &facts.DiagnosticPublished, &facts.Unconfirmed, checks.mutations); e != nil {
		return facts, failed("diagnostic", e.Code)
	}
	if e := checks.step("diagnostic"); e != nil {
		return facts, e
	}
	if e := intakeCreateAndVerifyWith(root, intent.RawPath, source, &facts.RawPublished, &facts.Unconfirmed, checks.mutations); e != nil {
		return facts, failed("raw", e.Code)
	}
	if e := checks.step("raw"); e != nil {
		return facts, e
	}
	if e := checks.unchanged(ctx); e != nil {
		return facts, e
	}
	if e := intakeCreateAndVerifyWith(root, intent.ReportPath, intent.Report, &facts.ReportPublished, &facts.Unconfirmed, checks.mutations); e != nil {
		return facts, failed("report", e.Code)
	}
	if e := checks.step("report"); e != nil {
		return facts, e
	}
	if e := checks.unchanged(ctx); e != nil {
		return facts, e
	}
	committed, pe := checks.mutations.replaceFile(root, intent.DiagnosticPath, intent.ProvisionalDiagnostic, intent.TerminalDiagnostic)
	if pe != nil || !committed || !intakeFileEquals(root, intent.DiagnosticPath, intent.TerminalDiagnostic) {
		if pe != nil {
			facts.Unconfirmed = true
			return facts, failed("diagnostic", pe.Code)
		}
		facts.Unconfirmed = true
		return facts, failed("diagnostic", "INTAKE_DIAGNOSTIC_UNCONFIRMED")
	}
	if e := checks.step("terminal_diagnostic"); e != nil {
		return facts, e
	}
	removed, pe := checks.mutations.removeFile(root, datahome.IntakeRecoveryPath, sealed)
	if removed {
		facts.IntentRemoved = true
	}
	if pe != nil {
		facts.Unconfirmed = !removed
		return facts, failed("cleanup", pe.Code)
	}
	if !removed {
		facts.Unconfirmed = true
		return facts, failed("cleanup", "INTAKE_INTENT_UNCONFIRMED")
	}
	if e := checks.step("intent_removed"); e != nil {
		return facts, e
	}
	return facts, nil
}

func intakeCreateAndVerify(root *runtimepaths.Root, path string, expected []byte, published, unconfirmed *bool) *runtimepaths.Error {
	return intakeCreateAndVerifyWith(root, path, expected, published, unconfirmed, intakeFileMutations{})
}

func intakeCreateAndVerifyWith(root *runtimepaths.Root, path string, expected []byte, published, unconfirmed *bool, mutations intakeFileMutations) *runtimepaths.Error {
	created, pe := mutations.createFile(root, path, expected)
	if created {
		*published = true
	}
	if pe != nil {
		*unconfirmed = true
		return pe
	}
	if !created || !intakeFileEquals(root, path, expected) {
		*unconfirmed = true
		return &runtimepaths.Error{Code: "INTAKE_FILE_UNCONFIRMED"}
	}
	return nil
}

func intakeFileEquals(root *runtimepaths.Root, path string, expected []byte) bool {
	raw, present, e := root.ReadSingleLinkFile(path, int64(len(expected)))
	return e == nil && present && bytes.Equal(raw, expected)
}
