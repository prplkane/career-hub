package emailrouting

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"careerhub.local/backend/internal/storage/sqlitestore"
)

const sourcePolicy = `{"job_alerts":{"include_any":["New jobs"],"exclude_any":["Security alert"]},"application_tracking":{"include_any":["Application update"],"exclude_any":[]}}`

func senderRuleDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sender-rules.sqlite3")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.OpenReadWriteExisting(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE job_email_sender_rules (
		connection_id TEXT NOT NULL, sender_address TEXT NOT NULL,
		disposition TEXT NOT NULL, purpose TEXT NOT NULL,
		subject_policy_json TEXT NOT NULL, PRIMARY KEY(connection_id, sender_address))`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ address, disposition, purpose, policy string }{
		{"alerts@example.test", "allowed", "both", sourcePolicy},
		{"jobs@example.test", "allowed", "job_alerts", "{}"},
		{"disabled@example.test", "disabled", "both", "{}"},
	} {
		if _, err := db.Exec(`INSERT INTO job_email_sender_rules
			(connection_id,sender_address,disposition,purpose,subject_policy_json)
			VALUES ('primary',?,?,?,?)`, row.address, row.disposition, row.purpose, row.policy); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestPythonSourceAllowedSenderRules(t *testing.T) {
	t.Parallel()
	db := senderRuleDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rules, err := LoadAllowed(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("allowed sender count=%d", len(rules))
	}
	if _, found := rules["disabled@example.test"]; found {
		t.Fatal("disabled sender entered allowed map")
	}
	if rules["jobs@example.test"].Purpose != SenderPurposeJobAlerts ||
		rules["alerts@example.test"].Purpose != SenderPurposeBoth {
		t.Fatal("wrong source purposes")
	}
	const canonical = `{"application_tracking":{"exclude_any":[],"include_any":["application update"]},"job_alerts":{"exclude_any":["security alert"],"include_any":["new jobs"]}}`
	if got := rules["alerts@example.test"].Policy.CanonicalJSON(); got != canonical {
		t.Fatalf("policy=%s", got)
	}
	for _, test := range []struct{ subject, want string }{
		{"New jobs today", SenderPurposeJobAlerts},
		{"Application update", SenderPurposeApplicationTracking},
		{"Security alert — new jobs", ""},
		{"Unrelated notice", ""},
	} {
		got := RoutedPurpose(SenderPurposeBoth, rules["alerts@example.test"].Policy, test.subject)
		if got != test.want {
			t.Errorf("subject %q: %q, want %q", test.subject, got, test.want)
		}
	}
}

func TestStoredPolicyAndAddressFailClosed(t *testing.T) {
	t.Parallel()
	for _, corrupt := range []struct{ name, statement, value string }{
		{"policy", `UPDATE job_email_sender_rules SET subject_policy_json = ? WHERE sender_address = 'alerts@example.test'`, "not-json"},
		{"address", `UPDATE job_email_sender_rules SET sender_address = ? WHERE sender_address = 'alerts@example.test'`, "Bad Sender <alerts@example.test>"},
		{"purpose", `UPDATE job_email_sender_rules SET purpose = ? WHERE sender_address = 'alerts@example.test'`, "unknown"},
	} {
		t.Run(corrupt.name, func(t *testing.T) {
			db := senderRuleDB(t)
			if _, err := db.Exec(corrupt.statement, corrupt.value); err != nil {
				t.Fatal(err)
			}
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			_, err = LoadAllowed(context.Background(), tx)
			if !errors.Is(err, ErrInvalidPolicy) || err.Error() != "EMAIL_CONNECTION_INVALID" {
				t.Fatalf("unsafe or wrong stored-rule failure: %v", err)
			}
		})
	}
}
