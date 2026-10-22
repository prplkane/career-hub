package schema

import (
	"context"
	"testing"

	"careerhub.local/backend/internal/emailrouting"
)

func TestEmailRoutingCurrentSchema(t *testing.T) {
	db := database(t)
	if err := upgrade(t, db, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO job_email_connections (
		connection_id, provider, account_address, folder, secret_name,
		enabled, lookback_days, max_messages_per_sync, revision,
		created_at, updated_at
	) VALUES ('primary', 'gmail_imap', 'alerts@example.test', 'INBOX',
		'synthetic', 1, 14, 250, 1, 't0', 't0')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO job_email_sender_rules (
		connection_id, sender_address, disposition, purpose,
		subject_policy_json, created_at, updated_at
	) VALUES ('primary', 'alerts@example.test', 'allowed', 'both', '{}', 't0', 't0'),
		('primary', 'disabled@example.test', 'disabled', 'job_alerts', '{}', 't0', 't0')`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rules, err := emailrouting.LoadAllowed(context.Background(), tx)
	if err != nil || len(rules) != 1 || rules["alerts@example.test"].Purpose != emailrouting.SenderPurposeBoth {
		t.Fatalf("current-schema allowed rules: count=%d error=%v", len(rules), err)
	}
	if _, found := rules["disabled@example.test"]; found {
		t.Fatal("current-schema disabled sender admitted")
	}
}
