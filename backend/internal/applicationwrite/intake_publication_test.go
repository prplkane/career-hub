//go:build windows

package applicationwrite

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
)

const intakePublicationTestRoot = "CAREERHUB_TEST_INTAKE_PUBLICATION_ROOT"
const intakePublicationTestCase = "CAREERHUB_TEST_INTAKE_PUBLICATION_CASE"

func TestIntakePublicationProcess(t *testing.T) {
	if home := os.Getenv(intakePublicationTestRoot); home != "" {
		runIntakePublicationCase(t, home, os.Getenv(intakePublicationTestCase))
		return
	}
	for _, scenario := range []string{"clean", "bad-source", "collision", "raced-intent", "raced-raw", "raced-report", "cleanup-conflict", "witness-before-report", "witness-after-report", "fault-intent", "fault-raw", "fault-report", "fault-terminal_diagnostic"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			marker := []byte(`{"application":"CareerHub","format_version":2,"channel":"stable","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`)
			if err := os.WriteFile(filepath.Join(home, datahome.MarkerPath), marker, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIntakePublicationProcess$")
			cmd.Env = append(os.Environ(), intakePublicationTestRoot+"="+home, intakePublicationTestCase+"="+scenario)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("fictional publication process failed: %v: %s", err, output)
			}
		})
	}
}

func runIntakePublicationCase(t *testing.T, home, scenario string) {
	t.Helper()
	root, e := runtimepaths.Open(home)
	if e != nil {
		t.Fatal(e.Code)
	}
	if e := root.ClaimForProcess(); e != nil {
		t.Fatal(e.Code)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	p := fixtureIntakeIntent(t)
	source := []byte("Synthetic resume with no private data.")
	if len(source) != p.Resume.Size {
		t.Fatal("fixture source size changed")
	}
	if scenario == "collision" {
		if _, e := root.EnsureDirectory(filepath.ToSlash(filepath.Dir(p.RawPath))); e != nil {
			t.Fatal(e.Code)
		}
		if created, e := root.CreateFile(p.RawPath, []byte("foreign")); e != nil || !created {
			t.Fatal("collision fixture unavailable")
		}
	}
	if scenario == "bad-source" {
		source = []byte("altered")
	}
	checks := intakePublicationChecks{witnesses: func(context.Context) *Error { return nil }}
	if scenario == "raced-intent" {
		checks.checkpoint = func(stage string) *Error {
			if stage == "preflight" {
				if created, e := root.CreateFile(datahome.IntakeRecoveryPath, []byte("foreign intent")); e != nil || !created {
					t.Fatal("raced intent fixture unavailable")
				}
			}
			return nil
		}
	}
	if scenario == "raced-raw" {
		checks.checkpoint = func(stage string) *Error {
			if stage == "diagnostic" {
				if created, e := root.CreateFile(p.RawPath, []byte("foreign raw")); e != nil || !created {
					t.Fatal("raced raw fixture unavailable")
				}
			}
			return nil
		}
	}
	if scenario == "raced-report" {
		checks.checkpoint = func(stage string) *Error {
			if stage == "raw" {
				if created, e := root.CreateFile(p.ReportPath, []byte("foreign report")); e != nil || !created {
					t.Fatal("raced report fixture unavailable")
				}
			}
			return nil
		}
	}
	if scenario == "cleanup-conflict" {
		checks.checkpoint = func(stage string) *Error {
			if stage == "terminal_diagnostic" {
				sealed, e := encodeIntakeIntent(p)
				if e != nil {
					t.Fatal(e.Code)
				}
				if committed, e := root.ReplaceFile(datahome.IntakeRecoveryPath, sealed, []byte("foreign intent")); e != nil || !committed {
					t.Fatal("cleanup conflict fixture unavailable")
				}
			}
			return nil
		}
	}
	if scenario == "witness-before-report" || scenario == "witness-after-report" {
		calls := 0
		failAt := 2
		if scenario == "witness-after-report" {
			failAt = 3
		}
		checks.witnesses = func(context.Context) *Error {
			calls++
			if calls == failAt {
				return failed("witness", "INTAKE_WITNESS_CHANGED")
			}
			return nil
		}
	}
	if len(scenario) > 6 && scenario[:6] == "fault-" {
		stage := scenario[6:]
		checks.checkpoint = func(current string) *Error {
			if current == stage {
				return failed("fixture", "INTAKE_TEST_FAULT")
			}
			return nil
		}
	}
	facts, problem := publishFrozenIntake(context.Background(), root, p, source, checks)
	intentPresent := intakePresent(t, root, datahome.IntakeRecoveryPath)
	rawPresent := intakePresent(t, root, p.RawPath)
	reportPresent := intakePresent(t, root, p.ReportPath)
	diagnosticPresent := intakePresent(t, root, p.DiagnosticPath)
	switch scenario {
	case "clean":
		if problem != nil || !facts.IntentRecorded || !facts.DiagnosticPublished || !facts.RawPublished || !facts.ReportPublished || !facts.IntentRemoved || facts.Unconfirmed || facts.recoveryRequired() || intentPresent || !rawPresent || !reportPresent || !diagnosticPresent {
			t.Fatalf("clean publication has wrong facts: %+v, %v", facts, problem)
		}
		if !intakeFileEquals(root, p.RawPath, source) || !intakeFileEquals(root, p.ReportPath, p.Report) || !intakeFileEquals(root, p.DiagnosticPath, p.TerminalDiagnostic) {
			t.Fatal("published bytes changed")
		}
		if info, e := datahome.Inspect(home, nil); e != nil || info == nil || *info != p.Home {
			t.Fatal("clean publication did not restore ordinary admission")
		}
		if _, again := publishFrozenIntake(context.Background(), root, p, source, checks); again == nil || again.Code != "INTAKE_PATH_CONFLICT" {
			t.Fatal("repeat request reused a published run")
		}
	case "bad-source", "collision":
		if problem == nil || facts.IntentRecorded || intentPresent || reportPresent || diagnosticPresent || (scenario == "bad-source" && rawPresent) {
			t.Fatalf("preflight mutated home: %+v, %v", facts, problem)
		}
	case "raced-intent":
		if problem == nil || problem.Code != "FILE_EXISTS" || !facts.Unconfirmed || !facts.recoveryRequired() || facts.IntentRecorded || !intentPresent || rawPresent || reportPresent || diagnosticPresent {
			t.Fatalf("uncertain intent was treated as clean rollback: %+v, %v", facts, problem)
		}
		if !intakeFileEquals(root, datahome.IntakeRecoveryPath, []byte("foreign intent")) {
			t.Fatal("raced intent was adopted or overwritten")
		}
	case "raced-raw":
		if problem == nil || problem.Code != "FILE_EXISTS" || !facts.Unconfirmed || !facts.recoveryRequired() || !facts.IntentRecorded || !intentPresent || !rawPresent || reportPresent || !diagnosticPresent {
			t.Fatalf("foreign raw was treated as owned: %+v, %v", facts, problem)
		}
		if !intakeFileEquals(root, p.RawPath, []byte("foreign raw")) {
			t.Fatal("raced raw was overwritten")
		}
	case "raced-report":
		if problem == nil || problem.Code != "FILE_EXISTS" || !facts.Unconfirmed || !facts.recoveryRequired() || !facts.IntentRecorded || !intentPresent || !rawPresent || !reportPresent || !diagnosticPresent {
			t.Fatalf("foreign report was treated as owned: %+v, %v", facts, problem)
		}
		if !intakeFileEquals(root, p.ReportPath, []byte("foreign report")) {
			t.Fatal("raced report was overwritten")
		}
	case "cleanup-conflict":
		if problem == nil || problem.Code != "FILE_CONFLICT" || !facts.Unconfirmed || !facts.recoveryRequired() || !facts.IntentRecorded || facts.IntentRemoved || !intentPresent || !rawPresent || !reportPresent || !diagnosticPresent {
			t.Fatalf("cleanup conflict erased publication facts: %+v, %v", facts, problem)
		}
		if !intakeFileEquals(root, p.ReportPath, p.Report) || !intakeFileEquals(root, p.DiagnosticPath, p.TerminalDiagnostic) || !intakeFileEquals(root, datahome.IntakeRecoveryPath, []byte("foreign intent")) {
			t.Fatal("cleanup conflict changed published report or foreign intent")
		}
	case "witness-before-report":
		if problem == nil || problem.Code != "INTAKE_WITNESS_CHANGED" || !facts.recoveryRequired() || !intentPresent || !rawPresent || reportPresent || !diagnosticPresent {
			t.Fatalf("pre-report witness drift was ignored: %+v, %v", facts, problem)
		}
	case "witness-after-report":
		if problem == nil || problem.Code != "INTAKE_WITNESS_CHANGED" || !facts.recoveryRequired() || !intentPresent || !rawPresent || !reportPresent || !diagnosticPresent || !intakeFileEquals(root, p.ReportPath, p.Report) {
			t.Fatalf("post-report witness drift erased commit fact: %+v, %v", facts, problem)
		}
	case "fault-intent":
		if problem == nil || !facts.recoveryRequired() || !intentPresent || rawPresent || reportPresent || diagnosticPresent {
			t.Fatalf("intent checkpoint facts wrong: %+v, %v", facts, problem)
		}
	case "fault-raw":
		if problem == nil || !facts.recoveryRequired() || !intentPresent || !rawPresent || reportPresent || !diagnosticPresent || !intakeFileEquals(root, p.RawPath, source) {
			t.Fatalf("raw checkpoint facts wrong: %+v, %v", facts, problem)
		}
	case "fault-report", "fault-terminal_diagnostic":
		if problem == nil || !facts.recoveryRequired() || !intentPresent || !rawPresent || !reportPresent || !diagnosticPresent || !intakeFileEquals(root, p.ReportPath, p.Report) {
			t.Fatalf("report checkpoint facts wrong: %+v, %v", facts, problem)
		}
		wantDiagnostic := p.ProvisionalDiagnostic
		if scenario == "fault-terminal_diagnostic" {
			wantDiagnostic = p.TerminalDiagnostic
		}
		if !intakeFileEquals(root, p.DiagnosticPath, wantDiagnostic) {
			t.Fatal("diagnostic state did not match checkpoint")
		}
	}
	if intentPresent && scenario != "raced-intent" && scenario != "cleanup-conflict" {
		sealed, e := encodeIntakeIntent(p)
		if e != nil || !intakeFileEquals(root, datahome.IntakeRecoveryPath, sealed) {
			t.Fatal("pending intent was not retained exactly")
		}
	}
	if intentPresent {
		if info, e := datahome.Inspect(home, nil); info != nil || e == nil || e.Code != "DATA_HOME_RECOVERY_REQUIRED" {
			t.Fatal("ordinary admission crossed unfinished publication")
		}
	}
	if scenario == "collision" {
		raw, _, e := root.ReadSingleLinkFile(p.RawPath, 32)
		if e != nil || !bytes.Equal(raw, []byte("foreign")) {
			t.Fatal("collision overwrote foreign file")
		}
	}
}

func intakePresent(t *testing.T, root *runtimepaths.Root, name string) bool {
	t.Helper()
	present, e := root.Exists(name)
	if e != nil {
		t.Fatal(e.Code)
	}
	return present
}
