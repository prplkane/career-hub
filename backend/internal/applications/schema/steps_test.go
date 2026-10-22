package schema

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func database(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := db.Exec("PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;"); err != nil {
		t.Fatal(err)
	}
	return db
}

// This helper models the future coordinator's transaction ownership only.
// It is not the native recovery/backup/admission implementation.
func upgrade(t *testing.T, db *sql.DB, previous int) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := applySteps(ctx, tx, previous); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			t.Fatalf("rollback failed: %v", rollbackErr)
		}
		return err
	}
	return tx.Commit()
}

func TestCurrentAndNewer(t *testing.T) {
	db := database(t)
	if err := upgrade(t, db, 0); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, db)
	if err := upgrade(t, db, Version); err != nil || !reflect.DeepEqual(before, snapshot(t, db)) {
		t.Fatalf("current schema changed: %v", err)
	}
	if err := upgrade(t, db, Version+1); err == nil || !reflect.DeepEqual(before, snapshot(t, db)) {
		t.Fatal("newer schema was not rejected without mutation")
	}
	if err := applySteps(nil, nil, 0); err == nil {
		t.Fatal("nil preconditions accepted")
	}
}

func TestFailedLateStepRollsBackEntireUpgrade(t *testing.T) {
	db := database(t)
	// A user/legacy collision fails only at step13, after many DDL/backfill
	// statements. Earlier Python executescript calls commit implicitly; this
	// engine deliberately keeps every earlier step in the caller transaction.
	if _, err := db.Exec("CREATE VIEW job_discoveries AS SELECT 1 AS discovery_id"); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, db)
	err := upgrade(t, db, 0)
	var typed *Error
	if !errors.As(err, &typed) || typed.Step != 13 || typed.Stage != "statement" {
		t.Fatalf("unexpected failure: %v", err)
	}
	if !reflect.DeepEqual(before, snapshot(t, db)) {
		t.Fatal("failed step13 left partially upgraded schema/data/version")
	}
	if strings.Contains(err.Error(), "job_discoveries") {
		t.Fatal("driver SQL leaked through public error")
	}
}

func TestRollbackAfterEveryStep(t *testing.T) {
	for stop := 1; stop <= Version; stop++ {
		t.Run(fmt.Sprint(stop), func(t *testing.T) {
			db := database(t)
			before := snapshot(t, db)
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			for _, step := range migrations[:stop] {
				for _, op := range step {
					if stage, err := applyAction(context.Background(), tx, op); err != nil {
						_ = tx.Rollback()
						t.Fatalf("%s: %v", stage, err)
					}
				}
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, snapshot(t, db)) {
				t.Fatal("rollback changed original schema")
			}
		})
	}
}

func TestCancellationAndDanglingReference(t *testing.T) {
	db := database(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := applySteps(ctx, tx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	// Construct step9's source shape. A dangling ID has no declared FK in
	// job_applications yet and must not survive the step10 precheck.
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range migrations[:9] {
		for _, op := range step {
			if _, err := applyAction(context.Background(), tx, op); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO job_applications(job_id,company,title,source_url,content_fingerprint,current_status,created_at,updated_at,applied_resume_id) VALUES ('synthetic','c','t','u','h','captured','now','now','missing')`)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, db)
	err = upgrade(t, db, 9)
	var typed *Error
	if !errors.As(err, &typed) || typed.Step != 10 || typed.Stage != "resume-references" || !reflect.DeepEqual(before, snapshot(t, db)) {
		t.Fatalf("dangling reference: %v", err)
	}
}

func TestPostingStorageLimitsRollback(t *testing.T) {
	for _, c := range []struct {
		name string
		id   any
		url  any
	}{
		{"blob", "synthetic", []byte("private synthetic posting")},
		{"null-primary-key", nil, "https://linkedin.com/jobs/view/123"},
		{"oversize", "synthetic", strings.Repeat("x", (64<<10)+1)},
		{"invalid-utf8", "synthetic", string([]byte{0xff})},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := database(t)
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			for _, step := range migrations[:17] {
				for _, op := range step {
					if _, err := applyAction(context.Background(), tx, op); err != nil {
						_ = tx.Rollback()
						t.Fatal(err)
					}
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec(`INSERT INTO job_discoveries(discovery_id,state,observed_url,content_scope,first_seen_at,last_seen_at,updated_at,revision) VALUES (?,'inbox',?,'link_only','now','now','now',1)`, c.id, c.url)
			if err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, db)
			err = upgrade(t, db, 17)
			var typed *Error
			if !errors.As(err, &typed) || typed.Step != 18 || typed.Stage != "posting-identities" || !reflect.DeepEqual(before, snapshot(t, db)) {
				t.Fatalf("unsupported storage was changed/skipped: %v", err)
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("posting text leaked")
			}
		})
	}
}

func TestInstalledResumeReferenceGuards(t *testing.T) {
	db := database(t)
	if err := upgrade(t, db, 0); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec(`INSERT INTO resume_library(resume_id,display_name,relative_path,file_name,sha256,size_bytes,added_at) VALUES ('r','synthetic','resumes/r.pdf','r.pdf','hash',1,'now');
		INSERT INTO job_applications(job_id,company,title,source_url,content_fingerprint,current_status,created_at,updated_at,applied_resume_id) VALUES ('synthetic','c','t','u','h','captured','now','now','r');
		INSERT INTO application_resume_attachments(job_id,relative_path,file_name,label,sha256,size_bytes,attached_at,resume_id) VALUES ('synthetic','resumes/r.pdf','r.pdf','synthetic','hash',1,'now','r');`)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, db)
	for _, statement := range []string{
		"DELETE FROM resume_library WHERE resume_id='r'",
		"UPDATE job_applications SET applied_resume_id='missing'",
		"UPDATE application_resume_attachments SET resume_id='missing'",
	} {
		if _, err := db.Exec(statement); err == nil || !reflect.DeepEqual(before, snapshot(t, db)) {
			t.Fatalf("resume reference guard failed: %v", err)
		}
	}
}

func firstDifference(got, want map[string]any) string {
	for _, key := range []string{"version", "schema", "tables"} {
		if !reflect.DeepEqual(got[key], want[key]) {
			if key == "tables" {
				g, w := got[key].(map[string]any), want[key].(map[string]any)
				for table, expected := range w {
					if !reflect.DeepEqual(g[table], expected) {
						return fmt.Sprintf("table %s: got %v; want %v", table, g[table], expected)
					}
				}
			}
			return fmt.Sprintf("%s differs", key)
		}
	}
	return "unknown difference"
}

func snapshot(t *testing.T, db *sql.DB) map[string]any {
	t.Helper()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query("SELECT type,name,sql FROM sqlite_master ORDER BY type,name")
	if err != nil {
		t.Fatal(err)
	}
	schema := make([][]any, 0)
	var names []string
	for rows.Next() {
		var typ, name string
		var statement sql.NullString
		if err := rows.Scan(&typ, &name, &statement); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		var normalized any
		if statement.Valid {
			normalized = strings.Join(strings.Fields(statement.String), " ")
		}
		schema = append(schema, []any{typ, name, normalized})
		if typ == "table" {
			names = append(names, name)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	tables := make(map[string]any)
	for _, name := range names {
		quoted := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
		rows, err := db.Query("SELECT name FROM pragma_table_info(?) ORDER BY cid", name)
		if err != nil {
			t.Fatal(err)
		}
		var columns, quotedColumns []string
		for rows.Next() {
			var column string
			if err := rows.Scan(&column); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			columns = append(columns, column)
			quotedColumns = append(quotedColumns, `"`+strings.ReplaceAll(column, `"`, `""`)+`"`)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		rows, err = db.Query("SELECT " + strings.Join(quotedColumns, ",") + " FROM " + quoted)
		if err != nil {
			t.Fatal(err)
		}
		data := make([][]any, 0)
		for rows.Next() {
			values, dest := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			data = append(data, values)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		sort.Slice(data, func(i, j int) bool {
			a, _ := json.Marshal(data[i])
			b, _ := json.Marshal(data[j])
			return string(a) < string(b)
		})
		tables[name] = map[string]any{"columns": columns, "rows": data}
	}
	encoded, err := json.Marshal(map[string]any{"version": version, "schema": schema, "tables": tables})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
