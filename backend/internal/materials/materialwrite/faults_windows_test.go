//go:build windows

package materialwrite

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/storage/sqlitestore"
)

func checkWriterReservation(t *testing.T, root string, blocked bool) {
	t.Helper()
	db, e := sqlitestore.OpenReadWriteExisting(context.Background(), filepath.Join(root, "applications/career_hub.sqlite3"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec("PRAGMA busy_timeout=30"); e != nil {
		t.Fatal(e)
	}
	tx, e := db.BeginTx(context.Background(), nil)
	if e == nil {
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if blocked {
			t.Fatal("material save did not retain the SQL writer reservation")
		}
		return
	}
	if !blocked {
		t.Fatalf("reservation leaked after material cleanup: %v", e)
	}
	var coded interface{ Code() int }
	if !errors.As(e, &coded) || (coded.Code()&255) != 5 {
		t.Fatalf("not an actual SQLite busy refusal: %v", e)
	}
}

func noResult(out *Outcome) bool {
	return out != nil && len(out.Content) == 0 && (len(out.Result) == 0 || bytes.Equal(out.Result, []byte("null")))
}
