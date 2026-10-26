package startup

import (
	"context"
	"errors"
	"testing"

	"careerhub.local/backend/internal/applications/schema"
	"careerhub.local/backend/internal/sidecar/readonly"
	"careerhub.local/backend/internal/storage/datahome"
)

func TestPreparationAdmitsExactWorkspaceBeforeAndAfterCleanup(t *testing.T) {
	for _, test := range []struct {
		name, phase, code string
		preparationFails  bool
	}{
		{name: "unchanged"},
		{"before identity change", "before", "DATA_HOME_IDENTITY_CHANGED", false},
		{"after identity change", "after", "DATA_HOME_IDENTITY_CHANGED", false},
		{"after failed preparation", "after", "DATA_HOME_IDENTITY_CHANGED", true},
		{"new recovery sentinel", "recovery", "DATA_HOME_RECOVERY_REQUIRED", false},
		{"canceled", "cancel", "DATA_HOME_STARTUP_CANCELLED", false},
		{"preparation error", "", "APPLICATION_BACKUP_VERIFY_FAILED", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			start := fixture(t)
			raw, e := datahome.MarkerBytes(start.Channel, "dh_0123456789abcdef0123456789abcdef")
			if e != nil {
				t.Fatal(e)
			}
			writeFixture(t, start.DataRoot, datahome.MarkerPath, string(raw))
			w, err := readonly.Open(start)
			if err != nil {
				t.Fatal(err)
			}
			changeID := func() {
				raw, e := datahome.MarkerBytes(start.Channel, "dh_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
				if e != nil {
					t.Fatal(e)
				}
				writeFixture(t, start.DataRoot, datahome.MarkerPath, string(raw))
			}
			if test.phase == "before" {
				changeID()
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			got, err := prepareAdmitted(ctx, start.DataRoot, w, func(gotContext context.Context, root string) (*schema.Preparation, error) {
				calls++
				if gotContext != ctx || root != start.DataRoot {
					t.Fatal("preparation inputs changed")
				}
				if test.phase == "after" {
					changeID()
				}
				if test.phase == "recovery" {
					writeFixture(t, root, datahome.RestoreRecoveryPath, "synthetic")
				}
				if test.phase == "cancel" {
					cancel()
				}
				if test.preparationFails {
					return nil, &schema.PreparationError{Code: "APPLICATION_BACKUP_VERIFY_FAILED"}
				}
				return &schema.Preparation{PreviousVersion: schema.Version}, nil
			})
			if test.phase == "before" && calls != 0 || test.phase != "before" && calls != 1 {
				t.Fatal("incorrect preparation dispatch")
			}
			if test.code != "" {
				requireCode(t, err, test.code)
				if got != nil {
					t.Fatal("failed preparation admitted workspace")
				}
			} else if err != nil || got != w {
				t.Fatal("successful preparation lost the admitted workspace")
			}
		})
	}
}

func TestPreparationFailureCodesAreFixedAndAllowlisted(t *testing.T) {
	for _, code := range []string{
		"APPLICATION_PATH_REJECTED", "DATA_HOME_OWNERSHIP_REQUIRED", "APPLICATION_ORPHANED_SQLITE_FILES",
		"APPLICATION_DATABASE_CREATE_FAILED", "APPLICATION_DATABASE_UNAVAILABLE", "APPLICATION_CLEANUP_FAILED",
		"APPLICATION_UPGRADE_LOCK_FAILED", "APPLICATION_ROLLBACK_FAILED", "APPLICATION_SCHEMA_READ_FAILED",
		"APPLICATION_SCHEMA_TOO_NEW", "APPLICATION_SCHEMA_UPGRADE_FAILED", "APPLICATION_COMMIT_UNCONFIRMED",
		"APPLICATION_BACKUP_PATH_FAILED", "APPLICATION_BACKUP_NAME_FAILED", "APPLICATION_BACKUP_CREATE_FAILED",
		"APPLICATION_BACKUP_CLEANUP_FAILED", "APPLICATION_BACKUP_VERIFY_FAILED", "APPLICATION_BACKUP_METADATA_FAILED",
		"APPLICATION_BACKUP_SOURCE_FAILED", "APPLICATION_BACKUP_COPY_FAILED",
	} {
		requireCode(t, preparationError(&schema.PreparationError{Code: code}), code)
	}
	requireCode(t, preparationError(&schema.PreparationError{Code: "APPLICATION_PREPARATION_CANCELED"}), "DATA_HOME_STARTUP_CANCELLED")
	requireCode(t, preparationError(&schema.PreparationError{Code: "private-value"}), "SIDECAR_STARTUP_FAILED")
	requireCode(t, preparationError(errors.New("private-value")), "SIDECAR_STARTUP_FAILED")
}
