//go:build windows

package applicationwrite

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
)

const intakeRecoveryTestRoot = "CAREERHUB_TEST_INTAKE_RECOVERY_ROOT"
const intakeRecoveryTestCase = "CAREERHUB_TEST_INTAKE_RECOVERY_CASE"
const intakeRecoveryTestPhase = "CAREERHUB_TEST_INTAKE_RECOVERY_PHASE"

func TestIntakeRecoveryRestartProcess(t *testing.T) {
	if home := os.Getenv(intakeRecoveryTestRoot); home != "" {
		runIntakeRecoveryPhase(t, home, os.Getenv(intakeRecoveryTestCase), os.Getenv(intakeRecoveryTestPhase))
		return
	}
	for _, scenario := range []string{"intent", "raw", "report", "terminal", "pre-drift", "raw-drift", "published-drift", "bad-raw", "bad-report", "report-no-raw", "foreign-diagnostic", "aborted-pending"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			marker := []byte(`{"application":"CareerHub","format_version":2,"channel":"stable","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`)
			if err := os.WriteFile(filepath.Join(home, datahome.MarkerPath), marker, 0600); err != nil {
				t.Fatal(err)
			}
			phases := []string{"stage", "recover"}
			if scenario == "aborted-pending" {
				phases = []string{"stage", "recover-fault", "recover"}
			}
			for _, phase := range phases {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIntakeRecoveryRestartProcess$")
				cmd.Env = append(os.Environ(), intakeRecoveryTestRoot+"="+home, intakeRecoveryTestCase+"="+scenario, intakeRecoveryTestPhase+"="+phase)
				output, err := cmd.CombinedOutput()
				cancel()
				if err != nil {
					t.Fatalf("%s process failed: %v: %s", phase, err, output)
				}
			}
		})
	}
}

func runIntakeRecoveryPhase(t *testing.T, home, scenario, phase string) {
	t.Helper()
	root, e := runtimepaths.Open(home)
	if e != nil {
		t.Fatal(e.Code)
	}
	if e := root.ClaimForProcess(); e != nil {
		t.Fatal(e.Code)
	}
	defer func() {
		if e := root.Close(); e != nil {
			t.Fatal(e)
		}
	}()
	p := fixtureIntakeIntent(t)
	source := []byte("Synthetic resume with no private data.")
	sealed, problem := encodeIntakeIntent(p)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	if phase == "stage" {
		stage := "intent"
		switch scenario {
		case "raw", "raw-drift", "bad-raw":
			stage = "raw"
		case "report", "published-drift", "bad-report", "report-no-raw":
			stage = "report"
		case "terminal":
			stage = "terminal_diagnostic"
		}
		_, problem := publishFrozenIntake(context.Background(), root, p, source, intakePublicationChecks{
			witnesses: func(context.Context) *Error { return nil },
			checkpoint: func(current string) *Error {
				if current == stage {
					return failed("fixture", "INTAKE_TEST_FAULT")
				}
				return nil
			},
		})
		if problem == nil || problem.Code != "INTAKE_TEST_FAULT" || !intakePresent(t, root, datahome.IntakeRecoveryPath) {
			t.Fatal("stage did not retain one exact intent")
		}
		switch scenario {
		case "bad-raw":
			if committed, e := root.ReplaceFile(p.RawPath, source, []byte("foreign raw")); e != nil || !committed {
				t.Fatal("bad raw fixture unavailable")
			}
		case "bad-report":
			if committed, e := root.ReplaceFile(p.ReportPath, p.Report, []byte("foreign report")); e != nil || !committed {
				t.Fatal("bad report fixture unavailable")
			}
		case "report-no-raw":
			if removed, e := root.RemoveFile(p.RawPath, source); e != nil || !removed {
				t.Fatal("missing raw fixture unavailable")
			}
		case "foreign-diagnostic":
			if _, e := root.EnsureDirectory(filepath.ToSlash(filepath.Dir(p.DiagnosticPath))); e != nil {
				t.Fatal(e.Code)
			}
			if created, e := root.CreateFile(p.DiagnosticPath, []byte("foreign diagnostic")); e != nil || !created {
				t.Fatal("foreign diagnostic fixture unavailable")
			}
		}
		return
	}
	if !intakeFileEquals(root, datahome.IntakeRecoveryPath, sealed) {
		t.Fatal("recovery did not start from the sealed intent")
	}
	match := !strings.Contains(scenario, "drift") && scenario != "aborted-pending"
	checkpoint := func(string) *Error { return nil }
	if phase == "recover-fault" {
		checkpoint = func(stage string) *Error {
			if stage == "aborted_diagnostic" {
				return failed("fixture", "INTAKE_TEST_FAULT")
			}
			return nil
		}
	}
	result, problem := recoverFrozenIntake(context.Background(), root, p, sealed,
		func(context.Context) ([]byte, bool, *Error) {
			if match {
				return source, true, nil
			}
			return nil, false, nil
		}, checkpoint)
	if phase == "recover-fault" {
		if problem == nil || problem.Code != "INTAKE_TEST_FAULT" || !intakePresent(t, root, datahome.IntakeRecoveryPath) || !intakeFileEquals(root, p.DiagnosticPath, intakeAbortedDiagnostic(p)) {
			t.Fatalf("interrupted abort lost its journal: %+v, %v", result, problem)
		}
		return
	}
	switch scenario {
	case "bad-raw", "bad-report", "report-no-raw", "foreign-diagnostic":
		if problem == nil || problem.Code != "INTAKE_ARTIFACT_CONFLICT" || !intakePresent(t, root, datahome.IntakeRecoveryPath) {
			t.Fatalf("foreign artifact was adopted: %+v, %v", result, problem)
		}
	case "raw-drift":
		if problem == nil || problem.Code != "INTAKE_WITNESS_CHANGED" || !intakePresent(t, root, datahome.IntakeRecoveryPath) || intakePresent(t, root, p.ReportPath) {
			t.Fatalf("raw-only drift was committed: %+v, %v", result, problem)
		}
	case "pre-drift", "aborted-pending":
		if problem != nil || result.State != "aborted_before_raw" || !result.Facts.IntentRemoved || intakePresent(t, root, datahome.IntakeRecoveryPath) || intakePresent(t, root, p.RawPath) || intakePresent(t, root, p.ReportPath) || !intakeFileEquals(root, p.DiagnosticPath, intakeAbortedDiagnostic(p)) {
			t.Fatalf("pre-raw abort was not settled: %+v, %v", result, problem)
		}
	case "published-drift":
		if problem != nil || result.State != "historical_stale" || !result.Facts.IntentRemoved || intakePresent(t, root, datahome.IntakeRecoveryPath) || !intakeFileEquals(root, p.ReportPath, p.Report) {
			t.Fatalf("published drift lost historical report: %+v, %v", result, problem)
		}
	default:
		if problem != nil || result.State != "recovered_published" || !result.Facts.IntentRemoved || intakePresent(t, root, datahome.IntakeRecoveryPath) || !intakeFileEquals(root, p.RawPath, source) || !intakeFileEquals(root, p.ReportPath, p.Report) || !intakeFileEquals(root, p.DiagnosticPath, p.TerminalDiagnostic) {
			t.Fatalf("interrupted publication did not recover: %+v, %v", result, problem)
		}
	}
	if !intakePresent(t, root, datahome.IntakeRecoveryPath) {
		if info, e := datahome.Inspect(home, nil); e != nil || info == nil || *info != p.Home {
			t.Fatal("completed recovery did not restore ordinary admission")
		}
	}
}
