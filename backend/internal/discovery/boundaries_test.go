package discovery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"

	"careerhub.local/backend/internal/storage/runtimepaths"

	// Registers the "sqlite" driver opened below; this test must not rely on
	// another test file importing sqlitestore for that side effect.
	_ "modernc.org/sqlite"
)

func TestStoredJSONScalarAndGrammarBoundaries(t *testing.T) {
	for _, raw := range []string{"", "null", "[]", "1", "{}{}", "{", "\xff", `{"bad":"\ud800"}`, `{"bad":"\udfff"}`, `{"bad":"\ud800\u1234"}`, `{"a":"\ud800","a":true}`, `{"a":NaN}`, `{"a":1e9999}`, `{"a":` + strings.Repeat("9", 4301) + `}`, `{"a":` + strings.Repeat("[", 128) + "0" + strings.Repeat("]", 128) + "}"} {
		if got, err := parseObject(raw); err == nil || got != nil {
			t.Fatalf("malformed/unsupported object accepted: length=%d", len(raw))
		}
	}
	for _, test := range []struct{ raw, want string }{
		{`{"a":1,"a":2}`, `{"a":2}`}, {`{"a":-0,"b":1.0,"c":-0.0}`, `{"a":0,"b":1.0,"c":-0.0}`},
		{`{"a":"\ud83e\udded","b":"\\ud800"}`, `{"a":"🧭","b":"\\ud800"}`},
		{`{"array":[true,null,{},1e0]}`, `{"array":[true,null,{},1.0]}`},
	} {
		got, e := parseObject(test.raw)
		if e != nil {
			t.Fatal("valid scalar document refused")
		}
		raw, e := json.Marshal(got)
		if e != nil {
			t.Fatal(e)
		}
		if string(raw) != test.want {
			t.Fatalf("JSON meaning/type lost: %s / %s", raw, test.want)
		}
	}
	for _, value := range []any{nil, []byte("1"), 12.5, "12", true} {
		if _, e := integerCell(value); e == nil {
			t.Fatal("unsupported integer coercion")
		}
	}
	for _, value := range []any{nil, []byte("private"), int64(1), true} {
		if _, e := textCell(value, false); e == nil {
			t.Fatal("unsupported text coercion")
		}
	}
	if _, e := json.Marshal(JSONDocument{"nonfinite": math.Inf(1)}); e == nil {
		t.Fatal("nonfinite exported JSON accepted")
	}
}

type captureFault struct {
	raw     []byte
	present bool
	err     *runtimepaths.Error
	calls   int
}

func (f *captureFault) ReadFile(path string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	f.calls++
	return f.raw, f.present, f.err
}
func TestCapturedSourceIntegrityAndRefusal(t *testing.T) {
	text := "Synthetic 🧭\nsecond\nthird\n"
	digest := sha256.Sum256([]byte(text))
	record := Record{SourceRelativePath: "jobs/discovery/captures/source.txt", ContentChars: int64(len([]rune(text))), ContentSHA256: hex.EncodeToString(digest[:])}
	for _, raw := range []string{text, "Synthetic 🧭\r\nsecond\rthird\r\n"} {
		files := &captureFault{raw: []byte(raw), present: true}
		got, e := capturedText(files, record)
		if e != nil || got != text || files.calls != 1 {
			t.Fatal("universal newlines/scalar length changed")
		}
	}
	for _, test := range []struct {
		change func(*Record, *captureFault)
		code   string
	}{
		{func(r *Record, f *captureFault) { f.present = false }, "SOURCE_PATH_REJECTED"},
		{func(r *Record, f *captureFault) { f.err = &runtimepaths.Error{Code: "PRIVATE"} }, "SOURCE_UNREADABLE"},
		{func(r *Record, f *captureFault) { f.raw = []byte{0xff} }, "SOURCE_UNREADABLE"},
		{func(r *Record, f *captureFault) { r.ContentChars++ }, "SOURCE_LENGTH_MISMATCH"},
		{func(r *Record, f *captureFault) { r.ContentSHA256 = strings.Repeat("0", 64) }, "SOURCE_HASH_MISMATCH"},
		{func(r *Record, f *captureFault) { f.raw = make([]byte, (4<<20)+1) }, "RESULT_LIMIT"},
	} {
		r := record
		files := &captureFault{raw: []byte(text), present: true}
		test.change(&r, files)
		got, e := capturedText(files, r)
		if got != "" || e == nil || e.Code != test.code {
			t.Fatalf("source failure changed: %s / %v", test.code, e)
		}
	}
	for _, path := range []string{"jobs/discovery/captures/../outside.txt", "jobs/discovery/captures-other/file", "/jobs/discovery/captures/file", "jobs/discovery/captures/file:stream", "jobs/discovery/captures//file", "jobs/discovery/captures/./file"} {
		r := record
		r.SourceRelativePath = path
		files := &captureFault{raw: []byte(text), present: true}
		if got, e := capturedText(files, r); got != "" || e == nil || e.Code != "SOURCE_PATH_REJECTED" || files.calls != 0 {
			t.Fatal("outside/noncanonical source attempted a file read")
		}
	}
	if got, e := capturedText(nil, Record{}); got != "" || e != nil {
		t.Fatal("link-only read needs a file")
	}
	if got, e := capturedText(nil, record); got != "" || e == nil {
		t.Fatal("unavailable source returned content")
	}
	r := Record{SourceRelativePath: "jobs/discovery/captures/empty.txt"}
	if got, e := capturedText(&captureFault{present: true}, r); got != "" || e != nil {
		t.Fatal("empty source digest convention changed")
	}
}

func TestInvalidRequestsBeforeDatabaseAndResultBounds(t *testing.T) {
	for _, test := range []struct {
		options PageOptions
		code    string
	}{
		{PageOptions{}, "INVALID_LIMIT"}, {PageOptions{Limit: 1, Offset: -1}, "INVALID_OFFSET"},
		{PageOptions{Limit: 1, Sort: "bad"}, "INVALID_SORT"},
		{PageOptions{Limit: 1, Sort: "last_desc", Filters: Filters{States: []string{"no"}}}, "INVALID_STATES"},
		{PageOptions{Limit: 1, Sort: "last_desc", Filters: Filters{States: make([]string, 101)}}, "INPUT_LIMIT"},
	} {
		result, e := ReadList(nil, nil, test.options)
		if result != nil || e == nil || e.Code != test.code {
			t.Fatal("invalid page reached storage")
		}
	}
	for _, id := range []string{"", ".", "C:job", "a/b", "a\\b"} {
		if _, e := NormalizeID(id); e == nil || e.Code != "INVALID_ID" {
			t.Fatal("invalid ID accepted")
		}
	}
	if id, e := NormalizeID("  ..  "); e != nil || id != ".." {
		t.Fatal("source plain lookup ID overrestricted")
	}
	if result, e := ReadDetail(nil, nil, nil, "private\x00"); result != nil || e == nil || e.Code != "INVALID_TEXT" {
		t.Fatal("NUL validation changed")
	}
	if result, e := ReadAssessment(nil, nil, "\xff"); result != nil || e == nil || e.Code != "INPUT_UNSUPPORTED" {
		t.Fatal("invalid UTF8 accepted")
	}
	if count, e := ReadCount(nil, nil, Filters{Search: "\xff"}); count != 0 || e == nil || e.Code != "INPUT_UNSUPPORTED" {
		t.Fatal("invalid count reached storage")
	}
	if err := snapshot(nil, nil, func(*reader) *Error { t.Fatal("invalid snapshot entered"); return nil }); err == nil {
		t.Fatal("invalid storage accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, e := ReadList(ctx, nil, PageOptions{Limit: 1, Sort: "last_desc"}); result != nil || e == nil {
		t.Fatal("canceled storage succeeded")
	}
	if e := checkResult(strings.Repeat("x", MaxReadBytes)); e == nil || e.Code != "RESULT_LIMIT" {
		t.Fatal("output limit missing")
	}
	if e := checkResult(make(chan int)); e == nil || e.Code != "DATABASE_VALUE_UNSUPPORTED" {
		t.Fatal("unsupported result accepted")
	}
	if _, e := plainText(strings.Repeat("x", (2<<20)+1), 500); e == nil || e.Code != "INPUT_LIMIT" {
		t.Fatal("input byte bound missing")
	}
	if _, e := normalizedTimestamp(strings.Repeat(" ", 16385)); e == nil || e.Code != "INPUT_LIMIT" {
		t.Fatal("timestamp byte bound missing")
	}
	if _, e := normalizedTimestamp("\xff"); e == nil || e.Code != "INPUT_UNSUPPORTED" {
		t.Fatal("timestamp encoding bound missing")
	}
}

func TestBoundedDatabaseScanAndCleanup(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("PRAGMA user_version=" + strconv.Itoa(SchemaVersion)); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		query                    string
		columns, maximum, budget int
		code                     string
	}{
		{"SELECT 1 UNION ALL SELECT 2", 1, 1, MaxReadBytes, "RESULT_LIMIT"},
		{"SELECT 1", 1, 1, 1, "RESULT_LIMIT"},
		{"SELECT CAST(X'FF' AS TEXT)", 1, 1, MaxReadBytes, "DATABASE_VALUE_UNSUPPORTED"},
		{"SELECT X'01'", 1, 1, MaxReadBytes, "DATABASE_VALUE_UNSUPPORTED"},
		{"SELECT 1e999", 1, 1, MaxReadBytes, "DATABASE_VALUE_UNSUPPORTED"},
		{"SELECT 1,2", 1, 1, MaxReadBytes, "DATABASE_SCAN_FAILED"},
		{"INVALID SQL", 1, 1, MaxReadBytes, "DATABASE_QUERY_FAILED"},
		{"SELECT NULL", 1, 1, MaxReadBytes, ""},
		{"WITH RECURSIVE valueset(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM valueset WHERE n<=10000) SELECT n FROM valueset", 1, MaxObservations, MaxReadBytes, "RESULT_LIMIT"},
	} {
		problem := snapshot(context.Background(), db, func(r *reader) *Error {
			r.remaining = test.budget
			return r.rows("synthetic", test.query, nil, test.columns, test.maximum, func([]any) error { return nil })
		})
		code := ""
		if problem != nil {
			code = problem.Code
		}
		if code != test.code || db.Stats().InUse != 0 {
			t.Fatalf("scan failure/cleanup mismatch: %s / %s", test.code, code)
		}
	}
}
