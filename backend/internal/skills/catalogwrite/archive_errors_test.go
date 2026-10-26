package catalogwrite

import (
	"context"
	"errors"
	"strings"
	"testing"

	"careerhub.local/backend/internal/skills/catalogtransaction"
)

func TestArchiveEntryGuardsAndFixedTransactionErrors(t *testing.T) {
	for _, service := range []*Service{nil, {}, New("", "")} {
		for _, recovery := range []bool{false, true} {
			var out *ArchiveOutcome
			var e *Error
			if recovery {
				out, e = service.RecoverRegistry(nil)
			} else {
				out, e = service.SetCustomGroupArchived(nil, "private id", true, "private revision")
			}
			if out == nil || e == nil || e.Code != "CATALOG_SERVICE_UNAVAILABLE" || out.Write.IntentRecorded || out.Result != nil {
				t.Fatal("invalid configuration admitted")
			}
		}
	}
	for _, tc := range []struct {
		input error
		code  string
	}{
		{context.Canceled, "CATALOG_OPERATION_CANCELLED"},
		{context.DeadlineExceeded, "CATALOG_OPERATION_CANCELLED"},
		{catalogtransaction.ErrPending, "CATALOG_RECOVERY_REQUIRED"},
		{catalogtransaction.ErrConflict, "CATALOG_TRANSACTION_CONFLICT"},
		{catalogtransaction.ErrJournal, "CATALOG_JOURNAL_INVALID"},
		{catalogtransaction.ErrTarget, "CATALOG_TARGET_INVALID"},
		{catalogtransaction.ErrInput, "CATALOG_TRANSACTION_INPUT_INVALID"},
		{errors.New("private dependency failure"), "CATALOG_TRANSACTION_FAILED"},
	} {
		e := archiveTransactionError(tc.input)
		if e.Code != tc.code || e.Stage != "transaction" || e.Committed || strings.Contains(e.Error(), "private") {
			t.Fatal("fixed transaction error mapping differs")
		}
	}
}
