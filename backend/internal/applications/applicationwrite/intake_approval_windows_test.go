//go:build windows

package applicationwrite

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"careerhub.local/backend/internal/intake/intakeread"
	"careerhub.local/backend/internal/intake/intakereview"
	"careerhub.local/backend/internal/intake/sourceupload"
	"careerhub.local/backend/internal/skills/catalogstore"
	"careerhub.local/backend/internal/skills/catalogtransaction"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
)

const approvalTestID = "src_fictional_1234567890"

func approvalOwnerFixture(t *testing.T, home string) {
	t.Helper()
	extractionPath, reviewPath, _ := intakeReviewPaths(approvalTestID)
	source := map[string]any{"id": approvalTestID, "path": "career_vault/raw/fictional.txt", "file_type": "txt", "sha256": strings.Repeat("a", 64)}
	extraction, _ := json.Marshal(map[string]any{"schema_version": 2, "status": "extracted", "extractor_version": "vault-intake-0.3", "registry_revision": strings.Repeat("0", 64), "source": source, "blocks": []any{map[string]any{"id": "b1", "order": 0, "kind": "paragraph", "text": "Source evidence", "is_bullet": true}}, "section_hints": []any{}, "technology_ids": []any{}, "technology_mentions": []any{}, "date_candidates": []any{}, "bullet_candidates": []any{}, "unresolved_fields": []any{}})
	review, _ := json.Marshal(map[string]any{"extraction_schema_version": 2, "source": source, "entities": []any{map[string]any{"id": "entity", "entity_type": "project", "title": "New source project", "items": []string{"Source summary"}, "pending_skills": []any{map[string]any{"name": "Fictional Tool"}}}}, "block_reviews": []any{map[string]any{"source_ref": map[string]any{"source_id": approvalTestID, "block_id": "b1"}, "category": "project", "disposition": "assigned", "owner_id": "entity"}}, "approval_decisions": []any{map[string]any{"subject_kind": "entity", "subject_id": "entity", "action": "create"}}})
	files := map[string][]byte{
		datahome.MarkerPath:                        []byte(`{"application":"CareerHub","format_version":2,"channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`),
		"resources/" + catalogstore.CoreGroupsPath: []byte("- {id: tools, name: Tools}\n"),
		"resources/" + catalogstore.CoreSkillsPath: []byte("[]\n"),
		"career_vault/profile.yaml":                []byte("name: Synthetic\ntarget_location: Anywhere\nwork_authorization: Synthetic\nmajor: Computing\n"),
		"career_vault/education.yaml":              []byte("schools: []\ncertifications: []\n"),
		"career_vault/experience_blocks.yaml":      []byte("- {id: exp, company: Example, role: Engineer, dates: '2023', category: Work, technologies: [], proof_points: []}\n"),
		"career_vault/project_blocks.yaml":         []byte("- {id: project, name: Existing, category: Project, summary: Synthetic, technologies: [], proof_points: [], custom: {keep: true}}\n"),
		"career_vault/skill_blocks.yaml":           []byte("- {id: skill, skill_family: Computing, related_experience: [exp], keywords: []}\n"),
		extractionPath:                             extraction, reviewPath: review,
	}
	for path, raw := range files {
		full := filepath.Join(home, filepath.FromSlash(path))
		if os.MkdirAll(filepath.Dir(full), 0700) != nil || os.WriteFile(full, raw, 0600) != nil {
			t.Fatal("fixture")
		}
	}
}

func TestIntakeApprovalOwnedRecoveryAndFaults(t *testing.T) {
	if home := os.Getenv("CAREERHUB_TEST_APPROVAL_HOME"); home != "" {
		runApprovalOwner(t, home, os.Getenv("CAREERHUB_TEST_APPROVAL_CASE"))
		return
	}
	for _, scenario := range []string{"success", "stale", "review-drift", "core-drift", "tampered-proposal", "partial", "recovery-source-drift", "recovery-journal-tamper", "after-commit", "source-after-commit", "terminal-diagnostic", "root-close"} {
		t.Run(scenario, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "home")
			approvalOwnerFixture(t, home)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIntakeApprovalOwnedRecoveryAndFaults$")
			cmd.Env = append(os.Environ(), "CAREERHUB_TEST_APPROVAL_HOME="+home, "CAREERHUB_TEST_APPROVAL_CASE="+scenario)
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			cmd.WaitDelay = time.Second
			if raw, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("native owner: %v %.1800s", err, raw)
			}
		})
	}
}

func encodeApprovalTestIntent(homeID string, changes []catalogtransaction.Change, guards []catalogtransaction.Guard) []byte {
	payload, _ := json.Marshal(struct {
		Version int                         `json:"version"`
		HomeID  string                      `json:"data_home_id"`
		Changes []catalogtransaction.Change `json:"changes"`
		Guards  []catalogtransaction.Guard  `json:"guards,omitempty"`
	}{3, homeID, changes, guards})
	raw, _ := json.Marshal(struct {
		Payload json.RawMessage `json:"payload"`
		SHA256  string          `json:"sha256"`
	}{payload, digestBytes(payload)})
	return raw
}

func runApprovalOwner(t *testing.T, home, scenario string) {
	root, pe := runtimepaths.Open(home)
	if pe != nil {
		t.Fatal(pe)
	}
	defer root.Close()
	if pe := root.ClaimForProcess(); pe != nil {
		t.Fatal(pe)
	}
	info, he := datahome.Inspect(home, nil)
	if he != nil {
		t.Fatal(he)
	}
	resources, pe := runtimepaths.Open(filepath.Join(home, "resources"))
	if pe != nil {
		t.Fatal(pe)
	}
	defer resources.Close()
	state, ce := catalogstore.ReadState(intakeCatalogReader{resources}, intakeCatalogReader{root})
	if ce != nil {
		t.Fatal(ce)
	}
	extractionPath, reviewPath, resultPath := intakeReviewPaths(approvalTestID)
	extraction, _, _ := root.ReadSingleLinkFile(extractionPath, intakeread.MaxExtractionBytes)
	review, _, _ := root.ReadSingleLinkFile(reviewPath, intakeread.MaxReviewBytes)
	vault := map[string][]byte{}
	for _, path := range intakereview.ApprovalPreviewPaths() {
		raw, _, _ := root.ReadSingleLinkFile(path, 2<<20)
		vault[path] = raw
	}
	p, err := intakereview.BuildApprovalPreview(context.Background(), extraction, review, vault, state.Snapshot.Revision(), map[string]string{})
	if err != nil {
		t.Fatal(err.Code)
	}
	owner := New(home).PinnedToHome(*info).PinnedToResources(filepath.Join(home, "resources"))
	if scenario == "stale" {
		p.ReviewRevision = "sha256:" + strings.Repeat("0", 64)
	}
	owner.checkpoint = func(stage string) *Error {
		if stage == "intake_approval_saved" && scenario == "source-after-commit" {
			if e := os.WriteFile(filepath.Join(home, reviewPath), append(bytes.Clone(review), ' '), 0600); e != nil {
				t.Fatal(e)
			}
		}
		if stage == "intake_approval_saved" && scenario == "after-commit" {
			return failed("test", "TEST_VERIFY")
		}
		if stage == "intake_approval_prepared" {
			if scenario == "review-drift" {
				if e := os.WriteFile(filepath.Join(home, reviewPath), append(bytes.Clone(review), ' '), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if scenario == "core-drift" {
				if e := os.WriteFile(filepath.Join(home, "resources", catalogstore.CoreSkillsPath), []byte("# changed\n[]\n"), 0600); e != nil {
					t.Fatal(e)
				}
			}
		}
		return nil
	}
	if scenario == "terminal-diagnostic" {
		owner.intakeMutations.replace = func(*runtimepaths.Root, string, []byte, []byte) (bool, *runtimepaths.Error) {
			return false, &runtimepaths.Error{Code: "TEST_DIAGNOSTIC"}
		}
	}
	if scenario == "root-close" {
		owner.closeRoot = func(r *runtimepaths.Root) error {
			r.Close()
			return &runtimepaths.Error{Code: "TEST_CLOSE"}
		}
	}
	partial := scenario == "partial" || strings.HasPrefix(scenario, "recovery-")
	if partial || scenario == "tampered-proposal" {
		owner.approvalBegin = func(ctx context.Context, r *runtimepaths.Root, id string, changes []catalogtransaction.Change, guards []catalogtransaction.Guard, validate catalogtransaction.Validate) (catalogtransaction.Report, error) {
			if scenario == "tampered-proposal" {
				changes[3].After = append(changes[3].After, []byte("# unexplained\n")...)
				return catalogtransaction.BeginApprovalOwned(ctx, r, id, changes, guards, validate)
			}
			journal := encodeApprovalTestIntent(id, changes, guards)
			if created, e := r.CreateFile(catalogtransaction.ApprovalJournalPath, journal); e != nil || !created {
				t.Fatal("intent fixture")
			}
			c := changes[3]
			if saved, e := r.ReplaceFile(c.Path, c.Before, c.After); e != nil || !saved {
				t.Fatal("partial fixture")
			}
			return catalogtransaction.Report{IntentRecorded: true, AppliedFiles: []string{c.Path}}, catalogtransaction.ErrIO
		}
	}
	out, problem := owner.ApplyIntakeApproval(context.Background(), approvalTestID, p.ReviewRevision, p.VaultRevision, p.RegistryRevision)
	if out == nil || out.RunID == "" {
		t.Fatal("no diagnostic correlation")
	}
	defer assertApprovalDiagnostics(t, home)
	if partial {
		if problem == nil || !out.Committed || !out.RecoveryRequired || out.Result != nil {
			t.Fatal("partial state hidden")
		}
		if scenario == "recovery-source-drift" {
			if err := os.WriteFile(filepath.Join(home, reviewPath), append(bytes.Clone(review), ' '), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if scenario == "recovery-journal-tamper" {
			raw, err := os.ReadFile(filepath.Join(home, catalogtransaction.ApprovalJournalPath))
			if err != nil {
				t.Fatal(err)
			}
			var envelope struct {
				Payload json.RawMessage `json:"payload"`
				SHA256  string          `json:"sha256"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatal(err)
			}
			var data struct {
				Version int                         `json:"version"`
				HomeID  string                      `json:"data_home_id"`
				Changes []catalogtransaction.Change `json:"changes"`
				Guards  []catalogtransaction.Guard  `json:"guards,omitempty"`
			}
			if err := json.Unmarshal(envelope.Payload, &data); err != nil || len(data.Changes) != 8 {
				t.Fatal("invalid journal fixture")
			}
			original := data.Changes[7].After
			data.Changes[7].After = bytes.Replace(original, []byte(`"status":"applied"`), []byte(`"status":"fabricated"`), 1)
			if bytes.Equal(original, data.Changes[7].After) {
				t.Fatal("journal tampering did not alter the result")
			}
			if err := os.WriteFile(filepath.Join(home, catalogtransaction.ApprovalJournalPath), encodeApprovalTestIntent(data.HomeID, data.Changes, data.Guards), 0600); err != nil {
				t.Fatal(err)
			}
		}
		out, problem = owner.RecoverIntakeApproval(context.Background())
		if scenario != "partial" {
			if problem == nil || !out.RecoveryRequired || out.Result != nil || out.RunID == "" {
				t.Fatal("unverified recovery accepted")
			}
			if present, _ := root.Exists(resultPath); present {
				t.Fatal("unverified result published")
			}
			return
		}
	}
	wantSuccess := scenario == "success" || scenario == "partial"
	if wantSuccess {
		if problem != nil || !out.Committed || out.RecoveryRequired || !out.ApprovalWrite.Completed || out.Result == nil {
			t.Fatalf("approval failed: %+v", problem)
		}
		if result, pe := intakeread.ParseApproval(out.Result); pe != nil || result.SourceID != approvalTestID {
			t.Fatal("wrong result")
		}
		if again, err := owner.ApplyIntakeApproval(context.Background(), approvalTestID, p.ReviewRevision, p.VaultRevision, p.RegistryRevision); err == nil || err.Code != "APPROVAL_ALREADY_APPLIED" || again.Committed {
			t.Fatal("repeat approval was not refused")
		}
		if again, err := owner.RecoverIntakeApproval(context.Background()); err != nil || again.Committed || again.CommitAttempted {
			t.Fatal("no-op recovery fabricated writes")
		}
	} else {
		if problem == nil || out.Result != nil {
			t.Fatal("failed operation presented success")
		}
		committed := scenario == "after-commit" || scenario == "source-after-commit" || scenario == "terminal-diagnostic" || scenario == "root-close"
		if out.Committed != committed {
			t.Fatal("lost commit fact", scenario)
		}
	}
}

func assertApprovalDiagnostics(t *testing.T, home string) {
	t.Helper()
	entries, e := os.ReadDir(filepath.Join(home, "career_vault/logs/native-intake"))
	if e != nil || len(entries) == 0 {
		t.Fatal("diagnostics missing")
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(home, "career_vault/logs/native-intake", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sourceupload.Decode(raw); err != nil {
			t.Fatal("invalid retained diagnostic")
		}
		if bytes.Contains(raw, []byte("Fictional Tool")) || bytes.Contains(raw, []byte("Source summary")) {
			t.Fatal("private content in diagnostic")
		}
	}
}
