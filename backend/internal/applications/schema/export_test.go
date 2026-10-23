package schema

import (
	"context"
	"database/sql"
)

// ApplyStepsForTest lets this package's external tests build a current-schema
// database through the private engine without exporting it to the product.
func ApplyStepsForTest(ctx context.Context, tx *sql.Tx, previous int) error {
	return applySteps(ctx, tx, previous)
}
