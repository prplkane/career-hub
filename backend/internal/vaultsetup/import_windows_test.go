//go:build windows

package vaultsetup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/vaultread"
)

const marker = `{"application":"CareerHub","channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef","format_version":2}`

func fixtureBundle(t *testing.T) *Bundle {
	t.Helper()
	texts := []string{
		"name: Synthetic Candidate\ntarget_location: US\nwork_authorization: Synthetic only\nmajor: Computing\n",
		"schools: []\n",
		"current_status: Synthetic\nwork_authorization: Synthetic only\nrequires_future_sponsorship: false\nstem_opt_eligible: false\n",
		"- {id: exp, company: Synthetic, role: Engineer, dates: '2025', category: technical, technologies: [], proof_points: []}\n",
		"- {id: project, name: Synthetic, category: technical, summary: Test, technologies: [], proof_points: []}\n",
		"- {id: skill, skill_family: Technical, related_experience: [exp], keywords: []}\n",
		"- {id: proof, claim: Synthetic, evidence_type: experience, source_block: exp}\n",
		"standards_metadata: {reviewed_on: '2026-09-01', review_due: '2027-09-01', market: US, authority_order: [user]}\nresume_voice: {}\nbullet_rules: {}\ndocument_rules: {layout: single_column, standard_sections: [Experience]}\nats_rules: {}\nselection_rules: {allowed_decisions: [full, compact, excluded]}\nquality_gates: {}\nforbidden: []\n",
	}
	files := map[string][]byte{}
	for index, path := range vaultread.SetupPaths() {
		files[path] = []byte(texts[index])
	}
	bundle, err := Prepare(context.Background(), files)
	if err != nil {
		t.Fatal(err.Code)
	}
	files[vaultread.SetupPaths()[0]][0] = 'X'
	if bundle.files[vaultread.SetupPaths()[0]][0] == 'X' {
		t.Fatal("caller mutated prepared bytes")
	}
	return bundle
}

func TestImportNativeOwnedProcess(t *testing.T) {
	if os.Getenv("CAREERHUB_SETUP_CHILD") != "" {
		runNative(t, os.Getenv("CAREERHUB_SETUP_ROOT"), os.Getenv("CAREERHUB_SETUP_CHILD"))
		return
	}
	for _, version := range []string{"1", "2"} {
		t.Run("version_"+version, func(t *testing.T) {
			for _, mode := range []string{"fresh", "no_lease", "matching_partial", "conflict", "stale_plan", "read_drift", "cancel", "create_before", "create_after", "home_drift", "close", "recovery", "restart"} {
				t.Run(mode, func(t *testing.T) {
					root := t.TempDir()
					if err := os.Mkdir(filepath.Join(root, "career_vault"), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(root, datahome.MarkerPath), []byte(marker), 0600); err != nil {
						t.Fatal(err)
					}
					run := func(mode string) {
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestImportNativeOwnedProcess$")
						cmd.Env = append(os.Environ(), "CAREERHUB_SETUP_CHILD="+mode, "CAREERHUB_SETUP_ROOT="+root, "CAREERHUB_SETUP_VERSION="+version)
						if out, err := cmd.CombinedOutput(); err != nil {
							t.Fatalf("%s: %v %.3000s", mode, err, out)
						}
					}
					if mode == "restart" {
						run("exit_mid_import")
						run("resume")
						return
					}
					run(mode)
				})
			}
		})
	}
}

func runNative(t *testing.T, dir, mode string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	held, pathErr := runtimepaths.Open(dir)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	defer held.Close()
	if mode != "no_lease" {
		if err := held.ClaimForProcess(); err != nil {
			t.Fatal(err)
		}
	}
	home := datahome.Info{FormatVersion: 2, Channel: "dev", Initialized: true, DataHomeID: "dh_0123456789abcdef0123456789abcdef"}
	s := New(dir, home)
	bundle := fixtureBundle(t)
	if os.Getenv("CAREERHUB_SETUP_VERSION") == "2" {
		var problem *Error
		bundle, problem = PrepareEmpty(ctx, "Synthetic Candidate")
		if problem != nil {
			t.Fatal(problem.Code)
		}
	}
	paths := vaultread.SetupPaths()
	write := func(path string, raw []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(path)), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "matching_partial" {
		write(paths[0], bundle.files[paths[0]])
	}
	if mode == "conflict" {
		write(paths[0], []byte("name: Existing user facts\n"))
	}
	if mode == "recovery" {
		write(datahome.RegistryRecoveryPath, []byte("opaque pending journal"))
	}
	preview, problem := s.Preview(ctx, bundle)
	if mode == "no_lease" || mode == "conflict" || mode == "recovery" {
		if problem == nil || preview.Plan != nil || preview.Committed {
			t.Fatal("unsafe preview accepted", preview, problem)
		}
		return
	}
	if problem != nil || preview.Plan == nil || preview.Committed {
		t.Fatal("preview", preview, problem)
	}
	if mode == "resume" && len(preview.Plan.Retain) != 2 {
		t.Fatal("interrupted process did not retain exactly two files")
	}
	create := s.create
	calls := 0
	s.create = func(root *runtimepaths.Root, path string, raw []byte) (bool, *runtimepaths.Error) {
		calls++
		if mode == "create_before" && calls == 2 {
			return false, &runtimepaths.Error{Code: "private synthetic cause"}
		}
		created, err := create(root, path, raw)
		if err != nil {
			return created, err
		}
		if calls == 2 {
			switch mode {
			case "cancel":
				cancel()
			case "exit_mid_import":
				os.Exit(0)
			case "create_after":
				return created, &runtimepaths.Error{Code: "private synthetic cause"}
			case "home_drift":
				write(datahome.MarkerPath, []byte(strings.Replace(marker, "0123456789abcdef0123456789abcdef", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1)))
			}
		}
		return created, nil
	}
	if mode == "stale_plan" {
		write(paths[0], bundle.files[paths[0]])
	}
	if mode == "read_drift" {
		read := s.read
		reads := 0
		s.read = func(root *runtimepaths.Root, path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
			value, present, err := read(root, path, limit)
			reads++
			// Last read during the final guard before first create changes an
			// already checked file. Atomic no-replace still refuses that target.
			if reads == 24 {
				write(paths[0], []byte("name: Intervening user facts\n"))
			}
			return value, present, err
		}
	}
	if mode == "close" {
		s.close = func(root *runtimepaths.Root) error { _ = root.Close(); return errors.New("private close cause") }
	}
	out, problem := s.Apply(ctx, bundle, preview.Plan.PlanID)
	success := mode == "fresh" || mode == "matching_partial" || mode == "resume"
	if success {
		if problem != nil || !out.Complete || !out.Committed || out.Created != len(preview.Plan.Create) {
			t.Fatal("apply", out, problem)
		}
		for path, want := range bundle.files {
			got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
			if err != nil || !bytes.Equal(got, want) {
				t.Fatal("content was normalized or lost", path, err)
			}
		}
		state, err := vaultread.ReadSetup(ctx, dir)
		if err != nil || !strings.Contains(string(state), `"state":"configured"`) {
			t.Fatal("not configured", err)
		}
		if _, err := vaultread.ReadProfile(ctx, dir); err != nil {
			t.Fatal("Profile unusable", err)
		}
		again, err2 := s.Preview(ctx, bundle)
		if err2 != nil || len(again.Plan.Create) != 0 {
			t.Fatal("repeat preview", err2)
		}
		replay, err2 := s.Apply(ctx, bundle, again.Plan.PlanID)
		if err2 != nil || !replay.Complete || replay.Committed || replay.Created != 0 {
			t.Fatal("repeat wrote", replay, err2)
		}
		return
	}
	if problem == nil || out.Complete || out.Plan != nil {
		t.Fatal("incomplete import reported success", out, problem)
	}
	if mode == "stale_plan" && (problem.Code != "VAULT_SETUP_PLAN_CHANGED" || calls != 0) {
		t.Fatal("stale preview wrote", out, problem)
	}
	if mode == "read_drift" {
		got, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(paths[0])))
		if string(got) != "name: Intervening user facts\n" || out.Committed {
			t.Fatal("overwrote intervening data", out)
		}
	}
	if mode == "cancel" || mode == "home_drift" || mode == "create_after" {
		if calls != 2 || !out.Committed || out.Created != 2 {
			t.Fatal("lost partial commit or continued", out, calls)
		}
	}
	if mode == "create_before" && (out.Created != 1 || !out.Committed || !out.CommitUnknown) {
		t.Fatal("lost earlier commit", out)
	}
	if mode == "close" && (!out.Committed || !out.CleanupFailed || out.Created != 8) {
		t.Fatal("close failure erased commit", out)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "private") || strings.Contains(problem.Error(), "private") {
		t.Fatal("native error leaked")
	}
}
