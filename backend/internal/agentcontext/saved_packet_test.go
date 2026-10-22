package agentcontext

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func savedPacketDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(savedPacketSQL)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

const savedPacketSQL = `
PRAGMA user_version=27;
CREATE TABLE job_applications(job_id TEXT PRIMARY KEY,company TEXT,title TEXT,source_url TEXT,content_fingerprint TEXT,decision TEXT,current_status TEXT,notes TEXT,created_at TEXT,updated_at TEXT,applied_resume_id TEXT,applied_resume_recorded INTEGER);
CREATE TABLE application_resume_attachments(job_id TEXT,resume_id TEXT,relative_path TEXT,file_name TEXT,label TEXT,sha256 TEXT,size_bytes INTEGER,attached_at TEXT);
CREATE TABLE application_resume_plans(job_id TEXT,plan_json TEXT,status TEXT,revision INTEGER,created_at TEXT,updated_at TEXT);
CREATE TABLE application_applied_resume_events(event_id INTEGER PRIMARY KEY,job_id TEXT,resume_id TEXT);
CREATE TABLE resume_library(resume_id TEXT PRIMARY KEY,display_name TEXT,relative_path TEXT,file_name TEXT,sha256 TEXT,size_bytes INTEGER,added_at TEXT,archived_at TEXT);
INSERT INTO job_applications VALUES('synthetic','Synthetic','Engineer','https://example.test/job','fp','apply','preparing','','2026-01-01','2026-01-01',NULL,0);
INSERT INTO resume_library VALUES('r1','Alpha','private/path','resume.txt','private-hash',12,'2026-01-01',NULL);
INSERT INTO resume_library VALUES('archived','Old','private/path','old.pdf','private-hash',12,'2026-01-01','2026-02-01');
INSERT INTO application_resume_attachments VALUES('synthetic','r1','private/path','resume.txt','Selected','private-hash',12,'2026-01-01');
`

func savedPacketRequest(purpose string) SavedPacketRequest {
	return SavedPacketRequest{JobID: "synthetic", Purpose: purpose, Operation: "context", RegistryMode: "off", Profile: "sandbox", Channel: "preview"}
}
func preparePacketFixture(t *testing.T, db *sql.DB, d, r *savedSourceFixture, req SavedPacketRequest, digest ResumeDigestReader) ([]byte, *Error) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("PRAGMA query_only=ON"); err != nil {
		t.Fatal(err)
	}
	raw, e := PrepareSavedPacket(ctx, tx, d, r, req, savedInputDate, digest)
	// The helper has not ended the caller's transaction, even after refusal.
	var count int
	if err := tx.QueryRow("SELECT count(*) FROM job_applications").Scan(&count); err != nil || count != 1 {
		t.Fatal("read ended transaction or changed ledger")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	return raw, e
}

const savedWorkbenchPath = "outputs/application_texts/synthetic/application_texts.yaml"
const savedWorkbenchFixture = `schema_version: 1
job_id: synthetic
jd_fingerprint: null
items:
  - {id: answer, kind: short_answer, exact_prompt: 'Why?', response: 'PRIVATE_RELATED_RESPONSE', state: submitted, limits: {words: 100, characters: null}}
observations: [{date: 2026-09-27, note: PRIVATE_OBSERVATION}]
extra: !!binary YWJj
`

func TestSavedPacketTransactionPurposeAndIdentity(t *testing.T) {
	for _, purpose := range []string{"assessment", "application_text", "resume_delta"} {
		t.Run(purpose, func(t *testing.T) {
			db := savedPacketDB(t)
			d, r := savedInputFixture()
			d.files[savedWorkbenchPath] = savedWorkbenchFixture
			calls := 0
			digest := func(ctx context.Context, raw json.RawMessage) (json.RawMessage, *string, *Error) {
				calls++
				if string(raw) != `[{"display_name":"Alpha","id":"r1"}]` {
					t.Fatal("digest received more than selected active identities")
				}
				return json.RawMessage(`[{"id":"r1","display_name":"Alpha","digest_status":"digested","digest_supported":true,"digest_source_id":"source","digest_processed_at":"2026-01-01"}]`), nil, nil
			}
			packet, e := preparePacketFixture(t, db, d, r, savedPacketRequest(purpose), digest)
			if e != nil {
				t.Fatal("saved packet", e.Code)
			}
			if bytes.Contains(packet, []byte("PRIVATE_RELATED_RESPONSE")) || bytes.Contains(packet, []byte("PRIVATE_OBSERVATION")) || bytes.Contains(packet, []byte("PRIVATE@example.test")) {
				t.Fatal("default packet leaked private text")
			}
			if purpose == "resume_delta" && calls != 1 || purpose != "resume_delta" && calls != 0 {
				t.Fatal("digest purpose boundary")
			}
			if purpose == "application_text" && d.reads[savedWorkbenchPath] != 2 || purpose != "application_text" && d.reads[savedWorkbenchPath] != 0 {
				t.Fatal("workbench purpose/read boundary")
			}
		})
	}
	db := savedPacketDB(t)
	d, r := savedInputFixture()
	d.files[savedWorkbenchPath] = savedWorkbenchFixture
	task, e := NormalizeTask(map[string]any{"schema_version": json.Number("1"), "purpose": "application_text", "kind": "short_answer", "exact_prompt": "Why?"}, "application_text")
	if e != nil {
		t.Fatal(e)
	}
	request := savedPacketRequest("application_text")
	request.Task = task
	packet, e := preparePacketFixture(t, db, d, r, request, nil)
	if e != nil || !bytes.Contains(packet, []byte("PRIVATE_RELATED_RESPONSE")) {
		t.Fatal("explicit related draft not available", e)
	}
}

func TestSavedPacketRefusalAndLibraryFallback(t *testing.T) {
	for _, mode := range []string{"stale", "wrong-job", "invalid-lists", "drift", "late-digest-failure", "digest-unavailable", "bad-library"} {
		t.Run(mode, func(t *testing.T) {
			db := savedPacketDB(t)
			d, r := savedInputFixture()
			d.files[savedWorkbenchPath] = savedWorkbenchFixture
			request := savedPacketRequest("application_text")
			var digest ResumeDigestReader
			switch mode {
			case "stale":
				d.files[savedWorkbenchPath] = strings.Replace(savedWorkbenchFixture, "jd_fingerprint: null", "jd_fingerprint: stale", 1)
			case "wrong-job":
				d.files[savedWorkbenchPath] = strings.Replace(savedWorkbenchFixture, "job_id: synthetic", "job_id: another", 1)
			case "invalid-lists":
				d.files[savedWorkbenchPath] = "schema_version: 1\njob_id: synthetic\nitems: []\nobservations: wrong"
			case "drift":
				d.before = func(path string, n int) {
					if path == savedWorkbenchPath && n == 2 {
						d.files[path] += "changed: true"
					}
				}
			case "late-digest-failure":
				request.Purpose = "resume_delta"
				digest = func(context.Context, json.RawMessage) (json.RawMessage, *string, *Error) {
					return nil, nil, failure("SOURCE_CHANGED")
				}
			case "digest-unavailable":
				request.Purpose = "resume_delta"
			case "bad-library":
				if _, err := db.Exec("UPDATE resume_library SET display_name=CAST(? AS TEXT) WHERE resume_id='r1'", append([]byte("PRIVATE_INVALID_TEXT"), 0xff)); err != nil {
					t.Fatal(err)
				}
			}
			packet, e := preparePacketFixture(t, db, d, r, request, digest)
			if mode == "bad-library" {
				if e != nil {
					t.Fatal("library fallback failed", e.Code)
				}
				if e != nil || !bytes.Contains(packet, []byte("Saved Resume Library data could not be read.")) || bytes.Contains(packet, []byte("PRIVATE_INVALID_TEXT")) {
					t.Fatal("unsafe library fallback", e)
				}
			} else if e == nil || packet != nil {
				t.Fatal("invalid context returned partial data")
			}
		})
	}
	for _, purpose := range []string{"assessment", "application_text", "resume_delta"} {
		r := savedPacketRequest(purpose)
		r.Operation = "prepare"
		d, core := savedInputFixture()
		if raw, e := PrepareSavedPacket(context.Background(), nil, d, core, r, savedInputDate, nil); raw != nil || e == nil || e.Code != "AGENT_CONTEXT_WRITE_RECEIPT_REQUIRED" || len(d.reads) != 0 {
			t.Fatal("invented prepare receipt")
		}
	}
}
