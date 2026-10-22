package applications

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestValueJSONRetainsSQLiteScalarTypes(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
		want  string
	}{
		{"large-real", float64(1e20), "100000000000000000000.0"},
		{"exponent-real", float64(1e21), "1e+21"},
		{"fractional-real", float64(1.25), "1.25"},
		{"integral-real", float64(1), "1.0"},
		{"positive-zero-real", float64(0), "0.0"},
		{"negative-zero-real", math.Copysign(0, -1), "-0.0"},
		{"integer", int64(1), "1"},
		{"integer-zero", int64(0), "0"},
		{"null", nil, "null"},
		{"text", "1", `"1"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := json.Marshal(Value{test.value})
			if err != nil || string(got) != test.want {
				t.Fatalf("got %s, %v; want %s", got, err, test.want)
			}
		})
	}
	for _, unsupported := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := json.Marshal(Value{unsupported}); err == nil {
			t.Fatal("nonfinite value unexpectedly serialized")
		}
	}
}

type step struct {
	contains string
	args     []string
	columns  int
	values   [][]driver.Value
	err      error
	nextErr  error
}
type script struct {
	t        *testing.T
	steps    []step
	openRows int
	connects int
}
type connector struct{ script *script }

func (c connector) Connect(context.Context) (driver.Conn, error) {
	c.script.connects++
	return &fakeConn{c.script}, nil
}
func (c connector) Driver() driver.Driver { return fakeDriver{} }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unexpected driver Open") }

type fakeConn struct{ script *script }

func (*fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unexpected Prepare") }
func (*fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("unexpected transaction") }
func (*fakeConn) Close() error                        { return nil }
func (c *fakeConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	s := c.script
	if s.openRows != 0 {
		s.t.Fatal("query issued before earlier rows closed")
	}
	if len(s.steps) == 0 {
		s.t.Fatalf("unexpected query: %s", query)
	}
	current := s.steps[0]
	s.steps = s.steps[1:]
	if !strings.Contains(query, current.contains) {
		s.t.Fatalf("query %s did not contain %s", query, current.contains)
	}
	if strings.Contains(query, "SELECT *") || strings.Contains(query, "ORDER BY occurred_at") {
		s.t.Fatal("unexpected query semantics")
	}
	if len(args) != len(current.args) {
		s.t.Fatalf("args: got %d want %d", len(args), len(current.args))
	}
	for i, arg := range args {
		if arg.Value != current.args[i] {
			s.t.Fatalf("argument %d changed", i)
		}
	}
	if current.err != nil {
		return nil, current.err
	}
	s.openRows++
	return &fakeRows{script: s, step: current}, nil
}

type fakeRows struct {
	script *script
	step   step
	index  int
	closed bool
}

func (r *fakeRows) Columns() []string {
	out := make([]string, r.step.columns)
	for i := range out {
		out[i] = fmt.Sprintf("column%d", i)
	}
	return out
}
func (r *fakeRows) Close() error {
	if !r.closed {
		r.closed = true
		r.script.openRows--
	}
	return nil
}
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.index < len(r.step.values) {
		copy(dest, r.step.values[r.index])
		r.index++
		return nil
	}
	if r.step.nextErr != nil {
		return r.step.nextErr
	}
	return io.EOF
}
func scripted(t *testing.T, steps ...step) (*sql.DB, *script) {
	t.Helper()
	s := &script{t: t, steps: steps}
	db := sql.OpenDB(connector{s})
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = db.Close()
		if s.openRows != 0 {
			t.Error("leaked rows")
		}
		if len(s.steps) != 0 {
			t.Errorf("unconsumed query steps: %d", len(s.steps))
		}
	})
	return db, s
}
func schema(version int64) step {
	return step{contains: "PRAGMA user_version", columns: 1, values: [][]driver.Value{{version}}}
}
func app(id string, resume driver.Value, recorded driver.Value) []driver.Value {
	return []driver.Value{id, "Company", "Title", "https://example.invalid/job", "hash", nil, "reviewed", "", "2026-01-01", "2026-01-02", resume, recorded}
}
func jsonValue(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEmptyIDsNeverTouchConnectionAndInputBounds(t *testing.T) {
	value, err := ReadSummaries(nil, nil, nil)
	if err != nil || len(value) != 0 {
		t.Fatal("empty IDs must bypass root and connection")
	}
	for _, ids := range [][]string{make([]string, MaxRequestedIDs+1), {strings.Repeat("x", MaxIDBytes+1)}, strings.Fields(strings.Repeat(strings.Repeat("x", MaxIDBytes)+" ", 257))} {
		if value, err := ReadSummaries(context.Background(), nil, ids); value != nil || err == nil || err.Code != "INPUT_LIMIT" {
			t.Fatal("missing input bound", err)
		}
	}
	if _, err = ReadSummaries(context.Background(), nil, []string{"bad\xff"}); err == nil || err.Code != "INPUT_UNSUPPORTED" {
		t.Fatal(err)
	}
}

func TestSummaryRequestValidatorKeepsBoundsAndExactIDs(t *testing.T) {
	ids := []string{"", " one ", "ONE", "one", "one", "nul\x00id", "Ω"}
	before := append([]string(nil), ids...)
	if err := ValidateSummaryRequest(ids); err != nil || !reflect.DeepEqual(ids, before) {
		t.Fatal("valid request changed or was normalized")
	}
	for _, ids := range [][]string{
		make([]string, MaxRequestedIDs+1),
		{strings.Repeat("x", MaxIDBytes+1)},
		strings.Fields(strings.Repeat(strings.Repeat("x", MaxIDBytes)+" ", 257)),
		{"bad\xff"},
	} {
		err := ValidateSummaryRequest(ids)
		_, readErr := ReadSummaries(nil, nil, ids)
		if err == nil || readErr == nil || err.Code != readErr.Code || err.Stage != "request" {
			t.Fatal("standalone validator diverged from read-model admission")
		}
	}
	for _, ids := range [][]string{nil, make([]string, MaxRequestedIDs),
		strings.Fields(strings.Repeat(strings.Repeat("x", MaxIDBytes)+" ", 256))} {
		if err := ValidateSummaryRequest(ids); err != nil {
			t.Fatal("inclusive request boundary rejected")
		}
	}
}

func TestSchemaReadNeverMigrates(t *testing.T) {
	for _, version := range []int64{0, 26, 28} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			db, _ := scripted(t, schema(version))
			value, err := ReadSummaries(context.Background(), db, []string{"one"})
			if version < SchemaVersion {
				if err != nil || len(value) != 0 {
					t.Fatal(value, err)
				}
			} else if value != nil || err == nil || err.Stage != "schema" || err.Code != "SCHEMA_TOO_NEW" {
				t.Fatal(value, err)
			}
		})
	}
}

func TestSummaryEnrichmentRetainsSourceTruth(t *testing.T) {
	ids := []string{"c", "b", "a"}
	db, s := scripted(t, schema(27),
		step{contains: "FROM job_applications", args: []string{"a", "b", "c", "missing"}, columns: 12, values: [][]driver.Value{app("c", "old", int64(1)), app("b", "shared", int64(0)), app("a", "shared", int64(2))}},
		step{contains: "FROM application_resume_attachments", args: ids, columns: 8, values: [][]driver.Value{{"a", nil, "relative.docx", "file.docx", "Attachment label", "sha", int64(15), "attached"}}},
		step{contains: "SELECT job_id, plan_json, status, revision", args: ids, columns: 6, values: [][]driver.Value{{"a", "not valid JSON", "draft", int64(2), "created", "updated"}}},
		step{contains: "MAX(event_id)", args: ids, columns: 2, values: [][]driver.Value{{"c", nil}}},
		step{contains: "SELECT resume_id, display_name FROM resume_library", args: []string{"shared"}, columns: 2, values: [][]driver.Value{{"shared", "Library name"}}},
	)
	value, err := ReadSummaries(context.Background(), db, []string{"a", "b", "c", "a", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(value) != 3 || s.connects != 1 {
		t.Fatal("unexpected records or connection count")
	}
	a := value["a"]
	if a.ResumeSelection.State != "selected" || a.ResumeSelection.Resume.ID.value != nil || a.ResumeSelection.Resume.DisplayName.value != "Attachment label" {
		t.Fatal("selected identity changed")
	}
	if !a.AppliedResume.Recorded || a.AppliedResume.Resume.DisplayName.value != "Library name" {
		t.Fatal("submitted identity changed")
	}
	if value["b"].AppliedResume.Recorded || value["b"].AppliedResume.Resume == nil {
		t.Fatal("source shared-prefetch edge changed")
	}
	if !value["c"].AppliedResume.Recorded || value["c"].AppliedResume.Resume != nil {
		t.Fatal("latest unknown event fell back to old resume")
	}
	if a.CurrentStatus.value != "reviewed" || a.ResumePlan.Revision.value != int64(2) {
		t.Fatal("stored state normalized")
	}
	out := jsonValue(t, value).(map[string]any)["c"].(map[string]any)
	for _, key := range []string{"decision", "resume_attachment", "resume_plan"} {
		if v, present := out[key]; !present || v != nil {
			t.Fatalf("missing explicit null: %s", key)
		}
	}
	if len(out) != 14 {
		t.Fatalf("unexpected payload fields: %v", out)
	}
	if _, ok := out["status_history"]; ok {
		t.Fatal("history expanded")
	}
	value["a"].AppliedResume.Resume.DisplayName = Value{}
	if value["b"].AppliedResume.Resume.DisplayName.value != "Library name" {
		t.Fatal("submitted identity shared between output records")
	}
}

func TestBatchingUses900AndBoundParameters(t *testing.T) {
	ids := make([]string, 901)
	rows := make([][]driver.Value, 901)
	for i := range ids {
		ids[i] = fmt.Sprintf("id-%04d", i)
		rows[i] = app(ids[i], nil, int64(0))
	}
	ids[0] = "x'); DROP TABLE job_applications; --"
	rows[0] = app(ids[0], nil, int64(0))
	steps := []step{schema(27),
		{contains: "FROM job_applications", args: ids[:900], columns: 12, values: rows[:900]},
		{contains: "FROM job_applications", args: ids[900:], columns: 12, values: rows[900:]},
	}
	for _, batch := range [][]string{ids[:900], ids[900:]} {
		steps = append(steps,
			step{contains: "FROM application_resume_attachments", args: batch, columns: 8},
			step{contains: "FROM application_resume_plans", args: batch, columns: 6})
	}
	for _, batch := range [][]string{ids[:900], ids[900:]} {
		steps = append(steps, step{contains: "MAX(event_id)", args: batch, columns: 2})
	}
	db, _ := scripted(t, steps...)
	value, err := ReadSummaries(context.Background(), db, append(ids, ids[0]))
	if err != nil || len(value) != 901 {
		t.Fatal(len(value), err)
	}
}

func TestQueryFailureDiscardPartialResultsAndCloseRows(t *testing.T) {
	private := errors.New("private database diagnostic")
	for _, stage := range []string{"schema", "applications", "enrichment"} {
		t.Run(stage, func(t *testing.T) {
			steps := []step{}
			if stage != "schema" {
				steps = append(steps, schema(27))
			}
			if stage == "enrichment" {
				steps = append(steps, step{contains: "FROM job_applications", args: []string{"one"}, columns: 12, values: [][]driver.Value{app("one", nil, int64(0))}})
			}
			broken := step{contains: "PRAGMA", columns: 1, nextErr: private}
			if stage == "applications" {
				broken = step{contains: "FROM job_applications", args: []string{"one"}, columns: 12, values: [][]driver.Value{app("one", nil, int64(0))}, nextErr: private}
			}
			if stage == "enrichment" {
				broken = step{contains: "FROM application_resume_attachments", args: []string{"one"}, columns: 8, err: private}
			}
			steps = append(steps, broken, schema(0))
			db, s := scripted(t, steps...)
			value, err := ReadSummaries(context.Background(), db, []string{"one"})
			if value != nil || err == nil || err.Stage != stage || !errors.Is(err, private) || strings.Contains(err.Error(), "private") {
				t.Fatal(value, err)
			}
			if s.openRows != 0 {
				t.Fatal("failure left rows open")
			}
			if _, err = ReadSummaries(context.Background(), db, []string{"one"}); err != nil {
				t.Fatal("connection not reusable", err)
			}
		})
	}
}

func TestSQLiteStorageValuesAndTruth(t *testing.T) {
	for _, raw := range []any{nil, int64(12), 1.5, "plain"} {
		v, err := value(raw)
		if err != nil || !reflect.DeepEqual(jsonValue(t, v), jsonValue(t, raw)) {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []any{[]byte("blob"), true, math.Inf(1), math.NaN(), "invalid\xff"} {
		if _, err := value(raw); err == nil {
			t.Fatal("unsupported storage accepted")
		}
	}
	if err := (&budget{remaining: 0}).row([]any{"x"}); !errors.Is(err, errBudget) {
		t.Fatal("result budget missing")
	}
	for _, raw := range []any{int64(-1), int64(2), 0.5, "0", []byte{1}} {
		if value, err := truth(raw); err != nil || !value {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []any{nil, int64(0), 0.0, "", []byte{}} {
		if value, err := truth(raw); err != nil || value {
			t.Fatal(raw, err)
		}
	}
}

func TestOutputBudgetIncludesRepeatedReferencesAndEscaping(t *testing.T) {
	shared := &ResumeIdentity{Value{"r"}, Value{strings.Repeat("<\n", 100)}}
	records := map[string]Summary{}
	for i := 0; i < 5; i++ {
		records[fmt.Sprint(i)] = Summary{AppliedResume: AppliedResume{true, shared}}
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if !fitsSummaries(records, len(encoded)) || fitsSummaries(records, len(encoded)-1) {
		t.Fatal("output budget not exact")
	}
	if fitsSummaries(records, 1000) {
		t.Fatal("repeated expanded values escaped result budget")
	}
}

func TestHiddenCellsAreValidatedAndChargedBeforeConsumption(t *testing.T) {
	for _, hidden := range []driver.Value{"bad\xff", strings.Repeat("x", MaxCellBytes+1)} {
		row := app("one", hidden, int64(0))
		db, s := scripted(t, schema(27), step{contains: "FROM job_applications", args: []string{"one"}, columns: 12, values: [][]driver.Value{row}})
		result, err := ReadSummaries(context.Background(), db, []string{"one"})
		if result != nil || err == nil || s.openRows != 0 {
			t.Fatal("hidden value accepted or rows leaked", err)
		}
	}
}

func TestCancelledContextAndNoRows(t *testing.T) {
	db, s := scripted(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if value, err := ReadSummaries(ctx, db, []string{"one"}); value != nil || err == nil || !errors.Is(err, context.Canceled) || s.connects != 0 {
		t.Fatal(value, err)
	}
	db2, _ := scripted(t, schema(27), step{contains: "FROM job_applications", args: []string{"absent"}, columns: 12})
	if value, err := ReadSummaries(context.Background(), db2, []string{"absent"}); err != nil || len(value) != 0 {
		t.Fatal(value, err)
	}
}
