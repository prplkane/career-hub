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

// applyHistorical builds a database as the first `upTo` steps created it at
// the time, not as they read today: `patch` strips the columns that were later
// retrofitted into early CREATE statements, and fails if the text has drifted.
func applyHistorical(t *testing.T, db *sql.DB, upTo int, patch func(step int, statement string) string) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i, step := range migrations[:upTo] {
		for _, op := range step {
			if patch != nil && op.kind == sqlAction {
				op.sql = patch(i+1, op.sql)
			}
			if stage, err := applyAction(context.Background(), tx, op); err != nil {
				_ = tx.Rollback()
				t.Fatalf("step %d %s: %v", i+1, stage, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != upTo {
		t.Fatalf("historical fixture version %d, %v", version, err)
	}
}

// historicalV19 removes the two columns that v20 and v25 add through
// columnAction but that the v14 and v17 CREATE statements now already carry.
// A database created before those steps existed never had them in CREATE order.
func historicalV19(t *testing.T, step int, statement string) string {
	t.Helper()
	remove := map[int]string{
		14: `                    purpose TEXT NOT NULL DEFAULT 'both' CHECK (
                        purpose IN (
                            'job_alerts', 'application_tracking', 'both'
                        )
                    ),
`,
		17: `                    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
`,
	}
	fragment, retrofitted := remove[step]
	if !retrofitted {
		return statement
	}
	if strings.Count(statement, fragment) != 1 {
		t.Fatalf("step %d no longer carries the retrofitted column text exactly once", step)
	}
	return strings.Replace(statement, fragment, "", 1)
}

// unorderedSchema compares what matters for equivalence without column order.
// SQLite appends an ALTER TABLE ADD COLUMN column after every existing column
// and also appends it to the stored CREATE text, so a table whose column was
// retrofitted into an early CREATE statement legitimately lists that column in
// a different position, with different sqlite_master text, on an upgraded
// database than on a fresh one. Index, trigger and view text is compared exactly.
func unorderedSchema(t *testing.T, db *sql.DB) map[string]any {
	t.Helper()
	full := snapshot(t, db)
	objects := map[string]any{}
	var tables []string
	for _, entry := range full["schema"].([]any) {
		row := entry.([]any)
		typ, name := row[0].(string), row[1].(string)
		if typ == "table" {
			tables = append(tables, name)
			objects[typ+":"+name] = "table"
			continue
		}
		objects[typ+":"+name] = row[2]
	}
	columns, foreignKeys := map[string]any{}, map[string]any{}
	for _, name := range tables {
		read := func(query string, width int) []string {
			rows, err := db.Query(query, name)
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for rows.Next() {
				values, dest := make([]any, width), make([]any, width)
				for i := range values {
					dest[i] = &values[i]
				}
				if err := rows.Scan(dest...); err != nil {
					_ = rows.Close()
					t.Fatal(err)
				}
				encoded, _ := json.Marshal(values)
				out = append(out, string(encoded))
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				t.Fatal(err)
			}
			sort.Strings(out)
			return out
		}
		columns[name] = read("SELECT name,type,\"notnull\",dflt_value,pk FROM pragma_table_info(?)", 5)
		foreignKeys[name] = read("SELECT \"table\",\"from\",\"to\",on_update,on_delete FROM pragma_foreign_key_list(?)", 5)
	}
	return map[string]any{"version": full["version"], "objects": objects, "columns": columns, "foreign_keys": foreignKeys}
}

func TestStepwiseUpgradeMatchesFreshSchema(t *testing.T) {
	fresh := database(t)
	if err := upgrade(t, fresh, 0); err != nil {
		t.Fatal(err)
	}
	freshExact, freshUnordered := snapshot(t, fresh), unorderedSchema(t, fresh)
	if freshExact["version"] != float64(Version) {
		t.Fatalf("fresh database is at %v, not %d", freshExact["version"], Version)
	}
	// The v13 statements carry no retrofitted column, so a v13 database that
	// takes the remaining steps must match a fresh database exactly, including
	// column order and stored statement text.
	from13 := database(t)
	applyHistorical(t, from13, 13, nil)
	if err := upgrade(t, from13, 13); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(t, from13); !reflect.DeepEqual(freshExact, got) {
		t.Fatalf("v13 upgrade differs from fresh schema: %s", firstDifference(got, freshExact))
	}
	// A database created when v19 was current predates the v20/v25 column
	// retrofits; its upgrade must add exactly those columns, constraints,
	// indexes and triggers, in whatever column position SQLite appends them.
	from19 := database(t)
	applyHistorical(t, from19, 19, func(step int, statement string) string { return historicalV19(t, step, statement) })
	for _, column := range []struct{ table, column string }{{"job_email_sender_rules", "purpose"}, {"job_email_application_outcomes", "revision"}} {
		var present int
		if err := from19.QueryRow("SELECT count(*) FROM pragma_table_info(?) WHERE name = ?", column.table, column.column).Scan(&present); err != nil || present != 0 {
			t.Fatalf("historical fixture already has %s.%s (%v)", column.table, column.column, err)
		}
	}
	if err := upgrade(t, from19, 19); err != nil {
		t.Fatal(err)
	}
	got := unorderedSchema(t, from19)
	if !reflect.DeepEqual(freshUnordered, got) {
		for _, key := range []string{"version", "objects", "columns", "foreign_keys"} {
			if !reflect.DeepEqual(freshUnordered[key], got[key]) {
				t.Fatalf("v19 upgrade differs from fresh schema in %s:\n got %v\nwant %v", key, got[key], freshUnordered[key])
			}
		}
		t.Fatal("v19 upgrade differs from fresh schema")
	}
	exact := snapshot(t, from19)["tables"].(map[string]any)
	if columns := exact["job_email_sender_rules"].(map[string]any)["columns"].([]any); columns[len(columns)-2] != "purpose" {
		t.Fatalf("expected the retrofitted column to be appended, got %v", columns)
	}
}

// Every v28 index must be the access path of the query it was added for, not
// merely present: the SET NULL foreign-key actions and the orphan-repair
// EXISTS checks search the child column, and the default feed sort must not
// need a temporary B-tree.
func TestVersion28IndexesServeTheirQueries(t *testing.T) {
	db := database(t)
	if err := upgrade(t, db, 0); err != nil {
		t.Fatal(err)
	}
	plan := func(query string, args ...any) string {
		rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			lines = append(lines, detail)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		return strings.Join(lines, "\n")
	}
	for _, c := range []struct{ query, index string }{
		// The child-side scan SQLite performs for ON DELETE SET NULL on each parent row.
		{"SELECT rowid FROM job_email_application_outcomes WHERE matched_discovery_id = ?", "job_email_application_outcomes_matched_discovery"},
		{"SELECT rowid FROM job_email_application_outcomes WHERE matched_job_id = ?", "job_email_application_outcomes_matched_job"},
		// applications/maintenance.go orphan repair and the discovery analogue.
		{"SELECT EXISTS(SELECT 1 FROM application_contact_provenance WHERE job_id = ?1)", "application_contact_provenance_job"},
		{"SELECT EXISTS(SELECT 1 FROM application_contact_provenance WHERE discovery_id = ?1)", "application_contact_provenance_discovery"},
	} {
		if got := plan(c.query, "x"); !strings.Contains(got, "USING COVERING INDEX "+c.index) && !strings.Contains(got, "USING INDEX "+c.index) {
			t.Fatalf("%s\nplan did not use %s:\n%s", c.query, c.index, got)
		}
	}
	// discovery/reads.go readList with the default "last_desc" sort and no filters.
	feed := `SELECT discoveries.discovery_id FROM job_discoveries AS discoveries
LEFT JOIN job_discovery_field_overrides AS overrides ON overrides.discovery_id = discoveries.discovery_id
LEFT JOIN job_discovery_assessments AS assessments ON assessments.discovery_id = discoveries.discovery_id
AND assessments.content_sha256 = discoveries.content_sha256 AND assessments.scope = discoveries.content_scope
ORDER BY discoveries.last_seen_at DESC, discoveries.discovery_id LIMIT ? OFFSET ?`
	if got := plan(feed, 50, 0); !strings.Contains(got, "SCAN discoveries USING INDEX job_discoveries_last_seen") || strings.Contains(got, "TEMP B-TREE") {
		t.Fatalf("feed sort plan:\n%s", got)
	}
}
