//go:build windows

package applicationwrite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/applicationanalysis"
	"careerhub.local/backend/internal/jobs/jobcard"
	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const detailsTestHome = "CAREERHUB_TEST_JOB_DETAILS_HOME"

func TestJobDetailsOwnedProcess(t *testing.T) {
	if home := os.Getenv(detailsTestHome); home != "" {
		runJobDetailsOwned(t, home)
		return
	}
	base := t.TempDir()
	home := filepath.Join(base, "home")
	for _, path := range []string{home, filepath.Join(home, "jobs"), filepath.Join(home, "jobs", "incoming"), filepath.Join(home, "jobs", "normalized")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	marker := []byte(`{"application":"CareerHub","format_version":2,"channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`)
	if err := os.WriteFile(filepath.Join(home, datahome.MarkerPath), marker, 0600); err != nil {
		t.Fatal(err)
	}
	source := []byte("Build local Go services.\n")
	if err := os.WriteFile(filepath.Join(home, "jobs", "incoming", "synthetic.txt"), source, 0600); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := applicationanalysis.SourceFingerprint(string(source))
	if err != nil {
		t.Fatal(err)
	}
	card := []byte(fmt.Sprintf(`id: synthetic
status: ready
company: Synthetic
title: Engineer
source_url: https://example.test/jobs/1
location: ''
remote_policy: ''
seniority: ''
required_skills: [Go]
required_platforms: []
required_capabilities: []
preferred_skills: []
experience_requirements: []
education_requirements: []
responsibilities: [Build services.]
visa_signals:
  citizenship_required: null
  clearance_required: null
  sponsorship_mentioned: null
  no_visa_or_opt: null
raw_jd_path: jobs/incoming/synthetic.txt
content_fingerprint: %s
`, fingerprint))
	if err := os.WriteFile(filepath.Join(home, "jobs", "normalized", "synthetic.yaml"), card, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestJobDetailsOwnedProcess$")
	cmd.Env = append(os.Environ(), detailsTestHome+"="+home)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.WaitDelay = time.Second
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("owned JD test failed: deadline=%v err=%v output=%.1200s", ctx.Err(), err, output)
	}
	final, err := os.ReadFile(filepath.Join(home, "jobs", "normalized", "synthetic.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, parseErr := cataloginput.ParseYAML(final)
	if parseErr != nil || !bytes.Contains(final, []byte(`"AfterCommit"`)) || parsed.Kind != "mapping" {
		t.Fatal("owned write did not persist a valid corrected card")
	}
}

func runJobDetailsOwned(t *testing.T, home string) {
	t.Helper()
	root, err := runtimepaths.Open(home)
	if err != nil {
		t.Fatal(err.Code)
	}
	defer root.Close()
	start := New(home)
	homeInfo, homeErr := datahome.Inspect(home, nil)
	if homeErr != nil {
		t.Fatal(homeErr.Code)
	}
	owner := start.PinnedToHome(*homeInfo)
	prior, priorErr := os.ReadFile(filepath.Join(home, "jobs", "normalized", "synthetic.yaml"))
	if priorErr != nil {
		t.Fatal(priorErr)
	}
	priorSHA := sha256.Sum256(prior)
	revision := "sha256:" + hex.EncodeToString(priorSHA[:])
	if out, e := owner.EditJobDetails(context.Background(), "synthetic", revision, map[string]any{"title": "X"}); e == nil || e.Code != "DATA_HOME_OWNERSHIP_REQUIRED" || out.Committed {
		t.Fatalf("unowned write accepted: %#v %#v", out, e)
	}
	if e := root.ClaimForProcess(); e != nil {
		t.Fatal(e.Code)
	}
	if out, e := owner.EditJobDetails(context.Background(), "synthetic", "sha256:"+strings.Repeat("0", 64), map[string]any{"title": "X"}); e == nil || e.Code != "JOB_DETAILS_REVISION_CONFLICT" || out.CommitAttempted || out.Committed {
		t.Fatalf("stale preflight accepted: %#v %#v", out, e)
	}
	updates := map[string]any{"title": "Senior Engineer", "required_skills": []any{"Go", "Python"}}
	out, problem := owner.EditJobDetails(context.Background(), "synthetic", revision, updates)
	if problem != nil || out == nil || !out.CommitAttempted || !out.Committed || len(out.Result) == 0 {
		t.Fatalf("owned edit failed: %#v %#v", out, problem)
	}
	var result struct {
		ID           string `json:"id"`
		CardRevision string `json:"card_revision"`
	}
	if json.Unmarshal(out.Result, &result) != nil || result.ID != "synthetic" || result.CardRevision == revision {
		t.Fatalf("missing new revision: %#v", result)
	}
	changed, readErr := os.ReadFile(filepath.Join(home, "jobs", "normalized", "synthetic.yaml"))
	if readErr != nil || bytes.Equal(prior, changed) {
		t.Fatal("card not replaced")
	}
	if out, e := owner.EditJobDetails(context.Background(), "synthetic", revision, updates); e == nil || e.Code != "JOB_DETAILS_REVISION_CONFLICT" || out.CommitAttempted || out.Committed {
		t.Fatalf("stale retry accepted: %#v %#v", out, e)
	}
	if out, e := owner.EditJobDetails(context.Background(), "synthetic", result.CardRevision, map[string]any{"status": "applied"}); e == nil || e.Code != "JOB_DETAILS_INVALID" || out.CommitAttempted || out.Committed {
		t.Fatalf("unsupported edit accepted: %#v %#v", out, e)
	}
	if e := os.WriteFile(filepath.Join(home, "jobs", "incoming", "synthetic.txt"), []byte("Changed raw JD.\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if out, e := owner.EditJobDetails(context.Background(), "synthetic", result.CardRevision, map[string]any{"title": "Unsafe"}); e == nil || e.Code != "JOB_DETAILS_SOURCE_CHANGED" || out.CommitAttempted || out.Committed {
		t.Fatalf("changed raw source accepted: %#v %#v", out, e)
	}
	final, readErr := os.ReadFile(filepath.Join(home, "jobs", "normalized", "synthetic.yaml"))
	if readErr != nil || !bytes.Equal(final, changed) {
		t.Fatal("rejected edit changed card")
	}
	if e := os.WriteFile(filepath.Join(home, "jobs", "incoming", "synthetic.txt"), []byte("Build local Go services.\n"), 0600); e != nil {
		t.Fatal(e)
	}
	faulty := New(home).PinnedToHome(*homeInfo)
	faulty.checkpoint = func(stage string) *Error {
		if stage == "job_details_committed" {
			return failed("synthetic", "JOB_DETAILS_POSTCOMMIT_TEST")
		}
		return nil
	}
	out, problem = faulty.EditJobDetails(context.Background(), "synthetic", result.CardRevision, map[string]any{"title": "AfterCommit"})
	if problem == nil || problem.Code != "JOB_DETAILS_POSTCOMMIT_TEST" || out == nil || !out.CommitAttempted || !out.Committed || len(out.Result) != 0 {
		t.Fatalf("post-commit fault lost publication facts: %#v %#v", out, problem)
	}
	afterCommit, readErr := os.ReadFile(filepath.Join(home, "jobs", "normalized", "synthetic.yaml"))
	if readErr != nil || bytes.Equal(afterCommit, changed) || !bytes.Contains(afterCommit, []byte(`"AfterCommit"`)) {
		t.Fatal("post-commit error falsely rolled back the card")
	}
	newSHA := sha256.Sum256(afterCommit)
	newRevision := "sha256:" + hex.EncodeToString(newSHA[:])
	otherMarker := []byte(`{"application":"CareerHub","format_version":2,"channel":"dev","data_home_id":"dh_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	if e := os.WriteFile(filepath.Join(home, datahome.MarkerPath), otherMarker, 0600); e != nil {
		t.Fatal(e)
	}
	if out, e := owner.EditJobDetails(context.Background(), "synthetic", newRevision, map[string]any{"title": "Wrong Home"}); e == nil || e.Code != "APPLICATION_LEDGER_HOME_CHANGED" || out.CommitAttempted || out.Committed {
		t.Fatalf("changed startup Home accepted: %#v %#v", out, e)
	}
	still, readErr := os.ReadFile(filepath.Join(home, "jobs", "normalized", "synthetic.yaml"))
	if readErr != nil || !bytes.Equal(still, afterCommit) {
		t.Fatal("refused changed Home was written")
	}
	if loaded, e := jobcard.Read(context.Background(), root, "jobs/normalized/synthetic.yaml"); e != nil || !loaded.Report.SchemaValid() {
		t.Fatalf("final card invalid: %#v", e)
	}
}
