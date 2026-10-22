package sqlitestore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"modernc.org/sqlite"
)

func ownedPath(t *testing.T, name string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, name)
}

func createFixture(t *testing.T, path string) {
	t.Helper()
	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	if runtime.GOOS == "windows" {
		u.Path = "/" + u.Path
	}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE sample (id INTEGER PRIMARY KEY, text_value TEXT, payload BLOB, optional TEXT); INSERT INTO sample VALUES (1,'Données',X'0001ff',NULL)"); err != nil {
		t.Fatal(err)
	}
}

func TestLocalURIIsEscapedAndCannotInjectOptions(t *testing.T) {
	for _, name := range []string{"Données %?#.sqlite", "literal%2f.sqlite"} {
		path := ownedPath(t, name)
		dsn, err := readOnlyURI(path)
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		wantPath := filepath.ToSlash(path)
		if runtime.GOOS == "windows" {
			wantPath = "/" + wantPath
		}
		if u.Host != "" || u.Fragment != "" || u.Path != wantPath || !reflect.DeepEqual(u.Query(), url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)", "query_only(1)"}}) {
			t.Fatalf("incorrect URI structure: %#v", u)
		}
		if runtime.GOOS == "windows" && !strings.HasPrefix(dsn, "file:///") {
			t.Fatal("Windows URI lacks local drive form")
		}
	}
	for _, path := range []string{"", ".", "relative.sqlite", "file:///injected.sqlite", "//server/share/file", `\\server\share\file`, `\\?\C:\file`, `\\.\pipe\file`, ownedPath(t, "nul") + "\x00", ownedPath(t, "utf8") + "\xff"} {
		if _, err := readOnlyURI(path); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("nonlocal/invalid syntax accepted")
		}
	}
	if runtime.GOOS == "windows" {
		for _, path := range []string{`C:relative`, `\rooted`, `/rooted`, `C:\safe\CON.db`, `C:\safe\aux .db`, `C:\safe\COM¹`, `C:\safe\db:stream`} {
			if _, err := readOnlyURI(path); !errors.Is(err, ErrInvalidPath) {
				t.Error("drive-relative/rooted path accepted")
			}
		}
	}
}

func TestWindowsDeviceNames(t *testing.T) {
	for _, name := range []string{"con", "PRN.txt", "AUX .db", "NUL", "CONIN$", "CONOUT$", "CLOCK$", "COM1", "LPT9", "COM².db"} {
		if !windowsDeviceComponent(name) {
			t.Errorf("device accepted: %s", name)
		}
	}
	for _, name := range []string{"COM", "LPT", "COM0", "LPT10", "COMtest", "company.sqlite", "normal.db"} {
		if windowsDeviceComponent(name) {
			t.Errorf("normal name rejected: %s", name)
		}
	}
}

func TestExistingTypedReadsAndReadOnlyOnReconnection(t *testing.T) {
	// '?' is forbidden by Windows filenames; its URI escaping is tested above.
	path := ownedPath(t, "Données %#.sqlite")
	createFixture(t, path)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := OpenReadOnly(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.Stats().MaxOpenConnections != 1 {
		t.Fatal("pool not bounded")
	}
	for iteration := 0; iteration < 2; iteration++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		var id int64
		var text string
		var payload []byte
		var optional sql.NullString
		err = db.QueryRowContext(ctx, "SELECT id,text_value,payload,optional FROM sample WHERE id=?", 1).Scan(&id, &text, &payload, &optional)
		if err != nil || id != 1 || text != "Données" || !reflect.DeepEqual(payload, []byte{0, 1, 255}) || optional.Valid {
			t.Fatalf("typed projection: %v", err)
		}
		var queryOnly, busy int
		if err = db.QueryRowContext(ctx, "PRAGMA query_only").Scan(&queryOnly); err != nil || queryOnly != 1 {
			t.Fatal("query_only not configured")
		}
		if err = db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil || busy != 5000 {
			t.Fatal("busy timeout not configured")
		}
		if _, err = db.ExecContext(ctx, "PRAGMA query_only=0"); err != nil {
			t.Fatal(err)
		}
		_, err = db.ExecContext(ctx, "INSERT INTO sample(id) VALUES (2)")
		var sqliteErr *sqlite.Error
		if !errors.As(err, &sqliteErr) || sqliteErr.Code()&255 != 8 {
			t.Fatalf("mode=ro not effective: %v", err)
		}
		cancel()
		// Force the next physical connection to prove DSN settings are per-open.
		db.SetMaxIdleConns(0)
		db.SetMaxIdleConns(1)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("read changed main database")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatal("DELETE fixture gained auxiliary files")
	}
}

func TestMissingNeverCreatesAndErrorsAreWrapped(t *testing.T) {
	path := ownedPath(t, "private-marker.sqlite")
	db, err := OpenReadOnly(context.Background(), path)
	var sqliteErr *sqlite.Error
	if db != nil || err == nil || err.Error() != "SQLite pilot open failed" || !errors.As(err, &sqliteErr) {
		t.Fatalf("missing error not preserved safely: %v", err)
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing database created")
	}
	if _, err = OpenReadOnly(nil, path); !errors.Is(err, ErrInvalidContext) {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = OpenReadOnly(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancelled open ignored")
	}
	if _, err = OpenReadOnly(context.Background(), "relative"); !errors.Is(err, ErrInvalidPath) {
		t.Fatal("invalid open path accepted")
	}
}

func TestCorruptDatabaseOpensBeforeSchemaReadFails(t *testing.T) {
	path := ownedPath(t, "corrupt.sqlite")
	content := []byte(strings.Repeat("not a SQLite database\n", 32))
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	db, err := OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatalf("physical open performed an extra database-content query: %v", err)
	}
	defer db.Close()
	if stats := db.Stats(); stats.OpenConnections != 1 || stats.InUse != 0 || stats.Idle != 1 || stats.MaxOpenConnections != 1 {
		t.Fatalf("physical connection lease was not returned: %#v", stats)
	}
	var version int
	err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code() != 26 {
		t.Fatalf("schema read did not report SQLite NOTADB: %v", err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(content, after) {
		t.Fatal("corrupt database was changed")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatal("corrupt database gained auxiliary files")
	}
}

func TestQueryCancellationAndReuse(t *testing.T) {
	path := ownedPath(t, "cancel.sqlite")
	createFixture(t, path)
	db, err := OpenReadOnly(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	var sum int64
	err = db.QueryRowContext(ctx, "WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1000000000) SELECT sum(x) FROM n").Scan(&sum)
	if err == nil || ctx.Err() == nil {
		t.Fatal("long query was not cancelled")
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("cooperative query cancellation exceeded test bound")
	}
	reuse, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	var count int
	if err = db.QueryRowContext(reuse, "SELECT count(id) FROM sample").Scan(&count); err != nil || count != 1 {
		t.Fatalf("connection not reusable: %v", err)
	}
}

func TestNativeBuildFingerprint(t *testing.T) {
	path := ownedPath(t, "fingerprint.sqlite")
	createFixture(t, path)
	db, err := OpenReadOnly(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var version, source string
	if err = db.QueryRowContext(ctx, "SELECT sqlite_version(),sqlite_source_id()").Scan(&version, &source); err != nil {
		t.Fatal(err)
	}
	if version != "3.53.4" {
		t.Fatalf("unexpected SQLite version %s", version)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA compile_options")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var options []string
	for rows.Next() {
		var option string
		if err = rows.Scan(&option); err != nil {
			t.Fatal(err)
		}
		options = append(options, option)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(options)
	digest := sha256.Sum256([]byte(strings.Join(options, "\n") + "\n"))
	t.Logf("SQLite=%s source_id=%s compile_options_sha256=%s options=%s", version, source, hex.EncodeToString(digest[:]), strings.Join(options, ","))
}
