//go:build windows

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/applicationwrite"
	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/operationgate"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/sidecar/readonly"
)

const newJobFilePhaseChild = "CAREERHUB_TEST_NEW_JOB_FILE_PHASE_CHILD"
const newJobFilePhaseRoot = "CAREERHUB_TEST_NEW_JOB_FILE_PHASE_ROOT"

func TestNewJobFilePhaseOwnedComposition(t *testing.T) {
	if os.Getenv(newJobFilePhaseChild) == "1" {
		runNewJobFilePhaseOwned(t, os.Getenv(newJobFilePhaseRoot))
		return
	}
	base := newJobHostFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNewJobFilePhaseOwnedComposition$")
	child.Env = append(os.Environ(), newJobFilePhaseChild+"=1", newJobFilePhaseRoot+"="+base)
	child.WaitDelay = time.Second
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("fictional new-JD file phase: %v %.1600s", err, output)
	}
}

func newJobHostFixture(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	data, resources := filepath.Join(base, "data"), filepath.Join(base, "resources")
	materialFixtureFile(t, filepath.Join(data, datahome.MarkerPath), []byte(`{"application":"CareerHub","format_version":2,"channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`))
	materialFixtureFile(t, filepath.Join(resources, "VERSION"), []byte("1.2.3-dev.01+build.7\n"))
	materialFixtureFile(t, filepath.Join(resources, filepath.FromSlash(catalogstore.CoreGroupsPath)), []byte("[]\n"))
	materialFixtureFile(t, filepath.Join(resources, filepath.FromSlash(catalogstore.CoreSkillsPath)), []byte("- {id: python, name: Python, category: programming_language}\n"))
	materialFixtureFile(t, filepath.Join(data, filepath.FromSlash(catalogstore.CustomGroupsPath)), []byte("[]\n"))
	materialFixtureFile(t, filepath.Join(data, filepath.FromSlash(catalogstore.CustomSkillsPath)), []byte("[]\n"))
	materialFixtureFile(t, filepath.Join(data, "career_vault", "visa_profile.yaml"), []byte("current_status: citizen\nrequires_sponsorship_now: false\n"))
	for _, path := range []string{filepath.Join(data, "jobs", "incoming"), filepath.Join(data, "jobs", "normalized"), filepath.Join(resources, "src", "intake_ui_static")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

func runNewJobFilePhaseOwned(t *testing.T, base string) {
	t.Helper()
	data, resources := filepath.Join(base, "data"), filepath.Join(base, "resources")
	start := protocol.StartRecord{DataRoot: data, ResourceRoot: resources, Channel: "dev"}
	workspace, err := readonly.OpenForProcess(start)
	if err != nil {
		t.Fatal("synthetic process admission", err)
	}
	home, err := workspace.StartupHome()
	if err != nil {
		t.Fatal(err)
	}
	called := false
	invalid, invalidErr := applicationwrite.New(data).PinnedToHome(home).BeginNewJobFilePhase(context.Background(),
		func(context.Context) (*applicationwrite.NewJobFileChoice, error) {
			called = true
			probe, cancel := context.WithTimeout(context.Background(), time.Millisecond)
			defer cancel()
			if release, err := operationgate.Acquire(probe); err == nil {
				release()
				t.Fatal("fresh inspection ran outside the operation gate")
			}
			return &applicationwrite.NewJobFileChoice{}, nil
		})
	if !called || invalidErr == nil || invalidErr.Code != "JOB_INTAKE_CHOICE_INVALID" || invalid == nil || invalid.IntentAttempted {
		t.Fatal("invalid inspection changed file state", invalid, invalidErr)
	}
	if _, err := os.Stat(filepath.Join(data, datahome.NewJobRecoveryPath)); !os.IsNotExist(err) {
		t.Fatal("invalid inspection created an intent")
	}
	request := readonly.NewJobRequest{SourceName: "fictional_job.txt", RawText: reanalysisCoordinatorSource,
		SourceURL: "https://example.test/jobs/new", IdempotencyKey: "fictional-key"}
	mismatchedStart := start
	mismatchedStart.DataRoot = filepath.Join(base, "other-data")
	mismatched, mismatchErr := beginNewJobFiles(context.Background(), workspace, mismatchedStart, request, "Fictional Labs", "Backend Engineer")
	if mismatchErr == nil || mismatchErr.Code != "JOB_INTAKE_REQUEST_INVALID" || mismatched == nil || mismatched.IntentAttempted {
		t.Fatal("cross-root inspector/writer pairing was accepted", mismatched, mismatchErr)
	}
	invalidStatus := request
	invalidStatus.InitialStatus = "interview"
	refused, statusErr := beginNewJobFiles(context.Background(), workspace, start, invalidStatus, "Fictional Labs", "Backend Engineer")
	if statusErr == nil || statusErr.Code != "JOB_INTAKE_INITIAL_STATUS_INVALID" || refused == nil || refused.IntentAttempted {
		t.Fatal("invalid initial status reached file publication", refused, statusErr)
	}
	if _, err := os.Stat(filepath.Join(data, datahome.NewJobRecoveryPath)); !os.IsNotExist(err) {
		t.Fatal("invalid initial status created an intent")
	}
	out, problem := beginNewJobFiles(context.Background(), workspace, start, request, "Fictional Labs", "Backend Engineer")
	if problem != nil || out == nil || !out.Complete || !out.RecoveryRequired || out.Unconfirmed || out.CleanupFailed ||
		out.JobID != "fictional_job" || out.Duplicate != nil || !out.IntentReported || !out.RawReported || !out.CardReported ||
		!out.IntentObserved || !out.RawObserved || !out.CardObserved {
		t.Fatalf("owned file phase not complete: %#v %#v", out, problem)
	}
	raw, err := os.ReadFile(filepath.Join(data, "jobs", "incoming", "fictional_job.txt"))
	if err != nil || !bytes.Equal(raw, []byte(request.RawText)) {
		t.Fatal("raw JD not published exactly", err)
	}
	if _, err := os.Stat(filepath.Join(data, "jobs", "normalized", "fictional_job.yaml")); err != nil {
		t.Fatal("draft card missing", err)
	}
	if _, err := os.Stat(filepath.Join(data, "applications", "career_hub.sqlite3")); !os.IsNotExist(err) {
		t.Fatal("file-only phase unexpectedly created a ledger")
	}
	observed, recoveryErr := applicationwrite.New(data).PinnedToHome(home).ObserveNewJobRecovery(context.Background())
	if recoveryErr != nil || observed == nil || observed.FileState != "complete" || observed.JobID != out.JobID {
		t.Fatalf("pending intent not observable: %#v %#v", observed, recoveryErr)
	}
}
