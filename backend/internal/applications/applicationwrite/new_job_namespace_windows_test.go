//go:build windows

package applicationwrite

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/operationgate"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

func TestNewJobNamespacesNative(t *testing.T) {
	if home := os.Getenv("CAREERHUB_NAMESPACE_TEST_HOME"); home != "" {
		runNewJobNamespaces(t, home, os.Getenv("CAREERHUB_NAMESPACE_TEST_CASE"))
		return
	}
	for _, scenario := range []string{"fresh", "file_incoming", "file_normalized", "junction", "interrupted", "home_drift", "witness_drift", "crash"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			intent, _, _ := frozenNewJobFixture(t)
			marker, _ := datahome.MarkerBytes(intent.Home.Channel, intent.Home.DataHomeID)
			fixtureFile(t, filepath.Join(home, datahome.MarkerPath), marker)
			if strings.HasPrefix(scenario, "file_") {
				fixtureFile(t, filepath.Join(home, "jobs", strings.TrimPrefix(scenario, "file_")), []byte("synthetic obstruction"))
			}
			var outside string
			if scenario == "junction" {
				outside = t.TempDir()
				if err := os.Mkdir(filepath.Join(home, "jobs"), 0700); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command("cmd.exe", "/c", "mklink", "/J", filepath.Join(home, "jobs", "incoming"), outside)
				cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("synthetic junction: %v %.300s", err, output)
				}
			}
			invoke := func(mode string) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNewJobNamespacesNative$")
				cmd.Env = append(os.Environ(), "CAREERHUB_NAMESPACE_TEST_HOME="+home, "CAREERHUB_NAMESPACE_TEST_CASE="+mode)
				cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
				output, err := cmd.CombinedOutput()
				if mode == "crash" {
					var exit *exec.ExitError
					if !errors.As(err, &exit) || exit.ExitCode() != 77 {
						t.Fatalf("expected crash: %v %.700s", err, output)
					}
				} else if err != nil {
					t.Fatalf("namespace %s: %v %.1000s", mode, err, output)
				}
			}
			invoke(scenario)
			if scenario == "crash" {
				entries, err := os.ReadDir(filepath.Join(home, "jobs", "incoming"))
				if err != nil || len(entries) != 0 {
					t.Fatal("crash did not leave only an empty namespace", err)
				}
				if _, err := os.Stat(filepath.Join(home, datahome.NewJobRecoveryPath)); !os.IsNotExist(err) {
					t.Fatal("namespace crash wrote an intent", err)
				}
				invoke("fresh") // Fresh process must tolerate the earlier empty directory.
			}
			if outside != "" {
				entries, err := os.ReadDir(outside)
				if err != nil || len(entries) != 0 {
					t.Fatal("junction target changed", err)
				}
			}
		})
	}
}

func runNewJobNamespaces(t *testing.T, home, scenario string) {
	intent, raw, card := frozenNewJobFixture(t)
	root, problem := runtimepaths.Open(home)
	if problem != nil {
		t.Fatal(problem.Code)
	}
	defer root.Close()
	if problem = root.ClaimForProcess(); problem != nil {
		t.Fatal(problem.Code)
	}
	ctx := context.Background()
	release, err := operationgate.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	witnessDrift := false
	checks := newJobPublicationChecks{
		witnesses: func(context.Context) *Error {
			if witnessDrift {
				return failed("witness", "JOB_INTAKE_INPUT_CHANGED")
			}
			return nil
		},
		checkpoint: func(stage string) *Error {
			if stage != "namespace:jobs/incoming" {
				return nil
			}
			switch scenario {
			case "crash":
				os.Exit(77)
			case "interrupted":
				return failed("fixture", "SYNTHETIC_INTERRUPTION")
			case "witness_drift":
				witnessDrift = true
			case "home_drift":
				other := intent.Home
				other.DataHomeID = "dh_" + strings.Repeat("b", 32)
				marker, _ := datahome.MarkerBytes(other.Channel, other.DataHomeID)
				fixtureFile(t, filepath.Join(home, datahome.MarkerPath), marker)
			}
			return nil
		},
	}
	facts, failure := publishFrozenNewJob(ctx, root, intent, raw, card, checks)
	if scenario == "fresh" {
		if failure != nil || !facts.NamespaceMayHaveChanged || !facts.CardObserved || !facts.RawObserved || !facts.IntentObserved {
			t.Fatalf("first use failed: %+v %+v", facts, failure)
		}
		return
	}
	want := map[string]string{"file_incoming": "JOB_INTAKE_STORAGE_UNAVAILABLE", "file_normalized": "JOB_INTAKE_STORAGE_UNAVAILABLE", "junction": "JOB_INTAKE_STORAGE_UNAVAILABLE", "interrupted": "SYNTHETIC_INTERRUPTION", "home_drift": "JOB_INTAKE_HOME_CHANGED", "witness_drift": "JOB_INTAKE_INPUT_CHANGED"}[scenario]
	changed := scenario != "file_incoming" && scenario != "junction"
	if failure == nil || failure.Code != want || facts.NamespaceMayHaveChanged != changed || facts.IntentAttempted || facts.RawAttempted || facts.CardAttempted || facts.Unconfirmed || facts.recoveryRequired() {
		t.Fatalf("namespace failure facts: %+v %+v", facts, failure)
	}
	if scenario == "interrupted" || scenario == "home_drift" || scenario == "witness_drift" {
		marker, _ := datahome.MarkerBytes(intent.Home.Channel, intent.Home.DataHomeID)
		fixtureFile(t, filepath.Join(home, datahome.MarkerPath), marker)
		checks.checkpoint = nil
		witnessDrift = false
		retry, failure := publishFrozenNewJob(ctx, root, intent, raw, card, checks)
		if failure != nil || !retry.CardObserved {
			t.Fatalf("namespace retry failed: %+v %+v", retry, failure)
		}
	}
}
