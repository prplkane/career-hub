package readonly

import (
	"context"
	"database/sql"

	"careerhub.local/backend/internal/emailoutcomes"
)

// EmailApplicationUpdates reads a minimized stored-outcome inbox from the
// admitted Data Home. It opens neither a mailbox nor Credential Manager.
func (w *Workspace) EmailApplicationUpdates(ctx context.Context, offset int64, limit int) (emailoutcomes.UpdatePage, error) {
	if offset < 0 || limit < 1 || limit > 100 {
		return emailoutcomes.UpdatePage{}, fail("EMAIL_APPLICATION_UPDATE_INVALID")
	}
	return admittedApplicationModel(ctx, w.CheckAdmission, func() (emailoutcomes.UpdatePage, error) {
		return readOwnedApplicationDB(ctx, w.start.DataRoot, func(db *sql.DB) (emailoutcomes.UpdatePage, error) {
			page, problem := emailoutcomes.ReadUpdatePage(ctx, db, offset, limit)
			if problem != nil {
				return emailoutcomes.UpdatePage{}, emailUpdateReadError(problem)
			}
			return page, nil
		})
	})
}

func emailUpdateReadError(problem *emailoutcomes.UpdateReadError) error {
	switch problem.Code {
	case "INPUT_INVALID":
		return fail("EMAIL_APPLICATION_UPDATE_INVALID")
	case "VALUE_INVALID":
		return fail("EMAIL_APPLICATION_UPDATE_READ_FAILED")
	case "SCHEMA_TOO_NEW":
		return fail("APPLICATION_SCHEMA_TOO_NEW")
	case "SCHEMA_PREPARATION_REQUIRED":
		return fail("APPLICATION_SCHEMA_PREPARATION_REQUIRED")
	case "CANCELED":
		return fail("APPLICATION_READ_CANCELED")
	default:
		return fail("EMAIL_APPLICATION_UPDATE_READ_FAILED")
	}
}
