//go:build windows

package vaultsetup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"careerhub.local/backend/internal/skills/cataloginput"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/vault/vaultread"
	"careerhub.local/backend/internal/vault/vaultwrite"
)

func TestEmptyNativeFirstEditsAndRestart(t *testing.T) {
	mode := os.Getenv("CAREERHUB_EMPTY_CHILD")
	if mode == "" {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "career_vault"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, datahome.MarkerPath), []byte(marker), 0600); err != nil {
			t.Fatal(err)
		}
		for _, stage := range []string{"create_edit", "restart"} {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEmptyNativeFirstEditsAndRestart$")
			cmd.Env = append(os.Environ(), "CAREERHUB_EMPTY_CHILD="+stage, "CAREERHUB_EMPTY_ROOT="+root)
			out, err := cmd.CombinedOutput()
			cancel()
			if err != nil {
				t.Fatalf("%s: %v %.3000s", stage, err, out)
			}
		}
		return
	}
	ctx := context.Background()
	dir := os.Getenv("CAREERHUB_EMPTY_ROOT")
	held, pathErr := runtimepaths.Open(dir)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	defer held.Close()
	if err := held.ClaimForProcess(); err != nil {
		t.Fatal(err)
	}
	read := func(path string) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	revision := func(path string) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(read(path))) }
	if mode == "create_edit" {
		bundle, problem := PrepareEmpty(ctx, "Synthetic Candidate")
		if problem != nil {
			t.Fatal(problem.Code)
		}
		s := New(dir, datahome.Info{FormatVersion: 2, Channel: "dev", Initialized: true, DataHomeID: "dh_0123456789abcdef0123456789abcdef"})
		preview, problem := s.Preview(ctx, bundle)
		if problem != nil {
			t.Fatal(problem.Code)
		}
		if out, problem := s.Apply(ctx, bundle, preview.Plan.PlanID); problem != nil || !out.Complete {
			t.Fatal("setup", problem)
		}
		editor := vaultwrite.New(dir)
		profilePath := "career_vault/profile.yaml"
		out, err := editor.UpdateProfile(ctx, "profile", map[string]any{"name": "Synthetic Edited", "role_targets": []any{}}, revision(profilePath))
		if err != nil || !out.Committed {
			t.Fatal("profile", err)
		}
		before := read(profilePath)
		out, err = editor.UpdateProfile(ctx, "profile", map[string]any{"name": "Overwrite", "role_targets": []any{}, "vault_schema_version": 1}, revision(profilePath))
		if err == nil || out.Committed || !bytes.Equal(before, read(profilePath)) {
			t.Fatal("version mutation admitted")
		}
		visaPath := "career_vault/visa_profile.yaml"
		// Explicit false/true remain facts; returning to unknown removes them,
		// rather than silently converting an unanswered checkbox to false.
		for _, flags := range []map[string]any{{"requires_future_sponsorship": true, "stem_opt_eligible": false}, {}} {
			out, err = editor.UpdateEligibility(ctx, "eligibility", flags, revision(visaPath))
			if err != nil || !out.Committed {
				t.Fatal("eligibility", err)
			}
		}
		evidence := vaultwrite.NewEvidence(dir, dir)
		created, err := evidence.CreateExperience(ctx, map[string]any{"company": "Synthetic", "role": "Engineer", "dates": "2026", "category": "technical", "technologies": []any{}, "proof_points": []any{}}, revision("career_vault/experience_blocks.yaml"))
		if err != nil || !created.Committed {
			t.Fatal("first experience", err)
		}
	}
	profile, err := vaultread.ReadProfile(ctx, dir)
	if err != nil || !bytes.Contains(profile, []byte("Synthetic Edited")) {
		t.Fatal("profile restart", err)
	}
	value, parseErr := cataloginput.ParseYAML(read("career_vault/profile.yaml"))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	if version, err := vaultread.SchemaVersion(value); err != nil || version != 2 {
		t.Fatal("version lost")
	}
	value, parseErr = cataloginput.ParseYAML(read("career_vault/visa_profile.yaml"))
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	for _, entry := range value.Entries {
		if entry.Value.Kind == "bool" {
			t.Fatal("unknown eligibility became a boolean")
		}
	}
	value, parseErr = cataloginput.ParseYAML(read("career_vault/experience_blocks.yaml"))
	if parseErr != nil || len(value.Items) != 1 {
		t.Fatal("first evidence lost", parseErr)
	}
	if !bytes.Equal(read(datahome.MarkerPath), []byte(marker)) {
		t.Fatal("Home identity changed")
	}
}
