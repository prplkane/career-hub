//go:build windows

package promotiondiagnostics

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/storage/datahome"
)

func TestPromotionDiagnosticOwnedStore(t *testing.T) {
	const variable = "CAREERHUB_PROMOTION_DIAGNOSTIC_ROOT"
	if home := os.Getenv(variable); home != "" {
		testPromotionOwnedStore(t, home)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPromotionDiagnosticOwnedStore$")
	cmd.Env = append(os.Environ(), variable+"="+filepath.Join(t.TempDir(), "home"))
	cmd.WaitDelay = time.Second
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("owned promotion snapshot: %v %.1500s", err, output)
	}
}

func testPromotionOwnedStore(t *testing.T, home string) {
	ctx := context.Background()
	info, problem := datahome.BootstrapForProcess(ctx, home, "dev")
	if problem != nil {
		t.Fatal(problem)
	}
	start, err := New("save_job")
	if err != nil {
		t.Fatal(err)
	}
	wrong := *info
	wrong.DataHomeID = "dh_11111111111111111111111111111111"
	if observed, err := Write(ctx, home, wrong, nil, start); observed || err == nil {
		t.Fatal("wrong Home admitted")
	}
	if observed, err := Write(ctx, home, *info, nil, start); !observed || err != nil {
		t.Fatal("begin unavailable", err)
	}
	name := filepath.Join(home, filepath.FromSlash(directory), start.OperationID+".json")
	original, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	intake := start
	intake.Stage, intake.Sequence = "intake", 2
	if observed, err := Write(ctx, home, *info, &start, intake); !observed || err != nil {
		t.Fatal("dispatch unavailable", err)
	}
	if _, err := Write(ctx, home, *info, &start, intake); err == nil {
		t.Fatal("stale compare-and-replace silently succeeded")
	}
	alias := filepath.Join(home, "diagnostic-alias.json")
	if err := os.Link(name, alias); err != nil {
		t.Fatal(err)
	}
	link := intake
	link.Stage, link.Sequence, link.Committed = "link", 3, true
	link.ChildRunID = "20260922T123000.123456Z_1234abcd"
	if _, err := Write(ctx, home, *info, &intake, link); err == nil {
		t.Fatal("hardlinked snapshot replaced")
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if observed, err := Write(ctx, home, *info, &intake, link); !observed || err != nil {
		t.Fatal("link checkpoint unavailable", err)
	}
	failed := link
	failed.Sequence, failed.State, failed.ErrorCode = 4, "failed", "JOB_INTAKE_RECOVERY_REQUIRED"
	failed.CommitUnknown, failed.Partial, failed.RecoveryRequired = true, true, true
	intentPath := filepath.Join(home, datahome.NewJobRecoveryPath)
	intent := []byte("fictional intent: diagnostic must not interpret or modify")
	if err := os.WriteFile(intentPath, intent, 0600); err != nil {
		t.Fatal(err)
	}
	newRun, _ := New("save_job")
	if _, err := Write(ctx, home, *info, nil, newRun); err == nil {
		t.Fatal("pending recovery admitted another promotion start")
	}
	otherIntent := filepath.Join(home, datahome.IntakeRecoveryPath)
	if err := os.WriteFile(otherIntent, []byte("foreign pending operation"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(ctx, home, *info, &link, failed); err == nil {
		t.Fatal("diagnostic bypassed another recovery sentinel")
	}
	if err := os.Remove(otherIntent); err != nil {
		t.Fatal(err)
	}
	if observed, err := Write(ctx, home, *info, &link, failed); !observed || err != nil {
		t.Fatal("could not record failure with own pending intake", err)
	}
	retained, err := os.ReadFile(name)
	decoded, decodeErr := Decode(retained)
	if err != nil || decodeErr != nil || decoded != failed || bytes.Equal(retained, original) {
		t.Fatal("durable facts lost", err, decodeErr)
	}
	unchanged, err := os.ReadFile(intentPath)
	if err != nil || !bytes.Equal(unchanged, intent) {
		t.Fatal("logging changed recovery authority")
	}
	if _, err := datahome.Inspect(home, nil); err == nil {
		t.Fatal("logging enabled ordinary product writes during recovery")
	}
}

func TestPromotionDiagnosticInterruptedRestart(t *testing.T) {
	const variable = "CAREERHUB_PROMOTION_DIAGNOSTIC_RESTART_ROOT"
	if home := os.Getenv(variable); home != "" {
		ctx := context.Background()
		info, err := datahome.BootstrapForProcess(ctx, home, "dev")
		if err != nil {
			t.Fatal(err)
		}
		if os.Getenv("CAREERHUB_PROMOTION_DIAGNOSTIC_RESTART_READ") == "1" {
			entries, err := os.ReadDir(filepath.Join(home, filepath.FromSlash(directory)))
			if err != nil || len(entries) != 1 {
				t.Fatal("interrupted run missing", err)
			}
			raw, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(directory), entries[0].Name()))
			r, de := Decode(raw)
			if err != nil || de != nil || r.State != "running" || r.Stage != "link" || !r.Committed || r.ChildRunID == "" {
				t.Fatal("interrupted parent became success or lost prior commit", err, de)
			}
			return
		}
		start, _ := New("record_as_applied")
		intake := start
		intake.Sequence, intake.Stage = 2, "intake"
		link := intake
		link.Sequence, link.Stage, link.Committed = 3, "link", true
		link.ChildRunID = "20260922T123000.123456Z_1234abcd"
		for _, pair := range []struct {
			before *Record
			after  Record
		}{{nil, start}, {&start, intake}, {&intake, link}} {
			if observed, e := Write(ctx, home, *info, pair.before, pair.after); !observed || e != nil {
				t.Fatal(e)
			}
		}
		os.Exit(77)
	}
	home := filepath.Join(t.TempDir(), "home")
	for _, reading := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPromotionDiagnosticInterruptedRestart$")
		cmd.Env = append(os.Environ(), variable+"="+home)
		if reading {
			cmd.Env = append(cmd.Env, "CAREERHUB_PROMOTION_DIAGNOSTIC_RESTART_READ=1")
		}
		cmd.WaitDelay = time.Second
		output, err := cmd.CombinedOutput()
		cancel()
		if reading && err != nil || !reading && (cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 77) {
			t.Fatalf("restart=%v: %v %.1200s", reading, err, output)
		}
	}
}
