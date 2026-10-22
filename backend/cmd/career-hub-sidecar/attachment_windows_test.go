//go:build windows

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"careerhub.local/backend/internal/catalogstore"
	"careerhub.local/backend/internal/datahome"
)

// Runs the actual native composition over full native schema, then verifies
// both migrated and pre-library attachment metadata without creating records.
func TestAttachmentRealHTTPRegisteredAndLegacy(t *testing.T) {
	root := t.TempDir()
	data, resources := filepath.Join(root, "data"), filepath.Join(root, "resources")
	materialFixtureFile(t, filepath.Join(resources, "VERSION"), []byte("1.2.3-dev\n"))
	materialFixtureFile(t, filepath.Join(resources, filepath.FromSlash(catalogstore.CoreGroupsPath)), []byte("[]\n"))
	materialFixtureFile(t, filepath.Join(resources, filepath.FromSlash(catalogstore.CoreSkillsPath)), []byte("- {id: core, name: Core, category: tool}\n"))
	if e := os.MkdirAll(filepath.Join(resources, "src", "intake_ui_static"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := datahome.Bootstrap(context.Background(), data, "dev"); e != nil {
		t.Fatal(e)
	}
	var before map[string][32]byte
	for _, mode := range []string{"prepare_attachments", "attachments"} {
		input, _ := json.Marshal(resumeManagementHTTPFixture{Root: data, Resources: resources, Mode: mode})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestResumeManagementRealHTTPHelper$")
		cmd.Env = append(os.Environ(), "CAREERHUB_RESUME_MANAGEMENT_HTTP_HELPER=1")
		cmd.Stdin = bytes.NewReader(input)
		materialRunTool(t, cmd)
		cancel()
		if mode == "prepare_attachments" {
			before = materialTree(t, data)
		}
	}
	if !reflect.DeepEqual(before, materialTree(t, data)) {
		t.Fatal("attachment download changed data")
	}
}

func seedAttachmentFixture(t *testing.T, data string) {
	t.Helper()
	db, e := sql.Open("sqlite", filepath.Join(data, "applications", "career_hub.sqlite3"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for _, job := range []string{"registered", "legacy", "invalid"} {
		if _, e := db.Exec(`INSERT INTO job_applications(job_id,company,title,source_url,content_fingerprint,current_status,created_at,updated_at) VALUES(?, 'Example', 'Engineer', '', '', 'captured', '2026-09-20T00:00:00Z', '2026-09-20T00:00:00Z')`, job); e != nil {
			t.Fatal(e)
		}
		var id any = "resume-digest-one"
		if job == "legacy" {
			id = nil
		}
		if _, e := db.Exec(`INSERT INTO application_resume_attachments(job_id,resume_id,relative_path,file_name,label,sha256,size_bytes,attached_at) SELECT ?,?,relative_path,file_name,display_name,sha256,size_bytes,added_at FROM resume_library WHERE resume_id='resume-digest-one'`, job, id); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := db.Exec(`UPDATE application_resume_attachments SET size_bytes=size_bytes+1 WHERE job_id='invalid'`); e != nil {
		t.Fatal(e)
	}
}
