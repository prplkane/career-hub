package savedassessment

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"careerhub.local/backend/internal/jobevaluation"
	_ "modernc.org/sqlite"
)

func TestQualifiedHitSkipsEvaluatorAndPreservesReviewHistory(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE job_applications(job_id TEXT PRIMARY KEY);
 INSERT INTO job_applications VALUES ('one');
 CREATE TABLE job_assessments(job_id TEXT PRIMARY KEY,job_fingerprint TEXT,job_analyzer_version INTEGER,candidate_revision TEXT,assessment_context_revision TEXT,assessment_version INTEGER,assessment_json TEXT,computed_at TEXT);
 CREATE TABLE job_assessment_resolution_events(event_id INTEGER PRIMARY KEY AUTOINCREMENT,job_id TEXT,finding_key TEXT,assessment_revision TEXT,resolution TEXT,note TEXT,recorded_at TEXT);`)
	if err != nil {
		t.Fatal(err)
	}
	p, a := cacheFixture(t)
	p.operation = "load"
	calls := 0
	p.evaluate = func(context.Context, jobevaluation.Input) (*jobevaluation.Result, *jobevaluation.Error) {
		calls++
		return a, nil
	}
	run := func(now string) (map[string]any, *Error) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		result, problem := p.ApplyInTransaction(ctx, tx, now)
		if problem != nil {
			if tx.Rollback() != nil {
				t.Fatal("rollback")
			}
			return nil, problem
		}
		if result.Changed {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if json.Unmarshal(result.Result, &out) != nil {
			t.Fatal("response")
		}
		return out, nil
	}
	first, e := run("2026-09-09T12:00:00Z")
	if e != nil || first["cache_hit"] != false || calls != 1 {
		t.Fatal("first calculation", e, calls)
	}
	second, e := run("2026-09-10T12:00:00Z")
	if e != nil || second["cache_hit"] != true || calls != 1 {
		t.Fatal("hit recalculated", e, calls)
	}
	expected := second["assessment"].(map[string]any)["assessment_revision"].(string)
	if second["assessment"].(map[string]any)["assessment_computed_at"] != "2026-09-09T12:00:00Z" {
		t.Fatal("hit changed computation time")
	}
	p.operation, p.finding, p.resolution, p.expected = "resolve", "recommendation", "accepted_risk", expected
	third, e := run("2026-09-10T12:00:00Z")
	if e != nil || third["cache_hit"] != true || calls != 1 {
		t.Fatal("resolution recalculated", e)
	}
	rows := third["assessment"].(map[string]any)["user_resolutions"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["stale"] != false {
		t.Fatal("review binding")
	}
	p.revisions.Context = "ch-saved-context-v2:sha256:" + strings.Repeat("d", 64)
	if _, e := run("2026-09-11T12:00:00Z"); e == nil || e.Code != "SAVED_ASSESSMENT_REVISION_CONFLICT" {
		t.Fatal("stale review accepted")
	}
	var persisted string
	if db.QueryRow("SELECT assessment_context_revision FROM job_assessments").Scan(&persisted) != nil || persisted == p.revisions.Context {
		t.Fatal("stale review committed refresh")
	}
	p.operation = "load"
	fourth, e := run("2026-09-11T12:00:00Z")
	if e != nil || fourth["cache_hit"] != false {
		t.Fatal("changed context not recomputed", e)
	}
	rows = fourth["assessment"].(map[string]any)["user_resolutions"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["stale"] != true {
		t.Fatal("old review lost or marked current")
	}
	_, err = db.Exec("UPDATE job_assessments SET assessment_json='{bad'")
	if err != nil {
		t.Fatal(err)
	}
	p.evaluate = func(context.Context, jobevaluation.Input) (*jobevaluation.Result, *jobevaluation.Error) {
		return nil, &jobevaluation.Error{Code: "UNAVAILABLE"}
	}
	if out, e := run("2026-09-12T12:00:00Z"); e == nil || out != nil {
		t.Fatal("unavailable evaluator reused corrupt cache")
	}
}
