package discovery

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"

	"careerhub.local/backend/internal/storage/runtimepaths"
)

type syntheticFiles struct{ root string }

func (f syntheticFiles) ReadFile(relative string, limit int64) ([]byte, bool, *runtimepaths.Error) {
	// Only test-owned, generated directories are used. Production uses Root.
	raw, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(relative)))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, &runtimepaths.Error{Code: "SYNTHETIC"}
	}
	if int64(len(raw)) > limit {
		return nil, false, &runtimepaths.Error{Code: "FILE_SIZE_LIMIT"}
	}
	return raw, true, nil
}

type countedQueries struct {
	queryer
	count int
}

func (q *countedQueries) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	q.count++
	return q.queryer.QueryContext(ctx, query, args...)
}
