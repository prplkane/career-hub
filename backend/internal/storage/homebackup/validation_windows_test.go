//go:build windows

package homebackup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/documents/envelope"
	"careerhub.local/backend/internal/storage/documents/keys"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/sqlitestore"
	"careerhub.local/backend/internal/vault/vaultread"
)

type syntheticValidationKeys struct {
	t       *testing.T
	missing bool
	reads   int
}

func (b *syntheticValidationKeys) Read(target string) ([]byte, bool, error) {
	b.reads++
	if target != "CareerHub/stable/document-key/"+testHomeID+"/key_"+strings.Repeat("a", 32) {
		b.t.Fatal("unexpected synthetic credential target")
	}
	if b.missing {
		return nil, false, nil
	}
	return bytes.Repeat([]byte{7}, 32), true, nil
}
func (b *syntheticValidationKeys) Write(string, []byte) error {
	b.t.Fatal("credential write during validation")
	return nil
}
func (b *syntheticValidationKeys) Delete(string) (bool, error) {
	b.t.Fatal("credential delete during validation")
	return false, nil
}

type validationFixtureResult struct {
	Accepted bool               `json:"accepted"`
	Summary  *PayloadValidation `json:"summary"`
}

func TestSnapshotValidationNativeAndPython(t *testing.T) {
	for _, scenario := range []string{"empty", "full", "vault-partial", "vault-invalid", "job-invalid", "job-missing-raw", "job-duplicate", "resume-missing", "resume-hash", "resume-path", "resume-blob-path", "old-schema", "database-corrupt", "database-future", "document-missing-key", "document-wrong-home", "document-auth", "document-hash", "document-missing", "document-extra", "document-blob-home", "document-purpose", "legacy-ledger", "legacy-blob", "document-mixed-legacy", "manifest-drift", "unowned"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSnapshotValidationHelper$")
			cmd.Env = append(os.Environ(), "CH_VALIDATION_HOME="+dir, "CH_VALIDATION_SCENARIO="+scenario)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("native %s: %v\n%s", scenario, err, out)
			}
			if scenario == "manifest-drift" || scenario == "unowned" {
				return
			} // Native admission/manifest guards, not source validator parity.

		})
	}
}

const validationJob = `id: synthetic
status: ready
company: Synthetic
title: Engineer
source_url: https://example.test/jobs/1
location: ''
remote_policy: ''
seniority: ''
required_skills: [Python]
required_platforms: []
required_capabilities: []
preferred_skills: []
experience_requirements: []
education_requirements: []
responsibilities: [Build services.]
visa_signals: {citizenship_required: null, clearance_required: null, sponsorship_mentioned: null, no_visa_or_opt: null}
raw_jd_path: jobs/incoming/synthetic.txt
`

func TestSnapshotValidationHelper(t *testing.T) {
	scenario := os.Getenv("CH_VALIDATION_SCENARIO")
	if scenario == "" {
		return
	}
	dir := os.Getenv("CH_VALIDATION_HOME")
	stage := filepath.Join(dir, filepath.FromSlash(syntheticSnapshot))
	ctx := context.Background()
	files := map[string][]byte{}
	manifest := testManifest()
	write := func(relative string, raw []byte) {
		t.Helper()
		file := filepath.Join(stage, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	marker, markerProblem := datahome.MarkerBytes("stable", testHomeID)
	if markerProblem != nil {
		t.Fatal(markerProblem)
	}
	write(datahome.MarkerPath, marker)
	if scenario == "full" || strings.HasPrefix(scenario, "vault-") {
		values := []string{
			"name: Synthetic\ntarget_location: US\nwork_authorization: synthetic\nmajor: Computer Science\n",
			"schools: [{id: school, institution: Synthetic College, degree: BS}]\ncertifications: [{id: cert, name: Synthetic, credential: Test, completed: '2026'}]\n",
			"current_status: synthetic\nwork_authorization: synthetic\nrequires_future_sponsorship: false\nstem_opt_eligible: false\n",
			"- {id: exp, company: Synthetic, role: Engineer, dates: '2025', category: technical, technologies: [], proof_points: []}\n",
			"- {id: project, name: Synthetic, category: technical, summary: Test, technologies: [], proof_points: [], related_experience: [exp]}\n",
			"- {id: skill, skill_family: Technical, related_experience: [exp], related_projects: [project], related_certifications: [cert], keywords: []}\n",
			"- {id: proof, claim: Synthetic, evidence_type: experience, source_block: exp}\n",
			"standards_metadata: {reviewed_on: '2026-09-01', review_due: '2027-09-01', market: US, authority_order: [false]}\nresume_voice: {}\nbullet_rules: {}\ndocument_rules: {layout: single_column, standard_sections: [false]}\nats_rules: {}\nselection_rules: {allowed_decisions: [full, compact, excluded, full]}\nquality_gates: {}\nforbidden: []\n",
		}
		for index, path := range vaultread.SetupPaths() {
			files[path] = []byte(values[index])
		}
		if scenario == "vault-partial" {
			delete(files, vaultread.SetupPaths()[1])
		}
		if scenario == "vault-invalid" {
			files[vaultread.SetupPaths()[0]] = []byte("name: [broken")
		}
	}
	if scenario == "full" || strings.HasPrefix(scenario, "job-") {
		files["jobs/normalized/synthetic.yaml"] = []byte(validationJob)
		files["jobs/incoming/synthetic.txt"] = []byte("Build services.")
		if scenario == "job-invalid" {
			files["jobs/normalized/synthetic.yaml"] = []byte("id: only")
		}
		if scenario == "job-missing-raw" {
			delete(files, "jobs/incoming/synthetic.txt")
		}
		if scenario == "job-duplicate" {
			files["jobs/normalized/duplicate.yaml"] = []byte(validationJob)
		}
	}
	if scenario == "full" || strings.HasPrefix(scenario, "resume-") || strings.HasPrefix(scenario, "database-") || scenario == "old-schema" {
		write(databasePath, nil)
		db, err := sqlitestore.OpenReadWriteExisting(ctx, filepath.Join(stage, filepath.FromSlash(databasePath)))
		if err != nil {
			t.Fatal(err)
		}
		version := int64(1)
		manifest.SQLiteSchemaVersion = &version
		if _, err = db.Exec("PRAGMA user_version=1"); err != nil {
			t.Fatal(err)
		}
		if scenario == "old-schema" {
			_, err = db.Exec("CREATE TABLE resume_library(id INTEGER)")
		} else {
			_, err = db.Exec("CREATE TABLE resume_library(relative_path,sha256,size_bytes)")
			if err != nil {
				t.Fatal(err)
			}
			name := "applications/resumes/library/test.txt"
			raw := []byte("synthetic resume")
			files[name] = raw
			f := testFile(name, string(raw))
			var relative any = name
			if scenario == "resume-path" {
				relative = "jobs/escape.txt"
			}
			if scenario == "resume-blob-path" {
				relative = []byte(name)
			}
			if scenario == "resume-hash" {
				f.SHA256 = strings.Repeat("f", 64)
			}
			_, err = db.Exec("INSERT INTO resume_library VALUES (?,?,?)", relative, f.SHA256, f.SizeBytes)
			if scenario == "resume-missing" {
				delete(files, name)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		if scenario == "database-future" {
			if _, err = db.Exec(fmt.Sprintf("PRAGMA user_version=%d", applications.SchemaVersion+1)); err != nil {
				t.Fatal(err)
			}
		}
		if err = db.Close(); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(stage, filepath.FromSlash(databasePath)))
		if err != nil {
			t.Fatal(err)
		}
		if scenario == "database-corrupt" {
			raw = []byte("invalid sqlite")
		}
		files[databasePath] = raw
	}
	if scenario == "full" || strings.HasPrefix(scenario, "document-") {
		var err error
		keyID := "key_" + strings.Repeat("a", 32)
		docID := "doc_" + strings.Repeat("b", 32)
		key := bytes.Repeat([]byte{7}, 32)
		state := keys.State{Version: 1, Algorithm: keys.Algorithm, DataHomeID: testHomeID, ActiveKeyID: keyID}
		if scenario == "document-wrong-home" {
			state.DataHomeID = "dh_" + strings.Repeat("f", 32)
		}
		files["documents/key_state.json"], err = state.CanonicalBytes()
		if err != nil {
			t.Fatal(err)
		}
		plain := []byte("synthetic private document")
		blobContext := envelope.Context{DataHomeID: testHomeID, KeyID: keyID, Purpose: "blob", ObjectID: docID}
		if scenario == "document-blob-home" {
			blobContext.DataHomeID = "dh_" + strings.Repeat("f", 32)
		}
		if scenario == "document-purpose" {
			blobContext.Purpose = "ledger"
			blobContext.ObjectID = "ledger"
		}
		blob, err := envelope.Encrypt(plain, key, blobContext)
		if err != nil {
			t.Fatal(err)
		}
		if scenario == "document-auth" {
			blob[len(blob)-1] ^= 1
		}
		files["documents/files/"+docID+".chdoc"] = blob
		f := testFile("unused", string(plain))
		if scenario == "document-hash" {
			f.SHA256 = strings.Repeat("f", 64)
		}
		record := map[string]any{"id": docID, "title": "Synthetic document", "category": "Identity", "issued_on": "", "expires_on": "", "notes": "", "filename": "synthetic.pdf", "content_type": "application/pdf", "size_bytes": len(plain), "sha256": f.SHA256, "created_at": "2026-09-08T12:00:00+00:00", "updated_at": "2026-09-08T12:00:00+00:00", "archived": true}
		ledger, _ := json.Marshal(map[string]any{"version": 1, "documents": []any{record}})
		files["documents/ledger.chdoc"], err = envelope.Encrypt(ledger, key, envelope.Context{DataHomeID: testHomeID, KeyID: keyID, Purpose: "ledger", ObjectID: "ledger"})
		if err != nil {
			t.Fatal(err)
		}
		if scenario == "document-missing" {
			delete(files, "documents/files/"+docID+".chdoc")
		}
		if scenario == "document-extra" {
			files["documents/files/extra.chdoc"] = blob
		}
	}
	if scenario == "legacy-ledger" || scenario == "document-mixed-legacy" {
		files["documents/ledger.dpapi"] = []byte("synthetic legacy bytes: must refuse before decryption")
	}
	if scenario == "legacy-blob" {
		files["documents/files/doc_"+strings.Repeat("b", 32)+".dpapi"] = []byte("synthetic legacy blob")
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		write(name, files[name])
		manifest.Files = append(manifest.Files, testFile(name, string(files[name])))
	}
	if scenario == "manifest-drift" {
		files["career_vault/profile.yaml"] = []byte("name: before")
		write("career_vault/profile.yaml", []byte("name: after!"))
		manifest.Files = append(manifest.Files, testFile("career_vault/profile.yaml", "name: before"))
	}
	root, p := runtimepaths.Open(dir)
	if p != nil {
		t.Fatal(p)
	}
	defer root.Close()
	if scenario != "unowned" {
		if p = root.ClaimForProcess(); p != nil {
			t.Fatal(p)
		}
	}
	backend := &syntheticValidationKeys{t: t, missing: scenario == "document-missing-key"}
	result, problem := ValidateSnapshot(ctx, dir, syntheticSnapshot, manifest, backend)
	want := scenario == "empty" || scenario == "full" || scenario == "old-schema"
	if (problem == nil) != want || !want && result != nil {
		t.Fatalf("%s result=%+v problem=%v", scenario, result, problem)
	}
	codes := map[string]string{
		"vault-partial": "BACKUP_VAULT_INCOMPLETE", "vault-invalid": "BACKUP_VAULT_INVALID",
		"job-invalid": "BACKUP_JOB_INVALID", "job-missing-raw": "BACKUP_JOB_INVALID", "job-duplicate": "BACKUP_JOB_DUPLICATE",
		"resume-missing": "BACKUP_RESUME_REFERENCE_INVALID", "resume-hash": "BACKUP_RESUME_REFERENCE_INVALID", "resume-path": "BACKUP_RESUME_REFERENCE_INVALID", "resume-blob-path": "BACKUP_RESUME_REFERENCE_INVALID",
		"old-schema": "", "database-corrupt": "BACKUP_DATABASE_NOT_STANDALONE", "database-future": "BACKUP_SCHEMA_UNSUPPORTED",
		"document-missing-key": "BACKUP_DOCUMENT_KEY_UNAVAILABLE", "document-wrong-home": "BACKUP_DOCUMENT_IDENTITY_INVALID", "document-auth": "BACKUP_DOCUMENT_AUTHENTICATION_FAILED",
		"document-hash": "BACKUP_DOCUMENT_CONTENT_INVALID", "document-missing": "BACKUP_DOCUMENT_STORAGE_INVALID", "document-extra": "BACKUP_DOCUMENT_STORAGE_INVALID",
		"document-blob-home": "BACKUP_DOCUMENT_IDENTITY_INVALID", "document-purpose": "BACKUP_DOCUMENT_IDENTITY_INVALID",
		"legacy-ledger": "BACKUP_DOCUMENT_MIGRATION_REQUIRED", "legacy-blob": "BACKUP_DOCUMENT_MIGRATION_REQUIRED", "document-mixed-legacy": "BACKUP_DOCUMENT_MIGRATION_REQUIRED",
		"manifest-drift": "BACKUP_PAYLOAD_CHANGED", "unowned": "BACKUP_OWNERSHIP_REQUIRED",
	}
	if code := codes[scenario]; code != "" && (problem == nil || problem.Code != code) {
		t.Fatalf("wrong refusal stage %v want %s", problem, code)
	}
	if scenario == "document-wrong-home" && backend.reads != 0 {
		t.Fatal("credential accessed before identity validation")
	}
	if (strings.HasPrefix(scenario, "legacy-") || scenario == "document-mixed-legacy") && backend.reads != 0 {
		t.Fatal("legacy archive touched current document credentials")
	}
	if (scenario == "document-blob-home" || scenario == "document-purpose") && backend.reads != 2 {
		t.Fatal("blob key accessed before context validation", backend.reads)
	}
	if scenario == "full" && (!result.VaultValidated || result.NormalizedJobsValidated != 1 || result.ResumeFilesValidated != 1 || result.DocumentsValidated != 1) {
		t.Fatal("incomplete full validation", result)
	}
	if scenario != "manifest-drift" {
		for name, want := range files {
			got, err := os.ReadFile(filepath.Join(stage, filepath.FromSlash(name)))
			if err != nil || !bytes.Equal(got, want) {
				t.Fatal("validation modified staged data", name, err)
			}
		}
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(filepath.Join(stage, filepath.FromSlash(databasePath+suffix))); !os.IsNotExist(err) {
			t.Fatal("validation left a database sidecar", err)
		}
	}
	if err := os.Rename(stage, stage+".closed"); err != nil {
		t.Fatal("validation leaked native handles", err)
	}
	if err := os.Rename(stage+".closed", stage); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(validationFixtureResult{problem == nil, result})
	if err := os.WriteFile(filepath.Join(dir, "validation-result.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
}
