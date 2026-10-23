//go:build windows

package readonly

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/discovery/discoverypreview"
	"careerhub.local/backend/internal/sidecar/protocol"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

type previewRequest struct {
	Start protocol.StartRecord
	Mode  string
	Cases []previewCase
}

func installPreviewCase(t *testing.T, start protocol.StartRecord, test previewCase) {
	t.Helper()
	for key, raw := range test.Files {
		role, relative, found := strings.Cut(key, ":")
		if !found || role != "data" && role != "resource" {
			t.Fatal("invalid synthetic file role")
		}
		reader := evidenceReader{resource: role == "resource", policy: true}
		if !reader.allowed(relative) && !(role == "data" && relative == "jobs/discovery/captures/synthetic.txt") {
			t.Fatal("out-of-scope fixture")
		}
		root := start.DataRoot
		if role == "resource" {
			root = start.ResourceRoot
		}
		if raw == nil {
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(relative))); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		} else {
			writeFixture(t, root, relative, *raw)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(start.DataRoot, filepath.FromSlash(applicationDatabasePath)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := test.Capture
	var path any
	if c.SourceRelativePath != "" {
		path = c.SourceRelativePath
	}
	_, err = db.Exec(`INSERT INTO job_discoveries(discovery_id,state,title,company,observed_url,content_scope,source_relative_path,content_sha256,content_chars,first_seen_at,last_seen_at,updated_at,revision)
	 VALUES (?,'inbox',?,?,?,?,?,?,?,'now','now','now',1)
	 ON CONFLICT(discovery_id) DO UPDATE SET title=excluded.title, company=excluded.company, content_scope=excluded.content_scope, source_relative_path=excluded.source_relative_path, content_sha256=excluded.content_sha256, content_chars=excluded.content_chars,revision=revision+1`,
		c.DiscoveryID, c.Title, c.Company, c.ObservedURL, c.ContentScope, path, c.ContentSHA256, c.ContentChars)
	if err != nil {
		t.Fatal(err)
	}
	if discoverypreview.Eligible(c.ContentScope) {
		_, err = db.Exec(`INSERT INTO job_discovery_assessments VALUES (?,?,1,1,'sha256:legacy','sha256:legacy',4,?,'[corrupt derived cache','now')
		 ON CONFLICT(discovery_id) DO UPDATE SET content_sha256=excluded.content_sha256,scope=excluded.scope,assessment_json=excluded.assessment_json`, c.DiscoveryID, c.ContentSHA256, c.ContentScope)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

type previewCaptureDrift struct {
	evaluationSource
	after func(string)
}

func (s previewCaptureDrift) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	raw, present, err := s.evaluationSource.ReadFile(path, limit)
	s.after(path)
	return raw, present, err
}
func TestDiscoveryPreviewProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_PREVIEW_HELPER") != "1" {
		return
	}
	var request previewRequest
	if json.NewDecoder(io.LimitReader(os.Stdin, 4<<20)).Decode(&request) != nil || len(request.Cases) == 0 || len(request.Cases) > 16 {
		t.Fatal("invalid synthetic request")
	}
	var w *Workspace
	var err error
	if request.Mode == "unowned" {
		w, err = Open(request.Start)
	} else {
		w, err = OpenForProcess(request.Start)
	}
	if err != nil {
		t.Fatalf("synthetic admission: %+v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if request.Mode != "unowned" && request.Mode != "missing-db" {
		if _, err := schema.PrepareOwned(ctx, request.Start.DataRoot); err != nil {
			t.Fatalf("synthetic schema: %+v", err)
		}
	}
	base := filepath.Dir(request.Start.DataRoot)
	writeFixture(t, request.Start.DataRoot, "documents/private.bin", "SYNTHETIC_PRIVATE_DOCUMENT_NOT_CONTEXT")
	if request.Mode == "corpus" {
		for _, test := range request.Cases {
			installPreviewCase(t, request.Start, test)
			t.Run(test.Name, func(t *testing.T) {
				before := tree(t, base)
				got, err := w.PrepareDiscoveryPreview(ctx, test.Capture.DiscoveryID, previewMonth())
				comparePreview(t, test, got, err)
				if !reflect.DeepEqual(before, tree(t, base)) {
					t.Fatal("native read changed a cache, source, job or application")
				}
			})
		}
		return
	}
	if request.Mode != "unowned" && request.Mode != "missing-db" {
		installPreviewCase(t, request.Start, request.Cases[0])
	}
	want := ""
	switch request.Mode {
	case "unowned":
		want = "ASSESSMENT_OWNERSHIP_REQUIRED"
	case "missing-db":
		want = "DISCOVERY_SCHEMA_PREPARATION_REQUIRED"
	case "identity":
		writeFixture(t, request.Start.DataRoot, datahome.MarkerPath, marker("dev", "dh_22222222222222222222222222222222"))
		want = "DATA_HOME_IDENTITY_CHANGED"
	case "recovery":
		writeFixture(t, request.Start.DataRoot, datahome.DocumentRecoveryPath, "synthetic recovery")
		want = "DATA_HOME_RECOVERY_REQUIRED"
	case "resources":
		writeFixture(t, request.Start.ResourceRoot, "VERSION", "invalid")
		want = "SIDECAR_RESOURCE_INVALID"
	case "canceled":
		cancel()
		want = "ASSESSMENT_INPUT_CANCELED"
	case "schema":
		writeApplicationSQL(t, request.Start.DataRoot, "PRAGMA user_version=28")
		want = "DISCOVERY_SCHEMA_TOO_NEW"
	case "directory":
		path := filepath.Join(request.Start.DataRoot, "jobs", "discovery", "captures", "synthetic.txt")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	case "capture-drift":
		data, e := runtimepaths.Open(request.Start.DataRoot)
		if e != nil {
			t.Fatal(e)
		}
		defer data.Close()
		resources, e := runtimepaths.Open(request.Start.ResourceRoot)
		if e != nil {
			t.Fatal(e)
		}
		defer resources.Close()
		db, e2 := sqlitestore.OpenReadOnly(ctx, filepath.Join(request.Start.DataRoot, filepath.FromSlash(applicationDatabasePath)))
		if e2 != nil {
			t.Fatal(e2)
		}
		defer db.Close()
		changed := false
		source := previewCaptureDrift{evaluationSource: data, after: func(path string) {
			if path == "career_vault/profile.yaml" && !changed {
				changed = true
				writeApplicationSQL(t, request.Start.DataRoot, "UPDATE job_discoveries SET title='Concurrent edit', revision=revision+1")
			}
		}}
		got, err := readPreview(ctx, db, source, resources, "synthetic", previewMonth())
		requireCode(t, err, "DISCOVERY_PREVIEW_INPUT_CHANGED")
		if got != nil || !changed {
			t.Fatal("concurrent capture edit was published")
		}
		return
	default:
		t.Fatal("unknown synthetic mode")
	}
	before := tree(t, base)
	got, err := w.PrepareDiscoveryPreview(ctx, "synthetic", previewMonth())
	if want != "" {
		requireCode(t, err, want)
	}
	if got != nil || err == nil {
		t.Fatal("native failure returned partial preview")
	}
	if !reflect.DeepEqual(before, tree(t, base)) {
		t.Fatal("failed native preview changed files")
	}
}
