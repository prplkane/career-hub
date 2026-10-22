//go:build windows

package applicationwrite

import (
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/assessmentcontext"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
)

func TestNewJobRecoveryOwnedObservation(t *testing.T) {
	const fixtureEnv = "CAREERHUB_TEST_NEW_JOB_OBSERVATION_HOME"
	if home := os.Getenv(fixtureEnv); home != "" {
		runNewJobRecoveryObservation(t, home)
		return
	}
	home := t.TempDir()
	for _, name := range []string{"jobs/incoming", "jobs/normalized"} {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(name)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	info := testNewJobIntent([]byte("private fictional JD"), []byte("id: software_engineer\n")).Home
	marker, markerErr := datahome.MarkerBytes(info.Channel, info.DataHomeID)
	if markerErr != nil {
		t.Fatal(markerErr.Code)
	}
	if err := os.WriteFile(filepath.Join(home, datahome.MarkerPath), marker, 0600); err != nil {
		t.Fatal(err)
	}
	intent, err := encodeNewJobIntent(testNewJobIntent([]byte("private fictional JD"), []byte("id: software_engineer\n")))
	if err != nil {
		t.Fatal(err.Code)
	}
	if err := os.WriteFile(filepath.Join(home, datahome.NewJobRecoveryPath), intent, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNewJobRecoveryOwnedObservation$")
	cmd.Env = append(os.Environ(), fixtureEnv+"="+home)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("owned new-job observation failed: %v: %.1200s", err, output)
	}
}

func runNewJobRecoveryObservation(t *testing.T, home string) {
	t.Helper()
	info := datahome.Info{FormatVersion: 2, Channel: "dev", Initialized: true,
		DataHomeID: "dh_" + strings.Repeat("a", 32)}
	owner := New(home).PinnedToHome(info)
	if observed, err := owner.ObserveNewJobRecovery(context.Background()); observed != nil || err == nil || err.Code != "DATA_HOME_OWNERSHIP_REQUIRED" {
		t.Fatal("unowned recovery read succeeded")
	}
	root, err := runtimepaths.Open(home)
	if err != nil {
		t.Fatal(err.Code)
	}
	if err := root.ClaimForProcess(); err != nil {
		t.Fatal(err.Code)
	}
	defer root.Close()
	otherHome := info
	otherHome.DataHomeID = "dh_" + strings.Repeat("d", 32)
	if observed, err := New(home).PinnedToHome(otherHome).ObserveNewJobRecovery(context.Background()); observed != nil || err == nil || err.Code != "APPLICATION_LEDGER_HOME_CHANGED" {
		t.Fatal("recovery read accepted another Home identity")
	}
	expectedSourceKey := sha256.Sum256([]byte(testNewJobIntent([]byte("private fictional JD"), []byte("id: software_engineer\n")).ContentFingerprint))
	check := func(want, ledger string) {
		t.Helper()
		observed, err := owner.ObserveNewJobRecovery(context.Background())
		if err != nil || observed == nil || observed.JobID != "software_engineer" || observed.FileState != want ||
			observed.InitialStatus != "captured" || observed.LedgerState != ledger || observed.SourceKeySHA256 != expectedSourceKey {
			t.Fatalf("recovery observation: %#v %#v; want %s", observed, err, want)
		}
	}
	check(string(newJobFilesNone), "not_checked")
	refused, recoveryErr := owner.RecoverIncompleteNewJobStep(context.Background(), "", "", assessmentcontext.Month{})
	if recoveryErr == nil || recoveryErr.Code != "JOB_INTAKE_RECOVERY_PLAN_INVALID" ||
		refused == nil || refused.Observation != nil {
		t.Fatal("invalid plan ID reached recovery observation", refused, recoveryErr)
	}
	plan, planErr := owner.PlanIncompleteNewJobRecovery(context.Background())
	if planErr != nil || plan == nil || plan.Disposition != "manual_review" {
		t.Fatal("absent-file recovery plan unavailable", plan, planErr)
	}
	refused, recoveryErr = owner.RecoverIncompleteNewJobStep(context.Background(), plan.PlanID, "", assessmentcontext.Month{})
	if recoveryErr == nil || recoveryErr.Code != "JOB_INTAKE_RECOVERY_FILES_INCOMPLETE" ||
		refused == nil || refused.Step != "" || refused.Plan == nil || refused.Plan.PlanID != plan.PlanID {
		t.Fatal("absent files authorized recovery", refused, recoveryErr)
	}
	p := testNewJobIntent([]byte("private fictional JD"), []byte("id: software_engineer\n"))
	if created, err := root.CreateFile(p.RawPath, []byte("private fictional JD")); err != nil || !created {
		t.Fatal("raw fixture unavailable")
	}
	check(string(newJobFilesRawOnly), "not_checked")
	plan, planErr = owner.PlanIncompleteNewJobRecovery(context.Background())
	if planErr != nil || plan == nil || plan.Disposition != "manual_review" {
		t.Fatal("raw-only recovery plan unavailable", plan, planErr)
	}
	refused, recoveryErr = owner.RecoverIncompleteNewJobStep(context.Background(), plan.PlanID, "", assessmentcontext.Month{})
	if recoveryErr == nil || recoveryErr.Code != "JOB_INTAKE_RECOVERY_FILES_INCOMPLETE" ||
		refused == nil || refused.Step != "" || refused.Plan == nil || refused.Plan.PlanID != plan.PlanID {
		t.Fatal("raw-only files authorized recovery", refused, recoveryErr)
	}
	if created, err := root.CreateFile(p.CardPath, []byte("id: software_engineer\n")); err != nil || !created {
		t.Fatal("card fixture unavailable")
	}
	check(string(newJobFilesComplete), "conflict")
	if replaced, err := root.ReplaceFile(p.CardPath, []byte("id: software_engineer\n"), []byte("wrong card bytes")); err != nil || !replaced {
		t.Fatal("wrong-byte fixture unavailable")
	}
	check(string(newJobFilesConflict), "not_checked")
	sealed, encodeErr := encodeNewJobIntent(p)
	if encodeErr != nil {
		t.Fatal(encodeErr.Code)
	}
	if removed, err := root.RemoveFile(datahome.NewJobRecoveryPath, sealed); err != nil || !removed {
		t.Fatal("intent removal fixture unavailable")
	}
	if observed, err := owner.ObserveNewJobRecovery(context.Background()); observed != nil || err == nil || err.Code != "JOB_INTAKE_RECOVERY_INTENT_MISSING" {
		t.Fatal("missing journal was inferred as a completed operation")
	}
}
