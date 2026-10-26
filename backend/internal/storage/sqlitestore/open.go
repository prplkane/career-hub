package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

const OpenTimeout = 5 * time.Second

var (
	ErrInvalidPath    = errors.New("SQLite pilot requires an absolute local path")
	ErrInvalidContext = errors.New("SQLite pilot requires a context")
)

type openError struct{ cause error }

func (e *openError) Error() string { return "SQLite pilot open failed" }
func (e *openError) Unwrap() error { return e.cause }

// readOnlyURI performs lexical validation only; it never resolves symlinks or
// inspects a path. In particular, checks performed by a caller before Open do
// not make later native SQLite main/journal/WAL/SHM opens race-free.
func readOnlyURI(path string) (string, error) {
	if !utf8.ValidString(path) || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
		return "", ErrInvalidPath
	}
	name := filepath.ToSlash(path)
	// Refuse UNC, extended/device namespaces, and double-slash network syntax.
	if strings.HasPrefix(name, "//") || strings.HasPrefix(path, `\\`) {
		return "", ErrInvalidPath
	}
	if volume := filepath.VolumeName(path); volume != "" {
		if len(volume) != 2 || volume[1] != ':' || !((volume[0] >= 'A' && volume[0] <= 'Z') || (volume[0] >= 'a' && volume[0] <= 'z')) {
			return "", ErrInvalidPath
		}
		for _, component := range strings.Split(name[len(volume):], "/") {
			if strings.ContainsRune(component, ':') || windowsDeviceComponent(component) {
				return "", ErrInvalidPath
			}
		}
		name = "/" + name // file:///C:/...; never an authority named C:.
	}
	query := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)", "query_only(1)"}}
	return (&url.URL{Scheme: "file", Path: name, RawQuery: query.Encode()}).String(), nil
}

func windowsDeviceComponent(component string) bool {
	base, _, _ := strings.Cut(component, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$", "CLOCK$":
		return true
	}
	if strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT") {
		return strings.Contains("123456789¹²³", base[3:]) && len([]rune(base[3:])) == 1
	}
	return false
}

// OpenReadOnly opens one pooled connection to an existing local database.
// The caller owns Close and supplies bounded contexts for subsequent queries.
// It configures the physical connection without a ping or schema-validation
// query; corrupt database content may fail only when a later query reads it.
// mode=ro cannot be changed into read-write by changing query_only. Neither
// option prohibits SQLite's auxiliary WAL/SHM effects or confines native opens.
// The five-second context bounds cooperative operations, not blocking OS calls
// or a driver open/busy handler that does not observe cancellation promptly.
func OpenReadOnly(ctx context.Context, absolutePath string) (*sql.DB, error) {
	if ctx == nil {
		return nil, ErrInvalidContext
	}
	dsn, err := readOnlyURI(absolutePath)
	if err != nil {
		return nil, err
	}
	return openURI(ctx, dsn)
}

// OpenReadWriteExisting is an explicit preparation primitive, never a read-path
// fallback. The caller must retain native path guards and process ownership.
// mode=rw refuses to create a missing main file. A transaction begins IMMEDIATE
// so schema preparation can reserve the writer before taking its recovery copy.
func OpenReadWriteExisting(ctx context.Context, absolutePath string) (*sql.DB, error) {
	if ctx == nil {
		return nil, ErrInvalidContext
	}
	dsn, err := readWriteURI(absolutePath, false)
	if err != nil {
		return nil, err
	}
	return openURI(ctx, dsn)
}

func readWriteURI(path string, backup bool) (string, error) {
	dsn, err := readOnlyURI(path) // Same lexical/URI safety checks as existing reads.
	if err != nil {
		return "", err
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return "", ErrInvalidPath
	}
	pragmas := []string{"busy_timeout(5000)", "foreign_keys(1)", "synchronous(FULL)", "temp_store(MEMORY)"}
	if backup {
		pragmas = append(pragmas, "journal_mode(DELETE)")
	}
	u.RawQuery = url.Values{"mode": {"rw"}, "_pragma": pragmas, "_txlock": {"immediate"}}.Encode()
	return u.String(), nil
}

func openURI(ctx context.Context, dsn string) (*sql.DB, error) {
	ctx, cancel := context.WithTimeout(ctx, OpenTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, &openError{err}
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, &openError{err}
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	lease, err := db.Conn(ctx)
	if err == nil {
		// Force physical open and per-connection settings without Ping's SELECT 1.
		// Return the lease even when cancellation arrived during driver open.
		closeErr := lease.Close()
		err = ctx.Err()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		_ = db.Close() // Preserve the primary error; never expose path-bearing text.
		return nil, &openError{err}
	}
	return db, nil
}
