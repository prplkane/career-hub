//go:build windows

package readonly

import (
	"bytes"
	"context"
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

type nativeDigestRequest struct {
	Start protocol.StartRecord
	Mode  string
}

func TestNativeRetainedJobDigest(t *testing.T) {
	for _, mode := range []string{"valid", "provided", "refresh", "empty", "unowned", "identity", "recovery", "resources", "canceled", "directory", "device", "junction", "profile-invalid", "catalog-invalid"} {
		t.Run(mode, func(t *testing.T) {
			_, start := fixture(t)
			raw, err := json.Marshal(nativeDigestRequest{start, mode})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRetainedDigestProcessHelper$")
			cmd.Env = append(os.Environ(), "CAREERHUB_JOB_DIGEST_HELPER=1")
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			cmd.WaitDelay = 500 * time.Millisecond
			cmd.Stdin = bytes.NewReader(raw)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("native digest %s: deadline=%v exit=%v %.2500s", mode, ctx.Err(), err, out)
			}
		})
	}
}

func TestRetainedDigestProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_JOB_DIGEST_HELPER") != "1" {
		return
	}
	var request nativeDigestRequest
	if json.NewDecoder(io.LimitReader(os.Stdin, 64<<10)).Decode(&request) != nil {
		t.Fatal("invalid native fixture request")
	}
	var workspace *Workspace
	var err error
	if request.Mode == "unowned" {
		workspace, err = Open(request.Start)
	} else {
		workspace, err = OpenForProcess(request.Start)
	}
	if err != nil {
		t.Fatal("synthetic admission failed")
	}
	data, resources := digestMemories()
	for path, raw := range data.files {
		writeFixture(t, request.Start.DataRoot, path, string(raw))
	}
	for path, raw := range resources.files {
		writeFixture(t, request.Start.ResourceRoot, path, string(raw))
	}
	writeFixture(t, request.Start.DataRoot, "documents/private.bin", "Not a job source; never an input")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	options := DigestOptions{}
	want := ""
	switch request.Mode {
	case "provided":
		options = DigestOptions{Company: "Explicit company", Title: "Explicit role"}
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
	case "empty":
		writeFixture(t, request.Start.DataRoot, digestRawPath, "\ufeff \r\n")
		want = "JOB_DIGEST_SOURCE_MISSING"
	case "directory":
		path := filepath.Join(request.Start.DataRoot, filepath.FromSlash(digestRawPath))
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		want = "JOB_CARD_SOURCE_UNAVAILABLE"
	case "device":
		writeFixture(t, request.Start.DataRoot, digestCardPath, strings.ReplaceAll(digestCard, digestRawPath, "jobs/incoming/NUL"))
		want = "JOB_CARD_SOURCE_UNAVAILABLE"
	case "junction":
		base := filepath.Dir(request.Start.DataRoot)
		writeFixture(t, base, "outside/secret.txt", "PRIVATE synthetic outside tree")
		link := filepath.Join(request.Start.DataRoot, "jobs", "incoming", "linked")
		target := filepath.Join(base, "outside")
		quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
		fixtureCtx, stop := context.WithTimeout(ctx, 15*time.Second)
		defer stop()
		command := exec.CommandContext(fixtureCtx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "New-Item -ItemType Junction -Path "+quote(link)+" -Target "+quote(target)+" -ErrorAction Stop | Out-Null")
		command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		command.WaitDelay = 500 * time.Millisecond
		if err := command.Run(); err != nil {
			t.Fatal("native junction fixture unavailable; no passing junction claim")
		}
		writeFixture(t, request.Start.DataRoot, digestCardPath, strings.ReplaceAll(digestCard, digestRawPath, "jobs/incoming/linked/secret.txt"))
		want = "JOB_CARD_SOURCE_UNAVAILABLE"
	case "profile-invalid":
		writeFixture(t, request.Start.DataRoot, digestProfilePath, "requires_sponsorship_now: 'yes'")
		want = "JOB_DIGEST_PROFILE_INVALID"
	case "catalog-invalid":
		writeFixture(t, request.Start.ResourceRoot, "src/data/job_technologies.yaml", "- {id: missing_name}")
		want = "JOB_DIGEST_CATALOG_INVALID"
	}
	base := filepath.Dir(request.Start.DataRoot)
	before := tree(t, base)
	got, err := workspace.DigestJobSource(ctx, digestCardPath, options)
	if want != "" {
		requireCode(t, err, want)
		if got != nil {
			t.Fatal("native failure returned partial digest")
		}
	} else {
		if err != nil || got == nil || len(got.Digest.Occurrences) != 2 {
			t.Fatalf("native complete digest failed: %v", err)
		}
		if request.Mode == "provided" {
			if got.Digest.Fields["company"].State != "provided" || got.Digest.Fields["title"].Value != "Explicit role" {
				t.Fatal("native explicit options")
			}
		} else if got.Digest.Fields["company"].Value != "Source Company" || got.Digest.Fields["company"].State != "explicit" {
			t.Fatal("native source decode/BOM/casing")
		}
	}
	if !reflect.DeepEqual(before, tree(t, base)) {
		t.Fatal("read-only digest changed files")
	}
	if request.Mode == "refresh" {
		writeFixture(t, request.Start.DataRoot, digestRawPath, strings.ReplaceAll(digestSource, "Python", "NotRegistered"))
		before = tree(t, base)
		fresh, err := workspace.DigestJobSource(ctx, digestCardPath, DigestOptions{})
		if err != nil || fresh == nil || fresh.RawFileSHA256 == got.RawFileSHA256 || len(fresh.Digest.Occurrences) != 1 {
			t.Fatal("native rescan reused stale source")
		}
		if !reflect.DeepEqual(before, tree(t, base)) {
			t.Fatal("native refresh changed files")
		}
	}
}
