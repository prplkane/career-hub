package applicationwrite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/runtimepaths"
	"careerhub.local/backend/internal/storage/sqlitestore"
)

const marker = `{"application":"CareerHub","format_version":2,"channel":"dev","data_home_id":"dh_0123456789abcdef0123456789abcdef"}`

type nativeStep struct {
	Op         string           `json:"op"`
	Now        string           `json:"now"`
	Decision   string           `json:"decision"`
	Status     string           `json:"status"`
	JobID      string           `json:"job_id"`
	Jobs       []map[string]any `json:"jobs"`
	Notes      any              `json:"notes"`
	Note       json.RawMessage  `json:"note,omitempty"`
	OccurredAt any              `json:"occurred_at"`
}
type table struct {
	Columns []string
	Rows    [][]any
}

type nativeRequest struct {
	Data, Mode string
	Steps      []nativeStep
}
type nativeOutput struct {
	Outcome *Outcome
	Error   *Error
	Tables  map[string]table
}

func fixtureFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func hasResult(out *Outcome) bool {
	return out != nil && len(out.Result) > 0 && !bytes.Equal(bytes.TrimSpace(out.Result), []byte("null"))
}

// Every native helper owns only a generated synthetic fixture and exits before
// any Python writer resumes. Production has no ownership-bypass constructor.
func TestLedgerNativeProcessHelper(t *testing.T) {
	if os.Getenv("CAREERHUB_LEDGER_NATIVE_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	var r nativeRequest
	d := json.NewDecoder(os.Stdin)
	d.UseNumber()
	if d.Decode(&r) != nil {
		t.Fatal("request")
	}
	root, pe := runtimepaths.Open(r.Data)
	if pe != nil {
		t.Fatal(pe)
	}
	defer root.Close()
	if r.Mode != "unowned" {
		if pe := root.ClaimForProcess(); pe != nil {
			t.Fatal(pe)
		}
	}
	nativeSetup(t, r)
	outputs := []nativeOutput{}
	for _, step := range r.Steps {
		s := New(r.Data)
		s.now = func() string { return step.Now }
		ctx, cancel := context.WithCancel(context.Background())
		installFaults(t, s, r, ctx, cancel)
		var out *Outcome
		var problem *Error
		switch step.Op {
		case "register":
			out, problem = s.RegisterJobs(ctx, step.Jobs)
		case "single":
			out, problem = s.RegisterJob(ctx, step.Jobs[0])
		case "decision":
			out, problem = s.SetDecision(ctx, step.JobID, step.Decision, step.Notes, nil)
		case "status":
			var note any = ""
			if len(step.Note) > 0 && json.Unmarshal(step.Note, &note) != nil {
				t.Fatal("note")
			}
			out, problem = s.SetStatus(ctx, step.JobID, step.Status, step.OccurredAt, note, nil)
		case "start":
			out, problem = s.StartApplication(ctx, step.JobID, step.OccurredAt)
		default:
			t.Fatal("fixture operation")
		}
		cancel()
		output := nativeOutput{Outcome: out, Error: problem}
		if r.Mode == "unsafe-companion" {
			if os.Remove(filepath.Join(r.Data, filepath.FromSlash(databasePath))+"-journal") != nil {
				t.Fatal("synthetic companion cleanup")
			}
		}
		if r.Mode != "missing-database" && r.Mode != "main-hardlink" {
			output.Tables = snapshotTables(t, r.Data)
		}
		outputs = append(outputs, output)
	}
	if json.NewEncoder(os.Stdout).Encode(outputs) != nil {
		t.Fatal("response")
	}
}

func snapshotTables(t *testing.T, root string) map[string]table {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := sqlitestore.OpenReadOnly(ctx, filepath.Join(root, filepath.FromSlash(databasePath)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			t.Fatal("schema")
		}
		names = append(names, name)
	}
	if rows.Err() != nil || rows.Close() != nil {
		t.Fatal("schema cleanup")
	}
	result := map[string]table{}
	quote := func(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }
	for _, name := range names {
		// Names come exclusively from the trusted generated source schema.
		cols, err := db.QueryContext(ctx, "PRAGMA table_info("+quote(name)+")")
		if err != nil {
			t.Fatal(err)
		}
		entry := table{Columns: []string{}, Rows: [][]any{}}
		for cols.Next() {
			values := make([]any, 6)
			pointers := make([]any, 6)
			for i := range values {
				pointers[i] = &values[i]
			}
			if cols.Scan(pointers...) != nil {
				t.Fatal("column")
			}
			entry.Columns = append(entry.Columns, values[1].(string))
		}
		if cols.Err() != nil || cols.Close() != nil {
			t.Fatal("columns cleanup")
		}
		fields := make([]string, len(entry.Columns))
		for i, col := range entry.Columns {
			fields[i] = quote(col)
		}
		values, err := db.QueryContext(ctx, "SELECT "+strings.Join(fields, ",")+" FROM "+quote(name)+" ORDER BY rowid")
		if err != nil {
			t.Fatal(err)
		}
		for values.Next() {
			row := make([]any, len(fields))
			pointers := make([]any, len(fields))
			for i := range row {
				pointers[i] = &row[i]
			}
			if values.Scan(pointers...) != nil {
				t.Fatal("row")
			}
			for i, value := range row {
				if blob, ok := value.([]byte); ok {
					row[i] = map[string]string{"blob": base64.StdEncoding.EncodeToString(blob)}
				}
			}
			entry.Rows = append(entry.Rows, row)
		}
		if values.Err() != nil || values.Close() != nil {
			t.Fatal("rows cleanup")
		}
		result[name] = entry
	}
	return result
}

func nativeSetup(t *testing.T, r nativeRequest) {
	t.Helper()
	if strings.HasPrefix(r.Mode, "pending-") {
		paths := map[string]string{"pending-document": datahome.DocumentRecoveryPath, "pending-restore": datahome.RestoreRecoveryPath,
			"pending-registry": datahome.RegistryRecoveryPath, "pending-evidence": datahome.EvidenceRecoveryPath,
			"pending-discovery": datahome.DiscoveryRecoveryPath}
		path, ok := paths[r.Mode]
		if !ok {
			t.Fatal("sentinel fixture")
		}
		fixtureFile(t, filepath.Join(r.Data, filepath.FromSlash(path)), []byte("{}"))
	}
	path := filepath.Join(r.Data, filepath.FromSlash(databasePath))
	if r.Mode == "missing-database" {
		if os.Remove(path) != nil {
			t.Fatal("remove synthetic db")
		}
		return
	}
	if r.Mode == "unsafe-companion" {
		if os.Mkdir(path+"-journal", 0700) != nil {
			t.Fatal("synthetic companion")
		}
		t.Cleanup(func() { _ = os.Remove(path + "-journal") })
	}
	if r.Mode == "main-hardlink" {
		if os.Link(path, filepath.Join(r.Data, "synthetic-link.sqlite3")) != nil {
			t.Fatal("synthetic hardlink")
		}
		return
	}
	if r.Mode != "schema-old" && r.Mode != "schema-new" && r.Mode != "constraint" && !strings.HasPrefix(r.Mode, "locked") {
		return
	}
	db, err := sqlitestore.OpenReadWriteExisting(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	query := ""
	switch r.Mode {
	case "schema-old":
		query = "PRAGMA user_version=" + strconv.Itoa(schema.Version-1)
	case "schema-new":
		query = "PRAGMA user_version=" + strconv.Itoa(schema.Version+1)
	case "constraint":
		query = "CREATE TRIGGER synthetic_abort BEFORE INSERT ON application_status_events BEGIN SELECT RAISE(ABORT,'private synthetic cause'); END"
	case "locked", "locked-canceled":
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tx.Rollback() })
		return
	}
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
}
func installFaults(t *testing.T, s *Service, r nativeRequest, ctx context.Context, cancel context.CancelFunc) {
	t.Helper()
	if r.Mode == "canceled" {
		cancel()
	}
	if r.Mode == "locked-canceled" {
		timer := time.AfterFunc(50*time.Millisecond, cancel)
		t.Cleanup(func() { timer.Stop() })
	}
	if r.Mode == "commit-reject" {
		s.commit = func(*sql.Tx) error { return errors.New("private commit cause") }
	}
	if r.Mode == "commit-ambiguous" {
		s.commit = func(tx *sql.Tx) error {
			if tx.Commit() != nil {
				t.Fatal("real commit")
			}
			return errors.New("private unconfirmed commit")
		}
	}
	if r.Mode == "commit-constraint" {
		// A real SQLite failure at COMMIT, not a fake error returned before
		// calling the driver. The IMMEDIATE transaction remains unconfirmed.
		s.commit = func(tx *sql.Tx) error {
			if _, err := tx.Exec("PRAGMA defer_foreign_keys=ON"); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec("INSERT INTO application_status_events(job_id,status,occurred_at,recorded_at,note) VALUES ('synthetic-nonexistent','captured','x','x','')"); err != nil {
				t.Fatal(err)
			}
			err := tx.Commit()
			if err == nil {
				t.Fatal("deferred FK unexpectedly committed")
			}
			return err
		}
	}
	if r.Mode == "rollback-error" || r.Mode == "noop-rollback-error" {
		s.rollback = func(tx *sql.Tx) error { _ = tx.Rollback(); return errors.New("private rollback cause") }
	}
	if r.Mode == "db-close-error" {
		s.closeDB = func(db *sql.DB) error { _ = db.Close(); return errors.New("private close cause") }
	}
	if r.Mode == "root-close-error" {
		s.closeRoot = func(root *runtimepaths.Root) error { _ = root.Close(); return errors.New("private close cause") }
	}
	s.checkpoint = func(stage string) *Error {
		if r.Mode == "cancel-"+stage {
			cancel()
		}
		if stage == "applied" && r.Mode == "rollback-error" || stage == "before_commit" && r.Mode == "precommit-error" {
			return failed("fixture", "SYNTHETIC_PRECOMMIT_FAILURE")
		}
		if stage == "before_commit" && r.Mode == "source-changed" {
			fixtureFile(t, filepath.Join(r.Data, "jobs", "raw", "synthetic.txt"), []byte("changed synthetic source"))
		}
		if stage == "before_commit" && r.Mode == "home-changed" || stage == "committed" && r.Mode == "post-home-changed" {
			fixtureFile(t, filepath.Join(r.Data, datahome.MarkerPath), []byte(strings.Replace(marker, "0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210", 1)))
		}
		if stage == "before_commit" && r.Mode == "new-recovery" {
			fixtureFile(t, filepath.Join(r.Data, datahome.RegistryRecoveryPath), []byte("{}"))
		}
		return nil
	}
}
