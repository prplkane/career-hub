//go:build windows

package readonly

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/sidecar/protocol"
)

const nativeCardPath = "jobs/normalized/synthetic.yaml"
const nativeRawPath = "jobs/incoming/synthetic.txt"
const nativeJobCard = `id: synthetic
status: draft
company: ''
title: ''
source_url: ''
location: ''
remote_policy: ''
seniority: ''
required_skills: []
required_platforms: []
required_capabilities: []
preferred_skills: []
experience_requirements: []
education_requirements: []
responsibilities: []
visa_signals:
  citizenship_required: null
  clearance_required: null
  sponsorship_mentioned: null
  no_visa_or_opt: null
raw_jd_path: jobs/incoming/synthetic.txt
`

type nativeCardRequest struct {
	Start protocol.StartRecord
	Mode  string
}

func TestNativeJobCardOwnershipPathsAndFreshness(t *testing.T) {
	for _, mode := range []string{"valid", "unowned", "identity", "recovery", "resources", "canceled", "directory", "refresh", "invalid-raw", "device", "junction"} {
		t.Run(mode, func(t *testing.T) {
			_, start := fixture(t)
			raw, err := json.Marshal(nativeCardRequest{start, mode})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestJobCardProcessHelper$")
			cmd.Env = append(os.Environ(), "CAREERHUB_JOB_CARD_HELPER=1")
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			cmd.WaitDelay = 500 * time.Millisecond
			cmd.Stdin = bytes.NewReader(raw)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("native job card %s: deadline=%v exit=%v %.2500s", mode, ctx.Err(), err, out)
			}
		})
	}
}

func TestJobCardProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_JOB_CARD_HELPER") != "1" {
		return
	}
	var request nativeCardRequest
	if json.NewDecoder(io.LimitReader(os.Stdin, 64<<10)).Decode(&request) != nil {
		t.Fatal("invalid fixture request")
	}
	var w *Workspace
	var err error
	if request.Mode == "unowned" {
		w, err = Open(request.Start)
	} else {
		w, err = OpenForProcess(request.Start)
	}
	if err != nil {
		t.Fatal("synthetic admission failed")
	}
	writeFixture(t, request.Start.DataRoot, nativeCardPath, nativeJobCard)
	writeFixture(t, request.Start.DataRoot, nativeRawPath, "Synthetic JD\r\nsecond line\rthird\n")
	writeFixture(t, request.Start.DataRoot, "documents/private.bin", "Not a JD: never an allowed input")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	want := ""
	switch request.Mode {
	case "unowned":
		want = "ASSESSMENT_OWNERSHIP_REQUIRED"
	case "identity":
		writeFixture(t, request.Start.DataRoot, datahome.MarkerPath, marker("dev", "dh_22222222222222222222222222222222"))
		want = "DATA_HOME_IDENTITY_CHANGED"
	case "recovery":
		writeFixture(t, request.Start.DataRoot, datahome.DocumentRecoveryPath, "recovery")
		want = "DATA_HOME_RECOVERY_REQUIRED"
	case "resources":
		writeFixture(t, request.Start.ResourceRoot, "VERSION", "invalid")
		want = "SIDECAR_RESOURCE_INVALID"
	case "canceled":
		cancel()
		want = "ASSESSMENT_INPUT_CANCELED"
	case "directory":
		path := filepath.Join(request.Start.DataRoot, filepath.FromSlash(nativeRawPath))
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		want = "JOB_CARD_SOURCE_UNAVAILABLE"
	case "invalid-raw":
		writeFixture(t, request.Start.DataRoot, nativeCardPath, strings.ReplaceAll(nativeJobCard, nativeRawPath, "jobs/incoming/../../documents/private.bin"))
	case "device":
		writeFixture(t, request.Start.DataRoot, nativeCardPath, strings.ReplaceAll(nativeJobCard, nativeRawPath, "jobs/incoming/NUL"))
		want = "JOB_CARD_SOURCE_UNAVAILABLE"
	case "junction":
		base := filepath.Dir(request.Start.DataRoot)
		writeFixture(t, base, "outside/secret.txt", "PRIVATE synthetic outside subtree")
		link := filepath.Join(request.Start.DataRoot, "jobs", "incoming", "linked")
		target := filepath.Join(base, "outside")
		quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
		fixtureCtx, stop := context.WithTimeout(ctx, 15*time.Second)
		defer stop()
		cmd := exec.CommandContext(fixtureCtx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "New-Item -ItemType Junction -Path "+quote(link)+" -Target "+quote(target)+" -ErrorAction Stop | Out-Null")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		cmd.WaitDelay = 500 * time.Millisecond
		if err := cmd.Run(); err != nil {
			t.Fatal("native junction fixture unavailable; no passing junction claim")
		}
		writeFixture(t, request.Start.DataRoot, nativeCardPath, strings.ReplaceAll(nativeJobCard, nativeRawPath, "jobs/incoming/linked/secret.txt"))
		want = "JOB_CARD_SOURCE_UNAVAILABLE"
	}
	base := filepath.Dir(request.Start.DataRoot)
	before := tree(t, base)
	result, err := w.ReadJobCard(ctx, nativeCardPath)
	if want != "" {
		requireCode(t, err, want)
		if result != nil {
			t.Fatal("native failure returned partial card")
		}
	} else if request.Mode == "invalid-raw" {
		if err != nil || result == nil || result.Report.SchemaValid() || result.RawPresent || result.RawText != "" {
			t.Fatal("escape was read or accepted")
		}
	} else if err != nil || result == nil || !result.Report.SchemaValid() || result.RawText != "Synthetic JD\nsecond line\nthird\n" {
		t.Fatalf("valid fresh native read failed: %v", err)
	}
	if request.Mode == "valid" {
		saved, readErr := w.SavedJob(ctx, "synthetic")
		sha := sha256.Sum256([]byte(nativeJobCard))
		if readErr != nil || saved == nil || saved.Card["card_revision"] != "sha256:"+hex.EncodeToString(sha[:]) {
			t.Fatal("saved-job projection did not expose the displayed card bytes revision")
		}
	}
	if !reflect.DeepEqual(before, tree(t, base)) {
		t.Fatal("validation modified private or resource files")
	}
	if request.Mode == "refresh" {
		writeFixture(t, request.Start.DataRoot, nativeRawPath, "Fresh JD")
		writeFixture(t, request.Start.DataRoot, nativeCardPath, strings.Replace(nativeJobCard, "title: ''", "title: Changed", 1))
		before = tree(t, base)
		fresh, err := w.ReadJobCard(ctx, nativeCardPath)
		if err != nil || fresh == nil || !fresh.Report.SchemaValid() || fresh.RawText != "Fresh JD" || fresh.CardFileSHA256 == result.CardFileSHA256 || fresh.RawFileSHA256 == result.RawFileSHA256 {
			t.Fatal("native fresh read reused stale data")
		}
		if !reflect.DeepEqual(before, tree(t, base)) {
			t.Fatal("refresh wrote files")
		}
	}
}
